package query

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/compression"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/lip"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/telemetry"
)

// AnalyzeImpactOptions contains options for analyzeImpact.
type AnalyzeImpactOptions struct {
	SymbolId         string
	Depth            int
	IncludeTests     bool
	IncludeTelemetry bool   // Include observed telemetry data
	TelemetryPeriod  string // Time period for telemetry ("7d", "30d", "90d")
}

// AnalyzeImpactResponse is the response for analyzeImpact.
type AnalyzeImpactResponse struct {
	Symbol           *SymbolInfo           `json:"symbol"`
	Visibility       *VisibilityInfo       `json:"visibility"`
	RiskScore        *RiskScore            `json:"riskScore"`
	BlastRadius      *BlastRadiusSummary   `json:"blastRadius,omitempty"`
	DirectImpact     []ImpactItem          `json:"directImpact"`
	TransitiveImpact []ImpactItem          `json:"transitiveImpact,omitempty"`
	ModulesAffected  []ModuleImpact        `json:"modulesAffected"`
	ObservedUsage    *ObservedUsageSummary `json:"observedUsage,omitempty"`
	RelatedDecisions []RelatedDecision     `json:"relatedDecisions,omitempty"` // v6.5: ADRs affecting impacted modules
	DocsToUpdate     []DocToUpdate         `json:"docsToUpdate,omitempty"`     // v7.3: Docs that mention this symbol
	// ArchImpact is Cartographer's architectural simulation for the symbol's file:
	// predicted affected modules, cycle risk, layer violations, and health delta.
	// Only populated when the binary is built with -tags cartographer.
	ArchImpact        *cartographer.ImpactAnalysis `json:"archImpact,omitempty"`
	BlendedConfidence float64                      `json:"blendedConfidence,omitempty"`
	Truncated         bool                         `json:"truncated,omitempty"`
	TruncationInfo    *TruncationInfo              `json:"truncationInfo,omitempty"`
	Provenance        *Provenance                  `json:"provenance"`
	Drilldowns        []output.Drilldown           `json:"drilldowns,omitempty"`
}

// DocToUpdate represents documentation that may need updating when a symbol changes.
type DocToUpdate struct {
	Path    string `json:"path"`
	Title   string `json:"title,omitempty"`
	Line    int    `json:"line,omitempty"`
	Context string `json:"context,omitempty"`
}

// ObservedUsageSummary contains telemetry-based usage information
type ObservedUsageSummary struct {
	HasTelemetry       bool     `json:"hasTelemetry"`
	TotalCalls         int64    `json:"totalCalls,omitempty"`
	LastObserved       string   `json:"lastObserved,omitempty"`
	MatchQuality       string   `json:"matchQuality,omitempty"`
	ObservedConfidence float64  `json:"observedConfidence,omitempty"`
	Trend              string   `json:"trend,omitempty"`
	CallerServices     []string `json:"callerServices,omitempty"`
}

// RiskScore describes the risk of changing a symbol.
type RiskScore struct {
	Level       string       `json:"level"` // high, medium, low
	Score       float64      `json:"score"`
	Explanation string       `json:"explanation"`
	Factors     []RiskFactor `json:"factors"`
}

// RiskFactor describes a factor in the risk score.
type RiskFactor struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight"`
	// Evidence is a concrete, human-readable basis for this factor's value
	// (e.g. "14 direct consumers", "3 modules affected"). Optional: only
	// AnalyzeChangeSet's aggregated factors populate it today.
	Evidence string `json:"evidence,omitempty"`
}

// ImpactItem describes an impact from changing a symbol.
type ImpactItem struct {
	StableId   string          `json:"stableId,omitempty"`
	Name       string          `json:"name,omitempty"`
	Kind       string          `json:"kind"` // direct-caller, transitive-caller, type-dependency, test-dependency
	Distance   int             `json:"distance"`
	ModuleId   string          `json:"moduleId,omitempty"`
	Location   *LocationInfo   `json:"location,omitempty"`
	Confidence float64         `json:"confidence"`
	Visibility *VisibilityInfo `json:"visibility,omitempty"`
}

// ModuleImpact describes the impact on a module.
type ModuleImpact struct {
	ModuleId      string `json:"moduleId"`
	Name          string `json:"name,omitempty"`
	ImpactCount   int    `json:"impactCount"`
	DirectCount   int    `json:"directCount"`
	BreakingCount int    `json:"breakingCount,omitempty"`
}

// BlastRadiusSummary summarizes the spread of impact across the codebase.
type BlastRadiusSummary struct {
	ModuleCount       int    `json:"moduleCount"`
	FileCount         int    `json:"fileCount"`
	UniqueCallerCount int    `json:"uniqueCallerCount"` // SCIP static callers only (drives thresholds)
	RiskLevel         string `json:"riskLevel"`         // "low", "medium", "high"

	// LIP semantic enrichment (omitted when LIP is unavailable)
	StaticCallerCount   int                  `json:"staticCallerCount,omitempty"`
	SemanticCallerCount int                  `json:"semanticCallerCount,omitempty"`
	ConfirmedCount      int                  `json:"confirmedCount,omitempty"`
	SemanticCallers     []SemanticCallerInfo `json:"semanticCallers,omitempty"`
}

// SemanticCallerInfo is an embedding-discovered caller surfaced in the blast radius.
type SemanticCallerInfo struct {
	SymbolURI  string  `json:"symbolUri,omitempty"`
	FileURI    string  `json:"fileUri"`
	Tier       string  `json:"tier"` // "semantic" or "both"
	Confidence float64 `json:"confidence"`
	Similarity float32 `json:"similarity,omitempty"`
}

// scipCallerProvider adapts SCIPAdapter to the TransitiveCallerProvider interface
type scipCallerProvider struct {
	adapter *scip.SCIPAdapter
}

// GetTransitiveCallers implements impact.TransitiveCallerProvider
func (p *scipCallerProvider) GetTransitiveCallers(symbolId string, maxDepth int) (map[string]int, error) {
	graph, err := p.adapter.BuildCallGraph(symbolId, scip.CallGraphOptions{
		Direction: scip.DirectionCallers,
		MaxDepth:  maxDepth,
		MaxNodes:  100, // Bounded for performance
	})
	if err != nil {
		return nil, err
	}

	result := make(map[string]int)
	if graph == nil {
		return result, nil
	}

	// BFS to compute depths from root
	visited := make(map[string]bool)
	queue := []struct {
		id    string
		depth int
	}{{symbolId, 0}}
	visited[symbolId] = true

	// Build adjacency list from edges (caller -> callee)
	// For callers direction: edges go from caller to callee
	// So we need to invert: who calls X?
	callersOf := make(map[string][]string)
	for _, edge := range graph.Edges {
		// edge.From is caller, edge.To is callee
		// We want: for each callee, list of callers
		callersOf[edge.To] = append(callersOf[edge.To], edge.From)
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, callerId := range callersOf[curr.id] {
			if !visited[callerId] {
				visited[callerId] = true
				callerDepth := curr.depth + 1
				result[callerId] = callerDepth
				if callerDepth < maxDepth {
					queue = append(queue, struct {
						id    string
						depth int
					}{callerId, callerDepth})
				}
			}
		}
	}

	return result, nil
}

