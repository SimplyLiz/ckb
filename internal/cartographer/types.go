// Package cartographer provides CGo bindings to the Rust cartographer library
// (formerly cartographer).
package cartographer

import "errors"

// ErrUnavailable is returned by stub builds when the cartographer library is not
// compiled in. Callers check Available() first; under `-tags cartographer` this
// value is never returned but still needs to be referenceable by tool impls.
var ErrUnavailable = errors.New("cartographer: not compiled in this build (use -tags cartographer)")

// ---------------------------------------------------------------------------
// Public types (shared between real bridge and stub builds)
// ---------------------------------------------------------------------------

// ProjectGraph is the full dependency graph returned by MapProject.
type ProjectGraph struct {
	Nodes           []GraphNode      `json:"nodes"`
	Edges           []GraphEdge      `json:"edges"`
	Cycles          []CycleInfo      `json:"cycles"`
	GodModules      []GodModuleInfo  `json:"godModules"`
	LayerViolations []LayerViolation `json:"layerViolations"`
	Metadata        GraphMetadata    `json:"metadata"`
}

// GraphNode represents a file/module in the dependency graph.
type GraphNode struct {
	ModuleID       string   `json:"moduleId"`
	Path           string   `json:"path"`
	Language       string   `json:"language"`
	SignatureCount int      `json:"signatureCount"`
	IsBridge       *bool    `json:"isBridge,omitempty"`
	BridgeScore    *float64 `json:"bridgeScore,omitempty"`
	Degree         *int     `json:"degree,omitempty"`
	RiskLevel      *string  `json:"riskLevel,omitempty"`
}

// GraphEdge represents an import/dependency relationship.
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// EdgeType is the kind of the importing file: "runtime", "test" or "doc",
	// or "type" for a type-only import (TS `import type`, Python under
	// `if TYPE_CHECKING:`), which Cartographer leaves out of cycle detection.
	// Module-level graphs carry only "runtime" and "type".
	EdgeType string `json:"edgeType"`
	// Resolution is the confidence with which the import was resolved:
	// "exact", "package" (a Go import resolved to its package directory: one
	// edge to every file of that package, so count the directory, not the
	// files, as one dependency), "suffix", or "fuzzy". Treat "fuzzy" edges as low-confidence.
	Resolution string `json:"resolution,omitempty"`
}

// GraphMetadata contains aggregate statistics.
type GraphMetadata struct {
	TotalFiles          int            `json:"totalFiles"`
	TotalEdges          int            `json:"totalEdges"`
	Languages           map[string]int `json:"languages"`
	GeneratedAt         string         `json:"generatedAt"`
	BridgeCount         *int           `json:"bridgeCount,omitempty"`
	CycleCount          *int           `json:"cycleCount,omitempty"`
	GodModuleCount      *int           `json:"godModuleCount,omitempty"`
	HealthScore         *float64       `json:"healthScore,omitempty"`
	LayerViolationCount *int           `json:"layerViolationCount,omitempty"`
	ArchitecturalDrift  *float64       `json:"architecturalDrift,omitempty"`
}

// CycleInfo describes a circular dependency.
type CycleInfo struct {
	Nodes     []string `json:"nodes"`
	PivotNode *string  `json:"pivotNode,omitempty"`
	Severity  string   `json:"severity"`
}

// GodModuleInfo describes an overly connected module.
type GodModuleInfo struct {
	ModuleID      string  `json:"moduleId"`
	Path          string  `json:"path"`
	Degree        int     `json:"degree"`
	CohesionScore float64 `json:"cohesionScore"`
	Severity      string  `json:"severity"`
}

// LayerViolation describes an architectural boundary crossing.
type LayerViolation struct {
	SourcePath    string `json:"sourcePath"`
	TargetPath    string `json:"targetPath"`
	SourceLayer   string `json:"sourceLayer"`
	TargetLayer   string `json:"targetLayer"`
	ViolationType string `json:"violationType"`
	Severity      string `json:"severity"`
}

// HealthReport contains the architectural health assessment.
type HealthReport struct {
	HealthScore         float64 `json:"healthScore"`
	TotalFiles          int     `json:"totalFiles"`
	TotalEdges          int     `json:"totalEdges"`
	BridgeCount         int     `json:"bridgeCount"`
	CycleCount          int     `json:"cycleCount"`
	GodModuleCount      int     `json:"godModuleCount"`
	LayerViolationCount int     `json:"layerViolationCount"`
}

