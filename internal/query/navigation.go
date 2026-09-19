package query

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// AINavigationMeta captures common response metadata aligned with the navigation spec.
type AINavigationMeta struct {
	CkbVersion    string             `json:"ckbVersion"`
	SchemaVersion int                `json:"schemaVersion"`
	Tool          string             `json:"tool"`
	Resolved      *ResolvedTarget    `json:"resolved,omitempty"`
	Truncation    *TruncationInfo    `json:"truncation,omitempty"`
	Drilldowns    []output.Drilldown `json:"drilldowns,omitempty"`
	Provenance    *Provenance        `json:"provenance,omitempty"`
}

// ResolvedTarget mirrors the resolution summary in the navigation spec.
type ResolvedTarget struct {
	SymbolId     string  `json:"symbolId,omitempty"`
	ResolvedFrom string  `json:"resolvedFrom,omitempty"`
	Confidence   float64 `json:"confidence,omitempty"`
}

// ExplainSymbolOptions controls explainSymbol behavior.
type ExplainSymbolOptions struct {
	SymbolId string
}

// ExplainSymbolResponse provides an AI-navigation friendly symbol overview.
type ExplainSymbolResponse struct {
	AINavigationMeta
	Facts   ExplainSymbolFacts   `json:"facts"`
	Summary ExplainSymbolSummary `json:"summary"`
}

// ExplainSymbolFacts mirrors the CKB navigation contract at a simplified level.
type ExplainSymbolFacts struct {
	Symbol      *SymbolInfo         `json:"symbol,omitempty"`
	Usage       *ExplainUsage       `json:"usage,omitempty"`
	History     *ExplainHistory     `json:"history,omitempty"`
	Flags       *ExplainSymbolFlags `json:"flags,omitempty"`
	Callers     []ExplainCaller     `json:"callers,omitempty"`
	Callees     []ExplainCallee     `json:"callees,omitempty"`
	Module      string              `json:"module,omitempty"`
	Warnings    []string            `json:"warnings,omitempty"`
	Annotations *AnnotationContext  `json:"annotations,omitempty"` // v6.5: Related ADRs and module metadata
	RelatedDocs []RelatedDoc        `json:"relatedDocs,omitempty"` // v7.3: Docs that reference this symbol
}

// RelatedDoc represents a documentation file that references a symbol.
type RelatedDoc struct {
	Path    string `json:"path"`
	Title   string `json:"title,omitempty"`
	Line    int    `json:"line,omitempty"`
	Context string `json:"context,omitempty"`
}

// ExplainUsage captures high level usage stats.
type ExplainUsage struct {
	CallerCount    int `json:"callerCount"`
	CalleeCount    int `json:"calleeCount"`
	ReferenceCount int `json:"referenceCount"`
	ModuleCount    int `json:"moduleCount"`
}

// ExplainHistory captures git derived history.
type ExplainHistory struct {
	CreatedAt       string `json:"createdAt,omitempty"`
	LastModifiedAt  string `json:"lastModifiedAt,omitempty"`
	CommitCount     int    `json:"commitCount,omitempty"`
	CommitFrequency string `json:"commitFrequency,omitempty"`
}

// ExplainSymbolFlags encodes quick status bits.
type ExplainSymbolFlags struct {
	IsPublicApi  bool `json:"isPublicApi"`
	IsExported   bool `json:"isExported"`
	IsEntrypoint bool `json:"isEntrypoint"`
	HasTests     bool `json:"hasTests"`
}

// ExplainCaller represents a caller evidence item.
type ExplainCaller struct {
	FileId  string `json:"fileId"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"`
	IsTest  bool   `json:"isTest"`
	Context string `json:"context,omitempty"`
}

// ExplainCallee is a placeholder for future expansion.
type ExplainCallee struct {
	SymbolId string `json:"symbolId"`
}

// ExplainSymbolSummary provides condensed text.
type ExplainSymbolSummary struct {
	Tldr     string `json:"tldr"`
	Identity string `json:"identity"`
	Usage    string `json:"usage"`
	History  string `json:"history"`
}

// JustifySymbolOptions controls justification logic.
type JustifySymbolOptions struct {
	SymbolId string
}

// JustifySymbolResponse returns a verdict-like assessment.
type JustifySymbolResponse struct {
	AINavigationMeta
	Facts            *ExplainSymbolFacts `json:"facts"`
	Verdict          string              `json:"verdict"`
	Confidence       float64             `json:"confidence"`
	Reasoning        string              `json:"reasoning"`
	RelatedDecisions []RelatedDecision   `json:"relatedDecisions,omitempty"` // v6.5: ADRs that may justify this symbol
}

// CallGraphOptions configures call graph retrieval.
type CallGraphOptions struct {
	SymbolId  string
	Direction string // "callers", "callees", or "both"
	Depth     int
}

// CallGraphResponse contains a lightweight call graph.
type CallGraphResponse struct {
	AINavigationMeta
	Root  string          `json:"root"`
	Nodes []CallGraphNode `json:"nodes"`
	Edges []CallGraphEdge `json:"edges"`
}

// CallGraphNode captures a node in the call graph.
type CallGraphNode struct {
	ID       string        `json:"id"`
	SymbolId string        `json:"symbolId,omitempty"`
	Name     string        `json:"name"`
	Location *LocationInfo `json:"location,omitempty"`
	Depth    int           `json:"depth"`
	Role     string        `json:"role"` // "root", "caller", "callee"
	Score    float64       `json:"score"`
}

// CallGraphEdge encodes a caller->callee relationship.
type CallGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ModuleOverviewOptions controls module overview behavior.
type ModuleOverviewOptions struct {
	Path string
	Name string
}

// ModuleOverviewResponse returns coarse module facts.
type ModuleOverviewResponse struct {
	AINavigationMeta
	Module           ModuleOverviewModule `json:"module"`
	Size             ModuleSize           `json:"size"`
	RecentCommits    []string             `json:"recentCommits,omitempty"`
	Annotations      *ModuleAnnotations   `json:"annotations,omitempty"`      // v6.5: Declared module metadata
	RelatedDecisions []RelatedDecision    `json:"relatedDecisions,omitempty"` // v6.5: ADRs affecting this module
	// Skeleton provides Cartographer's skeleton (signatures + imports + deps) for the module.
	// Only populated when the binary is built with -tags cartographer.
	Skeleton *cartographer.ModuleContext `json:"skeleton,omitempty"`
}

// ModuleOverviewModule contains module identity.
type ModuleOverviewModule struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Responsibility string `json:"responsibility,omitempty"` // v6.5: Short description of module purpose
}

// ModuleSize contains basic size stats.
type ModuleSize struct {
	FileCount   int `json:"fileCount"`
	SymbolCount int `json:"symbolCount"`
}

// ExplainSymbol provides an opinionated overview of a symbol using available backends.
func (e *Engine) ExplainSymbol(ctx context.Context, opts ExplainSymbolOptions) (*ExplainSymbolResponse, error) {
	startTime := time.Now()

	// Reuse GetSymbol for symbol identity and provenance
	symbolResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: opts.SymbolId, RepoStateMode: "full"})
	if err != nil {
		return nil, err
	}

	facts := ExplainSymbolFacts{}
	if symbolResp.Symbol != nil {
		facts.Symbol = symbolResp.Symbol
		facts.Flags = &ExplainSymbolFlags{
			IsPublicApi: strings.EqualFold(symbolResp.Symbol.Visibility.Visibility, "public"),
			IsExported:  strings.EqualFold(symbolResp.Symbol.Visibility.Visibility, "public"),
		}
		facts.Module = symbolResp.Symbol.ModuleId
	}

	// Collect callee data from call graph if SCIP is available
	var calleeCount int
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		symbolId := opts.SymbolId
		if symbolResp.Symbol != nil {
			symbolId = symbolResp.Symbol.StableId
		}
		calleeCount = e.scipAdapter.GetCalleeCount(symbolId)
		if calleeCount > 0 {
			// Populate callees list with symbol IDs
			graph, graphErr := e.scipAdapter.BuildCallGraph(symbolId, scip.CallGraphOptions{
				Direction: scip.DirectionCallees,
				MaxDepth:  1,
				MaxNodes:  20,
			})
			if graphErr == nil && graph != nil {
				for _, callee := range graph.Callees {
					facts.Callees = append(facts.Callees, ExplainCallee{
						SymbolId: callee.SymbolID,
					})
				}
			}
		}
	}

	// Collect reference data for usage/callers
	refResp, err := e.FindReferences(ctx, FindReferencesOptions{SymbolId: opts.SymbolId, IncludeTests: true, Limit: 200})
	if err == nil && refResp != nil {
		callers := make([]ExplainCaller, 0, len(refResp.References))
		moduleSet := map[string]struct{}{}
		hasTests := false

		for _, ref := range refResp.References {
			moduleName := topLevelModule(ref.Location.FileId)
			moduleSet[moduleName] = struct{}{}
			isCall := strings.Contains(strings.ToLower(ref.Kind), "call")
			if isCall {
				callers = append(callers, ExplainCaller{
					FileId:  ref.Location.FileId,
					Line:    ref.Location.StartLine,
					Kind:    ref.Kind,
					IsTest:  ref.IsTest,
					Context: ref.Context,
				})
			}
			if ref.IsTest {
				hasTests = true
			}
		}

		facts.Callers = callers
		facts.Usage = &ExplainUsage{
			CallerCount:    len(callers),
			CalleeCount:    calleeCount,
			ReferenceCount: len(refResp.References),
			ModuleCount:    len(moduleSet),
		}

		if facts.Flags != nil {
			facts.Flags.HasTests = hasTests
		}
	}

	// Compute history from git using definition path when available
	if facts.Symbol != nil && facts.Symbol.Location != nil && e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		history, err := e.gitAdapter.GetFileHistory(facts.Symbol.Location.FileId, 20)
		if err == nil {
			facts.History = &ExplainHistory{
				CreatedAt:       tailTimestamp(history.Commits),
				LastModifiedAt:  history.LastModified,
				CommitCount:     history.CommitCount,
				CommitFrequency: classifyCommitFrequency(history.CommitCount),
			}
		}
	}

	// v6.5: Add annotation context (related ADRs and module metadata)
	if facts.Module != "" {
		facts.Annotations = e.getAnnotationContext(facts.Module)
	}

	// v7.3: Add related documentation (top 3 docs mentioning this symbol)
	if symbolResp.Symbol != nil {
		relatedDocs := e.getRelatedDocs(symbolResp.Symbol.StableId, 3)
		if len(relatedDocs) > 0 {
			facts.RelatedDocs = relatedDocs
		}
	}

	summary := buildExplainSummary(facts)

	prov := symbolResp.Provenance
	if prov != nil {
		prov.QueryDurationMs = time.Since(startTime).Milliseconds()
	}

	resolved := &ResolvedTarget{}
	if facts.Symbol != nil {
		resolved.SymbolId = facts.Symbol.StableId
		resolved.ResolvedFrom = "id"
		resolved.Confidence = 1.0
	}

	var truncation *TruncationInfo
	if refResp != nil && refResp.Truncated {
		truncation = refResp.TruncationInfo
		if truncation == nil {
			truncation = &TruncationInfo{
				Reason:        "limit",
				OriginalCount: refResp.TotalCount,
				ReturnedCount: len(refResp.References),
			}
		}
	}

	return &ExplainSymbolResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "explainSymbol",
			Resolved:      resolved,
			Truncation:    truncation,
			Drilldowns:    symbolResp.Drilldowns,
			Provenance:    prov,
		},
		Facts:   facts,
		Summary: summary,
	}, nil
}

// buildExplainSummary constructs summary text from facts.
func buildExplainSummary(facts ExplainSymbolFacts) ExplainSymbolSummary {
	summary := ExplainSymbolSummary{}

	if facts.Symbol != nil {
		summary.Identity = fmt.Sprintf("%s %s in module %s", facts.Symbol.Kind, facts.Symbol.Name, facts.Symbol.ModuleId)
	}
	if facts.Usage != nil {
		summary.Usage = fmt.Sprintf("%d callers, %d references across %d modules", facts.Usage.CallerCount, facts.Usage.ReferenceCount, facts.Usage.ModuleCount)
	}
	if facts.History != nil {
		summary.History = fmt.Sprintf("%d commits, last modified %s", facts.History.CommitCount, facts.History.LastModifiedAt)
	}

	parts := []string{}
	if summary.Identity != "" {
		parts = append(parts, summary.Identity)
	}
	if summary.Usage != "" {
		parts = append(parts, summary.Usage)
	}
	summary.Tldr = strings.TrimSpace(strings.Join(parts, " – "))

	return summary
}

// JustifySymbol applies simple heuristics using explainSymbol facts.
func (e *Engine) JustifySymbol(ctx context.Context, opts JustifySymbolOptions) (*JustifySymbolResponse, error) {
	explain, err := e.ExplainSymbol(ctx, ExplainSymbolOptions(opts))
	if err != nil {
		return nil, err
	}

	verdict, confidence, reasoning := computeJustifyVerdict(explain.Facts)

	// v6.5: Extract related decisions from annotations for response
	var relatedDecisions []RelatedDecision
	if explain.Facts.Annotations != nil && len(explain.Facts.Annotations.RelatedDecisions) > 0 {
		relatedDecisions = explain.Facts.Annotations.RelatedDecisions
	}

	return &JustifySymbolResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "justifySymbol",
			Resolved:      explain.Resolved,
			Truncation:    explain.Truncation,
			Drilldowns:    explain.Drilldowns,
			Provenance:    explain.Provenance,
		},
		Facts:            &explain.Facts,
		Verdict:          verdict,
		Confidence:       confidence,
		Reasoning:        reasoning,
		RelatedDecisions: relatedDecisions,
	}, nil
}

