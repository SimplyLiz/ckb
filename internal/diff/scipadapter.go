package diff

import (
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// SCIPSymbolIndex adapts a SCIP index to the SymbolIndex interface
type SCIPSymbolIndex struct {
	index *scip.SCIPIndex
}

// NewSCIPSymbolIndex creates a new SCIPSymbolIndex from a SCIP index
func NewSCIPSymbolIndex(index *scip.SCIPIndex) *SCIPSymbolIndex {
	if index == nil {
		return nil
	}
	return &SCIPSymbolIndex{index: index}
}

// GetDocument returns the document for a file path, or nil if not found
func (s *SCIPSymbolIndex) GetDocument(filePath string) *DocumentInfo {
	doc := s.index.GetDocument(filePath)
	if doc == nil {
		return nil
	}

	// Convert occurrences
	occurrences := make([]OccurrenceInfo, 0, len(doc.Occurrences))
	for _, occ := range doc.Occurrences {
		occInfo := convertOccurrence(occ)
		if occInfo != nil {
			occurrences = append(occurrences, *occInfo)
		}
	}

	// Convert symbol definitions
	displayNames := make(map[string]string, len(doc.Symbols))
	for _, sym := range doc.Symbols {
		displayNames[sym.Symbol] = sym.DisplayName
	}
	bounds := scip.DefinitionBounds(doc)
	symbols := make([]SymbolDefInfo, 0, len(bounds))
	for _, b := range bounds {
		symbols = append(symbols, SymbolDefInfo{
			Symbol:    b.Symbol,
			Name:      displayNames[b.Symbol],
			StartLine: b.Start + 1,
			EndLine:   b.End + 1,
			DefLine:   b.DefLine + 1,
			Precision: b.Precision,
		})
	}

	return &DocumentInfo{
		RelativePath: doc.RelativePath,
		Language:     doc.Language,
		Occurrences:  occurrences,
		Symbols:      symbols,
	}
}

// GetSymbolInfo returns symbol information for a symbol ID
func (s *SCIPSymbolIndex) GetSymbolInfo(symbolID string) *SymbolInfo {
	sym := s.index.GetSymbol(symbolID)
	if sym == nil {
		return nil
	}

	// Get the pre-converted symbol for additional info
	var name, kind, signature string
	if converted, exists := s.index.ConvertedSymbols[symbolID]; exists {
		name = converted.Name
		kind = string(converted.Kind)
		signature = converted.SignatureNormalized
	} else {
		// Fall back to extracting name from display name
		name = sym.DisplayName
		if name == "" {
			name = scip.SymbolSimpleName(sym.Symbol)
		}
		if name == "" {
			name = extractSymbolName(sym.Symbol)
		}
		kind = scipKindToString(sym.Kind)
	}

	return &SymbolInfo{
		Symbol:    sym.Symbol,
		Name:      name,
		Kind:      kind,
		Signature: signature,
	}
}

// convertOccurrence converts a SCIP occurrence to OccurrenceInfo
func convertOccurrence(occ *scip.Occurrence) *OccurrenceInfo {
	if occ == nil || len(occ.Range) < 2 {
		return nil
	}

	startLine := int(occ.Range[0]) + 1 // #nosec G115 -- SCIP int32 fits in int
	endLine := startLine
	startCol := 0
	endCol := 0

	if len(occ.Range) >= 2 {
		startCol = int(occ.Range[1]) // #nosec G115 -- SCIP int32 fits in int
	}
	if len(occ.Range) >= 3 {
		endCol = int(occ.Range[2]) // #nosec G115 -- SCIP int32 fits in int
		// If only 3 elements, end is on same line
	}
	if len(occ.Range) >= 4 {
		endLine = int(occ.Range[2]) + 1 // #nosec G115 -- SCIP int32 fits in int
		endCol = int(occ.Range[3])      // #nosec G115 -- SCIP int32 fits in int
	}

	isDefinition := (occ.SymbolRoles & scip.SymbolRoleDefinition) != 0

	return &OccurrenceInfo{
		StartLine:    startLine,
		EndLine:      endLine,
		StartCol:     startCol,
		EndCol:       endCol,
		Symbol:       occ.Symbol,
		IsDefinition: isDefinition,
	}
}

// scipKindToString converts a SCIP kind to CKB's kind name.
func scipKindToString(kind int32) string {
	return string(scip.MapSCIPKind(kind))
}
