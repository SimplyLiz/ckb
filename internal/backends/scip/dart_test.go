package scip

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"
)

const dartPkg = "scip-dart pub app 1.0.0 lib/"

func kindOf(k bindingscip.SymbolInformation_Kind) int32 { return int32(k) }

// The shape of TasteHub's sync_service.dart and its generated part, as
// scip_dart writes them: no EnclosingRange, a declaration's annotations after
// its name for a method but before it for a top-level variable.
func TestOrderedBounds_DartTopLevelVariables(t *testing.T) {
	file := dartPkg + "`a.dart`/"
	remove, provider, svc, stateCls := file+"Store#remove().", file+"roundTimeoutProvider.", file+"SyncService#", file+"Store#"
	roundTimeout := svc + "roundTimeout."
	doc := &Document{
		RelativePath: "lib/a.dart",
		Symbols: []*SymbolInformation{
			{Symbol: stateCls, Kind: kindOf(bindingscip.SymbolInformation_Class)},
			{Symbol: remove, Kind: kindOf(bindingscip.SymbolInformation_Method)},
			{Symbol: provider, Kind: kindOf(bindingscip.SymbolInformation_Variable)},
			{Symbol: svc, Kind: kindOf(bindingscip.SymbolInformation_Class)},
		},
		Occurrences: []*Occurrence{
			{Range: []int32{80, 6, 11}, Symbol: stateCls, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{89, 15, 21}, Symbol: remove, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{88, 3, 11}, Symbol: "override"}, // annotation, after the name
			{Range: []int32{89, 30, 40}, Symbol: "prefs#remove()."},
			// line 91 is the closing brace: no occurrence
			{Range: []int32{95, 10, 21}, Symbol: svc}, // doc comment of the variable
			{Range: []int32{96, 6, 26}, Symbol: provider, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{96, 60, 71}, Symbol: svc},
			{Range: []int32{96, 72, 84}, Symbol: roundTimeout},
			{Range: []int32{99, 6, 17}, Symbol: svc, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{98, 1, 9}, Symbol: "Riverpod#"},
		},
	}
	funcs, holders := orderedBounds(doc)
	if len(funcs) != 1 || funcs[0].Symbol != remove || funcs[0].Start != 88 || funcs[0].End >= 95 {
		t.Fatalf("funcs = %+v, want remove() from its annotation (88) to before the variable", funcs)
	}
	if len(holders) != 1 || holders[0].Symbol != provider || holders[0].Start != 95 || holders[0].End != 97 {
		t.Fatalf("holders = %+v, want the provider from its doc comment (95) to before SyncService's annotation", holders)
	}

	callers := buildCallerIndex([]*Document{doc})
	if got := callers[svc]; !slices.Equal(got, []string{provider}) {
		t.Errorf("callers of SyncService = %v, want the provider, not remove()", got)
	}
}

// A generated part file opens with the annotation of its first declaration.
func TestOrderedBounds_AnnotationBeforeTheFirstDeclaration(t *testing.T) {
	file := dartPkg + "`a.g.dart`/"
	provider, svc := file+"syncServiceProvider.", dartPkg+"`a.dart`/SyncService#"
	doc := &Document{
		RelativePath: "lib/a.g.dart",
		Symbols:      []*SymbolInformation{{Symbol: provider, Kind: kindOf(bindingscip.SymbolInformation_Variable)}},
		Occurrences: []*Occurrence{
			{Range: []int32{11, 1, 12}, Symbol: "ProviderFor#"},
			{Range: []int32{11, 13, 24}, Symbol: svc},
			{Range: []int32{12, 6, 25}, Symbol: provider, SymbolRoles: SymbolRoleDefinition},
		},
	}
	if got := buildCallerIndex([]*Document{doc})[svc]; !slices.Equal(got, []string{provider}) {
		t.Errorf("callers of SyncService = %v, want the provider its annotation names", got)
	}
}

// Other indexers keep the line-based inference.
func TestOrderedBounds_OnlyDart(t *testing.T) {
	doc := &Document{RelativePath: "lib/a.py"}
	if usesOrderedBounds(doc) {
		t.Error("a Python document must not use the Dart rule")
	}
	dart := &Document{RelativePath: "lib/a.dart", Occurrences: []*Occurrence{{Range: []int32{0, 0, 1}, EnclosingRange: []int32{0, 0, 2, 1}}}}
	if usesOrderedBounds(dart) {
		t.Error("a Dart document with EnclosingRange must keep the indexer's ranges")
	}
}

