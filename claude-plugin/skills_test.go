package claudeplugin

import (
	"strings"
	"testing"
)

// The skills are written verbatim to ~/.claude/commands by `ckb setup`, so
// they have to stay valid as command files: frontmatter with a description,
// and a body that still takes $ARGUMENTS.
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
		if !strings.Contains(text, "npx -y @tastehub/ckb") {
			t.Errorf("%s: lost the npx fallback for users without ckb on PATH", name)
		}
	}
}
