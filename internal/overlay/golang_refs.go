package overlay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Layer 2 for Go: references on added lines, resolved exactly or not at all.
// LIP linked calls across files by name and measured 2.4 % precision for the
// callers it added, so the rule here is the opposite: an identifier gets an
// edge only when the file's own syntax plus the base index say which symbol
// it is, and a symbol that is not in the index (or among the overlay's own
// declarations) gets none. What could not be resolved is counted.
//
// Resolved:
//   - a name declared at package level, in this file or another file of
//     the package (go/parser's object resolution tells a local from it);
//   - pkg.Name, where pkg is one of the file's imports;
//   - x.M and x.F, where x is a local whose type is written next to it
//     (a parameter, receiver, var with a type, x := T{…} or &T{…}): T#M(). or
//     T#F. when the index has it. Promoted members (embedding) are not
//     followed; they count as unresolved;
//   - the keys of a composite literal T{F: …}.

// goSymbols is what Layer 2 looks names up in: the base index's symbols, the
// package prefixes by import path, and the declarations the overlay itself
// added (Layer 1) in this build.
type goSymbols struct {
	base     *scip.SCIPIndex
	prefixes map[string]string // import path -> "scip-go gomod <mod> <ver> `<path>`/"
	names    map[string]string // import path -> package name, from the package symbol
	declared map[string]bool
}

func (g *goSymbols) has(sym string) bool {
	if g.declared[sym] {
		return true
	}
	if _, ok := g.base.Symbols[sym]; ok {
		return true
	}
	_, ok := g.base.RefIndex[sym]
	return ok
}

var goPrefixCache sync.Map // *scip.SCIPIndex -> *goPrefixes

type goPrefixes struct {
	byPath map[string]string
	names  map[string]string
}

// goPackagePrefixes maps every Go package path the index mentions to its
// symbol prefix, once per base index.
func goPackagePrefixes(base *scip.SCIPIndex) *goPrefixes {
	if v, ok := goPrefixCache.Load(base); ok {
		return v.(*goPrefixes)
	}
	p := &goPrefixes{byPath: map[string]string{}, names: map[string]string{}}
	note := func(sym string) {
		if !strings.HasPrefix(sym, "scip-go ") {
			return
		}
		i := strings.IndexByte(sym, '`')
		if i < 0 {
			return
		}
		j := strings.Index(sym[i+1:], "`/")
		if j < 0 {
			return
		}
		pkgPath := sym[i+1 : i+1+j]
		if _, seen := p.byPath[pkgPath]; !seen {
			p.byPath[pkgPath] = sym[:i+1+j+2]
		}
	}
	for sym := range base.RefIndex {
		note(sym)
	}
	for sym, info := range base.Symbols {
		note(sym)
		if strings.HasPrefix(sym, "scip-go ") && strings.HasSuffix(sym, "`/") && info.DisplayName != "" {
			i := strings.IndexByte(sym, '`')
			p.names[sym[i+1:len(sym)-2]] = info.DisplayName
		}
	}
	// Keep one entry per index; an old index is dropped with the next.
	goPrefixCache.Range(func(k, _ any) bool { goPrefixCache.Delete(k); return true })
	goPrefixCache.Store(base, p)
	return p
}

var goUniverse = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true, "error": true, "float32": true,
	"float64": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "any": true, "comparable": true, "true": true, "false": true, "iota": true, "nil": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true, "copy": true, "delete": true,
	"imag": true, "len": true, "make": true, "max": true, "min": true, "new": true, "panic": true,
	"print": true, "println": true, "real": true, "recover": true, "_": true,
}

