// Package llm is the agent's chat client.
//
// The public surface here is deliberately provider-neutral: Message, Tool,
// ToolCall, Request, Response and StreamEvent describe a chat turn in terms
// the ReAct loop and tool registry understand, with no vendor-specific
// shapes leaking out. Each provider's HTTP wire format lives in its own file
// (anthropic.go today) and is reached through the provider switch in Chat /
// ChatStream. Adding a second provider is additive: a new constant, a new
// file, two new switch cases — the orchestrator never changes.
//
// Today exactly one provider is wired: Anthropic, talking to the native
// Messages API (POST /v1/messages, x-api-key + anthropic-version headers).
// This is NOT the OpenAI chat-completions protocol — Anthropic is not
// OpenAI-compatible, and routing it through a proxy was the source of the
// old breakage.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider identifies the upstream API dialect. Provider-neutral types above;
// provider-specific wire translation below.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultAnthropicVersion = "2023-06-01"
	defaultMaxTokens        = 4096
)

// Client is configured once (from env at boot, or per-company from the DB)
// and reused across requests.
type Client struct {
	httpc            *http.Client
	provider         Provider
	baseURL          string
	apiKey           string
	model            string
	maxTokens        int
	anthropicVersion string
}

// Config is the resolved per-company (or env-fallback) model configuration.
type Config struct {
	Provider  Provider // defaults to anthropic when empty
	BaseURL   string   // defaults to the provider's public endpoint when empty
	APIKey    string
	Model     string
	MaxTokens int // defaults to 4096 when <= 0

	// AnthropicVersion overrides the anthropic-version header. Empty = current
	// pinned default. Exposed mainly so an operator can bump it without a
	// redeploy if Anthropic ships a breaking version.
	AnthropicVersion string
}

func New(cfg Config) *Client {
	prov := cfg.Provider
	if prov == "" {
		prov = ProviderAnthropic
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}
	maxTok := cfg.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}
	ver := strings.TrimSpace(cfg.AnthropicVersion)
	if ver == "" {
		ver = defaultAnthropicVersion
	}
	return &Client{
		httpc:            &http.Client{Timeout: 120 * time.Second},
		provider:         prov,
		baseURL:          baseURL,
		apiKey:           cfg.APIKey,
		model:            cfg.Model,
		maxTokens:        maxTok,
		anthropicVersion: ver,
	}
}

// Model returns the model name we'll send upstream. Surfaced in the audit log
// so cost reporting can group by model.
func (c *Client) Model() string { return c.model }

// Configured reports whether the client has the minimum config to dial the
// provider. False means orchestration falls back to a canned reply (useful
// for local dev with no API key). For Anthropic the base URL always has a
// default, so the deciding factor is the API key.
func (c *Client) Configured() bool { return c.apiKey != "" }

// Message is one chat turn in provider-neutral form.
//
//	role=system     Content is the system prompt (collapsed into the
//	                provider's native system slot — never sent as a turn)
//	role=user       Content is the user's text
//	role=assistant  Content and/or ToolCalls (an assistant tool-use turn)
//	role=tool       a tool result: ToolCallID links it to the assistant's
//	                ToolCall.ID, Name is the tool name, Content is the result
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// Tool is the JSON-schema descriptor advertised to the model.
type Tool struct {
	Type     string       `json:"type"` // always "function" in the neutral form
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema
}

// ToolCall is one assistant-emitted tool invocation. Arguments is the
// JSON-encoded argument object (provider-neutral: Anthropic's structured
// `input` is marshalled into this string on the way out).
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Request is a provider-neutral chat request. Model is optional; the client's
// configured model is used when empty.
type Request struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream,omitempty"`
}

// StreamEvent is one chunk yielded by Client.ChatStream:
//
//	kind=delta       Content holds incremental assistant text
//	kind=tool_calls  ToolCalls is the FINAL accumulated tool-call list
//	kind=done        terminal; Content holds the full accumulated text
//	kind=error       Err populated; terminal
type StreamEvent struct {
	Kind      string
	Content   string
	ToolCalls []ToolCall
	Err       error
}

// Response is a provider-neutral non-streaming response. Shaped like a single
// choice so the ReAct loop reads choice.Message uniformly.
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

// Chat sends a non-streaming request and returns the assistant turn (text
// and/or tool calls). Errors surface the HTTP status + body so the audit log
// captures upstream failures verbatim.
func (c *Client) Chat(ctx context.Context, req Request) (*Response, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("llm: API key not set")
	}
	switch c.provider {
	case ProviderAnthropic:
		return c.anthropicChat(ctx, req)
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", c.provider)
	}
}

// ChatStream sends a streaming request. The returned channel yields
// StreamEvents in order: zero or more deltas, optionally a tool_calls event,
// then exactly one done or error event. Always drain the channel until
// done|error so the response body is closed.
func (c *Client) ChatStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("llm: API key not set")
	}
	switch c.provider {
	case ProviderAnthropic:
		return c.anthropicChatStream(ctx, req)
	default:
		return nil, fmt.Errorf("llm: unsupported provider %q", c.provider)
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
