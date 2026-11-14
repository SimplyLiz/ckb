package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/perf"
)

// ─── parent ──────────────────────────────────────────────────────────────────

var perfCmd = &cobra.Command{
	Use:   "perf",
	Short: "Scan for structural performance problems",
	Long: `Scan the codebase for structural issues that indicate hidden complexity.

Subcommands:

  coupling    Hidden coupling — file pairs that co-change frequently in git
              but have no static import edge between them.

  structural  Loop call sites in hot files — call expressions inside loop
              bodies in frequently-changed files (O(n)/O(n²) risk).

Run 'ckb perf <subcommand> --help' for details.`,
}

func init() {
	rootCmd.AddCommand(perfCmd)
}

// ─── ckb perf coupling ────────────────────────────────────────────────────────

var (
	perfCouplingMinCorrelation float64
	perfCouplingMinCoChanges   int
	perfCouplingWindowDays     int
	perfCouplingLimit          int
	perfCouplingScope          []string
	perfCouplingFormat         string
)

var perfCouplingCmd = &cobra.Command{
	Use:   "coupling",
	Short: "Find hidden coupling (co-change without a static link)",
	Long: `Find source file pairs that co-changed in the last 500 commits but have no
link in the SCIP index: neither references a symbol the other defines, and they
are not files of one Go package. This is the primary hidden-complexity signal:
files that look unrelated in the dependency graph but are implicitly coupled
through shared state, side effects, or a third party. Needs an index
('ckb index'); without one no pair is called hidden.

Examples:
  ckb perf coupling
  ckb perf coupling --min-correlation=0.5
  ckb perf coupling --scope=internal/auth,internal/sessions
  ckb perf coupling --format=json`,
	Run: runPerfCoupling,
}

func init() {
	perfCouplingCmd.Flags().Float64Var(&perfCouplingMinCorrelation, "min-correlation", 0.3, "Minimum co-change score (0–1): shared commits / (commits of the less-changed file + 2)")
	perfCouplingCmd.Flags().IntVar(&perfCouplingMinCoChanges, "min-co-changes", 3, "Minimum number of shared commits to consider a pair")
	perfCouplingCmd.Flags().IntVar(&perfCouplingWindowDays, "window", 0, "Ignored: the history is the last 500 commits")
	_ = perfCouplingCmd.Flags().MarkDeprecated("window", "the history is the last 500 commits")
	perfCouplingCmd.Flags().IntVar(&perfCouplingLimit, "limit", 50, "Maximum hidden-coupling pairs to return")
	perfCouplingCmd.Flags().StringSliceVar(&perfCouplingScope, "scope", nil, "Limit scan to these paths (comma-separated or repeated flag)")
	perfCouplingCmd.Flags().StringVar(&perfCouplingFormat, "format", "human", "Output format: human or json")
	perfCmd.AddCommand(perfCouplingCmd)
}

func runPerfCoupling(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(perfCouplingFormat)
	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)

	ctx := context.Background()
	result, err := engine.ScanHiddenCoupling(ctx, perf.ScanOptions{
		Scope:          perfCouplingScope,
		MinCorrelation: perfCouplingMinCorrelation,
		MinCoChanges:   perfCouplingMinCoChanges,
		Limit:          perfCouplingLimit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running perf coupling scan: %v\n", err)
		os.Exit(1)
	}

	if OutputFormat(perfCouplingFormat) == FormatJSON {
		output, err := FormatResponse(result, FormatJSON)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(output)
		return
	}

	printPerfCouplingResult(result)

	logger.Debug("Perf coupling scan completed",
		"hidden", len(result.HiddenCoupling),
		"pairsChecked", result.Summary.PairsChecked,
		"duration", time.Since(start).Milliseconds(),
	)
}

func printPerfCouplingResult(result *perf.PerfScanResult) {
	s := result.Summary
	fmt.Printf("Hidden coupling scan: last %d commits, %d files, %d pairs checked, %d hidden pairs found\n",
		s.CommitsRead, s.FilesObserved, s.PairsChecked, s.HiddenPairsFound)
	if s.PairsNotIndexed > 0 {
		fmt.Printf("%d pair(s) skipped: a file is not source code in the index, so its links are unknown\n", s.PairsNotIndexed)
	}
	fmt.Println()

	if s.NotAssessed != "" {
		fmt.Println("Not assessed: " + s.NotAssessed)
		return
	}
	if len(result.HiddenCoupling) == 0 {
		fmt.Println("No hidden coupling detected.")
		return
	}

	fmt.Println("Hidden coupling (co-change without a static link):")
	fmt.Println(strings.Repeat("─", 70))

	for _, p := range result.HiddenCoupling {
		fmt.Printf("[%s] %.0f%%  %d commits\n", strings.ToUpper(p.Level), p.Correlation*100, p.CoChangeCount)
		fmt.Printf("  %s\n", p.FileA)
		fmt.Printf("  %s\n", p.FileB)
		fmt.Printf("  → %s\n\n", p.Explanation)
	}
}

