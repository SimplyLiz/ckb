package scip

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

const pyPkg = "scip-python python . 0123 "

func pySym(module, rest string) string { return pyPkg + "`" + module + "`/" + rest }

func pyDef(line, col, end int32, symbol string) *scippb.Occurrence {
	return &scippb.Occurrence{Range: []int32{line, col, end}, Symbol: symbol, SymbolRoles: SymbolRoleDefinition}
}

// The layout of a repo of scripts: app/ reaches lib/ through sys.path.insert, which
// scip-python doesn't follow, and lib/trim.py imports its sibling, which it does.
func writePyRepairFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"lib/acct.py": "PRICES = {}\n\n\ndef price(model, w):\n    return PRICES.get(model)\n",
		"lib/trim.py": "from acct import (PRICES,\n                  price)\n\n\ndef cut(r):\n    return price(r, 1), PRICES\n",
		"app/report.py": "import sys\nsys.path.insert(0, \"../lib\")\nimport acct as lc  # noqa: E402\n\n\n" +
			"def run(m):\n    # lc.price in a comment\n    s = \"lc.price in a string\"\n    return lc.price(m, 2), lc.missing\n",
		"app/bill.py":        "from acct import price, PRICES as P\n\n\ndef bill(m):\n    return price(m, 0), P\n",
		"app/shadow.py":      "from acct import price\n\n\ndef price(m):\n    return m\n",
		"app/star.py":        "from acct import *\n\nx = price(1, 2)\n",
		"app/test_report.py": "import report as r\n\n\ndef test_price():\n    assert r.lc.price(\"x\", 0) is None\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx := &scippb.Index{
		Metadata: &scippb.Metadata{ProjectRoot: "file://" + root},
		Documents: []*scippb.Document{
			{RelativePath: "lib/acct.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("lib.acct", "__init__:")),
				pyDef(0, 0, 6, pySym("lib.acct", "PRICES.")),
				pyDef(3, 4, 9, pySym("lib.acct", "price().")),
				pyDef(3, 10, 15, pySym("lib.acct", "price().(model)")),
			}},
			{RelativePath: "lib/trim.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("lib.trim", "__init__:")),
				{Range: []int32{1, 18, 23}, Symbol: pySym("lib.acct", "price()."), SymbolRoles: SymbolRoleReadAccess},
				pyDef(4, 4, 7, pySym("lib.trim", "cut().")),
				{Range: []int32{5, 11, 16}, Symbol: pySym("lib.acct", "price()."), SymbolRoles: SymbolRoleReadAccess},
			}},
			{RelativePath: "app/report.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("app.report", "__init__:")),
				{Range: []int32{2, 7, 11}, Symbol: "local 0", SymbolRoles: SymbolRoleReadAccess},
				pyDef(5, 4, 7, pySym("app.report", "run().")),
				{Range: []int32{8, 11, 13}, Symbol: "local 0", SymbolRoles: SymbolRoleReadAccess},
			}},
			{RelativePath: "app/bill.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("app.bill", "__init__:")),
				{Range: []int32{0, 17, 22}, Symbol: "local 0", SymbolRoles: SymbolRoleReadAccess},
				pyDef(3, 4, 8, pySym("app.bill", "bill().")),
			}},
			{RelativePath: "app/shadow.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("app.shadow", "__init__:")),
				pyDef(3, 4, 9, pySym("app.shadow", "price().")),
			}},
			{RelativePath: "app/star.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("app.star", "__init__:")),
				pyDef(2, 0, 1, pySym("app.star", "x.")),
			}},
			{RelativePath: "app/test_report.py", Occurrences: []*scippb.Occurrence{
				pyDef(0, 0, 0, pySym("app.test_report", "__init__:")),
				pyDef(3, 4, 14, pySym("app.test_report", "test_price().")),
			}},
		},
	}
	data, err := proto.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(root, "index.scip")
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, indexPath
}

