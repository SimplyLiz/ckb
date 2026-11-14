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