// computeJustifyVerdict encapsulates verdict selection logic for unit testing.
// v6.5: Now considers ADRs - if a module has an accepted ADR, symbols are less likely to be removal candidates.
func computeJustifyVerdict(facts ExplainSymbolFacts) (verdict string, confidence float64, reasoning string) {
	// Has active callers -> keep
	if facts.Usage != nil && facts.Usage.CallerCount > 0 {
		return "keep", 0.9, fmt.Sprintf("Active callers detected (%d)", facts.Usage.CallerCount)
	}

	// v6.5: Has architectural decision -> investigate (not auto-remove)
	// An ADR suggests intentional design, even if no callers are found
	if facts.Annotations != nil && len(facts.Annotations.RelatedDecisions) > 0 {
		for _, d := range facts.Annotations.RelatedDecisions {
			if d.Status == "accepted" || d.Status == "proposed" {
				return "investigate", 0.75, fmt.Sprintf("No callers found, but related to %s: %s", d.ID, d.Title)
			}
		}
	}

	// Public API with no callers -> investigate
	if facts.Flags != nil && facts.Flags.IsPublicApi {
		return "investigate", 0.6, "Public API but no callers found"
	}

	// No callers, not public, no ADR -> remove candidate
	return "remove-candidate", 0.7, "No callers found"
}

// GetCallGraph builds a call graph using SCIP index data.
func (e *Engine) GetCallGraph(ctx context.Context, opts CallGraphOptions) (*CallGraphResponse, error) {
	startTime := time.Now()

	if opts.Depth == 0 {
		opts.Depth = 1
	}
	if opts.Depth > 4 {
		opts.Depth = 4 // Hard limit
	}
	if opts.Direction == "" {
		opts.Direction = "both"
	}

	symbolResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: opts.SymbolId, RepoStateMode: "full"})
	if err != nil {
		return nil, err
	}

	nodes := []CallGraphNode{}
	edges := []CallGraphEdge{}
	var warnings []string

	// Add root node
	rootId := opts.SymbolId
	rootName := opts.SymbolId
	if symbolResp.Symbol != nil {
		rootId = symbolResp.Symbol.StableId
		rootName = symbolResp.Symbol.Name
	}
	nodes = append(nodes, CallGraphNode{
		ID:       rootId,
		SymbolId: rootId,
		Name:     rootName,
		Depth:    0,
		Role:     "root",
		Score:    1.0,
	})

	// Use SCIP backend for call graph if available
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		scipIndex := e.scipAdapter.GetIndexInfo()
		if scipIndex.Available {
			// Convert direction
			var scipDirection scip.CallGraphDirection
			switch opts.Direction {
			case "callers":
				scipDirection = scip.DirectionCallers
			case "callees":
				scipDirection = scip.DirectionCallees
			default:
				scipDirection = scip.DirectionBoth
			}

			// Build call graph from SCIP using resolved symbol ID
			graph, err := e.scipAdapter.BuildCallGraph(rootId, scip.CallGraphOptions{
				Direction: scipDirection,
				MaxDepth:  opts.Depth,
				MaxNodes:  100,
			})

			if err == nil && graph != nil {
				// Add callers
				for _, caller := range graph.Callers {
					var loc *LocationInfo
					if caller.Location != nil {
						loc = &LocationInfo{
							FileId:      caller.Location.FileId,
							StartLine:   caller.Location.StartLine + 1, // Convert to 1-indexed
							StartColumn: caller.Location.StartColumn + 1,
						}
					}
					nodes = append(nodes, CallGraphNode{
						ID:       caller.SymbolID,
						SymbolId: caller.SymbolID,
						Name:     caller.Name,
						Location: loc,
						Depth:    1,
						Role:     "caller",
						Score:    1.0,
					})
					edges = append(edges, CallGraphEdge{From: caller.SymbolID, To: rootId})
				}

				// Add callees
				for _, callee := range graph.Callees {
					var loc *LocationInfo
					if callee.Location != nil {
						loc = &LocationInfo{
							FileId:      callee.Location.FileId,
							StartLine:   callee.Location.StartLine + 1, // Convert to 1-indexed
							StartColumn: callee.Location.StartColumn + 1,
						}
					}
					nodes = append(nodes, CallGraphNode{
						ID:       callee.SymbolID,
						SymbolId: callee.SymbolID,
						Name:     callee.Name,
						Location: loc,
						Depth:    1,
						Role:     "callee",
						Score:    1.0,
					})
					edges = append(edges, CallGraphEdge{From: rootId, To: callee.SymbolID})
				}

				// Add edges from BFS traversal (for deeper levels)
				for _, edge := range graph.Edges {
					// Skip edges already added (depth 1)
					alreadyAdded := false
					for _, e := range edges {
						if e.From == edge.From && e.To == edge.To {
							alreadyAdded = true
							break
						}
					}
					if !alreadyAdded {
						edges = append(edges, CallGraphEdge{From: edge.From, To: edge.To})
					}
				}

				// Add nodes from BFS traversal (for deeper levels)
				for id, node := range graph.Nodes {
					if id == rootId {
						continue
					}
					// Check if already added
					found := false
					for _, n := range nodes {
						if n.ID == id {
							found = true
							break
						}
					}
					if !found {
						var loc *LocationInfo
						if node.Location != nil {
							loc = &LocationInfo{
								FileId:      node.Location.FileId,
								StartLine:   node.Location.StartLine + 1,
								StartColumn: node.Location.StartColumn + 1,
							}
						}
						nodes = append(nodes, CallGraphNode{
							ID:       node.SymbolID,
							SymbolId: node.SymbolID,
							Name:     node.Name,
							Location: loc,
							Depth:    2, // Deeper than direct callers/callees
							Role:     "transitive",
							Score:    0.5,
						})
					}
				}
			}
		}
	} else {
		warnings = append(warnings, "SCIP backend not available; call graph may be incomplete")

		// Fallback: use reference-based approach for callers only
		if opts.Direction == "both" || opts.Direction == "callers" {
			refResp, err := e.FindReferences(ctx, FindReferencesOptions{SymbolId: opts.SymbolId, IncludeTests: true, Limit: 200})
			if err == nil {
				for _, ref := range refResp.References {
					if ref.Location == nil {
						continue
					}
					key := fmt.Sprintf("%s:%d:%d", ref.Location.FileId, ref.Location.StartLine, ref.Location.StartColumn)
					nodes = append(nodes, CallGraphNode{
						ID:       key,
						Name:     ref.Location.FileId,
						Location: ref.Location,
						Depth:    1,
						Role:     "caller",
						Score:    1.0,
					})
					edges = append(edges, CallGraphEdge{From: key, To: rootId})
				}
			}
		}

		if opts.Direction == "both" || opts.Direction == "callees" {
			warnings = append(warnings, "Callee analysis requires SCIP index")
		}
	}

	// Sort nodes by score for deterministic output
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Score > nodes[j].Score })

	prov := symbolResp.Provenance
	if prov != nil {
		prov.QueryDurationMs = time.Since(startTime).Milliseconds()
		prov.Warnings = append(prov.Warnings, warnings...)
	}

	var truncation *TruncationInfo
	if len(nodes) >= 100 {
		truncation = &TruncationInfo{
			Reason:        "max-nodes",
			OriginalCount: len(nodes),
			ReturnedCount: len(nodes),
		}
	}

	return &CallGraphResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "getCallGraph",
			Resolved:      &ResolvedTarget{SymbolId: rootId, ResolvedFrom: "id", Confidence: 1.0},
			Truncation:    truncation,
			Provenance:    prov,
		},
		Root:  rootId,
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// GetModuleOverview returns coarse module level information.
func (e *Engine) GetModuleOverview(ctx context.Context, opts ModuleOverviewOptions) (*ModuleOverviewResponse, error) {
	startTime := time.Now()

	modulePath := opts.Path
	if modulePath == "" {
		modulePath = "."
	}

	fileCount := 0
	_ = filepath.Walk(modulePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // continue walking on individual file errors
		}

		if info.IsDir() {
			base := filepath.Base(path)
			// Skip hidden directories and common non-source directories
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Mode().IsRegular() {
			fileCount++
		}
		return nil
	})

	var recentCommits []string
	if e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		commits, err := e.gitAdapter.GetRecentCommits(5)
		if err == nil {
			for _, c := range commits {
				recentCommits = append(recentCommits, fmt.Sprintf("%s %s", c.Hash, c.Message))
			}
		}
	}

	prov := &Provenance{QueryDurationMs: time.Since(startTime).Milliseconds()}

	moduleName := opts.Name
	if moduleName == "" {
		moduleName = filepath.Base(modulePath)
		if moduleName == "." {
			if cwd, err := os.Getwd(); err == nil {
				moduleName = filepath.Base(cwd)
			}
		}
	}

	// v6.5: Get module annotations and related decisions
	annotations := e.getModuleAnnotations(modulePath)
	relatedDecisions := e.getRelatedDecisions(modulePath)

	// Extract responsibility for module overview
	var responsibility string
	if annotations != nil && annotations.Responsibility != "" {
		responsibility = annotations.Responsibility
	}

	resp := &ModuleOverviewResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "getModuleOverview",
			Resolved:      &ResolvedTarget{SymbolId: modulePath, ResolvedFrom: "path", Confidence: 1.0},
			Provenance:    prov,
		},
		Module: ModuleOverviewModule{
			Name:           moduleName,
			Path:           modulePath,
			Responsibility: responsibility,
		},
		Size: ModuleSize{
			FileCount:   fileCount,
			SymbolCount: 0, // Not yet implemented
		},
		RecentCommits:    recentCommits,
		Annotations:      annotations,
		RelatedDecisions: relatedDecisions,
	}

	// Augment with Cartographer skeleton: signatures + imports + direct dependencies.
	if cartographer.Available() {
		if skeleton, cerr := cartographer.GetModuleContext(e.repoRoot, modulePath, 1); cerr == nil {
			resp.Skeleton = skeleton
		}
	}

	return resp, nil
}

// topLevelModule extracts the top-level directory from a path.
func topLevelModule(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "./"), string(filepath.Separator))
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// tailTimestamp returns the timestamp of the oldest commit in a list.
func tailTimestamp(commits []git.CommitInfo) string {
	if len(commits) == 0 {
		return ""
	}
	return commits[len(commits)-1].Timestamp
}

// classifyCommitFrequency categorizes commit frequency.
func classifyCommitFrequency(count int) string {
	switch {
	case count > 50:
		return "volatile"
	case count > 10:
		return "moderate"
	case count > 0:
		return "stable"
	default:
		return "unknown"
	}
}

// ExplainFileOptions controls explainFile behavior.
type ExplainFileOptions struct {
	FilePath string
}

// ExplainFileResponse provides lightweight file-level orientation.
type ExplainFileResponse struct {
	AINavigationMeta
	Facts    ExplainFileFacts   `json:"facts"`
	Summary  ExplainFileSummary `json:"summary"`
	Warnings []string           `json:"warnings,omitempty"`
}

// ExplainFileFacts contains the factual information about a file.
type ExplainFileFacts struct {
	Path       string                `json:"path"`
	Role       string                `json:"role"` // core, glue, test, config, unknown
	Language   string                `json:"language,omitempty"`
	LineCount  int                   `json:"lineCount"`
	Symbols    []ExplainFileSymbol   `json:"symbols"`  // Top defined symbols (max 15)
	Imports    []string              `json:"imports"`  // Key imports
	Exports    []string              `json:"exports"`  // Key exports/public symbols
	Hotspots   []ExplainFileHotspot  `json:"hotspots"` // Local hotspots
	Related    []ExplainFileRelated  `json:"related,omitempty"`
	Confidence float64               `json:"confidence"`
	Basis      []ConfidenceBasisItem `json:"confidenceBasis"`
}

// ExplainFileRelated is a semantically-related symbol surfaced by LIP's
// `stream_context` RPC. The ranking is relevance to the whole file as a
// cursor region, token-budgeted so callers can feed it straight into an
// LLM prompt. Returns an empty list when the daemon is unavailable or
// predates v2.1.
type ExplainFileRelated struct {
	URI         string  `json:"uri"`
	DisplayName string  `json:"displayName"`
	Kind        string  `json:"kind"`
	Relevance   float32 `json:"relevance"`
	TokenCost   uint32  `json:"tokenCost"`
}

