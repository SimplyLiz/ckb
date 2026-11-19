package bench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustParseFixture(t *testing.T, name string) *SessionRecord {
	t.Helper()
	rec, err := ParseTranscriptFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("ParseTranscriptFile(%s): %v", name, err)
	}
	return rec
}

func TestParseTranscript_Basic(t *testing.T) {
	rec := mustParseFixture(t, "session_basic.jsonl")

	if rec.SessionID != "sess-basic-0001" {
		t.Errorf("SessionID = %q, want sess-basic-0001", rec.SessionID)
	}
	if rec.Cwd != "/tmp/fixture-repo" {
		t.Errorf("Cwd = %q, want /tmp/fixture-repo", rec.Cwd)
	}
	if rec.Turns != 4 {
		t.Errorf("Turns = %d, want 4", rec.Turns)
	}

	wantTokens := Tokens{Input: 370, CacheCreation: 50, CacheRead: 650, Output: 125, Thinking: 5, Total: 1195}
	if rec.Tokens != wantTokens {
		t.Errorf("Tokens = %+v, want %+v", rec.Tokens, wantTokens)
	}

	wantToolCalls := map[string]int{"Read": 1, "Grep": 1, "mcp__ckb__searchSymbols": 1, "Bash": 1}
	if !mapsEqual(rec.ToolCalls, wantToolCalls) {
		t.Errorf("ToolCalls = %v, want %v", rec.ToolCalls, wantToolCalls)
	}

	if rec.AgentFileReads != 1 {
		t.Errorf("AgentFileReads = %d, want 1", rec.AgentFileReads)
	}
	if rec.DistinctFiles != 1 {
		t.Errorf("DistinctFiles = %d, want 1", rec.DistinctFiles)
	}
	if rec.AgentSearches != 1 {
		t.Errorf("AgentSearches = %d, want 1", rec.AgentSearches)
	}
	if rec.BashCalls != 1 {
		t.Errorf("BashCalls = %d, want 1", rec.BashCalls)
	}
	if rec.CKBCalls != 1 {
		t.Errorf("CKBCalls = %d, want 1", rec.CKBCalls)
	}
	wantCKBTools := map[string]int{"mcp__ckb__searchSymbols": 1}
	if !mapsEqual(rec.CKBTools, wantCKBTools) {
		t.Errorf("CKBTools = %v, want %v", rec.CKBTools, wantCKBTools)
	}

	wantDuration := 20 * time.Second
	if rec.Duration != wantDuration {
		t.Errorf("Duration = %v, want %v", rec.Duration, wantDuration)
	}

	// Ledger is left unpopulated by ParseTranscript itself.
	if rec.Ledger.Calls != -1 {
		t.Errorf("Ledger.Calls = %d, want -1 (unpopulated by ParseTranscript)", rec.Ledger.Calls)
	}

	if len(rec.Notes) != 0 {
		t.Errorf("Notes = %v, want none (no malformed lines in this fixture)", rec.Notes)
	}
}

func TestParseTranscript_Dedupe(t *testing.T) {
	rec := mustParseFixture(t, "session_dedupe.jsonl")

	// One streamed message (msg_dup, split across 4 lines) + one message with
	// neither message.id nor uuid (always its own turn) = 2 turns, not 5.
	if rec.Turns != 2 {
		t.Errorf("Turns = %d, want 2", rec.Turns)
	}

	wantTokens := Tokens{Input: 525, CacheCreation: 0, CacheRead: 1000, Output: 99, Thinking: 10, Total: 1624}
	if rec.Tokens != wantTokens {
		t.Errorf("Tokens = %+v, want %+v (usage must be counted once per message.id group, not once per line)", rec.Tokens, wantTokens)
	}

	// The same tool_use id ("tool_shared") appears on two lines of the same
	// message group; it must be counted once.
	if got := rec.ToolCalls["Read"]; got != 1 {
		t.Errorf("ToolCalls[Read] = %d, want 1 (duplicate tool_use id must be deduped)", got)
	}
	if rec.AgentFileReads != 1 {
		t.Errorf("AgentFileReads = %d, want 1", rec.AgentFileReads)
	}
	if rec.DistinctFiles != 1 {
		t.Errorf("DistinctFiles = %d, want 1", rec.DistinctFiles)
	}
}

