package query

import (
	"fmt"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// implFixture is an interface Backend with two implementations: mock in the interface's
// package, whose ID comes from an embedded type so only the index's relationship finds
// it, and Adapter in another package, which the index has no relationship for, as
// scip-go writes none across packages. Score has the same method names with another
// signature, so it implements nothing.
const (
	symImplBackend  = "scip-go gomod example.com/m v1 `example.com/m/be`/Backend#"
	symImplMarker   = "scip-go gomod example.com/m v1 `example.com/m/be`/Marker#"
	symImplBase     = "scip-go gomod example.com/m v1 `example.com/m/be`/base#"
	symImplMock     = "scip-go gomod example.com/m v1 `example.com/m/be`/mock#"
	symImplAdapter  = "scip-go gomod example.com/m v1 `example.com/m/adapter`/Adapter#"
	symImplScore    = "scip-go gomod example.com/m v1 `example.com/m/decoy`/Score#"
	implBackendText = "package be\n\ntype Backend interface {\n\tID() string\n\tPriority() int\n}\n\ntype Marker interface{}\n"
)

var implFixtureFiles = map[string]string{
	"be/be.go":           implBackendText,
	"be/mock.go":         "package be\n\ntype base struct{}\n\nfunc (base) ID() string { return \"b\" }\n\ntype mock struct{ base }\n\nfunc (m *mock) Priority() int { return 1 }\n",
	"adapter/adapter.go": "package adapter\n\ntype Adapter struct{}\n\nfunc (a *Adapter) ID() string { return \"a\" }\n\nfunc (a *Adapter) Priority() int { return 2 }\n",
	"decoy/decoy.go":     "package decoy\n\ntype Score struct{}\n\nfunc (s Score) ID() string { return \"s\" }\n\nfunc (s Score) Priority() string { return \"high\" }\n",
}

func implFixtureRepo(t *testing.T) (*Engine, string) {
	t.Helper()
	mock := typeFixtureDoc("be/mock.go", "go", []typeOcc{
		{symImplBase, 2, 5, true, scippb.SymbolInformation_Struct},
		{symImplBase + "ID().", 4, 12, true, scippb.SymbolInformation_Method},
		{symImplMock, 6, 5, true, scippb.SymbolInformation_Struct},
		{symImplMock + "Priority().", 8, 15, true, scippb.SymbolInformation_Method},
	})
	for _, info := range mock.Symbols {
		if info.Symbol == symImplMock+"Priority()." {
			info.Relationships = []*scippb.Relationship{{Symbol: symImplBackend + "Priority.", IsImplementation: true}}
		}
	}
	engine := changeSetEngine(t,
		typeFixtureDoc("be/be.go", "go", []typeOcc{
			{symImplBackend, 2, 5, true, scippb.SymbolInformation_Interface},
			{symImplBackend + "ID.", 3, 1, true, scippb.SymbolInformation_Method},
			{symImplBackend + "Priority.", 4, 1, true, scippb.SymbolInformation_Method},
			{symImplMarker, 7, 5, true, scippb.SymbolInformation_Interface},
		}),
		mock,
		typeFixtureDoc("adapter/adapter.go", "go", []typeOcc{
			{symImplAdapter, 2, 5, true, scippb.SymbolInformation_Struct},
			{symImplAdapter + "ID().", 4, 18, true, scippb.SymbolInformation_Method},
			{symImplAdapter + "Priority().", 6, 18, true, scippb.SymbolInformation_Method},
		}),
		typeFixtureDoc("decoy/decoy.go", "go", []typeOcc{
			{symImplScore, 2, 5, true, scippb.SymbolInformation_Struct},
			{symImplScore + "ID().", 4, 15, true, scippb.SymbolInformation_Method},
			{symImplScore + "Priority().", 6, 15, true, scippb.SymbolInformation_Method},
		}),
	)
	root := engine.repoRoot
	writeRepoFile(t, root, ".gitignore", ".ckb/\nindex.scip\n")
	for path, content := range doneCheckFiles {
		writeRepoFile(t, root, path, content)
	}
	for path, content := range implFixtureFiles {
		writeRepoFile(t, root, path, content)
	}
	gitInit(t, root)
	engine.scipAdapter.GetIndex().IndexedCommit = headCommit(t, root)
	return engine, root
}

func TestDoneCheck_InterfaceMethodRenamedListsImplementations(t *testing.T) {
	engine, root := implFixtureRepo(t)
	editRepoFile(t, root, "be/be.go", "\tPriority() int\n", "\tRank() int\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("check = %+v, want open", check)
	}
	// Adapter only by its method set, mock only by the index's relationship; Score's
	// Priority returns a string, so it implements nothing.
	want := []string{"adapter/adapter.go:7", "be/mock.go:9"}
	if got := openSiteKeys(check); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("open sites = %v, want %v", got, want)
	}
	for _, site := range check.OpenSites {
		if site.Via != "implements Backend" || site.Symbol != "Backend.Priority" || site.Change != "removed" {
			t.Errorf("site = %+v", site)
		}
	}
	if check.OpenSites[0].Consumer != "Adapter.Priority" {
		t.Errorf("consumer = %q, want the implementing method", check.OpenSites[0].Consumer)
	}
}

