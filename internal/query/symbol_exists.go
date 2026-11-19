package query

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/errors"
)

// SymbolExistsOptions is the input for SymbolExists.
type SymbolExistsOptions struct {
	Name            string
	Kinds           []string
	Scope           string
	IncludeExternal bool
}

// SymbolExistsResult is the response for SymbolExists.
type SymbolExistsResult struct {
	Exists     bool        `json:"exists"`
	Matches    int         `json:"matches"`
	Kinds      []string    `json:"kinds"`
	Receivers  []string    `json:"receivers,omitempty"`
	StaleIndex bool        `json:"staleIndex,omitempty"`
	Provenance *Provenance `json:"provenance"`
}

// SymbolExists answers whether a bare symbol name has any declaration in the index.
// Unlike SearchSymbols it queries symbols_fts_content directly with an exact WHERE
// clause, bypassing FTS5 tokenisation — so class methods and object-property
// declarations whose bare leaf name never surfaces through FTS ranking are found
// reliably.
func (e *Engine) SymbolExists(ctx context.Context, opts SymbolExistsOptions) (*SymbolExistsResult, error) {
	startTime := time.Now()

	if opts.Name == "" {
		return nil, errors.NewInvalidParameterError("name", "name is required")
	}

	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	notFound := func(reason string) *SymbolExistsResult {
		return &SymbolExistsResult{
			Exists:  false,
			Matches: 0,
			Kinds:   []string{},
			Provenance: e.buildProvenance(repoState, "head", startTime, nil,
				CompletenessInfo{Score: 0.5, Reason: reason}),
		}
	}

	if e.db == nil {
		return notFound("db-unavailable"), nil
	}

	if ftsErr := e.EnsureFTSCurrent(ctx); ftsErr != nil {
		e.logger.Warn("Failed to refresh stale FTS content", "error", ftsErr.Error())
	}

	sqlStr := `SELECT name, kind, COALESCE(signature, '') FROM symbols_fts_content WHERE name = ?`
	args := []interface{}{opts.Name}

	if opts.Scope != "" {
		sqlStr += ` AND file_path LIKE ?`
		args = append(args, opts.Scope+"%")
	}

	if !opts.IncludeExternal {
		sqlStr += ` AND file_path NOT LIKE '%node_modules%'`
	}

	if len(opts.Kinds) > 0 {
		placeholders := strings.Repeat("?,", len(opts.Kinds))
		placeholders = placeholders[:len(placeholders)-1]
		sqlStr += ` AND kind IN (` + placeholders + `)`
		for _, k := range opts.Kinds {
			args = append(args, k)
		}
	}

	rows, err := e.db.Query(sqlStr, args...)
	if err != nil {
		// Content table may not exist yet (index not yet populated); treat as not found.
		return notFound("fts-unavailable"), nil
	}
	defer rows.Close() //nolint:errcheck

	kindsSet := map[string]bool{}
	receiversSet := map[string]bool{}
	matchCount := 0

	for rows.Next() {
		var name, kind, signature string
		if scanErr := rows.Scan(&name, &kind, &signature); scanErr != nil {
			continue
		}
		matchCount++
		if kind != "" {
			kindsSet[kind] = true
		}
		// signature is "ReceiverName.leafName" for methods and properties.
		if strings.HasSuffix(signature, "."+name) {
			receiver := signature[:len(signature)-len("."+name)]
			if receiver != "" {
				receiversSet[receiver] = true
			}
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, e.wrapError(rowsErr, errors.InternalError)
	}

	kinds := make([]string, 0, len(kindsSet))
	for k := range kindsSet {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	var receivers []string
	if len(receiversSet) > 0 {
		receivers = make([]string, 0, len(receiversSet))
		for r := range receiversSet {
			receivers = append(receivers, r)
		}
		sort.Strings(receivers)
	}

	return &SymbolExistsResult{
		Exists:     matchCount > 0,
		Matches:    matchCount,
		Kinds:      kinds,
		Receivers:  receivers,
		StaleIndex: repoState.Dirty,
		Provenance: e.buildProvenance(repoState, "head", startTime, nil,
			CompletenessInfo{Score: 1.0, Reason: "exact-match"}),
	}, nil
}
