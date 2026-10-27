package scip

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// FindReferences finds all references to a symbol
// Uses RefIndex for O(1) lookup when available, falls back to O(n*m) scan otherwise
func (idx *SCIPIndex) FindReferences(symbolId string, options ReferenceOptions) ([]*SCIPReference, error) {
	references := make([]*SCIPReference, 0)

	// Use inverted index for O(1) lookup if available
	if idx.RefIndex != nil {
		occRefs, ok := idx.RefIndex[symbolId]
		if !ok {
			return references, nil // No references found
		}

		for _, occRef := range occRefs {
			ref := idx.processOccurrence(symbolId, occRef.Doc, occRef.Occ, options)
			if ref != nil {
				references = append(references, ref)
				if options.MaxResults > 0 && len(references) >= options.MaxResults {
					return references, nil
				}
			}
		}
		return references, nil
	}

	// Fallback: O(n*m) scan through all documents (for backwards compatibility)
	for _, doc := range idx.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbolId {
				ref := idx.processOccurrence(symbolId, doc, occ, options)
				if ref != nil {
					references = append(references, ref)
					if options.MaxResults > 0 && len(references) >= options.MaxResults {
						return references, nil
					}
				}
			}
		}
	}

	return references, nil
}

// processOccurrence converts an occurrence to a reference, applying filters
func (idx *SCIPIndex) processOccurrence(symbolId string, doc *Document, occ *Occurrence, options ReferenceOptions) *SCIPReference {
	// Determine reference kind from symbol roles
	kind := determineReferenceKind(occ)

	// Skip definition if not included
	if kind == RefDefinition && !options.IncludeDefinition {
		return nil
	}

	// Parse location
	location := parseOccurrenceRange(occ, doc.RelativePath)
	if location == nil {
		return nil
	}

	// Get context if file path is available
	context := ""
	if options.IncludeContext {
		context = extractContext(doc.RelativePath, location, idx)
	}

	// Determine the containing symbol (O(1) with ContainerIndex)
	fromSymbol := findContainingSymbolFast(doc, occ, idx)

	return &SCIPReference{
		SymbolId:   symbolId,
		Location:   location,
		Kind:       kind,
		FromSymbol: fromSymbol,
		Context:    context,
	}
}

// ReferenceOptions contains options for finding references
type ReferenceOptions struct {
	MaxResults          int
	IncludeDefinition   bool
	IncludeTests        bool
	IncludeContext      bool
	Scope               []string
	ReferenceKindFilter []ReferenceKind
}

// determineReferenceKind determines the kind of reference from symbol roles
func determineReferenceKind(occ *Occurrence) ReferenceKind {
	roles := occ.SymbolRoles

	// Check for definition
	if roles&SymbolRoleDefinition != 0 {
		return RefDefinition
	}

	// Check for forward declaration
	if roles&SymbolRoleForwardDefinition != 0 {
		return RefForwardDecl
	}

	// Check for write access
	if roles&SymbolRoleWriteAccess != 0 {
		return RefWrite
	}

	// Check for read access
	if roles&SymbolRoleReadAccess != 0 {
		return RefRead
	}

	// Check for import
	if roles&SymbolRoleImport != 0 {
		return RefImport
	}

	// Default to reference
	return RefReference
}

// findContainingSymbol finds the symbol that contains this occurrence (O(n²) fallback)
func findContainingSymbol(doc *Document, occ *Occurrence) string {
	// Use enclosing range to find containing symbol
	if len(occ.EnclosingRange) > 0 {
		// Find symbol whose definition contains this occurrence
		for _, sym := range doc.Symbols {
			// Find the definition occurrence of this symbol
			for _, defOcc := range doc.Occurrences {
				if defOcc.Symbol == sym.Symbol && defOcc.SymbolRoles&SymbolRoleDefinition != 0 {
					// Check if occurrence is within this symbol's scope
					if isWithinRange(occ.Range, defOcc.EnclosingRange) {
						return sym.Symbol
					}
				}
			}
		}
	}

	return ""
}

