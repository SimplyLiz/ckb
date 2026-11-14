package query

import (
	"errors"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

func TestUsableArchImpact(t *testing.T) {
	if usableArchImpact(&cartographer.ImpactAnalysis{}, nil) != nil {
		t.Error("an impact whose TargetModule decoded empty must be dropped")
	}
	if usableArchImpact(&cartographer.ImpactAnalysis{TargetModule: "a.go"}, errors.New("x")) != nil {
		t.Error("an error must drop the impact")
	}
	if usableArchImpact(&cartographer.ImpactAnalysis{TargetModule: "a.go"}, nil) == nil {
		t.Error("a decoded impact must be kept")
	}
}

func TestUsableGraph(t *testing.T) {
	score := 80.0
	if usableGraph(&cartographer.ProjectGraph{Nodes: []cartographer.GraphNode{{Path: "a.go"}}}, nil) != nil {
		t.Error("a graph without metadata (snake_case lib) must be dropped")
	}
	if usableGraph(&cartographer.ProjectGraph{Metadata: cartographer.GraphMetadata{TotalFiles: 3, HealthScore: &score}}, nil) == nil {
		t.Error("a decoded graph must be kept")
	}
}

func TestIsConceptSourceFile(t *testing.T) {
	for path, want := range map[string]bool{
		"src/brain/morphon.rs":                               true,
		"internal/query/impact.go":                           true,
		"lib/native/core.cpp":                                true,
		"docs/benchmark_results/v5.0.0/cartpole_latest.json": false,
		"README.md":   false,
		"layers.toml": false,
	} {
		if got := isConceptSourceFile(path); got != want {
			t.Errorf("isConceptSourceFile(%q) = %v, want %v", path, got, want)
		}
	}
}
