package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/jobs"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// v6.1 Job Management Handlers

// handleListJobs handles GET /jobs
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	opts := jobs.ListJobsOptions{}

	// Parse query params
	if status := r.URL.Query().Get("status"); status != "" {
		opts.Status = []jobs.JobStatus{jobs.JobStatus(status)}
	}
	if typ := r.URL.Query().Get("type"); typ != "" {
		opts.Type = []jobs.JobType{jobs.JobType(typ)}
	}
	if limit := QueryParamInt(r, "limit", 20); limit > 0 {
		opts.Limit = limit
	}

	resp, err := s.engine.ListJobs(opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleJobRoutes handles /jobs/:id routes
func (s *Server) handleJobRoutes(w http.ResponseWriter, r *http.Request) {
	// Extract job ID from path: /jobs/{id} or /jobs/{id}/cancel
	path := strings.TrimPrefix(r.URL.Path, "/jobs/")
	parts := strings.SplitN(path, "/", 2)
	jobID := parts[0]

	if jobID == "" {
		http.Error(w, "Missing job ID", http.StatusBadRequest)
		return
	}

	// Check for /cancel suffix
	if len(parts) > 1 && parts[1] == "cancel" {
		s.handleCancelJob(w, r, jobID)
		return
	}

	// Default: get job status
	s.handleGetJobStatus(w, r, jobID)
}

// handleGetJobStatus handles GET /jobs/:id
func (s *Server) handleGetJobStatus(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	job, err := s.engine.GetJob(jobID)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	if job == nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	WriteJSON(w, job, http.StatusOK)
}

// handleCancelJob handles POST /jobs/:id/cancel
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.engine.CancelJob(jobID)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, map[string]interface{}{
		"jobId":  jobID,
		"status": "cancelled",
	}, http.StatusOK)
}

