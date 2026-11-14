package query

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

// Co-change from Cartographer is used for one thing only: marking a pair CKB's
// own coupling analysis found as "hidden" (no import between the two files).
// Measured on four repos (2026-11-14), Cartographer's pairs with two co-changes
// scored 1.0 and pushed out strong pairs, and "no import link" was wrong in most
// samples — Go files of one package, TypeScript @/ aliases and inline Rust paths
// are links its import graph does not see. So a pair counts as hidden only when
//   - it co-changed at least minHiddenCoChanges times,
//   - its score passes the caller's correlation floor,
//   - Cartographer finds no import edge, and
//   - CKB finds no link either: not the same Go package, and (with a SCIP
//     index) neither file references a symbol the other defines.

// minHiddenCoChanges is the least number of shared commits for hidden coupling.
// Two is what any pair of files touched by two refactors has.
const minHiddenCoChanges = 3

// hiddenCouplingTTL bounds how long one Cartographer hidden-coupling result is
// reused for the same HEAD. Each call rebuilds the import graph and walks git
// history, which cost tens of seconds on CKB itself.
const hiddenCouplingTTL = 10 * time.Minute

type hiddenCouplingEntry struct {
	head  string
	at    time.Time
	pairs []cartographer.CoChangePair
}

var hiddenCouplingCache = struct {
	sync.Mutex
	byRepo map[string]hiddenCouplingEntry
}{byRepo: map[string]hiddenCouplingEntry{}}

// cartographerHiddenPairs returns Cartographer's hidden-coupling pairs with at
// least minHiddenCoChanges co-changes, cached per repo and HEAD. Nil when
// Cartographer is not compiled in or fails.
func (e *Engine) cartographerHiddenPairs() []cartographer.CoChangePair {
	if !cartographer.Available() {
		return nil
	}
	head := ""
	if e.gitAdapter != nil {
		head, _ = e.gitAdapter.GetHeadCommit()
	}
	hiddenCouplingCache.Lock()
	defer hiddenCouplingCache.Unlock()
	if c, ok := hiddenCouplingCache.byRepo[e.repoRoot]; ok && head != "" && c.head == head && time.Since(c.at) < hiddenCouplingTTL {
		return c.pairs
	}
	pairs, err := cartographer.HiddenCoupling(e.repoRoot, 0, minHiddenCoChanges)
	if err != nil {
		return nil
	}
	kept := pairs[:0]
	for _, p := range pairs {
		// The lib's minCount has been a floor of 2 regardless; enforce ours.
		if p.Count >= minHiddenCoChanges && p.FileA != "" && p.FileB != "" {
			kept = append(kept, p)
		}
	}
	hiddenCouplingCache.byRepo[e.repoRoot] = hiddenCouplingEntry{head: head, at: time.Now(), pairs: kept}
	return kept
}

// hiddenCoupling is a filtered view of Cartographer's hidden pairs.
type hiddenCoupling struct {
	e      *Engine
	idx    *scip.SCIPIndex
	scores map[string]cartographer.CoChangePair // "a\x00b" and "b\x00a"
}

// newHiddenCoupling loads Cartographer's hidden pairs whose score is at least
// minCorrelation. Returns nil when there are none, so callers can skip work.
func (e *Engine) newHiddenCoupling(minCorrelation float64) *hiddenCoupling {
	pairs := e.cartographerHiddenPairs()
	if len(pairs) == 0 {
		return nil
	}
	h := &hiddenCoupling{e: e, scores: make(map[string]cartographer.CoChangePair, len(pairs)*2)}
	if e.hasConsumerGraph() {
		h.idx = e.scipAdapter.GetIndex()
	}
	for _, p := range pairs {
		if p.CouplingScore < minCorrelation {
			continue
		}
		h.scores[p.FileA+"\x00"+p.FileB] = p
		h.scores[p.FileB+"\x00"+p.FileA] = p
	}
	if len(h.scores) == 0 {
		return nil
	}
	return h
}

// candidates returns every pair that passed the thresholds, once each. The
// link check is left to isHidden, which callers run after cheaper filters:
// it reads the SCIP documents of both files.
func (h *hiddenCoupling) candidates() []cartographer.CoChangePair {
	if h == nil {
		return nil
	}
	var out []cartographer.CoChangePair
	for k, p := range h.scores {
		if k == p.FileA+"\x00"+p.FileB {
			out = append(out, p)
		}
	}
	return out
}

// isHidden reports whether a and b are a Cartographer hidden pair that passed
// the thresholds and that CKB does not see linked.
func (h *hiddenCoupling) isHidden(a, b string) bool {
	if h == nil {
		return false
	}
	if _, ok := h.scores[a+"\x00"+b]; !ok {
		return false
	}
	return !filesLinked(h.idx, a, b)
}

// filesLinked reports whether two repo-relative files are statically connected
// in a way that needs no import Cartographer could see: Go files of the same
// directory (one package), or — with a SCIP index — one file referencing a
// symbol the other defines.
func filesLinked(idx *scip.SCIPIndex, a, b string) bool {
	if filepath.Ext(a) == ".go" && filepath.Ext(b) == ".go" && filepath.Dir(a) == filepath.Dir(b) {
		return true
	}
	if idx == nil {
		return false
	}
	return referencesInto(idx, a, b) || referencesInto(idx, b, a)
}

// referencesInto reports whether document from uses a symbol defined in to.
func referencesInto(idx *scip.SCIPIndex, from, to string) bool {
	doc := idx.GetDocument(from)
	if doc == nil || idx.DefinitionIndex == nil {
		return false
	}
	for _, occ := range doc.Occurrences {
		if occ == nil || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 || strings.HasPrefix(occ.Symbol, "local ") {
			continue
		}
		if def := idx.DefinitionIndex[occ.Symbol]; def != nil && def.Doc != nil && def.Doc.RelativePath == to {
			return true
		}
	}
	return false
}

// strongCochangePairs keeps Cartographer co-change pairs with at least
// minHiddenCoChanges shared commits and a score of at least minCorrelation,
// strongest first. Cartographer scores a pair against the less-changed file,
// so two commits of a file changed twice score 1.0; the count floor keeps
// those from ranking above real coupling.
func strongCochangePairs(pairs []cartographer.CoChangePair, minCorrelation float64) []cartographer.CoChangePair {
	out := make([]cartographer.CoChangePair, 0, len(pairs))
	for _, p := range pairs {
		if p.Count >= minHiddenCoChanges && p.CouplingScore >= minCorrelation && p.FileA != "" && p.FileB != "" {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CouplingScore != out[j].CouplingScore {
			return out[i].CouplingScore > out[j].CouplingScore
		}
		return out[i].Count > out[j].Count
	})
	return out
}
