package mcp

import (
	"io"
	"log/slog"
	"testing"
)

func TestPresetFiltering(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)

	// Test core preset (default)
	// v8.3: Core now includes explainPath, getModuleResponsibilities, exportForLLM
	coreTools := server.GetFilteredTools()
	if len(coreTools) != 25 {
		t.Errorf("expected 25 core tools, got %d", len(coreTools))
	}

	// Verify compound tools come first (preferred for AI workflows)
	expectedFirst := []string{
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "planRefactor", "expandToolset",
	}
	for i, expected := range expectedFirst {
		if i >= len(coreTools) {
			t.Errorf("missing tool at position %d: %s", i, expected)
			continue
		}
		if coreTools[i].Name != expected {
			t.Errorf("position %d: expected %s, got %s", i, expected, coreTools[i].Name)
		}
	}

	// Test full preset
	if err := server.SetPreset("full"); err != nil {
		t.Fatalf("failed to set full preset: %v", err)
	}
	fullTools := server.GetFilteredTools()
	// v8.5: +3 Cartographer (shotgunSurgery, evolution, blastRadius) +3 LIP annotation tools = 107; +1 symbolExists = 108; +1 (full includes the expanded presets) = 109; +1 analyzeOutgoingImpact = 110
	// v9.4: +1 assessChange (analyzeChange kept as a deprecated alias) = 111
	if len(fullTools) != 111 {
		t.Errorf("expected 111 full tools, got %d", len(fullTools))
	}

	// Full preset should still have core tools first
	for i, expected := range expectedFirst[:5] {
		if fullTools[i].Name != expected {
			t.Errorf("full preset position %d: expected %s, got %s", i, expected, fullTools[i].Name)
		}
	}
}

func TestPagination(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)

	// Set to full preset for more tools to paginate
	_ = server.SetPreset("full")
	allTools := server.GetFilteredTools()
	hash := server.GetToolsetHash()

	// First page
	page1, cursor1, err := PaginateTools(allTools, 0, 15, "full", hash)
	if err != nil {
		t.Fatalf("pagination error: %v", err)
	}
	if len(page1) != 15 {
		t.Errorf("expected 15 tools on page 1, got %d", len(page1))
	}
	if cursor1 == "" {
		t.Error("expected nextCursor for page 1")
	}

	// Decode and validate cursor
	offset, err := DecodeToolsCursor(cursor1, "full", hash)
	if err != nil {
		t.Fatalf("failed to decode cursor: %v", err)
	}
	if offset != 15 {
		t.Errorf("expected offset 15, got %d", offset)
	}

	// Second page
	page2, cursor2, err := PaginateTools(allTools, offset, 15, "full", hash)
	if err != nil {
		t.Fatalf("pagination error: %v", err)
	}
	if len(page2) != 15 {
		t.Errorf("expected 15 tools on page 2, got %d", len(page2))
	}
	if cursor2 == "" {
		t.Error("expected nextCursor for page 2")
	}

	// Last page
	lastOffset := len(allTools) - 10
	lastPage, lastCursor, err := PaginateTools(allTools, lastOffset, 15, "full", hash)
	if err != nil {
		t.Fatalf("pagination error: %v", err)
	}
	if len(lastPage) != 10 {
		t.Errorf("expected 10 tools on last page, got %d", len(lastPage))
	}
	if lastCursor != "" {
		t.Error("expected no nextCursor for last page")
	}
}

func TestCursorInvalidation(t *testing.T) {
	// Test that cursor is invalid when preset changes
	hash := ComputeToolsetHash([]Tool{{Name: "test", Description: "test"}})

	cursor := EncodeToolsCursor("core", 15, hash)

	// Same preset and hash - should work
	offset, err := DecodeToolsCursor(cursor, "core", hash)
	if err != nil {
		t.Errorf("unexpected error for valid cursor: %v", err)
	}
	if offset != 15 {
		t.Errorf("expected offset 15, got %d", offset)
	}

	// Different preset - should fail
	_, err = DecodeToolsCursor(cursor, "full", hash)
	if err == nil {
		t.Error("expected error for mismatched preset")
	}

	// Different hash - should fail
	_, err = DecodeToolsCursor(cursor, "core", "different-hash")
	if err == nil {
		t.Error("expected error for mismatched hash")
	}

	// Invalid cursor string - should fail
	_, err = DecodeToolsCursor("invalid", "core", hash)
	if err == nil {
		t.Error("expected error for invalid cursor")
	}

	// Empty cursor - should return 0 (first page)
	offset, err = DecodeToolsCursor("", "core", hash)
	if err != nil {
		t.Errorf("unexpected error for empty cursor: %v", err)
	}
	if offset != 0 {
		t.Errorf("expected offset 0 for empty cursor, got %d", offset)
	}
}

func TestExpandToolsetRateLimit(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)

	// Initially not expanded
	if server.IsExpanded() {
		t.Error("server should not be expanded initially")
	}

	// Mark as expanded
	server.MarkExpanded()

	if !server.IsExpanded() {
		t.Error("server should be expanded after MarkExpanded")
	}
}

