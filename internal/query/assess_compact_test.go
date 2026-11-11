package query

import (
	"encoding/json"
	"strings"
	"testing"
)

// The compact view keeps what an agent acts on and drops the symbol ids that made
// the full response large.
func TestCompactAssessChangeFrom(t *testing.T) {
	id := "scip-go gomod github.com/SimplyLiz/CodeMCP 67d776584a29 `github.com/SimplyLiz/CodeMCP/internal/query`/NewEngine()."
	tests := make([]AffectedTest, compactTestFiles+3)
	for i := range tests {
		tests[i] = AffectedTest{FilePath: "a_test.go", TestNames: []string{"TestA"}, AffectedBy: []string{id}, Reason: "direct"}
	}
	r := &AnalyzeChangeSetResponse{
		Summary:   &ChangeSummary{FilesChanged: 1, SymbolsChanged: 1, DirectlyAffected: 2, TransitivelyAffected: 3, EstimatedRisk: "high"},
		RiskScore: &RiskScore{Level: "high", Score: 0.8},
		DoneCheck: &DoneCheck{Status: "open", Diff: "36b79ae71709", OpenCount: 1, Note: "n", OpenSites: []OpenSite{
			{Symbol: "NewEngine", Change: "changed", File: "cmd/ckb/engine_helper.go", Line: 58, Code: "query.NewEngine(repoRoot, db, logger, cfg)", Consumer: "getEngine"},
		}},
		ChangedSymbols:  []ChangedSymbolInfo{{SymbolID: id, Name: "NewEngine"}},
		AffectedSymbols: []ImpactItem{{StableId: id, Name: "getEngine"}},
		AffectedTests:   tests,
		Contracts:       []ContractSignal{{Symbol: "NewEngine", File: "internal/query/engine.go", Kind: "signature-changed", Basis: "heuristic: x"}},
		ConsumerFiles:   &ConsumerFiles{Direct: []string{"cmd/ckb/engine_helper.go", "internal/mcp/server.go"}, Total: 4},
		IndexStaleness:  &IndexStalenessInfo{IsStale: true, StalenessMessage: "uncommitted"},
	}

	c := CompactAssessChangeFrom(r)
	if c.Risk != "high" || c.RiskScore != 0.8 {
		t.Errorf("risk = %s/%v", c.Risk, c.RiskScore)
	}
	if c.DoneCheck == nil || len(c.DoneCheck.OpenSites) != 1 || c.DoneCheck.OpenSites[0].At != "cmd/ckb/engine_helper.go:58" || c.DoneCheck.OpenSites[0].Consumer != "getEngine" {
		t.Errorf("doneCheck = %+v", c.DoneCheck)
	}
	if len(c.Tests) != compactTestFiles || c.TestsTotal != compactTestFiles+3 || c.Tests[0].Names[0] != "TestA" {
		t.Errorf("tests = %d of %d", len(c.Tests), c.TestsTotal)
	}
	if len(c.Contracts) != 1 || c.Contracts[0].Kind != "signature-changed" {
		t.Errorf("contracts = %+v", c.Contracts)
	}
	if c.ConsumersTotal != 2 || c.Stale != "uncommitted" {
		t.Errorf("consumers = %d, stale = %q", c.ConsumersTotal, c.Stale)
	}
	if !strings.Contains(c.Summary, "1 use of a changed declaration left open") {
		t.Errorf("summary = %q", c.Summary)
	}

	out, _ := json.Marshal(c)
	if strings.Contains(string(out), "scip-go") {
		t.Errorf("compact output carries a symbol id: %s", out)
	}

	if got := CompactAssessChangeFrom(nil); got.Summary == "" {
		t.Error("nil response gives no summary")
	}
}

func focusFixture(sites, tests int) *AnalyzeChangeSetResponse {
	r := &AnalyzeChangeSetResponse{
		Summary:       &ChangeSummary{FilesChanged: 1, SymbolsChanged: 1, DirectlyAffected: 2},
		RiskScore:     &RiskScore{Level: "high", Score: 0.6, Factors: []RiskFactor{{Name: "signature_changed_with_consumers", Evidence: "1 symbol(s) changed"}}},
		DoneCheck:     &DoneCheck{Status: "open", OpenCount: sites},
		Contracts:     []ContractSignal{{Symbol: "NewEngine", Kind: "signature-changed"}},
		ConsumerFiles: &ConsumerFiles{Direct: []string{"a.go"}},
		CoChangeCheck: &CoChangeCheck{OpenFiles: []CoChangeFile{{File: "b.go"}}},
	}
	for i := 0; i < sites; i++ {
		r.DoneCheck.OpenSites = append(r.DoneCheck.OpenSites, OpenSite{File: "s.go", Line: i + 1})
	}
	for i := 0; i < tests; i++ {
		r.AffectedTests = append(r.AffectedTests, AffectedTest{FilePath: "t_test.go"})
	}
	return r
}

