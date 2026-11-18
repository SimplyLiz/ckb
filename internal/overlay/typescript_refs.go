//go:build cgo

package overlay

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"gopkg.in/yaml.v3"

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
	modules map[string]*tsModule
	// projects are the tsconfig projects scip-typescript indexes, the root
	// first, read once.
	projects []tsProject
	// workspaces are the repository's own packages by name (package.json
	// workspaces, pnpm-workspace.yaml), read once.
	workspaces map[string]tsWorkspace
	// byPrefix maps a file symbol to its path, for the modules loaded.
	byPrefix map[string]string
	tyCache  map[tsTyKey]tsTy
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
	return &tsResolver{root: root, docs: docs, modules: map[string]*tsModule{}, byPrefix: map[string]string{}, tyCache: map[tsTyKey]tsTy{}}
}

// close frees the parse trees of the modules loaded.
func (r *tsResolver) close() {
	for _, m := range r.modules {
		if m != nil && m.tree != nil {
			m.tree.Close()
			m.tree = nil
		}
	}
}

var jsoncComment = regexp.MustCompile(`(?s)//[^\n]*|/\*.*?\*/`)
var jsoncTrailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// strictNull reports whether strictNullChecks governs file (the project
// pathsFor picks; projects of one directory that disagree count as strict).
// Without it TypeScript drops undefined and null from unions, so a
// literal's context T | undefined is T. No tsconfig: strict, as before.
func (r *tsResolver) strictNull(file string) bool {
	cands := r.projectsFor(file)
	for _, p := range cands {
		if p.strictNull {
			return true
		}
	}
	return len(cands) == 0
}

// projectsFor is the deepest referenced projects whose directory holds
// file, else the root.
func (r *tsResolver) projectsFor(file string) []tsProject {
	if r.projects == nil {
		r.projects = r.readProjects()
	}
	dir := path.Dir(file)
	best := -1
	var cands []tsProject
	for _, p := range r.projects[1:] {
		if p.dir != "." && dir != p.dir && !strings.HasPrefix(dir, p.dir+"/") {
			continue
		}
		d := 0
		if p.dir != "." {
			d = strings.Count(p.dir, "/") + 1
		}
		switch {
		case d > best:
			best, cands = d, []tsProject{p}
		case d == best:
			cands = append(cands, p)
		}
	}
	if len(cands) == 0 {
		return r.projects[:1]
	}
	return cands
}

// pathsFor is the compilerOptions.paths that govern file the way ckb index
// runs scip-typescript: once at the root, so the root tsconfig.json's
// project and the projects it references (recursively). A file takes the
// deepest referenced project whose directory holds it, else the root's. A
// package's tsconfig that nothing references is not read: scip-typescript
// does not see it either.
func (r *tsResolver) pathsFor(file string) []tsPathRule {
	cands := r.projectsFor(file)
	// Projects of one directory (tsconfig.app.json, tsconfig.node.json): the
	// paths those with any agree on.
	var agreed []tsPathRule
	for _, p := range cands {
		if p.rules == nil {
			continue
		}
		if agreed != nil && !tsSameRules(agreed, p.rules) {
			return nil
		}
		agreed = p.rules
	}
	return agreed
}

// tsProject is a tsconfig project scip-typescript indexes: its directory,
// paths rules and whether strictNullChecks is on.
type tsProject struct {
	dir        string
	rules      []tsPathRule
	strictNull bool
}

// readProjects reads the root tsconfig.json and the projects it references;
// the root comes first.
func (r *tsResolver) readProjects() []tsProject {
	root, ok := r.readConfig("tsconfig.json", 0)
	if !ok {
		return []tsProject{{dir: ".", strictNull: true}}
	}
	out := []tsProject{{dir: ".", rules: tsRules(root), strictNull: root.strictNull()}}
	seen := map[string]bool{"tsconfig.json": true}
	var walk func(refs []string, depth int)
	walk = func(refs []string, depth int) {
		if depth > 5 {
			return
		}
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			c, ok := r.readConfig(ref, 0)
			if !ok {
				continue
			}
			out = append(out, tsProject{dir: path.Dir(ref), rules: tsRules(c), strictNull: c.strictNull()})
			walk(c.refs, depth+1)
		}
	}
	walk(root.refs, 0)
	return out
}

// tsConfigFile is what Layer 2 reads of a tsconfig: its paths and baseUrl,
// resolved against the file that sets them, after what it extends.
type tsConfigFile struct {
	paths    map[string][]string
	pathsDir string // the config's directory that set paths
	baseURL  string // repo-relative, "" when unset
	refs     []string
	// strict and strictNullChecks as set, after what it extends; nil
	// when unset.
	strict, strictNullChecks *bool
}

func (c tsConfigFile) strictNull() bool {
	if c.strictNullChecks != nil {
		return *c.strictNullChecks
	}
	return c.strict != nil && *c.strict
}

func tsRules(c tsConfigFile) []tsPathRule {
	if c.paths == nil {
		return nil
	}
	base := c.baseURL
	if base == "" {
		base = c.pathsDir
	}
	keys := make([]string, 0, len(c.paths))
	for pat := range c.paths {
		keys = append(keys, pat)
	}
	sort.Strings(keys)
	var rules []tsPathRule
	for _, pat := range keys {
		rule := tsPathRule{prefix: pat}
		if i := strings.IndexByte(pat, '*'); i >= 0 {
			rule = tsPathRule{prefix: pat[:i], suffix: pat[i+1:], star: true}
		}
		for _, t := range c.paths[pat] {
			rule.targets = append(rule.targets, path.Join(base, t))
		}
		rules = append(rules, rule)
	}
	return rules
}

func tsSameRules(a, b []tsPathRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].prefix != b[i].prefix || a[i].suffix != b[i].suffix || a[i].star != b[i].star || strings.Join(a[i].targets, "\x00") != strings.Join(b[i].targets, "\x00") {
			return false
		}
	}
	return true
}

