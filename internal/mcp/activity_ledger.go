package mcp

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/activity"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// wireActivityRecorder eagerly creates (and prunes) the activity recorder
// for engine's DB, mirroring the SetMetricsDB wiring pattern. This makes
// retention pruning happen at engine-load time rather than waiting for the
// first tool call. Safe to call with a nil engine or DB.
func wireActivityRecorder(engine *query.Engine, logger *slog.Logger) {
	if engine == nil || engine.DB() == nil {
		return
	}
	var activityCfg config.ActivityConfig
	if cfg := engine.Config(); cfg != nil {
		activityCfg = cfg.Activity
	}
	if !activityCfg.Enabled {
		return
	}
	getOrCreateActivityRecorder(engine.DB(), activityCfg, logger)
}

// Activity ledger wiring for the MCP server. Follows the same "package-level
// registry keyed by *storage.DB" shape as wide_result_metrics's SetMetricsDB,
// but keeps one Recorder per repo DB since the engine cache can have several
// repos loaded (and therefore several SQLite databases) at once — a tool
// call is recorded into whichever repo's engine is active when it runs.

var (
	activityMu   sync.Mutex
	activityRecs = map[*storage.DB]*activity.Recorder{}
)

// getOrCreateActivityRecorder returns the Recorder for db, creating (and
// pruning) one on first use. Returns nil if db is nil or activity is
// disabled — callers can Record on a nil *activity.Recorder safely.
func getOrCreateActivityRecorder(db *storage.DB, cfg config.ActivityConfig, logger *slog.Logger) *activity.Recorder {
	if db == nil {
		return nil
	}

	activityMu.Lock()
	defer activityMu.Unlock()

	if r, ok := activityRecs[db]; ok {
		return r
	}

	r := activity.NewRecorder(db, activity.Config{
		Enabled:     cfg.Enabled,
		StoreParams: cfg.StoreParams,
	}, logger)
	activityRecs[db] = r

	// Prune on first wiring for this DB (roughly "server start" for that repo).
	if cfg.Enabled {
		pruneActivityLedger(db, cfg, logger)
	}

	return r
}

// pruneActivityLedger deletes tool_calls rows older than the configured
// retention. Best-effort: errors are logged at debug and otherwise ignored.
func pruneActivityLedger(db *storage.DB, cfg config.ActivityConfig, logger *slog.Logger) {
	if db == nil {
		return
	}
	retentionDays := cfg.RetentionDays
	if retentionDays <= 0 {
		retentionDays = 30
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays).UnixMilli()
	removed, err := db.PruneToolCalls(cutoff)
	if err != nil {
		if logger != nil {
			logger.Debug("activity ledger prune failed", "error", err.Error())
		}
		return
	}
	if logger != nil && removed > 0 {
		logger.Debug("activity ledger pruned old rows", "removed", removed, "retentionDays", retentionDays)
	}
}

// closeActivityRecorder closes and forgets the recorder for db, if any.
func closeActivityRecorder(db *storage.DB) {
	if db == nil {
		return
	}
	activityMu.Lock()
	r, ok := activityRecs[db]
	if ok {
		delete(activityRecs, db)
	}
	activityMu.Unlock()
	if ok {
		r.Close()
	}
}

// closeAllActivityRecorders closes every registered recorder. Call on
// server shutdown so buffered rows are flushed before the process exits.
func closeAllActivityRecorders() {
	activityMu.Lock()
	recs := make([]*activity.Recorder, 0, len(activityRecs))
	for db := range activityRecs {
		recs = append(recs, activityRecs[db])
		delete(activityRecs, db)
	}
	activityMu.Unlock()

	for _, r := range recs {
		r.Close()
	}
}

// resetActivityRecorders is a test-only hook to clear global state between
// test cases that each spin up their own MCPServer/DB.
func resetActivityRecorders() {
	closeAllActivityRecorders()
}

var (
	sessionIDOnce    sync.Once
	sessionIDValue   string
	processStartUnix = time.Now().Unix()
)

// currentSessionID returns CLAUDE_CODE_SESSION_ID if set, else a stable
// per-process fallback ("<hostname>:<pid>:<processStartUnix>") computed once.
func currentSessionID() string {
	sessionIDOnce.Do(func() {
		if v := os.Getenv("CLAUDE_CODE_SESSION_ID"); v != "" {
			sessionIDValue = v
			return
		}
		hostname, err := os.Hostname()
		if err != nil || hostname == "" {
			hostname = "unknown-host"
		}
		sessionIDValue = fmt.Sprintf("%s:%d:%d", hostname, os.Getpid(), processStartUnix)
	})
	return sessionIDValue
}

// consumerID returns "<clientInfo.name>/<clientInfo.version>" (or just the
// name) captured from the MCP initialize handshake. Falls back to
// "claude-code" when Claude Code's own env vars are present, else "unknown".
func (s *MCPServer) consumerID() string {
	s.mu.RLock()
	name := s.clientName
	version := s.clientVersion
	s.mu.RUnlock()

	if name != "" {
		if version != "" {
			return name + "/" + version
		}
		return name
	}

	if os.Getenv("CLAUDECODE") == "1" || os.Getenv("CLAUDE_CODE_ENTRYPOINT") != "" {
		return "claude-code"
	}

	return "unknown"
}

// recordActivity builds and enqueues an activity ledger entry for one
// handleCallTool invocation. It never returns an error and never blocks the
// MCP request path: recording happens on a best-effort basis into whichever
// repo's engine (and therefore SQLite DB) is active for this call. callErr
// is the error returned by the tool handler, if any; result is nil in that
// case since the handler produced no envelope.
func (s *MCPServer) recordActivity(toolName string, params map[string]interface{}, start time.Time, responseBytes []byte, result *envelope.Response, callErr error) {
	eng := s.engine()
	if eng == nil || eng.DB() == nil {
		return
	}

	var activityCfg config.ActivityConfig
	if cfg := eng.Config(); cfg != nil {
		activityCfg = cfg.Activity
	}
	if !activityCfg.Enabled {
		return
	}

	recorder := getOrCreateActivityRecorder(eng.DB(), activityCfg, s.logger)
	if recorder == nil {
		return
	}

	entry := activity.Entry{
		Tool:          toolName,
		Params:        params,
		SessionID:     currentSessionID(),
		Consumer:      s.consumerID(),
		StartTime:     start,
		DurationMs:    time.Since(start).Milliseconds(),
		ResponseBytes: len(responseBytes),
	}

	if callErr != nil {
		entry.Error = callErr.Error()
	}

	if result != nil {
		entry.Data = result.Data
		if result.Meta != nil && result.Meta.Truncation != nil {
			entry.Truncated = result.Meta.Truncation.IsTruncated
		}
	}

	recorder.Record(entry)
}
