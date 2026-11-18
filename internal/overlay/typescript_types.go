//go:build cgo

package overlay

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// Types for TypeScript's Layer 2: what the syntax says an expression's type
// is, followed only as far as it is exact. A member access, an object
// literal's key or a destructured name gets an edge when its receiver's type
// is an interface, a class or an object type of the repository and the
// member is declared there (or in a type it extends). Types come from
//
//   - annotations: parameters, variables, members, return types;
//   - initializers: const x = f(), const s = () => store.getState();
//   - calls of repository functions with a spelled or expression-bodied
//     return type, await, new C(), this, x[i] of an array or record;
//   - context: a variable's annotation, an argument's parameter type, a
//     return type, a key's member type, an array method's element type, and
//     zustand's create<T>() (the store's state).
//
// Unions of several types, intersections, type parameters, overloads and
// mapped types are not followed: a member of one is counted as unresolved.

// tsTyKind is what Layer 2 knows about a type.
type tsTyKind int

const (
	tyNone     tsTyKind = iota // no type the syntax spells or implies
	tyOutside                  // a primitive's, a global's or a package's type
	tyUnknown                  // may have repository members Layer 2 does not follow
	tyDecl                     // an interface or class of the repository: node, sym
	tyLit                      // an object type: node, members under sym+"typeLiteralN:"
	tyArray                    // an array of elem
	tyPromise                  // a promise of elem
	tyRecord                   // Record<K, elem>
	tyMap                      // Map<K, elem>
	tyFn                       // a function: node has parameters and a return type
	tyCallback                 // a function known by params and its result elem
	tyStore                    // a zustand store whose state is elem
	tyPick                     // Pick, Omit, Partial, Readonly of elem: keys, omit, optional
	tyObj                      // an object literal expression: node, members by the declaring document
)

type tsTy struct {
	k      tsTyKind
	m      *tsModule
	node   *sitter.Node
	sym    string // tyDecl: the type's suffix in m; tyLit: the members' owner in m
	elem   *tsTy
	params []tsTy
	// tyPick: the keys Pick keeps (nil: all) or Omit drops, and Partial's
	// optional members.
	keys     map[string]bool
	omit     bool
	optional bool
	// parts of a union (k stays tyUnknown, so every rule that does not ask
	// for them treats it as before) or, inter set, of an intersection.
	parts []tsTy
	inter bool
	// nullable: the type came out of a union with undefined or null. A
	// member access looks through it, an object literal's keys do not:
	// scip-typescript asks the contextual type for the property, and a
	// union with undefined has none, so the literal defines its own.
	nullable bool
	// orPromise: an async function's result context, T | PromiseLike<T>
	// (a union, which has no property of T's under any tsconfig).
	orPromise bool
}

var (
	tsNoTy      = tsTy{}
	tsOutsideTy = tsTy{k: tyOutside}
	tsUnknownTy = tsTy{k: tyUnknown}
)

func (t tsTy) of() tsTy {
	if t.elem == nil {
		return tsNoTy
	}
	return *t.elem
}

func tsWrap(k tsTyKind, elem tsTy) tsTy {
	switch elem.k {
	case tyUnknown:
		if elem.parts == nil {
			return tsUnknownTy
		}
	}
	return tsTy{k: k, elem: &elem}
}

// tsMaxDepth bounds every chain the engine follows (aliases, initializers,
// contexts): beyond it the type is unknown.
const tsMaxDepth = 12

type tsTyKey struct {
	file       string
	start, end uint32
	what       byte
}

// typeOfNode is the type a type node of module m spells.
func (r *tsResolver) typeOfNode(m *tsModule, t *sitter.Node, depth int) tsTy {
	if t == nil || depth > tsMaxDepth {
		return tsUnknownTy
	}
	switch t.Type() {
	case "type_annotation", "parenthesized_type", "readonly_type":
		if t.NamedChildCount() != 1 {
			return tsUnknownTy
		}
		return r.typeOfNode(m, t.NamedChild(0), depth+1)
	case "union_type":
		var keep *sitter.Node
		for i := 0; i < int(t.NamedChildCount()); i++ {
			c := t.NamedChild(i)
			if tsNullish(c, m.src) {
				continue
			}
			if keep != nil {
				return r.unionTy(m, t, depth)
			}
			keep = c
		}
		if keep == nil {
			return tsOutsideTy
		}
		k := r.typeOfNode(m, keep, depth+1)
		if int(t.NamedChildCount()) > 1 {
			k.nullable = true
		}
		return k
	case "array_type":
		if t.NamedChildCount() != 1 {
			return tsUnknownTy
		}
		return tsWrap(tyArray, r.typeOfNode(m, t.NamedChild(0), depth+1))
	case "generic_type":
		name, args := t.ChildByFieldName("name"), t.ChildByFieldName("type_arguments")
		if name == nil || args == nil {
			return tsUnknownTy
		}
		if zt, ok := r.zodInfer(m, t, depth); ok {
			return zt
		}
		if name.Type() != "type_identifier" {
			return r.typeOfNode(m, name, depth+1)
		}
		n := name.Content(m.src)
		if !tsBoundLocally(name, n, m.src) {
			if _, ok := r.resolveIn(m.file, n, tsTypeNS); !ok {
				arg := func(i int) tsTy {
					if int(args.NamedChildCount()) <= i {
						return tsUnknownTy
					}
					return r.typeOfNode(m, args.NamedChild(i), depth+1)
				}
				switch n {
				case "Pick", "Omit", "Partial", "Readonly":
					return r.mappedTy(m, n, args, depth)
				case "Array", "ReadonlyArray":
					return tsWrap(tyArray, arg(0))
				case "Promise", "PromiseLike":
					return tsWrap(tyPromise, arg(0))
				case "Record":
					return tsWrap(tyRecord, arg(1))
				case "Map", "ReadonlyMap", "WeakMap":
					return tsWrap(tyMap, arg(1))
				}
				if r.mentionsRepoIn(m, args) {
					return tsUnknownTy // Partial<T>, Set<T>, …: T's members may show
				}
				return tsOutsideTy
			}
		}
		return r.typeOfNode(m, name, depth+1)
	case "type_identifier":
		return r.namedType(m, t, depth)
	case "nested_type_identifier":
		mod, name := t.ChildByFieldName("module"), t.ChildByFieldName("name")
		if mod == nil || name == nil || mod.Type() != "identifier" || tsBoundLocally(mod, mod.Content(m.src), m.src) {
			return tsUnknownTy
		}
		res, ok := r.resolveIn(m.file, mod.Content(m.src), tsValue)
		switch {
		case !ok || res.external:
			return tsOutsideTy
		case res.namespace != "":
			return r.typeBySymbol(r.lookup(res.namespace, name.Content(m.src), tsTypeNS, 0), depth)
		}
		return tsUnknownTy
	case "object_type":
		owner, local := tsLitOwner(t, m.src)
		switch {
		case tsAsType(t):
			// x as { k: T }: scip-typescript makes k a local, so a member
			// access writes nothing, but T is followed (the declaring
			// document says which local it is, else unknown).
			return tsTy{k: tyLit, m: m, node: t}
		case local:
			return tsOutsideTy // its members are local symbols
		case owner == "" && tsInUnionOrIntersection(t):
			return tsTy{k: tyLit, m: m, node: t} // members by the declaring document
		case owner == "":
			return tsUnknownTy
		}
		return tsTy{k: tyLit, m: m, node: t, sym: owner}
	case "lookup_type":
		// T["k"]: the type of T's member k.
		key, ok := tsLookupKey(t, m.src)
		if !ok {
			return tsUnknownTy
		}
		obj := r.typeOfNode(m, t.NamedChild(0), depth+1)
		if len(obj.parts) > 0 && !obj.inter {
			return tsUnknownTy
		}
		switch mb := r.memberOf(obj, tsStringValue(key.Content(m.src)), depth+1); mb.k {
		case tyDecl:
			if len(mb.more) > 0 {
				return tsUnknownTy
			}
			return mb.typ
		case tyOutside:
			return tsOutsideTy
		}
		return tsUnknownTy
	case "intersection_type":
		return r.unionTy(m, t, depth)
	case "function_type":
		return tsTy{k: tyFn, m: m, node: t}
	case "predefined_type", "literal_type", "template_literal_type", "string", "number", "true", "false", "null", "undefined":
		return tsOutsideTy
	}
	return tsUnknownTy
}

// mappedTy is a homomorphic utility type of a repository type: its members
// are the base's (TypeScript keeps their declarations, so scip-typescript
// names the base's symbols). Pick and Omit need their keys spelled as string
// literals.
func (r *tsResolver) mappedTy(m *tsModule, n string, args *sitter.Node, depth int) tsTy {
	want := 1
	if n == "Pick" || n == "Omit" {
		want = 2
	}
	if int(args.NamedChildCount()) != want {
		return tsUnknownTy
	}
	base := r.typeOfNode(m, args.NamedChild(0), depth+1)
	switch base.k {
	case tyDecl, tyLit, tyPick:
	case tyOutside:
		return tsOutsideTy
	default:
		return tsUnknownTy
	}
	out := tsTy{k: tyPick, elem: &base, optional: n == "Partial"}
	if want == 2 {
		keys := map[string]bool{}
		if !tsStringLiterals(args.NamedChild(1), m.src, keys) {
			return tsUnknownTy
		}
		out.keys, out.omit = keys, n == "Omit"
	}
	return out
}

