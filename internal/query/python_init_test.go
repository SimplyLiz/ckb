package query

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"testing"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

const pyPkg = "scip-python python . 1 `pkg.shapes`/"
const pyUse = "scip-python python . 1 `pkg.use`/"

const pyShapes = `class Foo:
    def __init__(self, x):
        self.x = x


class Bar(Foo):
    pass


class Baz(Foo):
    def __init__(self):
        super().__init__(1)


class Qux(Bar):
    pass
`

const pyUses = `from pkg.shapes import Foo, Bar, Baz, Qux


def make_foo():
    return Foo(1)


def make_bar():
    return Bar(2)


def make_baz():
    return Baz()


def make_qux():
    return Qux(3)


def check(o):
    return isinstance(o, Foo)


def outer():
    return make_bar()
`

// pyOcc is an occurrence on a 0-based line; def with end > 0 encloses the lines to end.
type pyOcc struct {
	symbol    string
	line, end int32
	role      scippb.SymbolRole
}

// pyDoc builds a document the way scip-python writes one: a subclass is an
// implementation of its base.
func pyDoc(path string, occs []pyOcc, bases map[string]string) *scippb.Document {
	doc := &scippb.Document{RelativePath: path, Language: "python"}
	for _, o := range occs {
		occ := &scippb.Occurrence{Symbol: o.symbol, Range: []int32{o.line, 4, 8}, SymbolRoles: int32(o.role)}
		if o.role == scippb.SymbolRole_Definition {
			kind := scippb.SymbolInformation_Function
			if o.symbol[len(o.symbol)-1] == '#' {
				kind = scippb.SymbolInformation_Class
			}
			info := &scippb.SymbolInformation{Symbol: o.symbol, Kind: kind}
			if base, ok := bases[o.symbol]; ok {
				info.Relationships = []*scippb.Relationship{{Symbol: base, IsImplementation: true}}
			}
			doc.Symbols = append(doc.Symbols, info)
			if o.end > 0 {
				occ.EnclosingRange = []int32{o.line, 0, o.end, 1}
			}
		}
		doc.Occurrences = append(doc.Occurrences, occ)
	}
	return doc
}

// pyInitEngine is an engine over pyShapes and pyUses with their index.
func pyInitEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/shapes.py", pyShapes)
	writeRepoFile(t, root, "pkg/use.py", pyUses)
	def, read, imp := scippb.SymbolRole_Definition, scippb.SymbolRole_ReadAccess, scippb.SymbolRole_Import
	shapes := pyDoc("pkg/shapes.py", []pyOcc{
		{pyPkg + "Foo#", 0, 2, def},
		{pyPkg + "Foo#__init__().", 1, 2, def},
		{pyPkg + "Bar#", 5, 6, def},
		{pyPkg + "Foo#", 5, 0, read},
		{pyPkg + "Baz#", 9, 11, def},
		{pyPkg + "Foo#", 9, 0, read},
		{pyPkg + "Baz#__init__().", 10, 11, def},
		{pyPkg + "Foo#__init__().", 11, 0, read},
		{pyPkg + "Qux#", 14, 15, def},
		{pyPkg + "Bar#", 14, 0, read},
	}, map[string]string{pyPkg + "Bar#": pyPkg + "Foo#", pyPkg + "Baz#": pyPkg + "Foo#", pyPkg + "Qux#": pyPkg + "Bar#"})
	uses := pyDoc("pkg/use.py", []pyOcc{
		{pyPkg + "Foo#", 0, 0, imp},
		{pyPkg + "Bar#", 0, 0, imp},
		{pyPkg + "Baz#", 0, 0, imp},
		{pyPkg + "Qux#", 0, 0, imp},
		{pyUse + "make_foo().", 3, 4, def},
		{pyPkg + "Foo#", 4, 0, read},
		{pyUse + "make_bar().", 7, 8, def},
		{pyPkg + "Bar#", 8, 0, read},
		{pyUse + "make_baz().", 11, 12, def},
		{pyPkg + "Baz#", 12, 0, read},
		{pyUse + "make_qux().", 15, 16, def},
		{pyPkg + "Qux#", 16, 0, read},
		{pyUse + "check().", 19, 20, def},
		{pyPkg + "Foo#", 20, 0, read},
		{pyUse + "outer().", 23, 24, def},
		{pyUse + "make_bar().", 24, 0, read},
	}, nil)
	return scipEngine(t, root, "scip-python", []*scippb.Document{shapes, uses}), root
}

