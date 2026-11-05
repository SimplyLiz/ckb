package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"
	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// looksLikeSCIPID returns true if the string looks like a SCIP symbol ID, from
// any indexer. Parsing is what tells them apart from a name to search for; the
// scheme is free-form: rust-analyzer, semanticdb and cxx all have their own.
func looksLikeSCIPID(s string) bool {
	if strings.HasPrefix(s, "local ") {
		return true
	}
	sym, err := bindingscip.ParseSymbol(s)
	return err == nil && len(sym.Descriptors) > 0
}

var (
	refsScope       string
	refsIncludeTest bool
	refsLimit       int
	refsFormat      string
)

var refsCmd = &cobra.Command{
	Use:   "refs <symbolId>",
	Short: "Find all references to a symbol",
	Long: `Find all references to a symbol across the codebase.

Examples:
  ckb refs symbol-123
  ckb refs symbol-123 --scope=api-module
  ckb refs symbol-123 --include-tests
  ckb refs symbol-123 --limit=100`,
	Args: cobra.ExactArgs(1),
	Run:  runRefs,
}

func init() {
	refsCmd.Flags().StringVar(&refsScope, "scope", "", "Limit search to module ID")
	refsCmd.Flags().BoolVar(&refsIncludeTest, "include-tests", false, "Include test file references")
	refsCmd.Flags().IntVar(&refsLimit, "limit", 100, "Maximum number of references")
	refsCmd.Flags().StringVar(&refsFormat, "format", "human", "Output format (human, json)")
	rootCmd.AddCommand(refsCmd)
}

func runRefs(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(refsFormat)
	symbolID := args[0]

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	// If the input doesn't look like a SCIP ID, try searching for it first
	if !looksLikeSCIPID(symbolID) {
		searchOpts := query.SearchSymbolsOptions{
			Query: symbolID,
			Limit: 1,
		}
		searchResult, searchErr := engine.SearchSymbols(ctx, searchOpts)
		if searchErr == nil && len(searchResult.Symbols) > 0 {
			// Use the first matching symbol's stable ID
			symbolID = searchResult.Symbols[0].StableId
			logger.Debug("Resolved symbol name to SCIP ID",
				"query", args[0],
				"stableId", symbolID,
			)
		}
	}

	// Find references using Query Engine
	opts := query.FindReferencesOptions{
		SymbolId:     symbolID,
		Scope:        refsScope,
		IncludeTests: refsIncludeTest,
		Limit:        refsLimit,
	}
	response, err := engine.FindReferences(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding references: %v\n", err)
		os.Exit(1)
	}

	// Convert to CLI response format
	cliResponse := convertRefsResponse(symbolID, response)

	// Format and output
	output, err := FormatResponse(cliResponse, OutputFormat(refsFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("References query completed",
		"symbolId", symbolID,
		"refs", len(response.References),
		"duration", time.Since(start).Milliseconds(),
	)
}

// ReferencesResponseCLI contains reference results for CLI output
type ReferencesResponseCLI struct {
	SymbolID        string                `json:"symbolId"`
	TotalReferences int                   `json:"totalReferences"`
	References      []ReferenceCLI        `json:"references"`
	ByModule        []ModuleReferencesCLI `json:"byModule,omitempty"`
	Provenance      *ProvenanceCLI        `json:"provenance,omitempty"`
}

// ReferenceCLI represents a single reference to a symbol
type ReferenceCLI struct {
	Location   *LocationCLI `json:"location"`
	Kind       string       `json:"kind"`
	Context    string       `json:"context,omitempty"`
	FromSymbol string       `json:"fromSymbol,omitempty"`
	IsTest     bool         `json:"isTest"`
}

// ModuleReferencesCLI groups references by module
type ModuleReferencesCLI struct {
	ModuleID   string `json:"moduleId"`
	ModuleName string `json:"moduleName"`
	Count      int    `json:"count"`
}

func convertRefsResponse(symbolID string, resp *query.FindReferencesResponse) *ReferencesResponseCLI {
	refs := make([]ReferenceCLI, 0, len(resp.References))
	moduleCount := make(map[string]int)

	for _, r := range resp.References {
		ref := ReferenceCLI{
			Kind:    r.Kind,
			Context: r.Context,
			IsTest:  r.IsTest,
		}

		if r.Location != nil {
			ref.Location = &LocationCLI{
				FileID:      r.Location.FileId,
				Path:        r.Location.FileId,
				StartLine:   r.Location.StartLine,
				StartColumn: r.Location.StartColumn,
				EndLine:     r.Location.EndLine,
				EndColumn:   r.Location.EndColumn,
			}
		}

		refs = append(refs, ref)

		// Count by file path (no module info in references)
		if r.Location != nil {
			moduleCount[r.Location.FileId]++
		}
	}

	// Convert module counts
	byModule := make([]ModuleReferencesCLI, 0, len(moduleCount))
	for moduleID, count := range moduleCount {
		byModule = append(byModule, ModuleReferencesCLI{
			ModuleID: moduleID,
			Count:    count,
		})
	}

	result := &ReferencesResponseCLI{
		SymbolID:        symbolID,
		TotalReferences: resp.TotalCount,
		References:      refs,
		ByModule:        byModule,
	}

	if resp.Provenance != nil {
		result.Provenance = &ProvenanceCLI{
			RepoStateId:     resp.Provenance.RepoStateId,
			RepoStateDirty:  resp.Provenance.RepoStateDirty,
			QueryDurationMs: resp.Provenance.QueryDurationMs,
		}
	}

	return result
}
