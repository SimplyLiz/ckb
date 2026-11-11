package diff

import (
	"sort"
	"strconv"
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
	return mapDocWith(t, doc, diffText, nil)
}

func mapDocWith(t *testing.T, doc *scip.Document, diffText string, files FileReader) []impact.ChangedSymbol {
	t.Helper()
	index := &scip.SCIPIndex{Documents: []*scip.Document{doc}, DocumentsByPath: map[string]*scip.Document{doc.RelativePath: doc}, Symbols: map[string]*scip.SymbolInformation{}}
	for _, sym := range doc.Symbols {
		index.Symbols[sym.Symbol] = sym
	}
	parsed, err := ParseGitDiff(diffText)
	if err != nil {
		t.Fatalf("parse diff: %v", err)
	}
	result, err := NewDiffSymbolMapper(NewSCIPSymbolIndex(index)).WithFileReader(files).MapToSymbols(parsed)
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

func TestMapToSymbols_PureDeletionInsideFunction(t *testing.T) {
	// goDoc has the line deleted here, so the index predates the change.
	oldSide := fileDiff("lib/lib.go", "@@ -8,3 +8,2 @@\n \ty := x + 1\n-\tfmt.Println(y)\n \treturn y\n")
	if got := mapDoc(t, goDoc(), oldSide); names(got) != "Target" || got[0].ChangeType != impact.ChangeModified {
		t.Errorf("index before the change: got %+v, want Target modified", got)
	}
	if got := mapDoc(t, goDoc(), oldSide); len(got) == 1 && (len(got[0].Lines) != 1 || got[0].Lines[0] != 9) {
		t.Errorf("lines = %v, want the deletion's position in the new file, [9]", got[0].Lines)
	}

	// goDoc lacks the line deleted here, so the index already has the change: the
	// hunk's position in the new file falls inside Target.
	newSide := fileDiff("lib/lib.go", "@@ -8,4 +8,3 @@\n \ty := x + 1\n-\tlog.Print(z)\n \tfmt.Println(y)\n \treturn y\n")
	if got := mapDoc(t, goDoc(), newSide); names(got) != "Target" || got[0].ChangeType != impact.ChangeModified {
		t.Errorf("index with the change: got %+v, want Target modified", got)
	}
}

// Deleting a whole definition, index from before the change: named, and marked
// deleted, so that impact analysis finds its callers. The doc comment and blank
// line around it don't add a file-level entry.
func TestMapToSymbols_DeletedDefinition(t *testing.T) {
	diffText := fileDiff("lib/lib.go", "@@ -5,8 +5,1 @@\n-const Limit = 10\n-\n-func Target(x int) int {\n-\ty := x + 1\n-\tfmt.Println(y)\n-\treturn y\n-}\n \n")
	got := mapDoc(t, goDoc(), diffText)
	if names(got) != "Limit Target" {
		t.Fatalf("changed symbols = %q, want Limit Target", names(got))
	}
	for _, s := range got {
		if s.ChangeType != impact.ChangeDeleted {
			t.Errorf("%s: change type %s, want deleted", s.Name, s.ChangeType)
		}
		if s.Name == "Target" && (len(s.Lines) != 5 || s.Lines[0] != 7 || s.Lines[4] != 11) {
			t.Errorf("Target lines = %v, want its old lines 7-11", s.Lines)
		}
	}
}

// All lines rewritten but the name still there is a modification, and a body kept
// under a new name is too: what was renamed away isn't "deleted" code.
func TestMapToSymbols_RewriteIsNotDeletion(t *testing.T) {
	rewrite := fileDiff("lib/lib.go", "@@ -7,5 +7,3 @@\n-func Target(x int) int {\n-\ty := x + 1\n-\tfmt.Println(y)\n-\treturn y\n-}\n+func Target(x int) int {\n+\treturn x + 1\n+}\n")
	if got := mapDoc(t, goDoc(), rewrite); names(got) != "Target" || got[0].ChangeType != impact.ChangeModified {
		t.Errorf("rewrite: got %+v, want Target modified", got)
	}
	rename := fileDiff("lib/lib.go", "@@ -7,3 +7,3 @@\n-func Target(x int) int {\n+func Goal(x int) int {\n \ty := x + 1\n \tfmt.Println(y)\n")
	if got := mapDoc(t, goDoc(), rename); names(got) != "Target" || got[0].ChangeType != impact.ChangeModified {
		t.Errorf("rename: got %+v, want Target modified", got)
	}
}

// Deleting a whole definition when the index already includes the deletion: the
// tool can't name it from the index, and has to say that rather than nothing.
func TestMapToSymbols_DeletedDefinitionNotInIndex(t *testing.T) {
	diffText := fileDiff("lib/lib.go", "@@ -11,7 +11,3 @@\n }\n \n-func Doomed(a int) int {\n-\treturn a * 2\n-}\n-\n var Table = map[string]int{\n \t\"a\": 1,\n }\n")
	got := mapDoc(t, goDoc(), diffText)
	if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" {
		t.Fatalf("got %+v, want one file-level entry", got)
	}
	if !got[0].DeletionUnresolved {
		t.Errorf("DeletionUnresolved not set: %+v", got[0])
	}
	if !strings.Contains(got[0].Note, "func Doomed(a int) int {") {
		t.Errorf("note %q should quote the removed line", got[0].Note)
	}
}

