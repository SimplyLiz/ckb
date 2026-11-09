package update

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

const (
	// noticeVersion is the release the text below was written for. A test holds
	// it equal to version.Version, so bumping the version without rewriting the
	// news fails the build instead of shipping last release's news.
	noticeVersion = "9.4.0"

	// noticeStateFile is kept apart from update-check.json because Cache.Set
	// rewrites that file whole, from a background goroutine.
	noticeStateFile = "notice-state.json"
)

var noticeText = "CKB " + noticeVersion + " is here. Thanks for updating!\n" +
	"\n" +
	"What's new\n" +
	"  • Done check: `ckb changes --check` lists every use of a changed\n" +
	"    declaration your diff left untouched\n" +
	"  • findReferences over MCP is far smaller: grouped by file by default\n" +
	"  • Upgrades no longer need a fresh .ckb\n" +
	"\n" +
	"CKB is built by one developer, and I want it to stay free for the people\n" +
	"who make software on their own or are just getting started. It's free for\n" +
	"personal use, open source projects, and organizations under $25k annual\n" +
	"revenue.\n" +
	"\n" +
	"If your organization is above that, please get a license. It's what keeps\n" +
	"CKB going, and I appreciate it: https://codeknowledge.dev/pricing\n" +
	"\n" +
	"I'd love to hear from you, good or bad:\n" +
	"  GitHub   https://github.com/SimplyLiz/ckb/discussions\n" +
	"\n" +
	"(Shown once per version. Silence it with CKB_NO_NOTICE=1)\n"

type noticeState struct {
	LastSeenVersion string `json:"last_seen_version"`
}

// noticeRunner holds what the decision depends on, so tests need no real HOME.
type noticeRunner struct {
	dir      string // ~/.ckb
	current  string
	disabled bool
}

// ShowPostUpdateNotice prints the post-update notice to w on the first run of a
// new version. canDisplay says whether a person is reading w.
//
// It must run before anything creates ~/.ckb/update-check.json, and on every
// run that is not a protocol server, displayable or not: the file marks an
// earlier install, so a fresh install that wrote it first would later be
// thanked for updating.
func ShowPostUpdateNotice(w io.Writer, canDisplay bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	n := noticeRunner{
		dir:      filepath.Join(home, ".ckb"),
		current:  version.Version,
		disabled: os.Getenv("CKB_NO_NOTICE") != "",
	}
	n.run(w, canDisplay)
}

func (n noticeRunner) run(w io.Writer, canDisplay bool) {
	last := n.lastSeen()
	if last == "" && !n.hasEarlierInstall() {
		n.remember()
		return
	}
	// Equal or a downgrade: going back to a version must not repeat its news.
	if last != "" && !isNewerVersion(n.current, last) {
		return
	}
	// An upgrade stays unrecorded here, so the first run a person actually
	// reads still gets it. The version check keeps a build stamped with another
	// version from presenting these news as its own.
	if n.disabled || !canDisplay || n.current != noticeVersion {
		return
	}
	// Remember first: where the state cannot be written, the notice would
	// otherwise repeat on every run.
	if !n.remember() {
		return
	}
	_, _ = io.WriteString(w, "\n"+noticeText+"\n")
}

// hasEarlierInstall recognises versions from before the notice existed, which
// left no last-seen version behind.
func (n noticeRunner) hasEarlierInstall() bool {
	for _, name := range []string{cacheFileName, "ckb.db"} {
		if _, err := os.Stat(filepath.Join(n.dir, name)); err == nil {
			return true
		}
	}
	return false
}

func (n noticeRunner) lastSeen() string {
	data, err := os.ReadFile(filepath.Join(n.dir, noticeStateFile))
	if err != nil {
		return ""
	}
	var state noticeState
	if err := json.Unmarshal(data, &state); err != nil {
		return ""
	}
	return state.LastSeenVersion
}

// remember records the current version and reports whether that worked.
func (n noticeRunner) remember() bool {
	data, err := json.Marshal(noticeState{LastSeenVersion: n.current})
	if err != nil {
		return false
	}
	if err := os.MkdirAll(n.dir, 0755); err != nil {
		return false
	}
	path := filepath.Join(n.dir, noticeStateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return false
	}
	return os.Rename(tmp, path) == nil
}
