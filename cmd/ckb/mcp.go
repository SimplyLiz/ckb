package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/indexplan"
	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/slogutil"
	"github.com/SimplyLiz/CodeMCP/internal/version"

	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server for Claude Code integration",
	Long: `Start the Model Context Protocol (MCP) server.

The MCP server enables Claude Code and other MCP clients to query CKB
for codebase comprehension information. It communicates via stdio using
JSON-RPC 2.0 protocol.

The server exposes the following tools:
  - getStatus: Get CKB system status
  - doctor: Diagnose configuration issues
  - getSymbol: Get symbol metadata and location
  - searchSymbols: Search for symbols by name
  - findReferences: Find all references to a symbol
  - getArchitecture: Get codebase architecture
  - analyzeImpact: Analyze the impact of changing a symbol

Example usage:
  ckb mcp --stdio

This command is typically invoked by MCP clients (like Claude Code) and
not directly by users.`,
	RunE: runMCP,
}

var (
	mcpStdio         bool
	mcpWatch         bool
	mcpWatchInterval time.Duration
	mcpRepo          string
	mcpPreset        string
	mcpListPresets   bool
)

const defaultWatchInterval = 10 * time.Second

func init() {
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.Flags().BoolVar(&mcpStdio, "stdio", true, "Use stdio for communication (default)")
	mcpCmd.Flags().BoolVar(&mcpWatch, "watch", false, "Watch for changes and auto-reindex")
	mcpCmd.Flags().DurationVar(&mcpWatchInterval, "watch-interval", defaultWatchInterval,
		"Watch mode polling interval (min 5s, max 5m)")
	mcpCmd.Flags().StringVar(&mcpRepo, "repo", "", "Repository path or registry name (auto-detected)")
	mcpCmd.Flags().StringVar(&mcpPreset, "preset", mcp.DefaultPreset,
		"Tool preset: core, review, refactor, federation, docs, ops, full")
	mcpCmd.Flags().BoolVar(&mcpListPresets, "list-presets", false,
		"List available presets with tool counts and token estimates")
}

