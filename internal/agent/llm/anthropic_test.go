package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildAnthropicReq is the translation hot-spot: system collapsing, tool-call
// mapping, and merging consecutive tool results into one user turn.
func TestBuildAnthropicReq(t *testing.T) {
	c := New(Config{APIKey: "k", Model: "claude-sonnet-4-6"})

	assistant := Message{Role: "assistant"}
	tc := ToolCall{ID: "tu_1", Type: "function"}
	tc.Function.Name = "get_invoice"
	tc.Function.Arguments = `{"id":"INV-1"}`
	assistant.ToolCalls = []ToolCall{tc}

	req := Request{
		Temperature: 0.2,
		Messages: []Message{
			{Role: "system", Content: "You are a bot."},
			{Role: "user", Content: "hi"},
			assistant,
			{Role: "tool", ToolCallID: "tu_1", Name: "get_invoice", Content: "result A"},
			{Role: "tool", ToolCallID: "tu_2", Name: "get_invoice", Content: "result B"},
		},
		Tools: []Tool{{Type: "function", Function: ToolFunction{
			Name: "get_invoice", Description: "d", Parameters: map[string]any{"type": "object"},
		}}},
	}

	got := c.buildAnthropicReq(req, false)

	if got.System != "You are a bot." {
		t.Errorf("system not collapsed to top level: %q", got.System)
	}
	if got.MaxTokens != defaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", got.MaxTokens, defaultMaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 {
		t.Errorf("temperature not passed through: %v", got.Temperature)
	}
	// system must not appear as a turn → user, assistant, user(tool_results)
	if len(got.Messages) != 3 {
		t.Fatalf("want 3 turns (user, assistant, merged tool results), got %d", len(got.Messages))
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content[0].Type != "tool_use" {
		t.Errorf("assistant tool_use not mapped: %+v", got.Messages[1])
	}
	if got.Messages[1].Content[0].Name != "get_invoice" || string(got.Messages[1].Content[0].Input) != `{"id":"INV-1"}` {
		t.Errorf("tool_use input not mapped: %+v", got.Messages[1].Content[0])
	}
	last := got.Messages[2]
	if last.Role != "user" || len(last.Content) != 2 {
		t.Fatalf("consecutive tool results should merge into one user turn with 2 blocks, got role=%s blocks=%d", last.Role, len(last.Content))
	}
	if last.Content[0].ToolUseID != "tu_1" || last.Content[1].ToolUseID != "tu_2" {
		t.Errorf("tool_result tool_use_id mapping wrong: %+v", last.Content)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "get_invoice" {
		t.Errorf("tools not translated: %+v", got.Tools)
	}
}

func TestAnthropicChat_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Errorf("missing anthropic-version header")
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/messages") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"claude-sonnet-4-6",
			"stop_reason":"tool_use",
			"usage":{"input_tokens":11,"output_tokens":7},
			"content":[
				{"type":"text","text":"Let me look."},
				{"type":"tool_use","id":"tu_9","name":"get_invoice","input":{"id":"INV-9"}}
			]
		}`))
	}))
	defer srv.Close()

	c := New(Config{APIKey: "secret", BaseURL: srv.URL, Model: "claude-sonnet-4-6"})
	resp, err := c.Chat(context.Background(), Request{Messages: []Message{{Role: "user", Content: "find INV-9"}}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("want 1 choice, got %d", len(resp.Choices))
	}
	m := resp.Choices[0].Message
	if m.Content != "Let me look." {
		t.Errorf("text not assembled: %q", m.Content)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].ID != "tu_9" || m.ToolCalls[0].Function.Name != "get_invoice" {
		t.Errorf("tool_use not mapped to ToolCall: %+v", m.ToolCalls)
	}
	if m.ToolCalls[0].Function.Arguments != `{"id":"INV-9"}` {
		t.Errorf("tool args not marshalled: %q", m.ToolCalls[0].Function.Arguments)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Errorf("usage mapping wrong: %+v", resp.Usage)
	}
}

func TestParseAnthropicSSE(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_3","name":"get_invoice"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"id\":"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"INV-3\"}"}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")

	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		parseAnthropicSSE(strings.NewReader(stream), out)
	}()

	var text strings.Builder
	var toolCalls []ToolCall
	var doneContent string
	sawDone := false
	for ev := range out {
		switch ev.Kind {
		case "delta":
			text.WriteString(ev.Content)
		case "tool_calls":
			toolCalls = ev.ToolCalls
		case "done":
			sawDone = true
			doneContent = ev.Content
		case "error":
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if text.String() != "Hello world" {
		t.Errorf("text deltas = %q, want %q", text.String(), "Hello world")
	}
	if !sawDone || doneContent != "Hello world" {
		t.Errorf("done event missing/incomplete: sawDone=%v content=%q", sawDone, doneContent)
	}
	if len(toolCalls) != 1 || toolCalls[0].ID != "tu_3" || toolCalls[0].Function.Name != "get_invoice" {
		t.Fatalf("tool call not accumulated: %+v", toolCalls)
	}
	if toolCalls[0].Function.Arguments != `{"id":"INV-3"}` {
		t.Errorf("input_json_delta not concatenated: %q", toolCalls[0].Function.Arguments)
	}
	// sanity: arguments must be valid JSON
	var m map[string]any
	if err := json.Unmarshal([]byte(toolCalls[0].Function.Arguments), &m); err != nil {
		t.Errorf("accumulated args not valid JSON: %v", err)
	}
}
