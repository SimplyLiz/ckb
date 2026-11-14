//go:build cgo

package symbols

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

const resumeSrc = "export function resumeTitle() {}\n"

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitInitCommit(t *testing.T, root string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// extractRels returns the sorted repo-relative paths of extracted symbols named resumeTitle.
func extractRels(t *testing.T, root string, opts DirOptions) []string {
	t.Helper()
	syms, err := NewExtractor().ExtractDirectoryWithOptions(context.Background(), root, opts)
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, s := range syms {
		if s.Name != "resumeTitle" {
			continue
		}
		rel, _ := filepath.Rel(root, s.Path)
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func assertRels(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestExtractDirectory_GitHonoursGitignore(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":         "build/\n",
		"src/resume.ts":      resumeSrc,
		"build/resume.ts":    resumeSrc,
		"src/gen/.gitignore": "*\n",
		"src/gen/resume.ts":  resumeSrc, // ignored by nested .gitignore
	})
	gitInitCommit(t, root)
	// untracked but not ignored must still be found
	writeTree(t, root, map[string]string{"lib/resume.ts": resumeSrc})

	assertRels(t, extractRels(t, root, DirOptions{}), "lib/resume.ts", "src/resume.ts")
}

func TestExtractDirectory_GitScopeSubdir(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":      "build/\n",
		"src/resume.ts":   resumeSrc,
		"other/resume.ts": resumeSrc,
		"src/build/x.ts":  resumeSrc,
	})
	gitInitCommit(t, root)

	got := extractRels(t, filepath.Join(root, "src"), DirOptions{RepoRoot: root})
	assertRels(t, got, "resume.ts") // paths are relative to the scope dir here
}

func TestExtractDirectory_WalkerFallbackFindsBoth(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":      "build/\n",
		"src/resume.ts":   resumeSrc,
		"build/resume.ts": resumeSrc,
	})
	// No git init: .gitignore is not consulted.
	assertRels(t, extractRels(t, root, DirOptions{}), "build/resume.ts", "src/resume.ts")
}

func TestExtractDirectory_ExcludeInWalker(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"src/resume.ts":   resumeSrc,
		"build/resume.ts": resumeSrc,
	})
	got := extractRels(t, root, DirOptions{Exclude: []string{"build/**"}})
	assertRels(t, got, "src/resume.ts")
}

func TestExtractDirectory_ExcludeInGitPath(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"src/resume.ts":     resumeSrc,
		"dist/resume.ts":    resumeSrc,
		"src/a.min.ts":      resumeSrc,
		"builder/resume.ts": resumeSrc,
	})
	gitInitCommit(t, root)
	got := extractRels(t, root, DirOptions{Exclude: []string{"dist/**", "src/*.min.ts"}})
	assertRels(t, got, "builder/resume.ts", "src/resume.ts")
}

func TestMatchExclude(t *testing.T) {
	cases := []struct {
		globs []string
		rel   string
		want  bool
	}{
		{[]string{"build/**"}, "build/a/b.js", true},
		{[]string{"build/**"}, "build", true},
		{[]string{"build/**"}, "builder/a.js", false},
		{[]string{"*.min.js"}, "a.min.js", true},
		{[]string{"*.min.js"}, "src/a.min.js", false},
		{[]string{"src/*.min.js"}, "src/a.min.js", true},
		{[]string{"["}, "a", false}, // invalid pattern never matches
		{nil, "a", false},
	}
	for _, c := range cases {
		if got := MatchExclude(c.globs, c.rel); got != c.want {
			t.Errorf("MatchExclude(%v, %q) = %v, want %v", c.globs, c.rel, got, c.want)
		}
	}
}