// AnalyzeImpact analyzes the impact of changing a symbol.
func (e *Engine) AnalyzeImpact(ctx context.Context, opts AnalyzeImpactOptions) (*AnalyzeImpactResponse, error) {
	startTime := time.Now()

	// Default options
	if opts.Depth <= 0 {
		opts.Depth = 2
	}

	// Get repo state (full mode for impact analysis)
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Resolve symbol ID - try resolver first, then fall back to SCIP directly
	resolved, _ := e.resolver.ResolveSymbolId(opts.SymbolId)

	// Get symbol info from backend
	var symbolInfo *SymbolInfo
	var backendContribs []BackendContribution
	var completeness CompletenessInfo

	// Determine the symbol ID to use for SCIP lookup
	symbolIdForLookup := opts.SymbolId
	if resolved != nil && resolved.Symbol != nil {
		symbolIdForLookup = resolved.Symbol.StableId
	}

	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		result, symbolErr := e.scipAdapter.GetSymbol(ctx, symbolIdForLookup)
		if symbolErr == nil && result != nil {
			symbolInfo = &SymbolInfo{
				StableId:      result.StableID,
				Name:          result.Name,
				Kind:          result.Kind,
				ContainerName: result.ContainerName,
				ModuleId:      result.ModuleID,
				Visibility: &VisibilityInfo{
					Visibility: result.Visibility,
					Confidence: result.VisibilityConfidence,
					Source:     "scip",
				},
				Location: &LocationInfo{
					FileId:      result.Location.Path,
					StartLine:   result.Location.Line,
					StartColumn: result.Location.Column,
				},
			}
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "scip",
				Available:    true,
				Used:         true,
				Completeness: result.Completeness.Score,
			})
			completeness = CompletenessInfo{
				Score:  result.Completeness.Score,
				Reason: string(result.Completeness.Reason),
			}
		}
	}

	// Fallback to identity data
	if symbolInfo == nil && resolved != nil && resolved.Symbol != nil && resolved.Symbol.Fingerprint != nil {
		symbolInfo = &SymbolInfo{
			StableId:      resolved.Symbol.StableId,
			Name:          resolved.Symbol.Fingerprint.Name,
			Kind:          string(resolved.Symbol.Fingerprint.Kind),
			ContainerName: resolved.Symbol.Fingerprint.QualifiedContainer,
			Visibility: &VisibilityInfo{
				Visibility: "unknown",
				Confidence: 0.3,
				Source:     "default",
			},
		}
		completeness = CompletenessInfo{Score: 0.5, Reason: "identity-only"}
	}

	// If we still don't have symbol info, return not found
	if symbolInfo == nil {
		return nil, errors.NewCkbError(
			errors.SymbolNotFound,
			fmt.Sprintf("Symbol not found: %s", opts.SymbolId),
			nil, nil, nil,
		)
	}

	// Find references for impact analysis
	var refs []impact.Reference
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		refOpts := backends.RefOptions{
			MaxResults:   500,
			IncludeTests: opts.IncludeTests,
		}
		refsResult, refsErr := e.scipAdapter.FindReferences(ctx, symbolIdForLookup, refOpts)
		if refsErr == nil && refsResult != nil {
			for _, ref := range refsResult.References {
				impactRef := impact.Reference{
					Kind: impact.ReferenceKind(ref.Kind),
					Location: &impact.Location{
						FileId:    ref.Location.Path,
						StartLine: ref.Location.Line,
					},
					IsTest: isTestFilePath(ref.Location.Path), // Set IsTest based on file path
					// FromSymbol/FromName resolve the enclosing symbol (e.g. the
					// caller function containing this reference) when the backend
					// found one. Left empty (not "unknown") when genuinely
					// unresolvable, e.g. a package-level reference outside any
					// function.
					FromSymbol: ref.FromSymbol,
					FromName:   ref.FromSymbolName,
				}
				refs = append(refs, impactRef)
			}
		}
	}

	// Filter test references if not included
	if !opts.IncludeTests {
		refs = filterTestReferences(refs)
	}

	// Create impact analyzer with transitive caller support if SCIP available
	var analyzer *impact.ImpactAnalyzer
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		callerProv := &scipCallerProvider{adapter: e.scipAdapter}
		analyzer = impact.NewImpactAnalyzerWithCallers(opts.Depth, callerProv)
	} else {
		analyzer = impact.NewImpactAnalyzer(opts.Depth)
	}

	impactSymbol := &impact.Symbol{
		StableId: symbolInfo.StableId,
		Name:     symbolInfo.Name,
		Kind:     impact.SymbolKind(symbolInfo.Kind),
		ModuleId: symbolInfo.ModuleId,
	}
	if symbolInfo.Visibility != nil {
		impactSymbol.Modifiers = []string{symbolInfo.Visibility.Visibility}
	}

	result, err := analyzer.Analyze(impactSymbol, refs)
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Fetch LIP enrichment up-front so tier-1 static callers can be folded
	// into result.DirectImpact/TransitiveImpact before budget truncation and
	// item sorting. `ext` is also used later to populate semantic callers on
	// the BlastRadius summary via MergeBlastRadius.
	ext := e.fetchLIPEnrichment(symbolInfo)
	if ext != nil {
		result.DirectImpact, result.TransitiveImpact = impact.FoldExternalStaticItems(
			result.DirectImpact, result.TransitiveImpact, ext, e.repoRoot)
		// Recompute the blast-radius summary from the unioned item set so
		// UniqueCallerCount / FileCount / RiskLevel reflect LIP's static
		// contribution. Skipped when EdgesSource=="empty" because the fold
		// no-ops in that case and the original summary is already correct.
		if ext.EdgesSource != impact.EdgesSourceEmpty && result.BlastRadius != nil {
			all := append([]impact.ImpactItem{}, result.DirectImpact...)
			all = append(all, result.TransitiveImpact...)
			result.BlastRadius = impact.RecomputeBlastRadius(all)
		}
	}

	// Convert results
	directImpact := convertImpactItems(result.DirectImpact)
	transitiveImpact := convertImpactItems(result.TransitiveImpact)
	modulesAffected := convertModuleImpacts(result.ModulesAffected)

	// Apply budget
	budget := e.compressor.GetBudget()
	var truncationInfo *TruncationInfo
	totalItems := len(directImpact) + len(transitiveImpact)
	if totalItems > budget.MaxImpactItems {
		truncationInfo = &TruncationInfo{
			Reason:        "max-items",
			OriginalCount: totalItems,
			ReturnedCount: budget.MaxImpactItems,
		}
		// Truncate transitive first, then direct
		if len(directImpact) >= budget.MaxImpactItems {
			directImpact = directImpact[:budget.MaxImpactItems]
			transitiveImpact = nil
		} else {
			remaining := budget.MaxImpactItems - len(directImpact)
			if len(transitiveImpact) > remaining {
				transitiveImpact = transitiveImpact[:remaining]
			}
		}
	}

	// Sort by impact priority
	sortImpactItems(directImpact)
	sortImpactItems(transitiveImpact)

	// Sort modules by impact count
	sort.Slice(modulesAffected, func(i, j int) bool {
		return modulesAffected[i].ImpactCount > modulesAffected[j].ImpactCount
	})

	// Limit modules
	if len(modulesAffected) > budget.MaxModules {
		modulesAffected = modulesAffected[:budget.MaxModules]
	}

	// Convert visibility and risk score
	visibility := symbolInfo.Visibility
	riskScore := convertRiskScore(result.RiskScore)

	// Build provenance
	provenance := e.buildProvenance(repoState, "full", startTime, backendContribs, completeness)
	if result.AnalysisLimits != nil && result.AnalysisLimits.HasLimitations() {
		provenance.Warnings = append(provenance.Warnings, result.AnalysisLimits.Notes...)
	}

	// Generate drilldowns
	var compTrunc *compression.TruncationInfo
	if truncationInfo != nil {
		compTrunc = &compression.TruncationInfo{
			Reason:        compression.TruncMaxItems,
			OriginalCount: truncationInfo.OriginalCount,
			ReturnedCount: truncationInfo.ReturnedCount,
		}
	}

	var topModule *output.Module
	if len(modulesAffected) > 0 {
		topModule = &output.Module{
			ModuleId: modulesAffected[0].ModuleId,
			Name:     modulesAffected[0].Name,
		}
	}

	drilldowns := e.generateDrilldowns(compTrunc, completeness, opts.SymbolId, topModule)

	// Get telemetry data if enabled
	var observedUsage *ObservedUsageSummary
	var blendedConfidence float64

	if opts.IncludeTelemetry && e.config != nil && e.config.Telemetry.Enabled && e.db != nil {
		observedUsage, blendedConfidence = e.getObservedUsageForImpact(symbolIdForLookup, opts.TelemetryPeriod)

		// Add telemetry factors to risk score if we have observed data
		if observedUsage != nil && observedUsage.HasTelemetry && riskScore != nil {
			riskScore = e.enhanceRiskScoreWithTelemetry(riskScore, observedUsage)
		}
	} else {
		// Static-only confidence
		blendedConfidence = completeness.Score * 0.79
	}

	// v6.5: Gather related decisions for all affected modules
	var relatedDecisions []RelatedDecision
	seenDecisions := make(map[string]bool)

	// Check symbol's own module first
	if symbolInfo.ModuleId != "" {
		for _, d := range e.getRelatedDecisions(symbolInfo.ModuleId) {
			if !seenDecisions[d.ID] {
				relatedDecisions = append(relatedDecisions, d)
				seenDecisions[d.ID] = true
			}
		}
	}

	// Check affected modules (limit to avoid excessive lookups)
	for i, mod := range modulesAffected {
		if i >= 5 {
			break
		}
		for _, d := range e.getRelatedDecisions(mod.ModuleId) {
			if !seenDecisions[d.ID] {
				relatedDecisions = append(relatedDecisions, d)
				seenDecisions[d.ID] = true
			}
		}
	}

	// v7.3: Get documentation that may need updating (top 5 docs mentioning this symbol)
	var docsToUpdate []DocToUpdate
	if symbolInfo.StableId != "" {
		docsToUpdate = e.getDocsToUpdate(symbolInfo.StableId, 5)
	}

	// Convert blast radius, then enrich with LIP semantic coupling when available.
	// The static-item fold already happened up-front; MergeBlastRadius here
	// only blends in semantic (embedding-discovered) callers and preserves
	// the post-fold summary counts.
	var blastRadius *BlastRadiusSummary
	if result.BlastRadius != nil {
		enriched := result.BlastRadius
		if ext != nil {
			enriched = impact.MergeBlastRadius(result.BlastRadius, ext)
		}

		blastRadius = &BlastRadiusSummary{
			ModuleCount:         enriched.ModuleCount,
			FileCount:           enriched.FileCount,
			UniqueCallerCount:   enriched.UniqueCallerCount,
			RiskLevel:           enriched.RiskLevel,
			StaticCallerCount:   enriched.StaticCallerCount,
			SemanticCallerCount: enriched.SemanticCallerCount,
			ConfirmedCount:      enriched.ConfirmedCount,
		}
		for _, sc := range enriched.SemanticCallers {
			blastRadius.SemanticCallers = append(blastRadius.SemanticCallers, SemanticCallerInfo{
				SymbolURI:  sc.SymbolURI,
				FileURI:    sc.FileURI,
				Tier:       string(sc.Tier),
				Confidence: sc.Confidence,
				Similarity: sc.Similarity,
			})
		}
	}

	resp := &AnalyzeImpactResponse{
		Symbol:            symbolInfo,
		Visibility:        visibility,
		RiskScore:         riskScore,
		BlastRadius:       blastRadius,
		DirectImpact:      directImpact,
		TransitiveImpact:  transitiveImpact,
		ModulesAffected:   modulesAffected,
		ObservedUsage:     observedUsage,
		RelatedDecisions:  relatedDecisions,
		DocsToUpdate:      docsToUpdate,
		BlendedConfidence: blendedConfidence,
		Truncated:         truncationInfo != nil,
		TruncationInfo:    truncationInfo,
		Provenance:        provenance,
		Drilldowns:        drilldowns,
	}

	// Augment with Cartographer architectural simulation for the symbol's file.
	// Uses the file path as the module ID — gives predicted affected modules, cycle
	// risk, layer violations, and health delta. Gracefully skipped when unavailable.
	if cartographer.Available() && symbolInfo.Location != nil {
		filePath := symbolInfo.Location.FileId
		if archImpact, cerr := cartographer.SimulateChange(e.repoRoot, filePath, symbolInfo.Signature, ""); cerr == nil {
			resp.ArchImpact = archImpact
		}
	}

	return resp, nil
}

// getObservedUsageForImpact fetches telemetry data for impact analysis
func (e *Engine) getObservedUsageForImpact(symbolID string, period string) (*ObservedUsageSummary, float64) {
	storage := telemetry.NewStorage(e.db.Conn())

	// Compute period filter
	periodFilter := computeTelemetryPeriodFilter(period)

	// Get usage data
	usages, err := storage.GetObservedUsage(symbolID, periodFilter)
	if err != nil || len(usages) == 0 {
		return &ObservedUsageSummary{HasTelemetry: false}, 0.79 // Static-only confidence
	}

	// Calculate totals
	var totalCalls int64
	var lastObserved time.Time
	var matchQuality telemetry.MatchQuality

	for i, u := range usages {
		totalCalls += u.CallCount
		if i == 0 {
			lastObserved = u.IngestedAt
			matchQuality = u.MatchQuality
		}
	}

	// Get callers
	var callerServices []string
	if e.config.Telemetry.Aggregation.StoreCallers {
		callers, err := storage.GetObservedCallers(symbolID, 5)
		if err == nil {
			for _, c := range callers {
				callerServices = append(callerServices, c.CallerService)
			}
		}
	}

	// Compute trend
	trend := computeUsageTrend(usages)

	// Build summary
	summary := &ObservedUsageSummary{
		HasTelemetry:       true,
		TotalCalls:         totalCalls,
		LastObserved:       lastObserved.Format(time.RFC3339),
		MatchQuality:       string(matchQuality),
		ObservedConfidence: matchQuality.Confidence(),
		Trend:              trend,
		CallerServices:     callerServices,
	}

	// Compute blended confidence
	staticConfidence := 0.79
	observedConfidence := matchQuality.Confidence()
	blendedConfidence := computeBlendedConfidenceScore(staticConfidence, observedConfidence)

	return summary, blendedConfidence
}

