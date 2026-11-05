package query

import (
	"context"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
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

// Locals are out of symbol search on every path: an FTS table populated by this
// version, one populated by an older version, and the SCIP fallback.
func TestSearchSymbols_NoLocals(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()
		ctx := context.Background()

		index := engine.scipAdapter.GetIndex()
		var local *scip.SymbolInformation
		for id, sym := range index.Symbols {
			if scip.IsLocalSymbol(id) {
				local = sym
				break
			}
		}
		if local == nil {
			t.Skip("fixture index has no locals")
		}

		// Population leaves them out.
		if err := engine.PopulateFTSFromSCIP(ctx); err != nil {
			t.Fatalf("populate: %v", err)
		}
		fts := storage.NewFTSManager(engine.db.Conn(), storage.DefaultFTSConfig())
		var count int
		if err := engine.db.Conn().QueryRow(`SELECT COUNT(*) FROM symbols_fts_content WHERE id LIKE 'local %'`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("%d locals in a freshly populated FTS table", count)
		}

		assertNoLocals := func(when string, opts SearchSymbolsOptions) {
			t.Helper()
			resp, err := engine.SearchSymbols(ctx, opts)
			if err != nil {
				t.Fatalf("%s: SearchSymbols: %v", when, err)
			}
			for _, sym := range resp.Symbols {
				if scip.IsLocalSymbol(sym.StableId) {
					t.Errorf("%s: search returned local %s (%s)", when, sym.StableId, sym.Name)
				}
			}
		}

		// An older table has them, and the query leaves them out.
		const name = "zzlocalonly"
		stale := []storage.SymbolFTSRecord{
			{ID: "local 999999", Name: name, Kind: "unknown", FilePath: "a.go"},
			{ID: "scip-go gomod m v `m`/" + name + "().", Name: name, Kind: "function", FilePath: "a.go"},
		}
		if err := fts.BulkInsert(ctx, stale); err != nil {
			t.Fatalf("insert: %v", err)
		}
		assertNoLocals("older FTS table", SearchSymbolsOptions{Query: name, Limit: 10})

		// No FTS data at all: the SCIP fallback leaves them out too.
		if err := fts.Clear(ctx); err != nil {
			t.Fatalf("clear: %v", err)
		}
		assertNoLocals("SCIP fallback", SearchSymbolsOptions{Query: local.DisplayName, Limit: 500})

		// The fallback is reachable directly as well.
		direct, err := engine.scipAdapter.SearchSymbols(ctx, local.DisplayName, backends.SearchOptions{MaxResults: 500, IncludeTests: true})
		if err != nil {
			t.Fatalf("adapter search: %v", err)
		}
		for _, sym := range direct.Symbols {
			if scip.IsLocalSymbol(sym.StableID) {
				t.Errorf("adapter returned local %s", sym.StableID)
			}
		}
	})
}

// Which indexer wrote an ID decides nothing about whether it can be looked up.
func TestGetSymbol_RawIDFromAnyIndexer(t *testing.T) {
	testutil.ForEachLanguage(t, func(t *testing.T, fixture *testutil.FixtureContext) {
		engine, cleanup := setupGoldenEngine(t, fixture)
		defer cleanup()

		index := engine.scipAdapter.GetIndex()
		for _, id := range []string{
			"rust-analyzer cargo demo 0.1.0 util/helper().",
			"semanticdb maven maven/com.example/demo 1.0 com/example/Main#run().",
			"cxx . . $ demo/helper(abc123).",
		} {
			sym := &scip.SymbolInformation{Symbol: id, DisplayName: "helper"}
			occ := &scip.Occurrence{Range: []int32{3, 0, 6}, Symbol: id, SymbolRoles: scip.SymbolRoleDefinition}
			doc := &scip.Document{RelativePath: "demo.src", Occurrences: []*scip.Occurrence{occ}, Symbols: []*scip.SymbolInformation{sym}}
			index.Documents = append(index.Documents, doc)
			index.Symbols[id] = sym
			index.RefIndex[id] = []*scip.OccurrenceRef{{Doc: doc, Occ: occ}}
			index.DefinitionIndex[id] = index.RefIndex[id][0]

			resp, err := engine.GetSymbol(context.Background(), GetSymbolOptions{SymbolId: id})
			if err != nil {
				t.Errorf("GetSymbol(%q): %v", id, err)
				continue
			}
			if resp.Symbol == nil || resp.Symbol.StableId != id {
				t.Errorf("GetSymbol(%q) = %+v", id, resp.Symbol)
			}
		}

		if _, err := engine.GetSymbol(context.Background(), GetSymbolOptions{SymbolId: "rust-analyzer cargo demo 0.1.0 nope()."}); err == nil {
			t.Error("an ID the index doesn't have should still be not found")
		}
	})
}
