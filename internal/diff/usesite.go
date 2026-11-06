package diff

import (
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// This file answers one question about a use of a symbol whose declaration changed:
// did the diff touch that use? The index says where the uses were, the diff says which
// lines moved, and a file merely being in the diff says nothing about either.

// UseState is what the diff did to one use of a symbol.
type UseState int

const (
	// UseOpen means the diff left the use as it was.
	UseOpen UseState = iota
	// UseUpdated means the diff changed the use. Whether correctly is another matter:
	// nothing here compiles.
	UseUpdated
	// UseGone means the file no longer mentions the symbol at all.
	UseGone
)

// UseSite is one place the index says a symbol is used.
type UseSite struct {
	Line   int // 1-indexed, on whichever side of the diff the index covers
	Column int // 1-indexed, 0 if unknown
	Name   string
	// Literal means the use is a struct literal or pattern, which ends at its brace.
	Literal bool
}

// UseResult is the classification of a UseSite.
type UseResult struct {
	State UseState
	Line  int    // Line of the use in the current file, where there is one
	Code  string // That line, without surrounding whitespace
	// Unverified means the name isn't on the line the index gave, so the index is out
	// of date for this file and the position can't be trusted.
	Unverified bool
	// Import means the line only imports the name, which a changed signature leaves valid.
	Import bool
}

// maxCallSpan bounds how far a call's parentheses are followed.
const maxCallSpan = 40

// ClassifyUse decides what the diff did to a use. file is nil for a file outside the
// diff. oldLines and newLines are the file on either side of the diff; for a file
// outside the diff they are the same text and oldLines may be nil.
func ClassifyUse(file *impact.ChangedFile, oldLines, newLines []string, site UseSite) UseResult {
	if file != nil && file.Deleted {
		return UseResult{State: UseGone}
	}
	if file == nil {
		return classifyUnchanged(newLines, site)
	}

	blocks := fileBlocks(file)
	switch {
	case lineMentions(oldLines, site.Line, site.Name):
		return classifyOldSide(blocks, oldLines, newLines, site)
	case lineMentions(newLines, site.Line, site.Name):
		return classifyNewSide(blocks, oldLines, newLines, site)
	}
	if !mentionsAnywhere(newLines, site.Name) {
		return UseResult{State: UseGone}
	}
	return UseResult{State: UseOpen, Line: site.Line, Code: lineAt(newLines, site.Line), Unverified: true}
}

func classifyUnchanged(lines []string, site UseSite) UseResult {
	if lineMentions(lines, site.Line, site.Name) {
		return openUse(lines, site.Line)
	}
	if !mentionsAnywhere(lines, site.Name) {
		return UseResult{State: UseGone}
	}
	return UseResult{State: UseOpen, Line: site.Line, Code: lineAt(lines, site.Line), Unverified: true}
}

// classifyOldSide handles the usual case: the index was built before the diff.
func classifyOldSide(blocks []impact.ChangeBlock, oldLines, newLines []string, site UseSite) UseResult {
	newLine, changed := oldToNewLine(blocks, site.Line)
	if changed {
		return UseResult{State: UseUpdated, Line: newLine}
	}
	// A new argument on a line of its own updates this call without touching the name.
	oldEnd := siteEnd(oldLines, site.Line, site)
	if anyOldLineChanged(blocks, site.Line, oldEnd) {
		return UseResult{State: UseUpdated, Line: newLine}
	}
	newEnd := siteEnd(newLines, newLine, site)
	if anyNewLineChanged(blocks, newLine, newEnd) {
		return UseResult{State: UseUpdated, Line: newLine}
	}
	return openUse(newLines, newLine)
}

// classifyNewSide handles an index built after the diff, as a watcher does.
func classifyNewSide(blocks []impact.ChangeBlock, oldLines, newLines []string, site UseSite) UseResult {
	end := siteEnd(newLines, site.Line, site)
	if anyNewLineChanged(blocks, site.Line, end) {
		return UseResult{State: UseUpdated, Line: site.Line}
	}
	if oldLine, ok := newToOldLine(blocks, site.Line); ok {
		oldEnd := siteEnd(oldLines, oldLine, site)
		if anyOldLineChanged(blocks, oldLine, oldEnd) {
			return UseResult{State: UseUpdated, Line: site.Line}
		}
	}
	return openUse(newLines, site.Line)
}

func openUse(lines []string, line int) UseResult {
	return UseResult{State: UseOpen, Line: line, Code: lineAt(lines, line), Import: isImportLine(lines, line)}
}

// fileBlocks flattens a file's hunks into its changed blocks, in file order.
func fileBlocks(file *impact.ChangedFile) []impact.ChangeBlock {
	var blocks []impact.ChangeBlock
	for i := range file.Hunks {
		blocks = append(blocks, hunkBlocks(&file.Hunks[i])...)
	}
	return blocks
}

// oldToNewLine maps an old-side line to the new side. changed means the line itself
// was removed or replaced, so the result is only where its replacement starts.
func oldToNewLine(blocks []impact.ChangeBlock, line int) (newLine int, changed bool) {
	delta := 0
	for i := range blocks {
		block := &blocks[i]
		if line < block.OldLine {
			break
		}
		if line < block.OldLine+len(block.Removed) {
			return block.NewLine, true
		}
		delta += len(block.Added) - len(block.Removed)
	}
	return line + delta, false
}

// newToOldLine maps a new-side line back, if it isn't one the diff added.
func newToOldLine(blocks []impact.ChangeBlock, line int) (int, bool) {
	delta := 0
	for i := range blocks {
		block := &blocks[i]
		if line < block.NewLine {
			break
		}
		if line < block.NewLine+len(block.Added) {
			return 0, false
		}
		delta += len(block.Removed) - len(block.Added)
	}
	return line + delta, true
}

func anyOldLineChanged(blocks []impact.ChangeBlock, from, to int) bool {
	for i := range blocks {
		block := &blocks[i]
		if len(block.Removed) > 0 && block.OldLine <= to && block.OldLine+len(block.Removed)-1 >= from {
			return true
		}
	}
	return false
}

func anyNewLineChanged(blocks []impact.ChangeBlock, from, to int) bool {
	for i := range blocks {
		block := &blocks[i]
		if len(block.Added) > 0 && block.NewLine <= to && block.NewLine+len(block.Added)-1 >= from {
			return true
		}
	}
	return false
}

// siteEnd returns the last line of a use, which is on line.
func siteEnd(lines []string, line int, site UseSite) int {
	if site.Literal {
		if end, _, ok := rustLiteral(lines, line, site.Column, site.Name); ok {
			return end
		}
		return line
	}
	return callEnd(lines, line, site.Column, site.Name)
}

// callEnd returns the last line of the call starting at the name on line, or line
// itself if the name isn't called there. It counts parentheses and skips strings; a
// rough reading is enough, since a wrong end only moves one use between two labels.
func callEnd(lines []string, line, column int, name string) int {
	text := rawLine(lines, line)
	start := nameIndex(text, column, name)
	if start < 0 {
		return line
	}
	rest := text[start+len(name):]
	open := callParen(rest)
	if open < 0 {
		return line
	}

	depth := 0
	var quote byte
	scan := rest[open:]
	for current := line; current <= line+maxCallSpan; current++ {
		if current != line {
			scan = rawLine(lines, current)
		}
		for i := 0; i < len(scan); i++ {
			c := scan[i]
			switch {
			case quote != 0:
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'' || c == '`':
				quote = c
			case c == '(':
				depth++
			case c == ')':
				depth--
				if depth == 0 {
					return current
				}
			}
		}
		// Only a raw string carries over a line end.
		if quote != '`' {
			quote = 0
		}
	}
	return line
}

// callParen finds the opening parenthesis right after a name, past any type arguments
// (`save<T>(`, `save::<T>(`). It is -1 if the name isn't being called.
func callParen(rest string) int {
	i := 0
	for i < len(rest) && rest[i] == ' ' {
		i++
	}
	if strings.HasPrefix(rest[i:], "::") {
		i += 2
	}
	if i < len(rest) && rest[i] == '<' {
		depth := 0
		for ; i < len(rest); i++ {
			if rest[i] == '<' {
				depth++
			} else if rest[i] == '>' {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
		}
	}
	if i < len(rest) && rest[i] == '(' {
		return i
	}
	return -1
}

var importPrefixes = []string{"import ", "import{", "use ", "pub use ", "from ", "export {", "export{", "export *", "#include"}

var (
	bareListEntry = regexp.MustCompile(`^[A-Za-z_$][\w$]*(\s+as\s+[A-Za-z_$][\w$]*)?,?$`)
	// A line of a Rust use tree: `a, b::{c, d as e},` or `place::{`.
	useTreeLine = regexp.MustCompile(`^[\w$:{}\s,*]+$`)
)

// isImportLine says whether a line only brings a name into scope, including an entry
// of a multi-line import list. A changed signature leaves those valid.
func isImportLine(lines []string, line int) bool {
	text := strings.TrimSpace(rawLine(lines, line))
	if hasImportPrefix(text) {
		return true
	}
	if !bareListEntry.MatchString(text) && !useTreeLine.MatchString(text) {
		return false
	}
	for current := line - 1; current >= 1 && current >= line-maxCallSpan; current-- {
		previous := strings.TrimSpace(rawLine(lines, current))
		if hasImportPrefix(previous) {
			return strings.Contains(previous, "{") || strings.Contains(previous, "(")
		}
		if previous != "" && !useTreeLine.MatchString(previous) {
			return false
		}
	}
	return false
}

func hasImportPrefix(text string) bool {
	for _, prefix := range importPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// nameIndex finds name as a whole word on the line, preferring the index's column.
func nameIndex(text string, column int, name string) int {
	if column > 0 && column-1 <= len(text) {
		if i := wordIndex(text[column-1:], name); i >= 0 {
			return column - 1 + i
		}
	}
	return wordIndex(text, name)
}

func wordIndex(text, name string) int {
	if name == "" {
		return -1
	}
	for offset := 0; offset <= len(text)-len(name); {
		i := strings.Index(text[offset:], name)
		if i < 0 {
			return -1
		}
		at := offset + i
		end := at + len(name)
		if (at == 0 || !isWordByte(text[at-1])) && (end == len(text) || !isWordByte(text[end])) {
			return at
		}
		offset = at + 1
	}
	return -1
}

func lineMentions(lines []string, line int, name string) bool {
	return wordIndex(rawLine(lines, line), name) >= 0
}

func mentionsAnywhere(lines []string, name string) bool {
	for _, text := range lines {
		if wordIndex(text, name) >= 0 {
			return true
		}
	}
	return false
}

func rawLine(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

const maxUseCodeLength = 200

func lineAt(lines []string, line int) string {
	text := strings.TrimSpace(rawLine(lines, line))
	if len(text) > maxUseCodeLength {
		cut := maxUseCodeLength
		for cut > 0 && text[cut]&0xC0 == 0x80 { // Don't split a UTF-8 sequence
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}