func runMCP(cmd *cobra.Command, args []string) error {
	// Handle --list-presets flag
	if mcpListPresets {
		return listPresets()
	}

	// Create logger for MCP server
	// Writes to file (.ckb/logs/mcp.log) and stderr for errors
	cliLevel := slogutil.LevelFromVerbosity(verbosity, quiet)
	logger := slogutil.NewLogger(os.Stderr, cliLevel) // Default to stderr
	var factory *slogutil.LoggerFactory

	// Validate preset
	if !mcp.IsValidPreset(mcpPreset) {
		return fmt.Errorf("invalid preset: %s (valid: %v)", mcpPreset, mcp.ValidPresets())
	}

	// Determine mode and repo
	var server *mcp.MCPServer
	var repoRoot string
	var repoName string

	// Smart --repo detection: path vs registry name
	if mcpRepo != "" {
		if isRepoPath(mcpRepo) {
			// It's a path - use legacy single-engine mode
			repoRoot = mcpRepo
			fmt.Fprintf(os.Stderr, "Repository: %s (path)\n", repoRoot)
		} else {
			// It's a registry name - use multi-repo mode
			registry, err := repos.LoadRegistry()
			if err != nil {
				return fmt.Errorf("failed to load registry: %w", err)
			}
			entry, state, err := registry.Get(mcpRepo)
			if err != nil {
				return fmt.Errorf("repository '%s' not found in registry", mcpRepo)
			}
			if state != repos.RepoStateValid {
				return fmt.Errorf("repository '%s' is %s", mcpRepo, state)
			}
			repoRoot = entry.Path
			repoName = mcpRepo
			fmt.Fprintf(os.Stderr, "Repository: %s (%s) [%s]\n", repoName, repoRoot, state)
		}
	} else {
		// No --repo flag - use smart resolution
		resolved, err := repos.ResolveActiveRepo("")
		if err != nil {
			return fmt.Errorf("failed to resolve repository: %w", err)
		}

		if resolved.Entry != nil {
			repoRoot = resolved.Entry.Path
			repoName = resolved.Entry.Name

			// Format status message based on resolution source
			switch resolved.Source {
			case repos.ResolvedFromEnv:
				fmt.Fprintf(os.Stderr, "Repository: %s (%s) [from CKB_REPO]\n", repoName, repoRoot)
			case repos.ResolvedFromCWD:
				fmt.Fprintf(os.Stderr, "Repository: %s (%s) [cwd match]\n", repoName, repoRoot)
			case repos.ResolvedFromCWDGit:
				// Auto-detected unregistered git repo
				if resolved.State == repos.RepoStateUninitialized {
					if isLinkedWorktree(repoRoot) {
						// Worktrees come and go; registering each one only fills ~/.ckb.
						fmt.Fprintf(os.Stderr, "Repository: %s (%s) [linked worktree, not initialized]\n", repoName, repoRoot)
					} else if err := selfInitRepo(repoRoot); err != nil {
						fmt.Fprintf(os.Stderr, "Repository: %s (%s) [auto-detected, uninitialized]\n", repoName, repoRoot)
						fmt.Fprintf(os.Stderr, "  ⚠️  Automatic init failed (%v) — run 'ckb init' to set up\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "Repository: %s (%s) [auto-detected, initialized]\n", repoName, repoRoot)
					}
				} else {
					fmt.Fprintf(os.Stderr, "Repository: %s (%s) [auto-detected]\n", repoName, repoRoot)
					fmt.Fprintf(os.Stderr, "  ℹ️  Run 'ckb repo add %s .' to register permanently\n", repoName)
				}
				if resolved.SkippedDefault != "" {
					fmt.Fprintf(os.Stderr, "  Note: Default '%s' skipped (different git repo)\n", resolved.SkippedDefault)
				}
			case repos.ResolvedFromDefault:
				fmt.Fprintf(os.Stderr, "Repository: %s (%s) [default]\n", repoName, repoRoot)
				// Warn if we're in a different git repo
				if resolved.DetectedGitRoot != "" && resolved.DetectedGitRoot != repoRoot {
					fmt.Fprintf(os.Stderr, "  ⚠️  CWD is in '%s' but using default repo\n", filepath.Base(resolved.DetectedGitRoot))
				}
			}

		} else {
			// No repo found - fall back to current directory
			repoRoot = mustGetRepoRoot()
			fmt.Fprintf(os.Stderr, "Repository: %s (current directory)\n", repoRoot)
		}
	}

	// Change to repo directory so relative paths work
	if repoRoot != "" && repoRoot != "." {
		if err := os.Chdir(repoRoot); err != nil {
			logger.Error("Failed to change to repo directory",
				"path", repoRoot,
				"error", err.Error(),
			)
			return err
		}
	}

	// Set up file logging with LoggerFactory
	cfg, _ := config.LoadConfig(repoRoot)
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	factory = slogutil.NewLoggerFactory(repoRoot, cfg, cliLevel)
	defer factory.Close()

	// Create tee logger: file + stderr (errors only to stderr)
	if fileLogger, err := factory.MCPLogger(); err == nil {
		stderrHandler := slogutil.NewCKBHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})
		logger = slogutil.NewTeeLogger(fileLogger.Handler(), stderrHandler)
	}

	// Use lazy loading for fast MCP handshake
	// Capture repoRoot and logger for the closure
	root, log := repoRoot, logger
	server = mcp.NewMCPServerLazy(version.Version, func() (*query.Engine, error) {
		return getEngine(root, log)
	}, logger)

	// The reindex tool builds the index in-process with the same plan
	// 'ckb index' uses; the plan builder lives in this package, not in mcp.
	server.SetReindexRunner(func(ctx context.Context, repoRoot string, scope mcp.ReindexScope) (*mcp.ReindexRunInfo, error) {
		return runReindexForMCP(ctx, repoRoot, scope, log)
	})
	// Responses served without an index say so, with the next step checked here.
	server.SetIndexDiagnoser(diagnoseIndexOnPath)

	// Apply preset configuration
	if err := server.SetPreset(mcpPreset); err != nil {
		return fmt.Errorf("failed to set preset: %w", err)
	}

	// Log startup banner with token efficiency info
	preset, exposedCount, totalCount := server.GetPresetStats()
	activeTokens := server.EstimateActiveTokens()
	percentage := (exposedCount * 100) / totalCount

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "CKB MCP Server v%s\n", version.Version)
	fmt.Fprintf(os.Stderr, "  Active tools: %d / %d (%d%%)\n", exposedCount, totalCount, percentage)
	fmt.Fprintf(os.Stderr, "  Estimated context: %s\n", mcp.FormatTokens(activeTokens))
	fmt.Fprintf(os.Stderr, "  Preset: %s\n", preset)
	fmt.Fprintln(os.Stderr)

	// Start watch mode if enabled
	if mcpWatch {
		// Validate and clamp watch interval
		watchInterval := mcpWatchInterval
		if watchInterval < 5*time.Second {
			watchInterval = 5 * time.Second
		}
		if watchInterval > 5*time.Minute {
			watchInterval = 5 * time.Minute
		}

		go runWatchLoop(repoRoot, watchInterval, logger)
		logger.Info("Watch mode enabled", "pollInterval", watchInterval.String())
	} else if hasCkbDir(repoRoot) {
		startFirstIndex(repoRoot, logger)
	}

	if err := server.Start(); err != nil {
		logger.Error("MCP server error", "error", err.Error())
		return err
	}

	return nil
}

