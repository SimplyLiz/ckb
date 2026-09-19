package scip

import (
	"fmt"
	"strings"
)

// ExtractSymbols extracts all symbols from the SCIP index
func (idx *SCIPIndex) ExtractSymbols() ([]*SCIPSymbol, error) {
	symbols := make([]*SCIPSymbol, 0, len(idx.Symbols))

	for _, symInfo := range idx.Symbols {
		scipSym, err := convertToSCIPSymbol(symInfo, idx)
		if err != nil {
			// Log error but continue processing other symbols
			continue
		}
		symbols = append(symbols, scipSym)
	}

	return symbols, nil
}

// GetSymbolByID retrieves a specific symbol by its SCIP ID
func (idx *SCIPIndex) GetSymbolByID(symbolId string) (*SCIPSymbol, error) {
	symInfo := idx.GetSymbol(symbolId)
	if symInfo == nil {
		return nil, fmt.Errorf("symbol not found: %s", symbolId)
	}

	return convertToSCIPSymbol(symInfo, idx)
}

// FindSymbolsByName finds symbols matching a given name
func (idx *SCIPIndex) FindSymbolsByName(name string) ([]*SCIPSymbol, error) {
	var matches []*SCIPSymbol

	for _, symInfo := range idx.Symbols {
		scipId, err := ParseSCIPIdentifier(symInfo.Symbol)
		if err != nil {
			continue
		}

		// Match by simple name or display name
		simpleName := scipId.GetSimpleName()
		if simpleName == name || symInfo.DisplayName == name {
			scipSym, err := convertToSCIPSymbol(symInfo, idx)
			if err == nil {
				matches = append(matches, scipSym)
			}
		}
	}

	return matches, nil
}

// FindSymbolsByKind finds all symbols of a specific kind
func (idx *SCIPIndex) FindSymbolsByKind(kind SymbolKind) ([]*SCIPSymbol, error) {
	var matches []*SCIPSymbol

	for _, symInfo := range idx.Symbols {
		scipSym, err := convertToSCIPSymbol(symInfo, idx)
		if err != nil {
			continue
		}

		if scipSym.Kind == kind {
			matches = append(matches, scipSym)
		}
	}

	return matches, nil
}

// convertToSCIPSymbol converts SymbolInformation to SCIPSymbol
// Deprecated: Use GetCachedSymbol for better performance when possible
func convertToSCIPSymbol(symInfo *SymbolInformation, idx *SCIPIndex) (*SCIPSymbol, error) {
	return convertToSCIPSymbolWithIndex(symInfo, idx)
}

// convertToSCIPSymbolWithIndex converts SymbolInformation to SCIPSymbol
// This is the internal conversion function used during index loading
func convertToSCIPSymbolWithIndex(symInfo *SymbolInformation, idx *SCIPIndex) (*SCIPSymbol, error) {
	// Parse SCIP identifier
	scipId, err := ParseSCIPIdentifier(symInfo.Symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SCIP identifier: %w", err)
	}

	// Extract name
	name := symInfo.DisplayName
	if name == "" {
		name = scipId.GetSimpleName()
	}

	// Determine symbol kind
	kind := inferSymbolKind(symInfo, scipId)

	// Extract documentation
	documentation := strings.Join(symInfo.Documentation, "\n")

	// Find location using RefIndex if available for O(1) lookup
	var location *Location
	if idx.RefIndex != nil {
		location = findSymbolLocationFast(symInfo.Symbol, idx)
	} else {
		location = findSymbolLocation(symInfo.Symbol, idx)
	}

	// Extract modifiers
	modifiers := extractModifiers(symInfo)

	// Get container name
	containerName := scipId.GetContainerName()
	if containerName == "" && symInfo.EnclosingSymbol != "" {
		// Try to get container from enclosing symbol
		enclosingId, errEnc := ParseSCIPIdentifier(symInfo.EnclosingSymbol)
		if errEnc == nil {
			containerName = enclosingId.GetSimpleName()
		}
	}

	// Infer visibility
	visibility := inferVisibility(symInfo, scipId, name)

	return &SCIPSymbol{
		StableId:            symInfo.Symbol,
		Name:                name,
		Kind:                kind,
		Documentation:       documentation,
		SignatureNormalized: "", // Signature extraction requires relationship data not available at parse time
		Modifiers:           modifiers,
		Location:            location,
		ContainerName:       containerName,
		Visibility:          visibility,
	}, nil
}

