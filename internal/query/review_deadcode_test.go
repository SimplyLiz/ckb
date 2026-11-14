package query

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Cartographer's unreferenced exports are checked against a word search: a function
// called in its own file, or named in another file of its language, is not reported.
func TestPlausiblyUnreferenced(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	files := map[string]string{
		"lib/acct.py":  "def diagnose(w):\n    return w\n\n\ndef run(w):\n    return diagnose(w)\n\n\ndef lonely():\n    return 1\n\n\ndef shared():\n    return 2\n",
		"app/use.py":   "import acct\nacct.shared()\n",
		"docs/note.md": "lonely is mentioned in prose only\n",
		"web/a.ts":     "export const onlyHere = 1\n",
		"web/b.tsx":    "export const usedInTsx = 1\n",
		"web/c.js":     "console.log(usedInTsx)\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	cases := []struct {
		file, name string
		want       bool
	}{
		{"lib/acct.py", "diagnose", false}, // called in its own file
		{"lib/acct.py", "shared", false},   // used by another Python file
		{"lib/acct.py", "lonely", true},    // a Markdown mention isn't a use
		{"lib/acct.py", "__init__", false}, // scip-python's module symbol
		{"web/a.ts", "onlyHere", true},
		{"web/b.tsx", "usedInTsx", false}, // the JS/TS family is one language
	}
	for _, c := range cases {
		if got := plausiblyUnreferenced(root, c.file, c.name); got != c.want {
			t.Errorf("plausiblyUnreferenced(%s, %s) = %v, want %v", c.file, c.name, got, c.want)
		}
	}
}

// Cartographer's unreferenced exports carry the declaration line, so HoldTheLine
// can drop the ones on unchanged lines and the SCIP finding for the same
// declaration is not repeated.
func TestDeclarationLine(t *testing.T) {
	root := t.TempDir()
	body := "package x\n\n// An old helper.\nfunc lonelyHelper() {}\n\nfunc lonely() {}\n"
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{"lonelyHelper": 4, "lonely": 6, "missing": 0} {
		if got := declarationLine(root, "x.go", name); got != want {
			t.Errorf("declarationLine(%s) = %d, want %d", name, got, want)
		}
	}
	if got := declarationLine(root, "nope.go", "lonely"); got != 0 {
		t.Errorf("missing file: %d", got)
	}
}
