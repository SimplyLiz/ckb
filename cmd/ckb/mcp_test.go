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
