package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// --- watchBackoff: pure backoff policy, no clock/ticker needed ---

func TestWatchBackoff_NoFailuresIsZero(t *testing.T) {
	if got := watchBackoff(10*time.Second, 0); got != 0 {
		t.Errorf("watchBackoff(_, 0) = %v, want 0", got)
	}
	if got := watchBackoff(10*time.Second, -1); got != 0 {
		t.Errorf("watchBackoff(_, -1) = %v, want 0", got)
	}
}

func TestWatchBackoff_DoublesEachFailure(t *testing.T) {
	base := 10 * time.Second
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 80 * time.Second},
	}
	for _, tc := range cases {
		if got := watchBackoff(base, tc.failures); got != tc.want {
			t.Errorf("watchBackoff(%v, %d) = %v, want %v", base, tc.failures, got, tc.want)
		}
	}
}

func TestWatchBackoff_CapsAtMax(t *testing.T) {
	base := 10 * time.Second
	// After enough failures, backoff must never exceed the cap, and must
	// actually reach it (not asymptotically approach it forever).
	got := watchBackoff(base, 20)
	if got != maxWatchBackoff {
		t.Errorf("watchBackoff(%v, 20) = %v, want cap %v", base, got, maxWatchBackoff)
	}

	got2 := watchBackoff(base, 1000)
	if got2 != maxWatchBackoff {
		t.Errorf("watchBackoff(%v, 1000) = %v, want cap %v", base, got2, maxWatchBackoff)
	}
}

func TestWatchBackoff_LargeBaseDoesNotOverflow(t *testing.T) {
	// A base already at/above the cap must clamp immediately rather than
	// overflowing time.Duration via repeated doubling.
	got := watchBackoff(1*time.Hour, 5)
	if got != maxWatchBackoff {
		t.Errorf("watchBackoff(1h, 5) = %v, want cap %v", got, maxWatchBackoff)
	}
}

// --- watchCheckStale ---

func TestWatchCheckStale_NoMetadata_IsStale(t *testing.T) {
	dir := t.TempDir()
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb: %v", err)
	}

	stale, _, _, reason := watchCheckStale(dir, ckbDir)
	if !stale {
		t.Error("expected stale=true when no index metadata exists yet")
	}
	if reason != "no index yet" {
		t.Errorf("reason = %q, want %q", reason, "no index yet")
	}
}

// --- watchDetectLanguage ---

func TestWatchDetectLanguage_UsesSavedProjectConfig(t *testing.T) {
	dir := t.TempDir()
	if err := project.SaveConfig(dir, &project.ProjectConfig{Language: project.LangRust}); err != nil {
		t.Fatalf("failed to save project config: %v", err)
	}

	lang, ok := watchDetectLanguage(dir)
	if !ok {
		t.Fatal("expected ok=true when project.json exists")
	}
	if lang != project.LangRust {
		t.Errorf("lang = %v, want %v", lang, project.LangRust)
	}
}

func TestWatchDetectLanguage_FallsBackToAutoDetect(t *testing.T) {
	dir := t.TempDir()
	// No project.json yet (never indexed) — must fall back to manifest
	// detection so a first-ever watch tick can still build an index.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	lang, ok := watchDetectLanguage(dir)
	if !ok {
		t.Fatal("expected ok=true via auto-detect fallback")
	}
	if lang != project.LangGo {
		t.Errorf("lang = %v, want %v", lang, project.LangGo)
	}
}

func TestWatchDetectLanguage_NoLanguage_NotOK(t *testing.T) {
	dir := t.TempDir() // empty, no manifest of any kind

	if _, ok := watchDetectLanguage(dir); ok {
		t.Error("expected ok=false for a directory with no detectable language")
	}
}

// --- triggerReindex: sentinel error paths (no real indexer invoked) ---

func TestTriggerReindex_NoLanguage_ReturnsIndexerUnavailable(t *testing.T) {
	dir := t.TempDir()
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb: %v", err)
	}

	logger := newLogger("human")
	err := triggerReindex(dir, ckbDir, index.TriggerStale, "", logger)
	if !errors.Is(err, errIndexerUnavailable) {
		t.Errorf("err = %v, want errIndexerUnavailable", err)
	}
}

// --- triggerReindex: shared buildIndexPlan wiring, exercised with a fake indexer ---

