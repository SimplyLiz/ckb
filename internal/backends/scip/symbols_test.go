package scip

import (
	"fmt"
	"testing"
)

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

// Alias-only matches ("Server.Close" for query "Serve") must not consume the
// MaxResults cap ahead of real name matches such as ServeHTTP.
func TestSearchSymbols_AliasMatchesDoNotCrowdOutNameMatches(t *testing.T) {
	converted := map[string]*SCIPSymbol{}
	var names []NameEntry
	add := func(id, name, container string) {
		converted[id] = &SCIPSymbol{StableId: id, Name: name, ContainerName: container}
		alias := ""
		if container != "" {
			alias = container + "." + name
		}
		names = append(names, NameEntry{Name: name, Alias: alias, ID: id})
	}
	// 40 alias-only members sort first.
	for i := 0; i < 40; i++ {
		add(fmt.Sprintf("a%02d", i), fmt.Sprintf("Method%02d", i), "Server")
	}
	add("z-serve", "ServeHTTP", "Mux")

	check := func(t *testing.T, idx *SCIPIndex) {
		t.Helper()
		res, err := idx.SearchSymbols("Serve", SearchOptions{MaxResults: 30})
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 30 || res[0].Name != "ServeHTTP" {
			t.Fatalf("want ServeHTTP first of 30, got %d results, first %q", len(res), res[0].Name)
		}
	}
	t.Run("fast path", func(t *testing.T) {
		check(t, &SCIPIndex{ConvertedSymbols: converted, NameIndex: names})
	})
	t.Run("map fallback", func(t *testing.T) {
		check(t, &SCIPIndex{ConvertedSymbols: converted})
	})
}
