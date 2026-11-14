package query

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// An interface method has no references from the types implementing it, so its
// impact was its callers alone: Backend.Priority, called nowhere and implemented
// four times, read as nothing depending on it. The implementations are found the
// way the done check finds them: Adapter by its method set, mock by the index's
// relationship; Score's Priority returns a string and implements nothing.
func TestAnalyzeImpact_InterfaceMethodListsImplementations(t *testing.T) {
	engine, _ := implFixtureRepo(t)
	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: symImplBackend + "Priority.", Depth: 2})
	if err != nil {
		t.Fatalf("AnalyzeImpact: %v", err)
	}
	var got []string
	for _, item := range resp.DirectImpact {
		if item.Kind != kindImplementation {
			t.Errorf("unexpected consumer %+v", item)
			continue
		}
		got = append(got, fmt.Sprintf("%s %s", item.Name, item.Location.FileId))
	}
	sort.Strings(got)
	if want := "[Adapter.Priority adapter/adapter.go mock.Priority be/mock.go]"; fmt.Sprint(got) != want {
		t.Errorf("implementations = %v, want %s", got, want)
	}
	if !strings.Contains(resp.RiskScore.Explanation, "2 implementation(s)") {
		t.Errorf("explanation doesn't name them: %q", resp.RiskScore.Explanation)
	}
}

// A rename of the interface method breaks its implementations, so it gets the
// floor; deleting it doesn't (an extra method is no error), and neither does a
// rename of the interface type, which Go types satisfy structurally.
func TestPrepareChange_ImplementationsFeedRisk(t *testing.T) {
	engine, _ := implFixtureRepo(t)
	ctx := context.Background()

	rename, err := engine.PrepareChange(ctx, PrepareChangeOptions{Target: symImplBackend + "Priority.", ChangeType: ChangeRename})
	if err != nil {
		t.Fatalf("PrepareChange: %v", err)
	}
	if rename.TransitiveImpact.Implementations != 2 || rename.TransitiveImpact.DirectCallers != 0 {
		t.Errorf("summary = %+v, want 2 implementations, 0 callers", rename.TransitiveImpact)
	}
	if rename.RiskAssessment.Level != "high" || !containsString(rename.RiskAssessment.Factors, "rename breaks 2 implementation(s) unless they change too") {
		t.Errorf("rename risk = %+v, want the floor for 2 implementations", rename.RiskAssessment)
	}

	del, err := engine.PrepareChange(ctx, PrepareChangeOptions{Target: symImplBackend + "Priority.", ChangeType: ChangeDelete})
	if err != nil {
		t.Fatalf("PrepareChange: %v", err)
	}
	if del.RiskAssessment.Score >= highRiskFloor {
		t.Errorf("delete risk = %+v: deleting an interface method breaks no implementation", del.RiskAssessment)
	}

	modify, err := engine.PrepareChange(ctx, PrepareChangeOptions{Target: symImplBackend + "Priority.", ChangeType: ChangeModify})
	if err != nil {
		t.Fatalf("PrepareChange: %v", err)
	}
	if !strings.Contains(modify.RiskAssessment.Basis, "2 implementation(s) stay as they are") {
		t.Errorf("modify basis = %q", modify.RiskAssessment.Basis)
	}

	typeRename, err := engine.PrepareChange(ctx, PrepareChangeOptions{Target: symImplBackend, ChangeType: ChangeRename})
	if err != nil {
		t.Fatalf("PrepareChange: %v", err)
	}
	if typeRename.TransitiveImpact.Implementations != 2 {
		t.Errorf("interface type: summary = %+v, want its 2 implementations listed", typeRename.TransitiveImpact)
	}
	for _, f := range typeRename.RiskAssessment.Factors {
		if strings.Contains(f, "implementation(s) unless") {
			t.Errorf("renaming the interface type breaks no implementation, got %q", f)
		}
	}
}

