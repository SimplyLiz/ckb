package impact

// ImpactKind represents the type of impact a reference has
type ImpactKind string

const (
	DirectCaller        ImpactKind = "direct-caller"
	TransitiveCaller    ImpactKind = "transitive-caller"
	DirectCallee        ImpactKind = "direct-callee"     // forward direction: what this symbol calls
	TransitiveCallee    ImpactKind = "transitive-callee" // forward direction, distance >= 2
	TypeDependency      ImpactKind = "type-dependency"
	TestDependency      ImpactKind = "test-dependency"
	ImplementsInterface ImpactKind = "implements-interface"
	Unknown             ImpactKind = "unknown"
)

// ImpactItem represents a single item impacted by a symbol change
type ImpactItem struct {
	StableId   string          // Stable identifier of the impacted symbol
	Name       string          // Name of the impacted symbol
	Kind       ImpactKind      // Kind of impact
	Confidence float64         // Confidence score (0.0 - 1.0)
	ModuleId   string          // Module identifier
	ModuleName string          // Module name
	Location   *Location       // Location of the impact
	Visibility *VisibilityInfo // Visibility of the impacted symbol
	Distance   int             // Distance from original symbol (1 = direct, 2+ = transitive)
}

// ClassifyImpact determines the impact kind based on reference type and context
func ClassifyImpact(ref *Reference, symbol *Symbol) ImpactKind {
	// Test dependencies take precedence
	if ref.IsTest {
		return TestDependency
	}

	// Classify based on reference kind
	switch ref.Kind {
	case RefCall:
		// Direct function/method call
		return DirectCaller

	case RefImplements:
		// Interface implementation
		return ImplementsInterface

	case RefExtends:
		// Class extension - treated as direct caller for impact purposes
		return DirectCaller

	case RefType:
		// Type reference - could be parameter, return type, field type, etc.
		return TypeDependency

	case RefRead, RefWrite:
		// SCIP has no "call" role: a call to a function is a read occurrence, and
		// so is passing it as a value. Both break when its contract changes.
		if symbol.Kind == KindFunction || symbol.Kind == KindMethod {
			return DirectCaller
		}
		// Property or variable access
		if symbol.Kind == KindProperty || symbol.Kind == KindVariable || symbol.Kind == KindConstant {
			return DirectCaller
		}
		return TypeDependency

	default:
		return Unknown
	}
}

// ClassifyImpactWithConfidence determines impact kind with confidence score
func ClassifyImpactWithConfidence(ref *Reference, symbol *Symbol) (ImpactKind, float64) {
	kind := ClassifyImpact(ref, symbol)

	// Assign confidence based on reference kind and context
	var confidence float64
	switch kind {
	case DirectCaller:
		// High confidence for direct calls
		confidence = 0.95
		if ref.Kind == RefRead || ref.Kind == RefWrite {
			// Slightly lower for property access
			confidence = 0.9
		}

	case TransitiveCaller:
		// Medium-high confidence for transitive calls
		confidence = 0.85

	case TypeDependency:
		// Medium confidence for type dependencies
		confidence = 0.8

	case TestDependency:
		// High confidence for test dependencies
		confidence = 0.9

	case ImplementsInterface:
		// High confidence for interface implementations
		confidence = 0.95

	default:
		// Low confidence for unknown
		confidence = 0.5
	}

	return kind, confidence
}

// IsBreakingChange determines if a change to the symbol would break the reference
func IsBreakingChange(ref *Reference, symbol *Symbol, changeType string) bool {
	switch changeType {
	case "signature-change":
		// Signature changes affect calls and type dependencies
		return ref.Kind == RefCall || ref.Kind == RefType

	case "rename":
		// Rename affects all references
		return true

	case "remove":
		// Removal affects all references
		return true

	case "visibility-change":
		// Visibility changes only affect external references
		// Internal changes don't break internal references
		return ref.FromModule != symbol.ModuleId

	case "behavioral-change":
		// Behavioral changes affect callers but not type dependencies
		return ref.Kind == RefCall || ref.Kind == RefRead || ref.Kind == RefWrite

	default:
		// Conservative: assume breaking for unknown change types
		return true
	}
}
