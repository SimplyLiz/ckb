package storage

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func newTestDBForToolCalls(t *testing.T) *DB {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "ckb-tool-calls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := Open(tmpDir, logger)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestToolCallsTableExists(t *testing.T) {
	db := newTestDBForToolCalls(t)

	var tableName string
	err := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='tool_calls'
	`).Scan(&tableName)
	if err != nil {
		t.Fatalf("tool_calls table not found: %v", err)
	}
}

func TestInsertAndListToolCalls(t *testing.T) {
	db := newTestDBForToolCalls(t)

	now := time.Now().UnixMilli()
	batch := []ToolCall{
		{
			Ts:            now - 3000,
			SessionID:     "sess-1",
			Consumer:      "claude-code",
			Tool:          "prepareChange",
			ParamsHash:    "abc123",
			Params:        `{"target":"auth/session.go"}`,
			Target:        "auth/session.go",
			DurationMs:    312,
			ResponseBytes: 6400,
			Truncated:     false,
			Facts:         `{"dependents":27,"tests":3}`,
		},
		{
			Ts:            now - 2000,
			SessionID:     "sess-1",
			Consumer:      "claude-code",
			Tool:          "searchSymbols",
			ParamsHash:    "def456",
			Target:        "Engine",
			DurationMs:    50,
			ResponseBytes: 800,
			Truncated:     true,
		},
		{
			Ts:            now - 1000,
			SessionID:     "sess-2",
			Consumer:      "cursor",
			Tool:          "findReferences",
			ParamsHash:    "ghi789",
			DurationMs:    10,
			ResponseBytes: 0,
			Error:         "symbol not found",
		},
	}

	if err := db.InsertToolCalls(batch); err != nil {
		t.Fatalf("InsertToolCalls failed: %v", err)
	}

	// Empty batch is a no-op and must not error.
	if err := db.InsertToolCalls(nil); err != nil {
		t.Fatalf("InsertToolCalls(nil) failed: %v", err)
	}

	// List all, newest first.
	calls, err := db.ListToolCalls(ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(calls))
	}
	if calls[0].Tool != "findReferences" {
		t.Errorf("expected newest-first order, got first=%s", calls[0].Tool)
	}
	if calls[0].Error != "symbol not found" {
		t.Errorf("expected error preserved, got %q", calls[0].Error)
	}
	if calls[2].Tool != "prepareChange" || calls[2].Facts == "" {
		t.Errorf("expected prepareChange with facts, got %+v", calls[2])
	}

	// Filter by session.
	sess1, err := db.ListToolCalls(ToolCallFilter{SessionID: "sess-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls(session) failed: %v", err)
	}
	if len(sess1) != 2 {
		t.Fatalf("expected 2 calls for sess-1, got %d", len(sess1))
	}

	// Filter by consumer.
	cursor, err := db.ListToolCalls(ToolCallFilter{Consumer: "cursor", Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls(consumer) failed: %v", err)
	}
	if len(cursor) != 1 || cursor[0].Tool != "findReferences" {
		t.Fatalf("expected 1 cursor call, got %+v", cursor)
	}

	// Filter by tool.
	byTool, err := db.ListToolCalls(ToolCallFilter{Tool: "searchSymbols", Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls(tool) failed: %v", err)
	}
	if len(byTool) != 1 || !byTool[0].Truncated {
		t.Fatalf("expected 1 truncated searchSymbols call, got %+v", byTool)
	}

	// Filter by since (exclude the oldest call).
	since, err := db.ListToolCalls(ToolCallFilter{Since: now - 2500, Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls(since) failed: %v", err)
	}
	if len(since) != 2 {
		t.Fatalf("expected 2 calls since cutoff, got %d", len(since))
	}

	// Limit.
	limited, err := db.ListToolCalls(ToolCallFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListToolCalls(limit) failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 call with limit=1, got %d", len(limited))
	}
}

func TestSummarizeToolCalls(t *testing.T) {
	db := newTestDBForToolCalls(t)

	now := time.Now().UnixMilli()
	batch := []ToolCall{
		{Ts: now, Tool: "prepareChange", DurationMs: 100, ResponseBytes: 1000},
		{Ts: now, Tool: "prepareChange", DurationMs: 200, ResponseBytes: 2000},
		{Ts: now, Tool: "prepareChange", DurationMs: 300, ResponseBytes: 3000, Truncated: true},
		{Ts: now, Tool: "prepareChange", DurationMs: 400, ResponseBytes: 4000, Error: "boom"},
		{Ts: now, Tool: "searchSymbols", DurationMs: 50, ResponseBytes: 500},
	}
	if err := db.InsertToolCalls(batch); err != nil {
		t.Fatalf("InsertToolCalls failed: %v", err)
	}

	summaries, err := db.SummarizeToolCalls(0)
	if err != nil {
		t.Fatalf("SummarizeToolCalls failed: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 tool summaries, got %d", len(summaries))
	}

	var prepChange *ToolCallSummary
	for i := range summaries {
		if summaries[i].Tool == "prepareChange" {
			prepChange = &summaries[i]
		}
	}
	if prepChange == nil {
		t.Fatalf("prepareChange summary not found in %+v", summaries)
	}
	if prepChange.Calls != 4 {
		t.Errorf("expected 4 calls, got %d", prepChange.Calls)
	}
	if prepChange.Errors != 1 {
		t.Errorf("expected 1 error, got %d", prepChange.Errors)
	}
	if prepChange.Truncated != 1 {
		t.Errorf("expected 1 truncated, got %d", prepChange.Truncated)
	}
	if prepChange.AvgBytes != 2500 {
		t.Errorf("expected avg bytes 2500, got %v", prepChange.AvgBytes)
	}
	// durations sorted: 100,200,300,400 -> p50 should be between 200 and 300
	if prepChange.P50DurationMs < 200 || prepChange.P50DurationMs > 300 {
		t.Errorf("unexpected p50: %v", prepChange.P50DurationMs)
	}

	// Since filter that excludes everything.
	future := now + 1_000_000
	empty, err := db.SummarizeToolCalls(future)
	if err != nil {
		t.Fatalf("SummarizeToolCalls(future) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected no summaries after future cutoff, got %+v", empty)
	}
}

func TestPruneToolCalls(t *testing.T) {
	db := newTestDBForToolCalls(t)

	now := time.Now().UnixMilli()
	batch := []ToolCall{
		{Ts: now - 100_000, Tool: "old"},
		{Ts: now, Tool: "new"},
	}
	if err := db.InsertToolCalls(batch); err != nil {
		t.Fatalf("InsertToolCalls failed: %v", err)
	}

	removed, err := db.PruneToolCalls(now - 1000)
	if err != nil {
		t.Fatalf("PruneToolCalls failed: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 row removed, got %d", removed)
	}

	remaining, err := db.ListToolCalls(ToolCallFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListToolCalls failed: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Tool != "new" {
		t.Fatalf("expected only 'new' to remain, got %+v", remaining)
	}
}