// ExplainFileSymbol represents a symbol defined in the file.
type ExplainFileSymbol struct {
	StableId   string `json:"stableId"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Line       int    `json:"line"`
	Visibility string `json:"visibility,omitempty"`
}

// ExplainFileHotspot represents a hotspot in the file.
type ExplainFileHotspot struct {
	Line      int    `json:"line"`
	Name      string `json:"name"`
	Reason    string `json:"reason"` // high-churn, high-coupling, complex
	Intensity string `json:"intensity"`
}

// ExplainFileSummary provides a natural language summary.
type ExplainFileSummary struct {
	OneLiner    string   `json:"oneLiner"`
	KeySymbols  []string `json:"keySymbols"`
	Suggestions []string `json:"suggestions,omitempty"`
}

// ConfidenceBasisItem describes a component of confidence.
type ConfidenceBasisItem struct {
	Backend   string `json:"backend"`
	Status    string `json:"status"` // available, partial, missing
	Heuristic string `json:"heuristic,omitempty"`
}

// ExplainFile provides lightweight orientation for a file.
func (e *Engine) ExplainFile(ctx context.Context, opts ExplainFileOptions) (*ExplainFileResponse, error) {
	startTime := time.Now()

	// Normalize file path
	filePath := opts.FilePath
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(e.repoRoot, filePath)
	}

	// Clean the path to resolve .. and other traversals
	filePath = filepath.Clean(filePath)

	// Security: verify path is within repo root
	repoRootClean := filepath.Clean(e.repoRoot)
	if !strings.HasPrefix(filePath, repoRootClean+string(filepath.Separator)) && filePath != repoRootClean {
		return nil, fmt.Errorf("path outside repository: %s", opts.FilePath)
	}

	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("file not found: %s", opts.FilePath)
	}
	if fileInfo.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file: %s", opts.FilePath)
	}

	// Get relative path for display
	relPath := opts.FilePath
	if filepath.IsAbs(opts.FilePath) {
		if rel, err := filepath.Rel(e.repoRoot, opts.FilePath); err == nil {
			relPath = rel
		}
	}

	// Determine file role
	role := classifyFileRole(relPath)

	// Detect language from extension
	language := detectLanguage(relPath)

	// Count lines
	lineCount := countFileLines(filePath)

	// Collect symbols defined in this file
	symbols := []ExplainFileSymbol{}
	exports := []string{}
	imports := []string{}
	var confidenceBasis []ConfidenceBasisItem

	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})

		// Search for symbols in this file
		searchResult, err := e.scipAdapter.SearchSymbols(ctx, "", backends.SearchOptions{
			MaxResults: 50,
			Scope:      []string{relPath},
		})

		if err == nil && searchResult != nil {
			for _, sym := range searchResult.Symbols {
				if sym.Location.Path == relPath {
					symbols = append(symbols, ExplainFileSymbol{
						StableId:   sym.StableID,
						Name:       sym.Name,
						Kind:       sym.Kind,
						Line:       sym.Location.Line,
						Visibility: sym.Visibility,
					})

					// Track exports (public symbols) - language-aware
					if isExportedSymbol(sym.Name, sym.Visibility, language) {
						exports = append(exports, sym.Name)
					}
				}
			}
		}
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
	}

	// Sort symbols by line number and limit to 15
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Line < symbols[j].Line
	})
	if len(symbols) > 15 {
		symbols = symbols[:15]
	}

	// Limit exports to 10
	if len(exports) > 10 {
		exports = exports[:10]
	}

	// Get git history for hotspots
	hotspots := []ExplainFileHotspot{}
	if e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "git",
			Status:  "available",
		})

		// Get file blame to identify frequently changed areas
		// This is a simplified version - real hotspot detection would be more sophisticated
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "git",
			Status:  "missing",
		})
	}

	// Pull related symbols via LIP's stream_context RPC. Ranked by the
	// daemon's semantic relevance to the whole file, token-budgeted so a
	// caller can paste the list straight into an LLM prompt. Silent no-op
	// when the daemon is unavailable or older than v2.1.
	related := e.relatedViaStreamContext(relPath, lineCount)
	if len(related) > 0 {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "lip",
			Status:  "available",
		})
	}

	// Compute confidence based on available backends
	confidence := computeExplainFileConfidence(confidenceBasis)

	// Build key symbols for summary
	keySymbols := []string{}
	for i, sym := range symbols {
		if i >= 5 {
			break
		}
		keySymbols = append(keySymbols, sym.Name)
	}

	// Build one-liner summary
	oneLiner := buildFileOneLiner(relPath, role, len(symbols), keySymbols)

	// Build response
	response := &ExplainFileResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "explainFile",
		},
		Facts: ExplainFileFacts{
			Path:       relPath,
			Role:       role,
			Language:   language,
			LineCount:  lineCount,
			Symbols:    symbols,
			Imports:    imports,
			Exports:    exports,
			Hotspots:   hotspots,
			Related:    related,
			Confidence: confidence,
			Basis:      confidenceBasis,
		},
		Summary: ExplainFileSummary{
			OneLiner:   oneLiner,
			KeySymbols: keySymbols,
		},
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	response.Drilldowns = []output.Drilldown{
		{
			Label:          "Get module overview",
			Query:          fmt.Sprintf("getModuleOverview --path=%s", filepath.Dir(relPath)),
			RelevanceScore: 0.9,
		},
	}
	if len(symbols) > 0 {
		response.Drilldowns = append(response.Drilldowns, output.Drilldown{
			Label:          fmt.Sprintf("Explore %s", symbols[0].Name),
			Query:          fmt.Sprintf("explainSymbol %s", symbols[0].StableId),
			RelevanceScore: 0.85,
		})
	}

	return response, nil
}

// classifyFileRole determines the role of a file based on its path.
func classifyFileRole(path string) string {
	pathLower := strings.ToLower(path)

	// Test files
	if strings.Contains(pathLower, "_test.") || strings.Contains(pathLower, ".test.") ||
		strings.Contains(pathLower, "/test/") || strings.Contains(pathLower, "/tests/") ||
		strings.HasSuffix(pathLower, ".spec.") {
		return "test"
	}

	// Config files
	if strings.HasSuffix(pathLower, ".json") || strings.HasSuffix(pathLower, ".yaml") ||
		strings.HasSuffix(pathLower, ".yml") || strings.HasSuffix(pathLower, ".toml") ||
		strings.HasSuffix(pathLower, ".ini") || strings.Contains(pathLower, "config") {
		return "config"
	}

	// Documentation - classify as unknown per v5.2 spec (not code)
	if strings.HasSuffix(pathLower, ".md") || strings.HasSuffix(pathLower, ".txt") ||
		strings.Contains(pathLower, "/docs/") {
		return "unknown"
	}

	// Vendor/external - classify as unknown per v5.2 spec (external code)
	if strings.Contains(pathLower, "/vendor/") || strings.Contains(pathLower, "/node_modules/") {
		return "unknown"
	}

	// Main entry points
	if strings.Contains(pathLower, "/cmd/") || strings.HasSuffix(pathLower, "main.go") ||
		strings.HasSuffix(pathLower, "index.ts") || strings.HasSuffix(pathLower, "index.js") {
		return "entrypoint"
	}

	// Internal implementation
	if strings.Contains(pathLower, "/internal/") || strings.Contains(pathLower, "/pkg/") ||
		strings.Contains(pathLower, "/lib/") || strings.Contains(pathLower, "/src/") {
		return "core"
	}

	return "unknown"
}

// detectLanguage determines the programming language from file extension.
func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".dart":
		return "dart"
	case ".swift":
		return "swift"
	case ".kt", ".kts":
		return "kotlin"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	default:
		return ""
	}
}

// countFileLines counts the number of lines in a file using buffered scanning.
func countFileLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
	}
	return lineCount
}

// computeExplainFileConfidence computes confidence based on available backends.
// Per v5.2 spec:
// - Full static analysis coverage: 1.0
// - Partial static analysis: 0.89
// - Heuristics only: 0.79
// - Key backend missing: 0.69
func computeExplainFileConfidence(basis []ConfidenceBasisItem) float64 {
	scipAvailable := false
	for _, b := range basis {
		if b.Backend == "scip" && b.Status == "available" {
			scipAvailable = true
		}
	}

	if !scipAvailable {
		// Key backend missing - cap at 0.69
		return 0.69
	}

	// SCIP available but we use heuristics for role detection
	// This is partial static analysis, cap at 0.89
	return 0.89
}

// buildFileOneLiner creates a one-line summary of the file.
func buildFileOneLiner(path, role string, symbolCount int, keySymbols []string) string {
	fileName := filepath.Base(path)

	switch role {
	case "test":
		return fmt.Sprintf("%s is a test file with %d test functions/helpers", fileName, symbolCount)
	case "config":
		return fmt.Sprintf("%s is a configuration file", fileName)
	case "entrypoint":
		return fmt.Sprintf("%s is an entry point/main file", fileName)
	case "core":
		if len(keySymbols) > 0 {
			return fmt.Sprintf("%s defines %s and %d other symbols", fileName, keySymbols[0], symbolCount-1)
		}
		return fmt.Sprintf("%s is a core implementation file with %d symbols", fileName, symbolCount)
	default:
		if len(keySymbols) > 0 {
			return fmt.Sprintf("%s defines %s and %d other symbols", fileName, keySymbols[0], symbolCount-1)
		}
		return fmt.Sprintf("%s contains %d symbols", fileName, symbolCount)
	}
}

// isExportedSymbol determines if a symbol is exported based on language conventions.
func isExportedSymbol(name, visibility, language string) bool {
	// If visibility is explicitly set, use it
	if visibility == "public" {
		return true
	}
	if visibility == "private" || visibility == "protected" {
		return false
	}

	// Language-specific inference when visibility is unknown
	switch language {
	case "go":
		// Go: exported symbols start with uppercase
		return len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z'
	case "python":
		// Python: private symbols start with _
		return !strings.HasPrefix(name, "_")
	case "javascript", "typescript":
		// JS/TS: underscore prefix is conventionally private
		return !strings.HasPrefix(name, "_")
	default:
		// For other languages, don't assume - visibility unknown
		return false
	}
}

// ListEntrypointsOptions controls listEntrypoints behavior.
type ListEntrypointsOptions struct {
	ModuleFilter string // Optional filter to specific module
	Limit        int    // Max results (default 30)
}

// ListEntrypointsResponse provides the list of system entrypoints.
type ListEntrypointsResponse struct {
	AINavigationMeta
	Entrypoints     []EntrypointV52       `json:"entrypoints"`
	TotalCount      int                   `json:"totalCount"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Warnings        []string              `json:"warnings,omitempty"`
}

// EntrypointV52 represents a system entrypoint with v5.2 ranking signals.
type EntrypointV52 struct {
	SymbolId       string        `json:"symbolId"`
	Name           string        `json:"name"`
	Type           string        `json:"type"` // api, cli, job, event
	Location       *LocationInfo `json:"location"`
	DetectionBasis string        `json:"detectionBasis"` // naming, framework-config, static-call
	FanOut         int           `json:"fanOut"`         // Number of functions called
	Ranking        *RankingV52   `json:"ranking"`
}

// ListEntrypoints returns the system entrypoints.
func (e *Engine) ListEntrypoints(ctx context.Context, opts ListEntrypointsOptions) (*ListEntrypointsResponse, error) {
	startTime := time.Now()

	if opts.Limit <= 0 {
		opts.Limit = 30
	}

	entrypoints := []EntrypointV52{}
	var warnings []string
	var confidenceBasis []ConfidenceBasisItem

	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})

		// Search for main functions
		mainResults, _ := e.scipAdapter.SearchSymbols(ctx, "main", backends.SearchOptions{
			MaxResults: 50,
			Kind:       []string{"function"},
		})

		if mainResults != nil {
			for _, sym := range mainResults.Symbols {
				if sym.Name == "main" && strings.Contains(sym.Location.Path, "/cmd/") {
					fanOut := e.scipAdapter.GetCalleeCount(sym.StableID)
					entrypoints = append(entrypoints, EntrypointV52{
						SymbolId: sym.StableID,
						Name:     sym.Name,
						Type:     "cli",
						Location: &LocationInfo{
							FileId:    sym.Location.Path,
							StartLine: sym.Location.Line,
						},
						DetectionBasis: "naming",
						FanOut:         fanOut,
					})
				}
			}
		}

		// Search for handler patterns
		handlerPatterns := []string{"Handle", "Handler", "Serve", "Route"}
		for _, pattern := range handlerPatterns {
			results, _ := e.scipAdapter.SearchSymbols(ctx, pattern, backends.SearchOptions{
				MaxResults: 30,
				Kind:       []string{"function", "method"},
			})
			if results != nil {
				for _, sym := range results.Symbols {
					// Skip test files
					if strings.Contains(sym.Location.Path, "_test.") {
						continue
					}
					// Check if it looks like an API handler. Case-insensitive:
					// Go convention capitalizes exported names ("Handle",
					// "Handler"), but TS/JS methods are typically lowercase
					// ("handle") — the naming convention this is meant to
					// detect isn't specific to Go's export casing.
					nameLower := strings.ToLower(sym.Name)
					if strings.HasPrefix(nameLower, "handle") ||
						strings.HasSuffix(nameLower, "handler") ||
						strings.HasPrefix(nameLower, "serve") {
						fanOut := e.scipAdapter.GetCalleeCount(sym.StableID)
						entrypoints = append(entrypoints, EntrypointV52{
							SymbolId: sym.StableID,
							Name:     sym.Name,
							Type:     "api",
							Location: &LocationInfo{
								FileId:    sym.Location.Path,
								StartLine: sym.Location.Line,
							},
							DetectionBasis: "naming",
							FanOut:         fanOut,
						})
					}
				}
			}
		}

		// Search for job/worker patterns
		jobPatterns := []string{"Run", "Execute", "Process", "Worker"}
		for _, pattern := range jobPatterns {
			results, _ := e.scipAdapter.SearchSymbols(ctx, pattern, backends.SearchOptions{
				MaxResults: 20,
				Kind:       []string{"function", "method"},
			})
			if results != nil {
				for _, sym := range results.Symbols {
					if strings.Contains(sym.Location.Path, "_test.") {
						continue
					}
					if strings.HasPrefix(sym.Name, "Run") ||
						strings.HasPrefix(sym.Name, "Execute") ||
						strings.Contains(sym.Location.Path, "worker") ||
						strings.Contains(sym.Location.Path, "job") {
						fanOut := e.scipAdapter.GetCalleeCount(sym.StableID)
						entrypoints = append(entrypoints, EntrypointV52{
							SymbolId: sym.StableID,
							Name:     sym.Name,
							Type:     "job",
							Location: &LocationInfo{
								FileId:    sym.Location.Path,
								StartLine: sym.Location.Line,
							},
							DetectionBasis: "naming",
							FanOut:         fanOut,
						})
					}
				}
			}
		}
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		warnings = append(warnings, "SCIP index unavailable; entrypoint detection limited")
	}

	// Deduplicate by symbol ID
	seen := make(map[string]bool)
	uniqueEntrypoints := []EntrypointV52{}
	for _, ep := range entrypoints {
		if !seen[ep.SymbolId] {
			seen[ep.SymbolId] = true
			uniqueEntrypoints = append(uniqueEntrypoints, ep)
		}
	}
	entrypoints = uniqueEntrypoints

	// Apply moduleFilter if specified
	if opts.ModuleFilter != "" {
		filtered := []EntrypointV52{}
		for _, ep := range entrypoints {
			if ep.Location != nil && strings.HasPrefix(ep.Location.FileId, opts.ModuleFilter) {
				filtered = append(filtered, ep)
			}
		}
		entrypoints = filtered
	}

	// Apply ranking signals
	for i := range entrypoints {
		score := 0.0
		ep := &entrypoints[i]

		// Score by type
		switch ep.Type {
		case "cli":
			score += 100 // CLI mains are most important
		case "api":
			score += 80
		case "job":
			score += 60
		case "event":
			score += 40
		}

		// Score by fan-out (higher fan-out = more important)
		score += float64(ep.FanOut) * 2
		if score > 200 {
			score = 200 // Cap
		}

		ep.Ranking = NewRankingV52(score, map[string]interface{}{
			"type":           ep.Type,
			"detectionBasis": ep.DetectionBasis,
			"fanOut":         ep.FanOut,
		})
	}

	// Sort by ranking score with deterministic tie-breaker (name, then symbolId)
	sort.Slice(entrypoints, func(i, j int) bool {
		if entrypoints[i].Ranking.Score != entrypoints[j].Ranking.Score {
			return entrypoints[i].Ranking.Score > entrypoints[j].Ranking.Score
		}
		if entrypoints[i].Name != entrypoints[j].Name {
			return entrypoints[i].Name < entrypoints[j].Name
		}
		return entrypoints[i].SymbolId < entrypoints[j].SymbolId
	})

	// Track total count before limiting
	totalFound := len(entrypoints)

	// Apply limit
	if len(entrypoints) > opts.Limit {
		entrypoints = entrypoints[:opts.Limit]
	}

	// Compute confidence
	confidence := 0.79 // Heuristics-based
	for _, b := range confidenceBasis {
		if b.Backend == "scip" && b.Status == "available" {
			confidence = 0.89 // Partial static analysis
		}
	}

	// Build response
	response := &ListEntrypointsResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "listEntrypoints",
		},
		Entrypoints:     entrypoints,
		TotalCount:      totalFound,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Warnings:        warnings,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	if len(entrypoints) > 0 {
		response.Drilldowns = []output.Drilldown{
			{
				Label:          fmt.Sprintf("Explore %s", entrypoints[0].Name),
				Query:          fmt.Sprintf("explainSymbol %s", entrypoints[0].SymbolId),
				RelevanceScore: 0.9,
			},
			{
				Label:          fmt.Sprintf("Call graph for %s", entrypoints[0].Name),
				Query:          fmt.Sprintf("getCallGraph %s", entrypoints[0].SymbolId),
				RelevanceScore: 0.85,
			},
		}
	}

	return response, nil
}

