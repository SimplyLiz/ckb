package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	unwiredFormat        string
	unwiredScope         []string
	unwiredLimit         int
	unwiredMinConfidence float64
	unwiredExclude       []string
	unwiredIncludeTypes  bool
	unwiredMaxNodes      int
)

var unwiredCmd = &cobra.Command{
	Use:   "unwired",
	Short: "Find exported symbols not reachable from entrypoints",
	Long: `Find exported functions and methods that are never transitively called
from any application entrypoint (main, server, CLI commands).

This detects the "built but never plugged in" pattern where modules exist
and are tested but aren't wired into the execution pipeline.

Complements dead-code (which checks reference counts) by checking
reachability from the actual runtime entry points.

Examples:
  ckb unwired
  ckb unwired --scope src/cost,src/multisampling
  ckb unwired --min-confidence 0.9
  ckb unwired --include-types
  ckb unwired --format json`,
	Run: runUnwired,
}

func init() {
	unwiredCmd.Flags().StringVar(&unwiredFormat, "format", "human", "Output format (human, json)")
	unwiredCmd.Flags().StringSliceVar(&unwiredScope, "scope", nil, "Limit to specific packages/paths")
	unwiredCmd.Flags().IntVar(&unwiredLimit, "limit", 100, "Maximum results to return")
	unwiredCmd.Flags().Float64Var(&unwiredMinConfidence, "min-confidence", 0.80, "Minimum confidence threshold (0-1)")
	unwiredCmd.Flags().StringSliceVar(&unwiredExclude, "exclude", nil, "Patterns to exclude")
	unwiredCmd.Flags().BoolVar(&unwiredIncludeTypes, "include-types", false, "Include type definitions (higher FP rate)")
	unwiredCmd.Flags().IntVar(&unwiredMaxNodes, "max-nodes", 100000, "Max symbols in reachable set")
	rootCmd.AddCommand(unwiredCmd)
}

func runUnwired(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(unwiredFormat)

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	opts := query.FindUnwiredModulesOptions{
		Scope:           unwiredScope,
		ExcludePatterns: unwiredExclude,
		MinConfidence:   unwiredMinConfidence,
		IncludeTypes:    unwiredIncludeTypes,
		MaxNodes:        unwiredMaxNodes,
		Limit:           unwiredLimit,
	}

	response, err := engine.FindUnwiredModules(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if unwiredFormat == "json" {
		data, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		printUnwiredHuman(response)
	}

	logger.Debug("Unwired analysis completed",
		"unwiredCount", response.Summary.UnwiredCount,
		"reachableCount", response.ReachableCount,
		"duration", time.Since(start).Milliseconds(),
	)
}

func printUnwiredHuman(resp *query.FindUnwiredModulesResponse) {
	fmt.Println("Unwired Module Analysis")
	fmt.Println("============================================================")
	fmt.Println()

	if len(resp.Entrypoints) == 0 {
		fmt.Println("No entrypoints found, so nothing was analyzed.")
		fmt.Println("CKB starts from a main under cmd/, a package.json script, or a file a")
		fmt.Println("framework calls by convention (Next.js pages and route handlers).")
		return
	}
	fmt.Printf("Entrypoints: %d\n", len(resp.Entrypoints))
	if len(resp.Entrypoints) <= 20 {
		for _, ep := range resp.Entrypoints {
			fmt.Printf("  - %s\n", ep)
		}
	}
	fmt.Println()

	fmt.Printf("Reachable symbols: %d\n", resp.ReachableCount)
	fmt.Printf("Total exported:    %d\n", resp.Summary.TotalExported)
	fmt.Printf("Unwired:           %d\n", resp.Summary.UnwiredCount)
	if resp.Summary.ViaInterface > 0 {
		fmt.Printf("  (%d methods reached through an interface are not listed)\n", resp.Summary.ViaInterface)
	}
	if resp.Partial {
		fmt.Println("  (partial — reachable set budget exhausted)")
	}
	fmt.Println()

	if len(resp.UnwiredModules) == 0 {
		fmt.Println("No unwired modules found.")
		return
	}

	for _, mod := range resp.UnwiredModules {
		fmt.Printf("── %s (%d/%d exported symbols unwired)\n",
			mod.Path, mod.Summary.UnwiredCount, mod.Summary.TotalExported)
		for _, item := range mod.Items {
			fmt.Printf("   %s %s  (%.0f%% confidence)\n",
				item.Kind, item.SymbolName, item.Confidence*100)
			fmt.Printf("     %s\n", item.Reason)
			if item.ReferenceCount > 0 {
				fmt.Printf("     refs: %d (test: %d)\n", item.ReferenceCount, item.TestReferences)
			}
		}
		if more := mod.Summary.UnwiredCount - len(mod.Items); more > 0 {
			fmt.Printf("   … and %d more\n", more)
		}
		fmt.Println()
	}
	if resp.Truncated {
		listed := 0
		for _, mod := range resp.UnwiredModules {
			listed += len(mod.Items)
		}
		fmt.Printf("Showing %d of %d unwired symbols in %d of %d modules. Raise --limit to see more.\n",
			listed, resp.Summary.UnwiredCount, len(resp.UnwiredModules), resp.Summary.UnwiredModules)
	}
}
