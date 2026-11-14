package query

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewPR_DeadCodeCheck(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"pkg/used.go": `package pkg

func UsedFunc() string {
	return "hello"
}
`,
		"pkg/unused.go": `package pkg

func UnusedExportedFunc() string {
	return "nobody calls me"
}
`,
	}

	engine, cleanup := setupGitRepoWithBranch(t, files)
	defer cleanup()

	ctx := context.Background()
	resp, err := engine.ReviewPR(ctx, ReviewPROptions{
		BaseBranch: "main",
		HeadBranch: "feature/test",
		Checks:     []string{"dead-code"},
	})
	if err != nil {
		t.Fatalf("ReviewPR failed: %v", err)
	}

	// dead-code check should be present (may skip without SCIP index, that's fine)
	found := false
	for _, c := range resp.Checks {
		if c.Name == "dead-code" {
			found = true
			if c.Status != "pass" && c.Status != "skip" && c.Status != "warn" {
				t.Errorf("unexpected dead-code status %q", c.Status)
			}
		}
	}
	if !found {
		t.Error("expected 'dead-code' check to be present")
	}
}

func TestReviewPR_TestGapsCheck(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"pkg/handler.go": `package pkg

import "fmt"

func HandleRequest(input string) string {
	result := process(input)
	return fmt.Sprintf("handled: %s", result)
}

func process(s string) string {
	return s + " processed"
}
`,
	}

	engine, cleanup := setupGitRepoWithBranch(t, files)
	defer cleanup()

	ctx := context.Background()
	resp, err := engine.ReviewPR(ctx, ReviewPROptions{
		BaseBranch: "main",
		HeadBranch: "feature/test",
		Checks:     []string{"test-gaps"},
	})
	if err != nil {
		t.Fatalf("ReviewPR failed: %v", err)
	}

	found := false
	for _, c := range resp.Checks {
		if c.Name == "test-gaps" {
			found = true
			// May be pass (no gaps found), info (gaps found), or skip
			validStatuses := map[string]bool{"pass": true, "info": true, "skip": true}
			if !validStatuses[c.Status] {
				t.Errorf("unexpected test-gaps status %q", c.Status)
			}
		}
	}
	if !found {
		t.Error("expected 'test-gaps' check to be present")
	}
}

func TestReviewPR_BlastRadiusCheck(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"pkg/core.go": `package pkg

func CoreFunction() string {
	return "core"
}
`,
	}

	engine, cleanup := setupGitRepoWithBranch(t, files)
	defer cleanup()

	ctx := context.Background()

	// With maxFanOut=0 (default), blast-radius should run in informational mode
	resp, err := engine.ReviewPR(ctx, ReviewPROptions{
		BaseBranch: "main",
		HeadBranch: "feature/test",
		Checks:     []string{"blast-radius"},
	})
	if err != nil {
		t.Fatalf("ReviewPR failed: %v", err)
	}

	found := false
	for _, c := range resp.Checks {
		if c.Name == "blast-radius" {
			found = true
			// No SCIP index in this repo: no callers to count, which is a skip, not a pass.
			if c.Status != "info" && c.Status != "pass" && !(c.Status == "skip" && strings.Contains(c.Summary, "ckb index")) {
				t.Errorf("expected blast-radius to be info/pass with default policy (maxFanOut=0), got %q: %s", c.Status, c.Summary)
			}
			if c.Severity != "info" {
				t.Errorf("expected blast-radius severity 'info' in informational mode, got %q", c.Severity)
			}
		}
	}
	if !found {
		t.Error("expected 'blast-radius' check to be present")
	}

	// With maxFanOut set, it should run (pass or skip due to no SCIP index)
	policy := DefaultReviewPolicy()
	policy.MaxFanOut = 5
	resp2, err := engine.ReviewPR(ctx, ReviewPROptions{
		BaseBranch: "main",
		HeadBranch: "feature/test",
		Checks:     []string{"blast-radius"},
		Policy:     policy,
	})
	if err != nil {
		t.Fatalf("ReviewPR with maxFanOut failed: %v", err)
	}

	for _, c := range resp2.Checks {
		if c.Name == "blast-radius" {
			validStatuses := map[string]bool{"pass": true, "warn": true, "skip": true}
			if !validStatuses[c.Status] {
				t.Errorf("unexpected blast-radius status %q", c.Status)
			}
		}
	}
}