func pyOccurrencesOf(t *testing.T, indexPath, symbol string) []string {
	t.Helper()
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var idx scippb.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbol && occ.SymbolRoles&SymbolRoleDefinition == 0 {
				out = append(out, fmt.Sprintf("%s:%d,%d,%d", doc.RelativePath, occ.Range[0], occ.Range[1], occ.Range[2]))
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestRepairPythonImports(t *testing.T) {
	root, indexPath := writePyRepairFixture(t)
	added, err := RepairPythonImports(root, indexPath)
	if err != nil {
		t.Fatal(err)
	}

	got := pyOccurrencesOf(t, indexPath, pySym("lib.acct", "price()."))
	want := []string{
		"app/bill.py:0,17,22",        // from acct import price
		"app/bill.py:4,11,16",        // price(m, 0)
		"app/report.py:8,14,19",      // lc.price(m, 2); not the comment or the string
		"app/star.py:2,4,9",          // from acct import *
		"app/test_report.py:4,16,21", // r.lc.price: one hop through report
		"lib/trim.py:1,18,23",        // resolved by the indexer: untouched
		"lib/trim.py:5,11,16",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("price occurrences\n got %v\nwant %v", got, want)
	}
	// `PRICES as P`: the import names PRICES, the use is of the local name.
	if got := pyOccurrencesOf(t, indexPath, pySym("lib.acct", "PRICES.")); strings.Join(got, " ") != "app/bill.py:0,24,30 app/bill.py:4,24,25" {
		t.Errorf("PRICES occurrences = %v", got)
	}
	// shadow.py declares its own price: nothing of acct's is added there.
	// Seven occurrences added, and trim.py's import line given the import role.
	if added != 8 {
		t.Errorf("added = %d, want 8", added)
	}
	data, _ := os.ReadFile(indexPath)
	var idx scippb.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		t.Fatal(err)
	}
	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.SymbolRoles&SymbolRoleDefinition != 0 || strings.HasPrefix(occ.Symbol, "local ") {
				continue
			}
			onImport := (doc.RelativePath == "lib/trim.py" && occ.Range[0] <= 1) || (doc.RelativePath == "app/bill.py" && occ.Range[0] == 0)
			if got := occ.SymbolRoles == SymbolRoleImport; got != onImport {
				t.Errorf("%s:%v roles %d, import line %v", doc.RelativePath, occ.Range, occ.SymbolRoles, onImport)
			}
		}
	}

	// Running it again finds nothing left to add.
	if again, err := RepairPythonImports(root, indexPath); err != nil || again != 0 {
		t.Errorf("second run added %d (%v), want 0", again, err)
	}
}

func TestRepairPythonImports_NoPython(t *testing.T) {
	root := t.TempDir()
	idx := &scippb.Index{Documents: []*scippb.Document{{RelativePath: "main.go", Occurrences: []*scippb.Occurrence{
		pyDef(0, 0, 4, "scip-go gomod x v1 `x`/main()."),
	}}}}
	data, _ := proto.Marshal(idx)
	indexPath := filepath.Join(root, "index.scip")
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(indexPath)
	if added, err := RepairPythonImports(root, indexPath); err != nil || added != 0 {
		t.Fatalf("added %d (%v)", added, err)
	}
	after, _ := os.Stat(indexPath)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an index without Python was rewritten")
	}
}

func TestMaskPython(t *testing.T) {
	in := []string{`x = "a.b" + 'c' # lc.price`, `s = """`, `lc.price`, `"""; y = lc.z`}
	got := maskPython(in)
	for i := range in {
		if len(got[i]) != len(in[i]) {
			t.Errorf("line %d changed length: %q", i, got[i])
		}
	}
	if strings.Contains(got[0], "a.b") || strings.Contains(got[0], "lc") || !strings.HasPrefix(got[0], `x = "`) {
		t.Errorf("line 0: %q", got[0])
	}
	if strings.TrimSpace(got[2]) != "" {
		t.Errorf("inside a triple-quoted string: %q", got[2])
	}
	if !strings.HasSuffix(got[3], "y = lc.z") {
		t.Errorf("after the string closes: %q", got[3])
	}
}

// scip-python indexes git-ignored scratch too; the repair drops those documents.
func TestRepairPythonImports_DropsIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for rel, body := range map[string]string{".gitignore": "Temp/\n", "a.py": "x = 1\n", "Temp/scratch.py": "y = 2\n"} {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	idx := &scippb.Index{Documents: []*scippb.Document{
		{RelativePath: "a.py", Occurrences: []*scippb.Occurrence{pyDef(0, 0, 1, pySym("a", "x."))}},
		{RelativePath: "Temp/scratch.py", Occurrences: []*scippb.Occurrence{pyDef(0, 0, 1, pySym("Temp.scratch", "y."))}},
	}}
	data, _ := proto.Marshal(idx)
	indexPath := filepath.Join(root, "index.scip")
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairPythonImports(root, indexPath); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(indexPath)
	var got scippb.Index
	if err := proto.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Documents) != 1 || got.Documents[0].RelativePath != "a.py" {
		t.Errorf("documents = %v, want only a.py", got.Documents)
	}
}