// TraceUsageOptions controls traceUsage behavior.
type TraceUsageOptions struct {
	SymbolId string // Target symbol to trace to
	MaxPaths int    // Maximum paths to return (default 10)
	MaxDepth int    // Maximum path depth (default 5)
}

// TraceUsageResponse provides paths from entrypoints to a target symbol.
type TraceUsageResponse struct {
	AINavigationMeta
	TargetSymbol    string                `json:"targetSymbol"`
	Paths           []UsagePath           `json:"paths"`
	TotalPathsFound int                   `json:"totalPathsFound"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations     []string              `json:"limitations,omitempty"`
}

// UsagePath represents a path from an entrypoint to the target.
type UsagePath struct {
	PathType   string      `json:"pathType"` // api, cli, job, event, test, unknown
	Nodes      []PathNode  `json:"nodes"`
	Confidence float64     `json:"confidence"`
	Ranking    *RankingV52 `json:"ranking"`
}

// PathNode represents a node in a usage path.
type PathNode struct {
	SymbolId string        `json:"symbolId"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind,omitempty"`
	Location *LocationInfo `json:"location,omitempty"`
	Role     string        `json:"role"` // entrypoint, intermediate, target
}

// bfsCache holds per-request caches for BFS traversal
type bfsCache struct {
	callees map[string][]string          // symbolId -> list of callee IDs
	symbols map[string]*symbolCacheEntry // symbolId -> symbol info
}

type symbolCacheEntry struct {
	name     string
	location *LocationInfo
}

// TraceUsage traces how a symbol is reached from system entrypoints.
func (e *Engine) TraceUsage(ctx context.Context, opts TraceUsageOptions) (*TraceUsageResponse, error) {
	startTime := time.Now()

	if opts.MaxPaths <= 0 {
		opts.MaxPaths = 10
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 5
	}
	if opts.MaxDepth > 5 {
		opts.MaxDepth = 5 // Heavy tool cap
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	paths := []UsagePath{}

	// Initialize per-request cache
	cache := &bfsCache{
		callees: make(map[string][]string),
		symbols: make(map[string]*symbolCacheEntry),
	}

	// Resolve target symbol
	targetResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: opts.SymbolId, RepoStateMode: "full"})
	if err != nil {
		return nil, err
	}

	targetId := opts.SymbolId
	targetName := opts.SymbolId
	var targetLoc *LocationInfo
	if targetResp.Symbol != nil {
		targetId = targetResp.Symbol.StableId
		targetName = targetResp.Symbol.Name
		targetLoc = targetResp.Symbol.Location
		// Cache the target symbol
		cache.symbols[targetId] = &symbolCacheEntry{name: targetName, location: targetLoc}
	}

	// Get entrypoints to use as start nodes
	entrypointsResp, err := e.ListEntrypoints(ctx, ListEntrypointsOptions{Limit: 50})
	if err != nil {
		limitations = append(limitations, "Entrypoint detection failed")
	}

	var entrypoints []EntrypointV52
	if entrypointsResp != nil && len(entrypointsResp.Entrypoints) > 0 {
		entrypoints = entrypointsResp.Entrypoints
	}

	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})

		if len(entrypoints) > 0 {
			// Try to find paths from each entrypoint to the target
			for _, ep := range entrypoints {
				path := e.findPathBFSCached(ctx, ep.SymbolId, targetId, opts.MaxDepth, cache)
				if len(path) > 0 {
					// Build path nodes - resolve names only for final path
					nodes := make([]PathNode, len(path))
					for i, nodeId := range path {
						role := "intermediate"
						if i == 0 {
							role = "entrypoint"
						} else if i == len(path)-1 {
							role = "target"
						}

						// Get symbol info from cache or resolve
						nodeName := nodeId
						var loc *LocationInfo
						if cached, ok := cache.symbols[nodeId]; ok {
							nodeName = cached.name
							loc = cached.location
						} else {
							// Resolve and cache
							if symResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: nodeId, RepoStateMode: "head"}); err == nil && symResp.Symbol != nil {
								nodeName = symResp.Symbol.Name
								loc = symResp.Symbol.Location
								cache.symbols[nodeId] = &symbolCacheEntry{name: nodeName, location: loc}
							}
						}

						nodes[i] = PathNode{
							SymbolId: nodeId,
							Name:     nodeName,
							Role:     role,
							Location: loc,
						}
					}

					// Determine path type from entrypoint type
					pathType := ep.Type
					if pathType == "" {
						pathType = "unknown"
					}

					pathConfidence := 0.89 // Static analysis with partial coverage
					usagePath := UsagePath{
						PathType:   pathType,
						Nodes:      nodes,
						Confidence: pathConfidence,
						Ranking: NewRankingV52(computePathScore(pathType, len(nodes), pathConfidence), map[string]interface{}{
							"pathType":   pathType,
							"pathLength": len(nodes),
							"confidence": pathConfidence,
						}),
					}

					paths = append(paths, usagePath)

					// Check limit
					if len(paths) >= opts.MaxPaths {
						break
					}
				}
			}
		}

		// Fallback: if no paths from entrypoints, use direct callers
		if len(paths) == 0 {
			limitations = append(limitations, "Entrypoint set unavailable; showing nearest callers")

			// Get callers and build short paths from them
			graph, err := e.scipAdapter.BuildCallGraph(targetId, scip.CallGraphOptions{
				Direction: scip.DirectionCallers,
				MaxDepth:  2,
				MaxNodes:  20,
			})

			if err == nil && graph != nil {
				for _, caller := range graph.Callers {
					var callerLoc *LocationInfo
					if caller.Location != nil {
						callerLoc = &LocationInfo{
							FileId:      caller.Location.FileId,
							StartLine:   caller.Location.StartLine + 1,
							StartColumn: caller.Location.StartColumn + 1,
						}
					}

					// Check if caller is a test
					pathType := "unknown"
					if caller.Location != nil && isTestFilePath(caller.Location.FileId) {
						pathType = "test"
					}

					pathConfidence := 0.69 // Fallback mode
					usagePath := UsagePath{
						PathType: pathType,
						Nodes: []PathNode{
							{
								SymbolId: caller.SymbolID,
								Name:     caller.Name,
								Role:     "entrypoint",
								Location: callerLoc,
							},
							{
								SymbolId: targetId,
								Name:     targetName,
								Role:     "target",
								Location: targetLoc,
							},
						},
						Confidence: pathConfidence,
						Ranking: NewRankingV52(computePathScore(pathType, 2, pathConfidence), map[string]interface{}{
							"pathType":   pathType,
							"pathLength": 2,
							"confidence": pathConfidence,
						}),
					}

					paths = append(paths, usagePath)

					if len(paths) >= opts.MaxPaths {
						break
					}
				}
			}
		}
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		limitations = append(limitations, "SCIP index unavailable; path tracing requires static analysis")
	}

	// Sort paths by ranking score
	sort.Slice(paths, func(i, j int) bool {
		return paths[i].Ranking.Score > paths[j].Ranking.Score
	})

	// Compute overall confidence
	confidence := 0.39 // Default: speculative
	for _, b := range confidenceBasis {
		if b.Backend == "scip" && b.Status == "available" {
			confidence = 0.89
			if len(limitations) > 0 {
				confidence = 0.69 // Degraded due to limitations
			}
		}
	}

	// Build response
	response := &TraceUsageResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "traceUsage",
			Resolved:      &ResolvedTarget{SymbolId: targetId, ResolvedFrom: "id", Confidence: 1.0},
		},
		TargetSymbol:    targetId,
		Paths:           paths,
		TotalPathsFound: len(paths),
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	response.Drilldowns = []output.Drilldown{
		{
			Label:          fmt.Sprintf("Call graph for %s", targetName),
			Query:          fmt.Sprintf("getCallGraph %s", targetId),
			RelevanceScore: 0.9,
		},
		{
			Label:          fmt.Sprintf("Explain %s", targetName),
			Query:          fmt.Sprintf("explainSymbol %s", targetId),
			RelevanceScore: 0.85,
		},
	}

	return response, nil
}

// findPathBFSCached performs BFS to find a path from source to target with caching.
func (e *Engine) findPathBFSCached(ctx context.Context, sourceId, targetId string, maxDepth int, cache *bfsCache) []string {
	if sourceId == targetId {
		return []string{sourceId}
	}

	// BFS state
	type bfsNode struct {
		id    string
		path  []string
		depth int
	}

	const maxVisitedNodes = 500 // Cap to prevent explosion
	const maxCalleesPerNode = 30

	visited := make(map[string]bool)
	queue := []bfsNode{{id: sourceId, path: []string{sourceId}, depth: 0}}
	visited[sourceId] = true

	for len(queue) > 0 && len(visited) < maxVisitedNodes {
		current := queue[0]
		queue = queue[1:]

		if current.depth >= maxDepth {
			continue
		}

		// Get callees - check cache first
		callees, ok := cache.callees[current.id]
		if !ok {
			// Not in cache - fetch from SCIP
			if e.scipAdapter == nil {
				continue
			}

			graph, err := e.scipAdapter.BuildCallGraph(current.id, scip.CallGraphOptions{
				Direction: scip.DirectionCallees,
				MaxDepth:  1,
				MaxNodes:  maxCalleesPerNode,
			})

			if err != nil || graph == nil {
				cache.callees[current.id] = []string{} // Cache empty result
				continue
			}

			// Extract callee IDs and cache
			callees = make([]string, 0, len(graph.Callees))
			for _, callee := range graph.Callees {
				callees = append(callees, callee.SymbolID)
			}
			cache.callees[current.id] = callees
		}

		for _, calleeId := range callees {
			if visited[calleeId] {
				continue
			}
			visited[calleeId] = true

			newPath := make([]string, len(current.path)+1)
			copy(newPath, current.path)
			newPath[len(current.path)] = calleeId

			if calleeId == targetId {
				return newPath
			}

			queue = append(queue, bfsNode{
				id:    calleeId,
				path:  newPath,
				depth: current.depth + 1,
			})
		}
	}

	return nil // No path found
}

// computePathScore calculates a ranking score for a usage path.
func computePathScore(pathType string, pathLength int, confidence float64) float64 {
	score := 0.0

	// Score by path type
	switch pathType {
	case "cli":
		score += 100
	case "api":
		score += 80
	case "job":
		score += 60
	case "event":
		score += 50
	case "test":
		score += 40
	default:
		score += 20
	}

	// Prefer shorter paths (penalize longer ones)
	score -= float64(pathLength-1) * 5

	// Factor in confidence
	score *= confidence

	if score < 0 {
		score = 0
	}

	return score
}

// isTestFilePath checks if a path is a test file.
func isTestFilePath(path string) bool {
	pathLower := strings.ToLower(path)
	return strings.Contains(pathLower, "_test.") ||
		strings.Contains(pathLower, ".test.") ||
		strings.Contains(pathLower, "/test/") ||
		strings.Contains(pathLower, "/tests/")
}

// SummarizeDiffOptions controls summarizeDiff behavior.
// Exactly one selector must be provided: CommitRange, Commit, or TimeWindow.
type SummarizeDiffOptions struct {
	CommitRange *CommitRangeSelector `json:"commitRange,omitempty"`
	Commit      string               `json:"commit,omitempty"`
	TimeWindow  *TimeWindowSelector  `json:"timeWindow,omitempty"`
	NoAutoFetch bool                 `json:"noAutoFetch,omitempty"` // Disable auto-fetch of missing refs (see ReviewPROptions)
}

// CommitRangeSelector specifies a base..head range.
type CommitRangeSelector struct {
	Base string `json:"base"`
	Head string `json:"head"`
}

// TimeWindowSelector specifies a time range.
type TimeWindowSelector struct {
	Start string `json:"start"` // ISO8601
	End   string `json:"end"`   // ISO8601
}

