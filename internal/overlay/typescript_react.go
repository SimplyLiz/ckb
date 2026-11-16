//go:build cgo

package overlay

import (
	sitter "github.com/smacker/go-tree-sitter"
)

// React's hooks pass the repository's types through: what @types/react
// declares them to return, followed exactly.
//
//   - createContext<T>(d) is a context of T; useContext(c) and use(c) are T;
//   - useState<T>(x) and useState(x) are [T, setter] (T: x's type, or what a
//     lazy initializer returns);
//   - useRef<T>(x) is a ref whose current is T;
//   - useMemo(() => v) is v's type, useCallback(f) is f.
//
// Anything else imported from react falls back to the rule for package
// calls: unknown when the call passes repository types.

const (
	tsReactContext = "react:context"
	tsReactState   = "react:state"
	tsReactRef     = "react:ref"
)

// reactHook is the name of the react export fn calls ("" for none):
// useState, or React.useState for a default or namespace import.
func (r *tsResolver) reactHook(m *tsModule, fn *sitter.Node) string {
	switch fn.Type() {
	case "identifier":
		name := fn.Content(m.src)
		imp, ok := m.imports[name]
		if !ok || imp.spec != "react" || imp.name == "*" || imp.name == "default" || tsBoundLocally(fn, name, m.src) {
			return ""
		}
		return imp.name
	case "member_expression":
		obj, prop := fn.ChildByFieldName("object"), fn.ChildByFieldName("property")
		if obj == nil || prop == nil || obj.Type() != "identifier" {
			return ""
		}
		name := obj.Content(m.src)
		imp, ok := m.imports[name]
		if !ok || imp.spec != "react" || imp.name != "*" && imp.name != "default" || tsBoundLocally(obj, name, m.src) {
			return ""
		}
		return prop.Content(m.src)
	}
	return ""
}

// reactCall is the type of a call of one of React's hooks; ok is false for
// any other call.
func (r *tsResolver) reactCall(m *tsModule, call *sitter.Node, depth int) (tsTy, bool) {
	fn, args := call.ChildByFieldName("function"), call.ChildByFieldName("arguments")
	if fn == nil || args == nil {
		return tsNoTy, false
	}
	hook := r.reactHook(m, fn)
	if hook == "" {
		return tsNoTy, false
	}
	var targ *sitter.Node
	if ta := call.ChildByFieldName("type_arguments"); ta != nil {
		if ta.NamedChildCount() != 1 {
			return tsUnknownTy, true
		}
		targ = ta.NamedChild(0)
	}
	var first *sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if a := args.NamedChild(i); a.Type() != "comment" {
			first = a
			break
		}
	}
	// typeArg is T: spelled, else inferred from the first argument.
	typeArg := func(lazy bool) tsTy {
		if targ != nil {
			return r.typeOfNode(m, targ, depth+1)
		}
		if first == nil {
			return tsOutsideTy // undefined
		}
		t := r.exprType(m, first, depth+1)
		if lazy && t.k == tyFn {
			return r.returnOf(m, t, nil, depth+1)
		}
		return t
	}
	box := func(sym string, t tsTy) tsTy {
		if t.k == tyNone {
			return tsUnknownTy // an initializer whose type the syntax does not give
		}
		return tsTy{k: tyStore, sym: sym, elem: &t}
	}
	switch hook {
	case "createContext":
		return box(tsReactContext, typeArg(false)), true
	case "useContext", "use":
		if first == nil {
			return tsUnknownTy, true
		}
		if c := r.exprType(m, first, depth+1); c.k == tyStore && c.sym == tsReactContext {
			return c.of(), true
		}
		return tsUnknownTy, true
	case "useState":
		return box(tsReactState, typeArg(true)), true
	case "useRef":
		return box(tsReactRef, typeArg(false)), true
	case "useMemo":
		if targ != nil {
			return r.typeOfNode(m, targ, depth+1), true
		}
		if first == nil {
			return tsUnknownTy, true
		}
		t := r.exprType(m, first, depth+1)
		if t.k != tyFn {
			return tsUnknownTy, true
		}
		return r.returnOf(m, t, nil, depth+1), true
	case "useCallback":
		if targ != nil {
			return r.typeOfNode(m, targ, depth+1), true
		}
		if first == nil {
			return tsUnknownTy, true
		}
		return r.exprType(m, first, depth+1), true
	}
	return tsNoTy, false
}

// tsReactBox reports one of the hook results above (a tyStore with a
// react: symbol, so a zustand store's rules leave it alone).
func tsReactBox(t tsTy) bool {
	return t.k == tyStore && (t.sym == tsReactContext || t.sym == tsReactState || t.sym == tsReactRef)
}