// Each focus returns only its sections; the summary still covers everything.
func TestCompactAssessChangeFor_Focus(t *testing.T) {
	r := focusFixture(3, 2)

	done, err := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone})
	if err != nil {
		t.Fatal(err)
	}
	if done.DoneCheck == nil || len(done.DoneCheck.OpenSites) != 3 || len(done.CoChangeOpen) != 1 {
		t.Errorf("done: want 3 open sites and the co-change file, got %+v", done)
	}
	if len(done.Tests) != 0 || len(done.Contracts) != 0 || len(done.Consumers) != 0 || len(done.RiskFactors) != 0 {
		t.Errorf("done carries other sections: %+v", done)
	}
	if !strings.Contains(done.Summary, "2 test files") {
		t.Errorf("summary should still count the tests: %q", done.Summary)
	}

	tests, err := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusTests, RunCommand: "go test ./x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tests.Tests) != 2 || tests.RunTests != "go test ./x" || tests.DoneCheck != nil || len(tests.CoChangeOpen) != 0 {
		t.Errorf("tests: %+v", tests)
	}
	if !strings.Contains(tests.Summary, "3 uses") {
		t.Errorf("summary should still say what is open: %q", tests.Summary)
	}

	risk, err := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusRisk})
	if err != nil {
		t.Fatal(err)
	}
	if len(risk.RiskFactors) != 1 || len(risk.Contracts) != 1 || len(risk.Consumers) != 1 || risk.DoneCheck != nil || len(risk.Tests) != 0 {
		t.Errorf("risk: %+v", risk)
	}

	// No focus: every section, no risk factors, no run command.
	all, _ := CompactAssessChangeFor(r, CompactAssessOptions{RunCommand: "go test ./x"})
	if all.DoneCheck == nil || len(all.Tests) != 2 || len(all.Contracts) != 1 || all.RunTests != "" || len(all.RiskFactors) != 0 {
		t.Errorf("no focus: %+v", all)
	}
}

// offset and limit page the focused list, and more names the next offset.
func TestCompactAssessChangeFor_Paging(t *testing.T) {
	r := focusFixture(compactOpenSites+5, 0)

	first, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone})
	if len(first.DoneCheck.OpenSites) != compactOpenSites || !strings.Contains(first.More, "offset 50") {
		t.Fatalf("first page: %d sites, more %q", len(first.DoneCheck.OpenSites), first.More)
	}
	next, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone, Offset: 50})
	if len(next.DoneCheck.OpenSites) != 5 || next.DoneCheck.OpenSites[0].At != "s.go:51" || next.More != "" {
		t.Errorf("second page: %+v, more %q", next.DoneCheck.OpenSites, next.More)
	}
	small, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone, Offset: 2, Limit: 2})
	if len(small.DoneCheck.OpenSites) != 2 || small.DoneCheck.OpenSites[0].At != "s.go:3" || !strings.Contains(small.More, "offset 4") {
		t.Errorf("offset 2 limit 2: %+v, more %q", small.DoneCheck.OpenSites, small.More)
	}
	past, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone, Offset: 999})
	if len(past.DoneCheck.OpenSites) != 0 || past.DoneCheck.OpenCount != compactOpenSites+5 {
		t.Errorf("past the end: %+v", past.DoneCheck)
	}

	r.DoneCheck.Truncated = true
	cut, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusDone, Offset: 50})
	if !strings.Contains(cut.More, "fix those and call again") {
		t.Errorf("a truncated check should say so: %q", cut.More)
	}
}

func TestCompactAssessChangeFor_InvalidOptions(t *testing.T) {
	for _, opts := range []CompactAssessOptions{
		{Focus: "everything"},
		{Offset: 5},
		{Focus: AssessFocusRisk, Offset: 1},
		{Focus: AssessFocusDone, Limit: -1},
	} {
		if _, err := CompactAssessChangeFor(nil, opts); err == nil {
			t.Errorf("%+v: want an error", opts)
		}
	}
}
