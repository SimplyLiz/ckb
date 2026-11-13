package claudeplugin

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// The skills are written to ~/.claude/commands by `ckb setup`, so they have to
// stay valid as command files: frontmatter with a description, and a body that
// still takes $ARGUMENTS.
func TestSkillsAreWellFormed(t *testing.T) {
	for name, text := range map[string]string{"review": ReviewSkill, "audit": AuditSkill} {
		if !strings.HasPrefix(text, "---\ndescription: \"") {
			t.Errorf("%s: must start with quoted-description frontmatter", name)
		}
		if strings.Count(text, "\n---\n") < 1 {
			t.Errorf("%s: frontmatter is not closed", name)
		}
		if !strings.Contains(text, "$ARGUMENTS") {
			t.Errorf("%s: body no longer reads $ARGUMENTS", name)
		}
	}
}

// /ckb:review runs ckb review before the model reads it. An injected command
// that fails or is not allowed aborts the whole skill, so it has to swallow its
// exit code and be covered by allowed-tools.
func TestReviewInjectsCKB(t *testing.T) {
	inject := "!`" + PluginCKB + " review --target='$ARGUMENTS' --compact || true`"
	if !strings.Contains(ReviewSkill, "\n"+inject+"\n") {
		t.Errorf("review skill lost its injected ckb review line %q", inject)
	}
	if !strings.Contains(ReviewSkill, "\nallowed-tools: Bash("+PluginCKB+" review *)\n") {
		t.Error("review skill must allow the injected ckb review in allowed-tools")
	}
}

// The skills call CKB only through the plugin's scripts/ckb: a bare `ckb` is
// not on the Bash tool's PATH for plugin users, and the version pin lives in
// the shim.
func TestSkillsCallTheShim(t *testing.T) {
	for name, skill := range map[string]string{"review": ReviewSkill, "audit": AuditSkill} {
		for _, line := range strings.Split(skill, "\n") {
			for _, call := range []string{"ckb review", "ckb audit"} {
				for _, at := range []string{"`" + call, "Bash(" + call} {
					if strings.Contains(line, at) {
						t.Errorf("%s: %q calls ckb without the shim: %s", name, at, line)
					}
				}
				if strings.HasPrefix(line, call) {
					t.Errorf("%s: line calls ckb without the shim: %s", name, line)
				}
			}
		}
	}
}

// A command file has no ${CLAUDE_PLUGIN_ROOT}: every shim call becomes `ckb`
// with ckb on PATH and the pinned npx call without, or the injected line fails
// before the model reads a word.
func TestCommandText(t *testing.T) {
	for name, skill := range map[string]string{"review": ReviewSkill, "audit": AuditSkill} {
		for _, onPath := range []bool{true, false} {
			if got := CommandText(skill, onPath); strings.Contains(got, "CLAUDE_PLUGIN_ROOT") {
				t.Errorf("%s (onPath %v): still names CLAUDE_PLUGIN_ROOT", name, onPath)
			}
		}
	}
	for onPath, call := range map[bool]string{true: "ckb", false: NpxCommand} {
		got := CommandText(ReviewSkill, onPath)
		for _, want := range []string{
			"!`" + call + " review --target='$ARGUMENTS'",
			"allowed-tools: Bash(" + call + " review *)",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("review (onPath %v): missing %q", onPath, want)
			}
		}
		if !strings.Contains(got, "CKB-augmented") {
			t.Error("rewrite touched more than the calls")
		}
	}
	if !strings.HasPrefix(NpxCommand, "npx -y @tastehub/ckb@") {
		t.Errorf("NpxCommand = %q, want a pinned version", NpxCommand)
	}
}

// claude.ai and org-wide distribution reject a plugin with a top-level bin/.
func TestShim(t *testing.T) {
	if _, err := os.Stat("bin"); err == nil {
		t.Error("claude-plugin/bin exists; claude.ai and org plugin sync reject a top-level bin/")
	}
	info, err := os.Stat("scripts/ckb")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("scripts/ckb is not executable")
	}
}

// .mcp.json starts the server through the shim, so MCP, skills and hook
// resolve ckb by one rule and one pin.
func TestMCPServerUsesShim(t *testing.T) {
	data, err := os.ReadFile(".mcp.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	s := cfg.MCPServers["ckb"]
	if s.Command != "${CLAUDE_PLUGIN_ROOT}/scripts/ckb" || len(s.Args) != 1 || s.Args[0] != "mcp" {
		t.Errorf("ckb server = %+v", s)
	}
}

// hooks/hooks.json has two SessionStart hooks. The status line runs
// synchronously through the shim, for the sources where the model starts
// without CKB's index line in context; it may never fail the session (an older
// ckb without the command exits 1) and never wait for npx (CKB_SHIM_NO_NPX).
// The installer runs in the background: a synchronous download would hold up
// the first response.
func TestSessionStartHooks(t *testing.T) {
	data, err := os.ReadFile("hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
				Async   bool   `json:"async"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	groups := cfg.Hooks["SessionStart"]
	if len(cfg.Hooks) != 1 || len(groups) != 1 || len(groups[0].Hooks) != 2 {
		t.Fatalf("want two SessionStart hooks and no other events, got %+v", cfg.Hooks)
	}
	if groups[0].Matcher != "startup|resume|clear|compact" {
		t.Errorf("matcher = %q", groups[0].Matcher)
	}
	status, install := groups[0].Hooks[0], groups[0].Hooks[1]
	if status.Type != "command" || status.Async ||
		status.Command != `CKB_SHIM_NO_NPX=1 "${CLAUDE_PLUGIN_ROOT}/scripts/ckb" hook session-start 2>/dev/null || true` {
		t.Errorf("status hook = %+v", status)
	}
	if status.Timeout <= 0 || status.Timeout > 5 {
		t.Errorf("status timeout = %d s; the first response waits for synchronous SessionStart hooks", status.Timeout)
	}
	if install.Type != "command" || !install.Async ||
		install.Command != `"${CLAUDE_PLUGIN_ROOT}/scripts/ckb-install"` {
		t.Errorf("install hook = %+v", install)
	}
}

// scripts/ckb-install reads the version from the shim's pin= line, so that
// line stays the only place the plugin's pin is written down.
func TestInstallerReadsShimPin(t *testing.T) {
	shim, err := os.ReadFile("scripts/ckb")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^pin=` + regexp.QuoteMeta(version.Version) + `$`).Match(shim) {
		t.Errorf("scripts/ckb has no pin=%s line; run go run scripts/sync-version.go", version.Version)
	}
	installer, err := os.ReadFile("scripts/ckb-install")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installer), `pin=$(sed -n 's/^pin=//p' "$dir/ckb")`) {
		t.Error("ckb-install no longer reads the pin from scripts/ckb")
	}
	if regexp.MustCompile(`(?m)^pin=[0-9]`).Match(installer) {
		t.Error("ckb-install writes its own pin; it must read the shim's")
	}
	if info, err := os.Stat("scripts/ckb-install"); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Error("scripts/ckb-install is missing or not executable")
	}
}