// A deleted file with an index from before: every definition in it is reported.
func TestMapToSymbols_DeletedFileInIndex(t *testing.T) {
	diffText := "--- a/src/lib.ts\n+++ /dev/null\n@@ -1,8 +0,0 @@\n-export function target(x: number): number {\n-  const y = x + 1;\n-  return y;\n-}\n-\n-export const arrow = (a: number): number => {\n-  return a * 2;\n-};\n"
	got := mapDoc(t, tsDoc(), diffText)
	if names(got) != "arrow target" {
		t.Fatalf("changed symbols = %q, want arrow target", names(got))
	}
	for _, s := range got {
		if s.ChangeType != impact.ChangeDeleted {
			t.Errorf("%s: change type %s, want deleted", s.Name, s.ChangeType)
		}
	}
}

func TestMapToSymbols_AddedDefinitionInIndex(t *testing.T) {
	diffText := fileDiff("lib/lib.go", "@@ -19,2 +19,4 @@\n }\n \n+func Unused() {}\n+\n func After() {}\n")
	doc := goDoc()
	doc.Occurrences = append(doc.Occurrences, def(goPkg+"Unused().", 20, 20, 0, 20, 16))
	// After moved down with the insertion, so the index has the new file's lines.
	for _, occ := range doc.Occurrences {
		if occ.Symbol == goPkg+"After()." {
			occ.Range = []int32{22, 0, 1}
			occ.EnclosingRange = []int32{22, 0, 22, 15}
		}
	}
	got := mapDoc(t, doc, diffText)
	if len(got) != 1 || got[0].ChangeType != impact.ChangeAdded || !strings.HasSuffix(got[0].SymbolID, "Unused().") {
		t.Fatalf("got %+v, want Unused added", got)
	}
}

// rust-analyzer references an operator's method on the line of the operator, and
// its name isn't on that line. That must not count against the side it's on.
func TestMapToSymbols_OperatorReferencesAreNotEvidence(t *testing.T) {
	add := rsPkg + "ops/arith/impl#[i32][`Add<Self>`]add()."
	doc := rustDoc()
	doc.Occurrences = append(doc.Occurrences, ref(add, 8), ref(add, 8), ref(add, 8), ref(add, 9))
	diffText := fileDiff("src/lib.rs", "@@ -7,6 +7,1 @@\n impl Point {\n-    pub fn sum(&self) -> i32 {\n-        let total = self.x;\n-        total\n-    }\n }\n")
	got := mapDoc(t, doc, diffText)
	if names(got) != "sum" || got[0].ChangeType != impact.ChangeDeleted {
		t.Fatalf("got %+v, want sum deleted", got)
	}
}

