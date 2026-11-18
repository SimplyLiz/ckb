//go:build cgo

package overlay

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Unions, intersections and narrowing for TypeScript's Layer 2.
//
// A union of repository types stays tyUnknown to every rule that does not
// ask for its parts, so nothing that treated a union as unknown before
// changes. A member access on a union is a property of the union, and
// scip-typescript writes one occurrence per declaration of that property:
// one per remaining part, after narrowing. So the access gets every part's
// member, or nothing when a part's is not known.
//
// Narrowing is followed only on an identifier that is not reassigned (a
// const, or a parameter or let without assignments), through conditions on a
// discriminant (x.kind === "a", !==, typeof x === "string", && || ! ?:, if,
// else, switch cases, an earlier if that returns). A property chain
// (o.t.kind) is a reference TypeScript narrows too: a union reached through
// one stays unknown. A declaration that annotates a union and initializes it
// is narrowed by the assignment: unknown as well.

// tsUnionParts builds the parts of a union or an intersection type node:
// nil when one part is not known.
func (r *tsResolver) tsUnionParts(m *tsModule, t *sitter.Node, depth int) (parts []tsTy, nullable bool, ok bool) {
	for i := 0; i < int(t.NamedChildCount()); i++ {
		c := t.NamedChild(i)
		if tsNullish(c, m.src) {
			nullable = true
			continue
		}
		p := r.typeOfNode(m, c, depth+1)
		if tsZod(p) || p.k == tyPick && p.elem != nil && tsZod(*p.elem) {
			return nil, false, false // a schema's keys: narrowing reads type literals
		}
		switch p.k {
		case tyDecl, tyLit, tyPick:
		case tyOutside:
			p.m, p.node = m, c // the node says which primitive, for typeof and literals
		case tyUnknown:
			if p.parts == nil {
				return nil, false, false
			}
		default:
			return nil, false, false
		}
		parts = append(parts, p)
	}
	return parts, nullable, len(parts) > 0
}

// tsInUnionOrIntersection reports whether an object type is a part of a
// union or an intersection: scip-typescript gives its members local symbols
// there, or what the declaring document says.
func tsInUnionOrIntersection(t *sitter.Node) bool {
	p := t.Parent()
	for p != nil && p.Type() == "parenthesized_type" {
		p = p.Parent()
	}
	return p != nil && (p.Type() == "union_type" || p.Type() == "intersection_type")
}

// defAt is the symbol the document of m defines at n's start, "" for none.
func (r *tsResolver) defAt(m *tsModule, n *sitter.Node) string {
	if m.defPos == nil {
		m.defPos = map[[2]int32]string{}
		if d := r.docs(m.file); d != nil {
			for _, o := range d.Occurrences {
				if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
					m.defPos[[2]int32{o.Range[0], o.Range[1]}] = o.Symbol
				}
			}
		}
		m.cols = newUTF16Cols(m.src)
	}
	l, c := m.cols.pos(n.StartByte())
	return m.defPos[[2]int32{l, c}]
}

// posLitMember is the member of an object type whose owner Layer 1 does not
// name: the symbol the declaring document has at the member's name.
func (r *tsResolver) posLitMember(t tsTy, name string, depth int) tsMember {
	mn := tsFindMember(t.node, name, t.m.src)
	if mn == nil {
		return tsMember{k: tyUnknown}
	}
	nn := mn.ChildByFieldName("name")
	if nn == nil {
		return tsMember{k: tyUnknown}
	}
	sym := r.defAt(t.m, nn)
	switch {
	case sym == "":
		return tsMember{k: tyUnknown}
	case strings.HasPrefix(sym, "local "):
		return tsMember{typ: r.memberType(t.m, mn, depth), k: tyOutside}
	}
	return tsMember{sym: sym, typ: r.memberType(t.m, mn, depth), k: tyDecl}
}

// lacks reports whether t certainly has no member name: an object type or
// an interface without bases and index signatures that does not declare it.
func (r *tsResolver) lacks(t tsTy, name string) bool {
	var body *sitter.Node
	switch t.k {
	case tyLit:
		if tsZod(t) {
			return false
		}
		body = t.node
	case tyDecl:
		if t.node.Type() != "interface_declaration" {
			return false
		}
		for i := 0; i < int(t.node.NamedChildCount()); i++ {
			if t.node.NamedChild(i).Type() == "extends_type_clause" {
				return false
			}
		}
		body = t.node.ChildByFieldName("body")
	default:
		return false
	}
	if body == nil {
		return false
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		switch body.NamedChild(i).Type() {
		case "index_signature", "call_signature", "construct_signature":
			return false
		}
	}
	return tsFindMember(body, name, t.m.src) == nil
}

// interMember is a member of an intersection: the one part that has it.
func (r *tsResolver) interMember(t tsTy, name string, depth int) tsMember {
	var found *tsMember
	for _, p := range t.parts {
		if r.lacks(p, name) {
			continue
		}
		mb := r.memberOf(p, name, depth+1)
		switch mb.k {
		case tyDecl, tyOutside:
		default:
			return tsMember{k: tyUnknown}
		}
		if found != nil {
			return tsMember{k: tyUnknown} // two parts declare it
		}
		found = &mb
	}
	if found == nil {
		return tsMember{k: tyUnknown}
	}
	return *found
}

