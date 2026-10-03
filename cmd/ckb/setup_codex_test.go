package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
	if !found.SupportsProject {
		t.Error("SupportsProject should be true — Codex CLI supports trusted project-local .codex/config.toml")
	}
	if found.Format != "codexToml" {
		t.Errorf("Format = %q, want %q", found.Format, "codexToml")
	}
}

func TestGetConfigPath_Codex(t *testing.T) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	wantGlobal := filepath.Join(home, ".codex", "config.toml")
	if got := getConfigPath("codex", true); got != wantGlobal {
		t.Errorf("global path = %q, want %q", got, wantGlobal)
	}

	wantProject := filepath.Join(cwd, ".codex", "config.toml")
	if got := getConfigPath("codex", false); got != wantProject {
		t.Errorf("project path = %q, want %q", got, wantProject)
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

// TestUpsertCodexMCPServer_PreservesInlineCommentOnCommandLine covers the P2
// finding: a trailing comment on the replaced "command" line
// (command = "npx" # pinned for CI) used to be dropped entirely along with
// the whole line — upsertTOMLTable filtered by key name and threw the whole
// line away. The comment must survive, reattached to the new command line.
func TestUpsertCodexMCPServer_PreservesInlineCommentOnCommandLine(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "npx" # pinned for CI
args = ["mcp"]
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, "# pinned for CI") {
		t.Errorf("inline comment on replaced command line was dropped:\n%s", got)
	}
	if !strings.Contains(got, `command = "new-ckb"`) {
		t.Errorf("command was not updated:\n%s", got)
	}
	if strings.Contains(got, "npx") {
		t.Errorf("old command value was not replaced:\n%s", got)
	}
}

// TestUpsertCodexMCPServer_PreservesInlineCommentOnArgsLine covers the same
// fix for the "args" key.
func TestUpsertCodexMCPServer_PreservesInlineCommentOnArgsLine(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "ckb"
args = ["mcp"] # do not remove --watch below without checking with ops
`
	got := upsertCodexMCPServer(existing, "ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, "# do not remove --watch below without checking with ops") {
		t.Errorf("inline comment on replaced args line was dropped:\n%s", got)
	}
	if !strings.Contains(got, `args = ["mcp", "--watch"]`) {
		t.Errorf("args was not updated:\n%s", got)
	}
}

// TestUpsertCodexMCPServer_MultilineArgs_ReplacesWholeSpan covers the P2
// finding for a multiline args array:
//
//	args = [
//	  "mcp",
//	  "--watch",
//	]
//
// Filtering line-by-line by key name only matched the opening "args = ["
// line — the continuation lines ("mcp",, "--watch",, ]) don't look like a
// "key = value" line, so they were left behind as stray "kept" lines next
// to the new single-line args, corrupting the table. The whole array span
// must be dropped and replaced as one unit.
func TestUpsertCodexMCPServer_MultilineArgs_ReplacesWholeSpan(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "old-ckb"
args = [
  "mcp",
  "--preset=full",
]
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp", "--watch"})

	if strings.Contains(got, "--preset=full") {
		t.Errorf("old multiline args entries were not removed:\n%s", got)
	}
	if strings.Contains(got, "\"mcp\",\n") {
		t.Errorf("stray multiline array continuation line survived:\n%s", got)
	}
	if !strings.Contains(got, `args = ["mcp", "--watch"]`) {
		t.Errorf("new args line missing/incorrect:\n%s", got)
	}
	// The result must still be valid TOML: exactly one args key, no
	// dangling "]" left over from the old array.
	if count := strings.Count(got, "args ="); count != 1 {
		t.Errorf("expected exactly one 'args =' line, found %d:\n%s", count, got)
	}

	var probe any
	if _, err := toml.Decode(got, &probe); err != nil {
		t.Errorf("result is not valid TOML: %v\n%s", err, got)
	}
}

// TestUpsertCodexMCPServer_MultilineArgs_TrailingCommentOnClosingBracket
// covers a trailing comment on the closing bracket of a multiline array —
// it should be preserved the same way a single-line trailing comment is.
func TestUpsertCodexMCPServer_MultilineArgs_TrailingCommentOnClosingBracket(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "old-ckb"
args = [
  "mcp",
] # keep in sync with prod
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, "# keep in sync with prod") {
		t.Errorf("trailing comment on multiline array's closing bracket was dropped:\n%s", got)
	}
	if !strings.Contains(got, `args = ["mcp", "--watch"]`) {
		t.Errorf("new args line missing/incorrect:\n%s", got)
	}
}

func TestWriteCodexConfig_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeCodexConfig(path, "ckb", []string{"mcp", "--watch"}, false); err != nil {
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
	if err := writeCodexConfig(path, "ckb", []string{"mcp", "--watch", "--preset=review"}, false); err != nil {
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

// --- robustness: inline comments, quoted keys, subtables, CRLF, validation ---

// TestUpsertCodexMCPServer_HeaderWithInlineComment covers the P1 finding:
// a header line like "[mcp_servers.ckb] # comment" must still be recognized
// as the existing table, not treated as absent (which would append a second,
// TOML-illegal duplicate [mcp_servers.ckb] table).
func TestUpsertCodexMCPServer_HeaderWithInlineComment(t *testing.T) {
	existing := `[mcp_servers.ckb] # managed by ckb setup
command = "old-ckb"
args = ["mcp"]
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp"})

	if count := strings.Count(got, "[mcp_servers.ckb]"); count != 1 {
		t.Fatalf("expected exactly one ckb header, found %d:\n%s", count, got)
	}
	if strings.Contains(got, "old-ckb") {
		t.Errorf("old command was not replaced:\n%s", got)
	}
	if !strings.Contains(got, `command = "new-ckb"`) {
		t.Errorf("new command missing:\n%s", got)
	}
	if !strings.Contains(got, "# managed by ckb setup") {
		t.Errorf("inline comment on header line was dropped:\n%s", got)
	}
}