// tsStringLiterals collects a string literal type or a union of them.
func tsStringLiterals(t *sitter.Node, src []byte, into map[string]bool) bool {
	switch t.Type() {
	case "union_type", "parenthesized_type":
		for i := 0; i < int(t.NamedChildCount()); i++ {
			if !tsStringLiterals(t.NamedChild(i), src, into) {
				return false
			}
		}
		return t.NamedChildCount() > 0
	case "literal_type":
		if t.NamedChildCount() == 1 && t.NamedChild(0).Type() == "string" {
			into[tsStringValue(t.NamedChild(0).Content(src))] = true
			return true
		}
	}
	return false
}

// unionTy is a union of two or more non-nullish types: outside when none of
// them can have repository members, else unknown.
func (r *tsResolver) unionTy(m *tsModule, t *sitter.Node, depth int) tsTy {
	if !r.mentionsRepoIn(m, t) {
		return tsOutsideTy
	}
	parts, nullable, ok := r.tsUnionParts(m, t, depth)
	if !ok {
		return tsUnknownTy
	}
	return tsTy{k: tyUnknown, parts: parts, nullable: nullable, inter: t.Type() == "intersection_type"}
}

func tsNullish(c *sitter.Node, src []byte) bool {
	switch c.Type() {
	case "literal_type", "predefined_type", "undefined", "null":
		s := c.Content(src)
		return s == "undefined" || s == "null" || s == "void"
	}
	return false
}

// mentionsRepoIn reports whether a type node of m spells an object type or
// names a type of the repository.
func (r *tsResolver) mentionsRepoIn(m *tsModule, n *sitter.Node) bool {
	switch n.Type() {
	case "object_type":
		return true
	case "type_identifier":
		if tsBoundLocally(n, n.Content(m.src), m.src) {
			return false
		}
		res, ok := r.resolveIn(m.file, n.Content(m.src), tsTypeNS)
		return ok && !res.external
	case "type_query":
		// typeof x (ReturnType<typeof f>): x's type, a repository one
		// unless x is a package's or a global's value.
		if n.NamedChildCount() != 1 {
			return true
		}
		id := n.NamedChild(0)
		for id.Type() == "member_expression" {
			if id = id.ChildByFieldName("object"); id == nil {
				return true
			}
		}
		if id.Type() != "identifier" {
			return true
		}
		if tsBoundLocally(id, id.Content(m.src), m.src) {
			return true
		}
		res, ok := r.resolveIn(m.file, id.Content(m.src), tsValue)
		return ok && !res.external
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if r.mentionsRepoIn(m, n.NamedChild(i)) {
			return true
		}
	}
	return false
}

// namedType resolves a type name of m.
func (r *tsResolver) namedType(m *tsModule, t *sitter.Node, depth int) tsTy {
	name := t.Content(m.src)
	if tsBoundLocally(t, name, m.src) {
		return tsUnknownTy // a type parameter or a local type
	}
	res, ok := r.resolveIn(m.file, name, tsTypeNS)
	switch {
	case !ok || res.external:
		return tsOutsideTy // a global or a package's type
	case res.sym == "":
		return tsUnknownTy
	}
	return r.typeBySymbol(res.sym, depth)
}

// typeBySymbol is the type a top-level type symbol declares.
func (r *tsResolver) typeBySymbol(sym string, depth int) tsTy {
	if sym == "" || !strings.HasSuffix(sym, "#") {
		return tsUnknownTy
	}
	dm := r.module(r.byPrefix[tsSymPrefix(sym)])
	if dm == nil {
		return tsUnknownTy
	}
	suffix := strings.TrimPrefix(sym, dm.prefix)
	d := dm.nodes[suffix]
	if d == nil {
		return tsUnknownTy
	}
	switch d.Type() {
	case "interface_declaration", "class_declaration", "abstract_class_declaration":
		return tsTy{k: tyDecl, m: dm, node: d, sym: suffix}
	case "type_alias_declaration":
		if d.ChildByFieldName("type_parameters") != nil {
			return tsUnknownTy
		}
		v := d.ChildByFieldName("value")
		if v == nil {
			return tsUnknownTy
		}
		if v.Type() == "object_type" {
			return tsTy{k: tyLit, m: dm, node: v, sym: suffix}
		}
		return r.typeOfNode(dm, v, depth+1)
	case "enum_declaration":
		return tsOutsideTy
	}
	return tsUnknownTy
}

// tsLitOwner is the owner scip-typescript names an object type's members
// under, as Layer 1's walker gives it: local is true when the literal sits in
// a function body or another place whose symbols are local.
// tsAsType reports whether t is the type of an as expression.
func tsAsType(t *sitter.Node) bool {
	p := t.Parent()
	return p != nil && p.Type() == "as_expression" && p.NamedChildCount() == 2 && p.NamedChild(1).StartByte() == t.StartByte()
}

func tsLitOwner(t *sitter.Node, src []byte) (owner string, local bool) {
	if tsInsideBody(t) {
		return "", true
	}
	return tsPosOwner(t, src), false
}

// tsInsideBody reports whether n is inside a function's body or an
// expression (not a declaration's signature).
func tsInsideBody(n *sitter.Node) bool {
	for a := n.Parent(); a != nil; a = a.Parent() {
		switch a.Type() {
		case "statement_block":
			if p := a.Parent(); p != nil && (p.Type() == "internal_module" || p.Type() == "module") {
				continue
			}
			return true
		case "arrow_function", "function_expression", "function", "class", "object", "call_expression", "arguments":
			return true
		case "program":
			return false
		}
	}
	return false
}

// tsPosOwner is the owner a type in position t (directly an annotation's,
// an alias's, a type argument's or a constraint's type) gives a literal
// there; empty when Layer 1 does not name it.
func tsPosOwner(t *sitter.Node, src []byte) string {
	p := t.Parent()
	if p == nil {
		return ""
	}
	topLevel := func(n *sitter.Node) bool {
		a := n.Parent()
		if a != nil && a.Type() == "export_statement" {
			a = a.Parent()
		}
		return a != nil && a.Type() == "program"
	}
	switch p.Type() {
	case "type_annotation":
		q := p.Parent()
		if q == nil {
			return ""
		}
		switch q.Type() {
		case "required_parameter", "optional_parameter":
			if fn, ok := tsParamOwner(q, src); ok {
				if pat := q.ChildByFieldName("pattern"); pat != nil {
					return fn + "(" + tsEscape(pat.Content(src)) + ")"
				}
			}
		case "variable_declarator":
			decl := q.Parent()
			name := q.ChildByFieldName("name")
			if decl != nil && name != nil && name.Type() == "identifier" && topLevel(decl) {
				return tsTerm(name.Content(src))
			}
		case "property_signature":
			body := q.Parent()
			name := q.ChildByFieldName("name")
			if body == nil || name == nil || !tsSimpleMemberName(name) {
				return ""
			}
			if i := body.Parent(); i != nil && i.Type() == "interface_declaration" && topLevel(i) {
				if in := i.ChildByFieldName("name"); in != nil {
					return tsType(in.Content(src)) + tsTerm(name.Content(src))
				}
			}
		case "function_declaration":
			if topLevel(q) && q.ChildByFieldName("return_type") != nil && q.ChildByFieldName("return_type").StartByte() == p.StartByte() {
				if name := q.ChildByFieldName("name"); name != nil {
					return tsMethod(name.Content(src))
				}
			}
		}
	case "type_alias_declaration":
		if v := p.ChildByFieldName("value"); v != nil && v.StartByte() == t.StartByte() && topLevel(p) {
			if name := p.ChildByFieldName("name"); name != nil {
				return tsType(name.Content(src))
			}
		}
	case "type_arguments":
		g := p.Parent()
		if g == nil || g.Type() != "generic_type" {
			return ""
		}
		name := g.ChildByFieldName("name")
		if name == nil || name.Type() != "type_identifier" {
			return ""
		}
		// Layer 1 names a type argument's literal only one level down.
		if gp := g.Parent(); gp == nil || gp.Type() != "type_annotation" && gp.Type() != "type_alias_declaration" {
			return ""
		}
		if o := tsPosOwner(g, src); o != "" {
			return o + name.Content(src) + ":"
		}
	case "constraint":
		tp := p.Parent()
		if tp == nil || tp.Type() != "type_parameter" {
			return ""
		}
		name := tp.ChildByFieldName("name")
		tps := tp.Parent()
		if name == nil || tps == nil {
			return ""
		}
		d := tps.Parent()
		if d == nil || !topLevel(d) {
			return ""
		}
		dn := d.ChildByFieldName("name")
		if dn == nil {
			return ""
		}
		switch d.Type() {
		case "function_declaration":
			return tsMethod(dn.Content(src)) + "[" + tsEscape(name.Content(src)) + "]"
		case "interface_declaration", "class_declaration", "abstract_class_declaration", "type_alias_declaration":
			return tsType(dn.Content(src)) + "[" + tsEscape(name.Content(src)) + "]"
		}
	}
	return ""
}

// tsMember is what a member lookup found.
type tsMember struct {
	sym  string   // the member's symbol, when found
	more []string // a union's: the other parts' declarations
	typ  tsTy     // its type
	k    tsTyKind
}

