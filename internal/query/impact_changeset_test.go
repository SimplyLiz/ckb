package query

import (
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// --- Decisions dedup ---

func TestAppendDedupedDecision(t *testing.T) {
	seen := make(map[string]bool)
	var decisions []RelatedDecision

	decisions = appendDedupedDecision(decisions, seen, RelatedDecision{ID: "ADR-1", Title: "First"}, 10)
	decisions = appendDedupedDecision(decisions, seen, RelatedDecision{ID: "ADR-2", Title: "Second"}, 10)
	// Duplicate ID should not be added again.
	decisions = appendDedupedDecision(decisions, seen, RelatedDecision{ID: "ADR-1", Title: "First (dup)"}, 10)

	if len(decisions) != 2 {
		t.Fatalf("expected 2 deduped decisions, got %d: %+v", len(decisions), decisions)
	}
	if decisions[0].ID != "ADR-1" || decisions[1].ID != "ADR-2" {
		t.Errorf("unexpected decision order/content: %+v", decisions)
	}
}

func TestAppendDedupedDecision_RespectsCap(t *testing.T) {
	seen := make(map[string]bool)
	var decisions []RelatedDecision

	for i := 0; i < 15; i++ {
		id := string(rune('A' + i))
		decisions = appendDedupedDecision(decisions, seen, RelatedDecision{ID: id}, 10)
	}

	if len(decisions) != 10 {
		t.Fatalf("expected decisions capped at 10, got %d", len(decisions))
	}
}

// --- Contracts heuristic ---

func TestBuildContractSignal(t *testing.T) {
	tests := []struct {
		name       string
		symbol     string
		visibility string
		language   string
		file       string
		wantNil    bool
	}{
		{
			name:       "explicit public visibility",
			symbol:     "Resolve",
			visibility: "public",
			language:   "go",
			file:       "internal/foo/foo.go",
			wantNil:    false,
		},
		{
			name:       "go exported by capitalization, unknown visibility",
			symbol:     "KnowledgeContext",
			visibility: "unknown",
			language:   "go",
			file:       "internal/foo/foo.go",
			wantNil:    false,
		},
		{
			name:       "go unexported lowercase",
			symbol:     "resolve",
			visibility: "unknown",
			language:   "go",
			file:       "internal/foo/foo.go",
			wantNil:    true,
		},
		{
			name:       "explicit private visibility",
			symbol:     "Resolve",
			visibility: "private",
			language:   "go",
			file:       "internal/foo/foo.go",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildContractSignal(tt.symbol, tt.visibility, tt.language, tt.file)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil ContractSignal, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a ContractSignal, got nil")
			}
			if got.Symbol != tt.symbol {
				t.Errorf("Symbol = %q, want %q", got.Symbol, tt.symbol)
			}
			if got.File != tt.file {
				t.Errorf("File = %q, want %q", got.File, tt.file)
			}
			if got.Kind != "possible-contract-change" {
				t.Errorf("Kind = %q, want %q", got.Kind, "possible-contract-change")
			}
			if strings.Contains(strings.ToLower(got.Basis), "breaking") {
				t.Errorf("Basis must never say 'breaking change': %q", got.Basis)
			}
			if strings.Contains(strings.ToLower(got.Kind), "breaking") {
				t.Errorf("Kind must never say 'breaking change': %q", got.Kind)
			}
		})
	}
}

// --- Risk factors (structured, with Evidence) ---

func TestCalculateAggregatedRisk_FactorsHaveEvidence(t *testing.T) {
	e := &Engine{}

	changed := []impact.ChangedSymbol{
		{SymbolID: "s1", Name: "Foo", File: "a.go"},
		{SymbolID: "s2", Name: "Bar", File: "b.go"},
	}
	direct := []ImpactItem{{StableId: "d1"}, {StableId: "d2"}}
	transitive := []ImpactItem{{StableId: "t1"}}
	modules := []ModuleImpact{{ModuleId: "m1"}, {ModuleId: "m2"}}

	risk := e.calculateAggregatedRisk(changed, direct, transitive, modules)
	if risk == nil {
		t.Fatal("expected non-nil RiskScore")
	}

	wantNames := map[string]bool{
		"symbols_changed":   false,
		"direct_impact":     false,
		"transitive_impact": false,
		"module_spread":     false,
	}
	for _, f := range risk.Factors {
		if _, ok := wantNames[f.Name]; ok {
			wantNames[f.Name] = true
			if f.Evidence == "" {
				t.Errorf("factor %q has no Evidence", f.Name)
			}
			if f.Weight <= 0 {
				t.Errorf("factor %q has non-positive weight %v", f.Name, f.Weight)
			}
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("expected factor %q not present in %+v", name, risk.Factors)
		}
	}
}

