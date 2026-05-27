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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Response is the non-streaming chat-completions response body. We don't
// support streaming yet — keeps the wire format auditable for v1.
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