// unionMember is a member of a union: every part's declaration of it.
func (r *tsResolver) unionMember(t tsTy, name string, depth int) tsMember {
	var out tsMember
	seen := map[string]bool{}
	first := true
	for _, p := range t.parts {
		mb := r.memberOf(p, name, depth+1)
		switch mb.k {
		case tyDecl:
			if !seen[mb.sym] {
				seen[mb.sym] = true
				if out.sym == "" {
					out.sym = mb.sym
				} else {
					out.more = append(out.more, mb.sym)
				}
			}
		case tyOutside:
		default:
			return tsMember{k: tyUnknown}
		}
		if first {
			out.typ, first = mb.typ, false
		} else {
			out.typ = tsJoin(out.typ, mb.typ)
		}
	}
	out.k = tyOutside
	if out.sym != "" {
		out.k = tyDecl
	}
	return out
}

// memberOfRef is memberOf for a member access recv.name: a union is
// narrowed at recv first, and only an identifier's or a call's is followed.
func (r *tsResolver) memberOfRef(m *tsModule, t tsTy, name string, recv *sitter.Node, depth int) tsMember {
	if t.k != tyUnknown || t.parts == nil || t.inter {
		return r.memberOf(t, name, depth)
	}
	for recv != nil && (recv.Type() == "parenthesized_expression" || recv.Type() == "non_null_expression") && recv.NamedChildCount() == 1 {
		recv = recv.NamedChild(0)
	}
	if recv == nil {
		return tsMember{k: tyUnknown}
	}
	switch recv.Type() {
	case "identifier":
		var ok bool
		if t, ok = r.narrow(m, recv, t, depth); !ok {
			return tsMember{k: tyUnknown}
		}
	case "call_expression", "await_expression":
	default:
		return tsMember{k: tyUnknown}
	}
	if t.k != tyUnknown || t.parts == nil || t.inter {
		return r.memberOf(t, name, depth)
	}
	return r.unionMember(t, name, depth+1)
}

// tsNarrowState is a union being narrowed at one identifier. keep sets
// are over parts; abort means a condition tests the identifier in a way
// Layer 2 does not model, so the type there is not known.
type tsNarrowState struct {
	r       *tsResolver
	m       *tsModule
	name    string
	binding uint32 // the binding identifier's start, 0 for a top-level name
	parts   []tsTy
	depth   int
	abort   bool
}

type tsKeep []bool

func (st *tsNarrowState) all() tsKeep {
	k := make(tsKeep, len(st.parts))
	for i := range k {
		k[i] = true
	}
	return k
}

func tsAnd(a, b tsKeep) tsKeep {
	out := make(tsKeep, len(a))
	for i := range a {
		out[i] = a[i] && b[i]
	}
	return out
}

func tsOr(a, b tsKeep) tsKeep {
	out := make(tsKeep, len(a))
	for i := range a {
		out[i] = a[i] || b[i]
	}
	return out
}

