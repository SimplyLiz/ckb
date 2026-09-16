package main

import (
	"bytes"
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
	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/project"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
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
					fmt.Fprintf(os.Stderr, "Repository: %s (%s) [auto-detected, uninitialized]\n", repoName, repoRoot)
					fmt.Fprintf(os.Stderr, "  ⚠️  Run 'ckb init && ckb repo add %s .' to fully set up\n", repoName)
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
// anyway, same as the missing-indexer case.
const maxWatchFailures = 5

// errIndexerUnavailable means runWatchLoop can't build an index for this
// project right now — no supported/single language detected, the language
// has no known SCIP indexer, or the indexer binary isn't installed. It's
// treated as permanent for the life of the process: watch mode disables
// itself rather than retrying every tick.
var errIndexerUnavailable = errors.New("no usable SCIP indexer for this project")

// errRepoTooLarge means the project exceeds scipLargeRepoThreshold. A watch
// tick must never kick off a 30–90 min SCIP build inside the MCP server's
// background goroutine — that path requires an explicit 'ckb index --scip'.
var errRepoTooLarge = errors.New("repo exceeds automatic SCIP indexing threshold")

// watchState tracks consecutive-failure backoff for runWatchLoop so a
// persistently broken indexer doesn't retry an identical failure every
// tick forever.
type watchState struct {
	consecutiveFailures int
	lastAttempt         time.Time
	disabled            bool
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
// (e.g. 'ckb setup' wrote the MCP config but indexing was skipped,
// interrupted, or the indexer wasn't installed at the time). It backs off
// exponentially on repeated failures and disables itself entirely once the
// indexer is confirmed missing or failures cross maxWatchFailures, logging
// each transition exactly once rather than spamming every tick.
func runWatchLoop(repoRoot string, interval time.Duration, logger *slog.Logger) {
	ckbDir := filepath.Join(repoRoot, ".ckb")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	state := &watchState{}
	permanentLogged := false

	for range ticker.C {
		if state.disabled {
			continue
		}

		stale, trigger, triggerInfo, reason := watchCheckStale(repoRoot, ckbDir)
		if !stale {
			continue
		}

		if state.consecutiveFailures > 0 {
			if wait := watchBackoff(interval, state.consecutiveFailures); time.Since(state.lastAttempt) < wait {
				continue
			}
		}

		state.lastAttempt = time.Now()
		logger.Info("Index stale, triggering reindex", "trigger", string(trigger), "reason", reason)

		err := triggerReindex(repoRoot, ckbDir, trigger, triggerInfo, logger)
		switch {
		case err == nil:
			state.consecutiveFailures = 0

		case errors.Is(err, errIndexerUnavailable), errors.Is(err, errRepoTooLarge):
			if !permanentLogged {
				logger.Warn("Watch mode: disabling auto-reindex for this project",
					"reason", err.Error(),
					"hint", "install the indexer (or run 'ckb index --scip' for large repos), then run 'ckb index' or restart the MCP server",
				)
				permanentLogged = true
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

// watchDetectLanguage resolves the project language to index: the saved
// project config if one exists (written after the first successful index),
// or a fresh auto-detect otherwise — covering the "never indexed yet" case
// that project.json can't answer. Returns ok=false if no single supported
// language can be determined.
func watchDetectLanguage(repoRoot string) (project.Language, bool) {
	if cfg, err := project.LoadConfig(repoRoot); err == nil {
		return cfg.Language, true
	}
	lang, _, allLangs := project.DetectAllLanguages(repoRoot)
	if lang == project.LangUnknown || len(allLangs) > 1 {
		return project.LangUnknown, false
	}
	return lang, true
}

// triggerReindex runs the SCIP indexer and updates metadata. It never
// writes to stdout (unlike the CLI's performIndex) since this runs inside
// 'ckb mcp', whose stdout is the JSON-RPC transport — all status goes
// through logger (file + stderr).
//
// The command it runs comes from buildIndexPlan — the same plan builder
// `ckb index` uses via performIndex — so C++'s --compdb-path flag, Ruby's
// bundle-aware prefix, PHP's prerequisite check, and a custom
// scip.indexPath's --output flag are never missed here the way a
// hand-rolled `indexer.Command` would miss them.
func triggerReindex(repoRoot, ckbDir string, trigger index.RefreshTrigger, triggerInfo string, logger *slog.Logger) error {
	lang, ok := watchDetectLanguage(repoRoot)
	if !ok {
		return errIndexerUnavailable
	}

	// Never kick off an hours-long SCIP build from a background watch tick;
	// that requires the explicit opt-in 'ckb index --scip'.
	if fileCount := countSourceFiles(repoRoot, lang); fileCount >= scipLargeRepoThreshold {
		return errRepoTooLarge
	}

	manifest := project.FindManifestForLanguage(repoRoot, lang)
	indexPath := resolveIndexPath(repoRoot)

	plan, planResult := buildIndexPlan(repoRoot, lang, manifest, "", indexPath)
	if plan == nil {
		switch planResult.Outcome {
		case indexOutcomeIndexerMissing, indexOutcomeIndexerRequirementMissing:
			logger.Warn("Watch mode: cannot build index plan", "reason", planResult.Message)
			return errIndexerUnavailable
		default:
			return fmt.Errorf("building index plan: %s", planResult.Message)
		}
	}
	if planResult.Warning != "" {
		logger.Warn("Index plan warning", "warning", planResult.Warning)
	}

	// Acquire lock
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		// Another process is indexing, skip
		logger.Debug("Skipping reindex, locked by another process")
		return nil
	}
	defer lock.Release()

	// Run indexer
	start := time.Now()
	parts := strings.Fields(plan.Command)
	if len(parts) == 0 {
		return errIndexerUnavailable
	}

	cmd := exec.Command(parts[0], parts[1:]...) // #nosec G204 //nolint:gosec // command from trusted indexer config
	cmd.Dir = plan.IndexerDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		logger.Error("Indexer failed",
			"error", err.Error(),
			"stderr", stderr.String(),
		)
		return err
	}

	duration := time.Since(start)

	// Verify the index file the plan targeted actually exists before ever
	// writing metadata that would claim success — a language whose indexer
	// exits 0 without producing output (or a custom --output path that
	// wasn't honored) must be reported as a failure, not silently recorded
	// as a fresh index.
	if _, statErr := os.Stat(plan.IndexPath); os.IsNotExist(statErr) {
		err := fmt.Errorf("indexer completed but %s was not created", plan.IndexPath)
		logger.Error("Reindex failed", "error", err.Error())
		return err
	}

	// Update metadata with refresh trigger info
	newMeta := &index.IndexMeta{
		CreatedAt:   time.Now(),
		FileCount:   countSourceFiles(repoRoot, lang),
		Duration:    duration.Round(time.Millisecond * 100).String(),
		Indexer:     plan.Indexer.CheckCommand,
		IndexerArgs: parts,
		LastRefresh: &index.LastRefresh{
			At:          time.Now(),
			Trigger:     trigger,
			TriggerInfo: triggerInfo,
			DurationMs:  duration.Milliseconds(),
		},
	}

	if rs, err := repostate.ComputeRepoState(repoRoot); err == nil {
		newMeta.CommitHash = rs.HeadCommit
		newMeta.RepoStateID = rs.RepoStateID
	}

	if err := newMeta.Save(ckbDir); err != nil {
		logger.Error("Failed to save index metadata", "error", err.Error())
	}

	// Persist project config so future ticks (and 'ckb index') don't need to
	// re-detect the language, same as the CLI's performIndex does.
	if saveErr := project.SaveConfig(repoRoot, &project.ProjectConfig{
		Language:     lang,
		Indexer:      plan.Indexer.CheckCommand,
		ManifestPath: plan.Manifest,
		DetectedAt:   time.Now(),
	}); saveErr != nil {
		logger.Warn("Could not save project config", "error", saveErr.Error())
	}

	// Populate incremental tracking tables so subsequent incremental updates work
	if project.SupportsIncrementalIndexing(lang) {
		populateIncrementalTracking(repoRoot, lang)
	}

	logger.Info("Reindex complete",
		"trigger", string(trigger),
		"duration", duration.String(),
		"files", newMeta.FileCount,
	)

	return nil
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