// isRepoPath checks if a string looks like a filesystem path vs a registry name
func isRepoPath(s string) bool {
	// Contains path separator
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		return true
	}
	// Starts with . (relative path)
	if strings.HasPrefix(s, ".") {
		return true
	}
	// Exists as a directory
	info, err := os.Stat(s)
	if err == nil && info.IsDir() {
		return true
	}
	return false
}

// maxWatchBackoff caps the exponential backoff between reindex attempts
// after consecutive failures, so a flaky indexer doesn't get retried more
// slowly than useful but also never waits forever.
const maxWatchBackoff = 10 * time.Minute

// maxWatchFailures is how many consecutive reindex failures runWatchLoop
// tolerates before disabling itself for the rest of the process lifetime —
// retrying an identical failure forever wastes CPU and spams logs for no
// benefit; the fix (once made) needs an MCP server restart to pick up
// anyway. A missing indexer is different: see indexerWait.
const maxWatchFailures = 5

// errIndexerUnavailable means runWatchLoop can't build an index for this
// project right now — no supported/single language detected, the language
// has no known SCIP indexer, or the indexer binary isn't installed. A
// missing binary is waited for (see indexerWait): no retries, one LookPath per
// tick until it is on PATH. Anything else disables watch mode for the life of
// the process rather than retrying every tick.
var errIndexerUnavailable = errors.New("no usable SCIP indexer for this project")

// errRepoTooLarge means the project exceeds scipLargeRepoThreshold. A watch
// tick must never kick off a 30–90 min SCIP build inside the MCP server's
// background goroutine — that path requires an explicit 'ckb index --scip'.
var errRepoTooLarge = errors.New("repo exceeds automatic SCIP indexing threshold")

// errIndexLocked means another process holds the .ckb index lock: someone
// else is indexing this repo right now, so there is nothing to do but wait.
var errIndexLocked = errors.New("index lock held by another process")

// watchState tracks consecutive-failure backoff for runWatchLoop so a
// persistently broken indexer doesn't retry an identical failure every
// tick forever.
type watchState struct {
	consecutiveFailures int
	lastAttempt         time.Time
	disabled            bool
	// indexerWait holds builds back until a missing indexer is on PATH.
	indexerWait indexerWait
}

// watchBackoff returns how long to wait before the next reindex attempt
// after consecutiveFailures in a row, doubling from base each time and
// capping at maxWatchBackoff. Pure and clock-free so it's unit-testable
// without a real ticker.
func watchBackoff(base time.Duration, consecutiveFailures int) time.Duration {
	if consecutiveFailures <= 0 {
		return 0
	}
	d := base
	for i := 1; i < consecutiveFailures; i++ {
		if d >= maxWatchBackoff {
			return maxWatchBackoff
		}
		d *= 2
	}
	if d > maxWatchBackoff {
		d = maxWatchBackoff
	}
	return d
}

