package mcp

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// TestAssessChangeRegistration verifies assessChange is registered as its
// own tool, analyzeChange remains registered but marked as a deprecated
// alias in its description, and every preset that lists analyzeChange also
// lists assessChange (analyzeChange stays too, per the plan's two-minor-
// version deprecation window).
func TestAssessChangeRegistration(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)

	defs := server.GetToolDefinitions()
	byName := make(map[string]Tool, len(defs))
	for _, d := range defs {
		byName[d.Name] = d
	}

	assess, ok := byName["assessChange"]
	if !ok {
		t.Fatal("expected \"assessChange\" tool to be registered")
	}
	if strings.Contains(strings.ToLower(assess.Description), "deprecated") {
		t.Errorf("assessChange should not be described as deprecated: %q", assess.Description)
	}

	analyze, ok := byName["analyzeChange"]
	if !ok {
		t.Fatal("expected \"analyzeChange\" tool to still be registered (deprecated alias)")
	}
	if !strings.HasPrefix(analyze.Description, "Deprecated alias of assessChange") {
		t.Errorf("analyzeChange description should start with the deprecation notice, got %q", analyze.Description)
	}

	// Handlers must both be registered and callable (same underlying logic).
	if _, ok := server.tools["assessChange"]; !ok {
		t.Error("expected a registered handler for assessChange")
	}
	if _, ok := server.tools["analyzeChange"]; !ok {
		t.Error("expected a registered handler for analyzeChange")
	}

	// Every preset that lists analyzeChange must also list assessChange.
	for name, tools := range Presets {
		hasAnalyze, hasAssess := false, false
		for _, tname := range tools {
			if tname == "analyzeChange" {
				hasAnalyze = true
			}
			if tname == "assessChange" {
				hasAssess = true
			}
		}
		if hasAnalyze && !hasAssess {
			t.Errorf("preset %q lists analyzeChange but not assessChange", name)
		}
	}

	// Full preset (wildcard) must include both.
	if err := server.SetPreset(PresetFull); err != nil {
		t.Fatalf("failed to set full preset: %v", err)
	}
	fullTools := server.GetFilteredTools()
	fullNames := make(map[string]bool, len(fullTools))
	for _, tl := range fullTools {
		fullNames[tl.Name] = true
	}
	if !fullNames["assessChange"] {
		t.Error("expected full preset to include assessChange")
	}
	if !fullNames["analyzeChange"] {
		t.Error("expected full preset to still include analyzeChange")
	}

	// Review preset (explicit list) must include both.
	if err := server.SetPreset(PresetReview); err != nil {
		t.Fatalf("failed to set review preset: %v", err)
	}
	reviewTools := server.GetFilteredTools()
	reviewNames := make(map[string]bool, len(reviewTools))
	for _, tl := range reviewTools {
		reviewNames[tl.Name] = true
	}
	if !reviewNames["assessChange"] {
		t.Error("expected review preset to include assessChange")
	}
	if !reviewNames["analyzeChange"] {
		t.Error("expected review preset to still include analyzeChange")
	}
}
