package query

import (
	"context"
	"fmt"
	"sort"
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
	KeySymbolAt(path string, line int, name string) string
}

// derivedValue is a value whose shape changed: one the diff edited, or one whose
// initializer holds such a value.
type derivedValue struct {
	sym    impact.ChangedSymbol
	direct bool                     // The diff edited its initializer
	keys   map[string]*derivedValue // Key of this value to the changed value it holds there
	inner  *derivedValue            // A changed value it holds, where no key says which
	depth  int
	types  []derivedType
	// zod is what the change did to the value as a zod schema, read from the text on
	// both sides. Nil if it isn't one, or its file isn't in the diff.
	zod *diff.ZodChange
}

// derivedType is a type alias or interface that reads `typeof` a derivedValue.
type derivedType struct {
	id, name, path string
}

// derivedPath leads from a literal to the one that has to change: through the keys
// that hold it, to the value at the end.
type derivedPath struct {
	keys []string
	root *derivedValue
}

func (v *derivedValue) name() string { return leafName(v.sym.Name) }

// read says whether the schema was read to the end on both sides.
func (v *derivedValue) read() bool { return v.zod != nil && v.zod.Exact }

// matters says whether the change can have changed the type: it wasn't read, or it
// was and something other than a validator moved.
func (v *derivedValue) matters() bool { return !v.read() || !v.zod.Empty() }

// paths returns every way from a literal of this value's type down to a value the
// diff edited, by the keys that hold it.
func (v *derivedValue) paths(depth int) []derivedPath {
	if depth >= maxDerivedDepth {
		return nil
	}
	var out []derivedPath
	for _, key := range sortedValueKeys(v.keys) {
		held := v.keys[key]
		if held.direct {
			out = append(out, derivedPath{keys: []string{key}, root: held})
		}
		for _, p := range held.paths(depth + 1) {
			out = append(out, derivedPath{keys: append([]string{key}, p.keys...), root: p.root})
		}
	}
	return out
}

func sortedValueKeys(m map[string]*derivedValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// checkDerivedTypes lists what a changed TypeScript value breaks through the types read
// off it with `typeof`. For a zod schema the text says what changed: a key the output
// type now requires opens the literals that lack it, a removed or retyped key opens the
// places that name it, and an optional key or a validator opens nothing. Where the
// schema can't be read, and for any other value, every literal of the type is listed.
// A literal of a type that holds the changed one under a key is listed at that key.
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
		value.zod = c.zodChange(value)
		refs, err := adapter.FindReferences(ctx, value.sym.SymbolID, backends.RefOptions{MaxResults: maxImpactReferences, IncludeTests: true})
		if err != nil || refs == nil {
			c.failed++
			continue
		}
		name := value.name()
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
				if holder.inner == nil {
					holder.inner = value
				}
			case holder.keys == nil:
				holder.keys = map[string]*derivedValue{key: value}
			case holder.keys[key] == nil:
				holder.keys[key] = value
			}
		}
	}

	var open []rustOpen
	uses := func(t derivedType, query useQuery) {
		query.name, query.change, query.literal, query.lang, query.derived = t.name, impact.DeclChanged, true, "typescript", true
		files := make(map[string]bool)
		c.checkUses(ctx, adapter, impact.ChangedSymbol{SymbolID: t.id, Name: t.name, File: t.path}, query, files)
		if len(files) > 0 {
			open = append(open, rustOpen{name: t.name, change: impact.DeclChanged, files: files})
		}
	}
	for _, value := range order {
		name := value.name()
		if value.read() && !value.zod.Empty() {
			c.shapeContract(value)
			open = append(open, c.checkZodKeys(ctx, adapter, index, value)...)
		}
		for _, t := range value.types {
			typeOf := fmt.Sprintf("%s is typeof %s", t.name, name)
			// The value's own change.
			switch {
			case value.read():
				zod := value.zod
				switch {
				case len(zod.LiteralKeys()) > 0:
					uses(t, useQuery{
						via:     fmt.Sprintf("%s, which now requires %s", typeOf, strings.Join(zod.LiteralKeys(), ", ")),
						missing: &missingKeys{keys: zod.LiteralKeys()},
					})
				case zod.MembersRemoved():
					uses(t, useQuery{via: fmt.Sprintf("%s, which lost %s", typeOf, strings.Join(zod.Removed, ", "))})
				case zod.MembersAdded():
					uses(t, useQuery{via: fmt.Sprintf("%s, which gained %s", typeOf, strings.Join(zod.Added, ", ")), recordOnly: true})
				}
			case value.direct:
				uses(t, useQuery{via: typeOf + ", whose value changed" + unreadNote(value)})
			case value.inner != nil && value.inner.matters():
				uses(t, useQuery{via: fmt.Sprintf("%s, which holds %s, whose value changed", typeOf, value.inner.name())})
			}
			// What it holds under a key.
			for _, p := range value.paths(0) {
				at := strings.Join(p.keys, ".")
				held := fmt.Sprintf("%s, whose %s holds %s", typeOf, at, p.root.name())
				switch {
				case !p.root.read():
					last := p.keys[len(p.keys)-1]
					via := held + ", whose value changed" + unreadNote(p.root)
					uses(t, useQuery{via: via, keyVia: map[string]string{last: via}})
				case len(p.root.zod.LiteralKeys()) > 0:
					keys := p.root.zod.LiteralKeys()
					uses(t, useQuery{
						via:     fmt.Sprintf("%s, which now requires %s", held, strings.Join(keys, ", ")),
						missing: &missingKeys{path: p.keys, keys: keys},
					})
				}
			}
		}
	}
	return open
}

