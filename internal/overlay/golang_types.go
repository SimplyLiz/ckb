package overlay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// goTypes finds the types Layer 2 needs beyond the file it resolves: of a
// struct field, of a package-level variable, of what a function or method
// returns, and the members a struct gets from its embedded fields. It reads
// them from the declaring file's source, found through the index, the same
// way the changed file itself is read: what the source spells, resolved
// through that file's imports. Nothing is inferred; a type the source does
// not spell (an untyped var = f() chain it cannot follow, a generic
// instantiation) stays unknown, and the site stays unresolved.
type goTypes struct {
	g     *goSymbols
	root  string
	files map[string]*goDeclFile // by repo-relative path; nil entry = unreadable
	// docs maps a symbol the overlay declared to its document's path.
	docs map[string]string
}

type goDeclFile struct {
	f       *ast.File
	prefix  string            // package prefix of the file's symbols
	imports map[string]string // local name -> import path
}

func newGoTypes(g *goSymbols, root string) *goTypes {
	return &goTypes{g: g, root: root, files: map[string]*goDeclFile{}, docs: map[string]string{}}
}

// prefixOf is a scip-go symbol's package prefix ("... `<path>`/").
func prefixOf(sym string) string {
	if i := strings.Index(sym, "`/"); i >= 0 {
		return sym[:i+2]
	}
	return ""
}

// fileOf parses the file that declares sym.
func (t *goTypes) fileOf(sym string) *goDeclFile {
	p := t.docs[sym]
	if p == "" {
		ref := t.g.base.DefinitionIndex[sym]
		if ref == nil || ref.Doc == nil {
			return nil
		}
		p = ref.Doc.RelativePath
	}
	if pf, seen := t.files[p]; seen {
		return pf
	}
	t.files[p] = nil
	if t.root == "" || !strings.HasSuffix(p, ".go") {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(t.root, filepath.FromSlash(p)))
	if err != nil {
		return nil
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.SkipObjectResolution)
	if err != nil || f == nil {
		return nil
	}
	pf := &goDeclFile{f: f, prefix: prefixOf(sym), imports: fileImports(f, t.g)}
	t.files[p] = pf
	return pf
}

func fileImports(f *ast.File, g *goSymbols) map[string]string {
	out := map[string]string{}
	for _, im := range f.Imports {
		p, err := strconv.Unquote(im.Path.Value)
		if err != nil {
			continue
		}
		name := ""
		switch {
		case im.Name != nil:
			name = im.Name.Name
		case g.names[p] != "":
			name = g.names[p]
		default:
			name = importName(p)
		}
		if name != "_" && name != "." {
			out[name] = p
		}
	}
	return out
}

// spelled resolves a type expression as written in pf to a type symbol.
func (t *goTypes) spelled(pf *goDeclFile, e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return t.spelled(pf, x.X)
	case *ast.ParenExpr:
		return t.spelled(pf, x.X)
	case *ast.Ident:
		if goUniverse[x.Name] {
			return ""
		}
		if sym := pf.prefix + x.Name + "#"; t.g.has(sym) {
			return sym
		}
	case *ast.SelectorExpr:
		if pkg, ok := x.X.(*ast.Ident); ok {
			if p, ok := pf.imports[pkg.Name]; ok && t.g.prefixes[p] != "" {
				if sym := t.g.prefixes[p] + x.Sel.Name + "#"; t.g.has(sym) {
					return sym
				}
			}
		}
	}
	// Generic instantiations, maps, slices, funcs, channels: not a named
	// type whose members the index lists.
	return ""
}

// typeName is the declared name in a type symbol "<prefix>T#".
func typeName(typeSym string) string {
	return strings.TrimSuffix(strings.TrimPrefix(typeSym, prefixOf(typeSym)), "#")
}

// structOf finds the struct type T declares, or nil.
func (t *goTypes) structOf(typeSym string) (*goDeclFile, *ast.StructType) {
	pf := t.fileOf(typeSym)
	if pf == nil {
		return nil, nil
	}
	name := typeName(typeSym)
	for _, d := range pf.f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, _ := ts.Type.(*ast.StructType)
			return pf, st
		}
	}
	return nil, nil
}

