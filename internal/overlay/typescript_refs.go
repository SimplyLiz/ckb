//go:build cgo

package overlay

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Layer 2 for TypeScript: references on added lines, resolved exactly or
// left without an edge. An identifier gets an edge when the syntax and the
// index say which symbol it is:
//
//   - a top-level declaration of the same file that no enclosing scope
//     shadows;
//   - an imported name, followed through re-exports (three levels), and the
//     import's module string, which scip-typescript points at the file;
//   - ns.x for a namespace import, E.A for an enum, C.x for a class's
//     static member, new C() for a class with a constructor.
//
// Anything else that could name a symbol of this repository (a member of a
// value whose type is not spelled, a JSX attribute of a component, an
// import alias) is counted as unresolved. Locals, globals and names from
// external packages are neither.

// tsSymbolDocs gives the current document of a path: the overlay's when the
// file changed, else the base index's.
type tsSymbolDocs func(path string) *scip.Document

type tsResolver struct {
	root    string
	docs    tsSymbolDocs
	paths   []tsPathRule
	modules map[string]*tsModule
	// byPrefix maps a file symbol to its path, for the modules loaded.
	byPrefix map[string]string
}

type tsPathRule struct {
	prefix, suffix string   // the pattern around its "*" (no "*": prefix is the whole)
	star           bool     // the pattern has a "*"
	targets        []string // repo-relative, with "*" where the match goes
}

// tsExports is what a module exports beyond its own declarations.
type tsExports struct {
	named       map[string]tsExportRef // exported name -> where it comes from
	stars       []string               // export * from "spec"
	defaultName string                 // export default <identifier> or a named declaration
}

type tsExportRef struct {
	spec string // module specifier, "" for a local name
	name string // the name in that module, or the local name
}

func newTSResolver(root string, docs tsSymbolDocs) *tsResolver {
	r := &tsResolver{root: root, docs: docs, modules: map[string]*tsModule{}, byPrefix: map[string]string{}}
	r.paths = readTSPaths(root)
	return r
}

var jsoncComment = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
var jsoncTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// readTSPaths reads compilerOptions.paths (and baseUrl) of the repository's
// tsconfig.json. "extends" is not followed.
func readTSPaths(root string) []tsPathRule {
	data, err := os.ReadFile(filepath.Join(root, "tsconfig.json")) // #nosec G304 -- the repository's tsconfig
	if err != nil {
		return nil
	}
	data = jsoncTrailingComma.ReplaceAll(stripJSONCComments(data), []byte("$1"))
	var cfg struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	base := path.Clean(filepath.ToSlash(cfg.CompilerOptions.BaseURL))
	if cfg.CompilerOptions.BaseURL == "" {
		base = "."
	}
	var rules []tsPathRule
	for pat, targets := range cfg.CompilerOptions.Paths {
		rule := tsPathRule{prefix: pat}
		if i := strings.IndexByte(pat, '*'); i >= 0 {
			rule = tsPathRule{prefix: pat[:i], suffix: pat[i+1:], star: true}
		}
		for _, t := range targets {
			rule.targets = append(rule.targets, path.Join(base, t))
		}
		rules = append(rules, rule)
	}
	return rules
}

