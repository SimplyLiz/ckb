package diff

import (
	"regexp"
	"strings"
)

// A `let` whose pattern names an enum's variant only compiles while that variant is
// the enum's only one: `let Proposal::Admit { id } = p;`. A second variant makes the
// pattern refutable (E0005). `if let`, `while let` and `let … else` are refutable on
// purpose and stay as they are.

// RustLetAt returns the line of the `let` statement whose pattern the name on a line
// sits in, nested or not. False for a name in the type or the initializer, for
// `if let` and `while let`, and for a `let … else`.
func RustLetAt(lines []string, line, column int, name string) (int, bool) {
	at := nameIndex(rawLine(lines, line), column, name)
	if at < 0 {
		return 0, false
	}
	ref := rustPos{line, at}
	for l := line; l >= 1 && l > line-maxRustLetLines; l-- {
		text := rawLine(lines, l)
		found := rustLetKeywordRe.FindAllStringIndex(text, -1)
		for k := len(found) - 1; k >= 0; k-- {
			start := rustPos{l, found[k][0]}
			if !start.before(ref) {
				continue
			}
			if !rustStatementStart(lines, start) {
				return 0, false // `if let`, `while let`, `&& let`
			}
			pattern := rustPos{l, found[k][1]}
			end, ok := rustLetPattern(lines, pattern)
			if !ok || !ref.before(end) {
				return 0, false // The name is past the pattern
			}
			if rustLetElse(lines, end) {
				return 0, false
			}
			m := &parsedRustMatch{lines: lines}
			p, _ := parseRustPattern(m.text(pattern, end), 0, "")
			if _, in := p.find(len(m.text(pattern, ref))); !in {
				return 0, false
			}
			return l, true
		}
	}
	return 0, false
}

const maxRustLetLines = 20

var rustLetKeywordRe = regexp.MustCompile(`\blet\b`)

// rustStatementStart says whether what comes before the keyword ends a statement or
// opens a block, which a `let` that is a statement of its own follows.
func rustStatementStart(lines []string, at rustPos) bool {
	text := strings.TrimRight(rawLine(lines, at.line)[:at.col], " \t")
	for l := at.line - 1; text == "" && l >= 1 && l > at.line-maxRustLetLines; l-- {
		text = rawLine(lines, l)
		if i := strings.Index(text, "//"); i >= 0 {
			text = text[:i]
		}
		text = strings.TrimRight(text, " \t")
	}
	if text == "" {
		return true
	}
	return strings.ContainsRune(";{}]", rune(text[len(text)-1])) || strings.HasSuffix(text, "=>")
}

// rustLetPattern finds where the pattern after `let` ends: at its type or its `=`.
// False for a statement this doesn't read to its end.
func rustLetPattern(lines []string, after rustPos) (rustPos, bool) {
	depth := 0
	for l := after.line; l <= len(lines) && l < after.line+maxRustLetLines; l++ {
		text := lines[l-1]
		from := 0
		if l == after.line {
			from = after.col
		}
		for i := from; i < len(text); i++ {
			c := text[i]
			if c == '/' && i+1 < len(text) && text[i+1] == '/' {
				break
			}
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			switch {
			case c == '(' || c == '[' || c == '{':
				depth++
			case c == ')' || c == ']' || c == '}':
				depth--
				if depth < 0 {
					return rustPos{}, false
				}
			case depth == 0 && c == ':' && next == ':':
				i++
			case depth == 0 && (c == ':' || c == ';' || c == '=' && next != '=' && next != '>'):
				return rustPos{l, i}, true
			}
		}
	}
	return rustPos{}, false
}

// rustLetElse says whether the statement from the pattern's end on is a `let … else`:
// an `else` at its own depth that doesn't follow a block, which `if … else` does.
func rustLetElse(lines []string, from rustPos) bool {
	depth := 0
	var quote byte
	last := byte(0) // The last byte that isn't space, at the statement's own depth
	for l := from.line; l <= len(lines) && l < from.line+maxRustMatchLines; l++ {
		text := lines[l-1]
		i := 0
		if l == from.line {
			i = from.col
		}
		for ; i < len(text); i++ {
			c := text[i]
			if quote != 0 {
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
				continue
			}
			if c == '/' && i+1 < len(text) && text[i+1] == '/' {
				break
			}
			switch {
			case c == '"' || c == '\'' && isRustCharLiteral(text, i):
				quote = c
			case c == '(' || c == '[' || c == '{':
				depth++
			case c == ')' || c == ']' || c == '}':
				depth--
				if depth == 0 {
					last = c
				}
				continue
			case depth == 0 && c == ';':
				return false
			case depth == 0 && strings.HasPrefix(text[i:], "else") && (i == 0 || !isIdentByte(text[i-1])) && (i+4 == len(text) || !isIdentByte(text[i+4])):
				return last != '}'
			}
			if depth == 0 && !isRustSpace(c) {
				last = c
			}
		}
	}
	return false
}
