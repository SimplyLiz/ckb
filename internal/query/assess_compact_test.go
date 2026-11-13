package query

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/diff"
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

// The compact answer is text: sites under their symbol and file, one line each, with
// no key written twice and nothing escaped.
func TestCompactAssessChange_Text(t *testing.T) {
	id := "scip-typescript npm m 1.0.0 src/`schema.ts`/Profile#"
	r := &AnalyzeChangeSetResponse{
		Summary:   &ChangeSummary{FilesChanged: 1, SymbolsChanged: 2, DirectlyAffected: 3, EstimatedRisk: "high"},
		RiskScore: &RiskScore{Level: "high", Score: 0.8},
		DoneCheck: &DoneCheck{Status: "open", Diff: "36b79ae71709", OpenCount: 4, Note: "Uses the diff didn't touch.", OpenSites: []OpenSite{
			{Symbol: "ListKind", Change: "changed", File: "src/blocks.tsx", Line: 60, Code: `return <K extends ListKind>(kind: K) => a && b`, Consumer: "useEdit"},
			{Symbol: "ListKind", Change: "changed", File: "src/blocks.tsx", Line: 91, Code: "function EntryTools({ kind }: { kind: ListKind }) {"},
			{Symbol: "Profile", Change: "changed", File: "src/sample.ts", Line: 10, Code: "export const sampleProfile: Profile = {", Via: "Profile is typeof profileSchema, whose value changed"},
			{Symbol: "ListKind", Change: "changed", File: "tests/add.test.ts", Line: 5, Code: "const kinds: ListKind[] = [];", IsTest: true},
		}},
		AffectedTests: []AffectedTest{
			{FilePath: "tests/add.test.ts", TestNames: []string{"adds an entry", "keeps the order"}, AffectedBy: []string{id}, Reason: "direct"},
			{FilePath: "tests/far.test.ts", TestNames: []string{"renders"}, Reason: "transitive"},
			{FilePath: "tests/further.test.ts", Reason: "transitive"},
		},
		Contracts: []ContractSignal{{Symbol: "blankEntry", File: "src/entries.ts", Kind: "signature-changed", Basis: "heuristic: x",
			Before: "export function blankEntry(kind: ListKind)", After: "export function blankEntry(kind: ListKind, id: string)"}},
		ConsumerFiles: &ConsumerFiles{Direct: []string{"src/blocks.tsx", "src/sample.ts"}},
		CoChangeCheck: &CoChangeCheck{OpenFiles: []CoChangeFile{{File: "src/store.ts"}}},
	}
	text := CompactAssessChangeFrom(r).Text()

	sites := "ListKind (changed)\n" +
		"  src/blocks.tsx\n" +
		"    60: return <K extends ListKind>(kind: K) => a && b\n" +
		"    91: function EntryTools({ kind }: { kind: ListKind }) {\n" +
		"  tests/add.test.ts (test)\n" +
		"    5: const kinds: ListKind[] = [];\n" +
		"Profile (changed; Profile is typeof profileSchema, whose value changed)\n" +
		"  src/sample.ts\n" +
		"    10: export const sampleProfile: Profile = {\n"
	if !strings.Contains(text, sites) {
		t.Errorf("open sites aren't grouped by symbol and file:\n%s", text)
	}
	for _, want := range []string{
		"doneCheck: open, 4 uses left as they were (diff 36b79ae71709)",
		"signature-changed blankEntry: export function blankEntry(kind: ListKind) -> export function blankEntry(kind: ListKind, id: string)",
		"tests/add.test.ts: adds an entry, keeps the order",
		"2 more test files reach the change through other code",
		"src/store.ts",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	for _, not := range []string{`\u003c`, `"at"`, `"symbol"`, "renders", "scip-typescript", "useEdit"} {
		if strings.Contains(text, not) {
			t.Errorf("text has %q:\n%s", not, text)
		}
	}
	if strings.Count(text, "src/blocks.tsx") != 2 { // Under its symbol, and as a consumer
		t.Errorf("a file is named more often than its two sections need:\n%s", text)
	}

	// With focus tests the page lists every file, however the change reaches it.
	focused, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusTests, RunCommand: "npx vitest run"})
	if text := focused.Text(); !strings.Contains(text, "tests/far.test.ts: renders") || !strings.Contains(text, "run: npx vitest run") {
		t.Errorf("focus tests:\n%s", text)
	}
}

