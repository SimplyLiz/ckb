package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var (
	deadcodeFormat          string
	deadcodeScope           []string
	deadcodeLimit           int
	deadcodeMinConfidence   float64
	deadcodeExcludePatterns []string
	deadcodeIncludeTestOnly bool
	deadcodeUnexported      bool
)

var deadcodeCmd = &cobra.Command{
	Use:   "dead-code",
	Short: "Find unreferenced code using static analysis",
	Long: `Find dead code by analyzing the SCIP index for symbols with no references.

Detects:
- Exported symbols with zero references
- Symbols that only reference themselves (recursive but never called)
- Exported symbols only used within the same package
- Optionally: symbols only referenced from test files

Examples:
  ckb dead-code
  ckb dead-code --scope internal/legacy
  ckb dead-code --min-confidence 0.9
  ckb dead-code --unexported
  ckb dead-code --include-test-only
  ckb dead-code --exclude "*_generated.go"
  ckb dead-code --format human`,
	Run: runDeadCodeStatic,
}

func init() {
	deadcodeCmd.Flags().StringVar(&deadcodeFormat, "format", "human", "Output format (human, json)")
	deadcodeCmd.Flags().StringSliceVar(&deadcodeScope, "scope", nil, "Limit to specific packages/paths")
	deadcodeCmd.Flags().IntVar(&deadcodeLimit, "limit", 100, "Maximum results to return")
	deadcodeCmd.Flags().Float64Var(&deadcodeMinConfidence, "min-confidence", 0.7, "Minimum confidence threshold (0-1)")
	deadcodeCmd.Flags().StringSliceVar(&deadcodeExcludePatterns, "exclude", nil, "Patterns to exclude (can be repeated)")
	deadcodeCmd.Flags().BoolVar(&deadcodeIncludeTestOnly, "include-test-only", false, "Report symbols only referenced from tests as dead")
	deadcodeCmd.Flags().BoolVar(&deadcodeUnexported, "unexported", false, "Include unexported symbols")
	rootCmd.AddCommand(deadcodeCmd)
}

func runDeadCodeStatic(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(deadcodeFormat)

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	opts := query.FindDeadCodeOptions{
		Scope:             deadcodeScope,
		IncludeExported:   true,
		IncludeUnexported: deadcodeUnexported,
		MinConfidence:     deadcodeMinConfidence,
		ExcludePatterns:   deadcodeExcludePatterns,
		ExcludeTestOnly:   !deadcodeIncludeTestOnly,
		Limit:             deadcodeLimit,
	}

	response, err := engine.FindDeadCode(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding dead code: %v\n", err)
		os.Exit(1)
	}

	cliResponse := convertDeadCodeResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(deadcodeFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Dead code analysis completed",
		"deadCount", len(response.DeadCode),
		"duration", time.Since(start).Milliseconds(),
	)
}

// DeadCodeResponseCLI is the CLI response format for dead code detection.
type DeadCodeResponseCLI struct {
	// Analyzed is false when there was no index to look with; an empty
	// DeadCode then means "not checked", not "nothing dead".
	Analyzed          bool               `json:"analyzed"`
	NotAnalyzedReason string             `json:"notAnalyzedReason,omitempty"`
	DeadCode          []DeadCodeItemCLI  `json:"deadCode"`
	Summary           DeadCodeSummaryCLI `json:"summary"`
	Scope             []string           `json:"scope,omitempty"`
	Provenance        *ProvenanceCLI     `json:"provenance,omitempty"`
}

// DeadCodeItemCLI is a single dead code item in CLI format.
type DeadCodeItemCLI struct {
	SymbolName     string  `json:"symbolName"`
	Kind           string  `json:"kind"`
	FilePath       string  `json:"filePath"`
	LineNumber     int     `json:"lineNumber,omitempty"`
	Confidence     float64 `json:"confidence"`
	Reason         string  `json:"reason"`
	Category       string  `json:"category"`
	ReferenceCount int     `json:"referenceCount"`
	Exported       bool    `json:"exported"`
}

// DeadCodeSummaryCLI is the summary in CLI format.
type DeadCodeSummaryCLI struct {
	TotalSymbols    int            `json:"totalSymbols"`
	DeadCount       int            `json:"deadCount"`
	SuspiciousCount int            `json:"suspiciousCount"`
	ByKind          map[string]int `json:"byKind,omitempty"`
	ByCategory      map[string]int `json:"byCategory,omitempty"`
	EstimatedLines  int            `json:"estimatedLines"`
}

