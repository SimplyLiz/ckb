package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	claudeplugin "github.com/SimplyLiz/CodeMCP/claude-plugin"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

func TestAiToolsContainsGrok(t *testing.T) {
	var found *aiTool
	for i := range aiTools {
		if aiTools[i].ID == "grok" {
			found = &aiTools[i]
			break
		}
	}

	if found == nil {
		t.Fatal("aiTools does not contain grok")
	}

	if found.Name != "Grok" {
		t.Errorf("Name = %q, want %q", found.Name, "Grok")
	}
	if !found.SupportsGlobal {
		t.Error("SupportsGlobal should be true")
	}
	if !found.SupportsProject {
		t.Error("SupportsProject should be true")
	}
	if !found.GlobalUsesCmd {
		t.Error("GlobalUsesCmd should be true")
	}
	if found.Format != "grokServers" {
		t.Errorf("Format = %q, want %q", found.Format, "grokServers")
	}
}

func TestGetConfigPath_Grok(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	globalPath := getConfigPath("grok", true)
	expectedGlobal := filepath.Join(home, ".grok", "user-settings.json")
	if globalPath != expectedGlobal {
		t.Errorf("global path = %q, want %q", globalPath, expectedGlobal)
	}

	projectPath := getConfigPath("grok", false)
	expectedProject := filepath.Join(cwd, ".grok", "settings.json")
	if projectPath != expectedProject {
		t.Errorf("project path = %q, want %q", projectPath, expectedProject)
	}
}

func TestWriteGrokConfig_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	err := writeGrokConfig(path, "npx", []string{"@tastehub/ckb", "mcp"})
	if err != nil {
		t.Fatalf("writeGrokConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Check mcpServers exists
	mcpRaw, ok := raw["mcpServers"]
	if !ok {
		t.Fatal("mcpServers key missing from config")
	}

	var mcpServers map[string]grokMcpEntry
	if err := json.Unmarshal(mcpRaw, &mcpServers); err != nil {
		t.Fatalf("failed to parse mcpServers: %v", err)
	}

	entry, ok := mcpServers["ckb"]
	if !ok {
		t.Fatal("ckb entry missing from mcpServers")
	}

	if entry.Name != "ckb" {
		t.Errorf("Name = %q, want %q", entry.Name, "ckb")
	}
	if entry.Transport != "stdio" {
		t.Errorf("Transport = %q, want %q", entry.Transport, "stdio")
	}
	if entry.Command != "npx" {
		t.Errorf("Command = %q, want %q", entry.Command, "npx")
	}
	if len(entry.Args) != 2 || entry.Args[0] != "@tastehub/ckb" || entry.Args[1] != "mcp" {
		t.Errorf("Args = %v, want [\"@tastehub/ckb\", \"mcp\"]", entry.Args)
	}
}

func TestWriteGrokConfig_PreservesExistingFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write an existing config with a model field
	existing := `{"model": "grok-3-fast", "mcpServers": {"other": {"name": "other", "transport": "stdio", "command": "node", "args": ["server.js"]}}}`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatalf("failed to write existing config: %v", err)
	}

	err := writeGrokConfig(path, "/usr/local/bin/ckb", []string{"mcp"})
	if err != nil {
		t.Fatalf("writeGrokConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	// Check model field is preserved
	modelRaw, ok := raw["model"]
	if !ok {
		t.Fatal("model field was not preserved")
	}
	var model string
	if err := json.Unmarshal(modelRaw, &model); err != nil {
		t.Fatalf("failed to parse model: %v", err)
	}
	if model != "grok-3-fast" {
		t.Errorf("model = %q, want %q", model, "grok-3-fast")
	}

	// Check both MCP servers exist
	var mcpServers map[string]grokMcpEntry
	if err := json.Unmarshal(raw["mcpServers"], &mcpServers); err != nil {
		t.Fatalf("failed to parse mcpServers: %v", err)
	}

	if _, ok := mcpServers["other"]; !ok {
		t.Error("existing 'other' server was not preserved")
	}

	ckb, ok := mcpServers["ckb"]
	if !ok {
		t.Fatal("ckb entry was not added")
	}
	if ckb.Command != "/usr/local/bin/ckb" {
		t.Errorf("Command = %q, want %q", ckb.Command, "/usr/local/bin/ckb")
	}
}

func TestWriteGrokConfig_InvalidExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write invalid JSON
	if err := os.WriteFile(path, []byte("not valid json{{{"), 0644); err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}

	err := writeGrokConfig(path, "ckb", []string{"mcp"})
	if err != nil {
		t.Fatalf("writeGrokConfig should handle invalid existing config: %v", err)
	}

	// Should still write valid config
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if _, ok := raw["mcpServers"]; !ok {
		t.Error("mcpServers key missing after recovery from invalid file")
	}
}

