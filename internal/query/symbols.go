package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/complexity"
	"github.com/SimplyLiz/CodeMCP/internal/compression"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/output"
)

// GetSymbolOptions contains options for getSymbol.
type GetSymbolOptions struct {
	SymbolId      string
	RepoStateMode string // "head" or "full"
}

// GetSymbolResponse is the response for getSymbol.
type GetSymbolResponse struct {
	Symbol         *SymbolInfo        `json:"symbol,omitempty"`
	Redirected     bool               `json:"redirected,omitempty"`
	RedirectedFrom string             `json:"redirectedFrom,omitempty"`
	RedirectReason string             `json:"redirectReason,omitempty"`
	Deleted        bool               `json:"deleted,omitempty"`
	DeletedAt      string             `json:"deletedAt,omitempty"`
	Provenance     *Provenance        `json:"provenance"`
	Drilldowns     []output.Drilldown `json:"drilldowns,omitempty"`
}

// SymbolInfo contains symbol metadata.
type SymbolInfo struct {
	StableId            string          `json:"stableId"`
	Name                string          `json:"name"`
	Kind                string          `json:"kind"`
	Signature           string          `json:"signature,omitempty"`
	SignatureNormalized string          `json:"signatureNormalized,omitempty"`
	Visibility          *VisibilityInfo `json:"visibility"`
	ModuleId            string          `json:"moduleId"`
	ModuleName          string          `json:"moduleName,omitempty"`
	ContainerName       string          `json:"containerName,omitempty"`
	Location            *LocationInfo   `json:"location"`
	LocationFreshness   string          `json:"locationFreshness"`
	Documentation       string          `json:"documentation,omitempty"`
	// Lightweight counts (populated by BatchGet when includeCounts is true)
	ReferenceCount int `json:"referenceCount,omitempty"`
	CallerCount    int `json:"callerCount,omitempty"`
	CalleeCount    int `json:"calleeCount,omitempty"`
}

// VisibilityInfo describes symbol visibility.
type VisibilityInfo struct {
	Visibility string  `json:"visibility"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// LocationInfo describes a source location.
type LocationInfo struct {
	FileId      string `json:"fileId"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine,omitempty"`
	EndColumn   int    `json:"endColumn,omitempty"`
}

// TruncationInfo describes why results were truncated.
type TruncationInfo struct {
	Reason        string `json:"reason"`
	OriginalCount int    `json:"originalCount"`
	ReturnedCount int    `json:"returnedCount"`
}