// memberOf looks name up in t: a found member has k tyDecl; tyOutside means
// t has no repository members, tyUnknown that Layer 2 cannot tell, tyNone
// that t is not known at all.
func (r *tsResolver) memberOf(t tsTy, name string, depth int) tsMember {
	switch t.k {
	case tyNone:
		return tsMember{k: tyNone}
	case tyUnknown:
		if t.inter {
			return r.interMember(t, name, depth)
		}
		return tsMember{k: tyUnknown}
	case tyDecl:
		return r.declMember(t, name, depth)
	case tyLit:
		if tsZod(t) {
			return r.zodMember(t, name, depth)
		}
		if t.sym == "" && tsAsType(t.node) {
			// Locals in the index (Layer 1 writes none, so the document
			// cannot say): no edge, the member's type followed.
			mn := tsFindMember(t.node, name, t.m.src)
			if mn == nil {
				return tsMember{k: tyUnknown}
			}
			return tsMember{typ: r.memberType(t.m, mn, depth), k: tyOutside}
		}
		if t.sym == "" {
			return r.posLitMember(t, name, depth)
		}
		sym := r.literalMember(t.m.file, t.sym, name)
		if sym == "" {
			return tsMember{k: tyUnknown}
		}
		mn := tsFindMember(t.node, name, t.m.src)
		return tsMember{sym: sym, typ: r.memberType(t.m, mn, depth), k: tyDecl}
	case tyPick:
		if t.keys != nil && t.keys[name] == t.omit {
			return tsMember{k: tyUnknown} // not a member: a type error, or a key Layer 2 misread
		}
		mb := r.memberOf(t.of(), name, depth+1)
		if t.optional && mb.k == tyDecl {
			mb.typ.nullable = true // Partial: a?: T
		}
		return mb
	case tyObj:
		return r.objMember(t, name, depth)
	case tyStore:
		if tsReactBox(t) {
			return tsMember{k: tyOutside}
		}
		switch name {
		case "getState", "getInitialState":
			return tsMember{typ: tsTy{k: tyCallback, elem: t.elem}, k: tyOutside}
		}
	}
	return tsMember{k: tyOutside}
}

// declMember finds name among an interface's or class's own members, else
// in the types it extends.
func (r *tsResolver) declMember(t tsTy, name string, depth int) tsMember {
	if depth > tsMaxDepth {
		return tsMember{k: tyUnknown}
	}
	esc := tsEscape(name)
	hit := ""
	for _, s := range []string{t.sym + esc + ".", t.sym + esc + "().", t.sym + tsMethod("<get>"+name)} {
		if t.m.all[s] {
			if hit != "" {
				return tsMember{k: tyUnknown}
			}
			hit = s
		}
	}
	if hit != "" {
		var mn *sitter.Node
		if body := t.node.ChildByFieldName("body"); body != nil {
			mn = tsFindMember(body, name, t.m.src)
		}
		return tsMember{sym: t.m.prefix + hit, typ: r.memberType(t.m, mn, depth), k: tyDecl}
	}
	if p := tsParamProperty(t.node.ChildByFieldName("body"), name, t.m.src); p != nil {
		// constructor(readonly name: T): scip-typescript's member is the
		// parameter.
		s := t.sym + tsMethod("<constructor>") + "(" + esc + ")"
		if !t.m.all[s] {
			return tsMember{k: tyUnknown}
		}
		var typ tsTy
		if a := p.ChildByFieldName("type"); a != nil {
			typ = r.typeOfNode(t.m, a, depth+1)
			if p.Type() == "optional_parameter" {
				typ.nullable = true
			}
		}
		return tsMember{sym: t.m.prefix + s, typ: typ, k: tyDecl}
	}
	var found tsMember
	outside := false
	for _, b := range r.heritage(t, depth) {
		mb := r.memberOf(b, name, depth+1)
		switch mb.k {
		case tyDecl:
			if found.sym != "" && found.sym != mb.sym {
				return tsMember{k: tyUnknown}
			}
			found = mb
		case tyOutside:
			outside = true
		case tyUnknown, tyNone:
			return tsMember{k: tyUnknown}
		}
	}
	switch {
	case found.sym != "":
		return found
	case outside:
		return tsMember{k: tyOutside} // declared by a package's base type
	}
	return tsMember{k: tyUnknown}
}

// heritage is the types an interface extends or a class extends.
func (r *tsResolver) heritage(t tsTy, depth int) []tsTy {
	var out []tsTy
	for i := 0; i < int(t.node.NamedChildCount()); i++ {
		c := t.node.NamedChild(i)
		switch c.Type() {
		case "extends_type_clause":
			for j := 0; j < int(c.NamedChildCount()); j++ {
				out = append(out, r.typeOfNode(t.m, c.NamedChild(j), depth+1))
			}
		case "class_heritage":
			for j := 0; j < int(c.NamedChildCount()); j++ {
				e := c.NamedChild(j)
				if e.Type() != "extends_clause" {
					continue // implements: the class declares those members itself
				}
				v := e.ChildByFieldName("value")
				if v == nil {
					out = append(out, tsUnknownTy)
					continue
				}
				out = append(out, r.classValueType(t.m, v, depth+1))
			}
		}
	}
	return out
}

// classValueType is the instance type of a class expression in extends.
func (r *tsResolver) classValueType(m *tsModule, v *sitter.Node, depth int) tsTy {
	if v.Type() != "identifier" {
		return tsUnknownTy
	}
	n := v.Content(m.src)
	if tsBoundLocally(v, n, m.src) {
		return tsUnknownTy
	}
	res, ok := r.resolveIn(m.file, n, tsTypeNS)
	switch {
	case !ok || res.external:
		return tsOutsideTy
	}
	return r.typeBySymbol(res.sym, depth)
}

// tsParamProperty is the parameter of a class body's constructor that
// declares the member name (constructor(private readonly name: T)).
func tsParamProperty(body *sitter.Node, name string, src []byte) *sitter.Node {
	if body == nil || body.Type() != "class_body" {
		return nil
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		if c.Type() != "method_definition" {
			continue
		}
		if n := c.ChildByFieldName("name"); n == nil || n.Content(src) != "constructor" {
			continue
		}
		ps := c.ChildByFieldName("parameters")
		if ps == nil {
			return nil
		}
		for j := 0; j < int(ps.NamedChildCount()); j++ {
			p := ps.NamedChild(j)
			if p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
				continue
			}
			pat := p.ChildByFieldName("pattern")
			if pat == nil || pat.Type() != "identifier" || pat.Content(src) != name {
				continue
			}
			for k := 0; k < int(p.ChildCount()); k++ {
				switch p.Child(k).Type() {
				case "accessibility_modifier", "readonly", "override_modifier":
					return p
				}
			}
			return nil
		}
		return nil
	}
	return nil
}

// tsFindMember is the member signature or definition named name in a body
// (an interface's, a class's or an object type).
func tsFindMember(body *sitter.Node, name string, src []byte) *sitter.Node {
	if body == nil {
		return nil
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		switch c.Type() {
		case "property_signature", "method_signature", "public_field_definition", "method_definition", "abstract_method_signature":
			if n := c.ChildByFieldName("name"); n != nil && n.Content(src) == name {
				return c
			}
		}
	}
	return nil
}

// memberType is the type of a member's declaration.
func (r *tsResolver) memberType(m *tsModule, mn *sitter.Node, depth int) tsTy {
	if mn == nil {
		return tsNoTy
	}
	switch mn.Type() {
	case "property_signature", "public_field_definition":
		if t := mn.ChildByFieldName("type"); t != nil {
			res := r.typeOfNode(m, t, depth+1)
			if tsOptionalMember(mn) {
				res.nullable = true // a?: T is T | undefined
			}
			return res
		}
		if v := mn.ChildByFieldName("value"); v != nil {
			return r.exprType(m, v, depth+1)
		}
		return tsNoTy
	case "method_signature", "method_definition", "abstract_method_signature":
		if tsAccessor(mn, mn.ChildByFieldName("name")) != "" {
			if rt := mn.ChildByFieldName("return_type"); rt != nil {
				return r.typeOfNode(m, rt, depth+1)
			}
			return tsNoTy
		}
		return tsTy{k: tyFn, m: m, node: mn}
	}
	return tsNoTy
}

// returnOf is the result of calling a value of type t with the arguments
// args (nil when unknown).
func (r *tsResolver) returnOf(m *tsModule, t tsTy, args *sitter.Node, depth int) tsTy {
	switch t.k {
	case tyFn:
		// A generic function's type parameters are local names: a result
		// that mentions one is unknown, one that does not is exact.
		fn := t.node
		if rt := fn.ChildByFieldName("return_type"); rt != nil {
			if rt.Type() == "asserts" || rt.Type() == "type_predicate_annotation" || rt.Type() == "type_predicate" {
				return tsOutsideTy
			}
			res := r.typeOfNode(t.m, rt, depth+1)
			return res
		}
		if b := fn.ChildByFieldName("body"); b != nil && fn.Type() == "arrow_function" && b.Type() != "statement_block" {
			res := r.exprType(t.m, b, depth+1)
			if tsIsAsync(fn) {
				return tsWrap(tyPromise, res)
			}
			return res
		}
		if tsGenerator(fn) {
			return tsUnknownTy // a generator object: what it yields is not inferred
		}
		return r.inferReturn(t.m, fn, depth)
	case tyCallback:
		if t.sym == tsSetter {
			return tsOutsideTy // void
		}
		return t.of()
	case tyStore:
		if tsReactBox(t) {
			return tsNoTy
		}
		// useStore() is the state, useStore(selector) the selector's result.
		if args == nil || args.NamedChildCount() == 0 {
			return t.of()
		}
		if sel := args.NamedChild(0); sel.Type() == "arrow_function" {
			if b := sel.ChildByFieldName("body"); b != nil && b.Type() != "statement_block" {
				return r.exprType(m, b, depth+1)
			}
		}
		return tsNoTy
	case tyUnknown:
		return tsUnknownTy
	case tyOutside:
		// A package's or a global's function: its result has no repository
		// members unless it is generic over what the call passes it
		// (useContext(ctx), useState<T>(), structuredClone(x)).
		if args != nil && r.passesRepo(m, args, depth) {
			return tsUnknownTy
		}
		return tsOutsideTy
	}
	return tsNoTy
}

