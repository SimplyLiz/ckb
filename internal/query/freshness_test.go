package query

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
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func freshnessRepo(t *testing.T) (dir, commitA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir = t.TempDir()
	gitT(t, dir, "init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/a.ts", "src/b.ts"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-q", "-m", "A")
	return dir, gitT(t, dir, "rev-parse", "HEAD")
}

func TestComputeIndexFreshness(t *testing.T) {
	dir, a := freshnessRepo(t)

	t.Run("head equals indexed", func(t *testing.T) {
		f := computeIndexFreshness(dir, a, []string{"src/a.ts"})
		if f.IndexedCommit != a || f.CommitsBehind != 0 || len(f.ChangedSinceIndex) != 0 || f.AffectsResult {
			t.Fatalf("unexpected: %+v", f)
		}
	})

	if err := os.WriteFile(filepath.Join(dir, "src/a.ts"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("uncommitted change affects result", func(t *testing.T) {
		f := computeIndexFreshness(dir, a, []string{"src/a.ts", "src/b.ts"})
		if len(f.ChangedSinceIndex) != 1 || f.ChangedSinceIndex[0] != "src/a.ts" || !f.AffectsResult || f.Hint == "" {
			t.Fatalf("unexpected: %+v", f)
		}
	})

	t.Run("uncommitted change outside result", func(t *testing.T) {
		f := computeIndexFreshness(dir, a, []string{"src/b.ts"})
		if f.AffectsResult || len(f.ChangedSinceIndex) != 1 {
			t.Fatalf("unexpected: %+v", f)
		}
	})

	t.Run("untracked file listed", func(t *testing.T) {
		p := filepath.Join(dir, "src/new.ts")
		if err := os.WriteFile(p, []byte("n\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(p)
		f := computeIndexFreshness(dir, a, []string{"./src/new.ts"})
		if !f.AffectsResult {
			t.Fatalf("untracked file in result should affect: %+v", f)
		}
	})

	t.Run("committed change counts commits behind", func(t *testing.T) {
		gitT(t, dir, "commit", "-q", "-am", "B")
		f := computeIndexFreshness(dir, a, []string{"src/a.ts"})
		if f.CommitsBehind != 1 || !f.AffectsResult {
			t.Fatalf("unexpected: %+v", f)
		}
	})

	t.Run("unknown commit", func(t *testing.T) {
		for _, c := range []string{"", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"} {
			f := computeIndexFreshness(dir, c, []string{"src/a.ts"})
			if f.IndexedCommit != "" || f.ChangedSinceIndex != nil || f.AffectsResult || !strings.Contains(f.Hint, "unknown") {
				t.Fatalf("commit %q: %+v", c, f)
			}
		}
	})

	t.Run("cap", func(t *testing.T) {
		set := &changedSet{known: true}
		for i := 0; i < 60; i++ {
			set.files = append(set.files, "f/"+string(rune('a'+i%26))+string(rune('A'+i/26)))
		}
		f := buildIndexFreshness(dir, a, set, nil)
		if len(f.ChangedSinceIndex) != maxChangedSinceIndex || !f.ChangedSinceIndexTruncated {
			t.Fatalf("cap not applied: %d", len(f.ChangedSinceIndex))
		}
	})
}

func TestIndexFreshnessForNoSCIP(t *testing.T) {
	e := &Engine{}
	if e.IndexFreshnessFor(context.Background(), []string{"a"}) != nil {
		t.Fatal("expected nil without SCIP adapter")
	}
}
