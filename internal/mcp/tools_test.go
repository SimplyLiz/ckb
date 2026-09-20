package mcp

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
)

// expandToolsetTool returns the expandToolset tool definition from a fresh
// server's full tool list, failing the test if it isn't found.
func expandToolsetTool(t *testing.T) Tool {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)

	for _, tool := range server.GetToolDefinitions() {
		if tool.Name == "expandToolset" {
			return tool
		}
	}
	t.Fatal("expandToolset tool not found in GetToolDefinitions")
	return Tool{}
}

// TestExpandToolsetDescriptionMatchesOneExpansionRule asserts the tool
// description no longer tells the agent to "pick the smallest preset" --
// that advice contradicts toolExpandToolset's one-expansion-per-session
// rule (a second call is rejected, so "start small and expand again later"
// isn't a real option; the only choice is the one preset that covers the
// whole task, or "full").
func TestExpandToolsetDescriptionMatchesOneExpansionRule(t *testing.T) {
	tool := expandToolsetTool(t)

	if strings.Contains(strings.ToLower(tool.Description), "smallest preset") {
		t.Errorf("expandToolset description still says to pick the smallest preset, which contradicts the one-expansion rule: %s", tool.Description)
	}
	props, ok := tool.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("expandToolset InputSchema has no properties map")
	}
	presetProp, ok := props["preset"].(map[string]interface{})
	if !ok {
		t.Fatal("expandToolset InputSchema has no preset property")
	}
	presetDesc, _ := presetProp["description"].(string)
	if strings.Contains(strings.ToLower(presetDesc), "smallest preset") {
		t.Errorf("expandToolset preset param description still says to pick the smallest preset: %s", presetDesc)
	}
}

// TestExpandToolsetDescriptionCountsMatchPresets asserts the per-preset
// tool counts advertised in the expandToolset description are computed
// from presets.go (via GetPresetTools), not hardcoded -- so they can't
// drift from reality the way the old hardcoded numbers did.
func TestExpandToolsetDescriptionCountsMatchPresets(t *testing.T) {
	tool := expandToolsetTool(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)
	totalTools := len(server.GetToolDefinitions())

	wantCounts := map[string]int{
		PresetReview:     len(GetPresetTools(PresetReview)),
		PresetRefactor:   len(GetPresetTools(PresetRefactor)),
		PresetFederation: len(GetPresetTools(PresetFederation)),
		PresetDocs:       len(GetPresetTools(PresetDocs)),
		PresetOps:        len(GetPresetTools(PresetOps)),
	}

	for preset, count := range wantCounts {
		want := fmt.Sprintf("%s (%d tools)", preset, count)
		if !strings.Contains(tool.Description, want) {
			t.Errorf("expandToolset description missing/mismatched count for preset %q: want %q, got: %s", preset, want, tool.Description)
		}
	}

	wantFull := fmt.Sprintf("full (%d tools)", totalTools)
	if !strings.Contains(tool.Description, wantFull) {
		t.Errorf("expandToolset description missing/mismatched count for preset \"full\": want %q, got: %s", wantFull, tool.Description)
	}
}
