package main

import (
	"strings"
	"testing"
)

func syntheticChangesResponse() *ChangeSetResponseCLI {
	return &ChangeSetResponseCLI{
		Change: &ChangeInfoCLI{
			Branch:       "feature/session-rotation",
			Mode:         "working-tree",
			FilesChanged: 5,
		},
		Summary: &ChangeSummaryCLI{
			FilesChanged:         5,
			SymbolsChanged:       27,
			DirectlyAffected:     8,
			TransitivelyAffected: 14,
			EstimatedRisk:        "high",
		},
		ModulesAffected: []ModuleImpactCLI{
			{ModuleID: "internal/mail", ModuleName: "internal/mail", ImpactCount: 5},
			{ModuleID: "internal/session", ModuleName: "internal/session", ImpactCount: 3},
		},
		RiskScore: &RiskScoreCLI{
			Level: "high",
			Score: 0.78,
			Factors: []RiskFactorCLI{
				{Name: "direct_impact", Weight: 0.30, Value: 0.7, Evidence: "14 consumers across 3 modules"},
				{Name: "module_spread", Weight: 0.25, Value: 0.5, Evidence: "3 modules affected"},
			},
		},
		AffectedTests: []AffectedTestCLI{
			{FilePath: "internal/session/session_rotation_test.go", Reason: "direct"},
			{FilePath: "internal/auth/auth_integration_test.go", Reason: "transitive"},
		},
		Contracts: []ContractSignalCLI{
			{Symbol: "MailService.send()", File: "internal/mail/service.go", Kind: "possible-contract-change", Basis: "heuristic: exported/public symbol lines changed"},
		},
		Decisions: []RelatedDecisionCLI{
			{ID: "ADR-014", Title: "Session lifecycle"},
		},
		Reviewers: []SuggestedReviewCLI{
			{Owner: "Lisa", Coverage: 0.72},
			{Owner: "Sebastian", Coverage: 0.10},
		},
		TestGaps: &TestGapsCLI{
			UntestedConsumers: 3,
			Examples:          []string{"SessionMiddleware.Handle -> KnowledgeContext.resolve()"},
		},
		IndexStaleness: &IndexStalenessCLI{
			IsStale:       true,
			CommitsBehind: 2,
		},
		Confidence: &ChangeConfidenceCLI{
			Score:   0.62,
			Tier:    "medium",
			Reasons: []string{"SCIP index present", "2 commits behind"},
		},
	}
}

func TestFormatChangesHuman_ContainsKeySections(t *testing.T) {
	out := formatChangesHuman(syntheticChangesResponse(), false)

	wantSubstrings := []string{
		"Current change · feature/session-rotation (working tree)",
		"Risk: HIGH (0.78)",
		"weight 0.30",
		"14 consumers across 3 modules",
		"27 changed symbols",
		"8 downstream consumers",
		"2 affected modules",
		"Affected tests (2)",
		"direct",
		"transitive",
		"Possible contract changes (1)",
		"[heuristic: exported/public symbol lines changed]",
		"MailService.send()",
		"Relevant decisions",
		"ADR-014",
		"Session lifecycle",
		"Likely reviewers",
		"Lisa (72% of changed files)",
		"Sebastian",
		"Potential issues",
		"3 downstream consumer(s) have no reaching test",
		"(Consumer -> Changed)",
		"index is 2 commit(s) behind",
		"Confidence: medium",
		"SCIP index present, 2 commits behind",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}

	// Never call the contracts heuristic a "breaking change".
	if strings.Contains(strings.ToLower(out), "breaking change") {
		t.Errorf("output must never say 'breaking change', got:\n%s", out)
	}

	// Non-verbose mode should not print test-gap examples inline.
	if strings.Contains(out, "SessionMiddleware.Handle -> KnowledgeContext.resolve()") {
		t.Errorf("non-verbose output should not include test-gap examples, got:\n%s", out)
	}
}

func TestFormatChangesHuman_VerboseShowsExamples(t *testing.T) {
	out := formatChangesHuman(syntheticChangesResponse(), true)

	if !strings.Contains(out, "SessionMiddleware.Handle -> KnowledgeContext.resolve()") {
		t.Errorf("verbose output should include test-gap examples, got:\n%s", out)
	}
}

func TestFormatChangesHuman_CapsListsAtTenUnlessVerbose(t *testing.T) {
	resp := syntheticChangesResponse()
	resp.AffectedTests = nil
	for i := 0; i < 13; i++ {
		resp.AffectedTests = append(resp.AffectedTests, AffectedTestCLI{
			FilePath: "pkg/file_test.go",
			Reason:   "direct",
		})
	}

	capped := formatChangesHuman(resp, false)
	if !strings.Contains(capped, "… and 3 more") {
		t.Errorf("expected capped output to note 3 more affected tests, got:\n%s", capped)
	}

	full := formatChangesHuman(resp, true)
	if strings.Contains(full, "… and 3 more") {
		t.Errorf("verbose output should not cap affected tests, got:\n%s", full)
	}
}

func TestFormatChangesHuman_StagedMode(t *testing.T) {
	resp := syntheticChangesResponse()
	resp.Change.Mode = "staged"

	out := formatChangesHuman(resp, false)
	if !strings.Contains(out, "(staged)") {
		t.Errorf("expected staged mode label, got:\n%s", out)
	}
}

func TestFormatChangesHuman_RangeMode(t *testing.T) {
	resp := syntheticChangesResponse()
	resp.Change.Mode = "range"
	resp.Change.Base = "main"

	out := formatChangesHuman(resp, false)
	if !strings.Contains(out, "(vs main)") {
		t.Errorf("expected range mode label 'vs main', got:\n%s", out)
	}
}
