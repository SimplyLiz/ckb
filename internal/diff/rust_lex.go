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

func (s *rustCode) line(text string) string {
	out := []byte(text)
	blank := func(from, to int) {
		for k := from; k < to && k < len(out); k++ {
			out[k] = ' '
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case s.comment:
			if strings.HasPrefix(text[i:], "*/") {
				s.comment = false
				blank(i, i+2)
				i++
			} else {
				out[i] = ' '
			}
		case s.raw >= 0:
			closing := `"` + strings.Repeat("#", s.raw)
			if strings.HasPrefix(text[i:], closing) {
				s.raw = -1
				i += len(closing) - 1 // The closing quote stays, as a plain string's does
			} else {
				out[i] = ' '
			}
		case s.quote != 0:
			if c == '\\' {
				blank(i, i+2)
				i++
			} else if c == s.quote {
				s.quote = 0
			} else {
				out[i] = ' '
			}
		case strings.HasPrefix(text[i:], "//"):
			blank(i, len(text))
			return string(out)
		case strings.HasPrefix(text[i:], "/*"):
			s.comment = true
			blank(i, i+2)
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
	return string(out)
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
