package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The report: lisapc-mcp/ is a separate checkout inside the repo, ignored by
// its .gitignore, and its pyproject.toml made CKB call a TypeScript repo Python.
func TestDetect_IgnoredNestedRepoDoesNotDecide(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeFiles(t, root, map[string]string{
		".gitignore":                "lisapc-mcp/\n",
		"src/app.ts":                "export const a = 1\n",
		"lisapc-mcp/pyproject.toml": "[project]\nname = \"x\"\n",
	})
	gitRun(t, root, "-C", filepath.Join(root, "lisapc-mcp"), "init", "-q")
	gitRun(t, root, "add", ".gitignore", "src/app.ts")
	gitRun(t, root, "commit", "-qm", "init")

	lang, from, all := DetectAllLanguages(root)
	if lang != LangTypeScript || len(all) != 1 {
		t.Fatalf("DetectAllLanguages = %s from %q, all %v; want typescript alone", lang, from, all)
	}
	if got := FindManifestForLanguage(root, LangPython); got != "" {
		t.Errorf("FindManifestForLanguage(python) = %q, want none", got)
	}
}

func TestDetect_SkipsNestedReposAndIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	writeFiles(t, root, map[string]string{
		".gitignore":               "scratch/\n",
		"go.mod":                   "module x\n",
		"scratch/Cargo.toml":       "[package]\n",   // ignored, no .git
		"scratch/app/App.csproj":   "<Project/>\n",  // glob in an ignored dir
		"other/pubspec.yaml":       "name: other\n", // nested repo, not ignored
		"services/api/pom.xml":     "<project/>\n",  // this project's own
		"services/api/src/Main.kt": "fun main() {}\n",
	})
	gitRun(t, root, "-C", filepath.Join(root, "other"), "init", "-q")

	_, _, all := DetectAllLanguages(root)
	if want := []Language{LangGo, LangJava}; !reflect.DeepEqual(all, want) {
		t.Fatalf("languages = %v, want %v", all, want)
	}
}

// A submodule or worktree has a .git file, not a directory.
func TestDirFilter_GitFileIsNestedRepo(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"sub/.git":         "gitdir: ../.git/modules/sub\n",
		"sub/Cargo.toml":   "[package]\n",
		"plain/Cargo.toml": "[package]\n",
	})
	f := newDirFilter(root)
	if !f.skip(filepath.Join(root, "sub"), "sub") {
		t.Error("directory with a .git file not skipped")
	}
	if f.skip(filepath.Join(root, "plain"), "plain") {
		t.Error("plain directory skipped outside a git repo")
	}
	if f.skip(root, ".") {
		t.Error("root skipped")
	}
	if got := findManifest(root, "Cargo.toml"); got != filepath.Join("plain", "Cargo.toml") {
		t.Errorf("findManifest = %q, want plain/Cargo.toml", got)
	}
}
