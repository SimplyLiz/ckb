package query

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
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
	h := &hiddenCoupling{scores: map[string]cartographer.CoChangePair{}}
	for _, p := range []cartographer.CoChangePair{
		{FileA: "svc/a.go", FileB: "web/b.ts", Count: 5, CouplingScore: 0.8},
		{FileA: "pkg/x.go", FileB: "pkg/y.go", Count: 5, CouplingScore: 0.9},
		{FileA: "svc/a.go", FileB: "CHANGELOG.md", Count: 9, CouplingScore: 0.9},
	} {
		h.scores[p.FileA+"\x00"+p.FileB] = p
		h.scores[p.FileB+"\x00"+p.FileA] = p
	}
	if !h.isHidden("web/b.ts", "svc/a.go") {
		t.Error("unlinked pair should be hidden")
	}
	if h.isHidden("pkg/x.go", "pkg/y.go") {
		t.Error("same-package Go files are linked, not hidden")
	}
	if h.isHidden("svc/a.go", "CHANGELOG.md") {
		t.Error("a changelog imports nothing; the pair is not hidden coupling")
	}
	if h.isHidden("svc/a.go", "pkg/x.go") {
		t.Error("a pair Cartographer did not report is not hidden")
	}
	if got := len(h.candidates()); got != 3 {
		t.Errorf("candidates = %d, want 3 (each pair once)", got)
	}
	var nilH *hiddenCoupling
	if nilH.isHidden("a", "b") || nilH.candidates() != nil {
		t.Error("nil hiddenCoupling must report nothing")
	}
}

func TestStrongCochangePairs(t *testing.T) {
	in := []cartographer.CoChangePair{
		{FileA: "a", FileB: "b", Count: 2, CouplingScore: 1.0}, // two commits: dropped
		{FileA: "a", FileB: "c", Count: 4, CouplingScore: 0.5},
		{FileA: "a", FileB: "d", Count: 9, CouplingScore: 0.9},
		{FileA: "a", FileB: "e", Count: 6, CouplingScore: 0.2}, // under the floor
	}
	got := strongCochangePairs(in, 0.3)
	if len(got) != 2 || got[0].FileB != "d" || got[1].FileB != "c" {
		t.Errorf("got %+v, want d then c", got)
	}
}
