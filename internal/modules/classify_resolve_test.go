package modules

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
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
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

// A script that puts a sibling directory on sys.path imports its modules by bare
// name; without sys.path the same name is not taken for the repo's module.
func TestResolvePythonImport_SysPath(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"exp/living-context/lc1_account.py": "def price(): pass\n",
		"exp/roi/roi2.py":                   "import sys\nsys.path.insert(0, os.path.join(HERE, \"..\", \"living-context\"))\nimport lc1_account as lc\n",
		"exp/roi/plain.py":                  "import lc1_account\n",
		"pkg/util.py":                       "x = 1\n",
		"tools/run.py":                      "import pkg.util\n",
	})
	c := NewImportClassifier(&ModuleContext{RepoRoot: root, Language: LanguagePython})
	if got := c.ResolveImport("lc1_account", "exp/roi/roi2.py"); got != "exp/living-context/lc1_account.py" {
		t.Errorf("through sys.path: %q", got)
	}
	if got := c.ResolveImport("lc1_account", "exp/roi/plain.py"); got != "" {
		t.Errorf("without sys.path: %q, want unresolved", got)
	}
	if got := c.ResolveImport("pkg.util", "tools/run.py"); got != "pkg/util.py" {
		t.Errorf("top-level package: %q", got)
	}
	if got := c.ResolveImport("json", "exp/roi/roi2.py"); got != "" {
		t.Errorf("stdlib name: %q", got)
	}
}

func TestResolveGoImport_ModulePath(t *testing.T) {
	root := writeRepo(t, map[string]string{"go.mod": "module github.com/x/repo\n\ngo 1.22\n", "internal/q/q.go": "package q\n"})
	c := NewImportClassifier(&ModuleContext{RepoRoot: root, Language: LanguageGo})
	if got := c.ResolveImport("github.com/x/repo/internal/q", "main.go"); got != "internal/q" {
		t.Errorf("own package: %q", got)
	}
	if got := c.ResolveImport("github.com/other/lib", "main.go"); got != "" {
		t.Errorf("other module: %q", got)
	}
}

func TestGitIgnoredDirs(t *testing.T) {
	root := writeRepo(t, map[string]string{".gitignore": "Temp/\n", "a.py": "", "Temp/brainstorm/m.py": ""})
	dirs := GitIgnoredDirs(root)
	if !dirs["Temp"] || len(dirs) != 1 {
		t.Errorf("GitIgnoredDirs = %v, want Temp", dirs)
	}
}
