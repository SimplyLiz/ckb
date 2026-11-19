package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/query"
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

func TestFormatChangesHuman_NotAssessedRisk(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary:   &ChangeSummaryCLI{SymbolsChanged: 43, EstimatedRisk: query.RiskLevelNotAssessed},
		RiskScore: &RiskScoreCLI{Level: query.RiskLevelNotAssessed, Explanation: "no SCIP index"},
	}
	out := formatChangesHuman(resp, false)
	for _, want := range []string{"Risk: NOT ASSESSED", "no SCIP index", "43 changed symbols", "downstream consumers unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"LOW", "0 downstream", "(0.00)"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output contains %q:\n%s", unwanted, out)
		}
	}
}

func TestFormatChangeSet_NotAssessedRisk(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary:     &ChangeSummaryCLI{FilesChanged: 1, SymbolsChanged: 1, EstimatedRisk: query.RiskLevelNotAssessed},
		RiskScore:   &RiskScoreCLI{Level: query.RiskLevelNotAssessed, Explanation: "no SCIP index"},
		BlastRadius: &BlastRadiusCLI{RiskLevel: query.RiskLevelNotAssessed},
	}

	human := formatChangeSetHuman(resp)
	for _, want := range []string{"Risk: NOT ASSESSED", "no SCIP index", "Downstream Impact: not assessed"} {
		if !strings.Contains(human, want) {
			t.Errorf("human output misses %q:\n%s", want, human)
		}
	}
	for _, unwanted := range []string{"✓", "score:", "not_assessed", "Callers: 0", "Direct Impact:"} {
		if strings.Contains(human, unwanted) {
			t.Errorf("human output contains %q:\n%s", unwanted, human)
		}
	}

	markdown := formatImpactMarkdown(resp)
	for _, want := range []string{"**NOT ASSESSED**", "Downstream Impact | not assessed"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("markdown misses %q:\n%s", want, markdown)
		}
	}
	for _, unwanted := range []string{"NOT_ASSESSED", "unique callers", "Directly Affected", "🟢"} {
		if strings.Contains(markdown, unwanted) {
			t.Errorf("markdown contains %q:\n%s", unwanted, markdown)
		}
	}
}

func TestFormatChangeSetHuman_AssessedRiskUnchanged(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary:     &ChangeSummaryCLI{DirectlyAffected: 2, EstimatedRisk: "low"},
		RiskScore:   &RiskScoreCLI{Level: "low", Score: 0.17},
		BlastRadius: &BlastRadiusCLI{UniqueCallerCount: 2},
	}
	out := formatChangeSetHuman(resp)
	for _, want := range []string{"✓ Risk Level: low (score: 0.17)", "Direct Impact:    2 symbols", "Callers: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("output misses %q:\n%s", want, out)
		}
	}
}

func TestLooksLikeSCIPID(t *testing.T) {
	for id, want := range map[string]bool{
		"scip-go gomod example.com/demo abc `example.com/demo/lib`/Target().": true,
		"rust-analyzer cargo demo 0.1.0 target().":                            true,
		"semanticdb maven maven/com.example/demo 1.0 com/example/Main#run().": true,
		"cxx . . $ demo/helper(abc123).":                                      true,
		"local 12":                                                            true,
		"Target":                                                              false,
		"find the handler":                                                    false,
	} {
		if got := looksLikeSCIPID(id); got != want {
			t.Errorf("looksLikeSCIPID(%q) = %v, want %v", id, got, want)
		}
	}
}

