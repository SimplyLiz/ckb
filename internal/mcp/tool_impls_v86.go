package mcp

import (
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
)

// v8.6 Cartographer context tool implementations.

// toolQueryContext runs Cartographer's PKG retrieval pipeline:
// BM25 content search → personalized PageRank skeleton → context health.
// Returns a ready-to-inject context bundle graded A–F.
func (s *MCPServer) toolQueryContext(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("query context", cartographer.ErrUnavailable)
	}

	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, errors.NewInvalidParameterError("query", "required")
	}

	opts := &cartographer.QueryContextOpts{}
	if v, ok := params["budget"].(float64); ok && v > 0 {
		opts.Budget = int(v)
	}
	if v, ok := params["model"].(string); ok && v != "" {
		opts.Model = v
	}
	if v, ok := params["maxSearchResults"].(float64); ok && v > 0 {
		opts.MaxSearchResults = int(v)
	}

	repoRoot := s.engine().GetRepoRoot()
	result, err := cartographer.QueryContext(repoRoot, query, opts)
	if err != nil {
		return nil, errors.NewOperationError("query context", err)
	}

	return NewToolResponse().Data(result).Build(), nil
}

// toolContextHealth scores a context bundle on 6 research-backed metrics,
// returning a composite 0–100 score graded A–F with per-metric breakdown.
func (s *MCPServer) toolContextHealth(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("context health", cartographer.ErrUnavailable)
	}

	content, ok := params["content"].(string)
	if !ok || content == "" {
		return nil, errors.NewInvalidParameterError("content", "required")
	}

	opts := &cartographer.ContextHealthOpts{}
	if v, ok := params["model"].(string); ok && v != "" {
		opts.Model = v
	}
	if v, ok := params["signatureCount"].(float64); ok && v > 0 {
		opts.SignatureCount = int(v)
	}

	result, err := cartographer.ContextHealth(content, opts)
	if err != nil {
		return nil, errors.NewOperationError("context health", err)
	}

	return NewToolResponse().Data(result).Build(), nil
}

// toolDetectShotgunSurgery returns files ranked by co-change dispersion score.
func (s *MCPServer) toolDetectShotgunSurgery(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("detect shotgun surgery", cartographer.ErrUnavailable)
	}

	var limit, minPartners uint32
	if v, ok := params["limit"].(float64); ok && v > 0 {
		limit = uint32(v)
	}
	if v, ok := params["min_partners"].(float64); ok && v > 0 {
		minPartners = uint32(v)
	}

	repoRoot := s.engine().GetRepoRoot()
	entries, err := cartographer.ShotgunSurgery(repoRoot, limit, minPartners)
	if err == nil && !cartographer.ShotgunDecoded(entries) {
		err = cartographer.ErrUndecodable
	}
	if err != nil {
		return nil, errors.NewOperationError("detect shotgun surgery", err)
	}

	return NewToolResponse().Data(entries).Build(), nil
}

// toolGetArchitecturalEvolution returns health snapshots over git history.
func (s *MCPServer) toolGetArchitecturalEvolution(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("get architectural evolution", cartographer.ErrUnavailable)
	}

	var days uint32
	if v, ok := params["days"].(float64); ok && v > 0 {
		days = uint32(v)
	}

	repoRoot := s.engine().GetRepoRoot()
	result, err := cartographer.Evolution(repoRoot, days)
	if err == nil && !cartographer.EvolutionDecoded(result) {
		err = cartographer.ErrUndecodable
	}
	if err != nil {
		return nil, errors.NewOperationError("get architectural evolution", err)
	}

	return NewToolResponse().Data(result).Build(), nil
}

// toolRenderArchitecture renders the project's import graph as a Mermaid or
// Graphviz (DOT) diagram, suitable for inline rendering in IDEs that support
// Mermaid (Cursor, Claude Desktop, VS Code markdown preview, GitHub).
//
// With `focus` set, returns a BFS neighborhood of the given module. Without,
// returns the top-N most-connected nodes. `truncated=true` in the response
// signals that the node cap kicked in.
func (s *MCPServer) toolRenderArchitecture(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("render architecture", cartographer.ErrUnavailable)
	}

	format, _ := params["format"].(string)
	if format == "" {
		format = "mermaid"
	}
	if format != "mermaid" && format != "dot" {
		return nil, errors.NewInvalidParameterError("format", "must be \"mermaid\" or \"dot\"")
	}

	focus, _ := params["focus"].(string)

	var depth, maxNodes uint32
	if v, ok := params["depth"].(float64); ok && v > 0 {
		depth = uint32(v)
	}
	if v, ok := params["max_nodes"].(float64); ok && v > 0 {
		maxNodes = uint32(v)
	}

	repoRoot := s.engine().GetRepoRoot()
	result, err := cartographer.RenderArchitecture(repoRoot, format, focus, depth, maxNodes)
	if err != nil {
		return nil, errors.NewOperationError("render architecture", err)
	}

	return NewToolResponse().Data(result).Build(), nil
}

// toolGetBlastRadius returns the graph-theoretic blast radius for a module/file.
func (s *MCPServer) toolGetBlastRadius(params map[string]interface{}) (*envelope.Response, error) {
	if !cartographer.Available() {
		return nil, errors.NewOperationError("get blast radius", cartographer.ErrUnavailable)
	}

	target, ok := params["target"].(string)
	if !ok || target == "" {
		return nil, errors.NewInvalidParameterError("target", "required")
	}

	var maxRelated uint32
	if v, ok := params["max_related"].(float64); ok && v > 0 {
		maxRelated = uint32(v)
	}

	repoRoot := s.engine().GetRepoRoot()
	result, err := cartographer.BlastRadius(repoRoot, target, maxRelated)
	if err != nil {
		return nil, errors.NewOperationError("get blast radius", err)
	}

	return NewToolResponse().Data(result).Build(), nil
}