// fieldType is the type symbol of field f of struct T, including a field
// promoted from an embedded struct (up to depth levels down).
func (t *goTypes) fieldType(typeSym, field string, depth int) string {
	pf, st := t.structOf(typeSym)
	if st == nil {
		return ""
	}
	var embedded []string
	for _, fld := range st.Fields.List {
		if len(fld.Names) == 0 {
			if et := t.spelled(pf, fld.Type); et != "" {
				if typeName(et) == field {
					return et // the embedded field itself, named after its type
				}
				embedded = append(embedded, et)
			}
			continue
		}
		for _, n := range fld.Names {
			if n.Name == field {
				return t.spelled(pf, fld.Type)
			}
		}
	}
	if depth > 0 {
		for _, et := range embedded {
			if ft := t.fieldType(et, field, depth-1); ft != "" {
				return ft
			}
		}
	}
	return ""
}

// member resolves name on type T: its own method or field, else one promoted
// from an embedded struct.
func (t *goTypes) member(typeSym, name string, depth int) string {
	base := strings.TrimSuffix(typeSym, "#")
	for _, sym := range []string{base + "#" + name + "().", base + "#" + name + "."} {
		if t.g.has(sym) {
			return sym
		}
	}
	if depth <= 0 {
		return ""
	}
	pf, st := t.structOf(typeSym)
	if st == nil {
		return ""
	}
	for _, fld := range st.Fields.List {
		if len(fld.Names) != 0 {
			continue
		}
		if et := t.spelled(pf, fld.Type); et != "" {
			if sym := t.member(et, name, depth-1); sym != "" {
				return sym
			}
		}
	}
	return ""
}

// resultType is the type symbol of what function or method sym returns,
// when it returns one value, or one value and an error.
func (t *goTypes) resultType(sym string) string {
	pf := t.fileOf(sym)
	if pf == nil {
		return ""
	}
	desc := strings.TrimSuffix(strings.TrimPrefix(sym, pf.prefix), "().")
	recv, name := "", desc
	if i := strings.IndexByte(desc, '#'); i >= 0 {
		recv, name = desc[:i], desc[i+1:]
	}
	for _, d := range pf.f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != name {
			continue
		}
		if (recv == "") != (fd.Recv == nil) {
			continue
		}
		if recv != "" && (len(fd.Recv.List) == 0 || receiverName(fd.Recv.List[0].Type) != recv) {
			continue
		}
		res := fd.Type.Results
		if res == nil || len(res.List) == 0 || len(res.List) > 2 || len(res.List[0].Names) > 1 {
			return ""
		}
		if len(res.List) == 2 {
			if id, ok := res.List[1].Type.(*ast.Ident); !ok || id.Name != "error" {
				return ""
			}
		}
		return t.spelled(pf, res.List[0].Type)
	}
	return ""
}

// varType is the type symbol of package-level variable sym, when its
// declaration spells it (var x T, var x = T{…}, var x = &T{…}).
func (t *goTypes) varType(sym string) string {
	pf := t.fileOf(sym)
	if pf == nil {
		return ""
	}
	name := strings.TrimSuffix(strings.TrimPrefix(sym, pf.prefix), ".")
	for _, d := range pf.f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, n := range vs.Names {
				if n.Name != name {
					continue
				}
				if vs.Type != nil {
					return t.spelled(pf, vs.Type)
				}
				if i < len(vs.Values) {
					e := vs.Values[i]
					if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
						e = u.X
					}
					if lit, ok := e.(*ast.CompositeLit); ok && lit.Type != nil {
						return t.spelled(pf, lit.Type)
					}
				}
				return ""
			}
		}
	}
	return ""
}

// noteDeclared records where the overlay's own declarations live, so their
// types are read from the new source.
func (t *goTypes) noteDeclared(doc *scip.Document) {
	for _, o := range doc.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			t.docs[o.Symbol] = doc.RelativePath
		}
	}
}