func TestSetPresetInvalid(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)

	// Try to set an invalid preset
	err := server.SetPreset("nonexistent")
	if err == nil {
		t.Error("expected error for invalid preset")
	}

	// Verify the preset wasn't changed
	if server.GetActivePreset() != DefaultPreset {
		t.Errorf("preset should remain %s after invalid SetPreset, got %s",
			DefaultPreset, server.GetActivePreset())
	}
}

func TestGetActivePresetAfterSet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)

	// Initially should return default
	if server.GetActivePreset() != DefaultPreset {
		t.Errorf("expected default preset %s, got %s", DefaultPreset, server.GetActivePreset())
	}

	// Set to review preset
	if err := server.SetPreset(PresetReview); err != nil {
		t.Fatalf("SetPreset failed: %v", err)
	}

	// Should return review
	if server.GetActivePreset() != PresetReview {
		t.Errorf("expected preset %s, got %s", PresetReview, server.GetActivePreset())
	}
}

func TestPresetDescriptionsComplete(t *testing.T) {
	// Verify all presets have descriptions
	for _, preset := range ValidPresets() {
		desc, ok := PresetDescriptions[preset]
		if !ok {
			t.Errorf("preset %s missing from PresetDescriptions", preset)
		}
		if desc == "" {
			t.Errorf("preset %s has empty description", preset)
		}
	}
}

func TestGetPresetToolsInvalid(t *testing.T) {
	// Invalid preset should return core tools
	tools := GetPresetTools("nonexistent")
	coreTools := GetPresetTools(PresetCore)

	if len(tools) != len(coreTools) {
		t.Errorf("invalid preset should return core tools, got %d tools instead of %d",
			len(tools), len(coreTools))
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		tokens   int
		expected string
	}{
		{0, "~0 tokens"},
		{500, "~500 tokens"},
		{999, "~999 tokens"},
		{1000, "~1k tokens"},  // (1000+500)/1000 = 1
		{1499, "~1k tokens"},  // (1499+500)/1000 = 1
		{1500, "~2k tokens"},  // (1500+500)/1000 = 2 (rounds up at .5)
		{1501, "~2k tokens"},  // (1501+500)/1000 = 2
		{9040, "~9k tokens"},  // (9040+500)/1000 = 9
		{9499, "~9k tokens"},  // (9499+500)/1000 = 9
		{9500, "~10k tokens"}, // (9500+500)/1000 = 10 (rounds up at .5)
	}

	for _, tc := range tests {
		result := FormatTokens(tc.tokens)
		if result != tc.expected {
			t.Errorf("FormatTokens(%d) = %q, want %q", tc.tokens, result, tc.expected)
		}
	}
}

func TestToolsetHash(t *testing.T) {
	tools1 := []Tool{
		{Name: "a", Description: "desc a"},
		{Name: "b", Description: "desc b"},
	}
	tools2 := []Tool{
		{Name: "a", Description: "desc a"},
		{Name: "b", Description: "desc b"},
	}
	tools3 := []Tool{
		{Name: "a", Description: "different desc"},
		{Name: "b", Description: "desc b"},
	}

	hash1 := ComputeToolsetHash(tools1)
	hash2 := ComputeToolsetHash(tools2)
	hash3 := ComputeToolsetHash(tools3)

	if hash1 != hash2 {
		t.Error("identical tools should have identical hashes")
	}
	if hash1 == hash3 {
		t.Error("different descriptions should have different hashes")
	}
}

func TestGetAllPresetInfo(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server := NewMCPServer("test", nil, logger)
	allTools := server.GetToolDefinitions()
	infos := GetAllPresetInfo(allTools)

	// Should return info for all presets
	if len(infos) != len(ValidPresets()) {
		t.Errorf("expected %d preset infos, got %d", len(ValidPresets()), len(infos))
	}

	// Verify each preset has valid data
	for _, info := range infos {
		if info.Name == "" {
			t.Error("preset info has empty name")
		}
		if info.ToolCount == 0 {
			t.Errorf("preset %s has 0 tools", info.Name)
		}
		if info.TokenCount == 0 {
			t.Errorf("preset %s has 0 tokens", info.Name)
		}
		if info.Description == "" {
			t.Errorf("preset %s has no description", info.Name)
		}
	}

	// Verify core is the default
	var foundDefault bool
	for _, info := range infos {
		if info.Name == PresetCore && info.IsDefault {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Error("core preset should be marked as default")
	}

	// Full preset should have the most tools
	var fullInfo *PresetInfo
	for i := range infos {
		if infos[i].Name == PresetFull {
			fullInfo = &infos[i]
			break
		}
	}
	if fullInfo == nil {
		t.Fatal("full preset not found")
	}
	for _, info := range infos {
		if info.Name != PresetFull && info.ToolCount > fullInfo.ToolCount {
			t.Errorf("preset %s has more tools (%d) than full (%d)",
				info.Name, info.ToolCount, fullInfo.ToolCount)
		}
	}
}
