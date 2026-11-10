package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/lip"
)

// checkBlastRadius checks if changed symbols have high fan-out (many callers).
// Only reports functions and methods — variable/constant references are typically
// framework registrations (cobra commands, Qt signals, etc.), not real fan-out.
func (e *Engine) checkBlastRadius(ctx context.Context, changedFiles []string, opts ReviewPROptions, changeSet func() (*AnalyzeChangeSetResponse, error)) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	maxFanOut := opts.Policy.MaxFanOut
	informationalMode := maxFanOut <= 0

	// Fetch git churn map from Cartographer to identify hotspot files.
	// Used to escalate blast-radius findings for high-churn files from info → warning.
	var churnMap map[string]int
	if cartographer.Available() {
		if churn, err := cartographer.GitChurn(e.repoRoot, 0); err == nil {
			churnMap = churn
		}
	}

	// Prefetch LIP blast radius for all changed files in a single round-trip.
	// Returns nil when LIP is unavailable or doesn't support the message — the
	// rest of the function degrades to SCIP-only blast radius unchanged.
	var lipBR map[string]*impact.ExternalBlastRadius
	if e.lipSupports("query_blast_radius_batch") {
		lipURIs := make([]string, len(changedFiles))
		for i, f := range changedFiles {
			lipURIs[i] = "lip://local/" + f
		}
		if raw, _ := lip.QueryBlastRadiusBatch(lipURIs, 0.6); raw != nil {
			lipBR = make(map[string]*impact.ExternalBlastRadius, len(raw.Entries))
			for k, v := range raw.Entries {
				vCopy := v
				lipBR[k] = lip.EntryToExternal(&vCopy)
			}
		}
	}

	// The symbols the diff changed, not every symbol of a changed file: a large
	// file listed first used to take every slot, and the changes after it went
	// unchecked without a word.
	skipSeverity := "info"
	if !informationalMode {
		skipSeverity = "warning"
	}
	if !e.hasConsumerGraph() {
		return ReviewCheck{
			Name:     "blast-radius",
			Status:   "skip",
			Severity: skipSeverity,
			Summary:  "No SCIP index, so no callers to count; run 'ckb index'",
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}
	cs, err := changeSet()
	if err != nil || cs == nil {
		return ReviewCheck{
			Name:     "blast-radius",
			Status:   "skip",
			Severity: skipSeverity,
			Summary:  fmt.Sprintf("Could not map the diff to symbols: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}
	candidates := blastRadiusCandidates(cs.ChangedSymbols, changedFiles, e.scipAdapter.GetReferenceCount, e.scipAdapter.IsCallable)
	symbols := selectBlastRadiusSymbols(candidates, maxBlastRadiusSymbols)

	var findings []ReviewFinding
	for _, sym := range symbols {
		if ctx.Err() != nil {
			break
		}
		impactResp, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
			SymbolId: sym.stableId,
			Depth:    1,
		})
		if err != nil || impactResp == nil || impactResp.BlastRadius == nil {
			continue
		}

		// Merge LIP semantic enrichment into the SCIP-derived blast radius.
		// Keyed by symbol's stable ID which maps to LIP's symbol_uri via the
		// "lip://local/<file>#<symbol>" convention.
		semanticCount := 0
		if lipBR != nil {
			if enriched, ok := lip.LookupSymbol(lipBR, sym.file, sym.name); ok {
				// Convert BlastRadiusSummary → impact.BlastRadius for merge
				staticBR := &impact.BlastRadius{
					ModuleCount:       impactResp.BlastRadius.ModuleCount,
					FileCount:         impactResp.BlastRadius.FileCount,
					UniqueCallerCount: impactResp.BlastRadius.UniqueCallerCount,
					RiskLevel:         impactResp.BlastRadius.RiskLevel,
				}
				merged := impact.MergeBlastRadius(staticBR, enriched)
				if merged != nil {
					impactResp.BlastRadius = &BlastRadiusSummary{
						ModuleCount:         merged.ModuleCount,
						FileCount:           merged.FileCount,
						UniqueCallerCount:   merged.UniqueCallerCount,
						RiskLevel:           merged.RiskLevel,
						StaticCallerCount:   merged.StaticCallerCount,
						SemanticCallerCount: merged.SemanticCallerCount,
						ConfirmedCount:      merged.ConfirmedCount,
					}
					for _, sc := range merged.SemanticCallers {
						impactResp.BlastRadius.SemanticCallers = append(
							impactResp.BlastRadius.SemanticCallers,
							SemanticCallerInfo{
								SymbolURI:  sc.SymbolURI,
								FileURI:    sc.FileURI,
								Tier:       string(sc.Tier),
								Confidence: sc.Confidence,
								Similarity: sc.Similarity,
							},
						)
					}
					semanticCount = merged.SemanticCallerCount
				}
			}
		}

		callerCount := impactResp.BlastRadius.UniqueCallerCount

		if informationalMode {
			// In informational mode, only surface symbols with meaningful fan-out.
			// Symbols with 1-2 callers are normal coupling; 3+ suggests a change
			// that could ripple further than expected.
			if callerCount >= 3 || semanticCount > 0 {
				msg := fmt.Sprintf("Fan-out: %s has %d callers", sym.name, callerCount)
				if semanticCount > 0 {
					msg += fmt.Sprintf(" (+%d semantically coupled)", semanticCount)
				}
				hint := ""
				if sym.name != "" {
					hint = fmt.Sprintf("→ ckb explain %s", sym.name)
				}
				// Escalate from info → warning for hotspot files (high git churn).
				// A frequently-changing file with many callers is higher risk.
				severity := "info"
				if churnMap[sym.file] >= 15 {
					severity = "warning"
				}
				findings = append(findings, ReviewFinding{
					Check:    "blast-radius",
					Severity: severity,
					File:     sym.file,
					Message:  msg,
					Category: "risk",
					RuleID:   "ckb/blast-radius/high-fanout",
					Hint:     hint,
				})
			}
		} else if callerCount > maxFanOut {
			msg := fmt.Sprintf("High fan-out: %s has %d callers (threshold: %d)", sym.name, callerCount, maxFanOut)
			if semanticCount > 0 {
				msg += fmt.Sprintf(" (+%d semantically coupled)", semanticCount)
			}
			hint := ""
			if sym.name != "" {
				hint = fmt.Sprintf("→ ckb explain %s", sym.name)
			}
			findings = append(findings, ReviewFinding{
				Check:    "blast-radius",
				Severity: "warning",
				File:     sym.file,
				Message:  msg,
				Category: "risk",
				RuleID:   "ckb/blast-radius/high-fanout",
				Hint:     hint,
			})
		}
	}

	checked := blastRadiusCoverage(len(symbols), len(candidates))
	if informationalMode {
		status := "info"
		summary := "No changed function has 3+ callers" + checked
		if len(findings) > 0 {
			summary = fmt.Sprintf("%d changed function(s) have 3+ callers", len(findings)) + checked
		}
		return ReviewCheck{
			Name:     "blast-radius",
			Status:   status,
			Severity: "info",
			Summary:  summary,
			Duration: time.Since(start).Milliseconds(),
		}, findings
	}

	status := "pass"
	summary := "No high fan-out symbols in changes" + checked
	if len(findings) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d symbol(s) exceed fan-out threshold of %d", len(findings), maxFanOut) + checked
	}

	return ReviewCheck{
		Name:     "blast-radius",
		Status:   status,
		Severity: "warning",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// maxBlastRadiusSymbols caps the AnalyzeImpact calls the check makes.
const maxBlastRadiusSymbols = 30

// blastRadiusSymbol is a changed symbol the check counts callers for.
type blastRadiusSymbol struct {
	stableId string
	name     string
	kind     string
	file     string
	refs     int // References in the index, to rank before the expensive count
}

// blastRadiusCandidates returns the changed symbols worth counting callers for, in
// the order of the review's files: no file-level entries, nothing in a test file
// or outside the reviewed files, nothing deleted (the done check covers its
// uses), nothing only reworded, nothing but functions and methods, and no
// framework wiring.
func blastRadiusCandidates(changed []ChangedSymbolInfo, files []string, refCount func(string) int, isCallable func(string) bool) []blastRadiusSymbol {
	order := make(map[string]int, len(files))
	for i, f := range files {
		order[f] = i
	}
	seen := make(map[string]bool)
	var out []blastRadiusSymbol
	for _, sym := range changed {
		if _, reviewed := order[sym.File]; !reviewed || seen[sym.SymbolID] {
			continue
		}
		if strings.HasPrefix(sym.SymbolID, "file:") || sym.Trivial || sym.ChangeType == string(impact.ChangeDeleted) || isTestFilePath(sym.File) {
			continue
		}
		id, err := scip.ParseSCIPIdentifier(sym.SymbolID)
		if err != nil {
			continue
		}
		kind := string(id.ExtractSymbolKind())
		// Callers are what the check counts, and only something callable has them:
		// a changed type with dozens of references would take a slot and report 0.
		// `const f = () => …` has a constant's ID but is called like a function.
		if kind != string(scip.KindFunction) && kind != string(scip.KindMethod) && isCallable(sym.SymbolID) {
			kind = string(scip.KindFunction)
		}
		if (kind != string(scip.KindFunction) && kind != string(scip.KindMethod)) || isFrameworkSymbol(kind, sym.Name, sym.File) {
			continue
		}
		seen[sym.SymbolID] = true
		out = append(out, blastRadiusSymbol{stableId: sym.SymbolID, name: sym.Name, kind: kind, file: sym.File, refs: refCount(sym.SymbolID)})
	}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].file] < order[out[j].file] })
	return out
}

