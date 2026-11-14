package unwired

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/deadcode"
)

func fixtureAdapter(t *testing.T, dir string) (*scip.SCIPAdapter, string) {
	t.Helper()
	root, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.DefaultConfig()
	cfg.RepoRoot = root
	cfg.Backends.Scip.Enabled = true
	cfg.Backends.Scip.IndexPath = filepath.Join(root, "index.scip")
	adapter, err := scip.NewSCIPAdapter(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, root
}

func reported(result *Result) map[string]UnwiredItem {
	got := make(map[string]UnwiredItem)
	for _, m := range result.UnwiredModules {
		for _, item := range m.Items {
			got[item.FilePath+" "+item.SymbolName] = item
		}
	}
	return got
}

// testdata/go is a real scip-go index (regenerate with `scip-go index` in
// that directory; *.scip is gitignored, so `git add -f` it). main.go calls
// registry.Run, which calls every check through the Check interface; serve.go
// registers server.Start from an init nothing references.
func TestAnalyzeGoFixture(t *testing.T) {
	adapter, root := fixtureAdapter(t, "testdata/go")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDetector(adapter, root, logger)
	reachable, _ := d.BuildReachableSet(context.Background(), []architecture.Entrypoint{
		{FileId: "main.go", Name: "main.go", Kind: architecture.EntrypointCLI},
	}, 0)
	result, err := d.Analyze(context.Background(), DetectorOptions{}, reachable)
	if err != nil {
		t.Fatal(err)
	}
	got := reported(result)

	for _, key := range []string{
		"check/alpha.go Extra", // not in the interface, nothing calls it
		"check/beta.go Run",    // satisfies Check, but nothing builds a Beta
		"server/server.go Stop",
		"orphan/orphan.go Orphan",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("%s not reported; got %v", key, keys(got))
		}
	}
	for _, key := range []string{
		"check/alpha.go Run",     // reached through Check
		"server/server.go Start", // reached through serve.go's init
		"registry/registry.go Run",
		"check/check.go All",
	} {
		if item, ok := got[key]; ok {
			t.Errorf("%s reported as unwired: %+v", key, item)
		}
	}
	if result.Summary.ViaInterface != 1 {
		t.Errorf("ViaInterface = %d, want 1 (alpha.Run)", result.Summary.ViaInterface)
	}
	start := false
	for sym := range reachable {
		if strings.HasSuffix(sym, "`example.com/unw/server`/Start().") {
			start = true
		}
	}
	if !start {
		t.Errorf("server.Start not reachable; reachable has %d symbols", len(reachable))
	}
}

func keys(m map[string]UnwiredItem) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSplitMember(t *testing.T) {
	cases := map[string][3]string{
		"scip-go gomod m v1 `m/check`/alpha#Run().":      {"scip-go gomod m v1 `m/check`/alpha#", "Run", "ok"},
		"scip-go gomod m v1 `m/check`/Check#Run.":        {"scip-go gomod m v1 `m/check`/Check#", "Run", "ok"},
		"rust-analyzer cargo d 0.1.0 util/Foo#run(+1).":  {"rust-analyzer cargo d 0.1.0 util/Foo#", "run", "ok"},
		"scip-typescript npm m 1.0.0 src/`lib.ts`/Box#":  {"", "", ""},
		"scip-go gomod m v1 `m/check`/All().":            {"", "", ""},
		"scip-go gomod m v1 `m/check`/Check#Run().(ctx)": {"scip-go gomod m v1 `m/check`/Check#", "Run", "ok"},
	}
	for symbol, want := range cases {
		parent, name, ok := deadcode.SplitMember(symbol)
		if parent != want[0] || name != want[1] || ok != (want[2] == "ok") {
			t.Errorf("%s = %q %q %v, want %q %q %v", symbol, parent, name, ok, want[0], want[1], want[2] == "ok")
		}
	}
	if got := deadcode.DisplayName("scip-go gomod m v1 `m/check`/Check#"); got != "Check" {
		t.Errorf("DisplayName = %q", got)
	}
}

func TestCutToLimit(t *testing.T) {
	item := func(name string) UnwiredItem { return UnwiredItem{SymbolName: name} }
	modules := []UnwiredModule{
		{Path: "a", Items: []UnwiredItem{item("a1"), item("a2"), item("a3"), item("a4")}, Summary: ModuleSummary{UnwiredCount: 4}},
		{Path: "b", Items: []UnwiredItem{item("b1"), item("b2")}, Summary: ModuleSummary{UnwiredCount: 2}},
		{Path: "c", Items: []UnwiredItem{item("c1")}, Summary: ModuleSummary{UnwiredCount: 1}},
	}
	listed, truncated := cutToLimit(modules, 5)
	if !truncated {
		t.Fatal("7 items over a limit of 5 is truncated")
	}
	var shape []string
	for _, mod := range listed {
		shape = append(shape, mod.Path)
		var names []string
		for _, it := range mod.Items {
			names = append(names, it.SymbolName)
		}
		shape = append(shape, names...)
	}
	want := []string{"a", "a1", "a2", "b", "b1", "b2", "c", "c1"}
	if !reflect.DeepEqual(shape, want) {
		t.Errorf("listed = %v, want %v: one per module in turn, every module keeps its top items", shape, want)
	}
	if listed[0].Summary.UnwiredCount != 4 {
		t.Errorf("module summary = %d, want the full count 4", listed[0].Summary.UnwiredCount)
	}
	if _, truncated := cutToLimit(modules, 7); truncated {
		t.Error("7 items within a limit of 7 is not truncated")
	}
	if _, truncated := cutToLimit(modules, 0); truncated {
		t.Error("limit 0 means no limit")
	}
	listed, _ = cutToLimit(modules, 2)
	if len(listed) != 2 || len(listed[0].Items) != 1 || len(listed[1].Items) != 1 {
		t.Errorf("limit 2 = %+v, want a1 and b1", listed)
	}
}
