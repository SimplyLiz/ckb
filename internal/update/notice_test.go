package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

func newNoticeRunner(t *testing.T) noticeRunner {
	t.Helper()
	return noticeRunner{dir: t.TempDir(), current: noticeLine + ".0"}
}

func markEarlierInstall(t *testing.T, n noticeRunner, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(n.dir, name), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
}

func runNotice(n noticeRunner, canDisplay bool) string {
	var out bytes.Buffer
	n.run(&out, canDisplay)
	return out.String()
}

func shown(out string) bool { return strings.Contains(out, "is here. Thanks for updating!") }

func TestNoticeLineMatchesRelease(t *testing.T) {
	if got := majorMinor(version.Version); got != noticeLine {
		t.Fatalf("notice.txt is written for %s but this is %s: rewrite the news and bump noticeLine",
			noticeLine, version.Version)
	}
	if !strings.HasPrefix(noticeText(version.Version), "CKB "+version.Version+" is here.") {
		t.Fatalf("headline names the wrong version: %q", strings.SplitN(noticeText(version.Version), "\n", 2)[0])
	}
}

func TestNoticeTextHasNoPlaceholderLeft(t *testing.T) {
	text := noticeText(version.Version)
	if strings.Contains(text, "{{") {
		t.Fatalf("placeholder left in notice: %q", text)
	}
	if !strings.Contains(text, "https://codeknowledge.dev/pricing?ref=notice-"+noticeLine+"\n") {
		t.Fatal("pricing link lost its ref")
	}
	if !strings.Contains(noticeBody, "?ref={{ref}}") {
		t.Fatal("notice.txt needs {{ref}}: the release workflow fills it in for the Homebrew caveats")
	}
}

func TestNoticeFreshInstallIsSilentAndRecorded(t *testing.T) {
	n := newNoticeRunner(t)
	if got := runNotice(n, true); got != "" {
		t.Fatalf("fresh install printed: %q", got)
	}
	if n.lastSeen() != n.current {
		t.Fatalf("lastSeen = %q", n.lastSeen())
	}
	markEarlierInstall(t, n, cacheFileName) // the first run's refresh writes this
	if got := runNotice(n, true); got != "" {
		t.Fatalf("second run of a fresh install printed: %q", got)
	}
}

func TestNoticeFreshInstallWithoutTerminalIsStillRecorded(t *testing.T) {
	n := newNoticeRunner(t)
	runNotice(n, false)
	if n.lastSeen() != n.current {
		t.Fatal("a fresh install first run from a script must not look like an upgrade later")
	}
}

func TestNoticeUpgradeShownOnce(t *testing.T) {
	for _, marker := range []string{cacheFileName, "ckb.db"} {
		t.Run(marker, func(t *testing.T) {
			n := newNoticeRunner(t)
			markEarlierInstall(t, n, marker)
			if !shown(runNotice(n, true)) {
				t.Fatal("upgrade did not print the notice")
			}
			if got := runNotice(n, true); got != "" {
				t.Fatalf("notice repeated: %q", got)
			}
		})
	}
}

func TestNoticeSkippedPatchStillGetsNews(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "9.3.1"
	n.remember()
	n.current = noticeLine + ".1"
	out := runNotice(n, true)
	if !shown(out) || !strings.HasPrefix(out, "\nCKB "+n.current+" is here.") {
		t.Fatalf("going straight to %s did not show the %s news: %q", n.current, noticeLine, out)
	}
}

func TestNoticeNotRepeatedOnPatchUpdate(t *testing.T) {
	n := newNoticeRunner(t)
	n.remember() // saw x.y.0
	n.current = noticeLine + ".1"
	if got := runNotice(n, true); got != "" {
		t.Fatalf("patch update repeated the news: %q", got)
	}
}

func TestNoticeSkipsDowngrade(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "99.0.0"
	n.remember()
	n.current = noticeLine + ".0"
	if got := runNotice(n, true); got != "" {
		t.Fatalf("downgrade printed: %q", got)
	}
	if n.lastSeen() != "99.0.0" {
		t.Fatalf("downgrade rewrote lastSeen to %q", n.lastSeen())
	}
}

