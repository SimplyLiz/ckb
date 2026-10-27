package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/mcp"
)

func TestRunReindexForMCP_NoLanguage_IsNotPossible(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := runReindexForMCP(context.Background(), dir, mcp.ReindexAuto, newLogger("human"))
	if !errors.Is(err, mcp.ErrReindexNotPossible) {
		t.Fatalf("err = %v, want ErrReindexNotPossible", err)
	}
}

func TestRunReindexForMCP_LockHeld_IsLocked(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFakeIndexer(t, "scip-go", `: > "$(pwd)/index.scip"`)

	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Release()

	_, err = runReindexForMCP(context.Background(), dir, mcp.ReindexFull, newLogger("human"))
	if !errors.Is(err, mcp.ErrReindexLocked) {
		t.Fatalf("err = %v, want ErrReindexLocked", err)
	}
}

func TestRunReindexForMCP_FullBuild_WritesIndexAndMeta(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Writes an empty (valid, trivial) SCIP file wherever --output points,
	// or index.scip in cwd when there is no --output.
	writeFakeIndexer(t, "scip-go", `
out="index.scip"
prev=""
for arg in "$@"; do
  if [ "$prev" = "--output" ]; then out="$arg"; fi
  prev="$arg"
done
: > "$out"
`)

	info, err := runReindexForMCP(context.Background(), dir, mcp.ReindexFull, newLogger("human"))
	if err != nil {
		t.Fatalf("runReindexForMCP: %v", err)
	}
	if info.Mode != "full" {
		t.Errorf("mode = %q, want full", info.Mode)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.scip")); err != nil {
		t.Fatalf("index.scip not written: %v", err)
	}
	meta, err := index.LoadMeta(ckbDir)
	if err != nil || meta == nil {
		t.Fatalf("metadata not written: %v", err)
	}
	if meta.LastRefresh == nil || meta.LastRefresh.Trigger != index.TriggerManual {
		t.Errorf("metadata trigger = %+v, want manual", meta.LastRefresh)
	}
}

// A context that is already cancelled must stop the indexer instead of
// letting it run to completion.
func TestTriggerReindex_HonoursContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ckbDir := filepath.Join(dir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFakeIndexer(t, "scip-go", `sleep 5; : > index.scip`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := triggerReindex(ctx, dir, ckbDir, index.TriggerManual, "", newLogger("human"))
	if err == nil {
		t.Fatal("indexer ran to completion despite cancelled context")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr == nil {
		t.Fatal("index.scip written despite cancelled context")
	}
}
