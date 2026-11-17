//go:build cgo

package overlay

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// zod's data types come from values: z.infer<typeof s> is what the schema s
// parses to. scip-typescript points a member of such a type at the key of
// the z.object({…}) literal that declares it (a counter-numbered symbol,
// role0:), through every method that keeps the keys: extend, merge, pick,
// omit, partial, optional, refine, strict, z.input/z.output, parse() and
// z.preprocess. Checked on a probe project against scip-typescript 0.4.0
// with zod 4 (TestTSLayer2Zod).
//
// An object schema is a tyLit with sym tsZodSym: every rule that knows
// object types treats it as one with repository members, and memberOf finds
// the key in its literals (params, in order; a later one wins). Anything
// not modeled (transform, unions, lazy, a schema behind an annotation) is
// unknown, so its members count.

const tsZodSym = "zod:"

func tsZod(t tsTy) bool { return t.k == tyLit && t.sym == tsZodSym }

func tsZodSpec(spec string) bool { return spec == "zod" || strings.HasPrefix(spec, "zod/") }

// zodNS reports whether id names zod's namespace in m: import { z },
// import * as z or the default import of "zod".
func (r *tsResolver) zodNS(m *tsModule, id *sitter.Node) bool {
	if id == nil || id.Type() != "identifier" {
		return false
	}
	name := id.Content(m.src)
	imp, ok := m.imports[name]
	if !ok || !tsZodSpec(imp.spec) || tsBoundLocally(id, name, m.src) {
		return false
	}
	return imp.name == "z" || imp.name == "*" || imp.name == "default"
}

// zodInfer is the type of z.infer<typeof s> (z.input, z.output, z.TypeOf);
// ok is false when t is not one.
func (r *tsResolver) zodInfer(m *tsModule, t *sitter.Node, depth int) (tsTy, bool) {
	name, args := t.ChildByFieldName("name"), t.ChildByFieldName("type_arguments")
	if name == nil || args == nil || name.Type() != "nested_type_identifier" {
		return tsNoTy, false
	}
	mod, n := name.ChildByFieldName("module"), name.ChildByFieldName("name")
	if mod == nil || n == nil || !r.zodNS(m, mod) {
		return tsNoTy, false
	}
	switch n.Content(m.src) {
	case "infer", "input", "output", "TypeOf":
	default:
		return tsNoTy, false
	}
	if args.NamedChildCount() != 1 {
		return tsUnknownTy, true
	}
	q := args.NamedChild(0)
	if q.Type() != "type_query" || q.NamedChildCount() != 1 {
		return tsUnknownTy, true
	}
	return r.zodOut(m, q.NamedChild(0), depth+1), true
}

// zodParse is the type of s.parse(v) and s.parseAsync(v) for a zod schema
// s; ok is false for any other call.
func (r *tsResolver) zodParse(m *tsModule, call *sitter.Node, depth int) (tsTy, bool) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Type() != "member_expression" {
		return tsNoTy, false
	}
	obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
	if obj == nil || prop == nil {
		return tsNoTy, false
	}
	p := prop.Content(m.src)
	if p != "parse" && p != "parseAsync" || !r.zodSchema(m, obj, depth+1) {
		return tsNoTy, false
	}
	out := r.zodOut(m, obj, depth+1)
	if p == "parseAsync" {
		return tsWrap(tyPromise, out), true
	}
	return out, true
}

// zodSchema reports whether e is a zod schema: a call chain rooted at zod's
// namespace, or a name whose initializer is one.
func (r *tsResolver) zodSchema(m *tsModule, e *sitter.Node, depth int) bool {
	for depth <= tsMaxDepth {
		switch e.Type() {
		case "parenthesized_expression":
			if e.NamedChildCount() != 1 {
				return false
			}
			e = e.NamedChild(0)
		case "call_expression":
			fn := e.ChildByFieldName("function")
			if fn == nil || fn.Type() != "member_expression" {
				return false
			}
			e = fn.ChildByFieldName("object")
			if e == nil {
				return false
			}
			if r.zodNS(m, e) {
				return true
			}
		case "identifier":
			dm, v := r.zodInit(m, e)
			if v == nil {
				return false
			}
			m, e = dm, v
		default:
			return false
		}
		depth++
	}
	return false
}

