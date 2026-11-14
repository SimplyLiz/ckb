package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// hookCmd groups the commands Claude Code hooks call. They print what the hook
// protocol expects on stdout and nothing else, never fail, and stay fast:
// Claude's first response waits for SessionStart hooks.
var hookCmd = &cobra.Command{
	Use:    "hook",
	Short:  "Commands for Claude Code hooks (used by the CKB plugin)",
	Hidden: true,
}

var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start",
	Short: "Print the index state of the session's repo as SessionStart hook context",
	Long: `Reads the SessionStart hook input on stdin (for its cwd) and prints
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}
with one to three lines on the CKB index of that repo: current, behind HEAD,
being built, or missing, and the next step. Prints nothing outside a git repo
or in a repo without .ckb. Always exits 0; it never indexes or starts the
engine.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		defer func() { _ = recover() }() // a hook that panics would print a stack trace into the session
		text := sessionStartContext(hookCwd(os.Stdin))
		if text == "" {
			return
		}
		_ = writeSessionStartOutput(os.Stdout, text)
	},
}

func init() {
	hookCmd.AddCommand(hookSessionStartCmd)
	rootCmd.AddCommand(hookCmd)
}

// hookStdinWait bounds the wait for the hook input. Claude Code writes it and
// closes stdin at once; the bound only matters when something else runs the
// command with stdin left open.
const hookStdinWait = 500 * time.Millisecond

// hookCwd returns the cwd from the hook input on stdin, else
// CLAUDE_PROJECT_DIR, else the process's working directory.
func hookCwd(stdin *os.File) string {
	if stdin != nil && !isTerminal(stdin) {
		if cwd := readHookCwd(stdin, hookStdinWait); cwd != "" {
			return cwd
		}
	}
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	wd, _ := os.Getwd()
	return wd
}

func readHookCwd(r io.Reader, wait time.Duration) string {
	done := make(chan string, 1)
	go func() {
		var in struct {
			Cwd string `json:"cwd"`
		}
		data, _ := io.ReadAll(io.LimitReader(r, 1<<20))
		_ = json.Unmarshal(data, &in)
		done <- in.Cwd
	}()
	select {
	case cwd := <-done:
		return cwd
	case <-time.After(wait):
		return ""
	}
}

func writeSessionStartOutput(w io.Writer, text string) error {
	out := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "SessionStart",
			"additionalContext": text,
		},
	}
	return json.NewEncoder(w).Encode(out)
}

// sessionStartContext describes the CKB index of the repo dir is in, for the
// start of a session: whether answers rest on a current SCIP index, and if not,
// why and what comes next. "" outside a git repo or in one without .ckb. The
// static usage notes are in the MCP server's instructions, so this says only
// what holds for this repo now.
//
// It reads the same state 'ckb mcp' acts on at startup (startFirstIndex,
// startupRefreshReason), so where it says the server builds or refreshes the
// index, the server that starts beside it does that.
func sessionStartContext(dir string) string {
	if dir == "" {
		return ""
	}
	root := repos.FindGitRoot(dir)
	if root == "" || !hasCkbDir(root) {
		return ""
	}
	ckbDir := filepath.Join(root, ".ckb")

	if index.BuildRunning(ckbDir) {
		return "CKB: the SCIP index for this repo is being built right now. Until it loads, symbols, " +
			"references, callers and impact come from text search and parsers and can miss uses."
	}

	if !pathExists(resolveIndexPath(root)) {
		d := diagnoseIndex(root)
		if d.Ready && !isLinkedWorktree(root) {
			line := "CKB: no SCIP index yet; the CKB MCP server builds it in the background as it starts. " +
				"Until it loads, symbols, references, callers and impact come from text search and parsers and can miss uses."
			if d.Note != "" {
				line += "\n" + d.Note
			}
			return line
		}
		return "CKB: " + mcp.MissingIndexMessage(d, true)
	}

	meta, err := index.LoadMeta(ckbDir)
	if err != nil || meta == nil {
		return "CKB: the SCIP index has no build record, so how current it is is unknown. " +
			"Call the reindex tool before relying on impact or references."
	}

	lines := []string{indexFreshnessLine(root, ckbDir, meta)}
	if partial := mcp.PartialIndexMessage(meta, true); partial != "" {
		lines = append(lines, partial)
	}
	return strings.Join(lines, "\n")
}

// indexFreshnessLine is the one line on how current the index is.
func indexFreshnessLine(root, ckbDir string, meta *index.IndexMeta) string {
	f := meta.CheckFreshness(root)
	if f.Fresh {
		if c := shortCommit(f.IndexedCommit); c != "" {
			return fmt.Sprintf("CKB: the SCIP index is current (commit %s).", c)
		}
		return "CKB: the SCIP index is current."
	}
	if _, reason, refresh := startupRefreshReason(root, ckbDir); refresh {
		return fmt.Sprintf("CKB: the SCIP index is %s; the CKB MCP server refreshes it in the background as it starts. "+
			"Until then, answers about code changed since %s can be out of date.", reason, orIndexBuild(shortCommit(f.IndexedCommit)))
	}
	if f.HasUncommitted && f.CommitsBehind == 0 {
		return fmt.Sprintf("CKB: the SCIP index matches commit %s; uncommitted changes are not in it. "+
			"Call the reindex tool after larger edits, before relying on impact or references for them.", orIndexBuild(shortCommit(f.IndexedCommit)))
	}
	return fmt.Sprintf("CKB: the SCIP index is out of date (%s). Call the reindex tool before relying on impact or references.", f.Reason)
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func orIndexBuild(commit string) string {
	if commit == "" {
		return "the last index build"
	}
	return commit
}