// goLayer2 adds the references on added lines of src to doc and returns
// how many it added and how many sites it could not resolve. prefix is the
// file's package prefix.
func goLayer2(doc *scip.Document, src []byte, prefix string, lines map[int32]bool, g *goSymbols) (added, unresolved int) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0) // with object resolution
	if err != nil || f == nil {
		return 0, 0
	}
	pos := func(p token.Pos) (int32, int32) {
		pp := fset.Position(p)
		return int32(pp.Line - 1), int32(pp.Column - 1) // #nosec G115 -- file positions fit
	}
	onAdded := func(id *ast.Ident) bool {
		l, _ := pos(id.Pos())
		return lines[l]
	}
	// Positions that already have an occurrence (Layer 0/1 kept them).
	taken := map[[2]int32]bool{}
	for _, o := range doc.Occurrences {
		taken[[2]int32{o.Range[0], o.Range[1]}] = true
	}
	emit := func(id *ast.Ident, sym string) {
		l, c := pos(id.Pos())
		if taken[[2]int32{l, c}] {
			return
		}
		taken[[2]int32{l, c}] = true
		doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{
			Range:  []int32{l, c, c + int32(len(id.Name))}, // #nosec G115 -- name length fits
			Symbol: sym,
			// scip-go marks every reference a read (it writes no other
			// access role), and impact classification reads that role.
			SymbolRoles: scip.SymbolRoleReadAccess,
			Source:      scip.OccurrenceSourceOverlay,
		})
		added++
	}

	// Imports: local name -> package path.
	imports := map[string]string{}
	for _, im := range f.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		if im.Name != nil {
			name = im.Name.Name
		} else if n, ok := g.names[p]; ok {
			name = n
		} else {
			name = importName(p)
		}
		if name != "_" && name != "." {
			imports[name] = p
		}
	}
	pkgSym := func(p string) string { return g.prefixes[p] }

	// A package-level name of this package: the symbol it has, or "".
	samePackage := func(name string) string {
		for _, suffix := range []string{"().", "#", "."} {
			if sym := prefix + name + suffix; g.has(sym) {
				return sym
			}
		}
		return ""
	}
	// typeOf names the type an expression spells: T, *T, pkg.T, T[K].
	var typeOf func(e ast.Expr) string
	typeOf = func(e ast.Expr) string {
		switch t := e.(type) {
		case *ast.StarExpr:
			return typeOf(t.X)
		case *ast.IndexExpr:
			return typeOf(t.X)
		case *ast.IndexListExpr:
			return typeOf(t.X)
		case *ast.ParenExpr:
			return typeOf(t.X)
		case *ast.Ident:
			if t.Obj != nil && t.Obj.Kind != ast.Typ {
				return ""
			}
			if sym := prefix + t.Name + "#"; g.has(sym) {
				return sym
			}
		case *ast.SelectorExpr:
			if x, ok := t.X.(*ast.Ident); ok && x.Obj == nil {
				if p, ok := imports[x.Name]; ok && pkgSym(p) != "" {
					if sym := pkgSym(p) + t.Sel.Name + "#"; g.has(sym) {
						return sym
					}
				}
			}
		}
		return ""
	}
	// typeOfValue names the type of a value expression when it is spelled
	// out: T{…}, &T{…}.
	typeOfValue := func(e ast.Expr) string {
		if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
			e = u.X
		}
		if lit, ok := e.(*ast.CompositeLit); ok && lit.Type != nil {
			return typeOf(lit.Type)
		}
		return ""
	}
	// localType is the type symbol of a local variable, when written next
	// to its declaration.
	localType := func(id *ast.Ident) string {
		if id.Obj == nil || id.Obj.Kind != ast.Var {
			return ""
		}
		switch d := id.Obj.Decl.(type) {
		case *ast.Field:
			return typeOf(d.Type)
		case *ast.ValueSpec:
			if d.Type != nil {
				return typeOf(d.Type)
			}
			for i, n := range d.Names {
				if n.Name == id.Name && i < len(d.Values) {
					return typeOfValue(d.Values[i])
				}
			}
		case *ast.AssignStmt:
			if len(d.Lhs) != len(d.Rhs) {
				return ""
			}
			for i, l := range d.Lhs {
				if li, ok := l.(*ast.Ident); ok && li.Name == id.Name {
					return typeOfValue(d.Rhs[i])
				}
			}
		}
		return ""
	}
	// member resolves T's member name: method or field.
	member := func(typeSym, name string) string {
		base := strings.TrimSuffix(typeSym, "#")
		for _, sym := range []string{base + "#" + name + "().", base + "#" + name + "."} {
			if g.has(sym) {
				return sym
			}
		}
		return ""
	}
	isTopLevel := func(o *ast.Object) bool {
		switch d := o.Decl.(type) {
		case *ast.FuncDecl:
			return d.Recv == nil
		case *ast.TypeSpec:
			return true
		case *ast.ValueSpec:
			// Package-level iff it is in a top-level GenDecl.
			for _, decl := range f.Decls {
				if gd, ok := decl.(*ast.GenDecl); ok {
					for _, s := range gd.Specs {
						if s == d {
							return true
						}
					}
				}
			}
		}
		return false
	}

	// Identifiers already handled as part of a selector or a literal key,
	// and the ones that are names, not references.
	done := map[*ast.Ident]bool{}
	notRefs := nonReferenceIdents(f)
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if !onAdded(n.Sel) {
				return true
			}
			x, ok := n.X.(*ast.Ident)
			if !ok {
				// A chain or a call result: the receiver's type is not
				// visible.
				done[n.Sel] = true
				unresolved++
				return true
			}
			done[n.Sel] = true
			if x.Obj == nil {
				if p, isImport := imports[x.Name]; isImport {
					done[x] = true
					pfx := pkgSym(p)
					if pfx == "" {
						unresolved++
						return true
					}
					if onAdded(x) && g.has(pfx) {
						emit(x, pfx)
					}
					resolvedSel := false
					for _, suffix := range []string{"().", "#", "."} {
						if sym := pfx + n.Sel.Name + suffix; g.has(sym) {
							emit(n.Sel, sym)
							resolvedSel = true
							break
						}
					}
					if !resolvedSel {
						unresolved++
					}
					return true
				}
			}
			var typeSym string
			if x.Obj != nil {
				typeSym = localType(x)
			}
			if typeSym == "" {
				unresolved++
				return true
			}
			if sym := member(typeSym, n.Sel.Name); sym != "" {
				emit(n.Sel, sym)
			} else {
				unresolved++
			}
		case *ast.CompositeLit:
			switch n.Type.(type) {
			case *ast.MapType, *ast.ArrayType:
				return true // keys are values, resolved as identifiers
			}
			typeSym := ""
			if n.Type != nil {
				typeSym = typeOf(n.Type)
			}
			for _, el := range n.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || !onAdded(key) {
					continue
				}
				done[key] = true
				if typeSym == "" {
					unresolved++
					continue
				}
				if sym := member(typeSym, key.Name); sym != "" && strings.HasSuffix(sym, ".") && !strings.HasSuffix(sym, "().") {
					emit(key, sym)
				} else {
					unresolved++
				}
			}
		case *ast.Ident:
			if done[n] || !onAdded(n) || goUniverse[n.Name] && n.Obj == nil {
				return true
			}
			if n.Obj != nil {
				// Declared in this file: a package-level declaration, or
				// a local (scip-go's "local N", which Layer 2 leaves out).
				if isTopLevel(n.Obj) && !isDeclName(n) {
					if sym := samePackage(n.Name); sym != "" {
						emit(n, sym)
					}
				}
				return true
			}
			if _, isImport := imports[n.Name]; isImport {
				return true // a package name outside a selector (rare)
			}
			if notRefs[n] {
				return true
			}
			if sym := samePackage(n.Name); sym != "" {
				emit(n, sym)
			} else {
				unresolved++
			}
		}
		return true
	})
	sortOccurrences(doc.Occurrences)
	return added, unresolved
}

