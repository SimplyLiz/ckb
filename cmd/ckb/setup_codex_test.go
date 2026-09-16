package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAiToolsContainsCodex(t *testing.T) {
	var found *aiTool
	for i := range aiTools {
		if aiTools[i].ID == "codex" {
			found = &aiTools[i]
			break
		}
	}

	if found == nil {
		t.Fatal("aiTools does not contain codex")
	}
	if found.Name != "Codex" {
		t.Errorf("Name = %q, want %q", found.Name, "Codex")
	}
	if !found.SupportsGlobal {
		t.Error("SupportsGlobal should be true")
	}
	if found.SupportsProject {
		t.Error("SupportsProject should be false — Codex CLI only reads ~/.codex/config.toml")
	}
	if found.Format != "codexToml" {
		t.Errorf("Format = %q, want %q", found.Format, "codexToml")
	}
}

func TestGetConfigPath_Codex(t *testing.T) {
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".codex", "config.toml")

	if got := getConfigPath("codex", true); got != want {
		t.Errorf("global path = %q, want %q", got, want)
	}
	// Codex is global-only, but getConfigPath should still return something
	// sane rather than empty if ever called with global=false.
	if got := getConfigPath("codex", false); got != want {
		t.Errorf("project path = %q, want %q (codex is global-only)", got, want)
	}
}

// --- upsertCodexMCPServer / upsertTOMLTable: the textual TOML merge ---

func TestUpsertCodexMCPServer_EmptyFile(t *testing.T) {
	got := upsertCodexMCPServer("", "ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, "[mcp_servers.ckb]") {
		t.Fatalf("missing table header, got:\n%s", got)
	}
	if !strings.Contains(got, `command = "ckb"`) {
		t.Errorf("missing command line, got:\n%s", got)
	}
	if !strings.Contains(got, `args = ["mcp", "--watch"]`) {
		t.Errorf("missing/incorrect args line, got:\n%s", got)
	}
}

func TestUpsertCodexMCPServer_PreservesExistingOtherServers(t *testing.T) {
	existing := `# my codex config
model = "gpt-5"

[mcp_servers.other]
command = "node"
args = ["server.js"]

[some_other_table]
key = "value"
`

	got := upsertCodexMCPServer(existing, "/usr/local/bin/ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, `model = "gpt-5"`) {
		t.Error("top-level model field was clobbered")
	}
	if !strings.Contains(got, "[mcp_servers.other]") || !strings.Contains(got, `command = "node"`) {
		t.Error("existing 'other' mcp server was clobbered")
	}
	if !strings.Contains(got, "[some_other_table]") || !strings.Contains(got, `key = "value"`) {
		t.Error("unrelated table was clobbered")
	}
	if !strings.Contains(got, "[mcp_servers.ckb]") {
		t.Error("ckb table was not added")
	}
	if !strings.Contains(got, `command = "/usr/local/bin/ckb"`) {
		t.Error("ckb command was not written correctly")
	}
}

