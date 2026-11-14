package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	symbolRepoStateMode string
	symbolFormat        string
)

var symbolCmd = &cobra.Command{
	Use:   "symbol <symbolId>",
	Short: "Get detailed symbol information",
	Long: `Retrieve detailed information about a specific symbol by its stable ID.

RepoStateMode:
  head (default) - Use HEAD commit, ignore dirty state
  full - Include dirty state for exact location`,
	Args: cobra.ExactArgs(1),
	Run:  runSymbol,
}

func init() {
	symbolCmd.Flags().StringVar(&symbolRepoStateMode, "repo-state-mode", "head", "Repo state mode (head, full)")
	symbolCmd.Flags().StringVar(&symbolFormat, "format", "human", "Output format (human, json)")
	rootCmd.AddCommand(symbolCmd)
}

func runSymbol(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(symbolFormat)
	symbolID := args[0]

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	symbolID = remapSymbolID(engine, symbolID)
	ctx := newContext()

	// Get symbol from Query Engine
	opts := query.GetSymbolOptions{
		SymbolId:      symbolID,
		RepoStateMode: symbolRepoStateMode,
	}
	response, err := engine.GetSymbol(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting symbol: %v\n", err)
		os.Exit(1)
	}

	// Convert to CLI response format
	cliResponse := convertSymbolResponse(response)

	// Format and output
	output, err := FormatResponse(cliResponse, OutputFormat(symbolFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	duration := time.Since(start).Milliseconds()
	logger.Debug("Symbol query completed",
		"symbolId", symbolID,
		"duration", duration,
	)
}

// SymbolResponseCLI contains detailed symbol information for CLI output
type SymbolResponseCLI struct {
	Symbol     SymbolInfoCLI  `json:"symbol"`
	Location   *LocationCLI   `json:"location,omitempty"`
	Module     *ModuleInfoCLI `json:"module,omitempty"`
	Provenance *ProvenanceCLI `json:"provenance,omitempty"`
}

// SymbolInfoCLI contains symbol details
type SymbolInfoCLI struct {
	StableID             string   `json:"stableId"`
	Name                 string   `json:"name"`
	Kind                 string   `json:"kind"`
	SignatureNormalized  string   `json:"signatureNormalized,omitempty"`
	SignatureFull        string   `json:"signatureFull,omitempty"`
	Visibility           string   `json:"visibility"`
	VisibilityConfidence float64  `json:"visibilityConfidence"`
	ContainerName        string   `json:"containerName,omitempty"`
	Documentation        string   `json:"documentation,omitempty"`
	Modifiers            []string `json:"modifiers,omitempty"`
}

// LocationCLI represents a code location
type LocationCLI struct {
	FileID      string `json:"fileId"`
	Path        string `json:"path"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine,omitempty"`
	EndColumn   int    `json:"endColumn,omitempty"`
}

// ModuleInfoCLI contains module information
type ModuleInfoCLI struct {
	ModuleID string `json:"moduleId"`
	Name     string `json:"name"`
	RootPath string `json:"rootPath,omitempty"`
	Language string `json:"language,omitempty"`
}

// ProvenanceCLI contains response metadata
type ProvenanceCLI struct {
	RepoStateId     string   `json:"repoStateId"`
	RepoStateDirty  bool     `json:"repoStateDirty"`
	QueryDurationMs int64    `json:"queryDurationMs"`
	Warnings        []string `json:"warnings,omitempty"`
}

func convertSymbolResponse(resp *query.GetSymbolResponse) *SymbolResponseCLI {
	result := &SymbolResponseCLI{}

	if resp.Symbol != nil {
		visibility := "unknown"
		visibilityConfidence := 0.0
		if resp.Symbol.Visibility != nil {
			visibility = resp.Symbol.Visibility.Visibility
			visibilityConfidence = resp.Symbol.Visibility.Confidence
		}

		result.Symbol = SymbolInfoCLI{
			StableID:             resp.Symbol.StableId,
			Name:                 resp.Symbol.Name,
			Kind:                 resp.Symbol.Kind,
			SignatureNormalized:  resp.Symbol.SignatureNormalized,
			SignatureFull:        resp.Symbol.Signature,
			Visibility:           visibility,
			VisibilityConfidence: visibilityConfidence,
			ContainerName:        resp.Symbol.ContainerName,
			Documentation:        resp.Symbol.Documentation,
		}

		if resp.Symbol.Location != nil {
			result.Location = &LocationCLI{
				FileID:      resp.Symbol.Location.FileId,
				Path:        resp.Symbol.Location.FileId,
				StartLine:   resp.Symbol.Location.StartLine,
				StartColumn: resp.Symbol.Location.StartColumn,
				EndLine:     resp.Symbol.Location.EndLine,
				EndColumn:   resp.Symbol.Location.EndColumn,
			}
		}

		if resp.Symbol.ModuleId != "" {
			result.Module = &ModuleInfoCLI{
				ModuleID: resp.Symbol.ModuleId,
			}
		}
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