// handleRefreshArchitecture handles POST /architecture/refresh
func (s *Server) handleRefreshArchitecture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	// Parse request body
	var req struct {
		Scope  string `json:"scope"`
		Force  bool   `json:"force"`
		DryRun bool   `json:"dryRun"`
		Async  bool   `json:"async"`
	}
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			WriteError(w, err, http.StatusBadRequest)
			return
		}
	}

	// Default scope
	if req.Scope == "" {
		req.Scope = "all"
	}

	opts := query.RefreshArchitectureOptions{
		Scope:  req.Scope,
		Force:  req.Force,
		DryRun: req.DryRun,
		Async:  req.Async,
	}

	resp, err := s.engine.RefreshArchitecture(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// v6.0 Architectural Memory Handlers

// handleGetHotspots handles GET /hotspots
func (s *Server) handleGetHotspots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	opts := query.GetHotspotsOptions{
		Limit: QueryParamInt(r, "limit", 20),
	}

	if scope := r.URL.Query().Get("scope"); scope != "" {
		opts.Scope = scope
	}

	resp, err := s.engine.GetHotspots(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleGetOwnership handles GET /ownership
func (s *Server) handleGetOwnership(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "Missing 'path' parameter", http.StatusBadRequest)
		return
	}

	opts := query.GetOwnershipOptions{
		Path:           path,
		IncludeBlame:   QueryParamBool(r, "includeBlame", true),
		IncludeHistory: QueryParamBool(r, "includeHistory", false),
	}

	resp, err := s.engine.GetOwnership(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleDecisions handles GET and POST /decisions
func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// List decisions
		queryOpts := &query.DecisionsQuery{
			Limit: QueryParamInt(r, "limit", 20),
		}
		if module := r.URL.Query().Get("module"); module != "" {
			queryOpts.ModuleID = module
		}
		if status := r.URL.Query().Get("status"); status != "" {
			queryOpts.Status = status
		}

		resp, err := s.engine.GetDecisions(queryOpts)
		if err != nil {
			WriteError(w, err, http.StatusInternalServerError)
			return
		}
		WriteJSON(w, resp, http.StatusOK)

	case http.MethodPost:
		// Record new decision
		var req query.RecordDecisionInput
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, err, http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		resp, err := s.engine.RecordDecision(&req)
		if err != nil {
			WriteError(w, err, http.StatusInternalServerError)
			return
		}
		WriteJSON(w, resp, http.StatusCreated)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleModules handles /modules/:id/* routes
func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	// Extract module ID and action from path: /modules/{id}/overview or /modules/{id}/responsibilities
	path := strings.TrimPrefix(r.URL.Path, "/modules/")
	parts := strings.SplitN(path, "/", 2)
	moduleID := parts[0]

	if moduleID == "" {
		http.Error(w, "Missing module ID", http.StatusBadRequest)
		return
	}

	action := "overview"
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "overview":
		opts := query.ModuleOverviewOptions{
			Path: moduleID, // moduleID is typically the path
		}
		resp, err := s.engine.GetModuleOverview(ctx, opts)
		if err != nil {
			WriteError(w, err, http.StatusInternalServerError)
			return
		}
		WriteJSON(w, resp, http.StatusOK)

	case "responsibilities":
		opts := query.GetModuleResponsibilitiesOptions{
			ModuleId: moduleID,
		}
		resp, err := s.engine.GetModuleResponsibilities(ctx, opts)
		if err != nil {
			WriteError(w, err, http.StatusInternalServerError)
			return
		}
		WriteJSON(w, resp, http.StatusOK)

	default:
		http.Error(w, "Unknown module action: "+action, http.StatusNotFound)
	}
}

// handleGetCallGraph handles GET /callgraph/:id
func (s *Server) handleGetCallGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	symbolID := strings.TrimPrefix(r.URL.Path, "/callgraph/")
	if symbolID == "" {
		http.Error(w, "Missing symbol ID", http.StatusBadRequest)
		return
	}

	opts := query.CallGraphOptions{
		SymbolId:  s.remapSymbolID(w, symbolID),
		Direction: r.URL.Query().Get("direction"),
		Depth:     QueryParamInt(r, "depth", 2),
	}

	resp, err := s.engine.GetCallGraph(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleExplainSymbol handles GET /explain/symbol/:id
func (s *Server) handleExplainSymbol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	symbolID := strings.TrimPrefix(r.URL.Path, "/explain/symbol/")
	if symbolID == "" {
		http.Error(w, "Missing symbol ID", http.StatusBadRequest)
		return
	}

	opts := query.ExplainSymbolOptions{
		SymbolId: s.remapSymbolID(w, symbolID),
	}

	resp, err := s.engine.ExplainSymbol(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleJustifySymbol handles GET /justify/:id
func (s *Server) handleJustifySymbol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	symbolID := strings.TrimPrefix(r.URL.Path, "/justify/")
	if symbolID == "" {
		http.Error(w, "Missing symbol ID", http.StatusBadRequest)
		return
	}

	opts := query.JustifySymbolOptions{
		SymbolId: s.remapSymbolID(w, symbolID),
	}

	resp, err := s.engine.JustifySymbol(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleSummarizePR handles GET/POST /pr/summary
func (s *Server) handleSummarizePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	opts := query.SummarizePROptions{
		BaseBranch:       "main",
		IncludeOwnership: true,
	}

	// Parse query params for GET, body for POST
	if r.Method == http.MethodGet {
		if base := r.URL.Query().Get("baseBranch"); base != "" {
			opts.BaseBranch = base
		}
		if head := r.URL.Query().Get("headBranch"); head != "" {
			opts.HeadBranch = head
		}
		opts.IncludeOwnership = QueryParamBool(r, "includeOwnership", true)
	} else {
		// POST with JSON body
		var req struct {
			BaseBranch       string `json:"baseBranch"`
			HeadBranch       string `json:"headBranch"`
			IncludeOwnership *bool  `json:"includeOwnership"`
		}
		if r.Body != nil {
			defer r.Body.Close()
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
				WriteError(w, err, http.StatusBadRequest)
				return
			}
		}
		if req.BaseBranch != "" {
			opts.BaseBranch = req.BaseBranch
		}
		if req.HeadBranch != "" {
			opts.HeadBranch = req.HeadBranch
		}
		if req.IncludeOwnership != nil {
			opts.IncludeOwnership = *req.IncludeOwnership
		}
	}

	resp, err := s.engine.SummarizePR(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}

// handleOwnershipDrift handles GET /ownership/drift
func (s *Server) handleOwnershipDrift(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := context.Background()

	opts := query.OwnershipDriftOptions{
		Scope:     r.URL.Query().Get("scope"),
		Threshold: QueryParamFloat(r, "threshold", 0.3),
		Limit:     QueryParamInt(r, "limit", 20),
	}

	resp, err := s.engine.GetOwnershipDrift(ctx, opts)
	if err != nil {
		WriteError(w, err, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, resp, http.StatusOK)
}
