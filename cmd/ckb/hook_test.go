package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/repostate"
)

func TestSessionStartContext_SilentWithoutRepoOrCkb(t *testing.T) {
	if got := sessionStartContext(t.TempDir()); got != "" {
		t.Errorf("outside a git repo: got %q, want nothing", got)
	}
	if got := sessionStartContext(gitInitTemp(t)); got != "" {
		t.Errorf("git repo without .ckb: got %q, want nothing", got)
	}
	if got := sessionStartContext(""); got != "" {
		t.Errorf("empty dir: got %q, want nothing", got)
	}
}

// The states the hook reports, in the order a repo goes through them.
func TestSessionStartContext_IndexStates(t *testing.T) {
	repo := gitInitTemp(t)
	ckbDir := filepath.Join(repo, ".ckb")
	if err := os.MkdirAll(ckbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte(".ckb/\n/index.scip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := func(file, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", file}} {
			if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	commit("README.md", "hi\n")

	// No index, no language CKB can index: the diagnosis says so.
	got := sessionStartContext(repo)
	if !strings.HasPrefix(got, "CKB: No SCIP index") || !strings.Contains(got, "no language") {
		t.Errorf("no index, nothing indexable: got %q", got)
	}

	// A build holds the repo's index lock.
	lock, err := index.AcquireLock(ckbDir)
	if err != nil {
		t.Fatal(err)
	}
	got = sessionStartContext(repo)
	lock.Release()
	if !strings.Contains(got, "being built right now") {
		t.Errorf("build running: got %q", got)
	}

	// An index without its build record.
	if err = os.WriteFile(filepath.Join(repo, "index.scip"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got = sessionStartContext(repo); !strings.Contains(got, "no build record") {
		t.Errorf("index without metadata: got %q", got)
	}

	rs, err := repostate.ComputeRepoState(repo)
	if err != nil {
		t.Fatal(err)
	}
	meta := &index.IndexMeta{CommitHash: rs.HeadCommit, RepoStateID: rs.RepoStateID}
	if err = meta.Save(ckbDir); err != nil {
		t.Fatal(err)
	}
	if got = sessionStartContext(repo); got != "CKB: the SCIP index is current (commit "+rs.HeadCommit[:7]+")." {
		t.Errorf("fresh index: got %q", got)
	}

	if err = os.WriteFile(filepath.Join(repo, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got = sessionStartContext(repo); !strings.Contains(got, "uncommitted changes are not in it") {
		t.Errorf("uncommitted edits: got %q", got)
	}

	commit("README.md", "edited\n")
	got = sessionStartContext(repo)
	if !strings.Contains(got, "1 commit(s) behind HEAD") || !strings.Contains(got, "refreshes it in the background") {
		t.Errorf("one commit behind: got %q", got)
	}

	// A language left out of the build gets its own line.
	meta.Skipped = []index.SkippedLanguage{{Language: "python", Reason: "scip-python is not installed", Fix: "npm i -g @sourcegraph/scip-python"}}
	if err = meta.Save(ckbDir); err != nil {
		t.Fatal(err)
	}
	got = sessionStartContext(repo)
	if lines := strings.Split(got, "\n"); len(lines) != 2 || !strings.Contains(lines[1], "scip-python is not installed") {
		t.Errorf("partial index: got %q, want a second line naming Python", got)
	}

	// From a subdirectory the repo is the same.
	sub := filepath.Join(repo, "docs")
	if err = os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if sessionStartContext(sub) != got {
		t.Error("a subdirectory reports differently from the repo root")
	}
}

func TestReadHookCwd(t *testing.T) {
	in := `{"session_id":"s","cwd":"/some/repo","hook_event_name":"SessionStart","source":"startup"}`
	if got := readHookCwd(strings.NewReader(in), time.Second); got != "/some/repo" {
		t.Errorf("got %q, want /some/repo", got)
	}
	if got := readHookCwd(strings.NewReader("not json"), time.Second); got != "" {
		t.Errorf("bad input: got %q, want empty", got)
	}
	// stdin left open: give up after the wait instead of hanging the session.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer r.Close()
	start := time.Now()
	if got := readHookCwd(r, 50*time.Millisecond); got != "" || time.Since(start) > time.Second {
		t.Errorf("open stdin: got %q after %v", got, time.Since(start))
	}
}

func TestWriteSessionStartOutput(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSessionStartOutput(&buf, "line one\nline two"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if out.HookSpecificOutput.HookEventName != "SessionStart" || out.HookSpecificOutput.AdditionalContext != "line one\nline two" {
		t.Errorf("got %+v", out)
	}
}
