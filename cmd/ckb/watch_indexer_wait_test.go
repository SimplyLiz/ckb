package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// pathWithGitOnly is a PATH holding git and the shell but no SCIP indexer.
func pathWithGitOnly(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"git", "sh"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not on PATH", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// A first index or watch tick that finds no indexer used to switch itself off
// until the server restarted. It now waits, costs only a PATH lookup per tick
// while waiting, and builds on the first tick after the indexer appears.
func TestWatchTick_WaitsForMissingIndexerThenRetries(t *testing.T) {
	repo := gitInitTemp(t)
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ckbDir := filepath.Join(repo, ".ckb")
	if err := os.MkdirAll(ckbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitOnly := pathWithGitOnly(t)
	t.Setenv("PATH", gitOnly)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	state := &watchState{}
	logged := false
	watchTick(repo, ckbDir, time.Second, state, &logged, logger)
	if state.disabled {
		t.Fatal("missing indexer disabled the watch for good")
	}
	if got := state.indexerWait.commands; len(got) != 1 || got[0] != "scip-go" {
		t.Fatalf("awaited indexers = %v, want [scip-go]", got)
	}

	first := state.lastAttempt
	watchTick(repo, ckbDir, time.Second, state, &logged, logger)
	if !state.lastAttempt.Equal(first) {
		t.Fatal("tick tried to build while the indexer was still missing")
	}

	// The indexer appears (here one that fails, so the attempt is visible).
	fake := t.TempDir()
	if err := os.WriteFile(filepath.Join(fake, "scip-go"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+gitOnly)
	watchTick(repo, ckbDir, time.Second, state, &logged, logger)
	if state.lastAttempt.Equal(first) {
		t.Fatal("tick did not build after the indexer appeared")
	}
	if state.indexerWait.waiting() {
		t.Error("still waiting after the indexer appeared")
	}
	if state.consecutiveFailures != 1 {
		t.Errorf("consecutiveFailures = %d, want 1 from the fake indexer's exit", state.consecutiveFailures)
	}
}

func TestIndexerWait_NothingMissingGivesUp(t *testing.T) {
	var w indexerWait
	if w.await(nil) || w.waiting() || !w.ready() {
		t.Fatal("an empty wait must not hold builds back")
	}
	w.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	if !w.await([]string{"scip-python"}) || w.ready() {
		t.Fatal("missing indexer reported ready")
	}
	w.lookPath = func(string) (string, error) { return "/usr/local/bin/scip-python", nil }
	if !w.ready() || w.waiting() {
		t.Fatal("indexer on PATH did not end the wait")
	}
}

func TestWithInstallPrereqs(t *testing.T) {
	onPath := map[string]bool{"go": true}
	look := func(name string) (string, error) {
		if onPath[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	cases := []struct{ in, want string }{
		{
			"Indexing Python needs this first: scip-python not installed (install with: npm install -g @sourcegraph/scip-python).",
			"Indexing Python needs this first: scip-python not installed (install with: npm install -g @sourcegraph/scip-python; npm is not installed either, install Node.js first).",
		},
		{ // the tool is there: unchanged
			"scip-go not installed (install with: go install github.com/sourcegraph/scip-go@latest)",
			"scip-go not installed (install with: go install github.com/sourcegraph/scip-go@latest)",
		},
		{ // not a command CKB knows the provider of: unchanged
			"scip-clang not installed (install with: Download from https://github.com/sourcegraph/scip-clang/releases)",
			"scip-clang not installed (install with: Download from https://github.com/sourcegraph/scip-clang/releases)",
		},
		{
			"Rust: rust-analyzer not installed (install with: rustup component add rust-analyzer); Java: scip-java not installed (install with: cs install scip-java)",
			"Rust: rust-analyzer not installed (install with: rustup component add rust-analyzer; rustup is not installed either, install rustup first); " +
				"Java: scip-java not installed (install with: cs install scip-java; cs is not installed either, install Coursier first)",
		},
	}
	for _, c := range cases {
		if got := withInstallPrereqs(c.in, look); got != c.want {
			t.Errorf("withInstallPrereqs(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// The diagnosis names where the language came from, so a manifest that
// shouldn't count shows up as the reason.
func TestLanguageSources(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "pyproject.toml"), []byte("[project]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := languageSources(dir), "(Detected Python from "+filepath.Join("tools", "pyproject.toml")+".)"; got != want {
		t.Errorf("languageSources = %q, want %q", got, want)
	}
	if got := languageSources(t.TempDir()); got != "" {
		t.Errorf("languageSources without a language = %q, want empty", got)
	}
}
