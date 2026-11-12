package diff

import (
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// maxGoValueLines bounds how far a Go value is read: past it, the extent stays unknown.
const maxGoValueLines = 10000

// withGoValueExtents gives each package-level Go var and const with an initializer the
// lines its initializer spans. scip-go gives them no EnclosingRange, so the index only
// knows the line of the name, and a changed line further into `var cmd = &Command{…}`
// belongs to no definition. lines is the index's side of the file. The definitions
// read are copies: the document may be shared.
func withGoValueExtents(defs []*SymbolDefInfo, lines map[int]string) []*SymbolDefInfo {
	if len(lines) == 0 {
		return defs
	}
	var src *goSource
	out, copied := defs, false
	for i, def := range defs {
		if def.Precision != scip.BoundsPoint || !isGoPackageValue(def.Symbol) {
			continue
		}
		if src == nil {
			src = newGoSource(lines)
		}
		end := src.valueEnd(def.DefLine)
		if end <= def.EndLine {
			continue
		}
		if !copied {
			out, copied = append([]*SymbolDefInfo(nil), defs...), true
		}
		extended := *def
		extended.EndLine = end
		out[i] = &extended
	}
	return out
}

// isGoPackageValue says whether a scip-go symbol names a package-level var or const:
// a term (`pkg/name.`), not a type, method, field or function.
func isGoPackageValue(symbol string) bool {
	descriptor := symbol[strings.LastIndexByte(symbol, '/')+1:]
	return strings.HasSuffix(descriptor, ".") && !strings.ContainsAny(descriptor, "#()")
}

// goSource is one side of a Go file as a single string, for a scan across lines.
type goSource struct {
	text   string
	starts map[int]int // Line number to the offset it starts at
	lines  map[int]string
}

func newGoSource(lines map[int]string) *goSource {
	last := 0
	for line := range lines {
		last = max(last, line)
	}
	var b strings.Builder
	starts := make(map[int]int, last)
	for line := 1; line <= last; line++ {
		starts[line] = b.Len()
		b.WriteString(lines[line]) // A line the side doesn't show is empty, and checked below
		b.WriteByte('\n')
	}
	return &goSource{text: b.String(), starts: starts, lines: lines}
}

// valueEnd returns the last line of the value spec on line, `x = …` or `var x T = …`:
// where its initializer is closed and the line doesn't continue it. Zero if the line
// assigns nothing, or the value runs past what the side shows.
func (s *goSource) valueEnd(line int) int {
	start, ok := s.starts[line]
	if !ok {
		return 0
	}
	text := s.text[start:]
	end, current := 0, line
	assigned := false
	var last byte // Last byte of code on the current line; '"' for a string
	prev := -1
	goScan(text, func(i, depth int) bool {
		if i > prev+1 {
			skipped := text[prev+1 : i] // A string, rune or comment goScan didn't visit
			current += strings.Count(skipped, "\n")
			if skipped[0] == '"' || skipped[0] == '`' || skipped[0] == '\'' {
				last = '"'
			}
		}
		prev = i
		c := text[i]
		switch {
		case c == '\n':
			if depth > 0 || goLineContinues(last) {
				if !assigned {
					return false // Not a value spec: the name's line ends in something else
				}
				current++
				if current-line > maxGoValueLines {
					return false
				}
				last = 0
				return true
			}
			if assigned {
				end = current
			}
			return false
		case depth == 0 && (c == ')' || c == '}' || c == ']'):
			if assigned {
				end = current // The group's closing paren on the value's last line
			}
			return false
		case depth == 0 && c == '=' && !assigned:
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			if next != '=' && strings.IndexByte("=!<>:", last) < 0 {
				assigned = true
			}
		}
		if c != ' ' && c != '\t' && c != '\r' {
			last = c
		}
		return true
	})
	for l := line; l <= end; l++ {
		if _, shown := s.lines[l]; !shown {
			return 0
		}
	}
	return end
}

// goLineContinues says whether a line ending in this byte goes on: Go inserts no
// semicolon after an operator or a comma.
func goLineContinues(last byte) bool {
	return last != 0 && strings.IndexByte("+-*/%&|^<>=!,.:", last) >= 0
}