// passesRepo reports whether a call (given by its arguments) passes a
// value or a type argument that may carry repository members.
func (r *tsResolver) passesRepo(m *tsModule, args *sitter.Node, depth int) bool {
	if call := args.Parent(); call != nil {
		if ta := call.ChildByFieldName("type_arguments"); ta != nil && r.mentionsRepoIn(m, ta) {
			return true
		}
	}
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if a := args.NamedChild(i); a.Type() != "comment" && r.carriesRepo(r.exprType(m, a, depth+1), depth) {
			return true
		}
	}
	return false
}

// carriesRepo reports a type that has, holds or returns repository members.
func (r *tsResolver) carriesRepo(t tsTy, depth int) bool {
	if depth > tsMaxDepth {
		return true
	}
	switch t.k {
	case tyDecl, tyLit, tyPick, tyStore, tyUnknown:
		return true
	case tyArray, tyPromise, tyRecord, tyMap, tyCallback:
		return r.carriesRepo(t.of(), depth+1)
	case tyFn:
		return r.carriesRepo(r.returnOf(t.m, t, nil, depth+1), depth+1)
	}
	return false
}

func tsIsAsync(fn *sitter.Node) bool {
	for i := 0; i < int(fn.ChildCount()); i++ {
		if fn.Child(i).Type() == "async" {
			return true
		}
	}
	return false
}

// paramOf is the type of a function type's i-th parameter.
func (r *tsResolver) paramOf(t tsTy, i int, depth int) tsTy {
	switch t.k {
	case tyCallback:
		if i < len(t.params) {
			return t.params[i]
		}
		return tsNoTy
	case tyFn:
		ps := t.node.ChildByFieldName("parameters")
		if ps == nil {
			return tsNoTy
		}
		j := 0
		for k := 0; k < int(ps.NamedChildCount()); k++ {
			p := ps.NamedChild(k)
			if p.Type() != "required_parameter" && p.Type() != "optional_parameter" {
				continue
			}
			if pat := p.ChildByFieldName("pattern"); pat != nil && (pat.Type() == "rest_pattern" || pat.Content(t.m.src) == "this") {
				if pat.Type() == "rest_pattern" {
					return tsUnknownTy
				}
				continue
			}
			if j == i {
				if pt := p.ChildByFieldName("type"); pt != nil {
					res := r.typeOfNode(t.m, pt, depth+1)
					if p.Type() == "optional_parameter" || p.ChildByFieldName("value") != nil {
						res.nullable = true // p?: T and p: T = d are T | undefined to a caller
					}
					return res
				}
				return tsNoTy
			}
			j++
		}
		return tsNoTy
	case tyUnknown:
		return tsUnknownTy
	case tyOutside:
		return tsOutsideTy
	}
	return tsNoTy
}

// exprType is the type of the expression e of module m.
func (r *tsResolver) exprType(m *tsModule, e *sitter.Node, depth int) tsTy {
	if e == nil || depth > tsMaxDepth {
		return tsUnknownTy
	}
	key := tsTyKey{m.file, e.StartByte(), e.EndByte(), 'e'}
	if t, ok := r.tyCache[key]; ok {
		return t
	}
	r.tyCache[key] = tsNoTy // a cycle ends as no type
	t := r.exprType1(m, e, depth)
	r.tyCache[key] = t
	return t
}

func (r *tsResolver) exprType1(m *tsModule, e *sitter.Node, depth int) tsTy {
	switch e.Type() {
	case "parenthesized_expression", "non_null_expression":
		if e.NamedChildCount() != 1 {
			return tsNoTy
		}
		t := r.exprType(m, e.NamedChild(0), depth+1)
		if e.Type() == "non_null_expression" {
			t.nullable = false
		}
		return t
	case "satisfies_expression":
		if e.NamedChildCount() < 1 {
			return tsNoTy
		}
		return r.exprType(m, e.NamedChild(0), depth+1)
	case "as_expression":
		if e.NamedChildCount() != 2 {
			return tsNoTy // as const
		}
		return r.typeOfNode(m, e.NamedChild(1), depth+1)
	case "await_expression":
		if e.NamedChildCount() != 1 {
			return tsNoTy
		}
		t := r.exprType(m, e.NamedChild(0), depth+1)
		if t.k == tyPromise {
			return t.of()
		}
		return t
	case "identifier":
		return r.identType(m, e, depth)
	case "this":
		return r.thisType(m, e, depth)
	case "member_expression":
		obj, prop := e.ChildByFieldName("object"), e.ChildByFieldName("property")
		if obj == nil || prop == nil || prop.Type() != "property_identifier" {
			return tsNoTy
		}
		ot := r.exprType(m, obj, depth+1)
		if ot.k == tyArray {
			switch prop.Content(m.src) {
			case "length":
				return tsOutsideTy
			}
			return tsTy{k: tyArray, elem: ot.elem, sym: "method:" + prop.Content(m.src)}
		}
		if ot.k == tyMap {
			return tsTy{k: tyMap, elem: ot.elem, sym: "method:" + prop.Content(m.src)}
		}
		if ot.k == tyRecord {
			return ot.of() // rec.key: the value type
		}
		if tsReactBox(ot) {
			if ot.sym == tsReactRef && prop.Content(m.src) == "current" {
				return ot.of()
			}
			return tsOutsideTy // a context's Provider: React's
		}
		mb := r.memberOfRef(m, ot, prop.Content(m.src), obj, depth+1)
		switch mb.k {
		case tyDecl:
			return mb.typ
		case tyOutside:
			if mb.typ.k != tyNone {
				return mb.typ
			}
			return tsOutsideTy
		case tyUnknown:
			return tsUnknownTy
		}
		return tsNoTy
	case "subscript_expression":
		obj := e.ChildByFieldName("object")
		if obj == nil {
			return tsNoTy
		}
		switch ot := r.exprType(m, obj, depth+1); ot.k {
		case tyArray, tyRecord:
			if ot.sym != "" {
				return tsNoTy
			}
			return ot.of()
		case tyUnknown:
			return tsUnknownTy
		case tyOutside:
			return tsOutsideTy
		}
		return tsNoTy
	case "call_expression":
		fn, args := e.ChildByFieldName("function"), e.ChildByFieldName("arguments")
		if fn == nil {
			return tsNoTy
		}
		if st, ok := r.zustandStore(m, e, depth); ok {
			return st
		}
		if t, ok := r.reactCall(m, e, depth); ok {
			return t
		}
		if t, ok := r.zodParse(m, e, depth); ok {
			return t
		}
		ft := r.exprType(m, fn, depth+1)
		if (ft.k == tyArray || ft.k == tyMap) && strings.HasPrefix(ft.sym, "method:") {
			return r.collectionCall(m, ft, args, depth)
		}
		if e.ChildByFieldName("type_arguments") != nil && ft.k == tyFn {
			return tsUnknownTy
		}
		return r.returnOf(m, ft, args, depth)
	case "new_expression":
		c := e.ChildByFieldName("constructor")
		if c == nil || c.Type() != "identifier" {
			return tsNoTy
		}
		if ta := e.ChildByFieldName("type_arguments"); ta != nil {
			return r.newGeneric(m, c, ta, depth)
		}
		return r.classValueType(m, c, depth+1)
	case "arrow_function", "function_expression", "function", "generator_function":
		return tsTy{k: tyFn, m: m, node: e}
	case "object":
		return tsTy{k: tyObj, m: m, node: e}
	case "null", "undefined":
		return tsNullTy
	case "binary_expression":
		left, right, op := e.ChildByFieldName("left"), e.ChildByFieldName("right"), e.ChildByFieldName("operator")
		if left == nil || right == nil || op == nil {
			return tsNoTy
		}
		switch op.Type() {
		case "??", "||":
			return tsJoin(r.exprType(m, left, depth+1), r.exprType(m, right, depth+1))
		case "&&":
			switch rt := r.exprType(m, right, depth+1); rt.k {
			case tyOutside:
				return tsOutsideTy
			case tyNone:
				return tsNoTy
			}
			return tsUnknownTy // b, or a's falsy value
		}
		return tsOutsideTy // arithmetic, comparison, in, instanceof
	case "ternary_expression":
		a, b := e.ChildByFieldName("consequence"), e.ChildByFieldName("alternative")
		if a == nil || b == nil {
			return tsNoTy
		}
		return tsJoin(r.exprType(m, a, depth+1), r.exprType(m, b, depth+1))
	case "string", "template_string", "number", "true", "false", "unary_expression", "regex", "update_expression":
		return tsOutsideTy
	}
	return tsNoTy
}

