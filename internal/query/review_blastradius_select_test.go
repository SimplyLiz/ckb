package query

import (
	"fmt"
	"testing"
)

func TestBlastRadiusCandidates_OnlyChangedCallables(t *testing.T) {
	const pkg = "scip-typescript npm app 0.1.0 src/"
	changed := []ChangedSymbolInfo{
		{SymbolID: pkg + "`big.tsx`/Big().", Name: "Big", File: "src/big.tsx", ChangeType: "modified"},
		{SymbolID: pkg + "`entries.ts`/listOf().", Name: "listOf", File: "src/entries.ts", ChangeType: "added"},
		{SymbolID: pkg + "`entries.ts`/ListKind#", Name: "ListKind", File: "src/entries.ts", ChangeType: "modified"},        // type: has no callers
		{SymbolID: pkg + "`entries.ts`/MAX.", Name: "MAX", File: "src/entries.ts", ChangeType: "modified"},                  // constant
		{SymbolID: pkg + "`entries.ts`/gone().", Name: "gone", File: "src/entries.ts", ChangeType: "deleted"},               // done check's
		{SymbolID: pkg + "`entries.ts`/doc().", Name: "doc", File: "src/entries.ts", ChangeType: "modified", Trivial: true}, // comment only
		{SymbolID: "file:src/entries.ts", Name: "src/entries.ts", File: "src/entries.ts", ChangeType: "modified"},
		{SymbolID: pkg + "`x.test.ts`/helper().", Name: "helper", File: "src/x.test.ts", ChangeType: "modified"},
		{SymbolID: pkg + "`other.ts`/notReviewed().", Name: "notReviewed", File: "src/other.ts", ChangeType: "modified"}, // outside --scope
	}
	files := []string{"src/big.tsx", "src/entries.ts", "src/x.test.ts"}
	got := blastRadiusCandidates(changed, files, func(string) int { return 1 })
	var names []string
	for _, c := range got {
		names = append(names, c.name)
	}
	if fmt.Sprint(names) != "[Big listOf]" {
		t.Errorf("candidates = %v, want [Big listOf]", names)
	}
}

func TestSelectBlastRadiusSymbols_SpreadsOverFiles(t *testing.T) {
	// The first file has more changed functions than the cap. Before, it took every
	// slot and the second file's listOf, the one with the callers, was never looked at.
	var cands []blastRadiusSymbol
	for i := 0; i < 40; i++ {
		cands = append(cands, blastRadiusSymbol{stableId: fmt.Sprintf("big%d", i), name: fmt.Sprintf("big%d", i), file: "Documents.tsx", refs: 40 - i})
	}
	cands = append(cands,
		blastRadiusSymbol{stableId: "listOf", name: "listOf", file: "entries.ts", refs: 18},
		blastRadiusSymbol{stableId: "setList", name: "setList", file: "entries.ts", refs: 1},
		blastRadiusSymbol{stableId: "docx", name: "renderDocx", file: "docx.ts", refs: 2},
	)

	got := selectBlastRadiusSymbols(cands, 30)
	if len(got) != 30 {
		t.Fatalf("selected %d, want 30", len(got))
	}
	perFile := map[string]int{}
	has := map[string]bool{}
	for _, s := range got {
		perFile[s.file]++
		has[s.name] = true
	}
	for _, want := range []string{"listOf", "setList", "renderDocx", "big0", "big1"} {
		if !has[want] {
			t.Errorf("%s not selected: %v", want, perFile)
		}
	}
	if perFile["Documents.tsx"] != 27 {
		t.Errorf("Documents.tsx got %d slots, want the 27 left over", perFile["Documents.tsx"])
	}
	// Within the large file, the best-referenced ones are kept.
	if has["big39"] || !has["big26"] {
		t.Errorf("large file should keep its best-referenced functions")
	}
}

func TestSelectBlastRadiusSymbols_CutWithinRoundKeepsBestReferenced(t *testing.T) {
	cands := []blastRadiusSymbol{
		{name: "a", file: "a.ts", refs: 1},
		{name: "b", file: "b.ts", refs: 9},
		{name: "c", file: "c.ts", refs: 5},
	}
	got := selectBlastRadiusSymbols(cands, 2)
	if len(got) != 2 || got[0].name != "b" || got[1].name != "c" {
		t.Errorf("got %v, want b then c", got)
	}
}

func TestSelectBlastRadiusSymbols_UnderCapKeepsAll(t *testing.T) {
	cands := []blastRadiusSymbol{{name: "a", file: "a.ts"}, {name: "b", file: "a.ts"}}
	if got := selectBlastRadiusSymbols(cands, 30); len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestBlastRadiusCoverage(t *testing.T) {
	if got := blastRadiusCoverage(30, 52); got != " (30 of 52 changed functions checked)" {
		t.Errorf("got %q", got)
	}
	if got := blastRadiusCoverage(4, 4); got != " (4 changed function(s) checked)" {
		t.Errorf("got %q", got)
	}
}
