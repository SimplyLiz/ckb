package bench

import (
	"context"
	"fmt"
)

// BuildSessionRecord locates and parses the transcript for sessionID, then
// joins it with whatever ledger data is available from source.
//
// If cwd is non-empty it is used to compute the exact transcript slug first;
// LocateTranscript falls back to a glob across all project directories
// either way. If ledger is nil, NoLedger{} is used.
func BuildSessionRecord(ctx context.Context, sessionID, cwd string, ledger LedgerSource) (*SessionRecord, error) {
	if ledger == nil {
		ledger = NoLedger{}
	}

	path, err := LocateTranscript(sessionID, cwd)
	if err != nil {
		return nil, fmt.Errorf("locating transcript: %w", err)
	}

	rec, err := ParseTranscriptFile(path)
	if err != nil {
		return nil, fmt.Errorf("parsing transcript %s: %w", path, err)
	}

	if rec.SessionID == "" {
		rec.SessionID = sessionID
	}
	if rec.Cwd == "" {
		rec.Cwd = cwd
	}

	applyLedger(ctx, rec, sessionID, ledger)

	return rec, nil
}

// applyLedger queries ledger for sessionID and folds the result into rec.
// A nil calls slice (no error) means "no ledger data available", which is
// recorded as Ledger.Calls == -1 plus a note, distinct from a real zero.
func applyLedger(ctx context.Context, rec *SessionRecord, sessionID string, ledger LedgerSource) {
	calls, err := ledger.CallsForSession(ctx, sessionID)
	if err != nil {
		rec.Notes = append(rec.Notes, fmt.Sprintf("ledger query failed: %v", err))
		rec.Ledger = LedgerSummary{Calls: -1}
		return
	}
	if calls == nil {
		rec.Notes = append(rec.Notes, "ledger not wired: no activity-ledger data available for this session")
		rec.Ledger = LedgerSummary{Calls: -1}
		return
	}

	summary := LedgerSummary{Calls: len(calls)}
	if len(calls) > 0 {
		summary.Facts = map[string]int{}
	}
	for _, c := range calls {
		summary.Bytes += c.ResponseBytes
		if c.Error != "" {
			summary.Errors++
		}
		for k, v := range c.Facts {
			summary.Facts[k] += v
		}
	}
	rec.Ledger = summary
}