// objMember is the member name of an object literal expression: the symbol
// the declaring document defines at its key (scip-typescript's name0:, or a
// local), typed by its value. A key the literal does not spell, or one
// beside a spread that may hide it, is unknown.
func (r *tsResolver) objMember(t tsTy, name string, depth int) tsMember {
	if depth > tsMaxDepth {
		return tsMember{k: tyUnknown}
	}
	var key, val *sitter.Node
	spread := false
	for i := 0; i < int(t.node.NamedChildCount()); i++ {
		c := t.node.NamedChild(i)
		var k, v *sitter.Node
		switch c.Type() {
		case "pair":
			k, v = c.ChildByFieldName("key"), c.ChildByFieldName("value")
		case "shorthand_property_identifier":
			k = c
		case "method_definition":
			k, v = c.ChildByFieldName("name"), c
		case "spread_element":
			// { ...a, k } where a has k too, or where a is a package's or
			// an unresolved import's type (any, to scip-typescript): the
			// index does not point the spread type's property at the
			// literal's key. Where Layer 2 just does not know a's type, it
			// does (46 on Lebenslauf).
			if c.NamedChildCount() == 1 {
				st := r.exprType(t.m, c.NamedChild(0), depth+1)
				if st.k == tyOutside && st.sym != "null" || r.memberOf(st, name, depth+1).k == tyDecl {
					spread = true
				}
			}
		}
		if k == nil {
			continue
		}
		if k.Type() != "property_identifier" && k.Type() != "shorthand_property_identifier" {
			continue
		}
		if k.Content(t.m.src) == name {
			key, val = k, v // the last one wins, as in JavaScript
		}
	}
	if key == nil || spread {
		return tsMember{k: tyUnknown}
	}
	var typ tsTy
	switch {
	case key.Type() == "shorthand_property_identifier":
		typ = r.identType(t.m, key, depth+1)
	case val.Type() == "method_definition":
		typ = tsTy{k: tyFn, m: t.m, node: val}
	default:
		typ = r.exprType(t.m, val, depth+1)
	}
	sym := r.defAt(t.m, key)
	switch {
	case sym == "":
		return tsMember{k: tyUnknown}
	case strings.HasPrefix(sym, "local "):
		return tsMember{typ: typ, k: tyOutside}
	}
	return tsMember{sym: sym, typ: typ, k: tyDecl}
}

// newGeneric is the type of new C<…>(): the global Map and Array with their
// element type; anything else is left out (a generic class's members may
// mention its type parameters).
func (r *tsResolver) newGeneric(m *tsModule, c, ta *sitter.Node, depth int) tsTy {
	n := c.Content(m.src)
	if tsBoundLocally(c, n, m.src) {
		return tsNoTy
	}
	if _, ok := r.resolveIn(m.file, n, tsValue); ok {
		return tsNoTy
	}
	arg := func(i int) tsTy {
		if int(ta.NamedChildCount()) <= i {
			return tsUnknownTy
		}
		return r.typeOfNode(m, ta.NamedChild(i), depth+1)
	}
	switch n {
	case "Map", "WeakMap":
		return tsWrap(tyMap, arg(1))
	case "Array":
		return tsWrap(tyArray, arg(0))
	}
	return tsNoTy
}

// tsOrUndefined is T | undefined (Map.get, Array.find): a member access
// looks through it, a literal assigned in its place has its own keys.
func tsOrUndefined(t tsTy) tsTy {
	if t.k == tyNone || t.k == tyUnknown && t.parts == nil {
		return t
	}
	t.nullable = true
	return t
}

// tsNullTy is null or undefined: joined with T it makes T nullable.
var tsNullTy = tsTy{k: tyOutside, sym: "null"}

// tsJoin is the type of a value that is either a or b: the one type both
// are (null and undefined make it nullable), else unknown.
func tsJoin(a, b tsTy) tsTy {
	switch {
	case a.k == tyOutside && a.sym == "null" && b.k == tyOutside && b.sym == "null":
		return tsNullTy
	case a.k == tyOutside && a.sym == "null":
		b.nullable = true
		return b
	case b.k == tyOutside && b.sym == "null":
		a.nullable = true
		return a
	case a.k == tyNone || b.k == tyNone:
		return tsNoTy
	case a.k == tyUnknown || b.k == tyUnknown:
		return tsUnknownTy
	case a.k == tyOutside && b.k == tyOutside:
		return tsOutsideTy
	case a.k != b.k:
		return tsUnknownTy
	}
	switch a.k {
	case tyDecl, tyLit:
		if a.m != b.m || a.sym != b.sym {
			return tsUnknownTy
		}
	case tyArray, tyPromise, tyRecord, tyMap, tyStore:
		if a.sym != b.sym {
			return tsUnknownTy
		}
		e := tsJoin(a.of(), b.of())
		if e.k == tyUnknown {
			return tsUnknownTy
		}
		out := tsTy{k: a.k, elem: &e, nullable: a.nullable || b.nullable}
		return out
	default:
		return tsUnknownTy // two functions
	}
	a.nullable = a.nullable || b.nullable
	return a
}

// inferReturn is the result of a function without a return type: the join
// of what its return statements return (nested functions' left out).
func (r *tsResolver) inferReturn(m *tsModule, fn *sitter.Node, depth int) tsTy {
	body := fn.ChildByFieldName("body")
	if body == nil || body.Type() != "statement_block" {
		return tsNoTy
	}
	var t tsTy
	seen := false
	var walk func(n *sitter.Node) bool
	walk = func(n *sitter.Node) bool {
		switch n.Type() {
		case "function_declaration", "function_expression", "function", "arrow_function", "method_definition", "class", "class_declaration", "generator_function", "generator_function_declaration":
			return true
		case "return_statement":
			var rt tsTy
			if n.NamedChildCount() == 0 {
				rt = tsNullTy
			} else {
				rt = r.exprType(m, n.NamedChild(0), depth+1)
			}
			if !seen {
				t, seen = rt, true
			} else {
				t = tsJoin(t, rt)
			}
			return t.k != tyNone && t.k != tyUnknown
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if !walk(n.NamedChild(i)) {
				return false
			}
		}
		return true
	}
	walk(body)
	if !seen {
		return tsOutsideTy // void
	}
	if tsIsAsync(fn) && t.k != tyUnknown && t.k != tyNone && t.k != tyPromise {
		return tsWrap(tyPromise, t) // a returned promise is not wrapped again
	}
	return t
}

// collectionCall is the result of an array's or a map's method.
func (r *tsResolver) collectionCall(m *tsModule, ft tsTy, args *sitter.Node, depth int) tsTy {
	method := strings.TrimPrefix(ft.sym, "method:")
	coll := tsTy{k: ft.k, elem: ft.elem}
	if ft.k == tyMap {
		switch method {
		case "get":
			return tsOrUndefined(ft.of())
		case "has", "set", "delete", "size", "clear", "forEach":
			return tsOutsideTy
		}
		return tsNoTy
	}
	switch method {
	case "find", "findLast", "at", "pop", "shift":
		return tsOrUndefined(ft.of())
	case "filter":
		if args != nil && args.NamedChildCount() > 0 {
			if cb := args.NamedChild(0); cb.ChildByFieldName("return_type") != nil {
				return tsUnknownTy // (x): x is U => …: a narrowed element type
			}
		}
		return coll
	case "slice", "concat", "sort", "reverse", "toSorted", "toReversed", "splice":
		return coll
	case "map":
		if args != nil && args.NamedChildCount() > 0 {
			if cb := args.NamedChild(0); cb.Type() == "arrow_function" {
				if b := cb.ChildByFieldName("body"); b != nil && b.Type() != "statement_block" {
					return tsWrap(tyArray, r.exprType(m, b, depth+1))
				}
			}
		}
		return tsNoTy
	case "includes", "some", "every", "indexOf", "lastIndexOf", "findIndex", "findLastIndex", "join", "push", "unshift", "forEach", "length":
		return tsOutsideTy
	}
	return tsNoTy
}

// identType is the type of an identifier: its local binding's, or a
// top-level declaration's of this or another module.
func (r *tsResolver) identType(m *tsModule, e *sitter.Node, depth int) tsTy {
	name := e.Content(m.src)
	if id, bound := tsLocalBinding(e, name, m.src); bound {
		if id == nil {
			return tsNoTy
		}
		t := r.bindingType(m, id, depth)
		if t.k == tyOutside || t.k == tyNone {
			// unknown, any, Error, a catch clause's value: x instanceof C
			// makes it C.
			if ct, ok := r.instanceofType(m, e, depth); ok {
				return ct
			}
		}
		return t
	}
	res, ok := r.resolveIn(m.file, name, tsValue)
	switch {
	case !ok:
		if name == "undefined" {
			return tsOutsideTy
		}
		return tsOutsideTy // a global
	case res.external:
		return tsOutsideTy
	case res.namespace != "" || res.sym == "":
		return tsNoTy
	}
	return r.topValueType(res.sym, depth)
}

// topValueType is the type of a top-level value symbol: a function, or a
// variable with an annotation or an initializer.
func (r *tsResolver) topValueType(sym string, depth int) tsTy {
	dm := r.module(r.byPrefix[tsSymPrefix(sym)])
	if dm == nil {
		return tsNoTy
	}
	suffix := strings.TrimPrefix(sym, dm.prefix)
	d := dm.nodes[suffix]
	if d == nil {
		return tsNoTy
	}
	switch d.Type() {
	case "function_declaration", "generator_function_declaration":
		if dm.overloaded[suffix] {
			return tsUnknownTy
		}
		return tsTy{k: tyFn, m: dm, node: d}
	case "variable_declarator":
		return r.declaratorType(dm, d, depth)
	}
	return tsNoTy // a class, an enum, a namespace: the caller's own rules
}