// GetCachedSymbol retrieves a pre-converted symbol from the cache
// Falls back to conversion if not cached
func (idx *SCIPIndex) GetCachedSymbol(symbolId string) (*SCIPSymbol, error) {
	// Try cache first
	if idx.ConvertedSymbols != nil {
		if cached, ok := idx.ConvertedSymbols[symbolId]; ok {
			return cached, nil
		}
	}

	// Fall back to conversion
	symInfo := idx.GetSymbol(symbolId)
	if symInfo == nil {
		return nil, fmt.Errorf("symbol not found: %s", symbolId)
	}
	return convertToSCIPSymbolWithIndex(symInfo, idx)
}

// inferSymbolKind infers the symbol kind from various sources
func inferSymbolKind(symInfo *SymbolInformation, scipId *SCIPIdentifier) SymbolKind {
	// First try to use the SCIP kind field
	switch symInfo.Kind {
	case 1: // Class
		return KindClass
	case 2: // Interface
		return KindInterface
	case 3: // Enum
		return KindEnum
	case 6: // Function
		return KindFunction
	case 7: // Variable
		return KindVariable
	case 8: // Constant
		return KindConstant
	case 9: // Method
		return KindMethod
	case 10: // Property
		return KindProperty
	case 11: // Field
		return KindField
	case 12: // Parameter
		return KindParameter
	case 19: // Namespace
		return KindNamespace
	case 20: // Package
		return KindPackage
	case 21: // Type
		return KindType
	}

	// Fall back to inferring from descriptor
	return scipId.ExtractSymbolKind()
}

// findSymbolLocation finds the definition location of a symbol
// This is the O(n*m) fallback when RefIndex is not available
func findSymbolLocation(symbolId string, idx *SCIPIndex) *Location {
	// Search through all documents for the definition occurrence
	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbolId {
				// Check if this is a definition
				if occ.SymbolRoles&SymbolRoleDefinition != 0 {
					return parseOccurrenceRange(occ, doc.RelativePath)
				}
			}
		}
	}

	return nil
}

// findSymbolLocationFast finds the definition location of a symbol.
// Uses DefinitionIndex for O(1) lookup when available, falls back to
// scanning RefIndex (O(k) where k = occurrences of the symbol).
func findSymbolLocationFast(symbolId string, idx *SCIPIndex) *Location {
	// O(1): DefinitionIndex is built during the parallel doc phase.
	if idx.DefinitionIndex != nil {
		if ref, ok := idx.DefinitionIndex[symbolId]; ok {
			return parseOccurrenceRange(ref.Occ, ref.Doc.RelativePath)
		}
	}

	// Fallback: scan RefIndex (O(k)).
	for _, ref := range idx.RefIndex[symbolId] {
		if ref.Occ.SymbolRoles&SymbolRoleDefinition != 0 {
			return parseOccurrenceRange(ref.Occ, ref.Doc.RelativePath)
		}
	}

	return nil
}

// parseOccurrenceRange converts a SCIP occurrence range to a Location
func parseOccurrenceRange(occ *Occurrence, filePath string) *Location {
	if len(occ.Range) < 3 {
		return nil
	}

	// SCIP range format: [startLine, startChar, endChar] for single-line
	// or [startLine, startChar, endLine, endChar] for multi-line
	startLine := int(occ.Range[0])   // #nosec G115 -- SCIP int32 fits in int
	startColumn := int(occ.Range[1]) // #nosec G115 -- SCIP int32 fits in int

	var endLine, endColumn int
	if len(occ.Range) == 3 {
		// Single-line range
		endLine = startLine
		endColumn = int(occ.Range[2]) // #nosec G115 -- SCIP int32 fits in int
	} else if len(occ.Range) >= 4 {
		// Multi-line range
		endLine = int(occ.Range[2])   // #nosec G115 -- SCIP int32 fits in int
		endColumn = int(occ.Range[3]) // #nosec G115 -- SCIP int32 fits in int
	}

	return &Location{
		FileId:      filePath,
		StartLine:   startLine,
		StartColumn: startColumn,
		EndLine:     endLine,
		EndColumn:   endColumn,
	}
}

// extractModifiers extracts symbol modifiers from relationships and other metadata
func extractModifiers(symInfo *SymbolInformation) []string {
	modifiers := make([]string, 0)

	// Check relationships for modifiers
	for _, rel := range symInfo.Relationships {
		if rel.IsImplementation {
			modifiers = append(modifiers, "implements")
		}
		if rel.IsTypeDefinition {
			modifiers = append(modifiers, "type")
		}
	}

	return modifiers
}

