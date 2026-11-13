package query

import (
	"errors"
	"strings"
	"testing"
)

func TestAPIChangesFromContracts(t *testing.T) {
	contracts := []ContractSignal{
		{Symbol: "Price", File: "lib/acct.go", Kind: "signature-changed", ConsumerCount: 2, Before: "func Price(m string)", After: "func Price(m string, t Table)"},
		{Symbol: "Old", File: "lib/acct.go", Kind: "definition-removed"},
		{Symbol: "Loose", File: "lib/acct.go", Kind: "compatible-signature-change"},
		{Symbol: "price", File: "exp/lc1_account.py", Kind: "possible-contract-change"},
		{Symbol: "simulate", File: "exp/lc1_account.py", Kind: "possible-contract-change"},
		{Symbol: "Helper", File: "lib/acct_test.go", Kind: "signature-changed"},
		{Symbol: "_load", File: "exp/roi.py", Kind: "signature-changed"},
		{Symbol: "parse", File: "lib/acct.go", Kind: "definition-removed"},
	}
	changes, notCompared := apiChangesFromContracts(contracts, nil, false, true)
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want Price and Old", changes)
	}
	if changes[0].SymbolName != "Price" || !changes[0].AffectsUsers || !strings.Contains(changes[0].Description, "2 file(s) still use") {
		t.Errorf("Price = %+v", changes[0])
	}
	if changes[1].SymbolName != "Old" || changes[1].AffectsUsers {
		t.Errorf("Old = %+v", changes[1])
	}
	if strings.Join(notCompared, ",") != "exp/lc1_account.py" {
		t.Errorf("notCompared = %v", notCompared)
	}

	minor, _ := apiChangesFromContracts(contracts, []string{"lib/"}, true, true)
	if len(minor) != 3 {
		t.Errorf("with includeMinor and scope lib/: %d changes, want 3", len(minor))
	}
	// The review counts private symbols too: a use left behind breaks all the same.
	if all, _ := apiChangesFromContracts(contracts, nil, false, false); len(all) != 4 {
		t.Errorf("without ignorePrivate: %d changes, want Price, Old, _load and parse", len(all))
	}
}

func TestPrivateByName(t *testing.T) {
	for _, tt := range []struct {
		symbol, file string
		want         bool
	}{
		{"_load", "a.py", true}, {"__secret", "a.py", true}, {"__init__", "a.py", false}, {"price", "a.py", false},
		{"Account._bill", "a.py", true}, {"parse", "a.go", true}, {"Parse", "a.go", false}, {"_x", "a.ts", false},
	} {
		if got := privateByName(tt.symbol, tt.file); got != tt.want {
			t.Errorf("privateByName(%q, %q) = %v", tt.symbol, tt.file, got)
		}
	}
}

// The review's breaking check fails on a use left behind, warns when every use was
// updated, and skips (never passes) when declarations changed that it can't compare.
func TestCheckBreakingChanges_Statuses(t *testing.T) {
	e := &Engine{}
	run := func(contracts ...ContractSignal) (string, string) {
		c, _ := e.checkBreakingChanges(func() (*AnalyzeChangeSetResponse, error) {
			return &AnalyzeChangeSetResponse{Contracts: contracts}, nil
		})
		return c.Status, c.Summary
	}
	if status, _ := run(ContractSignal{Symbol: "F", File: "a.go", Kind: "signature-changed", ConsumerCount: 1}); status != "fail" {
		t.Errorf("use left: status %s, want fail", status)
	}
	if status, _ := run(ContractSignal{Symbol: "F", File: "a.go", Kind: "signature-changed"}); status != "warn" {
		t.Errorf("every use updated: status %s, want warn", status)
	}
	if status, summary := run(ContractSignal{Symbol: "price", File: "a.py", Kind: "possible-contract-change"}); status != "skip" || !strings.Contains(summary, "a.py") {
		t.Errorf("python: %s %q, want skip naming a.py", status, summary)
	}
	if status, _ := run(); status != "pass" {
		t.Errorf("nothing changed: status %s, want pass", status)
	}
	c, _ := e.checkBreakingChanges(func() (*AnalyzeChangeSetResponse, error) { return nil, errors.New("no git") })
	if c.Status != "skip" {
		t.Errorf("change set failed: status %s, want skip", c.Status)
	}
}
