package main

import (
	"context"

	"github.com/tandigital/logica-erp/internal/agent/llm"
)

// LLMProvider is the narrow contract chat handlers need to resolve a
// per-company LLM client. The full agentllmconfig.Service implements
// this; cmd/agent stays decoupled from the storage layer.
//
// ForCompany never returns nil: implementations fall back to the
// env-default config if the DB has no row, so chat never hard-fails on
// missing config.
type LLMProvider interface {
	ForCompany(ctx context.Context, companyID string) *llm.Client
}

// staticProvider wraps a single env-built client so callers without a DB
// can still hand a Provider to the chat handlers (tests, local dev with
// no DATABASE_URL).
type staticProvider struct{ c *llm.Client }

func (s staticProvider) ForCompany(_ context.Context, _ string) *llm.Client { return s.c }
