package mcp

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

// TestHandleInitializeIncludesInstructions asserts the "consumer contract
// v0": initialize responses must carry a non-empty Instructions string, and
// every tool name that string references must be a real, registered tool.
func TestHandleInitializeIncludesInstructions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)

	result, err := server.handleInitialize(map[string]interface{}{})
	if err != nil {
		t.Fatalf("handleInitialize returned error: %v", err)
	}

	if strings.TrimSpace(result.Instructions) == "" {
		t.Fatal("InitializeResult.Instructions must not be empty")
	}

	if len(result.Instructions) >= 1200 {
		t.Errorf("Instructions is %d chars, want < 1200 (costs context every session)", len(result.Instructions))
	}

	registered := registeredToolNames(server)

	for _, name := range instructionToolNames {
		if !registered[name] {
			t.Errorf("instructionToolNames references %q, but it is not a registered tool", name)
		}
		if !strings.Contains(result.Instructions, name) {
			t.Errorf("instructionToolNames lists %q, but it does not appear in the generated instructions text", name)
		}
	}
}

// TestInstructionsTextPresetAware asserts the reviewPR framing changes
// depending on whether the active preset already exposes reviewPR (review,
// full) versus requiring expandToolset first (core, refactor, federation,
// docs, ops).
func TestInstructionsTextPresetAware(t *testing.T) {
	core := instructionsText(PresetCore)
	if !strings.Contains(core, `expandToolset("review")`) {
		t.Errorf("core preset instructions should tell the agent to expandToolset to review before reviewPR is usable, got: %s", core)
	}

	review := instructionsText(PresetReview)
	if strings.Contains(review, `expandToolset("review")`) {
		t.Errorf("review preset instructions should not tell the agent to expand into review (it's already active), got: %s", review)
	}
	if !strings.Contains(review, "reviewPR") {
		t.Errorf("review preset instructions should still mention reviewPR directly, got: %s", review)
	}
}

// registeredToolNames returns the set of all tool names the server knows
// about, independent of the currently active preset.
func registeredToolNames(server *MCPServer) map[string]bool {
	names := make(map[string]bool)
	for _, tool := range server.GetToolDefinitions() {
		names[tool.Name] = true
	}
	return names
}
