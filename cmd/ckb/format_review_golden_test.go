package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

var updateGolden = flag.Bool("update-golden", false, "Update golden files")

const goldenDir = "../../testdata/review"

// goldenResponse returns a rich response exercising all formatter code paths.
func goldenResponse() *query.ReviewPRResponse {
	return &query.ReviewPRResponse{
		CkbVersion:    "8.2.0",
		SchemaVersion: "8.2",
		Tool:          "reviewPR",
		Verdict:       "warn",
		Score:         68,
		Narrative:     "Changes 25 files across 3 modules (Go, TypeScript). 2 breaking API changes detected; 2 safety-critical files changed. 2 safety-critical files need focused review.",
		PRTier:        "medium",
		Summary: query.ReviewSummary{
			TotalFiles:      25,
			TotalChanges:    480,
			GeneratedFiles:  3,
			ReviewableFiles: 22,
			CriticalFiles:   2,
			ChecksPassed:    4,
			ChecksWarned:    2,
			ChecksFailed:    1,
			ChecksSkipped:   1,
			TopRisks:        []string{"2 breaking API changes", "Critical path touched"},
			Languages:       []string{"Go", "TypeScript"},
			ModulesChanged:  3,
		},
		Checks: []query.ReviewCheck{
			{Name: "breaking", Status: "fail", Severity: "error", Summary: "2 breaking API changes detected", Duration: 120},
			{Name: "critical", Status: "fail", Severity: "error", Summary: "2 safety-critical files changed", Duration: 15},
			{Name: "complexity", Status: "warn", Severity: "warning", Summary: "+8 cyclomatic (engine.go)", Duration: 340},
			{Name: "coupling", Status: "warn", Severity: "warning", Summary: "2 missing co-change files", Duration: 210},
			{Name: "secrets", Status: "pass", Severity: "error", Summary: "No secrets detected", Duration: 95},
			{Name: "tests", Status: "pass", Severity: "warning", Summary: "12 tests cover the changes", Duration: 180},
			{Name: "risk", Status: "pass", Severity: "warning", Summary: "Risk score: 0.42 (low)", Duration: 150},
			{Name: "hotspots", Status: "pass", Severity: "info", Summary: "No volatile files touched", Duration: 45},
			{Name: "generated", Status: "info", Severity: "info", Summary: "3 generated files detected and excluded"},
		},
		Findings: []query.ReviewFinding{
			{
				Check:     "breaking",
				Severity:  "error",
				File:      "api/handler.go",
				StartLine: 42,
				Message:   "Removed public function HandleAuth()",
				Category:  "breaking",
				RuleID:    "ckb/breaking/removed-symbol",
				Tier:      1,
			},
			{
				Check:     "breaking",
				Severity:  "error",
				File:      "api/middleware.go",
				StartLine: 15,
				Message:   "Changed signature of ValidateToken()",
				Category:  "breaking",
				RuleID:    "ckb/breaking/changed-signature",
				Tier:      1,
			},
			{
				Check:      "critical",
				Severity:   "error",
				File:       "drivers/hw/plc_comm.go",
				StartLine:  78,
				Message:    "Safety-critical path changed (pattern: drivers/**)",
				Suggestion: "Requires sign-off from safety team",
				Category:   "critical",
				RuleID:     "ckb/critical/safety-path",
				Tier:       1,
			},
			{
				Check:      "critical",
				Severity:   "error",
				File:       "protocol/modbus.go",
				Message:    "Safety-critical path changed (pattern: protocol/**)",
				Suggestion: "Requires sign-off from safety team",
				Category:   "critical",
				RuleID:     "ckb/critical/safety-path",
				Tier:       1,
			},
			{
				Check:      "complexity",
				Severity:   "warning",
				File:       "internal/query/engine.go",
				StartLine:  155,
				EndLine:    210,
				Message:    "Complexity 12→20 in parseQuery()",
				Suggestion: "Consider extracting helper functions",
				Category:   "complexity",
				RuleID:     "ckb/complexity/increase",
				Tier:       2,
			},
			{
				Check:    "coupling",
				Severity: "warning",
				File:     "internal/query/engine.go",
				Message:  "Missing co-change: engine_test.go (87% co-change rate)",
				Category: "coupling",
				RuleID:   "ckb/coupling/missing-cochange",
				Tier:     2,
			},
			{
				Check:    "coupling",
				Severity: "warning",
				File:     "protocol/modbus.go",
				Message:  "Missing co-change: modbus_test.go (91% co-change rate)",
				Category: "coupling",
				RuleID:   "ckb/coupling/missing-cochange",
				Tier:     2,
			},
			{
				Check:    "hotspots",
				Severity: "info",
				File:     "config/settings.go",
				Message:  "Hotspot file (score: 0.78) — extra review attention recommended",
				Category: "risk",
				RuleID:   "ckb/hotspots/volatile-file",
				Tier:     3,
			},
		},
		Reviewers: []query.SuggestedReview{
			{Owner: "alice", Coverage: 0.85, Confidence: 0.9},
			{Owner: "bob", Coverage: 0.45, Confidence: 0.7},
		},
		Generated: []query.GeneratedFileInfo{
			{File: "api/types.pb.go", Reason: "Matches pattern *.pb.go", SourceFile: "api/types.proto"},
			{File: "parser/parser.tab.c", Reason: "flex/yacc generated output", SourceFile: "parser/parser.y"},
			{File: "ui/generated.ts", Reason: "Matches pattern *.generated.*"},
		},
		SplitSuggestion: &query.PRSplitSuggestion{
			ShouldSplit: true,
			Reason:      "25 files across 3 independent clusters — split recommended",
			Clusters: []query.PRCluster{
				{Name: "API Handler Refactor", Files: []string{"api/handler.go", "api/middleware.go"}, FileCount: 8, Additions: 240, Deletions: 120, Independent: true},
				{Name: "Protocol Update", Files: []string{"protocol/modbus.go"}, FileCount: 5, Additions: 130, Deletions: 60, Independent: true},
				{Name: "Driver Changes", Files: []string{"drivers/hw/plc_comm.go"}, FileCount: 12, Additions: 80, Deletions: 30, Independent: false},
			},
		},
		ChangeBreakdown: &query.ChangeBreakdown{
			Summary: map[string]int{
				"new":         5,
				"modified":    10,
				"refactoring": 3,
				"test":        4,
				"generated":   3,
			},
		},
		ReviewEffort: &query.ReviewEffort{
			EstimatedMinutes: 95,
			EstimatedHours:   1.58,
			Complexity:       "complex",
			Factors: []string{
				"22 reviewable files (44min base)",
				"3 module context switches (15min)",
				"2 safety-critical files (20min)",
			},
		},
		HealthReport: &query.CodeHealthReport{
			Deltas: []query.CodeHealthDelta{
				{File: "api/handler.go", HealthBefore: 82, HealthAfter: 70, Delta: -12, Grade: "B", GradeBefore: "B", TopFactor: "significant health degradation", Confidence: 1.0, Parseable: true},
				{File: "internal/query/engine.go", HealthBefore: 75, HealthAfter: 68, Delta: -7, Grade: "C", GradeBefore: "B", TopFactor: "minor health decrease", Confidence: 0.8, Parseable: true},
				{File: "protocol/modbus.go", HealthBefore: 60, HealthAfter: 65, Delta: 5, Grade: "C", GradeBefore: "C", TopFactor: "unchanged", Confidence: 1.0, Parseable: true},
			},
			AverageDelta: -4.67,
			WorstFile:    "protocol/modbus.go",
			WorstGrade:   "C",
			Degraded:     2,
			Improved:     1,
		},
	}
}

