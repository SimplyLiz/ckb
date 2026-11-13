package claudeplugin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
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
	const inject = "!`ckb review --target='$ARGUMENTS' --format=json || true`"
	if !strings.Contains(ReviewSkill, "\n"+inject+"\n") {
		t.Errorf("review skill lost its injected ckb review line %q", inject)
	}
	if !strings.Contains(ReviewSkill, "\nallowed-tools: Bash(ckb review *)\n") {
		t.Error("review skill must allow the injected ckb review in allowed-tools")
	}
}

// Without the plugin's bin/ckb and without ckb on PATH, a command file must
// call npx everywhere, or the injected line fails before the model reads a word.
func TestCommandTextWithoutCKBOnPath(t *testing.T) {
	for name, skill := range map[string]string{"review": ReviewSkill, "audit": AuditSkill} {
		if got := CommandText(skill, true); got != skill {
			t.Errorf("%s: with ckb on PATH the text must stay as is", name)
		}
		got := CommandText(skill, false)
		for _, line := range strings.Split(got, "\n") {
			for _, call := range []string{"ckb review", "ckb audit"} {
				for _, at := range []string{"`" + call, "Bash(" + call} {
					if strings.Contains(line, at) {
						t.Errorf("%s: %q still calls ckb directly: %s", name, at, line)
					}
				}
				if strings.HasPrefix(line, call) {
					t.Errorf("%s: line still starts with %q: %s", name, call, line)
				}
			}
		}
	}
	got := CommandText(ReviewSkill, false)
	for _, want := range []string{
		"!`" + NpxCommand + " review --target='$ARGUMENTS'",
		"allowed-tools: Bash(" + NpxCommand + " review *)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review without ckb on PATH: missing %q", want)
		}
	}
	if strings.Contains(got, "npx -y @tastehub/npx") || !strings.Contains(got, "CKB-augmented") {
		t.Error("rewrite touched more than the calls")
	}
}

// The plugin's bin/ckb is what makes `ckb` in the skills work for plugin users.
func TestShimIsExecutable(t *testing.T) {
	info, err := os.Stat("bin/ckb")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("bin/ckb is not executable; Claude Code only puts executables on PATH")
	}
}

// hooks/hooks.json calls `ckb hook session-start` through the shim, for the
// sources where the model starts without CKB's index line in context, and may
// never fail the session: an older ckb without the command exits 1.
func TestSessionStartHook(t *testing.T) {
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
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err = json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	groups := cfg.Hooks["SessionStart"]
	if len(cfg.Hooks) != 1 || len(groups) != 1 || len(groups[0].Hooks) != 1 {
		t.Fatalf("want exactly one SessionStart hook and no other events, got %+v", cfg.Hooks)
	}
	if groups[0].Matcher != "startup|resume|clear|compact" {
		t.Errorf("matcher = %q", groups[0].Matcher)
	}
	h := groups[0].Hooks[0]
	if h.Type != "command" || h.Command != `"${CLAUDE_PLUGIN_ROOT}/bin/ckb" hook session-start 2>/dev/null || true` {
		t.Errorf("hook = %+v", h)
	}
	if h.Timeout <= 0 || h.Timeout > 30 {
		t.Errorf("timeout = %d s; the first response waits for SessionStart hooks", h.Timeout)
	}
}
