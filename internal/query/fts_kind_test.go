package query

import (
	"testing"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

func TestInferKindString_UsesSCIPEnum(t *testing.T) {
	tests := []struct {
		kind bindingscip.SymbolInformation_Kind
		want string
	}{
		{bindingscip.SymbolInformation_Class, "class"},
		{bindingscip.SymbolInformation_Interface, "interface"},
		{bindingscip.SymbolInformation_Enum, "enum"},
		{bindingscip.SymbolInformation_Function, "function"},
		{bindingscip.SymbolInformation_Method, "method"},
		{bindingscip.SymbolInformation_Variable, "variable"},
		{bindingscip.SymbolInformation_Namespace, "namespace"},
	}
	for _, tt := range tests {
		if got := inferKindString(int32(tt.kind), nil); got != tt.want {
			t.Errorf("inferKindString(%s) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestInferKindString_UnsetKindFallsBackToDescriptor(t *testing.T) {
	id, err := scip.ParseSCIPIdentifier("scip-go go demo 1.0.0 pkg/Engine#")
	if err != nil {
		t.Fatal(err)
	}
	if got := inferKindString(0, id); got != "class" {
		t.Errorf("inferKindString(unset) = %q, want class from the # descriptor", got)
	}
}