// TestUpsertCodexMCPServer_QuotedTableKey covers quoted-key header spellings
// ([mcp_servers."ckb"]), which are valid TOML equivalent to [mcp_servers.ckb].
func TestUpsertCodexMCPServer_QuotedTableKey(t *testing.T) {
	existing := `[mcp_servers."ckb"]
command = "old-ckb"
args = ["mcp"]
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp"})

	if count := strings.Count(got, "old-ckb"); count != 0 {
		t.Errorf("old command not replaced, quoted header wasn't recognized:\n%s", got)
	}
	if !strings.Contains(got, `command = "new-ckb"`) {
		t.Errorf("new command missing:\n%s", got)
	}
}

// TestUpsertCodexMCPServer_PreservesEnvSubtable covers the requirement that
// a [mcp_servers.ckb.env] subtable (holding user-configured env vars) is
// never touched or deleted by the replace, since it lives outside the
// bare-key block that gets rewritten.
func TestUpsertCodexMCPServer_PreservesEnvSubtable(t *testing.T) {
	existing := `[mcp_servers.ckb]
command = "old-ckb"
args = ["mcp"]

[mcp_servers.ckb.env]
CKB_REPO = "/some/repo"
CUSTOM_VAR = "1"
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp", "--watch"})

	if !strings.Contains(got, "[mcp_servers.ckb.env]") {
		t.Fatalf("env subtable header was dropped:\n%s", got)
	}
	if !strings.Contains(got, `CKB_REPO = "/some/repo"`) || !strings.Contains(got, `CUSTOM_VAR = "1"`) {
		t.Errorf("env subtable contents were dropped:\n%s", got)
	}
	if !strings.Contains(got, `command = "new-ckb"`) {
		t.Errorf("command was not updated:\n%s", got)
	}
	if strings.Contains(got, "old-ckb") {
		t.Errorf("old command was not replaced:\n%s", got)
	}
}

