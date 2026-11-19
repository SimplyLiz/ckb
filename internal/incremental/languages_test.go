package incremental

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// Change detection used to know ".go" only, so in a TypeScript or Rust repo an
// incremental `ckb index` saw no change, skipped the indexer and reported the
// index as up to date.
func TestDetectChanges_FollowsTheLanguage(t *testing.T) {
	detector, _, dir, cleanup := setupTestDetector(t)
	defer cleanup()
	initGitRepo(t, dir)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	write := func(content string) {
		for _, f := range []string{"main.go", "src/app.ts", "src/view.tsx", "src/lib.rs", "pkg/mod.py", "README.md"} {
			p := filepath.Join(dir, f)
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write("one\n")
	git("add", ".")
	git("commit", "-qm", "one")
	base := git("rev-parse", "HEAD")[:40]
	write("two\n")
	git("commit", "-qam", "two")

	for _, tc := range []struct {
		lang project.Language
		want []string
	}{
		{"", []string{"main.go"}}, // IndexIncremental: Go
		{project.LangGo, []string{"main.go"}},
		{project.LangTypeScript, []string{"src/app.ts", "src/view.tsx"}},
		{project.LangRust, []string{"src/lib.rs"}},
		{project.LangPython, []string{"pkg/mod.py"}},
	} {
		var exts []string
		if tc.lang != "" {
			exts = project.GetIndexerConfig(tc.lang).Extensions
		}
		detector.SetExtensions(exts)
		changes, err := detector.DetectChanges(base)
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		got := map[string]bool{}
		for _, c := range changes {
			got[c.Path] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: changes %v, want %v", tc.lang, changes, tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%s: %s not detected (got %v)", tc.lang, w, changes)
			}
		}
	}
}

// Every language with incremental support needs extensions, or its changes
// are never seen.
func TestIncrementalLanguagesHaveExtensions(t *testing.T) {
	for lang, cfg := range project.Indexers {
		if cfg.SupportsIncremental && len(cfg.Extensions) == 0 {
			t.Errorf("%s supports incremental indexing but has no Extensions", lang)
		}
	}
}

// Populating the tracking tables after a full index kept documents whose
// Language was "go" or empty. rust-analyzer writes "rust", so a Rust repo had
// no tracked file and no call edge.
func TestPopulate_KeepsEveryLanguage(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		indexer, dir, cleanup := setupTestIndexer(t)

		pkg := "rust-analyzer cargo demo 0.1.0 "
		index := &scippb.Index{
			Metadata: &scippb.Metadata{ToolInfo: &scippb.ToolInfo{Name: "rust-analyzer"}},
			Documents: []*scippb.Document{{
				Language:     "rust",
				RelativePath: "src/lib.rs",
				Occurrences: []*scippb.Occurrence{
					{Symbol: pkg + "outer.", Range: []int32{0, 3, 8}, EnclosingRange: []int32{0, 0, 3, 1}, SymbolRoles: 1},
					{Symbol: pkg + "run.", Range: []int32{1, 4, 7}},
				},
				Symbols: []*scippb.SymbolInformation{
					{Symbol: pkg + "outer.", Kind: scippb.SymbolInformation_Function},
					{Symbol: pkg + "run.", Kind: scippb.SymbolInformation_Function},
				},
			}, {
				Language:     "go",
				RelativePath: "main.go",
			}},
		}
		data, err := proto.Marshal(index)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.scip"), data, 0644); err != nil {
			t.Fatal(err)
		}

		populate := indexer.updater.PopulateFromFullIndex
		if streaming {
			populate = indexer.updater.PopulateFromFullIndexStreaming
		}
		if err := populate(indexer.extractor); err != nil {
			t.Fatalf("streaming=%v: %v", streaming, err)
		}
		if n := indexer.store.GetTotalFileCount(); n != 2 {
			t.Errorf("streaming=%v: %d tracked files, want 2", streaming, n)
		}
		var caller string
		if err := indexer.store.db.QueryRow(`SELECT caller_id FROM callgraph WHERE callee_id = ?`, pkg+"run.").Scan(&caller); err != nil {
			t.Errorf("streaming=%v: no call edge to run: %v", streaming, err)
		} else if caller != pkg+"outer." {
			t.Errorf("streaming=%v: caller of run = %q, want outer", streaming, caller)
		}
		cleanup()
	}
}