// runWatchLoop periodically checks index freshness and reindexes if stale —
// including building an index for the first time when none exists yet
// (e.g. 'ckb setup' wrote the MCP config but left indexing for watch mode,
// which is now the non-blocking default). It backs off exponentially on
// repeated failures and disables itself entirely once the indexer is
// confirmed missing or failures cross maxWatchFailures, logging each
// transition exactly once rather than spamming every tick.
//
// The first check runs immediately, before the ticker's first tick — with
// setup no longer indexing in the foreground by default, a fresh project
// has no SCIP index at all until watch mode notices, and waiting a full
// poll interval (up to 5 minutes) to notice would be a regression from the
// old blocking behavior, not an improvement on it.
func runWatchLoop(repoRoot string, interval time.Duration, logger *slog.Logger) {
	ckbDir := filepath.Join(repoRoot, ".ckb")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	state := &watchState{}
	permanentLogged := false

	watchTick(repoRoot, ckbDir, interval, state, &permanentLogged, logger)

	for range ticker.C {
		watchTick(repoRoot, ckbDir, interval, state, &permanentLogged, logger)
	}
}

// watchTick runs one staleness-check-and-maybe-reindex cycle. Extracted out
// of runWatchLoop so the same logic can run once immediately on start and
// again on every ticker.C fire, without duplicating it.
func watchTick(repoRoot, ckbDir string, interval time.Duration, state *watchState, permanentLogged *bool, logger *slog.Logger) {
	if state.disabled || !state.indexerWait.ready() {
		return
	}

	stale, trigger, triggerInfo, reason := watchCheckStale(repoRoot, ckbDir)
	if !stale {
		return
	}

	if state.consecutiveFailures > 0 {
		if wait := watchBackoff(interval, state.consecutiveFailures); time.Since(state.lastAttempt) < wait {
			return
		}
	}

	state.lastAttempt = time.Now()
	logger.Info("Index stale, triggering reindex", "trigger", string(trigger), "reason", reason)

	_, err := triggerReindex(context.Background(), repoRoot, ckbDir, trigger, triggerInfo, logger)
	switch {
	case errors.Is(err, errIndexLocked):
		// Another process is indexing; its result shows up on the next tick.
		return

	case err == nil:
		state.consecutiveFailures = 0
		excludeBuiltIndex(repoRoot, logger)
		// The engine read its SCIP index when it was built; without this the
		// session keeps the old one (or none) until the server restarts.
		if eng := loadedEngine(); eng != nil {
			if reloadErr := eng.ReloadSCIP(context.Background()); reloadErr != nil {
				logger.Warn("Reindex finished but reloading it into the server failed", "error", reloadErr.Error())
			}
		}

	case errors.Is(err, errIndexerUnavailable) && state.indexerWait.await(missingIndexers(repoRoot)):
		// Installing the indexer is the fix, and it needs no restart: the next
		// tick builds once one of them is on PATH.
		logger.Warn("No SCIP indexer installed for this project; building once one is on PATH",
			"indexers", strings.Join(state.indexerWait.commands, ", "))

	case errors.Is(err, errIndexerUnavailable), errors.Is(err, errRepoTooLarge):
		if !*permanentLogged {
			logger.Warn("Watch mode: disabling auto-reindex for this project",
				"reason", err.Error(),
				"hint", "install the indexer (or run 'ckb index --scip' for large repos), then run 'ckb index' or restart the MCP server",
			)
			*permanentLogged = true
		}
		state.disabled = true

	default:
		state.consecutiveFailures++
		logger.Error("Reindex failed", "error", err.Error(), "consecutiveFailures", state.consecutiveFailures)
		if state.consecutiveFailures >= maxWatchFailures {
			logger.Warn("Watch mode: reindex failed repeatedly — disabling auto-reindex until the MCP server restarts",
				"failures", state.consecutiveFailures)
			state.disabled = true
		}
	}
}