// writeFakeIndexer writes an executable shell script named binName into a
// fresh temp directory and prepends that directory to PATH for the duration
// of the test, so exec.LookPath/exec.Command resolve it instead of any real
// SCIP indexer. script is the script body (no shebang needed).
func writeFakeIndexer(t *testing.T, binName, script string) {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, binName)
	content := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(binPath, []byte(content), 0755); err != nil { //nolint:gosec // test fixture, needs +x
		t.Fatalf("failed to write fake indexer %s: %v", binName, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestTriggerReindex_CustomOutputPath_UsesConfiguredPath verifies triggerReindex
// builds its command through buildIndexPlan the same way performIndex does:
// a configured scip.indexPath must add --output pointing at that path, not
// the default index.scip. Before the fix, triggerReindex ran indexer.Command
// verbatim and never looked at scip.indexPath at all, so a custom output
// path was silently never produced (and never checked for).
func TestTriggerReindex_CustomOutputPath_UsesConfiguredPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb: %v", err)
	}

	customCfg := `{
		"version": 5,
		"repoRoot": ".",
		"backends": {
			"scip": {"enabled": true, "indexPath": "custom/index.scip"},
			"lsp": {"enabled": false},
			"git": {"enabled": true}
		},
		"budget": {"maxModules": 20, "maxSymbolsPerModule": 10}
	}`
	if err := os.WriteFile(filepath.Join(ckbDir, "config.json"), []byte(customCfg), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}

	// Fake scip-go: honor --output by writing the index file there. If
	// triggerReindex ever regresses to running the indexer without --output,
	// this script writes nothing and the test fails on the missing file.
	writeFakeIndexer(t, "scip-go", `
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then
    out="$arg"
  fi
  prev="$arg"
done
if [ -z "$out" ]; then
  exit 1
fi
mkdir -p "$(dirname "$out")"
# Write a zero-byte file: an empty message is valid (trivial) SCIP protobuf,
# so downstream incremental-tracking population doesn't choke on garbage
# bytes — this test only cares that triggerReindex wrote to the *right*
# path, not about real SCIP content.
: > "$out"
`)

	logger := newLogger("human")
	err := triggerReindex(dir, ckbDir, index.TriggerStale, "", logger)
	if err != nil {
		t.Fatalf("triggerReindex() error = %v, want nil", err)
	}

	wantPath := filepath.Join(dir, "custom", "index.scip")
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Errorf("expected index file at %s, stat error: %v", wantPath, statErr)
	}

	meta, metaErr := index.LoadMeta(ckbDir)
	if metaErr != nil || meta == nil {
		t.Fatalf("expected index metadata to be saved, LoadMeta() = %v, %v", meta, metaErr)
	}
}

// TestTriggerReindex_MissingOutputFile_ReturnsError verifies that when the
// indexer exits 0 but never produces the index file buildIndexPlan targeted,
// triggerReindex reports failure instead of writing metadata that claims
// success. Before the fix, triggerReindex jumped straight from a successful
// exec.Command to saving metadata, with no check that the SCIP index
// actually exists.
func TestTriggerReindex_MissingOutputFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb: %v", err)
	}

	// Fake scip-go that "succeeds" (exit 0) without producing index.scip —
	// simulates an indexer that silently no-ops.
	writeFakeIndexer(t, "scip-go", `exit 0`)

	logger := newLogger("human")
	err := triggerReindex(dir, ckbDir, index.TriggerStale, "", logger)
	if err == nil {
		t.Fatal("triggerReindex() error = nil, want an error for missing output file")
	}

	if meta, _ := index.LoadMeta(ckbDir); meta != nil {
		t.Errorf("expected no index metadata to be saved on failure, got %+v", meta)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr == nil {
		t.Error("index.scip unexpectedly exists — fake indexer should not have created it")
	}
}

// --- runWatchLoop: first check must not wait for the first tick ---

// TestRunWatchLoop_FirstCheckRunsImmediately covers the P2 finding: with
// setup no longer indexing in the foreground by default, a fresh project
// has no SCIP index until watch mode notices — waiting a full poll interval
// (up to 5 minutes) before the first check would leave the project without
// code intelligence for that whole window even when its indexer is ready
// and installed. The interval passed here (1 hour) proves any reindex we
// observe can't be coming from ticker.C — only the pre-loop immediate check
// could have produced it this fast.
func TestRunWatchLoop_FirstCheckRunsImmediately(t *testing.T) {
	// Manual temp dir (not t.TempDir()) — runWatchLoop's immediate check
	// spawns a real indexer subprocess and opens a sqlite db for incremental
	// tracking, and the goroutine outlives this test function's return
	// (there's no cancellation for a watch loop, same as in production).
	// t.TempDir()'s cleanup runs synchronously at test-end and can race that
	// still-finishing background work; os.RemoveAll ignoring its error
	// avoids failing the test over a cleanup timing race unrelated to what
	// it's actually verifying.
	dir, err := os.MkdirTemp("", "ckb-watchloop-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("failed to create .ckb: %v", err)
	}

	writeFakeIndexer(t, "scip-go", `
out=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then
    out="$arg"
  fi
  prev="$arg"
done
if [ -z "$out" ]; then
  out="index.scip"
fi
mkdir -p "$(dirname "$out")"
: > "$out"
`)

	logger := newLogger("human")
	go runWatchLoop(dir, 1*time.Hour, logger)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if meta, _ := index.LoadMeta(ckbDir); meta != nil {
			return // success — the immediate pre-loop check ran and indexed
		}
		if time.Now().After(deadline) {
			t.Fatal("runWatchLoop did not perform its first check immediately on start (no metadata after 3s, with a 1h poll interval)")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