// zodInit is the initializer of the variable e names (a local or a
// top-level one, imported or not), nil when it has a type annotation or
// none.
func (r *tsResolver) zodInit(m *tsModule, e *sitter.Node) (*tsModule, *sitter.Node) {
	name := e.Content(m.src)
	var d *sitter.Node
	if id, bound := tsLocalBinding(e, name, m.src); bound {
		if id == nil {
			return nil, nil
		}
		d = id.Parent()
	} else {
		res, ok := r.resolveIn(m.file, name, tsValue)
		if !ok || res.external || res.namespace != "" || res.sym == "" {
			return nil, nil
		}
		dm := r.module(r.byPrefix[tsSymPrefix(res.sym)])
		if dm == nil {
			return nil, nil
		}
		m, d = dm, dm.nodes[strings.TrimPrefix(res.sym, dm.prefix)]
	}
	if d == nil || d.Type() != "variable_declarator" || d.ChildByFieldName("type") != nil {
		return nil, nil
	}
	if n := d.ChildByFieldName("name"); n == nil || n.Type() != "identifier" {
		return nil, nil
	}
	return m, d.ChildByFieldName("value")
}

// zodOut is the type schema expression e parses to.
func (r *tsResolver) zodOut(m *tsModule, e *sitter.Node, depth int) tsTy {
	if e == nil || depth > tsMaxDepth {
		return tsUnknownTy
	}
	switch e.Type() {
	case "parenthesized_expression":
		if e.NamedChildCount() != 1 {
			return tsUnknownTy
		}
		return r.zodOut(m, e.NamedChild(0), depth+1)
	case "identifier", "shorthand_property_identifier":
		dm, v := r.zodInit(m, e)
		if v == nil {
			return tsUnknownTy
		}
		return r.zodOut(dm, v, depth+1)
	case "call_expression":
		fn, args := e.ChildByFieldName("function"), e.ChildByFieldName("arguments")
		if fn == nil || args == nil {
			return tsUnknownTy
		}
		switch fn.Type() {
		case "member_expression":
			obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
			if obj == nil || prop == nil {
				return tsUnknownTy
			}
			if r.zodNS(m, obj) {
				return r.zodCtor(m, prop.Content(m.src), tsArgs(args), depth)
			}
			if obj.Type() == "member_expression" {
				if o, p := obj.ChildByFieldName("object"), obj.ChildByFieldName("property"); o != nil && p != nil && p.Content(m.src) == "coerce" && r.zodNS(m, o) {
					return tsOutsideTy // z.coerce.number()
				}
			}
			return r.zodMethod(m, r.zodOut(m, obj, depth), prop.Content(m.src), tsArgs(args), depth) // a chain link: bounded by the syntax, not a hop
		case "identifier":
			// A helper of the repository that builds a schema: what its
			// body returns, if that is one expression.
			if fm, body := r.zodHelper(m, fn); body != nil {
				return r.zodOut(fm, body, depth+1)
			}
		}
	}
	return tsUnknownTy
}

// zodHelper is the returned expression of the function fn names: an arrow
// function's expression body or a function's only return statement.
func (r *tsResolver) zodHelper(m *tsModule, fn *sitter.Node) (*tsModule, *sitter.Node) {
	name := fn.Content(m.src)
	if _, bound := tsLocalBinding(fn, name, m.src); bound {
		return nil, nil
	}
	res, ok := r.resolveIn(m.file, name, tsValue)
	if !ok || res.external || res.namespace != "" || res.sym == "" {
		return nil, nil
	}
	dm := r.module(r.byPrefix[tsSymPrefix(res.sym)])
	if dm == nil {
		return nil, nil
	}
	d := dm.nodes[strings.TrimPrefix(res.sym, dm.prefix)]
	if d == nil {
		return nil, nil
	}
	if d.Type() == "variable_declarator" {
		if d.ChildByFieldName("type") != nil {
			return nil, nil
		}
		d = d.ChildByFieldName("value")
		if d == nil || d.Type() != "arrow_function" {
			return nil, nil
		}
	}
	if d.ChildByFieldName("return_type") != nil {
		return nil, nil
	}
	body := d.ChildByFieldName("body")
	if body == nil {
		return nil, nil
	}
	if body.Type() != "statement_block" {
		return dm, body
	}
	var ret *sitter.Node
	for i := 0; i < int(body.NamedChildCount()); i++ {
		if c := body.NamedChild(i); c.Type() == "return_statement" {
			if ret != nil {
				return nil, nil
			}
			ret = c
		}
	}
	if ret == nil || ret.NamedChildCount() != 1 {
		return nil, nil
	}
	return dm, ret.NamedChild(0)
}

// tsArgs are a call's arguments without comments.
func tsArgs(args *sitter.Node) []*sitter.Node {
	var out []*sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if a := args.NamedChild(i); a.Type() != "comment" {
			out = append(out, a)
		}
	}
	return out
}

