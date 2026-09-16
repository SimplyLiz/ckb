package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

var (
	activityLast     int
	activitySession  bool
	activityConsumer string
	activityTool     string
	activitySince    string
	activitySummary  bool
	activityAll      bool
	activityJSON     bool
)

var activityCmd = &cobra.Command{
	Use:   "activity",
	Short: "Show the MCP tool-call activity ledger",
	Long: `Show recent MCP tool calls recorded by CKB's activity ledger.

Every MCP tool invocation (from Claude Code, Cursor, or any other MCP
client) is logged locally to .ckb/ckb.db: tool name, target, timing, size,
truncation, errors, and — for a handful of tools — honest structured facts
read straight out of that tool's own response. Nothing here is inferred or
narrated; it's a record of calls, not intent.

Examples:
  ckb activity                  # last 20 calls in this repo, human format
  ckb activity --last 100
  ckb activity --session        # current CLAUDE_CODE_SESSION_ID only
  ckb activity --consumer claude-code
  ckb activity --tool prepareChange
  ckb activity --since 2h
  ckb activity --json
  ckb activity --summary        # per-tool: calls, p50/p95 ms, avg bytes, error/truncation rate
  ckb activity --all            # across every registered repo`,
	Run: runActivity,
}

func init() {
	activityCmd.Flags().IntVar(&activityLast, "last", 20, "Number of most recent calls to show")
	activityCmd.Flags().BoolVar(&activitySession, "session", false, "Filter to the current CLAUDE_CODE_SESSION_ID")
	activityCmd.Flags().StringVar(&activityConsumer, "consumer", "", "Filter by consumer (e.g. claude-code)")
	activityCmd.Flags().StringVar(&activityTool, "tool", "", "Filter by tool name")
	activityCmd.Flags().StringVar(&activitySince, "since", "", "Only show calls since this long ago (e.g. 2h, 30m, 7d)")
	activityCmd.Flags().BoolVar(&activitySummary, "summary", false, "Show a per-tool summary instead of a call list")
	activityCmd.Flags().BoolVar(&activityAll, "all", false, "Show activity across every registered repo")
	activityCmd.Flags().BoolVar(&activityJSON, "json", false, "Output as JSON")
	rootCmd.AddCommand(activityCmd)
}

