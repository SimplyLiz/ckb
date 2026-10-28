package impact

// SymbolKind represents the type of symbol
type SymbolKind string

const (
	KindClass     SymbolKind = "class"
	KindInterface SymbolKind = "interface"
	KindFunction  SymbolKind = "function"
	KindMethod    SymbolKind = "method"
	KindProperty  SymbolKind = "property"
	KindVariable  SymbolKind = "variable"
	KindConstant  SymbolKind = "constant"
	KindType      SymbolKind = "type"
)

// Symbol represents a code symbol with metadata
type Symbol struct {
	StableId            string     // Unique identifier for the symbol
	Name                string     // Symbol name
	Kind                SymbolKind // Symbol kind (class, function, etc.)
	Signature           string     // Full signature
	SignatureNormalized string     // Normalized signature for comparison
	ModuleId            string     // Module identifier
	ModuleName          string     // Module name
	ContainerName       string     // Container name (class, namespace, etc.)
	Location            *Location  // Location in source code
	Modifiers           []string   // Modifiers from SCIP (public, private, static, etc.)
}

// Location represents a position in source code
type Location struct {
	FileId      string // File identifier
	StartLine   int    // Starting line number (1-indexed)
	StartColumn int    // Starting column number (1-indexed)
	EndLine     int    // Ending line number (1-indexed)
	EndColumn   int    // Ending column number (1-indexed)
}

// ReferenceKind represents the type of reference
type ReferenceKind string

const (
	RefCall       ReferenceKind = "call"       // Function/method call
	RefRead       ReferenceKind = "read"       // Read access
	RefWrite      ReferenceKind = "write"      // Write access
	RefType       ReferenceKind = "type"       // Type reference
	RefImplements ReferenceKind = "implements" // Interface implementation
	RefExtends    ReferenceKind = "extends"    // Class extension
)

// Reference represents a reference to a symbol
type Reference struct {
	Location   *Location     // Location of the reference
	Kind       ReferenceKind // Kind of reference
	FromSymbol string        // StableId of the referencing symbol
	FromName   string        // Human-readable name of the referencing symbol, if resolved by the backend
	FromModule string        // ModuleId of the referencing module
	IsTest     bool          // Whether this reference is from a test
}

// CouplingTier distinguishes how a caller relationship was discovered.
type CouplingTier string

const (
	CouplingSemantic CouplingTier = "semantic" // LIP embedding similarity — lower certainty
	CouplingBoth     CouplingTier = "both"     // confirmed by both SCIP and LIP
)

// EnrichedCaller is a caller discovered by either static analysis or semantic similarity.
type EnrichedCaller struct {
	SymbolURI  string       `json:"symbolUri,omitempty"`
	FileURI    string       `json:"fileUri"`
	Tier       CouplingTier `json:"tier"`
	Confidence float64      `json:"confidence"`           // 0.0–1.0
	Similarity float32      `json:"similarity,omitempty"` // raw cosine similarity (semantic/both only)
}

// BlastRadius summarizes the spread of impact across the codebase
type BlastRadius struct {
	ModuleCount       int    `json:"moduleCount"`       // Number of affected modules
	FileCount         int    `json:"fileCount"`         // Number of affected files
	UniqueCallerCount int    `json:"uniqueCallerCount"` // Number of unique callers (SCIP static only)
	RiskLevel         string `json:"riskLevel"`         // "low", "medium", "high"

	// Semantic enrichment from LIP (populated when LIP blast radius is available)
	StaticCallerCount   int              `json:"staticCallerCount,omitempty"`
	SemanticCallerCount int              `json:"semanticCallerCount,omitempty"`
	ConfirmedCount      int              `json:"confirmedCount,omitempty"` // callers found by both SCIP and LIP
	SemanticCallers     []EnrichedCaller `json:"semanticCallers,omitempty"`
}

// Blast radius classification thresholds
const (
	BlastRadiusLowModuleThreshold    = 2
	BlastRadiusMediumModuleThreshold = 5
	BlastRadiusLowCallerThreshold    = 5
	BlastRadiusMediumCallerThreshold = 20
)

// ClassifyBlastRadius determines the risk level based on module and caller counts
func ClassifyBlastRadius(moduleCount, callerCount int) string {
	// High if many modules OR many callers
	if moduleCount > BlastRadiusMediumModuleThreshold || callerCount > BlastRadiusMediumCallerThreshold {
		return "high"
	}
	if moduleCount > BlastRadiusLowModuleThreshold || callerCount > BlastRadiusLowCallerThreshold {
		return "medium"
	}
	return "low"
}
