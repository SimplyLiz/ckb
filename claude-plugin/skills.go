// Package claudeplugin is CKB's Claude Code plugin: this directory is what
// `.claude-plugin/marketplace.json` at the repo root points at, and what
// `claude plugin install ckb@lisa` copies to the user's machine.
//
// The skill texts under skills/ are the one place they are written down.
// `ckb setup --tool=claude-code` installs the same files as slash commands
// for users who do not use the plugin, so it embeds them from here instead of
// carrying a second copy. Claude Code ignores this Go file when it loads the
// plugin.
package claudeplugin

import (
	_ "embed"
	"regexp"
)

// ReviewSkill is skills/review/SKILL.md: frontmatter plus the /ckb:review body.
//
//go:embed skills/review/SKILL.md
var ReviewSkill string

// AuditSkill is skills/audit/SKILL.md: frontmatter plus the /ckb:audit body.
//
//go:embed skills/audit/SKILL.md
var AuditSkill string

// NpxCommand runs CKB without an install.
const NpxCommand = "npx -y @tastehub/ckb"

// ckbCall matches `ckb` where a skill runs it: at the start of a line (bash
// blocks), after a backtick (inline commands and !`...` injection) and in an
// allowed-tools rule. Prose says "CKB".
var ckbCall = regexp.MustCompile("(?m)(^|`|Bash\\()ckb ")

// CommandText is skill as a command file for ~/.claude/commands. The skills
// call `ckb`, which the plugin's bin/ckb provides; a command file has no
// plugin behind it, so where ckb is not on PATH (onPath false) every call
// becomes NpxCommand. Otherwise the text is the skill as is.
func CommandText(skill string, onPath bool) string {
	if onPath {
		return skill
	}
	return ckbCall.ReplaceAllString(skill, "${1}"+NpxCommand+" ")
}