// The sites that rarely break are one line per symbol by default, and their own list
// with focus rarely, paged like the others. Neither view drops one.
func TestCompactAssessChangeFor_Rarely(t *testing.T) {
	var rarely []OpenSite
	for i := range compactOpenSites + 3 {
		use := diff.TSUseRead
		if i%10 == 0 {
			use = diff.TSUseAnnotation
		}
		rarely = append(rarely, OpenSite{Symbol: "sectionSchema.key", Change: "changed", Via: "is wider", File: fmt.Sprintf("r%d.ts", i%4), Line: i + 1, Code: "s.key", Use: use})
	}
	r := &AnalyzeChangeSetResponse{DoneCheck: &DoneCheck{
		Status: "open", OpenCount: len(rarely) + 1, RarelyCount: len(rarely),
		OpenSites:   []OpenSite{{Symbol: "Kind", Change: "changed", File: "k.ts", Line: 3, Code: "const x: Record<Kind, 1> = {}", Use: diff.TSUseKey}},
		RarelySites: rarely,
		Rarely:      []RarelyGroup{{Symbol: "sectionSchema.key", Change: "changed", Via: "is wider", Count: len(rarely), Files: 4, Uses: map[string]int{"read": 47, "annotation": 6}}},
	}}

	c := CompactAssessChangeFrom(r)
	if len(c.DoneCheck.OpenSites) != 1 || c.DoneCheck.RarelyCount != len(rarely) || c.More != "" {
		t.Errorf("default: %d sites, rarely %d, more %q: want the likely one, the count, and no page to fetch", len(c.DoneCheck.OpenSites), c.DoneCheck.RarelyCount, c.More)
	}
	text := c.Text()
	for _, want := range []string{
		`rarely break, 53 counted and not listed (focus "rarely" lists them):`,
		"  sectionSchema.key (changed; is wider): 53 in 4 files, 47 read, 6 annotation\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(c.Summary, "54 uses of a changed declaration left open, 53 of them rarely break.") {
		t.Errorf("summary = %q", c.Summary)
	}
	if strings.Contains(text, "r1.ts") {
		t.Errorf("a site that rarely breaks is listed by default:\n%s", text)
	}

	first, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusRarely})
	if len(first.DoneCheck.OpenSites) != compactOpenSites || first.DoneCheck.OpenSites[0].At != "r0.ts:1" || len(first.DoneCheck.Rarely) != 0 {
		t.Fatalf("focus rarely: %d sites, first %+v, groups %v", len(first.DoneCheck.OpenSites), first.DoneCheck.OpenSites[0], first.DoneCheck.Rarely)
	}
	if !strings.Contains(first.More, `focus "rarely", offset 50`) || len(first.Tests) != 0 || len(first.CoChangeOpen) != 0 {
		t.Errorf("focus rarely: more %q, tests %d, co-change %d", first.More, len(first.Tests), len(first.CoChangeOpen))
	}
	next, _ := CompactAssessChangeFor(r, CompactAssessOptions{Focus: AssessFocusRarely, Offset: 50})
	if len(next.DoneCheck.OpenSites) != 3 || next.More != "" || next.DoneCheck.Status != "open" {
		t.Errorf("second page: %d sites, more %q", len(next.DoneCheck.OpenSites), next.More)
	}

	r.DoneCheck.Truncated = true
	if cut := CompactAssessChangeFrom(r); !strings.Contains(cut.More, "first 1 of 1 open sites likely to break") {
		t.Errorf("the cut counts the likely sites only: %q", cut.More)
	}
}