// readConfig reads a tsconfig (repo-relative path) and what it extends:
// a relative path, or a package's config under node_modules. A child's
// paths replace its parent's; baseUrl and paths are relative to the
// config that sets them.
func (r *tsResolver) readConfig(cfg string, depth int) (tsConfigFile, bool) {
	if depth > 5 {
		return tsConfigFile{}, false
	}
	data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(cfg))) // #nosec G304 -- a tsconfig in the repository
	if err != nil {
		return tsConfigFile{}, false
	}
	data = jsoncTrailingComma.ReplaceAll(stripJSONCComments(data), []byte("$1"))
	var raw struct {
		Extends         json.RawMessage `json:"extends"`
		CompilerOptions struct {
			BaseURL          *string             `json:"baseUrl"`
			Paths            map[string][]string `json:"paths"`
			Strict           *bool               `json:"strict"`
			StrictNullChecks *bool               `json:"strictNullChecks"`
		} `json:"compilerOptions"`
		References []struct {
			Path string `json:"path"`
		} `json:"references"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return tsConfigFile{}, false
	}
	dir := path.Dir(cfg)
	var out tsConfigFile
	var exts []string
	if len(raw.Extends) > 0 {
		var one string
		if json.Unmarshal(raw.Extends, &one) == nil {
			exts = []string{one}
		} else {
			_ = json.Unmarshal(raw.Extends, &exts) // TypeScript 5.0: an array, later entries win
		}
	}
	for _, e := range exts {
		if p := r.extendsPath(dir, e); p != "" {
			if parent, ok := r.readConfig(p, depth+1); ok {
				if parent.paths != nil {
					out.paths, out.pathsDir = parent.paths, parent.pathsDir
				}
				if parent.baseURL != "" {
					out.baseURL = parent.baseURL
				}
				if parent.strict != nil {
					out.strict = parent.strict
				}
				if parent.strictNullChecks != nil {
					out.strictNullChecks = parent.strictNullChecks
				}
			}
		}
	}
	if raw.CompilerOptions.BaseURL != nil {
		out.baseURL = path.Clean(path.Join(dir, filepath.ToSlash(*raw.CompilerOptions.BaseURL)))
	}
	if raw.CompilerOptions.Paths != nil {
		out.paths, out.pathsDir = raw.CompilerOptions.Paths, dir
	}
	if raw.CompilerOptions.Strict != nil {
		out.strict = raw.CompilerOptions.Strict
	}
	if raw.CompilerOptions.StrictNullChecks != nil {
		out.strictNullChecks = raw.CompilerOptions.StrictNullChecks
	}
	for _, ref := range raw.References {
		p := path.Join(dir, filepath.ToSlash(ref.Path))
		if !strings.HasSuffix(p, ".json") {
			p = path.Join(p, "tsconfig.json")
		}
		out.refs = append(out.refs, p)
	}
	return out, true
}

// extendsPath finds the config an extends entry names, repo-relative; ""
// when it is not in the repository.
func (r *tsResolver) extendsPath(dir, e string) string {
	exists := func(p string) bool {
		st, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(p)))
		return err == nil && !st.IsDir()
	}
	var cands []string
	if strings.HasPrefix(e, "./") || strings.HasPrefix(e, "../") {
		p := path.Join(dir, e)
		cands = []string{p, p + ".json"}
	} else {
		for d := dir; ; d = path.Dir(d) {
			p := path.Join(d, "node_modules", e)
			cands = append(cands, p, p+".json", path.Join(p, "tsconfig.json"))
			if d == "." || d == "/" || d == "" {
				break
			}
		}
	}
	for _, c := range cands {
		if !strings.HasPrefix(c, "../") && exists(c) {
			return c
		}
	}
	return ""
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
		for _, rule := range r.pathsFor(from) {
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
	if !strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "/") {
		return r.workspaceModule(spec)
	}
	return ""
}

// tsWorkspace is a package of the repository's workspace.
type tsWorkspace struct {
	dir string // repo-relative
	pkg tsPackageJSON
}

type tsPackageJSON struct {
	Name    string          `json:"name"`
	Main    string          `json:"main"`
	Types   string          `json:"types"`
	Typings string          `json:"typings"`
	Exports json.RawMessage `json:"exports"`
}

// workspaceModule resolves a package specifier to a file of a workspace
// package, as TypeScript does through the package manager's symlink:
// exports (types, import, default conditions), else types, typings, main.
func (r *tsResolver) workspaceModule(spec string) string {
	if r.workspaces == nil {
		r.workspaces = r.readWorkspaces()
	}
	name, sub := spec, ""
	parts := strings.SplitN(spec, "/", 3)
	switch {
	case strings.HasPrefix(spec, "@") && len(parts) >= 2:
		name = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			sub = parts[2]
		}
	case len(parts) >= 2:
		name = parts[0]
		sub = strings.TrimPrefix(spec, name+"/")
	}
	ws, ok := r.workspaces[name]
	if !ok {
		return ""
	}
	key := "."
	if sub != "" {
		key = "./" + sub
	}
	var target string
	if len(ws.pkg.Exports) > 0 {
		target = tsExportTarget(ws.pkg.Exports, key)
		if target == "" {
			return "" // exports does not export the subpath
		}
	} else if sub != "" {
		target = sub
	} else {
		for _, t := range []string{ws.pkg.Types, ws.pkg.Typings, ws.pkg.Main, "index"} {
			if t != "" {
				target = t
				break
			}
		}
	}
	return r.indexedFile(path.Clean(path.Join(ws.dir, target)))
}

// tsExportTarget looks key ("." or "./sub") up in a package.json exports
// field: a string, a subpath map (with "*" patterns) or conditions.
func tsExportTarget(raw json.RawMessage, key string) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if key == "." {
			return s
		}
		return ""
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	subpaths := false
	for k := range m {
		if strings.HasPrefix(k, ".") {
			subpaths = true
		}
	}
	if !subpaths {
		if key != "." {
			return ""
		}
		return tsCondition(raw)
	}
	if v, ok := m[key]; ok {
		return tsCondition(v)
	}
	for k, v := range m {
		i := strings.IndexByte(k, '*')
		if i < 0 || !strings.HasPrefix(key, k[:i]) || !strings.HasSuffix(key, k[i+1:]) || len(key) < len(k)-1 {
			continue
		}
		star := key[i : len(key)-len(k[i+1:])]
		if t := tsCondition(v); t != "" {
			return strings.ReplaceAll(t, "*", star)
		}
	}
	return ""
}

// tsCondition picks a target from a string or a conditions object, in the
// order TypeScript's bundler resolution prefers: types, import, default.
func tsCondition(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	for _, c := range []string{"types", "import", "default"} {
		if v, ok := m[c]; ok {
			if t := tsCondition(v); t != "" {
				return t
			}
		}
	}
	return ""
}

// readWorkspaces lists the workspace packages: the globs of package.json's
// workspaces (an array or {packages}) and of pnpm-workspace.yaml.
func (r *tsResolver) readWorkspaces() map[string]tsWorkspace {
	var globs []string
	if data, err := os.ReadFile(filepath.Join(r.root, "package.json")); err == nil { // #nosec G304 -- the repository's package.json
		var pj struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(data, &pj) == nil && len(pj.Workspaces) > 0 {
			var list []string
			if json.Unmarshal(pj.Workspaces, &list) != nil {
				var obj struct {
					Packages []string `json:"packages"`
				}
				if json.Unmarshal(pj.Workspaces, &obj) == nil {
					list = obj.Packages
				}
			}
			globs = append(globs, list...)
		}
	}
	if data, err := os.ReadFile(filepath.Join(r.root, "pnpm-workspace.yaml")); err == nil { // #nosec G304 -- the repository's workspace file
		var ws struct {
			Packages []string `yaml:"packages"`
		}
		if yaml.Unmarshal(data, &ws) == nil {
			globs = append(globs, ws.Packages...)
		}
	}
	out := map[string]tsWorkspace{}
	for _, g := range globs {
		if strings.HasPrefix(g, "!") {
			continue
		}
		g = strings.TrimSuffix(strings.TrimPrefix(path.Clean(g), "./"), "/")
		if strings.Contains(g, "**") {
			continue // deep globs: not followed
		}
		dirs, _ := filepath.Glob(filepath.Join(r.root, filepath.FromSlash(g)))
		for _, d := range dirs {
			data, err := os.ReadFile(filepath.Join(d, "package.json")) // #nosec G304 -- a workspace package.json
			if err != nil {
				continue
			}
			var pkg tsPackageJSON
			if json.Unmarshal(data, &pkg) != nil || pkg.Name == "" {
				continue
			}
			rel, err := filepath.Rel(r.root, d)
			if err != nil {
				continue
			}
			if _, dup := out[pkg.Name]; dup {
				out[pkg.Name] = tsWorkspace{} // two packages of one name: neither
				continue
			}
			out[pkg.Name] = tsWorkspace{dir: filepath.ToSlash(rel), pkg: pkg}
		}
	}
	for n, w := range out {
		if w.dir == "" {
			delete(out, n)
		}
	}
	return out
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

// starLookup is the value export name of file as scip-typescript resolves
// typeof import("m").name: a declaration of the module, or one reached
// through export * from. A name some module on the way re-exports by name
// (export { a } from "./a") is a local in its index: local is true.
func (r *tsResolver) starLookup(file, name string, depth int) (sym string, local bool) {
	if depth > 3 || file == "" {
		return "", false
	}
	m := r.module(file)
	if m == nil {
		return "", false
	}
	if tops := m.decls[name]; len(tops) > 0 {
		return pickTop(m.prefix, tops, tsValue), false
	}
	if _, ok := m.named[name]; ok {
		return "", true
	}
	for _, spec := range m.stars {
		s, loc := r.starLookup(r.resolveModule(file, spec), name, depth+1)
		if loc {
			return "", true
		}
		if s != "" {
			if sym != "" && sym != s {
				return "", false
			}
			sym = s
		}
	}
	return sym, false
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
	file    string
	src     []byte
	tree    *sitter.Tree
	root    *sitter.Node
	prefix  string
	decls   map[string][]tsTop
	all     map[string]bool // every synthesized suffix: "C#m().", "E#A."
	imports map[string]tsImport
	tsExports
	// params is the first parameter of each top-level function, what a
	// component's JSX attributes name.
	params map[string]tsParamShape
	defs   map[string]bool // definition suffixes in the file's document, read once
	// nodes are the top-level declarations by suffix ("f().", "T#", "x."
	// for a variable's declarator); overloaded, the functions with overloads.
	nodes      map[string]*sitter.Node
	overloaded map[string]bool
	// defPos are the document's definitions by start, cols its UTF-16
	// columns: read once, for members whose owner Layer 1 does not name.
	defPos map[[2]int32]string
	cols   *utf16Cols
	// keyPos are its object literal keys' property symbols (zod's schemas).
	keyPos map[[2]int32]string
}

// tsParamShape is a function's first parameter, what JSX attributes of a
// component name: an object type spelled in place (literal, the owner of its
// members' IDs) or a named type (typeName, resolved in the declaring file).
type tsParamShape struct {
	literal  string
	typeName string
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
	src, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(file))) // #nosec G304 -- a file in the repository
	if err != nil {
		r.modules[file] = nil
		return nil
	}
	return r.load(file, src)
}

// load parses file's source (once) for module.
func (r *tsResolver) load(file string, src []byte) *tsModule {
	if m, ok := r.modules[file]; ok {
		return m
	}
	r.modules[file] = nil
	prefix := tsFileSymbol(r.docs(file))
	if prefix == "" {
		return nil
	}
	tree, root, _ := tsParse(file, src)
	if tree == nil {
		return nil
	}
	w := &tsWalker{src: src, cols: newUTF16Cols(src)}
	w.statements(root, "")
	m := &tsModule{file: file, src: src, tree: tree, root: root, prefix: prefix, decls: tsTopDecls(w.decls), imports: tsImports(root, src), all: map[string]bool{}}
	for _, d := range w.decls {
		m.all[d.suffix] = true
	}
	m.named = map[string]tsExportRef{}
	tsReadExports(root, src, &m.tsExports)
	m.params = tsReadShapes(root, src)
	m.nodes, m.overloaded = tsTopNodes(root, src)
	r.modules[file] = m
	r.byPrefix[prefix] = file
	return m
}

// tsTopNodes indexes a file's top-level declarations by suffix. A suffix
// two declarations share (merged interfaces, a class and an interface) is
// left out.
func tsTopNodes(root *sitter.Node, src []byte) (map[string]*sitter.Node, map[string]bool) {
	nodes := map[string]*sitter.Node{}
	dup := map[string]bool{}
	over := map[string]bool{}
	put := func(s string, n *sitter.Node) {
		if _, ok := nodes[s]; ok {
			dup[s] = true
		}
		nodes[s] = n
	}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		st := root.NamedChild(i)
		if st.Type() == "export_statement" {
			d := st.ChildByFieldName("declaration")
			if d == nil {
				continue
			}
			st = d
		}
		name := st.ChildByFieldName("name")
		switch st.Type() {
		case "function_declaration", "generator_function_declaration":
			if name != nil {
				put(tsMethod(name.Content(src)), st)
			}
		case "function_signature":
			if name != nil {
				over[tsMethod(name.Content(src))] = true
			}
		case "class_declaration", "abstract_class_declaration", "interface_declaration", "type_alias_declaration", "enum_declaration":
			if name != nil {
				put(tsType(name.Content(src)), st)
			}
		case "lexical_declaration", "variable_declaration":
			for j := 0; j < int(st.NamedChildCount()); j++ {
				d := st.NamedChild(j)
				if n := d.ChildByFieldName("name"); d.Type() == "variable_declarator" && n != nil && n.Type() == "identifier" {
					put(tsTerm(n.Content(src)), d)
				}
			}
		}
	}
	for s := range dup {
		delete(nodes, s)
	}
	return nodes, over
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
	self := r.load(file, src)
	if self == nil || !bytes.Equal(self.src, src) {
		return 0, 0 // no index document, a syntax error, or a source other than the module's
	}
	l := &tsRefs{r: r, file: file, src: src, self: self, cols: newUTF16Cols(src), lines: lines, doc: doc, taken: map[tsTaken]bool{}, noValue: map[uint32]bool{}}
	for _, o := range doc.Occurrences {
		l.taken[tsTaken{o.Range[0], o.Range[1], o.Symbol}] = true
	}
	l.visit(self.root)
	return l.added, l.unresolved
}

// tsTaken is an occurrence already in the document: a position and a symbol
// (a shorthand property names both the property and the value).
type tsTaken struct {
	line, col int32
	sym       string
}

type tsRefs struct {
	r     *tsResolver
	file  string
	src   []byte
	self  *tsModule
	cols  *utf16Cols
	lines map[int32]bool
	doc   *scip.Document
	taken map[tsTaken]bool
	// noValue are the shorthand properties ({ a }) whose name stands for
	// the contextual type's member, or might: scip-typescript writes the
	// member there, the value only when the context has no such property.
	noValue map[uint32]bool
	// defs are the document's non-local definitions by start position.
	defs map[[2]int32]string

	added, unresolved int
}

func (l *tsRefs) text(n *sitter.Node) string { return n.Content(l.src) }

// defSymbolAt is the non-local symbol the document defines at a binding
// identifier (a parameter of a top-level function or a method, or a type
// parameter), else empty.
func (l *tsRefs) defSymbolAt(id *sitter.Node) string {
	if id == nil {
		return ""
	}
	if l.defs == nil {
		l.defs = map[[2]int32]string{}
		for _, o := range l.doc.Occurrences {
			if o.SymbolRoles&scip.SymbolRoleDefinition != 0 && !strings.HasPrefix(o.Symbol, "local ") {
				l.defs[[2]int32{o.Range[0], o.Range[1]}] = o.Symbol
			}
		}
	}
	sl, sc := l.cols.pos(id.StartByte())
	sym := l.defs[[2]int32{sl, sc}]
	if !strings.HasSuffix(sym, ")") && !strings.HasSuffix(sym, "]") {
		return "" // only parameters and type parameters: f().(p), f().[T]
	}
	return sym
}

func (l *tsRefs) onAdded(n *sitter.Node) bool {
	return l.lines[int32(n.StartPoint().Row)] // #nosec G115 -- line numbers fit
}

func (l *tsRefs) emit(n *sitter.Node, sym string) {
	sl, sc := l.cols.pos(n.StartByte())
	el, ec := l.cols.pos(n.EndByte())
	k := tsTaken{sl, sc, sym}
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

// missValue counts a shorthand property whose value would be a repository
// symbol.
func (l *tsRefs) missValue(k *sitter.Node) {
	if !l.onAdded(k) {
		return
	}
	name := l.text(k)
	if id, local := tsLocalBinding(k, name, l.src); local {
		if strings.HasSuffix(l.defSymbolAt(id), ")") {
			l.unresolved++ // a parameter with a global symbol, f().(p)
		}
		return
	}
	if _, imported := l.self.imports[name]; imported || tsBoundLocally(k, name, l.src) {
		return // a local, or an import (a local in the index either way)
	}
	if res, ok := l.resolveName(name, tsValue); ok && !res.external && res.sym != "" {
		l.unresolved++
	}
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
		if p := n.Parent(); p != nil && p.Type() == "program" {
			l.exportClause(n)
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
	case "object":
		l.objectKeys(n)
	case "variable_declarator":
		l.declPattern(n)
	case "call_expression":
		l.dynamicImport(n)
	case "required_parameter", "optional_parameter":
		l.paramPattern(n)
	case "lookup_type":
		l.lookupType(n)
	case "identifier", "type_identifier", "shorthand_property_identifier":
		l.ident(n)
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		l.visit(n.NamedChild(i))
	}
}

// lookupType handles T["k"]: scip-typescript writes T's member k at the
// string, quotes included. A union of types or of keys gets nothing there.
func (l *tsRefs) lookupType(n *sitter.Node) {
	key, ok := tsLookupKey(n, l.src)
	if !ok || !l.onAdded(key) {
		return
	}
	t := l.r.typeOfNode(l.self, n.NamedChild(0), 0)
	if len(t.parts) > 0 && !t.inter {
		return // (A | B)["k"]: the index has nothing
	}
	switch mb := l.r.memberOf(t, tsStringValue(l.text(key)), 0); mb.k {
	case tyDecl:
		if len(mb.more) > 0 {
			l.unresolved++
			return
		}
		l.emit(key, mb.sym)
	case tyOutside:
	default:
		l.unresolved++
	}
}

// tsLookupKey is the string literal key of a lookup type T["k"].
func tsLookupKey(n *sitter.Node, src []byte) (*sitter.Node, bool) {
	if n.NamedChildCount() != 2 {
		return nil, false
	}
	idx := n.NamedChild(1)
	if idx.Type() != "literal_type" || idx.NamedChildCount() != 1 || idx.NamedChild(0).Type() != "string" {
		return nil, false
	}
	return idx.NamedChild(0), true
}

// resolved is where a name points: its symbol and the file declaring it.
type tsResolved struct {
	sym, file string
	external  bool   // from a package outside the repository
	namespace string // the file of a namespace import ("* as ns")
}

// resolveName resolves a free name of this file in namespace ns.
func (l *tsRefs) resolveName(name string, ns tsNS) (tsResolved, bool) {
	return l.r.resolveIn(l.file, name, ns)
}

// resolveIn resolves a free name of file in namespace ns: an import, a
// top-level declaration, or (false) a global.
func (r *tsResolver) resolveIn(file, name string, ns tsNS) (tsResolved, bool) {
	m := r.module(file)
	if m == nil {
		return tsResolved{}, false
	}
	if imp, ok := m.imports[name]; ok {
		target := r.resolveModule(file, imp.spec)
		if target == "" {
			return tsResolved{external: true}, true
		}
		if imp.name == "*" {
			return tsResolved{namespace: target}, true
		}
		sym := r.importTarget(file, imp, ns, 0)
		return tsResolved{sym: sym, file: r.byPrefix[tsSymPrefix(sym)]}, true
	}
	if tops := m.decls[name]; len(tops) > 0 {
		return tsResolved{sym: pickTop(m.prefix, tops, ns), file: file}, true
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
	if !l.onAdded(n) || tsIsDeclarationName(n) || l.noValue[n.StartByte()] {
		return
	}
	name := l.text(n)
	ns := tsValue
	if n.Type() == "type_identifier" {
		ns = tsTypeNS
	}
	if tsIntrinsicTag(n, name) {
		return // <table>: JSX.IntrinsicElements' member, whatever value has the name
	}
	if ns == tsTypeNS {
		// A type parameter with a global symbol: f().[T], C#[T].
		if tp := tsTypeParamBinding(n, name, l.src); tp != nil {
			if sym := l.defSymbolAt(tp); strings.HasSuffix(sym, "]") {
				l.emit(n, sym)
			}
			return
		}
	}
	if id, local := tsLocalBinding(n, name, l.src); local {
		// A parameter with a global symbol: f().(p), C#m().(p). As a
		// shorthand property ({ p }) scip-typescript writes the property, a
		// counter or a local there, never the parameter.
		if sym := l.defSymbolAt(id); strings.HasSuffix(sym, ")") && ns == tsValue && n.Type() != "shorthand_property_identifier" {
			l.emit(n, sym)
		}
		return
	}
	res, ok := l.resolveName(name, ns)
	if !ok || res.external || res.namespace != "" {
		return
	}
	if n.Type() == "shorthand_property_identifier" {
		if _, imported := l.self.imports[name]; imported {
			return // { user } of an imported user: scip-typescript writes a local there
		}
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
	if p := n.Parent(); p != nil && p.Type() == "type_query" {
		// typeof import("m").x: m's export x, followed through re-exports
		// as scip-typescript does. (As a value, import("m") is a promise.)
		if target, ok := l.importCall(obj); ok {
			if target == "" {
				return // a package
			}
			s, local := l.r.starLookup(target, pname, 0)
			if s != "" {
				l.emit(prop, s)
				return
			}
			if local {
				return
			}
			l.unresolved++
			return
		}
	}
	switch t := l.r.exprType(l.self, obj, 0); t.k {
	case tyDecl, tyLit, tyPick, tyObj:
		l.memberEdgeRef(prop, obj, t)
		return
	case tyUnknown:
		if t.parts != nil {
			l.memberEdgeRef(prop, obj, t) // a union or an intersection
			return
		}
		l.unresolved++
		return
	case tyNone:
		// No type: a namespace's, an enum's or a class's static member, below.
	default:
		return // a member of a primitive, a global's or a package's type, an array, a function
	}
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

// paramPattern points the names a destructured parameter takes at the
// members of its type: the spelled one ({ a }: T, { a }: { a: X }), else the
// one its function's context gives it (rows.map(({ label }) => …)).
func (l *tsRefs) paramPattern(p *sitter.Node) {
	pat := p.ChildByFieldName("pattern")
	if pat == nil || pat.Type() != "object_pattern" || !l.patternOnAdded(pat) {
		return
	}
	var t tsTy
	if typ := p.ChildByFieldName("type"); typ != nil {
		t = l.r.typeOfNode(l.self, typ, 0)
	} else {
		ps := p.Parent()
		if ps == nil || ps.Type() != "formal_parameters" {
			return
		}
		i := 0
		for k := 0; k < int(ps.NamedChildCount()); k++ {
			c := ps.NamedChild(k)
			if c.StartByte() == p.StartByte() {
				break
			}
			if c.Type() == "required_parameter" || c.Type() == "optional_parameter" {
				i++
			}
		}
		t = l.r.paramOf(l.r.contextType(l.self, ps.Parent(), 0), i, 0)
	}
	l.patternKeys(pat, t)
}

// declPattern handles const { a, b: c } = e: the names point at the members
// of e's type, or at the exports of the module of await import("m").
func (l *tsRefs) declPattern(d *sitter.Node) {
	pat, v := d.ChildByFieldName("name"), d.ChildByFieldName("value")
	if pat == nil || v == nil || pat.Type() != "object_pattern" || !l.patternOnAdded(pat) {
		return
	}
	if v.Type() == "await_expression" && v.NamedChildCount() == 1 {
		if target, ok := l.importCall(v.NamedChild(0)); ok {
			if target == "" {
				return // a package
			}
			// A name the module declares points at it; one it re-exports
			// (a barrel's export { a } from "./a") is a local in
			// scip-typescript's index.
			m := l.r.module(target)
			for _, k := range tsPatternKeys(pat) {
				if !l.onAdded(k) || m == nil {
					l.miss(k)
					continue
				}
				if tops := m.decls[l.text(k)]; len(tops) > 0 {
					if s := pickTop(m.prefix, tops, tsValue); s != "" {
						l.emit(k, s)
						continue
					}
				}
				if _, re := m.named[l.text(k)]; !re && len(m.stars) == 0 {
					l.unresolved++
				}
			}
			return
		}
	}
	var t tsTy
	if typ := d.ChildByFieldName("type"); typ != nil {
		t = l.r.typeOfNode(l.self, typ, 0)
	} else {
		t = l.r.exprType(l.self, v, 0)
	}
	l.patternKeys(pat, t)
}

func (l *tsRefs) patternOnAdded(pat *sitter.Node) bool {
	for _, k := range tsPatternKeys(pat) {
		if l.onAdded(k) {
			return true
		}
	}
	return false
}

// tsPatternKeys are the property names an object pattern takes, nested
// patterns' included.
func tsPatternKeys(pat *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < int(pat.NamedChildCount()); i++ {
		var k *sitter.Node
		switch c := pat.NamedChild(i); c.Type() {
		case "shorthand_property_identifier_pattern":
			k = c
		case "object_assignment_pattern":
			k = c.ChildByFieldName("left")
		case "pair_pattern":
			k = c.ChildByFieldName("key")
			if v := c.ChildByFieldName("value"); v != nil && v.Type() == "object_pattern" {
				out = append(out, tsPatternKeys(v)...)
			}
		}
		if k != nil && (k.Type() == "shorthand_property_identifier_pattern" || k.Type() == "property_identifier") {
			out = append(out, k)
		}
	}
	return out
}

// patternKeys points the keys of an object pattern at the members of t,
// descending into nested patterns ({ a: { b } }).
func (l *tsRefs) patternKeys(pat *sitter.Node, t tsTy) {
	for i := 0; i < int(pat.NamedChildCount()); i++ {
		var k, nested *sitter.Node
		switch c := pat.NamedChild(i); c.Type() {
		case "shorthand_property_identifier_pattern":
			k = c
		case "object_assignment_pattern":
			k = c.ChildByFieldName("left")
		case "pair_pattern":
			k = c.ChildByFieldName("key")
			if v := c.ChildByFieldName("value"); v != nil {
				if v.Type() == "assignment_pattern" {
					v = v.ChildByFieldName("left")
				}
				if v != nil && v.Type() == "object_pattern" {
					nested = v
				}
			}
		}
		if k == nil || k.Type() != "shorthand_property_identifier_pattern" && k.Type() != "property_identifier" {
			continue
		}
		mb := l.keyEdge(k, t)
		if nested != nil {
			l.patternKeys(nested, mb.typ)
		}
	}
}

// keyEdge gives a key on an added line the edge to t's member of its name,
// or counts it, and returns the member.
func (l *tsRefs) keyEdge(k *sitter.Node, t tsTy) tsMember {
	switch t.k {
	case tyNone, tyOutside, tyArray, tyPromise, tyRecord, tyMap, tyFn, tyCallback, tyStore:
		return tsMember{k: tyNone}
	case tyUnknown:
		l.miss(k)
		return tsMember{k: tyUnknown}
	}
	mb := l.r.memberOf(t, l.text(k), 0)
	switch mb.k {
	case tyDecl:
		if l.onAdded(k) {
			l.emit(k, mb.sym)
		}
	case tyOutside, tyNone:
	default:
		l.miss(k)
	}
	return mb
}

// memberEdgeRef is memberEdge for obj.prop: a union's member is every
// remaining part's declaration of it.
func (l *tsRefs) memberEdgeRef(prop, obj *sitter.Node, t tsTy) {
	switch mb := l.r.memberOfRef(l.self, t, l.text(prop), obj, 0); mb.k {
	case tyDecl:
		l.emit(prop, mb.sym)
		for _, s := range mb.more {
			l.emit(prop, s)
		}
	case tyOutside:
	default:
		l.unresolved++
	}
}

// memberEdge gives prop the edge to t's member of its name, or counts it.
func (l *tsRefs) memberEdge(prop *sitter.Node, t tsTy) {
	switch mb := l.r.memberOf(t, l.text(prop), 0); mb.k {
	case tyDecl:
		l.emit(prop, mb.sym)
	case tyOutside:
	default:
		l.unresolved++ // a member the parse does not see, or of a type not followed
	}
}

// importCall reports whether e is import("m") (also typeof import("m"),
// which tsParseText turned into a call), with m's file ("" for a package).
func (l *tsRefs) importCall(e *sitter.Node) (string, bool) {
	if e.Type() != "call_expression" {
		return "", false
	}
	fn, args := e.ChildByFieldName("function"), e.ChildByFieldName("arguments")
	if fn == nil || args == nil || l.text(fn) != "import" || args.NamedChildCount() != 1 || args.NamedChild(0).Type() != "string" {
		return "", false
	}
	return l.r.resolveModule(l.file, tsStringValue(l.text(args.NamedChild(0)))), true
}

// dynamicImport points the module string of import("m") at m's file, as
// scip-typescript does.
func (l *tsRefs) dynamicImport(e *sitter.Node) {
	target, ok := l.importCall(e)
	if !ok || target == "" {
		return
	}
	s := e.ChildByFieldName("arguments").NamedChild(0)
	if !l.onAdded(s) {
		return
	}
	if fs := tsFileSymbol(l.r.docs(target)); fs != "" {
		l.emit(s, fs)
	}
}

// tsParamOwner is the descriptor of the function a parameter belongs to,
// when its parameters' IDs are global: a top-level function or a method of
// a top-level class.
func tsParamOwner(p *sitter.Node, src []byte) (string, bool) {
	if p == nil || p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
		return "", false
	}
	ps := p.Parent()
	if ps == nil || ps.Type() != "formal_parameters" {
		return "", false
	}
	f := ps.Parent()
	if f == nil {
		return "", false
	}
	topLevel := func(n *sitter.Node) bool {
		a := n.Parent()
		if a != nil && a.Type() == "export_statement" {
			a = a.Parent()
		}
		return a != nil && a.Type() == "program"
	}
	name := f.ChildByFieldName("name")
	if name == nil {
		return "", false
	}
	switch f.Type() {
	case "function_declaration":
		if topLevel(f) {
			return tsMethod(name.Content(src)), true
		}
	case "method_definition":
		body := f.Parent()
		if body == nil || body.Type() != "class_body" || tsAccessor(f, name) != "" || !tsSimpleMemberName(name) {
			return "", false
		}
		cls := body.Parent()
		if cls == nil || cls.Type() != "class_declaration" && cls.Type() != "abstract_class_declaration" || !topLevel(cls) {
			return "", false
		}
		cn := cls.ChildByFieldName("name")
		if cn == nil {
			return "", false
		}
		m := name.Content(src)
		if m == "constructor" {
			m = "<constructor>"
		}
		return tsType(cn.Content(src)) + tsMethod(m), true
	}
	return "", false
}

// objectKeys points the keys of an object literal at the members of the
// type its context gives it: a variable's annotation, as/satisfies, a
// parameter, a return type, an enclosing key's member, an array's element.
// spreadsRepoValue reports whether an object literal spreads a value whose
// type is a repository type or not known.
func (l *tsRefs) spreadsRepoValue(obj *sitter.Node) bool {
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		c := obj.NamedChild(i)
		if c.Type() != "spread_element" || c.NamedChildCount() != 1 {
			continue
		}
		switch l.r.exprType(l.self, c.NamedChild(0), 0).k {
		case tyOutside, tyNone:
		default:
			return true
		}
	}
	return false
}

// tsCallbackResult reports whether e is what a function passed as a call's
// argument returns: its expression body or a return statement's value,
// through parentheses and conditionals.
func tsCallbackResult(e *sitter.Node) bool {
	for p := e.Parent(); p != nil; e, p = p, p.Parent() {
		switch p.Type() {
		case "parenthesized_expression", "ternary_expression":
			continue
		case "return_statement":
			for p = p.Parent(); p != nil && p.Type() != "arrow_function" && p.Type() != "function_expression" && p.Type() != "function"; p = p.Parent() {
				if p.Type() == "function_declaration" || p.Type() == "method_definition" || p.Type() == "program" {
					return false
				}
			}
		case "arrow_function":
			if b := p.ChildByFieldName("body"); b == nil || b.StartByte() != e.StartByte() {
				return false
			}
		default:
			return false
		}
		if p == nil {
			return false
		}
		a := p.Parent()
		return a != nil && a.Type() == "arguments"
	}
	return false
}

func (l *tsRefs) objectKeys(obj *sitter.Node) {
	var keys []*sitter.Node
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		var k *sitter.Node
		switch c := obj.NamedChild(i); c.Type() {
		case "pair", "method_definition":
			field := "key"
			if c.Type() == "method_definition" {
				field = "name"
			}
			k = c.ChildByFieldName(field)
		case "shorthand_property_identifier":
			k = c
		}
		if k != nil && (k.Type() == "property_identifier" || k.Type() == "shorthand_property_identifier") && l.onAdded(k) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	t := l.r.contextType(l.self, obj, 0)
	if t.k == tyRecord && !t.orPromise {
		// Record<K, V>, optional or not: a shorthand is the value when K
		// is string, a key's own symbol when K spells the keys; counted
		// either way.
		for _, k := range keys {
			if k.Type() == "shorthand_property_identifier" {
				l.noValue[k.StartByte()] = true
				l.missValue(k)
			}
		}
		return
	}
	if t.orPromise || t.nullable && l.r.strictNull(l.file) {
		return // the literal's keys are its own properties
	}
	if t.k == tyNone && l.spreadsRepoValue(obj) && tsCallbackResult(obj) {
		// arr.map((x) => ({ ...x, k })): scip-typescript points k at the
		// inferred result's property, which can be the spread value's.
		for _, k := range keys {
			l.miss(k)
		}
		return
	}
	for _, k := range keys {
		mb := l.keyEdge(k, t)
		if k.Type() != "shorthand_property_identifier" {
			continue
		}
		switch {
		case mb.k == tyDecl || mb.k == tyUnknown:
			l.noValue[k.StartByte()] = true
		case t.k == tyOutside && t.sym != "null":
			// A package's or a global's type (RequestInit, a library's
			// options): scip-typescript writes its member when it has the
			// property, the value when it does not. Which, the parse cannot
			// tell.
			l.noValue[k.StartByte()] = true
			l.missValue(k)
		}
	}
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
	res, ok := l.resolveName(l.text(tag), tsValue)
	if !ok || res.external {
		return
	}
	if name := n.NamedChild(0); name.Type() == "property_identifier" && strings.HasSuffix(res.sym, "().") {
		if s := l.r.propMember(res.file, res.sym, l.text(name)); s != "" {
			l.emit(name, s)
			return
		}
	}
	l.unresolved++
}

// literalMember finds owner + "typeLiteralN:" + name among the file's
// definitions: a type literal's member, numbered by scip-typescript's
// counter, which only the document knows.
func (r *tsResolver) literalMember(file, owner, name string) string {
	m := r.module(file)
	if m == nil {
		return ""
	}
	if m.defs == nil {
		m.defs = map[string]bool{}
		if d := r.docs(file); d != nil {
			for _, o := range d.Occurrences {
				if o.SymbolRoles&scip.SymbolRoleDefinition != 0 && strings.HasPrefix(o.Symbol, m.prefix) {
					m.defs[strings.TrimPrefix(o.Symbol, m.prefix)] = true
				}
			}
		}
	}
	esc := tsEscape(name)
	hit := ""
	for s := range m.defs {
		rest, ok := strings.CutPrefix(s, owner+"typeLiteral")
		if !ok {
			continue
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 || !strings.HasPrefix(rest[i:], ":") {
			continue
		}
		if tail := rest[i+1:]; tail != esc+"." && tail != esc+"()." {
			continue
		}
		if hit != "" {
			return ""
		}
		hit = m.prefix + s
	}
	return hit
}

// propMember finds what a JSX attribute name of the component fn (a
// top-level function of file) points at: the member of its first
// parameter's type.
func (r *tsResolver) propMember(file, fn, name string) string {
	m := r.module(file)
	if m == nil || !strings.HasPrefix(fn, m.prefix) {
		return ""
	}
	ps, ok := m.params[strings.TrimPrefix(fn, m.prefix)]
	switch {
	case !ok:
		return ""
	case ps.literal != "":
		return r.literalMember(file, ps.literal, name)
	}
	res, ok := r.resolveIn(file, ps.typeName, tsTypeNS)
	if !ok || res.external || !strings.HasSuffix(res.sym, "#") {
		return ""
	}
	t := r.typeBySymbol(res.sym, 0)
	if t.k != tyDecl && t.k != tyLit {
		return ""
	}
	if mb := r.memberOf(t, name, 0); mb.k == tyDecl {
		return mb.sym
	}
	return ""
}

// tsReadShapes reads the first parameter of each top-level function.
func tsReadShapes(root *sitter.Node, src []byte) map[string]tsParamShape {
	params := map[string]tsParamShape{}
	for i := 0; i < int(root.NamedChildCount()); i++ {
		st := root.NamedChild(i)
		if st.Type() == "export_statement" {
			if d := st.ChildByFieldName("declaration"); d != nil {
				st = d
			} else {
				for j := 0; j < int(st.NamedChildCount()); j++ {
					if c := st.NamedChild(j); c.Type() == "function_declaration" || c.Type() == "class_declaration" {
						st = c
					}
				}
			}
		}
		name := st.ChildByFieldName("name")
		if name == nil {
			continue
		}
		switch st.Type() {
		case "function_declaration":
			fn := tsMethod(name.Content(src))
			ps := st.ChildByFieldName("parameters")
			if ps == nil || ps.NamedChildCount() == 0 {
				continue
			}
			p := ps.NamedChild(0)
			if p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
				continue
			}
			pat, typ := p.ChildByFieldName("pattern"), p.ChildByFieldName("type")
			if pat == nil || typ == nil || typ.NamedChildCount() != 1 {
				continue
			}
			switch t := typ.NamedChild(0); t.Type() {
			case "object_type":
				params[fn] = tsParamShape{literal: fn + "(" + tsEscape(pat.Content(src)) + ")"}
			case "type_identifier":
				if !tsTypeParamsBind(st, t.Content(src), src) {
					params[fn] = tsParamShape{typeName: t.Content(src)}
				}
			}
		}
	}
	return params
}

// exportClause handles a top-level export { a, b as c } without a source.
// scip-typescript writes the symbol the name stands for at the name and at
// the alias alike: a top-level declaration, an import followed to its
// target, a namespace import's file; a name that is a value and a type gets
// both symbols.
func (l *tsRefs) exportClause(n *sitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() != "export_clause" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			s := c.NamedChild(j)
			if s.Type() != "export_specifier" {
				continue
			}
			name := s.ChildByFieldName("name")
			if name == nil || name.Type() != "identifier" {
				continue
			}
			sites := []*sitter.Node{name}
			if a := s.ChildByFieldName("alias"); a != nil && a.Type() == "identifier" {
				sites = append(sites, a)
			}
			syms, ok := l.exportedSymbols(l.text(name))
			for _, site := range sites {
				if !l.onAdded(site) {
					continue
				}
				if !ok {
					l.unresolved++
					continue
				}
				for _, sym := range syms {
					l.emit(site, sym)
				}
			}
		}
	}
}

// exportedSymbols is what a name in a local export clause stands for, nil
// and true for a package import or a global (no repository edge), false
// for a repository name Layer 2 cannot resolve.
func (l *tsRefs) exportedSymbols(name string) ([]string, bool) {
	v, okV := l.resolveName(name, tsValue)
	if !okV || v.external {
		return nil, true
	}
	if v.namespace != "" {
		if fs := tsFileSymbol(l.r.docs(v.namespace)); fs != "" {
			return []string{fs}, true
		}
		return nil, false
	}
	t, _ := l.resolveName(name, tsTypeNS)
	var syms []string
	if v.sym != "" {
		syms = append(syms, v.sym)
	}
	if t.sym != "" && t.sym != v.sym {
		syms = append(syms, t.sym)
	}
	if len(syms) == 0 {
		return nil, false
	}
	return syms, true
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
			// scip-typescript writes the target at the name and at the
			// alias. A name that is a value and a type gets both symbols;
			// aliased, its name sometimes gets a local instead (when the
			// target module exports the name through an export clause), so
			// that name only counts.
			name := c.ChildByFieldName("name")
			if name == nil {
				return
			}
			var syms []string
			if v := l.r.lookup(target, l.text(name), tsValue, 0); v != "" {
				syms = append(syms, v)
			}
			if t := l.r.lookup(target, l.text(name), tsTypeNS, 0); t != "" && (len(syms) == 0 || syms[0] != t) {
				syms = append(syms, t)
			}
			alias := c.ChildByFieldName("alias")
			sites := []*sitter.Node{name}
			if alias != nil {
				sites = append(sites, alias)
			}
			for _, site := range sites {
				if !l.onAdded(site) {
					continue
				}
				if len(syms) == 0 || len(syms) > 1 && alias != nil && site == name {
					l.unresolved++
					continue
				}
				for _, sym := range syms {
					l.emit(site, sym)
				}
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
	_, ok := tsLocalBinding(n, name, src)
	return ok
}

// tsLocalBinding is tsBoundLocally that also returns the innermost binding's
// identifier where there is one (nil for a type variable, a function
// expression's own name, a namespace member).
func tsLocalBinding(n *sitter.Node, name string, src []byte) (*sitter.Node, bool) {
	return tsLocalBindingTP(n, name, src, false)
}

// tsTypeParamBinding is the name of the type parameter that binds n's name,
// nil when a type parameter is not the innermost binding.
func tsTypeParamBinding(n *sitter.Node, name string, src []byte) *sitter.Node {
	id, _ := tsLocalBindingTP(n, name, src, true)
	if id == nil || id.Parent() == nil || id.Parent().Type() != "type_parameter" {
		return nil
	}
	return id
}

// tsLocalBindingTP is tsLocalBinding; with typeParams, a binding type
// parameter returns its name instead of nil.
func tsLocalBindingTP(n *sitter.Node, name string, src []byte, typeParams bool) (*sitter.Node, bool) {
	tpBind := func(a *sitter.Node) (*sitter.Node, bool) {
		id := tsTypeParamName(a, name, src)
		if id == nil {
			return nil, false
		}
		if !typeParams || tsTypeVarBinds(a, name, src) {
			// infer T and [T in …] may shadow the type parameter T;
			// where they are is not modeled, so T is a local.
			return nil, true
		}
		return id, true
	}
	for a := n.Parent(); a != nil && a.Type() != "program"; a = a.Parent() {
		switch a.Type() {
		case "function_declaration", "generator_function_declaration", "function_expression", "function",
			"generator_function", "arrow_function", "method_definition", "function_signature", "method_signature",
			"abstract_method_signature", "call_signature", "construct_signature", "function_type", "constructor_type":
			if params := a.ChildByFieldName("parameters"); params != nil {
				if id := tsPatternBinding(params, name, src); id != nil {
					return id, true
				}
			}
			if p := a.ChildByFieldName("parameter"); p != nil && p.Content(src) == name {
				return p, true
			}
			if id, ok := tpBind(a); ok {
				return id, true
			}
			if a.Type() == "function_expression" || a.Type() == "function" {
				if fn := a.ChildByFieldName("name"); fn != nil && fn.Content(src) == name {
					return nil, true
				}
			}
			if body := a.ChildByFieldName("body"); body != nil {
				if id := tsVarBinding(body, name, src); id != nil {
					return id, true
				}
			}
		case "statement_block", "switch_body", "class_body":
			if p := a.Parent(); p != nil && (p.Type() == "internal_module" || p.Type() == "module") {
				return nil, true // inside a namespace: its members are not file-level names
			}
			if id := tsBlockBinding(a, name, src); id != nil {
				return id, true
			}
		case "for_statement":
			if init := a.ChildByFieldName("initializer"); init != nil {
				if id := tsPatternBinding(init, name, src); id != nil {
					return id, true
				}
			}
		case "for_in_statement":
			if left := a.ChildByFieldName("left"); left != nil {
				if id := tsPatternBinding(left, name, src); id != nil {
					return id, true
				}
			}
		case "catch_clause":
			if p := a.ChildByFieldName("parameter"); p != nil {
				if id := tsPatternBinding(p, name, src); id != nil {
					return id, true
				}
			}
		case "class_declaration", "abstract_class_declaration", "class", "interface_declaration", "type_alias_declaration":
			if id, ok := tpBind(a); ok {
				return id, true
			}
			if a.Type() == "class" {
				if cn := a.ChildByFieldName("name"); cn != nil && cn.Content(src) == name {
					return nil, true
				}
			}
			if a.Type() == "type_alias_declaration" || a.Type() == "interface_declaration" {
				if tsTypeVarBinds(a, name, src) {
					return nil, true
				}
			}
		}
	}
	return nil, false
}

// tsBindingType is the spelled type of a binding identifier: the annotation
// of a plain parameter or variable declarator. Nil for a destructured or an
// unannotated binding.
func tsBindingType(id *sitter.Node) *sitter.Node {
	if id == nil || id.Type() != "identifier" {
		return nil
	}
	p := id.Parent()
	if p == nil {
		return nil
	}
	field := ""
	switch p.Type() {
	case "required_parameter", "optional_parameter":
		field = "pattern"
	case "variable_declarator":
		field = "name"
	default:
		return nil
	}
	if b := p.ChildByFieldName(field); b == nil || b.StartByte() != id.StartByte() {
		return nil
	}
	return p.ChildByFieldName("type")
}

func tsTypeParamsBind(a *sitter.Node, name string, src []byte) bool {
	return tsTypeParamName(a, name, src) != nil
}

// tsTypeParamName is the name node of a's type parameter called name.
func tsTypeParamName(a *sitter.Node, name string, src []byte) *sitter.Node {
	tp := a.ChildByFieldName("type_parameters")
	if tp == nil {
		return nil
	}
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		if n := tp.NamedChild(i).ChildByFieldName("name"); n != nil && n.Content(src) == name {
			return n
		}
	}
	return nil
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
			// The variable is the first child: infer T extends U, [K in U as V].
			if n.NamedChildCount() > 0 {
				if c := n.NamedChild(0); c.Type() == "type_identifier" && c.Content(src) == name {
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
	return tsPatternBinding(n, name, src) != nil
}

// tsPatternBinding returns the identifier in a parameter list, declaration
// or pattern that binds name.
func tsPatternBinding(n *sitter.Node, name string, src []byte) *sitter.Node {
	switch n.Type() {
	case "identifier", "shorthand_property_identifier_pattern":
		if n.Content(src) == name {
			return n
		}
		return nil
	case "pair_pattern":
		if v := n.ChildByFieldName("value"); v != nil {
			return tsPatternBinding(v, name, src)
		}
		return nil
	case "assignment_pattern", "object_assignment_pattern":
		if left := n.ChildByFieldName("left"); left != nil {
			return tsPatternBinding(left, name, src)
		}
		return nil
	case "variable_declarator":
		if v := n.ChildByFieldName("name"); v != nil {
			return tsPatternBinding(v, name, src)
		}
		return nil
	case "required_parameter", "optional_parameter":
		if v := n.ChildByFieldName("pattern"); v != nil {
			return tsPatternBinding(v, name, src)
		}
		return nil
	case "type_annotation", "string", "number", "call_expression", "arrow_function", "function_expression":
		return nil
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if id := tsPatternBinding(n.NamedChild(i), name, src); id != nil {
			return id
		}
	}
	return nil
}

// tsBlockBinding checks the declarations directly in a block.
func tsBlockBinding(b *sitter.Node, name string, src []byte) *sitter.Node {
	for i := 0; i < int(b.NamedChildCount()); i++ {
		if id := tsStatementBinding(b.NamedChild(i), name, src); id != nil {
			return id
		}
	}
	return nil
}

func tsStatementBinding(st *sitter.Node, name string, src []byte) *sitter.Node {
	switch st.Type() {
	case "lexical_declaration", "variable_declaration":
		for j := 0; j < int(st.NamedChildCount()); j++ {
			if d := st.NamedChild(j); d.Type() == "variable_declarator" {
				if id := tsPatternBinding(d, name, src); id != nil {
					return id
				}
			}
		}
	case "function_declaration", "generator_function_declaration", "class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration", "enum_declaration":
		if n := st.ChildByFieldName("name"); n != nil && n.Content(src) == name {
			return n
		}
	case "export_statement":
		if d := st.ChildByFieldName("declaration"); d != nil {
			return tsStatementBinding(d, name, src)
		}
	}
	return nil
}

// tsVarBinding finds a var declaration of name anywhere in a function body
// (they are function-scoped), not entering nested functions.
func tsVarBinding(body *sitter.Node, name string, src []byte) *sitter.Node {
	var found *sitter.Node
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if found != nil {
			return
		}
		switch n.Type() {
		case "function_declaration", "function_expression", "function", "arrow_function", "method_definition", "class", "class_declaration":
			return
		case "variable_declaration":
			for j := 0; j < int(n.NamedChildCount()); j++ {
				if d := n.NamedChild(j); d.Type() == "variable_declarator" {
					if id := tsPatternBinding(d, name, src); id != nil {
						found = id
						return
					}
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

// tsIntrinsicTag reports whether n is the name of a JSX element that starts
// with a lower-case letter: TypeScript looks those up in
// JSX.IntrinsicElements, never among values.
func tsIntrinsicTag(n *sitter.Node, name string) bool {
	p := n.Parent()
	if p == nil || name == "" || !(name[0] >= 'a' && name[0] <= 'z') {
		return false
	}
	switch p.Type() {
	case "jsx_opening_element", "jsx_closing_element", "jsx_self_closing_element":
		tag := p.ChildByFieldName("name")
		return tag != nil && tag.StartByte() == n.StartByte()
	}
	return false
}
