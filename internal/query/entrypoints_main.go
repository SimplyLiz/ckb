package query

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// programMains are the functions a program starts in: every function or method
// named main outside test files, and in Go only the one in a package main (a
// main in a library package is just a function). They're read off the index
// directly, not searched: a search for "main" matches domain, remaining and
// every other name containing it, and its result cap ran out before the real
// ones, or the path rule threw them away (it wanted "/cmd/" in a repo-relative
// path, which neither main.go nor cmd/ckb/main.go has).
func (e *Engine) programMains(idx *scip.SCIPIndex) []EntrypointV52 {
	if idx == nil {
		return nil
	}
	var out []EntrypointV52
	for _, doc := range idx.Documents {
		if doc == nil || isTestFilePath(doc.RelativePath) {
			continue
		}
		for _, sym := range doc.Symbols {
			if sym == nil || !isMainFunction(sym.Symbol) {
				continue
			}
			if strings.HasSuffix(doc.RelativePath, ".go") && !e.isGoPackageMain(doc.RelativePath) {
				continue
			}
			out = append(out, EntrypointV52{
				SymbolId:       sym.Symbol,
				Name:           "main",
				Type:           "cli",
				Location:       &LocationInfo{FileId: doc.RelativePath, StartLine: definitionLine(idx, sym.Symbol)},
				DetectionBasis: "naming",
				FanOut:         idx.GetCalleeCount(sym.Symbol),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Location.FileId < out[j].Location.FileId })
	return out
}

// isMainFunction reports whether a SCIP symbol is a function or method named
// main, not a local, a field or a type of that name.
func isMainFunction(symbol string) bool {
	if strings.HasPrefix(symbol, "local ") || !strings.Contains(symbol, "main") {
		return false
	}
	parsed, err := bindingscip.ParseSymbol(symbol)
	if err != nil || len(parsed.Descriptors) == 0 {
		return false
	}
	last := parsed.Descriptors[len(parsed.Descriptors)-1]
	return last.Name == "main" && last.Suffix == bindingscip.Descriptor_Method
}

// isGoPackageMain reports whether the Go file at path declares package main.
func (e *Engine) isGoPackageMain(path string) bool {
	src, err := os.ReadFile(filepath.Join(e.repoRoot, path))
	if err != nil {
		return false
	}
	f, err := parser.ParseFile(token.NewFileSet(), path, src, parser.PackageClauseOnly)
	return err == nil && f.Name != nil && f.Name.Name == "main"
}
