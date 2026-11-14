package scip

import "testing"

func TestFindReferences_TypeIncludesUnnamedConstructorCalls(t *testing.T) {
	typeID := "scip-dart pub demo 1.0.0 lib/`a.dart`/Parser#"
	ctorID := typeID + "`<constructor>`()."
	overloadID := typeID + "`<init>`(+1)."
	plainInit := typeID + "`<init>`()."
	occ := func(sym string, line, col int32, roles int32) *OccurrenceRef {
		return &OccurrenceRef{
			Doc: &Document{RelativePath: "lib/a.dart"},
			Occ: &Occurrence{Range: []int32{line, col, col + 6}, Symbol: sym, SymbolRoles: roles},
		}
	}
	idx := &SCIPIndex{RefIndex: map[string][]*OccurrenceRef{
		typeID:     {occ(typeID, 1, 6, SymbolRoleDefinition), occ(typeID, 4, 2, 0)},
		ctorID:     {occ(ctorID, 2, 2, SymbolRoleDefinition), occ(ctorID, 7, 8, 0), occ(ctorID, 4, 2, 0)},
		plainInit:  {occ(plainInit, 9, 1, 0)},
		overloadID: {occ(overloadID, 11, 1, 0)},
	}}

	refs, err := idx.FindReferences(typeID, ReferenceOptions{IncludeDefinition: true})
	if err != nil {
		t.Fatal(err)
	}
	lines := map[int]bool{}
	for _, r := range refs {
		if r.SymbolId != typeID {
			t.Errorf("reference at line %d reported as %s", r.Location.StartLine, r.SymbolId)
		}
		lines[r.Location.StartLine] = true
	}
	for _, want := range []int{1, 4, 7, 9, 11} {
		if !lines[want] {
			t.Errorf("line %d missing from %v", want, lines)
		}
	}
	if lines[2] {
		t.Errorf("the constructor's own definition is not a usage: %v", lines)
	}
	if len(refs) != 5 {
		t.Errorf("got %d references, want 5: the shared position counts once", len(refs))
	}

	capped := ReferenceOptions{IncludeDefinition: true, MaxResults: 2}
	if refs, _ := idx.FindReferences(typeID, capped); len(refs) != 2 {
		t.Errorf("capped FindReferences returned %d, want 2", len(refs))
	}
	if got := idx.CountReferences(typeID, capped); got != 5 {
		t.Errorf("CountReferences = %d, want 5 including constructor calls", got)
	}
}

func TestFindReferences_ConstructorLookupOnlyForTypes(t *testing.T) {
	methodID := "scip-dart pub demo 1.0.0 lib/`a.dart`/Parser#parse()."
	idx := &SCIPIndex{RefIndex: map[string][]*OccurrenceRef{
		methodID + "`<constructor>`().": {{
			Doc: &Document{RelativePath: "lib/a.dart"},
			Occ: &Occurrence{Range: []int32{7, 8, 14}, Symbol: "x"},
		}},
	}}
	refs, err := idx.FindReferences(methodID, ReferenceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("got %d references for a method, want none", len(refs))
	}
}

func TestContainerEntries_InfersFunctionsWithoutEnclosingRange(t *testing.T) {
	pkg := "scip-dart pub demo 1.0.0 lib/`a.dart`/"
	first, second := pkg+"first().", pkg+"second()."
	doc := &Document{
		RelativePath: "lib/a.dart",
		Symbols:      []*SymbolInformation{{Symbol: first}, {Symbol: second}},
		Occurrences: []*Occurrence{
			{Range: []int32{0, 5, 10}, Symbol: first, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{2, 4, 10}, Symbol: pkg + "callee()."},
			{Range: []int32{5, 5, 11}, Symbol: second, SymbolRoles: SymbolRoleDefinition},
			{Range: []int32{6, 4, 10}, Symbol: pkg + "callee()."},
		},
	}
	entries := containerEntries(doc)
	if got := entries["lib/a.dart:2:4"]; got != first {
		t.Errorf("container at line 2 = %q, want first()", got)
	}
	if got := entries["lib/a.dart:6:4"]; got != second {
		t.Errorf("container at line 6 = %q, want second()", got)
	}
}
