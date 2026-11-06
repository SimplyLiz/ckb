package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/query"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

var (
	changesStaged  bool
	changesBase    string
	changesRepo    string
	changesVerbose bool
	changesFormat  string
	changesCheck   bool
)

// Exit codes of ckb changes --check. 1 stays what it is everywhere else: an error.
const (
	changesExitClear       = 0
	changesExitOpen        = 2
	changesExitNotAssessed = 3
)

var changesCmd = &cobra.Command{
	Use:   "changes",
	Short: "Assess what you actually changed and what to verify before finishing",
	Long: `The post-change counterpart to prepareChange: analyzes the current change
set and answers what might break, which tests should run, who should
review, and whether any public/exported symbols look like they may have
changed shape.

Defaults to the uncommitted working tree. Use --staged to look at the
index instead, or --base to compare against a branch/ref.

This is the same analysis as 'ckb impact diff' under a new name; 'ckb
impact diff' remains available as an alias.

Examples:
  ckb changes                    # Assess uncommitted working-tree changes
  ckb changes --staged           # Assess only staged changes
  ckb changes --base=main        # Compare against main branch
  ckb changes --verbose          # Show full lists instead of capped summaries
  ckb changes --repo=my-service  # Assess changes in a registered repo
  ckb changes --format=json
  ckb changes --check            # Exit 0 clear, 2 open uses, 3 not assessed, 1 error

--check is a text comparison and compiles nothing. "clear" means no use of a
changed declaration was left untouched, not that the change is correct.`,
	Run: runChanges,
}

func init() {
	changesCmd.Flags().BoolVar(&changesStaged, "staged", false, "Analyze only staged changes (--cached)")
	changesCmd.Flags().StringVar(&changesBase, "base", "", "Base ref/branch for comparison (default: working tree against HEAD)")
	changesCmd.Flags().StringVar(&changesRepo, "repo", "", "Repository path or registry name (auto-detected)")
	changesCmd.Flags().BoolVar(&changesVerbose, "verbose", false, "Show full lists instead of capped summaries")
	changesCmd.Flags().StringVar(&changesFormat, "format", "human", "Output format (human, json)")
	changesCmd.Flags().BoolVar(&changesCheck, "check", false, "Set the exit code from the done check: 0 clear, 2 open, 3 not assessed")
	rootCmd.AddCommand(changesCmd)
}

func runChanges(cmd *cobra.Command, args []string) {
	logger := newLogger(changesFormat)

	repoRoot := resolveChangesRepoRoot(changesRepo)
	engine := mustGetEngine(repoRoot, logger)
	ctx := newContext()

	resp, err := engine.AnalyzeChangeSet(ctx, query.AnalyzeChangeSetOptions{
		Staged:     changesStaged,
		BaseBranch: changesBase,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error assessing changes: %v\n", err)
		os.Exit(1)
	}

	cliResp := convertChangeSetResponse(resp)

	if changesFormat == "human" {
		fmt.Println(formatChangesHuman(cliResp, changesVerbose))
	} else {
		out, err := FormatResponse(cliResp, OutputFormat(changesFormat))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(out)
	}

	if changesCheck {
		os.Exit(doneCheckExitCode(resp.DoneCheck))
	}
}

// doneCheckExitCode maps a done check to the exit code of --check. Only an earned
// "clear" is 0, so a script can't take a missing answer for a good one.
func doneCheckExitCode(check *query.DoneCheck) int {
	switch {
	case check == nil:
		return changesExitNotAssessed
	case check.Status == query.DoneCheckClear:
		return changesExitClear
	case check.Status == query.DoneCheckOpen:
		return changesExitOpen
	}
	return changesExitNotAssessed
}

// resolveChangesRepoRoot resolves --repo the same way `ckb serve --repo` /
// `ckb mcp --repo` do: a filesystem path, or a name in the repo registry.
// Falls back to the current directory's registered/auto-detected repo when
// --repo is not given.
func resolveChangesRepoRoot(repo string) string {
	if repo == "" {
		return mustGetRepoRoot()
	}
	if isRepoPath(repo) {
		return repo
	}
	registry, err := repos.LoadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to load repo registry: %v\n", err)
		os.Exit(1)
	}
	entry, state, err := registry.Get(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: repository '%s' not found in registry\n", repo)
		os.Exit(1)
	}
	if state != repos.RepoStateValid {
		fmt.Fprintf(os.Stderr, "Error: repository '%s' is %s\n", repo, state)
		os.Exit(1)
	}
	return entry.Path
}
