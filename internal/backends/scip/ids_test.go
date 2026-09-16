package scip

import "testing"

// TestGetSimpleName_GoMethodDescriptor is a regression test for a bug where
// ckb impact prepare reported unexported Go methods (e.g. Engine#buildProvenance)
// as "public". GetSimpleName only split on the last '/' in the descriptor, so
// for a method descriptor like `pkg`/Engine#buildProvenance(). it returned
// "Engine#buildProvenance" instead of "buildProvenance" — the leading,
// capitalized receiver type name leaked into the "simple name" that
// inferVisibility uses to test Go export-by-case, making every method on an
// exported type look exported regardless of the method's own case.
func TestGetSimpleName_GoMethodDescriptor(t *testing.T) {
	tests := []struct {
		name       string
		descriptor string
		want       string
	}{
		{
			name:       "unexported method on exported type",
			descriptor: "`github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildProvenance().",
			want:       "buildProvenance",
		},
		{
			name:       "exported method on exported type",
			descriptor: "`github.com/SimplyLiz/CodeMCP/internal/query`/Engine#BuildProvenance().",
			want:       "BuildProvenance",
		},
		{
			name:       "package-level function (no receiver)",
			descriptor: "`github.com/SimplyLiz/CodeMCP/internal/api`/NewServer().",
			want:       "NewServer",
		},
		{
			name:       "type itself",
			descriptor: "`github.com/SimplyLiz/CodeMCP/internal/api`/Server#",
			want:       "Server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := &SCIPIdentifier{Scheme: "scip-go", Descriptor: tt.descriptor}
			if got := id.GetSimpleName(); got != tt.want {
				t.Errorf("GetSimpleName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestInferVisibility_GoUnexportedMethod is a regression test for the same
// bug at the point it actually surfaces: inferVisibility used the mangled
// "Engine#buildProvenance" simple name, saw the uppercase 'E' from the
// receiver type, and reported the unexported method as "public".
func TestInferVisibility_GoUnexportedMethod(t *testing.T) {
	id, err := ParseSCIPIdentifier("scip-go gomod github.com/SimplyLiz/CodeMCP v0.0.0 `github.com/SimplyLiz/CodeMCP/internal/query`/Engine#buildProvenance().")
	if err != nil {
		t.Fatalf("ParseSCIPIdentifier failed: %v", err)
	}

	name := id.GetSimpleName()
	got := inferVisibility(&SymbolInformation{}, id, name)
	if got != "private" {
		t.Errorf("inferVisibility(%q) = %q, want %q", name, got, "private")
	}
}