// stripJSONCComments removes comments outside strings.
func stripJSONCComments(data []byte) []byte {
	var out []byte
	inStr := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inStr {
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) && (data[i+1] == '/' || data[i+1] == '*') {
			loc := jsoncComment.FindIndex(data[i:])
			if loc != nil && loc[0] == 0 {
				i += loc[1] - 1
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// resolveModule returns the repo-relative path of the indexed file spec
// names from the file at from, "" when it is external or not indexed.
func (r *tsResolver) resolveModule(from, spec string) string {
	var cands []string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
		cands = []string{path.Join(path.Dir(from), spec)}
	default:
		for _, rule := range r.paths {
			switch {
			case rule.star && strings.HasPrefix(spec, rule.prefix) && strings.HasSuffix(spec, rule.suffix) && len(spec) >= len(rule.prefix)+len(rule.suffix):
				m := spec[len(rule.prefix) : len(spec)-len(rule.suffix)]
				for _, t := range rule.targets {
					cands = append(cands, strings.Replace(t, "*", m, 1))
				}
			case !rule.star && spec == rule.prefix:
				cands = append(cands, rule.targets...)
			}
		}
	}
	for _, c := range cands {
		if p := r.indexedFile(path.Clean(c)); p != "" {
			return p
		}
	}
	return ""
}

var tsResolveExts = []string{".ts", ".tsx", ".d.ts", ".mts", ".cts"}

// indexedFile finds the file module resolution picks for p: p itself, p
// with an extension, a .js specifier's .ts file, or p/index.
func (r *tsResolver) indexedFile(p string) string {
	try := []string{p}
	for _, e := range tsResolveExts {
		try = append(try, p+e)
	}
	for _, js := range []string{".js", ".jsx", ".mjs", ".cjs"} {
		if strings.HasSuffix(p, js) {
			stem := strings.TrimSuffix(p, js)
			for _, e := range tsResolveExts {
				try = append(try, stem+e)
			}
		}
	}
	for _, e := range tsResolveExts {
		try = append(try, p+"/index"+e)
	}
	for _, c := range try {
		if r.docs(c) != nil {
			return c
		}
	}
	return ""
}

// tsNS is the namespace a name is looked up in.
type tsNS int

const (
	tsValue tsNS = iota
	tsTypeNS
)

// lookup finds the one top-level symbol named name that file declares (or
// re-exports) in namespace ns. Empty when there is none or more than one.
func (r *tsResolver) lookup(file, name string, ns tsNS, depth int) string {
	if depth > 3 || file == "" {
		return ""
	}
	m := r.module(file)
	if m == nil {
		return ""
	}
	if tops := m.decls[name]; len(tops) > 0 {
		return pickTop(m.prefix, tops, ns)
	}
	if ref, ok := m.named[name]; ok {
		if ref.spec == "" {
			// export { a } of an imported a.
			if imp, ok := m.imports[ref.name]; ok {
				return r.importTarget(file, imp, ns, depth+1)
			}
			return ""
		}
		return r.lookup(r.resolveModule(file, ref.spec), ref.name, ns, depth+1)
	}
	found := ""
	for _, spec := range m.stars {
		if s := r.lookup(r.resolveModule(file, spec), name, ns, depth+1); s != "" {
			if found != "" && found != s {
				return ""
			}
			found = s
		}
	}
	return found
}

// pickTop chooses among the declarations of one name the one in namespace
// ns; empty when that is not exactly one symbol.
func pickTop(prefix string, tops []tsTop, ns tsNS) string {
	sym := ""
	for _, t := range tops {
		if ns == tsValue && !t.value || ns == tsTypeNS && !t.typ {
			continue
		}
		if sym != "" && sym != prefix+t.suffix {
			return ""
		}
		sym = prefix + t.suffix
	}
	return sym
}

// importTarget resolves an import binding to the symbol it names.
func (r *tsResolver) importTarget(from string, imp tsImport, ns tsNS, depth int) string {
	target := r.resolveModule(from, imp.spec)
	if target == "" {
		return ""
	}
	switch imp.name {
	case "*":
		return ""
	case "default":
		m := r.module(target)
		if m == nil || m.defaultName == "" {
			return ""
		}
		return r.lookup(target, m.defaultName, ns, depth)
	}
	return r.lookup(target, imp.name, ns, depth)
}

// tsTop is a top-level declaration: its descriptor and the namespaces it is
// in (a class, an enum and a namespace are both a value and a type).
type tsTop struct {
	suffix     string
	value, typ bool
}

// tsModule is what Layer 2 needs to know about a file: its symbol prefix,
// top-level declarations, imports and exports.
type tsModule struct {
	prefix  string
	decls   map[string][]tsTop
	all     map[string]bool // every synthesized suffix: "C#m().", "E#A."
	imports map[string]tsImport
	tsExports
}

// tsTopDecls indexes the top-level declarations among decls by name.
func tsTopDecls(decls []tsDecl) map[string][]tsTop {
	out := map[string][]tsTop{}
	for _, d := range decls {
		name, kind, ok := tsSplitTop(d.suffix)
		if !ok {
			continue
		}
		t := tsTop{suffix: d.suffix}
		switch kind {
		case "().", ".":
			t.value = true
		case "#":
			t.typ, t.value = true, d.classOrEnum
		case "/":
			t.typ, t.value = true, true
		}
		out[name] = append(out[name], t)
	}
	return out
}

// tsSplitTop splits a one-descriptor suffix ("f().", "x.", "C#", "N/") into
// the unescaped name and the descriptor's kind.
func tsSplitTop(suffix string) (string, string, bool) {
	for _, k := range []string{"().", ".", "#", "/"} {
		if !strings.HasSuffix(suffix, k) {
			continue
		}
		n := strings.TrimSuffix(suffix, k)
		if strings.HasPrefix(n, "`") && strings.HasSuffix(n, "`") && len(n) >= 2 {
			inner := n[1 : len(n)-1]
			if strings.Contains(strings.ReplaceAll(inner, "``", ""), "`") {
				return "", "", false
			}
			return strings.ReplaceAll(inner, "``", "`"), k, true
		}
		if n == "" || strings.ContainsAny(n, "#/.()[]`:") {
			return "", "", false
		}
		return n, k, true
	}
	return "", "", false
}

// tsImport is one import binding: the module and the name imported
// ("default", "*" for a namespace import).
type tsImport struct {
	spec, name string
}

func tsImports(root *sitter.Node, src []byte) map[string]tsImport {
	out := map[string]tsImport{}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		st := root.NamedChild(i)
		if st.Type() != "import_statement" {
			continue
		}
		srcNode := st.ChildByFieldName("source")
		if srcNode == nil {
			continue
		}
		spec := tsStringValue(srcNode.Content(src))
		for j := 0; j < int(st.NamedChildCount()); j++ {
			clause := st.NamedChild(j)
			if clause.Type() != "import_clause" {
				continue
			}
			for k := 0; k < int(clause.NamedChildCount()); k++ {
				c := clause.NamedChild(k)
				switch c.Type() {
				case "identifier":
					out[c.Content(src)] = tsImport{spec, "default"}
				case "namespace_import":
					for m := 0; m < int(c.NamedChildCount()); m++ {
						if id := c.NamedChild(m); id.Type() == "identifier" {
							out[id.Content(src)] = tsImport{spec, "*"}
						}
					}
				case "named_imports":
					for m := 0; m < int(c.NamedChildCount()); m++ {
						s := c.NamedChild(m)
						if s.Type() != "import_specifier" {
							continue
						}
						name := s.ChildByFieldName("name")
						if name == nil {
							continue
						}
						local := name
						if a := s.ChildByFieldName("alias"); a != nil {
							local = a
						}
						out[local.Content(src)] = tsImport{spec, name.Content(src)}
					}
				}
			}
		}
	}
	return out
}

// module parses file (once) and returns what Layer 2 needs from it. Nil
// when the file is not indexed, unreadable, or has a syntax error.
func (r *tsResolver) module(file string) *tsModule {
	if m, ok := r.modules[file]; ok {
		return m
	}
	r.modules[file] = nil
	prefix := tsFileSymbol(r.docs(file))
	if prefix == "" {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(file))) // #nosec G304 -- a file in the repository
	if err != nil {
		return nil
	}
	tree, root, _ := tsParse(file, src)
	if tree == nil {
		return nil
	}
	defer tree.Close()
	w := &tsWalker{src: src, cols: newUTF16Cols(src)}
	w.statements(root, "")
	m := &tsModule{prefix: prefix, decls: tsTopDecls(w.decls), imports: tsImports(root, src), all: map[string]bool{}}
	for _, d := range w.decls {
		m.all[d.suffix] = true
	}
	m.named = map[string]tsExportRef{}
	tsReadExports(root, src, &m.tsExports)
	r.modules[file] = m
	r.byPrefix[prefix] = file
	return m
}