// A subclass inherits __init__ unless it defines one; Qux inherits it through Bar.
func TestPythonInitClasses(t *testing.T) {
	engine, _ := pyInitEngine(t)
	got := pythonInitClasses(engine.scipAdapter, pyPkg+"Foo#__init__().")
	want := []string{pyPkg + "Foo#", pyPkg + "Bar#", pyPkg + "Qux#"}
	if !slices.Equal(got, want) {
		t.Errorf("classes = %v, want %v", got, want)
	}
	if got := pythonInitClasses(nil, pyPkg+"Foo#__init__()."); !slices.Equal(got, want[:1]) {
		t.Errorf("without an index = %v, want the own class", got)
	}
	if got := pythonInitClasses(engine.scipAdapter, pyUse+"make_foo()."); got != nil {
		t.Errorf("not an __init__ = %v", got)
	}
}

// fakeClassIndex is a hierarchy with a cycle, which no real one has, to show the
// walk stops.
type fakeClassIndex map[string][]string

func (f fakeClassIndex) Implementations(id string) []string { return f[id] }
func (f fakeClassIndex) DefinitionLocation(string) *backends.Location {
	return nil
}

func TestPythonInitClasses_Cycle(t *testing.T) {
	index := fakeClassIndex{pyPkg + "A#": {pyPkg + "B#"}, pyPkg + "B#": {pyPkg + "A#"}}
	if got := pythonInitClasses(index, pyPkg+"A#__init__()."); len(got) != 2 {
		t.Errorf("classes = %v, want A and B once", got)
	}
}

// The constructions are the calls only: not the base class list, isinstance or the
// import, and not Baz(), which runs Baz's own __init__.
func TestPythonConstructions(t *testing.T) {
	engine, root := pyInitEngine(t)
	calls, ok := pythonConstructions(context.Background(), engine.scipAdapter, root, pyPkg+"Foo#__init__().")
	if !ok {
		t.Fatal("lookup failed")
	}
	var got []string
	for _, c := range calls {
		got = append(got, fmt.Sprintf("%s:%d", c.Location.Path, c.Location.Line))
	}
	sort.Strings(got)
	if fmt.Sprint(got) != "[pkg/use.py:17 pkg/use.py:5 pkg/use.py:9]" {
		t.Errorf("constructions = %v, want Foo(1), Bar(2) and Qux(3)", got)
	}
}

// Before, Foo.__init__ had Baz.__init__ as its one consumer: every construction is a
// use of the class. Now the functions constructing Foo, Bar and Qux are direct
// consumers, and the callers of those, and of Baz(), transitive ones.
func TestAnalyzeImpact_PythonInitCountsConstructions(t *testing.T) {
	engine, _ := pyInitEngine(t)
	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: pyPkg + "Foo#__init__().", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	names := func(items []ImpactItem) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.StableId)
		}
		sort.Strings(out)
		return out
	}
	wantDirect := []string{pyPkg + "Baz#__init__().", pyUse + "make_bar().", pyUse + "make_foo().", pyUse + "make_qux()."}
	if got := names(resp.DirectImpact); !slices.Equal(got, wantDirect) {
		t.Errorf("direct = %v, want %v", got, wantDirect)
	}
	wantTransitive := []string{pyUse + "make_baz().", pyUse + "outer()."}
	if got := names(resp.TransitiveImpact); !slices.Equal(got, wantTransitive) {
		t.Errorf("transitive = %v, want %v", got, wantTransitive)
	}
	for _, it := range resp.TransitiveImpact {
		if it.Location == nil || it.Location.FileId != "pkg/use.py" {
			t.Errorf("%s has location %+v, want pkg/use.py", it.StableId, it.Location)
		}
	}
}

// The done check lists the constructions of a subclass that inherits the changed
// __init__, naming it; Baz() runs Baz's own and stays out.
func TestCheckSymbol_PythonInitSubclass(t *testing.T) {
	engine, root := pyInitEngine(t)
	checker := newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	sym := impact.ChangedSymbol{
		SymbolID:          pyPkg + "Foo#__init__().",
		Name:              "__init__",
		File:              "pkg/shapes.py",
		DeclarationBefore: "def __init__(self, x):",
		DeclarationAfter:  "def __init__(self, x, y):",
	}
	if _, ok := checker.checkSymbol(context.Background(), engine.scipAdapter, sym, impact.DeclChanged); !ok {
		t.Fatal("lookup failed")
	}
	var got []string
	for _, s := range checker.sites {
		got = append(got, fmt.Sprintf("%s:%d %s", s.File, s.Line, s.Via))
	}
	sort.Strings(got)
	want := []string{
		"pkg/shapes.py:12 ",
		"pkg/use.py:17 Qux, which inherits Foo.__init__",
		"pkg/use.py:5 ",
		"pkg/use.py:9 Bar, which inherits Foo.__init__",
	}
	if !slices.Equal(got, want) {
		t.Errorf("sites = %q, want %q", got, want)
	}
}

