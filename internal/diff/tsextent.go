package diff

import (
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// maxTSValueLines bounds how far a TypeScript value is read: past it, the extent stays
// unknown.
const maxTSValueLines = 10000

// withTSValueExtents gives each module-level TypeScript const, let and var the lines
// its initializer spans. scip-typescript gives such a definition an EnclosingRange only
// when the value is a function; for `export const schema = z.object({…})` the index
// knows the line of the name and nothing else, so a key added further down belongs to
// no definition. lines is the index's side of the file. The definitions read are
// copies: the document may be shared.
func withTSValueExtents(defs []*SymbolDefInfo, lines map[int]string) []*SymbolDefInfo {
	if len(lines) == 0 {
		return defs
	}
	var src *tsSource
	out, copied := defs, false
	for i, def := range defs {
		if def.Precision != scip.BoundsPoint || !isTSModuleValue(def.Symbol) {
			continue
		}
		if src == nil {
			src = newTSSource(lines)
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

// isTSModuleValue says whether a scip-typescript symbol names a module-level value:
// a term directly under the file (“ `lib.ts`/name. ”), not a type, a member, a
// function, a parameter or an object key.
func isTSModuleValue(symbol string) bool {
	if !strings.HasPrefix(symbol, "scip-typescript ") {
		return false
	}
	at := strings.LastIndex(symbol, "`/")
	if at < 0 {
		return false
	}
	descriptor := symbol[at+2:]
	return len(descriptor) > 1 && strings.HasSuffix(descriptor, ".") &&
		!strings.ContainsAny(descriptor[:len(descriptor)-1], "#().:/[]`")
}

// tsSource is one side of a TypeScript file as a single string, for a scan across lines.
type tsSource struct {
	text   string
	starts map[int]int // Line number to the offset it starts at
	lines  map[int]string
	last   int
}

func newTSSource(lines map[int]string) *tsSource {
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
	return &tsSource{text: b.String(), starts: starts, lines: lines, last: last}
}

var (
	tsVariableRe = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(?:const|let|var)\s`)
	// tsStatementRe matches a line that starts a statement at the left margin. Inside
	// an initializer nothing does: the value was read too far.
	tsStatementRe = regexp.MustCompile(`^(?:export|import|const|let|var|function|async function|class|interface|type|enum|declare)\s`)
	// tsContinuationRe matches a line that carries on the expression before it.
	tsContinuationRe = regexp.MustCompile(`^\s*(?:[.?:|&+*=,)\]}<>%^/-]|(?:as|satisfies|in|instanceof|extends)\b)`)
)

// valueEnd returns the last line of the variable statement on line: where its
// initializer is closed and neither that line nor the next carries it on. Zero if the
// line declares no variable, assigns nothing, or the value runs past what the side
// shows.
func (s *tsSource) valueEnd(line int) int {
	start, ok := s.starts[line]
	if !ok || !tsVariableRe.MatchString(s.lines[line]) {
		return 0
	}
	text := s.text[start:]
	end, current := 0, line
	assigned := false
	var last, beforeLast byte // Last two bytes of code on the current line; '"' for a string
	prev := -1
	tsScan(text, func(i, depth int) bool {
		if i > prev+1 {
			skipped := text[prev+1 : i] // A string, template, regex or comment tsScan didn't visit
			current += strings.Count(skipped, "\n")
			if c := skipped[0]; c == '"' || c == '`' || c == '\'' || (c == '/' && !strings.HasPrefix(skipped, "//") && !strings.HasPrefix(skipped, "/*")) {
				beforeLast, last = last, '"'
			}
		}
		prev = i
		c := text[i]
		switch {
		case c == '\n':
			carriesOn := depth > 0 || tsLineContinues(beforeLast, last) || s.nextContinues(current)
			if !carriesOn {
				if assigned {
					end = current
				}
				return false
			}
			if !assigned && depth == 0 && !tsLineContinues(beforeLast, last) {
				return false
			}
			current++
			if current-line > maxTSValueLines {
				return false
			}
			beforeLast, last = 0, 0
			return true
		case depth == 0 && c == ';':
			if assigned {
				end = current
			}
			return false
		case depth == 0 && (c == ')' || c == '}' || c == ']'):
			return false // Closes something the statement didn't open
		case depth == 0 && c == '=' && !assigned:
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			before := byte(0)
			if i > 0 {
				before = text[i-1]
			}
			if next != '=' && next != '>' && strings.IndexByte("=!<>", before) < 0 {
				assigned = true
			}
		}
		if c != ' ' && c != '\t' && c != '\r' {
			beforeLast, last = last, c
		}
		return true
	})
	for l := line; l <= end; l++ {
		text, shown := s.lines[l]
		if !shown {
			return 0
		}
		if l > line && tsStatementRe.MatchString(text) {
			return 0
		}
	}
	return end
}

