package query

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
	"github.com/SimplyLiz/CodeMCP/internal/index"
)

// doneCheckFiles are the fixture's files as changeSetEngine's index describes them.
var doneCheckFiles = map[string]string{
	"lib/lib.go":      "package lib\n\nfunc Target(a int) int {\n\treturn a\n}\n\n\n\nfunc Other() {\n}\n",
	"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\n// Exercises Target.\nfunc TestTarget(t *testing.T) {\n\tTarget(1)\n}\n",
	"app/app.go":      "package app\nimport \"example.com/m/lib\"\n\n// Run does it.\nfunc Run() {\n\tlib.Target(1)\n}\n",
	"app/app_test.go": "package app\n\nimport \"testing\"\n\nfunc\nfunc TestRun(t *testing.T) {\n\tRun()\n}\n",
	"cmd/main.go":     "package cmd\n\nimport \"example.com/m/app\"\n\nfunc\nfunc Main() {\n\tapp.Run()\n}\n",
}

// doneCheckRepo puts the fixture's files on disk, commits them, and marks the index as
// built from that commit.
func doneCheckRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	engine := changeSetEngine(t)
	root := engine.repoRoot
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".ckb/\nindex.scip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, content := range doneCheckFiles {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	return engine, root
}

func writeRepoFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func editRepoFile(t *testing.T, root, path, from, to string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), from) {
		t.Fatalf("%s doesn't contain %q", path, from)
	}
	writeRepoFile(t, root, path, strings.Replace(string(content), from, to, 1))
}

func headCommit(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func changeTargetSignature(t *testing.T, root string) {
	t.Helper()
	editRepoFile(t, root, "lib/lib.go", "Target(a int)", "Target(a int, b string)")
}

// The bug: a file the diff only touched in a comment counted as handled.
func TestDoneCheck_CommentEditLeavesTheCallOpen(t *testing.T) {
	engine, root := doneCheckRepo(t)
	changeTargetSignature(t, root)
	editRepoFile(t, root, "app/app.go", "// Run does it.", "// Run does it, reworded.")

	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})

	if got := strings.Join(resp.ConsumerFiles.ContractChanged, ","); got != "app/app.go" {
		t.Errorf("contractChanged = %q, want the file with the comment edit", got)
	}
	if resp.RiskScore.Level != "high" {
		t.Errorf("risk = %s, want high: %s", resp.RiskScore.Level, resp.RiskScore.Explanation)
	}
	check := resp.DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("status = %s, want open: %s", check.Status, check.Note)
	}
	if len(check.OpenSites) != 2 {
		t.Fatalf("openSites = %+v, want the call and the test", check.OpenSites)
	}
	first := check.OpenSites[0]
	if first.File != "app/app.go" || first.Line != 6 || first.Code != "lib.Target(1)" || first.IsTest {
		t.Errorf("first site = %+v, want production code first, with its line and code", first)
	}
	if second := check.OpenSites[1]; second.File != "lib/lib_test.go" || !second.IsTest {
		t.Errorf("second site = %+v, want the test", second)
	}
}

func TestDoneCheck_AllUsesTouchedIsClearWithoutTheFloor(t *testing.T) {
	engine, root := doneCheckRepo(t)
	changeTargetSignature(t, root)
	editRepoFile(t, root, "app/app.go", "lib.Target(1)", "lib.Target(1, \"x\")")
	editRepoFile(t, root, "lib/lib_test.go", "Target(1)", "Target(1, \"x\")")

	resp := runChangeSet(t, engine, AnalyzeChangeSetOptions{})

	check := resp.DoneCheck
	if check.Status != DoneCheckClear || check.OpenCount != 0 || check.UpdatedCount != 2 {
		t.Errorf("check = %+v, want clear with two uses touched", check)
	}
	if len(resp.ConsumerFiles.ContractChanged) != 0 {
		t.Errorf("contractChanged = %v, want none", resp.ConsumerFiles.ContractChanged)
	}
	if resp.RiskScore.Level != "medium" {
		t.Errorf("risk = %s, want medium from the exported symbol only: %s", resp.RiskScore.Level, resp.RiskScore.Explanation)
	}
}

