package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// SymbolIndex is an interface for querying symbols by file and line
// This abstracts the SCIP index to allow for testing and flexibility
type SymbolIndex interface {
	// GetDocument returns the document for a file path, or nil if not found
	GetDocument(filePath string) *DocumentInfo

	// GetSymbolInfo returns symbol information for a symbol ID
	GetSymbolInfo(symbolID string) *SymbolInfo
}

// DocumentInfo contains document data needed for symbol mapping
type DocumentInfo struct {
	RelativePath string
	Language     string
	Occurrences  []OccurrenceInfo
	Symbols      []SymbolDefInfo
}

// OccurrenceInfo contains occurrence data
type OccurrenceInfo struct {
	StartLine    int
	EndLine      int
	StartCol     int
	EndCol       int
	Symbol       string
	IsDefinition bool
}

// SymbolDefInfo contains symbol definition data. Lines are 1-based and inclusive.
type SymbolDefInfo struct {
	Symbol    string
	Name      string
	Kind      string
	StartLine int
	EndLine   int
	DefLine   int                  // Line of the defining identifier; 0 means StartLine
	Precision scip.BoundsPrecision // How far StartLine..EndLine can be trusted
}

// SymbolInfo contains symbol metadata
type SymbolInfo struct {
	Symbol    string
	Name      string
	Kind      string
	Signature string
}

// DiffSymbolMapper maps diff changes to SCIP symbols
type DiffSymbolMapper struct {
	index SymbolIndex
}

// NewDiffSymbolMapper creates a new DiffSymbolMapper
func NewDiffSymbolMapper(index SymbolIndex) *DiffSymbolMapper {
	return &DiffSymbolMapper{index: index}
}

