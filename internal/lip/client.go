// Package lip provides a best-effort client for the LIP (Liz Indexing Protocol)
// local socket. All operations degrade silently when LIP is not running —
// callers must never treat LIP unavailability as a fatal error.
//
// Wire protocol: length-prefixed JSON over a Unix domain socket.
// The LIP daemon uses Serde's #[serde(tag = "type", rename_all = "snake_case")]
// on ClientMessage, so every request must include a "type" field whose value is
// the snake_case of the Rust enum variant name.
package lip

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// SocketPath returns the path to the LIP Unix domain socket.
// The LIP_SOCKET environment variable overrides the default location.
func SocketPath() string {
	if p := os.Getenv("LIP_SOCKET"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/share/lip/lip.sock"
}

// =============================================================================
// Public result types
// =============================================================================

// NearestResult is a single result from a nearest-neighbour query.
type NearestResult struct {
	URI   string  `json:"uri"`
	Score float32 `json:"score"`
}

// HandshakeInfo is the daemon's response to Handshake.
type HandshakeInfo struct {
	DaemonVersion   string `json:"daemon_version"`
	ProtocolVersion int    `json:"protocol_version"`
	// SupportedMessages is the snake_case `type` tag list the daemon
	// understands. Empty when talking to a pre-v1.5 daemon that omits the
	// field — callers should fall back to ProtocolVersion comparisons.
	SupportedMessages []string `json:"supported_messages"`
}

// IndexStatusInfo is the public view of LIP index health.
type IndexStatusInfo struct {
	IndexedFiles  int      `json:"indexed_files"`
	Pending       int      `json:"pending"`
	LastUpdated   string   `json:"last_updated"`
	MixedModels   bool     `json:"mixed_models"`
	ModelsInIndex []string `json:"models_in_index"`
}

// FileStatusInfo is the public view of per-file LIP index status.
type FileStatusInfo struct {
	Indexed        bool   `json:"indexed"`
	AgeSeconds     int64  `json:"age_seconds"`
	EmbeddingModel string `json:"embedding_model"`
}

// CoverageInfo is the embedding coverage report for a directory tree.
type CoverageInfo struct {
	Root             string        `json:"root"`
	TotalFiles       int           `json:"total_files"`
	EmbeddedFiles    int           `json:"embedded_files"`
	CoverageFraction float32       `json:"coverage_fraction"`
	ByDirectory      []DirCoverage `json:"by_directory"`
}

// DirCoverage is per-directory embedding coverage.
type DirCoverage struct {
	Path          string `json:"path"`
	TotalFiles    int    `json:"total_files"`
	EmbeddedFiles int    `json:"embedded_files"`
}

// BoundaryRange is a semantic boundary within a file detected by FindBoundaries.
type BoundaryRange struct {
	StartLine      uint32  `json:"start_line"`
	EndLine        uint32  `json:"end_line"`
	ShiftMagnitude float32 `json:"shift_magnitude"`
	NearestSymbol  string  `json:"nearest_symbol"`
}

// SemanticDiffInfo is the result of a SemanticDiff call.
type SemanticDiffInfo struct {
	Distance     float32         `json:"distance"`
	MovingToward []NearestResult `json:"moving_toward"`
}

// NoveltyInfo is the result of a NoveltyScore call.
type NoveltyInfo struct {
	Score   float32       `json:"score"`
	PerFile []NoveltyItem `json:"per_file"`
}

// NoveltyItem is the per-file breakdown from NoveltyScore.
type NoveltyItem struct {
	URI   string  `json:"uri"`
	Score float32 `json:"score"`
}

// TermItem is a domain term returned by ExtractTerminology.
type TermItem struct {
	DisplayName string  `json:"display_name"`
	Score       float32 `json:"score"`
}

// ExplanationChunk is a ranked file chunk returned by ExplainMatch.
type ExplanationChunk struct {
	StartLine uint32  `json:"start_line"`
	EndLine   uint32  `json:"end_line"`
	ChunkText string  `json:"chunk_text"`
	Score     float32 `json:"score"`
}

// =============================================================================
// Internal wire response shapes
// =============================================================================

type embeddingBatchResp struct {
	Vectors [][]float32 `json:"vectors"`
}

type nearestResp struct {
	Results []NearestResult `json:"results"`
}

type batchNearestResp struct {
	Results [][]NearestResult `json:"results"`
}

type outliersResp struct {
	Outliers []NearestResult `json:"outliers"`
}

type indexStatusResp struct {
	IndexedFiles  int      `json:"indexed_files"`
	Pending       int      `json:"pending_embedding_files"`
	LastUpdatedMs *int64   `json:"last_updated_ms"`
	MixedModels   bool     `json:"mixed_models"`
	ModelsInIndex []string `json:"models_in_index"`
}

type fileStatusResp struct {
	Indexed        bool   `json:"indexed"`
	AgeSeconds     int64  `json:"age_seconds"`
	EmbeddingModel string `json:"embedding_model"`
}

type annotationGetResp struct {
	Value *string `json:"value"`
}

type batchAnnotationResp struct {
	Entries map[string]*string `json:"entries"`
}

type handshakeResp struct {
	DaemonVersion     string   `json:"daemon_version"`
	ProtocolVersion   int      `json:"protocol_version"`
	SupportedMessages []string `json:"supported_messages"`
}

type similarityResp struct {
	Score *float32 `json:"score"`
}

type queryExpansionResp struct {
	Terms []string `json:"terms"`
}

type clusterResp struct {
	Groups [][]string `json:"groups"`
}

type exportEmbeddingsResp struct {
	Embeddings map[string][]float32 `json:"embeddings"`
}

type semanticDriftResp struct {
	Distance *float32 `json:"distance"`
}

type similarityMatrixResp struct {
	URIs   []string    `json:"uris"`
	Matrix [][]float32 `json:"matrix"`
}

type coverageResp struct {
	Root             string        `json:"root"`
	TotalFiles       int           `json:"total_files"`
	EmbeddedFiles    int           `json:"embedded_files"`
	CoverageFraction *float32      `json:"coverage_fraction"`
	ByDirectory      []DirCoverage `json:"by_directory"`
}

type boundariesResp struct {
	Boundaries []BoundaryRange `json:"boundaries"`
}

type semanticDiffResp struct {
	Distance     float32         `json:"distance"`
	MovingToward []NearestResult `json:"moving_toward"`
}

type noveltyScoreResp struct {
	Score   float32       `json:"score"`
	PerFile []NoveltyItem `json:"per_file"`
}

type terminologyResp struct {
	Terms []TermItem `json:"terms"`
}

type pruneDeletedResp struct {
	Checked int      `json:"checked"`
	Removed []string `json:"removed"`
}

type centroidResp struct {
	Vector   []float32 `json:"vector"`
	Included int       `json:"included"`
}

type staleEmbeddingsResp struct {
	URIs []string `json:"uris"`
}

type explainMatchResp struct {
	Chunks     []ExplanationChunk `json:"chunks"`
	QueryModel string             `json:"query_model"`
}

// =============================================================================
// Embedding
// =============================================================================

// GetEmbedding requests a quantized embedding vector for the given URI.
// Implemented as a single-element EmbeddingBatch call.
func GetEmbedding(lipURI, model string) ([]float32, error) {
	vecs, _ := GetEmbeddingsBatch([]string{lipURI}, model)
	if len(vecs) == 0 {
		return nil, nil
	}
	return vecs[0], nil
}

// GetEmbeddingsBatch requests embeddings for multiple URIs in a single round-trip.
// Returns a slice parallel to uris — entries are nil when LIP has no embedding for
// that URI. Returns nil when LIP is unavailable.
func GetEmbeddingsBatch(uris []string, model string) ([][]float32, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	timeout := max(time.Duration(len(uris)+1)*100*time.Millisecond, 500*time.Millisecond)
	req := map[string]any{"type": "embedding_batch", "uris": uris}
	if model != "" {
		req["model"] = model
	}
	result, _ := lipRPC(
		req, timeout, 64<<20,
		func(r embeddingBatchResp) *embeddingBatchResp { return &r },
	)
	if result == nil {
		return nil, nil
	}
	out := make([][]float32, len(uris))
	for i, v := range result.Vectors {
		if i < len(out) && len(v) > 0 {
			out[i] = v
		}
	}
	return out, nil
}

// GetSymbolEmbedding requests an embedding for a specific symbol.
// For lip:// URIs the daemon routes to the symbol embedding store.
// The symbol, context, and model parameters are carried via the URI and model;
// the context string is not sent separately (daemon uses stored doc/signature).
func GetSymbolEmbedding(uri, _, _, model string) ([]float32, error) {
	return GetEmbedding(uri, model)
}

// =============================================================================
// Nearest-neighbour search
// =============================================================================

// NearestByFile returns the top-k files semantically closest to the given file URI.
func NearestByFile(uri string, topK int) ([]NearestResult, error) {
	return NearestByFileFiltered(uri, topK, "", 0)
}

// NearestByFileFiltered is NearestByFile with an optional glob filter and min score.
// filter restricts candidates (e.g. "*_test.go"); minScore=0 disables the threshold.
func NearestByFileFiltered(uri string, topK int, filter string, minScore float32) ([]NearestResult, error) {
	req := map[string]any{"type": "query_nearest", "uri": uri, "top_k": topK}
	if filter != "" {
		req["filter"] = filter
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	result, _ := lipRPC(req, 500*time.Millisecond, 4<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// NearestByText returns the top-k files whose content is semantically closest to
// the given query. Returns nil when LIP is unavailable.
func NearestByText(text string, topK int) ([]NearestResult, error) {
	return NearestByTextFiltered(text, topK, "", 0, "")
}

// NearestByTextFiltered is NearestByText with optional glob filter, min score, and model.
func NearestByTextFiltered(text string, topK int, filter string, minScore float32, model string) ([]NearestResult, error) {
	req := map[string]any{"type": "query_nearest_by_text", "text": text, "top_k": topK}
	if filter != "" {
		req["filter"] = filter
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	if model != "" {
		req["model"] = model
	}
	result, _ := lipRPC(req, 500*time.Millisecond, 4<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// BatchNearestByText embeds N query strings in one round-trip and returns the
// top-k nearest files for each. Returns nil when LIP is unavailable.
func BatchNearestByText(queries []string, topK int, filter, model string, minScore float32) ([][]NearestResult, error) {
	if len(queries) == 0 {
		return nil, nil
	}
	req := map[string]any{"type": "batch_query_nearest_by_text", "queries": queries, "top_k": topK}
	if filter != "" {
		req["filter"] = filter
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	if model != "" {
		req["model"] = model
	}
	timeout := time.Duration(len(queries)+1) * 300 * time.Millisecond
	result, _ := lipRPC(req, timeout, 32<<20,
		func(r batchNearestResp) *[][]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// NearestBySymbol finds the top-k symbols most similar to the given symbol URI.
func NearestBySymbol(symbolURI string, topK int) ([]NearestResult, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_nearest_by_symbol", "symbol_uri": symbolURI, "top_k": topK},
		500*time.Millisecond, 4<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// NearestByContrast performs contrastive nearest-neighbour: finds files nearest
// to normalize(embed(likeURI) − embed(unlikeURI)).
func NearestByContrast(likeURI, unlikeURI string, topK int) ([]NearestResult, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_nearest_by_contrast", "like_uri": likeURI, "unlike_uri": unlikeURI, "top_k": topK},
		500*time.Millisecond, 4<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// FindSemanticCounterpart finds the top-k candidates most semantically similar
// to the source URI. Useful for finding test files that cover an implementation.
func FindSemanticCounterpart(uri string, candidates []string, topK int) ([]NearestResult, error) {
	result, _ := lipRPC(
		map[string]any{"type": "find_semantic_counterpart", "uri": uri, "candidates": candidates, "top_k": topK},
		500*time.Millisecond, 4<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// NearestInStore performs nearest-neighbour against a caller-provided embedding
// map (keyed by URI). Enables cross-repo federation via ExportEmbeddings.
func NearestInStore(uri string, store map[string][]float32, topK int) ([]NearestResult, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_nearest_in_store", "uri": uri, "store": store, "top_k": topK},
		2*time.Second, 32<<20,
		func(r nearestResp) *[]NearestResult { return &r.Results })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// Outliers returns the top-k semantically outlying files from a group.
// Lower score = more outlier-like.
func Outliers(uris []string, topK int) ([]NearestResult, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "query_outliers", "uris": uris, "top_k": topK},
		500*time.Millisecond, 4<<20,
		func(r outliersResp) *[]NearestResult { return &r.Outliers })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// =============================================================================
// Similarity and distance
// =============================================================================

// Similarity returns the cosine similarity between two stored embeddings.
// ok is false when either URI has no cached embedding or LIP is unavailable.
func Similarity(uriA, uriB string) (float32, bool, error) {
	result, _ := lipRPC(
		map[string]any{"type": "similarity", "uri_a": uriA, "uri_b": uriB},
		500*time.Millisecond, 4<<10,
		func(r similarityResp) *float32 { return r.Score })
	if result == nil {
		return 0, false, nil
	}
	return *result, true, nil
}

// SemanticDrift returns the cosine distance (1 − similarity, range [0, 2]) between
// two stored embeddings. ok is false when either URI has no embedding.
func SemanticDrift(uriA, uriB string) (float32, bool, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_semantic_drift", "uri_a": uriA, "uri_b": uriB},
		500*time.Millisecond, 4<<10,
		func(r semanticDriftResp) *float32 { return r.Distance })
	if result == nil {
		return 0, false, nil
	}
	return *result, true, nil
}

// SimilarityMatrix computes all pairwise cosine similarities for a list of URIs.
// Returns (includedURIs, matrix, err). URIs without cached embeddings are excluded.
func SimilarityMatrix(uris []string) ([]string, [][]float32, error) {
	if len(uris) < 2 {
		return nil, nil, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "similarity_matrix", "uris": uris},
		2*time.Second, 32<<20,
		func(r similarityMatrixResp) *similarityMatrixResp { return &r })
	if result == nil {
		return nil, nil, nil
	}
	return result.URIs, result.Matrix, nil
}

// SemanticDiff measures how much the meaning of two content strings differs.
// distance is cosine distance in [0, 2]; MovingToward names concepts the content
// moved toward. Returns nil when LIP is unavailable.
func SemanticDiff(contentA, contentB string, topK int, model string) (*SemanticDiffInfo, error) {
	req := map[string]any{"type": "semantic_diff", "content_a": contentA, "content_b": contentB}
	if topK > 0 {
		req["top_k"] = topK
	}
	if model != "" {
		req["model"] = model
	}
	result, _ := lipRPC(req, 5*time.Second, 4<<20,
		func(r semanticDiffResp) *SemanticDiffInfo {
			return &SemanticDiffInfo{Distance: r.Distance, MovingToward: r.MovingToward}
		})
	return result, nil
}

// =============================================================================
// Aggregates
// =============================================================================

// GetCentroid computes the embedding centroid (mean) of a file set.
// Returns (vector, included, err); vector is nil when no URI had an embedding.
func GetCentroid(uris []string) ([]float32, int, error) {
	if len(uris) == 0 {
		return nil, 0, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "get_centroid", "uris": uris},
		2*time.Second, 4<<20,
		func(r centroidResp) *centroidResp { return &r })
	if result == nil {
		return nil, 0, nil
	}
	return result.Vector, result.Included, nil
}

// NoveltyScore quantifies how semantically novel a set of files is relative to
// the existing codebase. Per-file breakdown is sorted by descending novelty.
func NoveltyScore(uris []string) (*NoveltyInfo, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	return lipRPC(
		map[string]any{"type": "query_novelty_score", "uris": uris},
		3*time.Second, 1<<20,
		func(r noveltyScoreResp) *NoveltyInfo {
			return &NoveltyInfo{Score: r.Score, PerFile: r.PerFile}
		})
}

// Coverage reports embedding coverage under a filesystem path.
func Coverage(root string) (*CoverageInfo, error) {
	return lipRPC(
		map[string]any{"type": "query_coverage", "root": root},
		500*time.Millisecond, 1<<20,
		func(r coverageResp) *CoverageInfo {
			cov := float32(0)
			if r.CoverageFraction != nil {
				cov = *r.CoverageFraction
			}
			return &CoverageInfo{
				Root: r.Root, TotalFiles: r.TotalFiles,
				EmbeddedFiles: r.EmbeddedFiles, CoverageFraction: cov,
				ByDirectory: r.ByDirectory,
			}
		})
}

// ExportEmbeddings returns the raw stored embedding vectors for the given URIs.
// URIs with no cached vector are omitted. Useful for cross-repo NearestInStore.
func ExportEmbeddings(uris []string) (map[string][]float32, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "export_embeddings", "uris": uris},
		2*time.Second, 64<<20,
		func(r exportEmbeddingsResp) *map[string][]float32 { return &r.Embeddings })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// Cluster groups uris by embedding proximity within a cosine-similarity radius.
// URIs without a cached embedding are silently excluded.
func Cluster(uris []string, radius float32) ([][]string, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "cluster", "uris": uris, "radius": radius},
		5*time.Second, 4<<20,
		func(r clusterResp) *[][]string { return &r.Groups })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// =============================================================================
// Explainability
// =============================================================================

// FindBoundaries detects semantic boundaries within a file by measuring cosine
// distance between adjacent chunk windows. chunkLines=0 uses the daemon default
// (30 lines); threshold=0 uses the default (0.3).
func FindBoundaries(uri string, chunkLines int, threshold float32, model string) ([]BoundaryRange, error) {
	req := map[string]any{"type": "find_boundaries", "uri": uri}
	if chunkLines > 0 {
		req["chunk_lines"] = chunkLines
	}
	if threshold > 0 {
		req["threshold"] = threshold
	}
	if model != "" {
		req["model"] = model
	}
	result, _ := lipRPC(req, 5*time.Second, 1<<20,
		func(r boundariesResp) *[]BoundaryRange { return &r.Boundaries })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// ExplainMatch explains why resultURI ranked as a strong semantic match for query.
// Chunks the file, embeds each window, and returns the top-scoring regions.
// topK=0 and chunkLines=0 use daemon defaults (5 and 30 respectively).
func ExplainMatch(query, resultURI string, topK, chunkLines int, model string) ([]ExplanationChunk, string, error) {
	req := map[string]any{"type": "explain_match", "query": query, "result_uri": resultURI}
	if topK > 0 {
		req["top_k"] = topK
	}
	if chunkLines > 0 {
		req["chunk_lines"] = chunkLines
	}
	if model != "" {
		req["model"] = model
	}
	result, _ := lipRPC(req, 5*time.Second, 4<<20,
		func(r explainMatchResp) *explainMatchResp { return &r })
	if result == nil {
		return nil, "", nil
	}
	return result.Chunks, result.QueryModel, nil
}

// ExtractTerminology extracts the domain vocabulary most semantically central to
// a set of files. Requires symbol embeddings (call GetEmbeddingsBatch with lip://
// URIs first).
func ExtractTerminology(uris []string, topK int) ([]TermItem, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "extract_terminology", "uris": uris, "top_k": topK},
		3*time.Second, 1<<20,
		func(r terminologyResp) *[]TermItem { return &r.Terms })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// QueryExpansion embeds query, finds top-k nearest symbols, and returns their
// display names as expansion terms. Requires LIP_EMBEDDING_URL to be configured.
func QueryExpansion(query string, topK int) ([]string, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_expansion", "query": query, "top_k": topK},
		5*time.Second, 1<<20,
		func(r queryExpansionResp) *[]string { return &r.Terms })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// =============================================================================
// Index management
// =============================================================================

// IndexStatus returns overall LIP index health — file count, pending, last update,
// and model provenance (MixedModels warns when vectors were produced by different
// models, making cosine comparisons unreliable).
func IndexStatus() (*IndexStatusInfo, error) {
	return lipRPC(
		map[string]any{"type": "query_index_status"},
		200*time.Millisecond, 4<<10,
		func(r indexStatusResp) *IndexStatusInfo {
			lastUpdated := ""
			if r.LastUpdatedMs != nil {
				lastUpdated = time.UnixMilli(*r.LastUpdatedMs).UTC().Format(time.RFC3339)
			}
			return &IndexStatusInfo{
				IndexedFiles:  r.IndexedFiles,
				Pending:       r.Pending,
				LastUpdated:   lastUpdated,
				MixedModels:   r.MixedModels,
				ModelsInIndex: r.ModelsInIndex,
			}
		})
}

// FileStatus returns LIP index status for a single file URI, including the model
// that produced its embedding (empty when not yet embedded).
func FileStatus(uri string) (*FileStatusInfo, error) {
	return lipRPC(
		map[string]any{"type": "query_file_status", "uri": uri},
		200*time.Millisecond, 4<<10,
		func(r fileStatusResp) *FileStatusInfo {
			return &FileStatusInfo{
				Indexed:        r.Indexed,
				AgeSeconds:     r.AgeSeconds,
				EmbeddingModel: r.EmbeddingModel,
			}
		})
}

// StaleEmbeddings reports files under root whose stored embedding is older than
// their current filesystem mtime. Files with no index timestamp are conservatively
// included.
func StaleEmbeddings(root string) ([]string, error) {
	result, _ := lipRPC(
		map[string]any{"type": "query_stale_embeddings", "root": root},
		500*time.Millisecond, 1<<20,
		func(r staleEmbeddingsResp) *[]string { return &r.URIs })
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// ReindexFiles forces a re-index of specific file URIs from disk. Fire-and-forget:
// any LIP error is silently discarded.
func ReindexFiles(uris []string) {
	if len(uris) == 0 {
		return
	}
	timeout := time.Duration(len(uris)+1) * 200 * time.Millisecond
	_, _ = lipRPC(
		map[string]any{"type": "reindex_files", "uris": uris},
		timeout, 4<<10,
		func(r map[string]any) *struct{} { return &struct{}{} })
}

// PruneDeleted removes index entries for files that no longer exist on disk.
// Fires IndexChanged after removal. Returns (checked, removed, err).
func PruneDeleted() (int, int, error) {
	result, _ := lipRPC(
		map[string]any{"type": "prune_deleted"},
		10*time.Second, 4<<10,
		func(r pruneDeletedResp) *pruneDeletedResp { return &r })
	if result == nil {
		return 0, 0, nil
	}
	return result.Checked, len(result.Removed), nil
}

// =============================================================================
// Blast radius
// =============================================================================

// BlastRadiusItem is a static caller from LIP's blast radius response.
type BlastRadiusItem struct {
	FileURI    string  `json:"file_uri"`
	SymbolURI  string  `json:"symbol_uri"`
	Distance   int     `json:"distance"`
	Confidence float64 `json:"confidence"`
}

// BlastRadiusSemanticItem is a semantically coupled symbol from LIP.
type BlastRadiusSemanticItem struct {
	FileURI    string  `json:"file_uri"`
	SymbolURI  string  `json:"symbol_uri"`
	Similarity float32 `json:"similarity"`
	Source     string  `json:"source"` // "semantic" or "both"
}

// BlastRadiusEntry is a single symbol's blast radius from LIP.
type BlastRadiusEntry struct {
	SymbolURI            string                    `json:"symbol_uri"`
	FileURI              string                    `json:"file_uri"` // input file this entry belongs to
	DirectDependents     int                       `json:"direct_dependents"`
	TransitiveDependents int                       `json:"transitive_dependents"`
	AffectedFiles        []string                  `json:"affected_files"`
	DirectItems          []BlastRadiusItem         `json:"direct_items"`
	TransitiveItems      []BlastRadiusItem         `json:"transitive_items"`
	RiskLevel            string                    `json:"risk_level"`
	Truncated            bool                      `json:"truncated"`
	SemanticItems        []BlastRadiusSemanticItem `json:"semantic_items"`
	// EdgesSource is LIP v2.3.1+ provenance for the static call edges:
	// "tier1", "scip_with_tier1_edges", "scip_only", or "empty". Omitted
	// by older daemons — clients treat missing as "fold-eligible".
	EdgesSource string `json:"edges_source,omitempty"`
}

// BlastRadiusBatchResult is the full response from QueryBlastRadiusBatch.
// NotIndexedURIs lists input URIs that were absent from the LIP index —
// callers can distinguish "not indexed" from "indexed but zero callers".
type BlastRadiusBatchResult struct {
	Entries        map[string]BlastRadiusEntry // keyed by symbol_uri
	NotIndexedURIs []string                    // input file URIs not in index (omitted when empty)
}

type blastRadiusBatchResp struct {
	Results        []BlastRadiusEntry `json:"results"`
	NotIndexedURIs []string           `json:"not_indexed_uris,omitempty"`
}

// QueryBlastRadiusBatch asks LIP for blast radius of all symbols in the given
// changed files. One round-trip. Returns a map keyed by symbol_uri.
// Returns nil when LIP is unavailable.
//
// min_score is the cosine similarity threshold for semantic hits. Pass 0 to
// get static-only results (no semantic items). Typical values: 0.6–0.8.
func QueryBlastRadiusBatch(changedFileURIs []string, minScore float32) (*BlastRadiusBatchResult, error) {
	if len(changedFileURIs) == 0 {
		return nil, nil
	}
	req := map[string]any{
		"type":              "query_blast_radius_batch",
		"changed_file_uris": changedFileURIs,
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	// Budget: generous timeout — LIP needs to resolve symbols + compute embeddings
	timeout := max(time.Duration(len(changedFileURIs)+1)*200*time.Millisecond, 3*time.Second)
	raw, _ := lipRPC(req, timeout, 8<<20,
		func(r blastRadiusBatchResp) *blastRadiusBatchResp { return &r })
	if raw == nil {
		return nil, nil
	}
	// Index by symbol_uri for O(1) lookup in the merge path
	entries := make(map[string]BlastRadiusEntry, len(raw.Results))
	for _, entry := range raw.Results {
		entries[entry.SymbolURI] = entry
	}
	return &BlastRadiusBatchResult{
		Entries:        entries,
		NotIndexedURIs: raw.NotIndexedURIs,
	}, nil
}

type blastRadiusSymbolResp struct {
	Result *BlastRadiusEntry `json:"result,omitempty"`
}

// QueryBlastRadiusSymbol asks LIP for blast radius of a single symbol (v2.3+).
// Returns (nil, nil) when the symbol's file isn't indexed or LIP is
// unavailable — callers should treat both identically (fall back to the
// static SCIP blast radius unchanged).
//
// Prefer this over QueryBlastRadiusBatch when you already have a symbol URI:
// it skips the file-level fetch-and-filter workaround and lets LIP dispatch
// directly.
func QueryBlastRadiusSymbol(symbolURI string, minScore float32) (*BlastRadiusEntry, error) {
	if symbolURI == "" {
		return nil, nil
	}
	req := map[string]any{
		"type":       "query_blast_radius_symbol",
		"symbol_uri": symbolURI,
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	raw, _ := lipRPC(req, 2*time.Second, 2<<20,
		func(r blastRadiusSymbolResp) *blastRadiusSymbolResp { return &r })
	if raw == nil {
		return nil, nil
	}
	return raw.Result, nil
}

// =============================================================================
// Outgoing impact (v2.3.3)
// =============================================================================

// OutgoingImpactEntry is the result of a QueryOutgoingImpact call. Shape
// mirrors BlastRadiusEntry but traces the forward call graph — direct_items
// are callees at distance=1, transitive_items at distance>=2.
//
// target_uri echoes the request's symbol_uri (post-canonicalisation).
// edges_source and the semantic items reuse the same provenance and coupling
// vocabulary as blast radius, so callers can treat both results through the
// shared ExternalBlastRadius pipeline via OutgoingEntryToExternal.
type OutgoingImpactEntry struct {
	TargetURI       string                    `json:"target_uri"`
	DirectItems     []BlastRadiusItem         `json:"direct_items"`
	TransitiveItems []BlastRadiusItem         `json:"transitive_items"`
	SemanticItems   []BlastRadiusSemanticItem `json:"semantic_items"`
	// EdgesSource mirrors BlastRadiusEntry.EdgesSource: "tier1",
	// "scip_with_tier1_edges", "scip_only", or "empty". Omitted by
	// daemons that don't yet report provenance — treat as fold-eligible.
	EdgesSource string `json:"edges_source,omitempty"`
	Truncated   bool   `json:"truncated"`
}

type outgoingImpactResp struct {
	Result *OutgoingImpactEntry `json:"result,omitempty"`
}

// QueryOutgoingImpact asks LIP for the forward call graph of a symbol
// (v2.3.3+). Returns (nil, nil) when the symbol isn't indexed, LIP is
// unavailable, or the daemon doesn't support the RPC — callers should
// degrade to SCIP-only outgoing traversal.
//
// Depth is capped at 8 server-side (matching query_outgoing_calls). Semantic
// items are seeded from the target's own embedding, same as blast radius.
//
// Gate on Handshake.SupportedMessages containing "query_outgoing_impact"
// before calling — older daemons reject with UnknownMessage.
func QueryOutgoingImpact(symbolURI string, minScore float32) (*OutgoingImpactEntry, error) {
	if symbolURI == "" {
		return nil, nil
	}
	req := map[string]any{
		"type":       "query_outgoing_impact",
		"symbol_uri": symbolURI,
	}
	if minScore > 0 {
		req["min_score"] = minScore
	}
	raw, _ := lipRPC(req, 2*time.Second, 2<<20,
		func(r outgoingImpactResp) *outgoingImpactResp { return &r })
	if raw == nil {
		return nil, nil
	}
	return raw.Result, nil
}

// =============================================================================
// Annotations
// =============================================================================

// GetAnnotation queries the LIP daemon for an annotation on the given URI and key.
// Returns (value, true, nil) when found, ("", false, nil) when absent or LIP is
// unavailable.
func GetAnnotation(lipURI, key string) (string, bool, error) {
	result, _ := lipRPC(
		map[string]any{"type": "annotation_get", "symbol_uri": lipURI, "key": key},
		200*time.Millisecond, 1<<20,
		func(r annotationGetResp) *string { return r.Value })
	if result == nil {
		return "", false, nil
	}
	return *result, true, nil
}

// BatchAnnotationGet retrieves an annotation key for multiple symbol URIs under
// a single db lock. Returns a map of uri→value for entries that are present.
func BatchAnnotationGet(uris []string, key string) (map[string]string, error) {
	if len(uris) == 0 {
		return nil, nil
	}
	timeout := time.Duration(len(uris)+1) * 100 * time.Millisecond
	result, _ := lipRPC(
		map[string]any{"type": "batch_annotation_get", "uris": uris, "key": key},
		timeout, 4<<20,
		func(r batchAnnotationResp) *map[string]string {
			out := make(map[string]string, len(r.Entries))
			for k, v := range r.Entries {
				if v != nil {
					out[k] = *v
				}
			}
			return &out
		})
	if result == nil {
		return nil, nil
	}
	return *result, nil
}

// =============================================================================
// Protocol
// =============================================================================

type deltaAckResp struct {
	Accepted bool    `json:"accepted"`
	Error    *string `json:"error"`
}

// RegisterProjectRoot tells the LIP daemon the canonical filesystem root for
// this workspace (shipped in LIP v2.3.1). Once registered, LIP canonicalises
// inbound lip://local/<rel> URIs via longest-prefix match against the root
// set, so callers can send either relative or absolute forms on subsequent
// queries.
//
// Best-effort and idempotent: the daemon tracks roots as an unordered set.
// Safe to call on every connect. Returns (false, nil) when LIP is unavailable
// or rejects the root; callers treat both as "proceed without registration"
// since CKB's query paths already tolerate unregistered roots via the server's
// auto-detect heuristics.
//
// Gate on Handshake.SupportedMessages containing "register_project_root"
// before calling — older daemons will reject with UnknownMessage.
func RegisterProjectRoot(root string) (bool, error) {
	if root == "" {
		return false, nil
	}
	result, _ := lipRPC(
		map[string]any{"type": "register_project_root", "root": root},
		500*time.Millisecond, 4<<10,
		func(r deltaAckResp) *deltaAckResp { return &r })
	if result == nil {
		return false, nil
	}
	return result.Accepted, nil
}

// Handshake performs the version handshake. Clients can call this on connect to
// detect protocol drift before sending real queries.
func Handshake(clientVersion string) (*HandshakeInfo, error) {
	req := map[string]any{"type": "handshake"}
	if clientVersion != "" {
		req["client_version"] = clientVersion
	}
	return lipRPC(req, 200*time.Millisecond, 4<<10,
		func(r handshakeResp) *HandshakeInfo {
			return &HandshakeInfo{
				DaemonVersion:     r.DaemonVersion,
				ProtocolVersion:   r.ProtocolVersion,
				SupportedMessages: r.SupportedMessages,
			}
		})
}

// =============================================================================
// Transport
// =============================================================================

// maxFrameBytes bounds a single length-prefixed frame in either direction.
const maxFrameBytes = 64 << 20

// encodeFrame prefixes payload with its big-endian uint32 length.
func encodeFrame(payload []byte) ([]byte, error) {
	n := len(payload)
	if n > maxFrameBytes {
		return nil, fmt.Errorf("lip: frame of %d bytes exceeds %d", n, maxFrameBytes)
	}
	frame := make([]byte, 4, 4+n)
	binary.BigEndian.PutUint32(frame, uint32(n)) // #nosec G115 -- 0 <= n <= maxFrameBytes, checked above
	return append(frame, payload...), nil
}

// lipRPC is the shared transport for request→response LIP calls.
// T is the JSON response type; U is the public return type.
// Returns (nil, nil) on any error — callers treat nil as "LIP unavailable".
func lipRPC[T any, U any](req any, timeout time.Duration, maxRespBytes uint32, convert func(T) *U) (*U, error) {
	conn, err := net.DialTimeout("unix", SocketPath(), 100*time.Millisecond)
	if err != nil {
		return nil, nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	payload, _ := json.Marshal(req)
	frame, err := encodeFrame(payload)
	if err != nil {
		return nil, nil
	}
	if _, err := conn.Write(frame); err != nil {
		return nil, nil
	}

	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, nil
	}
	respLen := binary.BigEndian.Uint32(lenBuf)
	if respLen > maxRespBytes {
		return nil, nil
	}
	respBuf := make([]byte, respLen)
	if _, err := io.ReadFull(conn, respBuf); err != nil {
		return nil, nil
	}

	var r T
	if err := json.Unmarshal(respBuf, &r); err != nil {
		return nil, nil
	}
	return convert(r), nil
}