// enhanceRiskScoreWithTelemetry adds telemetry factors to risk assessment
func (e *Engine) enhanceRiskScoreWithTelemetry(riskScore *RiskScore, usage *ObservedUsageSummary) *RiskScore {
	// Copy existing factors
	enhanced := &RiskScore{
		Level:       riskScore.Level,
		Score:       riskScore.Score,
		Explanation: riskScore.Explanation,
		Factors:     make([]RiskFactor, len(riskScore.Factors)),
	}
	copy(enhanced.Factors, riskScore.Factors)

	// Add observed usage factor
	usageFactor := RiskFactor{
		Name:   "observed_usage",
		Weight: 0.2,
	}

	if usage.TotalCalls == 0 {
		usageFactor.Value = 0.0 // No observed usage - lower risk
	} else if usage.TotalCalls < 100 {
		usageFactor.Value = 0.3 // Low usage
	} else if usage.TotalCalls < 1000 {
		usageFactor.Value = 0.6 // Medium usage
	} else {
		usageFactor.Value = 1.0 // High usage - higher risk
	}
	enhanced.Factors = append(enhanced.Factors, usageFactor)

	// Add caller diversity factor
	if len(usage.CallerServices) > 0 {
		diversityFactor := RiskFactor{
			Name:   "caller_diversity",
			Weight: 0.15,
		}
		if len(usage.CallerServices) >= 5 {
			diversityFactor.Value = 1.0 // Many callers - higher risk
		} else if len(usage.CallerServices) >= 3 {
			diversityFactor.Value = 0.6
		} else {
			diversityFactor.Value = 0.3
		}
		enhanced.Factors = append(enhanced.Factors, diversityFactor)
	}

	// Add trend factor
	if usage.Trend != "" {
		trendFactor := RiskFactor{
			Name:   "usage_trend",
			Weight: 0.1,
		}
		switch usage.Trend {
		case "increasing":
			trendFactor.Value = 0.8 // Growing usage - higher risk
		case "decreasing":
			trendFactor.Value = 0.2 // Declining usage - lower risk
		default:
			trendFactor.Value = 0.5 // Stable
		}
		enhanced.Factors = append(enhanced.Factors, trendFactor)
	}

	// Recalculate score with new factors
	var totalWeight, weightedSum float64
	for _, f := range enhanced.Factors {
		totalWeight += f.Weight
		weightedSum += f.Value * f.Weight
	}

	if totalWeight > 0 {
		enhanced.Score = weightedSum / totalWeight
	}

	// Update level
	if enhanced.Score >= 0.7 {
		enhanced.Level = "high"
	} else if enhanced.Score >= 0.4 {
		enhanced.Level = "medium"
	} else {
		enhanced.Level = "low"
	}

	// Update explanation with telemetry info
	if usage.TotalCalls > 0 {
		enhanced.Explanation = fmt.Sprintf("%s Observed %d calls from %d services.",
			enhanced.Explanation, usage.TotalCalls, len(usage.CallerServices))
	} else {
		enhanced.Explanation = fmt.Sprintf("%s No runtime calls observed in telemetry window.",
			enhanced.Explanation)
	}

	return enhanced
}

// computeTelemetryPeriodFilter converts period string to date filter
func computeTelemetryPeriodFilter(period string) string {
	now := time.Now()
	switch period {
	case "7d":
		return now.AddDate(0, 0, -7).Format("2006-01-02")
	case "30d":
		return now.AddDate(0, 0, -30).Format("2006-01-02")
	case "90d":
		return now.AddDate(0, 0, -90).Format("2006-01-02")
	case "all":
		return ""
	default:
		return now.AddDate(0, 0, -90).Format("2006-01-02")
	}
}

// computeUsageTrend calculates the usage trend from observed data
func computeUsageTrend(usages []telemetry.ObservedUsage) string {
	if len(usages) < 2 {
		return "stable"
	}

	mid := len(usages) / 2
	var recentCalls, olderCalls int64
	for i, u := range usages {
		if i < mid {
			recentCalls += u.CallCount
		} else {
			olderCalls += u.CallCount
		}
	}

	if olderCalls == 0 {
		if recentCalls > 0 {
			return "increasing"
		}
		return "stable"
	}

	ratio := float64(recentCalls) / float64(olderCalls)
	if ratio > 1.2 {
		return "increasing"
	} else if ratio < 0.8 {
		return "decreasing"
	}
	return "stable"
}

// computeBlendedConfidenceScore combines static and observed confidence
func computeBlendedConfidenceScore(staticConfidence, observedConfidence float64) float64 {
	// Take higher, with small boost if both agree
	base := staticConfidence
	if observedConfidence > base {
		base = observedConfidence
	}

	agreementBoost := 0.0
	if staticConfidence > 0.5 && observedConfidence > 0.5 {
		agreementBoost = 0.03
	}

	result := base + agreementBoost
	if result > 1.0 {
		result = 1.0
	}
	return result
}

// filterTestReferences removes test references.
func filterTestReferences(refs []impact.Reference) []impact.Reference {
	filtered := make([]impact.Reference, 0, len(refs))
	for _, ref := range refs {
		if !ref.IsTest {
			filtered = append(filtered, ref)
		}
	}
	return filtered
}

// convertImpactItems converts impact items to response format.
func convertImpactItems(items []impact.ImpactItem) []ImpactItem {
	result := make([]ImpactItem, 0, len(items))

	for _, item := range items {
		ri := ImpactItem{
			StableId:   item.StableId,
			Name:       item.Name,
			Kind:       string(item.Kind),
			Distance:   item.Distance,
			ModuleId:   item.ModuleId,
			Confidence: item.Confidence,
		}
		if item.Location != nil {
			ri.Location = &LocationInfo{
				FileId:      item.Location.FileId,
				StartLine:   item.Location.StartLine,
				StartColumn: item.Location.StartColumn,
			}
		}
		if item.Visibility != nil {
			ri.Visibility = &VisibilityInfo{
				Visibility: string(item.Visibility.Visibility),
				Confidence: item.Visibility.Confidence,
				Source:     item.Visibility.Source,
			}
		}
		result = append(result, ri)
	}

	return result
}

// convertModuleImpacts converts module impacts to response format.
func convertModuleImpacts(modules []impact.ModuleSummary) []ModuleImpact {
	result := make([]ModuleImpact, 0, len(modules))

	for _, m := range modules {
		result = append(result, ModuleImpact{
			ModuleId:    m.ModuleId,
			Name:        m.Name,
			ImpactCount: m.ImpactCount,
		})
	}

	return result
}

// convertRiskScore converts risk score to response format.
func convertRiskScore(r *impact.RiskScore) *RiskScore {
	if r == nil {
		return &RiskScore{
			Level:       "unknown",
			Score:       0,
			Explanation: "Unable to compute risk score",
		}
	}

	factors := make([]RiskFactor, 0, len(r.Factors))
	for _, f := range r.Factors {
		factors = append(factors, RiskFactor{
			Name:   f.Name,
			Value:  f.Value,
			Weight: f.Weight,
		})
	}

	return &RiskScore{
		Level:       string(r.Level),
		Score:       r.Score,
		Explanation: r.Explanation,
		Factors:     factors,
	}
}

// sortImpactItems sorts impact items by priority.
func sortImpactItems(items []ImpactItem) {
	kindPriority := map[string]int{
		"direct-caller":     1,
		"transitive-caller": 2,
		"type-dependency":   3,
		"test-dependency":   4,
		"unknown":           5,
	}

	sort.Slice(items, func(i, j int) bool {
		pi := kindPriority[items[i].Kind]
		pj := kindPriority[items[j].Kind]
		if pi != pj {
			return pi < pj
		}
		if items[i].Confidence != items[j].Confidence {
			return items[i].Confidence > items[j].Confidence
		}
		return items[i].StableId < items[j].StableId
	})
}

// getDocsToUpdate returns documentation that may need updating when a symbol changes (v7.3).
func (e *Engine) getDocsToUpdate(symbolID string, limit int) []DocToUpdate {
	refs, err := e.GetDocsForSymbol(symbolID, limit)
	if err != nil || len(refs) == 0 {
		return nil
	}

	result := make([]DocToUpdate, 0, len(refs))
	for _, ref := range refs {
		doc := DocToUpdate{
			Path: ref.DocPath,
			Line: ref.Line,
		}
		// Try to get the document title
		if docInfo, err := e.GetDocumentInfo(ref.DocPath); err == nil && docInfo != nil {
			doc.Title = docInfo.Title
		}
		// Add context if available (truncate to reasonable length)
		if ref.Context != "" {
			ctx := ref.Context
			if len(ctx) > 80 {
				ctx = ctx[:77] + "..."
			}
			doc.Context = ctx
		}
		result = append(result, doc)
	}

	return result
}

// AnalyzeChangeSetOptions contains options for AnalyzeChangeSet.
type AnalyzeChangeSetOptions struct {
	DiffContent     string // Raw git diff content (if empty, uses git to get current diff)
	Staged          bool   // If true, analyze only staged changes (--cached)
	BaseBranch      string // Base branch for comparison (default: HEAD)
	TransitiveDepth int    // Max depth for transitive impact (default: 2)
	IncludeTests    bool   // Include test files in analysis
	Strict          bool   // Fail if index is stale
}

