package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// This file is the Anthropic Messages API dialect. It translates the
// provider-neutral Request/Response/Message/ToolCall types into Anthropic's
// wire format and back. The rest of the package — and every consumer — never
// sees an Anthropic-shaped struct.
//
// Key shape differences from the neutral form:
//   - The system prompt is a top-level `system` string, not a message turn.
//   - Message content is an array of typed blocks (text / tool_use /
//     tool_result), not a flat string + parallel tool_calls array.
//   - A tool result is a `tool_result` block inside a *user* turn, keyed by
//     tool_use_id. Consecutive neutral role="tool" messages are merged into a
//     single user turn (Anthropic wants one user turn answering one assistant
//     tool-use turn).
//   - max_tokens is required.

// ---- outbound (neutral -> Anthropic) ----

type anthropicReq struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string           `json:"role"` // user | assistant
	Content []anthropicBlock `json:"content"`
}

// anthropicBlock is the union of the block types we emit and receive. Only the
// fields relevant to Type are populated; omitempty keeps the wire clean.
type anthropicBlock struct {
	Type string `json:"type"` // text | tool_use | tool_result

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// buildAnthropicReq translates the neutral request. model/maxTokens come from
// the client when the request leaves them unset.
func (c *Client) buildAnthropicReq(req Request, stream bool) anthropicReq {
	model := req.Model
	if model == "" {
		model = c.model
	}

	var systemParts []string
	var msgs []anthropicMessage

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if m.Content != "" {
				systemParts = append(systemParts, m.Content)
			}

		case "user":
			msgs = append(msgs, anthropicMessage{
				Role:    "user",
				Content: []anthropicBlock{{Type: "text", Text: m.Content}},
			})

		case "assistant":
			var blocks []anthropicBlock
			if m.Content != "" {
				blocks = append(blocks, anthropicBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(strings.TrimSpace(tc.Function.Arguments))
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropicBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				})
			}
			msgs = append(msgs, anthropicMessage{Role: "assistant", Content: blocks})

		case "tool":
			block := anthropicBlock{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}
			// Merge into the preceding user turn when it's already carrying
			// tool_result blocks — Anthropic answers one assistant tool-use
			// turn with one user turn.
			if n := len(msgs); n > 0 && msgs[n-1].Role == "user" && isToolResultTurn(msgs[n-1]) {
				msgs[n-1].Content = append(msgs[n-1].Content, block)
			} else {
				msgs = append(msgs, anthropicMessage{Role: "user", Content: []anthropicBlock{block}})
			}
		}
	}

	out := anthropicReq{
		Model:     model,
		MaxTokens: c.maxTokens,
		System:    strings.Join(systemParts, "\n\n"),
		Messages:  msgs,
		Stream:    stream,
	}
	if req.Temperature > 0 {
		t := req.Temperature
		out.Temperature = &t
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	return out
}

func isToolResultTurn(m anthropicMessage) bool {
	return len(m.Content) > 0 && m.Content[len(m.Content)-1].Type == "tool_result"
}

func (c *Client) newAnthropicHTTPReq(ctx context.Context, body []byte, stream bool) (*http.Request, error) {
	url := c.baseURL + "/v1/messages"
	if strings.HasSuffix(c.baseURL, "/v1") {
		url = c.baseURL + "/messages"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", c.anthropicVersion)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	return httpReq, nil
}

// ---- inbound (Anthropic -> neutral) ----

type anthropicResp struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Model      string `json:"model"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (c *Client) anthropicChat(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(c.buildAnthropicReq(req, false))
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newAnthropicHTTPReq(ctx, body, false)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: %s: %s", resp.Status, truncate(raw, 500))
	}

	var ar anthropicResp
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("llm: decode: %w: %s", err, truncate(raw, 200))
	}

	msg := Message{Role: "assistant"}
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			msg.Content += b.Text
		case "tool_use":
			args := string(b.Input)
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			tc := ToolCall{ID: b.ID, Type: "function"}
			tc.Function.Name = b.Name
			tc.Function.Arguments = args
			msg.ToolCalls = append(msg.ToolCalls, tc)
		}
	}

	out := &Response{Model: ar.Model}
	out.Choices = append(out.Choices, struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	}{Message: msg, FinishReason: ar.StopReason})
	out.Usage.PromptTokens = ar.Usage.InputTokens
	out.Usage.CompletionTokens = ar.Usage.OutputTokens
	out.Usage.TotalTokens = ar.Usage.InputTokens + ar.Usage.OutputTokens
	return out, nil
}

func (c *Client) anthropicChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	body, err := json.Marshal(c.buildAnthropicReq(req, true))
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newAnthropicHTTPReq(ctx, body, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("llm: %s: %s", resp.Status, truncate(raw, 500))
	}

	out := make(chan StreamEvent, 16)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		parseAnthropicSSE(resp.Body, out)
	}()
	return out, nil
}

// parseAnthropicSSE walks the Anthropic Messages streaming protocol and pushes
// neutral StreamEvents into out. Anthropic emits typed events:
//
//	message_start         {message:{usage:{input_tokens}}}
//	content_block_start   {index, content_block:{type:text|tool_use, id, name}}
//	content_block_delta   {index, delta:{type:text_delta,text | input_json_delta,partial_json}}
//	content_block_stop    {index}
//	message_delta         {delta:{stop_reason}, usage:{output_tokens}}
//	message_stop
//
// Text deltas are emitted immediately. tool_use blocks accumulate their id +
// name (from the start event) and their input JSON (from input_json_delta
// chunks); all tool_use blocks are emitted as one consolidated tool_calls
// event when the stream ends.
func parseAnthropicSSE(r io.Reader, out chan<- StreamEvent) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	type blockAcc struct {
		kind string // text | tool_use
		id   string
		name string
		args strings.Builder
	}
	blocks := map[int]*blockAcc{}
	var order []int
	var fullContent strings.Builder

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // skip `event:` lines and blanks; the data JSON is self-describing
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}

		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue // tolerate keep-alives / unknown event payloads
		}

		switch ev.Type {
		case "content_block_start":
			b := &blockAcc{kind: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			order = append(order, ev.Index)

		case "content_block_delta":
			b := blocks[ev.Index]
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					fullContent.WriteString(ev.Delta.Text)
					out <- StreamEvent{Kind: "delta", Content: ev.Delta.Text}
				}
			case "input_json_delta":
				if b != nil {
					b.args.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "error":
			out <- StreamEvent{Kind: "error", Err: fmt.Errorf("llm: %s: %s", ev.Error.Type, ev.Error.Message)}
			return

		case "message_stop":
			// terminal — handled after the loop
		}
	}
	if err := sc.Err(); err != nil {
		out <- StreamEvent{Kind: "error", Err: err}
		return
	}

	// Emit any accumulated tool calls as one event, in block order.
	var tcs []ToolCall
	for _, idx := range order {
		b := blocks[idx]
		if b == nil || b.kind != "tool_use" {
			continue
		}
		args := strings.TrimSpace(b.args.String())
		if args == "" {
			args = "{}"
		}
		tc := ToolCall{ID: b.id, Type: "function"}
		tc.Function.Name = b.name
		tc.Function.Arguments = args
		tcs = append(tcs, tc)
	}
	if len(tcs) > 0 {
		out <- StreamEvent{Kind: "tool_calls", ToolCalls: tcs}
	}
	out <- StreamEvent{Kind: "done", Content: fullContent.String()}
}
