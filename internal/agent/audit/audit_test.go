package audit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tandigital/logica-erp/internal/platform/dbx"
)

// requireDB skips the test cleanly when LOGICA_DATABASE_URL is unset (e.g.
// running `go test` without the dev Postgres up). The CI workflow sets the
// var so the integration tests run there.
func requireDB(t *testing.T) *dbx.DB {
	t.Helper()
	url := os.Getenv("LOGICA_DATABASE_URL")
	if url == "" {
		url = os.Getenv("DATABASE_URL")
	}
	if url == "" {
		t.Skip("LOGICA_DATABASE_URL / DATABASE_URL unset — skipping integration test")
	}
	db, err := dbx.Open(context.Background(), url)
	if err != nil {
		t.Skipf("DB open failed (%v) — skipping integration test", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// adminUserID returns any usable user id from the live DB. The recorder's
// user_id field is FK-constrained, so we can't fabricate one.
func adminUserID(t *testing.T, db *dbx.DB) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		"SELECT id FROM users WHERE enabled = true LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("fetch user: %v", err)
	}
	return id
}

// TestRecorder_FullSessionCoverage walks a synthetic agent session through
// every event_type the orchestrator can emit and asserts each lands as a
// row. This is the "audit-log completeness" guarantee from spec §10 — if
// a future code change skips an audit.Record call, the type-count
// assertion catches it.
func TestRecorder_FullSessionCoverage(t *testing.T) {
	db := requireDB(t)
	uid := adminUserID(t, db)
	rec := New(db)

	// Pick a session id we own + can later filter on. UUID-shaped so we
	// don't collide with real session rows.
	sessionID := "ses_audittest_" + time.Now().Format("20060102150405")

	events := []Event{
		{Type: EventPrompt,        Turn: 1, Payload: map[string]any{"content": "hi"}},
		{Type: EventToolCall,      Turn: 2, Payload: map[string]any{"name": "list_documents"}},
		{Type: EventToolResult,    Turn: 2, Payload: map[string]any{"ok": true}},
		{Type: EventProposal,      Turn: 3, Payload: map[string]any{"document_id": "si_test"}},
		{Type: EventHumanApproved, Turn: 0, Payload: map[string]any{"approval_id": "aprq_x"}},
		{Type: EventHumanRejected, Turn: 0, Payload: map[string]any{"approval_id": "aprq_y"}},
		{Type: EventPolicyBlocked, Turn: 4, Payload: map[string]any{"reason": "Tier 2 disabled"}},
		{Type: EventError,         Turn: 5, Payload: map[string]any{"err": "upstream timeout"}},
	}
	for _, e := range events {
		e.SessionID = sessionID
		e.UserID = uid
		e.Model = "test-model"
		e.TokensIn = 10
		e.TokensOut = 5
		rec.Record(context.Background(), e)
	}

	// Cleanup: agent_audit_log has an append-only trigger so DELETE is
	// blocked. We toggle the trigger off → delete our test rows → toggle
	// back on. Safe because the test acts on a unique session_id.
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log DISABLE TRIGGER agent_audit_no_update")
		_, _ = db.Exec(ctx, "DELETE FROM agent_audit_log WHERE session_id = $1", sessionID)
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log ENABLE TRIGGER agent_audit_no_update")
	})

	// Verify count + per-type breakdown. Count must be 8 (one row per
	// event); event_type must hit every kind exactly once.
	var total int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM agent_audit_log WHERE session_id = $1", sessionID,
	).Scan(&total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != len(events) {
		t.Errorf("row count: want %d, got %d", len(events), total)
	}

	rows, err := db.Query(context.Background(),
		`SELECT event_type, count(*) FROM agent_audit_log
		 WHERE session_id = $1 GROUP BY event_type`, sessionID)
	if err != nil {
		t.Fatalf("breakdown query: %v", err)
	}
	defer rows.Close()
	seen := map[string]int{}
	for rows.Next() {
		var et string
		var n int
		if err := rows.Scan(&et, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[et] = n
	}
	expected := []EventType{
		EventPrompt, EventToolCall, EventToolResult, EventProposal,
		EventHumanApproved, EventHumanRejected, EventPolicyBlocked, EventError,
	}
	for _, et := range expected {
		if seen[string(et)] != 1 {
			t.Errorf("event_type %s: want 1 row, got %d", et, seen[string(et)])
		}
	}
}

// TestRecorder_DropsIncomplete asserts that an event missing a required
// field (session_id / user_id / type) is silently dropped — never inserted.
// This protects against a partial-state bug where the orchestrator forgets
// to populate a field and the recorder happily writes nonsense.
func TestRecorder_DropsIncomplete(t *testing.T) {
	db := requireDB(t)
	rec := New(db)
	uid := adminUserID(t, db)
	sessionID := "ses_audittest_drop_" + time.Now().Format("20060102150405")
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log DISABLE TRIGGER agent_audit_no_update")
		_, _ = db.Exec(ctx, "DELETE FROM agent_audit_log WHERE session_id = $1", sessionID)
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log ENABLE TRIGGER agent_audit_no_update")
	})

	cases := []Event{
		{ /* zero value: all required missing */ },
		{SessionID: sessionID, Type: EventPrompt /* user_id missing */},
		{SessionID: sessionID, UserID: uid /* type missing */},
		{UserID: uid, Type: EventPrompt /* session_id missing */},
	}
	for _, e := range cases {
		rec.Record(context.Background(), e) // never returns; logs on drop
	}

	var n int
	if err := db.QueryRow(context.Background(),
		"SELECT count(*) FROM agent_audit_log WHERE session_id = $1", sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows from incomplete events; got %d", n)
	}
}

// TestRecorder_PayloadRoundTrip confirms the JSON payload makes it to the
// row intact — important because audit replay (#33's cost dashboard,
// admin audit log) reads the payload column to drive the UI.
func TestRecorder_PayloadRoundTrip(t *testing.T) {
	db := requireDB(t)
	rec := New(db)
	uid := adminUserID(t, db)
	sessionID := "ses_audittest_pl_" + time.Now().Format("20060102150405")
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log DISABLE TRIGGER agent_audit_no_update")
		_, _ = db.Exec(ctx, "DELETE FROM agent_audit_log WHERE session_id = $1", sessionID)
		_, _ = db.Exec(ctx, "ALTER TABLE agent_audit_log ENABLE TRIGGER agent_audit_no_update")
	})

	rec.Record(context.Background(), Event{
		SessionID: sessionID, UserID: uid, Turn: 1, Type: EventToolCall,
		Model:    "claude-sonnet-4-5",
		Payload:  map[string]any{"name": "list_documents", "arguments": `{"doctype":"sales_invoice"}`},
		TokensIn: 100, TokensOut: 50, LatencyMS: 240,
	})

	var payload, model string
	var tokensIn, tokensOut, latency int
	if err := db.QueryRow(context.Background(),
		`SELECT payload::text, model, tokens_in, tokens_out, latency_ms
		 FROM agent_audit_log WHERE session_id = $1 LIMIT 1`, sessionID,
	).Scan(&payload, &model, &tokensIn, &tokensOut, &latency); err != nil {
		t.Fatalf("query: %v", err)
	}
	if model != "claude-sonnet-4-5" || tokensIn != 100 || tokensOut != 50 || latency != 240 {
		t.Errorf("metadata round-trip failed: model=%s tokens=%d/%d latency=%dms",
			model, tokensIn, tokensOut, latency)
	}
	if !contains(payload, "list_documents") || !contains(payload, "sales_invoice") {
		t.Errorf("payload missing keys: %s", payload)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