func TestGolden_Human(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output := formatReviewHuman(resp)
	checkGolden(t, "human.txt", output)
}

func TestGolden_Markdown(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output := formatReviewMarkdown(resp)
	checkGolden(t, "markdown.md", output)
}

func TestGolden_GitHubActions(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output := formatReviewGitHubActions(resp)
	checkGolden(t, "github-actions.txt", output)
}

func TestGolden_SARIF(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output, err := formatReviewSARIF(resp)
	if err != nil {
		t.Fatalf("formatReviewSARIF: %v", err)
	}
	// Normalize: re-marshal with sorted keys for stable output
	var parsed interface{}
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("unmarshal SARIF: %v", err)
	}
	// The driver version is version.Version, so pinning it in the fixture would
	// make every release bump break this test and turn a golden file into a
	// place the version has to be maintained. Only the tool's own version is
	// replaced — runs[].tool.driver — never the SARIF schema version at the top
	// level, which is genuinely fixed.
	normalizeDriverVersion(t, parsed, "0.0.0-golden")
	normalized, _ := json.MarshalIndent(parsed, "", "  ")
	checkGolden(t, "sarif.json", string(normalized))
}

func TestGolden_CodeClimate(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output, err := formatReviewCodeClimate(resp)
	if err != nil {
		t.Fatalf("formatReviewCodeClimate: %v", err)
	}
	checkGolden(t, "codeclimate.json", output)
}

