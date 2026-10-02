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

import _ "embed"

// ReviewSkill is skills/review/SKILL.md: frontmatter plus the /ckb:review body.
//
//go:embed skills/review/SKILL.md
var ReviewSkill string

// AuditSkill is skills/audit/SKILL.md: frontmatter plus the /ckb:audit body.
//
//go:embed skills/audit/SKILL.md
var AuditSkill string
