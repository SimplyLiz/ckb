package query

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCompactReview(t *testing.T) {
	r := &ReviewPRResponse{
		Verdict: "warn",
		Score:   71,
		Checks: []ReviewCheck{
			{Name: "secrets", Status: "pass"},
			{Name: "breaking", Status: "fail", Summary: "1 removed export"},
			{Name: "coupling", Status: "warn", Summary: "2 files"},
		},
		ReviewEffort: &ReviewEffort{EstimatedHours: 1.5, Complexity: "moderate"},
		Reviewers: []SuggestedReview{
			{Owner: "a", ExpertiseArea: "internal/query"}, {Owner: "b"}, {Owner: "c"}, {Owner: "d"},
		},
	}
	for i := 0; i < 5; i++ {
		r.Findings = append(r.Findings, ReviewFinding{Check: "breaking", Severity: "error", File: fmt.Sprintf("e%d.go", i)})
	}
	for i := 0; i < 4; i++ {
		r.Findings = append(r.Findings, ReviewFinding{Check: "coupling", Severity: "warning", File: fmt.Sprintf("w%d.go", i)})
	}
	r.Findings = append(r.Findings, ReviewFinding{Check: "hotspots", Severity: "info", File: "i.go"})

	got := CompactReview(r, 6)
	if f, _ := got["findings"].([]map[string]interface{}); len(f) != 6 || f[0]["file"] != "e0.go" || f[5]["file"] != "w0.go" {
		t.Errorf("findings = %v, want the first 6 in order", f)
	}
	if got["remainingFindings"] != 4 {
		t.Errorf("remainingFindings = %v, want 4", got["remainingFindings"])
	}
	if by, _ := got["remainingBySeverity"].(map[string]int); by["warning"] != 3 || by["info"] != 1 || by["error"] != 0 {
		t.Errorf("remainingBySeverity = %v", by)
	}
	if active, _ := got["activeChecks"].([]map[string]string); len(active) != 2 || active[0]["name"] != "breaking" {
		t.Errorf("activeChecks = %v", active)
	}
	if passed, _ := got["passedChecks"].([]string); len(passed) != 1 || passed[0] != "secrets" {
		t.Errorf("passedChecks = %v", passed)
	}
	if rv, _ := got["reviewers"].([]map[string]string); len(rv) != 3 || rv[0]["expertiseArea"] != "internal/query" {
		t.Errorf("reviewers = %v, want the first 3", rv)
	}
	if e, _ := got["reviewEffort"].(map[string]interface{}); e["complexity"] != "moderate" {
		t.Errorf("reviewEffort = %v", e)
	}

	// Under the limit: no remaining counts, and empty lists stay [] in JSON.
	small := CompactReview(&ReviewPRResponse{Verdict: "pass", Score: 100}, 30)
	if _, ok := small["remainingFindings"]; ok {
		t.Error("remainingFindings set without a rest")
	}
	b, _ := json.Marshal(small)
	var back map[string]interface{}
	_ = json.Unmarshal(b, &back)
	if _, ok := back["findings"].([]interface{}); !ok {
		t.Errorf("findings must marshal as [], got %s", b)
	}
}
