//go:build !cgo

package overlay

import "github.com/SimplyLiz/CodeMCP/internal/backends/scip"

// Without cgo there is no tree-sitter: TypeScript files stay at Layer 0.

func tsLayer1(*scip.Document, *scip.SCIPIndex, *scip.Document, []byte, *LineMap) (int, string) {
	return 0, "built without cgo: no TypeScript parser"
}

func tsNewDocument(*scip.SCIPIndex, string, []byte) (*scip.Document, string) { return nil, "" }

type tsResolver struct{}

func newTSResolver(string, func(string) *scip.Document) *tsResolver { return &tsResolver{} }

func tsLayer2(*scip.Document, string, []byte, map[int32]bool, *tsResolver) (int, int) { return 0, 0 }