// runReindexForMCP is the ReindexRunner behind the MCP reindex tool: the
// 'ckb index' code path without stdout (stdout is the JSON-RPC transport).
// Incremental first where the language supports it (unless scope is full),
// then the full build via triggerReindex — the same plan builder, lock and
// metadata handling the watch loop uses. Sentinel errors the tool should
// turn into guidance rather than a failure are wrapped accordingly.
func runReindexForMCP(ctx context.Context, repoRoot string, scope mcp.ReindexScope, logger *slog.Logger) (*mcp.ReindexRunInfo, error) {
	ckbDir := filepath.Join(repoRoot, ".ckb")

	if scope != mcp.ReindexFull {
		// Incremental reruns one indexer over the whole index: one language only
		// (tryIncrementalIndexTo also refuses an index that holds several).
		if targets, ok := indexedTargets(repoRoot); ok && len(targets) == 1 && project.SupportsIncrementalIndexing(targets[0].Lang) {
			if tryIncrementalIndexTo(repoRoot, ckbDir, targets[0].Lang, io.Discard) {
				excludeBuiltIndex(repoRoot, logger)
				return &mcp.ReindexRunInfo{Mode: "incremental"}, nil
			}
			logger.Debug("reindex: incremental not possible, running full build")
		}
	}

	note, err := triggerReindex(ctx, repoRoot, ckbDir, index.TriggerManual, "mcp reindex", logger)
	switch {
	case err == nil:
		excludeBuiltIndex(repoRoot, logger)
		return &mcp.ReindexRunInfo{Mode: "full", Note: note}, nil
	case errors.Is(err, errIndexerUnavailable), errors.Is(err, errRepoTooLarge):
		return nil, fmt.Errorf("%w: %v", mcp.ErrReindexNotPossible, err)
	case errors.Is(err, errIndexLocked):
		return nil, mcp.ErrReindexLocked
	default:
		return nil, err
	}
}

// excludeBuiltIndex keeps an index the server built (watch, first index, the
// reindex tool) out of 'git status', as 'ckb index' does, for repos that were
// initialized before init wrote .git/info/exclude. Logs instead of printing:
// stdout is the JSON-RPC transport.
func excludeBuiltIndex(repoRoot string, logger *slog.Logger) {
	if rel := excludeIndexFromGit(repoRoot, resolveIndexPath(repoRoot)); rel != "" {
		logger.Info("Kept .ckb/ and the index out of 'git status' through .git/info/exclude", "index", rel)
	}
}

// watchCheckStale reports whether the index needs a (re)build: either
// there's no metadata yet (never indexed — including a fresh 'ckb setup'
// that skipped indexing), or an existing index has gone stale.
func watchCheckStale(repoRoot, ckbDir string) (stale bool, trigger index.RefreshTrigger, triggerInfo, reason string) {
	meta, err := index.LoadMeta(ckbDir)
	if err != nil || meta == nil {
		return true, index.TriggerStale, "", "no index yet"
	}

	freshness := meta.CheckFreshness(repoRoot)
	if freshness.Fresh {
		return false, "", "", ""
	}

	trigger = index.TriggerStale
	triggerInfo = freshness.Reason
	if freshness.CommitsBehind > 0 {
		trigger = index.TriggerHEAD
		if meta.CommitHash != "" && freshness.CurrentCommit != "" {
			triggerInfo = fmt.Sprintf("%d commit(s) behind", freshness.CommitsBehind)
		}
	}
	return true, trigger, triggerInfo, freshness.Reason
}

