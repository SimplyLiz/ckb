package mcp

import (
	"context"
	"fmt"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// CompactPrepareChange is a token-budget-friendly view of prepareChange results.
type CompactPrepareChange struct {
	Target        string   `json:"target"`
	Risk          string   `json:"risk"`
	AffectedCount int      `json:"affected_count"`
	AffectedFiles []string `json:"affected_files"` // top 10
	TestsNeeded   []string `json:"tests_needed"`   // top 5
	OwnerSuggest  string   `json:"owner_suggest,omitempty"`
	Summary       string   `json:"summary"`
	Backend       string   `json:"backend"`
	Accuracy      string   `json:"accuracy"`
}

// buildMCPCompactPrepareChange converts a PrepareChangeResponse into compact form.
func buildMCPCompactPrepareChange(target string, r *query.PrepareChangeResponse, activeBackend string) CompactPrepareChange {
	risk := "unknown"
	if r.RiskAssessment != nil {
		risk = r.RiskAssessment.Level
	}

	seen := make(map[string]bool)
	var affectedFiles []string
	for _, dep := range r.DirectDependents {
		if dep.File != "" && !seen[dep.File] {
			seen[dep.File] = true
			affectedFiles = append(affectedFiles, dep.File)
		}
		if len(affectedFiles) >= 10 {
			break
		}
	}

	affectedCount := len(r.DirectDependents)
	if r.TransitiveImpact != nil {
		affectedCount += r.TransitiveImpact.TotalCallers
	}

	var testsNeeded []string
	for i, t := range r.RelatedTests {
		if i >= 5 {
			break
		}
		name := t.File
		if t.Name != "" {
			name = t.Name
		}
		testsNeeded = append(testsNeeded, name)
	}

	summary := fmt.Sprintf("Changing %s affects %d files with %s risk.", target, len(affectedFiles), risk)

	return CompactPrepareChange{
		Target:        target,
		Risk:          risk,
		AffectedCount: affectedCount,
		AffectedFiles: affectedFiles,
		TestsNeeded:   testsNeeded,
		Summary:       summary,
		Backend:       activeBackend,
		Accuracy:      envelope.AccuracyForBackend(activeBackend),
	}
}

// v8.0 Compound tool implementations
// These tools aggregate multiple granular queries to reduce AI tool calls by 60-70%

// toolExplore provides comprehensive area exploration
func (s *MCPServer) toolExplore(params map[string]interface{}) (*envelope.Response, error) {
	target, ok := params["target"].(string)
	if !ok || target == "" {
		return nil, errors.NewInvalidParameterError("target", "required")
	}

	depth := query.ExploreStandard
	if v, ok := params["depth"].(string); ok {
		switch v {
		case "shallow":
			depth = query.ExploreShallow
		case "deep":
			depth = query.ExploreDeep
		case "standard":
			depth = query.ExploreStandard
		}
	}

	focus := query.FocusStructure
	if v, ok := params["focus"].(string); ok {
		switch v {
		case "structure":
			focus = query.FocusStructure
		case "dependencies":
			focus = query.FocusDependencies
		case "changes":
			focus = query.FocusChanges
		}
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	result, err := engine.Explore(ctx, query.ExploreOptions{
		Target: target,
		Depth:  depth,
		Focus:  focus,
	})
	if err != nil {
		return nil, s.enrichNotFoundError(err)
	}

	resp := NewToolResponse().Data(result)
	for _, dw := range engine.GetDegradationWarnings() {
		resp.Warning(dw.Message)
	}
	return resp.Build(), nil
}

// toolUnderstand provides comprehensive symbol deep-dive
func (s *MCPServer) toolUnderstand(params map[string]interface{}) (*envelope.Response, error) {
	q, ok := params["query"].(string)
	if !ok || q == "" {
		return nil, errors.NewInvalidParameterError("query", "required")
	}

	includeReferences := true
	if v, ok := params["includeReferences"].(bool); ok {
		includeReferences = v
	}

	includeCallGraph := true
	if v, ok := params["includeCallGraph"].(bool); ok {
		includeCallGraph = v
	}

	maxReferences := 50
	if v, ok := params["maxReferences"].(float64); ok {
		maxReferences = int(v)
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	result, err := engine.Understand(ctx, query.UnderstandOptions{
		Query:             q,
		IncludeReferences: includeReferences,
		IncludeCallGraph:  includeCallGraph,
		MaxReferences:     maxReferences,
	})
	if err != nil {
		return nil, s.enrichNotFoundError(err)
	}

	resp := NewToolResponse().Data(result)
	for _, dw := range engine.GetDegradationWarnings() {
		resp.Warning(dw.Message)
	}
	return resp.Build(), nil
}

// toolPrepareChange provides pre-change analysis
func (s *MCPServer) toolPrepareChange(params map[string]interface{}) (*envelope.Response, error) {
	target, ok := params["target"].(string)
	if !ok || target == "" {
		return nil, errors.NewInvalidParameterError("target", "required")
	}

	changeType := query.ChangeModify
	if v, ok := params["changeType"].(string); ok {
		switch v {
		case "modify":
			changeType = query.ChangeModify
		case "rename":
			changeType = query.ChangeRename
		case "delete":
			changeType = query.ChangeDelete
		case "extract":
			changeType = query.ChangeExtract
		case "move":
			changeType = query.ChangeMove
		}
	}

	var targetPath string
	if v, ok := params["targetPath"].(string); ok {
		targetPath = v
	}

	var startLine, endLine int
	if v, ok := params["startLine"].(float64); ok {
		startLine = int(v)
	}
	if v, ok := params["endLine"].(float64); ok {
		endLine = int(v)
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	result, err := engine.PrepareChange(ctx, query.PrepareChangeOptions{
		Target:     target,
		ChangeType: changeType,
		TargetPath: targetPath,
		StartLine:  startLine,
		EndLine:    endLine,
	})
	if err != nil {
		return nil, s.enrichNotFoundError(err)
	}

	activeBackend := engine.ActiveBackendName()

	var changeFiles []string
	if result != nil {
		if result.Target != nil && result.Target.Path != "" {
			changeFiles = append(changeFiles, result.Target.Path)
		}
		for _, d := range result.DirectDependents {
			changeFiles = append(changeFiles, d.File)
		}
		for _, t := range result.RelatedTests {
			changeFiles = append(changeFiles, t.File)
		}
	}
	indexFreshness := engine.IndexFreshnessFor(ctx, changeFiles)

	// Support compact format
	format := "full"
	if v, ok := params["format"].(string); ok && v != "" {
		format = v
	}

	if format == "compact" {
		compact := buildMCPCompactPrepareChange(target, result, activeBackend)
		resp := NewToolResponse().Data(compact).WithBackend(activeBackend, s.logger).WithIndexFreshness(indexFreshness)
		return resp.Build(), nil
	}

	resp := NewToolResponse().Data(result).WithBackend(activeBackend, s.logger).WithIndexFreshness(indexFreshness)
	for _, dw := range engine.GetDegradationWarnings() {
		resp.Warning(dw.Message)
	}
	return resp.Build(), nil
}

// toolSwitchProject switches CKB to a different project directory
func (s *MCPServer) toolSwitchProject(params map[string]interface{}) (*envelope.Response, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		return nil, errors.NewInvalidParameterError("path", "required")
	}

	newRoot, err := s.switchProject(path)
	if err != nil {
		return nil, err
	}

	return NewToolResponse().
		Data(map[string]interface{}{
			"switched": true,
			"repoRoot": newRoot,
			"message":  "Successfully switched to " + newRoot,
		}).
		Build(), nil
}

// toolBatchGet retrieves multiple symbols by ID
func (s *MCPServer) toolBatchGet(params map[string]interface{}) (*envelope.Response, error) {
	symbolIds, ok := params["symbolIds"].([]interface{})
	if !ok || len(symbolIds) == 0 {
		return nil, errors.NewInvalidParameterError("symbolIds", "required array of symbol IDs")
	}

	ids := make([]string, 0, len(symbolIds))
	for _, id := range symbolIds {
		if s, ok := id.(string); ok {
			ids = append(ids, s)
		}
	}

	if len(ids) == 0 {
		return nil, errors.NewInvalidParameterError("symbolIds", "must contain string values")
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	includeCounts := false
	if v, ok := params["includeCounts"].(bool); ok {
		includeCounts = v
	}

	ctx := context.Background()
	result, err := engine.BatchGet(ctx, query.BatchGetOptions{
		SymbolIds:     ids,
		IncludeCounts: includeCounts,
	})
	if err != nil {
		return nil, err
	}

	return NewToolResponse().
		Data(result).
		Build(), nil
}

// toolBatchSearch performs multiple symbol searches
func (s *MCPServer) toolBatchSearch(params map[string]interface{}) (*envelope.Response, error) {
	queriesRaw, ok := params["queries"].([]interface{})
	if !ok || len(queriesRaw) == 0 {
		return nil, errors.NewInvalidParameterError("queries", "required array of search queries")
	}

	queries := make([]query.BatchSearchQuery, 0, len(queriesRaw))
	for _, qRaw := range queriesRaw {
		qMap, ok := qRaw.(map[string]interface{})
		if !ok {
			continue
		}

		q := query.BatchSearchQuery{}

		if v, ok := qMap["query"].(string); ok {
			q.Query = v
		}
		if v, ok := qMap["kind"].(string); ok {
			q.Kind = v
		}
		if v, ok := qMap["scope"].(string); ok {
			q.Scope = v
		}
		if v, ok := qMap["limit"].(float64); ok {
			q.Limit = int(v)
		}

		if q.Query != "" {
			queries = append(queries, q)
		}
	}

	if len(queries) == 0 {
		return nil, errors.NewInvalidParameterError("queries", "must contain valid query objects")
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	result, err := engine.BatchSearch(ctx, query.BatchSearchOptions{
		Queries: queries,
	})
	if err != nil {
		return nil, s.enrichNotFoundError(err)
	}

	return NewToolResponse().
		Data(result).
		Build(), nil
}

// toolPlanRefactor provides unified refactoring planning
func (s *MCPServer) toolPlanRefactor(params map[string]interface{}) (*envelope.Response, error) {
	target, ok := params["target"].(string)
	if !ok || target == "" {
		return nil, errors.NewInvalidParameterError("target", "required")
	}

	changeType := query.ChangeModify
	if v, ok := params["changeType"].(string); ok {
		switch v {
		case "modify":
			changeType = query.ChangeModify
		case "rename":
			changeType = query.ChangeRename
		case "delete":
			changeType = query.ChangeDelete
		case "extract":
			changeType = query.ChangeExtract
		case "move":
			changeType = query.ChangeMove
		}
	}

	var targetPath string
	if v, ok := params["targetPath"].(string); ok {
		targetPath = v
	}

	engine, err := s.GetEngine()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	result, err := engine.PlanRefactor(ctx, query.PlanRefactorOptions{
		Target:     target,
		ChangeType: changeType,
		TargetPath: targetPath,
	})
	if err != nil {
		return nil, s.enrichNotFoundError(err)
	}

	resp := NewToolResponse().Data(result)
	for _, dw := range engine.GetDegradationWarnings() {
		resp.Warning(dw.Message)
	}
	return resp.Build(), nil
}
