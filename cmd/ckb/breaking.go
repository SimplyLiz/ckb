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
	breakingBaseRef      string
	breakingTargetRef    string
	breakingFormat       string
	breakingScope        []string
	breakingIncludeMinor bool
)

var breakingCmd = &cobra.Command{
	Use:   "breaking-changes",
	Short: "Detect breaking API changes between git refs",
	Long: `Compare API surfaces between two git refs to detect breaking changes.

Detects:
- Removed public symbols
- Signature changes (function/method parameters, return types)
- Type definition changes
- Visibility changes (exported to unexported)
- Renamed symbols
- Deprecated symbols

Examples:
  ckb breaking-changes                           # Compare HEAD~1 to HEAD
  ckb breaking-changes --base=v1.0.0 --target=v2.0.0
  ckb breaking-changes --base=main --target=HEAD
  ckb breaking-changes --scope=pkg/api
  ckb breaking-changes --include-minor           # Include non-breaking changes
  ckb breaking-changes --format=json`,
	Run: runBreakingChanges,
}

func init() {
	breakingCmd.Flags().StringVar(&breakingBaseRef, "base", "HEAD~1", "Base git ref for comparison")
	breakingCmd.Flags().StringVar(&breakingTargetRef, "target", "HEAD", "Target git ref for comparison")
	breakingCmd.Flags().StringVar(&breakingFormat, "format", "human", "Output format (human, json)")
	breakingCmd.Flags().StringSliceVar(&breakingScope, "scope", nil, "Limit to specific packages/paths")
	breakingCmd.Flags().BoolVar(&breakingIncludeMinor, "include-minor", false, "Include non-breaking changes")

	rootCmd.AddCommand(breakingCmd)
}