// narrow is t at the identifier id after the conditions that guard it. ok
// is false when id may be reassigned (TypeScript narrows on assignment) or a
// condition tests it in a way not modeled.
func (r *tsResolver) narrow(m *tsModule, id *sitter.Node, t tsTy, depth int) (tsTy, bool) {
	name := id.Content(m.src)
	b, local := tsLocalBinding(id, name, m.src)
	var scope *sitter.Node
	if local {
		if b == nil {
			return t, false
		}
		scope = tsBindingScope(b)
		if !tsConstBinding(b) && tsAssigned(scope, name, m.src) {
			return t, false
		}
	} else if tsAssigned(m.root, name, m.src) {
		return t, false
	}
	st := &tsNarrowState{r: r, m: m, name: name, parts: t.parts, depth: depth}
	if b != nil {
		st.binding = b.StartByte()
	}
	keep := st.all()
	// Past a method, TypeScript's narrowing is not followed: a condition
	// beyond it that would narrow makes the type unknown.
	var probe tsKeep
	c := id
	for a := id.Parent(); a != nil && !st.abort; c, a = a, a.Parent() {
		atScope := scope != nil && tsSame(a, scope)
		switch a.Type() {
		case "function_declaration", "generator_function_declaration":
			a = nil // hoisted: its body is not narrowed by its surroundings
		case "method_definition", "class_body":
			if probe == nil {
				probe = keep
			}
		case "binary_expression":
			op := a.ChildByFieldName("operator")
			left, right := a.ChildByFieldName("left"), a.ChildByFieldName("right")
			if op == nil || left == nil || right == nil || !tsSame(right, c) {
				break
			}
			switch op.Type() {
			case "&&":
				keep = tsAnd(keep, st.cond(left, true))
			case "||":
				keep = tsAnd(keep, st.cond(left, false))
			case "??":
			default:
				if st.mentions(left) {
					st.abort = true
				}
			}
		case "ternary_expression":
			cond := a.ChildByFieldName("condition")
			if cond == nil || tsSame(cond, c) {
				break
			}
			if x := a.ChildByFieldName("consequence"); x != nil && tsSame(x, c) {
				keep = tsAnd(keep, st.cond(cond, true))
			} else if x := a.ChildByFieldName("alternative"); x != nil && tsSame(x, c) {
				keep = tsAnd(keep, st.cond(cond, false))
			}
		case "if_statement":
			cond := a.ChildByFieldName("condition")
			if cond == nil || tsSame(cond, c) {
				break
			}
			if x := a.ChildByFieldName("consequence"); x != nil && tsSame(x, c) {
				keep = tsAnd(keep, st.cond(cond, true))
			} else if x := a.ChildByFieldName("alternative"); x != nil && tsSame(x, c) {
				keep = tsAnd(keep, st.cond(cond, false))
			}
		case "while_statement", "do_statement", "for_statement":
			if cond := a.ChildByFieldName("condition"); cond != nil && st.mentions(cond) {
				st.abort = true
			}
		case "switch_body":
			keep = tsAnd(keep, st.switchCase(a, c))
		}
		if a == nil {
			break
		}
		switch a.Type() {
		case "statement_block", "program", "switch_case", "switch_default":
			keep = tsAnd(keep, st.before(a, c))
		}
		if atScope {
			break // conditions outside the binding's scope cannot test it
		}
	}
	if st.abort {
		return tsUnknownTy, false
	}
	if probe != nil {
		for i := range keep {
			if keep[i] != probe[i] {
				return tsUnknownTy, false
			}
		}
	}
	var parts []tsTy
	for i, p := range st.parts {
		if keep[i] {
			parts = append(parts, p)
		}
	}
	switch len(parts) {
	case 0:
		return tsUnknownTy, false // never: a condition misread
	case 1:
		p := parts[0]
		if p.k == tyOutside {
			p.m, p.node = nil, nil
		}
		return p, true
	}
	out := t
	out.parts = parts
	return out, true
}

// before narrows for c by the statements of block that precede it: an if
// one of whose branches ends the block, an assertion.
func (st *tsNarrowState) before(block, c *sitter.Node) tsKeep {
	keep := st.all()
	for i := 0; i < int(block.NamedChildCount()); i++ {
		s := block.NamedChild(i)
		if s.StartByte() >= c.StartByte() {
			break
		}
		switch s.Type() {
		case "if_statement":
			cond := s.ChildByFieldName("condition")
			if cond == nil || !st.mentions(cond) {
				continue
			}
			cons, alt := s.ChildByFieldName("consequence"), s.ChildByFieldName("alternative")
			ce := cons != nil && tsExits(cons)
			ae := alt != nil && tsExits(alt)
			switch {
			case ce && ae:
				st.abort = true // c is not reached
			case ce:
				keep = tsAnd(keep, st.cond(cond, false))
			case ae:
				keep = tsAnd(keep, st.cond(cond, true))
			}
		case "switch_statement", "try_statement", "labeled_statement":
			if st.mentions(s) && tsMayExit(s) {
				st.abort = true // a case or a branch that returns narrows what follows
			}
		case "expression_statement":
			if st.asserts(s) {
				st.abort = true
			}
		}
	}
	return keep
}

// tsMayExit reports a return, a throw, a break or a continue inside s.
func tsMayExit(s *sitter.Node) bool {
	switch s.Type() {
	case "return_statement", "throw_statement", "break_statement", "continue_statement":
		return true
	case "function_declaration", "function_expression", "arrow_function", "method_definition", "class_body":
		return false
	}
	for i := 0; i < int(s.NamedChildCount()); i++ {
		if tsMayExit(s.NamedChild(i)) {
			return true
		}
	}
	return false
}