// bindingType is the type of a local binding's identifier.
func (r *tsResolver) bindingType(m *tsModule, id *sitter.Node, depth int) tsTy {
	p := id.Parent()
	if t := tsBindingType(id); t != nil {
		res := r.typeOfNode(m, t, depth+1)
		if tsIsUnion(res) && p != nil && p.ChildByFieldName("value") != nil {
			return tsUnknownTy // initialized or defaulted: narrowed by the assignment
		}
		return res
	}
	if p == nil {
		return tsNoTy
	}
	if p.Type() == "object_assignment_pattern" || p.Type() == "assignment_pattern" {
		if t := r.bindingType1(m, id, depth); tsIsUnion(t) {
			return tsUnknownTy // { a = d }: narrowed by the default
		}
	}
	return r.bindingType1(m, id, depth)
}

// arrayElemType is the type of a name in an array pattern of a
// declaration: [state, setState] = useState(…), [a, b] = xs.
func (r *tsResolver) arrayElemType(m *tsModule, pat, id *sitter.Node, depth int) tsTy {
	d := pat.Parent()
	if d == nil || d.Type() != "variable_declarator" {
		return tsNoTy
	}
	if n := d.ChildByFieldName("name"); n == nil || !tsSame(n, pat) {
		return tsNoTy
	}
	v := d.ChildByFieldName("value")
	if v == nil {
		return tsNoTy
	}
	idx := -1
	for i := 0; i < int(pat.NamedChildCount()); i++ {
		if c := pat.NamedChild(i); tsSame(c, id) {
			idx = i
		}
	}
	switch t := r.exprType(m, v, depth+1); {
	case t.k == tyStore && t.sym == tsReactState:
		switch idx {
		case 0:
			return t.of()
		case 1:
			return tsSetterOf(t.of())
		}
	case t.k == tyArray && t.sym == "":
		return t.of()
	case t.k == tyUnknown:
		return tsUnknownTy
	}
	return tsNoTy
}

// tsIsUnion reports a union whose parts Layer 2 knows.
func tsIsUnion(t tsTy) bool { return t.k == tyUnknown && t.parts != nil && !t.inter }

func (r *tsResolver) bindingType1(m *tsModule, id *sitter.Node, depth int) tsTy {
	p := id.Parent()
	if p != nil && p.Type() == "array_pattern" {
		return r.arrayElemType(m, p, id, depth)
	}
	// A destructured name: the member of its pattern's type.
	key, pat := tsPatternKeyOf(id, m.src)
	if pat != nil {
		mb := r.memberOf(r.patternType(m, pat, depth+1), key, depth+1)
		switch mb.k {
		case tyDecl:
			return mb.typ
		case tyUnknown:
			return tsUnknownTy
		case tyOutside:
			if mb.typ.k != tyNone {
				return mb.typ // a local member whose type is followed
			}
			return tsOutsideTy
		}
		return tsNoTy
	}
	switch p.Type() {
	case "variable_declarator":
		if n := p.ChildByFieldName("name"); n != nil && n.StartByte() == id.StartByte() {
			return r.declaratorType(m, p, depth)
		}
	case "function_declaration", "generator_function_declaration":
		// A function declared inside another: its name is a local.
		if n := p.ChildByFieldName("name"); n != nil && n.StartByte() == id.StartByte() {
			return tsTy{k: tyFn, m: m, node: p}
		}
	case "required_parameter", "optional_parameter":
		if pat := p.ChildByFieldName("pattern"); pat == nil || pat.StartByte() != id.StartByte() {
			return tsNoTy
		}
		ps := p.Parent()
		if ps == nil || ps.Type() != "formal_parameters" {
			return tsNoTy
		}
		fn := ps.Parent()
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
		return r.paramOf(r.contextType(m, fn, depth+1), i, depth+1)
	case "arrow_function":
		if par := p.ChildByFieldName("parameter"); par != nil && par.StartByte() == id.StartByte() {
			return r.paramOf(r.contextType(m, p, depth+1), 0, depth+1)
		}
	case "for_in_statement":
		// for (const x of xs)
		if left := p.ChildByFieldName("left"); left == nil || left.StartByte() != id.StartByte() {
			return tsNoTy
		}
		return r.forOfElem(m, p, depth)
	case "lexical_declaration", "variable_declaration":
		if f := p.Parent(); f != nil && f.Type() == "for_in_statement" {
			return r.forOfElem(m, f, depth)
		}
	}
	return tsNoTy
}

func (r *tsResolver) forOfElem(m *tsModule, f *sitter.Node, depth int) tsTy {
	of := false
	for i := 0; i < int(f.ChildCount()); i++ {
		if f.Child(i).Type() == "of" {
			of = true
		}
	}
	right := f.ChildByFieldName("right")
	if !of || right == nil {
		return tsNoTy
	}
	if right.Type() == "call_expression" {
		// A generator's call: what it yields.
		if ft := r.exprType(m, right.ChildByFieldName("function"), depth+1); ft.k == tyFn && tsGenerator(ft.node) {
			if ft.node.ChildByFieldName("return_type") == nil {
				return tsUnknownTy
			}
			return r.generatorContext(ft.m, ft.node, 0, depth+1)
		}
	}
	switch t := r.exprType(m, right, depth+1); t.k {
	case tyArray:
		if t.sym == "" {
			return t.of()
		}
		return tsUnknownTy
	case tyNone, tyOutside:
		// No type: a member of the element counts through its local root.
		// A package's iterable stays that too: exprType's outside is not
		// sure enough (Object.values(o.m ?? {})) to drop the count.
		return tsNoTy
	}
	return tsUnknownTy
}

// declaratorType is a variable's type: its annotation's, else its
// initializer's.
func (r *tsResolver) declaratorType(m *tsModule, d *sitter.Node, depth int) tsTy {
	if t := d.ChildByFieldName("type"); t != nil {
		res := r.typeOfNode(m, t, depth+1)
		if tsIsUnion(res) && d.ChildByFieldName("value") != nil {
			return tsUnknownTy // x: A | B = a is A where it is read: narrowed by the assignment
		}
		return res
	}
	if v := d.ChildByFieldName("value"); v != nil {
		return r.exprType(m, v, depth+1)
	}
	return tsNoTy
}

// thisType is this in a method of a top-level class: the class.
func (r *tsResolver) thisType(m *tsModule, e *sitter.Node, depth int) tsTy {
	for a := e.Parent(); a != nil; a = a.Parent() {
		switch a.Type() {
		case "function_declaration", "function_expression", "function", "generator_function":
			return tsNoTy // this of a function: not the class's
		case "class_body":
			c := a.Parent()
			if c == nil || c.Type() != "class_declaration" && c.Type() != "abstract_class_declaration" {
				return tsNoTy
			}
			n := c.ChildByFieldName("name")
			if n == nil {
				return tsNoTy
			}
			if d := m.nodes[tsType(n.Content(m.src))]; d == nil || d.StartByte() != c.StartByte() {
				return tsNoTy
			}
			return tsTy{k: tyDecl, m: m, node: c, sym: tsType(n.Content(m.src))}
		}
	}
	return tsNoTy
}

// contextType is the type the context gives an object literal, an array
// literal or a function expression e: what it is assigned to, passed as or
// returned as.
func (r *tsResolver) contextType(m *tsModule, e *sitter.Node, depth int) tsTy {
	if e == nil || depth > tsMaxDepth {
		return tsUnknownTy
	}
	key := tsTyKey{m.file, e.StartByte(), e.EndByte(), 'c'}
	if t, ok := r.tyCache[key]; ok {
		return t
	}
	r.tyCache[key] = tsNoTy
	t := r.contextType1(m, e, depth)
	r.tyCache[key] = t
	return t
}

