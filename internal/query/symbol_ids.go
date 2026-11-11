package query

import (
	"fmt"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// notFoundMessage says a symbol ID resolved to nothing and, when the same ID with
// another SCIP descriptor suffix exists, which one: a type or type alias ends in
// "#", a value in ".", a function in "().", and an ID copied by hand often has
// the wrong one.
func (e *Engine) notFoundMessage(id string) string {
	msg := "Symbol not found: " + id
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return msg
	}
	idx := e.scipAdapter.GetIndex()
	if alt := descriptorVariant(idx, id); alt != "" {
		msg += " (did you mean " + alt + "?)"
	} else if _, cands := idx.ResolveAcrossVersions(id); len(cands) > 0 {
		msg += fmt.Sprintf(" (%d symbols match it under another package version, so none was picked: %s)",
			len(cands), strings.Join(cands, ", "))
	}
	return msg
}

// ResolveSymbolIDAcrossVersions maps a SCIP ID from an older index to the ID
// the loaded index has for the same symbol. scip-go puts the HEAD commit hash
// in the package-version field, so an ID an agent kept from before the last
// commit + reindex no longer matches exactly. It returns id itself when the
// index knows it or nothing better is found, and the one ID that differs only
// in the version field otherwise; remapped says whether it did the latter.
// Several candidates count as no match.
func (e *Engine) ResolveSymbolIDAcrossVersions(id string) (resolved string, remapped bool) {
	if e == nil || e.scipAdapter == nil || !e.scipAdapter.IsAvailable() || !strings.Contains(id, " ") {
		return id, false
	}
	got, _ := e.scipAdapter.GetIndex().ResolveAcrossVersions(id)
	if got == "" || got == id {
		return id, false
	}
	return got, true
}

// descriptorVariant returns the ID with another descriptor suffix that the index
// defines, or "".
func descriptorVariant(idx *scip.SCIPIndex, id string) string {
	if idx == nil || !strings.Contains(id, " ") {
		return ""
	}
	base := id
	for _, suf := range []string{"().", "#", "."} {
		if strings.HasSuffix(base, suf) {
			base = strings.TrimSuffix(base, suf)
			break
		}
	}
	for _, suf := range []string{"#", ".", "()."} {
		cand := base + suf
		if cand == id {
			continue
		}
		if _, ok := idx.DefinitionIndex[cand]; ok {
			return cand
		}
		if _, ok := idx.Symbols[cand]; ok {
			return cand
		}
	}
	return ""
}
