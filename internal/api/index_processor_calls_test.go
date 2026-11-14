package api

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// rust-analyzer states SCIP Kinds (Function 17, EnumMember 12) and gives
// function IDs without "()". A call belongs to the innermost function whose
// EnclosingRange holds it; a call after every function has no caller.
func TestDocCalls_RustKindsAndEnclosingRange(t *testing.T) {
	pkg := "rust-analyzer cargo demo 0.1.0 "
	outer, inner, run, off := pkg+"outer.", pkg+"outer/inner.", pkg+"run.", pkg+"Mode#Off."
	doc := &scip.Document{
		RelativePath: "src/lib.rs",
		Occurrences: []*scip.Occurrence{
			{Symbol: outer, Range: []int32{0, 3, 8}, EnclosingRange: []int32{0, 0, 9, 1}, SymbolRoles: scip.SymbolRoleDefinition},
			{Symbol: inner, Range: []int32{2, 7, 12}, EnclosingRange: []int32{2, 4, 4, 5}, SymbolRoles: scip.SymbolRoleDefinition},
		},
		Symbols: []*scip.SymbolInformation{
			{Symbol: outer, Kind: 17},
			{Symbol: inner, Kind: 17},
		},
	}
	info := map[string]*scip.SymbolInformation{
		outer: {Kind: 17}, inner: {Kind: 17}, run: {Kind: 17}, off: {Kind: 12},
	}
	calls := newDocCalls(doc)

	if !calls.isCallee(run, info) {
		t.Error("run (Kind Function) is not a callee")
	}
	if calls.isCallee(off, info) {
		t.Error("Mode::Off (Kind EnumMember, LSP's Function) counted as a callee")
	}
	for _, tc := range []struct {
		line int // 1-based
		want string
	}{
		{2, outer}, {4, inner}, {8, outer}, {30, ""},
	} {
		if got := calls.callerAt(tc.line); got != tc.want {
			t.Errorf("callerAt(%d) = %q, want %q", tc.line, got, tc.want)
		}
	}
}