// ImpactAnalysis is the predicted effect of a change.
type ImpactAnalysis struct {
	TargetModule    string          `json:"targetModule"`
	PredictedImpact PredictedImpact `json:"predictedImpact"`
}

// PredictedImpact details the effects of a simulated change.
type PredictedImpact struct {
	AffectedModules []string         `json:"affectedModules"`
	CallersCount    int              `json:"callersCount"`
	CalleesCount    int              `json:"calleesCount"`
	WillCreateCycle bool             `json:"willCreateCycle"`
	LayerViolations []LayerViolation `json:"layerViolations"`
	RiskLevel       string           `json:"riskLevel"`
	HealthImpact    float64          `json:"healthImpact"`
}

// SkeletonResult is a token-optimized view of the codebase.
type SkeletonResult struct {
	Files           []SkeletonFile `json:"files"`
	TotalFiles      int            `json:"totalFiles"`
	TotalSignatures int            `json:"totalSignatures"`
	EstimatedTokens int            `json:"estimatedTokens"`
	DetailLevel     string         `json:"detailLevel"`
}

// SkeletonFile is a single file's skeleton (signatures only, no bodies).
type SkeletonFile struct {
	Path       string   `json:"path"`
	Imports    []string `json:"imports"`
	Signatures []string `json:"signatures"`
}

// ModuleContext provides a single module's skeleton with optional dependencies.
type ModuleContext struct {
	Module       SkeletonFile     `json:"module"`
	Dependencies []DependencyInfo `json:"dependencies"`
}

// DependencyInfo describes a module dependency.
type DependencyInfo struct {
	ModuleID       string `json:"moduleId"`
	Path           string `json:"path"`
	SignatureCount int    `json:"signatureCount"`
}

// CoChangePair describes two files that frequently change together.
type CoChangePair struct {
	FileA         string  `json:"fileA"`
	FileB         string  `json:"fileB"`
	Count         int     `json:"count"`
	CouplingScore float64 `json:"couplingScore"`
}

// RankedSkeletonResult contains project files ranked by relevance to a set of focus files,
// pruned to a token budget via personalized PageRank.
type RankedSkeletonResult struct {
	Files []RankedSkeletonFile `json:"files"` // sorted by rank descending
}

// RankedSkeletonFile is one file in a ranked skeleton result.
type RankedSkeletonFile struct {
	Path            string   `json:"path"`
	ModuleID        string   `json:"moduleId"`
	Rank            float64  `json:"rank"`
	SignatureCount  int      `json:"signatureCount"`
	EstimatedTokens int      `json:"estimatedTokens"`
	Role            *string  `json:"role,omitempty"`
	Signatures      []string `json:"signatures"`
}

// UnreferencedSymbolsResult holds the unreferenced export analysis.
type UnreferencedSymbolsResult struct {
	TotalCount int                      `json:"totalCount"`
	Files      []UnreferencedSymbolFile `json:"files"`
}

// UnreferencedSymbolFile lists unreferenced exports for one file.
type UnreferencedSymbolFile struct {
	Path    string   `json:"path"`
	Symbols []string `json:"symbols"`
}

