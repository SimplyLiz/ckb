package query

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// commitAt writes each file and commits them with the given author date.
func commitAt(t *testing.T, dir string, date time.Time, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-q", "-m", "commit at " + date.Format(time.RFC3339)},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
			"GIT_AUTHOR_DATE="+date.Format(time.RFC3339),
			"GIT_COMMITTER_DATE="+date.Format(time.RFC3339),
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func newCouplingTestRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return &Engine{repoRoot: dir, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, dir
}

func TestBatchFileLastModified_NewestCommitPerFile(t *testing.T) {
	e, dir := newCouplingTestRepo(t)
	d1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	d2 := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	commitAt(t, dir, d1, map[string]string{"a.go": "1", "b.go": "1", "c.go": "1"})
	commitAt(t, dir, d2, map[string]string{"a.go": "2"})
	commitAt(t, dir, d3, map[string]string{"unrelated.go": "1"})

	got := e.batchFileLastModified(context.Background(), []string{"a.go", "b.go", "gone.go"})

	if !got["a.go"].Equal(d2) {
		t.Errorf("a.go = %v, want %v", got["a.go"], d2)
	}
	if !got["b.go"].Equal(d1) {
		t.Errorf("b.go = %v, want %v", got["b.go"], d1)
	}
	if _, ok := got["gone.go"]; ok {
		t.Errorf("gone.go has a date, want none")
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2: %v", len(got), got)
	}
}

// Paths come from git history, which on a PR branch is attacker-controlled.
// None of these may execute anything, and each must resolve to its own date.
func TestBatchFileLastModified_HostilePathNames(t *testing.T) {
	e, dir := newCouplingTestRepo(t)
	hostile := []string{
		"$(touch pwned-subst)",
		"`touch pwned-backtick`",
		`x";touch pwned-quote;"`,
		"tab\tname.go",
		":(glob)*.go",
		"-n",
	}
	d1 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	d2 := time.Date(2025, 9, 1, 10, 0, 0, 0, time.UTC)
	commitAt(t, dir, d1, map[string]string{"plain.go": "1"})
	files := map[string]string{}
	for _, h := range hostile {
		files[h] = "x"
	}
	commitAt(t, dir, d2, files)
	// Touching plain.go later would make an unescaped ":(glob)*.go" pathspec
	// pick up this date instead of its own.
	commitAt(t, dir, d2.Add(24*time.Hour), map[string]string{"plain.go": "2"})

	got := e.batchFileLastModified(context.Background(), hostile)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), "pwned") {
			t.Errorf("path name was executed: %s exists", ent.Name())
		}
	}
	for _, h := range hostile {
		if !got[h].Equal(d2) {
			t.Errorf("%q = %v, want %v", h, got[h], d2)
		}
	}
}

func TestParseLastModified(t *testing.T) {
	d1 := "2026-01-02T03:04:05+02:00"
	d2 := "2025-01-02T03:04:05Z"
	h := func(d string) string { return lastModifiedHeader + d + "\x00" }
	stream := h(d1) + "\na.go\x00" + "\nb.go\x00" + // second record: a file literally named "\nb.go"
		h("2025-06-01T00:00:00Z") + // a commit that touched none of the paths
		h(d2) + "\nb.go\x00" + "a.go\x00"

	got := map[string]time.Time{}
	parseLastModified(strings.NewReader(stream), []string{"a.go", "b.go", "\nb.go"}, got)

	t1, _ := time.Parse(time.RFC3339, d1)
	t2, _ := time.Parse(time.RFC3339, d2)
	for name, want := range map[string]time.Time{"a.go": t1, "\nb.go": t1, "b.go": t2} {
		if !got[name].Equal(want) {
			t.Errorf("%q = %v, want %v", name, got[name], want)
		}
	}
}
