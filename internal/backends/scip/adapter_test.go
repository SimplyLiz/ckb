package scip

import "testing"

// TestConvertToReference_ResolvesFromSymbol is a regression test for a bug
// where ckb impact prepare reported every directDependents entry with
// symbolId: "" and name: "unknown", even though only file/line were set.
//
// Root cause: SCIPReference.FromSymbol (the enclosing/caller symbol, already
// resolved by processOccurrence via findContainingSymbolFast in
// references.go) was dropped when converting to backends.Reference —
// convertToReference only copied Location/Kind/SymbolID/Context, never
// FromSymbol. The caller-resolution data existed; it just wasn't plumbed
// through the backend boundary.
func TestConvertToReference_ResolvesFromSymbol(t *testing.T) {
	adapter := &SCIPAdapter{}

	scipRef := &SCIPReference{
		SymbolId: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Target().",
		Location: &Location{FileId: "internal/query/engine.go", StartLine: 42},
		Kind:     RefReference,
		FromSymbol: "scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 " +
			"`github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildProvenance().",
	}

	ref := adapter.convertToReference(scipRef)

	if ref.FromSymbol != scipRef.FromSymbol {
		t.Errorf("FromSymbol = %q, want %q", ref.FromSymbol, scipRef.FromSymbol)
	}
	if ref.FromSymbolName != "buildProvenance" {
		t.Errorf("FromSymbolName = %q, want %q", ref.FromSymbolName, "buildProvenance")
	}
	// File/line should still be set as before.
	if ref.Location.Path != "internal/query/engine.go" {
		t.Errorf("Location.Path = %q, want %q", ref.Location.Path, "internal/query/engine.go")
	}
}

// TestConvertToReference_ModuleLevelEnclosingSymbol is a regression test
// for a bug found while validating the FromSymbol fix above: some scip-ts
// "enclosing symbols" are module/namespace-level (descriptor ends in a
// bare "/" with nothing after it, e.g. a whole-file scope), and
// GetSimpleName legitimately can't derive a short name for those. Without
// this guard, convertToReference still set FromSymbol to the raw SCIP ID,
// and downstream (internal/impact's extractNameFromStableId fallback)
// echoed that whole raw ID — spaces, backticks and all — as the "name" in
// ckb impact prepare's directDependents, which is worse than the
// "unknown" placeholder it replaced. FromSymbol/FromSymbolName should
// both stay empty in this case, same as when there's no enclosing symbol
// at all.
func TestConvertToReference_ModuleLevelEnclosingSymbol(t *testing.T) {
	adapter := &SCIPAdapter{}

	scipRef := &SCIPReference{
		SymbolId:   "scip-typescript npm fixture 1.0.0 src/internal/`util.ts`/FormatOutput().",
		Location:   &Location{FileId: "src/main.ts", StartLine: 1},
		Kind:       RefReference,
		FromSymbol: "scip-typescript npm fixture 1.0.0 src/`main.ts`/",
	}

	ref := adapter.convertToReference(scipRef)

	if ref.FromSymbol != "" {
		t.Errorf("FromSymbol = %q, want empty (module-level symbol has no short name)", ref.FromSymbol)
	}
	if ref.FromSymbolName != "" {
		t.Errorf("FromSymbolName = %q, want empty", ref.FromSymbolName)
	}
}

// TestConvertToReference_NoEnclosingSymbol verifies that when the backend
// genuinely can't resolve an enclosing symbol (e.g. a package-level
// reference outside any function), FromSymbol/FromSymbolName stay empty
// rather than being synthesized — callers are expected to omit these
// fields, not display a placeholder.
func TestConvertToReference_NoEnclosingSymbol(t *testing.T) {
	adapter := &SCIPAdapter{}

	scipRef := &SCIPReference{
		SymbolId:   "scip-go gomod mod v0 `pkg`/Target().",
		Location:   &Location{FileId: "pkg/file.go", StartLine: 1},
		Kind:       RefReference,
		FromSymbol: "",
	}

	ref := adapter.convertToReference(scipRef)

	if ref.FromSymbol != "" {
		t.Errorf("FromSymbol = %q, want empty", ref.FromSymbol)
	}
	if ref.FromSymbolName != "" {
		t.Errorf("FromSymbolName = %q, want empty", ref.FromSymbolName)
	}
}