func runBreakingChanges(cmd *cobra.Command, args []string) {
	start := time.Now()
	logger := newLogger(breakingFormat)

	repoRoot := mustGetRepoRoot()
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	opts := query.CompareAPIOptions{
		BaseRef:       breakingBaseRef,
		TargetRef:     breakingTargetRef,
		Scope:         breakingScope,
		IncludeMinor:  breakingIncludeMinor,
		IgnorePrivate: true,
	}

	response, err := engine.CompareAPI(ctx, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error analyzing breaking changes: %v\n", err)
		os.Exit(1)
	}

	// Convert to CLI format
	cliResponse := convertBreakingResponse(response)

	output, err := FormatResponse(cliResponse, OutputFormat(breakingFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(output)

	logger.Debug("Breaking changes analysis completed",
		"changesCount", len(response.Changes),
		"duration", time.Since(start).Milliseconds(),
	)

	// Exit with code 1 if breaking changes found (for CI)
	if response.Summary != nil && response.Summary.BreakingChanges > 0 {
		os.Exit(1)
	}
}

// BreakingResponseCLI is the CLI response format for breaking changes
type BreakingResponseCLI struct {
	BaseRef      string              `json:"baseRef"`
	TargetRef    string              `json:"targetRef"`
	Changes      []BreakingChangeCLI `json:"changes"`
	Summary      *BreakingSummaryCLI `json:"summary"`
	SemverAdvice string              `json:"semverAdvice,omitempty"`
	NotCompared  []string            `json:"notCompared,omitempty"`
	Provenance   *ProvenanceCLI      `json:"provenance,omitempty"`
}

// BreakingChangeCLI represents a single breaking change
type BreakingChangeCLI struct {
	Kind         string `json:"kind"`
	Severity     string `json:"severity"`
	SymbolName   string `json:"symbolName"`
	SymbolKind   string `json:"symbolKind"`
	Package      string `json:"package"`
	FilePath     string `json:"filePath"`
	LineNumber   int    `json:"lineNumber,omitempty"`
	Description  string `json:"description"`
	OldValue     string `json:"oldValue,omitempty"`
	NewValue     string `json:"newValue,omitempty"`
	Suggestion   string `json:"suggestion,omitempty"`
	AffectsUsers bool   `json:"affectsUsers"`
}

// BreakingSummaryCLI provides an overview of changes
type BreakingSummaryCLI struct {
	TotalChanges    int            `json:"totalChanges"`
	BreakingChanges int            `json:"breakingChanges"`
	Warnings        int            `json:"warnings"`
	Additions       int            `json:"additions"`
	ByKind          map[string]int `json:"byKind"`
	ByPackage       map[string]int `json:"byPackage,omitempty"`
}

func convertBreakingResponse(resp *query.CompareAPIResponse) *BreakingResponseCLI {
	cli := &BreakingResponseCLI{
		BaseRef:      resp.BaseRef,
		TargetRef:    resp.TargetRef,
		SemverAdvice: resp.SemverAdvice,
		NotCompared:  resp.NotCompared,
	}

	// Convert changes
	cli.Changes = make([]BreakingChangeCLI, len(resp.Changes))
	for i, c := range resp.Changes {
		cli.Changes[i] = BreakingChangeCLI{
			Kind:         c.Kind,
			Severity:     c.Severity,
			SymbolName:   c.SymbolName,
			SymbolKind:   c.SymbolKind,
			Package:      c.Package,
			FilePath:     c.FilePath,
			LineNumber:   c.LineNumber,
			Description:  c.Description,
			OldValue:     c.OldValue,
			NewValue:     c.NewValue,
			Suggestion:   c.Suggestion,
			AffectsUsers: c.AffectsUsers,
		}
	}

	// Convert summary
	if resp.Summary != nil {
		cli.Summary = &BreakingSummaryCLI{
			TotalChanges:    resp.Summary.TotalChanges,
			BreakingChanges: resp.Summary.BreakingChanges,
			Warnings:        resp.Summary.Warnings,
			Additions:       resp.Summary.Additions,
			ByKind:          resp.Summary.ByKind,
			ByPackage:       resp.Summary.ByPackage,
		}
	}

	// Convert provenance
	if resp.Provenance != nil {
		cli.Provenance = &ProvenanceCLI{
			RepoStateId:     resp.Provenance.RepoStateId,
			RepoStateDirty:  resp.Provenance.RepoStateDirty,
			QueryDurationMs: resp.Provenance.QueryDurationMs,
		}
	}

	return cli
}

// formatBreakingHuman formats breaking changes for human reading
func formatBreakingHuman(resp *BreakingResponseCLI) string {
	var sb strings.Builder

	sb.WriteString("Breaking Change Analysis\n")
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	sb.WriteString(fmt.Sprintf("Comparing: %s → %s\n\n", resp.BaseRef, resp.TargetRef))

	if len(resp.Changes) == 0 {
		if len(resp.NotCompared) > 0 {
			sb.WriteString(fmt.Sprintf("Not assessed: public declarations changed in %d file(s) whose language isn't compared:\n", len(resp.NotCompared)))
			for _, f := range resp.NotCompared {
				sb.WriteString("  " + f + "\n")
			}
		} else {
			sb.WriteString("No API changes detected.\n")
		}
		return sb.String()
	}

	// Group by severity
	var breaking, warnings, additions []BreakingChangeCLI
	for _, c := range resp.Changes {
		switch c.Severity {
		case "breaking":
			breaking = append(breaking, c)
		case "warning":
			warnings = append(warnings, c)
		case "non_breaking":
			additions = append(additions, c)
		}
	}

	// Breaking changes
	if len(breaking) > 0 {
		sb.WriteString(fmt.Sprintf("Breaking Changes (%d):\n\n", len(breaking)))
		for _, c := range breaking {
			sb.WriteString(fmt.Sprintf("  ✗ [%s] %s %s\n", c.Kind, c.SymbolKind, c.SymbolName))
			sb.WriteString(fmt.Sprintf("    %s\n", c.Description))
			if c.FilePath != "" {
				if c.LineNumber > 0 {
					sb.WriteString(fmt.Sprintf("    Location: %s:%d\n", c.FilePath, c.LineNumber))
				} else {
					sb.WriteString(fmt.Sprintf("    Location: %s\n", c.FilePath))
				}
			}
			if c.OldValue != "" && c.NewValue != "" {
				sb.WriteString(fmt.Sprintf("    Before: %s\n", c.OldValue))
				sb.WriteString(fmt.Sprintf("    After:  %s\n", c.NewValue))
			}
			sb.WriteString("\n")
		}
	}

	// Warnings
	if len(warnings) > 0 {
		sb.WriteString(fmt.Sprintf("Warnings (%d):\n\n", len(warnings)))
		for _, c := range warnings {
			sb.WriteString(fmt.Sprintf("  ⚠ [%s] %s %s\n", c.Kind, c.SymbolKind, c.SymbolName))
			sb.WriteString(fmt.Sprintf("    %s\n", c.Description))
			sb.WriteString("\n")
		}
	}

	if len(resp.NotCompared) > 0 {
		sb.WriteString(fmt.Sprintf("Not assessed (%d file(s), language not compared):\n", len(resp.NotCompared)))
		for _, f := range resp.NotCompared {
			sb.WriteString("  " + f + "\n")
		}
		sb.WriteString("\n")
	}

	// Additions (only if include-minor)
	if len(additions) > 0 {
		sb.WriteString(fmt.Sprintf("Additions (%d):\n\n", len(additions)))
		for _, c := range additions {
			sb.WriteString(fmt.Sprintf("  + [%s] %s %s\n", c.Kind, c.SymbolKind, c.SymbolName))
			sb.WriteString("\n")
		}
	}

	// Summary
	sb.WriteString("Summary:\n")
	sb.WriteString("━━━━━━━\n")
	if resp.Summary != nil {
		sb.WriteString(fmt.Sprintf("  Total changes: %d\n", resp.Summary.TotalChanges))
		sb.WriteString(fmt.Sprintf("  Breaking: %d\n", resp.Summary.BreakingChanges))
		sb.WriteString(fmt.Sprintf("  Warnings: %d\n", resp.Summary.Warnings))
		sb.WriteString(fmt.Sprintf("  Additions: %d\n", resp.Summary.Additions))
	}
	if resp.SemverAdvice != "" {
		sb.WriteString(fmt.Sprintf("\nRecommended version bump: %s\n", strings.ToUpper(resp.SemverAdvice)))
	}

	return sb.String()
}
