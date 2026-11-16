package overlay

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"golang.org/x/mod/modfile"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Layer 1 for Go: the declarations of a changed file, read with go/parser.
// go/parser rather than tree-sitter because it is exact for Go and reports
// positions in the byte columns scip-go writes, so a declaration the overlay
// synthesizes sits where the indexer would put it.

// goDecl is one top-level declaration (or a member of one) scip-go would
// write a definition for.
type goDecl struct {
	suffix string // descriptor after the package: "F().", "T#", "T#M().", "T#f.", "C."
	name   string
	kind   scippb.SymbolInformation_Kind
	line   int32 // 0-based line of the name
	col    int32 // 0-based byte column of the name
	// enclosing is set for functions and methods, the only declarations
	// scip-go gives an EnclosingRange: from "func" to the closing brace.
	enclosing []int32
}

// parseGoDecls parses src and returns its declarations and package name. ok
// is false when the file does not parse cleanly; the caller then stays with
// Layer 0 for it.
func parseGoDecls(src []byte) (decls []goDecl, pkg string, ok bool) {
	decls, pkg, _, ok = parseGoFile(src)
	return decls, pkg, ok
}

// parseGoFile is parseGoDecls plus the position of the package name, where
// scip-go puts the file's definition of the package symbol.
func parseGoFile(src []byte) (decls []goDecl, pkg string, pkgAt []int32, ok bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil || f == nil {
		return nil, "", nil, false
	}
	pos := func(p token.Pos) (int32, int32) {
		pp := fset.Position(p)
		return int32(pp.Line - 1), int32(pp.Column - 1) // #nosec G115 -- file positions fit
	}
	add := func(id *ast.Ident, suffix string, kind scippb.SymbolInformation_Kind, encl []int32) {
		if id == nil || id.Name == "_" {
			return
		}
		l, c := pos(id.Pos())
		decls = append(decls, goDecl{suffix: suffix, name: id.Name, kind: kind, line: l, col: c, enclosing: encl})
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			sl, sc := pos(d.Pos())
			el, ec := pos(d.End())
			encl := scipRange(sl, sc, el, ec)
			if d.Recv == nil || len(d.Recv.List) == 0 {
				add(d.Name, d.Name.Name+"().", scippb.SymbolInformation_Function, encl)
				continue
			}
			recv := receiverName(d.Recv.List[0].Type)
			if recv == "" {
				continue
			}
			add(d.Name, recv+"#"+d.Name.Name+"().", scippb.SymbolInformation_Method, encl)
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					typeDecls(s, add)
				case *ast.ValueSpec:
					kind := scippb.SymbolInformation_Variable
					if d.Tok == token.CONST {
						kind = scippb.SymbolInformation_Constant
					}
					for i, n := range s.Names {
						add(n, n.Name+".", kind, nil)
						// A variable of an anonymous struct type, declared
						// or from a literal: scip-go names its fields "v:f.".
						st, isStruct := s.Type.(*ast.StructType)
						if !isStruct && s.Type == nil && i < len(s.Values) {
							if lit, isLit := s.Values[i].(*ast.CompositeLit); isLit {
								st, isStruct = lit.Type.(*ast.StructType)
							}
						}
						if isStruct && n.Name != "_" {
							for _, fld := range st.Fields.List {
								for _, fn := range fld.Names {
									add(fn, n.Name+":"+fn.Name+".", scippb.SymbolInformation_Field, nil)
								}
							}
						}
					}
				}
			}
		}
	}
	pl, pc := pos(f.Name.Pos())
	return decls, f.Name.Name, []int32{pl, pc, pc + int32(len(f.Name.Name))}, true // #nosec G115 -- name length fits
}