// Uncommitted edits are what the check looks at, so they can't be what disqualifies it.
func TestDoneCheck_BodyEditIsClear(t *testing.T) {
	engine, root := doneCheckRepo(t)
	editRepoFile(t, root, "lib/lib.go", "return a", "return a + 1")
	if check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck; check.Status != DoneCheckClear {
		t.Errorf("check = %+v, want clear", check)
	}
}

func TestDoneCheck_DiffIdentifiesTheEdit(t *testing.T) {
	engine, root := doneCheckRepo(t)
	changeTargetSignature(t, root)
	before := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck.Diff
	editRepoFile(t, root, "app/app.go", "// Run does it.", "// Run does it, reworded.")
	after := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck.Diff
	if before == "" || before == after {
		t.Errorf("diff before = %q, after = %q, want them to differ", before, after)
	}
}

func TestDoneCheck_NotAssessed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, engine *Engine, root string)
		want  string
	}{
		{
			name: "index behind HEAD",
			setup: func(t *testing.T, engine *Engine, root string) {
				gitRun(t, root, "commit", "-q", "--allow-empty", "-m", "newer")
				editRepoFile(t, root, "lib/lib.go", "return a", "return a + 1")
			},
			want: "1 commit(s) behind",
		},
		{
			name: "index commit unknown",
			setup: func(t *testing.T, engine *Engine, root string) {
				engine.scipAdapter.GetIndex().IndexedCommit = ""
				editRepoFile(t, root, "lib/lib.go", "return a", "return a + 1")
			},
			want: "unknown",
		},
		{
			name: "changed file not in the index",
			setup: func(t *testing.T, engine *Engine, root string) {
				writeRepoFile(t, root, "tools/gen.go", "package tools\n")
				gitRun(t, root, "add", "-A")
				gitRun(t, root, "commit", "-q", "-m", "tool")
				engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
				editRepoFile(t, root, "tools/gen.go", "package tools", "package tools // edited")
			},
			want: "aren't in the index (tools/gen.go)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, root := doneCheckRepo(t)
			tt.setup(t, engine, root)
			check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
			if check.Status != DoneCheckNotAssessed || !strings.Contains(check.Note, tt.want) {
				t.Errorf("check = %+v, want not_assessed saying %q", check, tt.want)
			}
		})
	}
}

// Text next to the code can't hold a use, so it can't cost the check its clear.
func TestDoneCheck_DocsDontCostTheClear(t *testing.T) {
	engine, root := doneCheckRepo(t)
	writeRepoFile(t, root, "README.md", "# one\n")
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", "docs")
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	editRepoFile(t, root, "README.md", "one", "two")
	editRepoFile(t, root, "lib/lib.go", "return a", "return a + 1")
	if check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck; check.Status != DoneCheckClear {
		t.Errorf("check = %+v, want clear", check)
	}
}

// A new file has no uses to leave behind, and not being indexed yet is what it is.
func TestDoneCheck_NewFileIsClear(t *testing.T) {
	engine, root := doneCheckRepo(t)
	writeRepoFile(t, root, "tools/gen.go", "package tools\n")
	gitRun(t, root, "add", "-N", "tools/gen.go")
	if check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck; check.Status != DoneCheckClear {
		t.Errorf("check = %+v, want clear", check)
	}
}

func TestDoneCheck_NoChangesIsClear(t *testing.T) {
	engine, _ := doneCheckRepo(t)
	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckClear || check.Note != NothingToAssess {
		t.Errorf("check = %+v, want clear with nothing to assess", check)
	}
}

