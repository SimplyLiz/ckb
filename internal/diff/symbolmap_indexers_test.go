package diff

import (
	"sort"
	"strings"
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// The documents below have the shape real indexers emit, taken from indexes of
// small sample repos: scip-go gives only functions and methods an EnclosingRange;
// rust-analyzer gives nearly everything one, the crate module included;
// scip-typescript sets no kinds or display names, gives parameters a global
// symbol, and encloses the whole file in a module definition.

const (
	goPkg = "scip-go gomod example.com/demo abc `example.com/demo/lib`/"
	rsPkg = "rust-analyzer cargo demo 0.1.0 "
	tsPkg = "scip-typescript npm demo 1.0.0 src/`lib.ts`/"
)

func def(symbol string, line int32, enclosing ...int32) *scip.Occurrence {
	return &scip.Occurrence{Range: []int32{line, 0, 1}, Symbol: symbol, SymbolRoles: scip.SymbolRoleDefinition, EnclosingRange: enclosing}
}

func ref(symbol string, line int32) *scip.Occurrence {
	return &scip.Occurrence{Range: []int32{line, 0, 1}, Symbol: symbol, SymbolRoles: 8}
}

// goDoc is lib/lib.go:
//
//	 1 package lib
//	 2
//	 3 import "fmt"
//	 4
//	 5 const Limit = 10
//	 6
//	 7 func Target(x int) int {
//	 8 	y := x + 1
//	 9 	fmt.Println(y)
//	10 	return y
//	11 }
//	12
//	13 var Table = map[string]int{
//	14 	"a": 1,
//	15 }
//	16
//	17 type Point struct {
//	18 	X int
//	19 }
//	20
//	21 func After() {}
func goDoc() *scip.Document {
	return &scip.Document{
		RelativePath: "lib/lib.go",
		Occurrences: []*scip.Occurrence{
			def(goPkg, 0),
			ref("scip-go gomod github.com/golang/go/src go1.22 fmt/", 2),
			def(goPkg+"Limit.", 4),
			def(goPkg+"Target().", 6, 6, 0, 10, 1),
			def("local 0", 6),
			def("local 1", 7),
			ref("local 0", 7),
			ref("scip-go gomod github.com/golang/go/src go1.22 fmt/Println().", 8),
			ref("local 1", 8),
			ref("local 1", 9),
			def(goPkg+"Table.", 12),
			def(goPkg+"Point#", 16),
			def(goPkg+"Point#X.", 17),
			def(goPkg+"After().", 20, 20, 0, 20, 15),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: goPkg, DisplayName: "lib", Kind: 35},
			{Symbol: goPkg + "Limit.", DisplayName: "Limit", Kind: 8},
			{Symbol: goPkg + "Target().", DisplayName: "Target", Kind: 17},
			{Symbol: "local 0", DisplayName: "x", Kind: 61},
			{Symbol: "local 1", DisplayName: "y", Kind: 61},
			{Symbol: goPkg + "Table.", DisplayName: "Table", Kind: 61},
			{Symbol: goPkg + "Point#", DisplayName: "Point", Kind: 49},
			{Symbol: goPkg + "Point#X.", DisplayName: "X", Kind: 15},
			{Symbol: goPkg + "After().", DisplayName: "After", Kind: 17},
		},
	}
}

// rustDoc is src/lib.rs:
//
//	 1 pub const LIMIT: i32 = 10;
//	 2
//	 3 pub struct Point {
//	 4     pub x: i32,
//	 5 }
//	 6
//	 7 impl Point {
//	 8     pub fn sum(&self) -> i32 {
//	 9         let total = self.x;
//	10         total
//	11     }
//	12 }
func rustDoc() *scip.Document {
	return &scip.Document{
		RelativePath: "src/lib.rs",
		Occurrences: []*scip.Occurrence{
			def(rsPkg+"crate/", 0, 0, 0, 12, 0),
			def(rsPkg+"LIMIT.", 0, 0, 0, 26),
			def(rsPkg+"Point#", 2, 2, 0, 4, 1),
			def(rsPkg+"Point#x.", 3, 3, 4, 14),
			ref(rsPkg+"Point#", 6),
			def(rsPkg+"impl#[Point]sum().", 7, 7, 4, 10, 5),
			def("local 0", 7, 7, 15, 20),
			def("local 1", 8, 8, 12, 17),
			ref("local 0", 8),
			ref(rsPkg+"Point#x.", 8),
			ref("local 1", 9),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: rsPkg + "crate/", Kind: 29},
			{Symbol: rsPkg + "LIMIT.", DisplayName: "LIMIT", Kind: 8},
			{Symbol: rsPkg + "Point#", DisplayName: "Point", Kind: 49},
			{Symbol: rsPkg + "Point#x.", DisplayName: "x", Kind: 15},
			{Symbol: rsPkg + "impl#[Point]sum().", DisplayName: "sum", Kind: 26},
			{Symbol: "local 0", DisplayName: "self", Kind: 44},
			{Symbol: "local 1", DisplayName: "total", Kind: 61},
		},
	}
}

