package storage

import (
	"database/sql"
	"fmt"
	"sort"
)

// ToolCall represents a single MCP tool invocation recorded in the activity ledger.
type ToolCall struct {
	ID            int64
	Ts            int64 // unix ms, call start
	SessionID     string
	Consumer      string
	Tool          string
	ParamsHash    string
	Params        string // canonical JSON, truncated (may be empty)
	Target        string // best-effort primary argument (may be empty)
	DurationMs    int64
	ResponseBytes int64
	Truncated     bool
	Error         string // empty if no error
	Facts         string // JSON object of string->int (may be empty)
}

// ToolCallFilter narrows a ListToolCalls query.
type ToolCallFilter struct {
	Since     int64 // unix ms, inclusive (0 = no lower bound)
	Until     int64 // unix ms, exclusive (0 = no upper bound)
	SessionID string
	Consumer  string
	Tool      string
	Limit     int // 0 = default (20)
}

// ToolCallSummary holds aggregated per-tool stats over a time window.
type ToolCallSummary struct {
	Tool           string  `json:"tool"`
	Calls          int64   `json:"calls"`
	P50DurationMs  float64 `json:"p50DurationMs"`
	P95DurationMs  float64 `json:"p95DurationMs"`
	AvgBytes       float64 `json:"avgBytes"`
	Errors         int64   `json:"errors"`
	Truncated      int64   `json:"truncated"`
	TruncationRate float64 `json:"truncationRate"`
}

// InsertToolCalls writes a batch of tool call records in a single transaction.
// Empty batches are a no-op.
func (db *DB) InsertToolCalls(batch []ToolCall) error {
	if len(batch) == 0 {
		return nil
	}

	return db.WithTx(func(tx *sql.Tx) error {
		stmt, err := tx.Prepare(`
			INSERT INTO tool_calls (
				ts, session_id, consumer, tool, params_hash, params, target,
				duration_ms, response_bytes, truncated, error, facts
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return fmt.Errorf("failed to prepare tool_calls insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		for _, c := range batch {
			truncatedInt := 0
			if c.Truncated {
				truncatedInt = 1
			}
			if _, err := stmt.Exec(
				c.Ts,
				nullableString(c.SessionID),
				nullableString(c.Consumer),
				c.Tool,
				nullableString(c.ParamsHash),
				nullableString(c.Params),
				nullableString(c.Target),
				c.DurationMs,
				c.ResponseBytes,
				truncatedInt,
				nullableString(c.Error),
				nullableString(c.Facts),
			); err != nil {
				return fmt.Errorf("failed to insert tool_call: %w", err)
			}
		}

		return nil
	})
}

// nullableString returns nil for empty strings so they are stored as SQL NULL.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ListToolCalls returns tool call records matching the filter, newest first.
func (db *DB) ListToolCalls(filter ToolCallFilter) ([]ToolCall, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	query := `
		SELECT id, ts, COALESCE(session_id, ''), COALESCE(consumer, ''), tool,
		       COALESCE(params_hash, ''), COALESCE(params, ''), COALESCE(target, ''),
		       COALESCE(duration_ms, 0), COALESCE(response_bytes, 0), truncated,
		       COALESCE(error, ''), COALESCE(facts, '')
		FROM tool_calls
		WHERE 1=1
	`
	var args []interface{}

	if filter.Since > 0 {
		query += " AND ts >= ?"
		args = append(args, filter.Since)
	}
	if filter.Until > 0 {
		query += " AND ts < ?"
		args = append(args, filter.Until)
	}
	if filter.SessionID != "" {
		query += " AND session_id = ?"
		args = append(args, filter.SessionID)
	}
	if filter.Consumer != "" {
		query += " AND consumer = ?"
		args = append(args, filter.Consumer)
	}
	if filter.Tool != "" {
		query += " AND tool = ?"
		args = append(args, filter.Tool)
	}

	query += " ORDER BY ts DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool_calls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var calls []ToolCall
	for rows.Next() {
		var c ToolCall
		var truncatedInt int
		if err := rows.Scan(
			&c.ID, &c.Ts, &c.SessionID, &c.Consumer, &c.Tool,
			&c.ParamsHash, &c.Params, &c.Target,
			&c.DurationMs, &c.ResponseBytes, &truncatedInt,
			&c.Error, &c.Facts,
		); err != nil {
			return nil, fmt.Errorf("failed to scan tool_call: %w", err)
		}
		c.Truncated = truncatedInt != 0
		calls = append(calls, c)
	}

	return calls, rows.Err()
}

// SummarizeToolCalls returns per-tool aggregate stats for calls since the given
// unix-ms timestamp (0 = all time).
func (db *DB) SummarizeToolCalls(since int64) ([]ToolCallSummary, error) {
	query := `
		SELECT tool, COALESCE(duration_ms, 0), COALESCE(response_bytes, 0),
		       CASE WHEN error IS NOT NULL AND error != '' THEN 1 ELSE 0 END,
		       truncated
		FROM tool_calls
	`
	var args []interface{}
	if since > 0 {
		query += " WHERE ts >= ?"
		args = append(args, since)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tool_calls for summary: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type acc struct {
		durations []int64
		bytesSum  int64
		count     int64
		errors    int64
		truncated int64
	}
	byTool := make(map[string]*acc)

	for rows.Next() {
		var tool string
		var durationMs, responseBytes int64
		var isError, isTruncated int
		if err := rows.Scan(&tool, &durationMs, &responseBytes, &isError, &isTruncated); err != nil {
			return nil, fmt.Errorf("failed to scan tool_call summary row: %w", err)
		}
		a, ok := byTool[tool]
		if !ok {
			a = &acc{}
			byTool[tool] = a
		}
		a.durations = append(a.durations, durationMs)
		a.bytesSum += responseBytes
		a.count++
		if isError != 0 {
			a.errors++
		}
		if isTruncated != 0 {
			a.truncated++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	summaries := make([]ToolCallSummary, 0, len(byTool))
	for tool, a := range byTool {
		sort.Slice(a.durations, func(i, j int) bool { return a.durations[i] < a.durations[j] })
		summary := ToolCallSummary{
			Tool:          tool,
			Calls:         a.count,
			P50DurationMs: percentile(a.durations, 0.50),
			P95DurationMs: percentile(a.durations, 0.95),
			Errors:        a.errors,
			Truncated:     a.truncated,
		}
		if a.count > 0 {
			summary.AvgBytes = float64(a.bytesSum) / float64(a.count)
			summary.TruncationRate = float64(a.truncated) / float64(a.count)
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Calls > summaries[j].Calls })

	return summaries, nil
}

// percentile returns the p-th percentile (0.0-1.0) of a sorted int64 slice.
// Uses nearest-rank; returns 0 for an empty slice.
func percentile(sorted []int64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return float64(sorted[0])
	}
	rank := p * float64(n-1)
	lo := int(rank)
	hi := lo + 1
	if hi >= n {
		return float64(sorted[n-1])
	}
	frac := rank - float64(lo)
	return float64(sorted[lo])*(1-frac) + float64(sorted[hi])*frac
}

// PruneToolCalls deletes tool_calls rows older than the given unix-ms
// timestamp. Returns the number of rows removed.
func (db *DB) PruneToolCalls(before int64) (int64, error) {
	result, err := db.Exec(`DELETE FROM tool_calls WHERE ts < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("failed to prune tool_calls: %w", err)
	}
	return result.RowsAffected()
}
