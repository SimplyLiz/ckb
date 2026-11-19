package query

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cycles"
)

// The file import graph is read off the SCIP index, not resolved by CKB: an
// indexer already knows which file an import names. What it does not record
// is what kind of import it is, and that decides whether a cycle is real:
//
//   - static: loaded when the importing module loads. Only these form
//     runtime cycles.
//   - type:   `import type`, `import { type A }`, `if TYPE_CHECKING:` —
//     erased before anything runs.
//   - lazy:   `await import("…")`, a Python import inside a function — runs
//     after both modules have loaded.
//
// The kind comes from the import site's source text. Supported:
//   - TypeScript/JavaScript (scip-typescript): the module specifier carries an
//     occurrence of the target file's module symbol, `await import()` too.
//   - Python (scip-python): occurrences inside an import statement point at
//     what it imports.
//
// Go is left out: package cycles do not compile, and files of one package
// reference each other freely. Rust too: modules of one crate may form cycles
// legally, so the finding says nothing actionable.

// importKind is a bit set; an edge carries every kind of import site it has.
type importKind uint8

const (
	importStatic importKind = 1 << iota
	importType
	importLazy
)

// importGraph is the file graph of one SCIP index.
type importGraph struct {
	// edges[from][to] is the kinds of import sites from has for to, and
	// sites[from][to] how many static ones.
	edges map[string]map[string]importKind
	sites map[string]map[string]int
	// languages that contributed edges, for the "not checked" note.
	languages map[string]bool
}

var importGraphCache = struct {
	sync.Mutex
	byIndex map[*scip.SCIPIndex]*importGraph
}{byIndex: map[*scip.SCIPIndex]*importGraph{}}

// fileImportGraph returns the classified import graph of idx, built once per
// loaded index.
func fileImportGraph(idx *scip.SCIPIndex, repoRoot string) *importGraph {
	if idx == nil {
		return nil
	}
	importGraphCache.Lock()
	defer importGraphCache.Unlock()
	if g, ok := importGraphCache.byIndex[idx]; ok {
		return g
	}
	g := buildImportGraph(idx, repoRoot)
	// An index is replaced, not mutated, on reindex: drop graphs of old ones.
	for k := range importGraphCache.byIndex {
		delete(importGraphCache.byIndex, k)
	}
	importGraphCache.byIndex[idx] = g
	return g
}

func buildImportGraph(idx *scip.SCIPIndex, repoRoot string) *importGraph {
	g := &importGraph{
		edges:     map[string]map[string]importKind{},
		sites:     map[string]map[string]int{},
		languages: map[string]bool{},
	}
	for _, doc := range idx.Documents {
		if doc == nil {
			continue
		}
		lang := importLanguage(doc.RelativePath)
		if lang == "" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(repoRoot, doc.RelativePath)) // #nosec G304 -- path from the repo's own index
		if err != nil {
			continue
		}
		lines := strings.Split(string(src), "\n")
		switch lang {
		case "ts":
			g.addTSDocument(idx, doc, lines)
		case "py":
			g.addPythonDocument(idx, doc, lines)
		}
	}
	return g
}

func importLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		if strings.HasSuffix(path, ".d.ts") {
			return ""
		}
		return "ts"
	case ".py", ".pyi":
		return "py"
	}
	return ""
}

func (g *importGraph) add(from, to string, kind importKind, lang string) {
	if from == to || kind == 0 {
		return
	}
	if g.edges[from] == nil {
		g.edges[from] = map[string]importKind{}
		g.sites[from] = map[string]int{}
	}
	g.edges[from][to] |= kind
	if kind == importStatic {
		g.sites[from][to]++
	}
	g.languages[lang] = true
}

// definedIn returns the repo file defining symbol, or "". A Python module
// symbol the index references but never defines resolves through
// pythonModuleSuffixes.
func definedIn(idx *scip.SCIPIndex, symbol string) string {
	if idx.DefinitionIndex == nil || strings.HasPrefix(symbol, "local ") {
		return ""
	}
	if def := idx.DefinitionIndex[symbol]; def != nil && def.Doc != nil {
		return def.Doc.RelativePath
	}
	if strings.HasPrefix(symbol, "scip-python ") && strings.HasSuffix(symbol, "/__init__:") {
		return pythonModules(idx)[symbol]
	}
	return ""
}

