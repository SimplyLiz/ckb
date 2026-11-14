package query

import (
	"fmt"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func TestIsRunnableTestPath(t *testing.T) {
	cases := map[string]bool{
		"tests/cv-autosave.test.ts":     true,
		"src/foo.spec.tsx":              true,
		"src/__tests__/foo.ts":          true,
		"tests/render/fixtures.ts":      false, // test code, but vitest doesn't run it
		"tests/support/server.ts":       false,
		"internal/query/review_test.go": true,
		"tests/integration.rs":          true, // cargo runs every file in tests/
		"tests/test_api.py":             true,
		"src/lib/cv/store.ts":           false,
	}
	for path, want := range cases {
		if got := isRunnableTestPath(path); got != want {
			t.Errorf("isRunnableTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestChangedSourceFiles(t *testing.T) {
	got := changedSourceFiles([]impact.ChangedSymbol{
		{File: "src/store.ts", SymbolID: "a"},
		{File: "src/store.ts", SymbolID: "b"},
		{File: "src/notes.ts", SymbolID: "c", Trivial: true},
		{File: "src/gone.ts", SymbolID: "d", ChangeType: impact.ChangeDeleted},
		{File: "tests/x.test.ts", SymbolID: "e"},
	})
	if fmt.Sprint(got) != "[src/store.ts]" {
		t.Errorf("got %v", got)
	}
}

func TestTestsUsingFiles(t *testing.T) {
	const store = "scip-typescript npm app 0.1.0 src/`store.ts`/"
	storeDoc := &scip.Document{RelativePath: "src/store.ts", Symbols: []*scip.SymbolInformation{
		{Symbol: store},                // the module: naming it in an import isn't using it
		{Symbol: store + "useEditor."}, // the export the test calls
		{Symbol: "local 3"},
	}}
	ref := func(path string, sym string, roles int32) *scip.OccurrenceRef {
		return &scip.OccurrenceRef{Doc: &scip.Document{RelativePath: path}, Occ: &scip.Occurrence{Symbol: sym, SymbolRoles: roles}}
	}
	idx := &scip.SCIPIndex{
		DocumentsByPath: map[string]*scip.Document{"src/store.ts": storeDoc},
		RefIndex: map[string][]*scip.OccurrenceRef{
			store: {ref("tests/only-imports.test.ts", store, 0)},
			store + "useEditor.": {
				ref("src/store.ts", store+"useEditor.", scip.SymbolRoleDefinition),
				ref("tests/cv-versions.test.ts", store+"useEditor.", 0),
				ref("tests/cv-versions.test.ts", store+"useEditor.", 0),
				ref("tests/render/fixtures.ts", store+"useEditor.", 0),
				ref("src/app/page.tsx", store+"useEditor.", 0),
			},
		},
	}
	got := testsUsingFiles(idx, []string{"src/store.ts", "src/missing.ts"})
	if len(got) != 1 || fmt.Sprint(got["tests/cv-versions.test.ts"]) != "[src/store.ts]" {
		t.Errorf("got %v, want only tests/cv-versions.test.ts", got)
	}
}

func TestMergeFileLevelTests_KeepsSymbolLevelReason(t *testing.T) {
	tests := []AffectedTest{{FilePath: "tests/a.test.ts", Reason: "direct", Confidence: 0.9}}
	got := mergeFileLevelTests(tests, map[string][]string{
		"tests/a.test.ts": {"src/store.ts"},
		"tests/b.test.ts": {"src/store.ts"},
	})
	if len(got) != 2 || got[0].Reason != "direct" || got[1].FilePath != "tests/b.test.ts" || got[1].Reason != reasonUsesChangedFile {
		t.Errorf("got %+v", got)
	}
}
