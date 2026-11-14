package query

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
)

func TestFilesLinked(t *testing.T) {
	model := &scip.Document{RelativePath: "web/db/model.ts"}
	view := &scip.Document{RelativePath: "web/ui/view.ts"}
	other := &scip.Document{RelativePath: "web/ui/other.ts"}
	defOcc := &scip.Occurrence{Symbol: "npm pkg web/db/model.ts/loadModel().", SymbolRoles: scip.SymbolRoleDefinition}
	model.Occurrences = []*scip.Occurrence{defOcc}
	// view.ts uses loadModel through an @/ alias Cartographer never resolved.
	view.Occurrences = []*scip.Occurrence{{Symbol: defOcc.Symbol}, {Symbol: "local 1"}}
	other.Occurrences = []*scip.Occurrence{{Symbol: "local 2"}}
	idx := &scip.SCIPIndex{
		DocumentsByPath: map[string]*scip.Document{model.RelativePath: model, view.RelativePath: view, other.RelativePath: other},
		DefinitionIndex: map[string]*scip.OccurrenceRef{defOcc.Symbol: {Doc: model, Occ: defOcc}},
	}

	cases := []struct {
		name string
		idx  *scip.SCIPIndex
		a, b string
		want bool
	}{
		{"same Go package", nil, "internal/query/a.go", "internal/query/b.go", true},
		{"different Go packages, no index", nil, "internal/query/a.go", "internal/mcp/b.go", false},
		{"SCIP reference, either order", idx, "web/db/model.ts", "web/ui/view.ts", true},
		{"SCIP reference", idx, "web/ui/view.ts", "web/db/model.ts", true},
		{"no reference", idx, "web/ui/other.ts", "web/db/model.ts", false},
		{"file not indexed", idx, "web/ui/missing.ts", "web/db/model.ts", false},
	}
	for _, c := range cases {
		if got := filesLinked(c.idx, c.a, c.b); got != c.want {
			t.Errorf("%s: filesLinked(%s, %s) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

func TestHiddenCouplingIsHidden(t *testing.T) {
	doc := func(p string) *scip.Document { return &scip.Document{RelativePath: p} }
	idx := &scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}}
	for _, p := range []string{"svc/a.go", "web/b.ts", "pkg/x.go", "pkg/y.go", "web/c.ts"} {
		idx.DocumentsByPath[p] = doc(p)
	}
	var commits [][]string
	for i := 0; i < 5; i++ {
		commits = append(commits,
			[]string{"svc/a.go", "web/b.ts", "CHANGELOG.md"},
			[]string{"pkg/x.go", "pkg/y.go"})
	}
	// Two shared commits: under the floor whatever the score.
	commits = append(commits, []string{"svc/a.go", "web/c.ts"}, []string{"svc/a.go", "web/c.ts"})
	// web/d.ts is not in the index: its links are unknown, not absent.
	for i := 0; i < 4; i++ {
		commits = append(commits, []string{"svc/a.go", "web/d.ts"})
	}
	h := &hiddenCoupling{h: coupling.HistoryFromCommits(commits), idx: idx, minCorrelation: 0.5}

	if !h.isHidden("web/b.ts", "svc/a.go") {
		t.Error("unlinked pair should be hidden")
	}
	if h.isHidden("pkg/x.go", "pkg/y.go") {
		t.Error("same-package Go files are linked, not hidden")
	}
	if h.isHidden("svc/a.go", "CHANGELOG.md") {
		t.Error("a changelog imports nothing; the pair is not hidden coupling")
	}
	if h.isHidden("svc/a.go", "web/c.ts") {
		t.Error("two shared commits are under the floor")
	}
	if h.isHidden("svc/a.go", "web/d.ts") {
		t.Error("a file outside the index has unknown links")
	}
	if h.isHidden("svc/a.go", "pkg/x.go") {
		t.Error("files that never changed together are not hidden")
	}
	got := h.hiddenPartners("svc/a.go")
	if len(got) != 1 || got[0].FileB != "web/b.ts" || got[0].Count != 5 {
		t.Errorf("hiddenPartners(svc/a.go) = %+v, want only web/b.ts with 5 commits", got)
	}
	var nilH *hiddenCoupling
	if nilH.isHidden("a", "b") || nilH.hiddenPartners("a") != nil {
		t.Error("nil hiddenCoupling must report nothing")
	}
}

func TestFilesLinkedGoPackage(t *testing.T) {
	doc := func(p string, occs ...*scip.Occurrence) *scip.Document {
		return &scip.Document{RelativePath: p, Occurrences: occs}
	}
	def := func(sym string) *scip.Occurrence {
		return &scip.Occurrence{Symbol: sym, SymbolRoles: scip.SymbolRoleDefinition}
	}
	use := func(sym string) *scip.Occurrence { return &scip.Occurrence{Symbol: sym} }
	const (
		mapSym  = "scip-go gomod example.com/m . `example.com/m/internal/diff`/MapSymbols()."
		siteSym = "scip-go gomod example.com/m . `example.com/m/internal/diff`/UseSite#"
		viewSym = "npm pkg web/ui/view.ts/render()."
		otherTS = "npm pkg web/ui/other.ts/helper()."
	)
	symbolmap := doc("internal/diff/symbolmap.go", def(mapSym))
	usesite := doc("internal/diff/usesite.go", def(siteSym))
	// A test of package diff that defines nothing anyone uses.
	usesiteTest := doc("internal/diff/usesite_test.go", use(siteSym), def("scip-go gomod example.com/m . `example.com/m/internal/diff`/TestUseSite()."))
	// donecheck.go imports diff and uses MapSymbols, nothing of usesite_test.go.
	donecheck := doc("internal/query/donecheck.go", use(mapSym))
	// A test in query that uses diff.
	donecheckTest := doc("internal/query/donecheck_test.go", use(siteSym))
	// A Go package that never touches diff.
	unrelated := doc("internal/mcp/tools.go", use("local 1"))
	view := doc("web/ui/view.ts", def(viewSym))
	other := doc("web/ui/other.ts", def(otherTS))
	// model.ts uses view.ts, nothing of other.ts in the same directory.
	model := doc("web/db/model.ts", use(viewSym))

	idx := &scip.SCIPIndex{DocumentsByPath: map[string]*scip.Document{}, DefinitionIndex: map[string]*scip.OccurrenceRef{}}
	for _, d := range []*scip.Document{symbolmap, usesite, usesiteTest, donecheck, donecheckTest, unrelated, view, other, model} {
		idx.DocumentsByPath[d.RelativePath] = d
		for _, o := range d.Occurrences {
			if o.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				idx.DefinitionIndex[o.Symbol] = &scip.OccurrenceRef{Doc: d, Occ: o}
			}
		}
	}

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"Go file uses the package, not the file", "internal/query/donecheck.go", "internal/diff/usesite.go", true},
		{"_test.go in the used package", "internal/diff/usesite_test.go", "internal/query/donecheck.go", true},
		{"_test.go on the using side", "internal/query/donecheck_test.go", "internal/diff/symbolmap.go", true},
		{"Go package without a reference", "internal/mcp/tools.go", "internal/diff/symbolmap.go", false},
		{"TypeScript stays file-exact", "web/db/model.ts", "web/ui/other.ts", false},
		{"TypeScript reference", "web/db/model.ts", "web/ui/view.ts", true},
	}
	for _, c := range cases {
		if got := filesLinked(idx, c.a, c.b); got != c.want {
			t.Errorf("%s: filesLinked(%s, %s) = %v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}
