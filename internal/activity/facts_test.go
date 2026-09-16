package activity

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/query"
)

func TestExtractPrepareChangeFacts(t *testing.T) {
	resp := &query.PrepareChangeResponse{
		DirectDependents: []query.PrepareDependent{{SymbolId: "a"}, {SymbolId: "b"}},
		RelatedTests:     []query.PrepareTest{{File: "x_test.go"}},
		CoChangeFiles:    []query.PrepareCoChange{{File: "y.go"}, {File: "z.go"}, {File: "w.go"}},
	}

	facts := ExtractFacts("prepareChange", resp)
	if facts == nil {
		t.Fatalf("expected facts, got nil")
	}
	if facts["dependents"] != 2 || facts["tests"] != 1 || facts["cochange"] != 3 {
		t.Errorf("unexpected facts: %+v", facts)
	}
}

func TestExtractChangeSetFactsForBothToolNames(t *testing.T) {
	resp := &query.AnalyzeChangeSetResponse{
		ChangedSymbols:  []query.ChangedSymbolInfo{{SymbolID: "a"}},
		AffectedSymbols: []query.ImpactItem{{StableId: "b"}, {StableId: "c"}},
		ModulesAffected: []query.ModuleImpact{{ModuleId: "m1"}},
	}

	for _, tool := range []string{"analyzeChange", "assessChange"} {
		facts := ExtractFacts(tool, resp)
		if facts == nil {
			t.Fatalf("%s: expected facts, got nil", tool)
		}
		if facts["changed"] != 1 || facts["affected"] != 2 || facts["modules"] != 1 {
			t.Errorf("%s: unexpected facts: %+v", tool, facts)
		}
	}
}

func TestExtractAnalyzeImpactFacts(t *testing.T) {
	data := map[string]interface{}{
		"directImpact":     []map[string]interface{}{{"stableId": "a"}, {"stableId": "b"}},
		"transitiveImpact": []map[string]interface{}{{"stableId": "c"}},
		"blastRadius":      map[string]interface{}{"moduleCount": 4},
	}

	facts := ExtractFacts("analyzeImpact", data)
	if facts == nil {
		t.Fatalf("expected facts, got nil")
	}
	if facts["direct"] != 2 || facts["transitive"] != 1 || facts["modules"] != 4 {
		t.Errorf("unexpected facts: %+v", facts)
	}
}

func TestExtractFindReferencesFacts(t *testing.T) {
	data := map[string]interface{}{
		"references": []map[string]interface{}{{"kind": "call"}},
		"totalCount": 42,
	}

	facts := ExtractFacts("findReferences", data)
	if facts == nil || facts["references"] != 42 {
		t.Errorf("unexpected facts: %+v", facts)
	}
}

func TestExtractSearchSymbolsFacts(t *testing.T) {
	data := map[string]interface{}{
		"symbols":    []map[string]interface{}{{"name": "Foo"}, {"name": "Bar"}},
		"totalCount": 2,
	}

	facts := ExtractFacts("searchSymbols", data)
	if facts == nil || facts["results"] != 2 {
		t.Errorf("unexpected facts: %+v", facts)
	}
}

func TestExtractFactsUnknownToolReturnsNil(t *testing.T) {
	if facts := ExtractFacts("someOtherTool", map[string]interface{}{"x": 1}); facts != nil {
		t.Errorf("expected nil facts for unregistered tool, got %+v", facts)
	}
}

func TestExtractFactsNilDataReturnsNil(t *testing.T) {
	if facts := ExtractFacts("prepareChange", nil); facts != nil {
		t.Errorf("expected nil facts for nil data, got %+v", facts)
	}
}

func TestExtractFactsWrongShapeReturnsNilNotPanic(t *testing.T) {
	// prepareChange extractor expects *query.PrepareChangeResponse; feed it
	// something else entirely and make sure it doesn't panic.
	facts := ExtractFacts("prepareChange", map[string]interface{}{"unexpected": "shape"})
	if facts != nil {
		t.Errorf("expected nil facts for mismatched shape, got %+v", facts)
	}

	facts = ExtractFacts("analyzeImpact", "not a map")
	if facts != nil {
		t.Errorf("expected nil facts for non-map analyzeImpact data, got %+v", facts)
	}

	facts = ExtractFacts("prepareChange", (*query.PrepareChangeResponse)(nil))
	if facts != nil {
		t.Errorf("expected nil facts for nil typed pointer, got %+v", facts)
	}
}

// panicyExtractor simulates a buggy extractor to prove ExtractFacts recovers.
func TestExtractFactsRecoversFromPanic(t *testing.T) {
	orig := factsExtractors["prepareChange"]
	defer func() { factsExtractors["prepareChange"] = orig }()

	factsExtractors["prepareChange"] = func(data interface{}) map[string]int {
		panic("boom")
	}

	facts := ExtractFacts("prepareChange", "anything")
	if facts != nil {
		t.Errorf("expected nil facts after recovered panic, got %+v", facts)
	}
}
