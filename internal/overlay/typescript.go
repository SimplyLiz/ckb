//go:build cgo

package overlay

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Layer 1 for TypeScript: the declarations of a changed file, read with
// tree-sitter, under the IDs scip-typescript gives them.
//
// Only declarations whose descriptor follows from the syntax alone are
// synthesized: functions, classes and their members, interfaces and their
// members, enums and their members, type aliases, namespaces, top-level
// variables, and the parameters and type parameters of those. scip-typescript
// numbers object-literal properties ("experience0:") and the members of type
// literals ("T#typeLiteral40:x.") with counters that depend on the order the
// checker visits symbols, also from other files. Object-literal properties
// and destructured variables are left out; type-literal members on new lines
// get the owner's old number or a placeholder (tsNumberLiterals).

// tsDecl is one declaration scip-typescript writes a definition for.
type tsDecl struct {
	suffix string // descriptor after the file's symbol: "f().", "C#m().", "f().(p)"
	line   int32  // 0-based line of the name
	col    int32  // UTF-16 column of the name
	end    int32  // UTF-16 column after the name
	// enclosing is set where scip-typescript sets an EnclosingRange:
	// functions, classes, interfaces, enums, type aliases, methods and
	// constructors (the declaration with its modifiers), and a variable
	// whose initializer is a function (the initializer).
	enclosing []int32
	// classOrEnum marks a type symbol ("X#") that is a value too.
	classOrEnum bool
}

func tsLanguage(path string) *sitter.Language {
	if strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".jsx") {
		return tsx.GetLanguage()
	}
	return typescript.GetLanguage()
}

// parseTSDecls parses src and returns its declarations and the lines a
// syntax error touches (nil when it parses cleanly). Declarations inside an
// error are not walked. ok is false when there is no tree at all.
func parseTSDecls(path string, src []byte) (decls []tsDecl, errLines map[int32]bool, ok bool) {
	p := sitter.NewParser()
	p.SetLanguage(tsLanguage(path))
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return nil, nil, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil {
		return nil, nil, false
	}
	w := &tsWalker{src: src, cols: newUTF16Cols(src)}
	w.statements(root, "")
	if root.HasError() {
		errLines = map[int32]bool{}
		tsErrorLines(root, errLines)
	}
	return w.decls, errLines, true
}

// tsErrorLines collects the lines of the error and missing nodes under n.
func tsErrorLines(n *sitter.Node, out map[int32]bool) {
	if n.IsError() || n.IsMissing() {
		for l := n.StartPoint().Row; l <= n.EndPoint().Row; l++ {
			out[int32(l)] = true // #nosec G115 -- line numbers fit
		}
		return
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if c := n.Child(i); c.HasError() || c.IsMissing() {
			tsErrorLines(c, out)
		}
	}
}

// fileEnd is the position scip-typescript gives the end of a file: the line
// and UTF-16 column after the last byte.
func tsFileEnd(src []byte) (int32, int32) {
	c := newUTF16Cols(src)
	return c.pos(uint32(len(src))) // #nosec G115 -- file sizes fit
}

type tsWalker struct {
	src   []byte
	cols  *utf16Cols
	decls []tsDecl
	// lits are the members of type literals, numbered later: scip-typescript
	// numbers them with a counter no parse reproduces (tsNumberLiterals).
	lits   []tsLit
	groups int
}

// tsLit is a type literal's member: owner is the descriptor its ID starts
// with ("T#", "f().(p)", "f().", "f().[E]"), group the literal it is in, and
// decl.suffix the member's own descriptor ("x.", "m().").
type tsLit struct {
	owner string
	group int
	decl  tsDecl
}

// literal records the members of t when it is an object type (directly or
// in a type annotation), under owner.
func (w *tsWalker) literal(t *sitter.Node, owner string) {
	if t != nil && t.Type() == "type_annotation" && t.NamedChildCount() == 1 {
		t = t.NamedChild(0)
	}
	if t == nil || t.Type() != "object_type" {
		return
	}
	g := w.groups
	w.groups++
	for i := 0; i < int(t.NamedChildCount()); i++ {
		m := t.NamedChild(i)
		name := m.ChildByFieldName("name")
		if name == nil || !tsSimpleMemberName(name) {
			continue
		}
		tail := ""
		switch m.Type() {
		case "property_signature":
			tail = tsTerm(w.text(name))
		case "method_signature":
			tail = tsMethod(w.text(name))
		default:
			continue
		}
		l, c := w.cols.pos(name.StartByte())
		_, e := w.cols.pos(name.EndByte())
		w.lits = append(w.lits, tsLit{owner: owner, group: g, decl: tsDecl{suffix: tail, line: l, col: c, end: e}})
	}
}

