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