// asserts reports a call statement that may be an assertion about the
// identifier: a repository function with an asserts return type, or one
// named like an assertion (assert, invariant, expect, ensure) whose
// arguments mention it. Other calls of packages are taken as not asserting.
func (st *tsNarrowState) asserts(s *sitter.Node) bool {
	e := s.NamedChild(0)
	for e != nil && e.Type() == "await_expression" && e.NamedChildCount() == 1 {
		e = e.NamedChild(0)
	}
	if e == nil || e.Type() != "call_expression" {
		return false
	}
	args := e.ChildByFieldName("arguments")
	if args == nil || !st.mentions(args) {
		return false
	}
	fn := e.ChildByFieldName("function")
	if fn == nil {
		return false
	}
	name := strings.ToLower(fn.Content(st.m.src))
	for _, w := range []string{"assert", "invariant", "expect", "ensure"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	if ft := st.r.exprType(st.m, fn, st.depth+1); ft.k == tyFn {
		if rt := ft.node.ChildByFieldName("return_type"); rt != nil && strings.Contains(rt.Content(ft.m.src), "asserts") {
			return true
		}
	}
	return false
}

func tsSame(a, b *sitter.Node) bool {
	return a.StartByte() == b.StartByte() && a.EndByte() == b.EndByte() && a.Type() == b.Type()
}

// tsBindingScope is the node a binding identifier is visible in: its
// function (parameters), its block, or the file.
func tsBindingScope(b *sitter.Node) *sitter.Node {
	for a := b.Parent(); a != nil; a = a.Parent() {
		switch a.Type() {
		case "formal_parameters":
			if fn := a.Parent(); fn != nil {
				return fn
			}
		case "arrow_function":
			return a // x => …
		case "statement_block", "program", "for_statement", "for_in_statement", "catch_clause", "switch_body":
			return a
		}
	}
	return nil
}

// tsConstBinding reports a const declaration's name.
func tsConstBinding(b *sitter.Node) bool {
	if b == nil {
		return false
	}
	for a := b.Parent(); a != nil; a = a.Parent() {
		switch a.Type() {
		case "variable_declarator", "object_pattern", "array_pattern", "pair_pattern", "shorthand_property_identifier_pattern":
			continue
		case "lexical_declaration":
			k := a.Child(0)
			return k != nil && k.Type() == "const"
		}
		return false
	}
	return false
}

// tsAssigned reports an assignment to name anywhere in scope (shadowing
// ignored: a false positive only loses narrowing).
func tsAssigned(scope *sitter.Node, name string, src []byte) bool {
	if scope == nil {
		return true
	}
	found := false
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if found {
			return
		}
		switch n.Type() {
		case "assignment_expression", "augmented_assignment_expression":
			if l := n.ChildByFieldName("left"); l != nil && tsPatternNames(l, name, src) {
				found = true
				return
			}
		case "update_expression":
			if a := n.ChildByFieldName("argument"); a != nil && a.Type() == "identifier" && a.Content(src) == name {
				found = true
				return
			}
		case "for_in_statement":
			if l := n.ChildByFieldName("left"); l != nil && l.Type() == "identifier" && l.Content(src) == name {
				found = true
				return
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(scope)
	return found
}

// tsPatternNames reports whether an assignment target writes name.
func tsPatternNames(l *sitter.Node, name string, src []byte) bool {
	switch l.Type() {
	case "identifier", "shorthand_property_identifier_pattern":
		return l.Content(src) == name
	case "object_pattern", "array_pattern", "pair_pattern", "assignment_pattern", "object_assignment_pattern", "rest_pattern", "parenthesized_expression":
		for i := 0; i < int(l.NamedChildCount()); i++ {
			c := l.NamedChild(i)
			if k := l.ChildByFieldName("key"); l.Type() == "pair_pattern" && k != nil && tsSame(c, k) {
				continue
			}
			if tsPatternNames(c, name, src) {
				return true
			}
		}
	}
	return false
}

// tsExits reports a statement after which the block does not go on.
func tsExits(s *sitter.Node) bool {
	switch s.Type() {
	case "return_statement", "throw_statement", "continue_statement", "break_statement":
		return true
	case "else_clause":
		if s.NamedChildCount() > 0 {
			return tsExits(s.NamedChild(int(s.NamedChildCount()) - 1))
		}
	case "if_statement":
		cons, alt := s.ChildByFieldName("consequence"), s.ChildByFieldName("alternative")
		return cons != nil && alt != nil && tsExits(cons) && tsExits(alt)
	case "statement_block":
		for i := int(s.NamedChildCount()) - 1; i >= 0; i-- {
			c := s.NamedChild(i)
			if c.Type() == "comment" {
				continue
			}
			return tsExits(c)
		}
	}
	return false
}

// refersHere reports whether e is the identifier being narrowed (the same
// binding).
func (st *tsNarrowState) refersHere(e *sitter.Node) bool {
	for e != nil && (e.Type() == "parenthesized_expression" || e.Type() == "non_null_expression") && e.NamedChildCount() == 1 {
		e = e.NamedChild(0)
	}
	if e == nil || e.Type() != "identifier" && e.Type() != "shorthand_property_identifier" || e.Content(st.m.src) != st.name {
		return false
	}
	b, local := tsLocalBinding(e, st.name, st.m.src)
	if !local {
		return st.binding == 0
	}
	return b != nil && b.StartByte() == st.binding
}

// mentions reports whether n refers to the identifier, directly or through
// a const it initializes (TypeScript narrows by aliased conditions).
func (st *tsNarrowState) mentions(n *sitter.Node) bool {
	return st.mentions1(n, 0)
}

func (st *tsNarrowState) mentions1(n *sitter.Node, depth int) bool {
	switch n.Type() {
	case "identifier", "shorthand_property_identifier":
		if st.refersHere(n) {
			return true
		}
		if depth > 0 {
			return false
		}
		if b, local := tsLocalBinding(n, n.Content(st.m.src), st.m.src); local && b != nil && tsConstBinding(b) {
			// const ok = r.ok, and const { kind } = r (a destructured
			// discriminant, which TypeScript narrows r by too).
			d := b.Parent()
			for d != nil && (d.Type() == "object_pattern" || d.Type() == "array_pattern" || d.Type() == "pair_pattern" || d.Type() == "shorthand_property_identifier_pattern" || d.Type() == "object_assignment_pattern") {
				d = d.Parent()
			}
			if d != nil && d.Type() == "variable_declarator" {
				if v := d.ChildByFieldName("value"); v != nil && st.mentions1(v, depth+1) {
					return true
				}
			}
		}
		return false
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if st.mentions1(n.NamedChild(i), depth) {
			return true
		}
	}
	return false
}

// discriminant is x.P (or x?.P) of the narrowed x: P.
func (st *tsNarrowState) discriminant(e *sitter.Node) (string, bool) {
	if e.Type() != "member_expression" {
		return "", false
	}
	obj, prop := e.ChildByFieldName("object"), e.ChildByFieldName("property")
	if obj == nil || prop == nil || prop.Type() != "property_identifier" || !st.refersHere(obj) {
		return "", false
	}
	return prop.Content(st.m.src), true
}

// tsLiteralValue is a literal expression's value as a literal type spells
// it: "a" for "a" and 'a', true, 1.
func tsLiteralValue(e *sitter.Node, src []byte) (string, bool) {
	switch e.Type() {
	case "string":
		return tsStringValue(e.Content(src)), true
	case "true", "false", "number":
		return e.Content(src), true
	}
	return "", false
}

func tsNullLiteral(e *sitter.Node, src []byte) bool {
	return e.Type() == "null" || e.Type() == "undefined" || e.Type() == "identifier" && e.Content(src) == "undefined"
}

// cond is the parts for which cond can be truth.
func (st *tsNarrowState) cond(cond *sitter.Node, truth bool) tsKeep {
	src := st.m.src
	switch cond.Type() {
	case "parenthesized_expression":
		if cond.NamedChildCount() == 1 {
			return st.cond(cond.NamedChild(0), truth)
		}
	case "unary_expression":
		if op := cond.ChildByFieldName("operator"); op != nil && op.Type() == "!" {
			if a := cond.ChildByFieldName("argument"); a != nil {
				return st.cond(a, !truth)
			}
		}
	case "identifier":
		if st.refersHere(cond) {
			return st.all() // truthiness: only null and undefined go
		}
	case "member_expression":
		if p, ok := st.discriminant(cond); ok {
			return st.byTruthiness(p, truth)
		}
	case "call_expression":
		if k, ok := st.predicate(cond, truth); ok {
			return k
		}
	case "binary_expression":
		op, left, right := cond.ChildByFieldName("operator"), cond.ChildByFieldName("left"), cond.ChildByFieldName("right")
		if op == nil || left == nil || right == nil {
			break
		}
		switch op.Type() {
		case "&&":
			if truth {
				return tsAnd(st.cond(left, true), st.cond(right, true))
			}
			return tsOr(st.cond(left, false), tsAnd(st.cond(left, true), st.cond(right, false)))
		case "||":
			if truth {
				return tsOr(st.cond(left, true), tsAnd(st.cond(left, false), st.cond(right, true)))
			}
			return tsAnd(st.cond(left, false), st.cond(right, false))
		case "in":
			if left.Type() == "string" && st.refersHere(right) {
				return st.byIn(tsStringValue(left.Content(src)), truth)
			}
		case "===", "==", "!==", "!=":
			if op.Type() == "!==" || op.Type() == "!=" {
				truth = !truth
			}
			if _, ok := tsLiteralValue(left, src); ok || tsNullLiteral(left, src) {
				left, right = right, left
			}
			if tsNullLiteral(right, src) {
				if st.refersHere(left) {
					return st.all() // x == null: only null and undefined
				}
				break
			}
			val, ok := tsLiteralValue(right, src)
			if !ok {
				break
			}
			if p, ok := st.discriminant(left); ok {
				return st.byValue(p, val, truth)
			}
			if left.Type() == "unary_expression" {
				if o := left.ChildByFieldName("operator"); o != nil && o.Type() == "typeof" {
					if a := left.ChildByFieldName("argument"); a != nil && st.refersHere(a) && right.Type() == "string" {
						return st.byTypeof(val, truth)
					}
				}
			}
		}
	}
	if st.mentions(cond) {
		st.abort = true
	}
	return st.all()
}

// byValue keeps the parts whose P can be val (truth) or something else.
func (st *tsNarrowState) byValue(p, val string, truth bool) tsKeep {
	keep := st.all()
	for i, part := range st.parts {
		pv, _, known := st.r.discValues(part, p, st.depth+1)
		if !known {
			continue
		}
		if truth {
			keep[i] = pv[val]
		} else {
			keep[i] = !(len(pv) == 1 && pv[val])
		}
	}
	return keep
}

// byTruthiness keeps the parts whose P can be truthy (truth) or falsy.
func (st *tsNarrowState) byTruthiness(p string, truth bool) tsKeep {
	keep := st.all()
	for i, part := range st.parts {
		pv, optional, known := st.r.discValues(part, p, st.depth+1)
		if !known {
			continue
		}
		truthy, falsy := false, optional
		for v := range pv {
			switch v {
			case "false", "0", "", "null", "undefined":
				falsy = true
			default:
				truthy = true
			}
		}
		keep[i] = truthy && truth || falsy && !truth
	}
	return keep
}

// byIn keeps the parts that may have key (truth) or may lack it.
func (st *tsNarrowState) byIn(key string, truth bool) tsKeep {
	keep := st.all()
	for i, part := range st.parts {
		if truth {
			keep[i] = !st.r.lacks(part, key)
			continue
		}
		if part.k == tyLit || part.k == tyDecl && part.node.Type() == "interface_declaration" {
			body := part.node
			if part.k == tyDecl {
				body = part.node.ChildByFieldName("body")
			}
			if mn := tsFindMember(body, key, part.m.src); mn != nil && !tsOptionalMember(mn) {
				keep[i] = false
			}
		}
	}
	return keep
}

// byTypeof keeps the parts typeof can call kind (truth) or not.
func (st *tsNarrowState) byTypeof(kind string, truth bool) tsKeep {
	keep := st.all()
	for i, part := range st.parts {
		switch pk := st.r.typeofKind(part); {
		case pk == "":
		case truth:
			keep[i] = pk == kind
		default:
			keep[i] = pk != kind
		}
	}
	return keep
}

// predicate is a call of a repository type guard (x is T) on the
// identifier: the parts that are T (truth) or not. ok is false when the
// call is not one, or T is not one of the parts.
func (st *tsNarrowState) predicate(call *sitter.Node, truth bool) (tsKeep, bool) {
	fn, args := call.ChildByFieldName("function"), call.ChildByFieldName("arguments")
	if fn == nil || args == nil || !st.mentions(args) {
		return nil, false
	}
	ft := st.r.exprType(st.m, fn, st.depth+1)
	if ft.k != tyFn {
		return nil, false
	}
	rt := ft.node.ChildByFieldName("return_type")
	if rt == nil || rt.Type() != "type_predicate_annotation" || rt.NamedChildCount() != 1 {
		return nil, false
	}
	pr := rt.NamedChild(0)
	pn, pt := pr.ChildByFieldName("name"), pr.ChildByFieldName("type")
	if pr.Type() != "type_predicate" || pn == nil || pt == nil {
		return nil, false
	}
	// The argument at the predicate's parameter must be the identifier.
	ps := ft.node.ChildByFieldName("parameters")
	if ps == nil {
		return nil, false
	}
	at := -1
	for i, j := 0, 0; i < int(ps.NamedChildCount()); i++ {
		p := ps.NamedChild(i)
		if p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
			continue
		}
		if pat := p.ChildByFieldName("pattern"); pat != nil && pat.Content(ft.m.src) == pn.Content(ft.m.src) {
			at = j
		}
		j++
	}
	var argList []*sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if a := args.NamedChild(i); a.Type() != "comment" {
			argList = append(argList, a)
		}
	}
	if at < 0 || at >= len(argList) || !st.refersHere(argList[at]) {
		return nil, false
	}
	target := st.r.typeOfNode(ft.m, pt, st.depth+1)
	keep := make(tsKeep, len(st.parts))
	hit := false
	for i, part := range st.parts {
		same := tsSameTy(part, target)
		hit = hit || same
		keep[i] = same == truth
	}
	if !hit {
		return nil, false
	}
	return keep, true
}

// tsSameTy reports the same declared type.
func tsSameTy(a, b tsTy) bool {
	if a.k != b.k || a.node == nil || b.node == nil || a.m == nil || b.m == nil {
		return false
	}
	return a.m.file == b.m.file && tsSame(a.node, b.node)
}

// switchCase is the parts for c, a case of switch (x.P): the labels of its
// case and of the empty cases falling into it.
func (st *tsNarrowState) switchCase(body, c *sitter.Node) tsKeep {
	sw := body.Parent()
	if sw == nil || sw.Type() != "switch_statement" || c.Type() != "switch_case" && c.Type() != "switch_default" {
		return st.all()
	}
	v := sw.ChildByFieldName("value")
	for v != nil && v.Type() == "parenthesized_expression" && v.NamedChildCount() == 1 {
		v = v.NamedChild(0)
	}
	if v == nil {
		return st.all()
	}
	p, ok := st.discriminant(v)
	if !ok {
		if st.mentions(v) {
			st.abort = true
		}
		return st.all()
	}
	var cases []*sitter.Node
	idx := -1
	for i := 0; i < int(body.NamedChildCount()); i++ {
		k := body.NamedChild(i)
		if k.Type() != "switch_case" && k.Type() != "switch_default" {
			continue
		}
		if tsSame(k, c) {
			idx = len(cases)
		}
		cases = append(cases, k)
	}
	if idx < 0 {
		return st.all()
	}
	label := func(k *sitter.Node) (string, bool) {
		if k.Type() != "switch_case" {
			return "", false
		}
		lv := k.ChildByFieldName("value")
		if lv == nil {
			return "", false
		}
		return tsLiteralValue(lv, st.m.src)
	}
	if c.Type() == "switch_default" {
		keep := st.all()
		for _, k := range cases {
			l, ok := label(k)
			if !ok {
				continue // default itself
			}
			keep = tsAnd(keep, st.byValue(p, l, false))
		}
		if idx > 0 && tsCaseBody(cases[idx-1]) == nil || idx > 0 && !tsExits(tsCaseBody(cases[idx-1])) {
			st.abort = true // a case falls into default
		}
		return keep
	}
	keep := make(tsKeep, len(st.parts))
	for i := idx; i >= 0; i-- {
		k := cases[i]
		if i < idx && tsCaseBody(k) != nil {
			if !tsExits(tsCaseBody(k)) {
				st.abort = true // falls through with statements
			}
			break
		}
		l, ok := label(k)
		if !ok {
			st.abort = true // default falls into this case
			break
		}
		keep = tsOr(keep, st.byValue(p, l, true))
	}
	return keep
}

// tsCaseBody is a case's last statement, nil for an empty case.
func tsCaseBody(k *sitter.Node) *sitter.Node {
	var last *sitter.Node
	v := k.ChildByFieldName("value")
	for i := 0; i < int(k.NamedChildCount()); i++ {
		c := k.NamedChild(i)
		if c.Type() == "comment" || v != nil && tsSame(c, v) {
			continue
		}
		last = c
	}
	return last
}

// typeofKind is what typeof says of a union part, "" when not known.
func (r *tsResolver) typeofKind(p tsTy) string {
	switch p.k {
	case tyDecl:
		if p.node.Type() == "class_declaration" || p.node.Type() == "abstract_class_declaration" {
			return "object"
		}
		if body := p.node.ChildByFieldName("body"); body != nil && !tsHasCallSignature(body) {
			return "object"
		}
		return ""
	case tyLit:
		if !tsHasCallSignature(p.node) {
			return "object"
		}
	case tyOutside:
		if p.node == nil || p.m == nil {
			return ""
		}
		n := p.node
		switch n.Type() {
		case "predefined_type":
			switch s := n.Content(p.m.src); s {
			case "string", "number", "boolean", "bigint", "symbol":
				return s
			}
		case "template_literal_type":
			return "string"
		case "literal_type":
			if n.NamedChildCount() == 1 {
				switch n.NamedChild(0).Type() {
				case "string":
					return "string"
				case "number", "unary_expression":
					return "number"
				case "true", "false":
					return "boolean"
				}
			}
		case "array_type", "tuple_type":
			return "object"
		}
	}
	return ""
}

func tsHasCallSignature(body *sitter.Node) bool {
	for i := 0; i < int(body.NamedChildCount()); i++ {
		if t := body.NamedChild(i).Type(); t == "call_signature" || t == "construct_signature" {
			return true
		}
	}
	return false
}

// discValues are the literal values a part's member P can have and
// whether it is optional; known is false when the part does not spell them.
func (r *tsResolver) discValues(part tsTy, p string, depth int) (vals map[string]bool, optional, known bool) {
	if depth > tsMaxDepth {
		return nil, false, false
	}
	var body *sitter.Node
	switch part.k {
	case tyLit:
		body = part.node
	case tyDecl:
		body = part.node.ChildByFieldName("body")
	case tyPick:
		if part.keys != nil && part.keys[p] == part.omit || part.optional {
			return nil, false, false
		}
		return r.discValues(part.of(), p, depth+1)
	case tyUnknown:
		if !part.inter {
			return nil, false, false
		}
		for _, q := range part.parts {
			if r.lacks(q, p) {
				continue
			}
			return r.discValues(q, p, depth+1)
		}
		return nil, false, false
	default:
		return nil, false, false
	}
	mn := tsFindMember(body, p, part.m.src)
	if mn == nil || mn.Type() != "property_signature" && mn.Type() != "public_field_definition" {
		return nil, false, false
	}
	t := mn.ChildByFieldName("type")
	if t == nil || t.NamedChildCount() != 1 {
		return nil, false, false
	}
	vals = map[string]bool{}
	if !tsLiteralValues(t.NamedChild(0), part.m.src, vals) {
		return nil, false, false
	}
	return vals, tsOptionalMember(mn), true
}

// tsLiteralValues collects a literal type or a union of them.
func tsLiteralValues(t *sitter.Node, src []byte, into map[string]bool) bool {
	switch t.Type() {
	case "union_type", "parenthesized_type":
		for i := 0; i < int(t.NamedChildCount()); i++ {
			if !tsLiteralValues(t.NamedChild(i), src, into) {
				return false
			}
		}
		return t.NamedChildCount() > 0
	case "literal_type":
		if t.NamedChildCount() == 1 {
			if v, ok := tsLiteralValue(t.NamedChild(0), src); ok {
				into[v] = true
				return true
			}
		}
	}
	return false
}

// instanceofType is the class a guard proves the identifier id an instance
// of: x instanceof C on the right of &&, after || !(…), in an if's or a
// ternary's branch, or after an if that returns when it is not one. Only
// followed for a binding never reassigned, and only one class: two guards
// naming different classes (an intersection, a subclass) are not modeled.
// The caller asks only when the declared type is not the repository's
// (unknown, any, Error, a catch clause's value).
func (r *tsResolver) instanceofType(m *tsModule, id *sitter.Node, depth int) (tsTy, bool) {
	if m.instanceof == 0 {
		m.instanceof = 1
		if strings.Contains(string(m.src), "instanceof") {
			m.instanceof = 2
		}
	}
	if m.instanceof == 1 {
		return tsTy{}, false
	}
	name := id.Content(m.src)
	b, local := tsLocalBinding(id, name, m.src)
	if !local || b == nil {
		return tsTy{}, false
	}
	scope := tsBindingScope(b)
	if scope == nil {
		return tsTy{}, false
	}
	st := &tsNarrowState{r: r, m: m, name: name, binding: b.StartByte(), depth: depth}
	var classes, not []*sitter.Node
	add := func(pos, neg []*sitter.Node) { classes, not = append(classes, pos...), append(not, neg...) }
	c := id
walk:
	for a := id.Parent(); a != nil; c, a = a, a.Parent() {
		switch a.Type() {
		case "function_declaration", "generator_function_declaration", "method_definition", "class_body":
			if !tsSame(a, scope) {
				break walk // hoisted, or past a method: not narrowed by its surroundings
			}
		case "binary_expression":
			op, left, right := a.ChildByFieldName("operator"), a.ChildByFieldName("left"), a.ChildByFieldName("right")
			if op == nil || left == nil || right == nil || !tsSame(right, c) {
				break
			}
			switch op.Type() {
			case "&&":
				add(st.instanceofFacts(left, true))
			case "||":
				add(st.instanceofFacts(left, false))
			}
		case "ternary_expression", "if_statement":
			cond := a.ChildByFieldName("condition")
			if cond == nil || tsSame(cond, c) {
				break
			}
			if x := a.ChildByFieldName("consequence"); x != nil && tsSame(x, c) {
				add(st.instanceofFacts(cond, true))
			} else if x := a.ChildByFieldName("alternative"); x != nil && tsSame(x, c) {
				add(st.instanceofFacts(cond, false))
			}
		case "statement_block", "program", "switch_case", "switch_default":
			for i := 0; i < int(a.NamedChildCount()); i++ {
				s := a.NamedChild(i)
				if s.StartByte() >= c.StartByte() {
					break
				}
				if s.Type() != "if_statement" {
					continue
				}
				cond := s.ChildByFieldName("condition")
				if cond == nil || !strings.Contains(cond.Content(m.src), "instanceof") {
					continue
				}
				cons, alt := s.ChildByFieldName("consequence"), s.ChildByFieldName("alternative")
				ce, ae := cons != nil && tsExits(cons), alt != nil && tsExits(alt)
				switch {
				case ce && !ae:
					add(st.instanceofFacts(cond, false))
				case ae && !ce:
					add(st.instanceofFacts(cond, true))
				}
			}
		}
		if tsSame(a, scope) {
			break
		}
	}
	if len(classes) == 0 {
		return tsTy{}, false
	}
	cls := classes[0].Content(m.src)
	for _, k := range classes[1:] {
		if k.Content(m.src) != cls {
			return tsTy{}, false
		}
	}
	for _, k := range not {
		if k.Content(m.src) == cls {
			return tsTy{}, false // never: a branch Layer 2 does not type
		}
	}
	if tsAssigned(scope, name, m.src) {
		return tsTy{}, false
	}
	t := r.classValueType(m, classes[0], depth+1)
	if t.k != tyDecl {
		return tsTy{}, false
	}
	return t, true
}

// instanceofFacts are the classes cond being truth proves the identifier an
// instance of (pos) and not an instance of (neg).
func (st *tsNarrowState) instanceofFacts(cond *sitter.Node, truth bool) (pos, neg []*sitter.Node) {
	switch cond.Type() {
	case "parenthesized_expression":
		if cond.NamedChildCount() == 1 {
			return st.instanceofFacts(cond.NamedChild(0), truth)
		}
	case "unary_expression":
		if op := cond.ChildByFieldName("operator"); op != nil && op.Type() == "!" {
			if a := cond.ChildByFieldName("argument"); a != nil {
				return st.instanceofFacts(a, !truth)
			}
		}
	case "binary_expression":
		op, left, right := cond.ChildByFieldName("operator"), cond.ChildByFieldName("left"), cond.ChildByFieldName("right")
		if op == nil || left == nil || right == nil {
			break
		}
		switch op.Type() {
		case "instanceof":
			if right.Type() == "identifier" && st.refersHere(left) {
				if truth {
					return []*sitter.Node{right}, nil
				}
				return nil, []*sitter.Node{right}
			}
		case "&&", "||":
			if truth == (op.Type() == "&&") {
				p1, n1 := st.instanceofFacts(left, truth)
				p2, n2 := st.instanceofFacts(right, truth)
				return append(p1, p2...), append(n1, n2...)
			}
		}
	}
	return nil, nil
}