// tsReadExports reads a module's re-exports and default export.
func tsReadExports(root *sitter.Node, src []byte, ex *tsExports) {
	for i := 0; i < int(root.NamedChildCount()); i++ {
		st := root.NamedChild(i)
		if st.Type() != "export_statement" {
			continue
		}
		spec := ""
		if s := st.ChildByFieldName("source"); s != nil {
			spec = tsStringValue(s.Content(src))
		}
		isDefault := false
		for j := 0; j < int(st.ChildCount()); j++ {
			if st.Child(j).Type() == "default" {
				isDefault = true
			}
		}
		if d := st.ChildByFieldName("declaration"); d != nil {
			if isDefault {
				if n := d.ChildByFieldName("name"); n != nil {
					ex.defaultName = n.Content(src)
				}
			}
			continue
		}
		if v := st.ChildByFieldName("value"); v != nil && isDefault {
			switch v.Type() {
			case "identifier":
				ex.defaultName = v.Content(src)
			case "function_declaration", "function_expression", "class_declaration", "class":
				if n := v.ChildByFieldName("name"); n != nil {
					ex.defaultName = n.Content(src)
				}
			}
			continue
		}
		hasClause := false
		for j := 0; j < int(st.NamedChildCount()); j++ {
			c := st.NamedChild(j)
			switch c.Type() {
			case "export_clause":
				hasClause = true
				for k := 0; k < int(c.NamedChildCount()); k++ {
					s := c.NamedChild(k)
					if s.Type() != "export_specifier" {
						continue
					}
					name := s.ChildByFieldName("name")
					if name == nil {
						continue
					}
					exported := name
					if a := s.ChildByFieldName("alias"); a != nil {
						exported = a
					}
					ex.named[exported.Content(src)] = tsExportRef{spec: spec, name: name.Content(src)}
				}
			case "namespace_export":
				hasClause = true // export * as ns from: not followed
			}
		}
		if !hasClause && spec != "" {
			ex.stars = append(ex.stars, spec)
		}
	}
}