// GetSymbol retrieves symbol information by ID.
func (e *Engine) GetSymbol(ctx context.Context, opts GetSymbolOptions) (*GetSymbolResponse, error) {
	startTime := time.Now()

	// Default to head mode
	if opts.RepoStateMode == "" {
		opts.RepoStateMode = "head"
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, opts.RepoStateMode)
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Resolve symbol ID through aliases
	resolved, err := e.resolver.ResolveSymbolId(opts.SymbolId)
	if err != nil {
		// If identity resolution fails and this looks like a raw SCIP ID,
		// try querying SCIP directly as a fallback
		if strings.HasPrefix(opts.SymbolId, "scip-") && e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
			result, scipErr := e.scipAdapter.GetSymbol(ctx, opts.SymbolId)
			if scipErr == nil && result != nil {
				// Successfully found in SCIP - build response directly
				completeness := CompletenessInfo{
					Score:  result.Completeness.Score,
					Reason: string(result.Completeness.Reason),
				}
				backendContribs := []BackendContribution{{
					BackendId:    "scip",
					Available:    true,
					Used:         true,
					ResultCount:  1,
					Completeness: result.Completeness.Score,
				}}
				return &GetSymbolResponse{
					Symbol: &SymbolInfo{
						StableId:            result.StableID,
						Name:                result.Name,
						Kind:                result.Kind,
						Signature:           result.SignatureFull,
						SignatureNormalized: result.SignatureNormalized,
						ContainerName:       result.ContainerName,
						ModuleId:            result.ModuleID,
						Documentation:       result.Documentation,
						LocationFreshness:   e.getLocationFreshness(repoState),
						Visibility: &VisibilityInfo{
							Visibility: result.Visibility,
							Confidence: result.VisibilityConfidence,
							Source:     "scip",
						},
						Location: &LocationInfo{
							FileId:      result.Location.Path,
							StartLine:   result.Location.Line,
							StartColumn: result.Location.Column,
							EndLine:     result.Location.EndLine,
							EndColumn:   result.Location.EndColumn,
						},
					},
					Provenance: e.buildProvenance(repoState, opts.RepoStateMode, startTime, backendContribs, completeness),
					Drilldowns: []output.Drilldown{
						{Label: "Find references", Query: fmt.Sprintf("findReferences %s", opts.SymbolId)},
						{Label: "Get call graph", Query: fmt.Sprintf("getCallGraph %s", opts.SymbolId)},
					},
				}, nil
			}
		}

		// Check if it's a known error type
		if ckbErr, ok := err.(*errors.CkbError); ok {
			completeness := CompletenessInfo{Score: 0.0, Reason: "symbol-not-found"}
			return &GetSymbolResponse{
				Provenance: e.buildProvenance(repoState, opts.RepoStateMode, startTime, nil, completeness),
				Drilldowns: []output.Drilldown{
					{Label: "Search for similar symbols", Query: fmt.Sprintf("searchSymbols %s", opts.SymbolId)},
				},
			}, ckbErr
		}
		return nil, e.wrapError(err, errors.SymbolNotFound)
	}

	// Handle deleted symbols
	if resolved.Deleted {
		completeness := CompletenessInfo{Score: 1.0, Reason: "symbol-deleted"}
		return &GetSymbolResponse{
			Deleted:    true,
			DeletedAt:  resolved.DeletedAt,
			Provenance: e.buildProvenance(repoState, opts.RepoStateMode, startTime, nil, completeness),
		}, nil
	}

	// Build response from resolved symbol
	response := &GetSymbolResponse{
		Redirected:     resolved.Redirected,
		RedirectedFrom: resolved.RedirectedFrom,
		RedirectReason: string(resolved.RedirectReason),
	}

	var backendContribs []BackendContribution
	var completeness CompletenessInfo

	if resolved.Symbol != nil {
		// Query SCIP backend for symbol data
		if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
			result, err := e.scipAdapter.GetSymbol(ctx, resolved.Symbol.StableId)
			if err == nil && result != nil {
				// Build symbol info from backend result
				response.Symbol = &SymbolInfo{
					StableId:            result.StableID,
					Name:                result.Name,
					Kind:                result.Kind,
					Signature:           result.SignatureFull,
					SignatureNormalized: result.SignatureNormalized,
					ContainerName:       result.ContainerName,
					ModuleId:            result.ModuleID,
					Documentation:       result.Documentation,
					LocationFreshness:   e.getLocationFreshness(repoState),
					Visibility: &VisibilityInfo{
						Visibility: result.Visibility,
						Confidence: result.VisibilityConfidence,
						Source:     "scip",
					},
					Location: &LocationInfo{
						FileId:      result.Location.Path,
						StartLine:   result.Location.Line,
						StartColumn: result.Location.Column,
						EndLine:     result.Location.EndLine,
						EndColumn:   result.Location.EndColumn,
					},
				}
				backendContribs = append(backendContribs, BackendContribution{
					BackendId:    "scip",
					Available:    true,
					Used:         true,
					ResultCount:  1,
					Completeness: result.Completeness.Score,
				})
				completeness = CompletenessInfo{
					Score:  result.Completeness.Score,
					Reason: string(result.Completeness.Reason),
				}
			}
		}

		// Fallback to identity data if no backend result
		if response.Symbol == nil && resolved.Symbol.Fingerprint != nil {
			response.Symbol = &SymbolInfo{
				StableId:          resolved.Symbol.StableId,
				Name:              resolved.Symbol.Fingerprint.Name,
				Kind:              string(resolved.Symbol.Fingerprint.Kind),
				ContainerName:     resolved.Symbol.Fingerprint.QualifiedContainer,
				LocationFreshness: e.getLocationFreshness(repoState),
				Visibility: &VisibilityInfo{
					Visibility: "unknown",
					Confidence: 0.3,
					Source:     "default",
				},
			}
			if resolved.Symbol.Location != nil {
				response.Symbol.Location = &LocationInfo{
					FileId:      resolved.Symbol.Location.Path,
					StartLine:   resolved.Symbol.Location.Line,
					StartColumn: resolved.Symbol.Location.Column,
				}
			}
			completeness = CompletenessInfo{
				Score:  0.5,
				Reason: "identity-only",
			}
		}
	}

	response.Provenance = e.buildProvenance(repoState, opts.RepoStateMode, startTime, backendContribs, completeness)
	response.Drilldowns = e.generateDrilldowns(nil, completeness, opts.SymbolId, nil)

	return response, nil
}

// getLocationFreshness determines location freshness based on repo state.
func (e *Engine) getLocationFreshness(repoState *RepoState) string {
	if repoState.Dirty {
		return "may-be-stale"
	}
	return "fresh"
}

// SearchSymbolsOptions contains options for searchSymbols.
type SearchSymbolsOptions struct {
	Query           string
	Scope           string
	Kinds           []string
	Limit           int
	MinLines        int      // Filter: minimum body line count (post-enrichment)
	MinComplexity   int      // Filter: minimum cyclomatic complexity (post-enrichment)
	ExcludePatterns []string // Filter: exclude symbols whose name contains any pattern (e.g., "#", ".()")
}

// SearchSymbolsResponse is the response for searchSymbols.
type SearchSymbolsResponse struct {
	Symbols        []SearchResultItem `json:"symbols"`
	TotalCount     int                `json:"totalCount"`
	Truncated      bool               `json:"truncated"`
	TruncationInfo *TruncationInfo    `json:"truncationInfo,omitempty"`
	Provenance     *Provenance        `json:"provenance"`
	Drilldowns     []output.Drilldown `json:"drilldowns,omitempty"`
}

// RankingV52 contains v5.2 ranking signals for auditable, deterministic ordering.
type RankingV52 struct {
	Score         float64                `json:"score"`
	Signals       map[string]interface{} `json:"signals"`
	PolicyVersion string                 `json:"policyVersion"`
}