// findContainingSymbolFast uses the pre-built ContainerIndex for O(1) lookup
func findContainingSymbolFast(doc *Document, occ *Occurrence, idx *SCIPIndex) string {
	if idx == nil || idx.ContainerIndex == nil || len(occ.Range) < 2 {
		return findContainingSymbol(doc, occ) // fallback to O(n²)
	}

	key := fmt.Sprintf("%s:%d:%d", doc.RelativePath, occ.Range[0], occ.Range[1])
	if container, ok := idx.ContainerIndex[key]; ok {
		return container
	}

	return ""
}

// isWithinRange checks if a range is within another range
func isWithinRange(inner, outer []int32) bool {
	if len(inner) < 2 || len(outer) < 2 {
		return false
	}

	innerStart := inner[0]
	var innerEnd int32
	if len(inner) >= 4 {
		innerEnd = inner[2]
	} else {
		innerEnd = inner[0]
	}

	outerStart := outer[0]
	var outerEnd int32
	if len(outer) >= 4 {
		outerEnd = outer[2]
	} else {
		outerEnd = outer[0]
	}

	return innerStart >= outerStart && innerEnd <= outerEnd
}

// extractContext extracts code context around a location
func extractContext(relativePath string, location *Location, idx *SCIPIndex) string {
	// This is a simplified version - in production, you'd want to:
	// 1. Read the actual file from disk (relativePath relative to repo root)
	// 2. Extract the line(s) around the location
	// 3. Handle edge cases (file not found, etc.)

	// For now, return a placeholder
	return fmt.Sprintf("// Context at %s:%d:%d", relativePath, location.StartLine, location.StartColumn)
}

// ExtractContextFromFile extracts code context from a file
func ExtractContextFromFile(repoRoot, relativePath string, location *Location, contextLines int) (string, error) {
	filePath := repoRoot + "/" + relativePath

	// Read file
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	// Read all lines
	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")

	// Calculate context range
	startLine := location.StartLine - contextLines
	if startLine < 0 {
		startLine = 0
	}

	endLine := location.EndLine + contextLines
	if endLine >= len(lines) {
		endLine = len(lines) - 1
	}

	// Extract context lines
	contextLines_slice := lines[startLine : endLine+1]
	return strings.Join(contextLines_slice, "\n"), nil
}

// FindReferencesByLocation finds references at a specific location
func (idx *SCIPIndex) FindReferencesByLocation(filePath string, line, column int) ([]*SCIPReference, error) {
	// Find the document
	doc := idx.GetDocument(filePath)
	if doc == nil {
		return nil, fmt.Errorf("document not found: %s", filePath)
	}

	// Find occurrence at the given location
	var targetOcc *Occurrence
	for _, occ := range doc.Occurrences {
		if isLocationInRange(line, column, occ.Range) {
			targetOcc = occ
			break
		}
	}

	if targetOcc == nil {
		return nil, fmt.Errorf("no symbol found at %s:%d:%d", filePath, line, column)
	}

	// Find all references to this symbol
	return idx.FindReferences(targetOcc.Symbol, ReferenceOptions{
		IncludeDefinition: true,
		IncludeTests:      true,
		IncludeContext:    false,
	})
}

// isLocationInRange checks if a line/column is within a SCIP range
func isLocationInRange(line, column int, scipRange []int32) bool {
	if len(scipRange) < 3 {
		return false
	}

	startLine := int(scipRange[0])
	startColumn := int(scipRange[1])

	var endLine, endColumn int
	if len(scipRange) == 3 {
		// Single-line range
		endLine = startLine
		endColumn = int(scipRange[2])
	} else if len(scipRange) >= 4 {
		// Multi-line range
		endLine = int(scipRange[2])
		endColumn = int(scipRange[3])
	}

	// Check if location is within range
	if line < startLine || line > endLine {
		return false
	}

	if line == startLine && column < startColumn {
		return false
	}

	if line == endLine && column > endColumn {
		return false
	}

	return true
}

// GroupReferencesByFile groups references by file path
func GroupReferencesByFile(refs []*SCIPReference) map[string][]*SCIPReference {
	grouped := make(map[string][]*SCIPReference)

	for _, ref := range refs {
		if ref.Location == nil {
			continue
		}

		filePath := ref.Location.FileId
		grouped[filePath] = append(grouped[filePath], ref)
	}

	return grouped
}

