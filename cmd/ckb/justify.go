package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	justifyFormat string
)

var justifyCmd = &cobra.Command{
	Use:   "justify <symbol-id>",
	Short: "Get keep/investigate/remove verdict for a symbol",
	Long: `Analyze a symbol and provide a verdict on whether to keep, investigate, or remove it.

The verdict is based on:
  - Active callers (keep)
  - Public API status (investigate if no callers)
  - No callers and not public (remove candidate)

Examples:
  ckb justify 'scip-go gomod myproject myproject/pkg/MyFunction'
  ckb justify --format=human 'symbol-id'`,
	Args: cobra.ExactArgs(1),
	Run:  runJustify,
}

func init() {
	justifyCmd.Flags().StringVar(&justifyFormat, "format", "human", "Output format (human, json)")
	rootCmd.AddCommand(justifyCmd)
}

func runJustify(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(justifyFormat)
	symbolId := args[0]

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	symbolId = remapSymbolID(engine, symbolId)
	ctx := newContext()

	opts := query.JustifySymbolOptions{
		SymbolId: symbolId,
	}
	response, err := engine.JustifySymbol(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error justifying symbol: %v\n", err)
		os.Exit(1)
	}

	cliResponse := convertJustifyResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(justifyFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Justify query completed",
		"symbolId", symbolId,
		"verdict", response.Verdict,
		"duration", time.Since(start).Milliseconds(),
	)
}

// JustifyResponseCLI contains justify results for CLI output
type JustifyResponseCLI struct {
	SymbolId   string         `json:"symbolId"`
	Verdict    string         `json:"verdict"`
	Confidence float64        `json:"confidence"`
	Reasoning  string         `json:"reasoning"`
	Provenance *ProvenanceCLI `json:"provenance,omitempty"`
}

func convertJustifyResponse(resp *query.JustifySymbolResponse) *JustifyResponseCLI {
	result := &JustifyResponseCLI{
		SymbolId:   resp.Resolved.SymbolId,
		Verdict:    resp.Verdict,
		Confidence: resp.Confidence,
		Reasoning:  resp.Reasoning,
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
