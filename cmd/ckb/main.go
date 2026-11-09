package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/SimplyLiz/CodeMCP/internal/update"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Update check with deferred notification pattern:
	// 1. Show cached notification immediately (non-blocking)
	// 2. Start background refresh for next run
	// Skip for mcp/serve commands to avoid breaking protocol
	var refreshWg sync.WaitGroup
	if !isMCPCommand() {
		// Before the refresh below: that creates the file the notice reads as
		// the mark of an earlier install.
		update.ShowPostUpdateNotice(os.Stderr, isTerminal(os.Stderr))

		checker := update.NewChecker()
		// Show cached update notification (instant, no HTTP)
		if info := checker.CheckCached(); info != nil && isTerminal(os.Stderr) {
			_, _ = os.Stderr.WriteString(info.FormatUpdateMessage())
		}
		// Refresh cache in background for next run
		refreshWg.Add(1)
		go func() {
			defer refreshWg.Done()
			checker.RefreshCache(context.Background())
		}()
	}

	if err := rootCmd.Execute(); err != nil {
		logger.Error("Command execution failed", "error", err.Error())
		os.Exit(1)
	}

	// Wait briefly for cache refresh to complete (max 500ms)
	// This ensures the cache gets populated for next run
	waitWithTimeout(&refreshWg, 500*time.Millisecond)
}

// waitWithTimeout waits for a WaitGroup with a maximum timeout
func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Completed before timeout
	case <-time.After(timeout):
		// Timeout reached, exit anyway
	}
}

// isTerminal reports whether f is a terminal. The update box and notice are for
// a person: agents and scripts capture stderr together with the result and
// otherwise have to filter them out of every call. A character-device check is
// not enough, /dev/null is one too.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) // #nosec G115 -- a file descriptor fits in int
}

// isMCPCommand checks if the current command is 'mcp' or 'serve'. These must
// not show update notifications as it would break protocols. It resolves the
// command through cobra so a flag in front (`ckb -v mcp`) cannot hide it.
func isMCPCommand() bool {
	return isMCPCommandArgs(os.Args[1:])
}

func isMCPCommandArgs(args []string) bool {
	cmd, _, err := rootCmd.Find(args)
	if err != nil || cmd == nil {
		return false
	}
	return cmd.Name() == "mcp" || cmd.Name() == "serve"
}
