package query

import (
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
	if alt := descriptorVariant(e.scipAdapter.GetIndex(), id); alt != "" {
		msg += " (did you mean " + alt + "?)"
	}
	return msg
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