func (r *tsResolver) contextType1(m *tsModule, e *sitter.Node, depth int) tsTy {
	p := e.Parent()
	if p == nil {
		return tsNoTy
	}
	switch p.Type() {
	case "parenthesized_expression":
		return r.contextType(m, p, depth+1)
	case "variable_declarator":
		if v := p.ChildByFieldName("value"); v == nil || v.StartByte() != e.StartByte() {
			return tsNoTy
		}
		if t := p.ChildByFieldName("type"); t != nil {
			return r.typeOfNode(m, t, depth+1)
		}
	case "as_expression", "satisfies_expression":
		if p.NamedChildCount() == 2 && p.NamedChild(0).StartByte() == e.StartByte() {
			return r.typeOfNode(m, p.NamedChild(1), depth+1)
		}
	case "pair":
		if v := p.ChildByFieldName("value"); v == nil || v.StartByte() != e.StartByte() {
			return tsNoTy
		}
		obj := p.Parent()
		k := p.ChildByFieldName("key")
		if obj == nil || k == nil {
			return tsNoTy
		}
		ot := r.contextType(m, obj, depth+1)
		if ot.k == tyRecord {
			return ot.of()
		}
		if k.Type() != "property_identifier" {
			if ot.k == tyNone || ot.k == tyOutside {
				return ot
			}
			return tsUnknownTy
		}
		return r.memberContext(ot, k.Content(m.src), depth)
	case "jsx_expression":
		// <F a={{ … }} />: F's props' member a; <Ctx.Provider value={…}>:
		// the context's type.
		attr := p.Parent()
		if attr == nil || attr.Type() != "jsx_attribute" || attr.NamedChildCount() < 2 {
			return tsNoTy
		}
		name := attr.NamedChild(0)
		el := attr.Parent()
		if name.Type() != "property_identifier" || el == nil {
			return tsNoTy
		}
		tag := el.ChildByFieldName("name")
		if tag == nil {
			return tsNoTy
		}
		switch tag.Type() {
		case "identifier":
			ft := r.exprType(m, tag, depth+1)
			if ft.k != tyFn {
				return tsNoTy
			}
			return r.memberContext(r.paramOf(ft, 0, depth+1), name.Content(m.src), depth)
		case "member_expression":
			obj, prop := tag.ChildByFieldName("object"), tag.ChildByFieldName("property")
			if obj == nil || prop == nil || prop.Content(m.src) != "Provider" || name.Content(m.src) != "value" {
				return tsNoTy
			}
			if ct := r.exprType(m, obj, depth+1); ct.k == tyStore && ct.sym == tsReactContext {
				return ct.of()
			}
		}
		return tsNoTy
	case "array":
		if at := r.contextType(m, p, depth+1); at.k == tyArray && at.sym == "" {
			return at.of()
		} else if at.k == tyUnknown {
			return tsUnknownTy
		}
	case "return_statement":
		for a := p.Parent(); a != nil; a = a.Parent() {
			switch a.Type() {
			case "function_declaration", "function_expression", "function", "arrow_function", "method_definition", "generator_function", "generator_function_declaration":
				return r.resultContext(m, a, depth)
			case "program", "class_body":
				return tsNoTy
			}
		}
	case "arrow_function":
		if b := p.ChildByFieldName("body"); b != nil && b.StartByte() == e.StartByte() {
			return r.resultContext(m, p, depth)
		}
	case "yield_expression":
		for i := 0; i < int(p.ChildCount()); i++ {
			if p.Child(i).Type() == "*" {
				return tsUnknownTy // yield* e: an iterable of what the generator yields
			}
		}
		for a := p.Parent(); a != nil; a = a.Parent() {
			switch a.Type() {
			case "function_declaration", "function_expression", "function", "arrow_function", "method_definition", "generator_function", "generator_function_declaration":
				if !tsGenerator(a) {
					return tsUnknownTy
				}
				return r.generatorContext(m, a, 0, depth)
			case "program", "class_body":
				return tsUnknownTy
			}
		}
	case "arguments":
		call := p.Parent()
		if call != nil && call.Type() == "new_expression" {
			i := 0
			for k := 0; k < int(p.NamedChildCount()); k++ {
				if p.NamedChild(k).StartByte() == e.StartByte() {
					break
				}
				i++
			}
			return r.paramOf(r.constructorOf(m, call, depth+1), i, depth+1)
		}
		if call == nil || call.Type() != "call_expression" {
			return tsNoTy
		}
		i := 0
		for k := 0; k < int(p.NamedChildCount()); k++ {
			if p.NamedChild(k).StartByte() == e.StartByte() {
				break
			}
			i++
		}
		return r.argContext(m, call, i, depth)
	case "assignment_expression":
		if right := p.ChildByFieldName("right"); right != nil && right.StartByte() == e.StartByte() {
			if left := p.ChildByFieldName("left"); left != nil {
				return r.exprType(m, left, depth+1)
			}
		}
	case "required_parameter", "optional_parameter", "public_field_definition":
		// A default value or a field's initializer: the declared type.
		if v := p.ChildByFieldName("value"); v != nil && v.StartByte() == e.StartByte() {
			if t := p.ChildByFieldName("type"); t != nil {
				return r.typeOfNode(m, t, depth+1)
			}
		}
	case "ternary_expression":
		if c := p.ChildByFieldName("condition"); c != nil && c.StartByte() == e.StartByte() {
			return tsNoTy
		}
		return r.contextType(m, p, depth+1)
	case "binary_expression":
		op := p.ChildByFieldName("operator")
		if op != nil && (op.Type() == "??" || op.Type() == "||") {
			return r.contextType(m, p, depth+1)
		}
		if right := p.ChildByFieldName("right"); op != nil && op.Type() == "&&" && right != nil && tsSame(right, e) {
			return r.contextType(m, p, depth+1) // cond && { … }: TypeScript gives the right side the context
		}
	case "spread_element":
		// { ...(c ? { k } : {}) }: a spread in an object literal has the
		// literal's context.
		if o := p.Parent(); o != nil && o.Type() == "object" {
			return r.contextType(m, o, depth+1)
		}
	}
	return tsNoTy
}

// memberContext is the context ot gives its member name's value.
func (r *tsResolver) memberContext(ot tsTy, name string, depth int) tsTy {
	mb := r.memberOf(ot, name, depth+1)
	switch mb.k {
	case tyDecl:
		return mb.typ
	case tyNone:
		return tsNoTy
	case tyOutside:
		if mb.typ.k != tyNone {
			return mb.typ // a local member whose type is followed
		}
		return tsOutsideTy
	}
	return tsUnknownTy
}

// resultContext is what a function's result must be: its declared return
// type (a promise's element in an async function), else its own context's.
func (r *tsResolver) resultContext(m *tsModule, fn *sitter.Node, depth int) tsTy {
	if tsGenerator(fn) {
		// TReturn; an async generator's return expression is TReturn |
		// PromiseLike<TReturn>, as an async function's.
		t := r.generatorContext(m, fn, 1, depth)
		if tsIsAsync(fn) {
			t.orPromise = true
		}
		return t
	}
	var t tsTy
	if rt := fn.ChildByFieldName("return_type"); rt != nil {
		t = r.typeOfNode(m, rt, depth+1)
	} else {
		switch fn.Type() {
		case "arrow_function", "function_expression", "function":
			ct := r.contextType(m, fn, depth+1)
			switch ct.k {
			case tyFn:
				t = r.returnOf(m, tsTy{k: tyFn, m: ct.m, node: ct.node}, nil, depth+1)
			case tyCallback:
				t = ct.of()
			case tyUnknown:
				return tsUnknownTy
			}
		}
		if tsIsAsync(fn) {
			if t.k == tyPromise {
				t = t.of()
			}
			t.orPromise = true
		}
		return t
	}
	if tsIsAsync(fn) {
		// An async function's return expression is contextually typed
		// T | PromiseLike<T>: a union, which has no property of T's.
		if t.k == tyPromise {
			t = t.of()
		}
		t.orPromise = true
	}
	return t
}

// tsIterationTypes are the global types a generator's result can be
// declared as; their type arguments are T (what it yields), TReturn, TNext.
var tsIterationTypes = map[string]bool{
	"Generator": true, "AsyncGenerator": true, "Iterator": true, "AsyncIterator": true,
	"Iterable": true, "AsyncIterable": true, "IterableIterator": true, "AsyncIterableIterator": true,
	"IteratorObject": true, "AsyncIteratorObject": true,
}

// generatorContext is what generator fn's yield operands (i 0) or return
// expressions (i 1) must be: that type argument of its declared result. A
// missing argument defaults to any or void (outside). Without a declared
// result, a declaration or a class's method has no such context; a function
// expression may get one from its own context, which is not followed.
func (r *tsResolver) generatorContext(m *tsModule, fn *sitter.Node, i int, depth int) tsTy {
	rt := fn.ChildByFieldName("return_type")
	if rt == nil {
		if fn.Type() == "generator_function_declaration" {
			return tsNoTy
		}
		if p := fn.Parent(); fn.Type() == "method_definition" && p != nil && p.Type() == "class_body" {
			return tsNoTy
		}
		if r.contextType(m, fn, depth+1).k == tyNone {
			return tsNoTy
		}
		return tsUnknownTy
	}
	t := rt
	if t.Type() == "type_annotation" && t.NamedChildCount() == 1 {
		t = t.NamedChild(0)
	}
	name, args := t, (*sitter.Node)(nil)
	if t.Type() == "generic_type" {
		name, args = t.ChildByFieldName("name"), t.ChildByFieldName("type_arguments")
	}
	if name == nil || name.Type() != "type_identifier" {
		return tsUnknownTy
	}
	n := name.Content(m.src)
	if !tsIterationTypes[n] || tsBoundLocally(name, n, m.src) {
		return tsUnknownTy
	}
	if _, ok := r.resolveIn(m.file, n, tsTypeNS); ok {
		return tsUnknownTy // the repository's own type of that name
	}
	if args == nil || int(args.NamedChildCount()) <= i {
		return tsOutsideTy
	}
	return r.typeOfNode(m, args.NamedChild(i), depth+1)
}

