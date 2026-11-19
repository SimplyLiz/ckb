package overlay

import (
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// RemapStats counts what Layer 0 did to one document.
type RemapStats struct {
	Kept    int // occurrences moved to their new line (or left in place)
	Dropped int // occurrences on removed or replaced lines
	// DroppedDefs are the symbols whose definition sat on a changed line:
	// a changed signature, a rename, or a removal.
	DroppedDefs []string
}

// RemapDocument returns a copy of doc with every occurrence on an unchanged
// line moved to its new line and every occurrence touching a changed line
// dropped. Enclosing ranges keep the occurrence and stretch or shrink with
// the edits inside them. doc itself, which the base index shares, is not
// modified. SymbolInformation is kept as it is, including that of symbols
// whose definition line changed: whether such a symbol still exists is Layer
// 1's question, and its references in unchanged files are what still needs
// updating either way.
func RemapDocument(doc *scip.Document, lm *LineMap) (*scip.Document, RemapStats) {
	var st RemapStats
	out := &scip.Document{
		RelativePath: doc.RelativePath,
		Language:     doc.Language,
		Symbols:      doc.Symbols,
		Occurrences:  make([]*scip.Occurrence, 0, len(doc.Occurrences)),
	}
	for _, occ := range doc.Occurrences {
		rng, ok := remapRange(occ.Range, lm)
		if !ok {
			st.Dropped++
			if occ.SymbolRoles&scip.SymbolRoleDefinition != 0 && occ.Symbol != "" && !scip.IsLocalSymbol(occ.Symbol) {
				st.DroppedDefs = append(st.DroppedDefs, occ.Symbol)
			}
			continue
		}
		cp := *occ
		cp.Range = rng
		if len(occ.EnclosingRange) >= 3 {
			cp.EnclosingRange = remapExtent(occ.EnclosingRange, lm)
		}
		out.Occurrences = append(out.Occurrences, &cp)
		st.Kept++
	}
	return out, st
}

// remapRange maps an occurrence's own range ([line, startChar, endChar] or
// [startLine, startChar, endLine, endChar], 0-based lines). It fails when
// any line the range covers changed.
func remapRange(r []int32, lm *LineMap) ([]int32, bool) {
	if len(r) < 3 {
		return nil, false
	}
	start := int(r[0]) + 1
	end := start
	if len(r) >= 4 {
		end = int(r[2]) + 1
	}
	if !lm.Unchanged(start, end) {
		return nil, false
	}
	ns, _ := lm.Map(start)
	out := append([]int32(nil), r...)
	out[0] = int32(ns - 1) // #nosec G115 -- line numbers came from int32
	if len(r) >= 4 {
		ne, _ := lm.Map(end)
		out[2] = int32(ne - 1) // #nosec G115 -- line numbers came from int32
	}
	return out, true
}

// remapExtent maps an enclosing range, whose ends may sit on changed lines.
func remapExtent(r []int32, lm *LineMap) []int32 {
	out := append([]int32(nil), r...)
	start := int(r[0]) + 1
	ns := lm.MapStart(start)
	out[0] = int32(ns - 1) // #nosec G115 -- line numbers came from int32
	if len(r) >= 4 {
		ne := lm.MapEnd(int(r[2]) + 1)
		if ne < ns {
			ne = ns
		}
		out[2] = int32(ne - 1) // #nosec G115 -- line numbers came from int32
		if out[0] == out[2] {
			return []int32{out[0], out[1], out[3]}
		}
	}
	return out
}