func TestDoneResult(t *testing.T) {
	open := []OpenSite{{Symbol: "S", File: "b.go", Line: 1}, {Symbol: "S", File: "a_test.go", Line: 1, IsTest: true}, {Symbol: "S", File: "a.go", Line: 2}}
	tests := []struct {
		name     string
		checker  doneChecker
		hasGraph bool
		nothing  bool
		doubt    string
		status   string
		note     string
	}{
		{name: "no graph says so", status: DoneCheckNotAssessed, note: "No SCIP index"},
		{name: "nothing beats no graph", nothing: true, status: DoneCheckClear, note: NothingToAssess},
		{name: "clean", hasGraph: true, status: DoneCheckClear, note: "not a proof"},
		{name: "a stale index", hasGraph: true, doubt: "stale", status: DoneCheckNotAssessed, note: "stale"},
		{name: "a cut list", checker: doneChecker{cut: true}, hasGraph: true, status: DoneCheckNotAssessed, note: "not every use"},
		{name: "a failed lookup", checker: doneChecker{failed: 2}, hasGraph: true, status: DoneCheckNotAssessed, note: "2 changed declaration"},
		{name: "an unreadable declaration", checker: doneChecker{unreadable: []string{"a.go:F"}}, hasGraph: true, status: DoneCheckNotAssessed, note: "can't be compared"},
		{name: "a changed module path", checker: doneChecker{modulePath: []string{"go.mod"}}, hasGraph: true, status: DoneCheckNotAssessed, note: "module path in go.mod changed"},
		{name: "open wins and keeps the doubt", checker: doneChecker{sites: open, cut: true}, hasGraph: true, status: DoneCheckOpen, note: "There may be more: not every use"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.checker.result("diff", tt.hasGraph, tt.nothing, tt.doubt)
			if got.Status != tt.status || !strings.Contains(got.Note, tt.note) {
				t.Errorf("got %s %q, want %s with %q", got.Status, got.Note, tt.status, tt.note)
			}
		})
	}

	sorted := (&doneChecker{sites: open}).result("diff", true, false, "")
	if sorted.OpenSites[0].File != "a.go" || sorted.OpenSites[2].File != "a_test.go" {
		t.Errorf("sites = %+v, want production first, then by file", sorted.OpenSites)
	}

	many := doneChecker{sites: make([]OpenSite, MaxOpenSites+5)}
	cut := many.result("diff", true, false, "")
	if len(cut.OpenSites) != MaxOpenSites || !cut.Truncated || cut.OpenCount != MaxOpenSites+5 {
		t.Errorf("listed %d of %d, truncated %v", len(cut.OpenSites), cut.OpenCount, cut.Truncated)
	}
}

func TestDoneResult_SaysWhenAnIndexIsBeingBuilt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("nothing locks the index file on Windows")
	}
	root := t.TempDir()
	lock, err := index.AcquireLock(filepath.Join(root, ".ckb"))
	if err != nil {
		t.Fatal(err)
	}
	during := (&doneChecker{repoRoot: root}).result("diff", false, false, "")
	lock.Release()
	if during.Status != DoneCheckNotAssessed || !strings.Contains(during.Note, "being built") {
		t.Errorf("during a build: %+v", during)
	}
	after := (&doneChecker{repoRoot: root}).result("diff", false, false, "")
	if !strings.Contains(after.Note, "Run 'ckb index'") {
		t.Errorf("after the build: %+v", after)
	}
}

type fakeReferences struct {
	result *backends.ReferencesResult
	err    error
}

func (f fakeReferences) FindReferences(context.Context, string, backends.RefOptions) (*backends.ReferencesResult, error) {
	return f.result, f.err
}