// argContext is the type a call gives its i-th argument.
func (r *tsResolver) argContext(m *tsModule, call *sitter.Node, i int, depth int) tsTy {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return tsNoTy
	}
	// zustand: create<T>()(init) and create<T>(init) type init's result as
	// T; a middleware (persist(init, …)) passes its first argument's on.
	if s, ok := r.zustandInit(m, call, depth); ok {
		if i == 0 {
			return s
		}
		return tsNoTy
	}
	if r.reactHook(m, fn) == "createContext" {
		// createContext<T>(defaultValue: T)
		if ta := call.ChildByFieldName("type_arguments"); ta != nil && ta.NamedChildCount() == 1 && i == 0 {
			return r.typeOfNode(m, ta.NamedChild(0), depth+1)
		}
		return tsNoTy
	}
	ft := r.exprType(m, fn, depth+1)
	switch ft.k {
	case tyCallback:
		if ft.sym != tsSetter || i != 0 {
			return tsNoTy
		}
		// set((prev) => s): a function from the state to it. The argument
		// itself is a union with that function type (S | ((prev: S) => S),
		// zustand's T | Partial<T> | …), which has no property of S's: a
		// literal's keys are its own, as scip-typescript writes them.
		st := ft.of()
		if a := call.ChildByFieldName("arguments"); a != nil && i < int(a.NamedChildCount()) {
			switch a.NamedChild(i).Type() {
			case "arrow_function", "function_expression", "function":
				return tsTy{k: tyCallback, params: []tsTy{st}, elem: &st}
			}
		}
		return tsNoTy
	case tyFn:
		// edit<T>(id, fn: (s: State) => T): fn's s is State whatever T is;
		// a parameter type that mentions T is unknown (a local type name).
		return r.paramOf(ft, i, depth+1)
	case tyStore:
		if i == 0 && !tsReactBox(ft) {
			return tsTy{k: tyCallback, params: []tsTy{ft.of()}}
		}
		return tsNoTy
	case tyArray:
		method := strings.TrimPrefix(ft.sym, "method:")
		el := ft.of()
		switch method {
		case "map", "forEach", "filter", "find", "findIndex", "findLast", "findLastIndex", "some", "every", "flatMap":
			if i == 0 {
				return tsTy{k: tyCallback, params: []tsTy{el, tsOutsideTy, {k: tyArray, elem: ft.elem}}}
			}
		case "push", "unshift", "includes", "indexOf", "lastIndexOf":
			return el
		case "sort", "toSorted":
			if i == 0 {
				return tsTy{k: tyCallback, params: []tsTy{el, el}}
			}
		}
		return tsNoTy
	case tyMap:
		if strings.TrimPrefix(ft.sym, "method:") == "forEach" && i == 0 {
			return tsTy{k: tyCallback, params: []tsTy{ft.of()}}
		}
		if strings.TrimPrefix(ft.sym, "method:") == "set" && i == 1 {
			return ft.of()
		}
		return tsNoTy
	case tyUnknown:
		return tsUnknownTy
	case tyOutside:
		return tsOutsideTy
	}
	return tsNoTy
}

// zustandCreate reports whether n is zustand's create (imported from
// "zustand" and not shadowed).
func (r *tsResolver) zustandName(m *tsModule, n *sitter.Node, spec string, names ...string) bool {
	if n == nil || n.Type() != "identifier" {
		return false
	}
	name := n.Content(m.src)
	imp, ok := m.imports[name]
	if !ok || imp.spec != spec || tsBoundLocally(n, name, m.src) {
		return false
	}
	for _, want := range names {
		if imp.name == want {
			return true
		}
	}
	return false
}

// zustandStore recognizes create<T>()(init) and create<T>(init): a store of T.
func (r *tsResolver) zustandStore(m *tsModule, call *sitter.Node, depth int) (tsTy, bool) {
	t, ok := r.zustandState(m, call, depth)
	if !ok {
		return tsNoTy, false
	}
	return tsWrap(tyStore, t), true
}

func (r *tsResolver) zustandState(m *tsModule, call *sitter.Node, depth int) (tsTy, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return tsNoTy, false
	}
	create := call
	if fn.Type() == "call_expression" {
		// create<T>()(init)
		create = fn
		if a := fn.ChildByFieldName("arguments"); a == nil || a.NamedChildCount() != 0 {
			return tsNoTy, false
		}
	}
	cf := create.ChildByFieldName("function")
	ta := create.ChildByFieldName("type_arguments")
	if !r.zustandName(m, cf, "zustand", "create") || ta == nil || ta.NamedChildCount() != 1 {
		return tsNoTy, false
	}
	if create == call {
		if a := call.ChildByFieldName("arguments"); a == nil || a.NamedChildCount() == 0 {
			return tsNoTy, false // create<T>(): a curried call, the store is its result's
		}
	}
	return r.typeOfNode(m, ta.NamedChild(0), depth+1), true
}

// zustandInit is the context a store's initializer gets in call: a
// callback (set, get) returning the state.
func (r *tsResolver) zustandInit(m *tsModule, call *sitter.Node, depth int) (tsTy, bool) {
	if st, ok := r.zustandState(m, call, depth); ok {
		return tsTy{k: tyCallback, params: []tsTy{tsSetterOf(st), {k: tyCallback, elem: &st}}, elem: &st}, true
	}
	fn := call.ChildByFieldName("function")
	if r.zustandName(m, fn, "zustand/middleware", "persist", "devtools", "subscribeWithSelector") ||
		r.zustandName(m, fn, "zustand/middleware/immer", "immer") {
		ct := r.contextType(m, call, depth+1)
		if ct.k == tyCallback {
			return ct, true
		}
		return tsNoTy, true
	}
	return tsNoTy, false
}

// tsOptionalMember reports a "?" after a member's name.
func tsOptionalMember(mn *sitter.Node) bool {
	for i := 0; i < int(mn.ChildCount()); i++ {
		if mn.Child(i).Type() == "?" {
			return true
		}
	}
	return false
}

func tsGenerator(fn *sitter.Node) bool {
	switch fn.Type() {
	case "generator_function", "generator_function_declaration":
		return true
	}
	for i := 0; i < int(fn.ChildCount()); i++ {
		if fn.Child(i).Type() == "*" {
			return true
		}
	}
	return false
}

// tsPatternKeyOf is the property a name in an object pattern takes, and the
// pattern: { a }, { a = 1 }, { k: a }, { k: a = 1 }.
func tsPatternKeyOf(id *sitter.Node, src []byte) (string, *sitter.Node) {
	p := id.Parent()
	if p == nil {
		return "", nil
	}
	switch {
	case id.Type() == "shorthand_property_identifier_pattern" && p.Type() == "object_pattern":
		return id.Content(src), p
	case p.Type() == "object_assignment_pattern":
		if l := p.ChildByFieldName("left"); l != nil && l.StartByte() == id.StartByte() {
			if pp := p.Parent(); pp != nil && pp.Type() == "object_pattern" {
				return id.Content(src), pp
			}
		}
	case p.Type() == "assignment_pattern":
		if pp := p.Parent(); pp != nil && pp.Type() == "pair_pattern" {
			return tsPatternKeyOf(p, src)
		}
	case p.Type() == "pair_pattern":
		if v := p.ChildByFieldName("value"); v != nil && v.StartByte() == id.StartByte() {
			k := p.ChildByFieldName("key")
			if pp := p.Parent(); k != nil && k.Type() == "property_identifier" && pp != nil && pp.Type() == "object_pattern" {
				return k.Content(src), pp
			}
		}
	}
	return "", nil
}

// patternType is the type an object pattern destructures: a parameter's
// (spelled, or from its function's context), a declaration's value, or the
// enclosing pattern's member (nested patterns).
func (r *tsResolver) patternType(m *tsModule, pat *sitter.Node, depth int) tsTy {
	if depth > tsMaxDepth {
		return tsUnknownTy
	}
	p := pat.Parent()
	if p == nil {
		return tsNoTy
	}
	switch p.Type() {
	case "required_parameter", "optional_parameter":
		if pt := p.ChildByFieldName("pattern"); pt == nil || pt.StartByte() != pat.StartByte() {
			return tsNoTy
		}
		if t := p.ChildByFieldName("type"); t != nil {
			return r.typeOfNode(m, t, depth+1)
		}
		ps := p.Parent()
		if ps == nil || ps.Type() != "formal_parameters" {
			return tsNoTy
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
		return r.paramOf(r.contextType(m, ps.Parent(), depth+1), i, depth+1)
	case "variable_declarator":
		if n := p.ChildByFieldName("name"); n == nil || n.StartByte() != pat.StartByte() {
			return tsNoTy
		}
		return r.declaratorType(m, p, depth)
	case "pair_pattern", "assignment_pattern":
		key, outer := tsPatternKeyOf(pat, m.src)
		if outer == nil {
			return tsNoTy
		}
		mb := r.memberOf(r.patternType(m, outer, depth+1), key, depth+1)
		switch mb.k {
		case tyDecl:
			return mb.typ
		case tyUnknown:
			return tsUnknownTy
		case tyOutside:
			if mb.typ.k != tyNone {
				return mb.typ
			}
			return tsOutsideTy
		}
	}
	return tsNoTy
}

// constructorOf is the constructor new C(…) calls, for a repository class
// declaring one (an inherited one is not followed: unknown).
func (r *tsResolver) constructorOf(m *tsModule, n *sitter.Node, depth int) tsTy {
	c := n.ChildByFieldName("constructor")
	if c == nil || c.Type() != "identifier" {
		return tsNoTy
	}
	ct := r.classValueType(m, c, depth+1)
	switch ct.k {
	case tyDecl:
	case tyOutside:
		return tsOutsideTy
	default:
		return tsUnknownTy
	}
	if ct.node.Type() != "class_declaration" && ct.node.Type() != "abstract_class_declaration" {
		return tsUnknownTy
	}
	if n.ChildByFieldName("type_arguments") != nil || ct.node.ChildByFieldName("type_parameters") != nil {
		return tsUnknownTy
	}
	body := ct.node.ChildByFieldName("body")
	if body == nil {
		return tsUnknownTy
	}
	var found *sitter.Node
	for i := 0; i < int(body.NamedChildCount()); i++ {
		mm := body.NamedChild(i)
		if mm.Type() == "method_definition" {
			if nm := mm.ChildByFieldName("name"); nm != nil && nm.Content(ct.m.src) == "constructor" {
				if found != nil {
					return tsUnknownTy // overloads
				}
				found = mm
			}
		}
	}
	if found == nil {
		return tsUnknownTy
	}
	return tsTy{k: tyFn, m: ct.m, node: found}
}