// inferVisibility infers the visibility of a symbol
func inferVisibility(symInfo *SymbolInformation, scipId *SCIPIdentifier, name string) string {
	// Check for common visibility indicators in the name
	if strings.HasPrefix(name, "_") {
		return "private"
	}

	if strings.HasPrefix(name, "__") {
		return "internal"
	}

	// Check if it's an exported symbol (varies by language)
	language := scipId.GetLanguage()
	switch language {
	case "go":
		// In Go, uppercase first letter means exported
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			return "public"
		}
		return "private"
	case "typescript", "javascript":
		// TypeScript/JavaScript: underscore prefix means private
		if strings.HasPrefix(name, "_") {
			return "private"
		}
		return "public"
	case "python":
		// Python: single underscore is protected, double is private
		if strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__") {
			return "private"
		}
		if strings.HasPrefix(name, "_") {
			return "protected"
		}
		return "public"
	}

	// Default to public if we can't determine
	return "public"
}

// GetSymbolSignature extracts or constructs a signature for a symbol
func GetSymbolSignature(symInfo *SymbolInformation, scipId *SCIPIdentifier) string {
	// For methods/functions, extract signature from descriptor
	if IsMethodDescriptor(scipId.Descriptor) {
		return scipId.Descriptor
	}

	// For other symbols, construct a simple signature
	if symInfo.DisplayName != "" {
		return symInfo.DisplayName
	}

	return scipId.GetSimpleName()
}

// SearchSymbols performs a search across all symbols.
//
// When NameIndex is available it iterates a compact, sorted []NameEntry slice
// instead of the ConvertedSymbols map. The slice is cache-line friendly
// (contiguous structs vs scattered map buckets), which gives meaningfully
// better throughput on large symbol sets. Prefix queries also benefit from
// binary-search early termination.
func (idx *SCIPIndex) SearchSymbols(query string, options SearchOptions) ([]*SCIPSymbol, error) {
	var matches []*SCIPSymbol
	queryLower := strings.ToLower(query)

	if len(idx.NameIndex) > 0 && len(idx.ConvertedSymbols) > 0 {
		// Fast path: iterate the compact sorted name slice.
		// For purely prefix queries we could binary-search the lower bound,
		// but substring queries (the common case) still require a full scan.
		// The cache-line advantage of []NameEntry over map iteration already
		// gives a significant speedup at large N.
		for _, entry := range idx.NameIndex {
			nameMatch := strings.Contains(strings.ToLower(entry.Name), queryLower)
			aliasMatch := entry.Alias != "" && strings.Contains(strings.ToLower(entry.Alias), queryLower)
			if !nameMatch && !aliasMatch {
				continue
			}
			sym, ok := idx.ConvertedSymbols[entry.ID]
			if !ok {
				continue
			}
			if matchesQuery(sym, queryLower, options) {
				matches = append(matches, sym)
				if options.MaxResults > 0 && len(matches) >= options.MaxResults {
					return matches, nil
				}
			}
		}
		return matches, nil
	}

	// Fallback: ConvertedSymbols map iteration (no NameIndex).
	if len(idx.ConvertedSymbols) > 0 {
		for _, scipSym := range idx.ConvertedSymbols {
			if matchesQuery(scipSym, queryLower, options) {
				matches = append(matches, scipSym)
			}
			if options.MaxResults > 0 && len(matches) >= options.MaxResults {
				break
			}
		}
		return matches, nil
	}

	// Last resort: convert on-the-fly (no pre-converted cache at all).
	for _, symInfo := range idx.Symbols {
		scipSym, err := convertToSCIPSymbol(symInfo, idx)
		if err != nil {
			continue
		}
		if matchesQuery(scipSym, queryLower, options) {
			matches = append(matches, scipSym)
		}
		if options.MaxResults > 0 && len(matches) >= options.MaxResults {
			break
		}
	}

	return matches, nil
}

// SearchOptions contains options for symbol search
type SearchOptions struct {
	MaxResults   int
	IncludeTests bool
	Scope        []string
	Kind         []SymbolKind
}

// matchesQuery checks if a symbol matches a search query
func matchesQuery(sym *SCIPSymbol, queryLower string, options SearchOptions) bool {
	// Check name match, including the container-qualified alias
	// ("Handler.handle") so a member stays findable by searching its
	// container's name even when FTS is unavailable.
	nameLower := strings.ToLower(sym.Name)
	nameMatch := strings.Contains(nameLower, queryLower)
	aliasMatch := false
	if !nameMatch && sym.ContainerName != "" {
		aliasMatch = strings.Contains(strings.ToLower(sym.ContainerName+"."+sym.Name), queryLower)
	}
	if !nameMatch && !aliasMatch {
		return false
	}

	// Filter by kind if specified
	if len(options.Kind) > 0 {
		kindMatch := false
		for _, k := range options.Kind {
			if sym.Kind == k {
				kindMatch = true
				break
			}
		}
		if !kindMatch {
			return false
		}
	}

	// Filter by scope if specified
	if len(options.Scope) > 0 {
		if sym.Location == nil {
			return false
		}
		scopeMatch := false
		for _, scope := range options.Scope {
			if strings.HasPrefix(sym.Location.FileId, scope) {
				scopeMatch = true
				break
			}
		}
		if !scopeMatch {
			return false
		}
	}

	// Filter tests if not included
	if !options.IncludeTests && sym.Location != nil {
		if isTestFile(sym.Location.FileId) {
			return false
		}
	}

	return true
}

