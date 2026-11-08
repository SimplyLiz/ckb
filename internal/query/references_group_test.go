package query

import (
	"reflect"
	"testing"
)

func TestGroupReferencesByFile(t *testing.T) {
	at := func(file string, line int) *LocationInfo { return &LocationInfo{FileId: file, StartLine: line} }
	refs := []ReferenceInfo{
		{Location: at("b.dart", 9), Kind: "reference"},
		{Location: at("a_test.dart", 4), Kind: "reference", IsTest: true},
		{Location: at("b.dart", 2), Kind: "reference"},
		{Location: at("b.dart", 9), Kind: "reference"},
		{Location: at("b.dart", 1), Kind: "definition"},
		{Location: nil, Kind: "reference"},
	}
	want := []FileReferences{
		{Path: "b.dart", Lines: []int{2, 9}, DefinitionLines: []int{1}},
		{Path: "a_test.dart", Lines: []int{4}, IsTest: true},
	}
	if got := GroupReferencesByFile(refs); !reflect.DeepEqual(got, want) {
		t.Errorf("GroupReferencesByFile = %+v, want %+v", got, want)
	}
}
