package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/SimplyLiz/CodeMCP/internal/project"
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

func writeTestSCIP(t *testing.T, path string, docPath string) {
	t.Helper()
	data, err := proto.Marshal(&scippb.Index{
		Metadata:  &scippb.Metadata{ProjectRoot: "file:///indexer"},
		Documents: []*scippb.Document{{RelativePath: docPath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestSCIPDocs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- test temp path
	if err != nil {
		t.Fatal(err)
	}
	var idx scippb.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	docs := make([]string, 0, len(idx.Documents))
	for _, d := range idx.Documents {
		docs = append(docs, d.RelativePath)
	}
	return docs
}

// A nested manifest: the indexer writes engine/index.scip with paths relative
// to engine/. Reindexing must replace the previous run's index, not keep it.
func TestFinalizeIndexOutput_NestedManifest(t *testing.T) {
	repo := t.TempDir()
	engine := filepath.Join(repo, "engine")
	if err := os.Mkdir(engine, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &indexPlan{IndexerDir: engine, IndexPath: filepath.Join(repo, "index.scip")}

	writeTestSCIP(t, filepath.Join(engine, "index.scip"), "src/first.rs")
	if err := finalizeIndexOutput(repo, plan); err != nil {
		t.Fatal(err)
	}
	if got := readTestSCIPDocs(t, plan.IndexPath); len(got) != 1 || got[0] != "engine/src/first.rs" {
		t.Fatalf("first run: docs = %v", got)
	}
	if _, err := os.Stat(filepath.Join(engine, "index.scip")); !os.IsNotExist(err) {
		t.Errorf("index left behind in the indexer directory: %v", err)
	}

	writeTestSCIP(t, filepath.Join(engine, "index.scip"), "src/second.rs")
	if err := finalizeIndexOutput(repo, plan); err != nil {
		t.Fatal(err)
	}
	if got := readTestSCIPDocs(t, plan.IndexPath); len(got) != 1 || got[0] != "engine/src/second.rs" {
		t.Fatalf("second run kept the old index: docs = %v", got)
	}

	// An indexer that wrote nothing must not get the old index rebased twice.
	if err := finalizeIndexOutput(repo, plan); err != nil {
		t.Fatal(err)
	}
	if got := readTestSCIPDocs(t, plan.IndexPath); len(got) != 1 || got[0] != "engine/src/second.rs" {
		t.Fatalf("old index rebased again: docs = %v", got)
	}
}

func TestFinalizeIndexOutput_CustomOutputPath(t *testing.T) {
	repo := t.TempDir()
	engine := filepath.Join(repo, "engine")
	if err := os.Mkdir(engine, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &indexPlan{IndexerDir: engine, IndexPath: filepath.Join(repo, "custom.scip")}
	writeTestSCIP(t, plan.IndexPath, "src/lib.rs")
	if err := finalizeIndexOutput(repo, plan); err != nil {
		t.Fatal(err)
	}
	if got := readTestSCIPDocs(t, plan.IndexPath); len(got) != 1 || got[0] != "engine/src/lib.rs" {
		t.Fatalf("docs = %v", got)
	}
}

func TestFinalizeIndexOutput_RepoRootIndexerUntouched(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "index.scip")
	writeTestSCIP(t, path, "src/lib.rs")
	if err := finalizeIndexOutput(repo, &indexPlan{IndexerDir: repo, IndexPath: path}); err != nil {
		t.Fatal(err)
	}
	if got := readTestSCIPDocs(t, path); len(got) != 1 || got[0] != "src/lib.rs" {
		t.Fatalf("docs = %v", got)
	}
}

func TestTryIncrementalIndex_NestedManifestRunsFullIndex(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "svc", "go.mod"), []byte("module example.com/svc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if tryIncrementalIndexTo(repo, filepath.Join(repo, ".ckb"), project.LangGo, &out) {
		t.Fatal("incremental index claimed success for a nested manifest")
	}
	if !strings.Contains(out.String(), "Nested project") {
		t.Errorf("output = %q", out.String())
	}
}