// scip_dart's IDs for private declarations are locals; the source names them.
func TestNameDartPrivates(t *testing.T) {
	root := t.TempDir()
	src := "import 'x.dart';\n" + // 0
		"class _TileState extends State<Tile> {\n" + // 1
		"  String? _result;\n" + // 2
		"  @override\n" + // 3
		"  void dispose() {}\n" + // 4
		"  Future<void> _syncNow() async {\n" + // 5
		"    _result = 'x';\n" + // 6
		"  }\n" + // 7
		"}\n" + // 8
		"Future<void> _boot() async {}\n" // 9
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "tile.dart"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	file := dartPkg + "`tile.dart`/"
	k := kindOf
	doc := &Document{
		RelativePath: "lib/tile.dart",
		Symbols: []*SymbolInformation{
			{Symbol: file, Kind: 0},
			{Symbol: "local 1", Kind: k(bindingscip.SymbolInformation_Class)},
			{Symbol: "local 2", Kind: k(bindingscip.SymbolInformation_Field)},
			{Symbol: file + "_TileState#dispose().", Kind: k(bindingscip.SymbolInformation_Method)},
			{Symbol: "local 3", Kind: k(bindingscip.SymbolInformation_Method)},
			{Symbol: "local 4", Kind: k(bindingscip.SymbolInformation_Function)},
			{Symbol: "local 5"}, // a body variable: no Kind, stays local
		},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 0, 6}, Symbol: file, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 6, 16}, Symbol: "local 1", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{2, 10, 17}, Symbol: "local 2", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{4, 7, 14}, Symbol: file + "_TileState#dispose().", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{5, 15, 23}, Symbol: "local 3", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{6, 4, 11}, Symbol: "local 2"},
			{Range: []int32{6, 4, 5}, Symbol: "local 5", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{9, 13, 18}, Symbol: "local 4", SymbolRoles: SymbolRoleDefinition},
		},
	}
	nameDartPrivates(doc, root)

	want := []string{
		file,
		file + "_TileState#",
		file + "_TileState#_result.",
		file + "_TileState#dispose().",
		file + "_TileState#_syncNow().",
		file + "_TileState#_result.",
		"local 5",
		file + "_boot().",
	}
	for i, occ := range doc.Occurrences {
		if occ.Symbol != want[i] {
			t.Errorf("occurrence %d = %q, want %q", i, occ.Symbol, want[i])
		}
	}
	if doc.Symbols[4].Symbol != file+"_TileState#_syncNow()." || doc.Symbols[4].DisplayName != "_syncNow" {
		t.Errorf("symbol info = %+v, want the renamed method with its name", doc.Symbols[4])
	}
}

// A source that no longer matches the index leaves the local IDs alone.
func TestNameDartPrivates_StaleSource(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "a.dart"), []byte("// rewritten\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := dartPkg + "`a.dart`/"
	doc := &Document{
		RelativePath: "lib/a.dart",
		Symbols:      []*SymbolInformation{{Symbol: "local 1", Kind: kindOf(bindingscip.SymbolInformation_Function)}},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 0, 6}, Symbol: file, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 5, 10}, Symbol: "local 1", SymbolRoles: SymbolRoleDefinition},
		},
	}
	nameDartPrivates(doc, root)
	if doc.Occurrences[1].Symbol != "local 1" {
		t.Errorf("renamed to %q from a source that doesn't hold the name", doc.Occurrences[1].Symbol)
	}
}

func TestDartLeadStart(t *testing.T) {
	lines := []string{
		"final a = Provider(", // 0
		"  (ref) => X.y);",    // 1: the tail of a
		"/// Uses [Y].",       // 2
		"/// More text.",      // 3
		"@Riverpod(",          // 4
		"  keepAlive: true,",  // 5
		")",                   // 6
		"final b = 1;",        // 7
		"",                    // 8
		"/// [Z]",             // 9
		"final c = 2;",        // 10
		"final d = 3;",        // 11
	}
	for _, tc := range []struct{ def, want int }{
		{7, 2},   // doc comment and multi-line annotation, not a's tail
		{10, 9},  // after a blank line
		{11, 11}, // right below a declaration: nothing leads it
		{1, 1},
	} {
		if got := dartLeadStart(lines, tc.def); got != tc.want {
			t.Errorf("dartLeadStart(%d) = %d, want %d", tc.def, got, tc.want)
		}
	}
}