// SummarizeDiffResponse provides a compressed summary of changes.
type SummarizeDiffResponse struct {
	AINavigationMeta
	Selector        DiffSelector         `json:"selector"`
	ChangedFiles    []DiffFileChange     `json:"changedFiles"`
	SymbolsAffected []DiffSymbolAffected `json:"symbolsAffected"`
	RiskSignals     []DiffRiskSignal     `json:"riskSignals"`
	SuggestedTests  []SuggestedTest      `json:"suggestedTests,omitempty"`
	Summary         DiffSummaryText      `json:"summary"`
	Commits         []DiffCommitInfo     `json:"commits,omitempty"`
	// FunctionChanges provides function-level diff from Cartographer semidiff
	// (added/removed function signatures per file). Only populated for commitRange
	// selectors when the binary is built with -tags cartographer.
	FunctionChanges []cartographer.SemidiffFile `json:"functionChanges,omitempty"`
	Confidence      float64                     `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem       `json:"confidenceBasis"`
	Limitations     []string                    `json:"limitations,omitempty"`
}

// DiffSelector records which selector was used.
type DiffSelector struct {
	Type  string `json:"type"` // commitRange, commit, timeWindow
	Value string `json:"value"`
}

// DiffFileChange represents a changed file.
type DiffFileChange struct {
	FilePath   string `json:"filePath"`
	ChangeType string `json:"changeType"` // added, modified, deleted, renamed
	Additions  int    `json:"additions"`
	Deletions  int    `json:"deletions"`
	OldPath    string `json:"oldPath,omitempty"` // if renamed
	Language   string `json:"language,omitempty"`
	Role       string `json:"role,omitempty"` // core, test, config, unknown
	RiskLevel  string `json:"riskLevel"`      // low, medium, high
}

// DiffSymbolAffected represents a symbol affected by the changes.
type DiffSymbolAffected struct {
	SymbolId     string `json:"symbolId,omitempty"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	FilePath     string `json:"filePath"`
	ChangeType   string `json:"changeType"` // added, modified, deleted
	IsPublicAPI  bool   `json:"isPublicApi"`
	IsEntrypoint bool   `json:"isEntrypoint"`
}

// DiffRiskSignal represents a risk indicator.
type DiffRiskSignal struct {
	Type        string  `json:"type"`     // api-change, signature-change, breaking-change, high-churn, test-gap
	Severity    string  `json:"severity"` // low, medium, high
	FilePath    string  `json:"filePath"`
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
}

// SuggestedTest represents a suggested test to run.
type SuggestedTest struct {
	TestPath string `json:"testPath"`
	Reason   string `json:"reason"`
	Priority string `json:"priority"` // high, medium, low
}

// DiffSummaryText provides a human-readable summary.
type DiffSummaryText struct {
	OneLiner     string   `json:"oneLiner"`
	KeyChanges   []string `json:"keyChanges"`
	RiskOverview string   `json:"riskOverview,omitempty"`
}

// DiffCommitInfo represents a commit included in the diff.
type DiffCommitInfo struct {
	Hash      string `json:"hash"`
	Message   string `json:"message"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
}

// SummarizeDiff compresses diffs into "what changed, what might break".
func (e *Engine) SummarizeDiff(ctx context.Context, opts SummarizeDiffOptions) (*SummarizeDiffResponse, error) {
	startTime := time.Now()

	// Validate: exactly one selector must be provided
	selectorCount := 0
	if opts.CommitRange != nil {
		selectorCount++
	}
	if opts.Commit != "" {
		selectorCount++
	}
	if opts.TimeWindow != nil {
		selectorCount++
	}

	if selectorCount == 0 {
		// Default: last 30 days
		thirtyDaysAgo := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
		opts.TimeWindow = &TimeWindowSelector{
			Start: thirtyDaysAgo,
			End:   time.Now().Format(time.RFC3339),
		}
	} else if selectorCount > 1 {
		return nil, fmt.Errorf("exactly one selector required: commitRange, commit, or timeWindow")
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	changedFiles := []DiffFileChange{}
	symbolsAffected := []DiffSymbolAffected{}
	riskSignals := []DiffRiskSignal{}
	suggestedTests := []SuggestedTest{}
	commits := []DiffCommitInfo{}
	var selector DiffSelector

	// Check git backend
	if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
		return nil, fmt.Errorf("git backend unavailable; summarizeDiff requires git")
	}
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "git",
		Status:  "available",
	})

	// Get diff based on selector type
	var diffStats []git.DiffStats
	var base, head string
	var err error

	if opts.CommitRange != nil {
		selector = DiffSelector{Type: "commitRange", Value: opts.CommitRange.Base + ".." + opts.CommitRange.Head}
		base = opts.CommitRange.Base
		head = opts.CommitRange.Head
		if !opts.NoAutoFetch {
			if resolved, rerr := e.gitAdapter.EnsureRef(base); rerr == nil {
				base = resolved
			}
			if resolved, rerr := e.gitAdapter.EnsureRef(head); rerr == nil {
				head = resolved
			}
		}
		diffStats, err = e.gitAdapter.GetCommitRangeDiff(base, head)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit range diff: %w", err)
		}

		// Get commits in range
		commitsInRange, _ := e.gitAdapter.GetRecentCommits(100)
		for _, c := range commitsInRange {
			commits = append(commits, DiffCommitInfo{
				Hash:      c.Hash,
				Message:   c.Message,
				Author:    c.Author,
				Timestamp: c.Timestamp,
			})
		}
	} else if opts.Commit != "" {
		selector = DiffSelector{Type: "commit", Value: opts.Commit}
		diffStats, err = e.gitAdapter.GetCommitDiff(opts.Commit)
		if err != nil {
			return nil, fmt.Errorf("failed to get commit diff: %w", err)
		}
		// Single commit
		commits = append(commits, DiffCommitInfo{Hash: opts.Commit})
	} else if opts.TimeWindow != nil {
		selector = DiffSelector{Type: "timeWindow", Value: opts.TimeWindow.Start + " to " + opts.TimeWindow.End}
		// Get commits in time window
		commitList, err := e.gitAdapter.GetCommitsSinceDate(opts.TimeWindow.Start, 1000)
		if err != nil {
			return nil, fmt.Errorf("failed to get commits in time window: %w", err)
		}
		if len(commitList) == 0 {
			// No commits in range
			return &SummarizeDiffResponse{
				AINavigationMeta: AINavigationMeta{
					CkbVersion:    "5.2",
					SchemaVersion: 1,
					Tool:          "summarizeDiff",
				},
				Selector:        selector,
				ChangedFiles:    []DiffFileChange{},
				SymbolsAffected: []DiffSymbolAffected{},
				RiskSignals:     []DiffRiskSignal{},
				Summary: DiffSummaryText{
					OneLiner:   "No commits found in the specified time window",
					KeyChanges: []string{},
				},
				Confidence:      1.0,
				ConfidenceBasis: confidenceBasis,
			}, nil
		}

		for _, c := range commitList {
			commits = append(commits, DiffCommitInfo{
				Hash:      c.Hash,
				Message:   c.Message,
				Author:    c.Author,
				Timestamp: c.Timestamp,
			})
		}

		// Get diff from oldest to newest commit
		head = commitList[0].Hash
		base = commitList[len(commitList)-1].Hash + "^"
		diffStats, err = e.gitAdapter.GetCommitRangeDiff(base, head)
		if err != nil {
			// Try without the ^ suffix (first commit case)
			base = commitList[len(commitList)-1].Hash
			diffStats, err = e.gitAdapter.GetCommitRangeDiff(base, head)
			if err != nil {
				limitations = append(limitations, "Could not compute full diff for time window")
			}
		}
	}

	// Cap files analyzed
	const maxFiles = 50
	if len(diffStats) > maxFiles {
		limitations = append(limitations, fmt.Sprintf("Truncated to %d files (total: %d)", maxFiles, len(diffStats)))
		diffStats = diffStats[:maxFiles]
	}

	// Process changed files
	for _, stat := range diffStats {
		changeType := "modified"
		if stat.IsNew {
			changeType = "added"
		} else if stat.IsDeleted {
			changeType = "deleted"
		} else if stat.IsRenamed {
			changeType = "renamed"
		}

		language := detectLanguage(stat.FilePath)
		role := classifyFileRole(stat.FilePath)
		riskLevel := classifyFileRiskLevel(stat, role)

		changedFiles = append(changedFiles, DiffFileChange{
			FilePath:   stat.FilePath,
			ChangeType: changeType,
			Additions:  stat.Additions,
			Deletions:  stat.Deletions,
			OldPath:    stat.OldPath,
			Language:   language,
			Role:       role,
			RiskLevel:  riskLevel,
		})

		// Generate risk signals
		if stat.Additions+stat.Deletions > 200 {
			riskSignals = append(riskSignals, DiffRiskSignal{
				Type:        "high-churn",
				Severity:    "medium",
				FilePath:    stat.FilePath,
				Description: fmt.Sprintf("Large change: +%d/-%d lines", stat.Additions, stat.Deletions),
				Confidence:  0.9,
			})
		}

		// Suggest tests for changed files
		if role == "core" || role == "entrypoint" {
			testPath := suggestTestPath(stat.FilePath, language)
			if testPath != "" {
				suggestedTests = append(suggestedTests, SuggestedTest{
					TestPath: testPath,
					Reason:   fmt.Sprintf("Tests for %s", stat.FilePath),
					Priority: "high",
				})
			}
		}
	}

	// Detect symbols affected using SCIP if available
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})

		// For each changed file, find symbols defined there
		for _, file := range changedFiles {
			if file.ChangeType == "deleted" {
				continue
			}

			searchResult, err := e.scipAdapter.SearchSymbols(ctx, "", backends.SearchOptions{
				MaxResults: 30,
				Scope:      []string{file.FilePath},
			})

			if err == nil && searchResult != nil {
				for _, sym := range searchResult.Symbols {
					if sym.Location.Path == file.FilePath {
						isPublicAPI := sym.Visibility == "public" || isExportedSymbol(sym.Name, sym.Visibility, file.Language)
						symbolsAffected = append(symbolsAffected, DiffSymbolAffected{
							SymbolId:    sym.StableID,
							Name:        sym.Name,
							Kind:        sym.Kind,
							FilePath:    file.FilePath,
							ChangeType:  file.ChangeType,
							IsPublicAPI: isPublicAPI,
						})

						// Generate API change risk signal for public symbols
						if isPublicAPI && file.ChangeType == "modified" {
							riskSignals = append(riskSignals, DiffRiskSignal{
								Type:        "api-change",
								Severity:    "high",
								FilePath:    file.FilePath,
								Description: fmt.Sprintf("Public symbol %s was modified", sym.Name),
								Confidence:  0.85,
							})
						}
					}
				}
			}
		}
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		limitations = append(limitations, "SCIP index unavailable; symbol-level analysis limited")
	}

	// Cap symbols and signals
	if len(symbolsAffected) > 30 {
		symbolsAffected = symbolsAffected[:30]
		limitations = append(limitations, "Truncated symbol list to 30")
	}
	if len(riskSignals) > 20 {
		riskSignals = riskSignals[:20]
	}

	// Deduplicate suggested tests
	testPathsSeen := make(map[string]bool)
	uniqueTests := []SuggestedTest{}
	for _, t := range suggestedTests {
		if !testPathsSeen[t.TestPath] {
			testPathsSeen[t.TestPath] = true
			uniqueTests = append(uniqueTests, t)
		}
	}
	suggestedTests = uniqueTests
	if len(suggestedTests) > 10 {
		suggestedTests = suggestedTests[:10]
	}

	// Build summary
	summary := buildDiffSummary(changedFiles, symbolsAffected, riskSignals, commits)

	// Compute confidence
	confidence := computeDiffConfidence(confidenceBasis, limitations)

	// Build response
	response := &SummarizeDiffResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "summarizeDiff",
		},
		Selector:        selector,
		ChangedFiles:    changedFiles,
		SymbolsAffected: symbolsAffected,
		RiskSignals:     riskSignals,
		SuggestedTests:  suggestedTests,
		Summary:         summary,
		Commits:         commits,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Augment with Cartographer function-level diff for commit range selectors.
	if cartographer.Available() && base != "" {
		if files, cerr := cartographer.Semidiff(e.repoRoot, base, head); cerr == nil && len(files) > 0 {
			response.FunctionChanges = files
		}
	}

	// Add drilldowns
	response.Drilldowns = []output.Drilldown{
		{
			Label:          "View architecture",
			Query:          "getArchitecture",
			RelevanceScore: 0.7,
		},
	}
	if len(changedFiles) > 0 {
		response.Drilldowns = append(response.Drilldowns, output.Drilldown{
			Label:          fmt.Sprintf("Explain %s", filepath.Base(changedFiles[0].FilePath)),
			Query:          fmt.Sprintf("explainFile %s", changedFiles[0].FilePath),
			RelevanceScore: 0.85,
		})
	}
	if len(symbolsAffected) > 0 {
		response.Drilldowns = append(response.Drilldowns, output.Drilldown{
			Label:          fmt.Sprintf("Explore %s", symbolsAffected[0].Name),
			Query:          fmt.Sprintf("explainSymbol %s", symbolsAffected[0].SymbolId),
			RelevanceScore: 0.8,
		})
	}

	return response, nil
}

// classifyFileRiskLevel determines the risk level of a file change.
func classifyFileRiskLevel(stat git.DiffStats, role string) string {
	totalChanges := stat.Additions + stat.Deletions

	// Deleted files are always high risk
	if stat.IsDeleted {
		return "high"
	}

	// New files are generally low risk
	if stat.IsNew {
		return "low"
	}

	// Core files with large changes are high risk
	if role == "core" || role == "entrypoint" {
		if totalChanges > 100 {
			return "high"
		}
		if totalChanges > 30 {
			return "medium"
		}
	}

	// Config changes can have wide impact
	if role == "config" {
		return "medium"
	}

	// Test changes are lower risk
	if role == "test" {
		return "low"
	}

	if totalChanges > 200 {
		return "high"
	}
	if totalChanges > 50 {
		return "medium"
	}

	return "low"
}

// suggestTestPath suggests a test file path for a source file.
func suggestTestPath(filePath, language string) string {
	ext := filepath.Ext(filePath)
	base := strings.TrimSuffix(filePath, ext)

	switch language {
	case "go":
		return base + "_test.go"
	case "typescript", "javascript":
		return base + ".test" + ext
	case "python":
		dir := filepath.Dir(filePath)
		name := filepath.Base(base)
		return filepath.Join(dir, "test_"+name+".py")
	default:
		return ""
	}
}

// buildDiffSummary creates a human-readable summary of the diff.
func buildDiffSummary(files []DiffFileChange, symbols []DiffSymbolAffected, risks []DiffRiskSignal, commits []DiffCommitInfo) DiffSummaryText {
	// Count changes by type
	added, modified, deleted := 0, 0, 0
	for _, f := range files {
		switch f.ChangeType {
		case "added":
			added++
		case "modified":
			modified++
		case "deleted":
			deleted++
		}
	}

	// Build one-liner
	parts := []string{}
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", modified))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", deleted))
	}

	oneLiner := fmt.Sprintf("%d files changed", len(files))
	if len(parts) > 0 {
		oneLiner = strings.Join(parts, ", ")
	}
	if len(commits) > 0 {
		oneLiner = fmt.Sprintf("%s across %d commits", oneLiner, len(commits))
	}

	// Key changes
	keyChanges := []string{}
	for i, f := range files {
		if i >= 5 {
			break
		}
		keyChanges = append(keyChanges, fmt.Sprintf("%s (%s)", filepath.Base(f.FilePath), f.ChangeType))
	}

	// Risk overview
	riskOverview := ""
	highRisks := 0
	for _, r := range risks {
		if r.Severity == "high" {
			highRisks++
		}
	}
	if highRisks > 0 {
		riskOverview = fmt.Sprintf("%d high-risk signals detected", highRisks)
	} else if len(risks) > 0 {
		riskOverview = fmt.Sprintf("%d risk signals detected (no high severity)", len(risks))
	}

	return DiffSummaryText{
		OneLiner:     oneLiner,
		KeyChanges:   keyChanges,
		RiskOverview: riskOverview,
	}
}

// computeDiffConfidence computes confidence for summarizeDiff.
func computeDiffConfidence(basis []ConfidenceBasisItem, limitations []string) float64 {
	gitAvailable := false
	scipAvailable := false
	for _, b := range basis {
		if b.Backend == "git" && b.Status == "available" {
			gitAvailable = true
		}
		if b.Backend == "scip" && b.Status == "available" {
			scipAvailable = true
		}
	}

	if !gitAvailable {
		return 0.39 // Git is essential
	}

	if scipAvailable && len(limitations) == 0 {
		return 0.89 // Partial static analysis
	}

	if scipAvailable {
		return 0.79 // With limitations
	}

	return 0.69 // Git only
}

// GetHotspotsOptions controls getHotspots behavior.
type GetHotspotsOptions struct {
	TimeWindow     *TimeWindowSelector `json:"timeWindow,omitempty"`
	Scope          string              `json:"scope,omitempty"`          // Module to focus on
	Limit          int                 `json:"limit,omitempty"`          // Max results (default 20)
	SkipComplexity bool                `json:"skipComplexity,omitempty"` // Skip tree-sitter enrichment (faster)
}

// GetHotspotsResponse provides ranked hotspot files.
type GetHotspotsResponse struct {
	AINavigationMeta
	Hotspots        []HotspotV52          `json:"hotspots"`
	TotalCount      int                   `json:"totalCount"`
	TimeWindow      string                `json:"timeWindow"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations     []string              `json:"limitations,omitempty"`
}