// AnalyzeChangeSetResponse is the response for AnalyzeChangeSet.
type AnalyzeChangeSetResponse struct {
	Summary         *ChangeSummary      `json:"summary"`
	ChangedSymbols  []ChangedSymbolInfo `json:"changedSymbols"`
	AffectedSymbols []ImpactItem        `json:"affectedSymbols"`
	ModulesAffected []ModuleImpact      `json:"modulesAffected"`
	BlastRadius     *BlastRadiusSummary `json:"blastRadius,omitempty"`
	RiskScore       *RiskScore          `json:"riskScore"`
	Recommendations []Recommendation    `json:"recommendations,omitempty"`
	IndexStaleness  *IndexStalenessInfo `json:"indexStaleness,omitempty"`
	Truncated       bool                `json:"truncated,omitempty"`
	TruncationInfo  *TruncationInfo     `json:"truncationInfo,omitempty"`
	Provenance      *Provenance         `json:"provenance"`
	Drilldowns      []output.Drilldown  `json:"drilldowns,omitempty"`

	// Change describes what was diffed: branch, base ref, and mode. Added for
	// assessChange/`ckb changes` (analyzeChange's grown-up form).
	Change *ChangeInfo `json:"change,omitempty"`
	// AffectedTests are tests that reach the changed symbols (direct or
	// transitive), derived via deriveAffectedTests (shared with GetAffectedTests).
	AffectedTests []AffectedTest `json:"affectedTests,omitempty"`
	// TestGaps summarizes direct consumers of changed symbols that no test
	// appears to reach within a depth-2 call graph traversal.
	TestGaps *TestGapsInfo `json:"testGaps,omitempty"`
	// Contracts flags changed symbols that are public/exported, as a
	// "possible contract change" heuristic. This is deliberately not called
	// a "breaking change" -- it is a line-level, visibility-only signal.
	Contracts []ContractSignal `json:"contracts,omitempty"`
	// Decisions are ADRs related to any changed symbol's module, collected
	// from each per-symbol AnalyzeImpact call and deduped by ADR ID.
	Decisions []RelatedDecision `json:"decisions,omitempty"`
	// Reviewers are suggested reviewers for the changed files (ownership-based).
	Reviewers []SuggestedReview `json:"reviewers,omitempty"`
	// Confidence summarizes result quality (SCIP availability/freshness,
	// symbol-mapping confidence). Mirrors the shape of envelope.Confidence;
	// see ChangeConfidence's doc comment for why it isn't that type directly.
	Confidence *ChangeConfidence `json:"confidence,omitempty"`
}

// ChangeInfo describes the diff that was analyzed.
type ChangeInfo struct {
	Branch       string `json:"branch,omitempty"`
	Base         string `json:"base,omitempty"`
	Mode         string `json:"mode"` // "working-tree" | "staged" | "range"
	FilesChanged int    `json:"filesChanged"`
}

// ContractSignal flags a changed symbol whose visibility suggests its public
// contract may have shifted. v1 is a heuristic (exported symbol touched); a
// real signature-diff basis is planned (see plan step 2c / Cartographer
// diff_skeleton). Never call this a "breaking change" -- it is not verified.
type ContractSignal struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Kind   string `json:"kind"`  // "possible-contract-change"
	Basis  string `json:"basis"` // "heuristic: exported/public symbol lines changed" | "signature"
}

// TestGapsInfo summarizes direct consumers of changed symbols with no
// detected reaching test (call graph, depth 2).
type TestGapsInfo struct {
	UntestedConsumers int      `json:"untestedConsumers"`
	Examples          []string `json:"examples,omitempty"` // "Consumer -> changed symbol", up to 5
}

// ChangeConfidenceFactor explains one component of a ChangeConfidence score.
// Field shape mirrors envelope.ConfidenceFactor.
type ChangeConfidenceFactor struct {
	Factor string  `json:"factor"`
	Status string  `json:"status"`
	Impact float64 `json:"impact"`
}

// ChangeConfidence describes AnalyzeChangeSet's result quality. It
// deliberately mirrors envelope.Confidence's shape (Score/Tier/Reasons)
// rather than embedding that type: internal/envelope imports internal/query
// (builder.go's FromProvenance takes a *query.Provenance), so internal/query
// importing internal/envelope back would be an import cycle. internal/mcp,
// which imports both packages, converts this to envelope.Confidence when
// wrapping the assessChange/analyzeChange tool response.
type ChangeConfidence struct {
	Score   float64                  `json:"score"`
	Tier    string                   `json:"tier"` // "high" | "medium" | "low" | "speculative"
	Reasons []string                 `json:"reasons,omitempty"`
	Factors []ChangeConfidenceFactor `json:"factors,omitempty"`
}

// changeScoreToTier mirrors envelope.ScoreToTier's thresholds without
// importing internal/envelope (see ChangeConfidence doc comment).
func changeScoreToTier(score float64) string {
	switch {
	case score >= 0.95:
		return "high"
	case score >= 0.70:
		return "medium"
	case score >= 0.30:
		return "low"
	default:
		return "speculative"
	}
}

// ChangeSummary provides a high-level overview of a change set.
type ChangeSummary struct {
	FilesChanged         int    `json:"filesChanged"`
	SymbolsChanged       int    `json:"symbolsChanged"`
	DirectlyAffected     int    `json:"directlyAffected"`
	TransitivelyAffected int    `json:"transitivelyAffected"`
	EstimatedRisk        string `json:"estimatedRisk"` // "low", "medium", "high", "critical"
}

// ChangedSymbolInfo represents a symbol affected by a code change.
type ChangedSymbolInfo struct {
	SymbolID   string  `json:"symbolId"`
	Name       string  `json:"name"`
	File       string  `json:"file"`
	ChangeType string  `json:"changeType"` // "added", "modified", "deleted"
	Lines      []int   `json:"lines,omitempty"`
	Confidence float64 `json:"confidence"`
}

// Recommendation suggests an action based on impact analysis.
type Recommendation struct {
	Type     string `json:"type"`     // "coverage", "review", "split", "test"
	Severity string `json:"severity"` // "info", "warning", "error"
	Message  string `json:"message"`
	Action   string `json:"action,omitempty"`
	// Source names the finding this recommendation derives from (e.g.
	// "risk", "module-spread", "test-gaps", "contracts"), so callers can
	// trace generic text back to concrete evidence.
	Source string `json:"source,omitempty"`
}

// IndexStalenessInfo provides information about SCIP index freshness.
type IndexStalenessInfo struct {
	IsStale          bool   `json:"isStale"`
	CommitsBehind    int    `json:"commitsBehind,omitempty"`
	IndexedCommit    string `json:"indexedCommit,omitempty"`
	HeadCommit       string `json:"headCommit,omitempty"`
	StalenessMessage string `json:"stalenessMessage,omitempty"`
}

