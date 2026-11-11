package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	callgraphDirection string
	callgraphDepth     int
	callgraphFormat    string
)

var callgraphCmd = &cobra.Command{
	Use:   "callgraph <symbol-id>",
	Short: "Get caller/callee relationships for a symbol",
	Long: `Build a lightweight call graph showing callers and callees of a symbol.

Direction options:
  - callers: Show only functions that call this symbol
  - callees: Show only functions called by this symbol
  - both: Show both callers and callees (default)

Examples:
  ckb callgraph 'scip-go gomod myproject myproject/pkg/MyFunction'
  ckb callgraph --direction=callers --depth=2 'symbol-id'
  ckb callgraph --format=human 'symbol-id'`,
	Args: cobra.ExactArgs(1),
	Run:  runCallgraph,
}

func init() {
	callgraphCmd.Flags().StringVar(&callgraphDirection, "direction", "both", "Direction to traverse (callers, callees, both)")
	callgraphCmd.Flags().IntVar(&callgraphDepth, "depth", 1, "Maximum depth to traverse (1-4)")
	callgraphCmd.Flags().StringVar(&callgraphFormat, "format", "human", "Output format (human, json)")
	rootCmd.AddCommand(callgraphCmd)
}

const cliCallGraphNodes = 200

func runCallgraph(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(callgraphFormat)
	symbolId := args[0]

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	symbolId = remapSymbolID(engine, symbolId)
	ctx := newContext()

	opts := query.CallGraphOptions{
		SymbolId:  symbolId,
		Direction: callgraphDirection,
		Depth:     callgraphDepth,
	}
	// A person reading a terminal can take more than the LLM default.
	opts.Limit = cliCallGraphNodes
	response, err := engine.GetCallGraph(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting call graph: %v\n", err)
		os.Exit(1)
	}

	cliResponse := convertCallgraphResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(callgraphFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Callgraph query completed",
		"symbolId", symbolId,
		"direction", callgraphDirection,
		"nodes", len(response.Nodes),
		"edges", len(response.Edges),
		"duration", time.Since(start).Milliseconds(),
	)
}

// CallgraphResponseCLI contains call graph results for CLI output
type CallgraphResponseCLI struct {
	Root       string             `json:"root"`
	Nodes      []CallgraphNodeCLI `json:"nodes"`
	Edges      []CallgraphEdgeCLI `json:"edges"`
	Provenance *ProvenanceCLI     `json:"provenance,omitempty"`
}

// CallgraphNodeCLI represents a node in the call graph
type CallgraphNodeCLI struct {
	ID       string       `json:"id"`
	SymbolId string       `json:"symbolId,omitempty"`
	Name     string       `json:"name"`
	Location *LocationCLI `json:"location,omitempty"`
	Depth    int          `json:"depth"`
	Role     string       `json:"role"`
	Score    float64      `json:"score"`
}

// CallgraphEdgeCLI represents an edge in the call graph
type CallgraphEdgeCLI struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func convertCallgraphResponse(resp *query.CallGraphResponse) *CallgraphResponseCLI {
	nodes := make([]CallgraphNodeCLI, 0, len(resp.Nodes))
	for _, n := range resp.Nodes {
		node := CallgraphNodeCLI{
			ID:       n.ID,
			SymbolId: n.SymbolId,
			Name:     n.Name,
			Depth:    n.Depth,
			Role:     n.Role,
			Score:    n.Score,
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

	edges := make([]CallgraphEdgeCLI, 0, len(resp.Edges))
	for _, e := range resp.Edges {
		edges = append(edges, CallgraphEdgeCLI{
			From: e.From,
			To:   e.To,
		})
	}

	result := &CallgraphResponseCLI{
		Root:  resp.Root,
		Nodes: nodes,
		Edges: edges,
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
