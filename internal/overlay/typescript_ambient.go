//go:build cgo

package overlay

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// tsAmbient is one declare module "m" { … } block of a file: its namespace
// descriptor, its declarations by name and what it exports beyond them.
type tsAmbient struct {
	ns   string // `"m"`/
	tops map[string][]tsTop
	tsExports
}

// tsAmbientModule is the name of declare module "m" { … } and its body.
func tsAmbientModule(st *sitter.Node, src []byte) (string, *sitter.Node) {
	if st.Type() != "ambient_declaration" || st.NamedChildCount() != 1 {
		return "", nil
	}
	mod := st.NamedChild(0)
	if mod.Type() != "module" {
		return "", nil
	}
	name, body := mod.ChildByFieldName("name"), mod.ChildByFieldName("body")
	if name == nil || name.Type() != "string" || body == nil {
		return "", nil
	}
	return name.Content(src), body
}

// tsReadAmbient reads a file's top-level declare module "m" blocks.
func tsReadAmbient(root *sitter.Node, src []byte, decls []tsDecl) map[string]*tsAmbient {
	var out map[string]*tsAmbient
	for i := 0; i < int(root.NamedChildCount()); i++ {
		st := root.NamedChild(i)
		text, body := tsAmbientModule(st, src)
		if body == nil {
			continue
		}
		spec := tsStringValue(text)
		if out == nil {
			out = map[string]*tsAmbient{}
		}
		if out[spec] != nil {
			out[spec] = &tsAmbient{} // two blocks of one module: left out
			continue
		}
		a := &tsAmbient{ns: tsNamespace(text)}
		var own []tsDecl
		for _, d := range decls {
			if rest, ok := strings.CutPrefix(d.suffix, a.ns); ok && rest != "" {
				d.suffix = rest
				own = append(own, d)
			}
		}
		a.tops = tsTopDecls(own)
		for n, tops := range a.tops {
			for j := range tops {
				tops[j].suffix = a.ns + tops[j].suffix
			}
			a.tops[n] = tops
		}
		tsReadExports(body, src, &a.tsExports)
		out[spec] = a
	}
	return out
}

// ambientFile is the file whose declare module "spec" block declares the
// module, when exactly one does.
func (r *tsResolver) ambientFile(spec string) (string, *tsAmbient) {
	if r.paths == nil {
		return "", nil
	}
	if r.ambients == nil {
		r.ambients = map[string][]string{}
		for _, p := range r.paths() {
			d := r.docs(p)
			prefix := tsFileSymbol(d)
			if prefix == "" {
				continue
			}
			for _, si := range d.Symbols {
				rest, ok := strings.CutPrefix(si.Symbol, prefix+"`\"")
				if !ok || !strings.HasSuffix(rest, "\"`/") {
					continue
				}
				inner := strings.TrimSuffix(rest, "\"`/")
				if strings.Contains(inner, "`") {
					continue
				}
				r.ambients[inner] = append(r.ambients[inner], p)
			}
		}
	}
	files := r.ambients[spec]
	if len(files) != 1 {
		return "", nil
	}
	m := r.module(files[0])
	if m == nil || m.ambient[spec] == nil || m.ambient[spec].ns == "" {
		return "", nil
	}
	return files[0], m.ambient[spec]
}

// ambientLookup is name of the declare module "spec" block in namespace ns
// ("default" for its default export); ok is false when no single block
// declares the module.
func (r *tsResolver) ambientLookup(spec, name string, ns tsNS) (sym, file string, ok bool) {
	file, a := r.ambientFile(spec)
	if a == nil {
		return "", "", false
	}
	if name == "default" {
		if a.defaultName == "" {
			return "", file, true
		}
		name = a.defaultName
	}
	m := r.module(file)
	return pickTop(m.prefix, a.tops[name], ns), file, true
}

// ambientIn resolves a name used inside one of file's declare module
// blocks: the block's declaration of it, when exactly one block has one.
func (m *tsModule) ambientIn(name string, ns tsNS) string {
	sym := ""
	for _, a := range m.ambient {
		if tops := a.tops[name]; len(tops) > 0 {
			if sym != "" {
				return ""
			}
			sym = pickTop(m.prefix, tops, ns)
		}
	}
	return sym
}

// ambientImport handles import … from "m" of a package the repository
// declares in a declare module "m" block: scip-typescript writes the block's
// namespace at the string and its declarations at the specifiers.
func (l *tsRefs) ambientImport(n, srcNode *sitter.Node) {
	spec := tsStringValue(l.text(srcNode))
	file, a := l.r.ambientFile(spec)
	if a == nil {
		return
	}
	m := l.r.module(file)
	if l.onAdded(srcNode) {
		l.emit(srcNode, m.prefix+a.ns)
	}
	if n.Type() != "import_statement" {
		return
	}
	var specs func(c *sitter.Node)
	specs = func(c *sitter.Node) {
		var name *sitter.Node
		imported := ""
		switch c.Type() {
		case "import_specifier":
			if c.ChildByFieldName("alias") != nil {
				return
			}
			name = c.ChildByFieldName("name")
			if name != nil {
				imported = l.text(name)
			}
		case "identifier":
			if p := c.Parent(); p != nil && p.Type() == "import_clause" {
				name, imported = c, "default"
			}
		}
		if name != nil {
			if !l.onAdded(name) {
				return
			}
			v, _, _ := l.r.ambientLookup(spec, imported, tsValue)
			t, _, _ := l.r.ambientLookup(spec, imported, tsTypeNS)
			switch {
			case v != "" && (t == "" || t == v):
				l.emit(name, v)
			case t != "" && v == "":
				l.emit(name, t)
			case v != "" && t != "":
				l.unresolved++
			}
			return
		}
		for i := 0; i < int(c.NamedChildCount()); i++ {
			specs(c.NamedChild(i))
		}
	}
	specs(n)
}