// AnalyzeChangeSet analyzes the impact of a set of code changes (from git diff).
func (e *Engine) AnalyzeChangeSet(ctx context.Context, opts AnalyzeChangeSetOptions) (*AnalyzeChangeSetResponse, error) {
	startTime := time.Now()

	// Default options
	if opts.TransitiveDepth <= 0 {
		opts.TransitiveDepth = 2
	}
	if opts.BaseBranch == "" {
		opts.BaseBranch = "HEAD"
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Check index staleness
	var indexStaleness *IndexStalenessInfo
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		indexInfo := e.scipAdapter.GetIndexInfo()
		if indexInfo != nil && indexInfo.Freshness != nil {
			indexStaleness = &IndexStalenessInfo{
				IsStale:          indexInfo.Freshness.IsStale(),
				CommitsBehind:    indexInfo.Freshness.CommitsBehindHead,
				IndexedCommit:    indexInfo.IndexedCommit,
				HeadCommit:       repoState.HeadCommit,
				StalenessMessage: indexInfo.Freshness.Warning,
			}
			if opts.Strict && indexStaleness.IsStale {
				return nil, errors.NewCkbError(
					errors.IndexStale,
					fmt.Sprintf("SCIP index is stale: %s", indexStaleness.StalenessMessage),
					nil,
					[]errors.FixAction{{
						Type:        errors.RunCommand,
						Command:     "ckb index",
						Safe:        true,
						Description: "Rebuild the SCIP index",
					}},
					nil,
				)
			}
		}
	}

	// Get diff content
	diffContent := opts.DiffContent
	if diffContent == "" {
		var diffErr error
		diffContent, diffErr = e.getGitDiff(opts.Staged, opts.BaseBranch)
		if diffErr != nil {
			return nil, e.wrapError(diffErr, errors.InternalError)
		}
	}

	// Parse the diff
	parser := diff.NewGitDiffParser()
	parsedDiff, err := parser.Parse(diffContent)
	if err != nil {
		return nil, errors.NewCkbError(
			errors.InternalError,
			fmt.Sprintf("Failed to parse diff: %v", err),
			err, nil, nil,
		)
	}

	// Filter to source files only
	parsedDiff = diff.FilterSourceFiles(parsedDiff)

	// Map changes to symbols
	var changedSymbols []impact.ChangedSymbol
	var symbolIndex diff.SymbolIndex

	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		scipIndex := e.scipAdapter.GetIndex()
		if scipIndex != nil {
			symbolIndex = diff.NewSCIPSymbolIndex(scipIndex)
		}
	}

	if symbolIndex != nil {
		mapper := diff.NewDiffSymbolMapper(symbolIndex)
		changedSymbols, err = mapper.MapToSymbols(parsedDiff)
		if err != nil {
			e.logger.Warn("Failed to map diff to symbols",
				"error", err.Error(),
			)
			// Continue with file-level analysis
		}
	} else {
		// Create file-level entries when no index available
		for _, file := range parsedDiff.Files {
			path := diff.GetEffectivePath(&file)
			changeType := impact.ChangeModified
			if file.IsNew {
				changeType = impact.ChangeAdded
			} else if file.Deleted {
				changeType = impact.ChangeDeleted
			}
			changedSymbols = append(changedSymbols, impact.ChangedSymbol{
				SymbolID:   fmt.Sprintf("file:%s", path),
				Name:       path,
				File:       path,
				ChangeType: changeType,
				Lines:      diff.GetAllChangedLines(&file),
				Confidence: 0.3,
			})
		}
	}

	// Filter out test files from changed symbols if --include-tests=false
	if !opts.IncludeTests {
		filteredSymbols := make([]impact.ChangedSymbol, 0, len(changedSymbols))
		for _, sym := range changedSymbols {
			if !isTestFilePath(sym.File) {
				filteredSymbols = append(filteredSymbols, sym)
			}
		}
		changedSymbols = filteredSymbols
	}

	// Analyze impact for each changed symbol
	var allDirectImpact []ImpactItem
	var allTransitiveImpact []ImpactItem
	moduleImpactMap := make(map[string]*ModuleImpact)
	seenAffected := make(map[string]bool)

	// v1: decisions surfaced per changed symbol's module, deduped by ADR ID, capped at 10.
	var changeDecisions []RelatedDecision
	seenDecisionIDs := make(map[string]bool)

	// v1: contracts heuristic -- exported/public changed symbols.
	var contracts []ContractSignal
	seenContractSymbols := make(map[string]bool)

	// v1: test-gap synthesis -- for each direct consumer of a changed symbol,
	// check whether any test reaches it within a depth-2 call graph. Bounded
	// to avoid O(symbols * consumers) AnalyzeImpact calls on large diffs.
	const maxTestGapConsumers = 20
	testGapConsumersChecked := 0
	seenGapConsumers := make(map[string]bool)
	untestedConsumers := 0
	var untestedExamples []string

	// Confidence input: average symbol-mapping confidence across changed symbols.
	var totalMappingConfidence float64
	var mappingConfidenceCount int

	for _, sym := range changedSymbols {
		if sym.Confidence > 0 {
			totalMappingConfidence += sym.Confidence
			mappingConfidenceCount++
		}

		// Skip file-level entries for transitive analysis
		if strings.HasPrefix(sym.SymbolID, "file:") {
			continue
		}

		impactResult, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
			SymbolId:     sym.SymbolID,
			Depth:        opts.TransitiveDepth,
			IncludeTests: opts.IncludeTests,
		})
		if err != nil {
			continue // Skip symbols that can't be analyzed
		}

		// Collect direct impact (deduplicated)
		for _, item := range impactResult.DirectImpact {
			if !seenAffected[item.StableId] {
				seenAffected[item.StableId] = true
				allDirectImpact = append(allDirectImpact, item)
			}
		}

		// Collect transitive impact (deduplicated)
		for _, item := range impactResult.TransitiveImpact {
			if !seenAffected[item.StableId] {
				seenAffected[item.StableId] = true
				allTransitiveImpact = append(allTransitiveImpact, item)
			}
		}

		// Aggregate module impacts
		for _, mod := range impactResult.ModulesAffected {
			if existing, ok := moduleImpactMap[mod.ModuleId]; ok {
				existing.ImpactCount += mod.ImpactCount
				existing.DirectCount += mod.DirectCount
			} else {
				moduleImpactMap[mod.ModuleId] = &ModuleImpact{
					ModuleId:    mod.ModuleId,
					Name:        mod.Name,
					ImpactCount: mod.ImpactCount,
					DirectCount: mod.DirectCount,
				}
			}
		}

		// Decisions: surface RelatedDecisions already computed by AnalyzeImpact
		// for this symbol's module, deduped by ADR ID, capped at 10.
		for _, d := range impactResult.RelatedDecisions {
			changeDecisions = appendDedupedDecision(changeDecisions, seenDecisionIDs, d, 10)
		}

		// Contracts heuristic: flag changed symbols whose visibility looks
		// public/exported. Deliberately not "breaking change" -- this is a
		// visibility-only, line-level signal (see plan step 2c for a
		// signature-diff basis).
		if impactResult.Symbol != nil && impactResult.Visibility != nil && !seenContractSymbols[sym.SymbolID] {
			lang := detectLanguage(sym.File)
			if signal := buildContractSignal(impactResult.Symbol.Name, impactResult.Visibility.Visibility, lang, sym.File); signal != nil {
				seenContractSymbols[sym.SymbolID] = true
				contracts = append(contracts, *signal)
			}
		}

		// Test-gap synthesis: for each direct consumer of this changed
		// symbol, check whether any test reaches it (depth 2). Bounded.
		for _, consumer := range impactResult.DirectImpact {
			if testGapConsumersChecked >= maxTestGapConsumers {
				break
			}
			if seenGapConsumers[consumer.StableId] {
				continue
			}
			seenGapConsumers[consumer.StableId] = true
			testGapConsumersChecked++

			hasTest := e.hasReachingTest(ctx, consumer)
			untestedConsumers, untestedExamples = recordTestGap(untestedConsumers, untestedExamples, consumer.Name, sym.Name, hasTest)
		}
	}

	// Convert module map to slice
	modulesAffected := make([]ModuleImpact, 0, len(moduleImpactMap))
	for _, mod := range moduleImpactMap {
		modulesAffected = append(modulesAffected, *mod)
	}
	sort.Slice(modulesAffected, func(i, j int) bool {
		return modulesAffected[i].ImpactCount > modulesAffected[j].ImpactCount
	})

	// Convert changed symbols to response format
	changedSymbolInfos := make([]ChangedSymbolInfo, len(changedSymbols))
	for i, sym := range changedSymbols {
		changedSymbolInfos[i] = ChangedSymbolInfo{
			SymbolID:   sym.SymbolID,
			Name:       sym.Name,
			File:       sym.File,
			ChangeType: string(sym.ChangeType),
			Lines:      sym.Lines,
			Confidence: sym.Confidence,
		}
	}

	// Build summary
	summary := &ChangeSummary{
		FilesChanged:         len(parsedDiff.Files),
		SymbolsChanged:       len(changedSymbols),
		DirectlyAffected:     len(allDirectImpact),
		TransitivelyAffected: len(allTransitiveImpact),
	}

	// Calculate aggregated risk score
	riskScore := e.calculateAggregatedRisk(changedSymbols, allDirectImpact, allTransitiveImpact, modulesAffected)
	summary.EstimatedRisk = riskScore.Level

	// Calculate blast radius
	uniqueFiles := make(map[string]bool)
	for _, item := range allDirectImpact {
		if item.Location != nil {
			uniqueFiles[item.Location.FileId] = true
		}
	}
	for _, item := range allTransitiveImpact {
		if item.Location != nil {
			uniqueFiles[item.Location.FileId] = true
		}
	}

	blastRadius := &BlastRadiusSummary{
		ModuleCount:       len(modulesAffected),
		FileCount:         len(uniqueFiles),
		UniqueCallerCount: len(allDirectImpact) + len(allTransitiveImpact),
		RiskLevel:         riskScore.Level,
	}

	testGapsInfo := &TestGapsInfo{
		UntestedConsumers: untestedConsumers,
		Examples:          untestedExamples,
	}

	// Generate recommendations
	recommendations := e.generateRecommendations(summary, riskScore, changedSymbols, modulesAffected, testGapsInfo, contracts)

	// Apply budget
	budget := e.compressor.GetBudget()
	var truncationInfo *TruncationInfo
	totalAffected := len(allDirectImpact) + len(allTransitiveImpact)
	if totalAffected > budget.MaxImpactItems {
		truncationInfo = &TruncationInfo{
			Reason:        "max-items",
			OriginalCount: totalAffected,
			ReturnedCount: budget.MaxImpactItems,
		}
		if len(allDirectImpact) >= budget.MaxImpactItems {
			allDirectImpact = allDirectImpact[:budget.MaxImpactItems]
			allTransitiveImpact = nil
		} else {
			remaining := budget.MaxImpactItems - len(allDirectImpact)
			if len(allTransitiveImpact) > remaining {
				allTransitiveImpact = allTransitiveImpact[:remaining]
			}
		}
	}

	// Limit modules
	if len(modulesAffected) > budget.MaxModules {
		modulesAffected = modulesAffected[:budget.MaxModules]
	}

	// Sort impact items
	sortImpactItems(allDirectImpact)
	sortImpactItems(allTransitiveImpact)

	// Combine all affected for response
	allAffected := append(allDirectImpact, allTransitiveImpact...)

	// Build provenance
	var backendContribs []BackendContribution
	var completeness CompletenessInfo
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId:    "scip",
			Available:    true,
			Used:         true,
			Completeness: 0.9,
		})
		completeness = CompletenessInfo{Score: 0.9, Reason: "scip-available"}
	} else {
		completeness = CompletenessInfo{Score: 0.3, Reason: "file-level-only"}
	}
	provenance := e.buildProvenance(repoState, "full", startTime, backendContribs, completeness)

	// Add staleness warning if applicable
	if indexStaleness != nil && indexStaleness.IsStale {
		provenance.Warnings = append(provenance.Warnings, indexStaleness.StalenessMessage)
	}

	// Change: what was diffed (branch, base, mode).
	mode := "working-tree"
	base := ""
	if opts.Staged {
		mode = "staged"
	} else if opts.BaseBranch != "" && opts.BaseBranch != "HEAD" {
		mode = "range"
		base = opts.BaseBranch
	}
	branch := ""
	if e.gitAdapter != nil {
		if b, berr := e.gitAdapter.GetCurrentBranch(); berr == nil {
			branch = b
		}
	}
	changeInfo := &ChangeInfo{
		Branch:       branch,
		Base:         base,
		Mode:         mode,
		FilesChanged: len(parsedDiff.Files),
	}

	// Reviewers: ownership-based suggestions over the unique changed files.
	var reviewers []SuggestedReview
	seenReviewerFiles := make(map[string]bool)
	var reviewerFiles []PRFileChange
	for _, sym := range changedSymbolInfos {
		if sym.File == "" || seenReviewerFiles[sym.File] {
			continue
		}
		seenReviewerFiles[sym.File] = true
		reviewerFiles = append(reviewerFiles, PRFileChange{Path: sym.File})
	}
	if e.gitAdapter != nil && len(reviewerFiles) > 0 {
		reviewers = e.getSuggestedReviewers(ctx, reviewerFiles)
	} else if len(reviewerFiles) > 0 {
		provenance.Warnings = append(provenance.Warnings, "reviewers unavailable: git ownership data not available")
	}

	// Affected tests: reuse the same traversal GetAffectedTests uses (see
	// deriveAffectedTests), fed with this call's own changed/affected symbols
	// so we don't recursively call AnalyzeChangeSet.
	affectedTests := e.deriveAffectedTests(changedSymbolInfos, allAffected)

	// Confidence: SCIP availability + freshness + average symbol-mapping confidence.
	confidence := e.buildChangeConfidence(completeness, indexStaleness, totalMappingConfidence, mappingConfidenceCount)

	return &AnalyzeChangeSetResponse{
		Summary:         summary,
		ChangedSymbols:  changedSymbolInfos,
		AffectedSymbols: allAffected,
		ModulesAffected: modulesAffected,
		BlastRadius:     blastRadius,
		RiskScore:       riskScore,
		Recommendations: recommendations,
		IndexStaleness:  indexStaleness,
		Truncated:       truncationInfo != nil,
		TruncationInfo:  truncationInfo,
		Provenance:      provenance,
		Change:          changeInfo,
		AffectedTests:   affectedTests,
		TestGaps:        testGapsInfo,
		Contracts:       contracts,
		Decisions:       changeDecisions,
		Reviewers:       reviewers,
		Confidence:      confidence,
	}, nil
}

