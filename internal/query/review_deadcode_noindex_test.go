package query

import (
	"context"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

// Without a SCIP index the dead-code check used to pass with "No dead code in
// changed files". It skips when nothing else could look, and says it only
// checked unreferenced exports when Cartographer did.
func TestCheckDeadCodeWithoutIndexIsNotAPass(t *testing.T) {
	engine, cleanup := testEngine(t)
	defer cleanup()

	resp, err := engine.FindDeadCode(context.Background(), FindDeadCodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Analyzed || resp.NotAnalyzedReason == "" {
		t.Fatalf("FindDeadCode without index: analyzed=%v reason=%q", resp.Analyzed, resp.NotAnalyzedReason)
	}

	check, _ := engine.checkDeadCode(context.Background(), []string{"pkg/a.go"}, ReviewPROptions{Policy: DefaultReviewPolicy()})
	if cartographer.Available() {
		if !strings.Contains(check.Summary, "unreferenced exports only") {
			t.Errorf("summary %q doesn't say the check was partial", check.Summary)
		}
		return
	}
	if check.Status != "skip" || !strings.Contains(check.Summary, "Not analyzed") {
		t.Errorf("status %q summary %q, want skip / Not analyzed", check.Status, check.Summary)
	}
}
