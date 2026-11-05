package query

import (
	"context"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/testutil"
)

// Every search hit has to be something an agent can jump to, on the same 1-based
// lines findReferences reports, through the FTS path as well as the SCIP one.
func TestSearchSymbols_FTSResultsHaveDefinitionLocations(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()
		ctx := context.Background()

		resp, err := engine.SearchSymbols(ctx, SearchSymbolsOptions{Query: "Handler", Limit: 20})
		if err != nil {
			t.Fatalf("SearchSymbols: %v", err)
		}
		if len(resp.Symbols) == 0 {
			t.Fatal("no results")
		}
		viaFTS := false
		for _, contrib := range resp.Provenance.Backends {
			viaFTS = viaFTS || contrib.BackendId == "fts5"
		}
		if !viaFTS {
			t.Fatalf("results did not come through FTS: %+v", resp.Provenance.Backends)
		}

		for _, sym := range resp.Symbols {
			if sym.Location == nil || sym.Location.StartLine < 1 || sym.Location.StartColumn < 1 {
				t.Errorf("%s: location %+v, want 1-based line and column", sym.StableId, sym.Location)
			}
		}

		sym := resp.Symbols[0]
		refs, err := engine.FindReferences(ctx, FindReferencesOptions{SymbolId: sym.StableId, IncludeTests: true, Limit: 200})
		if err != nil {
			t.Fatalf("FindReferences: %v", err)
		}
		for _, ref := range refs.References {
			if ref.Kind == "definition" && ref.Location != nil {
				if ref.Location.FileId != sym.Location.FileId || ref.Location.StartLine != sym.Location.StartLine {
					t.Errorf("search says %s:%d, refs say %s:%d", sym.Location.FileId, sym.Location.StartLine, ref.Location.FileId, ref.Location.StartLine)
				}
				return
			}
		}
	})
}
