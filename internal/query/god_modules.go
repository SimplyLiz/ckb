package query

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// A god module is a file that both depends on much of the code and declares a
// lot of it: change it and you are likely touching many concerns at once.
// Cartographer's rule (detect_god_modules), on SCIP data instead of its own
// import resolution, so it holds for every indexed language:
//
//   - breadth: distinct units the file references a definition in, at least
//     godMinBreadth. A unit is a file, or for Go a package (its directory) —
//     a Go file referencing five files of one package depends on one thing;
//   - size: declarations of the file (types, functions, methods, variables;
//     fields and locals not counted), at least godMinDeclarations.
//
// Tests, generated files and build output take no part, as in codeBridges.
const (
	godMinBreadth      = 15
	godMinDeclarations = 40
)

// GodModule is one file over both thresholds.
type GodModule struct {
	File         string `json:"file"`
	Dependencies int    `json:"dependencies"` // distinct files (Go: packages) it references
	Declarations int    `json:"declarations"`
	// Severity: "high" from 1.5× both thresholds, "critical" from 2×, else "medium".
	Severity string `json:"severity"`
}

// godModules returns the god modules of idx, most dependencies first.
func godModules(idx *scip.SCIPIndex, skip func(string) bool) []GodModule {
	if idx == nil {
		return nil
	}
	var out []GodModule
	for _, doc := range idx.Documents {
		if m, ok := godModuleOf(idx, doc, skip); ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dependencies != out[j].Dependencies {
			return out[i].Dependencies > out[j].Dependencies
		}
		return out[i].File < out[j].File
	})
	return out
}

// godModuleOf checks one document against both thresholds.
func godModuleOf(idx *scip.SCIPIndex, doc *scip.Document, skip func(string) bool) (GodModule, bool) {
	if doc == nil || skip(doc.RelativePath) {
		return GodModule{}, false
	}
	unit := func(p string) string {
		if strings.HasSuffix(p, ".go") {
			return path.Dir(p) + "/"
		}
		return p
	}
	self := unit(doc.RelativePath)
	deps := map[string]bool{}
	for _, occ := range doc.Occurrences {
		if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || strings.HasPrefix(occ.Symbol, "local ") {
			continue
		}
		def := idx.DefinitionIndex[occ.Symbol]
		if def == nil || def.Doc == nil || skip(def.Doc.RelativePath) {
			continue
		}
		if u := unit(def.Doc.RelativePath); u != self {
			deps[u] = true
		}
	}
	if len(deps) < godMinBreadth {
		return GodModule{}, false
	}
	decls := countDeclarations(doc)
	if decls < godMinDeclarations {
		return GodModule{}, false
	}
	sev := "medium"
	switch {
	case len(deps) >= 2*godMinBreadth && decls >= 2*godMinDeclarations:
		sev = "critical"
	case 2*len(deps) >= 3*godMinBreadth && 2*decls >= 3*godMinDeclarations:
		sev = "high"
	}
	return GodModule{File: doc.RelativePath, Dependencies: len(deps), Declarations: decls, Severity: sev}, true
}

// countDeclarations counts the symbols doc declares: types, functions,
// methods and top-level variables. Fields, parameters, type parameters, meta
// symbols (scip-typescript's `x0:`), locals and the namespace do not count —
// a TSX component's destructured props came to 100+ "declarations" otherwise.
func countDeclarations(doc *scip.Document) int {
	n := 0
	seen := map[string]bool{}
	for _, sym := range doc.Symbols {
		if sym == nil || seen[sym.Symbol] || strings.HasPrefix(sym.Symbol, "local ") {
			continue
		}
		seen[sym.Symbol] = true
		if isDeclarationSymbol(sym.Symbol) {
			n++
		}
	}
	return n
}

// isDeclarationSymbol: the descriptor (after the last "/") is a type ("T#"),
// a function or method ("f().", "T#m()."), or a term outside any type ("x.").
func isDeclarationSymbol(symbol string) bool {
	d := symbol
	if i := strings.LastIndex(symbol, "/"); i >= 0 {
		d = symbol[i+1:]
	}
	switch {
	case d == "":
		return false // the namespace
	case strings.HasSuffix(d, "()."):
		return !strings.Contains(strings.TrimSuffix(d, "()."), "(")
	case strings.HasSuffix(d, "#"):
		return !strings.ContainsAny(strings.TrimSuffix(d, "#"), "#(")
	case strings.HasSuffix(d, "."):
		return !strings.ContainsAny(d, "#(")
	}
	return false
}

// structuralRiskFactors names what makes file structurally risky to change:
// a runtime import cycle through it, being a bridge, being a god module. Empty
// without a SCIP index. These replace Cartographer's SimulateChange factors,
// whose "may introduce a dependency cycle" only meant the file already sat in
// one, and whose layer check ran on an empty default config.
func (e *Engine) structuralRiskFactors(file string) []string {
	if file == "" || !e.hasConsumerGraph() {
		return nil
	}
	idx := e.scipAdapter.GetIndex()
	classes := pathclass.FromConfig(e.repoRoot, e.config)
	var out []string
	if g := fileImportGraph(idx, e.repoRoot); g != nil && len(g.languages) > 0 {
		for _, c := range g.runtimeCycles() {
			if slices.Contains(c.Nodes, file) {
				out = append(out, fmt.Sprintf("File is part of a runtime import cycle through %d files: %s", len(c.Nodes), cycleList(c.Nodes)))
				break
			}
		}
	}
	if cut, ok := codeBridges(idx, e.repoRoot, classes)[file]; ok {
		out = append(out, fmt.Sprintf("File is a bridge: the only link to %d files", cut))
	}
	if m, ok := godModuleOf(idx, idx.GetDocument(file), structuralNoise(classes)); ok {
		out = append(out, fmt.Sprintf("File is a god module: depends on %d files/packages, declares %d symbols", m.Dependencies, m.Declarations))
	}
	return out
}