// ─── ckb perf structural ──────────────────────────────────────────────────────

var (
	perfStructuralWindowDays int
	perfStructuralMinChurn   int
	perfStructuralLimit      int
	perfStructuralScope      []string
	perfStructuralFormat     string
)

var perfStructuralCmd = &cobra.Command{
	Use:   "structural",
	Short: "Find loop call sites in hot files (O(n)/O(n²) risk)",
	Long: `Detect structural performance anti-patterns in high-churn files.

Uses tree-sitter to find call expressions inside loop bodies in frequently-
changed files. These are the primary signal for O(n) or O(n²) hidden costs
that do not appear in profiling until production load.

Requires a CGO-enabled build. Returns an empty result with noCGO=true otherwise.

Examples:
  ckb perf structural
  ckb perf structural --min-churn=5
  ckb perf structural --scope=internal/query --window=90
  ckb perf structural --format=json`,
	Run: runPerfStructural,
}

func init() {
	perfStructuralCmd.Flags().IntVar(&perfStructuralWindowDays, "window", 90, "Git history window in days for identifying hot files")
	perfStructuralCmd.Flags().IntVar(&perfStructuralMinChurn, "min-churn", 3, "Minimum commit count for a file to be considered hot")
	perfStructuralCmd.Flags().IntVar(&perfStructuralLimit, "limit", 100, "Maximum number of loop call sites to return")
	perfStructuralCmd.Flags().StringSliceVar(&perfStructuralScope, "scope", nil, "Limit scan to these paths (comma-separated or repeated flag)")
	perfStructuralCmd.Flags().StringVar(&perfStructuralFormat, "format", "human", "Output format: human or json")
	perfCmd.AddCommand(perfStructuralCmd)
}

func runPerfStructural(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(perfStructuralFormat)
	repoRoot := mustGetRepoRoot()

	analyzer := perf.NewAnalyzer(repoRoot, logger)

	ctx := context.Background()
	result, err := analyzer.AnalyzeStructural(ctx, perf.StructuralPerfOptions{
		Scope:         perfStructuralScope,
		WindowDays:    perfStructuralWindowDays,
		MinChurnCount: perfStructuralMinChurn,
		Limit:         perfStructuralLimit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error running structural perf scan: %v\n", err)
		os.Exit(1)
	}

	if OutputFormat(perfStructuralFormat) == FormatJSON {
		output, err := FormatResponse(result, FormatJSON)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(output)
		return
	}

	printPerfStructuralResult(result)

	logger.Debug("Structural perf scan completed",
		"callSites", len(result.LoopCallSites),
		"filesScanned", result.Summary.FilesScanned,
		"duration", time.Since(start).Milliseconds(),
	)
}

func printPerfStructuralResult(result *perf.StructuralPerfResult) {
	if result.NoCGO {
		fmt.Println("Structural perf scan requires a CGO-enabled build (tree-sitter).")
		fmt.Println("Rebuild with CGO_ENABLED=1 to enable loop call-site detection.")
		return
	}

	s := result.Summary
	fmt.Printf("Structural perf scan: %d files scanned (%d hot), %d loop call sites found\n\n",
		s.FilesScanned, s.HotFilesFound, s.CallSitesFound)

	if len(result.LoopCallSites) == 0 {
		fmt.Println("No loop call sites found in hot files.")
		return
	}

	fmt.Println("Loop call sites in hot files:")
	fmt.Println(strings.Repeat("─", 70))

	for _, cs := range result.LoopCallSites {
		ep := ""
		if cs.NearEntrypoint {
			ep = " [entrypoint]"
		}
		fmt.Printf("[%s]%s  %s:%d  (%d commits)\n",
			strings.ToUpper(cs.Severity), ep, cs.File, cs.Line, cs.ChurnCount)
		fmt.Printf("  fn: %s  loop: %s\n", cs.FunctionName, cs.LoopType)
		fmt.Printf("  call: %s\n", cs.CallText)
		fmt.Printf("  → %s\n\n", cs.Explanation)
	}
}