// isTestFile checks if a file path represents a test file
func isTestFile(path string) bool {
	pathLower := strings.ToLower(path)
	return strings.Contains(pathLower, "_test.") ||
		strings.Contains(pathLower, ".test.") ||
		strings.Contains(pathLower, "/test/") ||
		strings.Contains(pathLower, "/tests/") ||
		strings.HasSuffix(pathLower, "_test") ||
		strings.HasSuffix(pathLower, ".spec.")
}

// singleReturnNew lists New* constructors known to return only (T), not (T, error).
// These are excluded from the "New prefix implies error" heuristic.
var singleReturnNew = map[string]bool{
	"NewScanner":      true, // bufio.NewScanner → *Scanner
	"NewReader":       true, // bufio/bytes/strings.NewReader → *Reader
	"NewWriter":       true, // bufio.NewWriter → *Writer
	"NewBuffer":       true, // bytes.NewBuffer → *Buffer
	"NewBufferString": true, // bytes.NewBufferString → *Buffer
	"NewReplacer":     true, // strings.NewReplacer → *Replacer
	"NewTicker":       true, // time.NewTicker → *Ticker
	"NewTimer":        true, // time.NewTimer → *Timer
	"NewCond":         true, // sync.NewCond → *Cond
	"NewMutex":        true, // various — not stdlib but common
	"New":             true, // log.New → *Logger, errors.New → error (neither is (T,error))
	"NewRWMutex":      true,
	"NewWaitGroup":    true,
	"NewPool":         true,
	"NewMap":          true,
	"NewOnce":         true,
	"NewServeMux":     true, // net/http.NewServeMux → *ServeMux
	"NewRegexp":       true,
	"NewParser":       true, // common single-return constructor
	"NewLogger":       true,
}

// noErrorMethods lists method names that return bool or are routinely discarded safely,
// even though their names match error-returning patterns.
var noErrorMethods = map[string]bool{
	"Scan":           true, // bufio.Scanner.Scan() → bool (errors via .Err())
	"WriteHeader":    true, // http.ResponseWriter.WriteHeader() returns nothing
	"WriteJSON":      true, // common HTTP helpers that handle errors internally
	"WriteJSONError": true,
	"WriteError":     true,
	"WriteCkbError":  true,
	"BadRequest":     true, // HTTP convenience wrappers (no return value)
	"NotFound":       true,
	"InternalError":  true,
}

// LikelyReturnsError uses heuristics to determine if a function likely returns an error.
// Since SignatureFull is not always populated, this uses name patterns and documentation.
func LikelyReturnsError(symbolName string) bool {
	// Exclude known single-return methods
	if noErrorMethods[symbolName] {
		return false
	}

	// Common Go stdlib/convention patterns for error-returning functions.
	// "Lock" is deliberately absent: sync.{Mutex,RWMutex}.Lock dominates real
	// code and returns nothing, so the rule produces far more false positives
	// than the rare file-lock true positive it would catch.
	errorPatterns := []string{
		"Open", "Read", "Write", "Close", "Create",
		"Dial", "Listen", "Accept", "Connect",
		"Parse", "Unmarshal", "Marshal", "Decode", "Encode",
		"Execute", "Exec", "Query",
		"Send", "Recv", "Flush",
		"Acquire",
		"Start", "Stop", "Init", "Setup",
	}

	// Check if name starts with or equals any pattern
	for _, p := range errorPatterns {
		if symbolName == p || strings.HasPrefix(symbolName, p) {
			return true
		}
	}

	// Functions starting with New commonly return (T, error),
	// but exclude known single-return constructors.
	if strings.HasPrefix(symbolName, "New") && !singleReturnNew[symbolName] {
		return true
	}

	return false
}

// CountSymbolsByPath counts the number of symbols in documents matching a path prefix
func (idx *SCIPIndex) CountSymbolsByPath(pathPrefix string) int {
	count := 0
	for _, doc := range idx.Documents {
		// Match documents where the path starts with the prefix
		// or if the prefix is "." (root), count all
		if pathPrefix == "." || strings.HasPrefix(doc.RelativePath, pathPrefix) {
			count += len(doc.Symbols)
		}
	}
	return count
}
