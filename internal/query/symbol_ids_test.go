package query

import (
	"fmt"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestSpreadReferences_EveryFileBeforeRepeats(t *testing.T) {
	var refs []ReferenceInfo
	for i := 0; i < 5; i++ {
		refs = append(refs, ReferenceInfo{Location: &LocationInfo{FileId: "src/a.ts", StartLine: i + 1}})
	}
	refs = append(refs,
		ReferenceInfo{Location: &LocationInfo{FileId: "src/b.ts", StartLine: 1}},
		ReferenceInfo{Location: &LocationInfo{FileId: "tests/a.test.ts", StartLine: 1}},
		ReferenceInfo{Location: &LocationInfo{FileId: "tests/a.test.ts", StartLine: 2}},
	)
	got := spreadReferences(refs, 4)
	var files []string
	for _, r := range got {
		files = append(files, fmt.Sprintf("%s:%d", r.Location.FileId, r.Location.StartLine))
	}
	if fmt.Sprint(files) != "[src/a.ts:1 src/a.ts:2 src/b.ts:1 tests/a.test.ts:1]" {
		t.Errorf("got %v", files)
	}
}

func TestDescriptorVariant(t *testing.T) {
	const base = "scip-typescript npm app 0.1.0 src/`entries.ts`/"
	idx := &scip.SCIPIndex{
		DefinitionIndex: map[string]*scip.OccurrenceRef{base + "ListKind#": {}, base + "listOf().": {}},
		Symbols:         map[string]*scip.SymbolInformation{},
	}
	for in, want := range map[string]string{
		base + "ListKind.": base + "ListKind#",
		base + "ListKind":  base + "ListKind#",
		base + "listOf.":   base + "listOf().",
		base + "ListKind#": "",
		base + "missing.":  "",
		"ListKind":         "",
	} {
		if got := descriptorVariant(idx, in); got != want {
			t.Errorf("descriptorVariant(%q) = %q, want %q", in, got, want)
		}
	}
}
