package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// A truncated index — what a server sees when it loads while the indexer is
// still writing — must come back as an error. It used to panic the parser
// goroutine (slice bounds out of range), taking the whole process down.
func TestLoadSCIPIndex_TruncatedFileIsAnError(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	for _, cut := range []int{1, 2, len(fixture) / 3, len(fixture) / 2, len(fixture) - 1} {
		path := filepath.Join(t.TempDir(), "index.scip")
		if err := os.WriteFile(path, fixture[:cut], 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadSCIPIndex(path); err == nil {
			t.Errorf("cut at %d/%d bytes: loaded without error", cut, len(fixture))
		}
	}
}

func TestLoadSCIPIndex_GarbageIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.scip")
	if err := os.WriteFile(path, []byte("not a scip index"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadSCIPIndex(path); err == nil {
		t.Fatal("garbage loaded without error")
	}
}

// A call in `let id = project.save(x)` sits inside the local's enclosing range, as
// rust-analyzer writes it. The container has to be the function, or the caller is lost.
func TestContainerEntries_LocalsAndParametersAreNotContainers(t *testing.T) {
	const fn = "rust-analyzer cargo app 0.1.0 store/run()."
	doc := &Document{
		RelativePath: "src/store.rs",
		Occurrences: []*Occurrence{
			{Symbol: fn, Range: []int32{10, 3, 6}, SymbolRoles: SymbolRoleDefinition, EnclosingRange: []int32{10, 0, 20, 1}},
			{Symbol: "local 12", Range: []int32{12, 4, 6}, SymbolRoles: SymbolRoleDefinition, EnclosingRange: []int32{12, 0, 12, 30}},
			{Symbol: "rust-analyzer cargo app 0.1.0 store/run().(x)", Range: []int32{11, 4, 5}, SymbolRoles: SymbolRoleDefinition, EnclosingRange: []int32{11, 0, 11, 9}},
			{Symbol: "rust-analyzer cargo app 0.1.0 store/save().", Range: []int32{12, 12, 16}},
			{Symbol: "rust-analyzer cargo app 0.1.0 store/load().", Range: []int32{11, 10, 14}},
			{Symbol: "rust-analyzer cargo app 0.1.0 store/top().", Range: []int32{30, 0, 3}},
		},
	}

	entries := containerEntries(doc)
	for _, key := range []string{"src/store.rs:12:12", "src/store.rs:11:10"} {
		if entries[key] != fn {
			t.Errorf("container of %s = %q, want the function", key, entries[key])
		}
	}
	if got, ok := entries["src/store.rs:30:0"]; ok {
		t.Errorf("a reference outside every definition has container %q, want none", got)
	}
}