// unreadNote says why a zod schema's keys weren't compared, if it is one.
func unreadNote(value *derivedValue) string {
	if value.zod == nil || value.zod.Exact {
		return ""
	}
	return "; its keys weren't compared: " + value.zod.Unread
}

// zodChange reads what the diff did to a value as a zod schema. Nil if it isn't one
// on both sides, or its file isn't in the diff: then nothing in it moved, whatever it
// holds.
func (c *doneChecker) zodChange(value *derivedValue) *diff.ZodChange {
	path := value.sym.File
	file := c.files[path]
	if file == nil || file.IsNew || file.Deleted {
		return nil
	}
	oldLines := c.readOld(file, path)
	newLines, ok := c.readNew(file, path)
	if oldLines == nil || !ok {
		return nil
	}
	change, ok := diff.ZodSchemaChange(oldLines, newLines, value.name())
	if !ok {
		return nil
	}
	return &change
}

// shapeContract records what changed in a schema as a contract line, under the type
// read off it, or under the schema where no type is.
func (c *doneChecker) shapeContract(value *derivedValue) {
	detail := strings.Join(value.zod.Lines(), "; ")
	basis := fmt.Sprintf("heuristic: the keys of %s read from the text on both sides", value.name())
	if len(value.types) == 0 {
		c.shapes = append(c.shapes, ContractSignal{Symbol: value.name(), File: value.sym.File, Kind: "shape-changed", Basis: basis, Detail: detail})
	}
	for _, t := range value.types {
		c.shapes = append(c.shapes, ContractSignal{Symbol: t.name, File: t.path, Kind: "shape-changed", Basis: basis, Detail: detail})
	}
}

// checkZodKeys lists the places that name a key the schema dropped, retyped or made
// optional. The index has them: scip-typescript gives the key in `z.object({…})` a
// symbol, and a read or a literal through the inferred type refers to it.
func (c *doneChecker) checkZodKeys(ctx context.Context, adapter referenceFinder, index derivedIndex, value *derivedValue) []rustOpen {
	path := value.sym.File
	file := c.files[path]
	if file == nil {
		return nil
	}
	oldLines := c.readOld(file, path)
	var open []rustOpen
	for _, key := range value.zod.Keys {
		query := useQuery{name: key.Key, change: impact.DeclChanged}
		switch key.Kind {
		case diff.ZodRemoved:
			query.change = impact.DeclRemoved
			query.via = "the key is gone from " + value.name()
		case diff.ZodRetyped:
			query.via = fmt.Sprintf("was %s, is %s", key.Before, key.After)
			query.readsOnly = key.Wider
		case diff.ZodNowOptional:
			query.via = "now optional"
			query.readsOnly = true
		default:
			continue
		}
		id := index.KeySymbolAt(path, diff.ZodKeyLine(oldLines, value.name(), key.Key), key.Key)
		if id == "" {
			continue // Written in a base schema, which reports it itself
		}
		name := value.name() + "." + key.Key
		files := make(map[string]bool)
		c.checkUses(ctx, adapter, impact.ChangedSymbol{SymbolID: id, Name: name, File: path}, query, files)
		if len(files) > 0 {
			open = append(open, rustOpen{name: name, change: query.change, files: files})
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
