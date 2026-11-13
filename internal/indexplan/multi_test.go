package indexplan

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

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

func writeDocIndex(t *testing.T, path, doc string) {
	t.Helper()
	data, err := proto.Marshal(&scippb.Index{
		Metadata:  &scippb.Metadata{ProjectRoot: "file:///repo"},
		Documents: []*scippb.Document{{RelativePath: doc}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func indexDocs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var idx scippb.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	var docs []string
	for _, d := range idx.Documents {
		docs = append(docs, d.RelativePath)
	}
	sort.Strings(docs)
	return docs
}

// A session opening the index during a multi-language build reads the
// previous index, never a missing file or one language's part.
func TestRunAll_PreviousIndexStaysUntilTheMerge(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.scip")
	writeDocIndex(t, indexPath, "old.go")

	sub := filepath.Join(dir, "tool")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	plans := []*Plan{
		{Lang: project.LangGo, IndexerDir: dir, IndexPath: indexPath, base: "scip-go", Command: "scip-go"},
		// Nested manifest: the part path is relative to the indexer's directory
		// and the documents get rebased.
		{Lang: project.LangPython, IndexerDir: sub, IndexPath: indexPath, base: "scip-python index .", Command: "scip-python index ."},
		// Not every indexer calls it --output.
		{Lang: project.LangRuby, IndexerDir: dir, IndexPath: indexPath, base: "scip-ruby .", Command: "scip-ruby ."},
		// No output flag at all: runs in place.
		{Lang: project.LangPHP, IndexerDir: dir, IndexPath: indexPath, base: "vendor/bin/scip-php", Command: "vendor/bin/scip-php"},
	}
	docFor := map[project.Language]string{project.LangGo: "main.go", project.LangPython: "cli.py", project.LangRuby: "app.rb", project.LangPHP: "app.php"}

	res, err := RunAll(dir, plans, func(p *Plan) error {
		out := indexPath
		flag := project.OutputFlag(p.Lang)
		if f := strings.Fields(p.Command); flag != "" && len(f) > 2 && f[len(f)-2] == flag {
			out = filepath.Join(p.IndexerDir, f[len(f)-1])
			if docs := indexDocs(t, indexPath); len(docs) != 1 || docs[0] != "old.go" {
				t.Errorf("%s running: index holds %v, want the previous one", p.Lang, docs)
			}
		} else if p.Lang != project.LangPHP {
			t.Errorf("%s ran without %s: %q", p.Lang, flag, p.Command)
		}
		writeDocIndex(t, out, docFor[p.Lang])
		return nil
	})
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	for _, r := range res {
		if r.Err != nil {
			t.Errorf("%s: %v", r.Plan.Lang, r.Err)
		}
		if strings.Contains(r.Plan.Command, ".part") {
			t.Errorf("%s: result carries the part command %q", r.Plan.Lang, r.Plan.Command)
		}
	}
	want := []string{"app.php", "app.rb", "main.go", "tool/cli.py"}
	if got := indexDocs(t, indexPath); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("merged docs = %v, want %v", got, want)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "index.scip.*"))
	if len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}

// When every language fails, the previous index is untouched.
func TestRunAll_AllFailedKeepsThePreviousIndex(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.scip")
	writeDocIndex(t, indexPath, "old.go")
	plans := []*Plan{
		{Lang: project.LangGo, IndexerDir: dir, IndexPath: indexPath, base: "scip-go", Command: "scip-go"},
		{Lang: project.LangRuby, IndexerDir: dir, IndexPath: indexPath, base: "scip-ruby .", Command: "scip-ruby ."},
	}
	_, err := RunAll(dir, plans, func(*Plan) error { return fmt.Errorf("boom") })
	if err == nil {
		t.Fatal("RunAll succeeded with every indexer failing")
	}
	if docs := indexDocs(t, indexPath); len(docs) != 1 || docs[0] != "old.go" {
		t.Errorf("index holds %v, want the previous one", docs)
	}
}

// scip-typescript --infer-tsconfig writes a tsconfig.json where none was and
// leaves it; the run removes it again, and never one that was there before.
func TestRunOne_RemovesTheInferredTSConfig(t *testing.T) {
	for _, had := range []bool{false, true} {
		dir := t.TempDir()
		if had {
			writeFiles(t, dir, map[string]string{"tsconfig.json": `{"compilerOptions":{}}`})
		}
		plan := &Plan{Lang: project.LangTypeScript, Command: "scip-typescript index --infer-tsconfig",
			IndexerDir: dir, IndexPath: filepath.Join(dir, "index.scip")}
		runOne(dir, plan, func(p *Plan) error {
			writeFiles(t, dir, map[string]string{"tsconfig.json": "{}", "index.scip": ""})
			return nil
		})
		_, err := os.Stat(filepath.Join(dir, "tsconfig.json"))
		if had && err != nil {
			t.Errorf("the repo's own tsconfig.json was removed")
		}
		if !had && err == nil {
			t.Errorf("the inferred tsconfig.json was left behind")
		}
	}
}

// scip-php can't be told where to write: with a custom index path its
// index.scip in the repo root moves there, and a run that wrote nothing
// leaves the previous index as it was.
func TestFinalize_FixedOutputMovesToCustomPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, ".ckb", "custom.scip")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := &Plan{Lang: project.LangPHP, IndexerDir: dir, IndexPath: custom, base: "vendor/bin/scip-php", Command: "vendor/bin/scip-php"}

	writeDocIndex(t, filepath.Join(dir, "index.scip"), "app.php")
	if err := Finalize(dir, plan); err != nil {
		t.Fatal(err)
	}
	if got := indexDocs(t, custom); strings.Join(got, ",") != "app.php" {
		t.Errorf("custom index holds %v, want [app.php]", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.scip")); !os.IsNotExist(err) {
		t.Errorf("index.scip left in the repo root: %v", err)
	}

	if err := Finalize(dir, plan); err != nil {
		t.Fatal(err)
	}
	if got := indexDocs(t, custom); strings.Join(got, ",") != "app.php" {
		t.Errorf("after a run that wrote nothing: %v", got)
	}
}