// TestUpsertCodexMCPServer_PreservesCommentsInBlock covers a stray comment
// line living inside the ckb table alongside command/args — it must survive
// a replace, not just the lines outside the table.
func TestUpsertCodexMCPServer_PreservesCommentsInBlock(t *testing.T) {
	existing := `[mcp_servers.ckb]
# do not remove this
command = "old-ckb"
args = ["mcp"]
`
	got := upsertCodexMCPServer(existing, "new-ckb", []string{"mcp"})

	if !strings.Contains(got, "# do not remove this") {
		t.Errorf("comment inside table block was dropped:\n%s", got)
	}
}

func TestWriteCodexConfig_PreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	existing := "model = \"gpt-5\"\r\n\r\n[mcp_servers.other]\r\ncommand = \"node\"\r\n"
	if err := os.WriteFile(path, []byte(existing), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, false); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if strings.Contains(string(data), "\n") && !strings.Contains(string(data), "\r\n") {
		t.Errorf("expected CRLF line endings to be preserved, got:\n%q", data)
	}
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("CRLF line endings were not preserved:\n%q", data)
	}
}

func TestWriteCodexConfig_InvalidExistingTOML_AbortsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	invalid := "this is not [valid toml\ncommand = \n"
	if err := os.WriteFile(path, []byte(invalid), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	err := writeCodexConfig(path, "ckb", []string{"mcp"}, false)
	if err == nil {
		t.Fatal("expected an error for invalid existing TOML, got nil")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("failed to read file: %v", readErr)
	}
	if string(data) != invalid {
		t.Errorf("file content should be untouched on invalid-TOML abort, got:\n%s", data)
	}
}

func TestWriteCodexConfig_AtomicWrite_NoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, false); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.toml" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected only config.toml in dir, found: %v", names)
	}
}

// --- inline-table / dotted-key ckb entries: refuse to rewrite, not corrupt ---

// TestWriteCodexConfig_InlineTableCkbEntry_RefusesWithoutWriting covers the
// P1 finding: given a valid TOML inline table
//
//	[mcp_servers]
//	ckb = { command = "old", args = ["mcp"] }
//
// upsertTOMLTable used to only look for a `[mcp_servers.ckb]` header line,
// find none, and append one — but TOML forbids extending an inline table
// with a later header, so the generated file failed the
// validate-before-write check and setup aborted with a raw TOML parse
// error. writeCodexConfig must instead detect the inline table up front,
// leave the file untouched, and return an actionable error with the exact
// snippet to paste by hand.
func TestWriteCodexConfig_InlineTableCkbEntry_RefusesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := "[mcp_servers]\nckb = { command = \"old\", args = [\"mcp\"] }\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	err := writeCodexConfig(path, "new-ckb", []string{"mcp", "--watch"}, false)
	if err == nil {
		t.Fatal("expected an error for an inline-table ckb entry, got nil")
	}
	if !strings.Contains(err.Error(), "inline-table") && !strings.Contains(err.Error(), "dotted-key") {
		t.Errorf("error should explain the inline-table/dotted-key situation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "[mcp_servers.ckb]") {
		t.Errorf("error should include the manual snippet to paste, got: %v", err)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("failed to read file: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("file content should be untouched, got:\n%s", data)
	}
}

// TestWriteCodexConfig_DottedKeyCkbEntry_TopLevel_RefusesWithoutWriting
// covers the fully-dotted spelling with no table headers at all:
//
//	mcp_servers.ckb.command = "old"
//	mcp_servers.ckb.args = ["mcp"]
func TestWriteCodexConfig_DottedKeyCkbEntry_TopLevel_RefusesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := "mcp_servers.ckb.command = \"old\"\nmcp_servers.ckb.args = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	err := writeCodexConfig(path, "new-ckb", []string{"mcp"}, false)
	if err == nil {
		t.Fatal("expected an error for a dotted-key ckb entry, got nil")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("failed to read file: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("file content should be untouched, got:\n%s", data)
	}
}

// TestWriteCodexConfig_DottedKeyCkbEntry_UnderHeader_RefusesWithoutWriting
// covers dotted keys nested under a [mcp_servers] header (not
// [mcp_servers.ckb]) — the same unsupported shape, just a different
// starting point:
//
//	[mcp_servers]
//	ckb.command = "old"
//	ckb.args = ["mcp"]
func TestWriteCodexConfig_DottedKeyCkbEntry_UnderHeader_RefusesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := "[mcp_servers]\nckb.command = \"old\"\nckb.args = [\"mcp\"]\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	err := writeCodexConfig(path, "new-ckb", []string{"mcp"}, false)
	if err == nil {
		t.Fatal("expected an error for a dotted-key ckb entry under [mcp_servers], got nil")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("failed to read file: %v", readErr)
	}
	if string(data) != original {
		t.Errorf("file content should be untouched, got:\n%s", data)
	}
}

