package query

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/perf"
)

// hiddenScanNoIndex is PerfScanSummary.NotAssessed without a SCIP index.
const hiddenScanNoIndex = "no SCIP index: without one CKB cannot tell a missing link from one it does not see, so no pair is called hidden. Run 'ckb index'."

// ScanHiddenCoupling lists the repository's hidden-coupling pairs
// (scanPerformance, ckb perf coupling): co-change pairs from coupling.History
// that pass the thresholds and have no link in the SCIP index, by the rules
// in cochange_hidden.go. Without an index nothing is checked and
// Summary.NotAssessed says so.
func (e *Engine) ScanHiddenCoupling(ctx context.Context, opts perf.ScanOptions) (*perf.PerfScanResult, error) {
	if opts.MinCorrelation <= 0 {
		opts.MinCorrelation = 0.3
	}
	if opts.MinCoChanges <= 0 {
		opts.MinCoChanges = minHiddenCoChanges
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}

	h := e.cochangeHistory(ctx)
	if h == nil {
		return nil, fmt.Errorf("reading git history of %s failed", e.repoRoot)
	}
	var idx *scip.SCIPIndex
	if e.hasConsumerGraph() {
		idx = e.scipAdapter.GetIndex()
	}
	return scanHiddenCoupling(ctx, h, idx, opts)
}

// scanHiddenCoupling is ScanHiddenCoupling on a given history and index (nil:
// none), opts already defaulted.
func scanHiddenCoupling(ctx context.Context, h *coupling.History, idx *scip.SCIPIndex, opts perf.ScanOptions) (*perf.PerfScanResult, error) {
	result := &perf.PerfScanResult{
		HiddenCoupling: []perf.HiddenCouplingPair{},
		Summary:        perf.PerfScanSummary{CommitsRead: h.Commits(), FilesObserved: h.Files()},
	}
	if idx == nil {
		result.Summary.NotAssessed = hiddenScanNoIndex
		return result, nil
	}
	hc := &hiddenCoupling{h: h, idx: idx, minCorrelation: opts.MinCorrelation}

	for _, p := range h.AllPairs(opts.MinCoChanges) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if p.Score < opts.MinCorrelation {
			continue
		}
		if !inScanScope(p.FileA, opts.Scope) || !inScanScope(p.FileB, opts.Scope) {
			continue
		}
		result.Summary.PairsChecked++
		if !isConceptSourceFile(p.FileA) || !isConceptSourceFile(p.FileB) ||
			hc.idx.GetDocument(p.FileA) == nil || hc.idx.GetDocument(p.FileB) == nil {
			result.Summary.PairsNotIndexed++
			continue
		}
		if !hc.isHiddenLink(p.FileA, p.FileB) {
			continue
		}
		if len(result.HiddenCoupling) < opts.Limit {
			result.HiddenCoupling = append(result.HiddenCoupling, perf.HiddenCouplingPair{
				FileA:         p.FileA,
				FileB:         p.FileB,
				Correlation:   p.Score,
				CoChangeCount: p.Count,
				Level:         perf.CorrelationLevel(p.Score),
				Explanation: fmt.Sprintf("%s and %s changed together in %d commits, "+
					"but neither references a symbol the other defines — likely shared state, "+
					"a shared format or a third party between them",
					filepath.Base(p.FileA), filepath.Base(p.FileB), p.Count),
			})
		}
		result.Summary.HiddenPairsFound++
	}
	return result, nil
}

// inScanScope reports whether file lies under one of scope's paths (all files
// when scope is empty).
func inScanScope(file string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		s = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(s)), "/")
		if s == "." || file == s || strings.HasPrefix(file, s+"/") {
			return true
		}
	}
	return false
}