// tsStringValue strips the quotes of a string literal's source text.
func tsStringValue(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') {
		return s[1 : len(s)-1]
	}
	return s
}

// tsLayer2 resolves the references on the given lines of a TypeScript file
// and adds them to doc. It returns how many it added and how many sites it
// left without an edge.
func tsLayer2(doc *scip.Document, file string, src []byte, lines map[int32]bool, r *tsResolver) (added, unresolved int) {
	tree, root, _ := tsParse(file, src)
	if tree == nil {
		return 0, 0
	}
	defer tree.Close()
	self := r.module(file)
	if self == nil {
		return 0, 0
	}
	l := &tsRefs{r: r, file: file, src: src, self: self, cols: newUTF16Cols(src), lines: lines, doc: doc, taken: map[[2]int32]bool{}}
	for _, o := range doc.Occurrences {
		l.taken[[2]int32{o.Range[0], o.Range[1]}] = true
	}
	l.visit(root)
	return l.added, l.unresolved
}

type tsRefs struct {
	r     *tsResolver
	file  string
	src   []byte
	self  *tsModule
	cols  *utf16Cols
	lines map[int32]bool
	doc   *scip.Document
	taken map[[2]int32]bool

	added, unresolved int
}

func (l *tsRefs) text(n *sitter.Node) string { return n.Content(l.src) }

func (l *tsRefs) onAdded(n *sitter.Node) bool {
	return l.lines[int32(n.StartPoint().Row)] // #nosec G115 -- line numbers fit
}

func (l *tsRefs) emit(n *sitter.Node, sym string) {
	sl, sc := l.cols.pos(n.StartByte())
	el, ec := l.cols.pos(n.EndByte())
	k := [2]int32{sl, sc}
	if l.taken[k] {
		return
	}
	l.taken[k] = true
	l.doc.Occurrences = append(l.doc.Occurrences, &scip.Occurrence{
		Range:  scipRange(sl, sc, el, ec),
		Symbol: sym,
		Source: scip.OccurrenceSourceOverlay,
	})
	l.added++
}

func (l *tsRefs) miss(n *sitter.Node) {
	if l.onAdded(n) {
		l.unresolved++
	}
}

func (l *tsRefs) visit(n *sitter.Node) {
	switch n.Type() {
	case "import_statement":
		l.importOrExportFrom(n)
		return
	case "export_statement":
		if n.ChildByFieldName("source") != nil {
			l.importOrExportFrom(n)
			return
		}
	case "member_expression":
		l.member(n)
		if obj := n.ChildByFieldName("object"); obj != nil {
			l.visit(obj)
		}
		return
	case "nested_type_identifier":
		l.qualifiedType(n)
		return
	case "jsx_attribute":
		l.jsxAttribute(n)
		for i := 1; i < int(n.NamedChildCount()); i++ {
			l.visit(n.NamedChild(i))
		}
		return
	case "identifier", "type_identifier", "shorthand_property_identifier":
		l.ident(n)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		l.visit(n.NamedChild(i))
	}
}

