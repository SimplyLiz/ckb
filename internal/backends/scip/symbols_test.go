package scip

import "testing"

// TestSearchSymbols_ContainerQualifiedAlias_NoFTS is a regression test for a
// bug where the non-FTS SearchSymbols path matched only a symbol's bare
// display name. A query for a container ("Handler") no longer found its
// member ("Handler#handle") once FTS was unavailable — even though FTS
// itself (see convertSymbolToFTSRecord in internal/query/fts.go) already
// indexes the container-qualified alias ("Handler.handle").
//
// This exercises all three SearchSymbols code paths, since the bug affected
// each independently: the NameIndex fast path (pre-filtered on entry.Name
// alone), the ConvertedSymbols map fallback, and the on-the-fly last-resort
// conversion (both routed through matchesQuery, which also only checked
// sym.Name).
func TestSearchSymbols_ContainerQualifiedAlias_NoFTS(t *testing.T) {
	const symbolID = "scip-typescript npm fixture 1.0.0 src/pkg/`handler.ts`/Handler#handle()."

	symInfo := &SymbolInformation{
		Symbol: symbolID,
		Kind:   9, // Method
	}

	sym, err := convertToSCIPSymbol(symInfo, &SCIPIndex{
		Symbols: map[string]*SymbolInformation{symbolID: symInfo},
	})
	if err != nil {
		t.Fatalf("convertToSCIPSymbol failed: %v", err)
	}
	if sym.Name != "handle" {
		t.Fatalf("precondition: expected Name=handle, got %q", sym.Name)
	}
	if sym.ContainerName != "Handler" {
		t.Fatalf("precondition: expected ContainerName=Handler, got %q", sym.ContainerName)
	}

	assertFindsHandle := func(t *testing.T, idx *SCIPIndex) {
		t.Helper()
		results, err := idx.SearchSymbols("Handler", SearchOptions{MaxResults: 10})
		if err != nil {
			t.Fatalf("SearchSymbols error: %v", err)
		}
		if len(results) != 1 || results[0].StableId != symbolID {
			t.Fatalf("query %q: expected to find %s via container-qualified alias, got %+v", "Handler", symbolID, results)
		}
	}

	t.Run("NameIndex fast path", func(t *testing.T) {
		idx := &SCIPIndex{
			ConvertedSymbols: map[string]*SCIPSymbol{symbolID: sym},
			NameIndex: []NameEntry{
				{Name: sym.Name, Alias: sym.ContainerName + "." + sym.Name, ID: symbolID},
			},
		}
		assertFindsHandle(t, idx)
	})

	t.Run("ConvertedSymbols map fallback (no NameIndex)", func(t *testing.T) {
		idx := &SCIPIndex{
			ConvertedSymbols: map[string]*SCIPSymbol{symbolID: sym},
		}
		assertFindsHandle(t, idx)
	})

	t.Run("on-the-fly conversion (no cache at all)", func(t *testing.T) {
		idx := &SCIPIndex{
			Symbols: map[string]*SymbolInformation{symbolID: symInfo},
		}
		assertFindsHandle(t, idx)
	})
}
