package unwired

import (
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Two things the reference graph does not show.
//
// A Go `init` runs when its package loads, so a file nobody references still
// runs: cobra subcommands register themselves this way, and from main.go the
// walk never reached them, which put the whole HTTP API, the A2A server and the
// compliance frameworks under "unwired".
//
// A method called through an interface has no reference of its own; see
// deadcode.InterfaceReach, shared with the dead code analyzer.

// initFiles lists, per directory, the Go files that define an `init`. All of
// a package's init functions share one symbol in scip-go, so the definition
// occurrences are what tell the files apart.
func initFiles(index *scip.SCIPIndex) map[string][]string {
	files := make(map[string][]string)
	for _, doc := range index.Documents {
		if !strings.HasSuffix(doc.RelativePath, ".go") {
			continue
		}
		for _, occ := range doc.Occurrences {
			if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 && strings.HasSuffix(occ.Symbol, "/init().") {
				dir := filepath.Dir(doc.RelativePath)
				files[dir] = append(files[dir], doc.RelativePath)
				break
			}
		}
	}
	return files
}