// SemidiffFile describes function-level changes in one file between two commits.
type SemidiffFile struct {
	Path    string   `json:"path"`
	Status  string   `json:"status"` // "added", "modified", "deleted"
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// SearchContentOptions configures a content search request (mirrors Rust SearchOptions).
type SearchContentOptions struct {
	Literal           bool     `json:"literal,omitempty"`
	CaseSensitive     *bool    `json:"caseSensitive,omitempty"` // default true
	ContextLines      int      `json:"contextLines,omitempty"`
	BeforeContext     int      `json:"beforeContext,omitempty"`
	AfterContext      int      `json:"afterContext,omitempty"`
	MaxResults        int      `json:"maxResults,omitempty"`
	FileGlob          string   `json:"fileGlob,omitempty"`
	ExcludeGlob       string   `json:"excludeGlob,omitempty"`
	ExtraPatterns     []string `json:"extraPatterns,omitempty"`
	InvertMatch       bool     `json:"invertMatch,omitempty"`
	WordRegexp        bool     `json:"wordRegexp,omitempty"`
	OnlyMatching      bool     `json:"onlyMatching,omitempty"`
	FilesWithMatches  bool     `json:"filesWithMatches,omitempty"`
	FilesWithoutMatch bool     `json:"filesWithoutMatch,omitempty"`
	CountOnly         bool     `json:"countOnly,omitempty"`
	NoIgnore          bool     `json:"noIgnore,omitempty"`
	SearchPath        string   `json:"searchPath,omitempty"`
}

// FindOptions configures a file-find request (mirrors Rust FindOptions).
type FindOptions struct {
	ModifiedSinceSecs *uint64 `json:"modifiedSinceSecs,omitempty"`
	NewerThan         string  `json:"newerThan,omitempty"`
	MinSizeBytes      *uint64 `json:"minSizeBytes,omitempty"`
	MaxSizeBytes      *uint64 `json:"maxSizeBytes,omitempty"`
	MaxDepth          *int    `json:"maxDepth,omitempty"`
	NoIgnore          bool    `json:"noIgnore,omitempty"`
}

// ContextLine is one line of before/after context around a search match.
type ContextLine struct {
	LineNumber int    `json:"lineNumber"`
	Line       string `json:"line"`
}

// ContentMatch is one matching line with optional surrounding context.
type ContentMatch struct {
	Path          string        `json:"path"`
	LineNumber    int           `json:"lineNumber"`
	Line          string        `json:"line"`
	MatchedTexts  []string      `json:"matchedTexts,omitempty"`
	BeforeContext []ContextLine `json:"beforeContext,omitempty"`
	AfterContext  []ContextLine `json:"afterContext,omitempty"`
}

// FileCount holds the match count for one file (count_only mode).
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// SearchResult is returned by SearchContent.
type SearchResult struct {
	Matches           []ContentMatch `json:"matches"`
	TotalMatches      int            `json:"totalMatches"`
	FilesSearched     int            `json:"filesSearched"`
	Truncated         bool           `json:"truncated"`
	FilesWithMatches  []string       `json:"filesWithMatches,omitempty"`
	FilesWithoutMatch []string       `json:"filesWithoutMatch,omitempty"`
	FileCounts        []FileCount    `json:"fileCounts,omitempty"`
}

// FindFile is one file returned by FindFiles.
type FindFile struct {
	Path      string  `json:"path"`
	Language  *string `json:"language,omitempty"`
	SizeBytes uint64  `json:"sizeBytes"`
	Modified  *string `json:"modified,omitempty"`
}

// FindResult is returned by FindFiles.
type FindResult struct {
	Files        []FindFile `json:"files"`
	TotalMatches int        `json:"totalMatches"`
	Truncated    bool       `json:"truncated"`
}

// ---------------------------------------------------------------------------
// Replace types
// ---------------------------------------------------------------------------

// ReplaceOptions controls replace_content behaviour.
type ReplaceOptions struct {
	Literal       bool   `json:"literal,omitempty"`
	CaseSensitive *bool  `json:"caseSensitive,omitempty"`
	WordRegexp    bool   `json:"wordRegexp,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	Backup        bool   `json:"backup,omitempty"`
	ContextLines  *int   `json:"contextLines,omitempty"`
	FileGlob      string `json:"fileGlob,omitempty"`
	ExcludeGlob   string `json:"excludeGlob,omitempty"`
	SearchPath    string `json:"searchPath,omitempty"`
	NoIgnore      bool   `json:"noIgnore,omitempty"`
	MaxPerFile    int    `json:"maxPerFile,omitempty"`
}

// DiffLine is one line in a contextual diff produced by ReplaceContent.
type DiffLine struct {
	Kind       string `json:"kind"` // "context", "removed", "added", "separator"
	LineNumber int    `json:"lineNumber"`
	Content    string `json:"content"`
}

// FileChange describes the replacements made (or previewed) in one file.
type FileChange struct {
	Path         string     `json:"path"`
	Replacements int        `json:"replacements"`
	Diff         []DiffLine `json:"diff"`
}

// ReplaceResult is returned by ReplaceContent.
type ReplaceResult struct {
	FilesChanged      int          `json:"filesChanged"`
	TotalReplacements int          `json:"totalReplacements"`
	Changes           []FileChange `json:"changes"`
	DryRun            bool         `json:"dryRun"`
}

// ---------------------------------------------------------------------------
// Extract types
// ---------------------------------------------------------------------------

// ExtractOptions controls extract_content behaviour.
type ExtractOptions struct {
	Groups        []int  `json:"groups,omitempty"`
	Separator     string `json:"separator,omitempty"`
	Format        string `json:"format,omitempty"` // "text", "json", "csv", "tsv"
	Count         bool   `json:"count,omitempty"`
	Dedup         bool   `json:"dedup,omitempty"`
	Sort          bool   `json:"sort,omitempty"`
	CaseSensitive *bool  `json:"caseSensitive,omitempty"`
	FileGlob      string `json:"fileGlob,omitempty"`
	ExcludeGlob   string `json:"excludeGlob,omitempty"`
	SearchPath    string `json:"searchPath,omitempty"`
	NoIgnore      bool   `json:"noIgnore,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

// ExtractMatch is one extracted row.
type ExtractMatch struct {
	Path       string   `json:"path"`
	LineNumber int      `json:"lineNumber"`
	Groups     []string `json:"groups"`
}

// CountEntry is a frequency entry returned when ExtractOptions.Count is true.
type CountEntry struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// ExtractResult is returned by ExtractContent.
type ExtractResult struct {
	Matches       []ExtractMatch `json:"matches,omitempty"`
	Counts        []CountEntry   `json:"counts,omitempty"`
	Total         int            `json:"total"`
	FilesSearched int            `json:"filesSearched"`
	Truncated     bool           `json:"truncated"`
}

// ---------------------------------------------------------------------------
// Context health types
// ---------------------------------------------------------------------------

// ContextHealthOpts controls context_health scoring.
// All fields are optional; zero values use sensible defaults.
type ContextHealthOpts struct {
	// Model family for context window default: "claude" (200K), "gpt4" (128K),
	// "llama" (128K), "gpt35" (16K). Defaults to "claude".
	Model string `json:"model,omitempty"`
	// Override context window size in tokens (0 = use model default).
	WindowSize int `json:"windowSize,omitempty"`
	// Number of symbol signatures in the content (improves signal/entity scoring).
	SignatureCount int `json:"signatureCount,omitempty"`
	// Tokens occupied by signature text (improves signal density scoring).
	SignatureTokens int `json:"signatureTokens,omitempty"`
	// Relative positions (0.0–1.0) of key modules in the output order.
	// Used for position health (U-bias) scoring.
	KeyPositions []float64 `json:"keyPositions,omitempty"`
}

// MetricBreakdown holds the individual normalized (0–1) metric scores.
type MetricBreakdown struct {
	SignalDensity       float64 `json:"signal_density"`
	CompressionDensity  float64 `json:"compression_density"`
	PositionHealth      float64 `json:"position_health"`
	EntityDensity       float64 `json:"entity_density"`
	UtilizationHeadroom float64 `json:"utilization_headroom"`
	DedupRatio          float64 `json:"dedup_ratio"`
}

// ContextHealthReport is returned by ContextHealth.
type ContextHealthReport struct {
	TokenCount      int             `json:"token_count"`
	CharCount       int             `json:"char_count"`
	WindowSize      int             `json:"window_size"`
	UtilizationPct  float64         `json:"utilization_pct"`
	Metrics         MetricBreakdown `json:"metrics"`
	Score           float64         `json:"score"` // 0–100
	Grade           string          `json:"grade"` // A / B / C / D / F
	Warnings        []string        `json:"warnings"`
	Recommendations []string        `json:"recommendations"`
}

// CartographerError is returned when a cartographer FFI call fails.
type CartographerError struct {
	Message string
}

func (e *CartographerError) Error() string {
	return "cartographer: " + e.Message
}

// ---------------------------------------------------------------------------
// BM25 search types
// ---------------------------------------------------------------------------

// BM25Options controls a BM25 ranked search request.
type BM25Options struct {
	// BM25 term saturation parameter (default 1.5).
	K1 float64 `json:"k1,omitempty"`
	// BM25 length normalisation parameter (default 0.75).
	B float64 `json:"b,omitempty"`
	// Maximum results to return (0 = default 20).
	MaxResults int `json:"maxResults,omitempty"`
	// Include only files matching this glob (e.g. "*.rs").
	FileGlob string `json:"fileGlob,omitempty"`
	// Restrict search to this subdirectory.
	SearchPath string `json:"searchPath,omitempty"`
	// Search vendor/generated files too.
	NoIgnore bool `json:"noIgnore,omitempty"`
}

// BM25Match is one file ranked by BM25 relevance.
type BM25Match struct {
	Path          string   `json:"path"`
	Score         float64  `json:"score"`
	MatchingTerms []string `json:"matchingTerms"`
	Snippets      []string `json:"snippets"`
}

// BM25Result is returned by BM25Search.
type BM25Result struct {
	Matches []BM25Match `json:"matches"`
	Total   int         `json:"total"`
}

// ---------------------------------------------------------------------------
// Query context (PKG retrieval pipeline) types
// ---------------------------------------------------------------------------

// QueryContextOpts controls the full PKG retrieval pipeline.
type QueryContextOpts struct {
	// Token budget for the skeleton portion (default 8000).
	Budget int `json:"budget,omitempty"`
	// Target model family for health scoring: "claude" (default), "gpt4", "llama", "gpt35".
	Model string `json:"model,omitempty"`
	// Max search hits used as focus seeds (default 20).
	MaxSearchResults int `json:"maxSearchResults,omitempty"`
}

// QueryContextResult is returned by QueryContext.
type QueryContextResult struct {
	// Ready-to-inject context string (ranked skeleton with header).
	Context string `json:"context"`
	// Files included in the skeleton, sorted by PageRank descending.
	FilesUsed []string `json:"filesUsed"`
	// Files used as PageRank personalization seeds (from search hits).
	FocusFiles []string `json:"focusFiles"`
	// Total estimated tokens in Context.
	TotalTokens int `json:"totalTokens"`
	// Context health report for the bundle.
	Health ContextHealthReport `json:"health"`
}

// ---------------------------------------------------------------------------
// Shotgun surgery types
// ---------------------------------------------------------------------------

// ShotgunSurgeryEntry describes a file that exhibits the shotgun surgery smell:
// a change to it historically required simultaneous changes to many other files.
type ShotgunSurgeryEntry struct {
	File            string  `json:"file"`
	PartnerCount    int     `json:"partnerCount"`
	TotalCochanges  int     `json:"totalCochanges"`
	Entropy         float64 `json:"entropy"`
	DispersionScore float64 `json:"dispersionScore"`
}

// ---------------------------------------------------------------------------
// Evolution types
// ---------------------------------------------------------------------------

// EvolutionResult captures architectural health snapshots over git history.
type EvolutionResult struct {
	Snapshots       []EvolutionSnapshot `json:"snapshots"`
	HealthTrend     string              `json:"healthTrend"` // "improving" | "stable" | "degrading"
	DebtIndicators  []string            `json:"debtIndicators"`
	Recommendations []string            `json:"recommendations"`
}

// EvolutionSnapshot is the health state at a point in time.
type EvolutionSnapshot struct {
	Timestamp   interface{} `json:"timestamp"` // string or int64 depending on Rust build
	HealthScore float64     `json:"healthScore"`
}

// ---------------------------------------------------------------------------
// Blast radius types
// ---------------------------------------------------------------------------

// BlastRadiusResult is the graph-theoretic blast radius for a module/file.
type BlastRadiusResult struct {
	ModuleID string               `json:"moduleId"`
	Related  []BlastRadiusRelated `json:"related"`
}

// BlastRadiusRelated is a module that would be affected by a change.
type BlastRadiusRelated struct {
	ModuleID     string `json:"moduleId"`
	Path         string `json:"path"`
	Relationship string `json:"relationship"` // "dependent" | "dependency"
}

// ---------------------------------------------------------------------------
// Render architecture types
// ---------------------------------------------------------------------------

// RenderArchitectureResult is returned by RenderArchitecture.
// Truncated is true when the node cap kicked in — callers should tighten
// focus/depth or raise maxNodes to see more.
type RenderArchitectureResult struct {
	Diagram   string `json:"diagram"`
	Truncated bool   `json:"truncated"`
	Format    string `json:"format"` // "mermaid" | "dot"
	NodeCount int    `json:"nodeCount"`
}

// ---------------------------------------------------------------------------
