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
)

// selfInitExcludes are the paths a self-initialized repo gets written into:
// .ckb/ (config, caches) and the default SCIP index in the repo root.
var selfInitExcludes = []string{".ckb/", "/index.scip"}

// selfInitRepo initializes CKB for a git repo that 'ckb mcp' was started in
// without a .ckb/ directory, so the agent gets a working server without the
// user running 'ckb init' first. Same registration as 'ckb setup' (NoActivate:
// the global default repo stays untouched), with all of runInitCore's text
// discarded — stdout is the JSON-RPC transport here.
//
// Nobody asked for these files in this repo, so they're also kept out of
// 'git status' via .git/info/exclude: local to this clone, never committed,
// and the project's own .gitignore stays untouched.
func selfInitRepo(repoRoot string) error {
	if err := runInitCore(initOptions{Dir: repoRoot, NoActivate: true, Out: io.Discard}); err != nil {
		return err
	}
	return excludeFromGit(repoRoot, selfInitExcludes)
}

// excludeFromGit appends each pattern to the repo's info/exclude unless git
// already ignores that path (via .gitignore, a global excludes file, or an
// earlier run). Resolving the file through 'git rev-parse --git-path' keeps
// it correct for worktrees, where .git is a file, not a directory.
func excludeFromGit(repoRoot string, patterns []string) error {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-path", "info/exclude").Output() // #nosec G204 //nolint:gosec // fixed git subcommand; repoRoot is the detected git root
	if err != nil {
		return fmt.Errorf("locating info/exclude: %w", err)
	}
	excludePath := strings.TrimSpace(string(out))
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(repoRoot, excludePath)
	}

	current, _ := os.ReadFile(excludePath) // #nosec G304 -- path from git rev-parse
	existing := map[string]bool{}
	for _, line := range strings.Split(string(current), "\n") {
		existing[strings.TrimSpace(line)] = true
	}

	var missing []string
	for _, p := range patterns {
		if existing[p] {
			continue
		}
		// check-ignore exits 0 when the path is already ignored.
		path := strings.TrimPrefix(p, "/")
		if exec.Command("git", "-C", repoRoot, "check-ignore", "-q", path).Run() == nil { // #nosec G204 //nolint:gosec // fixed git subcommand on a constant pattern
			continue
		}
		missing = append(missing, p)
	}
	if len(missing) == 0 {
		return nil
	}

	if err = os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) // #nosec G302 -- git's own default mode for info/exclude
	if err != nil {
		return err
	}
	defer f.Close()

	var b strings.Builder
	if len(current) > 0 && current[len(current)-1] != '\n' {
		b.WriteString("\n")
	}
	b.WriteString("# added by ckb mcp (local CKB index, safe to delete)\n")
	for _, p := range missing {
		b.WriteString(p + "\n")
	}
	_, err = f.WriteString(b.String())
	return err
}

// hasCkbDir reports whether repoRoot has been initialized. 'ckb mcp' started
// outside any git repo falls back to the current directory without
// initializing it, and must not start indexing e.g. a home directory.
func hasCkbDir(repoRoot string) bool {
	info, err := os.Stat(filepath.Join(repoRoot, ".ckb"))
	return err == nil && info.IsDir()
}

// startFirstIndex builds the SCIP index once in the background when the repo
// has none and watch mode is off — the plugin manifest starts 'ckb mcp'
// without --watch, so without this a fresh repo would stay on git-only
// features for good. A single watchTick brings the same guards as watch mode
// (errRepoTooLarge, errIndexerUnavailable, the index lock) and reloads the
// result into the running engine. An existing index, stale or not, is left
// alone: keeping it current is what --watch opts into.
func startFirstIndex(repoRoot string, logger *slog.Logger) {
	if _, err := os.Stat(resolveIndexPath(repoRoot)); err == nil {
		return
	}
	logger.Info("No SCIP index yet, building one in the background")
	go func() {
		permanentLogged := false
		watchTick(repoRoot, filepath.Join(repoRoot, ".ckb"), time.Minute, &watchState{}, &permanentLogged, logger)
	}()
}