// zodCtor is the output of z.name(args…).
func (r *tsResolver) zodCtor(m *tsModule, name string, args []*sitter.Node, depth int) tsTy {
	switch name {
	case "object", "strictObject", "looseObject":
		if len(args) == 0 || args[0].Type() != "object" {
			return tsUnknownTy
		}
		return tsTy{k: tyLit, m: m, node: args[0], sym: tsZodSym, params: []tsTy{{m: m, node: args[0]}}}
	case "array":
		if len(args) != 1 {
			return tsUnknownTy
		}
		return tsWrap(tyArray, r.zodOut(m, args[0], depth+1))
	case "record", "partialRecord":
		if len(args) == 0 {
			return tsUnknownTy
		}
		return tsWrap(tyRecord, r.zodOut(m, args[len(args)-1], depth+1))
	case "map":
		if len(args) != 2 {
			return tsUnknownTy
		}
		return tsWrap(tyMap, r.zodOut(m, args[1], depth+1))
	case "preprocess":
		if len(args) < 2 {
			return tsUnknownTy
		}
		return r.zodOut(m, args[1], depth+1)
	case "optional", "nullable", "nullish":
		if len(args) != 1 {
			return tsUnknownTy
		}
		t := r.zodOut(m, args[0], depth+1)
		t.nullable = true
		return t
	case "string", "number", "boolean", "bigint", "date", "symbol", "undefined", "null", "void",
		"any", "unknown", "never", "nan", "enum", "nativeEnum", "literal", "email", "url", "uuid",
		"uuidv4", "uuidv7", "guid", "cuid", "cuid2", "ulid", "nanoid", "ipv4", "ipv6", "cidrv4",
		"cidrv6", "base64", "base64url", "e164", "jwt", "emoji", "int", "int32", "uint32", "int64",
		"uint64", "float32", "float64", "templateLiteral", "file", "stringbool", "hostname", "hex":
		return tsOutsideTy
	}
	return tsUnknownTy
}

// zodMethod is the output of s.name(args…) for a schema s whose output is in.
func (r *tsResolver) zodMethod(m *tsModule, in tsTy, name string, args []*sitter.Node, depth int) tsTy {
	switch name {
	case "transform":
		if len(args) == 1 && in.k != tyUnknown && r.zodIdentity(m, args[0]) {
			return in // transform(f) with f<P>(p: P): P
		}
		return tsUnknownTy
	case "pipe", "overwrite", "and", "or", "keyof", "element", "unwrap", "shape":
		return tsUnknownTy
	case "array":
		return tsWrap(tyArray, in)
	}
	if in.k == tyOutside {
		return tsOutsideTy // a primitive's checks: z.string().max(3)
	}
	if in.k == tyUnknown && in.parts == nil {
		return tsUnknownTy
	}
	switch name {
	case "optional", "nullable", "nullish", "exactOptional":
		in.nullable = true
		return in
	case "default", "prefault", "catch", "nonoptional":
		in.nullable = false
		return in
	case "strict", "strip", "passthrough", "loose", "describe", "meta", "refine", "superRefine",
		"check", "readonly", "brand", "required", "min", "max", "length", "nonempty", "size", "positive",
		"nonnegative", "int", "finite", "trim", "toLowerCase", "toUpperCase", "regex", "startsWith", "endsWith":
		return in // checks keep the type: z.array(s).min(1).max(9)
	case "extend", "safeExtend":
		if !tsZod(in) || len(args) != 1 || args[0].Type() != "object" {
			return tsUnknownTy
		}
		out := in
		out.params = append(append([]tsTy(nil), in.params...), tsTy{m: m, node: args[0]})
		return out
	case "merge":
		if !tsZod(in) || len(args) != 1 {
			return tsUnknownTy
		}
		o := r.zodOut(m, args[0], depth+1)
		if !tsZod(o) {
			return tsUnknownTy
		}
		out := in
		out.params = append(append([]tsTy(nil), in.params...), o.params...)
		return out
	case "pick", "omit":
		if !tsZod(in) || len(args) != 1 || args[0].Type() != "object" {
			return tsUnknownTy
		}
		keys := map[string]bool{}
		for i := 0; i < int(args[0].NamedChildCount()); i++ {
			p := args[0].NamedChild(i)
			if p.Type() != "pair" {
				return tsUnknownTy
			}
			k := p.ChildByFieldName("key")
			if k == nil || k.Type() != "property_identifier" {
				return tsUnknownTy
			}
			keys[k.Content(m.src)] = true
		}
		return tsTy{k: tyPick, elem: &in, keys: keys, omit: name == "omit"}
	case "partial":
		if len(args) != 0 {
			return tsUnknownTy
		}
		return tsTy{k: tyPick, elem: &in, optional: true}
	}
	return tsUnknownTy
}