// triggerReindex runs the SCIP indexers and updates metadata. It never
// writes to stdout (unlike the CLI's performIndex) since this runs inside
// 'ckb mcp', whose stdout is the JSON-RPC transport — all status goes
// through logger (file + stderr).
//
// The languages come from indexedTargets and the plans from planLanguages —
// the same plan builder `ckb index` uses via performIndex — so C++'s
// --compdb-path flag, Ruby's bundle-aware prefix, PHP's prerequisite check,
// and a custom scip.indexPath's --output flag are never missed here the way
// a hand-rolled `indexer.Command` would miss them. Every language with an
// installed indexer goes into the one index; the rest are recorded in the
// index metadata as skipped, and the returned note names them.
func triggerReindex(ctx context.Context, repoRoot, ckbDir string, trigger index.RefreshTrigger, triggerInfo string, logger *slog.Logger) (string, error) {
	targets, ok := indexedTargets(repoRoot)
	if !ok {
		return "", errIndexerUnavailable
	}

	// Never kick off an hours-long SCIP build from a background watch tick;
	// that requires the explicit opt-in 'ckb index --scip'.
	indexPath := resolveIndexPath(repoRoot)
	lp := planLanguages(repoRoot, targets, "", indexPath, false)
	for _, w := range lp.Warnings {
		logger.Warn("Index plan warning", "warning", w)
	}
	if len(lp.Plans) == 0 {
		logger.Warn("Watch mode: cannot build index plan", "reason", skippedNote(lp.Skipped))
		if lp.LargeOnly {
			return "", errRepoTooLarge
		}
		for _, res := range lp.Failures {
			if res.Outcome == indexOutcomeError {
				return "", fmt.Errorf("building index plan: %s", res.Message)
			}
		}
		return "", errIndexerUnavailable
	}
	if len(lp.Skipped) > 0 {
		logger.Warn("Indexing without some languages", "skipped", skippedNote(lp.Skipped))
	}

	// Acquire lock
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		logger.Debug("Skipping reindex, locked by another process")
		return "", errIndexLocked
	}
	defer lock.Release()

	start := time.Now()
	results, runErr := indexplan.RunAll(repoRoot, lp.Plans, func(plan *indexPlan) error {
		parts := strings.Fields(plan.Command)
		if len(parts) == 0 {
			return errIndexerUnavailable
		}
		cmd := exec.CommandContext(ctx, parts[0], parts[1:]...) // #nosec G204 //nolint:gosec // command from trusted indexer config
		cmd.Dir = plan.IndexerDir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			logger.Error("Indexer failed",
				"language", string(plan.Lang),
				"error", err.Error(),
				"stderr", stderr.String(),
			)
			return err
		}
		return nil
	})
	duration := time.Since(start)
	for _, r := range results {
		if r.Err != nil {
			logger.Error("Reindex failed", "language", string(r.Plan.Lang), "error", r.Err.Error())
		}
	}
	// No language made it into an index: nothing must be recorded as fresh.
	if runErr != nil {
		return "", runErr
	}

	// Update metadata with refresh trigger info, and persist the project
	// config so 'ckb index' and later ticks see the languages, same as the
	// CLI's performIndex does.
	meta, saveErrs := recordIndexBuild(repoRoot, ckbDir, lp, results, duration, false, &index.LastRefresh{
		At:          time.Now(),
		Trigger:     trigger,
		TriggerInfo: triggerInfo,
		DurationMs:  duration.Milliseconds(),
	})
	for _, e := range saveErrs {
		logger.Warn("Reindex bookkeeping", "error", e.Error())
	}
	if meta == nil {
		return "", fmt.Errorf("no language indexed")
	}

	// Populate incremental tracking tables so subsequent incremental updates work
	if tracksIncrementally(meta) {
		populateIncrementalTracking(repoRoot, project.Language(meta.Languages[0].Language), io.Discard) // stdout is the JSON-RPC transport
	}

	logger.Info("Reindex complete",
		"trigger", string(trigger),
		"duration", duration.String(),
		"files", meta.FileCount,
		"languages", meta.Indexer,
	)

	if len(meta.Skipped) > 0 {
		return "Not indexed, these stay on text search: " + skippedNote(meta.Skipped) + ".", nil
	}
	return "", nil
}

// listPresets prints available presets with tool counts and token estimates
func listPresets() error {
	// Create a minimal logger for server initialization (silent)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Create server to get tool definitions
	server := mcp.NewMCPServer(version.Version, nil, logger)
	allTools := server.GetToolDefinitions()
	presets := mcp.GetAllPresetInfo(allTools)

	fmt.Println()
	fmt.Println("Available presets:")
	fmt.Println()

	// Print table header
	fmt.Printf("  %-12s %6s %14s  %s\n", "PRESET", "TOOLS", "TOKENS", "DESCRIPTION")
	fmt.Printf("  %-12s %6s %14s  %s\n", "------", "-----", "------", "-----------")

	for _, p := range presets {
		suffix := ""
		if p.IsDefault {
			suffix = " (default)"
		}
		fmt.Printf("  %-12s %6d %14s  %s%s\n",
			p.Name,
			p.ToolCount,
			mcp.FormatTokens(p.TokenCount),
			p.Description,
			suffix,
		)
	}

	fmt.Println()
	fmt.Printf("Use: ckb mcp --preset=<name>\n")
	fmt.Println()

	return nil
}