// resolved is where a name points: its symbol and the file declaring it.
type tsResolved struct {
	sym, file string
	external  bool   // from a package outside the repository
	namespace string // the file of a namespace import ("* as ns")
}

// resolveName resolves a free name of this file in namespace ns.
func (l *tsRefs) resolveName(name string, ns tsNS) (tsResolved, bool) {
	if imp, ok := l.self.imports[name]; ok {
		target := l.r.resolveModule(l.file, imp.spec)
		if target == "" {
			return tsResolved{external: true}, true
		}
		if imp.name == "*" {
			return tsResolved{namespace: target}, true
		}
		sym := l.r.importTarget(l.file, imp, ns, 0)
		return tsResolved{sym: sym, file: l.r.byPrefix[tsSymPrefix(sym)]}, true
	}
	if tops := l.self.decls[name]; len(tops) > 0 {
		return tsResolved{sym: pickTop(l.self.prefix, tops, ns), file: l.file}, true
	}
	return tsResolved{}, false // a global
}

// tsSymPrefix is the file symbol a top-level symbol starts with.
func tsSymPrefix(sym string) string {
	i := strings.LastIndex(strings.TrimRight(sym, ".#/()"), "/")
	if i < 0 {
		return ""
	}
	return sym[:i+1]
}

func (l *tsRefs) ident(n *sitter.Node) {
	if !l.onAdded(n) || tsIsDeclarationName(n) {
		return
	}
	name := l.text(n)
	ns := tsValue
	if n.Type() == "type_identifier" {
		ns = tsTypeNS
	}
	if tsBoundLocally(n, name, l.src) {
		return
	}
	res, ok := l.resolveName(name, ns)
	if !ok || res.external || res.namespace != "" {
		return
	}
	if res.sym == "" {
		l.unresolved++
		return
	}
	sym := res.sym
	if p := n.Parent(); p != nil && p.Type() == "new_expression" && strings.HasSuffix(sym, "#") {
		if c := p.ChildByFieldName("constructor"); c != nil && c.StartByte() == n.StartByte() {
			if m := l.r.module(res.file); m != nil && m.all[strings.TrimPrefix(sym, m.prefix)+"`<constructor>`()."] {
				sym += "`<constructor>`()."
			}
		}
	}
	l.emit(n, sym)
}

// member handles obj.prop: a namespace import's export, an enum member, a
// class's static member, a namespace's member. Other receivers are values
// whose type the syntax does not spell.
func (l *tsRefs) member(n *sitter.Node) {
	prop := n.ChildByFieldName("property")
	obj := n.ChildByFieldName("object")
	if prop == nil || obj == nil || !l.onAdded(prop) {
		return
	}
	if prop.Type() != "property_identifier" {
		l.unresolved++
		return
	}
	pname := l.text(prop)
	root := obj
	for root.Type() == "member_expression" || root.Type() == "call_expression" || root.Type() == "non_null_expression" || root.Type() == "parenthesized_expression" {
		next := root.ChildByFieldName("object")
		if root.Type() == "call_expression" {
			next = root.ChildByFieldName("function")
		}
		if next == nil && root.NamedChildCount() > 0 {
			next = root.NamedChild(0)
		}
		if next == nil {
			break
		}
		root = next
	}
	if root.Type() != "identifier" {
		l.unresolved++ // this.x, literals, …
		return
	}
	rname := l.text(root)
	if tsBoundLocally(root, rname, l.src) {
		l.unresolved++
		return
	}
	res, ok := l.resolveName(rname, tsValue)
	if !ok || res.external {
		return // a global or a package: not this repository's symbols
	}
	if obj.Type() != "identifier" {
		l.unresolved++ // a chain on a repository value
		return
	}
	if res.namespace != "" {
		if sym := l.r.lookup(res.namespace, pname, tsValue, 0); sym != "" {
			l.emit(prop, sym)
			return
		}
		l.unresolved++
		return
	}
	if res.sym == "" || !(strings.HasSuffix(res.sym, "#") || strings.HasSuffix(res.sym, "/")) {
		l.unresolved++ // a member of a variable or a function's result
		return
	}
	m := l.r.module(res.file)
	if m == nil {
		l.unresolved++
		return
	}
	owner := strings.TrimPrefix(res.sym, m.prefix)
	var hit string
	for _, k := range []string{".", "()."} {
		if s := owner + tsEscape(pname) + k; m.all[s] {
			if hit != "" {
				l.unresolved++
				return
			}
			hit = m.prefix + s
		}
	}
	if hit == "" {
		l.unresolved++ // inherited, or declared where the parse does not reach
		return
	}
	l.emit(prop, hit)
}

