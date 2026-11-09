package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

func newNoticeRunner(t *testing.T) noticeRunner {
	t.Helper()
	return noticeRunner{dir: t.TempDir(), current: noticeVersion}
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

func TestNoticeVersionMatchesRelease(t *testing.T) {
	if noticeVersion != version.Version {
		t.Fatalf("notice is written for %s but this is %s: rewrite the news in notice.go and bump noticeVersion",
			noticeVersion, version.Version)
	}
	if !strings.HasPrefix(noticeText, "CKB "+version.Version+" is here.") {
		t.Fatalf("notice headline names the wrong version: %q", strings.SplitN(noticeText, "\n", 2)[0])
	}
}

func TestNoticeFreshInstallIsSilentAndRecorded(t *testing.T) {
	n := newNoticeRunner(t)
	if got := runNotice(n, true); got != "" {
		t.Fatalf("fresh install printed: %q", got)
	}
	if n.lastSeen() != noticeVersion {
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
	if n.lastSeen() != noticeVersion {
		t.Fatal("a fresh install first run from a script must not look like an upgrade later")
	}
}

func TestNoticeUpgradeShownOnce(t *testing.T) {
	for _, marker := range []string{cacheFileName, "ckb.db"} {
		t.Run(marker, func(t *testing.T) {
			n := newNoticeRunner(t)
			markEarlierInstall(t, n, marker)
			if got := runNotice(n, true); !strings.Contains(got, noticeText) {
				t.Fatalf("upgrade did not print the notice: %q", got)
			}
			if got := runNotice(n, true); got != "" {
				t.Fatalf("notice repeated: %q", got)
			}
		})
	}
}

func TestNoticeWaitsForOlderLastSeenVersion(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "9.3.0"
	n.remember()
	n.current = noticeVersion
	if got := runNotice(n, true); !strings.Contains(got, noticeText) {
		t.Fatalf("upgrade from a recorded version did not print: %q", got)
	}
}

func TestNoticeSkipsDowngrade(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "99.0.0"
	n.remember()
	n.current = noticeVersion
	if got := runNotice(n, true); got != "" {
		t.Fatalf("downgrade printed: %q", got)
	}
	if n.lastSeen() != "99.0.0" {
		t.Fatalf("downgrade rewrote lastSeen to %q", n.lastSeen())
	}
}

func TestNoticeDisabledByEnvironment(t *testing.T) {
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
	n := noticeRunner{dir: filepath.Join(home, ".ckb"), current: version.Version}
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
	if !strings.Contains(out.String(), noticeText) {
		t.Fatalf("without CKB_NO_NOTICE the upgrade did not print: %q", out.String())
	}
}

func TestNoticeWithoutTerminalStaysPending(t *testing.T) {
	n := newNoticeRunner(t)
	markEarlierInstall(t, n, cacheFileName)
	if got := runNotice(n, false); got != "" {
		t.Fatalf("no terminal printed: %q", got)
	}
	if got := runNotice(n, true); !strings.Contains(got, noticeText) {
		t.Fatalf("first terminal run after a script did not print: %q", got)
	}
}

func TestNoticeNotForOtherVersions(t *testing.T) {
	n := newNoticeRunner(t)
	n.current = "9.5.0"
	markEarlierInstall(t, n, cacheFileName)
	if got := runNotice(n, true); got != "" {
		t.Fatalf("9.5.0 presented the news of %s: %q", noticeVersion, got)
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
