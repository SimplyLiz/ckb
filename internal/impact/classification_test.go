package impact

import (
	"testing"
)

func TestClassifyImpact(t *testing.T) {
	tests := []struct {
		name     string
		ref      *Reference
		symbol   *Symbol
		expected ImpactKind
	}{
		{
			name: "direct call",
			ref: &Reference{
				Kind:   RefCall,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindFunction},
			expected: DirectCaller,
		},
		{
			name: "test dependency",
			ref: &Reference{
				Kind:   RefCall,
				IsTest: true,
			},
			symbol:   &Symbol{Kind: KindFunction},
			expected: TestDependency,
		},
		{
			name: "implements interface",
			ref: &Reference{
				Kind:   RefImplements,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindInterface},
			expected: ImplementsInterface,
		},
		{
			name: "extends class",
			ref: &Reference{
				Kind:   RefExtends,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindClass},
			expected: DirectCaller,
		},
		{
			name: "type reference",
			ref: &Reference{
				Kind:   RefType,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindType},
			expected: TypeDependency,
		},
		{
			name: "property read",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindProperty},
			expected: DirectCaller,
		},
		{
			name: "property write",
			ref: &Reference{
				Kind:   RefWrite,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindProperty},
			expected: DirectCaller,
		},
		{
			name: "variable read",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindVariable},
			expected: DirectCaller,
		},
		{
			name: "read of a function is a caller: SCIP has no call role",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindFunction},
			expected: DirectCaller,
		},
		{
			name: "read of a method is a caller",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindMethod},
			expected: DirectCaller,
		},
		{
			name: "read of a type is a type dependency",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:   &Symbol{Kind: KindClass},
			expected: TypeDependency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyImpact(tt.ref, tt.symbol)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestClassifyImpactWithConfidence(t *testing.T) {
	tests := []struct {
		name          string
		ref           *Reference
		symbol        *Symbol
		expectedKind  ImpactKind
		minConfidence float64
		maxConfidence float64
	}{
		{
			name: "direct call - high confidence",
			ref: &Reference{
				Kind:   RefCall,
				IsTest: false,
			},
			symbol:        &Symbol{Kind: KindFunction},
			expectedKind:  DirectCaller,
			minConfidence: 0.9,
			maxConfidence: 1.0,
		},
		{
			name: "property access - slightly lower confidence",
			ref: &Reference{
				Kind:   RefRead,
				IsTest: false,
			},
			symbol:        &Symbol{Kind: KindProperty},
			expectedKind:  DirectCaller,
			minConfidence: 0.85,
			maxConfidence: 0.95,
		},
		{
			name: "type dependency - medium confidence",
			ref: &Reference{
				Kind:   RefType,
				IsTest: false,
			},
			symbol:        &Symbol{Kind: KindType},
			expectedKind:  TypeDependency,
			minConfidence: 0.75,
			maxConfidence: 0.85,
		},
		{
			name: "test dependency - high confidence",
			ref: &Reference{
				Kind:   RefCall,
				IsTest: true,
			},
			symbol:        &Symbol{Kind: KindFunction},
			expectedKind:  TestDependency,
			minConfidence: 0.85,
			maxConfidence: 0.95,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, confidence := ClassifyImpactWithConfidence(tt.ref, tt.symbol)

			if kind != tt.expectedKind {
				t.Errorf("expected kind %s, got %s", tt.expectedKind, kind)
			}

			if confidence < tt.minConfidence || confidence > tt.maxConfidence {
				t.Errorf("confidence %f out of expected range [%f, %f]", confidence, tt.minConfidence, tt.maxConfidence)
			}
		})
	}
}

func TestIsBreakingChange(t *testing.T) {
	tests := []struct {
		name       string
		ref        *Reference
		symbol     *Symbol
		changeType string
		expected   bool
	}{
		{
			name: "signature change affects call",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "signature-change",
			expected:   true,
		},
		{
			name: "signature change affects type dependency",
			ref: &Reference{
				Kind:       RefType,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "signature-change",
			expected:   true,
		},
		{
			name: "signature change doesn't affect property read",
			ref: &Reference{
				Kind:       RefRead,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "signature-change",
			expected:   false,
		},
		{
			name: "rename affects all",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "rename",
			expected:   true,
		},
		{
			name: "removal affects all",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "remove",
			expected:   true,
		},
		{
			name: "visibility change affects external",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "visibility-change",
			expected:   true,
		},
		{
			name: "visibility change doesn't affect internal",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module1",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "visibility-change",
			expected:   false,
		},
		{
			name: "behavioral change affects caller",
			ref: &Reference{
				Kind:       RefCall,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "behavioral-change",
			expected:   true,
		},
		{
			name: "behavioral change doesn't affect type dependency",
			ref: &Reference{
				Kind:       RefType,
				FromModule: "module2",
			},
			symbol: &Symbol{
				ModuleId: "module1",
			},
			changeType: "behavioral-change",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBreakingChange(tt.ref, tt.symbol, tt.changeType)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
