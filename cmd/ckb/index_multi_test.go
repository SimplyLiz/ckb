package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/SimplyLiz/CodeMCP/internal/index"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// multiLangRepo is a repo with a go.mod and a requirements.txt, and fake
// scip-go/scip-python indexers that copy a fixture index each. PATH holds only
// the fakes and the system tools, so a real indexer never runs. pythonScript
// overrides the fake scip-python's body; "" leaves scip-python uninstalled.
func multiLangRepo(t *testing.T, pythonScript string) (dir, ckbDir string) {
	t.Helper()
	dir = t.TempDir()
	ckbDir = filepath.Join(dir, ".ckb")
	for name, body := range map[string]string{
		"go.mod":           "module example.com/x\n\ngo 1.21\n",
		"main.go":          "package main\n\nfunc Run() {}\n",
		"requirements.txt": "requests\n",
		"tool/main.py":     "def main():\n    pass\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(ckbDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fixtures := t.TempDir()
	write := func(name string, idx *scippb.Index) string {
		data, err := proto.Marshal(idx)
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(fixtures, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	goIdx := write("go.scip", &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file://" + dir, ToolInfo: &scippb.ToolInfo{Name: "scip-go"}},
		Documents: []*scippb.Document{{
			RelativePath: "main.go",
			Symbols:      []*scippb.SymbolInformation{{Symbol: "scip-go gomod example.com/x v1 `example.com/x`/Run()."}},
		}},
	})
	pyIdx := write("py.scip", &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file://" + dir, ToolInfo: &scippb.ToolInfo{Name: "scip-python"}},
		Documents: []*scippb.Document{{
			RelativePath: "tool/main.py",
			Symbols:      []*scippb.SymbolInformation{{Symbol: "scip-python python x 0.1 `tool.main`/main()."}},
		}},
	})

	bin := t.TempDir()
	fake := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test fixture, needs +x
			t.Fatal(err)
		}
	}
	// Like the real indexers: index.scip in the working directory unless
	// --output says otherwise, which a multi-language build always does.
	const output = `out=index.scip; while [ $# -gt 0 ]; do [ "$1" = --output ] && { out=$2; shift; }; shift; done; `
	fake("scip-go", output+"cp '"+goIdx+"' \"$out\"")
	switch pythonScript {
	case "":
	case "ok":
		fake("scip-python", output+"cp '"+pyIdx+"' \"$out\"")
	default:
		fake("scip-python", pythonScript)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	return dir, ckbDir
}

func readIndexDocs(t *testing.T, path string) []string {
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
	return docs
}

func TestTriggerReindex_MultiLanguage_MergesBoth(t *testing.T) {
	dir, ckbDir := multiLangRepo(t, "ok")

	note, err := triggerReindex(context.Background(), dir, ckbDir, index.TriggerStale, "", newLogger("human"))
	if err != nil {
		t.Fatalf("triggerReindex() = %v", err)
	}
	if note != "" {
		t.Errorf("note = %q, want none with both indexers installed", note)
	}
	if docs := readIndexDocs(t, filepath.Join(dir, "index.scip")); strings.Join(docs, ",") != "main.go,tool/main.py" {
		t.Errorf("documents = %v, want both languages", docs)
	}
	for _, leftover := range []string{"index.scip.go.part", "index.scip.python.part", "index.scip.prev"} {
		if _, err := os.Stat(filepath.Join(dir, leftover)); err == nil {
			t.Errorf("%s left behind", leftover)
		}
	}

	meta, _ := index.LoadMeta(ckbDir)
	if meta == nil || !meta.MultiLanguage() || len(meta.Skipped) != 0 {
		t.Fatalf("meta = %+v, want two languages and none skipped", meta)
	}
	if meta.Languages[0].Language != "go" || meta.Languages[1].Language != "python" {
		t.Errorf("languages = %+v", meta.Languages)
	}
	cfg, err := project.LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Language != project.LangGo || len(cfg.Languages) != 2 || cfg.LanguageFromFlag {
		t.Errorf("project config = %+v", cfg)
	}

	// A second build replaces the merged index instead of merging into it.
	if _, err := triggerReindex(context.Background(), dir, ckbDir, index.TriggerStale, "", newLogger("human")); err != nil {
		t.Fatal(err)
	}
	if docs := readIndexDocs(t, filepath.Join(dir, "index.scip")); len(docs) != 2 {
		t.Errorf("documents after rebuild = %v", docs)
	}

	// The incremental update reruns one indexer over the whole file: refused.
	var out bytes.Buffer
	if tryIncrementalIndexTo(dir, ckbDir, project.LangGo, &out) {
		t.Error("incremental update ran on a multi-language index")
	}
	if !strings.Contains(out.String(), "several languages") {
		t.Errorf("incremental refusal = %q", out.String())
	}
}

