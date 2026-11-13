package indexplan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/project"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitAddAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

func langsOf(targets []Target) []project.Language {
	var out []project.Language
	for _, t := range targets {
		out = append(out, t.Lang)
	}
	return out
}

func TestDetectTargets_ScriptsBesideAManifest(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"go.mod": "module x\n\ngo 1.21\n", "main.go": "package main\n"}
	for i := 0; i < minScriptFiles; i++ {
		files[fmt.Sprintf("scripts/s%d.py", i)] = "print(1)\n"
	}
	// Fixtures don't count.
	files["testdata/fixture.py"] = "x = 1\n"
	writeFiles(t, dir, files)
	gitAddAll(t, dir)

	got := DetectTargets(dir)
	if len(got) != 2 || got[0].Lang != project.LangGo || got[1].Lang != project.LangPython {
		t.Fatalf("targets = %v, want go then python", langsOf(got))
	}
	if got[1].Manifest == "" {
		t.Error("a manifest-less python target should say what it came from")
	}
}

func TestDetectTargets_AFewScriptsAreNoLanguage(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"go.mod": "module x\n", "main.go": "package main\n", "gen.py": "print(1)\n"})
	gitAddAll(t, dir)
	if got := DetectTargets(dir); len(got) != 1 {
		t.Fatalf("targets = %v, want go alone", langsOf(got))
	}
}

func TestDetectTargets_TypeScriptAndJavaScriptAreOne(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"package.json": `{"name":"x"}`,
		"index.js":     "module.exports = 1\n",
		"api/go.mod":   "module x\n",
	})
	got := DetectTargets(dir)
	n := 0
	for _, tg := range got {
		if tg.Lang == project.LangTypeScript || tg.Lang == project.LangJavaScript {
			n++
		}
	}
	if n != 1 || len(got) != 2 {
		t.Fatalf("targets = %v, want one TS/JS target and go", langsOf(got))
	}
}

func TestRunAll_SinglePlanIsTheOldBuild(t *testing.T) {
	dir := t.TempDir()
	plan := &Plan{Lang: project.LangGo, IndexerDir: dir, IndexPath: filepath.Join(dir, "index.scip")}
	res, err := RunAll(dir, []*Plan{plan}, func(p *Plan) error {
		return os.WriteFile(p.IndexPath, nil, 0o644)
	})
	if err != nil || len(res) != 1 || res[0].Err != nil {
		t.Fatalf("RunAll = %v, %v", res, err)
	}

	// An indexer that writes nothing is an error, not a fresh index.
	_ = os.Remove(plan.IndexPath)
	res, err = RunAll(dir, []*Plan{plan}, func(*Plan) error { return nil })
	if err == nil || res[0].Err == nil {
		t.Fatal("RunAll accepted an indexer that wrote nothing")
	}
}
