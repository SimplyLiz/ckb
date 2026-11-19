package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"

	"github.com/spf13/cobra"
)

var (
	refreshFormat   string
	refreshVerbose  bool
	refreshSkipTest bool
)

var refreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Regenerate the SCIP index",
	Long: `Regenerate the SCIP index for the current repository.

This command runs scip-go to create a fresh index of all symbols, references,
and relationships in your Go codebase. The index is stored at index.scip
by default.

After refreshing, all CKB queries will use the updated index.`,
	RunE: runRefresh,
}

func init() {
	refreshCmd.Flags().StringVar(&refreshFormat, "format", "human", "Output format (human, json)")
	refreshCmd.Flags().BoolVarP(&refreshVerbose, "verbose", "v", false, "Show verbose output from scip-go")
	refreshCmd.Flags().BoolVar(&refreshSkipTest, "skip-tests", false, "Skip indexing test files")
	rootCmd.AddCommand(refreshCmd)
}

// RefreshResult contains the result of a refresh operation
type RefreshResult struct {
	Success       bool      `json:"success"`
	IndexPath     string    `json:"indexPath"`
	Duration      int64     `json:"durationMs"`
	FilesIndexed  int       `json:"filesIndexed"`
	SymbolsCount  int       `json:"symbolsCount"`
	IndexSize     int64     `json:"indexSizeBytes"`
	PreviousState string    `json:"previousState,omitempty"`
	NewState      string    `json:"newState"`
	Error         string    `json:"error,omitempty"`
	Warnings      []string  `json:"warnings,omitempty"`
	RefreshedAt   time.Time `json:"refreshedAt"`
}

func runRefresh(cmd *cobra.Command, args []string) error {
	start := time.Now()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	repoRoot := mustGetRepoRoot()

	// Load config to get index path
	cfg, err := config.LoadConfig(repoRoot)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	indexPath := scip.GetIndexPath(repoRoot, cfg.Backends.Scip.IndexPath)
	indexDir := filepath.Dir(indexPath)

	// Get current repo state for comparison
	var previousCommit string
	rs, err := repostate.ComputeRepoState(repoRoot)
	if err == nil {
		previousCommit = rs.HeadCommit
	}

	// Check for existing index stats
	var previousFiles, previousSymbols int
	if existingIndex, loadErr := scip.LoadSCIPIndex(indexPath); loadErr == nil {
		previousFiles = len(existingIndex.Documents)
		previousSymbols = len(existingIndex.Symbols)
	}

	// Ensure index directory exists
	if mkdirErr := os.MkdirAll(indexDir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create index directory %s: %w", indexDir, mkdirErr)
	}

	// Find scip-go
	scipGoPath, err := findScipGo()
	if err != nil {
		result := &RefreshResult{
			Success:     false,
			Error:       err.Error(),
			RefreshedAt: time.Now(),
		}
		return outputRefreshResult(result, refreshFormat, logger)
	}

	// Build scip-go command
	cmdArgs := []string{
		"--output=" + indexPath,
		"--project-root=" + repoRoot,
		"--module-root=" + repoRoot,
		"--repository-root=" + repoRoot,
	}

	// Add module version (git commit) for tracking
	if previousCommit != "" {
		cmdArgs = append(cmdArgs, "--module-version="+previousCommit[:12])
	}

	if refreshSkipTest {
		cmdArgs = append(cmdArgs, "--skip-tests")
	}

	if refreshVerbose {
		cmdArgs = append(cmdArgs, "--verbose")
	} else {
		cmdArgs = append(cmdArgs, "--quiet")
	}

	// Add package pattern to index all packages
	cmdArgs = append(cmdArgs, "./...")

	logger.Info("Running scip-go indexer",
		"command", scipGoPath,
		"args", strings.Join(cmdArgs, " "),
	)

	if refreshFormat == "human" {
		fmt.Println("Indexing codebase with scip-go...")
	}

	// Run scip-go
	indexCmd := exec.Command(scipGoPath, cmdArgs...) // #nosec G204 //nolint:gosec // scipGoPath from exec.LookPath
	indexCmd.Dir = repoRoot

	var output []byte
	if refreshVerbose {
		indexCmd.Stdout = os.Stdout
		indexCmd.Stderr = os.Stderr
		err = indexCmd.Run()
	} else {
		output, err = indexCmd.CombinedOutput()
	}

	duration := time.Since(start)

	if err != nil {
		result := &RefreshResult{
			Success:     false,
			IndexPath:   indexPath,
			Duration:    duration.Milliseconds(),
			Error:       fmt.Sprintf("scip-go failed: %v\n%s", err, string(output)),
			RefreshedAt: time.Now(),
		}
		return outputRefreshResult(result, refreshFormat, logger)
	}

	// Load the new index to get stats
	newIndex, err := scip.LoadSCIPIndex(indexPath)
	if err != nil {
		result := &RefreshResult{
			Success:     false,
			IndexPath:   indexPath,
			Duration:    duration.Milliseconds(),
			Error:       fmt.Sprintf("index created but failed to load: %v", err),
			RefreshedAt: time.Now(),
		}
		return outputRefreshResult(result, refreshFormat, logger)
	}

	// Get index file size
	var indexSize int64
	if info, statErr := os.Stat(indexPath); statErr == nil {
		indexSize = info.Size()
	}

	// Build warnings
	var warnings []string
	if rs != nil && rs.Dirty {
		warnings = append(warnings, "Repository has uncommitted changes - index may not reflect all changes")
	}

	// Compute state description
	var previousState string
	if previousFiles > 0 {
		previousState = fmt.Sprintf("%d files, %d symbols", previousFiles, previousSymbols)
	}

	result := &RefreshResult{
		Success:       true,
		IndexPath:     indexPath,
		Duration:      duration.Milliseconds(),
		FilesIndexed:  len(newIndex.Documents),
		SymbolsCount:  len(newIndex.Symbols),
		IndexSize:     indexSize,
		PreviousState: previousState,
		NewState:      fmt.Sprintf("%d files, %d symbols", len(newIndex.Documents), len(newIndex.Symbols)),
		Warnings:      warnings,
		RefreshedAt:   time.Now(),
	}

	// Update index metadata so freshness check stays in sync
	ckbDir := filepath.Join(repoRoot, ".ckb")
	meta := &index.IndexMeta{
		CreatedAt: time.Now(),
		FileCount: result.FilesIndexed,
		Duration:  fmt.Sprintf("%dms", result.Duration),
		Indexer:   "scip-go",
	}
	if rs != nil {
		meta.CommitHash = rs.HeadCommit
		meta.RepoStateID = rs.RepoStateID
		meta.RecordWorktree(repoRoot, rs.Dirty)
	}
	if saveErr := meta.Save(ckbDir); saveErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: Could not save index metadata: %v\n", saveErr)
	}

	return outputRefreshResult(result, refreshFormat, logger)
}

