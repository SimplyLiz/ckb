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
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// ReviewSkill is skills/review/SKILL.md: frontmatter plus the /ckb:review body.
//
//go:embed skills/review/SKILL.md
var ReviewSkill string

// AuditSkill is skills/audit/SKILL.md: frontmatter plus the /ckb:audit body.
//
//go:embed skills/audit/SKILL.md
var AuditSkill string

// NpxCommand runs CKB without an install, pinned to the version that writes
// the command file: the skills depend on its CLI flags.
var NpxCommand = "npx -y @tastehub/ckb@" + version.Version

// PluginCKB is how the skills call CKB: the plugin's scripts/ckb, whose path
// Claude Code substitutes into the skill text when it loads it. Quoted, so a
// plugin root with a space stays one word; the allowed-tools rule carries the
// same quotes and matches.
const PluginCKB = `"${CLAUDE_PLUGIN_ROOT}/scripts/ckb"`

// CommandText is skill as a command file for ~/.claude/commands. A command
// file has no plugin behind it and no ${CLAUDE_PLUGIN_ROOT}, so every
// PluginCKB becomes `ckb` where ckb is on PATH (onPath true) and NpxCommand
// otherwise.
func CommandText(skill string, onPath bool) string {
	call := NpxCommand
	if onPath {
		call = "ckb"
	}
	return strings.ReplaceAll(skill, PluginCKB, call)
}
