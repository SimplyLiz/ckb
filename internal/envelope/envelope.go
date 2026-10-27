// Package envelope provides a standardized response wrapper for all MCP tool responses.
// Every tool response is wrapped in a consistent envelope that includes metadata about
// confidence, provenance, freshness, truncation, warnings, and suggested next calls.
package envelope

import "github.com/SimplyLiz/CodeMCP/internal/query"

// ConfidenceTier represents the quality tier of results.
type ConfidenceTier string

const (
	// TierHigh indicates SCIP-backed results with a fresh index.
	TierHigh ConfidenceTier = "high"
	// TierMedium indicates LSP results or a stale SCIP index.
	TierMedium ConfidenceTier = "medium"
	// TierLow indicates heuristic or git-only results.
	TierLow ConfidenceTier = "low"
	// TierSpeculative indicates cross-repo or uncommitted change results.
	TierSpeculative ConfidenceTier = "speculative"
)

// ConfidenceFactor explains one component of the confidence score.
// v8.0: Added for transparency in confidence scoring.
type ConfidenceFactor struct {
	Factor string  `json:"factor"` // e.g., "scip_backend", "repo_state"
	Status string  `json:"status"` // e.g., "available", "unavailable", "dirty"
	Impact float64 `json:"impact"` // contribution to score (-1.0 to 1.0)
}

// Confidence describes result quality.
type Confidence struct {
	Score   float64            `json:"score"`             // 0.0 - 1.0
	Tier    ConfidenceTier     `json:"tier"`              // high, medium, low, speculative
	Reasons []string           `json:"reasons,omitempty"` // why this tier
	Factors []ConfidenceFactor `json:"factors,omitempty"` // v8.0: breakdown of score
}

// Provenance describes which backends contributed to the result.
type Provenance struct {
	Backends    []string `json:"backends"`              // e.g., ["scip", "git"]
	RepoStateID string   `json:"repoStateId,omitempty"` // commit hash or state ID
}

// IndexAge describes SCIP index freshness.
type IndexAge struct {
	CommitsBehind int    `json:"commitsBehind,omitempty"` // commits behind HEAD
	StaleReason   string `json:"staleReason,omitempty"`   // "uncommitted-changes", "behind-head"
}

// Freshness describes data currency.
type Freshness struct {
	IndexAge *IndexAge `json:"indexAge,omitempty"`
}

// Truncation describes result trimming.
type Truncation struct {
	IsTruncated bool   `json:"isTruncated"`
	Shown       int    `json:"shown,omitempty"`  // items returned
	Total       int    `json:"total,omitempty"`  // total available
	Reason      string `json:"reason,omitempty"` // "max-symbols", "max-modules", etc.
}

// CacheInfo describes cache status for this response.
// v8.0: Added for cache transparency.
type CacheInfo struct {
	Hit   bool   `json:"hit"`             // true if served from cache
	Age   string `json:"age,omitempty"`   // if hit, how old (e.g., "2m30s")
	Key   string `json:"key,omitempty"`   // cache key for debugging
	Stale bool   `json:"stale,omitempty"` // served stale while refreshing
}

// Meta holds response metadata.
type Meta struct {
	Confidence *Confidence `json:"confidence,omitempty"`
	Provenance *Provenance `json:"provenance,omitempty"`
	Freshness  *Freshness  `json:"freshness,omitempty"`
	Truncation *Truncation `json:"truncation,omitempty"`
	Cache      *CacheInfo  `json:"cache,omitempty"`    // v8.0: cache status
	Backend    string      `json:"backend,omitempty"`  // "scip" | "lsp" | "tree-sitter" | "git"
	Accuracy   string      `json:"accuracy,omitempty"` // "high" | "medium" | "low"

	// IndexFreshness is set on SCIP-backed responses: how the loaded index
	// relates to the working tree and whether that touches this result.
	IndexFreshness *query.IndexFreshness `json:"indexFreshness,omitempty"`
}

// AccuracyForBackend returns the accuracy tier string for a given backend name.
func AccuracyForBackend(backend string) string {
	switch backend {
	case "scip":
		return "high"
	case "lsp":
		return "medium"
	case "tree-sitter":
		return "low"
	default:
		return "low"
	}
}

// SuggestedCall represents a recommended follow-up tool call.
type SuggestedCall struct {
	Tool   string                 `json:"tool"`             // tool name
	Params map[string]interface{} `json:"params,omitempty"` // pre-filled parameters
	Reason string                 `json:"reason,omitempty"` // why this is suggested
}

// Warning represents a non-fatal issue.
type Warning struct {
	Code    string `json:"code,omitempty"` // machine-readable code
	Message string `json:"message"`        // human-readable message
}

// Response is the standard envelope for all MCP tool responses.
type Response struct {
	SchemaVersion      string          `json:"schemaVersion"`
	Data               interface{}     `json:"data"`
	Meta               *Meta           `json:"meta,omitempty"`
	Warnings           []Warning       `json:"warnings,omitempty"`
	Error              *string         `json:"error,omitempty"`
	SuggestedNextCalls []SuggestedCall `json:"suggestedNextCalls,omitempty"`
}

// CurrentSchemaVersion is the current envelope schema version.
const CurrentSchemaVersion = "1.0"
