package scip

// SymbolKind represents the kind of a symbol
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
	KindPackage   SymbolKind = "package"
	KindModule    SymbolKind = "module"
	KindField     SymbolKind = "field"
	KindParameter SymbolKind = "parameter"
	KindNamespace SymbolKind = "namespace"
	KindEnum      SymbolKind = "enum"
	KindUnknown   SymbolKind = "unknown"
)

// ReferenceKind represents the kind of a reference
type ReferenceKind string

const (
	RefDefinition  ReferenceKind = "definition"
	RefReference   ReferenceKind = "reference"
	RefCall        ReferenceKind = "call"
	RefType        ReferenceKind = "type"
	RefRead        ReferenceKind = "read"
	RefWrite       ReferenceKind = "write"
	RefImport      ReferenceKind = "import"
	RefImplements  ReferenceKind = "implements"
	RefForwardDecl ReferenceKind = "forward_declaration"
)

// Location represents a position in source code
type Location struct {
	// FileId is the document path relative to repo root
	FileId string

	// StartLine is the starting line (0-indexed in SCIP format)
	StartLine int

	// StartColumn is the starting column (0-indexed in SCIP format)
	StartColumn int

	// EndLine is the ending line (0-indexed in SCIP format)
	EndLine int

	// EndColumn is the ending column (0-indexed in SCIP format)
	EndColumn int
}

// SCIPSymbol represents a symbol extracted from a SCIP index
type SCIPSymbol struct {
	// StableId is the SCIP stable identifier
	StableId string

	// Name is the human-readable name
	Name string

	// Kind is the symbol kind
	Kind SymbolKind

	// Documentation is the doc comment (transient, not stored)
	Documentation string

	// SignatureNormalized is the normalized signature
	SignatureNormalized string

	// Modifiers are symbol modifiers (public, private, static, etc.)
	Modifiers []string

	// Location is the definition location
	Location *Location

	// ContainerName is the containing symbol (class, namespace, etc.)
	ContainerName string

	// Visibility indicates access level
	Visibility string
}

// SCIPReference represents a reference to a symbol
type SCIPReference struct {
	// SymbolId is the stable ID of the referenced symbol
	SymbolId string

	// Location is where the reference appears
	Location *Location

	// Kind is the reference kind
	Kind ReferenceKind

	// FromSymbol is the symbol containing this reference
	FromSymbol string

	// Context is the surrounding code snippet
	Context string
}

// Metadata represents SCIP index metadata
type Metadata struct {
	// Version is the SCIP protocol version
	Version string

	// ToolInfo contains information about the indexing tool
	ToolInfo *ToolInfo

	// ProjectRoot is the root directory of the project
	ProjectRoot string

	// TextDocumentEncoding is the encoding used for text documents
	TextDocumentEncoding string
}

// ToolInfo contains information about the indexing tool
type ToolInfo struct {
	Name      string
	Version   string
	Arguments []string
}

// Document represents a source document in the SCIP index
type Document struct {
	// RelativePath is the path relative to the project root
	RelativePath string

	// Language is the programming language
	Language string

	// Occurrences are all symbol occurrences in this document
	Occurrences []*Occurrence

	// Symbols are symbol definitions in this document
	Symbols []*SymbolInformation

	// dartLeads maps the definition line of a Dart top-level variable to the
	// first line of the doc comment and annotations above it, read from the
	// source on load (dartLeadStart). Nil when the source wasn't read.
	dartLeads map[int]int
}

// Occurrence represents a single occurrence of a symbol in a document
type Occurrence struct {
	// Range is the location of the occurrence
	Range []int32

	// Symbol is the SCIP symbol identifier
	Symbol string

	// SymbolRoles indicates the role(s) of this occurrence
	SymbolRoles int32

	// OverrideDocumentation provides additional documentation
	OverrideDocumentation []string

	// SyntaxKind is the syntax node kind
	SyntaxKind int32

	// Diagnostics are associated diagnostics
	Diagnostics []*Diagnostic

	// EnclosingRange is the range of the enclosing scope
	EnclosingRange []int32
}

// SymbolInformation contains detailed information about a symbol
type SymbolInformation struct {
	// Symbol is the SCIP symbol identifier
	Symbol string

	// Documentation is the doc comment
	Documentation []string

	// Relationships are relationships to other symbols
	Relationships []*Relationship

	// Kind is the symbol kind
	Kind int32

	// DisplayName is the human-readable name
	DisplayName string

	// SignatureDocumentation is the signature documentation
	SignatureDocumentation *Document

	// EnclosingSymbol is the containing symbol
	EnclosingSymbol string
}

// Relationship represents a relationship between symbols
type Relationship struct {
	// Symbol is the related symbol
	Symbol string

	// IsReference indicates if this is a reference
	IsReference bool

	// IsImplementation indicates if this is an implementation
	IsImplementation bool

	// IsTypeDefinition indicates if this is a type definition
	IsTypeDefinition bool

	// IsDefinition indicates if this is a definition
	IsDefinition bool
}

// Diagnostic represents a diagnostic message
type Diagnostic struct {
	// Severity is the diagnostic severity
	Severity int32

	// Code is the diagnostic code
	Code string

	// Message is the diagnostic message
	Message string

	// Source is the diagnostic source
	Source string

	// Tags are diagnostic tags
	Tags []int32
}

// SymbolRole constants (from SCIP protocol)
const (
	SymbolRoleDefinition        int32 = 1
	SymbolRoleImport            int32 = 2
	SymbolRoleWriteAccess       int32 = 4
	SymbolRoleReadAccess        int32 = 8
	SymbolRoleGenerated         int32 = 16
	SymbolRoleTest              int32 = 32
	SymbolRoleForwardDefinition int32 = 64
)
