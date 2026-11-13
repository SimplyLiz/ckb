package audit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, src := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A script nobody imports gets no "add tests"; the helper the scripts share,
// a module a script imports and a package member still do.
func TestStandaloneScripts(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"scripts/shebang.py": "#!/usr/bin/env python3\nprint('hi')\n",
		"scripts/guard.py":   "import sys\n\ndef main():\n    pass\n\nif __name__ == '__main__': main()\n",
		"scripts/toplevel.py": "\"\"\"Aufruf: toplevel.py <x>\"\"\"\nfrom blib import *\nimport sys\n" +
			"sys.path.insert(0, '.')\n",
		"scripts/loop.py": "import sys\nfor name in sys.argv[1:]:\n    print(name)\n",
		// Imported by toplevel.py: a shared helper, even with work at module level.
		"scripts/blib.py": "with open('/dev/null') as f:\n    KEY = f.read()\n",
		// Has a guard, but eval.py imports it.
		"scripts/recon.py": "def main():\n    pass\nif __name__ == '__main__':\n    main()\n",
		"scripts/eval.py":  "#!/usr/bin/env python3\nimport recon\nrecon.main()\n",
		// A module: definitions and assignments only.
		"scripts/lib.py": "import os\nX = os.getcwd()\ndef f():\n    return X\n",
		// Inside a package, module-level work alone doesn't make a script.
		"pkg/__init__.py":      "",
		"pkg/setup_logging.py": "import logging\nlogging.basicConfig()\n",
		// A TS bin script and the module it uses.
		"bin/cli.ts": "#!/usr/bin/env node\nimport { run } from '../src/run'\nrun()\n",
		"src/run.ts": "#!/usr/bin/env node\nexport function run() {}\n",
		"main.go":    "package main\n\nfunc main() {}\n",
	})
	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, rel)
		}
		return nil
	})

	got := standaloneScripts(dir, files, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var names []string
	for f := range got {
		names = append(names, f)
	}
	sort.Strings(names)
	want := "bin/cli.ts,scripts/eval.py,scripts/guard.py,scripts/loop.py,scripts/shebang.py,scripts/toplevel.py"
	if strings.Join(names, ",") != want {
		t.Errorf("standalone scripts = %v\nwant %s", names, want)
	}
}

func TestIsModuleLevelWork(t *testing.T) {
	for line, want := range map[string]bool{
		"main(sys.argv[1])":         true,
		"sys.path.insert(0, x)":     true,
		"for name in sys.argv[1:]:": true,
		"with open(p) as f:":        true,
		"x = f()":                   false,
		"x=f()":                     false,
		"def main():":               false,
		"if TYPE_CHECKING:":         false,
		"    main()":                false,
		"import os":                 false,
		"@decorator(1)":             false,
		"":                          false,
	} {
		if got := isModuleLevelWork(line); got != want {
			t.Errorf("isModuleLevelWork(%q) = %v, want %v", line, got, want)
		}
	}
}

// In a git repo the audit reads the files git lists: a gitignored virtualenv
// is somebody else's code.
func TestFindSourceFiles_GitIgnoredLeftOut(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".gitignore":                       ".venv/\n",
		"app.py":                           "print(1)\n",
		"new.py":                           "print(2)\n",
		".venv/lib/site-packages/pip/x.py": "print(3)\n",
		"node_modules/dep/index.js":        "1\n",
		"vendor/lib.go":                    "package lib\n",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "app.py", ".gitignore", "vendor/lib.go"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	files, err := NewAnalyzer(dir, slog.New(slog.NewTextHandler(io.Discard, nil))).findSourceFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if got := strings.Join(files, ","); got != "app.py,new.py" {
		t.Errorf("files = %s, want app.py,new.py (tracked and untracked, not ignored or vendored)", got)
	}
}

// The script's test_coverage factor says why it isn't scored.
func TestAnalyze_ScriptNotScoredForTests(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"run.py": "#!/usr/bin/env python3\nprint('x')\n",
		"lib.py": "def f():\n    return 1\n",
	})
	a := NewAnalyzer(dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	res, err := a.Analyze(context.Background(), AuditOptions{RepoRoot: dir, MinScore: 0.001})
	if err != nil {
		t.Fatal(err)
	}
	cov := map[string]RiskFactor{}
	for _, item := range res.Items {
		for _, f := range item.Factors {
			if f.Factor == FactorTestCoverage {
				cov[item.File] = f
			}
		}
	}
	if f := cov["run.py"]; f.Contribution != 0 || !strings.Contains(f.Value, "not scored") {
		t.Errorf("run.py test_coverage = %+v, want not scored", f)
	}
	if f := cov["lib.py"]; f.Contribution != 15 {
		t.Errorf("lib.py test_coverage = %+v, want 15", f)
	}
}