// TestWriteCodexConfig_MCPServersHeaderWithoutCkb_StillAppendsNormally is the
// regression guard: an [mcp_servers] header with unrelated entries (no ckb
// key at all) must NOT be mistaken for an inline/dotted ckb entry — setup
// should append a normal [mcp_servers.ckb] table exactly as before.
func TestWriteCodexConfig_MCPServersHeaderWithoutCkb_StillAppendsNormally(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	original := "[mcp_servers]\nother = { command = \"node\" }\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, false); err != nil {
		t.Fatalf("writeCodexConfig should succeed when mcp_servers has no ckb entry: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if !strings.Contains(string(data), "[mcp_servers.ckb]") {
		t.Errorf("expected [mcp_servers.ckb] to be appended, got:\n%s", data)
	}
}

// --- file mode: new global 0600, new project 0644, existing mode preserved ---

// TestWriteCodexConfig_NewGlobalFileIs0600 covers the P1 finding: a freshly
// created ~/.codex/config.toml may end up holding secrets in a
// [mcp_servers.*.env] subtable another tool (or the user) adds later, so CKB
// must create it private-to-owner from the start rather than the general
// 0644 default.
func TestWriteCodexConfig_NewGlobalFileIs0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, true); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("new global config mode = %o, want 0600", perm)
	}
}

// TestWriteCodexConfig_NewProjectFileIs0644 covers the project-scope half of
// the same fix: <repo>/.codex/config.toml has no more reason to be private
// than any other project config file CKB writes (.mcp.json, .vscode/mcp.json,
// etc.), all of which are 0644.
func TestWriteCodexConfig_NewProjectFileIs0644(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, false); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0644 {
		t.Errorf("new project config mode = %o, want 0644", perm)
	}
}

// TestWriteCodexConfig_PreservesExistingMode_Global covers the P1 regression:
// re-running 'ckb setup --tool=codex --global' against a config.toml the
// user (or Codex itself) had already locked down to 0600 must not widen it
// back to 0644/0600-by-default logic — the atomic replace has to carry the
// existing file's mode forward untouched.
func TestWriteCodexConfig_PreservesExistingMode_Global(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("[mcp_servers.ckb]\ncommand = \"old\"\nargs = [\"mcp\"]\n"), 0600); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	if err := writeCodexConfig(path, "ckb", []string{"mcp", "--watch"}, true); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("existing 0600 config was not preserved, mode = %o", perm)
	}
}

// TestWriteCodexConfig_PreservesExistingMode_UnusualPermissions covers the
// general case (not just the 0600 round-trip): whatever mode the file
// already had — even one CKB would never choose itself — must survive a
// rewrite unchanged.
func TestWriteCodexConfig_PreservesExistingMode_UnusualPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := os.WriteFile(path, []byte("[mcp_servers.ckb]\ncommand = \"old\"\nargs = [\"mcp\"]\n"), 0640); err != nil {
		t.Fatalf("failed to seed existing config: %v", err)
	}

	if err := writeCodexConfig(path, "ckb", []string{"mcp"}, false); err != nil {
		t.Fatalf("writeCodexConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat written file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0640 {
		t.Errorf("existing 0640 config was not preserved, mode = %o", perm)
	}
}

