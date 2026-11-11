package main

import (
	"fmt"
	"os"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// remapSymbolID maps a SCIP ID from an index built at another commit to the ID
// the loaded index has for the same symbol, as MCP and A2A do: scip-go puts the
// commit hash in the package-version field, so a copied ID goes stale on every
// commit. The note goes to stderr, so --format=json output stays parseable.
func remapSymbolID(engine *query.Engine, id string) string {
	got, remapped := engine.ResolveSymbolIDAcrossVersions(id)
	if remapped {
		fmt.Fprintf(os.Stderr, "note: %s is from an index at another package version; using %s\n", id, got)
	}
	return got
}