func typeDecls(s *ast.TypeSpec, add func(*ast.Ident, string, scippb.SymbolInformation_Kind, []int32)) {
	t := s.Name.Name
	switch tt := s.Type.(type) {
	case *ast.StructType:
		add(s.Name, t+"#", scippb.SymbolInformation_Struct, nil)
		for _, fld := range tt.Fields.List {
			if len(fld.Names) == 0 {
				// An embedded field is named after its type.
				if id := embeddedName(fld.Type); id != nil {
					add(id, t+"#"+id.Name+".", scippb.SymbolInformation_Field, nil)
				}
				continue
			}
			for _, n := range fld.Names {
				add(n, t+"#"+n.Name+".", scippb.SymbolInformation_Field, nil)
			}
		}
	case *ast.InterfaceType:
		add(s.Name, t+"#", scippb.SymbolInformation_Interface, nil)
		for _, m := range tt.Methods.List {
			if _, isFunc := m.Type.(*ast.FuncType); !isFunc {
				continue // an embedded interface or a type constraint
			}
			for _, n := range m.Names {
				add(n, t+"#"+n.Name+".", scippb.SymbolInformation_Method, nil)
			}
		}
	default:
		add(s.Name, t+"#", scippb.SymbolInformation_Type, nil)
	}
}

// receiverName is the type name of a method receiver: T, *T, T[K], *T[K, V].
func receiverName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return ""
		}
	}
}

func embeddedName(e ast.Expr) *ast.Ident {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.SelectorExpr:
			return t.Sel
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t
		default:
			return nil
		}
	}
}

// goPackagePrefix finds the scip-go package symbol ("scip-go gomod <module>
// <version> `<pkgpath>`/") for a file of package pkg at newPath: the file's
// own base document when it stays in its directory with the same package,
// else a base document of the same package in the new directory. Empty when
// neither exists (a new package).
func goPackagePrefix(base *scip.SCIPIndex, oldDoc *scip.Document, oldPath, newPath, pkg string) string {
	if oldDoc != nil && path.Dir(oldPath) == path.Dir(newPath) {
		if p := packageSymbolOf(base, oldDoc, pkg); p != "" {
			return p
		}
	}
	dir := path.Dir(newPath)
	for _, d := range base.Documents {
		if d.RelativePath == oldPath || path.Dir(d.RelativePath) != dir || !strings.HasSuffix(d.RelativePath, ".go") {
			continue
		}
		if p := packageSymbolOf(base, d, pkg); p != "" {
			return p
		}
	}
	return ""
}

// packageSymbolOf returns the package symbol a document defines, when its
// display name is pkg.
func packageSymbolOf(base *scip.SCIPIndex, d *scip.Document, pkg string) string {
	for _, o := range d.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition == 0 || !strings.HasPrefix(o.Symbol, "scip-go ") || !strings.HasSuffix(o.Symbol, "`/") {
			continue
		}
		info := base.Symbols[o.Symbol]
		if info == nil || info.DisplayName == "" || info.DisplayName == pkg {
			return o.Symbol
		}
	}
	return ""
}

// addedLines is the set of 0-based new-file lines the diff added.
func addedLines(lm *LineMap) map[int32]bool {
	out := map[int32]bool{}
	for _, b := range lm.blocks {
		for i := 0; i < b.Added; i++ {
			out[int32(b.New-1+i)] = true // #nosec G115 -- line numbers fit
		}
	}
	return out
}

// goLayer1 adds to a Layer 0 document the declarations that start on added
// lines, gives every function its exact extent, and drops the
// SymbolInformation of declarations that are gone from the file. src is the
// file's new content. It returns false, leaving doc as Layer 0 made it, when
// the file does not parse.
func goLayer1(doc *scip.Document, base *scip.SCIPIndex, oldDoc *scip.Document, oldPath string, src []byte, lm *LineMap) (added int, ok bool) {
	added, _, ok = goLayer1Prefix(doc, base, oldDoc, oldPath, src, lm)
	return added, ok
}

