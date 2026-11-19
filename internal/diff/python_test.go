package diff

import (
	"slices"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

func TestPythonRenamedKeywords(t *testing.T) {
	tests := []struct {
		name, before, after string
		want                []string
	}{
		{"renamed with default", "def f(mid, out=0, model=OPUS)", "def f(mid, out=0, mdl=OPUS)", []string{"model"}},
		{"renamed required", "def f(a, b)", "def f(a, c)", []string{"b"}},
		{"renamed and added", "def f(a)", "def f(b, c=1)", nil},
		{"renamed and annotated", "def f(a)", "def f(b: int)", nil},
		{"default dropped too", "def f(a=1)", "def f(b)", nil},
		{"nothing renamed", "def f(a)", "def f(a)", nil},
		{"return changed too", "def f(a) -> int", "def f(b) -> str", nil},
		{"positional-only renamed", "def f(a, /)", "def f(b, /)", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PythonRenamedKeywords(tt.before, tt.after, "f"); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPythonCallPassesKeyword(t *testing.T) {
	kw := []string{"model"}
	tests := []struct {
		name  string
		lines []string
		want  bool
	}{
		{"by position", []string{`x = resp("a", 1, 2)`}, false},
		{"other keywords", []string{`x = resp("a", cr=5, out=1)`}, false},
		{"the keyword", []string{`x = resp("a", cr=5, model="m")`}, true},
		{"spaced keyword", []string{`x = resp("a", model = "m")`}, true},
		{"comparison isn't a keyword", []string{`x = resp("a", model == m)`}, false},
		{"unpacked mapping", []string{`x = resp("a", **opts)`}, true},
		{"multi-line", []string{`x = resp(`, `    "a",  # model=no`, `    model="m",`, `)`}, true},
		{"multi-line without", []string{`x = resp(`, `    "a",  # model=no`, `)`}, false},
		{"nested call keyword is not ours", []string{`x = resp(g(model=1), 2)`}, false},
		{"string holding the keyword", []string{`x = resp("model=1")`}, false},
		{"handed on as a value", []string{`fs = [resp, other]`}, true},
		{"never closed", []string{`x = resp("a",`}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PythonCallPassesKeyword(tt.lines, 1, "resp", kw); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// A decorator added, changed or removed above a method is the method's change, not
// the class's.
func TestPythonDecoratorAttribution(t *testing.T) {
	lines := map[int]string{
		1: "class Base:",
		2: "    x = 1",
		3: "",
		4: "    @cached",
		5: "    def uid(self, tag):",
		6: "        return tag",
	}
	class := &SymbolDefInfo{Symbol: "Base", StartLine: 1, EndLine: 6, DefLine: 1}
	uid := &SymbolDefInfo{Symbol: "uid", StartLine: 5, EndLine: 6, DefLine: 5}
	defs := withPythonDecoratorExtents([]*SymbolDefInfo{class, uid}, lines)
	if defs[1].StartLine != 4 || uid.StartLine != 5 {
		t.Fatalf("extended start = %d (original %d), want 4 on a copy", defs[1].StartLine, uid.StartLine)
	}

	// @property inserted above the existing @cached, before old line 4.
	block := impact.ChangeBlock{OldLine: 4, NewLine: 4, Added: []string{"    @property"}}
	if got := pythonDecoratedDef(block, indexMatchesOld, defs, lines); got == nil || got.Symbol != "uid" {
		t.Fatalf("decorated def = %v, want uid", got)
	}
	// A plain line inserted there is the class's, as before.
	block.Added = []string{"    y = 2"}
	if got := pythonDecoratedDef(block, indexMatchesOld, defs, lines); got != nil {
		t.Fatalf("decorated def = %v, want none", got.Symbol)
	}
}

func TestPythonIsCallAndInitClass(t *testing.T) {
	for line, want := range map[string]bool{
		"        self.r = Root()": true,
		"r = Root (1)":            true,
		"class Sub(Root):":        false,
		"if isinstance(x, Root):": false,
		"factory = Root":          false,
		"x = Rooted() + Root()":   true,
	} {
		if got := PythonIsCall([]string{line}, 1, "Root"); got != want {
			t.Errorf("PythonIsCall(%q) = %v, want %v", line, got, want)
		}
	}
	init := "scip-python python . abc `experiments.roi.test_roi1`/Root#__init__()."
	if got := PythonInitClass(init); got != "scip-python python . abc `experiments.roi.test_roi1`/Root#" {
		t.Errorf("PythonInitClass = %q", got)
	}
	if got := PythonInitClass("scip-python python . abc `m`/Root#run()."); got != "" {
		t.Errorf("a method isn't __init__: %q", got)
	}
}
