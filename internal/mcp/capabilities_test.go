package mcp

import (
	"encoding/json"
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

	// ~900 chars is the design target (see capabilities.go doc comment on
	// instructionsCore); this leaves headroom without letting the text creep
	// back toward the old 949-977 byte version that blew Codex's 512-char
	// truncation budget.
	if len(result.Instructions) >= 950 {
		t.Errorf("Instructions is %d chars, want < 950 (costs context every session, and pushes optional detail further past Codex's 512-char truncation)", len(result.Instructions))
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

// TestInstructionsCoreFitsIn512Bytes asserts the critical "when to use CKB"
// contract survives Codex's 512-character truncation of MCP `instructions`
// (https://learn.chatgpt.com/ko-KR/docs/extend/mcp). Everything that must
// reach a truncating client has to be inside this window: prepareChange for
// pre-edit checks, explore/understand for orientation, searchSymbols +
// findReferences for symbol lookup, and the expandToolset expansion rule
// (including that it's a one-time, session-scoped operation).
func TestInstructionsCoreFitsIn512Bytes(t *testing.T) {
	text := instructionsText(DefaultPreset)
	full := []byte(text)
	cut := 512
	if len(full) < cut {
		cut = len(full)
	}
	first512 := string(full[:cut])

	for _, want := range []string{"prepareChange", "explore", "understand", "searchSymbols", "findReferences", "expandToolset", "once"} {
		if !strings.Contains(first512, want) {
			t.Errorf("first 512 chars of instructions must contain %q (Codex truncates here), got: %s", want, first512)
		}
	}
}

// TestInstructionsTextPresetAware asserts the reviewPR framing changes
// depending on whether the active preset already exposes reviewPR (review,
// full) versus requiring expandToolset first (core, refactor, federation,
// docs, ops), and that the expansion instruction uses the real tool call
// shape (preset + reason params) rather than pseudo-call syntax.
func TestInstructionsTextPresetAware(t *testing.T) {
	core := instructionsText(PresetCore)
	if strings.Contains(core, `expandToolset("review")`) {
		t.Errorf("core preset instructions must not use pseudo-call syntax like expandToolset(\"review\"); expandToolset takes {preset, reason}, got: %s", core)
	}
	if !strings.Contains(core, `expandToolset with preset "review" and a reason`) {
		t.Errorf("core preset instructions should tell the agent to call expandToolset with preset \"review\" and a reason before reviewPR is usable, got: %s", core)
	}

	review := instructionsText(PresetReview)
	if strings.Contains(review, `expandToolset with preset "review"`) {
		t.Errorf("review preset instructions should not tell the agent to expand into review (it's already active), got: %s", review)
	}
	if !strings.Contains(review, "reviewPR") {
		t.Errorf("review preset instructions should still mention reviewPR directly, got: %s", review)
	}
}

// TestInstructionsTextEveryPresetToolsResolvable asserts that, for every
// preset, every tool name from instructionToolNames that appears in that
// preset's generated instructions text is either (a) actually available in
// that preset, or (b) reached via expandToolset — never named as directly
// callable when it isn't actually in the active toolset.
func TestInstructionsTextEveryPresetToolsResolvable(t *testing.T) {
	for _, preset := range ValidPresets() {
		text := instructionsText(preset)

		for _, name := range instructionToolNames {
			if !strings.Contains(text, name) {
				continue // not mentioned for this preset's text, nothing to check
			}
			if presetHasTool(preset, name) {
				continue // mentioned and genuinely available - fine
			}
			// Mentioned but not in this preset's toolset: the text must route
			// the agent through expandToolset rather than implying it's
			// directly callable.
			if !strings.Contains(text, "expandToolset") {
				t.Errorf("preset %q: instructions mention %q but it is not in this preset and the text does not route through expandToolset: %s", preset, name, text)
			}
		}
	}
}

// TestInitializeWireFormatIncludesInstructions asserts the JSON actually
// sent over the wire for an initialize response carries the "instructions"
// field (InitializeResult.Instructions has `json:"instructions,omitempty"`,
// so an empty string would silently vanish from the payload).
func TestInitializeWireFormatIncludesInstructions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)

	result, err := server.handleInitialize(map[string]interface{}{})
	if err != nil {
		t.Fatalf("handleInitialize returned error: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal InitializeResult: %v", err)
	}

	var wire map[string]interface{}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("failed to unmarshal InitializeResult JSON: %v", err)
	}

	instructions, ok := wire["instructions"].(string)
	if !ok || strings.TrimSpace(instructions) == "" {
		t.Errorf("initialize JSON must contain a non-empty \"instructions\" field, got wire payload: %s", data)
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