// a962f49's rule: for a one-method interface, a type whose package doesn't import
// the interface's is listed apart and not counted.
func TestAnalyzeImpact_OneMethodInterfaceNameMatchApart(t *testing.T) {
	const (
		symLabeler = "scip-go gomod example.com/m v1 `example.com/m/lbl`/Labeler#"
		symNear    = "scip-go gomod example.com/m v1 `example.com/m/near`/Near#"
		symFar     = "scip-go gomod example.com/m v1 `example.com/m/far`/Far#"
	)
	engine := changeSetEngine(t,
		typeFixtureDoc("lbl/lbl.go", "go", []typeOcc{
			{symLabeler, 2, 5, true, scippb.SymbolInformation_Interface},
			{symLabeler + "Label.", 2, 26, true, scippb.SymbolInformation_Method},
		}),
		typeFixtureDoc("near/near.go", "go", []typeOcc{
			{symNear, 4, 5, true, scippb.SymbolInformation_Struct},
			{symNear + "Label().", 8, 15, true, scippb.SymbolInformation_Method},
		}),
		typeFixtureDoc("far/far.go", "go", []typeOcc{
			{symFar, 2, 5, true, scippb.SymbolInformation_Struct},
			{symFar + "Label().", 4, 14, true, scippb.SymbolInformation_Method},
		}),
	)
	root := engine.repoRoot
	writeRepoFile(t, root, "go.mod", "module example.com/m\n\ngo 1.22\n")
	writeRepoFile(t, root, "lbl/lbl.go", "package lbl\n\ntype Labeler interface{ Label() string }\n")
	writeRepoFile(t, root, "near/near.go", "package near\n\nimport \"example.com/m/lbl\"\n\ntype Near struct{}\n\nvar _ lbl.Labeler = Near{}\n\nfunc (Near) Label() string { return \"n\" }\n")
	writeRepoFile(t, root, "far/far.go", "package far\n\ntype Far struct{}\n\nfunc (Far) Label() string { return \"f\" }\n")

	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: symLabeler + "Label.", Depth: 1})
	if err != nil {
		t.Fatalf("AnalyzeImpact: %v", err)
	}
	var counted, apart []string
	for _, item := range resp.DirectImpact {
		counted = append(counted, item.Name)
	}
	for _, item := range resp.NameMatchImplementations {
		apart = append(apart, item.Name)
	}
	if fmt.Sprint(counted) != "[Near.Label]" || fmt.Sprint(apart) != "[Far.Label]" {
		t.Errorf("counted %v, apart %v; want [Near.Label] and [Far.Label]", counted, apart)
	}
}

// After an edit to the interface, assessChange asks about the symbol as indexed:
// the implementations are matched against the declaration of the indexed commit,
// not the edited file, whose new signature none of them has yet.
func TestAnalyzeImpact_ImplementationsOfTheIndexedInterface(t *testing.T) {
	engine, root := implFixtureRepo(t)
	editRepoFile(t, root, "be/be.go", "\tPriority() int\n", "\tPriority() int64\n")
	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: symImplBackend + "Priority.", Depth: 1})
	if err != nil {
		t.Fatalf("AnalyzeImpact: %v", err)
	}
	if len(resp.DirectImpact) != 2 {
		t.Errorf("got %d implementations after the edit, want the 2 of the indexed interface: %+v", len(resp.DirectImpact), resp.DirectImpact)
	}
}

func TestGoDeclaresInterface(t *testing.T) {
	cases := map[string]bool{
		"type Backend interface {":            true,
		"type (\n\tBackend interface {":       true,
		"type Backend[T any] interface{}":     true,
		"type Backend struct{}":               false,
		"type BackendX interface{}":           false,
		"// Backend interface is described..": false,
	}
	for src, want := range cases {
		if got := goDeclaresInterface(src, "Backend"); got != want {
			t.Errorf("goDeclaresInterface(%q) = %v, want %v", src, got, want)
		}
	}
}