func (w *tsWalker) text(n *sitter.Node) string { return n.Content(w.src) }

func (w *tsWalker) rng(n *sitter.Node) []int32 {
	sl, sc := w.cols.pos(n.StartByte())
	el, ec := w.cols.pos(n.EndByte())
	return scipRange(sl, sc, el, ec)
}

// add records a declaration named by the node name.
func (w *tsWalker) add(name *sitter.Node, suffix string, enclosing *sitter.Node) {
	if name == nil {
		return
	}
	l, c := w.cols.pos(name.StartByte())
	_, e := w.cols.pos(name.EndByte())
	d := tsDecl{suffix: suffix, line: l, col: c, end: e}
	if enclosing != nil {
		d.enclosing = w.rng(enclosing)
	}
	w.decls = append(w.decls, d)
}

// statements walks a list of statements whose declarations belong to owner
// ("" for the file, "N/" inside namespace N).
func (w *tsWalker) statements(n *sitter.Node, owner string) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		w.statement(n.NamedChild(i), owner, nil)
	}
}

// statement handles one statement; outer is the outermost wrapper (export,
// declare) whose range the declaration's range starts with, as TypeScript
// counts those keywords as the declaration's modifiers.
func (w *tsWalker) statement(n *sitter.Node, owner string, outer *sitter.Node) {
	whole := n
	if outer != nil {
		whole = outer
	}
	switch n.Type() {
	case "export_statement":
		if d := n.ChildByFieldName("declaration"); d != nil {
			w.statement(d, owner, whole)
			return
		}
		// export default function () {} / class {}: no name, no symbol.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "function_declaration", "generator_function_declaration", "class_declaration", "abstract_class_declaration":
				w.statement(c, owner, whole)
			}
		}
	case "expression_statement":
		// tree-sitter parses a bare "namespace N {}" as an expression.
		if n.NamedChildCount() == 1 && n.NamedChild(0).Type() == "internal_module" {
			w.statement(n.NamedChild(0), owner, outer)
		}
	case "ambient_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			w.statement(n.NamedChild(i), owner, whole)
		}
	case "function_declaration", "generator_function_declaration", "function_signature":
		name := n.ChildByFieldName("name")
		if name == nil {
			return
		}
		s := owner + tsMethod(w.text(name))
		w.add(name, s, whole)
		w.signature(n, s)
	case "class_declaration", "abstract_class_declaration":
		name := n.ChildByFieldName("name")
		if name == nil {
			return
		}
		s := owner + tsType(w.text(name))
		w.add(name, s, whole)
		w.decls[len(w.decls)-1].classOrEnum = true
		w.typeParams(n, s)
		if body := n.ChildByFieldName("body"); body != nil {
			w.classBody(body, s)
		}
	case "interface_declaration":
		name := n.ChildByFieldName("name")
		if name == nil {
			return
		}
		s := owner + tsType(w.text(name))
		w.add(name, s, whole)
		w.typeParams(n, s)
		if body := n.ChildByFieldName("body"); body != nil {
			w.typeMembers(body, s)
		}
	case "type_alias_declaration":
		name := n.ChildByFieldName("name")
		if name == nil {
			return
		}
		s := owner + tsType(w.text(name))
		w.add(name, s, whole)
		w.typeParams(n, s)
		w.literal(n.ChildByFieldName("value"), s)
	case "enum_declaration":
		name := n.ChildByFieldName("name")
		if name == nil {
			return
		}
		s := owner + tsType(w.text(name))
		w.add(name, s, whole)
		w.decls[len(w.decls)-1].classOrEnum = true
		if body := n.ChildByFieldName("body"); body != nil {
			for i := 0; i < int(body.NamedChildCount()); i++ {
				m := body.NamedChild(i)
				switch m.Type() {
				case "property_identifier":
					w.add(m, s+tsTerm(w.text(m)), nil)
				case "enum_assignment":
					if mn := m.ChildByFieldName("name"); mn != nil {
						w.add(mn, s+tsTerm(w.text(mn)), nil)
					}
				}
			}
		}
	case "internal_module", "module":
		name := n.ChildByFieldName("name")
		if name == nil || name.Type() != "identifier" && name.Type() != "string" {
			return // a.b.c namespaces: left out
		}
		s := owner + tsNamespace(w.text(name))
		w.add(name, s, nil)
		if body := n.ChildByFieldName("body"); body != nil {
			w.statements(body, s)
		}
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			d := n.NamedChild(i)
			if d.Type() != "variable_declarator" {
				continue
			}
			name := d.ChildByFieldName("name")
			if name == nil || name.Type() != "identifier" {
				continue // destructuring
			}
			var encl *sitter.Node
			if v := d.ChildByFieldName("value"); v != nil {
				switch v.Type() {
				case "arrow_function", "function_expression", "function", "generator_function":
					encl = v
				}
			}
			w.add(name, owner+tsTerm(w.text(name)), encl)
		}
	}
}

