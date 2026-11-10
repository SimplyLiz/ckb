package update

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

const (
	// noticeLine is the minor release the news in notice.txt were written for.
	// They are shown on every version of that line to anyone who has not seen
	// them, so a user going straight from 9.3 to 9.4.1 still gets them. A test
	// holds it equal to version.Version's major.minor: a minor bump without new
	// news fails the build instead of shipping last release's news.
	noticeLine = "9.4"

	// noticeStateFile is kept apart from update-check.json because Cache.Set
	// rewrites that file whole, from a background goroutine.
	noticeStateFile = "notice-state.json"

	// noticeLockFile keeps two ckb processes started together from both
	// showing the notice. A lock older than noticeLockStale is left over from a
	// crash and taken over.
	noticeLockFile  = "notice.lock"
	noticeLockStale = 10 * time.Second
)

// noticeBody is shared with the Homebrew formula, which the release workflow
// builds from the same file as its caveats. {{ref}} tells the pricing page's
// analytics where a visit came from.
//
//go:embed notice.txt
var noticeBody string

func noticeText(current string) string {
	body := strings.ReplaceAll(noticeBody, "{{ref}}", "notice-"+noticeLine)
	return "CKB " + current + " is here. Thanks for updating!\n\n" +
		body + "\n" +
		"(Shown once per version. Silence it with CKB_NO_NOTICE=1)\n"
}

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
// new release line. canDisplay says whether a person is reading w.
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
	// A build of another line must not present these news as its own.
	if majorMinor(n.current) != noticeLine || !n.due(last) {
		return
	}
	// An upgrade stays unrecorded here, so the first run a person actually
	// reads still gets it.
	if n.disabled || !canDisplay {
		return
	}
	release, ok := n.lock()
	if !ok {
		return
	}
	defer release()
	// Another process may have shown it between the first read and the lock.
	if !n.due(n.lastSeen()) {
		return
	}
	// Remember first: where the state cannot be written, the notice would
	// otherwise repeat on every run.
	if !n.remember() {
		return
	}
	_, _ = io.WriteString(w, "\n"+noticeText(n.current)+"\n")
}

// due reports whether the last seen version predates this line. Equal, later
// and downgrades are not due: going back must not repeat news.
func (n noticeRunner) due(last string) bool {
	return last == "" || isNewerVersion(noticeLine+".0", last)
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
	if os.MkdirAll(n.dir, 0755) != nil {
		return false
	}
	path := filepath.Join(n.dir, noticeStateFile)
	tmp, err := os.CreateTemp(n.dir, noticeStateFile+".*.tmp")
	if err != nil {
		return false
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return false
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return false
	}
	return true
}

// lock takes the notice lock and returns its release.
func (n noticeRunner) lock() (func(), bool) {
	path := filepath.Join(n.dir, noticeLockFile)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, true
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false
		}
		info, statErr := os.Stat(path)
		if statErr != nil || time.Since(info.ModTime()) < noticeLockStale {
			return nil, false
		}
		_ = os.Remove(path)
	}
	return nil, false
}

// majorMinor returns "X.Y" of a version string.
func majorMinor(v string) string {
	p := parseVersion(v)
	return strconv.Itoa(p[0]) + "." + strconv.Itoa(p[1])
}