func runActivity(cmd *cobra.Command, args []string) {
	sinceMs, err := parseActivitySince(activitySince)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	sessionID := ""
	if activitySession {
		sessionID = os.Getenv("CLAUDE_CODE_SESSION_ID")
		if sessionID == "" {
			fmt.Fprintln(os.Stderr, "Error: --session requires CLAUDE_CODE_SESSION_ID to be set in the environment")
			os.Exit(1)
		}
	}

	filter := storage.ToolCallFilter{
		Since:     sinceMs,
		SessionID: sessionID,
		Consumer:  activityConsumer,
		Tool:      activityTool,
		Limit:     activityLast,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	if activityAll {
		runActivityAllRepos(logger, filter, sinceMs)
		return
	}

	repoRoot := mustGetRepoRoot()
	db, err := storage.Open(repoRoot, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	repoName := repoDisplayName(repoRoot)
	report, err := buildActivityReport(db, repoName, filter, sinceMs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	printActivityReports([]*ActivityReportCLI{report})
}

func runActivityAllRepos(logger *slog.Logger, filter storage.ToolCallFilter, sinceMs int64) {
	registry, err := repos.LoadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading repo registry: %v\n", err)
		os.Exit(1)
	}

	entries := registry.List()
	reports := make([]*ActivityReportCLI, 0, len(entries))

	for _, e := range entries {
		if registry.ValidateState(e.Name) != repos.RepoStateValid {
			continue
		}

		db, err := storage.Open(e.Path, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to open %s: %v\n", e.Name, err)
			continue
		}

		report, err := buildActivityReport(db, e.Name, filter, sinceMs)
		_ = db.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read activity for %s: %v\n", e.Name, err)
			continue
		}
		reports = append(reports, report)
	}

	printActivityReports(reports)
}

// repoDisplayName returns the last path component of repoRoot, for labeling
// output when --all is used.
func repoDisplayName(repoRoot string) string {
	clean := strings.TrimRight(repoRoot, "/")
	if idx := strings.LastIndex(clean, "/"); idx >= 0 && idx+1 < len(clean) {
		return clean[idx+1:]
	}
	return clean
}

// ActivityReportCLI holds either a call list or a summary for one repo.
type ActivityReportCLI struct {
	Repo    string                    `json:"repo"`
	Summary bool                      `json:"summary"`
	Calls   []ActivityCallCLI         `json:"calls,omitempty"`
	Tools   []storage.ToolCallSummary `json:"tools,omitempty"`
}

// ActivityCallCLI is one recorded tool call, formatted for CLI output.
type ActivityCallCLI struct {
	Time          string         `json:"time"`
	TsMs          int64          `json:"tsMs"`
	SessionID     string         `json:"sessionId,omitempty"`
	Consumer      string         `json:"consumer,omitempty"`
	Tool          string         `json:"tool"`
	Target        string         `json:"target,omitempty"`
	DurationMs    int64          `json:"durationMs"`
	ResponseBytes int64          `json:"responseBytes"`
	Truncated     bool           `json:"truncated"`
	Error         string         `json:"error,omitempty"`
	Facts         map[string]int `json:"facts,omitempty"`
}

func buildActivityReport(db *storage.DB, repoName string, filter storage.ToolCallFilter, sinceMs int64) (*ActivityReportCLI, error) {
	report := &ActivityReportCLI{Repo: repoName, Summary: activitySummary}

	if activitySummary {
		summaries, err := db.SummarizeToolCalls(sinceMs)
		if err != nil {
			return nil, fmt.Errorf("failed to summarize activity: %w", err)
		}
		report.Tools = summaries
		return report, nil
	}

	calls, err := db.ListToolCalls(filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list activity: %w", err)
	}

	report.Calls = make([]ActivityCallCLI, 0, len(calls))
	for _, c := range calls {
		cli := ActivityCallCLI{
			Time:          time.UnixMilli(c.Ts).Local().Format("15:04:05"),
			TsMs:          c.Ts,
			SessionID:     c.SessionID,
			Consumer:      c.Consumer,
			Tool:          c.Tool,
			Target:        c.Target,
			DurationMs:    c.DurationMs,
			ResponseBytes: c.ResponseBytes,
			Truncated:     c.Truncated,
			Error:         c.Error,
		}
		if c.Facts != "" {
			var facts map[string]int
			if err := json.Unmarshal([]byte(c.Facts), &facts); err == nil {
				cli.Facts = facts
			}
		}
		report.Calls = append(report.Calls, cli)
	}

	return report, nil
}

func printActivityReports(reports []*ActivityReportCLI) {
	if activityJSON {
		var out interface{} = reports
		if len(reports) == 1 && !activityAll {
			out = reports[0]
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	for i, r := range reports {
		if i > 0 {
			fmt.Println()
		}
		if activityAll {
			fmt.Printf("== %s ==\n", r.Repo)
		}
		if activitySummary {
			printActivitySummaryHuman(r.Tools)
		} else {
			printActivityCallsHuman(r.Calls)
		}
	}
}

func printActivityCallsHuman(calls []ActivityCallCLI) {
	if len(calls) == 0 {
		fmt.Println("No activity recorded.")
		return
	}

	for _, c := range calls {
		target := c.Target
		if target == "" {
			target = "-"
		}
		fmt.Printf("%s  %-12s %-16s %s\n", c.Time, displayOrDash(c.Consumer), c.Tool, target)

		if len(c.Facts) > 0 {
			fmt.Printf("          %s\n", formatFacts(c.Facts))
		}

		if c.Error != "" {
			fmt.Printf("          ✗ %s\n", c.Error)
		}

		sizeStr := formatActivityBytes(c.ResponseBytes)
		truncStr := ""
		if c.Truncated {
			truncStr = " (truncated)"
		}
		fmt.Printf("          %s · %d ms%s\n", sizeStr, c.DurationMs, truncStr)
	}
}

func displayOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// formatFacts renders a facts map deterministically (sorted by key) as
// "key value · key value · ...".
func formatFacts(facts map[string]int) string {
	keys := make([]string, 0, len(facts))
	for k := range facts {
		keys = append(keys, k)
	}
	// Simple insertion sort — facts maps are tiny (a handful of keys).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, facts[k]))
	}
	return strings.Join(parts, " · ")
}

// formatActivityBytes renders a byte count as "B" or "KB" (matching the
// ckb activity human-format spec: "6.4 KB", not the "KiB" suffix
// cmd/ckb/format.go's formatBytes uses elsewhere).
func formatActivityBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
}

func printActivitySummaryHuman(tools []storage.ToolCallSummary) {
	if len(tools) == 0 {
		fmt.Println("No activity recorded.")
		return
	}

	fmt.Printf("%-24s %8s %8s %8s %10s %8s %10s\n", "TOOL", "CALLS", "P50 MS", "P95 MS", "AVG KB", "ERRORS", "TRUNCATED")
	for _, t := range tools {
		fmt.Printf("%-24s %8d %8.0f %8.0f %10.1f %8d %10d\n",
			t.Tool, t.Calls, t.P50DurationMs, t.P95DurationMs, t.AvgBytes/1024.0, t.Errors, t.Truncated)
	}
}

// parseActivitySince parses a duration string like "2h", "30m", or "7d" and
// returns the corresponding unix-ms cutoff (now - duration). Returns 0 (no
// lower bound) for an empty string.
func parseActivitySince(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	d, err := parseDurationWithDays(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --since value %q: %w", s, err)
	}

	return time.Now().Add(-d).UnixMilli(), nil
}

// parseDurationWithDays extends time.ParseDuration with a "d" (days) unit,
// since the standard library doesn't support it.
func parseDurationWithDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		days, err := strconv.ParseFloat(numStr, 64)
		if err != nil {
			return 0, fmt.Errorf("expected a number before 'd', got %q", s)
		}
		return time.Duration(days * 24 * float64(time.Hour)), nil
	}
	return time.ParseDuration(s)
}
