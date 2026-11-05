package main

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

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

// isMCPCommand checks if the current command is 'mcp' or 'serve'
// These commands should not show update notifications as it would break protocols
// isTerminal reports whether f is a character device. The update box is for a
// person at a terminal: agents and scripts capture stderr together with the
// result and otherwise have to filter the box out of every call.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func isMCPCommand() bool {
	if len(os.Args) < 2 {
		return false
	}
	cmd := os.Args[1]
	return cmd == "mcp" || cmd == "serve"
}
