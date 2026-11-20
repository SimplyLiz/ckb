//go:build cgo

package overlay

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"

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
	// tsSetter marks a state setter (useState's, zustand's set): a
	// callback whose argument is the state elem or a function from it to
	// the state, and whose result is not the state.
	tsSetter = "setter"
)

// tsSetterOf is the setter of a state t.
func tsSetterOf(t tsTy) tsTy {
	return tsTy{k: tyCallback, sym: tsSetter, elem: &t}
}

// reactHook is the name of the react export fn calls ("" for none):
// useState, or React.useState for a default or namespace import.
func (r *tsResolver) reactHook(m *tsModule, fn *sitter.Node) string {
	if !r.packageTyped(m.file, "react") {
		// No types for react where the file sits: its hooks are any to
		// scip-typescript, and the rule for package calls applies.
		return ""
	}
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

// packageTyped reports whether a package import of spec from file has
// types. What the index was built with decides: another document of the
// file's package (the nearest directory with a package.json) that refers to
// a symbol of spec or @types/spec. Without one, the installation now:
// node_modules/@types/<spec>, or a <spec> package that ships its own
// declarations, in the file's directory or one above it. Read once per
// package root and spec.
func (r *tsResolver) packageTyped(file, spec string) bool {
	exists := func(p string) bool {
		_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(p)))
		return err == nil
	}
	pkg := ""
	for d := path.Dir(file); ; d = path.Dir(d) {
		if exists(path.Join(d, "package.json")) {
			pkg = d
			break
		}
		if d == "." || d == "/" || d == "" {
			break
		}
	}
	key := pkg + "\x00" + spec
	if v, ok := r.typedPkgs[key]; ok {
		return v
	}
	types := "@types/" + strings.ReplaceAll(strings.TrimPrefix(spec, "@"), "/", "__")
	typed := r.indexUsesPackage(pkg, file, []string{" npm " + spec + " ", " npm " + types + " "})
	for d := path.Dir(file); !typed; d = path.Dir(d) {
		nm := path.Join(d, "node_modules")
		if exists(path.Join(nm, types)) || exists(path.Join(nm, spec, "index.d.ts")) || packageJSONTypes(filepath.Join(r.root, filepath.FromSlash(path.Join(nm, spec, "package.json")))) {
			typed = true
		}
		if d == "." || d == "/" || d == "" {
			break
		}
	}
	if r.typedPkgs == nil {
		r.typedPkgs = map[string]bool{}
	}
	r.typedPkgs[key] = typed
	return typed
}

// indexUsesPackage reports a document under dir ("" or ".": the whole
// repository), other than file, with an occurrence whose symbol has one of
// the package infixes outside the package's global declarations.
func (r *tsResolver) indexUsesPackage(dir, file string, infixes []string) bool {
	if r.paths == nil || r.docs == nil {
		return false
	}
	under := dir + "/"
	for _, p := range r.paths() {
		if p == file || dir != "" && dir != "." && !strings.HasPrefix(p, under) {
			continue
		}
		d := r.docs(p)
		if d == nil {
			continue
		}
		for _, o := range d.Occurrences {
			for _, in := range infixes {
				// A global.d.ts symbol does not count: @types/react declares
				// the DOM's element interfaces there, which reach every file
				// of the program even where "react" does not resolve.
				if strings.Contains(o.Symbol, in) && !strings.Contains(o.Symbol, "`global.d.ts`") {
					return true
				}
			}
		}
	}
	return false
}

// packageJSONTypes reports a package.json that names its declarations.
func packageJSONTypes(p string) bool {
	data, err := os.ReadFile(p) // #nosec G304 -- a package.json under the repository
	if err != nil {
		return false
	}
	var pj struct {
		Types   string `json:"types"`
		Typings string `json:"typings"`
	}
	return json.Unmarshal(data, &pj) == nil && (pj.Types != "" || pj.Typings != "")
}
