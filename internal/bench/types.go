// Package bench joins a Claude Code session transcript (client-side: tokens,
// turns, tool calls) with CKB's server-side activity ledger (what CKB actually
// delivered) into one SessionRecord, and compares two such records.
//
// See docs/plans/change-intelligence-and-activity-ledger.md, section 3
// ("Benchmark instrumentation").
package bench

import (
	"context"
	"time"
)

// LedgerCall is one row of the (future) activity ledger, i.e. one MCP tool
// call CKB served, as recorded server-side in the `tool_calls` table
// described in plan section 1. It is defined here, independently of
// internal/storage, because that table is being built concurrently by
// another work package; bench only needs this shape to join against it.
type LedgerCall struct {
	TS            time.Time
	Tool          string
	Target        string
	DurationMS    int
	ResponseBytes int
	Error         string
	Facts         map[string]int
}

// LedgerSource abstracts the server-side activity ledger so bench can be
// wired to it once it exists, and tested without it today.
type LedgerSource interface {
	// CallsForSession returns every ledger row recorded for sessionID, in
	// any order. A nil, nil return means "no ledger data available" (not
	// "zero calls happened") and callers should treat the ledger portion
	// of a SessionRecord as unpopulated.
	CallsForSession(ctx context.Context, sessionID string) ([]LedgerCall, error)
}

// NoLedger is a LedgerSource that always reports no data. It is the current
// CLI wiring until the storage-backed `tool_calls` ledger lands.
type NoLedger struct{}

// CallsForSession always returns nil, nil: no ledger is wired yet.
func (NoLedger) CallsForSession(_ context.Context, _ string) ([]LedgerCall, error) {
	return nil, nil
}

// Tokens breaks down token usage by class, summed across the deduped
// assistant turns of a session.
type Tokens struct {
	Input         int `json:"input"`
	CacheCreation int `json:"cacheCreation"`
	CacheRead     int `json:"cacheRead"`
	Output        int `json:"output"`
	Thinking      int `json:"thinking"`
	Total         int `json:"total"`
}

// LedgerSummary aggregates LedgerCall rows for a session. Calls is -1 when no
// ledger source produced data (see Ledger field docs on SessionRecord); it is
// never a bare zero used to mean "no data", so callers cannot mistake
// "ledger not wired" for "CKB was called zero times".
type LedgerSummary struct {
	Calls  int            `json:"calls"`
	Bytes  int            `json:"bytes"`
	Errors int            `json:"errors"`
	Facts  map[string]int `json:"facts,omitempty"`
}

// SessionRecord is the joined view of one Claude Code session: transcript
// (client side) plus activity ledger (server side, when wired).
type SessionRecord struct {
	SessionID string        `json:"sessionId"`
	Cwd       string        `json:"cwd,omitempty"`
	Start     time.Time     `json:"start"`
	End       time.Time     `json:"end"`
	Duration  time.Duration `json:"durationNs"`

	// Turns is the count of deduped assistant messages (see dedupe notes in
	// transcript.go).
	Turns int `json:"turns"`

	Tokens Tokens `json:"tokens"`

	// ToolCalls counts every tool_use block by tool name, across all
	// assistant turns.
	ToolCalls map[string]int `json:"toolCalls,omitempty"`

	AgentFileReads int `json:"agentFileReads"` // Read tool_use blocks
	DistinctFiles  int `json:"distinctFiles"`  // distinct input.file_path values seen across Read calls
	AgentSearches  int `json:"agentSearches"`  // Grep + Glob tool_use blocks
	BashCalls      int `json:"bashCalls"`

	// CKBCalls / CKBTools cover tool_use blocks whose name is prefixed
	// "mcp__ckb__" — i.e. calls the agent made against the CKB MCP server,
	// as seen from the transcript (not the ledger).
	CKBCalls int            `json:"ckbCalls"`
	CKBTools map[string]int `json:"ckbTools,omitempty"`

	// Ledger is the server-side view, joined by SessionID. Ledger.Calls is
	// -1 when no ledger source was wired or it returned nil data, so a
	// caller printing this can't confuse "not wired" with "0 CKB calls".
	Ledger LedgerSummary `json:"ledger"`

	// Notes carries non-fatal issues found while building the record:
	// "ledger not wired", malformed-line counts, missing transcript
	// fields, etc. Never invented causal narrative, per plan section 1.
	Notes []string `json:"notes,omitempty"`
}