// With the source read, a variable's doc comment belongs to it even past a
// line without occurrences, and the tail of the variable before stays there.
func TestOrderedBounds_DartLeadsFromSource(t *testing.T) {
	file := dartPkg + "`a.dart`/"
	a, b, x, y := file+"a.", file+"b.", file+"X#", file+"Y#"
	doc := &Document{
		RelativePath: "lib/a.dart",
		Symbols: []*SymbolInformation{
			{Symbol: a, Kind: kindOf(bindingscip.SymbolInformation_Variable)},
			{Symbol: b, Kind: kindOf(bindingscip.SymbolInformation_Variable)},
		},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 6, 7}, Symbol: a, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 12, 13}, Symbol: x}, // a's initializer
			{Range: []int32{2, 10, 11}, Symbol: y}, // b's doc comment
			// line 3: more doc text, no occurrence
			{Range: []int32{4, 6, 7}, Symbol: b, SymbolRoles: SymbolRoleDefinition},
		},
		dartLeads: map[int]int{0: 0, 4: 2},
	}
	callers := buildCallerIndex([]*Document{doc})
	if got := callers[x]; !slices.Equal(got, []string{a}) {
		t.Errorf("callers of X = %v, want a, whose initializer uses it", got)
	}
	if got := callers[y]; !slices.Equal(got, []string{b}) {
		t.Errorf("callers of Y = %v, want b, whose doc comment names it", got)
	}
}

// scip_dart counts columns in UTF-16 code units.
func TestIdentifierAt_UTF16(t *testing.T) {
	lines := []string{"x = '½' + _unit;", "s = '😀' + _emoji;"}
	if got := identifierAt(lines, []int32{0, 10, 15}); got != "_unit" {
		t.Errorf("after ½: %q, want _unit", got)
	}
	if got := identifierAt(lines, []int32{1, 11, 17}); got != "_emoji" {
		t.Errorf("after an emoji (two code units): %q, want _emoji", got)
	}
	if got := identifierAt(lines, []int32{1, 6, 8}); got != "" {
		t.Errorf("a range inside a surrogate pair gave %q", got)
	}
}

// The shape of a freezed model: the library references the generated
// part's private function, the part the library's private class, each
// through a local ID of its own document.
func TestLinkDartParts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	lib := "part 'm.g.dart';\n" + // 0
		"class _M {}\n" + // 1
		"M fromJson(j) => _$MFromJson(j);\n" // 2
	part := "part of 'm.dart';\n" + // 0
		"_M _$MFromJson(j) => _M();\n" // 1
	for name, src := range map[string]string{"m.dart": lib, "m.g.dart": part} {
		if err := os.WriteFile(filepath.Join(root, "lib", name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mFile, gFile := dartPkg+"`m.dart`/", dartPkg+"`m.g.dart`/"
	k := kindOf
	libDoc := &Document{
		RelativePath: "lib/m.dart",
		Symbols: []*SymbolInformation{
			{Symbol: "local 0", Kind: k(bindingscip.SymbolInformation_Class)},
			{Symbol: mFile + "fromJson().", Kind: k(bindingscip.SymbolInformation_Function)},
		},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 0, 0}, Symbol: mFile, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 6, 8}, Symbol: "local 0", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{2, 2, 10}, Symbol: mFile + "fromJson().", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{2, 17, 28}, Symbol: "local 1"},
		},
	}
	partDoc := &Document{
		RelativePath: "lib/m.g.dart",
		Symbols:      []*SymbolInformation{{Symbol: "local 0", Kind: k(bindingscip.SymbolInformation_Function)}},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 0, 0}, Symbol: gFile, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 3, 14}, Symbol: "local 0", SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{1, 0, 2}, Symbol: "local 1"},
			{Range: []int32{1, 21, 23}, Symbol: "local 1"},
		},
	}
	files := []*dartFile{nameDartPrivates(libDoc, root), nameDartPrivates(partDoc, root)}
	changed := linkDartParts(files)
	if len(changed) != 2 {
		t.Errorf("changed %d documents, want both", len(changed))
	}
	if got := libDoc.Occurrences[3].Symbol; got != gFile+"_$MFromJson()." {
		t.Errorf("library's reference = %q, want the part's function", got)
	}
	for _, i := range []int{2, 3} {
		if got := partDoc.Occurrences[i].Symbol; got != mFile+"_M#" {
			t.Errorf("part's reference %d = %q, want the library's class", i, got)
		}
	}
}

// A name two classes of the library declare stays local: the reference
// could mean either.
func TestLinkDartParts_AmbiguousName(t *testing.T) {
	doc := &Document{RelativePath: "lib/a.dart", Occurrences: []*Occurrence{{Symbol: "local 3"}}}
	files := []*dartFile{
		{doc: &Document{RelativePath: "lib/b.dart"}, parts: []string{"lib/a.dart"}, decls: map[string]string{"_x": ""}},
		{doc: doc, partOf: "lib/b.dart", refs: map[string]string{"local 3": "_x"}},
	}
	if changed := linkDartParts(files); len(changed) != 0 || doc.Occurrences[0].Symbol != "local 3" {
		t.Errorf("an ambiguous name was linked: %q", doc.Occurrences[0].Symbol)
	}
}