func TestTriggerReindex_MissingIndexer_IndexesTheRest(t *testing.T) {
	dir, ckbDir := multiLangRepo(t, "")

	// The diagnosis before the build: ready, naming what stays out.
	d := diagnoseIndex(dir)
	if !d.Ready || !strings.Contains(d.Note, "scip-python not installed") {
		t.Errorf("diagnosis = %+v, want ready with a note on scip-python", d)
	}

	note, err := triggerReindex(context.Background(), dir, ckbDir, index.TriggerStale, "", newLogger("human"))
	if err != nil {
		t.Fatalf("triggerReindex() = %v, want the Go half built", err)
	}
	if !strings.Contains(note, "Python") || !strings.Contains(note, "scip-python") {
		t.Errorf("note = %q, want it to name Python and its indexer", note)
	}
	if docs := readIndexDocs(t, filepath.Join(dir, "index.scip")); strings.Join(docs, ",") != "main.go" {
		t.Errorf("documents = %v, want the Go one", docs)
	}
	meta, _ := index.LoadMeta(ckbDir)
	if meta == nil || meta.MultiLanguage() || len(meta.Skipped) != 1 {
		t.Fatalf("meta = %+v, want go indexed and python skipped", meta)
	}
	if sk := meta.Skipped[0]; sk.Language != "python" || sk.Fix == "" || sk.Indexer != "scip-python" {
		t.Errorf("skipped = %+v, want python with its install command", sk)
	}
}

func TestTriggerReindex_OneIndexerFails_KeepsTheOther(t *testing.T) {
	dir, ckbDir := multiLangRepo(t, "echo boom >&2; exit 3")

	note, err := triggerReindex(context.Background(), dir, ckbDir, index.TriggerStale, "", newLogger("human"))
	if err != nil {
		t.Fatalf("triggerReindex() = %v", err)
	}
	if !strings.Contains(note, "scip-python failed") {
		t.Errorf("note = %q", note)
	}
	if docs := readIndexDocs(t, filepath.Join(dir, "index.scip")); strings.Join(docs, ",") != "main.go" {
		t.Errorf("documents = %v", docs)
	}
}

func TestTriggerReindex_AllFail_RestoresPreviousIndex(t *testing.T) {
	dir, ckbDir := multiLangRepo(t, "exit 3")
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	// Both fail: scip-go too.
	bin := strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]
	if err := os.WriteFile(filepath.Join(bin, "scip-go"), []byte("#!/bin/sh\nexit 2\n"), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}

	if _, err := triggerReindex(context.Background(), dir, ckbDir, index.TriggerStale, "", newLogger("human")); err == nil {
		t.Fatal("triggerReindex() = nil with every indexer failing")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.scip")); err != nil {
		t.Errorf("previous index not restored: %v", err)
	}
	if meta, _ := index.LoadMeta(ckbDir); meta != nil {
		t.Errorf("metadata written for a failed build: %+v", meta)
	}
}

func TestIndexedTargets_FlagChoiceSticks(t *testing.T) {
	dir, _ := multiLangRepo(t, "ok")
	if err := project.SaveConfig(dir, &project.ProjectConfig{
		Language: project.LangPython, Languages: []project.Language{project.LangPython}, LanguageFromFlag: true,
	}); err != nil {
		t.Fatal(err)
	}
	targets, ok := indexedTargets(dir)
	if !ok || len(targets) != 1 || targets[0].Lang != project.LangPython {
		t.Errorf("targets = %v, want python alone (chosen with --lang)", targets)
	}

	// Detected rather than chosen: detect again, so a new language is picked up.
	if err := project.SaveConfig(dir, &project.ProjectConfig{
		Language: project.LangGo, Languages: []project.Language{project.LangGo},
	}); err != nil {
		t.Fatal(err)
	}
	if targets, _ := indexedTargets(dir); len(targets) != 2 {
		t.Errorf("targets = %v, want go and python", targets)
	}
}

// The file count is the tracked files, the ones the index holds, not every file
// on disk: an ignored scratch directory doesn't count.
func TestCountSourceFiles_TrackedOnly(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{".gitignore": "Temp/\n", "a.py": "", "b/c.py": "", "Temp/x.py": "", "Temp/y.py": ""} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := countSourceFiles(dir, project.LangPython); n != 4 {
		t.Fatalf("outside git: %d, want 4 files on disk", n)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if n := countSourceFiles(dir, project.LangPython); n != 2 {
		t.Fatalf("in git: %d, want the 2 tracked files", n)
	}
}
