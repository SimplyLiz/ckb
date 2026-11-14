package query

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// Two-sided co-change (hidden coupling, hotspot partners, implicit co-owners)
// comes from coupling.History: the last 500 commits, each pair scored against
// the less-changed file. It replaced Cartographer's git_cochange with the same
// rules and score, so the floors below, calibrated on Cartographer's pairs,
// carry over.
//
// A pair counts as hidden only when
//   - both files are source code,
//   - it co-changed at least minHiddenCoChanges times,
//   - its score passes the caller's correlation floor,
//   - both files are in the SCIP index, and
//   - CKB finds no link: not the same Go package, and neither file references
//     a symbol the other defines.
//
// Without an index nothing is hidden. Cartographer's import graph was the only
// other "no link" evidence, and measured on four repos (2026-11-14) it was
// wrong in most samples: Go files of one package, TypeScript @/ aliases and
// inline Rust paths are links it did not see.

// minHiddenCoChanges is the least number of shared commits for hidden coupling
// and for listed co-change partners. Two is what any pair of files touched by
// two refactors has.
const minHiddenCoChanges = 3

// cochangeHistoryTTL bounds how long one History is reused for the same HEAD.
const cochangeHistoryTTL = 10 * time.Minute

type cochangeHistoryEntry struct {
	head string
	at   time.Time
	h    *coupling.History
}

var cochangeHistoryCache = struct {
	sync.Mutex
	byRepo map[string]cochangeHistoryEntry
}{byRepo: map[string]cochangeHistoryEntry{}}

// cochangeHistory returns the repo's co-change history, cached per repo and
// HEAD. Nil when git fails.
func (e *Engine) cochangeHistory(ctx context.Context) *coupling.History {
	head := ""
	if e.gitAdapter != nil {
		head, _ = e.gitAdapter.GetHeadCommit()
	}
	cochangeHistoryCache.Lock()
	defer cochangeHistoryCache.Unlock()
	if c, ok := cochangeHistoryCache.byRepo[e.repoRoot]; ok && head != "" && c.head == head && time.Since(c.at) < cochangeHistoryTTL {
		return c.h
	}
	h, err := coupling.LoadHistory(ctx, e.repoRoot, 0, pathclass.FromConfig(e.repoRoot, e.config))
	if err != nil {
		return nil
	}
	cochangeHistoryCache.byRepo[e.repoRoot] = cochangeHistoryEntry{head: head, at: time.Now(), h: h}
	return h
}

// cochangePartners returns up to limit partners of file with at least
// minHiddenCoChanges shared commits and a score of at least minScore, strongest
// first, skipping files skip reports (nil skips nothing).
func (e *Engine) cochangePartners(ctx context.Context, file string, minScore float64, limit int, skip func(string) bool) []coupling.Pair {
	h := e.cochangeHistory(ctx)
	if h == nil {
		return nil
	}
	var out []coupling.Pair
	for _, p := range h.StrongPartners(file, minHiddenCoChanges, minScore, 0) {
		if skip != nil && skip(p.FileB) {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// hiddenCoupling finds hidden pairs against the co-change history and the
// SCIP index.
type hiddenCoupling struct {
	h              *coupling.History
	idx            *scip.SCIPIndex
	minCorrelation float64
}

// newHiddenCoupling returns nil when there is no SCIP index or no history, so
// callers can skip work.
func (e *Engine) newHiddenCoupling(ctx context.Context, minCorrelation float64) *hiddenCoupling {
	if !e.hasConsumerGraph() {
		return nil
	}
	idx := e.scipAdapter.GetIndex()
	if idx == nil {
		return nil
	}
	h := e.cochangeHistory(ctx)
	if h == nil {
		return nil
	}
	return &hiddenCoupling{h: h, idx: idx, minCorrelation: minCorrelation}
}

// hiddenPartners returns the partners of file that pass the thresholds and
// the link check, strongest first. FileA is always file.
func (hc *hiddenCoupling) hiddenPartners(file string) []coupling.Pair {
	if hc == nil || !isConceptSourceFile(file) || hc.idx.GetDocument(file) == nil {
		return nil
	}
	var out []coupling.Pair
	for _, p := range hc.h.StrongPartners(file, minHiddenCoChanges, hc.minCorrelation, 0) {
		if hc.isHiddenLink(file, p.FileB) {
			out = append(out, p)
		}
	}
	return out
}

// isHidden reports whether a and b co-changed enough to pass the thresholds
// and CKB sees no link between them.
func (hc *hiddenCoupling) isHidden(a, b string) bool {
	if hc == nil {
		return false
	}
	n := 0
	for _, p := range hc.h.Partners(a) {
		if p.FileB == b {
			if p.Count < minHiddenCoChanges || p.Score < hc.minCorrelation {
				return false
			}
			n = p.Count
			break
		}
	}
	return n > 0 && hc.isHiddenLink(a, b)
}

// isHiddenLink is the static half of isHidden: both files are source code in
// the index and neither links to the other.
func (hc *hiddenCoupling) isHiddenLink(a, b string) bool {
	// "No link" says nothing about a changelog, a config or a fixture: they
	// never import anything. Hidden coupling is a claim about code.
	if !isConceptSourceFile(a) || !isConceptSourceFile(b) {
		return false
	}
	// A file the index does not cover has unknown links, not none.
	if hc.idx.GetDocument(a) == nil || hc.idx.GetDocument(b) == nil {
		return false
	}
	return !filesLinked(hc.idx, a, b)
}

// filesLinked reports whether two repo-relative files are statically
// connected: Go files of the same directory (one package), or — with a SCIP
// index — one file referencing a symbol the other defines.
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
		if definedIn(idx, occ.Symbol) == to {
			return true
		}
	}
	return false
}