// tsDoc is src/lib.ts:
//
//	1 export function target(x: number): number {
//	2   const y = x + 1;
//	3   return y;
//	4 }
//	5
//	6 export const arrow = (a: number): number => {
//	7   return a * 2;
//	8 };
func tsDoc() *scip.Document {
	return &scip.Document{
		RelativePath: "src/lib.ts",
		Occurrences: []*scip.Occurrence{
			def(tsPkg, 0, 0, 0, 8, 0),
			def(tsPkg+"target().", 0, 0, 0, 3, 1),
			def(tsPkg+"target().(x)", 0),
			def("local 5", 1),
			ref(tsPkg+"target().(x)", 1),
			ref("local 5", 2),
			def(tsPkg+"arrow.", 5, 5, 21, 7, 1),
			def("local 7", 5),
			ref("local 7", 6),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: tsPkg}, {Symbol: tsPkg + "target()."}, {Symbol: tsPkg + "target().(x)"},
			{Symbol: "local 5"}, {Symbol: tsPkg + "arrow."}, {Symbol: "local 7"},
		},
	}
}

func mapDoc(t *testing.T, doc *scip.Document, diffText string) []impact.ChangedSymbol {
	t.Helper()
	index := &scip.SCIPIndex{Documents: []*scip.Document{doc}, DocumentsByPath: map[string]*scip.Document{doc.RelativePath: doc}, Symbols: map[string]*scip.SymbolInformation{}}
	for _, sym := range doc.Symbols {
		index.Symbols[sym.Symbol] = sym
	}
	parsed, err := ParseGitDiff(diffText)
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	result, err := NewDiffSymbolMapper(NewSCIPSymbolIndex(index)).MapToSymbols(parsed)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	return result
}

