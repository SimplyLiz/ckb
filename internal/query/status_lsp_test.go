package query

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/lsp"
	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// newLspStatusEngine builds an Engine with only the fields getBackendStatuses reads.
func newLspStatusEngine(t *testing.T, servers map[string]config.LspServerCfg) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.RepoRoot = t.TempDir()
	cfg.Backends.Lsp.Servers = servers

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sup := lsp.NewLspSupervisor(cfg, logger)
	t.Cleanup(func() { _ = sup.Shutdown() })

	return &Engine{
		config:        cfg,
		repoRoot:      cfg.RepoRoot,
		logger:        logger,
		lspSupervisor: sup,
	}
}

// fakeLspBinary writes an executable named name into a fresh dir and prepends
// that dir to PATH for the duration of the test.
func fakeLspBinary(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func findLspStatus(t *testing.T, e *Engine) BackendStatus {
	t.Helper()
	for _, bs := range e.getBackendStatuses(t.Context()) {
		if bs.Id == "lsp" {
			return bs
		}
	}
	t.Fatal("no lsp backend status returned")
	return BackendStatus{}
}

// A configured server whose binary is on PATH but has not been started yet
// must report available and healthy, not "No servers running".
func TestLspStatusIdleLazyServerIsAvailable(t *testing.T) {
	fakeLspBinary(t, "fake-tsserver-ok")
	e := newLspStatusEngine(t, map[string]config.LspServerCfg{
		"typescript": {Command: "fake-tsserver-ok", Args: []string{"--stdio"}},
	})

	bs := findLspStatus(t, e)
	if !bs.Available || !bs.Healthy {
		t.Fatalf("expected available+healthy, got available=%v healthy=%v details=%q",
			bs.Available, bs.Healthy, bs.Details)
	}
	if !strings.Contains(bs.Details, "typescript: configured, starts on demand") {
		t.Errorf("details = %q, want it to contain %q", bs.Details, "typescript: configured, starts on demand")
	}
}

// A configured server whose binary is missing must be unavailable and name the binary.
func TestLspStatusMissingBinaryIsUnavailable(t *testing.T) {
	e := newLspStatusEngine(t, map[string]config.LspServerCfg{
		"typescript": {Command: "definitely-not-a-binary-xyz"},
	})

	bs := findLspStatus(t, e)
	if bs.Available || bs.Healthy {
		t.Fatalf("expected unavailable, got available=%v healthy=%v", bs.Available, bs.Healthy)
	}
	if !strings.Contains(bs.Details, "definitely-not-a-binary-xyz") {
		t.Errorf("details = %q, want it to name the missing binary", bs.Details)
	}
}

// Mixed case: one server on PATH makes the backend available; the missing one is still named.
func TestLspStatusMixedServers(t *testing.T) {
	fakeLspBinary(t, "fake-tsserver-mixed")
	e := newLspStatusEngine(t, map[string]config.LspServerCfg{
		"typescript": {Command: "fake-tsserver-mixed"},
		"python":     {Command: "definitely-not-a-binary-xyz"},
	})

	bs := findLspStatus(t, e)
	if !bs.Available || !bs.Healthy {
		t.Fatalf("expected available+healthy, got available=%v healthy=%v", bs.Available, bs.Healthy)
	}
	for _, want := range []string{
		"typescript: configured, starts on demand",
		"python: definitely-not-a-binary-xyz not found on PATH",
	} {
		if !strings.Contains(bs.Details, want) {
			t.Errorf("details = %q, want it to contain %q", bs.Details, want)
		}
	}
}

// No servers configured: unavailable with the "nothing configured" details.
func TestLspStatusNoServersConfigured(t *testing.T) {
	e := newLspStatusEngine(t, map[string]config.LspServerCfg{})

	bs := findLspStatus(t, e)
	if bs.Available || bs.Healthy {
		t.Fatalf("expected unavailable, got available=%v healthy=%v", bs.Available, bs.Healthy)
	}
	if bs.Details != "No LSP servers configured" {
		t.Errorf("details = %q, want %q", bs.Details, "No LSP servers configured")
	}
}