var pythonModuleCache = struct {
	sync.Mutex
	byIndex map[*scip.SCIPIndex]map[string]string
}{byIndex: map[*scip.SCIPIndex]map[string]string{}}

// pythonModules returns pythonModuleSuffixes of idx, built once per loaded
// index.
func pythonModules(idx *scip.SCIPIndex) map[string]string {
	pythonModuleCache.Lock()
	defer pythonModuleCache.Unlock()
	if m, ok := pythonModuleCache.byIndex[idx]; ok {
		return m
	}
	m := pythonModuleSuffixes(idx)
	for k := range pythonModuleCache.byIndex {
		delete(pythonModuleCache.byIndex, k)
	}
	pythonModuleCache.byIndex[idx] = m
	return m
}

// addTSDocument adds the imports of a TypeScript/JavaScript document: every
// reference to another file's module symbol (scip-typescript names a module
// "…`file.ts`/" and puts an occurrence on each specifier naming it).
func (g *importGraph) addTSDocument(idx *scip.SCIPIndex, doc *scip.Document, lines []string) {
	for _, occ := range doc.Occurrences {
		if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || !strings.HasSuffix(occ.Symbol, "`/") || len(occ.Range) < 3 {
			continue
		}
		to := definedIn(idx, occ.Symbol)
		if to == "" || to == doc.RelativePath {
			continue
		}
		line := int(occ.Range[0])
		if line < 0 || line >= len(lines) {
			continue
		}
		kind := importStatic
		if tsSpecifierAt(lines[line], occ.Range) {
			kind = classifyTSImport(lines, line, utf16Prefix(lines[line], int(occ.Range[1])))
		}
		g.add(doc.RelativePath, to, kind, "typescript")
	}
}

// tsSpecifierAt reports whether the occurrence range on line covers a quoted
// string, as a module specifier does. When the file changed after indexing,
// the range points elsewhere and the import's kind cannot be read: the edge
// then counts as static. A stale index may report a cycle that a type-only
// import breaks; it must not hide one.
func tsSpecifierAt(line string, rng []int32) bool {
	if len(rng) != 3 {
		return false // multi-line range: no specifier spans lines
	}
	u := utf16.Encode([]rune(line))
	start, end := int(rng[1]), int(rng[2])
	if start < 0 || end > len(u) || end-start < 2 {
		return false
	}
	q := u[start]
	return (q == '"' || q == '\'' || q == '`') && u[end-1] == q
}

// utf16Prefix returns the part of s before UTF-16 column col (SCIP's default
// position encoding).
func utf16Prefix(s string, col int) string {
	u := utf16.Encode([]rune(s))
	if col > len(u) {
		col = len(u)
	}
	if col < 0 {
		col = 0
	}
	return string(utf16.Decode(u[:col]))
}

// classifyTSImport classifies the import whose module specifier starts after
// before (the text of line in front of it). Zero for module references that
// are no import: jest.mock("…"), declare module "…".
func classifyTSImport(lines []string, line int, before string) importKind {
	s := strings.TrimRight(before, " \t")
	if s == "" {
		return 0
	}
	if q := s[len(s)-1]; q == '"' || q == '\'' || q == '`' {
		s = strings.TrimRight(s[:len(s)-1], " \t")
	}
	switch {
	case strings.HasSuffix(s, "("):
		s = strings.TrimRight(s[:len(s)-1], " \t")
		switch {
		case endsWithWord(s, "import"):
			return importLazy
		case endsWithWord(s, "require"):
			// `import x = require("…")` and CommonJS require: loaded on the spot.
			return importStatic
		}
		return 0
	case endsWithWord(s, "from"):
		stmt := tsStatementBefore(lines, line, s)
		if tsTypeOnly(stmt) {
			return importType
		}
		return importStatic
	case endsWithWord(s, "import"):
		// Side-effect import: import "./polyfill"
		return importStatic
	}
	return 0
}

