package query

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A TypeScript type can take its shape from a value: `type Profile = z.infer<typeof
// profileSchema>`. A key added to the schema changes Profile, and no line of Profile's
// declaration moved, so its literals went unlisted and the check said clear. The same
// holds for `ReturnType<typeof fn>`, `(typeof KEYS)[number]` and a drizzle table's
// `typeof users.$inferSelect`: this follows `typeof`, whatever wraps it.

// maxDerivedDepth bounds how many values a change is followed through: a schema, the
// schema that holds it, and so on.
const maxDerivedDepth = 4

// maxDerivedValues bounds the values looked at for one diff.
const maxDerivedValues = 200

// derivedIndex is what the check needs beyond references.
type derivedIndex interface {
	TypeSymbolIn(path, name string) string
	ValueSymbolIn(path, name string) string
}

// derivedValue is a value whose shape changed: one the diff edited, or one whose
// initializer holds such a value.
type derivedValue struct {
	sym    impact.ChangedSymbol
	direct bool              // The diff edited its initializer
	keys   map[string]string // Key of this value to the changed value it holds there
	inner  string            // A changed value it holds, where no key says which
	depth  int
	types  []derivedType
}

// derivedType is a type alias or interface that reads `typeof` a derivedValue.
type derivedType struct {
	id, name, path string
}

// checkDerivedTypes lists the literals of each type read off a changed TypeScript
// value with `typeof`. A literal of a type that holds the changed one under a key is
// listed at that key.
func (c *doneChecker) checkDerivedTypes(ctx context.Context, adapter referenceFinder, changed []impact.ChangedSymbol) []rustOpen {
	index, _ := adapter.(derivedIndex)
	if index == nil {
		return nil
	}
	listed := make(map[string]bool, len(changed)) // Symbols whose own uses are listed already
	for _, sym := range changed {
		if d := symbolDeclarationChange(sym); d == impact.DeclChanged || d == impact.DeclRemoved {
			listed[sym.SymbolID] = true
		}
	}
	values := make(map[string]*derivedValue)
	var order []*derivedValue
	for _, sym := range changed {
		if sym.Trivial || sym.ChangeType == impact.ChangeAdded || sym.ChangeType == impact.ChangeDeleted {
			continue
		}
		if diff.TypeLanguage(sym.File) != "typescript" || !isTSValueSymbol(sym.SymbolID) || values[sym.SymbolID] != nil {
			continue
		}
		values[sym.SymbolID] = &derivedValue{sym: sym, direct: true}
		order = append(order, values[sym.SymbolID])
	}

	// First every value the change reaches, then their types: a value found late can
	// add a key to one found early.
	for at := 0; at < len(order) && len(order) <= maxDerivedValues; at++ {
		value := order[at]
		refs, err := adapter.FindReferences(ctx, value.sym.SymbolID, backends.RefOptions{MaxResults: maxImpactReferences, IncludeTests: true})
		if err != nil || refs == nil {
			c.failed++
			continue
		}
		name := leafName(value.sym.Name)
		seenTypes := make(map[string]bool)
		for i := range refs.References {
			ref := &refs.References[i]
			path := ref.Location.Path
			lines := c.indexSide(path)
			if lines == nil {
				continue
			}
			if alias, ok := diff.TSTypeofAlias(lines, ref.Location.Line, ref.Location.Column, name); ok {
				if id := index.TypeSymbolIn(path, alias); id != "" && !listed[id] && !seenTypes[id] {
					seenTypes[id] = true
					value.types = append(value.types, derivedType{id: id, name: alias, path: path})
				}
				continue
			}
			holderName, key, ok := diff.TSEnclosingValue(lines, ref.Location.Line)
			if !ok || holderName == name {
				continue
			}
			id := index.ValueSymbolIn(path, holderName)
			if id == "" {
				continue
			}
			holder := values[id]
			if holder == nil {
				if value.depth+1 >= maxDerivedDepth {
					continue
				}
				holder = &derivedValue{sym: impact.ChangedSymbol{SymbolID: id, Name: holderName, File: path}, depth: value.depth + 1}
				values[id] = holder
				order = append(order, holder)
			}
			switch {
			case key == "":
				holder.inner = cmp.Or(holder.inner, name)
			case holder.keys == nil:
				holder.keys = map[string]string{key: name}
			case holder.keys[key] == "":
				holder.keys[key] = name
			}
		}
	}

	var open []rustOpen
	for _, value := range order {
		name := leafName(value.sym.Name)
		for _, t := range value.types {
			query := useQuery{
				name:    t.name,
				change:  impact.DeclChanged,
				literal: true,
				lang:    "typescript",
				derived: true,
			}
			switch {
			case value.direct:
				query.via = fmt.Sprintf("%s is typeof %s, whose value changed", t.name, name)
			case value.inner != "":
				query.via = fmt.Sprintf("%s is typeof %s, which holds %s, whose value changed", t.name, name, value.inner)
			}
			for key, inner := range value.keys {
				if query.keyVia == nil {
					query.keyVia = make(map[string]string, len(value.keys))
				}
				query.keyVia[key] = fmt.Sprintf("%s is typeof %s, whose %s holds %s, whose value changed", t.name, name, key, inner)
				if query.via == "" {
					query.via = query.keyVia[key]
				}
			}
			files := make(map[string]bool)
			c.checkUses(ctx, adapter, impact.ChangedSymbol{SymbolID: t.id, Name: t.name, File: t.path}, query, files)
			if len(files) > 0 {
				open = append(open, rustOpen{name: t.name, change: impact.DeclChanged, files: files})
			}
		}
	}
	return open
}

// isTSValueSymbol says whether a scip-typescript symbol names a module-level value or
// function, the two things `typeof` is written over.
func isTSValueSymbol(id string) bool {
	at := strings.LastIndex(id, "`/")
	if !strings.HasPrefix(id, "scip-typescript ") || at < 0 {
		return false
	}
	descriptor := id[at+2:]
	descriptor = strings.TrimSuffix(strings.TrimSuffix(descriptor, "."), "()")
	return descriptor != "" && !strings.ContainsAny(descriptor, "#().:/[]`")
}

// indexSide returns a file as the index has it: the diff's old side for a changed
// file, the file itself for any other.
func (c *doneChecker) indexSide(path string) []string {
	if file := c.files[path]; file != nil {
		if old := c.readOld(file, path); old != nil {
			return old
		}
	}
	lines, ok := c.readNew(c.files[path], path)
	if !ok {
		return nil
	}
	return lines
}