// `Foo(1), Bar(2)` on one line constructs Foo and Bar, and both run Foo.__init__: the
// function was listed twice with the same line (via_module on the Rubik copy, six rows
// for five consumers).
func TestAnalyzeImpact_PythonInitOneRowPerConsumerLine(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/shapes.py", "class Foo:\n    def __init__(self, x):\n        self.x = x\n\n\nclass Bar(Foo):\n    pass\n")
	writeRepoFile(t, root, "pkg/use.py", "from pkg.shapes import Foo, Bar\n\n\ndef both():\n    return Foo(1), Bar(2)\n")
	def, read, imp := scippb.SymbolRole_Definition, scippb.SymbolRole_ReadAccess, scippb.SymbolRole_Import
	shapes := pyDoc("pkg/shapes.py", []pyOcc{
		{pyPkg + "Foo#", 0, 2, def},
		{pyPkg + "Foo#__init__().", 1, 2, def},
		{pyPkg + "Bar#", 5, 6, def},
		{pyPkg + "Foo#", 5, 0, read},
	}, map[string]string{pyPkg + "Bar#": pyPkg + "Foo#"})
	uses := pyDoc("pkg/use.py", []pyOcc{
		{pyPkg + "Foo#", 0, 0, imp},
		{pyPkg + "Bar#", 0, 0, imp},
		{pyUse + "both().", 3, 4, def},
		{pyPkg + "Foo#", 4, 0, read},
		{pyPkg + "Bar#", 4, 0, read},
	}, nil)
	engine := scipEngine(t, root, "scip-python", []*scippb.Document{shapes, uses})
	resp, err := engine.AnalyzeImpact(context.Background(), AnalyzeImpactOptions{SymbolId: pyPkg + "Foo#__init__().", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.DirectImpact) != 1 || resp.DirectImpact[0].StableId != pyUse+"both()." {
		var got []string
		for _, it := range resp.DirectImpact {
			got = append(got, fmt.Sprintf("%s@%v", it.StableId, it.Location))
		}
		t.Errorf("direct = %v, want both() once", got)
	}
}

// `Foo(1), Bar(2)` is one line to edit: the done check lists it once, as Foo's, not a
// second time for Bar, which inherits Foo.__init__ (Rubik's lc1_wins.py:15).
func TestCheckSymbol_PythonInitOneSitePerLine(t *testing.T) {
	root := t.TempDir()
	writeRepoFile(t, root, "pkg/shapes.py", "class Foo:\n    def __init__(self, x):\n        self.x = x\n\n\nclass Bar(Foo):\n    pass\n")
	writeRepoFile(t, root, "pkg/use.py", "from pkg.shapes import Foo, Bar\n\n\ndef both():\n    return Foo(1), Bar(2)\n")
	def, read, imp := scippb.SymbolRole_Definition, scippb.SymbolRole_ReadAccess, scippb.SymbolRole_Import
	shapes := pyDoc("pkg/shapes.py", []pyOcc{
		{pyPkg + "Foo#", 0, 6, def},
		{pyPkg + "Foo#__init__().", 1, 8, def},
		{pyPkg + "Bar#", 5, 6, def},
		{pyPkg + "Foo#", 5, 10, read},
	}, map[string]string{pyPkg + "Bar#": pyPkg + "Foo#"})
	uses := pyDoc("pkg/use.py", []pyOcc{
		{pyPkg + "Foo#", 0, 23, imp},
		{pyPkg + "Bar#", 0, 28, imp},
		{pyUse + "both().", 3, 4, def},
		{pyPkg + "Foo#", 4, 11, read},
		{pyPkg + "Bar#", 4, 19, read},
	}, nil)
	engine := scipEngine(t, root, "scip-python", []*scippb.Document{shapes, uses})
	checker := newDoneChecker(root, AnalyzeChangeSetOptions{}, &impact.ParsedDiff{})
	sym := impact.ChangedSymbol{
		SymbolID:          pyPkg + "Foo#__init__().",
		Name:              "__init__",
		File:              "pkg/shapes.py",
		DeclarationBefore: "def __init__(self, x):",
		DeclarationAfter:  "def __init__(self, x, y):",
	}
	if _, ok := checker.checkSymbol(context.Background(), engine.scipAdapter, sym, impact.DeclChanged); !ok {
		t.Fatal("lookup failed")
	}
	var got []string
	for _, s := range checker.sites {
		got = append(got, fmt.Sprintf("%s:%d %s", s.File, s.Line, s.Via))
	}
	if want := []string{"pkg/use.py:5 "}; !slices.Equal(got, want) {
		t.Errorf("sites = %q, want %q", got, want)
	}
}
