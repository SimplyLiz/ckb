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
