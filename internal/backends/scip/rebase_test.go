package scip

import (
	"os"
	"path/filepath"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

func TestRebaseDocumentPaths(t *testing.T) {
	const symbol = "scip-go gomod example v1 `example`/Run()."
	data, err := proto.Marshal(&scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file:///repo/engine", ToolInfo: &scippb.ToolInfo{Name: "rust-analyzer"}},
		Documents: []*scippb.Document{{
			RelativePath: "crates/a/src/lib.rs",
			Symbols:      []*scippb.SymbolInformation{{Symbol: symbol}},
			Occurrences:  []*scippb.Occurrence{{Symbol: symbol, Range: []int32{1, 2, 5}, SymbolRoles: 1}},
		}},
		ExternalSymbols: []*scippb.SymbolInformation{{Symbol: "external"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RebaseDocumentPaths(path, "engine", "/repo"); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want the original 0644", info.Mode().Perm())
	}

	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got scippb.Index
	if err := proto.Unmarshal(rewritten, &got); err != nil {
		t.Fatalf("rewritten index does not parse: %v", err)
	}
	if got.Metadata.ProjectRoot != "file:///repo" {
		t.Errorf("project root = %q", got.Metadata.ProjectRoot)
	}
	if got.Metadata.ToolInfo.GetName() != "rust-analyzer" {
		t.Errorf("metadata lost its tool info: %v", got.Metadata.ToolInfo)
	}
	if len(got.Documents) != 1 || got.Documents[0].RelativePath != "engine/crates/a/src/lib.rs" {
		t.Fatalf("documents = %v", got.Documents)
	}
	if len(got.Documents[0].Occurrences) != 1 || got.Documents[0].Occurrences[0].Symbol != symbol {
		t.Errorf("occurrences changed: %v", got.Documents[0].Occurrences)
	}
	if len(got.ExternalSymbols) != 1 {
		t.Errorf("external symbols dropped: %v", got.ExternalSymbols)
	}
}

func TestRebaseDocumentPaths_CorruptIndexLeavesFileAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.scip")
	corrupt := []byte{0x12, 0xff} // document field claiming more bytes than exist
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RebaseDocumentPaths(path, "engine", "/repo"); err == nil {
		t.Fatal("expected an error for a corrupt index")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(corrupt) {
		t.Error("corrupt index was modified")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestLoadSCIPIndex_SkipsDocumentsOutsideTheProject(t *testing.T) {
	data, err := proto.Marshal(&scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file:///repo"},
		Documents: []*scippb.Document{
			{RelativePath: "a.go"},
			{RelativePath: "../../Library/Caches/go-build/03/testmain.go"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadSCIPIndex(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Documents) != 1 || idx.Documents[0].RelativePath != "a.go" {
		t.Errorf("documents = %d, want only a.go", len(idx.Documents))
	}
}
