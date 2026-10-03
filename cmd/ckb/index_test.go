package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPerformIndex_NotInitialized verifies performIndex returns a structured
// error result instead of calling os.Exit when .ckb/ is missing. Before the
// refactor this path called os.Exit(1) directly, which meant `ckb setup`
// could never call the same logic without killing itself.
func TestPerformIndex_NotInitialized(t *testing.T) {
	dir := t.TempDir()

	result := performIndex(dir)

	if result.Outcome != indexOutcomeError {
		t.Errorf("Outcome = %v, want indexOutcomeError", result.Outcome)
	}
}

// TestPerformIndex_NoLanguageDetected verifies performIndex reports a
// non-fatal, structured outcome when no supported manifest is found — this
// is the "no usable index" path `ckb setup` relies on to continue without
// dying. It's deterministic (no dependency on any SCIP indexer being
// installed) because an empty directory never resolves to a language.
func TestPerformIndex_NoLanguageDetected(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatalf("failed to create .ckb dir: %v", err)
	}

	// Reset language-detection-affecting globals in case another test left
	// them set.
	oldLang := indexLang
	indexLang = ""
	defer func() { indexLang = oldLang }()

	result := performIndex(dir)

	if result.Outcome != indexOutcomeNoLanguageDetected {
		t.Errorf("Outcome = %v, want indexOutcomeNoLanguageDetected (message: %s)", result.Outcome, result.Message)
	}
}