// isDeclName reports whether id is the name in its own declaration (Layer 1
// writes that one as the definition).
func isDeclName(id *ast.Ident) bool {
	switch d := id.Obj.Decl.(type) {
	case *ast.FuncDecl:
		return d.Name == id
	case *ast.TypeSpec:
		return d.Name == id
	case *ast.ValueSpec:
		for _, n := range d.Names {
			if n == id {
				return true
			}
		}
	}
	return false
}

// nonReferenceIdents collects identifiers go/parser leaves unresolved that
// are names, not package-level references: struct field and interface method
// names, method names in declarations, labels, import names and the package
// clause.
func nonReferenceIdents(f *ast.File) map[*ast.Ident]bool {
	out := map[*ast.Ident]bool{f.Name: true}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			for _, nm := range n.Names {
				out[nm] = true
			}
		case *ast.FuncDecl:
			if n.Recv != nil {
				out[n.Name] = true
			}
		case *ast.LabeledStmt:
			out[n.Label] = true
		case *ast.BranchStmt:
			if n.Label != nil {
				out[n.Label] = true
			}
		case *ast.ImportSpec:
			if n.Name != nil {
				out[n.Name] = true
			}
		}
		return true
	})
	return out
}

// importName is the package name an import path conventionally has: its last
// element, without a major-version element or a "go-"/".vN" decoration.
func importName(p string) string {
	base := path.Base(p)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = path.Base(path.Dir(p))
	}
	if i := strings.Index(base, ".v"); i > 0 {
		base = base[:i]
	}
	base = strings.TrimPrefix(base, "go-")
	return strings.ReplaceAll(base, "-", "_")
}