func endsWithWord(s, word string) bool {
	if !strings.HasSuffix(s, word) {
		return false
	}
	i := len(s) - len(word)
	return i == 0 || !isIdentByte(s[i-1])
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || b == '.' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// tsStatementBefore joins the import/export statement that ends in head (the
// text of line before the specifier), walking back over the lines of a
// multi-line import to the one starting with import or export.
func tsStatementBefore(lines []string, line int, head string) string {
	parts := []string{head}
	cur := strings.TrimSpace(head)
	for l := line - 1; l >= 0 && l >= line-200; l-- {
		if strings.HasPrefix(cur, "import") || strings.HasPrefix(cur, "export") {
			break
		}
		cur = strings.TrimSpace(lines[l])
		parts = append(parts, cur)
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	stmt := strings.Join(parts, " ")
	// The statement starts at the last import/export keyword on its first line.
	if i := firstKeyword(stmt); i > 0 {
		stmt = stmt[i:]
	}
	return strings.TrimSpace(stmt)
}

func firstKeyword(s string) int {
	best := -1
	for _, kw := range []string{"import", "export"} {
		for i := 0; ; {
			j := strings.Index(s[i:], kw)
			if j < 0 {
				break
			}
			at := i + j
			if (at == 0 || !isIdentByte(s[at-1])) && (at+len(kw) == len(s) || !isIdentByte(s[at+len(kw)])) {
				// Keep the first statement keyword: "import { a } from" has
				// no second one; "export { x } from" neither. Stop at the first.
				if best < 0 || at < best {
					best = at
				}
				break
			}
			i = at + len(kw)
		}
	}
	return best
}

// tsTypeOnly reports whether an import/export … from statement imports types
// only: `import type …`, `export type …`, or braces whose every name is
// marked `type` with no default or namespace import beside them.
func tsTypeOnly(stmt string) bool {
	rest := ""
	switch {
	case strings.HasPrefix(stmt, "import"):
		rest = strings.TrimSpace(stmt[len("import"):])
	case strings.HasPrefix(stmt, "export"):
		rest = strings.TrimSpace(stmt[len("export"):])
	default:
		return false
	}
	if strings.HasPrefix(rest, "type ") || strings.HasPrefix(rest, "type{") {
		return true
	}
	if !strings.HasPrefix(rest, "{") {
		return false
	}
	end := strings.Index(rest, "}")
	if end < 0 {
		return false
	}
	names := strings.Split(rest[1:end], ",")
	seen := 0
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		seen++
		if !strings.HasPrefix(n, "type ") {
			return false
		}
	}
	return seen > 0
}

// pyImportStmt is one Python import statement and the kind its position gives.
type pyImportStmt struct {
	first, last int
	kind        importKind
}

// addPythonDocument adds the imports of a Python document: references inside
// an import statement to symbols defined in other files.
func (g *importGraph) addPythonDocument(idx *scip.SCIPIndex, doc *scip.Document, lines []string) {
	stmts := pythonImportStatements(lines)
	if len(stmts) == 0 {
		return
	}
	kindAt := make(map[int]importKind, len(stmts))
	for _, st := range stmts {
		for l := st.first; l <= st.last; l++ {
			kindAt[l] = st.kind
		}
	}
	for _, occ := range doc.Occurrences {
		if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || len(occ.Range) < 3 {
			continue
		}
		kind, ok := kindAt[int(occ.Range[0])]
		if !ok {
			continue
		}
		if to := definedIn(idx, occ.Symbol); to != "" && to != doc.RelativePath {
			g.add(doc.RelativePath, to, kind, "python")
		}
	}
}

// pythonModuleSuffixes maps module symbols that scip-python references but
// never defines to the file defining them. In a src layout it defines a module
// by its path ("`src.pkg.b`/__init__:") and references it by its import name
// ("`pkg.b`/__init__:"), so `import pkg.b` and `from pkg import b` resolve to
// nothing. Each defined module registers every dotted suffix under its own
// package; a suffix two files share is dropped.
func pythonModuleSuffixes(idx *scip.SCIPIndex) map[string]string {
	out := map[string]string{}
	ambiguous := map[string]bool{}
	for sym, def := range idx.DefinitionIndex {
		if def == nil || def.Doc == nil || importLanguage(def.Doc.RelativePath) != "py" {
			continue
		}
		prefix, module, ok := pythonModuleSymbol(sym)
		if !ok {
			continue
		}
		parts := strings.Split(module, ".")
		for i := 1; i < len(parts); i++ {
			key := prefix + pythonModuleDescriptor(strings.Join(parts[i:], "."))
			if _, defined := idx.DefinitionIndex[key]; defined || ambiguous[key] {
				continue
			}
			if prev, seen := out[key]; seen && prev != def.Doc.RelativePath {
				delete(out, key)
				ambiguous[key] = true
				continue
			}
			out[key] = def.Doc.RelativePath
		}
	}
	return out
}

// pythonModuleSymbol splits a scip-python module symbol
// "scip-python python <pkg> <version> `a.b`/__init__:" into its prefix up to
// the descriptor and the dotted module name.
func pythonModuleSymbol(sym string) (prefix, module string, ok bool) {
	if !strings.HasPrefix(sym, "scip-python ") || !strings.HasSuffix(sym, "/__init__:") {
		return "", "", false
	}
	head := strings.TrimSuffix(sym, "/__init__:")
	sp := strings.LastIndexByte(head, ' ')
	if sp < 0 {
		return "", "", false
	}
	prefix, module = head[:sp+1], head[sp+1:]
	if strings.HasPrefix(module, "`") && strings.HasSuffix(module, "`") && len(module) >= 2 {
		module = module[1 : len(module)-1]
	}
	if module == "" || strings.ContainsAny(module, "`/ ") {
		return "", "", false
	}
	return prefix, module, true
}

// pythonModuleDescriptor writes a module name the way scip-python does:
// backquoted when it has a dot.
func pythonModuleDescriptor(module string) string {
	if strings.Contains(module, ".") {
		return "`" + module + "`/__init__:"
	}
	return module + "/__init__:"
}

// pythonImportStatements finds the import statements of a Python file with
// their line span and kind: inside a def → lazy, inside `if TYPE_CHECKING:`
// → type, else static (module level, class body, try/except, if).
func pythonImportStatements(lines []string) []pyImportStmt {
	type block struct {
		indent int
		kind   string // "def", "typecheck", "other"
	}
	var stack []block
	var out []pyImportStmt
	for l := 0; l < len(lines); l++ {
		raw := strings.TrimRight(lines[l], " \t\r")
		trimmed := strings.TrimLeft(raw, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(trimmed)
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import") {
			kind := importStatic
			for _, b := range stack {
				if b.kind == "def" {
					kind = importLazy
					break
				}
				if b.kind == "typecheck" {
					kind = importType
				}
			}
			last := l
			open := strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
			for (open > 0 || strings.HasSuffix(strings.TrimRight(lines[last], " \t\r"), "\\")) && last+1 < len(lines) {
				last++
				open += strings.Count(lines[last], "(") - strings.Count(lines[last], ")")
			}
			out = append(out, pyImportStmt{first: l, last: last, kind: kind})
			l = last
			continue
		}
		if strings.HasSuffix(trimmed, ":") {
			kind := "other"
			switch {
			case strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def "):
				kind = "def"
			case strings.HasPrefix(trimmed, "if TYPE_CHECKING") || strings.HasPrefix(trimmed, "if typing.TYPE_CHECKING"):
				kind = "typecheck"
			}
			stack = append(stack, block{indent: indent, kind: kind})
		}
	}
	return out
}

// runtimeCycles returns the cycles among static imports, largest first, with
// each cycle's nodes sorted.
func (g *importGraph) runtimeCycles() []cycles.Cycle {
	if g == nil {
		return nil
	}
	adj := map[string][]string{}
	meta := map[[2]string]cycles.EdgeMeta{}
	nodeSet := map[string]bool{}
	for from, tos := range g.edges {
		for to, kind := range tos {
			if kind&importStatic == 0 {
				continue
			}
			adj[from] = append(adj[from], to)
			meta[[2]string{from, to}] = cycles.EdgeMeta{Strength: g.sites[from][to]}
			nodeSet[from], nodeSet[to] = true, true
		}
	}
	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for k := range adj {
		sort.Strings(adj[k])
	}
	res := cycles.NewDetector().Detect(nodes, adj, meta, cycles.DetectOptions{})
	out := res.Cycles
	for i := range out {
		sort.Strings(out[i].Nodes)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Nodes[0] < out[j].Nodes[0]
	})
	return out
}

// checkedLanguages lists the languages whose imports the graph holds.
func (g *importGraph) checkedLanguages() []string {
	if g == nil {
		return nil
	}
	var out []string
	for l := range g.languages {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}
