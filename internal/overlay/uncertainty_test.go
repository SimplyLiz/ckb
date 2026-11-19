package overlay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// buildFor runs Build over a repo dir holding newText at lib/lib.go, with
// base the index of goOld plus a user of Target in app/app.go.
func buildFor(t *testing.T, newText string) *Info {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte(newText), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &scip.Document{RelativePath: "app/app.go", Occurrences: []*scip.Occurrence{
		{Range: []int32{3, 1, 7}, Symbol: goPkg + "Target().", SymbolRoles: scip.SymbolRoleReadAccess},
	}}
	base := scip.PatchIndex(&scip.SCIPIndex{}, map[string]*scip.Document{"lib/lib.go": goOldDoc(), "app/app.go": app})
	ch := &Changes{Root: dir, Diff: &impact.ParsedDiff{Files: []impact.ChangedFile{
		{OldPath: "lib/lib.go", NewPath: "lib/lib.go", Hunks: diffOf(t, goOld, newText)},
	}}}
	_, info := Build(base, ch)
	return info
}

func TestRebuildReason(t *testing.T) {
	// A body edit: nothing the overlay cannot do.
	body := strings.Replace(goOld, "return a\n", "return a + 1\n", 1)
	if r := buildFor(t, body).RebuildReason(); r != "" {
		t.Errorf("body edit: rebuild %q, want none", r)
	}
	// Target's signature changes and app/app.go, unchanged, calls it.
	sig := strings.Replace(goOld, "func Target(a int) int {", "func Target(a, b int) int {", 1)
	if r := buildFor(t, sig).RebuildReason(); !strings.Contains(r, "Target().") {
		t.Errorf("signature change: rebuild %q, want it to name Target", r)
	}
	// Gone() changes too, but nobody else uses it: no reason by itself.
	gone := strings.Replace(goOld, "func Gone() {}", "func Gone(x int) {}", 1)
	if r := buildFor(t, gone).RebuildReason(); r != "" {
		t.Errorf("unused declaration changed: rebuild %q, want none", r)
	}
	// A syntax error: the file stays at Layer 0.
	broken := strings.Replace(goOld, "return a\n}", "return a +\n", 1)
	if r := buildFor(t, broken).RebuildReason(); !strings.Contains(r, "can only remap") {
		t.Errorf("syntax error: rebuild %q, want the Layer 0 reason", r)
	}
	// Unresolved sites past the threshold.
	in := &Info{Uncertainty: Uncertainty{UnresolvedSites: UnresolvedRebuildThreshold}}
	if !strings.Contains(in.RebuildReason(), "unresolved") {
		t.Errorf("threshold: %q", in.RebuildReason())
	}
	in.Uncertainty.UnresolvedSites--
	if in.RebuildReason() != "" {
		t.Errorf("below the threshold: %q", in.RebuildReason())
	}
}