func TestUpsertCodexMCPServer_ReplacesExistingCkbBlock(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "old-ckb"
args = ["mcp"]

[mcp_servers.other]
command = "node"
args = ["server.js"]
`

	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp", "--watch", "--preset=full"})

	if strings.Contains(got, "old-ckb") {
		t.Errorf("old ckb command was not replaced, got:\n%s", got)
	}
	if !strings.Contains(got, `command = "new-ckb"`) {
		t.Errorf("new ckb command missing, got:\n%s", got)
	}
	if !strings.Contains(got, `args = ["mcp", "--watch", "--preset=full"]`) {
		t.Errorf("new ckb args missing/incorrect, got:\n%s", got)
	}
	// The other table, after the replaced block, must survive untouched.
	if !strings.Contains(got, "[mcp_servers.other]") || !strings.Contains(got, `command = "node"`) {
		t.Errorf("other mcp server was clobbered by the ckb block replacement, got:\n%s", got)
	}

	// Exactly one [mcp_servers.ckb] header must remain.
	if count := strings.Count(got, "[mcp_servers.ckb]"); count != 1 {
		t.Errorf("expected exactly one [mcp_servers.ckb] header, found %d", count)
	}
}

func TestWriteCodexConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeCodexConfig(path, "ckb", []string{"mcp", "--watch"}); err != nil {
		t.Fatalf("writeCodexConfig (create) failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if !strings.Contains(string(data), `args = ["mcp", "--watch"]`) {
		t.Fatalf("unexpected content after first write:\n%s", data)
	}

	// Second call must update in place, not duplicate the table.
	if err := writeCodexConfig(path, "ckb", []string{"mcp", "--watch", "--preset=review"}); err != nil {
		t.Fatalf("writeCodexConfig (update) failed: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read updated file: %v", err)
	}
	content := string(data)
	if count := strings.Count(content, "[mcp_servers.ckb]"); count != 1 {
		t.Fatalf("expected exactly one [mcp_servers.ckb] header after update, found %d:\n%s", count, content)
	}
	if !strings.Contains(content, `--preset=review`) {
		t.Errorf("update did not take effect:\n%s", content)
	}
}

// --- --watch / --no-watch / --no-index wiring through runSetup ---

// resetSetupFlags saves the current package-level setup flag values and
// returns a func to restore them, so end-to-end runSetup tests don't leak
// state into other tests (these are cobra-bound package vars, not locals).
func resetSetupFlags(t *testing.T) func() {
	t.Helper()
	oldGlobal, oldNpx, oldTool, oldPreset, oldNoIndex, oldNoWatch :=
		setupGlobal, setupNpx, setupTool, setupPreset, setupNoIndex, setupNoWatch
	return func() {
		setupGlobal, setupNpx, setupTool, setupPreset, setupNoIndex, setupNoWatch =
			oldGlobal, oldNpx, oldTool, oldPreset, oldNoIndex, oldNoWatch
	}
}

func TestRunSetup_CodexGlobal_IncludesWatchByDefault(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // in case of cross-platform home lookup

	setupTool = "codex"
	setupGlobal = false // codex forces global since SupportsProject is false
	setupPreset = "core"
	setupNoIndex = true // irrelevant here since codex is global-only, but be explicit
	setupNoWatch = false
	setupNpx = true // avoids depending on os.Executable() resolving a real ckb binary

	if err := runSetup(nil, nil); err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("failed to read generated codex config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `"--watch"`) {
		t.Errorf("expected --watch in generated args, got:\n%s", content)
	}
	if !strings.Contains(content, `"npx"`) {
		t.Errorf("expected npx command since --npx was set, got:\n%s", content)
	}
}

func TestRunSetup_NoWatch_OmitsWatchFlag(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	setupTool = "codex"
	setupGlobal = false
	setupPreset = "core"
	setupNoIndex = true
	setupNoWatch = true
	setupNpx = true

	if err := runSetup(nil, nil); err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("failed to read generated codex config: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "--watch") {
		t.Errorf("--no-watch was set; --watch should not appear, got:\n%s", content)
	}
}

// --- --no-index / project-scope auto-init+index wiring ---

// TestEnsureProjectReady_NoIndex_StillInits verifies --no-index skips the
// indexing step but still initializes .ckb/, so the MCP server has
// something to start against.
func TestEnsureProjectReady_NoIndex_StillInits(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	// runInit registers the repo in the global registry at $HOME/.ckb —
	// isolate that from the real user's registry.
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	setupNoIndex = true

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".ckb")); statErr != nil {
		t.Errorf(".ckb/ was not created even though init should still run: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr == nil {
		t.Error("index.scip should not exist — indexing should have been skipped by --no-index")
	}
}

// TestEnsureProjectReady_NoLanguage_DoesNotFailSetup verifies that when a
// project has no detectable language (so no indexer to speak of), setup
// still succeeds — the whole point of moving index.go's os.Exit calls to
// returned outcomes.
func TestEnsureProjectReady_NoLanguage_DoesNotFailSetup(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	setupNoIndex = false

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady should not fail setup when no language is detected: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".ckb")); statErr != nil {
		t.Errorf(".ckb/ was not created: %v", statErr)
	}
}
