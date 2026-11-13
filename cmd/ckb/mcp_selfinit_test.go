package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

func gitInitTemp(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// captureStdout runs fn and returns everything it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

// TestSelfInitRepo is the 'ckb mcp' path: it must initialize the repo it was
// given (not the process cwd), leave the global default alone, write nothing
// to stdout (the JSON-RPC transport), and leave 'git status' clean.
func TestSelfInitRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dirA := chdirTemp(t)
	if err := runInitCore(initOptions{Out: io.Discard}); err != nil {
		t.Fatalf("init repo A: %v", err)
	}

	dirB := gitInitTemp(t)
	var initErr error
	stdout := captureStdout(t, func() { initErr = selfInitRepo(dirB) })
	if initErr != nil {
		t.Fatalf("selfInitRepo: %v", initErr)
	}

	if stdout != "" {
		t.Errorf("selfInitRepo wrote to stdout: %q", stdout)
	}
	// cwd is still repo A, so this proves initOptions.Dir is honored.
	if !hasCkbDir(dirB) {
		t.Error("repo B has no .ckb/")
	}

	registry, err := repos.LoadRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if entry, ok := registry.Repos[registry.Default]; !ok || entry.Path != dirA {
		t.Errorf("default repo changed to %+v, want %q", entry, dirA)
	}
	if entry, _ := registry.GetByPath(dirB); entry == nil {
		t.Error("repo B was not registered")
	}

	// A background index lands in the repo root; it must stay invisible too.
	if err := os.WriteFile(filepath.Join(dirB, "index.scip"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if status := gitStatusPorcelain(t, dirB); status != "" {
		t.Errorf("git status not clean after self-init:\n%s", status)
	}
}

func TestExcludeFromGit(t *testing.T) {
	dir := gitInitTemp(t)
	excludePath := filepath.Join(dir, ".git", "info", "exclude")

	// No trailing newline: the appended block must not glue onto the last line.
	if err := os.WriteFile(excludePath, []byte("*.log"), 0644); err != nil {
		t.Fatal(err)
	}
	// Already ignored by the project: must not be duplicated into exclude.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".ckb/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := excludeFromGit(dir, selfInitExcludes); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if lines[0] != "*.log" {
		t.Errorf("first line = %q, want the original *.log", lines[0])
	}
	if n := strings.Count(string(data), "/index.scip"); n != 1 {
		t.Errorf("/index.scip appears %d times, want 1:\n%s", n, data)
	}
	if strings.Contains(string(data), ".ckb/") {
		t.Errorf(".ckb/ added although .gitignore covers it:\n%s", data)
	}
}

func TestStartFirstIndex_LeavesExistingIndexAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	startFirstIndex(dir, slog.New(slog.NewTextHandler(&logs, nil)))
	if logs.Len() != 0 {
		t.Errorf("startFirstIndex acted on a repo that already has an index: %s", logs.String())
	}
}

func TestIsLinkedWorktree(t *testing.T) {
	repo := gitInitTemp(t)
	if isLinkedWorktree(repo) {
		t.Error("main checkout reported as a linked worktree")
	}
	if isLinkedWorktree(t.TempDir()) {
		t.Error("directory outside git reported as a linked worktree")
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("commit", "--allow-empty", "-m", "init")
	worktree := filepath.Join(t.TempDir(), "wt")
	run("worktree", "add", "-q", worktree)
	if !isLinkedWorktree(worktree) {
		t.Error("linked worktree not detected")
	}
}

func TestStartFirstIndex_SkipsLinkedWorktree(t *testing.T) {
	repo := gitInitTemp(t)
	if out, err := exec.Command("git", "-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	worktree := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "-C", repo, "worktree", "add", "-q", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree: %v\n%s", err, out)
	}

	var logs bytes.Buffer
	startFirstIndex(worktree, slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "worktree") {
		t.Errorf("expected the worktree skip to be logged, got: %s", logs.String())
	}
}

// A repo initialized before init wrote info/exclude gets its index and .ckb/ kept
// out of 'git status' on the next index; a tracked index is left alone.
func TestExcludeIndexFromGit(t *testing.T) {
	dir := gitInitTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".ckb/config.json", "index.scip"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if rel := excludeIndexFromGit(dir, filepath.Join(dir, "index.scip")); rel != "index.scip" {
		t.Errorf("excludeIndexFromGit = %q, want index.scip", rel)
	}
	if rel := excludeIndexFromGit(dir, filepath.Join(dir, "index.scip")); rel != "" {
		t.Errorf("second excludeIndexFromGit = %q, want nothing added", rel)
	}
	if got := gitStatusPorcelain(t, dir); got != "" {
		t.Fatalf("git status after backfill = %q, want clean", got)
	}

	tracked := gitInitTemp(t)
	if err := os.WriteFile(filepath.Join(tracked, "index.scip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", tracked, "add", "index.scip").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	excludeIndexFromGit(tracked, filepath.Join(tracked, "index.scip"))
	if b, _ := os.ReadFile(filepath.Join(tracked, ".git", "info", "exclude")); strings.Contains(string(b), "index.scip") {
		t.Errorf("tracked index was excluded: %q", b)
	}
}

// 'ckb setup' in a repo without .ckb/ initializes it; like 'ckb init' it keeps
// .ckb/ and the index out of 'git status'.
func TestEnsureCkbInitialized_ExcludesFromGit(t *testing.T) {
	t.Setenv("CKB_HOME", t.TempDir())
	dir := gitInitTemp(t)
	t.Chdir(dir)
	if err := ensureCkbInitialized(); err != nil {
		t.Fatalf("ensureCkbInitialized: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitStatusPorcelain(t, dir); got != "" {
		t.Fatalf("git status after setup's init = %q, want clean", got)
	}
}

// An index the MCP server builds is kept out of 'git status' too, without
// writing to stdout (the JSON-RPC transport).
func TestExcludeBuiltIndex_LogsNotPrints(t *testing.T) {
	dir := gitInitTemp(t)
	if err := os.MkdirAll(filepath.Join(dir, ".ckb"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	stdout := captureStdout(t, func() {
		excludeBuiltIndex(dir, slog.New(slog.NewTextHandler(&logs, nil)))
	})
	if stdout != "" {
		t.Errorf("wrote to stdout: %q", stdout)
	}
	if !strings.Contains(logs.String(), "index.scip") {
		t.Errorf("exclude not logged: %s", logs.String())
	}
	if got := gitStatusPorcelain(t, dir); got != "" {
		t.Fatalf("git status = %q, want clean", got)
	}
}
