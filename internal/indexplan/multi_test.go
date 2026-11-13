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
		// No known output flag: runs in place.
		{Lang: project.LangRuby, IndexerDir: dir, IndexPath: indexPath, base: "scip-ruby .", Command: "scip-ruby ."},
	}
	docFor := map[project.Language]string{project.LangGo: "main.go", project.LangPython: "cli.py", project.LangRuby: "app.rb"}

	res, err := RunAll(dir, plans, func(p *Plan) error {
		out := indexPath
		if f := strings.Fields(p.Command); len(f) > 2 && f[len(f)-2] == "--output" {
			out = filepath.Join(p.IndexerDir, f[len(f)-1])
			if docs := indexDocs(t, indexPath); len(docs) != 1 || docs[0] != "old.go" {
				t.Errorf("%s running: index holds %v, want the previous one", p.Lang, docs)
			}
		} else if p.Lang != project.LangRuby {
			t.Errorf("%s ran without --output: %q", p.Lang, p.Command)
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
	want := []string{"app.rb", "main.go", "tool/cli.py"}
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