// goLayer1Prefix is goLayer1, also returning the package prefix it used.
func goLayer1Prefix(doc *scip.Document, base *scip.SCIPIndex, oldDoc *scip.Document, oldPath string, src []byte, lm *LineMap) (added int, prefix string, ok bool) {
	decls, pkg, ok := parseGoDecls(src)
	if !ok {
		return 0, "", false
	}
	prefix = goPackagePrefix(base, oldDoc, oldPath, doc.RelativePath, pkg)
	if prefix == "" {
		prefix = "ckb-overlay . . . `" + path.Dir(doc.RelativePath) + "`/"
	}
	newLines := addedLines(lm)

	// What the new file declares, by symbol.
	declared := make(map[string]*goDecl, len(decls))
	for i := range decls {
		declared[prefix+decls[i].suffix] = &decls[i]
	}

	// Definitions Layer 0 kept: exact extents from the parse.
	present := map[string]bool{}
	for _, o := range doc.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition == 0 {
			continue
		}
		present[o.Symbol] = true
		if d := declared[o.Symbol]; d != nil && d.enclosing != nil && len(o.EnclosingRange) > 0 && o.Range[0] == d.line {
			o.EnclosingRange = d.enclosing
		}
	}

	// Declarations on added lines.
	infos := map[string]*scip.SymbolInformation{}
	for _, s := range doc.Symbols {
		infos[s.Symbol] = s
	}
	for sym, d := range declared {
		if present[sym] || !newLines[d.line] {
			continue
		}
		occ := &scip.Occurrence{
			Range:       []int32{d.line, d.col, d.col + int32(len(d.name))}, // #nosec G115 -- name length fits
			Symbol:      sym,
			SymbolRoles: scip.SymbolRoleDefinition,
		}
		if d.enclosing != nil {
			occ.EnclosingRange = d.enclosing
		}
		doc.Occurrences = append(doc.Occurrences, occ)
		if infos[sym] == nil {
			info := &scip.SymbolInformation{Symbol: sym, DisplayName: d.name, Kind: int32(d.kind)}
			if old := base.Symbols[sym]; old != nil {
				cp := *old
				info = &cp
			}
			infos[sym] = info
		}
		present[sym] = true
		added++
	}

	// SymbolInformation: what the file still declares, plus non-declaration
	// entries (the package) as they were.
	kept := make([]*scip.SymbolInformation, 0, len(infos))
	for _, s := range doc.Symbols {
		if present[s.Symbol] || declared[s.Symbol] == nil && !wasDeclaredIn(oldDoc, s.Symbol) {
			kept = append(kept, s)
		}
		delete(infos, s.Symbol)
	}
	for _, s := range infos {
		kept = append(kept, s)
	}
	doc.Symbols = kept
	sortOccurrences(doc.Occurrences)
	return added, prefix, true
}

// wasDeclaredIn reports whether the base document had a definition of sym.
func wasDeclaredIn(d *scip.Document, sym string) bool {
	if d == nil {
		return false
	}
	for _, o := range d.Occurrences {
		if o.Symbol == sym && o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			return true
		}
	}
	return false
}

// sortOccurrences orders occurrences by position, as indexers write them.
func sortOccurrences(occs []*scip.Occurrence) {
	sort.SliceStable(occs, func(i, j int) bool {
		a, b := occs[i].Range, occs[j].Range
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return a[1] < b[1]
	})
}

// scipRange writes a range the way SCIP does: three elements when it starts
// and ends on the same line.
func scipRange(sl, sc, el, ec int32) []int32 {
	if sl == el {
		return []int32{sl, sc, ec}
	}
	return []int32{sl, sc, el, ec}
}