// --- --watch / --no-watch / --no-index wiring through runSetup ---

// resetSetupFlags saves the current package-level setup flag values and
// returns a func to restore them, so end-to-end runSetup tests don't leak
// state into other tests (these are cobra-bound package vars, not locals).
func resetSetupFlags(t *testing.T) func() {
	t.Helper()
	oldGlobal, oldNpx, oldTool, oldPreset, oldNoIndex, oldNoWatch, oldIndexNow :=
		setupGlobal, setupNpx, setupTool, setupPreset, setupNoIndex, setupNoWatch, setupIndexNow
	return func() {
		setupGlobal, setupNpx, setupTool, setupPreset, setupNoIndex, setupNoWatch, setupIndexNow =
			oldGlobal, oldNpx, oldTool, oldPreset, oldNoIndex, oldNoWatch, oldIndexNow
	}
}

func TestRunSetup_CodexGlobal_IncludesWatchByDefault(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // in case of cross-platform home lookup

	setupTool = "codex"
	setupGlobal = true // explicitly test the --global path (writes ~/.codex/config.toml)
	setupPreset = "core"
	setupNoIndex = true
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
	setupGlobal = true
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

// TestRunSetup_CodexProject_WritesRepoLocalConfig verifies the P0 fix: Codex
// project-scope setup writes <repo>/.codex/config.toml (and runs the
// init/index auto-readiness path, same as any other project-scope tool) —
// it no longer silently forces --global and skips readiness entirely.
func TestRunSetup_CodexProject_WritesRepoLocalConfig(t *testing.T) {
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

	setupTool = "codex"
	setupGlobal = false
	setupPreset = "core"
	setupNoIndex = true // keep the test fast/hermetic; init-only is exercised elsewhere
	setupNoWatch = false
	setupNpx = true

	if err := runSetup(nil, nil); err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".ckb")); statErr != nil {
		t.Errorf("project scope should have run ensureProjectReady (.ckb/ missing): %v", statErr)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("expected <repo>/.codex/config.toml, got: %v", err)
	}
	if !strings.Contains(string(data), "[mcp_servers.ckb]") {
		t.Errorf("missing ckb table in project-local codex config:\n%s", data)
	}

	// The global config must not have been touched.
	home, _ := os.UserHomeDir()
	if _, statErr := os.Stat(filepath.Join(home, ".codex", "config.toml")); statErr == nil {
		t.Error("project-scope setup should not have written the global ~/.codex/config.toml")
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
// returned outcomes. Uses --index-now to force the foreground path this
// actually exercises: with --watch's non-blocking default, ensureProjectReady
// wouldn't call performIndex at all here, so the "no language" outcome would
// never be reached.
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
	setupIndexNow = true

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady should not fail setup when no language is detected: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".ckb")); statErr != nil {
		t.Errorf(".ckb/ was not created: %v", statErr)
	}
}

// TestEnsureProjectReady_WatchDefault_SkipsForegroundIndex covers the P2
// fix: with --watch enabled (the default) and neither --index-now nor
// --no-watch set, ensureProjectReady must not block on a foreground index —
// it initializes .ckb/ and leaves indexing for the watch loop to pick up
// once the MCP server actually starts (runWatchLoop's immediate first
// check, tested in mcp_test.go).
func TestEnsureProjectReady_WatchDefault_SkipsForegroundIndex(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	// If ensureProjectReady regressed to indexing in the foreground here,
	// this fake indexer would produce index.scip and the assertion below
	// would catch it.
	writeFakeIndexer(t, "scip-go", `: > index.scip`)

	setupNoIndex = false
	setupNoWatch = false
	setupIndexNow = false

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, ".ckb")); statErr != nil {
		t.Errorf(".ckb/ was not created: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr == nil {
		t.Error("index.scip should not exist yet — indexing should be deferred to watch mode, not run in the foreground")
	}
}

