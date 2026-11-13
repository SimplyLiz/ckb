package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/paths"
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
	b.WriteString("# added by ckb (local CKB index, safe to delete)\n")
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
// result into the running engine. An existing index is left alone here;
// startStartupRefresh catches it up once if HEAD moved, and keeping it current
// during the session is what --watch opts into.
//
// Nobody asked for this build, so it has to stay cheap for the machine: a linked
// worktree gets none (agents open them by the dozen, and each would index the
// whole tree), and only one first index runs per machine. The index lock is per
// repo, so without the machine-wide one every session opened in a different
// repo would start an indexer at once. A session that finds the lock taken skips
// its build; the next session in that repo tries again.
func startFirstIndex(repoRoot string, logger *slog.Logger) {
	if _, err := os.Stat(resolveIndexPath(repoRoot)); err == nil {
		return
	}
	if isLinkedWorktree(repoRoot) {
		logger.Info("Linked git worktree: skipping the automatic first index", "hint", "run 'ckb index' here if you want one")
		return
	}
	go func() {
		state := &watchState{}
		if !firstIndexTick(repoRoot, state, logger) {
			return
		}
		// No indexer installed: look for it on PATH now and then, and build
		// when it's there, instead of staying text-only until a restart.
		for state.indexerWait.waiting() {
			time.Sleep(firstIndexRecheck)
			if pathExists(resolveIndexPath(repoRoot)) {
				return // built meanwhile by 'ckb index' or the reindex tool
			}
			awaited := state.indexerWait.commands
			if !state.indexerWait.ready() {
				continue
			}
			logger.Info("SCIP indexer now on PATH, building the first index", "indexers", strings.Join(awaited, ", "))
			if !firstIndexTick(repoRoot, state, logger) {
				state.indexerWait.await(awaited) // machine lock busy: try again later
			}
		}
	}()
}

// startStartupRefresh brings an index that fell behind HEAD up to date once,
// in the background, when 'ckb mcp' starts without --watch. Without it an
// index built on install stays where it was: two weeks of commits later the
// agent asks prepareChange about code the index has never seen, and nothing
// tells it so unless it calls getStatus.
//
// Only a moved HEAD (new commits, another branch) or a newly installed indexer
// triggers it. Uncommitted edits alone don't: they change with every session,
// and an index rebuild per session start is what watch mode opts into. An
// index without metadata was built by hand or by an old version; its state is
// unknown, so it is left alone too.
//
// Incremental first where the language supports it, the full build otherwise,
// under the same machine-wide lock as the first index: nobody asked for this
// build either, and several sessions starting at once in different repos must
// not run several indexers. errRepoTooLarge and a missing indexer end it
// quietly; triggerReindex enforces both.
func startStartupRefresh(repoRoot string, logger *slog.Logger) {
	ckbDir := filepath.Join(repoRoot, ".ckb")
	trigger, reason, ok := startupRefreshReason(repoRoot, ckbDir)
	if !ok {
		return
	}
	go func() {
		if !refreshIndexOnce(repoRoot, ckbDir, trigger, reason, logger) {
			return
		}
		if eng := loadedEngine(); eng != nil {
			if err := eng.ReloadSCIP(context.Background()); err != nil {
				logger.Warn("Index refreshed but reloading it into the server failed", "error", err.Error())
			}
		}
	}()
}

// startupRefreshReason says why the index needs a refresh at startup, and
// false when it doesn't (see startStartupRefresh for what counts).
func startupRefreshReason(repoRoot, ckbDir string) (index.RefreshTrigger, string, bool) {
	if !pathExists(resolveIndexPath(repoRoot)) {
		return "", "", false
	}
	meta, err := index.LoadMeta(ckbDir)
	if err != nil || meta == nil {
		return "", "", false
	}
	f := meta.CheckFreshness(repoRoot)
	if f.Fresh || f.HasUncommitted {
		return "", "", false
	}
	if f.CommitsBehind > 0 {
		return index.TriggerHEAD, fmt.Sprintf("%d commit(s) behind HEAD", f.CommitsBehind), true
	}
	if f.IndexedCommit != "" && f.CurrentCommit != "" && f.IndexedCommit != f.CurrentCommit {
		return index.TriggerHEAD, f.Reason, true
	}
	return index.TriggerStale, f.Reason, true
}

// refreshIndexOnce runs the startup refresh under the machine-wide lock.
// True when the index changed and the engine should reload it.
func refreshIndexOnce(repoRoot, ckbDir string, trigger index.RefreshTrigger, reason string, logger *slog.Logger) bool {
	lockDir, err := firstIndexLockDir()
	if err != nil {
		logger.Warn("Skipping the startup index refresh", "error", err.Error())
		return false
	}
	machineLock, err := index.AcquireLock(lockDir)
	if err != nil {
		logger.Info("Another background index is running on this machine; skipping the startup refresh")
		return false
	}
	defer machineLock.Release()

	logger.Info("Index is stale, refreshing it in the background", "reason", reason)
	if tryIncrementalReindex(repoRoot, ckbDir, logger) {
		logger.Info("Index refreshed", "mode", "incremental")
		return true
	}
	note, err := triggerReindex(context.Background(), repoRoot, ckbDir, trigger, reason, logger)
	switch {
	case err == nil:
		excludeBuiltIndex(repoRoot, logger)
		logger.Info("Index refreshed", "mode", "full", "note", note)
		return true
	case errors.Is(err, errIndexLocked):
		logger.Info("Another process is indexing this repo; leaving the refresh to it")
	case errors.Is(err, errIndexerUnavailable), errors.Is(err, errRepoTooLarge):
		logger.Info("Startup index refresh not possible", "reason", err.Error(), "hint", "run 'ckb index'")
	default:
		logger.Warn("Startup index refresh failed", "error", err.Error())
	}
	return false
}

// firstIndexTick runs one watchTick under the machine-wide first-index lock.
// False when it didn't run: the lock is held by another session's first index,
// or its directory isn't available.
func firstIndexTick(repoRoot string, state *watchState, logger *slog.Logger) bool {
	lockDir, err := firstIndexLockDir()
	if err != nil {
		logger.Warn("Skipping the automatic first index", "error", err.Error())
		return false
	}
	machineLock, err := index.AcquireLock(lockDir)
	if err != nil {
		logger.Info("Another first index is running on this machine; skipping it for now")
		return false
	}
	defer machineLock.Release()

	logger.Info("No SCIP index yet, building one in the background")
	permanentLogged := false
	watchTick(repoRoot, filepath.Join(repoRoot, ".ckb"), time.Minute, state, &permanentLogged, logger)
	return true
}

// firstIndexLockDir is where the machine-wide first-index lock lives.
func firstIndexLockDir() (string, error) {
	home, err := paths.GetCKBHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "first-index"), nil
}

// isLinkedWorktree reports whether dir is a linked git worktree rather than the
// main checkout: there, git's own directory differs from the common directory.
func isLinkedWorktree(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir").Output() // #nosec G204 //nolint:gosec // fixed git subcommand; dir is the detected git root
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	return len(fields) == 2 && fields[0] != fields[1]
}