// goNewDocument synthesizes the document of a Go file the base index does
// not have, when its package is one the base index knows (a file added to an
// existing package). Every declaration is new; references come from Layer 2.
// Nil when the file does not parse or its package is not in the index.
func goNewDocument(base *scip.SCIPIndex, root, newPath string, src []byte) (*scip.Document, string) {
	decls, pkg, pkgAt, ok := parseGoFile(src)
	if !ok {
		return nil, ""
	}
	prefix := goPackagePrefix(base, nil, "", newPath, pkg)
	if prefix == "" {
		prefix = goNewPackagePrefix(base, root, newPath, pkg)
	}
	if prefix == "" {
		return nil, ""
	}
	doc := &scip.Document{RelativePath: newPath, Language: "go"}
	doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Range: pkgAt, Symbol: prefix, SymbolRoles: scip.SymbolRoleDefinition})
	if info := base.Symbols[prefix]; info != nil {
		doc.Symbols = append(doc.Symbols, info)
	}
	for i := range decls {
		d := &decls[i]
		sym := prefix + d.suffix
		occ := &scip.Occurrence{
			Range:       []int32{d.line, d.col, d.col + int32(len(d.name))}, // #nosec G115 -- name length fits
			Symbol:      sym,
			SymbolRoles: scip.SymbolRoleDefinition,
		}
		if d.enclosing != nil {
			occ.EnclosingRange = d.enclosing
		}
		doc.Occurrences = append(doc.Occurrences, occ)
		doc.Symbols = append(doc.Symbols, &scip.SymbolInformation{Symbol: sym, DisplayName: d.name, Kind: int32(d.kind)})
	}
	sortOccurrences(doc.Occurrences)
	return doc, prefix
}

// goNewPackagePrefix builds the package symbol for a file in a directory the
// index has no Go package in: the import path from the nearest go.mod, and
// the module's version field from a package of the same module the index
// has, as scip-go would write it. Empty for an external test package (scip-go
// names those differently), a module the index has no package of, or a
// go.mod outside the repository.
func goNewPackagePrefix(base *scip.SCIPIndex, root, newPath, pkg string) string {
	if root == "" || strings.HasSuffix(pkg, "_test") {
		return ""
	}
	modDir, modPath := nearestGoMod(root, path.Dir(newPath))
	if modPath == "" {
		return ""
	}
	version := moduleVersion(base, modPath)
	if version == "" {
		return ""
	}
	importPath := modPath
	if rel := strings.TrimPrefix(strings.TrimPrefix(path.Dir(newPath), modDir), "/"); rel != "" && rel != "." {
		importPath = modPath + "/" + rel
	}
	return "scip-go gomod " + modPath + " " + version + " " + scipDescriptorName(importPath) + "/"
}

// nearestGoMod walks up from dir (repo-relative) to the repository root and
// returns the first go.mod's directory (repo-relative, "" for the root) and
// module path.
func nearestGoMod(root, dir string) (string, string) {
	for {
		if dir == "." {
			dir = ""
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "go.mod")) // #nosec G304 -- a go.mod inside the repository
		if err == nil {
			if mp := modfile.ModulePath(data); mp != "" {
				return dir, mp
			}
			return "", ""
		}
		if dir == "" {
			return "", ""
		}
		dir = path.Dir(dir)
	}
}

// moduleVersion is the version field scip-go wrote for module modPath, read
// from a package symbol of that module in the index.
func moduleVersion(base *scip.SCIPIndex, modPath string) string {
	head := "scip-go gomod " + modPath + " "
	for _, d := range base.Documents {
		if !strings.HasSuffix(d.RelativePath, ".go") {
			continue
		}
		for _, o := range d.Occurrences {
			if o.SymbolRoles&scip.SymbolRoleDefinition == 0 || !strings.HasPrefix(o.Symbol, head) || !strings.HasSuffix(o.Symbol, "/") {
				continue
			}
			rest := o.Symbol[len(head):]
			if i := strings.IndexByte(rest, ' '); i > 0 {
				return rest[:i]
			}
		}
	}
	return ""
}

// scipDescriptorName writes a SCIP descriptor name: as is when it consists of
// SCIP's simple identifier characters (ASCII), else in backticks with backticks doubled.
func scipDescriptorName(name string) string {
	simple := name != ""
	for _, r := range name {
		if !(r == '_' || r == '+' || r == '-' || r == '$' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9') {
			simple = false
			break
		}
	}
	if simple {
		return name
	}
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// goBuilds reports whether the go tool would build the file at abs under the
// default context (GOOS, GOARCH, no tags), which is what scip-go indexes: a
// file behind a build tag is not in the index, so the overlay must not add it.
func goBuilds(abs string) bool {
	ok, err := build.Default.MatchFile(filepath.Dir(abs), filepath.Base(abs))
	return err == nil && ok
}