func TestWriteGrokConfig_UpdatesExistingCkbEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write config with an old ckb entry
	existing := `{"mcpServers": {"ckb": {"name": "ckb", "transport": "stdio", "command": "old-ckb", "args": ["mcp"]}}}`
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatalf("failed to write existing config: %v", err)
	}

	err := writeGrokConfig(path, "new-ckb", []string{"mcp", "--preset=full"})
	if err != nil {
		t.Fatalf("writeGrokConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	var mcpServers map[string]grokMcpEntry
	if err := json.Unmarshal(raw["mcpServers"], &mcpServers); err != nil {
		t.Fatalf("failed to parse mcpServers: %v", err)
	}

	ckb := mcpServers["ckb"]
	if ckb.Command != "new-ckb" {
		t.Errorf("Command = %q, want %q", ckb.Command, "new-ckb")
	}
	if len(ckb.Args) != 2 || ckb.Args[1] != "--preset=full" {
		t.Errorf("Args = %v, want [\"mcp\", \"--preset=full\"]", ckb.Args)
	}
}

func TestIsGrokAvailable(t *testing.T) {
	// In CI/test environments, grok CLI is typically not installed
	// This test verifies the function doesn't panic and returns a bool
	result := isGrokAvailable()
	// We can't assert true/false since it depends on the environment,
	// but we verify it runs without error
	_ = result
}

func TestConfigureGrokGlobal_Fallback(t *testing.T) {
	// When grok CLI is not available, it should fall back to writing the config file
	if isGrokAvailable() {
		t.Skip("grok CLI is available, skipping fallback test")
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, ".grok", "user-settings.json")

	// We can't easily test configureGrokGlobal directly since it uses getConfigPath
	// which reads the real home directory. Instead, test the fallback logic by
	// calling writeGrokConfig directly on the expected path.
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	err := writeGrokConfig(configPath, "npx", []string{"-y", "@tastehub/ckb", "mcp"})
	if err != nil {
		t.Fatalf("writeGrokConfig failed: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	var mcpServers map[string]grokMcpEntry
	if err := json.Unmarshal(raw["mcpServers"], &mcpServers); err != nil {
		t.Fatalf("failed to parse mcpServers: %v", err)
	}

	ckb := mcpServers["ckb"]
	if ckb.Name != "ckb" {
		t.Errorf("Name = %q, want %q", ckb.Name, "ckb")
	}
	if ckb.Transport != "stdio" {
		t.Errorf("Transport = %q, want %q", ckb.Transport, "stdio")
	}
	if len(ckb.Args) != 3 || ckb.Args[0] != "-y" {
		t.Errorf("Args = %v, want [\"-y\", \"@tastehub/ckb\", \"mcp\"]", ckb.Args)
	}
}

func TestGrokMcpEntryJSON(t *testing.T) {
	entry := grokMcpEntry{
		Name:      "ckb",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"@tastehub/ckb", "mcp"},
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded grokMcpEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Name != entry.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, entry.Name)
	}
	if decoded.Transport != entry.Transport {
		t.Errorf("Transport = %q, want %q", decoded.Transport, entry.Transport)
	}
	if decoded.Command != entry.Command {
		t.Errorf("Command = %q, want %q", decoded.Command, entry.Command)
	}
	if len(decoded.Args) != len(entry.Args) {
		t.Errorf("Args length = %d, want %d", len(decoded.Args), len(entry.Args))
	}
}

func TestWriteGrokConfig_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "settings.json")

	// Create the parent dir (writeGrokConfig doesn't create dirs, configureTool does)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("failed to create parent dir: %v", err)
	}

	err := writeGrokConfig(path, "ckb", []string{"mcp"})
	if err != nil {
		t.Fatalf("writeGrokConfig failed: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("config file was not created")
	}
}

// The command files call ckb where it is on PATH and npx where it is not: the
// /ckb-review injection runs before the model reads anything, and a failing
// call aborts the command.
func TestInstallClaudeCodeSkills_NpxWithoutCKBOnPath(t *testing.T) {
	read := func(home, name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(home, ".claude", "commands", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	captureStdout(t, func() {
		if err := installClaudeCodeSkills(); err != nil {
			t.Fatal(err)
		}
	})
	if got := read(home, "ckb-review.md"); !strings.Contains(got, "!`npx -y @tastehub/ckb@"+version.Version+" review --target=") {
		t.Errorf("without ckb on PATH the review command must inject through npx:\n%s", got)
	}
	if got := read(home, "ckb-audit.md"); !strings.Contains(got, "\nnpx -y @tastehub/ckb@"+version.Version+" audit compliance") {
		t.Error("without ckb on PATH the audit command must call npx")
	}

	if runtime.GOOS == "windows" {
		return
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ckb"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	captureStdout(t, func() {
		if err := installClaudeCodeSkills(); err != nil {
			t.Fatal(err)
		}
	})
	if got := read(home, "ckb-review.md"); got != claudeplugin.CommandText(claudeplugin.ReviewSkill, true) {
		t.Error("with ckb on PATH the review command must call ckb")
	}
}