func outputRefreshResult(result *RefreshResult, format string, logger *slog.Logger) error {
	if format == "json" {
		output, err := FormatResponse(result, FormatJSON)
		if err != nil {
			return err
		}
		fmt.Println(output)
		return nil
	}

	// Human-readable output
	if result.Success {
		fmt.Println("\n✓ SCIP index refreshed successfully")
		fmt.Printf("  Index path: %s\n", result.IndexPath)
		fmt.Printf("  Duration: %dms\n", result.Duration)
		fmt.Printf("  Files indexed: %d\n", result.FilesIndexed)
		fmt.Printf("  Symbols indexed: %d\n", result.SymbolsCount)
		fmt.Printf("  Index size: %s\n", formatBytes(result.IndexSize))

		if result.PreviousState != "" {
			fmt.Printf("  Previous: %s\n", result.PreviousState)
		}

		for _, w := range result.Warnings {
			fmt.Printf("\n⚠ Warning: %s\n", w)
		}

		fmt.Println("\nYou can now use CKB tools with the refreshed index.")
	} else {
		fmt.Println("\n✗ Index refresh failed")
		fmt.Printf("  Error: %s\n", result.Error)
		fmt.Println("\nTroubleshooting:")
		fmt.Println("  1. Ensure scip-go is installed: go install github.com/sourcegraph/scip-go@latest")
		fmt.Println("  2. Ensure your Go code compiles: go build ./...")
		fmt.Println("  3. Run with --verbose for more details")
		return fmt.Errorf("refresh failed: %s", result.Error)
	}

	return nil // #nosec G703 -- paths are internally constructed from repo root
}

func findScipGo() (string, error) {
	// Check common locations
	locations := []string{
		"scip-go", // In PATH
		filepath.Join(os.Getenv("HOME"), "go/bin/scip-go"), // Go default
		filepath.Join(os.Getenv("GOPATH"), "bin/scip-go"),  // GOPATH
	}

	for _, loc := range locations {
		if path, err := exec.LookPath(loc); err == nil {
			return path, nil
		}
		// Check if it exists directly (for absolute paths)
		if filepath.IsAbs(loc) {
			if _, err := os.Stat(loc); err == nil {
				return loc, nil
			}
		}
	}

	return "", fmt.Errorf("scip-go not found. Install with: go install github.com/sourcegraph/scip-go@latest")
}
