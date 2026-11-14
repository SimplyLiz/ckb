package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	traceFormat   string
	traceMaxPaths int
	traceMaxDepth int
)

var traceCmd = &cobra.Command{
	Use:   "trace <symbol-id>",
	Short: "Trace how a symbol is reached from system entrypoints",
	Long: `Trace usage paths from system entrypoints to a target symbol.

Returns causal paths showing how the symbol is called, not just immediate neighbors.
Useful for understanding how code is actually used.

Examples:
  ckb trace 'scip-go gomod myproject myproject/pkg/MyFunction'
  ckb trace --max-paths=20 --max-depth=3 'symbol-id'
  ckb trace --format=human 'symbol-id'`,
	Args: cobra.ExactArgs(1),
	Run:  runTrace,
}

func init() {
	traceCmd.Flags().StringVar(&traceFormat, "format", "human", "Output format (human, json)")
	traceCmd.Flags().IntVar(&traceMaxPaths, "max-paths", 10, "Maximum paths to return")
	traceCmd.Flags().IntVar(&traceMaxDepth, "max-depth", 5, "Maximum path depth (1-5)")
	rootCmd.AddCommand(traceCmd)
}

func runTrace(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(traceFormat)
	symbolId := args[0]

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	symbolId = remapSymbolID(engine, symbolId)
	ctx := newContext()

	opts := query.TraceUsageOptions{
		SymbolId: symbolId,
		MaxPaths: traceMaxPaths,
		MaxDepth: traceMaxDepth,
	}
	response, err := engine.TraceUsage(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error tracing usage: %v\n", err)
		os.Exit(1)
	}

	cliResponse := convertTraceResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(traceFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Trace usage completed",
		"symbolId", symbolId,
		"pathsFound", len(response.Paths),
		"duration", time.Since(start).Milliseconds(),
	)
}

// TraceResponseCLI contains trace results for CLI output
type TraceResponseCLI struct {
	TargetSymbol    string         `json:"targetSymbol"`
	Paths           []UsagePathCLI `json:"paths"`
	TotalPathsFound int            `json:"totalPathsFound"`
	Confidence      float64        `json:"confidence"`
	Limitations     []string       `json:"limitations,omitempty"`
	Provenance      *ProvenanceCLI `json:"provenance,omitempty"`
}

type UsagePathCLI struct {
	PathType   string        `json:"pathType"`
	Nodes      []PathNodeCLI `json:"nodes"`
	Confidence float64       `json:"confidence"`
	Score      float64       `json:"score"`
}

type PathNodeCLI struct {
	SymbolId string       `json:"symbolId"`
	Name     string       `json:"name"`
	Kind     string       `json:"kind,omitempty"`
	Location *LocationCLI `json:"location,omitempty"`
	Role     string       `json:"role"`
}

func convertTraceResponse(resp *query.TraceUsageResponse) *TraceResponseCLI {
	paths := make([]UsagePathCLI, 0, len(resp.Paths))
	for _, p := range resp.Paths {
		nodes := make([]PathNodeCLI, 0, len(p.Nodes))
		for _, n := range p.Nodes {
			node := PathNodeCLI{
				SymbolId: n.SymbolId,
				Name:     n.Name,
				Kind:     n.Kind,
				Role:     n.Role,
			}
			if n.Location != nil {
				node.Location = &LocationCLI{
					FileID:      n.Location.FileId,
					Path:        n.Location.FileId,
					StartLine:   n.Location.StartLine,
					StartColumn: n.Location.StartColumn,
				}
			}
			nodes = append(nodes, node)
		}

		path := UsagePathCLI{
			PathType:   p.PathType,
			Nodes:      nodes,
			Confidence: p.Confidence,
		}
		if p.Ranking != nil {
			path.Score = p.Ranking.Score
		}
		paths = append(paths, path)
	}

	result := &TraceResponseCLI{
		TargetSymbol:    resp.TargetSymbol,
		Paths:           paths,
		TotalPathsFound: resp.TotalPathsFound,
		Confidence:      resp.Confidence,
		Limitations:     resp.Limitations,
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