// HotspotV52 represents a hotspot with v5.2 ranking signals.
type HotspotV52 struct {
	FilePath   string             `json:"filePath"`
	Role       string             `json:"role,omitempty"` // core, test, config, unknown
	Language   string             `json:"language,omitempty"`
	Churn      HotspotChurn       `json:"churn"`
	Coupling   *HotspotCoupling   `json:"coupling,omitempty"`
	Complexity *HotspotComplexity `json:"complexity,omitempty"` // v6.2.2: tree-sitter complexity
	Recency    string             `json:"recency"`              // recent, moderate, stale
	RiskLevel  string             `json:"riskLevel"`            // low, medium, high
	Ranking    *RankingV52        `json:"ranking"`
	// CochangePartners lists other files that frequently change together with this file
	// (from Cartographer temporal coupling analysis). Only populated when built with -tags cartographer.
	CochangePartners []string `json:"cochangePartners,omitempty"`
}

// HotspotChurn contains churn-related metrics.
type HotspotChurn struct {
	ChangeCount    int     `json:"changeCount"`
	AuthorCount    int     `json:"authorCount"`
	AverageChanges float64 `json:"averageChanges"`
	Score          float64 `json:"score"`
}

// HotspotCoupling contains coupling-related metrics.
type HotspotCoupling struct {
	DependentCount  int     `json:"dependentCount"`
	DependencyCount int     `json:"dependencyCount"`
	Score           float64 `json:"score"`
}

// HotspotComplexity contains code complexity metrics.
type HotspotComplexity struct {
	Cyclomatic    int     `json:"cyclomatic"`    // Max cyclomatic complexity
	Cognitive     int     `json:"cognitive"`     // Max cognitive complexity
	FunctionCount int     `json:"functionCount"` // Number of functions analyzed
	Score         float64 `json:"score"`         // Normalized complexity score (0-1)
}

// GetHotspots returns files that deserve attention based on churn, coupling, and recency.
func (e *Engine) GetHotspots(ctx context.Context, opts GetHotspotsOptions) (*GetHotspotsResponse, error) {
	startTime := time.Now()

	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 50 {
		opts.Limit = 50 // Hard cap per v5.2 spec
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	hotspots := []HotspotV52{}

	// Determine time window
	var timeWindowStr string
	var since string
	if opts.TimeWindow != nil && opts.TimeWindow.Start != "" {
		since = opts.TimeWindow.Start
		timeWindowStr = opts.TimeWindow.Start
		if opts.TimeWindow.End != "" {
			timeWindowStr += " to " + opts.TimeWindow.End
		}
	} else {
		// Default: 30 days
		since = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
		timeWindowStr = "last 30 days"
	}

	// Check git backend
	if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
		return nil, fmt.Errorf("git backend unavailable; getHotspots requires git")
	}
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "git",
		Status:  "available",
	})

	// Get hotspots from git backend
	gitHotspots, err := e.gitAdapter.GetHotspots(opts.Limit*2, since) // Get extra for filtering
	if err != nil {
		return nil, fmt.Errorf("failed to get hotspots: %w", err)
	}

	// Apply scope filter if specified
	if opts.Scope != "" {
		filtered := []git.ChurnMetrics{}
		for _, h := range gitHotspots {
			if strings.HasPrefix(h.FilePath, opts.Scope) {
				filtered = append(filtered, h)
			}
		}
		gitHotspots = filtered
	}

	// Convert to v5.2 format with enrichment
	for _, gh := range gitHotspots {
		role := classifyFileRole(gh.FilePath)
		language := detectLanguage(gh.FilePath)
		recency := classifyRecency(gh.LastModified)
		riskLevel := classifyHotspotRisk(gh, role)

		// Calculate ranking score
		// Score = churn_score * recency_multiplier * role_multiplier
		recencyMultiplier := 1.0
		switch recency {
		case "recent":
			recencyMultiplier = 1.5
		case "moderate":
			recencyMultiplier = 1.0
		case "stale":
			recencyMultiplier = 0.5
		}

		roleMultiplier := 1.0
		switch role {
		case "core", "entrypoint":
			roleMultiplier = 1.5
		case "test":
			roleMultiplier = 0.5
		case "config":
			roleMultiplier = 1.2
		}

		score := gh.HotspotScore * recencyMultiplier * roleMultiplier

		hotspot := HotspotV52{
			FilePath: gh.FilePath,
			Role:     role,
			Language: language,
			Churn: HotspotChurn{
				ChangeCount:    gh.ChangeCount,
				AuthorCount:    gh.AuthorCount,
				AverageChanges: gh.AverageChanges,
				Score:          gh.HotspotScore,
			},
			Recency:   recency,
			RiskLevel: riskLevel,
			Ranking: NewRankingV52(score, map[string]interface{}{
				"churn":    gh.HotspotScore,
				"coupling": 0.0, // Not yet implemented
				"recency":  recency,
			}),
		}

		hotspots = append(hotspots, hotspot)
	}

	// Sort by ranking score with deterministic tie-breaker
	sort.Slice(hotspots, func(i, j int) bool {
		if hotspots[i].Ranking.Score != hotspots[j].Ranking.Score {
			return hotspots[i].Ranking.Score > hotspots[j].Ranking.Score
		}
		return hotspots[i].FilePath < hotspots[j].FilePath
	})

	// Track total before limiting
	totalCount := len(hotspots)

	// Apply limit
	if len(hotspots) > opts.Limit {
		hotspots = hotspots[:opts.Limit]
	}

	// Add complexity data via tree-sitter (v6.2.2)
	if e.complexityAnalyzer != nil && !opts.SkipComplexity {
		for i := range hotspots {
			fc, err := e.complexityAnalyzer.GetFileComplexityFull(ctx, filepath.Join(e.repoRoot, hotspots[i].FilePath))
			if err == nil && fc.Error == "" && fc.FunctionCount > 0 {
				// Calculate normalized score (0-1) based on max complexity thresholds
				// Cyclomatic > 10 is high, > 20 is very high
				// Cognitive > 15 is high, > 30 is very high
				cyclomaticScore := float64(fc.MaxCyclomatic) / 20.0
				cognitiveScore := float64(fc.MaxCognitive) / 30.0
				if cyclomaticScore > 1.0 {
					cyclomaticScore = 1.0
				}
				if cognitiveScore > 1.0 {
					cognitiveScore = 1.0
				}
				// Use max of the two as the complexity score
				complexityScore := cyclomaticScore
				if cognitiveScore > complexityScore {
					complexityScore = cognitiveScore
				}

				hotspots[i].Complexity = &HotspotComplexity{
					Cyclomatic:    fc.MaxCyclomatic,
					Cognitive:     fc.MaxCognitive,
					FunctionCount: fc.FunctionCount,
					Score:         complexityScore,
				}
			}
		}
	}

	// Add coupling data if SCIP available
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "partial", // We're only using it for coupling hints
		})
		// Note: Full coupling analysis would require more work
		// For now, we mark SCIP as available but coupling is not fully implemented
		limitations = append(limitations, "Coupling analysis not yet implemented; using churn-only ranking")
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		limitations = append(limitations, "SCIP unavailable; coupling analysis skipped")
	}

	// Compute confidence
	confidence := 0.79 // Git churn is heuristic-based
	if len(limitations) > 1 {
		confidence = 0.69
	}

	// Build response
	response := &GetHotspotsResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "getHotspots",
		},
		Hotspots:        hotspots,
		TotalCount:      totalCount,
		TimeWindow:      timeWindowStr,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Augment hotspots with Cartographer co-change partners (temporal coupling).
	// Surfaces files that frequently change together — captured from git history.
	if cartographer.Available() && len(response.Hotspots) > 0 {
		if pairs, cerr := cartographer.GitCochange(e.repoRoot, 0, 3); cerr == nil && len(pairs) > 0 {
			hotspotSet := make(map[string]struct{}, len(response.Hotspots))
			for _, h := range response.Hotspots {
				hotspotSet[h.FilePath] = struct{}{}
			}
			// Build filePath → top partners map (limit to 3 per file).
			partnerMap := make(map[string][]string)
			for _, pair := range pairs {
				if _, isHotspot := hotspotSet[pair.FileA]; isHotspot {
					if len(partnerMap[pair.FileA]) < 3 {
						partnerMap[pair.FileA] = append(partnerMap[pair.FileA], pair.FileB)
					}
				}
				if _, isHotspot := hotspotSet[pair.FileB]; isHotspot {
					if len(partnerMap[pair.FileB]) < 3 {
						partnerMap[pair.FileB] = append(partnerMap[pair.FileB], pair.FileA)
					}
				}
			}
			for i := range response.Hotspots {
				if partners, ok := partnerMap[response.Hotspots[i].FilePath]; ok {
					response.Hotspots[i].CochangePartners = partners
				}
			}
		}
	}

	// Add drilldowns
	if len(hotspots) > 0 {
		response.Drilldowns = []output.Drilldown{
			{
				Label:          fmt.Sprintf("Explain %s", filepath.Base(hotspots[0].FilePath)),
				Query:          fmt.Sprintf("explainFile %s", hotspots[0].FilePath),
				RelevanceScore: 0.9,
			},
			{
				Label:          "View recent changes",
				Query:          "summarizeDiff",
				RelevanceScore: 0.8,
			},
		}
	}

	return response, nil
}

// classifyRecency determines recency category based on last modified date.
func classifyRecency(lastModified string) string {
	if lastModified == "" {
		return "stale"
	}

	// Parse ISO8601 timestamp
	t, err := time.Parse(time.RFC3339, lastModified)
	if err != nil {
		// Try alternate format
		t, err = time.Parse("2006-01-02T15:04:05-07:00", lastModified)
		if err != nil {
			return "stale"
		}
	}

	daysSince := time.Since(t).Hours() / 24

	switch {
	case daysSince <= 7:
		return "recent"
	case daysSince <= 30:
		return "moderate"
	default:
		return "stale"
	}
}

// classifyHotspotRisk determines risk level for a hotspot.
func classifyHotspotRisk(churn git.ChurnMetrics, role string) string {
	// High churn + core file = high risk
	if churn.ChangeCount > 20 && (role == "core" || role == "entrypoint") {
		return "high"
	}

	// Many authors = potential coordination risk
	if churn.AuthorCount > 5 {
		return "high"
	}

	// High churn in any file
	if churn.ChangeCount > 30 {
		return "high"
	}

	// Moderate churn
	if churn.ChangeCount > 10 {
		return "medium"
	}

	// Test files with high churn are medium risk (tests should be stable)
	if role == "test" && churn.ChangeCount > 15 {
		return "medium"
	}

	return "low"
}

// ExplainPathOptions controls explainPath behavior.
type ExplainPathOptions struct {
	FilePath    string `json:"filePath"`
	ContextHint string `json:"contextHint,omitempty"` // e.g., "from traceUsage"
}

// ExplainPathResponse explains why a path exists and what role it plays.
type ExplainPathResponse struct {
	AINavigationMeta
	FilePath            string                `json:"filePath"`
	Role                string                `json:"role"` // core, glue, legacy, test-only, config, unknown
	RoleExplanation     string                `json:"roleExplanation"`
	ClassificationBasis []ClassificationBasis `json:"classificationBasis"`
	RelatedPaths        []RelatedPath         `json:"relatedPaths,omitempty"`
	Confidence          float64               `json:"confidence"`
	ConfidenceBasis     []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations         []string              `json:"limitations,omitempty"`
}