// qualifiedType handles ns.T in a type: the module part resolved like a
// value, the name looked up in it.
func (l *tsRefs) qualifiedType(n *sitter.Node) {
	mod := n.ChildByFieldName("module")
	name := n.ChildByFieldName("name")
	if mod == nil || name == nil {
		return
	}
	if mod.Type() == "identifier" {
		l.ident(mod)
	}
	if !l.onAdded(name) {
		return
	}
	if mod.Type() != "identifier" || tsBoundLocally(mod, l.text(mod), l.src) {
		l.unresolved++
		return
	}
	res, ok := l.resolveName(l.text(mod), tsValue)
	switch {
	case !ok || res.external:
	case res.namespace != "":
		if sym := l.r.lookup(res.namespace, l.text(name), tsTypeNS, 0); sym != "" {
			l.emit(name, sym)
		} else {
			l.unresolved++
		}
	default:
		if m := l.r.module(res.file); m != nil && strings.HasSuffix(res.sym, "/") {
			if s := strings.TrimPrefix(res.sym, m.prefix) + tsEscape(l.text(name)) + "#"; m.all[s] {
				l.emit(name, m.prefix+s)
				return
			}
		}
		l.unresolved++
	}
}

// jsxAttribute counts the attribute of a component from this repository as
// unresolved: its symbol is a member of the props type, often a type
// literal's counter-numbered one.
func (l *tsRefs) jsxAttribute(n *sitter.Node) {
	if n.NamedChildCount() == 0 || !l.onAdded(n) {
		return
	}
	el := n.Parent()
	if el == nil {
		return
	}
	tag := el.ChildByFieldName("name")
	if tag == nil || tag.Type() != "identifier" {
		return
	}
	if res, ok := l.resolveName(l.text(tag), tsValue); ok && !res.external {
		l.unresolved++
	}
}

// importOrExportFrom handles import … from "spec" and export … from "spec":
// the module string points at the file, each named specifier at the symbol.
func (l *tsRefs) importOrExportFrom(n *sitter.Node) {
	srcNode := n.ChildByFieldName("source")
	if srcNode == nil {
		return
	}
	target := l.r.resolveModule(l.file, tsStringValue(l.text(srcNode)))
	if target == "" {
		return // a package, or a file the index does not have
	}
	if l.onAdded(srcNode) {
		if fs := tsFileSymbol(l.r.docs(target)); fs != "" {
			l.emit(srcNode, fs)
		}
	}
	var specs func(c *sitter.Node)
	specs = func(c *sitter.Node) {
		switch c.Type() {
		case "import_specifier", "export_specifier":
			name := c.ChildByFieldName("name")
			if name == nil || !l.onAdded(name) {
				return
			}
			v := l.r.lookup(target, l.text(name), tsValue, 0)
			t := l.r.lookup(target, l.text(name), tsTypeNS, 0)
			switch {
			case v != "" && t != "" && v != t:
				l.unresolved++
			case v != "":
				l.emit(name, v)
			case t != "":
				l.emit(name, t)
			default:
				l.unresolved++
			}
			if a := c.ChildByFieldName("alias"); a != nil && l.onAdded(a) {
				l.unresolved++ // scip-typescript's occurrence on an alias: not modeled
			}
			return
		case "import_clause":
			for i := 0; i < int(c.NamedChildCount()); i++ {
				if d := c.NamedChild(i); d.Type() == "identifier" && l.onAdded(d) {
					if sym := l.r.importTarget(l.file, tsImport{spec: tsStringValue(l.text(srcNode)), name: "default"}, tsValue, 0); sym != "" {
						l.emit(d, sym)
					} else {
						l.unresolved++
					}
				}
			}
		}
		for i := 0; i < int(c.NamedChildCount()); i++ {
			specs(c.NamedChild(i))
		}
	}
	specs(n)
}