// NewRankingV52 creates a new v5.2 ranking with the given score and signals.
func NewRankingV52(score float64, signals map[string]interface{}) *RankingV52 {
	return &RankingV52{
		Score:         score,
		Signals:       signals,
		PolicyVersion: "5.2",
	}
}

// SearchResultItem represents a symbol search result.
type SearchResultItem struct {
	StableId   string          `json:"stableId"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	ModuleId   string          `json:"moduleId"`
	ModuleName string          `json:"moduleName,omitempty"`
	Location   *LocationInfo   `json:"location,omitempty"`
	Visibility *VisibilityInfo `json:"visibility,omitempty"`
	Score      float64         `json:"score"`
	Ranking    *RankingV52     `json:"ranking,omitempty"`
	// Body metrics (enriched via tree-sitter when available)
	Lines      int `json:"lines,omitempty"`      // body line count
	Cyclomatic int `json:"cyclomatic,omitempty"` // cyclomatic complexity
	Cognitive  int `json:"cognitive,omitempty"`  // cognitive complexity
	// Evidence attached by the LIP semantic path — ranked chunks from the
	// file that best explain why it matched the query. Only populated when
	// the result came from SemanticSearchWithLIP with ExplainMatch enabled.
	SemanticEvidence []SemanticEvidenceChunk `json:"semanticEvidence,omitempty"`
}

// SemanticEvidenceChunk is a single ranked snippet returned by LIP's
// `explain_match` RPC, pinning a semantic hit to the specific lines that
// drove the score. Makes semantic results auditable instead of opaque.
type SemanticEvidenceChunk struct {
	StartLine uint32  `json:"startLine"`
	EndLine   uint32  `json:"endLine"`
	Text      string  `json:"text,omitempty"`
	Score     float32 `json:"score"`
}

// generateCacheKey creates a deterministic cache key for search options.
func generateSearchCacheKey(opts SearchSymbolsOptions) string {
	// Build a deterministic key from the options
	keyParts := []string{
		"search",
		opts.Query,
		opts.Scope,
		fmt.Sprintf("%d", opts.Limit),
	}
	if len(opts.Kinds) > 0 {
		sort.Strings(opts.Kinds)
		keyParts = append(keyParts, strings.Join(opts.Kinds, ","))
	}
	keyStr := strings.Join(keyParts, "|")

	// Hash for shorter key
	hash := sha256.Sum256([]byte(keyStr))
	return "search:" + hex.EncodeToString(hash[:16])
}

// SearchSymbols searches for symbols by name.
func (e *Engine) SearchSymbols(ctx context.Context, opts SearchSymbolsOptions) (*SearchSymbolsResponse, error) {
	startTime := time.Now()

	// Default limit
	if opts.Limit <= 0 {
		opts.Limit = 20
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Check cache first
	cacheKey := generateSearchCacheKey(opts)
	if e.cache != nil && repoState.HeadCommit != "" {
		cachedJSON, found, err := e.cache.GetQueryCache(cacheKey, repoState.HeadCommit)
		if err == nil && found {
			var cached SearchSymbolsResponse
			if err := json.Unmarshal([]byte(cachedJSON), &cached); err == nil {
				// Update stats
				e.cacheStatsMu.Lock()
				e.cacheHits++
				e.cacheStatsMu.Unlock()

				// Update duration to reflect cache hit
				cached.Provenance.QueryDurationMs = time.Since(startTime).Milliseconds()
				cached.Provenance.CachedAt = cached.Provenance.RepoStateId
				return &cached, nil
			}
		}
		// Track cache miss
		e.cacheStatsMu.Lock()
		e.cacheMisses++
		e.cacheStatsMu.Unlock()
	}

	var results []SearchResultItem
	var backendContribs []BackendContribution
	var completeness CompletenessInfo
	lipRanked := false // true when results already came from LIP semantic search

	// Try FTS5 first for fast symbol search.
	// Request more results when filters are set, since most will be excluded.
	ftsMultiplier := 2
	if len(opts.ExcludePatterns) > 0 || opts.MinLines > 0 || opts.MinComplexity > 0 {
		ftsMultiplier = 10
	}
	// Expand short queries via LIP before hitting FTS — "auth" broadens to
	// "auth authenticate authorization principal..." which lifts recall on
	// vocabulary-mismatch misses. Falls back to the raw query when LIP is
	// unavailable or returns nothing.
	ftsQuery := e.expandQueryViaLIP(opts.Query)
	ftsResults, ftsErr := e.SearchSymbolsFTS(ctx, ftsQuery, opts.Limit*ftsMultiplier)
	if ftsErr == nil && len(ftsResults) > 0 {
		for _, r := range ftsResults {
			// Skip symbols with no name — they can match via documentation/signature
			// in FTS5 but are meaningless as search results.
			if r.Name == "" {
				continue
			}
			// Filter by kinds if specified
			if len(opts.Kinds) > 0 {
				matchKind := false
				for _, k := range opts.Kinds {
					if strings.EqualFold(r.Kind, k) {
						matchKind = true
						break
					}
				}
				if !matchKind {
					continue
				}
			}
			// Filter by scope if specified
			if opts.Scope != "" && !strings.HasPrefix(r.FilePath, opts.Scope) {
				continue
			}

			results = append(results, SearchResultItem{
				StableId: r.ID,
				Name:     r.Name,
				Kind:     r.Kind,
				ModuleId: filepath.Dir(r.FilePath),
				Location: &LocationInfo{
					FileId: r.FilePath,
				},
				Visibility: &VisibilityInfo{
					Visibility: inferVisibility(r.Name, r.Kind),
					Confidence: 0.7,
					Source:     "fts",
				},
				Score: r.Rank,
			})
		}
		if len(results) > 0 {
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "fts5",
				Available:    true,
				Used:         true,
				ResultCount:  len(results),
				Completeness: 0.95,
			})
			completeness = CompletenessInfo{
				Score:  0.95,
				Reason: "fts5-search",
			}
		}
	}

	// Fall back to SCIP if FTS returned no results
	if len(results) == 0 && e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		searchOpts := backends.SearchOptions{
			MaxResults:   opts.Limit * 2, // Request more to allow for ranking
			IncludeTests: true,
			Scope:        parseScope(opts.Scope),
			Kind:         opts.Kinds,
		}
		searchResult, err := e.scipAdapter.SearchSymbols(ctx, opts.Query, searchOpts)
		if err == nil && searchResult != nil {
			for _, sym := range searchResult.Symbols {
				results = append(results, SearchResultItem{
					StableId: sym.StableID,
					Name:     sym.Name,
					Kind:     sym.Kind,
					ModuleId: sym.ModuleID,
					Location: &LocationInfo{
						FileId:      sym.Location.Path,
						StartLine:   sym.Location.Line,
						StartColumn: sym.Location.Column,
						EndLine:     sym.Location.EndLine,
						EndColumn:   sym.Location.EndColumn,
					},
					Visibility: &VisibilityInfo{
						Visibility: sym.Visibility,
						Confidence: sym.VisibilityConfidence,
						Source:     "scip",
					},
				})
			}
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "scip",
				Available:    true,
				Used:         true,
				ResultCount:  len(searchResult.Symbols),
				Completeness: searchResult.Completeness.Score,
			})
			completeness = CompletenessInfo{
				Score:  searchResult.Completeness.Score,
				Reason: string(searchResult.Completeness.Reason),
			}
		}
	} else if len(results) == 0 && e.treesitterExtractor != nil {
		// Tree-sitter fallback when SCIP not available
		tsResults, err := e.searchWithTreesitter(ctx, opts)
		if err == nil && len(tsResults) > 0 {
			results = tsResults
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "treesitter",
				Available:    true,
				Used:         true,
				ResultCount:  len(tsResults),
				Completeness: 0.7,
			})
			completeness = CompletenessInfo{
				Score:   0.7,
				Reason:  "treesitter-fallback",
				Details: "Using tree-sitter analysis. Run 'ckb index' for cross-file references.",
			}
		}
	}

	// LIP semantic supplement/fallback: when symbol-level backends returned nothing
	// (or fewer results than the minimum useful threshold), ask LIP for files whose
	// content matches the query semantically, then resolve their symbols from the FTS
	// content table. The threshold of 3 mirrors the PPR/LIP re-ranking gate — below
	// that the lexical results aren't trustworthy enough to stand alone.
	const lipFallbackThreshold = 3
	if len(results) < lipFallbackThreshold && e.lipSemanticAvailable() {
		lipSymLimit := opts.Limit * 3
		// Attach explain_match evidence when the daemon supports it
		// (v2.0+). Gives the caller specific matching chunks per hit
		// instead of a bare file URL.
		search := SemanticSearchWithLIP
		if e.lipSupports("explain_match") {
			search = SemanticSearchWithLIPExplained
		}
		lipResults := search(opts.Query, 20, "", 0, func(fileURIs []string) map[string][]SearchResultItem {
			// Convert file:// URIs back to repo-relative paths for the batch query.
			relPaths := make([]string, len(fileURIs))
			for i, uri := range fileURIs {
				rel := strings.TrimPrefix(uri, "file://"+e.repoRoot)
				relPaths[i] = strings.TrimPrefix(rel, "/")
			}
			// One query for all files instead of N individual queries.
			byPath := e.symbolsForFiles(ctx, relPaths, lipSymLimit)
			out := make(map[string][]SearchResultItem, len(fileURIs))
			for i, uri := range fileURIs {
				syms := byPath[relPaths[i]]
				items := make([]SearchResultItem, 0, len(syms))
				for _, s := range syms {
					items = append(items, SearchResultItem{
						StableId: s.ID,
						Name:     s.Name,
						Kind:     s.Kind,
						ModuleId: filepath.Dir(s.FilePath),
						Location: &LocationInfo{FileId: s.FilePath},
						Visibility: &VisibilityInfo{
							Visibility: inferVisibility(s.Name, s.Kind),
							Confidence: 0.6,
							Source:     "lip",
						},
					})
				}
				out[uri] = items
			}
			return out
		})
		if len(lipResults) > 0 {
			// Deduplicate against the existing (sparse) lexical results before merging.
			existing := make(map[string]struct{}, len(results))
			for _, r := range results {
				if r.StableId != "" {
					existing[r.StableId] = struct{}{}
				}
			}
			for _, r := range lipResults {
				if _, dup := existing[r.StableId]; !dup {
					results = append(results, r)
				}
			}
			lipRanked = true // LIP pass already ordered these; skip RerankWithLIP
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "lip-semantic",
				Available:    true,
				Used:         true,
				ResultCount:  len(lipResults),
				Completeness: 0.6,
			})
			if completeness.Score == 0 {
				completeness = CompletenessInfo{
					Score:   0.6,
					Reason:  "lip-semantic-fallback",
					Details: "No lexical matches found. Results from LIP semantic file search.",
				}
			}
		}
	}

	// If no results, return empty response
	if len(results) == 0 {
		completeness = CompletenessInfo{Score: 0.0, Reason: "no-results"}
		return &SearchSymbolsResponse{
			Symbols:    []SearchResultItem{},
			TotalCount: 0,
			Truncated:  false,
			Provenance: e.buildProvenance(repoState, "head", startTime, backendContribs, completeness),
		}, nil
	}

	// Enrich results with body ranges and complexity from tree-sitter.
	e.enrichWithBodyRanges(ctx, results)

	// Apply post-enrichment filters (minLines, minComplexity, excludePatterns)
	if opts.MinLines > 0 || opts.MinComplexity > 0 || len(opts.ExcludePatterns) > 0 {
		filtered := results[:0]
		for _, r := range results {
			if opts.MinLines > 0 && r.Lines > 0 && r.Lines < opts.MinLines {
				continue
			}
			if opts.MinComplexity > 0 && r.Cyclomatic < opts.MinComplexity {
				continue
			}
			excluded := false
			for _, p := range opts.ExcludePatterns {
				if strings.Contains(r.Name, p) {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
			filtered = append(filtered, r)
		}
		results = filtered
	}

	// Apply ranking
	rankSearchResults(results, opts.Query)

	// Sort by score descending; break ties by StableId to get a total order
	// across runs (sort.Slice is not stable, and equal-score results would
	// otherwise appear in non-deterministic order between test runs).
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].StableId < results[j].StableId
	})

	// Apply re-ranking. SCIP available → PPR over symbol graph (Standard tier).
	// LSP-only → LIP embedding similarity as ranking signal (Fast tier).
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() && len(results) > 3 {
		// Build graph and re-rank (lazy graph building)
		if symbolGraph, err := BuildGraphFromSCIP(ctx, e.scipAdapter, e.logger); err == nil && symbolGraph.NumNodes() > 0 {
			if reranked, err := RerankWithPPR(ctx, symbolGraph, results, opts.Limit); err == nil {
				results = reranked
			}
		}
	} else if len(results) > 3 && !lipRanked && e.lipSemanticAvailable() {
		// Fast tier: use LIP file embeddings as a semantic re-ranking signal.
		// Skip when results already came from LIP semantic search (lipRanked=true) —
		// they're already ordered by similarity, a second pass would be redundant.
		if reranked, err := RerankWithLIP(ctx, results, e.repoRoot, opts.Query); err == nil {
			results = reranked
		}
	}

	// Apply limit and track truncation
	totalCount := len(results)
	var truncationInfo *TruncationInfo
	if len(results) > opts.Limit {
		truncationInfo = &TruncationInfo{
			Reason:        "max-results",
			OriginalCount: totalCount,
			ReturnedCount: opts.Limit,
		}
		results = results[:opts.Limit]
	}

	// Build provenance
	provenance := e.buildProvenance(repoState, "head", startTime, backendContribs, completeness)

	// Generate drilldowns
	var compTrunc *compression.TruncationInfo
	if truncationInfo != nil {
		compTrunc = &compression.TruncationInfo{
			Reason:        compression.TruncMaxSymbols,
			OriginalCount: truncationInfo.OriginalCount,
			ReturnedCount: truncationInfo.ReturnedCount,
		}
	}
	drilldowns := e.generateDrilldowns(compTrunc, completeness, "", nil)

	response := &SearchSymbolsResponse{
		Symbols:        results,
		TotalCount:     totalCount,
		Truncated:      truncationInfo != nil,
		TruncationInfo: truncationInfo,
		Provenance:     provenance,
		Drilldowns:     drilldowns,
	}

	// Store in cache
	if e.cache != nil && repoState.HeadCommit != "" && len(results) > 0 {
		if responseJSON, err := json.Marshal(response); err == nil {
			ttl := 300 // 5 minutes default
			if e.config != nil && e.config.Cache.QueryTtlSeconds > 0 {
				ttl = e.config.Cache.QueryTtlSeconds
			}
			_ = e.cache.SetQueryCache(cacheKey, string(responseJSON), repoState.HeadCommit, repoState.RepoStateId, ttl)
		}
	}

	return response, nil
}

// parseScope converts scope string to slice.
func parseScope(scope string) []string {
	if scope == "" {
		return nil
	}
	return []string{scope}
}

// isTypeLikeKind reports whether kind names a type-defining symbol (as
// opposed to a member like a property/field/parameter). Used to break ties
// between symbols whose names only match case-insensitively — e.g. a class
// `Model` vs. a field `model` — in favor of the type.
//
// This covers every type-defining SymbolKind this codebase emits
// (internal/backends/scip/types.go): class, interface, struct, type, and
// enum (SCIP kind 3 / inferKindString case 3 — e.g. Go/TS/Java/Rust enums,
// case-fold-matched by a query like "STATUS" against `enum Status`).
// There is no separate trait/protocol/typealias SymbolKind in this
// codebase — Rust traits and meta descriptors both resolve to KindType,
// and Swift-style protocols aren't a distinct kind here — so nothing
// further needs listing.
func isTypeLikeKind(kind string) bool {
	switch kind {
	case "class", "interface", "struct", "type", "enum":
		return true
	default:
		return false
	}
}

// rankSearchResults applies ranking to search results with v5.2 signals.
//
// Case handling: a query like "Model" must outrank a case-insensitive-only
// match like a field named "model" — matching Go's FTS candidate selection
// (searchExact in internal/storage/fts.go), which already sorts a
// case-sensitive exact match ahead of a case-fold-only one before any LIMIT
// is applied, so the right candidate survives into this pool in the first
// place. Here we replicate that preference in the score so it holds even
// for the SCIP/tree-sitter fallback paths that don't go through FTS at all.
func rankSearchResults(results []SearchResultItem, query string) {
	queryLower := strings.ToLower(query)

	for i := range results {
		score := 0.0
		var matchType string

		// Determine match type and apply score. A case-sensitive exact match
		// ("Model" == "Model") outranks a case-insensitive-only one
		// ("Model" vs. "model") — the latter is still an "exact" name match,
		// just not the one the user typed.
		nameLower := strings.ToLower(results[i].Name)
		switch {
		case results[i].Name == query:
			matchType = "exact"
			score += 100
		case strings.EqualFold(results[i].Name, query):
			matchType = "exact-fold"
			score += 80
			// Among case-fold-only matches, prefer the type over a member
			// (field/property/parameter) with the same name: a user typing
			// "Model" almost always means the type, not a `model` field.
			if isTypeLikeKind(results[i].Kind) {
				score += 15
			}
		case strings.HasPrefix(nameLower, queryLower):
			matchType = "partial"
			score += 50
		case strings.Contains(nameLower, queryLower):
			matchType = "partial"
			score += 25
		default:
			matchType = "fuzzy"
			score += 10
		}

		// Visibility weight
		if results[i].Visibility != nil {
			switch results[i].Visibility.Visibility {
			case "public":
				score += 30
			case "internal":
				score += 20
			case "private":
				score += 10
			default:
				score += 5
			}
		}

		// Kind weight
		switch {
		case isTypeLikeKind(results[i].Kind):
			score += 25
		case results[i].Kind == "function":
			score += 20
		case results[i].Kind == "method":
			score += 15
		case results[i].Kind == "property":
			score += 10
		default:
			score += 5
		}

		results[i].Score = score

		// Build v5.2 ranking signals
		scope := ""
		if results[i].Location != nil && results[i].Location.FileId != "" {
			scope = results[i].Location.FileId
		}
		if results[i].ModuleId != "" {
			scope = results[i].ModuleId
		}

		results[i].Ranking = NewRankingV52(score, map[string]interface{}{
			"matchType": matchType,
			"kind":      results[i].Kind,
			"scope":     scope,
		})
	}
}

// FindReferencesOptions contains options for findReferences.
type FindReferencesOptions struct {
	SymbolId     string
	Scope        string
	IncludeTests bool
	Limit        int
}

// FindReferencesResponse is the response for findReferences.
type FindReferencesResponse struct {
	References     []ReferenceInfo    `json:"references"`
	TotalCount     int                `json:"totalCount"`
	Truncated      bool               `json:"truncated"`
	TruncationInfo *TruncationInfo    `json:"truncationInfo,omitempty"`
	Provenance     *Provenance        `json:"provenance"`
	Drilldowns     []output.Drilldown `json:"drilldowns,omitempty"`
}

// ReferenceInfo describes a reference to a symbol.
type ReferenceInfo struct {
	Location *LocationInfo `json:"location"`
	Kind     string        `json:"kind"`
	Context  string        `json:"context,omitempty"`
	IsTest   bool          `json:"isTest,omitempty"`
}

// FindReferences finds all references to a symbol.
func (e *Engine) FindReferences(ctx context.Context, opts FindReferencesOptions) (*FindReferencesResponse, error) {
	startTime := time.Now()

	// Default options
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	// Get repo state (full mode for references)
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Resolve symbol ID - first try the identity resolver
	var symbolIdToQuery string
	resolved, err := e.resolver.ResolveSymbolId(opts.SymbolId)
	if err == nil && resolved.Symbol != nil {
		symbolIdToQuery = resolved.Symbol.StableId
	} else {
		// Fall back to using the raw symbol ID directly (for SCIP symbols not in SQLite)
		symbolIdToQuery = opts.SymbolId
	}

	var refs []ReferenceInfo
	var backendContribs []BackendContribution
	var completeness CompletenessInfo

	// Query SCIP for references
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		refOpts := backends.RefOptions{
			MaxResults:         opts.Limit * 2,
			IncludeTests:       opts.IncludeTests,
			IncludeDeclaration: true,
			Scope:              parseScope(opts.Scope),
		}
		refsResult, err := e.scipAdapter.FindReferences(ctx, symbolIdToQuery, refOpts)
		if err == nil && refsResult != nil {
			for _, ref := range refsResult.References {
				refs = append(refs, ReferenceInfo{
					Location: &LocationInfo{
						FileId:      ref.Location.Path,
						StartLine:   ref.Location.Line,
						StartColumn: ref.Location.Column,
						EndLine:     ref.Location.EndLine,
						EndColumn:   ref.Location.EndColumn,
					},
					Kind:    ref.Kind,
					Context: ref.Context,
				})
			}
			backendContribs = append(backendContribs, BackendContribution{
				BackendId:    "scip",
				Available:    true,
				Used:         true,
				ResultCount:  len(refsResult.References),
				Completeness: refsResult.Completeness.Score,
			})
			completeness = CompletenessInfo{
				Score:  refsResult.Completeness.Score,
				Reason: string(refsResult.Completeness.Reason),
			}
		}
	}

	// If no results and symbol wasn't found in identity system, return not found
	if len(refs) == 0 && (resolved == nil || resolved.Symbol == nil) {
		return nil, errors.NewCkbError(
			errors.SymbolNotFound,
			fmt.Sprintf("Symbol not found: %s", opts.SymbolId),
			nil, nil, nil,
		)
	}

	// Deduplicate
	refs = deduplicateReferences(refs)

	// Sort deterministically
	sortReferences(refs)

	// Apply limit and track truncation
	totalCount := len(refs)
	var truncationInfo *TruncationInfo
	if len(refs) > opts.Limit {
		truncationInfo = &TruncationInfo{
			Reason:        "max-refs",
			OriginalCount: totalCount,
			ReturnedCount: opts.Limit,
		}
		refs = refs[:opts.Limit]
	}

	// Build provenance
	provenance := e.buildProvenance(repoState, "full", startTime, backendContribs, completeness)

	// Generate drilldowns
	var compTrunc *compression.TruncationInfo
	if truncationInfo != nil {
		compTrunc = &compression.TruncationInfo{
			Reason:        compression.TruncMaxRefs,
			OriginalCount: truncationInfo.OriginalCount,
			ReturnedCount: truncationInfo.ReturnedCount,
		}
	}
	drilldowns := e.generateDrilldowns(compTrunc, completeness, opts.SymbolId, nil)

	return &FindReferencesResponse{
		References:     refs,
		TotalCount:     totalCount,
		Truncated:      truncationInfo != nil,
		TruncationInfo: truncationInfo,
		Provenance:     provenance,
		Drilldowns:     drilldowns,
	}, nil
}

// deduplicateReferences removes duplicate references.
func deduplicateReferences(refs []ReferenceInfo) []ReferenceInfo {
	seen := make(map[string]bool)
	result := make([]ReferenceInfo, 0, len(refs))

	for _, ref := range refs {
		if ref.Location == nil {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", ref.Location.FileId, ref.Location.StartLine, ref.Location.StartColumn)
		if !seen[key] {
			seen[key] = true
			result = append(result, ref)
		}
	}

	return result
}

// sortReferences sorts references deterministically.
func sortReferences(refs []ReferenceInfo) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Location.FileId != refs[j].Location.FileId {
			return refs[i].Location.FileId < refs[j].Location.FileId
		}
		if refs[i].Location.StartLine != refs[j].Location.StartLine {
			return refs[i].Location.StartLine < refs[j].Location.StartLine
		}
		return refs[i].Location.StartColumn < refs[j].Location.StartColumn
	})
}

// enrichWithBodyRanges upgrades search results with full body ranges and
// per-symbol complexity from tree-sitter. SCIP stores the range of the symbol
// name token (EndLine == StartLine), and FTS stores no line info at all
// (StartLine == 0). Tree-sitter gives us real scope ranges, and the complexity
// analyzer gives us cyclomatic/cognitive metrics per function.
func (e *Engine) enrichWithBodyRanges(ctx context.Context, results []SearchResultItem) {
	if e.treesitterExtractor == nil {
		return
	}

	// Collect files that need enrichment (no endLine or no complexity)
	needsEnrich := make(map[string][]int) // fileId → indices into results
	for i, r := range results {
		if r.Location == nil || r.Location.FileId == "" {
			continue
		}
		if r.Location.EndLine <= r.Location.StartLine || r.Cyclomatic == 0 {
			needsEnrich[r.Location.FileId] = append(needsEnrich[r.Location.FileId], i)
		}
	}
	if len(needsEnrich) == 0 {
		return
	}

	// Extract symbols and complexity per file
	for fileId, indices := range needsEnrich {
		absPath := filepath.Join(e.repoRoot, fileId)

		// Get body ranges from symbol extractor
		e.tsMu.Lock()
		syms, err := e.treesitterExtractor.ExtractFile(ctx, absPath)
		e.tsMu.Unlock()

		// Get complexity from complexity analyzer
		var cxFuncs []complexity.ComplexityResult
		if e.complexityAnalyzer != nil {
			if fc, cxErr := e.complexityAnalyzer.GetFileComplexityFull(ctx, absPath); cxErr == nil && fc != nil {
				cxFuncs = fc.Functions
			}
		}

		if (err != nil || len(syms) == 0) && len(cxFuncs) == 0 {
			continue
		}

		// Build lookups for body ranges
		type lineKey struct {
			name string
			line int
		}
		type symbolMetrics struct {
			startLine  int
			endLine    int
			lines      int
			cyclomatic int
			cognitive  int
		}
		byNameLine := make(map[lineKey]*symbolMetrics)
		byName := make(map[string]*symbolMetrics)

		// Populate from symbol extractor (body ranges)
		for _, sym := range syms {
			if sym.EndLine > sym.Line {
				m := &symbolMetrics{
					startLine: sym.Line,
					endLine:   sym.EndLine,
					lines:     sym.EndLine - sym.Line + 1,
				}
				byNameLine[lineKey{sym.Name, sym.Line}] = m
				if _, exists := byName[sym.Name]; !exists {
					byName[sym.Name] = m
				}
			}
		}

		// Merge complexity data
		for _, fn := range cxFuncs {
			key := lineKey{fn.Name, fn.StartLine}
			if m, ok := byNameLine[key]; ok {
				m.cyclomatic = fn.Cyclomatic
				m.cognitive = fn.Cognitive
			} else if m, ok := byName[fn.Name]; ok && m.startLine == fn.StartLine {
				m.cyclomatic = fn.Cyclomatic
				m.cognitive = fn.Cognitive
			}
		}

		// Apply to results
		for _, idx := range indices {
			r := &results[idx]
			matchName := r.Name
			if hashIdx := strings.LastIndex(matchName, "#"); hashIdx >= 0 {
				matchName = matchName[hashIdx+1:]
			}

			var m *symbolMetrics
			if r.Location.StartLine > 0 {
				m = byNameLine[lineKey{matchName, r.Location.StartLine}]
			}
			if m == nil {
				m = byName[matchName]
			}
			if m == nil {
				continue
			}

			if r.Location.EndLine <= r.Location.StartLine {
				if r.Location.StartLine == 0 {
					r.Location.StartLine = m.startLine
				}
				r.Location.EndLine = m.endLine
			}
			if m.lines > 0 {
				r.Lines = m.lines
			}
			if m.cyclomatic > 0 {
				r.Cyclomatic = m.cyclomatic
				r.Cognitive = m.cognitive
			}
		}
	}
}

// searchWithTreesitter performs symbol search using tree-sitter as fallback.
// Acquires tsMu because tree-sitter cgo is not thread-safe.
func (e *Engine) searchWithTreesitter(ctx context.Context, opts SearchSymbolsOptions) ([]SearchResultItem, error) {
	if e.treesitterExtractor == nil {
		return nil, nil
	}

	// Determine search scope
	searchRoot := e.repoRoot
	if opts.Scope != "" {
		searchRoot = filepath.Join(e.repoRoot, opts.Scope)
	}

	// Extract all symbols from the directory — tree-sitter cgo requires mutex
	e.tsMu.Lock()
	allSymbols, err := e.treesitterExtractor.ExtractDirectory(ctx, searchRoot, nil)
	e.tsMu.Unlock()
	if err != nil {
		e.logger.Warn("Tree-sitter extraction failed",
			"error", err.Error(),
			"root", searchRoot,
		)
		return nil, err
	}

	// Filter by query string and kinds
	queryLower := strings.ToLower(opts.Query)
	var results []SearchResultItem

	for _, sym := range allSymbols {
		// Name matching
		nameLower := strings.ToLower(sym.Name)
		if !strings.Contains(nameLower, queryLower) {
			continue
		}

		// Kind filtering
		if len(opts.Kinds) > 0 {
			matchesKind := false
			for _, k := range opts.Kinds {
				if strings.EqualFold(sym.Kind, k) {
					matchesKind = true
					break
				}
			}
			if !matchesKind {
				continue
			}
		}

		// Convert to relative path for moduleId
		relPath, _ := filepath.Rel(e.repoRoot, sym.Path)
		if relPath == "" {
			relPath = sym.Path
		}

		// Generate a stable ID based on path and position
		stableId := generateTreesitterSymbolId(relPath, sym.Name, sym.Kind, sym.Line)

		results = append(results, SearchResultItem{
			StableId: stableId,
			Name:     sym.Name,
			Kind:     sym.Kind,
			ModuleId: filepath.Dir(relPath),
			Location: &LocationInfo{
				FileId:    relPath,
				StartLine: sym.Line,
				EndLine:   sym.EndLine,
			},
			Visibility: &VisibilityInfo{
				Visibility: inferVisibility(sym.Name, sym.Kind),
				Confidence: 0.5, // Lower confidence for inferred visibility
				Source:     "treesitter",
			},
		})
	}

	return results, nil
}

// generateTreesitterSymbolId creates a stable ID for tree-sitter extracted symbols.
func generateTreesitterSymbolId(path, name, kind string, line int) string {
	key := fmt.Sprintf("%s:%s:%s:%d", path, name, kind, line)
	hash := sha256.Sum256([]byte(key))
	return "ts-" + hex.EncodeToString(hash[:8])
}

// inferVisibility guesses visibility from naming conventions.
func inferVisibility(name, kind string) string {
	if name == "" {
		return "unknown"
	}

	// Go convention: lowercase = package-private, uppercase = exported
	firstChar := rune(name[0])
	if firstChar >= 'A' && firstChar <= 'Z' {
		return "public"
	}

	// Python/JS convention: underscore prefix = private
	if strings.HasPrefix(name, "_") {
		if strings.HasPrefix(name, "__") {
			return "private"
		}
		return "internal"
	}

	return "internal"
}