func TestMapToSymbols_CommentAndWhitespaceChanges(t *testing.T) {
	const code = confidenceExactBody
	tests := []struct {
		name       string
		diff       string
		confidence float64
		trivial    bool
	}{
		{"comment added in a body", "@@ -8,2 +8,3 @@\n \ty := x + 1\n+\t// explain\n \tfmt.Println(y)\n", confidenceTrivial, true},
		{"comment reworded", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\t// was\n+\t// is\n \tfmt.Println(y)\n", confidenceTrivial, true},
		{"line reindented", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\tfmt.Println(y)\n+\t\tfmt.Println( y )\n \treturn y\n", confidenceTrivial, true},
		{"blank line removed", "@@ -8,3 +8,2 @@\n \ty := x + 1\n-\n \tfmt.Println(y)\n", confidenceTrivial, true},
		{"comment and code", "@@ -8,3 +8,4 @@\n \ty := x + 1\n+\t// explain\n-\tfmt.Println(y)\n+\tfmt.Print(y)\n \treturn y\n", code, false},
		{"comment replaced by code", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\t// was\n+\tz := 1\n \tfmt.Println(y)\n", code, false},
		{"trailing comment counts as code", "@@ -8,3 +8,3 @@\n \ty := x + 1\n-\tfmt.Println(y)\n+\tfmt.Println(y) // why\n \treturn y\n", code, false},
		{"compiler directive counts as code", "@@ -8,2 +8,3 @@\n \ty := x + 1\n+\t//go:noinline\n \tfmt.Println(y)\n", code, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapDoc(t, goDoc(), fileDiff("lib/lib.go", tt.diff))
			if names(got) != "Target" {
				t.Fatalf("changed symbols = %q, want Target", names(got))
			}
			if got[0].Confidence != tt.confidence || got[0].Trivial != tt.trivial {
				t.Errorf("confidence %v trivial %v, want %v %v", got[0].Confidence, got[0].Trivial, tt.confidence, tt.trivial)
			}
		})
	}
}

// Only comments outside any definition: reported at file level, and marked so.
func TestMapToSymbols_CommentOutsideDefinitions(t *testing.T) {
	got := mapDoc(t, goDoc(), fileDiff("lib/lib.go", "@@ -12,2 +12,3 @@\n \n+// Table is a table.\n var Table = map[string]int{\n"))
	if len(got) != 1 || got[0].SymbolID != "file:lib/lib.go" || got[0].DeletionUnresolved {
		t.Fatalf("got %+v, want one plain file-level entry", got)
	}
	if !strings.Contains(got[0].Note, "comments") {
		t.Errorf("note = %q", got[0].Note)
	}
}

func TestIsTrivialBlock_IndentationLanguages(t *testing.T) {
	block := impact.ChangeBlock{Removed: []string{"    return x"}, Added: []string{"return x"}}
	if isTrivialBlock("a.py", block) {
		t.Error("reindenting Python changes behavior")
	}
	if !isTrivialBlock("a.go", block) {
		t.Error("reindenting Go doesn't")
	}
	rustAttr := impact.ChangeBlock{Added: []string{"#[derive(Debug)]"}}
	if isTrivialBlock("a.rs", rustAttr) {
		t.Error("# isn't a comment in Rust")
	}
	if !isTrivialBlock("a.py", impact.ChangeBlock{Added: []string{"# note"}}) {
		t.Error("# is a comment in Python")
	}
}

// A definition moved to another file of the same Go package is still there for
// every caller: the package is the scope, not the file. Moved anywhere else, or
// into the package's external tests, it is gone.
func TestMapToSymbols_GoDefinitionMovedWithinPackage(t *testing.T) {
	removeTarget := fileDiff("lib/lib.go", "@@ -6,7 +6,1 @@\n \n-func Target(x int) int {\n-\ty := x + 1\n-\tfmt.Println(y)\n-\treturn y\n-}\n-\n func After() {}\n")
	newFile := func(path string, lines ...string) string {
		var b strings.Builder
		b.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + path + "\n")
		b.WriteString("@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
		return b.String()
	}
	body := []string{"package lib", "", "import \"fmt\"", "", "func Target(x int) int {", "\ty := x + 1", "\tfmt.Println(y)", "\treturn y", "}"}
	changedSig := append([]string{}, body...)
	changedSig[4] = "func Target(x int, label string) int {"

	cases := []struct {
		name       string
		other      string
		wantType   impact.ChangeType
		wantChange impact.DeclarationChange
	}{
		{"same package", newFile("lib/target.go", body...), impact.ChangeModified, impact.DeclUnchanged},
		{"same package, signature changed", newFile("lib/target.go", changedSig...), impact.ChangeModified, impact.DeclChanged},
		{"another package", newFile("other/target.go", body...), impact.ChangeDeleted, impact.DeclRemoved},
		{"into the tests", newFile("lib/target_test.go", body...), impact.ChangeDeleted, impact.DeclRemoved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapDoc(t, goDoc(), "diff --git a/lib/lib.go b/lib/lib.go\n"+removeTarget+c.other)
			var target *impact.ChangedSymbol
			for i := range got {
				if got[i].Name == "Target" {
					target = &got[i]
				}
			}
			if target == nil {
				t.Fatalf("Target not reported: %s", names(got))
			}
			if target.ChangeType != c.wantType || target.Declaration != c.wantChange {
				t.Errorf("got %s/%q (%q -> %q), want %s/%q", target.ChangeType, target.Declaration,
					target.DeclarationBefore, target.DeclarationAfter, c.wantType, c.wantChange)
			}
		})
	}
}

// Between two test files of one directory, a move is only within the package if
// both say the same package: lib_test is another one.
func TestMapToSymbols_GoMoveBetweenTestFiles(t *testing.T) {
	doc := goDoc()
	doc.RelativePath = "lib/lib_test.go"
	oldFile := []string{"package lib", "", "import \"fmt\"", "", "const Limit = 10", "", "func Target(x int) int {",
		"\ty := x + 1", "\tfmt.Println(y)", "\treturn y", "}", "", "var Table = map[string]int{", "\t\"a\": 1,", "}",
		"", "type Point struct {", "\tX int", "}", "", "func After() {}"}
	newFile := append(append([]string{}, oldFile[:6]...), oldFile[12:]...)
	remove := "diff --git a/lib/lib_test.go b/lib/lib_test.go\n--- a/lib/lib_test.go\n+++ b/lib/lib_test.go\n" +
		"@@ -6,8 +6,2 @@\n \n-func Target(x int) int {\n-\ty := x + 1\n-\tfmt.Println(y)\n-\treturn y\n-}\n-\n var Table = map[string]int{\n"
	for _, c := range []struct {
		pkg  string
		want impact.DeclarationChange
	}{{"lib", impact.DeclUnchanged}, {"lib_test", impact.DeclRemoved}} {
		t.Run(c.pkg, func(t *testing.T) {
			target := []string{"package " + c.pkg, "", "func Target(x int) int {", "\treturn x + 1", "}"}
			add := "diff --git a/lib/target_test.go b/lib/target_test.go\nnew file mode 100644\n--- /dev/null\n+++ b/lib/target_test.go\n@@ -0,0 +1,5 @@\n"
			for _, l := range target {
				add += "+" + l + "\n"
			}
			reader := func(path string, old bool) []string {
				switch {
				case path == "lib/lib_test.go" && old:
					return oldFile
				case path == "lib/lib_test.go":
					return newFile
				case path == "lib/target_test.go" && !old:
					return target
				}
				return nil
			}
			for _, sym := range mapDocWith(t, doc, remove+add, reader) {
				if sym.Name == "Target" && sym.Declaration != c.want {
					t.Errorf("got %s/%q, want %q", sym.ChangeType, sym.Declaration, c.want)
				}
			}
		})
	}
}

// renameDoc is lib/rules.go, a type with a method and a function taking it:
//
//	1 package lib
//	2
//	3 type rules struct{}
//	4
//	5 func (r *rules) line(id string) string {
//	6 	return id
//	7 }
//	8
//	9 func parse(r *rules) int { return 0 }
func renameDoc() *scip.Document {
	sym := func(s string) string { return goPkg + s }
	return &scip.Document{
		RelativePath: "lib/rules.go",
		Occurrences: []*scip.Occurrence{
			def(sym("rules#"), 2),
			def(sym("rules#line()."), 4, 4, 0, 6, 1),
			ref(sym("rules#"), 4),
			def(sym("parse()."), 8, 8, 0, 8, 37),
			ref(sym("rules#"), 8),
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: sym("rules#"), DisplayName: "rules", Kind: 49},
			{Symbol: sym("rules#line()."), DisplayName: "line", Kind: 26},
			{Symbol: sym("parse()."), DisplayName: "parse", Kind: 17},
		},
	}
}