// signature adds the type parameters and the plain-identifier parameters of
// a function-like declaration whose symbol is s.
func (w *tsWalker) signature(n *sitter.Node, s string) {
	w.typeParams(n, s)
	w.literal(n.ChildByFieldName("return_type"), s)
	params := n.ChildByFieldName("parameters")
	if params == nil {
		return
	}
	for i := 0; i < int(params.NamedChildCount()); i++ {
		p := params.NamedChild(i)
		if p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
			continue
		}
		pat := p.ChildByFieldName("pattern")
		if pat != nil {
			w.literal(p.ChildByFieldName("type"), s+"("+tsEscape(w.text(pat))+")")
		}
		if pat != nil && pat.Type() == "rest_pattern" && pat.NamedChildCount() == 1 {
			pat = pat.NamedChild(0)
		}
		if pat == nil || pat.Type() != "identifier" {
			continue // destructured, or this
		}
		w.add(pat, s+"("+tsEscape(w.text(pat))+")", nil)
	}
}

func (w *tsWalker) typeParams(n *sitter.Node, s string) {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		return
	}
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		p := tp.NamedChild(i)
		if p.Type() != "type_parameter" {
			continue
		}
		if name := p.ChildByFieldName("name"); name != nil {
			w.add(name, s+"["+tsEscape(w.text(name))+"]", nil)
			if c := p.ChildByFieldName("constraint"); c != nil && c.NamedChildCount() == 1 {
				w.literal(c.NamedChild(0), s+"["+tsEscape(w.text(name))+"]")
			}
		}
	}
}

func (w *tsWalker) classBody(body *sitter.Node, cls string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		switch m.Type() {
		case "method_definition", "method_signature", "abstract_method_signature":
			name := m.ChildByFieldName("name")
			if name == nil || !tsSimpleMemberName(name) {
				continue
			}
			n := w.text(name)
			if n == "constructor" {
				s := cls + tsMethod("<constructor>")
				// scip-typescript puts the constructor's definition on the
				// declaration's first token: a modifier when one precedes it.
				w.add(m.Child(0), s, m)
				w.signature(m, s)
				continue
			}
			if acc := tsAccessor(m, name); acc != "" {
				n = "<" + acc + ">" + n
			}
			s := cls + tsMethod(n)
			w.add(name, s, m)
			w.signature(m, s)
		case "public_field_definition":
			name := m.ChildByFieldName("name")
			if name == nil || !tsSimpleMemberName(name) {
				continue
			}
			w.add(name, cls+tsTerm(w.text(name)), nil)
		}
	}
}

// typeMembers adds the property and method signatures of an interface body.
func (w *tsWalker) typeMembers(body *sitter.Node, owner string) {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		name := m.ChildByFieldName("name")
		if name == nil || !tsSimpleMemberName(name) {
			continue
		}
		switch m.Type() {
		case "property_signature":
			w.add(name, owner+tsTerm(w.text(name)), nil)
		case "method_signature":
			s := owner + tsMethod(w.text(name))
			w.add(name, s, nil)
			w.signature(m, s)
		}
	}
}

// tsSimpleMemberName leaves out computed and string-literal member names.
func tsSimpleMemberName(n *sitter.Node) bool {
	switch n.Type() {
	case "property_identifier", "identifier", "type_identifier":
		return true
	}
	return false
}

// tsAccessor reports "get" or "set" when one of those keywords precedes the
// member's name.
func tsAccessor(m, name *sitter.Node) string {
	for i := 0; i < int(m.ChildCount()); i++ {
		c := m.Child(i)
		if c.StartByte() >= name.StartByte() {
			break
		}
		if t := c.Type(); t == "get" || t == "set" {
			return t
		}
	}
	return ""
}

