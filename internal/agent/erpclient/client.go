// Package erpclient is the HTTP shim through which the agent talks to the
// ERP core. Every request forwards the acting user's JWT + X-Company-Id —
// the agent never holds a privileged service account. If the user can't see
// or do something, neither can the agent.
package erpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	httpc   *http.Client
	baseURL string
}

func New(baseURL string) *Client {
	return &Client{
		httpc:   &http.Client{Timeout: 30 * time.Second},
		baseURL: baseURL,
	}
}

// CallContext bundles per-request auth context propagated from the user's
// session — the agent uses the user's own JWT, never its own.
type CallContext struct {
	Token     string // JWT — required
	CompanyID string // X-Company-Id — optional
}

// Do dispatches an HTTP request to the ERP core. The body, if provided, is
// JSON-encoded; the response is JSON-decoded into out (out may be nil for
// fire-and-forget calls). Non-2xx responses are returned as ErrAPI errors
// carrying the status + truncated body for the audit log.
func (c *Client) Do(ctx context.Context, cc CallContext, method, path string, body any, out any) error {
	if cc.Token == "" {
		return errors.New("erpclient: token required")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+cc.Token)
	if cc.CompanyID != "" {
		req.Header.Set("X-Company-Id", cc.CompanyID)
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Path: path, Body: string(raw)}
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("erpclient: decode %s: %w", path, err)
	}
	return nil
}

// APIError carries the upstream ERP response on a non-2xx. The agent's tool
// dispatcher converts these into structured tool_result events.
type APIError struct {
	Status int
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("erp api %s: status=%d body=%s", e.Path, e.Status, truncate(e.Body, 300))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