// Two files declaring simulate(): the compact text says whose each contract is, and a
// name declared once stays as short as before.
func TestCompactAssessChange_SameNameContracts(t *testing.T) {
	r := &AnalyzeChangeSetResponse{
		Summary: &ChangeSummary{FilesChanged: 2},
		Contracts: []ContractSignal{
			{Symbol: "simulate", File: "lc/lc1_account.py", Kind: "possible-contract-change"},
			{Symbol: "simulate", File: "lc/lc1b_trim.py", Kind: "possible-contract-change"},
			{Symbol: "price", File: "lc/lc1_account.py", Kind: "possible-contract-change"},
		},
	}
	text := CompactAssessChangeFrom(r).Text()
	for _, want := range []string{"possible-contract-change simulate (lc/lc1_account.py)\n", "possible-contract-change simulate (lc/lc1b_trim.py)\n", "possible-contract-change price\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
}

// Renaming an interface method and its implementations changed three symbols named
// Priority: the contracts and the open sites now say whose, from the qualified name,
// while the JSON symbol fields keep the bare name the checks match by.
func TestCompactAssessChange_QualifiedNames(t *testing.T) {
	r := &AnalyzeChangeSetResponse{
		Summary: &ChangeSummary{FilesChanged: 3},
		ChangedSymbols: []ChangedSymbolInfo{
			{Name: "Priority", QualifiedName: "Backend.Priority", File: "backends/backend.go"},
			{Name: "Priority", QualifiedName: "LspAdapter.Priority", File: "backends/lsp/adapter.go"},
			{Name: "simulate", File: "lc/lc1_account.py"},
		},
		Contracts: []ContractSignal{
			{Symbol: "Priority", File: "backends/backend.go", Kind: "definition-removed"},
			{Symbol: "Priority", File: "backends/lsp/adapter.go", Kind: "definition-removed"},
			{Symbol: "simulate", File: "lc/lc1_account.py", Kind: "possible-contract-change"},
		},
		DoneCheck: &DoneCheck{Status: "open", OpenCount: 1, OpenSites: []OpenSite{
			{Symbol: "Priority", QualifiedName: "LspAdapter.Priority", Change: "removed", File: "backends/lsp/lsp_test.go", Line: 138, Code: "adapter.Priority()"},
		}},
	}
	text := CompactAssessChangeFrom(r).Text()
	for _, want := range []string{
		"definition-removed Backend.Priority\n",
		"definition-removed LspAdapter.Priority\n",
		"possible-contract-change simulate\n",
		"LspAdapter.Priority (removed)\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if r.Contracts[0].Symbol != "Priority" || r.DoneCheck.OpenSites[0].Symbol != "Priority" {
		t.Error("the response's symbol fields were changed")
	}
}

// Sites of two changed symbols with one bare name are counted apart.
func TestGroupRarely_ByQualifiedName(t *testing.T) {
	groups := groupRarely([]OpenSite{
		{Symbol: "Priority", QualifiedName: "Backend.Priority", Change: "removed", File: "a.go", Use: "read"},
		{Symbol: "Priority", QualifiedName: "LspAdapter.Priority", Change: "removed", File: "b.go", Use: "read"},
		{Symbol: "Priority", QualifiedName: "LspAdapter.Priority", Change: "removed", File: "c.go", Use: "read"},
	})
	if len(groups) != 2 || groups[0].QualifiedName != "Backend.Priority" || groups[1].Count != 2 {
		t.Fatalf("groups = %+v, want Backend.Priority 1 and LspAdapter.Priority 2", groups)
	}
	if line := rarelyLine(groups[1]); !strings.HasPrefix(line, "LspAdapter.Priority (removed): 2 in 2 files") {
		t.Errorf("rarelyLine = %q", line)
	}
}