func convertDeadCodeResponse(resp *query.FindDeadCodeResponse) *DeadCodeResponseCLI {
	cli := &DeadCodeResponseCLI{
		Analyzed:          resp.Analyzed,
		NotAnalyzedReason: resp.NotAnalyzedReason,
		DeadCode:          make([]DeadCodeItemCLI, len(resp.DeadCode)),
		Summary: DeadCodeSummaryCLI{
			TotalSymbols:    resp.Summary.TotalSymbols,
			DeadCount:       resp.Summary.DeadCount,
			SuspiciousCount: resp.Summary.SuspiciousCount,
			ByKind:          resp.Summary.ByKind,
			ByCategory:      resp.Summary.ByCategory,
			EstimatedLines:  resp.Summary.EstimatedLines,
		},
		Scope: resp.Scope,
	}

	for i, item := range resp.DeadCode {
		cli.DeadCode[i] = DeadCodeItemCLI{
			SymbolName:     item.SymbolName,
			Kind:           item.Kind,
			FilePath:       item.FilePath,
			LineNumber:     item.LineNumber,
			Confidence:     item.Confidence,
			Reason:         item.Reason,
			Category:       item.Category,
			ReferenceCount: item.ReferenceCount,
			Exported:       item.Exported,
		}
	}

	if resp.Provenance != nil {
		cli.Provenance = &ProvenanceCLI{
			RepoStateId:     resp.Provenance.RepoStateId,
			RepoStateDirty:  resp.Provenance.RepoStateDirty,
			QueryDurationMs: resp.Provenance.QueryDurationMs,
		}
	}

	return cli
}

// formatDeadCodeHuman formats dead code response for human reading.
func formatDeadCodeHuman(resp *DeadCodeResponseCLI) string {
	var sb strings.Builder

	sb.WriteString("Dead Code Analysis\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━\n\n")

	if !resp.Analyzed {
		reason := resp.NotAnalyzedReason
		if reason == "" {
			reason = "no SCIP index; run 'ckb index'"
		}
		sb.WriteString("Not analyzed: " + reason + "\n")
		return sb.String()
	}

	if len(resp.DeadCode) == 0 {
		sb.WriteString("No dead code found.\n")
		sb.WriteString(fmt.Sprintf("\nAnalyzed %d symbols.\n", resp.Summary.TotalSymbols))
		return sb.String()
	}

	// Group by confidence level
	var highConfidence, mediumConfidence []DeadCodeItemCLI
	for _, item := range resp.DeadCode {
		if item.Confidence >= 0.9 {
			highConfidence = append(highConfidence, item)
		} else {
			mediumConfidence = append(mediumConfidence, item)
		}
	}

	// High confidence (definitely dead)
	if len(highConfidence) > 0 {
		sb.WriteString(fmt.Sprintf("Definitely Dead (%d items, 90%%+ confidence):\n\n", len(highConfidence)))
		for _, item := range highConfidence {
			sb.WriteString(fmt.Sprintf("  ✗ %s %s\n", item.Kind, item.SymbolName))
			sb.WriteString(fmt.Sprintf("    %s", item.FilePath))
			if item.LineNumber > 0 {
				sb.WriteString(fmt.Sprintf(":%d", item.LineNumber))
			}
			sb.WriteString("\n")
			sb.WriteString(fmt.Sprintf("    Reason: %s\n", item.Reason))
			sb.WriteString("\n")
		}
	}

	// Medium confidence (possibly dead)
	if len(mediumConfidence) > 0 {
		sb.WriteString(fmt.Sprintf("Possibly Dead (%d items, 70%%+ confidence):\n\n", len(mediumConfidence)))
		for _, item := range mediumConfidence {
			sb.WriteString(fmt.Sprintf("  ? %s %s\n", item.Kind, item.SymbolName))
			sb.WriteString(fmt.Sprintf("    %s", item.FilePath))
			if item.LineNumber > 0 {
				sb.WriteString(fmt.Sprintf(":%d", item.LineNumber))
			}
			sb.WriteString("\n")
			sb.WriteString(fmt.Sprintf("    Reason: %s\n", item.Reason))
			sb.WriteString(fmt.Sprintf("    Confidence: %.0f%%\n", item.Confidence*100))
			sb.WriteString("\n")
		}
	}

	// Summary
	sb.WriteString("Summary:\n")
	sb.WriteString("━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("  Total symbols analyzed: %d\n", resp.Summary.TotalSymbols))
	sb.WriteString(fmt.Sprintf("  Definitely dead: %d (90%%+ confidence)\n", resp.Summary.DeadCount))
	sb.WriteString(fmt.Sprintf("  Possibly dead: %d\n", resp.Summary.SuspiciousCount))
	sb.WriteString(fmt.Sprintf("  Estimated removable: ~%d lines\n", resp.Summary.EstimatedLines))

	if len(resp.Summary.ByKind) > 0 {
		sb.WriteString("\n  By kind:\n")
		for kind, count := range resp.Summary.ByKind {
			sb.WriteString(fmt.Sprintf("    %s: %d\n", kind, count))
		}
	}

	sb.WriteString("\nRun `ckb dead-code --format json` for machine-readable output.\n")

	return sb.String()
}