// MapToSymbols maps changed lines in a ParsedDiff to symbols
func (m *DiffSymbolMapper) MapToSymbols(diff *impact.ParsedDiff) ([]impact.ChangedSymbol, error) {
	if diff == nil {
		return nil, nil
	}

	var result []impact.ChangedSymbol
	seen := make(map[string]bool) // Dedupe by symbol ID

	for _, file := range diff.Files {
		symbols, err := m.mapFileToSymbols(&file)
		if err != nil {
			// Log but continue with other files
			continue
		}

		for _, sym := range symbols {
			if !seen[sym.SymbolID] {
				seen[sym.SymbolID] = true
				result = append(result, sym)
			} else {
				// Update existing with merged info if needed
				for i := range result {
					if result[i].SymbolID == sym.SymbolID {
						result[i].Lines = mergeLines(result[i].Lines, sym.Lines)
						// Keep higher confidence
						if sym.Confidence > result[i].Confidence {
							result[i].Confidence = sym.Confidence
						}
						break
					}
				}
			}
		}
	}

	// Sort by confidence descending, then by name
	sort.Slice(result, func(i, j int) bool {
		if result[i].Confidence != result[j].Confidence {
			return result[i].Confidence > result[j].Confidence
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// Mapping confidences. A definition's own line changing is certain; a change
// inside an indexer-given range nearly so; a range inferred from the next
// function also contains whatever sits between functions.
const (
	confidenceDefinitionLine = 1.0
	confidenceExactBody      = 0.8
	confidenceInferredBody   = 0.6
	confidenceUnresolved     = 0.3
)

// mapFileToSymbols maps changes in a single file to the definitions containing
// them: the innermost function, method, type or other named definition around
// each changed line. A symbol merely referenced on a changed line is not changed,
// and neither is the package around it or a local inside it.
//
// Lines that no definition can be shown to contain come back as one file-level
// entry at low confidence, with a note, rather than as a guess at a symbol.
func (m *DiffSymbolMapper) mapFileToSymbols(file *impact.ChangedFile) ([]impact.ChangedSymbol, error) {
	// Determine the file path to look up
	filePath := file.NewPath
	if file.Deleted {
		filePath = file.OldPath
	}
	if filePath == "" {
		return nil, nil
	}

	doc, docPath := m.lookupDocument(file, filePath)
	if doc == nil {
		// File not in index - return with low confidence
		return m.mapFileWithoutIndex(file)
	}

	side := resolveIndexSide(file, doc, docPath)
	if side == indexMatchesNeither {
		return []impact.ChangedSymbol{fileLevelEntry(file, filePath, changedNewLines(file),
			"index does not match this file on either side of the diff; run 'ckb index' to attribute these lines")}, nil
	}

	defs := changeableDefinitions(doc)
	found := make(map[string]*attributedSymbol)
	var unresolved []int

	for hunkIdx := range file.Hunks {
		for _, block := range hunkBlocks(&file.Hunks[hunkIdx]) {
			resolved := false
			for _, pos := range blockPositions(block, side) {
				for _, def := range pos.enclosing(defs) {
					found[def.Symbol] = found[def.Symbol].merge(def, hunkIdx, pos.confidence(def))
					found[def.Symbol].lines = append(found[def.Symbol].lines, newFileLines(block)...)
					resolved = true
				}
			}
			if !resolved {
				unresolved = append(unresolved, newFileLines(block)...)
			}
		}
	}

	changeType := fileChangeType(file)
	result := make([]impact.ChangedSymbol, 0, len(found)+1)
	for symbolID, sym := range found {
		result = append(result, impact.ChangedSymbol{
			SymbolID:   symbolID,
			Name:       m.symbolName(symbolID, sym.name),
			File:       filePath,
			ChangeType: changeType,
			Lines:      uniqueLines(sym.lines),
			Confidence: sym.confidence,
			HunkIndex:  sym.hunkIndex,
		})
	}
	if len(unresolved) > 0 {
		result = append(result, fileLevelEntry(file, filePath, unresolved,
			"changed lines are outside every definition the index knows about"))
	}

	return result, nil
}

// lookupDocument finds a changed file in the index. A renamed file is indexed
// under its old path if the index predates the rename.
func (m *DiffSymbolMapper) lookupDocument(file *impact.ChangedFile, filePath string) (*DocumentInfo, string) {
	paths := []string{filePath, strings.TrimPrefix(filePath, "/")}
	if file.Renamed && file.OldPath != "" {
		paths = append(paths, file.OldPath)
	}
	for _, path := range paths {
		if doc := m.index.GetDocument(path); doc != nil {
			return doc, path
		}
	}
	return nil, ""
}

// symbolName returns a display name for a symbol. scip-typescript sets no
// display names, so the identifier's last descriptor stands in for them.
func (m *DiffSymbolMapper) symbolName(symbolID, defName string) string {
	if info := m.index.GetSymbolInfo(symbolID); info != nil && info.Name != "" {
		return info.Name
	}
	if defName != "" {
		return defName
	}
	if name := scip.SymbolSimpleName(symbolID); name != "" {
		return name
	}
	return extractSymbolName(symbolID)
}

// attributedSymbol accumulates the changes attributed to one definition.
type attributedSymbol struct {
	name       string
	lines      []int
	hunkIndex  int
	confidence float64
}

func (a *attributedSymbol) merge(def *SymbolDefInfo, hunkIdx int, confidence float64) *attributedSymbol {
	if a == nil {
		a = &attributedSymbol{name: def.Name, hunkIndex: hunkIdx}
	}
	if confidence > a.confidence {
		a.confidence = confidence
	}
	return a
}

// changeableDefinitions returns a document's definitions a change can belong to.
func changeableDefinitions(doc *DocumentInfo) []*SymbolDefInfo {
	defs := make([]*SymbolDefInfo, 0, len(doc.Symbols))
	for i := range doc.Symbols {
		def := &doc.Symbols[i]
		if !scip.IsChangeableDefinition(def.Symbol) || def.StartLine <= 0 {
			continue
		}
		defs = append(defs, def)
	}
	return defs
}

// innermostDefinitions returns the narrowest definitions among those the match
// function accepts. More than one only if they span the same lines, as with
// "const a, b = 1, 2".
func innermostDefinitions(defs []*SymbolDefInfo, match func(*SymbolDefInfo) bool) []*SymbolDefInfo {
	var best []*SymbolDefInfo
	bestSpan := 0
	for _, def := range defs {
		if !match(def) {
			continue
		}
		span := def.EndLine - def.StartLine
		switch {
		case best == nil || span < bestSpan:
			best, bestSpan = []*SymbolDefInfo{def}, span
		case span == bestSpan:
			best = append(best, def)
		}
	}
	return best
}

// indexPosition is where a changed line or block sits in the index's coordinates:
// on a line, or for a pure insertion or deletion in the gap before that line.
type indexPosition struct {
	line  int
	isGap bool
}

// enclosing returns the innermost definitions containing this position. A gap
// has to be strictly inside: code inserted right after a function's closing
// brace isn't part of it.
func (p indexPosition) enclosing(defs []*SymbolDefInfo) []*SymbolDefInfo {
	if p.isGap {
		return innermostDefinitions(defs, func(d *SymbolDefInfo) bool {
			return d.StartLine < p.line && p.line <= d.EndLine
		})
	}
	return innermostDefinitions(defs, func(d *SymbolDefInfo) bool {
		return d.StartLine <= p.line && p.line <= d.EndLine
	})
}

func (p indexPosition) confidence(def *SymbolDefInfo) float64 {
	if !p.isGap && (p.line == def.StartLine || p.line == def.DefLine) {
		return confidenceDefinitionLine
	}
	if def.Precision == scip.BoundsInferred {
		return confidenceInferredBody
	}
	return confidenceExactBody
}

// blockPositions returns the positions of a change block in the index's
// coordinates. A modification is located by the lines it has on the index's side
// of the diff, so it can't fall out of a function whose last line it replaced.
func blockPositions(block impact.ChangeBlock, side indexSide) []indexPosition {
	start, count := block.NewLine, len(block.Added)
	if side == indexMatchesOld {
		start, count = block.OldLine, len(block.Removed)
	}
	if count == 0 {
		return []indexPosition{{line: start, isGap: true}}
	}
	positions := make([]indexPosition, count)
	for i := range positions {
		positions[i] = indexPosition{line: start + i}
	}
	return positions
}

// hunkBlocks returns a hunk's change blocks. A hunk built by hand has no text,
// so it gets one block per added line, in new-file coordinates.
func hunkBlocks(hunk *impact.ChangedHunk) []impact.ChangeBlock {
	if len(hunk.Blocks) > 0 {
		return hunk.Blocks
	}
	blocks := make([]impact.ChangeBlock, 0, len(hunk.Added))
	for _, line := range hunk.Added {
		blocks = append(blocks, impact.ChangeBlock{NewLine: line, Added: []string{""}})
	}
	return blocks
}

// indexSide says which side of a diff an index's line numbers refer to. Usually
// the old one: an index built at HEAD, and a diff of the working tree on top of
// it. Once something is reindexed (watch mode, or a diff of commits already
// indexed), it is the new one.
type indexSide int

const (
	indexMatchesOld indexSide = iota
	indexMatchesNew
	indexMatchesNeither
)

// resolveIndexSide decides which side of the diff a document's line numbers refer
// to, by checking the hunks' own text against the occurrences on those lines. The
// lines a change sits on are all covered by its hunk, so the check is about
// exactly the lines it has to be right for.
func resolveIndexSide(file *impact.ChangedFile, doc *DocumentInfo, docPath string) indexSide {
	switch {
	case file.IsNew:
		return indexMatchesNew
	case file.Deleted:
		return indexMatchesOld
	case file.Renamed:
		if docPath == file.OldPath {
			return indexMatchesOld
		}
		return indexMatchesNew
	}

	oldLines := make(map[int]string)
	newLines := make(map[int]string)
	for i := range file.Hunks {
		hunk := &file.Hunks[i]
		for j, text := range hunk.OldText {
			oldLines[hunk.OldStart+j] = text
		}
		for j, text := range hunk.NewText {
			newLines[hunk.NewStart+j] = text
		}
	}
	if len(oldLines) == 0 && len(newLines) == 0 {
		return indexMatchesNew // Built by hand: no text, and Added is all there is
	}

	var oldScore, newScore, defsSeen, defsMatched int
	names := make(map[string]string)
	for _, occ := range doc.Occurrences {
		if strings.HasPrefix(occ.Symbol, "local ") {
			continue
		}
		name, known := names[occ.Symbol]
		if !known {
			name = scip.SymbolSimpleName(occ.Symbol)
			names[occ.Symbol] = name
		}
		if name == "" {
			continue
		}
		oldText, inOld := oldLines[occ.StartLine]
		newText, inNew := newLines[occ.StartLine]
		if !inOld && !inNew {
			continue
		}
		oldHit := inOld && containsWord(oldText, name)
		newHit := inNew && containsWord(newText, name)
		oldScore += lineScore(inOld, oldHit)
		newScore += lineScore(inNew, newHit)
		if occ.IsDefinition && scip.IsChangeableDefinition(occ.Symbol) {
			defsSeen++
			if oldHit || newHit {
				defsMatched++
			}
		}
	}

	switch {
	case defsSeen > 0 && defsMatched == 0:
		return indexMatchesNeither
	case newScore > oldScore:
		return indexMatchesNew
	}
	// A tie means nothing in the hunks moved relative to the index, so either
	// side locates the change the same way.
	return indexMatchesOld
}

func lineScore(inWindow, hit bool) int {
	switch {
	case hit:
		return 1
	case inWindow:
		return -1
	}
	return 0
}

// containsWord reports whether name occurs in text as a whole identifier.
func containsWord(text, name string) bool {
	for offset := 0; ; {
		i := strings.Index(text[offset:], name)
		if i < 0 {
			return false
		}
		start := offset + i
		end := start + len(name)
		if (start == 0 || !isIdentByte(text[start-1])) && (end == len(text) || !isIdentByte(text[end])) {
			return true
		}
		offset = start + 1
	}
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// newFileLines returns where a block sits in the new file: its added lines, or
// for a pure deletion the line now following it.
func newFileLines(block impact.ChangeBlock) []int {
	if len(block.Added) == 0 {
		return []int{block.NewLine}
	}
	lines := make([]int, len(block.Added))
	for i := range lines {
		lines[i] = block.NewLine + i
	}
	return lines
}

// changedNewLines returns the new-file lines of every change block in a file.
func changedNewLines(file *impact.ChangedFile) []int {
	var lines []int
	for i := range file.Hunks {
		for _, block := range hunkBlocks(&file.Hunks[i]) {
			lines = append(lines, newFileLines(block)...)
		}
	}
	return lines
}

func fileChangeType(file *impact.ChangedFile) impact.ChangeType {
	switch {
	case file.IsNew:
		return impact.ChangeAdded
	case file.Deleted:
		return impact.ChangeDeleted
	}
	return impact.ChangeModified
}

// fileLevelEntry is the honest answer when no definition can be named: this file
// changed on these lines, and here is why nothing more precise can be said.
func fileLevelEntry(file *impact.ChangedFile, filePath string, lines []int, note string) impact.ChangedSymbol {
	return impact.ChangedSymbol{
		SymbolID:   fmt.Sprintf("file:%s", filePath),
		Name:       filePath,
		File:       filePath,
		ChangeType: fileChangeType(file),
		Lines:      uniqueLines(lines),
		Confidence: confidenceUnresolved,
		Note:       note,
	}
}

// mapFileWithoutIndex creates low-confidence entries for files not in the index
func (m *DiffSymbolMapper) mapFileWithoutIndex(file *impact.ChangedFile) ([]impact.ChangedSymbol, error) {
	filePath := file.NewPath
	if file.Deleted {
		filePath = file.OldPath
	}
	if filePath == "" {
		return nil, nil
	}

	// Collect all changed lines (added for new/modified, removed for deleted)
	var allLines []int
	for _, hunk := range file.Hunks {
		if file.Deleted {
			allLines = append(allLines, hunk.Removed...)
		} else {
			allLines = append(allLines, hunk.Added...)
		}
	}

	if len(allLines) == 0 && len(file.Hunks) == 0 {
		return nil, nil
	}

	// Create a single entry for the file with low confidence
	return []impact.ChangedSymbol{fileLevelEntry(file, filePath, allLines, "file is not in the index")}, nil
}

// extractSymbolName extracts a readable name from a SCIP symbol ID
func extractSymbolName(symbolID string) string {
	// SCIP symbol format: scheme manager package descriptor name
	// Example: scip-go gomod github.com/foo/bar pkg.Function().
	parts := strings.Split(symbolID, " ")
	if len(parts) > 0 {
		// Get the last part which is usually the name
		last := parts[len(parts)-1]
		// Remove trailing (). for function symbols
		last = strings.TrimSuffix(last, "().")
		// Remove trailing () for functions without trailing dot
		last = strings.TrimSuffix(last, "()")
		// Remove trailing . for types
		last = strings.TrimSuffix(last, ".")
		return last
	}
	return symbolID
}

// mergeLines merges two line slices and returns unique sorted lines
func mergeLines(a, b []int) []int {
	seen := make(map[int]bool)
	for _, l := range a {
		seen[l] = true
	}
	for _, l := range b {
		seen[l] = true
	}
	result := make([]int, 0, len(seen))
	for l := range seen {
		result = append(result, l)
	}
	sort.Ints(result)
	return result
}

// uniqueLines returns unique sorted lines
func uniqueLines(lines []int) []int {
	if len(lines) == 0 {
		return lines
	}
	seen := make(map[int]bool)
	result := make([]int, 0, len(lines))
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			result = append(result, l)
		}
	}
	sort.Ints(result)
	return result
}
