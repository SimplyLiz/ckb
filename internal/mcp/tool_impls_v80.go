// Package mcp contains v8.0 MCP tool implementations.
// v8.0 focuses on reliability and error clarity as the foundation for compound operations.
package mcp

import (
	"context"
	goerrors "errors"
	"fmt"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
)

// getBackendRemediation returns remediation steps for a backend.
func getBackendRemediation(backendID string) string {
	switch backendID {
	case "scip":
		return "Run 'ckb index' to generate SCIP index for code intelligence"
	case "lsp":
		return "Configure LSP server in .ckb/config.json or run 'ckb setup'"
	case "git":
		return "Initialize git with 'git init' or navigate to a git repository"
	default:
		return "Run 'ckb doctor' to diagnose configuration"
	}
}

// formatTimeAgo formats a time as a human-readable "X ago" string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
}

// ReindexInput is the input for the reindex tool.
type ReindexInput struct {
	Scope          string `json:"scope"`          // "auto" | "full" | "incremental"
	TimeoutSeconds int    `json:"timeoutSeconds"` // default 300
}

// ReindexOutput is the output from the reindex tool.
type ReindexOutput struct {
	// Status: reindexed (index built and loaded) | reloaded (file on disk was
	// already fresh, the running engine had not loaded it) | skipped (fresh and
	// loaded) | busy (another reindex is running for this repo) | failed |
	// action_required (cannot be built from here; Message says what to do).
	Status              string `json:"status"`
	Mode                string `json:"mode,omitempty"` // full | incremental
	DurationMs          int64  `json:"durationMs"`
	Symbols             int    `json:"symbols,omitempty"`
	Documents           int    `json:"documents,omitempty"`
	CommitsBehindBefore int    `json:"commitsBehindBefore"`
	Message             string `json:"message,omitempty"`
}

const (
	reindexDefaultTimeout = 5 * time.Minute
	reindexMinTimeout     = 10 * time.Second
	reindexMaxTimeout     = 30 * time.Minute
)

// toolReindex implements the reindex MCP tool: build the SCIP index with the
// same plan 'ckb index' uses (incremental where the language supports it),
// then load it into the running engine — no shell, no server restart.
func (s *MCPServer) toolReindex(params map[string]interface{}) (*envelope.Response, error) {
	s.logger.Debug("Executing reindex", "params", params)

	scope := ReindexAuto
	if scopeStr, ok := params["scope"].(string); ok && scopeStr != "" {
		switch ReindexScope(scopeStr) {
		case ReindexAuto, ReindexFull, ReindexIncremental:
			scope = ReindexScope(scopeStr)
		default:
			return nil, errors.NewInvalidParameterError("scope", "must be auto, full or incremental")
		}
	}
	timeout := reindexDefaultTimeout
	if secs, ok := params["timeoutSeconds"].(float64); ok && secs > 0 {
		timeout = time.Duration(secs * float64(time.Second))
		if timeout < reindexMinTimeout {
			timeout = reindexMinTimeout
		}
		if timeout > reindexMaxTimeout {
			timeout = reindexMaxTimeout
		}
	}

	eng := s.engine()
	if eng == nil {
		return nil, errors.NewOperationError("reindex", fmt.Errorf("no engine loaded"))
	}
	repoRoot := eng.GetRepoRoot()
	if !s.reindexGuard.tryBegin(repoRoot) {
		return envelope.Operational(&ReindexOutput{
			Status:  "busy",
			Message: "A reindex is already running for this repository; call again when it has finished.",
		}), nil
	}
	defer s.reindexGuard.end(repoRoot)

	start := time.Now()
	ctx := context.Background()

	// An index that 'ckb index' built outside while this server ran is picked
	// up here (#134) — that alone may be all the caller needed.
	reloaded, reloadErr := eng.EnsureSCIPLoaded(ctx)
	if reloadErr != nil {
		s.logger.Warn("reindex: reloading the index on disk failed, rebuilding", "error", reloadErr.Error())
	}

	staleness := s.getIndexStaleness()
	fresh, _ := staleness["fresh"].(bool)
	loaded, _ := staleness["loaded"].(bool)
	commitsBehindBefore, _ := staleness["commitsBehind"].(int)

	counts := func(out *ReindexOutput) *ReindexOutput {
		if sb := eng.GetScipBackend(); sb != nil {
			if info := sb.GetIndexInfo(); info != nil && info.Available {
				out.Symbols = info.SymbolCount
				out.Documents = info.DocumentCount
			}
		}
		out.DurationMs = time.Since(start).Milliseconds()
		out.CommitsBehindBefore = commitsBehindBefore
		return out
	}

	if scope != ReindexFull && fresh && loaded {
		if reloaded {
			return envelope.Operational(counts(&ReindexOutput{
				Status:  "reloaded",
				Message: "Index on disk was already fresh; loaded it into the running server.",
			})), nil
		}
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "skipped",
			Message: "Index is fresh and loaded. Pass scope \"full\" to rebuild anyway.",
		})), nil
	}

	if s.reindexRunner == nil {
		msg := "This server cannot build the index itself. Run 'ckb index' in a terminal; the running server picks the new index up on its next call."
		if reason, ok := staleness["reason"].(string); ok && reason != "" {
			msg = "Index is stale (" + reason + "). " + msg
		}
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "action_required",
			Message: msg,
		})), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.logger.Info("reindex: building SCIP index", "repo", repoRoot, "scope", string(scope), "timeout", timeout.String())
	info, err := s.reindexRunner(runCtx, repoRoot, scope)
	switch {
	case err == nil:
	case goerrors.Is(err, ErrReindexNotPossible):
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "action_required",
			Message: err.Error() + ". Run 'ckb index' (or 'ckb index --scip' for large repos) in a terminal; the running server picks the new index up on its next call.",
		})), nil
	case goerrors.Is(err, ErrReindexLocked):
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "busy",
			Message: "Another process is indexing this repository (index lock held); call again when it has finished.",
		})), nil
	case runCtx.Err() != nil:
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "failed",
			Message: fmt.Sprintf("Indexer did not finish within %s; raise timeoutSeconds or run 'ckb index' in a terminal.", timeout),
		})), nil
	default:
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "failed",
			Message: "Indexer failed: " + err.Error(),
		})), nil
	}

	if err := eng.ReloadSCIP(ctx); err != nil {
		return envelope.Operational(counts(&ReindexOutput{
			Status:  "failed",
			Mode:    info.Mode,
			Message: "Index built but loading it into the running server failed: " + err.Error(),
		})), nil
	}

	return envelope.Operational(counts(&ReindexOutput{
		Status:  "reindexed",
		Mode:    info.Mode,
		Message: "Index rebuilt and loaded.",
	})), nil
}
