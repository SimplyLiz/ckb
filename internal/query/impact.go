package query

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// AllItems skips the item budget, for a caller aggregating several symbols that
	// applies its own. ConsumerFiles is complete either way.
	AllItems bool
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
	// TestImpact holds the test code using the symbol when IncludeTests is off. It
	// is kept out of DirectImpact, TransitiveImpact and everything counted from
	// them, so a well-tested symbol doesn't look like a widely used one.
	TestImpact []ImpactItem `json:"-"`
	// ConsumerFiles lists every file behind the item lists, which are budgeted.
	ConsumerFiles  *ConsumerFiles     `json:"consumerFiles,omitempty"`
	Truncated      bool               `json:"truncated,omitempty"`
	TruncationInfo *TruncationInfo    `json:"truncationInfo,omitempty"`
	Provenance     *Provenance        `json:"provenance"`
	Drilldowns     []output.Drilldown `json:"drilldowns,omitempty"`
}

// MaxConsumerFiles bounds ConsumerFiles. A path is about 50 bytes, so this is at
// most some 15 KB, or 3.7k tokens, against 20 items at 200 bytes each that say less.
const MaxConsumerFiles = 300

// ConsumerFiles is the list of files to look at when a symbol changes: unlike the
// consumer items it is not budgeted down to a sample, because a path is cheap and a
// missing one is a file nobody checks. Tests has every test file using or reaching
// the symbol, whether or not test code counts as a consumer.
type ConsumerFiles struct {
	Direct     []string `json:"direct"`               // Reference a changed symbol themselves
	Transitive []string `json:"transitive,omitempty"` // Reach one only through other code
	Tests      []string `json:"tests,omitempty"`
	// ContractChanged is the part of Direct to look at first: files with a use the change
	// left as it was of a symbol whose declaration it changed or removed, whether or not
	// the change touched the file elsewhere. They are likely to break. Heuristic:
	// declarations are compared as text, not types.
	ContractChanged      []string `json:"contractChanged,omitempty"`
	ContractChangedTests []string `json:"contractChangedTests,omitempty"`
	Total                int      `json:"total"` // Files found, including any cut from these lists
	// Truncated means the lists are not the whole answer, and Note says why.
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// BuildConsumerFiles lists the files of consumers. A file is listed once, at its
// closest. If the list has to be cut, transitive files go first. incomplete says
// what the lookup itself did not cover, if anything.
func BuildConsumerFiles(direct, transitive, tests []ImpactItem, incomplete string) *ConsumerFiles {
	seen := make(map[string]bool)
	collect := func(groups ...[]ImpactItem) []string {
		var paths []string
		for _, items := range groups {
			for i := range items {
				if items[i].Location == nil || items[i].Location.FileId == "" || seen[items[i].Location.FileId] {
					continue
				}
				seen[items[i].Location.FileId] = true
				paths = append(paths, items[i].Location.FileId)
			}
		}
		sort.Strings(paths)
		return paths
	}
	splitTests := func(items []ImpactItem) (production, inTests []ImpactItem) {
		for i := range items {
			if isTestFile(items[i].Location) {
				inTests = append(inTests, items[i])
			} else {
				production = append(production, items[i])
			}
		}
		return production, inTests
	}
	directProd, directTests := splitTests(direct)
	transitiveProd, transitiveTests := splitTests(transitive)

	files := &ConsumerFiles{Direct: collect(directProd)}
	files.Tests = collect(directTests, tests, transitiveTests)
	files.Transitive = collect(transitiveProd)
	if files.Direct == nil {
		files.Direct = []string{}
	}
	files.Total = len(files.Direct) + len(files.Transitive) + len(files.Tests)

	notes := make([]string, 0, 2)
	if files.Total > MaxConsumerFiles {
		room := MaxConsumerFiles
		cut := func(paths []string) []string {
			if len(paths) > room {
				paths = paths[:room]
			}
			room -= len(paths)
			return paths
		}
		files.Direct = cut(files.Direct)
		files.Tests = cut(files.Tests)
		files.Transitive = cut(files.Transitive)
		notes = append(notes, fmt.Sprintf("showing %d of %d files", MaxConsumerFiles, files.Total))
	}
	if incomplete != "" {
		notes = append(notes, incomplete)
	}
	if len(notes) > 0 {
		files.Truncated = true
		files.Note = strings.Join(notes, "; ")
	}
	return files
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
	// StableId and ModuleId are NOT omitempty: schemaVersion 1 documents
	// them as always-present strings, so an unresolved value serializes
	// as "" rather than dropping the key (see PrepareDependent for the
	// same contract).
	StableId   string          `json:"stableId"`
	Name       string          `json:"name,omitempty"`
	Kind       string          `json:"kind"` // direct-caller, transitive-caller, type-dependency, test-dependency
	Distance   int             `json:"distance"`
	ModuleId   string          `json:"moduleId"`
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
	RiskLevel         string `json:"riskLevel"`         // same value as riskScore.level, kept for consumers reading it here

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
	nodes   map[string]*scip.CallGraphNode // callers seen by the last GetTransitiveCallers
	// separateTests moves callers in test files out of the result into testCallers.
	separateTests bool
	testCallers   map[string]int
	nodeLimitHit  bool // The call graph stopped at transitiveCallerNodes
}

// transitiveCallerNodes bounds the call graph walked per symbol, for performance.
const transitiveCallerNodes = 100

// CallerLocation implements impact.CallerLocator. The module is the directory
// of the caller's file, the same derivation used for direct references.
func (p *scipCallerProvider) CallerLocation(callerId string) (string, *impact.Location, bool) {
	n := p.nodes[callerId]
	if n == nil || n.Location == nil || n.Location.FileId == "" {
		return "", nil, false
	}
	return filepath.Dir(n.Location.FileId), &impact.Location{
		FileId:    n.Location.FileId,
		StartLine: n.Location.StartLine + 1,
	}, true
}

// GetTransitiveCallers implements impact.TransitiveCallerProvider
func (p *scipCallerProvider) GetTransitiveCallers(symbolId string, maxDepth int) (map[string]int, error) {
	graph, err := p.adapter.BuildCallGraph(symbolId, scip.CallGraphOptions{
		Direction: scip.DirectionCallers,
		MaxDepth:  maxDepth,
		MaxNodes:  transitiveCallerNodes,
	})
	if err != nil {
		return nil, err
	}

	result := make(map[string]int)
	if graph == nil {
		return result, nil
	}
	p.nodes = graph.Nodes
	p.nodeLimitHit = len(graph.Nodes) >= transitiveCallerNodes

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

	if p.separateTests {
		p.testCallers = make(map[string]int)
		for id, depth := range result {
			if n := graph.Nodes[id]; n != nil && n.Location != nil && isTestFilePath(n.Location.FileId) {
				p.testCallers[id] = depth
				delete(result, id)
			}
		}
	}

	return result, nil
}

// testCallerItems describes the transitive callers GetTransitiveCallers set aside.
// Direct ones come from the references, which also know the file-level uses.
func (p *scipCallerProvider) testCallerItems() []ImpactItem {
	items := make([]ImpactItem, 0, len(p.testCallers))
	for id, depth := range p.testCallers {
		if depth <= 1 {
			continue
		}
		n := p.nodes[id]
		items = append(items, ImpactItem{
			StableId:   id,
			Name:       n.Name,
			Kind:       string(impact.TestDependency),
			Distance:   depth,
			ModuleId:   filepath.Dir(n.Location.FileId),
			Location:   &LocationInfo{FileId: n.Location.FileId, StartLine: n.Location.StartLine + 1},
			Confidence: 0.85,
		})
	}
	return items
}

// maxImpactReferences bounds the references read per symbol; past it,
// ConsumerFiles says the list is partial.
const maxImpactReferences = 5000

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

	// Search without a SCIP index hands out tree-sitter IDs, which are hashes of a
	// position: they name nothing here, and suggesting another search would only
	// produce the same ID.
	if symbolInfo == nil && strings.HasPrefix(opts.SymbolId, treesitterIDPrefix) {
		return nil, errors.NewCkbError(
			errors.IndexMissing,
			fmt.Sprintf("Impact analysis needs a SCIP index, and %s came from a tree-sitter search without one", opts.SymbolId),
			nil,
			errors.GetSuggestedFixes(errors.IndexMissing),
			nil,
		)
	}

	// If we still don't have symbol info, return not found
	if symbolInfo == nil {
		return nil, errors.NewCkbError(
			errors.SymbolNotFound,
			e.notFoundMessage(opts.SymbolId),
			nil, nil, nil,
		)
	}

	// Find references for impact analysis
	var refs []impact.Reference
	var incomplete []string
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		refOpts := backends.RefOptions{
			MaxResults:   maxImpactReferences,
			IncludeTests: true, // Set aside below if not wanted as consumers
		}
		refsResult, refsErr := e.scipAdapter.FindReferences(ctx, symbolIdForLookup, refOpts)
		if refsErr == nil && refsResult != nil {
			if refsResult.TotalReferences > len(refsResult.References) {
				incomplete = append(incomplete, fmt.Sprintf("looked at the first %d of %d references",
					len(refsResult.References), refsResult.TotalReferences))
			}
			for _, ref := range refsResult.References {
				fromModule := ""
				if ref.Location.Path != "" {
					fromModule = filepath.Dir(ref.Location.Path)
				}
				impactRef := impact.Reference{
					Kind: impact.ReferenceKind(ref.Kind),
					Location: &impact.Location{
						FileId:    ref.Location.Path,
						StartLine: ref.Location.Line,
					},
					IsTest: isTestFilePath(ref.Location.Path),
					// FromSymbol/FromName resolve the enclosing symbol (e.g. the
					// caller function containing this reference) when the backend
					// found one. Left empty (not "unknown") when genuinely
					// unresolvable, e.g. a package-level reference outside any
					// function.
					FromSymbol: ref.FromSymbol,
					FromName:   ref.FromSymbolName,
					// FromModule is derived from the reference's own file path,
					// the same way resolvePrepareTarget derives ModuleId for a
					// symbol-based prepareChange target: the backend doesn't
					// resolve a module id on references, so without this every
					// direct dependent reported an empty moduleId and the
					// module-spread count in prepareChange silently undercounted.
					FromModule: fromModule,
				}
				refs = append(refs, impactRef)
			}
		}
	}

	// Without IncludeTests, test code isn't counted as a consumer, but it still uses
	// the symbol and is reported as such.
	var testRefs []impact.Reference
	if !opts.IncludeTests {
		refs, testRefs = splitTestReferences(refs)
	}

	// Create impact analyzer with transitive caller support if SCIP available
	var analyzer *impact.ImpactAnalyzer
	var callerProv *scipCallerProvider
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		callerProv = &scipCallerProvider{adapter: e.scipAdapter, separateTests: !opts.IncludeTests}
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

	testImpact := testReferenceItems(testRefs, impactSymbol)
	if callerProv != nil {
		testImpact = append(testImpact, callerProv.testCallerItems()...)
		if callerProv.nodeLimitHit {
			incomplete = append(incomplete, fmt.Sprintf("stopped following callers after %d symbols", transitiveCallerNodes))
		}
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

	consumerFiles := BuildConsumerFiles(directImpact, transitiveImpact, testImpact, strings.Join(incomplete, "; "))

	// Sort by impact priority, so the budget below keeps the ones that matter most
	sortImpactItems(directImpact)
	sortImpactItems(transitiveImpact)

	// Apply budget
	budget := e.compressor.GetBudget()
	var truncationInfo *TruncationInfo
	totalItems := len(directImpact) + len(transitiveImpact)
	if !opts.AllItems && totalItems > budget.MaxImpactItems {
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

	// Reached on identity data alone: the symbol is known, but no references were
	// looked up, so a low score here would be a statement about nothing.
	consumersNotAssessed := !e.hasConsumerGraph() && len(refs) == 0
	if consumersNotAssessed {
		riskScore = notAssessedRisk(1)
	}

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

	// One level per answer. The analyzer classifies the blast radius by its
	// counts alone, which disagreed with riskScore in the same response (medium
	// next to high, low next to medium). prepareChange already reports the
	// score's level here; the counts stay as they are.
	if blastRadius != nil {
		blastRadius.RiskLevel = riskScore.Level
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
		TestImpact:        testImpact,
		ConsumerFiles:     consumerFiles,
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

// splitTestReferences separates references from test code.
func splitTestReferences(refs []impact.Reference) (production, tests []impact.Reference) {
	production = make([]impact.Reference, 0, len(refs))
	for _, ref := range refs {
		if ref.IsTest {
			tests = append(tests, ref)
		} else {
			production = append(production, ref)
		}
	}
	return production, tests
}

// testReferenceItems describes direct references from test code.
func testReferenceItems(refs []impact.Reference, symbol *impact.Symbol) []ImpactItem {
	items := make([]impact.ImpactItem, 0, len(refs))
	for i := range refs {
		ref := &refs[i]
		kind, confidence := impact.ClassifyImpactWithConfidence(ref, symbol)
		items = append(items, impact.ImpactItem{
			StableId:   ref.FromSymbol,
			Name:       ref.FromName,
			Kind:       kind,
			Confidence: confidence,
			ModuleId:   ref.FromModule,
			Location:   ref.Location,
			Distance:   1,
		})
	}
	return convertImpactItems(items)
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
	Summary *ChangeSummary `json:"summary"`
	// DoneCheck says which uses of a changed declaration the diff left untouched.
	// It and CoChangeCheck come right after the summary on purpose: they are
	// what an agent has to act on, and a client that keeps only the head of a
	// large result (Claude Code moves results over its size limit to a file and
	// shows a preview) must still show them.
	DoneCheck *DoneCheck `json:"doneCheck,omitempty"`
	// CoChangeCheck says which files usually change with the touched files and were
	// left alone, from git history alone.
	CoChangeCheck   *CoChangeCheck      `json:"coChangeCheck,omitempty"`
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
	// ConsumerFiles lists every file using or reaching a changed symbol, while
	// AffectedSymbols is budgeted to a detailed sample of them.
	ConsumerFiles *ConsumerFiles `json:"consumerFiles,omitempty"`
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

// ContractSignal flags a changed symbol whose contract may have shifted. Never call
// this a "breaking change": it is a heuristic, nothing is compiled.
//
// Kind says what it rests on: "signature-changed", "definition-removed" and
// "compatible-signature-change" compare the declaration's text across the diff,
// "possible-contract-change" is visibility alone, for code it can't read, and
// "shape-changed" is a type read off a value whose keys or members changed.
type ContractSignal struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	Kind   string `json:"kind"`  // See above
	Basis  string `json:"basis"` // What it rests on, always starting "heuristic:"
	// Before and After are the declarations compared, where known.
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Detail says what changed where the declaration didn't: the keys of the schema a
	// type is read off, as `+ custom (required in output, default [])`.
	Detail string `json:"detail,omitempty"`
	// ConsumerCount counts the production files with a use the diff left as it was, and
	// Consumers names the first of them.
	ConsumerCount int      `json:"consumerCount,omitempty"`
	Consumers     []string `json:"consumers,omitempty"`
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
	EstimatedRisk        string `json:"estimatedRisk"` // "low", "medium", "high", "critical", or RiskLevelNotAssessed
}

// ChangedSymbolInfo represents a symbol affected by a code change.
type ChangedSymbolInfo struct {
	SymbolID   string  `json:"symbolId"`
	Name       string  `json:"name"`
	File       string  `json:"file"`
	ChangeType string  `json:"changeType"`      // "added", "modified", "deleted"
	Lines      []int   `json:"lines,omitempty"` // New-file lines; a deleted symbol's are its old-file lines
	Confidence float64 `json:"confidence"`
	// Note qualifies the entry: on a file-level entry (symbolId "file:..."), which
	// changed lines could not be attributed to a definition and why; on a symbol,
	// that only comments or whitespace changed in it.
	Note string `json:"note,omitempty"`
	// Declaration says what happened to its declaration, from comparing text:
	// "unchanged" (a body edit), "compatible", "changed" or "removed". Empty if unread.
	Declaration string `json:"declaration,omitempty"`
	// Trivial is set when only comments or whitespace changed in it.
	Trivial bool `json:"trivial,omitempty"`
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
		mapper := diff.NewDiffSymbolMapper(symbolIndex).WithFileReader(e.diffFileReader(opts.Staged, opts.BaseBranch))
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
	var allTestImpact []ImpactItem
	// testCauses maps a consumer (see impactItemKey) to the changed symbols it uses
	// or reaches, so an affected test names what selected it rather than itself.
	testCauses := make(map[string][]string)
	moduleImpactMap := make(map[string]*ModuleImpact)
	lookupNotes := make(map[string]bool)

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

	// Deleted symbols whose consumers were not touched by this diff: they don't
	// compile anymore unless something else changes.
	changedFilePaths := make(map[string]bool, len(parsedDiff.Files))
	for i := range parsedDiff.Files {
		changedFilePaths[diff.GetEffectivePath(&parsedDiff.Files[i])] = true
		if parsedDiff.Files[i].Renamed {
			changedFilePaths[parsedDiff.Files[i].OldPath] = true // Where an older index has it
		}
	}
	var brokenContracts []brokenContract
	var contractChangedFiles, contractChangedTests []string
	done := newDoneChecker(e.repoRoot, opts, parsedDiff)
	if opts.DiffContent == "" {
		done.base = e.diffOldRev(opts.Staged, opts.BaseBranch) // Read the old side the diff came from
	}
	var usesFinder referenceFinder
	if e.hasConsumerGraph() {
		usesFinder = e.scipAdapter
	}
	publicContractChanges := 0
	// Go and TypeScript types whose members changed, read from the files before the
	// symbols are looked at: a changed line inside one is explained by it.
	rustOpens := done.checkTypeFiles(ctx, usesFinder)
	// Interfaces whose method set changed: their implementations don't refer to them.
	rustOpens = append(rustOpens, done.checkImplementations(usesFinder)...)
	// TypeScript types read off a changed value with typeof: their declaration didn't move.
	rustOpens = append(rustOpens, done.checkDerivedTypes(ctx, usesFinder, changedSymbols)...)

	// Confidence input: average symbol-mapping confidence across changed symbols.
	var totalMappingConfidence float64
	var mappingConfidenceCount int

	for _, sym := range changedSymbols {
		if sym.Confidence > 0 && !sym.Trivial { // Trivial is low for another reason than mapping
			totalMappingConfidence += sym.Confidence
			mappingConfidenceCount++
		}

		if sym.DeletionUnresolved && !done.explains(sym) {
			done.unreadable = append(done.unreadable, unreadableName(sym))
		}
		if sym.NotInIndex {
			done.unindexed = append(done.unindexed, sym.File)
		}

		// Skip file-level entries for transitive analysis, and anything that only
		// changed comments or whitespace: it has no blast radius to report.
		if strings.HasPrefix(sym.SymbolID, "file:") || sym.Trivial {
			continue
		}

		impactResult, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
			SymbolId:     sym.SymbolID,
			Depth:        opts.TransitiveDepth,
			IncludeTests: opts.IncludeTests,
			AllItems:     true, // This aggregate is budgeted below, once
		})
		if err != nil {
			done.failed++
			continue // Skip symbols that can't be analyzed
		}

		declaration := symbolDeclarationChange(sym)
		if declaration == "" && sym.ChangeType != impact.ChangeAdded && !done.explains(sym) {
			done.unreadable = append(done.unreadable, unreadableName(sym))
		}
		var contractConsumers []string
		if declaration == impact.DeclChanged || declaration == impact.DeclRemoved {
			// A file in the diff only counts as handled if the diff touched its uses.
			handled := changedFilePaths
			if openFiles, ok := done.checkSymbol(ctx, usesFinder, sym, declaration); ok {
				handled = withoutFiles(changedFilePaths, openFiles)
			}
			if viaAlias := done.checkRustAlias(ctx, usesFinder, sym); len(viaAlias) > 0 {
				rustOpens = append(rustOpens, rustOpen{name: sym.Name, change: declaration, files: viaAlias})
			}
			broken := findBrokenContract(sym.Name, declaration, impactResult.DirectImpact, handled)
			if broken != nil {
				brokenContracts = append(brokenContracts, *broken)
				contractConsumers = broken.files
				contractChangedFiles = append(contractChangedFiles, broken.files...)
			}
			for i := range impactResult.TestImpact {
				item := &impactResult.TestImpact[i]
				if item.Distance == 1 && item.Location != nil && !handled[item.Location.FileId] {
					contractChangedTests = append(contractChangedTests, item.Location.FileId)
				}
			}
			for i := range impactResult.DirectImpact {
				item := &impactResult.DirectImpact[i]
				if isTestFile(item.Location) && !handled[item.Location.FileId] {
					contractChangedTests = append(contractChangedTests, item.Location.FileId)
				}
			}
			if broken == nil && impactResult.Visibility != nil &&
				(isExportedSymbol(sym.Name, impactResult.Visibility.Visibility, detectLanguage(sym.File)) || declaresPublic(sym.DeclarationBefore)) {
				publicContractChanges++
			}
		}

		for _, items := range [][]ImpactItem{impactResult.DirectImpact, impactResult.TransitiveImpact, impactResult.TestImpact} {
			for i := range items {
				if items[i].Location != nil && isRunnableTestPath(items[i].Location.FileId) {
					key := impactItemKey(&items[i])
					testCauses[key] = appendUnique(testCauses[key], sym.SymbolID)
				}
			}
		}
		allDirectImpact = append(allDirectImpact, impactResult.DirectImpact...)
		allTransitiveImpact = append(allTransitiveImpact, impactResult.TransitiveImpact...)
		allTestImpact = append(allTestImpact, impactResult.TestImpact...)
		if impactResult.ConsumerFiles != nil && impactResult.ConsumerFiles.Note != "" {
			lookupNotes[impactResult.ConsumerFiles.Note] = true
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

		// Contracts heuristic: flag what the declaration's text says changed, or for
		// code that can't be read, symbols whose visibility looks public. Deliberately
		// not "breaking change": nothing here was compiled.
		if impactResult.Symbol != nil && impactResult.Visibility != nil && !seenContractSymbols[sym.SymbolID] {
			lang := detectLanguage(sym.File)
			if signal := contractSignalFor(sym, declaration, impactResult.Symbol.Name, impactResult.Visibility.Visibility, lang, contractConsumers); signal != nil {
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
			// A use outside any definition has no callers a test could reach it through.
			if consumer.StableId == "" || seenGapConsumers[consumer.StableId] {
				continue
			}
			seenGapConsumers[consumer.StableId] = true
			testGapConsumersChecked++

			hasTest := e.hasReachingTest(ctx, consumer)
			untestedConsumers, untestedExamples = recordTestGap(untestedConsumers, untestedExamples, consumer.Name, sym.Name, hasTest)
		}
	}

	// What the changed symbols' own references can't reach. Risk and consumerFiles follow
	// the same answer as the check does for those.
	rustOpens = append(rustOpens, done.checkRustFiles(ctx, usesFinder)...)
	for _, open := range rustOpens {
		var files []string
		for path := range open.files {
			if isTestFilePath(path) {
				contractChangedTests = append(contractChangedTests, path)
			} else {
				files = append(files, path)
			}
		}
		if len(files) > 0 {
			sort.Strings(files)
			contractChangedFiles = append(contractChangedFiles, files...)
			brokenContracts = append(brokenContracts, brokenContract{name: open.name, change: open.change, consumers: files, files: files})
		}
	}

	contracts = append(contracts, done.shapes...)

	allDirectImpact, allTransitiveImpact = mergeConsumers(allDirectImpact, allTransitiveImpact)

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
			SymbolID:    sym.SymbolID,
			Name:        sym.Name,
			File:        sym.File,
			ChangeType:  string(sym.ChangeType),
			Lines:       sym.Lines,
			Confidence:  sym.Confidence,
			Note:        sym.Note,
			Declaration: string(symbolDeclarationChange(sym)),
			Trivial:     sym.Trivial,
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
	substantive := make([]impact.ChangedSymbol, 0, len(changedSymbols))
	for _, sym := range changedSymbols {
		if !sym.Trivial {
			substantive = append(substantive, sym)
		}
	}
	riskScore := e.calculateAggregatedRisk(substantive, allDirectImpact, allTransitiveImpact, modulesAffected)
	if len(substantive) == 0 && len(changedSymbols) > 0 {
		riskScore.Explanation = "Only comments or whitespace changed."
	}
	applyContractChangesToRisk(riskScore, brokenContracts, publicContractChanges)
	for _, sym := range changedSymbols {
		if sym.DeletionUnresolved {
			riskScore.Explanation += " Removed code could not be traced to its consumers, so this is a lower bound."
			break
		}
	}
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

	// Nothing in the diff: there is nothing a level could describe.
	if len(parsedDiff.Files) == 0 {
		riskScore = &RiskScore{Level: RiskLevelNotAssessed, Explanation: NothingToAssess}
		summary.EstimatedRisk = riskScore.Level
		blastRadius.RiskLevel = riskScore.Level
	}

	// Without SCIP there is no consumer graph, so "0 consumers" means nobody
	// looked, and a score built on it would read as a clean bill of health.
	// Consumers found some other way still count as a lower bound.
	if !e.hasConsumerGraph() && len(allDirectImpact)+len(allTransitiveImpact) == 0 && len(changedSymbols) > 0 {
		riskScore = notAssessedRisk(len(changedSymbols))
		summary.EstimatedRisk = riskScore.Level
		blastRadius.RiskLevel = riskScore.Level
	}

	testGapsInfo := &TestGapsInfo{
		UntestedConsumers: untestedConsumers,
		Examples:          untestedExamples,
	}

	// Generate recommendations
	recommendations := e.generateRecommendations(summary, riskScore, changedSymbols, modulesAffected, testGapsInfo, contracts)
	recommendations = append(contractRecommendations(brokenContracts, changedSymbols), recommendations...)

	// Before the budget: a cut in the consumer list must not lose a file or a test.
	consumerFiles := BuildConsumerFiles(allDirectImpact, allTransitiveImpact, allTestImpact, joinNotes(lookupNotes))
	consumerFiles.ContractChanged = boundedUniqueSorted(contractChangedFiles)
	consumerFiles.ContractChangedTests = boundedUniqueSorted(contractChangedTests)
	testConsumers := make([]ImpactItem, 0, len(allDirectImpact)+len(allTransitiveImpact)+len(allTestImpact))
	testConsumers = append(append(append(testConsumers, allDirectImpact...), allTransitiveImpact...), allTestImpact...)
	affectedTests := e.deriveAffectedTests(changedSymbolInfos, testConsumers, testCauses)
	if e.hasConsumerGraph() {
		affectedTests = mergeFileLevelTests(affectedTests, testsUsingFiles(e.scipAdapter.GetIndex(), changedSourceFiles(changedSymbols)))
	}

	// Sort impact items, so the budget below keeps the ones that matter most
	sortImpactItems(allDirectImpact)
	sortImpactItems(allTransitiveImpact)

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
	for _, sym := range changedSymbols {
		if sym.DeletionUnresolved {
			provenance.Warnings = append(provenance.Warnings, fmt.Sprintf("deleted symbols not resolved in %s: %s", sym.File, sym.Note))
		}
	}

	var staleIndex string
	if e.hasConsumerGraph() {
		staleIndex = indexDoubt(e.repoRoot, e.scipAdapter.GetIndexInfo().IndexedCommit)
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
		ConsumerFiles:   consumerFiles,
		TestGaps:        testGapsInfo,
		DoneCheck:       done.result(diffContent, e.hasConsumerGraph(), len(parsedDiff.Files) == 0, staleIndex),
		CoChangeCheck:   e.coChangeCheck(ctx, parsedDiff),
		Contracts:       contracts,
		Decisions:       changeDecisions,
		Reviewers:       reviewers,
		Confidence:      confidence,
	}, nil
}

// joinNotes joins distinct notes in a stable order.
func joinNotes(notes map[string]bool) string {
	list := make([]string, 0, len(notes))
	for note := range notes {
		list = append(list, note)
	}
	sort.Strings(list)
	return strings.Join(list, "; ")
}

// consumerKey identifies a consumer across changed symbols. A reference outside
// any definition, like an import or a package-level initializer, has no symbol to
// name, so its file stands in: those are consumers too, and two of them in
// different files are not the same one.
func consumerKey(item ImpactItem) string {
	if item.StableId != "" {
		return item.StableId
	}
	if item.Location != nil {
		return "file:" + item.Location.FileId
	}
	return "name:" + item.Name
}

// mergeConsumers dedupes the consumers of all changed symbols. Direct beats
// transitive, whichever changed symbol came first: a consumer calling one changed
// symbol and reaching another is a direct consumer. A file-level entry says nothing
// more once the file has a named consumer, which is the usual fate of an import.
func mergeConsumers(direct, transitive []ImpactItem) (mergedDirect, mergedTransitive []ImpactItem) {
	namedFiles := make(map[string]bool)
	for i := range direct {
		if direct[i].StableId != "" && direct[i].Location != nil {
			namedFiles[direct[i].Location.FileId] = true
		}
	}
	seen := make(map[string]bool, len(direct)+len(transitive))
	for _, item := range direct {
		key := consumerKey(item)
		if seen[key] || (item.StableId == "" && item.Location != nil && namedFiles[item.Location.FileId]) {
			continue
		}
		seen[key] = true
		mergedDirect = append(mergedDirect, item)
	}
	closest := make(map[string]int, len(transitive))
	for _, item := range transitive {
		key := consumerKey(item)
		if seen[key] {
			continue
		}
		if i, ok := closest[key]; ok {
			if item.Distance < mergedTransitive[i].Distance {
				mergedTransitive[i] = item
			}
			continue
		}
		closest[key] = len(mergedTransitive)
		mergedTransitive = append(mergedTransitive, item)
	}
	return mergedDirect, mergedTransitive
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

// forkPoint returns where head forked from base, for diffing a branch the way
// a pull request shows it. Without a merge base (a shallow clone, unrelated
// histories) it keeps base, which compares the two tips.
func (e *Engine) forkPoint(base, head string) string {
	if e.gitAdapter == nil || base == "" {
		return base
	}
	mb, err := e.gitAdapter.MergeBase(base, head)
	if err != nil {
		e.logger.Warn("No merge base, diffing against the base's tip", "base", base, "head", head, "error", err)
		return base
	}
	return mb
}

// diffFileReader reads a changed file's whole text from the two sides getGitDiff
// compares. For a diff passed in from outside these may be other revisions; the
// mapper checks the text against the hunks and drops what doesn't match.
func (e *Engine) diffFileReader(staged bool, baseBranch string) diff.FileReader {
	var oldRev string
	var resolved bool
	show := func(object string) []string {
		cmd := exec.Command("git", "show", object)
		cmd.Dir = e.repoRoot
		out, err := cmd.Output()
		if err != nil {
			return nil
		}
		return splitFileLines(string(out))
	}
	return func(path string, old bool) []string {
		if !old {
			if staged {
				return show(":" + path)
			}
			content, err := os.ReadFile(filepath.Join(e.repoRoot, path))
			if err != nil {
				return nil
			}
			return splitFileLines(string(content))
		}
		if !resolved {
			resolved = true
			oldRev = e.diffOldRev(staged, baseBranch)
		}
		return show(oldRev + ":" + path)
	}
}

// diffOldRev is the revision getGitDiff compares against: the base's merge base with
// HEAD, the base itself for staged changes, HEAD without one.
func (e *Engine) diffOldRev(staged bool, baseBranch string) string {
	switch {
	case baseBranch == "":
		return "HEAD"
	case baseBranch != "HEAD" && !staged:
		return e.forkPoint(baseBranch, "HEAD")
	}
	return baseBranch
}

// getGitDiff gets the current git diff.
func (e *Engine) getGitDiff(staged bool, baseBranch string) (string, error) {
	args := []string{"diff", fmt.Sprintf("-U%d", diff.DeclarationContextLines)}
	if staged {
		args = append(args, "--cached")
	}
	// A bare `git diff` compares the working tree with the index, so anything staged
	// would look unchanged. HEAD covers both, unless there is no commit yet.
	base := baseBranch
	if base == "" && !staged {
		base = "HEAD"
	}
	if base != "" && base != "HEAD" && !staged {
		base = e.forkPoint(base, "HEAD")
	}
	if staged && base == "HEAD" {
		base = ""
	}
	run := func(args []string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = e.repoRoot
		out, err := cmd.Output()
		return string(out), err
	}
	withBase := args
	if base != "" {
		withBase = append(append([]string{}, args...), base)
	}
	out, err := run(withBase)
	if err != nil && base == "HEAD" {
		out, err = run(args)
	}
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}
	return out, nil
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

// boundedUniqueSorted is a sorted file list at most as long as ConsumerFiles allows.
func boundedUniqueSorted(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	unique := make([]string, 0, len(paths))
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}
	sort.Strings(unique)
	if len(unique) > MaxConsumerFiles {
		unique = unique[:MaxConsumerFiles]
	}
	return unique
}

// symbolDeclarationChange is what happened to a changed symbol's declaration. A
// definition deleted outright is removed whatever its text could show.
func symbolDeclarationChange(sym impact.ChangedSymbol) impact.DeclarationChange {
	if sym.ChangeType == impact.ChangeDeleted {
		return impact.DeclRemoved
	}
	return sym.Declaration
}

// brokenContract is a symbol whose declaration changed or went away while code
// outside the files in the diff still uses it.
type brokenContract struct {
	name      string
	change    impact.DeclarationChange
	consumers []string
	files     []string
}

// withoutFiles returns files minus the ones in remove.
func withoutFiles(files, remove map[string]bool) map[string]bool {
	kept := make(map[string]bool, len(files))
	for path := range files {
		if !remove[path] {
			kept[path] = true
		}
	}
	return kept
}

// findBrokenContract returns the changed symbol's production consumers in files the
// diff didn't handle, or nil if there are none. handled normally holds the changed
// files whose uses of this symbol were all touched. A test using it breaks too, but the
// tests are reported apart and don't add to the risk of the change.
func findBrokenContract(name string, change impact.DeclarationChange, direct []ImpactItem, handled map[string]bool) *brokenContract {
	var consumers, files []string
	for i := range direct {
		item := &direct[i]
		if isTestFile(item.Location) || (item.Location != nil && handled[item.Location.FileId]) {
			continue
		}
		consumer := item.Name
		if item.Location != nil {
			files = append(files, item.Location.FileId)
			if consumer == "" {
				consumer = item.Location.FileId // A use at file level, such as an import
			}
		}
		consumers = append(consumers, consumer)
	}
	if len(consumers) == 0 {
		return nil
	}
	return &brokenContract{name: name, change: change, consumers: consumers, files: files}
}

const (
	highRiskFloor   = 0.6
	mediumRiskFloor = 0.4
)

// applyContractChangesToRisk keeps the score from calling a change low when a
// declaration changed under code that still uses it, because the count-based score
// has no idea: however few consumers there are, that is what breaks a build.
//
// The rule, from what these changes do rather than from any sample:
//   - Removed, or changed so existing uses may not survive, with production consumers
//     outside the diff, of any visibility: at least high. An unexported function's
//     callers break just like an exported one's.
//   - The same on an exported symbol with no such consumers: at least medium. Nothing
//     here visibly breaks, but this index can't see other repos.
//   - A compatible change, a body edit, or either with every consumer in the diff:
//     no floor. The counts are all that's left.
//
// It stays a heuristic: declarations are compared as text, and nothing is compiled.
func applyContractChangesToRisk(risk *RiskScore, broken []brokenContract, publicChanges int) {
	var removed, changed int
	for i := range broken {
		if broken[i].change == impact.DeclRemoved {
			removed++
		} else {
			changed++
		}
	}
	if removed+changed > 0 && risk.Score < highRiskFloor {
		risk.Score = highRiskFloor
		risk.Level = "high"
	}
	if removed > 0 {
		risk.Factors = append(risk.Factors, RiskFactor{
			Name:     "deleted_with_consumers",
			Value:    1,
			Evidence: fmt.Sprintf("%d deleted symbol(s) still used by code this diff left as it was", removed),
		})
		risk.Explanation += fmt.Sprintf(" %d deleted symbol(s) are still used by code this diff left as it was.", removed)
	}
	if changed > 0 {
		risk.Factors = append(risk.Factors, RiskFactor{
			Name:     "signature_changed_with_consumers",
			Value:    1,
			Evidence: fmt.Sprintf("%d symbol(s) changed declaration, still used by code this diff left as it was (heuristic: text comparison)", changed),
		})
		risk.Explanation += fmt.Sprintf(" %d symbol(s) changed their declaration and are still used by code this diff left as it was (heuristic: compared as text).", changed)
	}
	if publicChanges > 0 {
		if risk.Score < mediumRiskFloor {
			risk.Score = mediumRiskFloor
			risk.Level = "medium"
		}
		risk.Factors = append(risk.Factors, RiskFactor{
			Name:     "public_contract_changed",
			Value:    1,
			Evidence: fmt.Sprintf("%d exported symbol(s) changed declaration; consumers in other repos can't be seen", publicChanges),
		})
		risk.Explanation += fmt.Sprintf(" %d exported symbol(s) changed their declaration, and consumers in other repos can't be seen.", publicChanges)
	}
}

// contractRecommendations says what a declaration change breaks, or that it can't tell.
func contractRecommendations(broken []brokenContract, changedSymbols []impact.ChangedSymbol) []Recommendation {
	var recs []Recommendation
	for _, b := range broken {
		shown := b.consumers
		more := ""
		if len(shown) > 5 {
			more = fmt.Sprintf(" and %d more", len(shown)-5)
			shown = shown[:5]
		}
		rec := Recommendation{
			Type:     "review",
			Severity: "error",
			Message:  fmt.Sprintf("Deleted %s is still used by %s%s, at uses this diff left as they were.", b.name, strings.Join(shown, ", "), more),
			Action:   "Update those consumers, or keep the symbol",
			Source:   "deleted-symbols",
		}
		if b.change == impact.DeclChanged {
			rec.Severity = "warning"
			rec.Message = fmt.Sprintf("%s changed its declaration and is still used by %s%s, at uses this diff left as they were (heuristic).", b.name, strings.Join(shown, ", "), more)
			rec.Action = "Update those consumers, or keep the old signature"
			rec.Source = "contracts"
		}
		recs = append(recs, rec)
	}
	for _, sym := range changedSymbols {
		if sym.DeletionUnresolved {
			recs = append(recs, Recommendation{
				Type:     "coverage",
				Severity: "warning",
				Message:  fmt.Sprintf("Removed code in %s can't be traced: %s.", sym.File, sym.Note),
				Action:   "The index already includes this deletion. Index the base revision and re-run to see the consumers, or search for the removed names",
				Source:   "deleted-symbols",
			})
		}
	}
	return recs
}

// RiskLevelNotAssessed is the risk level of a change whose consumers could not
// be looked up. It is deliberately not on the low..critical scale.
const RiskLevelNotAssessed = "not_assessed"

// NothingToAssess explains a not-assessed risk for a diff with no source changes.
const NothingToAssess = "Nothing to assess: no source changes in this diff."

// hasConsumerGraph reports whether anything can answer who uses a symbol. Without
// it, "0 consumers" means nobody looked.
func (e *Engine) hasConsumerGraph() bool {
	return e.scipAdapter != nil && e.scipAdapter.IsAvailable()
}

// notAssessedRisk is the risk of a change analyzed without a consumer graph.
func notAssessedRisk(changedSymbols int) *RiskScore {
	return &RiskScore{
		Level:       RiskLevelNotAssessed,
		Explanation: fmt.Sprintf("%d symbol(s) changed, but downstream impact was not assessed: no SCIP index. Run 'ckb index' for a risk score.", changedSymbols),
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
	Reason     string   `json:"reason"`               // "direct", "transitive", "coverage", "naming-convention", "semantic-proximity"
	AffectedBy []string `json:"affectedBy,omitempty"` // Symbol IDs that caused this test to be selected
	Confidence float64  `json:"confidence"`
}

// TestSummary provides an overview of affected tests.
type TestSummary struct {
	TotalFiles      int `json:"totalFiles"`
	DirectFiles     int `json:"directFiles"`
	TransitiveFiles int `json:"transitiveFiles"`
	CoverageFiles   int `json:"coverageFiles"`
	// FileLevelFiles use something defined in a changed file, not a changed symbol.
	FileLevelFiles   int    `json:"fileLevelFiles,omitempty"`
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
	return e.affectedTestsFromChangeSet(ctx, changeSet, startTime), nil
}

// affectedTestsOptions are the AnalyzeChangeSet options GetAffectedTests runs
// with, for a caller that computes the change set once and shares it.
func affectedTestsChangeSetOptions(baseBranch string, staged bool) AnalyzeChangeSetOptions {
	if baseBranch == "" {
		baseBranch = "HEAD"
	}
	return AnalyzeChangeSetOptions{
		Staged:          staged,
		BaseBranch:      baseBranch,
		TransitiveDepth: 1,
		IncludeTests:    true,
	}
}

// affectedTestsFromChangeSet builds GetAffectedTests' response from a change set
// analyzed with tests included.
func (e *Engine) affectedTestsFromChangeSet(ctx context.Context, changeSet *AnalyzeChangeSetResponse, startTime time.Time) *AffectedTestsResponse {
	var coverageUsed bool

	tests := changeSet.AffectedTests

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
		case reasonUsesChangedFile:
			summary.FileLevelFiles++
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
	}
}

// deriveAffectedTests computes the tests that use or reach a set of changed
// symbols, given every consumer of them including those in test files. Other
// consumers are ignored.
//
// causes maps a consumer (impactItemKey) to the changed symbols that selected it;
// those, not the test's own id, go into AffectedBy. TestNames are short names.
func (e *Engine) deriveAffectedTests(changedSymbols []ChangedSymbolInfo, affectedSymbols []ImpactItem, causes map[string][]string) []AffectedTest {
	testFileMap := make(map[string]*AffectedTest)

	// 1. Direct test files (tests that reference changed symbols)
	for _, sym := range affectedSymbols {
		if sym.Location == nil || !isRunnableTestPath(sym.Location.FileId) {
			continue
		}
		path := ""
		if sym.Location != nil {
			path = sym.Location.FileId
		} else {
			continue
		}

		existing, ok := testFileMap[path]
		if !ok {
			existing = &AffectedTest{FilePath: path, Reason: categorizeTestReason(sym.Distance), Confidence: sym.Confidence}
			testFileMap[path] = existing
		}
		if categorizeTestReason(sym.Distance) == "direct" {
			existing.Reason = "direct"
		}
		if sym.Confidence > existing.Confidence {
			existing.Confidence = sym.Confidence
		}
		for _, cause := range causes[impactItemKey(&sym)] {
			existing.AffectedBy = appendUnique(existing.AffectedBy, cause)
		}
		if name := impact.ShortName(sym.Name, sym.StableId); name != "" {
			existing.TestNames = appendUnique(existing.TestNames, name)
		}
	}

	// 2. Test files for changed production code (heuristic: find corresponding test files)
	for _, sym := range changedSymbols {
		if isTestFilePath(sym.File) {
			continue // Already a test file
		}

		// Find corresponding test files
		testFiles := findCorrespondingTestFiles(e.repoRoot, sym.File)
		for _, testFile := range testFiles {
			if _, ok := testFileMap[testFile]; !ok {
				testFileMap[testFile] = &AffectedTest{
					FilePath:   testFile,
					Reason:     "naming-convention", // Named after the file, whether it uses the symbol or not
					AffectedBy: []string{sym.SymbolID},
					Confidence: 0.8,
				}
			}
		}
	}

	// Note: coverage-based test mapping not implemented yet (requires parsing coverage files)

	// 3. LIP semantic pass: for each changed production file, find semantically
	// similar test files. This catches tests that cover the changed code but don't
	// follow naming conventions (e.g. integration tests, table-driven suites).
	for _, sym := range changedSymbols {
		if isTestFilePath(sym.File) {
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
		// Cap the per-file lists; the file is what has to be looked at.
		if len(test.AffectedBy) > 5 {
			test.AffectedBy = test.AffectedBy[:5]
		}
		if len(test.TestNames) > 5 {
			test.TestNames = test.TestNames[:5]
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

// impactItemKey identifies a consumer across the per-symbol impact results: its
// symbol, or its file and line where no enclosing symbol was resolved.
func impactItemKey(item *ImpactItem) string {
	if item.StableId != "" {
		return item.StableId
	}
	if item.Location != nil {
		return fmt.Sprintf("%s:%d", item.Location.FileId, item.Location.StartLine)
	}
	return ""
}

func appendUnique(list []string, value string) []string {
	for _, have := range list {
		if have == value {
			return list
		}
	}
	return append(list, value)
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

// contractSignalFor flags a changed symbol by what its declaration did. A body edit
// flags nothing, however public the symbol: its contract is the same text as before.
// Only a declaration that can't be read falls back on visibility.
func contractSignalFor(sym impact.ChangedSymbol, declaration impact.DeclarationChange, name, visibility, language string, consumers []string) *ContractSignal {
	var kind, basis string
	switch declaration {
	case impact.DeclUnchanged:
		return nil
	case impact.DeclChanged:
		kind, basis = "signature-changed", "heuristic: declaration text differs across the diff"
	case impact.DeclRemoved:
		kind, basis = "definition-removed", "heuristic: declaration is gone from the file, deleted or renamed"
	case impact.DeclCompatible:
		kind, basis = "compatible-signature-change", "heuristic: declaration text differs, existing uses should still fit"
	default:
		return buildContractSignal(name, visibility, language, sym.File)
	}
	signal := &ContractSignal{
		Symbol: name,
		File:   sym.File,
		Kind:   kind,
		Basis:  basis,
		Before: boundedText(sym.DeclarationBefore),
		After:  boundedText(sym.DeclarationAfter),
	}
	if files := boundedUniqueSorted(consumers); len(files) > 0 {
		signal.ConsumerCount = len(files)
		signal.Consumers = files
		if len(signal.Consumers) > maxContractConsumers {
			signal.Consumers = signal.Consumers[:maxContractConsumers]
		}
	}
	return signal
}

// maxContractConsumers bounds the files named on each signal; ConsumerFiles has them all.
const maxContractConsumers = 10

func boundedText(text string) string {
	const limit = 200
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

var publicDeclarationRe = regexp.MustCompile(`^\s*(export|pub)\b`)

// declaresPublic reports whether a declaration's own text marks it as public, for
// languages where the name doesn't and the index may not say.
func declaresPublic(declaration string) bool {
	return publicDeclarationRe.MatchString(declaration)
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
		AllItems:     true, // A budget cuts transitive items first, and a test can be one
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
	return isTestFilePath(loc.FileId)
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
// jsTestRunner names the JavaScript test runner a repo uses: "vitest" when it
// has a vitest config or depends on vitest, "jest-config" when it has a jest
// config, "" when neither shows. Suggesting npx jest in a vitest project hands
// the agent a command that fails.
func jsTestRunner(repoRoot string) string {
	for _, ext := range []string{"ts", "mts", "cts", "js", "mjs", "cjs"} {
		if fileExists(filepath.Join(repoRoot, "vitest.config."+ext)) {
			return "vitest"
		}
	}
	for _, ext := range []string{"js", "ts", "mjs", "cjs"} {
		if fileExists(filepath.Join(repoRoot, "jest.config."+ext)) {
			return "jest-config"
		}
	}
	if data, err := os.ReadFile(filepath.Join(repoRoot, "package.json")); err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if _, ok := pkg.DevDependencies["vitest"]; ok {
				return "vitest"
			}
			if _, ok := pkg.Dependencies["vitest"]; ok {
				return "vitest"
			}
		}
	}
	return ""
}

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
		files := strings.Join(jsTests[:min(5, len(jsTests))], " ")
		switch jsTestRunner(repoRoot) {
		case "vitest":
			return fmt.Sprintf("npx vitest run %s", files)
		case "jest-config":
			return fmt.Sprintf("npm test -- %s", files)
		}
		return fmt.Sprintf("npx jest %s", files)

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
		if sym.Confidence < 0.5 && !sym.Trivial {
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
			Message:  fmt.Sprintf("%d possible contract change(s) (heuristic: from declaration text or visibility, not compiled).", len(contracts)),
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