// A type renamed, here exported and moved to another package: its methods and the
// signatures naming it follow along, and only the type itself is removed. Whoever
// still names the old type is that removal's open site.
func TestMapToSymbols_GoTypeRenameCarriesItsMethods(t *testing.T) {
	header := "diff --git a/lib/rules.go b/lib/rules.go\n--- a/lib/rules.go\n+++ b/lib/rules.go\n"
	cases := []struct {
		name, hunk string
		want       map[string]impact.DeclarationChange
	}{
		{"renamed in place",
			"@@ -1,9 +1,9 @@\n package lib\n \n-type rules struct{}\n+type Rules struct{}\n \n-func (r *rules) line(id string) string {\n+func (r *Rules) line(id string) string {\n \treturn id\n }\n \n-func parse(r *rules) int { return 0 }\n+func parse(r *Rules) int { return 0 }\n",
			map[string]impact.DeclarationChange{"rules": impact.DeclRemoved, "line": impact.DeclUnchanged, "parse": impact.DeclUnchanged}},
		{"qualified from another package",
			"@@ -1,9 +1,9 @@\n package lib\n \n-type rules struct{}\n+type Rules struct{}\n \n-func (r *rules) line(id string) string {\n+func (r *Rules) line(id string) string {\n \treturn id\n }\n \n-func parse(r *rules) int { return 0 }\n+func parse(r *other.Rules) int { return 0 }\n",
			map[string]impact.DeclarationChange{"line": impact.DeclUnchanged, "parse": impact.DeclUnchanged}},
		{"method changed on the way",
			"@@ -1,9 +1,9 @@\n package lib\n \n-type rules struct{}\n+type Rules struct{}\n \n-func (r *rules) line(id string) string {\n+func (r *Rules) line(id int) string {\n \treturn id\n }\n \n-func parse(r *rules) int { return 0 }\n+func parse(r *Rules, n int) int { return 0 }\n",
			map[string]impact.DeclarationChange{"line": impact.DeclChanged, "parse": impact.DeclChanged}},
		{"type kept, method's receiver moved to a new type",
			"@@ -1,9 +1,10 @@\n package lib\n \n type rules struct{}\n+type other struct{}\n \n-func (r *rules) line(id string) string {\n+func (r *other) line(id string) string {\n \treturn id\n }\n \n func parse(r *rules) int { return 0 }\n",
			map[string]impact.DeclarationChange{"line": impact.DeclRemoved}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := make(map[string]impact.DeclarationChange)
			for _, s := range mapDoc(t, renameDoc(), header+c.hunk) {
				got[s.Name] = s.Declaration
			}
			for name, want := range c.want {
				if got[name] != want {
					t.Errorf("%s: got %q, want %q (all: %v)", name, got[name], want, got)
				}
			}
		})
	}
}
