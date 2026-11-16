package index

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A snapshot of the working tree, for an index built from uncommitted
// changes. The index overlay patches the loaded index with the changes made
// since it was built; when it was built from a dirty tree, those are the
// changes since that tree, not since the commit (which the index already
// contains in part). The snapshot is a git tree object written through a
// temporary git index: HEAD, every tracked change, and untracked source
// files. HEAD, the repository's index and the working tree are not touched;
// git writes blobs for the changed files into the object store, unreferenced,
// so 'git gc' drops them in time.

// snapshotSourceExts are the untracked files a snapshot includes: files an
// indexer reads. Everything else untracked (build output, binaries) stays out
// of the object store.
var snapshotSourceExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".py": true, ".rs": true, ".java": true, ".kt": true, ".kts": true, ".dart": true, ".rb": true,
	".cs": true, ".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".h": true, ".hh": true,
	".hpp": true, ".php": true, ".swift": true, ".scala": true, ".m": true, ".mm": true,
}

func gitEnv(ctx context.Context, repoRoot string, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 //nolint:gosec // fixed git subcommands
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// worktreeIndex fills a temporary git index at tmp with the working tree as
// an indexer reads it. It starts from a copy of the repository's own index,
// so only files whose stat data changed are hashed again.
func worktreeIndex(ctx context.Context, repoRoot, tmp string) ([]string, error) {
	gitIndex, err := gitEnv(ctx, repoRoot, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	src := strings.TrimSpace(string(gitIndex))
	if !filepath.IsAbs(src) {
		src = filepath.Join(repoRoot, src)
	}
	if err := copyFile(src, tmp); err != nil {
		return nil, err
	}
	env := []string{"GIT_INDEX_FILE=" + tmp}
	if _, err := gitEnv(ctx, repoRoot, env, "add", "-u"); err != nil {
		return nil, err
	}
	out, err := gitEnv(ctx, repoRoot, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	var untracked []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" && snapshotSourceExts[strings.ToLower(filepath.Ext(p))] {
			untracked = append(untracked, p)
		}
	}
	for len(untracked) > 0 {
		n := min(len(untracked), 500)
		if _, err := gitEnv(ctx, repoRoot, env, append([]string{"add", "--"}, untracked[:n]...)...); err != nil {
			return nil, err
		}
		untracked = untracked[n:]
	}
	return env, nil
}

// SnapshotWorktree writes the working tree (as worktreeIndex sees it) as a
// git tree and returns its hash.
func SnapshotWorktree(ctx context.Context, repoRoot string) (string, error) {
	tmp, err := tempIndexPath(repoRoot, "snapshot")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp) }()
	env, err := worktreeIndex(ctx, repoRoot, tmp)
	if err != nil {
		return "", err
	}
	out, err := gitEnv(ctx, repoRoot, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DiffSinceSnapshot is the zero-context diff (with renames) from a snapshot
// tree to the working tree as it is now.
func DiffSinceSnapshot(ctx context.Context, repoRoot, tree string) ([]byte, error) {
	if _, err := gitEnv(ctx, repoRoot, nil, "cat-file", "-e", tree+"^{tree}"); err != nil {
		return nil, fmt.Errorf("snapshot tree %s is gone: %w", tree, err)
	}
	tmp, err := tempIndexPath(repoRoot, "overlay")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmp) }()
	env, err := worktreeIndex(ctx, repoRoot, tmp)
	if err != nil {
		return nil, err
	}
	return gitEnv(ctx, repoRoot, env, "diff", "--cached", "-U0", "--no-color", "--no-ext-diff", "--no-textconv", "-M", tree, "--")
}

// RecordWorktree records in the metadata what the index was built from
// beyond the commit: a snapshot tree when the working tree was dirty, nothing
// when it was clean. Best effort: without a snapshot the overlay diffs
// against the commit, as for an index built from a clean tree.
func (m *IndexMeta) RecordWorktree(repoRoot string, dirty bool) {
	m.WorktreeTree = ""
	if !dirty {
		return
	}
	if tree, err := SnapshotWorktree(context.Background(), repoRoot); err == nil {
		m.WorktreeTree = tree
	}
}

func tempIndexPath(repoRoot, name string) (string, error) {
	dir := filepath.Join(repoRoot, ".ckb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, name+"-*.gitindex")
	if err != nil {
		return "", err
	}
	path := f.Name()
	_ = f.Close()
	return path, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- the repository's git index
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) // #nosec G304 -- a temp file under .ckb
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