func tsEscape(name string) string { return scipDescriptorName(name) }
func tsType(name string) string   { return tsEscape(name) + "#" }
func tsTerm(name string) string   { return tsEscape(name) + "." }
func tsMethod(name string) string { return tsEscape(name) + "()." }
func tsNamespace(name string) string {
	return tsEscape(name) + "/"
}

// utf16Cols converts byte offsets to the 0-based line and UTF-16 column
// TypeScript reports.
type utf16Cols struct {
	src    []byte
	starts []uint32 // byte offset of each line
}

func newUTF16Cols(src []byte) *utf16Cols {
	c := &utf16Cols{src: src, starts: []uint32{0}}
	for i, b := range src {
		if b == '\n' {
			c.starts = append(c.starts, uint32(i+1)) // #nosec G115 -- file sizes fit
		}
	}
	return c
}

func (c *utf16Cols) pos(off uint32) (int32, int32) {
	lo, hi := 0, len(c.starts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if c.starts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	line := c.starts[lo]
	col := 0
	for b := c.src[line:off]; len(b) > 0; {
		r, size := utf8.DecodeRune(b)
		if r >= 0x10000 {
			col += 2
		} else {
			col++
		}
		b = b[size:]
	}
	return int32(lo), int32(col) // #nosec G115 -- positions fit
}

// tsFileSymbol is the symbol scip-typescript defines at [0,0,0] for the file
// itself: "scip-typescript npm <name> <version> src/lib/`a.ts`/".
func tsFileSymbol(d *scip.Document) string { return tsFileSymbolOf(d) }

// tsFileStart is where TypeScript starts a source file's node: its first
// token, after leading comments and whitespace.
func tsFileStart(root *sitter.Node) uint32 {
	for i := 0; i < int(root.ChildCount()); i++ {
		if c := root.Child(i); c.Type() != "comment" && c.Type() != "hash_bang_line" {
			return c.StartByte()
		}
	}
	return root.EndByte()
}

// tsParse parses src with the grammar for path. Nil on a syntax error.
func tsParse(path string, src []byte) (*sitter.Tree, *sitter.Node, int32) {
	p := sitter.NewParser()
	p.SetLanguage(tsLanguage(path))
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil || tree == nil {
		return nil, nil, -1
	}
	root := tree.RootNode()
	if root.HasError() {
		lines := map[int32]bool{}
		tsErrorLines(root, lines)
		first := int32(-1)
		for l := range lines {
			if first < 0 || l < first {
				first = l
			}
		}
		tree.Close()
		return nil, nil, first
	}
	return tree, root, -1
}

// tsFileRange is the EnclosingRange scip-typescript gives the file symbol.
func tsFileRange(root *sitter.Node, src []byte) []int32 {
	c := newUTF16Cols(src)
	sl, sc := c.pos(tsFileStart(root))
	el, ec := c.pos(uint32(len(src))) // #nosec G115 -- file sizes fit
	return scipRange(sl, sc, el, ec)
}

// tsLayer1 adds to a Layer 0 document the declarations that start on added
// lines, gives every declaration Layer 0 kept the extent the new source
// gives it, and drops the SymbolInformation of declarations gone from the
// file. It returns the number added and a reason when the file stays at
// Layer 0.
func tsLayer1(doc *scip.Document, base *scip.SCIPIndex, oldDoc *scip.Document, src []byte, lm *LineMap) (int, string) {
	prefix := tsFileSymbol(oldDoc)
	if prefix == "" {
		return 0, "no file symbol in the index"
	}
	tree, root, errLine := tsParse(doc.RelativePath, src)
	if tree == nil {
		if errLine >= 0 {
			return 0, fmt.Sprintf("syntax error at line %d (or a construct the parser does not know)", errLine+1)
		}
		return 0, "does not parse"
	}
	defer tree.Close()
	w := &tsWalker{src: src, cols: newUTF16Cols(src)}
	w.statements(root, "")
	newLines := addedLines(lm)
	decls := append(w.decls, tsNumberLiterals(doc, oldDoc, prefix, w.lits, newLines)...)
	added := tsApplyDecls(doc, base, oldDoc, prefix, decls, newLines)
	for _, o := range doc.Occurrences {
		if o.Symbol == prefix && o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
			o.EnclosingRange = tsFileRange(root, src)
		}
	}
	return added, ""
}