func TestGolden_JSON(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output, err := formatJSON(resp)
	if err != nil {
		t.Fatalf("formatJSON: %v", err)
	}
	checkGolden(t, "json.json", output)
}

func TestGolden_Compliance(t *testing.T) {
	t.Parallel()
	resp := goldenResponse()
	output := formatReviewCompliance(resp)
	// Normalize the timestamp line which changes every run.
	output = regexp.MustCompile(`(?m)^Generated:.*$`).ReplaceAllString(output, "Generated:   <TIMESTAMP>")
	checkGolden(t, "compliance.txt", output)
}

func checkGolden(t *testing.T, filename, actual string) {
	t.Helper()
	path := filepath.Join(goldenDir, filename)

	if *updateGolden {
		if err := os.WriteFile(path, []byte(actual), 0644); err != nil {
			t.Fatalf("write golden file: %v", err)
		}
		t.Logf("Updated golden file: %s", path)
		return
	}

	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Golden file %s not found. Run with -update-golden to create it.\n%v", path, err)
	}

	// Normalize line endings
	expectedStr := strings.ReplaceAll(string(expected), "\r\n", "\n")
	actualStr := strings.ReplaceAll(actual, "\r\n", "\n")

	if expectedStr != actualStr {
		// Show first difference
		expLines := strings.Split(expectedStr, "\n")
		actLines := strings.Split(actualStr, "\n")
		for i := 0; i < len(expLines) || i < len(actLines); i++ {
			exp := ""
			act := ""
			if i < len(expLines) {
				exp = expLines[i]
			}
			if i < len(actLines) {
				act = actLines[i]
			}
			if exp != act {
				t.Errorf("Golden file mismatch at line %d:\n  expected: %q\n  actual:   %q\n\nRun with -update-golden to update.", i+1, exp, act)
				return
			}
		}
	}
}

// normalizeDriverVersion replaces runs[].tool.driver.{version,semanticVersion}
// in a decoded SARIF document with a fixed placeholder.
func normalizeDriverVersion(t *testing.T, doc interface{}, placeholder string) {
	t.Helper()
	root, ok := doc.(map[string]interface{})
	if !ok {
		t.Fatalf("SARIF root is %T, want object", doc)
	}
	runs, ok := root["runs"].([]interface{})
	if !ok || len(runs) == 0 {
		t.Fatalf("SARIF has no runs to normalize")
	}
	replaced := 0
	for _, r := range runs {
		run, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		tool, ok := run["tool"].(map[string]interface{})
		if !ok {
			continue
		}
		driver, ok := tool["driver"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, key := range []string{"version", "semanticVersion"} {
			if _, present := driver[key]; present {
				driver[key] = placeholder
				replaced++
			}
		}
	}
	// Guard against the formatter renaming or dropping the fields, which would
	// otherwise make this normalization silently vacuous.
	if replaced == 0 {
		t.Fatal("no driver version fields found to normalize — did the SARIF formatter change?")
	}
}