func TestCheckSymbol(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "use.ts", "import { save } from './lib'\n\nsave(1)\n")
	refs := func(total int) *backends.ReferencesResult {
		return &backends.ReferencesResult{TotalReferences: total, References: []backends.Reference{
			{Kind: "reference", Location: backends.Location{Path: "use.ts", Line: 1}},
			{Kind: "reference", Location: backends.Location{Path: "use.ts", Line: 3}},
			{Kind: "reference", Location: backends.Location{Path: "missing.ts", Line: 3}},
		}}
	}
	sym := impact.ChangedSymbol{SymbolID: "s", Name: "Lib.save()"}

	checker := func() *doneChecker {
		return newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	}

	// A changed signature leaves the import valid; a removal takes the import with it.
	changed := checker()
	files, ok := changed.checkSymbol(context.Background(), fakeReferences{result: refs(3)}, sym, impact.DeclChanged)
	if !ok || !files["use.ts"] || len(changed.sites) != 1 || changed.sites[0].Line != 3 {
		t.Errorf("changed: ok %v, files %v, sites %+v, want only the call", ok, files, changed.sites)
	}
	if !changed.cut {
		t.Error("an unreadable file must cost the check its clear")
	}
	removed := checker()
	removed.checkSymbol(context.Background(), fakeReferences{result: refs(3)}, sym, impact.DeclRemoved)
	if len(removed.sites) != 2 {
		t.Errorf("removed: sites %+v, want the import and the call", removed.sites)
	}

	short := checker()
	short.checkSymbol(context.Background(), fakeReferences{result: refs(99)}, sym, impact.DeclChanged)
	if !short.cut {
		t.Error("a capped reference list must cost the check its clear")
	}

	failed := checker()
	if _, ok := failed.checkSymbol(context.Background(), fakeReferences{err: errors.New("no")}, sym, impact.DeclChanged); ok || failed.failed != 1 {
		t.Errorf("failed lookup: ok %v, failed %d", ok, failed.failed)
	}
	none := checker()
	if _, ok := none.checkSymbol(context.Background(), nil, sym, impact.DeclChanged); ok || none.failed != 1 {
		t.Errorf("no finder: ok %v, failed %d", ok, none.failed)
	}
}

func TestLeafName(t *testing.T) {
	for in, want := range map[string]string{"Save": "Save", "Store.Put()": "Put", "a::b::c": "c", "Foo#bar().": "bar", "": ""} {
		if got := leafName(in); got != want {
			t.Errorf("leafName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChangesGoModulePath(t *testing.T) {
	hunk := func(removed, added string) impact.ChangedHunk {
		return impact.ChangedHunk{
			OldStart: 1, NewStart: 1, Removed: []int{3}, Added: []int{3},
			OldText: []string{"", "go 1.26", removed}, NewText: []string{"", "go 1.26", added},
		}
	}
	for _, tt := range []struct {
		name string
		file impact.ChangedFile
		want bool
	}{
		{"module renamed", impact.ChangedFile{OldPath: "go.mod", NewPath: "go.mod", Hunks: []impact.ChangedHunk{hunk("module a.com/x", "module a.com/y")}}, true},
		{"nested module", impact.ChangedFile{OldPath: "tools/go.mod", NewPath: "tools/go.mod", Hunks: []impact.ChangedHunk{hunk("module a.com/x", "module a.com/y")}}, true},
		{"a dependency bump", impact.ChangedFile{OldPath: "go.mod", NewPath: "go.mod", Hunks: []impact.ChangedHunk{hunk("\tgolang.org/x/term v0.45.0", "\tgolang.org/x/term v0.46.0")}}, false},
		{"go.mod deleted", impact.ChangedFile{OldPath: "go.mod", Deleted: true, Hunks: []impact.ChangedHunk{{OldStart: 1, Removed: []int{1}, OldText: []string{"module a.com/x"}}}}, true},
		{"go.mod new", impact.ChangedFile{NewPath: "go.mod", IsNew: true, Hunks: []impact.ChangedHunk{{NewStart: 1, Added: []int{1}, NewText: []string{"module a.com/x"}}}}, false},
		{"not a go.mod", impact.ChangedFile{OldPath: "a.go", NewPath: "a.go", Hunks: []impact.ChangedHunk{hunk("module a.com/x", "module a.com/y")}}, false},
	} {
		if got := changesGoModulePath(&tt.file); got != tt.want {
			t.Errorf("%s: changesGoModulePath = %v, want %v", tt.name, got, tt.want)
		}
	}
}
