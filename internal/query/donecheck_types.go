package query

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A field added to a Go struct or a TypeScript interface reaches no symbol the diff
// mapper can name: the indexers give a type no enclosing range, so the type reads as
// unchanged and the check says clear. This is the case of an editor's type growing a
// field while the importer that builds the type goes untouched. The Rust check already
// lists a struct's literals when its fields changed; this does the same for Go and
// TypeScript, from the text of each changed file.

// checkTypeFiles looks at what the diff did to the members of each type declared in a
// changed Go or TypeScript file, and lists the places that build the type: Go composite
// literals, and TypeScript object literals typed as it or returned as it. Each site has
// `via` saying why. Only files on both sides of the diff are read: a new type has no
// uses yet, a deleted one is covered by its removal.
func (c *doneChecker) checkTypeFiles(ctx context.Context, adapter referenceFinder) []rustOpen {
	index, _ := adapter.(rustIndex)
	if index == nil {
		return nil
	}
	var open []rustOpen
	for _, file := range c.orderedFiles() {
		path := file.OldPath
		lang := diff.TypeLanguage(path)
		if lang == "" || path != file.NewPath || file.Deleted || file.IsNew {
			continue
		}
		oldLines := c.readOld(file, path)
		newLines, ok := c.readNew(file, path)
		if oldLines == nil || !ok {
			continue
		}
		for _, name := range diff.DeclaredTypes(oldLines, lang) {
			before, okBefore := diff.TypeFields(oldLines, lang, name)
			after, okAfter := diff.TypeFields(newLines, lang, name)
			if !okBefore || !okAfter {
				continue
			}
			if first, last, ok := diff.TypeSpan(newLines, lang, name); ok {
				c.compared = append(c.compared, comparedSpan{path: path, first: first, last: last})
			}
			if slices.Equal(before, after) {
				continue
			}
			id := index.TypeSymbolIn(path, name)
			if id == "" {
				continue
			}
			query := useQuery{
				name:    name,
				change:  impact.DeclChanged,
				via:     fmt.Sprintf("fields of %s changed", name),
				literal: true,
				lang:    lang,
			}
			if lang == "go" {
				// The compiler's rule: what breaks is a positional literal, and a keyed
				// one setting a field that went away or changed type.
				oldTypes, okOld := diff.GoStructFieldTypes(oldLines, name)
				newTypes, okNew := diff.GoStructFieldTypes(newLines, name)
				if okOld && okNew {
					query.goBroken, _ = diff.GoBrokenFields(oldTypes, newTypes)
				}
			}
			sym := impact.ChangedSymbol{SymbolID: id, Name: name, File: path}
			files := make(map[string]bool)
			c.checkUses(ctx, adapter, sym, query, files)
			if len(files) > 0 {
				open = append(open, rustOpen{name: name, change: impact.DeclChanged, files: files})
			}
		}
	}
	return open
}

// comparedSpan is a type body in a changed file whose members were compared on both
// sides, so a changed line in it has been read even though no symbol names it.
type comparedSpan struct {
	path        string
	first, last int
}

// explains says whether a file-level entry's changed lines all sit in type bodies this
// check compared. The mapper has no definition for such a line, and without this it
// counts as a declaration that couldn't be compared.
func (c *doneChecker) explains(sym impact.ChangedSymbol) bool {
	if !strings.HasPrefix(sym.SymbolID, "file:") || len(sym.Lines) == 0 {
		return false
	}
	for _, line := range sym.Lines {
		inside := false
		for _, span := range c.compared {
			if span.path == sym.File && span.first <= line && line <= span.last {
				inside = true
				break
			}
		}
		if !inside {
			return false
		}
	}
	return true
}

// orderedFiles returns the diff's files once each, in path order, so the sites come
// out the same way every run.
func (c *doneChecker) orderedFiles() []*impact.ChangedFile {
	seen := make(map[*impact.ChangedFile]bool, len(c.files))
	var files []*impact.ChangedFile
	for _, file := range c.files {
		if !seen[file] {
			seen[file] = true
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b *impact.ChangedFile) int {
		if a.OldPath != b.OldPath {
			if a.OldPath < b.OldPath {
				return -1
			}
			return 1
		}
		return 0
	})
	return files
}