func TestParseTranscript_MalformedTolerance(t *testing.T) {
	rec := mustParseFixture(t, "session_malformed.jsonl")

	// 3 bad records: a non-JSON line, a truncated JSON object, and a
	// well-formed assistant record with a null message.
	if len(rec.Notes) != 1 {
		t.Fatalf("Notes = %v, want exactly one note about skipped lines", rec.Notes)
	}
	if rec.Notes[0] != "3 malformed line(s) skipped" {
		t.Errorf("Notes[0] = %q, want %q", rec.Notes[0], "3 malformed line(s) skipped")
	}

	// The 3 well-formed assistant records must still be extracted correctly.
	if rec.Turns != 3 {
		t.Errorf("Turns = %d, want 3", rec.Turns)
	}
	wantTokens := Tokens{Input: 60, Output: 25, Total: 85}
	if rec.Tokens != wantTokens {
		t.Errorf("Tokens = %+v, want %+v", rec.Tokens, wantTokens)
	}
	if rec.AgentFileReads != 1 || rec.AgentSearches != 1 {
		t.Errorf("AgentFileReads=%d AgentSearches=%d, want 1 and 1", rec.AgentFileReads, rec.AgentSearches)
	}
}

func TestLocateTranscript_ExactSlug(t *testing.T) {
	dir := t.TempDir()
	cwd := "/Users/lisa/Work/Projects/CKB/src"
	slug := SlugifyCwd(cwd)
	if slug != "-Users-lisa-Work-Projects-CKB-src" {
		t.Fatalf("SlugifyCwd(%q) = %q, want -Users-lisa-Work-Projects-CKB-src", cwd, slug)
	}

	projDir := filepath.Join(dir, slug)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "abc-123"
	transcriptPath := filepath.Join(projDir, sessionID+".jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := locateTranscriptIn(dir, sessionID, cwd)
	if err != nil {
		t.Fatalf("locateTranscriptIn: %v", err)
	}
	if got != transcriptPath {
		t.Errorf("locateTranscriptIn = %q, want %q", got, transcriptPath)
	}
}

func TestLocateTranscript_GlobFallback(t *testing.T) {
	dir := t.TempDir()
	// Session lives under a project dir that does not match the cwd we pass
	// in (e.g. the session ran in a different directory than the caller's).
	otherProjDir := filepath.Join(dir, "-some-other-project")
	if err := os.MkdirAll(otherProjDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionID := "xyz-789"
	transcriptPath := filepath.Join(otherProjDir, sessionID+".jsonl")
	if err := os.WriteFile(transcriptPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := locateTranscriptIn(dir, sessionID, "/does/not/match/anything")
	if err != nil {
		t.Fatalf("locateTranscriptIn: %v", err)
	}
	if got != transcriptPath {
		t.Errorf("locateTranscriptIn = %q, want %q (glob fallback)", got, transcriptPath)
	}
}

func TestLocateTranscript_NotFound(t *testing.T) {
	dir := t.TempDir()
	if _, err := locateTranscriptIn(dir, "does-not-exist", "/nope"); err == nil {
		t.Error("expected an error for a session with no transcript anywhere")
	}
}

func TestCompare_Math(t *testing.T) {
	a := SessionRecord{
		SessionID:      "A",
		Tokens:         Tokens{Input: 1000, CacheRead: 2000, CacheCreation: 500, Output: 300, Total: 3800},
		Turns:          10,
		ToolCalls:      map[string]int{"Read": 5, "Bash": 3},
		AgentFileReads: 5,
		AgentSearches:  2,
		CKBCalls:       0,
		Duration:       10 * time.Second,
	}
	b := SessionRecord{
		SessionID:      "B",
		Tokens:         Tokens{Input: 500, CacheRead: 1000, CacheCreation: 100, Output: 200, Total: 1800},
		Turns:          6,
		ToolCalls:      map[string]int{"Read": 2, "Bash": 1, "mcp__ckb__searchSymbols": 4},
		AgentFileReads: 2,
		AgentSearches:  1,
		CKBCalls:       4,
		Duration:       6 * time.Second,
	}

	cmp := Compare(a, b)

	if cmp.SessionA != "A" || cmp.SessionB != "B" {
		t.Fatalf("SessionA/B = %q/%q", cmp.SessionA, cmp.SessionB)
	}

	byMetric := map[string]Delta{}
	for _, d := range cmp.Metrics {
		byMetric[d.Metric] = d
	}

	tt := byMetric["tokens.total"]
	if tt.A != 3800 || tt.B != 1800 || tt.Delta != -2000 {
		t.Errorf("tokens.total = %+v, want A=3800 B=1800 Delta=-2000", tt)
	}
	wantPct := -2000.0 / 3800.0 * 100
	if diff := tt.Percent - wantPct; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("tokens.total.Percent = %v, want %v", tt.Percent, wantPct)
	}

	ctx := byMetric["tokens.context"]
	// context = input + cacheRead + cacheCreation
	if ctx.A != 3500 || ctx.B != 1600 {
		t.Errorf("tokens.context = %+v, want A=3500 B=1600", ctx)
	}

	tc := byMetric["toolCalls.total"]
	if tc.A != 8 || tc.B != 7 {
		t.Errorf("toolCalls.total = %+v, want A=8 B=7", tc)
	}

	dur := byMetric["durationMs"]
	if dur.A != 10000 || dur.B != 6000 || dur.Delta != -4000 {
		t.Errorf("durationMs = %+v, want A=10000 B=6000 Delta=-4000", dur)
	}

	ckb := byMetric["ckbCalls"]
	if ckb.A != 0 || ckb.B != 4 || ckb.Percent != 0 {
		t.Errorf("ckbCalls = %+v, want A=0 B=4 Percent=0 (division by zero A guarded)", ckb)
	}
}

func TestNoLedger(t *testing.T) {
	var l LedgerSource = NoLedger{}
	calls, err := l.CallsForSession(context.Background(), "any-session")
	if err != nil {
		t.Fatalf("NoLedger returned error: %v", err)
	}
	if calls != nil {
		t.Fatalf("NoLedger returned %v, want nil", calls)
	}
}

type fakeLedger struct {
	calls []LedgerCall
	err   error
}

func (f fakeLedger) CallsForSession(_ context.Context, _ string) ([]LedgerCall, error) {
	return f.calls, f.err
}

func TestApplyLedger_NotWired(t *testing.T) {
	rec := &SessionRecord{}
	applyLedger(context.Background(), rec, "s1", NoLedger{})
	if rec.Ledger.Calls != -1 {
		t.Errorf("Ledger.Calls = %d, want -1", rec.Ledger.Calls)
	}
	found := false
	for _, n := range rec.Notes {
		if n == "ledger not wired: no activity-ledger data available for this session" {
			found = true
		}
	}
	if !found {
		t.Errorf("Notes = %v, want a note about the ledger not being wired", rec.Notes)
	}
}

func TestApplyLedger_WithData(t *testing.T) {
	rec := &SessionRecord{}
	fl := fakeLedger{calls: []LedgerCall{
		{Tool: "searchSymbols", ResponseBytes: 100, Facts: map[string]int{"symbols": 3}},
		{Tool: "findReferences", ResponseBytes: 50, Error: "timeout"},
	}}
	applyLedger(context.Background(), rec, "s1", fl)
	if rec.Ledger.Calls != 2 {
		t.Errorf("Ledger.Calls = %d, want 2", rec.Ledger.Calls)
	}
	if rec.Ledger.Bytes != 150 {
		t.Errorf("Ledger.Bytes = %d, want 150", rec.Ledger.Bytes)
	}
	if rec.Ledger.Errors != 1 {
		t.Errorf("Ledger.Errors = %d, want 1", rec.Ledger.Errors)
	}
	if rec.Ledger.Facts["symbols"] != 3 {
		t.Errorf("Ledger.Facts[symbols] = %d, want 3", rec.Ledger.Facts["symbols"])
	}
}

func TestApplyLedger_Error(t *testing.T) {
	rec := &SessionRecord{}
	fl := fakeLedger{err: errors.New("boom")}
	applyLedger(context.Background(), rec, "s1", fl)
	if rec.Ledger.Calls != -1 {
		t.Errorf("Ledger.Calls = %d, want -1 on ledger error", rec.Ledger.Calls)
	}
	if len(rec.Notes) == 0 {
		t.Error("expected a note recording the ledger query failure")
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
