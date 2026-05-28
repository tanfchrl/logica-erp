package erpclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDo_RequiresToken(t *testing.T) {
	c := New("http://example.invalid")
	err := c.Do(context.Background(), CallContext{}, "GET", "/x", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "token required") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestDo_ForwardsAuthAndCompanyAndJSON(t *testing.T) {
	var gotAuth, gotCo, gotMethod, gotPath, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCo = r.Header.Get("X-Company-Id")
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"ok":true,"name":"INV-1"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var out struct {
		OK   bool   `json:"ok"`
		Name string `json:"name"`
	}
	body := map[string]any{"foo": "bar"}
	if err := c.Do(context.Background(), CallContext{Token: "tok-abc", CompanyID: "cmp_123"}, "POST", "/api/v1/x", body, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer tok-abc" {
		t.Errorf("auth header = %q, want Bearer tok-abc", gotAuth)
	}
	if gotCo != "cmp_123" {
		t.Errorf("company header = %q, want cmp_123", gotCo)
	}
	if gotMethod != "POST" || gotPath != "/api/v1/x" {
		t.Errorf("method/path mismatch: %s %s", gotMethod, gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if !strings.Contains(gotBody, `"foo":"bar"`) {
		t.Errorf("request body did not contain marshalled JSON, got %q", gotBody)
	}
	if !out.OK || out.Name != "INV-1" {
		t.Errorf("decoded body wrong: %+v", out)
	}
}

func TestDo_OmitsCompanyHeader_WhenEmpty(t *testing.T) {
	var gotCo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCo = r.Header.Get("X-Company-Id")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Do(context.Background(), CallContext{Token: "tok"}, "GET", "/", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCo != "" {
		t.Errorf("expected no X-Company-Id, got %q", gotCo)
	}
}

func TestDo_APIErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"code":"ledger_imbalanced"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	err := c.Do(context.Background(), CallContext{Token: "tok"}, "POST", "/api/v1/je/submit", map[string]any{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T (%v)", err, err)
	}
	if apiErr.Status != 422 {
		t.Errorf("status = %d, want 422", apiErr.Status)
	}
	if !strings.Contains(apiErr.Body, "ledger_imbalanced") {
		t.Errorf("body lost: %q", apiErr.Body)
	}
}

func TestDo_NoOutSilentSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ignored":1}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.Do(context.Background(), CallContext{Token: "tok"}, "GET", "/x", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestDo_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	var out map[string]any
	err := c.Do(context.Background(), CallContext{Token: "tok"}, "GET", "/x", nil, &out)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestAPIError_TruncatesLongBody(t *testing.T) {
	long := strings.Repeat("x", 1000)
	e := &APIError{Status: 500, Path: "/p", Body: long}
	msg := e.Error()
	if !strings.HasSuffix(msg, "…") {
		t.Errorf("expected truncation marker, got tail %q", msg[len(msg)-10:])
	}
	if len(msg) > 500 {
		t.Errorf("error message not truncated: len=%d", len(msg))
	}
	// Make sure the json compaction expectation isn't lost — Error() uses raw body.
	if _, err := json.Marshal(e); err != nil {
		t.Errorf("APIError should marshal cleanly: %v", err)
	}
}