func TestDoneCheck_RenamedImplementationIsUpdated(t *testing.T) {
	engine, root := implFixtureRepo(t)
	editRepoFile(t, root, "be/be.go", "\tPriority() int\n", "\tRank() int\n")
	editRepoFile(t, root, "adapter/adapter.go", "func (a *Adapter) Priority() int", "func (a *Adapter) Rank() int")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[be/mock.go:9]" {
		t.Errorf("open sites = %v, want only mock", got)
	}
	if check.UpdatedCount != 1 {
		t.Errorf("updated = %d, want the adapter's method", check.UpdatedCount)
	}
}

func TestDoneCheck_InterfaceSignatureChangeListsImplementations(t *testing.T) {
	engine, root := implFixtureRepo(t)
	editRepoFile(t, root, "be/be.go", "\tPriority() int\n", "\tPriority() int64\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	want := []string{"adapter/adapter.go:7", "be/mock.go:9"}
	if got := openSiteKeys(check); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("open sites = %v, want %v", got, want)
	}
}

func TestDoneCheck_MethodAddedToInterfaceListsImplementors(t *testing.T) {
	engine, root := implFixtureRepo(t)
	editRepoFile(t, root, "be/be.go", "\tPriority() int\n", "\tPriority() int\n\tName() string\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckOpen {
		t.Fatalf("check = %+v, want open", check)
	}
	want := []string{"adapter/adapter.go:3", "be/mock.go:7"}
	if got := openSiteKeys(check); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("open sites = %v, want %v: the types, which lack the new method", got, want)
	}
	if site := check.OpenSites[0]; site.Symbol != "Backend.Name" || site.Via != "implements Backend" {
		t.Errorf("site = %+v", site)
	}

	// The adapter gaining the method handles it.
	editRepoFile(t, root, "adapter/adapter.go", "type Adapter struct{}\n", "type Adapter struct{}\n\nfunc (a *Adapter) Name() string { return \"a\" }\n")
	check = runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if got := openSiteKeys(check); fmt.Sprint(got) != "[be/mock.go:7]" {
		t.Errorf("open sites = %v, want only mock", got)
	}
	if check.UpdatedCount != 1 {
		t.Errorf("updated = %d, want the adapter", check.UpdatedCount)
	}
}

func TestDoneCheck_InterfaceChangeWithoutLookupIsNotAssessed(t *testing.T) {
	engine, root := implFixtureRepo(t)
	// Every type implemented the empty interface, so its implementors can't be listed.
	editRepoFile(t, root, "be/be.go", "type Marker interface{}\n", "type Marker interface {\n\tMark()\n}\n")

	check := runChangeSet(t, engine, AnalyzeChangeSetOptions{}).DoneCheck
	if check.Status != DoneCheckNotAssessed {
		t.Errorf("check = %+v, want not_assessed", check)
	}
}

// A one-method interface is satisfied by every type with that method; one whose
// package doesn't import the interface's is only counted.
func TestGoSeesPackage(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	writeRepoFile(t, root, "a/a.go", "package a\n\ntype Labeler interface{ Label() string }\n")
	writeRepoFile(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nvar _ a.Labeler\n")
	writeRepoFile(t, root, "c/c.go", "package c\n\ntype U struct{}\n")
	writeRepoFile(t, root, "tools/go.mod", "module example.com/tools\n")
	writeRepoFile(t, root, "tools/x/x.go", "package x\n")

	if got := goImportPath(root, "a"); got != "example.com/m/a" {
		t.Errorf("goImportPath(a) = %q", got)
	}
	if got := goImportPath(root, "tools/x"); got != "example.com/tools/x" {
		t.Errorf("goImportPath(tools/x) = %q, want the nested module's", got)
	}
	c := &doneChecker{repoRoot: root}
	sees := func(typeFile string) bool {
		return c.goSeesPackage(&scip.GoTypeMethods{Type: scip.DefinitionSite{Path: typeFile}}, "a/a.go")
	}
	if !sees("a/impl.go") || !sees("b/b.go") || sees("c/c.go") {
		t.Errorf("same package %v, importer %v, stranger %v; want true, true, false", sees("a/impl.go"), sees("b/b.go"), sees("c/c.go"))
	}
	if got := rarelyLine(RarelyGroup{Symbol: "Labeler.Label", Change: "changed", Via: "implements Labeler", Count: 1, Files: 1, Uses: map[string]int{useNameMatch: 1}}); got != "Labeler.Label (changed; implements Labeler): 1 in 1 file, 1 name-match" {
		t.Errorf("rarelyLine = %q", got)
	}
}