// selectBlastRadiusSymbols picks at most limit candidates, spread over their files:
// every file's best-referenced symbol first, then every file's second, and so on.
// Within a round the better-referenced go first, so a cut keeps them.
func selectBlastRadiusSymbols(candidates []blastRadiusSymbol, limit int) []blastRadiusSymbol {
	if len(candidates) <= limit {
		return candidates
	}
	var files []string
	byFile := make(map[string][]blastRadiusSymbol)
	for _, c := range candidates {
		if _, ok := byFile[c.file]; !ok {
			files = append(files, c.file)
		}
		byFile[c.file] = append(byFile[c.file], c)
	}
	for _, f := range files {
		syms := byFile[f]
		sort.SliceStable(syms, func(i, j int) bool { return syms[i].refs > syms[j].refs })
	}
	selected := make([]blastRadiusSymbol, 0, limit)
	for round := 0; len(selected) < limit; round++ {
		var this []blastRadiusSymbol
		for _, f := range files {
			if round < len(byFile[f]) {
				this = append(this, byFile[f][round])
			}
		}
		if len(this) == 0 {
			break
		}
		sort.SliceStable(this, func(i, j int) bool { return this[i].refs > this[j].refs })
		if room := limit - len(selected); len(this) > room {
			this = this[:room]
		}
		selected = append(selected, this...)
	}
	return selected
}

