package lsp

import (
	"io"
	"log/slog"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/config"
)

func TestConfiguredServersReportsRunningAndPath(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Backends.Lsp.Servers = map[string]config.LspServerCfg{
		"go":         {Command: "definitely-not-a-binary-xyz"},
		"typescript": {Command: "sh"}, // present on PATH in every supported CI environment
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	s := NewLspSupervisor(cfg, logger)
	defer func() { _ = s.Shutdown() }()

	// Simulate a held process for "typescript" without spawning one.
	s.mu.Lock()
	s.processes["typescript"] = NewLspProcess("typescript", cfg.RepoRoot)
	s.mu.Unlock()

	got := s.ConfiguredServers()
	if len(got) != 2 {
		t.Fatalf("expected 2 servers, got %d: %+v", len(got), got)
	}

	byLang := map[string]ServerAvailability{}
	for _, sa := range got {
		byLang[sa.Language] = sa
	}

	gs := byLang["go"]
	if gs.OnPath || gs.Running {
		t.Errorf("go: OnPath=%v Running=%v, want false/false", gs.OnPath, gs.Running)
	}
	if gs.Command != "definitely-not-a-binary-xyz" {
		t.Errorf("go: Command=%q", gs.Command)
	}

	ts := byLang["typescript"]
	if !ts.OnPath || !ts.Running {
		t.Errorf("typescript: OnPath=%v Running=%v, want true/true", ts.OnPath, ts.Running)
	}

	// Drop the fake process before Shutdown so it does not touch a nil cmd.
	s.mu.Lock()
	delete(s.processes, "typescript")
	s.mu.Unlock()
}