// buildChangeConfidence computes a ChangeConfidence for AnalyzeChangeSet from
// SCIP availability/freshness and the average symbol-mapping confidence.
// Score/tier thresholds mirror envelope.ScoreToTier (see changeScoreToTier).
func (e *Engine) buildChangeConfidence(
	completeness CompletenessInfo,
	staleness *IndexStalenessInfo,
	totalMappingConfidence float64,
	mappingConfidenceCount int,
) *ChangeConfidence {
	score := completeness.Score
	var reasons []string
	var factors []ChangeConfidenceFactor

	hasSCIP := e.scipAdapter != nil && e.scipAdapter.IsAvailable()
	if hasSCIP {
		factors = append(factors, ChangeConfidenceFactor{Factor: "scip_backend", Status: "available", Impact: 0.3})
	} else {
		reasons = append(reasons, "no SCIP index: file-level mapping")
		factors = append(factors, ChangeConfidenceFactor{Factor: "scip_backend", Status: "unavailable", Impact: -0.2})
	}

	if staleness != nil && staleness.IsStale {
		score -= 0.1
		reasons = append(reasons, fmt.Sprintf("index is %d commit(s) behind", staleness.CommitsBehind))
		factors = append(factors, ChangeConfidenceFactor{Factor: "index_freshness", Status: "stale", Impact: -0.1})
	} else if hasSCIP {
		factors = append(factors, ChangeConfidenceFactor{Factor: "index_freshness", Status: "fresh", Impact: 0.0})
	}

	if mappingConfidenceCount > 0 {
		avgMapping := totalMappingConfidence / float64(mappingConfidenceCount)
		reasons = append(reasons, fmt.Sprintf("average symbol-mapping confidence %.0f%%", avgMapping*100))
		factors = append(factors, ChangeConfidenceFactor{Factor: "symbol_mapping", Status: fmt.Sprintf("%.0f%%", avgMapping*100), Impact: (avgMapping - 0.5) * 0.2})
		score = (score + avgMapping) / 2
	}

	if score < 0 {
		score = 0
	} else if score > 1 {
		score = 1
	}

	if len(reasons) == 0 {
		reasons = append(reasons, completeness.Reason)
	}

	return &ChangeConfidence{
		Score:   score,
		Tier:    changeScoreToTier(score),
		Reasons: reasons,
		Factors: factors,
	}
}

// getGitDiff gets the current git diff.
func (e *Engine) getGitDiff(staged bool, baseBranch string) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	if baseBranch != "" && baseBranch != "HEAD" {
		args = append(args, baseBranch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = e.repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	return string(out), nil
}

// bridgeMultiplierFromGraph computes a risk multiplier from Cartographer's
// betweenness-centrality scores for the changed files. Files on critical
// architectural paths (high BridgeScore) yield a larger multiplier so that
// the same textual change is reported as riskier when it lands in a bridge.
//
// The multiplier is 1.0 + max(BridgeScore)/1000, capped at 2.0. BridgeScore
// is betweenness_centrality * 1000 (range 0-1000) per api.rs, so this maps
// a "perfect bridge" to a 2x risk amplification and a non-bridge to 1x.
//
// Matches files by both Path (exact) and ModuleID, to cover the cases where
// ChangedSymbol.File is either a repo-relative path or a module identifier.
// Returns (1.0, nil) when no nodes match — callers should treat a nil factor
// as "no adjustment, skip the factor append".
func bridgeMultiplierFromGraph(nodes []cartographer.GraphNode, files []string) (float64, *RiskFactor) {
	if len(nodes) == 0 || len(files) == 0 {
		return 1.0, nil
	}
	byPath := make(map[string]float64, len(nodes))
	byModule := make(map[string]float64, len(nodes))
	for _, n := range nodes {
		if n.BridgeScore == nil {
			continue
		}
		if n.Path != "" {
			byPath[n.Path] = *n.BridgeScore
		}
		if n.ModuleID != "" {
			byModule[n.ModuleID] = *n.BridgeScore
		}
	}
	if len(byPath) == 0 && len(byModule) == 0 {
		return 1.0, nil
	}

	var maxScore float64
	matched := false
	for _, f := range files {
		if s, ok := byPath[f]; ok {
			if s > maxScore {
				maxScore = s
			}
			matched = true
			continue
		}
		if s, ok := byModule[f]; ok {
			if s > maxScore {
				maxScore = s
			}
			matched = true
		}
	}
	if !matched {
		return 1.0, nil
	}

	multiplier := math.Min(1.0+maxScore/1000.0, 2.0)
	return multiplier, &RiskFactor{
		Name:     "bridge_centrality",
		Value:    maxScore / 1000.0, // 0.0-1.0 informational
		Weight:   0,                 // applied as multiplier, not weighted mean
		Evidence: fmt.Sprintf("on a critical architectural path (bridge score %.2f)", maxScore/1000.0),
	}
}

// calculateAggregatedRisk computes an aggregated risk score for the change set.
func (e *Engine) calculateAggregatedRisk(
	changedSymbols []impact.ChangedSymbol,
	directImpact []ImpactItem,
	transitiveImpact []ImpactItem,
	modules []ModuleImpact,
) *RiskScore {
	var factors []RiskFactor

	// Factor: Number of symbols changed
	symbolCountFactor := RiskFactor{
		Name:     "symbols_changed",
		Weight:   0.2,
		Evidence: fmt.Sprintf("%d changed symbol(s)", len(changedSymbols)),
	}
	switch {
	case len(changedSymbols) > 20:
		symbolCountFactor.Value = 1.0
	case len(changedSymbols) > 10:
		symbolCountFactor.Value = 0.7
	case len(changedSymbols) > 5:
		symbolCountFactor.Value = 0.5
	default:
		symbolCountFactor.Value = 0.2
	}
	factors = append(factors, symbolCountFactor)

	// Factor: Direct impact count
	directImpactFactor := RiskFactor{
		Name:     "direct_impact",
		Weight:   0.3,
		Evidence: fmt.Sprintf("%d direct consumers", len(directImpact)),
	}
	switch {
	case len(directImpact) > 50:
		directImpactFactor.Value = 1.0
	case len(directImpact) > 20:
		directImpactFactor.Value = 0.7
	case len(directImpact) > 10:
		directImpactFactor.Value = 0.5
	default:
		directImpactFactor.Value = 0.2
	}
	factors = append(factors, directImpactFactor)

	// Factor: Transitive impact
	transitiveFactor := RiskFactor{
		Name:     "transitive_impact",
		Weight:   0.2,
		Evidence: fmt.Sprintf("%d transitive consumers", len(transitiveImpact)),
	}
	switch {
	case len(transitiveImpact) > 100:
		transitiveFactor.Value = 1.0
	case len(transitiveImpact) > 50:
		transitiveFactor.Value = 0.7
	case len(transitiveImpact) > 20:
		transitiveFactor.Value = 0.5
	default:
		transitiveFactor.Value = 0.2
	}
	factors = append(factors, transitiveFactor)

	// Factor: Module spread
	moduleFactor := RiskFactor{
		Name:     "module_spread",
		Weight:   0.3,
		Evidence: fmt.Sprintf("%d modules affected", len(modules)),
	}
	switch {
	case len(modules) > 5:
		moduleFactor.Value = 1.0
	case len(modules) > 3:
		moduleFactor.Value = 0.7
	case len(modules) > 1:
		moduleFactor.Value = 0.4
	default:
		moduleFactor.Value = 0.1
	}
	factors = append(factors, moduleFactor)

	// Calculate weighted score
	var totalWeight, weightedSum float64
	for _, f := range factors {
		totalWeight += f.Weight
		weightedSum += f.Value * f.Weight
	}

	score := 0.0
	if totalWeight > 0 {
		score = weightedSum / totalWeight
	}

	// Bridge-centrality adjustment: if any changed file sits on a critical
	// architectural path, amplify the score. See bridgeMultiplierFromGraph
	// for the multiplier shape. The graph is only fetched when the
	// cartographer build tag is on; under the stub build this is a no-op.
	bridgeAmplified := false
	if cartographer.Available() && e.repoRoot != "" {
		if graph, err := cartographer.MapProject(e.repoRoot); err == nil && graph != nil {
			files := make([]string, 0, len(changedSymbols))
			seen := make(map[string]struct{}, len(changedSymbols))
			for _, s := range changedSymbols {
				if s.File == "" {
					continue
				}
				if _, dup := seen[s.File]; dup {
					continue
				}
				seen[s.File] = struct{}{}
				files = append(files, s.File)
			}
			if mul, factor := bridgeMultiplierFromGraph(graph.Nodes, files); factor != nil {
				score = math.Min(score*mul, 1.0)
				factors = append(factors, *factor)
				bridgeAmplified = true
			}
		}
	}

	// Determine level
	var level string
	switch {
	case score >= 0.8:
		level = "critical"
	case score >= 0.6:
		level = "high"
	case score >= 0.4:
		level = "medium"
	default:
		level = "low"
	}

	// Build explanation
	explanation := fmt.Sprintf("Change affects %d symbols across %d modules with %d direct and %d transitive impacts.",
		len(changedSymbols), len(modules), len(directImpact), len(transitiveImpact))
	if bridgeAmplified {
		explanation += " Risk amplified by bridge centrality (change lands on a critical architectural path)."
	}

	return &RiskScore{
		Level:       level,
		Score:       score,
		Explanation: explanation,
		Factors:     factors,
	}
}

// GetAffectedTestsOptions contains options for GetAffectedTests.
type GetAffectedTestsOptions struct {
	DiffContent     string // Raw git diff content (if empty, uses git)
	Staged          bool   // If true, analyze only staged changes
	BaseBranch      string // Base branch for comparison (default: HEAD)
	TransitiveDepth int    // Max depth for transitive impact (default: 1)
	UseCoverage     bool   // Use coverage data if available
}

// AffectedTestsResponse contains the list of tests affected by changes.
type AffectedTestsResponse struct {
	Tests        []AffectedTest `json:"tests"`
	Summary      *TestSummary   `json:"summary"`
	CoverageUsed bool           `json:"coverageUsed"`
	Confidence   float64        `json:"confidence"`
	RunCommand   string         `json:"runCommand,omitempty"`
	Provenance   *Provenance    `json:"provenance,omitempty"`
}

// AffectedTest describes a test that should be run.
type AffectedTest struct {
	FilePath   string   `json:"filePath"`
	TestNames  []string `json:"testNames,omitempty"`  // Specific test functions if known
	Reason     string   `json:"reason"`               // "direct", "transitive", "coverage"
	AffectedBy []string `json:"affectedBy,omitempty"` // Symbol IDs that caused this test to be selected
	Confidence float64  `json:"confidence"`
}

// TestSummary provides an overview of affected tests.
type TestSummary struct {
	TotalFiles       int    `json:"totalFiles"`
	DirectFiles      int    `json:"directFiles"`
	TransitiveFiles  int    `json:"transitiveFiles"`
	CoverageFiles    int    `json:"coverageFiles"`
	EstimatedRuntime string `json:"estimatedRuntime,omitempty"`
}

// GetAffectedTests returns the list of tests affected by the current changes.
func (e *Engine) GetAffectedTests(ctx context.Context, opts GetAffectedTestsOptions) (*AffectedTestsResponse, error) {
	startTime := time.Now()

	// Default options
	if opts.TransitiveDepth <= 0 {
		opts.TransitiveDepth = 1 // Shallower default for tests
	}
	if opts.BaseBranch == "" {
		opts.BaseBranch = "HEAD"
	}

	// First, get the change set analysis (with tests included)
	changeSetOpts := AnalyzeChangeSetOptions{
		DiffContent:     opts.DiffContent,
		Staged:          opts.Staged,
		BaseBranch:      opts.BaseBranch,
		TransitiveDepth: opts.TransitiveDepth,
		IncludeTests:    true, // Important: include tests
		Strict:          false,
	}

	changeSet, err := e.AnalyzeChangeSet(ctx, changeSetOpts)
	if err != nil {
		return nil, err
	}

	var coverageUsed bool

	// Shared with AnalyzeChangeSet's AffectedTests field -- see deriveAffectedTests.
	tests := e.deriveAffectedTests(changeSet.ChangedSymbols, changeSet.AffectedSymbols)

	// Build summary
	summary := &TestSummary{TotalFiles: len(tests)}
	for _, t := range tests {
		switch t.Reason {
		case "direct":
			summary.DirectFiles++
		case "transitive":
			summary.TransitiveFiles++
		case "coverage":
			summary.CoverageFiles++
		}
	}

	// Calculate overall confidence
	var totalConf float64
	for _, t := range tests {
		totalConf += t.Confidence
	}
	avgConfidence := 0.0
	if len(tests) > 0 {
		avgConfidence = totalConf / float64(len(tests))
	}

	// Generate run command
	runCommand := generateTestRunCommand(e.repoRoot, tests)

	// Build provenance
	repoState, _ := e.GetRepoState(ctx, "fast")
	provenance := e.buildProvenance(repoState, "fast", startTime, nil, CompletenessInfo{Score: avgConfidence})

	return &AffectedTestsResponse{
		Tests:        tests,
		Summary:      summary,
		CoverageUsed: coverageUsed,
		Confidence:   avgConfidence,
		RunCommand:   runCommand,
		Provenance:   provenance,
	}, nil
}

// deriveAffectedTests computes the tests that reach a set of changed/affected
// symbols. Shared by GetAffectedTests and AnalyzeChangeSet so the traversal
// lives in one place -- AnalyzeChangeSet cannot call GetAffectedTests
// directly since GetAffectedTests itself calls AnalyzeChangeSet.
func (e *Engine) deriveAffectedTests(changedSymbols []ChangedSymbolInfo, affectedSymbols []ImpactItem) []AffectedTest {
	testFileMap := make(map[string]*AffectedTest)

	// 1. Direct test files (tests that reference changed symbols)
	for _, sym := range affectedSymbols {
		if !isTestFile(sym.Location) {
			continue
		}
		path := ""
		if sym.Location != nil {
			path = sym.Location.FileId
		} else {
			continue
		}

		if existing, ok := testFileMap[path]; ok {
			existing.AffectedBy = append(existing.AffectedBy, sym.StableId)
			if sym.Confidence > existing.Confidence {
				existing.Confidence = sym.Confidence
			}
		} else {
			testFileMap[path] = &AffectedTest{
				FilePath:   path,
				Reason:     categorizeTestReason(sym.Distance),
				AffectedBy: []string{sym.StableId},
				Confidence: sym.Confidence,
			}
		}
	}

	// 2. Test files for changed production code (heuristic: find corresponding test files)
	for _, sym := range changedSymbols {
		if isTestFilePathEnhanced(sym.File) {
			continue // Already a test file
		}

		// Find corresponding test files
		testFiles := findCorrespondingTestFiles(e.repoRoot, sym.File)
		for _, testFile := range testFiles {
			if _, ok := testFileMap[testFile]; !ok {
				testFileMap[testFile] = &AffectedTest{
					FilePath:   testFile,
					Reason:     "direct",
					AffectedBy: []string{sym.SymbolID},
					Confidence: 0.8, // High confidence for corresponding test
				}
			}
		}
	}

	// Note: coverage-based test mapping not implemented yet (requires parsing coverage files)

	// 3. LIP semantic pass: for each changed production file, find semantically
	// similar test files. This catches tests that cover the changed code but don't
	// follow naming conventions (e.g. integration tests, table-driven suites).
	for _, sym := range changedSymbols {
		if isTestFilePathEnhanced(sym.File) {
			continue
		}
		fileURI := "file://" + filepath.Join(e.repoRoot, sym.File)
		// Filter to *_test.go files only; require at least 0.6 cosine similarity.
		const lipTestMinScore = float32(0.6)
		neighbors, _ := lip.NearestByFileFiltered(fileURI, 5, "*_test.go", lipTestMinScore)
		for _, n := range neighbors {
			relPath := strings.TrimPrefix(strings.TrimPrefix(n.URI, "file://"+e.repoRoot), "/")
			if relPath == "" || relPath == sym.File {
				continue
			}
			if _, ok := testFileMap[relPath]; !ok {
				testFileMap[relPath] = &AffectedTest{
					FilePath:   relPath,
					Reason:     "semantic-proximity",
					AffectedBy: []string{sym.SymbolID},
					Confidence: float64(n.Score),
				}
			}
		}
	}

	// Convert map to slice
	tests := make([]AffectedTest, 0, len(testFileMap))
	for _, test := range testFileMap {
		// Cap affected-by list
		if len(test.AffectedBy) > 5 {
			test.AffectedBy = test.AffectedBy[:5]
		}
		tests = append(tests, *test)
	}

	// Sort by confidence then path
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].Confidence != tests[j].Confidence {
			return tests[i].Confidence > tests[j].Confidence
		}
		return tests[i].FilePath < tests[j].FilePath
	})

	return tests
}

