package query

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The index is Go; TypeScript files under web/ have no document in it, so an empty
// lookup there is said to be "not indexed", and one under lib/ is not.
func TestUnindexedNote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	e := changeSetEngine(t)
	for rel, body := range map[string]string{
		"lib/lib.go":         "package lib\n",
		"web/hook.ts":        "export const register = 1\n",
		"web/types/index.ts": "export type T = number\n",
		"README.md":          "x\n",
	} {
		p := filepath.Join(e.repoRoot, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "lib", "web", "README.md"}} {
		if out, err := exec.Command("git", append([]string{"-C", e.repoRoot}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	if got := e.UnindexedLanguages("web/"); strings.Join(got, ",") != "typescript (2 files)" {
		t.Errorf("UnindexedLanguages(web/) = %v", got)
	}
	if note := e.UnindexedNote("web"); !strings.Contains(note, "web has files the index doesn't cover: typescript (2 files)") {
		t.Errorf("note = %q", note)
	}
	if note := e.UnindexedNote("lib/"); note != "" {
		t.Errorf("lib is indexed, note = %q", note)
	}
}