// ClassificationBasis explains how a role was determined.
type ClassificationBasis struct {
	Type       string  `json:"type"` // naming, location, usage, history
	Signal     string  `json:"signal"`
	Confidence float64 `json:"confidence"`
}

// RelatedPath represents a path related to the explained one.
type RelatedPath struct {
	Path     string `json:"path"`
	Relation string `json:"relation"` // test-for, config-for, imports, imported-by
}

// ExplainPath explains why a path exists and what role it plays.
func (e *Engine) ExplainPath(ctx context.Context, opts ExplainPathOptions) (*ExplainPathResponse, error) {
	startTime := time.Now()

	if opts.FilePath == "" {
		return nil, fmt.Errorf("filePath is required")
	}

	// Clean and validate path
	filePath := filepath.Clean(opts.FilePath)
	if !filepath.IsAbs(filePath) {
		filePath = filepath.Join(e.repoRoot, filePath)
	}

	// Security: verify path is within repo root
	repoRootClean := filepath.Clean(e.repoRoot)
	if !strings.HasPrefix(filePath, repoRootClean+string(filepath.Separator)) && filePath != repoRootClean {
		return nil, fmt.Errorf("path outside repository: %s", opts.FilePath)
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	var classificationBasis []ClassificationBasis

	// Get relative path for analysis
	relPath, _ := filepath.Rel(e.repoRoot, filePath)
	if relPath == "" {
		relPath = opts.FilePath
	}

	// Classify the file role using multiple signals
	role, explanation, basis := classifyPathRole(relPath)
	classificationBasis = basis

	// Add naming-based confidence
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "naming",
		Status:  "available",
	})

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		limitations = append(limitations, "File does not exist; classification based on path only")
	}

	// Find related paths
	relatedPaths := findRelatedPaths(relPath, e.repoRoot)

	// Add location-based confidence
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend:   "location",
		Status:    "available",
		Heuristic: "path-pattern-matching",
	})

	// Compute confidence based on classification basis
	confidence := computePathConfidence(classificationBasis)
	if len(limitations) > 0 {
		confidence *= 0.8
	}

	// Build response
	response := &ExplainPathResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "explainPath",
		},
		FilePath:            relPath,
		Role:                role,
		RoleExplanation:     explanation,
		ClassificationBasis: classificationBasis,
		RelatedPaths:        relatedPaths,
		Confidence:          confidence,
		ConfidenceBasis:     confidenceBasis,
		Limitations:         limitations,
	}

	// Add standard limitation
	if len(limitations) == 0 {
		limitations = append(limitations, "Intent inferred from static signals; actual purpose may differ")
		response.Limitations = limitations
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	response.Drilldowns = []output.Drilldown{
		{
			Label:          "Explain file contents",
			Query:          fmt.Sprintf("explainFile %s", relPath),
			RelevanceScore: 0.9,
		},
	}

	return response, nil
}

// classifyPathRole classifies a file path and returns role, explanation, and basis.
func classifyPathRole(path string) (string, string, []ClassificationBasis) {
	pathLower := strings.ToLower(path)
	var basis []ClassificationBasis

	// Test files
	if strings.Contains(pathLower, "_test.") || strings.Contains(pathLower, ".test.") ||
		strings.Contains(pathLower, "/test/") || strings.Contains(pathLower, "/tests/") ||
		strings.Contains(pathLower, "/__tests__/") || strings.HasPrefix(pathLower, "test_") {
		basis = append(basis, ClassificationBasis{
			Type:       "naming",
			Signal:     "test file pattern",
			Confidence: 0.95,
		})
		return "test-only", "Test file based on naming convention", basis
	}

	// Config files
	configPatterns := []string{
		"config", ".json", ".yaml", ".yml", ".toml", ".ini", ".env",
		"dockerfile", "makefile", ".mod", ".sum", "package.json",
		"tsconfig", "webpack", "babel", ".eslint", ".prettier",
	}
	for _, pattern := range configPatterns {
		if strings.Contains(pathLower, pattern) {
			basis = append(basis, ClassificationBasis{
				Type:       "naming",
				Signal:     fmt.Sprintf("config pattern: %s", pattern),
				Confidence: 0.85,
			})
			return "config", "Configuration file based on naming pattern", basis
		}
	}

	// Documentation
	if strings.HasSuffix(pathLower, ".md") || strings.HasSuffix(pathLower, ".txt") ||
		strings.HasSuffix(pathLower, ".rst") || strings.Contains(pathLower, "/docs/") {
		basis = append(basis, ClassificationBasis{
			Type:       "naming",
			Signal:     "documentation pattern",
			Confidence: 0.9,
		})
		return "unknown", "Documentation file (not code)", basis
	}

	// Vendor/external
	if strings.Contains(pathLower, "/vendor/") || strings.Contains(pathLower, "/node_modules/") ||
		strings.Contains(pathLower, "/third_party/") {
		basis = append(basis, ClassificationBasis{
			Type:       "location",
			Signal:     "vendor directory",
			Confidence: 0.95,
		})
		return "unknown", "External/vendored code", basis
	}

	// Glue/integration files
	gluePatterns := []string{
		"adapter", "bridge", "facade", "wrapper", "middleware",
		"handler", "controller", "router", "routes",
	}
	for _, pattern := range gluePatterns {
		if strings.Contains(pathLower, pattern) {
			basis = append(basis, ClassificationBasis{
				Type:       "naming",
				Signal:     fmt.Sprintf("glue pattern: %s", pattern),
				Confidence: 0.75,
			})
			return "glue", "Integration/glue code based on naming pattern", basis
		}
	}

	// Legacy indicators
	legacyPatterns := []string{"legacy", "deprecated", "old", "v1", "compat"}
	for _, pattern := range legacyPatterns {
		if strings.Contains(pathLower, pattern) {
			basis = append(basis, ClassificationBasis{
				Type:       "naming",
				Signal:     fmt.Sprintf("legacy pattern: %s", pattern),
				Confidence: 0.7,
			})
			return "legacy", "Potentially legacy code based on naming", basis
		}
	}

	// Entry points
	if strings.Contains(pathLower, "/cmd/") || strings.HasSuffix(pathLower, "main.go") ||
		strings.HasSuffix(pathLower, "index.ts") || strings.HasSuffix(pathLower, "index.js") ||
		strings.Contains(pathLower, "__main__") {
		basis = append(basis, ClassificationBasis{
			Type:       "location",
			Signal:     "entry point location",
			Confidence: 0.85,
		})
		return "core", "Entry point / main module", basis
	}

	// Core business logic locations
	corePatterns := []string{"/internal/", "/src/", "/lib/", "/pkg/", "/core/", "/domain/", "/services/"}
	for _, pattern := range corePatterns {
		if strings.Contains(pathLower, pattern) {
			basis = append(basis, ClassificationBasis{
				Type:       "location",
				Signal:     fmt.Sprintf("core location: %s", pattern),
				Confidence: 0.7,
			})
			return "core", "Core business logic based on location", basis
		}
	}

	// Default: unknown with low confidence
	basis = append(basis, ClassificationBasis{
		Type:       "naming",
		Signal:     "no strong pattern match",
		Confidence: 0.5,
	})
	return "unknown", "Could not determine role from path alone", basis
}

// findRelatedPaths finds paths related to the given path.
func findRelatedPaths(path, repoRoot string) []RelatedPath {
	var related []RelatedPath
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	dir := filepath.Dir(path)

	// Look for test file
	testPatterns := []string{
		base + "_test" + ext,
		base + ".test" + ext,
		filepath.Join(dir, "test_"+filepath.Base(base)+ext),
	}
	for _, testPath := range testPatterns {
		fullPath := filepath.Join(repoRoot, testPath)
		if _, err := os.Stat(fullPath); err == nil {
			related = append(related, RelatedPath{
				Path:     testPath,
				Relation: "test-for",
			})
			break
		}
	}

	// Look for config file
	configPatterns := []string{
		filepath.Join(dir, "config.json"),
		filepath.Join(dir, "config.yaml"),
		filepath.Join(dir, filepath.Base(base)+".config"+ext),
	}
	for _, configPath := range configPatterns {
		fullPath := filepath.Join(repoRoot, configPath)
		if _, err := os.Stat(fullPath); err == nil {
			related = append(related, RelatedPath{
				Path:     configPath,
				Relation: "config-for",
			})
			break
		}
	}

	return related
}

// computePathConfidence computes confidence from classification basis.
func computePathConfidence(basis []ClassificationBasis) float64 {
	if len(basis) == 0 {
		return 0.5
	}

	// Use the highest confidence from the basis
	maxConf := 0.0
	for _, b := range basis {
		if b.Confidence > maxConf {
			maxConf = b.Confidence
		}
	}

	// Cap at 0.79 since this is heuristic-only
	if maxConf > 0.79 {
		maxConf = 0.79
	}

	return maxConf
}

// ListKeyConceptsOptions controls listKeyConcepts behavior.
type ListKeyConceptsOptions struct {
	Limit int `json:"limit,omitempty"` // Max concepts (default 12, max 12)
}