func TestNoticeNotForOtherLines(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "99.1.0"
	markEarlierInstall(t, n, cacheFileName)
	if got := runNotice(n, true); got != "" {
		t.Fatalf("99.1.0 presented the news of %s: %q", noticeLine, got)
	}
}

func TestNoticeDisabled(t *testing.T) {
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	n.disabled = true
	if got := runNotice(n, true); got != "" {
		t.Fatalf("CKB_NO_NOTICE printed: %q", got)
	}
	if n.lastSeen() != "" {
		t.Fatal("a silenced upgrade must stay pending")
	}
}

func TestShowPostUpdateNoticeReadsEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CKB_NO_NOTICE", "1")
	n := noticeRunner{dir: filepath.Join(home, ".ckb")}
	if err := os.MkdirAll(n.dir, 0755); err != nil {
		t.Fatal(err)
	}
	markEarlierInstall(t, n, cacheFileName)

	var out bytes.Buffer
	ShowPostUpdateNotice(&out, true)
	if out.Len() != 0 {
		t.Fatalf("CKB_NO_NOTICE=1 printed: %q", out.String())
	}
	t.Setenv("CKB_NO_NOTICE", "")
	ShowPostUpdateNotice(&out, true)
	if !shown(out.String()) {
		t.Fatalf("without CKB_NO_NOTICE the upgrade did not print: %q", out.String())
	}
}

func TestNoticeWithoutTerminalStaysPending(t *testing.T) {
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	if got := runNotice(n, false); got != "" {
		t.Fatalf("no terminal printed: %q", got)
	}
	if !shown(runNotice(n, true)) {
		t.Fatal("first terminal run after a script did not print")
	}
}

func TestNoticeSilentWhenStateCannotBeWritten(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	if err := os.Chmod(n.dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(n.dir, 0755) })
	if got := runNotice(n, true); got != "" {
		t.Fatalf("would repeat on every run: %q", got)
	}
}

func TestNoticeShownOnceAcrossConcurrentRuns(t *testing.T) {
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	var mu sync.Mutex
	count := 0
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if shown(runNotice(n, true)) {
				mu.Lock()
				count++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if count != 1 {
		t.Fatalf("shown %d times by concurrent runs", count)
	}
	if _, err := os.Stat(filepath.Join(n.dir, noticeLockFile)); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
}

func TestNoticeTakesOverStaleLock(t *testing.T) {
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	lock := filepath.Join(n.dir, noticeLockFile)
	markEarlierInstall(t, n, noticeLockFile)
	if got := runNotice(n, true); got != "" {
		t.Fatalf("shown despite a fresh lock: %q", got)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if !shown(runNotice(n, true)) {
		t.Fatal("a crashed run's lock blocked the notice for good")
	}
}

func TestBrewUpgradeCommand(t *testing.T) {
	if !isBrewPath("/opt/homebrew/Cellar/ckb/9.4.0/bin/ckb") || !isBrewPath("/home/linuxbrew/.linuxbrew/Cellar/ckb/9.4.0/bin/ckb") {
		t.Fatal("Homebrew path not recognised")
	}
	if isBrewPath("/usr/local/bin/ckb") || isBrewPath("/opt/homebrew/Cellar/ckb-other/1/bin/ckb") {
		t.Fatal("non-Homebrew path taken for Homebrew")
	}
	c := &Checker{cache: &Cache{path: filepath.Join(t.TempDir(), "c.json")}, isBrewPath: true}
	if got := c.getUpgradeCommand(); got != "brew upgrade ckb" {
		t.Fatalf("upgrade command = %q", got)
	}
}

func TestNoticeTextSafeForHomebrewHeredoc(t *testing.T) {
	// scripts/brew-formula.sh puts notice.txt into an interpolating Ruby heredoc.
	if strings.Contains(noticeBody, "#{") || strings.Contains(noticeBody, `\`) {
		t.Fatal("notice.txt holds #{ or a backslash, which the Homebrew caveats heredoc would interpret")
	}
}