// tsApplyDecls is tsLayer1's declaration step; newLines nil means every line
// is new.
func tsApplyDecls(doc *scip.Document, base *scip.SCIPIndex, oldDoc *scip.Document, prefix string, decls []tsDecl, newLines map[int32]bool) int {
	type at struct {
		sym  string
		line int32
	}
	declared := map[string]bool{}
	byAt := map[at]*tsDecl{}
	for i := range decls {
		sym := prefix + decls[i].suffix
		declared[sym] = true
		byAt[at{sym, decls[i].line}] = &decls[i]
	}
	// Definitions Layer 0 kept: the extent from the new source.
	present := map[at]bool{}
	kept := map[string]bool{}
	for _, o := range doc.Occurrences {
		if o.SymbolRoles&scip.SymbolRoleDefinition == 0 {
			continue
		}
		k := at{o.Symbol, o.Range[0]}
		present[k] = true
		kept[o.Symbol] = true
		if d := byAt[k]; d != nil && len(o.EnclosingRange) > 0 && d.enclosing != nil {
			o.EnclosingRange = d.enclosing
		}
	}
	infos := map[string]*scip.SymbolInformation{}
	for _, s := range doc.Symbols {
		infos[s.Symbol] = s
	}
	added := 0
	for i := range decls {
		d := &decls[i]
		sym := prefix + d.suffix
		k := at{sym, d.line}
		if present[k] || newLines != nil && !newLines[d.line] {
			continue
		}
		occ := &scip.Occurrence{Range: scipRange(d.line, d.col, d.line, d.end), Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
		if d.enclosing != nil {
			occ.EnclosingRange = d.enclosing
		}
		doc.Occurrences = append(doc.Occurrences, occ)
		if infos[sym] == nil {
			info := &scip.SymbolInformation{Symbol: sym}
			if old := base.Symbols[sym]; old != nil {
				cp := *old
				info = &cp
			}
			infos[sym] = info
			doc.Symbols = append(doc.Symbols, info)
		}
		present[k] = true
		kept[sym] = true
		added++
	}
	// SymbolInformation of a declaration the file no longer has goes; the
	// file symbol and what Layer 1 does not synthesize (counters,
	// destructuring) stay as Layer 0 left them.
	out := doc.Symbols[:0]
	for _, s := range doc.Symbols {
		if kept[s.Symbol] || s.Symbol == prefix || !declared[s.Symbol] && !wasDeclaredIn(oldDoc, s.Symbol) || !tsSynthesized(s.Symbol, prefix) {
			out = append(out, s)
		}
	}
	doc.Symbols = out
	sortOccurrences(doc.Occurrences)
	return added
}

// tsNumberLiterals gives the type-literal members that start on new lines
// (all when newLines is nil) the ID scip-typescript writes, up to the
// number: a literal whose owner has exactly one number among the
// definitions of the base document and the overlay's keeps it (the uses
// Layer 0 kept, also in unchanged files, carry it), any other gets a
// placeholder above every number the file has. A fresh index renumbers them; until then a new member is
// defined and found under one ID in the overlay.
func tsNumberLiterals(doc, oldDoc *scip.Document, prefix string, lits []tsLit, newLines map[int32]bool) []tsDecl {
	if len(lits) == 0 {
		return nil
	}
	maxN := -1
	byOwner := map[string]map[int]bool{}
	occs := doc.Occurrences
	if oldDoc != nil {
		occs = append(append([]*scip.Occurrence(nil), occs...), oldDoc.Occurrences...)
	}
	for _, o := range occs {
		rest, ok := strings.CutPrefix(o.Symbol, prefix)
		if !ok {
			continue
		}
		for i := strings.Index(rest, "typeLiteral"); i >= 0; {
			j := i + len("typeLiteral")
			k := j
			for k < len(rest) && rest[k] >= '0' && rest[k] <= '9' {
				k++
			}
			if k > j && k < len(rest) && rest[k] == ':' {
				n, _ := strconv.Atoi(rest[j:k])
				maxN = max(maxN, n)
				if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
					if byOwner[rest[:i]] == nil {
						byOwner[rest[:i]] = map[int]bool{}
					}
					byOwner[rest[:i]][n] = true
				}
			}
			next := strings.Index(rest[k:], "typeLiteral")
			if next < 0 {
				break
			}
			i = k + next
		}
	}
	num := map[int]int{}
	var out []tsDecl
	for _, l := range lits {
		if newLines != nil && !newLines[l.decl.line] {
			continue
		}
		n, ok := num[l.group]
		if !ok {
			if ns := byOwner[l.owner]; len(ns) == 1 {
				for k := range ns {
					n = k
				}
			} else {
				maxN++
				n = maxN
			}
			num[l.group] = n
		}
		d := l.decl
		d.suffix = l.owner + "typeLiteral" + strconv.Itoa(n) + ":" + d.suffix
		out = append(out, d)
	}
	return out
}

