package scip

import (
	"os"
	"path/filepath"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func writeTestIndex(t *testing.T, path string, idx *scippb.Index) {
	t.Helper()
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeIndexes(t *testing.T) {
	dir := t.TempDir()
	const goSym = "scip-go gomod example v1 `example`/Run()."
	const pySym = "scip-python python example 0.1 `tool.main`/main()."
	goPath := filepath.Join(dir, "go.scip")
	pyPath := filepath.Join(dir, "py.scip")
	writeTestIndex(t, goPath, &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file:///repo", ToolInfo: &scippb.ToolInfo{Name: "scip-go", Arguments: []string{"a"}}},
		Documents: []*scippb.Document{{
			RelativePath: "main.go",
			Language:     "go",
			Symbols:      []*scippb.SymbolInformation{{Symbol: goSym}},
			Occurrences:  []*scippb.Occurrence{{Symbol: goSym, Range: []int32{1, 5, 8}, SymbolRoles: 1}},
		}},
		ExternalSymbols: []*scippb.SymbolInformation{{Symbol: "shared external"}, {Symbol: "go external"}},
	})
	writeTestIndex(t, pyPath, &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file:///repo", ToolInfo: &scippb.ToolInfo{Name: "scip-python", Arguments: []string{"b"}}},
		Documents: []*scippb.Document{
			{
				RelativePath: "tool/main.py",
				Language:     "python",
				Symbols:      []*scippb.SymbolInformation{{Symbol: pySym}},
				Occurrences:  []*scippb.Occurrence{{Symbol: pySym, Range: []int32{0, 4, 8}, SymbolRoles: 1}},
			},
			// Already in the Go index: the first copy wins.
			{RelativePath: "main.go", Language: "python"},
		},
		ExternalSymbols: []*scippb.SymbolInformation{{Symbol: "shared external", Documentation: []string{"second"}}},
	})

	out := filepath.Join(dir, "index.scip")
	if err := MergeIndexes(out, []string{goPath, pyPath}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got scippb.Index
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatalf("merged index does not parse: %v", err)
	}
	if got.Metadata.ToolInfo.GetName() != "scip-go" || len(got.Metadata.ToolInfo.Arguments) != 1 {
		t.Errorf("metadata = %v, want the first input's alone", got.Metadata)
	}
	if len(got.Documents) != 2 || got.Documents[0].RelativePath != "main.go" || got.Documents[0].Language != "go" ||
		got.Documents[1].RelativePath != "tool/main.py" {
		t.Fatalf("documents = %v", got.Documents)
	}
	if len(got.ExternalSymbols) != 2 || len(got.ExternalSymbols[0].Documentation) != 0 {
		t.Fatalf("external symbols = %v", got.ExternalSymbols)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("merged index mode = %v (%v), want 0644", info.Mode().Perm(), err)
	}

	// The loader serves both languages' symbols from the one file.
	idx, err := LoadSCIPIndex(out)
	if err != nil {
		t.Fatal(err)
	}
	if idx.GetDocument("tool/main.py") == nil || idx.GetDocument("main.go") == nil {
		t.Errorf("loader is missing a document")
	}
	if idx.GetSymbol(goSym) == nil || idx.GetSymbol(pySym) == nil {
		t.Errorf("loader is missing a symbol")
	}
}

func TestMergeIndexesInPlace(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "index.scip")
	b := filepath.Join(dir, "b.scip")
	writeTestIndex(t, a, &scippb.Index{Documents: []*scippb.Document{{RelativePath: "a.go"}}})
	writeTestIndex(t, b, &scippb.Index{Documents: []*scippb.Document{{RelativePath: "b.py"}}})
	if err := MergeIndexes(a, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(a)
	var got scippb.Index
	if err := proto.Unmarshal(data, &got); err != nil || len(got.Documents) != 2 {
		t.Fatalf("documents = %v (%v)", got.Documents, err)
	}
	if err := MergeIndexes(a, nil); err == nil {
		t.Error("merging nothing should fail")
	}
}
