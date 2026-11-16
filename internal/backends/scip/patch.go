package scip

import (
	"maps"
	"sort"
	"time"
)

// PatchIndex returns a new index that is base with some documents swapped:
// replace maps a document path to its new document, or to nil when the
// document is gone. base is not modified, and every unchanged document,
// occurrence and symbol is shared between the two, so the patched index costs
// little more than the replaced documents.
//
// The derived structures are brought to the state a full load of the patched
// documents would produce (same ordering rules: documents by path, the first
// definition in path order, the last SymbolInformation in path order), but only
// the entries that the replaced documents touch are rebuilt. CallerIndex and
// the versionless index are lazy, as after a load.
//
// Dart part files are not relinked (linkDartParts runs at load only): a
// replaced Dart document keeps whatever local IDs its replacement carries.
func PatchIndex(base *SCIPIndex, replace map[string]*Document) *SCIPIndex {
	if base == nil {
		return nil
	}

	// Old documents leaving the index, and new ones entering it.
	oldDocs := make(map[*Document]bool, len(replace))
	var newDocs []*Document
	for path, doc := range replace {
		if old := base.DocumentsByPath[path]; old != nil {
			oldDocs[old] = true
		}
		if doc != nil {
			doc.RelativePath = path
			newDocs = append(newDocs, doc)
		}
	}
	sort.Slice(newDocs, func(i, j int) bool { return newDocs[i].RelativePath < newDocs[j].RelativePath })

	idx := &SCIPIndex{
		Metadata:        base.Metadata,
		DocumentsByPath: cloneOrMake(base.DocumentsByPath),
		LoadedAt:        time.Now(),
		IndexedCommit:   base.IndexedCommit,
	}

	// Documents, sorted by path as the loader leaves them.
	docs := make([]*Document, 0, len(base.Documents)+len(newDocs))
	for _, d := range base.Documents {
		if !oldDocs[d] {
			docs = append(docs, d)
		}
	}
	docs = append(docs, newDocs...)
	sort.SliceStable(docs, func(i, j int) bool { return docs[i].RelativePath < docs[j].RelativePath })
	idx.Documents = docs
	for path := range replace {
		delete(idx.DocumentsByPath, path)
	}
	for _, d := range newDocs {
		idx.DocumentsByPath[d.RelativePath] = d
	}

	// Every symbol a leaving or entering document mentions has entries to
	// redo; declared ones also need their SymbolInformation and conversion
	// redone.
	touched := map[string]bool{}
	declared := map[string]bool{}
	note := func(d *Document) {
		for _, s := range d.Symbols {
			touched[s.Symbol] = true
			declared[s.Symbol] = true
		}
		for _, o := range d.Occurrences {
			if o.Symbol == "" {
				continue
			}
			touched[o.Symbol] = true
			if o.SymbolRoles&SymbolRoleDefinition != 0 {
				declared[o.Symbol] = true
			}
		}
	}
	for d := range oldDocs {
		note(d)
	}
	newResults := make(map[*Document]docResult, len(newDocs))
	for _, d := range newDocs {
		note(d)
		newResults[d] = indexDocument(d)
	}

	// RefIndex and DefinitionIndex.
	idx.RefIndex = cloneOrMake(base.RefIndex)
	idx.DefinitionIndex = cloneOrMake(base.DefinitionIndex)
	for sym := range touched {
		var refs []*OccurrenceRef
		for _, r := range base.RefIndex[sym] {
			if !oldDocs[r.Doc] {
				refs = append(refs, r)
			}
		}
		for _, d := range newDocs {
			refs = append(refs, newResults[d].refEntries[sym]...)
		}
		// Grouped by document in path order, occurrence order within one.
		sort.SliceStable(refs, func(i, j int) bool { return refs[i].Doc.RelativePath < refs[j].Doc.RelativePath })
		if len(refs) == 0 {
			delete(idx.RefIndex, sym)
		} else {
			idx.RefIndex[sym] = refs
		}
		delete(idx.DefinitionIndex, sym)
		for _, r := range refs {
			if r.Occ.SymbolRoles&SymbolRoleDefinition != 0 {
				idx.DefinitionIndex[sym] = r
				break
			}
		}
	}

	// ContainerIndex: keys carry the document path.
	idx.ContainerIndex = cloneOrMake(base.ContainerIndex)
	for d := range oldDocs {
		for k := range containerEntries(d) {
			delete(idx.ContainerIndex, k)
		}
	}
	for _, d := range newDocs {
		for k, v := range newResults[d].containerEntries {
			idx.ContainerIndex[k] = v
		}
	}

	// Symbols: the last document in path order that lists one wins.
	idx.Symbols = cloneOrMake(base.Symbols)
	for sym := range declared {
		delete(idx.Symbols, sym)
	}
	if len(declared) > 0 {
		for _, d := range docs {
			for _, s := range d.Symbols {
				if declared[s.Symbol] {
					idx.Symbols[s.Symbol] = s
				}
			}
		}
	}

	// ConvertedSymbols and NameIndex.
	idx.ConvertedSymbols = cloneOrMake(base.ConvertedSymbols)
	nameIdx := make([]NameEntry, 0, len(base.NameIndex)+len(declared))
	for _, e := range base.NameIndex {
		if !declared[e.ID] {
			nameIdx = append(nameIdx, e)
		}
	}
	for sym := range declared {
		delete(idx.ConvertedSymbols, sym)
		info := idx.Symbols[sym]
		if info == nil {
			continue
		}
		conv, err := convertToSCIPSymbol(info, idx)
		if err != nil {
			continue
		}
		idx.ConvertedSymbols[sym] = conv
		alias := ""
		if conv.ContainerName != "" {
			alias = conv.ContainerName + "." + conv.Name
		}
		nameIdx = append(nameIdx, NameEntry{Name: conv.Name, Alias: alias, ID: sym})
	}
	sort.Slice(nameIdx, func(a, b int) bool {
		if nameIdx[a].Name != nameIdx[b].Name {
			return nameIdx[a].Name < nameIdx[b].Name
		}
		return nameIdx[a].ID < nameIdx[b].ID
	})
	idx.NameIndex = nameIdx

	return idx
}

// cloneOrMake copies m, or makes an empty map when m is nil (an index that
// was never loaded, as in tests).
func cloneOrMake[K comparable, V any](m map[K]V) map[K]V {
	if m == nil {
		return map[K]V{}
	}
	return maps.Clone(m)
}