// tsSynthesized reports whether Layer 1 would synthesize a declaration of
// sym (a descriptor without counters or destructuring), so its absence from
// the parse means it is gone.
func tsSynthesized(sym, prefix string) bool {
	rest := strings.TrimPrefix(sym, prefix)
	return rest != sym && !strings.Contains(rest, "typeLiteral") && !strings.Contains(rest, ":") && !strings.Contains(rest, "(`")
}

// tsNewDocument synthesizes the document of a TypeScript file the base index
// does not have, when a document of the same package tells the package
// fields and root. Nil when there is none or the file does not parse.
func tsNewDocument(base *scip.SCIPIndex, newPath string, src []byte) (*scip.Document, string) {
	prefix := tsNewFileSymbol(base, newPath)
	if prefix == "" {
		return nil, ""
	}
	tree, root, _ := tsParse(newPath, src)
	if tree == nil {
		return nil, ""
	}
	defer tree.Close()
	w := &tsWalker{src: src, cols: newUTF16Cols(src)}
	w.statements(root, "")
	lang := "typescript"
	if strings.HasSuffix(newPath, ".tsx") {
		lang = "typescriptreact"
	}
	doc := &scip.Document{RelativePath: newPath, Language: lang}
	doc.Occurrences = append(doc.Occurrences, &scip.Occurrence{Range: []int32{0, 0, 0}, Symbol: prefix, SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: tsFileRange(root, src)})
	doc.Symbols = append(doc.Symbols, &scip.SymbolInformation{Symbol: prefix})
	tsApplyDecls(doc, base, nil, prefix, append(w.decls, tsNumberLiterals(doc, nil, prefix, w.lits, nil)...), nil)
	return doc, prefix
}

// tsNewFileSymbol builds the file symbol of newPath from the base document
// whose package root is the longest prefix of newPath: scip-typescript names
// a file by its package's name and version and its path below the package
// root, one descriptor per segment.
func tsNewFileSymbol(base *scip.SCIPIndex, newPath string) string {
	bestRoot, bestHead := "", ""
	found := false
	for _, d := range base.Documents {
		sym := tsFileSymbol(d)
		if sym == "" {
			continue
		}
		head, segs, ok := tsSplitFileSymbol(sym)
		if !ok {
			continue
		}
		parts := strings.Split(d.RelativePath, "/")
		if len(segs) > len(parts) {
			continue
		}
		root := strings.Join(parts[:len(parts)-len(segs)], "/")
		if root != "" && !strings.HasPrefix(newPath, root+"/") {
			continue
		}
		if !found || len(root) > len(bestRoot) {
			bestRoot, bestHead, found = root, head, true
		}
	}
	if !found {
		return ""
	}
	rel := newPath
	if bestRoot != "" {
		rel = strings.TrimPrefix(newPath, bestRoot+"/")
	}
	var b strings.Builder
	b.WriteString(bestHead)
	for _, s := range strings.Split(rel, "/") {
		b.WriteString(tsEscape(s))
		b.WriteByte('/')
	}
	return b.String()
}

// tsSplitFileSymbol splits a file symbol into its head ("scip-typescript npm
// <name> <version> ") and the path segments of its descriptors.
func tsSplitFileSymbol(sym string) (string, []string, bool) {
	fields := 0
	i := 0
	for ; i < len(sym) && fields < 4; i++ {
		if sym[i] == ' ' {
			fields++
		}
	}
	if fields < 4 {
		return "", nil, false
	}
	head, desc := sym[:i], sym[i:]
	var segs []string
	for desc != "" {
		var name string
		if desc[0] == '`' {
			j := 1
			for ; j < len(desc); j++ {
				if desc[j] == '`' {
					if j+1 < len(desc) && desc[j+1] == '`' {
						j++
						continue
					}
					break
				}
			}
			if j >= len(desc) {
				return "", nil, false
			}
			name = strings.ReplaceAll(desc[1:j], "``", "`")
			desc = desc[j+1:]
		} else {
			j := strings.IndexByte(desc, '/')
			if j < 0 {
				return "", nil, false
			}
			name = desc[:j]
			desc = desc[j:]
		}
		if desc == "" || desc[0] != '/' {
			return "", nil, false // not a pure namespace path
		}
		desc = desc[1:]
		segs = append(segs, name)
	}
	return head, segs, len(segs) > 0
}
