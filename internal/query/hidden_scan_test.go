package query

import (
	"context"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/perf"
)

func TestScanHiddenCoupling(t *testing.T) {
	model := &scip.Document{RelativePath: "web/db/model.ts"}
	view := &scip.Document{RelativePath: "web/ui/view.ts"}
	def := &scip.Occurrence{Symbol: "npm pkg web/db/model.ts/loadModel().", SymbolRoles: scip.SymbolRoleDefinition}
	model.Occurrences = []*scip.Occurrence{def}
	view.Occurrences = []*scip.Occurrence{{Symbol: def.Symbol}}
	idx := &scip.SCIPIndex{
		DocumentsByPath: map[string]*scip.Document{model.RelativePath: model, view.RelativePath: view},
		DefinitionIndex: map[string]*scip.OccurrenceRef{def.Symbol: {Doc: model, Occ: def}},
	}
	for _, p := range []string{"svc/a.go", "web/b.ts", "pkg/x.go", "pkg/y.go", "svc/c.go"} {
		idx.DocumentsByPath[p] = &scip.Document{RelativePath: p}
	}
	var commits [][]string
	for i := 0; i < 5; i++ {
		commits = append(commits,
			[]string{"svc/a.go", "web/b.ts", "CHANGELOG.md"}, // hidden; changelog is no code
			[]string{"pkg/x.go", "pkg/y.go"},                 // one Go package: linked
			[]string{"web/db/model.ts", "web/ui/view.ts"},    // SCIP reference: linked
			[]string{"svc/c.go", "web/new.ts"})               // web/new.ts not indexed
	}
	commits = append(commits, []string{"svc/a.go", "pkg/x.go"}, []string{"svc/a.go", "pkg/x.go"}) // under the floor
	h := coupling.HistoryFromCommits(commits)
	opts := perf.ScanOptions{MinCorrelation: 0.3, MinCoChanges: 3, Limit: 50}

	res, err := scanHiddenCoupling(context.Background(), h, idx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.HiddenCoupling) != 1 {
		t.Fatalf("hidden = %+v, want only svc/a.go/web/b.ts", res.HiddenCoupling)
	}
	p := res.HiddenCoupling[0]
	if p.FileA != "svc/a.go" || p.FileB != "web/b.ts" || p.CoChangeCount != 5 || p.Level != "high" {
		t.Errorf("pair = %+v, want svc/a.go/web/b.ts, 5 commits, high", p)
	}
	s := res.Summary
	// a/b, a/CHANGELOG, b/CHANGELOG, x/y, model/view, c/new pass the thresholds;
	// the two CHANGELOG pairs and c/new have a file that is no indexed code.
	if s.PairsChecked != 6 || s.PairsNotIndexed != 3 || s.HiddenPairsFound != 1 || s.CommitsRead != 22 || s.NotAssessed != "" {
		t.Errorf("summary = %+v", s)
	}

	scoped, _ := scanHiddenCoupling(context.Background(), h, idx, perf.ScanOptions{MinCorrelation: 0.3, MinCoChanges: 3, Limit: 50, Scope: []string{"svc/"}})
	if len(scoped.HiddenCoupling) != 0 {
		t.Errorf("scope svc/ keeps only pairs inside svc/, got %+v", scoped.HiddenCoupling)
	}
	limited, _ := scanHiddenCoupling(context.Background(), h, idx, perf.ScanOptions{MinCorrelation: 0.3, MinCoChanges: 3, Limit: 1})
	if len(limited.HiddenCoupling) != 1 || limited.Summary.HiddenPairsFound != 1 {
		t.Errorf("limit 1: %+v", limited)
	}

	none, _ := scanHiddenCoupling(context.Background(), h, nil, opts)
	if len(none.HiddenCoupling) != 0 || none.Summary.NotAssessed == "" || none.Summary.PairsChecked != 0 {
		t.Errorf("without an index nothing is hidden and NotAssessed says why: %+v", none.Summary)
	}
}

func TestInScanScope(t *testing.T) {
	cases := []struct {
		file  string
		scope []string
		want  bool
	}{
		{"a/b.go", nil, true},
		{"internal/auth/x.go", []string{"internal/auth"}, true},
		{"internal/auth/x.go", []string{"internal/auth/"}, true},
		{"internal/authz/x.go", []string{"internal/auth"}, false},
		{"main.go", []string{"main.go"}, true},
		{"main.go", []string{"."}, true},
	}
	for _, c := range cases {
		if got := inScanScope(c.file, c.scope); got != c.want {
			t.Errorf("inScanScope(%q, %v) = %v, want %v", c.file, c.scope, got, c.want)
		}
	}
}