func TestReviewPR_Staged(t *testing.T) {
	t.Parallel()

	engine, cleanup := testEngine(t)
	defer cleanup()
	repoRoot := engine.repoRoot

	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	gitCmd("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("# Test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", ".")
	gitCmd("commit", "-m", "initial")

	// Stage a new file without committing
	if err := os.WriteFile(filepath.Join(repoRoot, "staged.go"), []byte("package main\n\nfunc Staged() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCmd("add", "staged.go")

	reinitEngine(t, engine)

	ctx := context.Background()
	resp, err := engine.ReviewPR(ctx, ReviewPROptions{
		Staged: true,
		Checks: []string{"secrets"}, // lightweight check
	})
	if err != nil {
		t.Fatalf("ReviewPR --staged failed: %v", err)
	}

	if resp.Summary.TotalFiles != 1 {
		t.Errorf("expected 1 staged file, got %d", resp.Summary.TotalFiles)
	}
}

func TestReviewPR_ScopeFilter(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"internal/query/engine.go": "package query\n\nfunc Engine() {}\n",
		"cmd/ckb/main.go":          "package main\n\nfunc main() {}\n",
		"internal/query/review.go": "package query\n\nfunc Review() {}\n",
	}

	engine, cleanup := setupGitRepoWithBranch(t, files)
	defer cleanup()

	ctx := context.Background()
	resp, err := engine.ReviewPR(ctx, ReviewPROptions{
		BaseBranch: "main",
		HeadBranch: "feature/test",
		Scope:      "internal/query/",
		Checks:     []string{"secrets"}, // lightweight check
	})
	if err != nil {
		t.Fatalf("ReviewPR with scope failed: %v", err)
	}

	// Only internal/query/ files should be in scope
	if resp.Summary.TotalFiles != 2 {
		t.Errorf("expected 2 files in scope 'internal/query/', got %d", resp.Summary.TotalFiles)
	}
}

func TestReviewPR_HintField(t *testing.T) {
	t.Parallel()

	// Verify that the Hint field is properly set on ReviewFinding
	f := ReviewFinding{
		Check:    "dead-code",
		Severity: "warning",
		File:     "test.go",
		Message:  "Dead code detected",
		Hint:     "→ ckb explain MyFunc",
	}

	if f.Hint == "" {
		t.Error("expected Hint to be set")
	}
	if f.Hint != "→ ckb explain MyFunc" {
		t.Errorf("unexpected Hint value: %q", f.Hint)
	}
}

func TestFindingTier_NewChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check string
		tier  int
	}{
		{"dead-code", 2},
		{"blast-radius", 2},
		{"test-gaps", 3},
		{"comment-drift", 3},
		{"format-consistency", 3},
		// existing
		{"breaking", 1},
		{"secrets", 1},
		{"coupling", 2},
	}

	for _, tt := range tests {
		got := findingTier(tt.check)
		if got != tt.tier {
			t.Errorf("findingTier(%q) = %d, want %d", tt.check, got, tt.tier)
		}
	}
}

func TestDefaultReviewPolicy_NewFields(t *testing.T) {
	t.Parallel()

	policy := DefaultReviewPolicy()

	if policy.DeadCodeMinConfidence != 0.8 {
		t.Errorf("expected DeadCodeMinConfidence 0.8, got %f", policy.DeadCodeMinConfidence)
	}
	if policy.TestGapMinLines != 5 {
		t.Errorf("expected TestGapMinLines 5, got %d", policy.TestGapMinLines)
	}
}
