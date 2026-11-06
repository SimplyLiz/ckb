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
// session-scoped operation that can expand to "full"), how to get to reviewPR
// for PR review, and assessChange as the last step of a change.
func TestInstructionsCoreFitsIn512Bytes(t *testing.T) {
	text := instructionsText()
	full := []byte(text)
	cut := 512
	if len(full) < cut {
		cut = len(full)
	}
	first512 := string(full[:cut])

	for _, want := range []string{"prepareChange", "searchSymbols", "findReferences", "expandToolset", "reviewPR", "once", "full", "assessChange", "openSites"} {
		if !strings.Contains(first512, want) {
			t.Errorf("first 512 chars of instructions must contain %q (Codex truncates here), got: %s", want, first512)
		}
	}
}

// TestInstructionsTextStateIndependent asserts the instructions a client
// receives do not depend on the session's preset: they are generated once at
// initialize and never resent, so a text that varied with the starting preset
// would be wrong the moment expandToolset ran. Every preset must produce the
// same text, and so must a session that has expanded.
func TestInstructionsTextStateIndependent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	want := instructionsText()

	for preset := range Presets {
		server := NewMCPServer("test", nil, logger)
		if err := server.SetPreset(preset); err != nil {
			t.Fatalf("SetPreset(%q): %v", preset, err)
		}
		got, err := server.handleInitialize(map[string]interface{}{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Instructions != want {
			t.Errorf("preset %q produced different instructions:\n%s", preset, got.Instructions)
		}
	}

	server := NewMCPServer("test", nil, logger)
	if _, err := server.toolExpandToolset(map[string]interface{}{
		"preset": PresetRefactor,
		"reason": "state independence check",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := server.handleInitialize(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Instructions != want {
		t.Errorf("instructions changed after expandToolset:\n%s", got.Instructions)
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

// TestInstructionsTextEveryToolResolvable checks the instructions against the
// real tool registry, in both directions:
//   - every tool name found in the text is a registered tool and is listed in
//     instructionToolNames (an unlisted mention would escape the drift check);
//   - every tool the text names that is not in the starting "core" preset is
//     reachable: the text routes through expandToolset, and one of the presets
//     the text sends the agent to ("review" or "full") really contains it.
func TestInstructionsTextEveryToolResolvable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewMCPServer("test", nil, logger)
	registered := registeredToolNames(server)
	text := instructionsText()

	listed := make(map[string]bool, len(instructionToolNames))
	for _, n := range instructionToolNames {
		listed[n] = true
	}
	inCore := make(map[string]bool)
	for _, name := range GetPresetTools(PresetCore) {
		inCore[name] = true
	}
	inPreset := func(preset, tool string) bool {
		for _, n := range GetPresetTools(preset) {
			if n == tool || n == "*" { // "full" is the wildcard preset
				return true
			}
		}
		return false
	}

	mentioned := 0
	for name := range registered {
		if !strings.Contains(text, name) {
			continue
		}
		mentioned++
		if !listed[name] {
			t.Errorf("instructions mention registered tool %q but instructionToolNames does not list it", name)
		}
		if inCore[name] {
			continue
		}
		if !strings.Contains(text, "expandToolset") {
			t.Errorf("%q is not in the core preset and the text does not route through expandToolset", name)
		}
		if !inPreset(PresetReview, name) && !inPreset(PresetFull, name) {
			t.Errorf("%q is not in core, review or full: the instructions point at a tool the agent cannot reach", name)
		}
	}
	if mentioned < len(instructionToolNames) {
		t.Errorf("only %d of %d instructionToolNames are registered tools found in the text", mentioned, len(instructionToolNames))
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
