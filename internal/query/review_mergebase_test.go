package query

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// main moves on after the branch forked: a file only main changed is not part
// of the branch's change, and must not show up, reversed, as one.
func TestReviewAndSummarize_DiffFromForkPoint(t *testing.T) {
	engine, cleanup := setupGitRepoWithBranch(t, map[string]string{"feature.go": "package x\n\nfunc F() {}\n"})
	defer cleanup()
	repo := engine.repoRoot

	runGit(t, repo, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repo, "mainonly.go"), []byte("package x\n\nfunc M() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "main moves on")
	runGit(t, repo, "checkout", "feature/test")

	ctx := context.Background()
	pr, err := engine.SummarizePR(ctx, SummarizePROptions{BaseBranch: "main", NoAutoFetch: true})
	if err != nil {
		t.Fatalf("SummarizePR: %v", err)
	}
	var paths []string
	for _, f := range pr.ChangedFiles {
		paths = append(paths, f.Path)
	}
	if len(paths) != 1 || paths[0] != "feature.go" {
		t.Errorf("SummarizePR files = %v, want only feature.go", paths)
	}

	rev, err := engine.ReviewPR(ctx, ReviewPROptions{BaseBranch: "main", NoAutoFetch: true, Checks: []string{"generated"}})
	if err != nil {
		t.Fatalf("ReviewPR: %v", err)
	}
	if rev.Summary.TotalFiles != 1 {
		t.Errorf("ReviewPR totalFiles = %d, want 1: main's own commit is not the branch's change", rev.Summary.TotalFiles)
	}

	diff, err := engine.getGitDiff(false, "main")
	if err != nil {
		t.Fatalf("getGitDiff: %v", err)
	}
	if want := "feature.go"; !contains(diff, want) || contains(diff, "mainonly.go") {
		t.Errorf("getGitDiff(main) should name feature.go and not mainonly.go:\n%s", diff)
	}
}