// tsDeclNameParents are the node types whose "name" field declares a name.
var tsDeclNameParents = map[string]bool{
	"function_declaration": true, "generator_function_declaration": true, "function_signature": true,
	"function_expression": true, "function": true, "generator_function": true,
	"class_declaration": true, "abstract_class_declaration": true, "class": true,
	"interface_declaration": true, "type_alias_declaration": true, "enum_declaration": true,
	"variable_declarator": true, "method_definition": true, "method_signature": true,
	"abstract_method_signature": true, "public_field_definition": true, "property_signature": true,
	"type_parameter": true, "internal_module": true, "module": true, "enum_assignment": true,
}

// tsIsDeclarationName reports whether n is the name a declaration or a
// binding pattern introduces (Layer 1's, or a local).
func tsIsDeclarationName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	if tsDeclNameParents[p.Type()] {
		if name := p.ChildByFieldName("name"); name != nil && name.StartByte() == n.StartByte() {
			return true
		}
	}
	switch p.Type() {
	case "required_parameter", "optional_parameter":
		pat := p.ChildByFieldName("pattern")
		return pat != nil && pat.StartByte() == n.StartByte()
	case "arrow_function":
		par := p.ChildByFieldName("parameter")
		return par != nil && par.StartByte() == n.StartByte()
	case "array_pattern", "rest_pattern", "object_pattern", "catch_clause", "enum_body":
		return true
	case "pair_pattern":
		v := p.ChildByFieldName("value")
		return v != nil && v.StartByte() == n.StartByte()
	case "assignment_pattern", "object_assignment_pattern":
		left := p.ChildByFieldName("left")
		return left != nil && left.StartByte() == n.StartByte()
	case "for_in_statement":
		left := p.ChildByFieldName("left")
		return left != nil && left.StartByte() == n.StartByte()
	case "import_specifier", "import_clause", "namespace_import", "export_specifier":
		return true
	}
	return false
}

// tsBoundLocally reports whether an enclosing scope below the file binds
// name: a parameter, a local variable, function, class or type, a type
// parameter, an infer or mapped-type variable, a catch or loop binding.
// Conservative: any binding of the name in any namespace counts, so a
// shadowed name never gets the top-level symbol's edge.
func tsBoundLocally(n *sitter.Node, name string, src []byte) bool {
	for a := n.Parent(); a != nil && a.Type() != "program"; a = a.Parent() {
		switch a.Type() {
		case "function_declaration", "generator_function_declaration", "function_expression", "function",
			"generator_function", "arrow_function", "method_definition", "function_signature", "method_signature",
			"abstract_method_signature", "call_signature", "construct_signature", "function_type", "constructor_type":
			if params := a.ChildByFieldName("parameters"); params != nil && tsPatternBinds(params, name, src) {
				return true
			}
			if p := a.ChildByFieldName("parameter"); p != nil && p.Content(src) == name {
				return true
			}
			if tsTypeParamsBind(a, name, src) {
				return true
			}
			if a.Type() == "function_expression" || a.Type() == "function" {
				if fn := a.ChildByFieldName("name"); fn != nil && fn.Content(src) == name {
					return true
				}
			}
			if body := a.ChildByFieldName("body"); body != nil && tsVarBinds(body, name, src) {
				return true
			}
		case "statement_block", "switch_body", "class_body":
			if p := a.Parent(); p != nil && (p.Type() == "internal_module" || p.Type() == "module") {
				return true // inside a namespace: its members are not file-level names
			}
			if tsBlockBinds(a, name, src) {
				return true
			}
		case "for_statement":
			if init := a.ChildByFieldName("initializer"); init != nil && tsPatternBinds(init, name, src) {
				return true
			}
		case "for_in_statement":
			if left := a.ChildByFieldName("left"); left != nil && tsPatternBinds(left, name, src) {
				return true
			}
		case "catch_clause":
			if p := a.ChildByFieldName("parameter"); p != nil && tsPatternBinds(p, name, src) {
				return true
			}
		case "class_declaration", "abstract_class_declaration", "class", "interface_declaration", "type_alias_declaration":
			if tsTypeParamsBind(a, name, src) {
				return true
			}
			if a.Type() == "class" {
				if cn := a.ChildByFieldName("name"); cn != nil && cn.Content(src) == name {
					return true
				}
			}
			if a.Type() == "type_alias_declaration" || a.Type() == "interface_declaration" {
				if tsTypeVarBinds(a, name, src) {
					return true
				}
			}
		}
	}
	return false
}