// ListKeyConceptsResponse provides main ideas/concepts in the codebase.
type ListKeyConceptsResponse struct {
	AINavigationMeta
	Concepts        []ConceptV52          `json:"concepts"`
	TotalFound      int                   `json:"totalFound"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations     []string              `json:"limitations,omitempty"`
}

// ConceptV52 represents a key concept in the codebase.
type ConceptV52 struct {
	Name        string      `json:"name"`
	Category    string      `json:"category"` // domain, technical, pattern
	Occurrences int         `json:"occurrences"`
	Files       []string    `json:"files,omitempty"`
	Symbols     []string    `json:"symbols,omitempty"` // Sample symbol IDs
	Description string      `json:"description,omitempty"`
	Ranking     *RankingV52 `json:"ranking"`
}

// ListKeyConcepts discovers main ideas/concepts in the codebase through semantic clustering.
func (e *Engine) ListKeyConcepts(ctx context.Context, opts ListKeyConceptsOptions) (*ListKeyConceptsResponse, error) {
	startTime := time.Now()

	if opts.Limit <= 0 {
		opts.Limit = 12
	}
	if opts.Limit > 12 {
		opts.Limit = 12 // Hard cap per v5.2 spec
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	concepts := []ConceptV52{}

	// Concept extraction strategy:
	// 1. Extract from package/module names
	// 2. Extract from type/struct names
	// 3. Extract from common prefixes in symbols
	// 4. Rank by frequency and spread

	conceptCounts := make(map[string]*conceptData)

	// Get symbols from SCIP if available
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})

		// Search for various symbol types to extract concepts
		searchTerms := []string{"", "Handler", "Service", "Manager", "Client", "Server", "Config", "Error"}
		for _, term := range searchTerms {
			results, _ := e.scipAdapter.SearchSymbols(ctx, term, backends.SearchOptions{
				MaxResults: 100,
				Kind:       []string{"class", "interface", "struct", "type"},
			})

			if results != nil {
				for _, sym := range results.Symbols {
					// Extract concept from symbol name
					conceptName := extractConcept(sym.Name)
					if conceptName == "" {
						continue
					}

					if _, exists := conceptCounts[conceptName]; !exists {
						conceptCounts[conceptName] = &conceptData{
							files:   make(map[string]bool),
							symbols: []string{},
						}
					}

					cd := conceptCounts[conceptName]
					cd.count++
					cd.files[sym.Location.Path] = true
					if len(cd.symbols) < 3 {
						cd.symbols = append(cd.symbols, sym.StableID)
					}
				}
			}
		}

		// Also extract from function/method names
		funcResults, _ := e.scipAdapter.SearchSymbols(ctx, "", backends.SearchOptions{
			MaxResults: 200,
			Kind:       []string{"function", "method"},
		})

		if funcResults != nil {
			for _, sym := range funcResults.Symbols {
				conceptName := extractConcept(sym.Name)
				if conceptName == "" {
					continue
				}

				if _, exists := conceptCounts[conceptName]; !exists {
					conceptCounts[conceptName] = &conceptData{
						files:   make(map[string]bool),
						symbols: []string{},
					}
				}

				cd := conceptCounts[conceptName]
				cd.count++
				cd.files[sym.Location.Path] = true
			}
		}
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		limitations = append(limitations, "SCIP index unavailable; concept extraction limited")

		usedCartographer := false
		if cartographer.Available() {
			// Cartographer has a pre-built, ignore-aware file index — faster than
			// walking the FS and adds module-level grouping for richer concepts.
			if graph, cerr := cartographer.MapProject(e.repoRoot); cerr == nil {
				usedCartographer = true
				for _, node := range graph.Nodes {
					ext := filepath.Ext(node.Path)
					name := strings.TrimSuffix(filepath.Base(node.Path), ext)
					name = strings.TrimSuffix(name, "_test")
					name = strings.TrimSuffix(name, ".test")

					if conceptName := extractConcept(name); conceptName != "" {
						if _, exists := conceptCounts[conceptName]; !exists {
							conceptCounts[conceptName] = &conceptData{files: make(map[string]bool)}
						}
						conceptCounts[conceptName].count++
						conceptCounts[conceptName].files[node.Path] = true
					}

					// Module ID (directory) carries semantic grouping — contributes
					// an extra signal toward concepts that span multiple files.
					if node.ModuleID != "" {
						if modConcept := extractConcept(filepath.Base(node.ModuleID)); modConcept != "" {
							if _, exists := conceptCounts[modConcept]; !exists {
								conceptCounts[modConcept] = &conceptData{files: make(map[string]bool)}
							}
							conceptCounts[modConcept].count++
							conceptCounts[modConcept].files[node.Path] = true
						}
					}
				}
			}
		}

		if !usedCartographer {
			// Last resort: extract from file/directory names via OS walk.
			_ = filepath.WalkDir(e.repoRoot, func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "node_modules" {
						return filepath.SkipDir
					}
					return nil
				}

				ext := filepath.Ext(path)
				if ext != ".go" && ext != ".ts" && ext != ".js" && ext != ".py" {
					return nil
				}

				name := strings.TrimSuffix(filepath.Base(path), ext)
				name = strings.TrimSuffix(name, "_test")
				name = strings.TrimSuffix(name, ".test")

				conceptName := extractConcept(name)
				if conceptName == "" {
					return nil
				}

				relPath, _ := filepath.Rel(e.repoRoot, path)

				if _, exists := conceptCounts[conceptName]; !exists {
					conceptCounts[conceptName] = &conceptData{
						files:   make(map[string]bool),
						symbols: []string{},
					}
				}
				conceptCounts[conceptName].count++
				conceptCounts[conceptName].files[relPath] = true
				return nil
			})
		}
	}

	// Convert to concepts and rank
	for name, data := range conceptCounts {
		// Skip if too few occurrences or single file
		if data.count < 2 || len(data.files) < 1 {
			continue
		}

		files := make([]string, 0, len(data.files))
		for f := range data.files {
			files = append(files, f)
			if len(files) >= 5 {
				break
			}
		}

		category := categorizeConceptV52(name)
		description := generateConceptDescription(name, category, data.count, len(data.files))

		// Score based on occurrence count and file spread
		score := float64(data.count) * float64(len(data.files))

		concepts = append(concepts, ConceptV52{
			Name:        name,
			Category:    category,
			Occurrences: data.count,
			Files:       files,
			Symbols:     data.symbols,
			Description: description,
			Ranking: NewRankingV52(score, map[string]interface{}{
				"occurrences": data.count,
				"fileSpread":  len(data.files),
			}),
		})
	}

	// Sort by ranking score with deterministic tie-breaker
	sort.Slice(concepts, func(i, j int) bool {
		if concepts[i].Ranking.Score != concepts[j].Ranking.Score {
			return concepts[i].Ranking.Score > concepts[j].Ranking.Score
		}
		return concepts[i].Name < concepts[j].Name
	})

	// Track total before limiting
	totalFound := len(concepts)

	// Apply limit
	if len(concepts) > opts.Limit {
		concepts = concepts[:opts.Limit]
	}

	// Compute confidence
	confidence := 0.69 // Heuristic extraction
	for _, b := range confidenceBasis {
		if b.Backend == "scip" && b.Status == "available" {
			confidence = 0.79
		}
	}

	// Build response
	response := &ListKeyConceptsResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "listKeyConcepts",
		},
		Concepts:        concepts,
		TotalFound:      totalFound,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	if len(concepts) > 0 {
		response.Drilldowns = []output.Drilldown{
			{
				Label:          "View architecture",
				Query:          "getArchitecture",
				RelevanceScore: 0.85,
			},
		}
		if len(concepts[0].Symbols) > 0 {
			response.Drilldowns = append(response.Drilldowns, output.Drilldown{
				Label:          fmt.Sprintf("Explore %s", concepts[0].Name),
				Query:          fmt.Sprintf("explainSymbol %s", concepts[0].Symbols[0]),
				RelevanceScore: 0.8,
			})
		}
	}

	return response, nil
}

// conceptData holds intermediate concept data during extraction.
type conceptData struct {
	count   int
	files   map[string]bool
	symbols []string
}

// extractConcept extracts a concept name from a symbol or file name.
func extractConcept(name string) string {
	// Skip common non-concept names
	skipNames := map[string]bool{
		"main": true, "init": true, "new": true, "get": true, "set": true,
		"run": true, "start": true, "stop": true, "close": true, "open": true,
		"read": true, "write": true, "do": true, "make": true, "create": true,
		"delete": true, "update": true, "list": true, "find": true, "err": true,
		"error": true, "test": true, "mock": true, "stub": true,
	}

	if skipNames[strings.ToLower(name)] {
		return ""
	}

	// Split camelCase/PascalCase
	words := splitCamelCase(name)
	if len(words) == 0 {
		return ""
	}

	// Return the most significant word (usually the last non-suffix)
	suffixes := map[string]bool{
		"handler": true, "service": true, "manager": true, "client": true,
		"server": true, "config": true, "error": true, "impl": true,
		"factory": true, "builder": true, "provider": true, "adapter": true,
	}

	// Find concept word (skip common suffixes)
	for i := len(words) - 1; i >= 0; i-- {
		word := strings.ToLower(words[i])
		if !suffixes[word] && len(word) > 2 {
			return titleCase(word)
		}
	}

	// If all words are suffixes, use the first one
	if len(words) > 0 && len(words[0]) > 2 {
		return titleCase(strings.ToLower(words[0]))
	}

	return ""
}

// titleCase returns the string with the first letter capitalized.
func titleCase(s string) string {
	if len(s) == 0 {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// splitCamelCase splits a camelCase or PascalCase string into words.
func splitCamelCase(s string) []string {
	var words []string
	var current strings.Builder

	for i, r := range s {
		if i > 0 && (r >= 'A' && r <= 'Z') {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		}
		current.WriteRune(r)
	}

	if current.Len() > 0 {
		words = append(words, current.String())
	}

	return words
}

// categorizeConceptV52 categorizes a concept as domain, technical, or pattern.
func categorizeConceptV52(name string) string {
	nameLower := strings.ToLower(name)

	// Technical patterns
	technicalTerms := map[string]bool{
		"cache": true, "queue": true, "pool": true, "buffer": true,
		"socket": true, "stream": true, "worker": true, "channel": true,
		"mutex": true, "lock": true, "sync": true, "async": true,
		"http": true, "grpc": true, "rest": true, "api": true,
		"database": true, "query": true, "index": true, "schema": true,
	}

	if technicalTerms[nameLower] {
		return "technical"
	}

	// Pattern names
	patternTerms := map[string]bool{
		"factory": true, "builder": true, "singleton": true, "adapter": true,
		"observer": true, "strategy": true, "decorator": true, "facade": true,
		"proxy": true, "bridge": true, "composite": true, "visitor": true,
	}

	if patternTerms[nameLower] {
		return "pattern"
	}

	// Default to domain
	return "domain"
}

// generateConceptDescription generates a description for a concept.
func generateConceptDescription(name, category string, occurrences, fileCount int) string {
	switch category {
	case "technical":
		return fmt.Sprintf("Technical concept '%s' found in %d files", name, fileCount)
	case "pattern":
		return fmt.Sprintf("Design pattern '%s' used across %d files", name, fileCount)
	default:
		return fmt.Sprintf("Domain concept '%s' with %d occurrences in %d files", name, occurrences, fileCount)
	}
}

// RecentlyRelevantOptions controls recentlyRelevant behavior.
type RecentlyRelevantOptions struct {
	TimeWindow   *TimeWindowSelector `json:"timeWindow,omitempty"`
	ModuleFilter string              `json:"moduleFilter,omitempty"`
	Limit        int                 `json:"limit,omitempty"` // Max results (default 20)
}

// RecentlyRelevantResponse provides recently active files/symbols.
type RecentlyRelevantResponse struct {
	AINavigationMeta
	Items           []RecentItem          `json:"items"`
	TotalCount      int                   `json:"totalCount"`
	TimeWindow      string                `json:"timeWindow"`
	Confidence      float64               `json:"confidence"`
	ConfidenceBasis []ConfidenceBasisItem `json:"confidenceBasis"`
	Limitations     []string              `json:"limitations,omitempty"`
}

// RecentItem represents a recently relevant file or symbol.
type RecentItem struct {
	Type         string      `json:"type"` // file, symbol
	Path         string      `json:"path,omitempty"`
	SymbolId     string      `json:"symbolId,omitempty"`
	Name         string      `json:"name"`
	LastModified string      `json:"lastModified"`
	ChangeCount  int         `json:"changeCount"`
	Authors      []string    `json:"authors,omitempty"`
	Ranking      *RankingV52 `json:"ranking"`
}

// RecentlyRelevant finds what matters now - files/symbols with recent activity.
func (e *Engine) RecentlyRelevant(ctx context.Context, opts RecentlyRelevantOptions) (*RecentlyRelevantResponse, error) {
	startTime := time.Now()

	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	var confidenceBasis []ConfidenceBasisItem
	var limitations []string
	items := []RecentItem{}

	// Determine time window
	var timeWindowStr string
	var since string
	if opts.TimeWindow != nil && opts.TimeWindow.Start != "" {
		since = opts.TimeWindow.Start
		timeWindowStr = opts.TimeWindow.Start
		if opts.TimeWindow.End != "" {
			timeWindowStr += " to " + opts.TimeWindow.End
		}
	} else {
		// Default: 7 days
		since = time.Now().AddDate(0, 0, -7).Format("2006-01-02")
		timeWindowStr = "last 7 days"
	}

	// Check git backend
	if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
		return nil, fmt.Errorf("git backend unavailable; recentlyRelevant requires git")
	}
	confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
		Backend: "git",
		Status:  "available",
	})

	// Get commits in time window
	commits, err := e.gitAdapter.GetCommitsSinceDate(since, 500)
	if err != nil {
		return nil, fmt.Errorf("failed to get commits: %w", err)
	}

	// Aggregate file changes
	fileChanges := make(map[string]*recentFileData)
	for _, commit := range commits {
		// Get files changed in this commit
		diffStats, err := e.gitAdapter.GetCommitDiff(commit.Hash)
		if err != nil {
			continue
		}

		for _, stat := range diffStats {
			filePath := stat.FilePath

			// Apply module filter if specified
			if opts.ModuleFilter != "" && !strings.HasPrefix(filePath, opts.ModuleFilter) {
				continue
			}

			if _, exists := fileChanges[filePath]; !exists {
				fileChanges[filePath] = &recentFileData{
					authors:      make(map[string]bool),
					lastModified: commit.Timestamp,
				}
			}

			fd := fileChanges[filePath]
			fd.changeCount++
			fd.authors[commit.Author] = true

			// Update last modified if more recent
			if commit.Timestamp > fd.lastModified {
				fd.lastModified = commit.Timestamp
			}
		}
	}

	// Convert to items
	for path, data := range fileChanges {
		authors := make([]string, 0, len(data.authors))
		for a := range data.authors {
			authors = append(authors, a)
			if len(authors) >= 3 {
				break
			}
		}

		// Calculate score based on recency and change frequency
		recencyScore := computeRecencyScore(data.lastModified)
		changeScore := float64(data.changeCount)
		authorScore := float64(len(data.authors)) * 0.5
		score := (recencyScore * 10) + changeScore + authorScore

		items = append(items, RecentItem{
			Type:         "file",
			Path:         path,
			Name:         filepath.Base(path),
			LastModified: data.lastModified,
			ChangeCount:  data.changeCount,
			Authors:      authors,
			Ranking: NewRankingV52(score, map[string]interface{}{
				"recency":     recencyScore,
				"changeCount": data.changeCount,
				"authorCount": len(data.authors),
			}),
		})
	}

	// Sort by ranking score with deterministic tie-breaker
	sort.Slice(items, func(i, j int) bool {
		if items[i].Ranking.Score != items[j].Ranking.Score {
			return items[i].Ranking.Score > items[j].Ranking.Score
		}
		return items[i].Path < items[j].Path
	})

	// Track total before limiting
	totalCount := len(items)

	// Apply limit
	if len(items) > opts.Limit {
		items = items[:opts.Limit]
	}

	// Add SCIP status
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "available",
		})
	} else {
		confidenceBasis = append(confidenceBasis, ConfidenceBasisItem{
			Backend: "scip",
			Status:  "missing",
		})
		limitations = append(limitations, "SCIP unavailable; symbol-level relevance not included")
	}

	// Compute confidence
	confidence := 0.89 // Git activity is reliable
	if len(limitations) > 0 {
		confidence = 0.79
	}

	// Build response
	response := &RecentlyRelevantResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    "5.2",
			SchemaVersion: 1,
			Tool:          "recentlyRelevant",
		},
		Items:           items,
		TotalCount:      totalCount,
		TimeWindow:      timeWindowStr,
		Confidence:      confidence,
		ConfidenceBasis: confidenceBasis,
		Limitations:     limitations,
	}

	// Add provenance
	repoState, _ := e.GetRepoState(ctx, "head")
	response.Provenance = &Provenance{
		RepoStateId:     repoState.RepoStateId,
		RepoStateDirty:  repoState.Dirty,
		QueryDurationMs: time.Since(startTime).Milliseconds(),
	}

	// Add drilldowns
	if len(items) > 0 {
		response.Drilldowns = []output.Drilldown{
			{
				Label:          fmt.Sprintf("Explain %s", items[0].Name),
				Query:          fmt.Sprintf("explainFile %s", items[0].Path),
				RelevanceScore: 0.9,
			},
			{
				Label:          "View hotspots",
				Query:          "getHotspots",
				RelevanceScore: 0.85,
			},
			{
				Label:          "Summarize changes",
				Query:          "summarizeDiff",
				RelevanceScore: 0.8,
			},
		}
	}

	return response, nil
}

// recentFileData holds intermediate data for recently changed files.
type recentFileData struct {
	changeCount  int
	authors      map[string]bool
	lastModified string
}

// computeRecencyScore computes a score based on how recent a timestamp is.
func computeRecencyScore(timestamp string) float64 {
	if timestamp == "" {
		return 0
	}

	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		// Try alternate format
		t, err = time.Parse("2006-01-02T15:04:05-07:00", timestamp)
		if err != nil {
			return 0
		}
	}

	daysSince := time.Since(t).Hours() / 24

	switch {
	case daysSince <= 1:
		return 10.0
	case daysSince <= 3:
		return 8.0
	case daysSince <= 7:
		return 6.0
	case daysSince <= 14:
		return 4.0
	case daysSince <= 30:
		return 2.0
	default:
		return 1.0
	}
}

// getRelatedDocs returns documentation that references a symbol (v7.3).
func (e *Engine) getRelatedDocs(symbolID string, limit int) []RelatedDoc {
	refs, err := e.GetDocsForSymbol(symbolID, limit)
	if err != nil || len(refs) == 0 {
		return nil
	}

	result := make([]RelatedDoc, 0, len(refs))
	for _, ref := range refs {
		doc := RelatedDoc{
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
