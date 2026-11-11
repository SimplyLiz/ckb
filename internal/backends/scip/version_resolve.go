package scip

import "sort"

// versionlessKey returns id with its package-version field cut out, so that
// "scip-go gomod example.com/m 67d776584a29 `pkg`/F()." and the same symbol
// indexed at another commit map to one key. scip-go sets the version of the
// module being indexed to the HEAD commit hash, so every commit changes every
// ID of the repo's own symbols.
//
// SCIP escapes a space inside the scheme or a package field by doubling it, so
// a field ends at a single space only. ok is false for "local ..." symbols and
// for anything with fewer than five fields.
func versionlessKey(id string) (key string, ok bool) {
	var ends [4]int // index of the space that ends field i
	field, i := 0, 0
	for i < len(id) && field < 4 {
		if id[i] == ' ' {
			if i+1 < len(id) && id[i+1] == ' ' {
				i += 2
				continue
			}
			ends[field] = i
			field++
		}
		i++
	}
	if field < 4 || ends[3]+1 >= len(id) || id[:ends[0]] == "local" {
		return "", false
	}
	// scheme manager name | version | descriptor
	return id[:ends[2]] + " " + id[ends[3]+1:], true
}

// symbolKnown reports whether the index has any record of id.
func (idx *SCIPIndex) symbolKnown(id string) bool {
	if _, ok := idx.Symbols[id]; ok {
		return true
	}
	if _, ok := idx.DefinitionIndex[id]; ok {
		return true
	}
	_, ok := idx.RefIndex[id]
	return ok
}

func (idx *SCIPIndex) buildVersionlessIndex() {
	m := make(map[string][]string)
	seen := make(map[string]struct{})
	add := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		if k, ok := versionlessKey(id); ok {
			m[k] = append(m[k], id)
		}
	}
	for id := range idx.Symbols {
		add(id)
	}
	for id := range idx.DefinitionIndex {
		add(id)
	}
	for id := range idx.RefIndex {
		add(id)
	}
	for _, ids := range m {
		sort.Strings(ids)
	}
	idx.versionlessIndex = m
}

// ResolveAcrossVersions maps a SCIP ID the index doesn't know to the ID it has
// for the same symbol under another package version: same scheme, manager,
// package name and descriptor. It returns id unchanged when the index knows it,
// and the single match when there is exactly one. With no match resolved is
// "" and candidates is nil; with several, resolved is "" and candidates lists
// them, so a caller never silently picks one.
func (idx *SCIPIndex) ResolveAcrossVersions(id string) (resolved string, candidates []string) {
	if idx == nil || id == "" {
		return "", nil
	}
	if idx.symbolKnown(id) {
		return id, nil
	}
	key, ok := versionlessKey(id)
	if !ok {
		return "", nil
	}
	idx.versionlessIndexOnce.Do(idx.buildVersionlessIndex)
	matches := idx.versionlessIndex[key]
	switch len(matches) {
	case 0:
		return "", nil
	case 1:
		return matches[0], nil
	default:
		return "", append([]string(nil), matches...)
	}
}
