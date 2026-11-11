package api

import (
	"net/http"
	"strconv"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// handleUnwired handles GET /unwired — find exported symbols not reachable from entrypoints.
func (s *Server) handleUnwired(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	q := r.URL.Query()

	opts := query.FindUnwiredModulesOptions{
		MinConfidence: 0.80,
		Limit:         100,
		MaxNodes:      100000,
	}

	if scope := q.Get("scope"); scope != "" {
		opts.Scope = []string{scope}
	}
	if minConf := q.Get("minConfidence"); minConf != "" {
		if v, err := strconv.ParseFloat(minConf, 64); err == nil {
			opts.MinConfidence = v
		}
	}
	if limit := q.Get("limit"); limit != "" {
		if v, err := strconv.Atoi(limit); err == nil {
			opts.Limit = v
		}
	}
	if q.Get("includeTypes") == "true" {
		opts.IncludeTypes = true
	}

	result, err := s.engine.FindUnwiredModules(ctx, opts)
	if err != nil {
		InternalError(w, "Failed to find unwired modules", err)
		return
	}

	WriteJSON(w, result, http.StatusOK)
}
