package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestSnapshotWorktree(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\n\nfunc A() {}\n")
	gitT(t, dir, "init", "-q")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "base")

	// Built from: a.go edited, b.go untracked, a binary untracked.
	write("a.go", "package a\n\nfunc A() {}\n\nfunc A2() {}\n")
	write("b.go", "package a\n\nfunc B() {}\n")
	write("tool.bin", "\x00\x01binary")
	status := gitT(t, dir, "status", "--porcelain")

	ctx := context.Background()
	tree, err := SnapshotWorktree(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	files := gitT(t, dir, "ls-tree", "-r", "--name-only", tree)
	if !strings.Contains(files, "b.go") || strings.Contains(files, "tool.bin") {
		t.Errorf("snapshot holds %q, want a.go and b.go without tool.bin", files)
	}
	if got := gitT(t, dir, "status", "--porcelain"); got != status {
		t.Errorf("git status changed:\n%s\nwas\n%s", got, status)
	}

	// Nothing changed since: an empty diff.
	d, err := DiffSinceSnapshot(ctx, dir, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 0 {
		t.Errorf("diff right after the snapshot: %s", d)
	}

	// Only what changed after the snapshot shows.
	write("b.go", "package a\n\nfunc B() {}\n\nfunc B2() {}\n")
	d, err = DiffSinceSnapshot(ctx, dir, tree)
	if err != nil {
		t.Fatal(err)
	}
	text := string(d)
	if !strings.Contains(text, "+func B2() {}") || strings.Contains(text, "A2") || strings.Contains(text, "a.go") {
		t.Errorf("diff since the snapshot:\n%s", text)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".ckb", "*.gitindex"))
	if len(leftovers) != 0 {
		t.Errorf("temporary git indexes left behind: %v", leftovers)
	}
}
