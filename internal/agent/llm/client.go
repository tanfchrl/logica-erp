// Package llm is a thin OpenAI-compatible chat-completions client.
//
// BYOM (bring-your-own-model): the agent service is configured via two env
// vars, AGENT_LLM_BASE_URL and AGENT_LLM_API_KEY. Anything that speaks the
// OpenAI HTTP wire protocol — LiteLLM, Ollama (>= 0.5), OpenRouter, Together,
// the Anthropic OpenAI-compat endpoint, or vLLM — drops in unchanged.
//
// This client intentionally does NOT pull in github.com/sashabaranov/go-openai
// or any vendor SDK. The chat-completions surface is small, stable, and we
// want zero coupling to a single provider's release cycle.
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
	"time"
)

// Client is configured from env at boot and reused across requests.
type Client struct {
	httpc    *http.Client
	baseURL  string
	apiKey   string
	model    string
}

// Config matches the env vars consumed by cmd/agent.
type Config struct {
	BaseURL string // AGENT_LLM_BASE_URL — e.g. https://api.openai.com/v1
	APIKey  string // AGENT_LLM_API_KEY  — bearer
	Model   string // AGENT_LLM_MODEL    — model name accepted by the backend
}

func New(cfg Config) *Client {
	return &Client{
		httpc:   &http.Client{Timeout: 120 * time.Second},
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
	}
}

// Model returns the model name we'll send to the backend. Surfaced in the
// audit log so cost reporting can group by model.
func (c *Client) Model() string { return c.model }

// Configured reports whether the client has the minimum config to actually
// dial an LLM. False means orchestration should fall back to canned replies
// (useful for local dev without an API key).
func (c *Client) Configured() bool { return c.baseURL != "" }

// Message is one chat-completions message.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"` // tool name when role=tool
}

// Tool is the JSON-schema descriptor sent to the model.
type Tool struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall is one assistant-emitted tool invocation.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON-encoded string
	} `json:"function"`
}

// Request is the chat-completions request body.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream,omitempty"`
}

// StreamEvent is one chunk yielded by Client.ChatStream. The Kind selects
// which fields are populated:
//
//	kind=delta       Content holds incremental assistant text
//	kind=tool_calls  ToolCalls is the FINAL accumulated tool-call list
//	                 (streaming tool_call deltas are accumulated inside
//	                  the stream reader before we emit one event)
//	kind=done        terminal event; nothing else after this
//	kind=error       Err populated; terminal
type StreamEvent struct {
	Kind      string
	Content   string
	ToolCalls []ToolCall
	Err       error
}

// Response is the non-streaming chat-completions response body. Used for
// tool-call detection; final assistant turns prefer ChatStream.
type Response struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

// Chat sends a non-streaming chat completion. Errors surface the HTTP status
// + body so the audit log captures upstream failures verbatim.
func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("llm: AGENT_LLM_BASE_URL not set")
	}
	if req.Model == "" {
		req.Model = c.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm: %s: %s", resp.Status, truncate(rawBody, 500))
	}
	var out Response
	if err := json.Unmarshal(rawBody, &out); err != nil {
		return nil, fmt.Errorf("llm: decode: %w: %s", err, truncate(rawBody, 200))
	}
	return &out, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// ChatStream sends a streaming chat-completions request. The returned
// channel yields StreamEvents in order: zero or more deltas, optionally a
// tool_calls event (when the model emits tool calls instead of content),
// then exactly one done or error event. Always close the response body by
// draining the channel until done|error.
func (c *Client) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("llm: AGENT_LLM_BASE_URL not set")
	}
	if req.Model == "" {
		req.Model = c.model
	}
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
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
		parseSSE(resp.Body, out)
	}()
	return out, nil
}

// parseSSE walks the SSE stream from an OpenAI-compatible chat-completions
// endpoint and pushes StreamEvents into out. The OpenAI wire format:
//
//	data: <json>\n\n
//	data: <json>\n\n
//	data: [DONE]\n\n
//
// Each JSON is a chat-completion chunk with a `choices[0].delta` carrying
// incremental content and/or tool_calls. Tool-call deltas come as an array
// of partial entries keyed by index — we accumulate them in a local map
// and emit one consolidated tool_calls event when the stream ends without
// content (the usual "tool-use" finish).
func parseSSE(r io.Reader, out chan<- StreamEvent) {
	sc := bufio.NewScanner(r)
	// SSE lines can be large when a tool_call's arguments are big.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	type partialToolCall struct {
		ID       string
		Name     string
		Args     string
	}
	tcAcc := map[int]*partialToolCall{}
	var tcOrder []int
	var fullContent string
	finishReason := ""

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id,omitempty"`
						Type     string `json:"type,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function,omitempty"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason,omitempty"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// Don't fatal on a single malformed chunk — the OpenAI wire
			// occasionally interleaves keep-alives or vendor extensions.
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		if choice.Delta.Content != "" {
			fullContent += choice.Delta.Content
			out <- StreamEvent{Kind: "delta", Content: choice.Delta.Content}
		}
		for _, td := range choice.Delta.ToolCalls {
			acc, ok := tcAcc[td.Index]
			if !ok {
				acc = &partialToolCall{}
				tcAcc[td.Index] = acc
				tcOrder = append(tcOrder, td.Index)
			}
			if td.ID != "" {
				acc.ID = td.ID
			}
			if td.Function.Name != "" {
				acc.Name = td.Function.Name
			}
			if td.Function.Arguments != "" {
				acc.Args += td.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		out <- StreamEvent{Kind: "error", Err: err}
		return
	}

	// Stream ended. If we accumulated tool calls, emit them as one event.
	if len(tcOrder) > 0 {
		tcs := make([]ToolCall, 0, len(tcOrder))
		for _, idx := range tcOrder {
			p := tcAcc[idx]
			tc := ToolCall{ID: p.ID, Type: "function"}
			tc.Function.Name = p.Name
			tc.Function.Arguments = p.Args
			tcs = append(tcs, tc)
		}
		out <- StreamEvent{Kind: "tool_calls", ToolCalls: tcs}
	}
	out <- StreamEvent{Kind: "done", Content: fullContent}
	_ = finishReason // reserved for usage stats / future telemetry
}