// TestEnsureProjectReady_IndexNow_ForcesForegroundIndex covers --index-now:
// even with --watch enabled (the default), it must still build the index in
// the foreground before setup returns.
func TestEnsureProjectReady_IndexNow_ForcesForegroundIndex(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	// Manual temp dirs, not t.TempDir(): this exercises a real foreground
	// index run (real "go" toolchain probes for tier detection, plus
	// populateIncrementalTracking opening a sqlite db), which can leave
	// GOCACHE/Library/Caches content under $HOME after the test returns.
	// t.TempDir()'s synchronous cleanup can race that tail end of work;
	// os.RemoveAll ignoring its error avoids failing the test on a cleanup
	// timing race unrelated to what it verifies. Disabling the tier summary
	// below avoids most of that work outright.
	home, err := os.MkdirTemp("", "ckb-setup-home-*")
	if err != nil {
		t.Fatalf("failed to create temp home dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	t.Setenv("HOME", home)

	oldShowTier := indexShowTier
	indexShowTier = false
	defer func() { indexShowTier = oldShowTier }()

	dir, err := os.MkdirTemp("", "ckb-setup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	writeFakeIndexer(t, "scip-go", `: > index.scip`)

	setupNoIndex = false
	setupNoWatch = false
	setupIndexNow = true

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr != nil {
		t.Errorf("--index-now should have built the index in the foreground: %v", statErr)
	}
}

// TestEnsureProjectReady_NoWatch_ForcesForegroundIndex covers --no-watch:
// since the generated MCP config won't have a watch loop to build the index
// later, ensureProjectReady must still build it in the foreground —
// otherwise nothing ever would.
func TestEnsureProjectReady_NoWatch_ForcesForegroundIndex(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	// Manual temp dirs — see the comment in
	// TestEnsureProjectReady_IndexNow_ForcesForegroundIndex.
	home, err := os.MkdirTemp("", "ckb-setup-home-*")
	if err != nil {
		t.Fatalf("failed to create temp home dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	t.Setenv("HOME", home)

	oldShowTier := indexShowTier
	indexShowTier = false
	defer func() { indexShowTier = oldShowTier }()

	dir, err := os.MkdirTemp("", "ckb-setup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	writeFakeIndexer(t, "scip-go", `: > index.scip`)

	setupNoIndex = false
	setupNoWatch = true
	setupIndexNow = false

	if err := ensureProjectReady(); err != nil {
		t.Fatalf("ensureProjectReady failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr != nil {
		t.Errorf("--no-watch should have built the index in the foreground (nothing else would): %v", statErr)
	}
}

// TestRunSetup_IndexNow_BuildsForegroundIndex is the end-to-end wiring check
// for --index-now through the real runSetup flag/CLI path, not just
// ensureProjectReady directly.
func TestRunSetup_IndexNow_BuildsForegroundIndex(t *testing.T) {
	restore := resetSetupFlags(t)
	defer restore()

	// Manual temp dirs — see the comment in
	// TestEnsureProjectReady_IndexNow_ForcesForegroundIndex.
	home, err := os.MkdirTemp("", "ckb-setup-home-*")
	if err != nil {
		t.Fatalf("failed to create temp home dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(home) }()
	t.Setenv("HOME", home)

	oldShowTier := indexShowTier
	indexShowTier = false
	defer func() { indexShowTier = oldShowTier }()

	dir, err := os.MkdirTemp("", "ckb-setup-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}
	writeFakeIndexer(t, "scip-go", `: > index.scip`)

	setupTool = "codex"
	setupGlobal = false
	setupPreset = "core"
	setupNoIndex = false
	setupNoWatch = false
	setupIndexNow = true
	setupNpx = true

	if err := runSetup(nil, nil); err != nil {
		t.Fatalf("runSetup failed: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "index.scip")); statErr != nil {
		t.Errorf("--index-now should have built the index in the foreground: %v", statErr)
	}
}

// TestRunSetup_ConfigWrittenBeforeIndexing_EvenIfInitFails proves the
// ordering fix: the MCP config must already be on disk before
// ensureProjectReady's init/index step runs, so a slow indexer (or, as
// here, an init failure) never leaves the developer without a working
// config. It forces ensureCkbInitialized to fail by making the project
// directory read-only (so creating the new .ckb/ dir fails), while
// pre-creating .codex/ with normal permissions so the config write itself
// can still succeed — isolating which of the two steps actually failed.
func TestRunSetup_ConfigWrittenBeforeIndexing_EvenIfInitFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission checks don't apply")
	}

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

	// Pre-create .codex/ (so configureTool's MkdirAll is a no-op — it won't
	// need write access to the now-read-only parent) before locking dir down.
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0755); err != nil {
		t.Fatalf("failed to pre-create .codex: %v", err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("failed to chmod dir read-only: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0755) }() // restore before t.TempDir() cleanup runs

	setupTool = "codex"
	setupGlobal = false
	setupPreset = "core"
	setupNoIndex = false
	setupNoWatch = false
	setupNpx = true

	err = runSetup(nil, nil)
	if err == nil {
		t.Fatal("expected runSetup to fail — .ckb/ cannot be created in a read-only directory")
	}

	data, readErr := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if readErr != nil {
		t.Fatalf("MCP config should have been written before the failing init step, but: %v", readErr)
	}
	if !strings.Contains(string(data), "[mcp_servers.ckb]") {
		t.Errorf("written config missing ckb table:\n%s", data)
	}
}

// --- Windows npx wrapping ---

func TestCodexWindowsWrap_WrapsNpxOnWindows(t *testing.T) {
	cmd, args := codexWindowsWrap("windows", "npx", []string{"-y", "@tastehub/ckb", "mcp", "--watch"})

	if cmd != "cmd" {
		t.Errorf("command = %q, want %q", cmd, "cmd")
	}
	want := []string{"/c", "npx", "-y", "@tastehub/ckb", "mcp", "--watch"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestCodexWindowsWrap_NoOpOnNonWindows(t *testing.T) {
	cmd, args := codexWindowsWrap("darwin", "npx", []string{"-y", "@tastehub/ckb", "mcp"})
	if cmd != "npx" {
		t.Errorf("command = %q, want unchanged %q", cmd, "npx")
	}
	if len(args) != 3 || args[0] != "-y" {
		t.Errorf("args = %v, want unchanged", args)
	}
}

func TestCodexWindowsWrap_NoOpForLocalBinaryOnWindows(t *testing.T) {
	// A resolved local binary path (not npx) needs no shell wrapper even on
	// Windows — it's invoked directly.
	cmd, args := codexWindowsWrap("windows", `C:\Users\dev\ckb.exe`, []string{"mcp", "--watch"})
	if cmd != `C:\Users\dev\ckb.exe` {
		t.Errorf("command = %q, want unchanged", cmd)
	}
	if len(args) != 2 {
		t.Errorf("args = %v, want unchanged", args)
	}
}

func TestCodexWindowsWrap_HandlesNpxViaPathSuffix(t *testing.T) {
	// isNpxCommand also matches a full path ending in /npx (e.g. resolved
	// from a node_modules/.bin shim) — codexWindowsWrap must honor that too.
	cmd, args := codexWindowsWrap("windows", "/usr/local/bin/npx", []string{"mcp"})
	if cmd != "cmd" {
		t.Errorf("command = %q, want %q", cmd, "cmd")
	}
	want := []string{"/c", "/usr/local/bin/npx", "mcp"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}