// blastRadiusCoverage says how much of the change the check looked at, when it
// could not look at all of it.
func blastRadiusCoverage(checked, total int) string {
	if checked >= total {
		return fmt.Sprintf(" (%d changed function(s) checked)", total)
	}
	return fmt.Sprintf(" (%d of %d changed functions checked)", checked, total)
}

// isFrameworkSymbol returns true if this symbol is likely framework wiring
// rather than real application logic. These symbols have "callers" that are
// framework registrations, not actual fan-out.
//
// This works across languages because SCIP provides symbol kinds uniformly:
// - Go: cobra.Command vars, init() registrations
// - C++: Qt signal/slot vars, gtest TEST() macro expansions
// - Java: Spring @Bean fields, JUnit @Test annotations
// - Python: Flask route decorators, pytest fixtures
//
// The heuristic: variables and constants in CLI/test/config files are almost
// always framework wiring. Functions and methods are the real blast-radius targets.
func isFrameworkSymbol(kind, name, file string) bool {
	// Variables and constants are references, not call targets
	switch kind {
	case "variable", "constant", "property", "field":
		return true
	}

	// Known framework patterns by name (language-agnostic)
	lowerName := strings.ToLower(name)
	frameworkPatterns := []string{
		"init",      // Go init(), C++ static initializers
		"setup",     // Test setup functions
		"teardown",  // Test teardown functions
		"register",  // Framework registration
		"configure", // Framework configuration
	}
	for _, p := range frameworkPatterns {
		if lowerName == p {
			return true
		}
	}

	// CLI command patterns (Go cobra, Python click, etc.)
	if strings.HasPrefix(file, "cmd/") && strings.HasSuffix(lowerName, "cmd") {
		return true
	}

	return false
}
