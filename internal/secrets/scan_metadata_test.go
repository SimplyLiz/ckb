package secrets

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func pgURL() string { return "postgres://svc:" + "Zq8vR2mXk4Lp" + "@db.internal:5432/app" }

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// Every scope reports when it ran and how many files it read, not only the
// working-tree scan: staged and history results used to come back with
// scannedAt 0001-01-01 and filesScanned 0 next to real findings.
func TestScanMetadataEveryScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.env"), []byte("DB="+pgURL()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", ".")

	sc := NewScanner(dir, slog.Default())
	for _, scope := range []ScanScope{ScopeWorkdir, ScopeStaged} {
		res, err := sc.Scan(context.Background(), ScanOptions{RepoRoot: dir, Scope: scope, ApplyAllowlist: true})
		if err != nil {
			t.Fatalf("%s: %v", scope, err)
		}
		if res.ScannedAt.IsZero() || res.Duration == "" {
			t.Errorf("%s: scannedAt %v duration %q", scope, res.ScannedAt, res.Duration)
		}
		if res.Summary.FilesScanned != 2 {
			t.Errorf("%s: filesScanned %d, want 2", scope, res.Summary.FilesScanned)
		}
		if res.Summary.TotalFindings != 1 {
			t.Errorf("%s: %d findings, want 1: %+v", scope, res.Summary.TotalFindings, res.Findings)
		}
	}

	gitIn(t, dir, "commit", "-q", "-m", "init")
	res, err := sc.Scan(context.Background(), ScanOptions{RepoRoot: dir, Scope: ScopeHistory, ApplyAllowlist: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.ScannedAt.IsZero() || res.Summary.FilesScanned != 2 || res.Summary.TotalFindings != 1 {
		t.Errorf("history: scannedAt %v filesScanned %d findings %d",
			res.ScannedAt, res.Summary.FilesScanned, res.Summary.TotalFindings)
	}
	if len(res.Findings) == 1 && res.Findings[0].Commit == "" {
		t.Error("history finding without commit")
	}
}
