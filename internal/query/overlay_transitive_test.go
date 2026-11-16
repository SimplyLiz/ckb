package query

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestOverlayOnlyCallers(t *testing.T) {
	ov := scip.OccurrenceSourceOverlay
	edges := []scip.CallGraphEdge{
		{From: "a", To: "root"},
		{From: "b", To: "root", Source: ov},
		{From: "c", To: "b"},             // only behind the overlay edge
		{From: "d", To: "a"},             // indexer path
		{From: "d", To: "b"},             // and a second one over the overlay
		{From: "e", To: "a", Source: ov}, // overlay edge from an indexer node
	}
	result := map[string]int{"a": 1, "b": 1, "c": 2, "d": 2, "e": 2}
	got := overlayOnlyCallers("root", edges, result, 2)
	want := map[string]bool{"b": true, "c": true, "e": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("%s should be overlay-only; got %v", id, got)
		}
	}
	// Without overlay edges nothing is marked.
	if got := overlayOnlyCallers("root", edges[:1], map[string]int{"a": 1}, 2); got != nil {
		t.Errorf("no overlay edges: got %v", got)
	}
}