// appendDedupedDecision appends d to decisions if its ID hasn't been seen
// (per the seen map) and the list hasn't reached capN. Pure helper factored
// out of AnalyzeChangeSet's per-symbol loop so the dedup/cap logic is
// unit-testable without a full Engine.
func appendDedupedDecision(decisions []RelatedDecision, seen map[string]bool, d RelatedDecision, capN int) []RelatedDecision {
	if len(decisions) >= capN {
		return decisions
	}
	if seen[d.ID] {
		return decisions
	}
	seen[d.ID] = true
	return append(decisions, d)
}

// buildContractSignal returns a ContractSignal for symbolName if visibility
// (refined by language-specific inference via isExportedSymbol) indicates
// it's public/exported, or nil otherwise. Pure heuristic: a visibility-only,
// line-level signal, deliberately not called a "breaking change" anywhere.
func buildContractSignal(symbolName, visibility, language, file string) *ContractSignal {
	if !isExportedSymbol(symbolName, visibility, language) {
		return nil
	}
	return &ContractSignal{
		Symbol: symbolName,
		File:   file,
		Kind:   "possible-contract-change",
		Basis:  "heuristic: exported/public symbol lines changed",
	}
}

// recordTestGap updates the untested-consumer count and capped example list
// (max 5, "Consumer -> Symbol") for one direct consumer. Pure helper so the
// counting/capping logic is unit-testable without AnalyzeImpact.
func recordTestGap(untestedCount int, examples []string, consumerName, symbolName string, hasTest bool) (int, []string) {
	if hasTest {
		return untestedCount, examples
	}
	untestedCount++
	if len(examples) < 5 {
		examples = append(examples, fmt.Sprintf("%s -> %s", consumerName, symbolName))
	}
	return untestedCount, examples
}

// hasReachingTest reports whether any test symbol reaches consumer within a
// depth-2 call graph traversal (caller direction). Used for test-gap
// synthesis: a direct consumer of a changed symbol counts as "untested" when
// no test transitively calls it. Conservative on error: if the consumer
// can't be analyzed, it is reported as unverified (counts as a gap).
func (e *Engine) hasReachingTest(ctx context.Context, consumer ImpactItem) bool {
	if consumer.Kind == "test-dependency" || isTestFile(consumer.Location) {
		return true
	}
	if consumer.StableId == "" {
		return false
	}

	result, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
		SymbolId:     consumer.StableId,
		Depth:        2,
		IncludeTests: true,
	})
	if err != nil {
		return false
	}
	for _, item := range result.DirectImpact {
		if item.Kind == "test-dependency" || isTestFile(item.Location) {
			return true
		}
	}
	for _, item := range result.TransitiveImpact {
		if item.Kind == "test-dependency" || isTestFile(item.Location) {
			return true
		}
	}
	return false
}

// isTestFile checks if a location is in a test file.
func isTestFile(loc *LocationInfo) bool {
	if loc == nil {
		return false
	}
	return isTestFilePathEnhanced(loc.FileId)
}

// isTestFilePathEnhanced checks if a file path is a test file (more comprehensive than isTestFilePath).
func isTestFilePathEnhanced(path string) bool {
	pathLower := strings.ToLower(path)

	// Go tests
	if strings.HasSuffix(pathLower, "_test.go") {
		return true
	}
	// TypeScript/JavaScript tests
	if strings.HasSuffix(pathLower, ".test.ts") || strings.HasSuffix(pathLower, ".test.js") ||
		strings.HasSuffix(pathLower, ".spec.ts") || strings.HasSuffix(pathLower, ".spec.js") ||
		strings.HasSuffix(pathLower, ".test.tsx") || strings.HasSuffix(pathLower, ".spec.tsx") {
		return true
	}
	// Python tests
	if strings.HasSuffix(pathLower, "_test.py") || strings.HasPrefix(filepath.Base(pathLower), "test_") {
		return true
	}
	// Dart tests
	if strings.HasSuffix(pathLower, "_test.dart") {
		return true
	}
	// Test directory patterns
	if strings.Contains(pathLower, "/test/") || strings.Contains(pathLower, "/tests/") ||
		strings.Contains(pathLower, "/__tests__/") || strings.Contains(pathLower, "/spec/") {
		return true
	}

	return false
}

// categorizeTestReason determines if a test is direct or transitive based on distance.
func categorizeTestReason(distance int) string {
	if distance <= 1 {
		return "direct"
	}
	return "transitive"
}