// nextContinues says whether the next line with code on it starts with something
// that can only carry on an expression: `.object(`, `? a`, `as const`.
func (s *tsSource) nextContinues(line int) bool {
	for l := line + 1; l <= s.last; l++ {
		text := strings.TrimSpace(s.lines[l])
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		if strings.HasPrefix(text, "/*") || strings.HasPrefix(text, "++") || strings.HasPrefix(text, "--") {
			return false
		}
		return tsContinuationRe.MatchString(text)
	}
	return false
}

// tsLineContinues says whether a line ending in these two bytes of code goes on: no
// statement ends in an operator, a comma or an arrow. A lone `>` closes type
// arguments, as in `} as Record<string, string>`.
func tsLineContinues(beforeLast, last byte) bool {
	switch last {
	case 0:
		return false
	case '>':
		return beforeLast == '='
	case '+', '-':
		return beforeLast != last // `i++` ends a statement
	}
	return strings.IndexByte("*/%&|^<=!,.:?(", last) >= 0
}

// tsScan walks TypeScript source outside strings, template literals, regex literals
// and comments, calling visit with each byte's index and the bracket depth before it.
// visit returns false to stop. A newline is visited unless a template literal or a
// block comment holds it.
func tsScan(s string, visit func(i, depth int) bool) {
	depth := 0
	var last byte // Last byte of code seen, to tell a regex from a division
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\'':
			i = tsStringEnd(s, i)
			last = '"'
			continue
		case c == '`':
			i = tsTemplateEnd(s, i)
			last = '"'
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
			continue // The newline ending the comment is visited
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += end + 3
			} else {
				i = len(s)
			}
			continue
		case c == '/' && tsRegexMayStart(last):
			if end := tsRegexEnd(s, i); end > i {
				i = end
				last = '"'
				continue
			}
		}
		if !visit(i, depth) {
			return
		}
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			last = c
		}
	}
}

// tsStringEnd returns the index of the quote closing the string opened at s[i]. A
// string doesn't cross a line: an apostrophe in JSX text ends at the newline before it.
func tsStringEnd(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case s[i]:
			return j
		case '\n':
			return j - 1
		}
	}
	return len(s)
}

// tsTemplateEnd returns the index of the backtick closing the template literal opened
// at s[i], past every `${…}` in it and the strings and templates those hold.
func tsTemplateEnd(s string, i int) int {
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\':
			j++
		case s[j] == '`':
			return j
		case s[j] == '$' && j+1 < len(s) && s[j+1] == '{':
			depth := 0
			for j++; j < len(s); j++ {
				switch s[j] {
				case '{':
					depth++
				case '}':
					depth--
				case '"', '\'':
					j = tsStringEnd(s, j)
				case '`':
					j = tsTemplateEnd(s, j)
				}
				if depth == 0 {
					break
				}
			}
		}
	}
	return len(s)
}

// tsRegexMayStart says whether a `/` after this byte of code opens a regex literal
// and is no division: after an operator or an opening bracket, never after a value.
func tsRegexMayStart(last byte) bool {
	return last == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", last) >= 0
}

// tsRegexEnd returns the index of the `/` closing the regex literal opened at s[i],
// or i if the line has none.
func tsRegexEnd(s string, i int) int {
	class := false
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '\n':
			return i
		case '[':
			class = true
		case ']':
			class = false
		case '/':
			if !class {
				return j
			}
		}
	}
	return i
}
