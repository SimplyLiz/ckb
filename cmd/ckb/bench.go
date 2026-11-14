package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/bench"
	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

var (
	benchSessionCwd    string
	benchSessionFormat string
	benchCompareFormat string
)

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark instrumentation: join Claude Code transcripts with CKB's activity ledger",
	Long: `Join a Claude Code session transcript (client side: tokens, turns, tool
calls) with CKB's activity ledger (server side: what CKB actually delivered)
into one record, and compare two such records.

This never reports "tokens saved" or "avoided" - it produces the numbers for
both sides, neutrally, so you can look.`,
}

var benchSessionCmd = &cobra.Command{
	Use:   "session <sessionId>",
	Short: "Build a benchmark record for one Claude Code session",
	Long: `Reads the Claude Code transcript for <sessionId> from
~/.claude/projects/<cwd-slug>/<sessionId>.jsonl (falling back to a glob
search across all project directories) and joins it with CKB's activity
ledger for the same session, when available.

Examples:
  ckb bench session abc123-def456
  ckb bench session abc123-def456 --cwd /path/to/repo
  ckb bench session abc123-def456 --format json`,
	Args: cobra.ExactArgs(1),
	Run:  runBenchSession,
}

var benchCompareCmd = &cobra.Command{
	Use:   "compare <sessionA> <sessionB>",
	Short: "Compare two benchmark session records side by side",
	Long: `Builds a record for each session (see "ckb bench session") and reports
absolute and percent deltas for total tokens, context volume, output tokens,
turns, tool calls, agent file reads, agent searches, CKB calls, and duration.

Examples:
  ckb bench compare abc123-def456 xyz789-uvw012
  ckb bench compare abc123-def456 xyz789-uvw012 --format json`,
	Args: cobra.ExactArgs(2),
	Run:  runBenchCompare,
}

func init() {
	benchSessionCmd.Flags().StringVar(&benchSessionCwd, "cwd", "", "Working directory the session ran in (defaults to the current directory; used to locate the transcript's project folder, falls back to a glob search if not found there)")
	benchSessionCmd.Flags().StringVar(&benchSessionFormat, "format", "human", "Output format (human, json)")
	benchCompareCmd.Flags().StringVar(&benchCompareFormat, "format", "human", "Output format (human, json)")

	benchCmd.AddCommand(benchSessionCmd)
	benchCmd.AddCommand(benchCompareCmd)
	rootCmd.AddCommand(benchCmd)
}

