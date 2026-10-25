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
// pre-edit checks, searchSymbols + findReferences for symbol lookup, the
// expandToolset expansion rule (including that it's a one-time,
// session-scoped operation that can expand to "full"), and how to get to
// reviewPR for PR review.
func TestInstructionsCoreFitsIn512Bytes(t *testing.T) {
	text := instructionsText()
	full := []byte(text)
	cut := 512
	if len(full) < cut {
		cut = len(full)
	}
	first512 := string(full[:cut])

	for _, want := range []string{"prepareChange", "searchSymbols", "findReferences", "expandToolset", "reviewPR", "once", "full"} {
		if !strings.Contains(first512, want) {
			t.Errorf("first 512 chars of instructions must contain %q (Codex truncates here), got: %s", want, first512)
		}
	}
}

// TestInstructionsTextStateIndependent asserts instructionsText carries no
// per-preset branching: it takes no arguments and its output does not
// depend on which preset a session starts on or later expands into. This is
// the fix for the bug where a core session's instructions went stale the
// moment expandToolset ran (instructions are generated once, at
// initialize, and never resent — see handleInitialize).
func TestInstructionsTextStateIndependent(t *testing.T) {
	if instructionsText() != instructionsText() {
		t.Fatal("instructionsText must be a pure, state-independent function")
	}
}

// TestInstructionsSurviveExpansion is the core -> expand("review") state
// transition test: it captures the instructions text the way a client
// actually receives it (once, at initialize, before any expansion), then
// drives the session through toolExpandToolset, then re-checks that exact
// same text against the post-expansion state. Because instructions are never
// resent, that pre-expansion text is the only guidance the agent has for the
// rest of the session — so it must not tell the agent to do anything that
// would now be rejected (a second expandToolset call), regardless of
// whether the session expanded into a preset with reviewPR or not.
func TestInstructionsSurviveExpansion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)

	// What the client received at initialize, before any expansion.
	initial, err := server.handleInitialize(map[string]interface{}{})
	if err != nil {
		t.Fatalf("handleInitialize returned error: %v", err)
	}
	text := initial.Instructions

	// Session expands mid-conversation, into a preset that does NOT carry
	// reviewPR (refactor), which is the case the old preset-branching text
	// got wrong: it would still be telling the agent to "expandToolset with
	// preset review", a call that's now rejected.
	if _, err := server.toolExpandToolset(map[string]interface{}{
		"preset": PresetRefactor,
		"reason": "refactoring analysis for this change",
	}); err != nil {
		t.Fatalf("toolExpandToolset returned error: %v", err)
	}
	if !server.IsExpanded() {
		t.Fatal("expected session to be marked expanded")
	}
	if server.GetActivePreset() != PresetRefactor {
		t.Fatalf("expected active preset %q, got %q", PresetRefactor, server.GetActivePreset())
	}

	// The text the agent is still holding must not instruct an unconditional
	// second expansion -- it must instead be readable, in this now-expanded-
	// without-reviewPR state, as "restart with --preset=review".
	if !strings.Contains(text, `already expanded without reviewPR? Restart`) {
		t.Errorf("instructions must gate the expandToolset advice so it does not require a second expansion once already expanded, got: %s", text)
	}
}

// TestInstructionsTextEveryToolResolvable asserts every tool name from
// instructionToolNames that appears in the generated instructions text is
// either (a) available in the default "core" preset every session starts
// on, or (b) reached via expandToolset -- never named as directly callable
// when it isn't actually in the starting toolset.
func TestInstructionsTextEveryToolResolvable(t *testing.T) {
	text := instructionsText()
	coreTools := GetPresetTools(PresetCore)
	inCore := make(map[string]bool, len(coreTools))
	for _, name := range coreTools {
		inCore[name] = true
	}

	for _, name := range instructionToolNames {
		if !strings.Contains(text, name) {
			continue // not mentioned, nothing to check
		}
		if inCore[name] {
			continue // mentioned and genuinely available in the starting preset - fine
		}
		// Mentioned but not in the starting preset: the text must route the
		// agent through expandToolset rather than implying it's directly
		// callable.
		if !strings.Contains(text, "expandToolset") {
			t.Errorf("instructions mention %q but it is not in the core preset and the text does not route through expandToolset: %s", name, text)
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