func tsTypeParamsBind(a *sitter.Node, name string, src []byte) bool {
	tp := a.ChildByFieldName("type_parameters")
	if tp == nil {
		return false
	}
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		if n := tp.NamedChild(i).ChildByFieldName("name"); n != nil && n.Content(src) == name {
			return true
		}
	}
	return false
}

// tsTypeVarBinds finds infer X and [K in …] variables named name under a
// type declaration.
func tsTypeVarBinds(a *sitter.Node, name string, src []byte) bool {
	found := false
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if found {
			return
		}
		switch n.Type() {
		case "infer_type", "mapped_type_clause":
			for i := 0; i < int(n.NamedChildCount()); i++ {
				if c := n.NamedChild(i); c.Type() == "type_identifier" && c.Content(src) == name {
					found = true
					return
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(a)
	return found
}

// tsPatternBinds reports whether a parameter list, declaration or pattern
// binds name.
func tsPatternBinds(n *sitter.Node, name string, src []byte) bool {
	switch n.Type() {
	case "identifier", "shorthand_property_identifier_pattern":
		return n.Content(src) == name
	case "pair_pattern":
		if v := n.ChildByFieldName("value"); v != nil {
			return tsPatternBinds(v, name, src)
		}
		return false
	case "assignment_pattern", "object_assignment_pattern":
		if left := n.ChildByFieldName("left"); left != nil {
			return tsPatternBinds(left, name, src)
		}
		return false
	case "variable_declarator":
		if v := n.ChildByFieldName("name"); v != nil {
			return tsPatternBinds(v, name, src)
		}
		return false
	case "required_parameter", "optional_parameter":
		if v := n.ChildByFieldName("pattern"); v != nil {
			return tsPatternBinds(v, name, src)
		}
		return false
	case "type_annotation", "string", "number", "call_expression", "arrow_function", "function_expression":
		return false
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if tsPatternBinds(n.NamedChild(i), name, src) {
			return true
		}
	}
	return false
}

// tsBlockBinds checks the declarations directly in a block.
func tsBlockBinds(b *sitter.Node, name string, src []byte) bool {
	for i := 0; i < int(b.NamedChildCount()); i++ {
		if tsStatementBinds(b.NamedChild(i), name, src) {
			return true
		}
	}
	return false
}

func tsStatementBinds(st *sitter.Node, name string, src []byte) bool {
	switch st.Type() {
	case "lexical_declaration", "variable_declaration":
		for j := 0; j < int(st.NamedChildCount()); j++ {
			if d := st.NamedChild(j); d.Type() == "variable_declarator" && tsPatternBinds(d, name, src) {
				return true
			}
		}
	case "function_declaration", "generator_function_declaration", "class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration", "enum_declaration":
		if n := st.ChildByFieldName("name"); n != nil && n.Content(src) == name {
			return true
		}
	case "export_statement":
		if d := st.ChildByFieldName("declaration"); d != nil {
			return tsStatementBinds(d, name, src)
		}
	}
	return false
}

// tsVarBinds finds var declarations of name anywhere in a function body
// (they are function-scoped), not entering nested functions.
func tsVarBinds(body *sitter.Node, name string, src []byte) bool {
	found := false
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if found {
			return
		}
		switch n.Type() {
		case "function_declaration", "function_expression", "function", "arrow_function", "method_definition", "class", "class_declaration":
			return
		case "variable_declaration":
			for j := 0; j < int(n.NamedChildCount()); j++ {
				if d := n.NamedChild(j); d.Type() == "variable_declarator" && tsPatternBinds(d, name, src) {
					found = true
					return
				}
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(body)
	return found
}
