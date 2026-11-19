package diff

import "strings"

// rustCode blanks what isn't code in Rust source, line by line: strings, raw strings,
// char literals and comments. Their brackets, dots and slashes say nothing about the
// code around them, `"../game"` and `"HEAD~1..HEAD"` least of all. What is blanked
// becomes spaces, so offsets stay where they were. A string or block comment that runs
// past a line carries over to the next.
type rustCode struct {
	quote   byte // '"' or '\'' inside a string or char literal
	raw     int  // Inside a raw string closed by `"` and this many `#`, -1 outside
	comment bool // Inside a block comment
}

func newRustCode() *rustCode { return &rustCode{raw: -1} }

// What each byte of a line is, as rustCode.kinds returns it. A string's quotes are code;
// what is between them is string.
const (
	rustKindCode    byte = 'c'
	rustKindString  byte = 's'
	rustKindComment byte = '/'
)

func (s *rustCode) line(text string) string {
	code, _ := s.kinds(text)
	return code
}

// rustCodeLine is one line with its strings and comments blanked, read on its own: a
// string or comment open from an earlier line isn't known.
func rustCodeLine(text string) string { return newRustCode().line(text) }

// RustCodeLine is rustCodeLine for the done check's own readers.
func RustCodeLine(text string) string { return rustCodeLine(text) }

// rustCodeText is a text of several lines, joined by newlines, blanked as one.
func rustCodeText(text string) string {
	code := newRustCode()
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = code.line(l)
	}
	return strings.Join(lines, "\n")
}

// kinds is line, and what each byte of the line was.
func (s *rustCode) kinds(text string) (string, []byte) {
	out := []byte(text)
	kind := make([]byte, len(text))
	for k := range kind {
		kind[k] = rustKindCode
	}
	blankAs := func(from, to int, as byte) {
		for k := from; k < to && k < len(out); k++ {
			out[k] = ' '
			kind[k] = as
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case s.comment:
			if strings.HasPrefix(text[i:], "*/") {
				blankAs(i, i+2, rustKindComment)
				s.comment = false
				i++
			} else {
				blankAs(i, i+1, rustKindComment)
			}
		case s.raw >= 0:
			closing := `"` + strings.Repeat("#", s.raw)
			if strings.HasPrefix(text[i:], closing) {
				s.raw = -1
				i += len(closing) - 1 // The closing quote stays, as a plain string's does
			} else {
				blankAs(i, i+1, rustKindString)
			}
		case s.quote != 0:
			if c == '\\' {
				blankAs(i, i+2, rustKindString)
				i++
			} else if c == s.quote {
				s.quote = 0
			} else {
				blankAs(i, i+1, rustKindString)
			}
		case strings.HasPrefix(text[i:], "//"):
			blankAs(i, len(text), rustKindComment)
			return string(out), kind
		case strings.HasPrefix(text[i:], "/*"):
			s.comment = true
			blankAs(i, i+2, rustKindComment)
			i++
		case c == '"':
			s.quote = c
		case c == '\'' && isRustCharLiteral(text, i):
			s.quote = c
		case (c == 'r' || c == 'b') && (i == 0 || !isIdentByte(text[i-1])):
			if n, ok := rawStringStart(text[i:]); ok {
				s.raw = strings.Count(text[i:i+n], "#")
				i += n - 1
			}
		}
	}
	return string(out), kind
}

// rawStringStart returns the length of a raw string's opening, `r"`, `r#"` or `br#"`.
func rawStringStart(text string) (int, bool) {
	i := 0
	if strings.HasPrefix(text, "br") {
		i = 2
	} else if strings.HasPrefix(text, "r") {
		i = 1
	} else {
		return 0, false
	}
	for i < len(text) && text[i] == '#' {
		i++
	}
	if i < len(text) && text[i] == '"' {
		return i + 1, true
	}
	return 0, false
}