// --- Confidence mapping ---

func TestChangeScoreToTier(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.99, "high"},
		{0.95, "high"},
		{0.94, "medium"},
		{0.70, "medium"},
		{0.69, "low"},
		{0.30, "low"},
		{0.29, "speculative"},
		{0.0, "speculative"},
	}
	for _, tt := range tests {
		if got := changeScoreToTier(tt.score); got != tt.want {
			t.Errorf("changeScoreToTier(%v) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestBuildChangeConfidence_NoSCIP(t *testing.T) {
	e := &Engine{} // no scipAdapter -> hasSCIP == false

	conf := e.buildChangeConfidence(CompletenessInfo{Score: 0.3, Reason: "file-level-only"}, nil, 0, 0)
	if conf == nil {
		t.Fatal("expected non-nil ChangeConfidence")
	}
	if conf.Tier != "speculative" && conf.Tier != "low" {
		t.Errorf("expected a low/speculative tier without SCIP, got %q (score %v)", conf.Tier, conf.Score)
	}
	found := false
	for _, r := range conf.Reasons {
		if strings.Contains(r, "no SCIP index") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'no SCIP index' reason, got %+v", conf.Reasons)
	}
}

func TestBuildChangeConfidence_StaleIndexReason(t *testing.T) {
	e := &Engine{}

	staleness := &IndexStalenessInfo{IsStale: true, CommitsBehind: 3}
	conf := e.buildChangeConfidence(CompletenessInfo{Score: 0.9, Reason: "scip-available"}, staleness, 0, 0)

	found := false
	for _, r := range conf.Reasons {
		if strings.Contains(r, "3 commit(s) behind") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a staleness reason mentioning commits behind, got %+v", conf.Reasons)
	}
}

func TestBuildChangeConfidence_AverageMappingConfidence(t *testing.T) {
	e := &Engine{}

	conf := e.buildChangeConfidence(CompletenessInfo{Score: 0.3, Reason: "file-level-only"}, nil, 1.6, 2) // avg 0.8
	found := false
	for _, r := range conf.Reasons {
		if strings.Contains(r, "80%") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an 80%% mapping-confidence reason, got %+v", conf.Reasons)
	}
}

// --- Test-gap counting ---

func TestRecordTestGap(t *testing.T) {
	untested, examples := 0, []string(nil)

	// Consumer with a reaching test: no change.
	untested, examples = recordTestGap(untested, examples, "TestedCaller", "ChangedFn", true)
	if untested != 0 || len(examples) != 0 {
		t.Fatalf("hasTest=true should not record a gap, got untested=%d examples=%v", untested, examples)
	}

	// Consumer with no reaching test: counted and recorded.
	untested, examples = recordTestGap(untested, examples, "UntestedCaller", "ChangedFn", false)
	if untested != 1 {
		t.Errorf("expected untested=1, got %d", untested)
	}
	if len(examples) != 1 || examples[0] != "UntestedCaller -> ChangedFn" {
		t.Errorf("unexpected examples: %+v", examples)
	}
}

func TestRecordTestGap_ExamplesCappedAtFive(t *testing.T) {
	untested, examples := 0, []string(nil)
	for i := 0; i < 10; i++ {
		untested, examples = recordTestGap(untested, examples, "Caller", "ChangedFn", false)
	}
	if untested != 10 {
		t.Errorf("expected untested count to keep incrementing past the example cap, got %d", untested)
	}
	if len(examples) != 5 {
		t.Errorf("expected examples capped at 5, got %d: %+v", len(examples), examples)
	}
}

// --- Recommendations carry a Source ---

func TestGenerateRecommendations_SourceAttribution(t *testing.T) {
	e := &Engine{}

	summary := &ChangeSummary{TransitivelyAffected: 25}
	risk := &RiskScore{Level: "high"}
	changed := []impact.ChangedSymbol{{SymbolID: "s1", Confidence: 0.9}}
	modules := []ModuleImpact{{ModuleId: "m1"}}
	testGaps := &TestGapsInfo{UntestedConsumers: 3}
	contracts := []ContractSignal{{Symbol: "Foo", File: "a.go", Kind: "possible-contract-change", Basis: "heuristic: exported/public symbol lines changed"}}

	recs := e.generateRecommendations(summary, risk, changed, modules, testGaps, contracts)

	sources := make(map[string]bool)
	for _, r := range recs {
		if r.Source == "" {
			t.Errorf("recommendation %+v has no Source", r)
		}
		sources[r.Source] = true
	}
	for _, want := range []string{"risk", "transitive-impact", "test-gaps", "contracts"} {
		if !sources[want] {
			t.Errorf("expected a recommendation sourced from %q, got sources=%v", want, sources)
		}
	}
}
