package claudeplugin

import (
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