// zodMember is the member name of an object schema: the key's symbol in its
// document and the type its value parses to.
func (r *tsResolver) zodMember(t tsTy, name string, depth int) tsMember {
	for i := len(t.params) - 1; i >= 0; i-- {
		lm, obj := t.params[i].m, t.params[i].node
		spread := false
		for j := int(obj.NamedChildCount()) - 1; j >= 0; j-- {
			c := obj.NamedChild(j)
			var key, val *sitter.Node
			switch c.Type() {
			case "pair":
				key, val = c.ChildByFieldName("key"), c.ChildByFieldName("value")
				if key == nil || key.Type() != "property_identifier" && key.Type() != "string" {
					spread = true // a computed key: may be name
					continue
				}
				if tsStringValue(key.Content(lm.src)) != name {
					continue
				}
			case "shorthand_property_identifier":
				if c.Content(lm.src) != name {
					continue
				}
				key, val = c, c
			case "spread_element":
				spread = true
				continue
			default:
				continue
			}
			sym := r.keySymAt(lm, key)
			if sym == "" {
				return tsMember{k: tyUnknown}
			}
			return tsMember{sym: sym, typ: r.zodOut(lm, val, depth+1), k: tyDecl}
		}
		if spread {
			return tsMember{k: tyUnknown}
		}
	}
	return tsMember{k: tyUnknown}
}

// keySymAt is the property symbol scip-typescript has at an object
// literal's key in m's document: its definition, else (an extend's keys
// are references to a symbol nothing defines) a reference to a property.
func (r *tsResolver) keySymAt(m *tsModule, key *sitter.Node) string {
	if m.keyPos == nil {
		m.keyPos = map[[2]int32]string{}
		if d := r.docs(m.file); d != nil {
			for _, o := range d.Occurrences {
				if strings.HasPrefix(o.Symbol, "local ") || len(o.Range) < 2 {
					continue
				}
				k := [2]int32{o.Range[0], o.Range[1]}
				switch {
				case o.SymbolRoles&scip.SymbolRoleDefinition != 0:
					m.keyPos[k] = o.Symbol
				case strings.HasSuffix(o.Symbol, ":") && m.keyPos[k] == "":
					m.keyPos[k] = o.Symbol
				}
			}
		}
		if m.cols == nil {
			m.cols = newUTF16Cols(m.src)
		}
	}
	l, c := m.cols.pos(key.StartByte())
	if s := m.keyPos[[2]int32{l, c}]; strings.HasSuffix(s, ":") {
		return s
	}
	return ""
}

// zodIdentity reports whether f names a function of the repository whose
// type keeps its argument's: f<P>(p: P): P.
func (r *tsResolver) zodIdentity(m *tsModule, f *sitter.Node) bool {
	if f.Type() != "identifier" || tsBoundLocally(f, f.Content(m.src), m.src) {
		return false
	}
	res, ok := r.resolveIn(m.file, f.Content(m.src), tsValue)
	if !ok || res.external || res.namespace != "" || res.sym == "" {
		return false
	}
	dm := r.module(r.byPrefix[tsSymPrefix(res.sym)])
	if dm == nil {
		return false
	}
	d := dm.nodes[strings.TrimPrefix(res.sym, dm.prefix)]
	if d == nil || d.Type() != "function_declaration" || dm.overloaded[strings.TrimPrefix(res.sym, dm.prefix)] {
		return false
	}
	tps, ps, rt := d.ChildByFieldName("type_parameters"), d.ChildByFieldName("parameters"), d.ChildByFieldName("return_type")
	if tps == nil || ps == nil || rt == nil || ps.NamedChildCount() != 1 || rt.NamedChildCount() != 1 {
		return false
	}
	p := ps.NamedChild(0)
	pt := p.ChildByFieldName("type")
	if p.Type() != "required_parameter" || pt == nil || pt.NamedChildCount() != 1 {
		return false
	}
	a, b := pt.NamedChild(0), rt.NamedChild(0)
	if a.Type() != "type_identifier" || b.Type() != "type_identifier" || a.Content(dm.src) != b.Content(dm.src) {
		return false
	}
	for i := 0; i < int(tps.NamedChildCount()); i++ {
		if n := tps.NamedChild(i).ChildByFieldName("name"); n != nil && n.Content(dm.src) == a.Content(dm.src) {
			return true
		}
	}
	return false
}