func names(symbols []impact.ChangedSymbol) string {
	out := make([]string, 0, len(symbols))
	for _, s := range symbols {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func fileDiff(path, hunks string) string {
	return "--- a/" + path + "\n+++ b/" + path + "\n" + hunks
}

func TestMapToSymbols_InnermostDefinitionOnly(t *testing.T) {
	tests := []struct {
		name string
		doc  *scip.Document
		diff string
		want string
	}{
		{
			name: "go: body line that references others gives the function alone",
			doc:  goDoc(),
			diff: fileDiff("lib/lib.go", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\tfmt.Println(y)\n+\tfmt.Printf(\"%d\", y)\n \treturn y\n"),
			want: "Target",
		},
		{
			name: "go: signature line",
			doc:  goDoc(),
			diff: fileDiff("lib/lib.go", "@@ -6,3 +6,3 @@\n \n-func Target(x int) int {\n+func Target(x int64) int {\n \ty := x + 1\n"),
			want: "Target",
		},
		{
			name: "go: single-line const",
			doc:  goDoc(),
			diff: fileDiff("lib/lib.go", "@@ -4,3 +4,3 @@\n \n-const Limit = 10\n+const Limit = 11\n \n"),
			want: "Limit",
		},
		{
			name: "go: struct field, which has no range but is defined on the line",
			doc:  goDoc(),
			diff: fileDiff("lib/lib.go", "@@ -17,3 +17,3 @@\n type Point struct {\n-\tX int\n+\tX int64\n }\n"),
			want: "X",
		},
		{
			name: "rust: method body, not the impl or the crate",
			doc:  rustDoc(),
			diff: fileDiff("src/lib.rs", "@@ -8,3 +8,3 @@\n     pub fn sum(&self) -> i32 {\n-        let total = self.x;\n+        let total = self.x + 1;\n         total\n"),
			want: "sum",
		},
		{
			name: "rust: field inside a struct is innermost",
			doc:  rustDoc(),
			diff: fileDiff("src/lib.rs", "@@ -3,3 +3,3 @@\n pub struct Point {\n-    pub x: i32,\n+    pub x: i64,\n }\n"),
			want: "x",
		},
		{
			name: "rust: single-line range is one line, not an end column read as a line",
			doc:  rustDoc(),
			diff: fileDiff("src/lib.rs", "@@ -1,3 +1,3 @@\n-pub const LIMIT: i32 = 10;\n+pub const LIMIT: i32 = 11;\n \n pub struct Point {\n"),
			want: "LIMIT",
		},
		{
			name: "typescript: body gives the function, not the module or the parameter",
			doc:  tsDoc(),
			diff: fileDiff("src/lib.ts", "@@ -1,3 +1,3 @@\n export function target(x: number): number {\n-  const y = x + 1;\n+  const y = x + 2;\n   return y;\n"),
			want: "target",
		},
		{
			name: "typescript: arrow function assigned to a const",
			doc:  tsDoc(),
			diff: fileDiff("src/lib.ts", "@@ -6,3 +6,3 @@\n export const arrow = (a: number): number => {\n-  return a * 2;\n+  return a * 3;\n };\n"),
			want: "arrow",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapDoc(t, tt.doc, tt.diff)
			if names(got) != tt.want {
				t.Fatalf("changed symbols = %q, want %q", names(got), tt.want)
			}
		})
	}
}

// With no EnclosingRange at all (older scip-go) the function bound is inferred
// from the next function, and says so through a lower confidence.
func TestMapToSymbols_InferredBoundsHaveLowerConfidence(t *testing.T) {
	doc := goDoc()
	for _, occ := range doc.Occurrences {
		occ.EnclosingRange = nil
	}
	got := mapDoc(t, doc, fileDiff("lib/lib.go", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\tfmt.Println(y)\n+\tfmt.Printf(\"%d\", y)\n \treturn y\n"))
	if names(got) != "Target" {
		t.Fatalf("changed symbols = %q, want Target", names(got))
	}
	if got[0].Confidence != confidenceInferredBody {
		t.Errorf("confidence = %v, want %v", got[0].Confidence, confidenceInferredBody)
	}
}

// A line no definition contains is not attributed to anything.
func TestMapToSymbols_NoEnclosingDefinitionIsLowConfidence(t *testing.T) {
	got := mapDoc(t, goDoc(), fileDiff("lib/lib.go", "@@ -13,3 +13,3 @@\n var Table = map[string]int{\n-\t\"a\": 1,\n+\t\"a\": 2,\n }\n"))
	if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" {
		t.Fatalf("got %+v, want one file-level entry", got)
	}
	if got[0].Confidence >= 0.5 || got[0].Note == "" {
		t.Errorf("want low confidence with a note, got %v %q", got[0].Confidence, got[0].Note)
	}
	if len(got[0].Lines) != 1 || got[0].Lines[0] != 14 {
		t.Errorf("lines = %v, want [14]", got[0].Lines)
	}
}

// The usual case: index built at HEAD, working-tree diff on top. Lines added
// earlier in the file must not push a later change into the wrong function.
func TestMapToSymbols_IndexPredatesChange(t *testing.T) {
	diffText := fileDiff("lib/lib.go",
		"@@ -2,3 +2,6 @@\n \n import \"fmt\"\n+\n+// new comment\n+var extra = 1\n \n"+
			"@@ -20,2 +23,2 @@\n \n-func After() {}\n+func After() { fmt.Println() }\n")
	got := mapDoc(t, goDoc(), diffText)
	want := "After lib/lib.go" // The inserted var isn't in this index
	if names(got) != want {
		t.Fatalf("changed symbols = %q, want %q", names(got), want)
	}
	for _, s := range got {
		if s.Name == "After" && (len(s.Lines) != 1 || s.Lines[0] != 24) {
			t.Errorf("After lines = %v, want new-file line [24]", s.Lines)
		}
	}
}

// The other case: the index already has the change in it (watch mode, or a diff
// of commits that were indexed).
func TestMapToSymbols_IndexIncludesChange(t *testing.T) {
	diffText := fileDiff("lib/lib.go", "@@ -5,3 +5,3 @@\n-const Limit = 9\n+const Limit = 10\n \n func Target(x int) int {\n"+
		"@@ -19,3 +19,3 @@\n }\n \n-func After(a int) {}\n+func After() {}\n")
	if got := mapDoc(t, goDoc(), diffText); names(got) != "After Limit" {
		t.Fatalf("changed symbols = %q, want %q", names(got), "After Limit")
	}
}

func TestMapToSymbols_IndexMatchesNeitherSide(t *testing.T) {
	// Target is on line 7 in the index, but the hunk shows other text around it.
	diffText := fileDiff("lib/lib.go", "@@ -6,3 +6,3 @@\n // unrelated\n-func Renamed() {\n+func RenamedAgain() {\n // unrelated\n")
	got := mapDoc(t, goDoc(), diffText)
	if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" || got[0].Confidence >= 0.5 {
		t.Fatalf("got %+v, want one low-confidence file-level entry", got)
	}
	if !strings.Contains(got[0].Note, "ckb index") {
		t.Errorf("note %q should say how to fix it", got[0].Note)
	}
}

func TestContainsWord(t *testing.T) {
	for _, tt := range []struct {
		text, name string
		want       bool
	}{
		{"func Target(x int) int {", "Target", true},
		{"func Targets(x int) int {", "Target", false},
		{"x := y", "y", true},
		{"yy := myy", "y", false},
		{"yy y", "y", true},
	} {
		if got := containsWord(tt.text, tt.name); got != tt.want {
			t.Errorf("containsWord(%q, %q) = %v", tt.text, tt.name, got)
		}
	}
}
