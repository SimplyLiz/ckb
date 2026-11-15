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