// benchLedgerSource returns the LedgerSource the CLI wires bench commands to:
// the tool_calls ledger of the current repo's .ckb/ckb.db. Outside a CKB
// repo (or when the DB cannot be opened) it falls back to NoLedger, and the
// record carries a "ledger not wired" note instead of failing.
func benchLedgerSource() (bench.LedgerSource, func()) {
	repoRoot, err := getRepoRoot()
	if err != nil {
		return bench.NoLedger{}, func() {}
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db, err := storage.Open(repoRoot, logger)
	if err != nil {
		return bench.NoLedger{}, func() {}
	}
	return bench.StorageLedger{DB: db}, func() { _ = db.Close() }
}

func runBenchSession(cmd *cobra.Command, args []string) {
	sessionID := args[0]

	cwd := benchSessionCwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	ctx := newContext()
	ledger, closeLedger := benchLedgerSource()
	defer closeLedger()
	rec, err := bench.BuildSessionRecord(ctx, sessionID, cwd, ledger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building session record: %v\n", err)
		os.Exit(1)
	}

	output, err := formatBenchSession(rec, OutputFormat(benchSessionFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(output)
}

func runBenchCompare(cmd *cobra.Command, args []string) {
	sessionA, sessionB := args[0], args[1]
	ctx := newContext()
	ledger, closeLedger := benchLedgerSource()
	defer closeLedger()

	recA, err := bench.BuildSessionRecord(ctx, sessionA, "", ledger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building session record for %s: %v\n", sessionA, err)
		os.Exit(1)
	}
	recB, err := bench.BuildSessionRecord(ctx, sessionB, "", ledger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building session record for %s: %v\n", sessionB, err)
		os.Exit(1)
	}

	comparison := bench.Compare(*recA, *recB)

	output, err := formatBenchCompare(comparison, OutputFormat(benchCompareFormat))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(output)
}

// formatBenchSession renders a bench.SessionRecord. It deliberately does not
// go through the shared FormatResponse/formatHuman dispatch in format.go
// (which would require adding a case there) - bench output has its own
// shape (key/value block, not the Response{Facts,Provenance,...} envelope),
// so it is self-contained here.
func formatBenchSession(rec *bench.SessionRecord, format OutputFormat) (string, error) {
	switch format {
	case FormatJSON:
		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}
		return string(data), nil
	case FormatHuman, "":
		return formatBenchSessionHuman(rec), nil
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

func formatBenchCompare(cmp bench.Comparison, format OutputFormat) (string, error) {
	switch format {
	case FormatJSON:
		data, err := json.MarshalIndent(cmp, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal JSON: %w", err)
		}
		return string(data), nil
	case FormatHuman, "":
		return formatBenchCompareHuman(cmp), nil
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}
}

func formatBenchSessionHuman(rec *bench.SessionRecord) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Session:         %s\n", rec.SessionID)
	if rec.Cwd != "" {
		fmt.Fprintf(&b, "Cwd:             %s\n", rec.Cwd)
	}
	if !rec.Start.IsZero() {
		fmt.Fprintf(&b, "Start:           %s\n", rec.Start.Format("2006-01-02T15:04:05Z07:00"))
	}
	if !rec.End.IsZero() {
		fmt.Fprintf(&b, "End:             %s\n", rec.End.Format("2006-01-02T15:04:05Z07:00"))
	}
	fmt.Fprintf(&b, "Duration:        %s\n", rec.Duration)
	fmt.Fprintf(&b, "Turns:           %d\n", rec.Turns)
	fmt.Fprintf(&b, "Tokens:          input=%d cacheCreation=%d cacheRead=%d output=%d thinking=%d total=%d\n",
		rec.Tokens.Input, rec.Tokens.CacheCreation, rec.Tokens.CacheRead, rec.Tokens.Output, rec.Tokens.Thinking, rec.Tokens.Total)
	fmt.Fprintf(&b, "Tool calls:      %d%s\n", sumIntMap(rec.ToolCalls), formatCountMapSuffix(rec.ToolCalls))
	fmt.Fprintf(&b, "Agent reads:     %d (%d distinct file(s))\n", rec.AgentFileReads, rec.DistinctFiles)
	fmt.Fprintf(&b, "Agent searches:  %d\n", rec.AgentSearches)
	fmt.Fprintf(&b, "Bash calls:      %d\n", rec.BashCalls)
	fmt.Fprintf(&b, "CKB calls:       %d%s\n", rec.CKBCalls, formatCountMapSuffix(rec.CKBTools))

	if rec.Ledger.Calls < 0 {
		fmt.Fprintf(&b, "Ledger:          not wired\n")
	} else {
		fmt.Fprintf(&b, "Ledger:          calls=%d bytes=%d errors=%d%s\n",
			rec.Ledger.Calls, rec.Ledger.Bytes, rec.Ledger.Errors, formatCountMapSuffix(rec.Ledger.Facts))
	}

	if len(rec.Notes) > 0 {
		fmt.Fprintf(&b, "Notes:\n")
		for _, n := range rec.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatBenchCompareHuman(cmp bench.Comparison) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Session A: %s\n", cmp.SessionA)
	fmt.Fprintf(&b, "Session B: %s\n\n", cmp.SessionB)

	headers := []string{"Metric", "A", "B", "Δ", "Δ%"}
	rows := make([][]string, 0, len(cmp.Metrics))
	for _, d := range cmp.Metrics {
		pct := "n/a"
		if d.A != 0 {
			pct = fmt.Sprintf("%s%%", formatNumber(d.Percent))
		}
		rows = append(rows, []string{
			d.Metric,
			formatNumber(d.A),
			formatNumber(d.B),
			formatNumber(d.Delta),
			pct,
		})
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	writeRow := func(cells []string) {
		parts := make([]string, len(cells))
		for i, cell := range cells {
			if i == 0 {
				parts[i] = padRight(cell, widths[i])
			} else {
				parts[i] = padLeft(cell, widths[i])
			}
		}
		fmt.Fprintln(&b, strings.Join(parts, "  "))
	}

	writeRow(headers)
	for _, row := range rows {
		writeRow(row)
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatNumber(v float64) string {
	if math.Trunc(v) == v {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func sumIntMap(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func formatCountMapSuffix(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, m[k]))
	}
	return fmt.Sprintf(" (%s)", strings.Join(parts, ", "))
}
