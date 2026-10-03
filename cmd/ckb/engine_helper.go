package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"

	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/slogutil"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

var (
	engineOnce   sync.Once
	sharedEngine *query.Engine
	engineErr    error

	// builtEngine is sharedEngine, published once construction succeeded,
	// for callers that must not trigger construction themselves (see
	// loadedEngine).
	builtEngine atomic.Pointer[query.Engine]
)

// loadedEngine returns the shared engine if something has already built it,
// or nil. Unlike getEngine it never builds one: the watch loop uses it to
// reload a fresh index into a running engine, and an engine nobody has asked
// for yet will read the new index on its own when it is built.
func loadedEngine() *query.Engine {
	return builtEngine.Load()
}

// getEngine returns a shared Query Engine instance.
// The engine is lazily initialized on first use.
func getEngine(repoRoot string, logger *slog.Logger) (*query.Engine, error) {
	engineOnce.Do(func() {
		// Load configuration
		cfg, err := config.LoadConfig(repoRoot)
		if err != nil {
			logger.Warn("Failed to load config, using defaults", "error", err.Error())
			cfg = config.DefaultConfig()
		}

		// Open storage — db is passed to the engine and lives for the process lifetime.
		// Intentionally not deferred: the engine needs the DB for every subsequent call.
		db, err := storage.Open(repoRoot, logger)
		if err != nil {
			engineErr = fmt.Errorf("failed to open database: %w", err)
			return
		}
		// db.Close() is called at process exit (OS reclaims resources)

		// Create engine
		engine, err := query.NewEngine(repoRoot, db, logger, cfg)
		if err != nil {
			engineErr = fmt.Errorf("failed to create engine: %w", err)
			return
		}
		engine.StartBgTasks()

		// Configure tier mode from CLI flag, env var, or config
		tierMode, err := resolveTierMode(cfg)
		if err != nil {
			engineErr = fmt.Errorf("invalid tier configuration: %w", err)
			return
		}
		engine.SetTierMode(tierMode)

		// Validate that the tier requirements can be satisfied
		if validErr := engine.ValidateTierMode(); validErr != nil {
			// Log warning but don't fail - fall back to available tier
			logger.Warn("Requested tier not available", "error", validErr.Error())
		}

		sharedEngine = engine
		builtEngine.Store(engine)
	})

	return sharedEngine, engineErr
}

// mustGetEngine returns the shared Query Engine or exits on error.
func mustGetEngine(repoRoot string, logger *slog.Logger) *query.Engine {
	engine, err := getEngine(repoRoot, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing engine: %v\n", err)
		os.Exit(1)
	}
	return engine
}

// getRepoRoot returns the repository root directory.
// It uses the global repo resolution order:
// 1. CKB_REPO environment variable
// 2. Current directory matches a registered repo
// 3. Default repo from registry
// 4. Falls back to current working directory
func getRepoRoot() (string, error) {
	resolved, err := repos.ResolveActiveRepo("")
	if err != nil {
		// Fall back to CWD if registry can't be loaded
		return os.Getwd()
	}

	if resolved.Entry != nil {
		return resolved.Entry.Path, nil
	}

	// No registered repo found, fall back to CWD
	return os.Getwd()
}

// mustGetRepoRoot returns the repository root or exits on error.
func mustGetRepoRoot() string {
	repoRoot, err := getRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return repoRoot
}

// newContext creates a new context for command execution.
func newContext() context.Context {
	return context.Background()
}

// newLogger creates a logger with the specified format.
// Logs always go to stderr to keep stdout clean for command output.
// Respects global -v/-q flags and CKB_DEBUG env var.
func newLogger(format string) *slog.Logger {
	level := slogutil.LevelFromVerbosity(verbosity, quiet)
	if os.Getenv("CKB_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	// Suppress warnings (stale SCIP, etc.) for all output formats unless
	// verbose mode is explicitly enabled. Warnings on stderr corrupt
	// machine-readable output (JSON, markdown) when stderr is redirected.
	if level < slog.LevelError {
		level = slog.LevelError
	}
	return slogutil.NewLogger(os.Stderr, level)
}