// GroupReferencesByKind groups references by reference kind
func GroupReferencesByKind(refs []*SCIPReference) map[ReferenceKind][]*SCIPReference {
	grouped := make(map[ReferenceKind][]*SCIPReference)

	for _, ref := range refs {
		grouped[ref.Kind] = append(grouped[ref.Kind], ref)
	}

	return grouped
}

// FilterReferences filters references based on criteria
func FilterReferences(refs []*SCIPReference, filter func(*SCIPReference) bool) []*SCIPReference {
	filtered := make([]*SCIPReference, 0)

	for _, ref := range refs {
		if filter(ref) {
			filtered = append(filtered, ref)
		}
	}

	return filtered
}

// CountReferencesByKind counts references by kind
func CountReferencesByKind(refs []*SCIPReference) map[ReferenceKind]int {
	counts := make(map[ReferenceKind]int)

	for _, ref := range refs {
		counts[ref.Kind]++
	}

	return counts
}

// GetDefinitionReference returns the definition reference from a list of references
func GetDefinitionReference(refs []*SCIPReference) *SCIPReference {
	for _, ref := range refs {
		if ref.Kind == RefDefinition {
			return ref
		}
	}
	return nil
}

// GetNonDefinitionReferences returns all non-definition references
func GetNonDefinitionReferences(refs []*SCIPReference) []*SCIPReference {
	return FilterReferences(refs, func(ref *SCIPReference) bool {
		return ref.Kind != RefDefinition
	})
}

// FindImplementations finds implementations of an interface or abstract class
func (idx *SCIPIndex) FindImplementations(symbolId string) ([]*SCIPReference, error) {
	implementations := make([]*SCIPReference, 0)

	// Search through all symbols for implementation relationships
	for _, symInfo := range idx.Symbols {
		for _, rel := range symInfo.Relationships {
			if rel.Symbol == symbolId && rel.IsImplementation {
				// Find the definition location of the implementing symbol
				location := findSymbolLocation(symInfo.Symbol, idx)
				if location != nil {
					implementations = append(implementations, &SCIPReference{
						SymbolId:   symInfo.Symbol,
						Location:   location,
						Kind:       RefImplements,
						FromSymbol: symbolId,
					})
				}
			}
		}
	}

	return implementations, nil
}

// FindTypeReferences finds all type references to a symbol
func (idx *SCIPIndex) FindTypeReferences(symbolId string) ([]*SCIPReference, error) {
	typeRefs := make([]*SCIPReference, 0)

	// Search through all symbols for type definition relationships
	for _, symInfo := range idx.Symbols {
		for _, rel := range symInfo.Relationships {
			if rel.Symbol == symbolId && rel.IsTypeDefinition {
				// Find the definition location of the referencing symbol
				location := findSymbolLocation(symInfo.Symbol, idx)
				if location != nil {
					typeRefs = append(typeRefs, &SCIPReference{
						SymbolId:   symInfo.Symbol,
						Location:   location,
						Kind:       RefType,
						FromSymbol: symbolId,
					})
				}
			}
		}
	}

	return typeRefs, nil
}

// CountReferences returns the number of distinct reference locations that
// FindReferences would return for symbolId if it were not capped by
// options.MaxResults. It applies the same filters as FindReferences (definition
// handling) and skips context extraction and containing-symbol lookup, so it is
// a cheap full count. MaxResults is ignored.
func (idx *SCIPIndex) CountReferences(symbolId string, options ReferenceOptions) int {
	type locKey struct {
		path      string
		line, col int
	}
	seen := make(map[locKey]struct{})

	visit := func(doc *Document, occ *Occurrence) {
		if determineReferenceKind(occ) == RefDefinition && !options.IncludeDefinition {
			return
		}
		location := parseOccurrenceRange(occ, doc.RelativePath)
		if location == nil {
			return
		}
		seen[locKey{location.FileId, location.StartLine, location.StartColumn}] = struct{}{}
	}

	if idx.RefIndex != nil {
		for _, occRef := range idx.RefIndex[symbolId] {
			visit(occRef.Doc, occRef.Occ)
		}
	} else {
		for _, doc := range idx.Documents {
			for _, occ := range doc.Occurrences {
				if occ.Symbol == symbolId {
					visit(doc, occ)
				}
			}
		}
	}

	return len(seen)
}
