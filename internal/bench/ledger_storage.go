package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// StorageLedger reads the activity ledger (tool_calls table) of one repo
// database. session_id in that table is CLAUDE_CODE_SESSION_ID verbatim
// when the MCP server ran under Claude Code, so no translation is needed.
type StorageLedger struct {
	DB *storage.DB
	// MaxCalls bounds one session read. Zero means 10000.
	MaxCalls int
}

// CallsForSession implements LedgerSource.
func (l StorageLedger) CallsForSession(_ context.Context, sessionID string) ([]LedgerCall, error) {
	if l.DB == nil {
		return nil, nil
	}
	limit := l.MaxCalls
	if limit <= 0 {
		limit = 10000
	}
	rows, err := l.DB.ListToolCalls(storage.ToolCallFilter{SessionID: sessionID, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("reading tool_calls for session %s: %w", sessionID, err)
	}
	out := make([]LedgerCall, 0, len(rows))
	for _, r := range rows {
		c := LedgerCall{
			TS:            time.UnixMilli(r.Ts),
			Tool:          r.Tool,
			Target:        r.Target,
			DurationMS:    int(r.DurationMs),
			ResponseBytes: int(r.ResponseBytes),
			Error:         r.Error,
		}
		if r.Facts != "" {
			var facts map[string]int
			if json.Unmarshal([]byte(r.Facts), &facts) == nil && len(facts) > 0 {
				c.Facts = facts
			}
		}
		out = append(out, c)
	}
	// Non-nil even when empty: "queried, zero calls" is distinct from
	// "not wired" (see LedgerSummary.Calls == -1).
	return out, nil
}
