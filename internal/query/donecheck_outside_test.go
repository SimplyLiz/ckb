package query

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A tsconfig that includes src alone leaves tests/ out of the index: a use there was
// never looked at, so the check can't be clear about it.
func TestCheckOutsideIndex(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"src/board.ts":        "export function post(x: number) {}\n",
		"src/use.ts":          "post(1);\n",
		"tests/board.test.ts": "post(1);\n",
		"tests/other.test.ts": "repost(1);\n",
		"docs/board.md":       "call post(x)\n",
		"tools/post.rs":       "fn post() {}\n",
		"vite.config.js":      "post(1);\n",
		"testdata/x/post.ts":  "post(1);\n",
		"example.go":          "//go:build ignore\n\npackage main\n\nfunc main() { post(1) }\n",
		"win.go":              "//go:build windows\n\npackage main\n\nfunc init() { post(1) }\n",
	} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, root, "init", "-q")
	runGit(t, root, "add", ".")

	c := newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{Files: []impact.ChangedFile{{OldPath: "src/board.ts", NewPath: "src/board.ts"}}})
	indexed := map[string]bool{"src/board.ts": true, "src/use.ts": true}
	c.checkOutsideIndex(map[string]string{"post": "ts"}, func(path string) bool { return indexed[path] })
	if len(c.outside) != 1 || c.outside[0] != "tests/board.test.ts" || c.failed != 0 {
		t.Errorf("outside = %v, failed %d; want only tests/board.test.ts", c.outside, c.failed)
	}
	c = newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	c.checkOutsideIndex(map[string]string{"post": "go"}, func(string) bool { return false })
	if len(c.outside) != 1 || c.outside[0] != "win.go" {
		t.Errorf("go: outside = %v, want only win.go", c.outside)
	}

	c = newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	c.checkOutsideIndex(map[string]string{"absent": "ts"}, func(string) bool { return false })
	if len(c.outside) != 0 || c.failed != 0 {
		t.Errorf("no match: outside = %v, failed %d", c.outside, c.failed)
	}
}
