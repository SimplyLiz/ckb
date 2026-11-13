package query

import (
	"context"
	"os"
	"path/filepath"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
)

// A Python class is called to construct it: `Foo(x)` runs Foo.__init__, and so does
// `Bar(x)` for a `class Bar(Foo)` that defines no __init__ of its own. scip-python
// records both as uses of the class, never of __init__, so __init__'s references miss
// every construction.

// pyClassIndex is what following a class to its subclasses needs from the index.
type pyClassIndex interface {
	Implementations(symbolID string) []string
	DefinitionLocation(symbolID string) *backends.Location
}

// pythonInitClasses are the classes whose construction runs the __init__ initID: its
// own class, then every subclass, at any depth, that inherits it by defining none.
// Nil for any other symbol. Without an index to follow, only the own class.
//
// Only the index's base-class relationships are read, not the MRO: a subclass that
// lists another base with its own __init__ before this class still counts.
func pythonInitClasses(index pyClassIndex, initID string) []string {
	class := diff.PythonInitClass(initID)
	if class == "" {
		return nil
	}
	classes := []string{class}
	if index == nil {
		return classes
	}
	seen := map[string]bool{class: true}
	for i := 0; i < len(classes); i++ {
		for _, sub := range index.Implementations(classes[i]) {
			if seen[sub] || diff.PythonInitClass(sub+"__init__().") == "" {
				continue // Seen, or no class ID of scip-python's
			}
			seen[sub] = true
			if index.DefinitionLocation(sub+"__init__().") != nil {
				continue // Defines its own: a construction runs that one
			}
			classes = append(classes, sub)
		}
	}
	return classes
}

// pythonConstructions are the calls that construct one of pythonInitClasses(initID),
// each a use of the __init__. False if the references of one couldn't be looked up.
func pythonConstructions(ctx context.Context, adapter referenceFinder, repoRoot, initID string) ([]backends.Reference, bool) {
	index, _ := adapter.(pyClassIndex)
	classes := pythonInitClasses(index, initID)
	if len(classes) == 0 || adapter == nil {
		return nil, adapter != nil
	}
	ok := true
	files := make(map[string][]string)
	var calls []backends.Reference
	for _, class := range classes {
		name := scip.SymbolSimpleName(class)
		refs, err := adapter.FindReferences(ctx, class, backends.RefOptions{
			MaxResults:   maxImpactReferences,
			IncludeTests: true,
		})
		if err != nil || refs == nil {
			ok = false
			continue
		}
		for _, ref := range refs.References {
			if ref.Kind == "import" || ref.Kind == "definition" || ref.Location.Path == "" {
				continue
			}
			lines, read := files[ref.Location.Path]
			if !read {
				if content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(ref.Location.Path))); err == nil {
					lines = splitFileLines(string(content))
				}
				files[ref.Location.Path] = lines
			}
			// A base class list, isinstance or an annotation names the class without
			// constructing it. A line that can't be read counts.
			if diff.PythonIsCall(lines, ref.Location.Line, name) {
				calls = append(calls, ref)
			}
		}
	}
	return calls, ok
}