// A cut consumer list must not look complete, in either output.
func TestChangeSetTruncationIsVisible(t *testing.T) {
	resp := convertChangeSetResponse(&query.AnalyzeChangeSetResponse{
		Truncated:      true,
		TruncationInfo: &query.TruncationInfo{Reason: "max-items", OriginalCount: 36, ReturnedCount: 20},
		ConsumerFiles: &query.ConsumerFiles{
			Direct: []string{"cmd/ckb/serve.go"}, Transitive: []string{"cmd/ckb/main.go"}, Tests: []string{"internal/api/api_test.go"}, Total: 3,
		},
		AffectedTests: []query.AffectedTest{{FilePath: "internal/api/api_test.go", Reason: "direct", TestNames: []string{"TestServer"}}},
	})

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"truncated":true`, `"originalCount":36`, `"consumerFiles":`, `"direct":["cmd/ckb/serve.go"]`, `"testNames":["TestServer"]`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JSON lacks %s: %s", want, data)
		}
	}

	for name, out := range map[string]string{
		"changes": formatChangesHuman(resp, false),
		"diff":    formatChangeSetHuman(resp),
	} {
		for _, want := range []string{"cut to 20 of 36", "cmd/ckb/serve.go", "cmd/ckb/main.go"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s output lacks %q:\n%s", name, want, out)
			}
		}
	}
}

func TestImpactTruncationIsVisible(t *testing.T) {
	resp := convertImpactResponse("sym", &query.AnalyzeImpactResponse{
		Truncated:      true,
		TruncationInfo: &query.TruncationInfo{Reason: "max-items", OriginalCount: 41, ReturnedCount: 20},
		ConsumerFiles:  &query.ConsumerFiles{Direct: []string{"a/a.go"}, Total: 1},
	})
	out, err := formatImpactHuman(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cut to 20 of 41") || !strings.Contains(out, "a/a.go") {
		t.Errorf("output hides the cut:\n%s", out)
	}
}

// `Risk: LOW` over an empty diff reads as a verdict on a change that doesn't exist.
func TestNoChangesSaysSoInsteadOfARiskLevel(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary:   &ChangeSummaryCLI{EstimatedRisk: query.RiskLevelNotAssessed},
		RiskScore: &RiskScoreCLI{Level: query.RiskLevelNotAssessed, Explanation: query.NothingToAssess},
	}
	for name, out := range map[string]string{
		"changes":  formatChangesHuman(resp, false),
		"diff":     formatChangeSetHuman(resp),
		"markdown": formatImpactMarkdown(resp),
	} {
		if !strings.Contains(out, "Nothing to assess") {
			t.Errorf("%s:\n%s", name, out)
		}
		if strings.Contains(out, "Risk") && name != "diff" {
			t.Errorf("%s shows a risk over nothing:\n%s", name, out)
		}
	}
}

func TestDoneCheckExitCode(t *testing.T) {
	tests := []struct {
		check *query.DoneCheck
		want  int
	}{
		{&query.DoneCheck{Status: query.DoneCheckClear}, 0},
		{&query.DoneCheck{Status: query.DoneCheckOpen}, 2},
		{&query.DoneCheck{Status: query.DoneCheckNotAssessed}, 3},
		{&query.DoneCheck{Status: "something new"}, 3},
		{nil, 3},
	}
	for _, tt := range tests {
		if got := doneCheckExitCode(tt.check); got != tt.want {
			t.Errorf("doneCheckExitCode(%+v) = %d, want %d", tt.check, got, tt.want)
		}
	}
}

func TestWriteDoneCheck(t *testing.T) {
	var b strings.Builder
	writeDoneCheck(&b, &query.DoneCheck{
		Status:       query.DoneCheckOpen,
		OpenCount:    2,
		UpdatedCount: 1,
		OpenSites: []query.OpenSite{
			{Symbol: "Save", Change: "changed", File: "b/b.go", Line: 6, Code: "lib.Save(1)"},
			{Symbol: "Save", Change: "changed", File: "c/c.go", Line: 9, Code: "other()", Unverified: true},
		},
		Note: "Text comparison.",
	}, false)
	out := b.String()
	for _, want := range []string{"Done check: OPEN", "2 uses left as they were", "b/b.go:6  lib.Save(1)", "c/c.go:9", "line may be off", "Text comparison."} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(strings.ToLower(out), "safe") {
		t.Errorf("the check must not call anything safe:\n%s", out)
	}

	// The ones that rarely break are a count per symbol, and --verbose lists them.
	rarely := &query.DoneCheck{
		Status: query.DoneCheckOpen, OpenCount: 2, RarelyCount: 2,
		RarelySites: []query.OpenSite{
			{Symbol: "s.key", Change: "changed", File: "a.ts", Line: 3, Code: "s.key", Use: "read"},
			{Symbol: "s.key", Change: "changed", File: "b.ts", Line: 4, Code: "s.key", Use: "read"},
		},
		Rarely: []query.RarelyGroup{{Symbol: "s.key", Change: "changed", Count: 2, Files: 2, Uses: map[string]int{"read": 2}}},
	}
	b.Reset()
	writeDoneCheck(&b, rarely, false)
	if out := b.String(); !strings.Contains(out, "2 more rarely break, counted per symbol (--verbose lists them)") || !strings.Contains(out, "s.key: 2 in 2 files") || strings.Contains(out, "a.ts:3") || strings.Contains(out, "and 0 more") {
		t.Errorf("rarely, default:\n%s", out)
	}
	b.Reset()
	writeDoneCheck(&b, rarely, true)
	if out := b.String(); !strings.Contains(out, "a.ts:3  s.key") || !strings.Contains(out, "b.ts:4") {
		t.Errorf("rarely, verbose:\n%s", out)
	}

	b.Reset()
	writeDoneCheck(&b, nil, false)
	if b.Len() != 0 {
		t.Errorf("no check should print nothing, got %q", b.String())
	}
}

// Three changed Priority methods: the contract lines and the done check's sites say
// whose, and a qualified name of twenty characters still has a space before its file.
func TestFormatChangesHuman_QualifiedNames(t *testing.T) {
	resp := &ChangeSetResponseCLI{
		Summary: &ChangeSummaryCLI{SymbolsChanged: 2},
		ChangedSymbols: []ChangedSymbolCLI{
			{Name: "Priority", QualifiedName: "Backend.Priority", File: "internal/backends/backend.go"},
			{Name: "Priority", QualifiedName: "SCIPAdapter.Priority", File: "internal/backends/scip/adapter.go"},
		},
		Contracts: []ContractSignalCLI{
			{Symbol: "Priority", File: "internal/backends/backend.go", Kind: "definition-removed"},
			{Symbol: "Priority", File: "internal/backends/scip/adapter.go", Kind: "definition-removed"},
		},
		DoneCheck: &query.DoneCheck{Status: "open", OpenCount: 1, OpenSites: []query.OpenSite{
			{Symbol: "Priority", QualifiedName: "SCIPAdapter.Priority", Change: "removed", File: "internal/backends/scip/adapter_test.go", Line: 9, Code: "a.Priority()"},
		}},
	}
	out := formatChangesHuman(resp, false)
	for _, want := range []string{
		"  Backend.Priority    internal/backends/backend.go\n",
		"  SCIPAdapter.Priority internal/backends/scip/adapter.go\n",
		"[SCIPAdapter.Priority removed]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}
