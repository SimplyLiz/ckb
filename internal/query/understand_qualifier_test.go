package query

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitQualifiedQuery(t *testing.T) {
	cases := []struct{ q, c, m string }{
		{"Backend.Priority", "Backend", "Priority"},
		{"Backend#Priority()", "Backend", "Priority"},
		{"backends.Backend.Priority", "Backend", "Priority"},
		{"Priority", "", ""},
		{"scip-go gomod m v `p`/F().", "", ""},
		{".Priority", "", ""},
	}
	for _, c := range cases {
		gotC, gotM, _ := splitQualifiedQuery(c.q)
		if gotC != c.c || gotM != c.m {
			t.Errorf("splitQualifiedQuery(%q) = %q, %q; want %q, %q", c.q, gotC, gotM, c.c, c.m)
		}
	}
}

// The fixture has Process on both CachingService and DefaultService: a
// "Container.Member" query must land on that container's method, and the
// resolved symbol must carry its module.
func TestUnderstand_ContainerQualifier(t *testing.T) {
	dir := t.TempDir()
	engine := newTestEngineWithGit(t, dir)
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "go", ".scip", "index.scip"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), fixture, 0644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := engine.ReloadSCIP(context.Background()); err != nil {
		t.Fatalf("ReloadSCIP: %v", err)
	}

	for _, container := range []string{"CachingService", "DefaultService"} {
		for _, sep := range []string{".", "#"} {
			q := container + sep + "Process"
			resp, err := engine.Understand(context.Background(), UnderstandOptions{Query: q})
			if err != nil {
				t.Fatalf("Understand(%q): %v", q, err)
			}
			if resp.Symbol == nil {
				t.Fatalf("Understand(%q): no symbol", q)
			}
			if !strings.Contains(resp.Symbol.StableId, container+"#Process") {
				t.Errorf("Understand(%q) resolved to %s", q, resp.Symbol.StableId)
			}
			if resp.Symbol.ModuleId == "" {
				t.Errorf("Understand(%q): empty moduleId", q)
			}
		}
	}
}

// The real case: search ranks LspAdapter#Priority first for "Backend.Priority".
func TestQualifiedMatches_PrefersContainer(t *testing.T) {
	const pkg = "scip-go gomod github.com/SimplyLiz/CodeMCP 67d776584a29 "
	items := []SearchResultItem{
		{StableId: pkg + "`github.com/SimplyLiz/CodeMCP/internal/backends/lsp`/LspAdapter#Priority().", Name: "Priority", ModuleId: "internal/backends/lsp"},
		{StableId: pkg + "`github.com/SimplyLiz/CodeMCP/internal/backends/scip`/SCIPAdapter#Priority().", Name: "Priority", ModuleId: "internal/backends/scip"},
		{StableId: pkg + "`github.com/SimplyLiz/CodeMCP/internal/backends`/Backend#Priority.", Name: "Priority", ModuleId: "internal/backends"},
	}
	if got := qualifiedMatches(items, "Backend.Priority"); len(got) != 1 || got[0] != 2 {
		t.Errorf("Backend.Priority: got %v, want [2]", got)
	}
	if got := qualifiedMatches(items, "Priority"); got != nil {
		t.Errorf("unqualified query: got %v, want none", got)
	}
	// No type container: fall back to the package directory.
	fn := []SearchResultItem{
		{StableId: pkg + "`github.com/SimplyLiz/CodeMCP/internal/api`/NewEngine().", Name: "NewEngine", ModuleId: "internal/api"},
		{StableId: pkg + "`github.com/SimplyLiz/CodeMCP/internal/query`/NewEngine().", Name: "NewEngine", ModuleId: "internal/query"},
	}
	if got := qualifiedMatches(fn, "query.NewEngine"); len(got) != 1 || got[0] != 1 {
		t.Errorf("query.NewEngine: got %v, want [1]", got)
	}
}