// findCorrespondingTestFiles finds test files that likely test a given source file.
func findCorrespondingTestFiles(repoRoot, sourcePath string) []string {
	var candidates []string

	ext := filepath.Ext(sourcePath)
	base := strings.TrimSuffix(filepath.Base(sourcePath), ext)
	dir := filepath.Dir(sourcePath)

	switch ext {
	case ".go":
		// Go: foo.go -> foo_test.go
		testFile := filepath.Join(repoRoot, dir, base+"_test.go")
		if fileExists(testFile) {
			candidates = append(candidates, filepath.Join(dir, base+"_test.go"))
		}
	case ".ts", ".tsx":
		// TypeScript: foo.ts -> foo.test.ts, foo.spec.ts
		for _, suffix := range []string{".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx"} {
			testFile := filepath.Join(repoRoot, dir, base+suffix)
			if fileExists(testFile) {
				candidates = append(candidates, filepath.Join(dir, base+suffix))
			}
		}
		// Also check __tests__ directory
		testsDir := filepath.Join(repoRoot, dir, "__tests__", base+".test.ts")
		if fileExists(testsDir) {
			candidates = append(candidates, filepath.Join(dir, "__tests__", base+".test.ts"))
		}
	case ".js", ".jsx":
		// JavaScript: same as TypeScript
		for _, suffix := range []string{".test.js", ".spec.js", ".test.jsx", ".spec.jsx"} {
			testFile := filepath.Join(repoRoot, dir, base+suffix)
			if fileExists(testFile) {
				candidates = append(candidates, filepath.Join(dir, base+suffix))
			}
		}
	case ".py":
		// Python: foo.py -> test_foo.py or foo_test.py
		testFile1 := filepath.Join(repoRoot, dir, "test_"+base+".py")
		testFile2 := filepath.Join(repoRoot, dir, base+"_test.py")
		if fileExists(testFile1) {
			candidates = append(candidates, filepath.Join(dir, "test_"+base+".py"))
		}
		if fileExists(testFile2) {
			candidates = append(candidates, filepath.Join(dir, base+"_test.py"))
		}
		// Check tests/ directory
		testsDir := filepath.Join(repoRoot, "tests", "test_"+base+".py")
		if fileExists(testsDir) {
			candidates = append(candidates, "tests/test_"+base+".py")
		}
	case ".dart":
		// Dart: foo.dart -> foo_test.dart (usually in test/ mirroring lib/)
		if strings.HasPrefix(sourcePath, "lib/") {
			testPath := strings.Replace(sourcePath, "lib/", "test/", 1)
			testPath = strings.TrimSuffix(testPath, ".dart") + "_test.dart"
			if fileExists(filepath.Join(repoRoot, testPath)) {
				candidates = append(candidates, testPath)
			}
		}
	}

	return candidates
}

// fileExists checks if a file exists.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// generateTestRunCommand generates a command to run the affected tests.
func generateTestRunCommand(repoRoot string, tests []AffectedTest) string {
	if len(tests) == 0 {
		return ""
	}

	// Detect test framework from file extensions
	var goTests, jsTests, pyTests, dartTests []string

	for _, t := range tests {
		ext := filepath.Ext(t.FilePath)
		switch {
		case strings.HasSuffix(t.FilePath, "_test.go"):
			goTests = append(goTests, t.FilePath)
		case ext == ".ts" || ext == ".tsx" || ext == ".js" || ext == ".jsx":
			jsTests = append(jsTests, t.FilePath)
		case ext == ".py":
			pyTests = append(pyTests, t.FilePath)
		case ext == ".dart":
			dartTests = append(dartTests, t.FilePath)
		}
	}

	// Generate command based on dominant test type
	switch {
	case len(goTests) > 0:
		// Group by package
		packages := make(map[string]bool)
		for _, t := range goTests {
			pkg := "./" + filepath.Dir(t) + "/..."
			packages[pkg] = true
		}
		pkgList := make([]string, 0, len(packages))
		for pkg := range packages {
			pkgList = append(pkgList, pkg)
		}
		sort.Strings(pkgList)
		return fmt.Sprintf("go test %s", strings.Join(pkgList, " "))

	case len(jsTests) > 0:
		// Check for common test runners
		if fileExists(filepath.Join(repoRoot, "jest.config.js")) ||
			fileExists(filepath.Join(repoRoot, "jest.config.ts")) {
			return fmt.Sprintf("npm test -- %s", strings.Join(jsTests[:min(5, len(jsTests))], " "))
		}
		return fmt.Sprintf("npx jest %s", strings.Join(jsTests[:min(5, len(jsTests))], " "))

	case len(pyTests) > 0:
		return fmt.Sprintf("pytest %s", strings.Join(pyTests[:min(10, len(pyTests))], " "))

	case len(dartTests) > 0:
		if len(dartTests) <= 3 {
			return fmt.Sprintf("flutter test %s", strings.Join(dartTests, " "))
		}
		return "flutter test"
	}

	return ""
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// hasUnrelatedModuleChanges checks if changes span multiple unrelated modules.
// Returns true only when:
// 1. Changes touch 2+ files
// 2. Impact affects 3+ distinct modules (suggesting broad, disconnected changes)
func hasUnrelatedModuleChanges(changedSymbols []impact.ChangedSymbol, modules []ModuleImpact) bool {
	// Count distinct files changed
	changedFiles := make(map[string]bool)
	for _, sym := range changedSymbols {
		if sym.File != "" {
			changedFiles[sym.File] = true
		}
	}

	// Single file = cohesive by definition
	if len(changedFiles) <= 1 {
		return false
	}

	// Need 3+ affected modules to suggest unrelated changes
	// (2 modules could easily be caller/callee)
	return len(modules) >= 3
}

// generateRecommendations creates actionable recommendations based on impact analysis.
func (e *Engine) generateRecommendations(
	summary *ChangeSummary,
	risk *RiskScore,
	changedSymbols []impact.ChangedSymbol,
	modules []ModuleImpact,
	testGaps *TestGapsInfo,
	contracts []ContractSignal,
) []Recommendation {
	var recs []Recommendation

	// Recommend review for high-risk changes
	if risk.Level == "high" || risk.Level == "critical" {
		recs = append(recs, Recommendation{
			Type:     "review",
			Severity: "warning",
			Message:  fmt.Sprintf("High-risk change affecting %d modules. Consider additional code review.", len(modules)),
			Action:   "Request review from module owners",
			Source:   "risk",
		})
	}

	// Recommend splitting when changes span unrelated modules
	if hasUnrelatedModuleChanges(changedSymbols, modules) {
		recs = append(recs, Recommendation{
			Type:     "split",
			Severity: "info",
			Message:  fmt.Sprintf("Changes affect %d distinct modules. Consider splitting into focused PRs.", len(modules)),
			Action:   "Group related changes into separate PRs",
			Source:   "module-spread",
		})
	}

	// Check for low-confidence mappings
	lowConfidenceCount := 0
	for _, sym := range changedSymbols {
		if sym.Confidence < 0.5 {
			lowConfidenceCount++
		}
	}
	if lowConfidenceCount > 0 && lowConfidenceCount > len(changedSymbols)/2 {
		recs = append(recs, Recommendation{
			Type:     "coverage",
			Severity: "info",
			Message:  fmt.Sprintf("%d symbols have low mapping confidence. Index may be stale.", lowConfidenceCount),
			Action:   "Run 'ckb index' to refresh the SCIP index",
			Source:   "symbol-mapping-confidence",
		})
	}

	// Recommend running tests if transitive impact is significant
	if summary.TransitivelyAffected > 20 {
		recs = append(recs, Recommendation{
			Type:     "test",
			Severity: "warning",
			Message:  fmt.Sprintf("Significant transitive impact (%d symbols). Run comprehensive test suite.", summary.TransitivelyAffected),
			Action:   "Run full test suite before merging",
			Source:   "transitive-impact",
		})
	}

	// Recommend adding tests for untested downstream consumers
	if testGaps != nil && testGaps.UntestedConsumers > 0 {
		recs = append(recs, Recommendation{
			Type:     "test",
			Severity: "warning",
			Message:  fmt.Sprintf("%d downstream consumer(s) have no reaching test.", testGaps.UntestedConsumers),
			Action:   "Add or extend tests covering the affected consumers",
			Source:   "test-gaps",
		})
	}

	// Flag possible contract changes for extra review (heuristic, not verified)
	if len(contracts) > 0 {
		recs = append(recs, Recommendation{
			Type:     "review",
			Severity: "info",
			Message:  fmt.Sprintf("%d possible contract change(s) (heuristic: exported symbol lines changed).", len(contracts)),
			Action:   "Double-check API/consumer compatibility for the flagged symbols",
			Source:   "contracts",
		})
	}

	return recs
}

// fetchLIPEnrichment pulls blast-radius enrichment for a symbol from LIP via
// the best available RPC. Preference order:
//  1. query_blast_radius_symbol (v2.3+) — direct symbol-URI call, no filtering.
//  2. query_blast_radius_batch (v2.2) — fetch all symbols in the file, then
//     LookupSymbol by name as a fallback.
//
// Returns nil when the symbol lacks a location, LIP is unavailable, or the
// daemon doesn't support either RPC — callers should treat nil as "skip
// enrichment" and fall back to SCIP-only results unchanged.
func (e *Engine) fetchLIPEnrichment(symbolInfo *SymbolInfo) *impact.ExternalBlastRadius {
	if symbolInfo == nil || symbolInfo.Location == nil {
		return nil
	}
	switch {
	case e.lipSupports("query_blast_radius_symbol"):
		// Prefer the SCIP-form URI for symbols imported from SCIP (the common
		// case — CKB's StableID is the raw scip-go symbol). Fall back to the
		// Tier-1 `lip://local/<file>#<name>` form for symbols that don't
		// round-trip through SCIP.
		symURI := lip.SCIPSymbolToURI(symbolInfo.StableId)
		if symURI == "" || !strings.HasPrefix(symURI, "lip://") {
			symURI = "lip://local/" + symbolInfo.Location.FileId + "#" + symbolInfo.Name
		}
		if entry, _ := lip.QueryBlastRadiusSymbol(symURI, 0.6); entry != nil {
			return lip.EntryToExternal(entry)
		}
	case e.lipSupports("query_blast_radius_batch"):
		fileURI := "lip://local/" + symbolInfo.Location.FileId
		if lipEntries, _ := lip.QueryBlastRadiusBatch([]string{fileURI}, 0.6); lipEntries != nil {
			converted := make(map[string]*impact.ExternalBlastRadius, len(lipEntries.Entries))
			for k, v := range lipEntries.Entries {
				vCopy := v
				converted[k] = lip.EntryToExternal(&vCopy)
			}
			if ext, ok := lip.LookupSymbol(converted, symbolInfo.Location.FileId, symbolInfo.Name); ok {
				return ext
			}
		}
	}
	return nil
}
