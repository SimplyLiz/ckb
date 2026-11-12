package diff

import (
	"regexp"
	"strings"
)

// A variant added to a Rust enum breaks every `match` over it that has no arm for the
// rest. The index has no symbol to show that: the new variant has no uses yet, and the
// enum's own declaration line may be outside every hunk. So both sides of the file are
// read whole, and the matches are found from the references of the old variants.

// RustEnumVariants returns the names of an enum's variants, in order. False means the
// file has no enum of that name with braces.
func RustEnumVariants(lines []string, name string) ([]string, bool) {
	re := regexp.MustCompile(`^\s*(pub(\s*\([^)]*\))?\s+)?enum\s+` + regexp.QuoteMeta(name) + `\b`)
	for i, text := range lines {
		m := re.FindStringIndex(text)
		if m == nil {
			continue
		}
		body, ok := bracedBody(lines, i, m[1])
		if !ok {
			return nil, false
		}
		body = rustBlockCommentRe.ReplaceAllString(body, "")
		var variants []string
		for _, entry := range splitTopLevel(rustAttrRe.ReplaceAllString(body, ""), ',') {
			if m := rustVariantRe.FindStringSubmatch(entry); m != nil {
				variants = append(variants, m[1])
			}
		}
		return variants, true
	}
	return nil, false
}

var rustVariantRe = regexp.MustCompile(`^\s*([A-Za-z_]\w*)`)

// RustMatch is a `match` expression one of whose arms names a type.
type RustMatch struct {
	Line     int  // The line with the `match` keyword, 1-based
	CatchAll bool // An arm such as `_ =>` or `other =>` takes whatever the others don't
	// Nested says the name sits inside another pattern, as in `Some(E::A)`. Covered
	// says an arm without a guard takes whatever is at that place: `Some(_)`, `Some(x)`.
	Nested, Covered bool
	patterns        []string
}

// Names says whether an arm's pattern spells the word.
func (m RustMatch) Names(word string) bool {
	for _, p := range m.patterns {
		if wordIndex(p, word) >= 0 {
			return true
		}
	}
	return false
}

// RustMatchAt returns the match whose arm pattern the name on a line starts, as in
// `DraftError::Conflict(m) =>`, or sits in, as in `Some(E::A) =>`, which is Nested. A
// name in an arm's guard or body isn't one.
func RustMatchAt(lines []string, line, column int, name string) (RustMatch, bool) {
	at := nameIndex(rawLine(lines, line), column, name)
	if at < 0 {
		return RustMatch{}, false
	}
	ref := rustPos{line, at}
	for l := line; l >= 1 && l > line-maxRustMatchLines; l-- {
		text := rawLine(lines, l)
		for _, m := range rustMatchKeywordRe.FindAllStringIndex(text, -1) {
			start := rustPos{l, m[0]}
			if !start.before(ref) {
				continue
			}
			parsed, ok := parseRustMatch(lines, start)
			if !ok || !parsed.end.after(ref) {
				continue // Ends before the name: not the match it is in
			}
			found := RustMatch{Line: l, CatchAll: parsed.catchAll(), patterns: parsed.patterns()}
			if parsed.armAt(ref) {
				return found, true
			}
			path, ok := parsed.nestedPath(ref)
			if !ok {
				return RustMatch{}, false
			}
			// `&E::A` and `x @ E::A` start the arm all the same.
			found.Nested = len(path) > 0
			found.Covered = found.Nested && parsed.covers(path)
			return found, true
		}
	}
	return RustMatch{}, false
}

const maxRustMatchLines = 300

var (
	rustMatchKeywordRe = regexp.MustCompile(`\bmatch\b`)
	rustBindingRe      = regexp.MustCompile(`^(ref\s+)?(mut\s+)?[a-z_][a-z0-9_]*$`)
)

type rustPos struct{ line, col int }

func (p rustPos) before(q rustPos) bool {
	return p.line < q.line || p.line == q.line && p.col < q.col
}

func (p rustPos) after(q rustPos) bool { return q.before(p) }

type rustArm struct {
	start, end   rustPos   // Of the pattern, up to the `=>`
	alternatives []rustPos // Where each top-level alternative of the pattern starts
	pattern      strings.Builder
}

type parsedRustMatch struct {
	lines []string
	arms  []*rustArm
	end   rustPos
}

var rustPathPrefixRe = regexp.MustCompile(`^(?:[A-Za-z_]\w*\s*::\s*)*$`)

// armAt says whether the position starts a top-level alternative of an arm's pattern,
// past nothing but a path: `E::A`, `crate::x::E::A`, `| E::B`.
func (m *parsedRustMatch) armAt(p rustPos) bool {
	for _, arm := range m.arms {
		if p.before(arm.start) || !p.before(arm.end) {
			continue
		}
		for _, alt := range arm.alternatives {
			if alt.line == p.line && alt.col <= p.col && rustPathPrefixRe.MatchString(m.lines[p.line-1][alt.col:p.col]) {
				return true
			}
		}
		return false
	}
	return false
}

func (m *parsedRustMatch) catchAll() bool {
	for _, arm := range m.arms {
		p := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(arm.pattern.String()), "|"))
		if p == "_" || rustBindingRe.MatchString(p) {
			return true
		}
	}
	return false
}

func (m *parsedRustMatch) patterns() []string {
	out := make([]string, len(m.arms))
	for i, arm := range m.arms {
		out[i] = arm.pattern.String()
	}
	return out
}

// isRustCharLiteral says whether the quote at i opens a char literal, 'a' or '\n',
// rather than a lifetime.
func isRustCharLiteral(text string, i int) bool {
	if i+2 < len(text) && text[i+1] == '\\' {
		return true
	}
	return i+2 < len(text) && text[i+2] == '\''
}

// parseRustMatch reads the match starting at its keyword: each arm's pattern and where it
// is, and where the match ends. Strings and comments are skipped.
func parseRustMatch(lines []string, start rustPos) (*parsedRustMatch, bool) {
	m := &parsedRustMatch{lines: lines}
	depth := 0
	inBody := false   // Past the match's `{`
	inPattern := true // In an arm's pattern, not its body
	bodyStarted := false
	blockArm := false // The arm's body is a block, which ends it without a comma
	altPending := true
	var arm *rustArm
	var quote byte
	inBlockComment := false

	newArm := func() { arm, inPattern, bodyStarted, blockArm, altPending = nil, true, false, false, true }

	for l := start.line; l <= len(lines) && l < start.line+maxRustMatchLines; l++ {
		text := lines[l-1]
		from := 0
		if l == start.line {
			from = start.col + len("match")
		}
		for i := from; i < len(text); i++ {
			c := text[i]
			if inBlockComment {
				if c == '*' && i+1 < len(text) && text[i+1] == '/' {
					inBlockComment = false
					i++
				}
				continue
			}
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
			if c == '/' && i+1 < len(text) && text[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
			if c == '"' || c == '\'' && isRustCharLiteral(text, i) {
				quote = c
			}
			pos := rustPos{l, i}
			space := c == ' ' || c == '\t'

			if !inBody {
				switch c {
				case '(', '[':
					depth++
				case ')', ']', '}':
					depth--
				case '{':
					if depth == 0 {
						inBody, depth = true, 1
						newArm()
					} else {
						depth++
					}
				case ';':
					if depth == 0 {
						return nil, false // `match` as a word, not an expression
					}
				}
				continue
			}

			if inPattern && depth == 1 {
				if c == '=' && i+1 < len(text) && text[i+1] == '>' {
					if arm != nil {
						arm.end = pos
					}
					inPattern = false
					i++
					continue
				}
				if c == '}' {
					m.end = pos
					return m, true
				}
				if arm == nil && !space && c != ',' {
					arm = &rustArm{start: pos}
					m.arms = append(m.arms, arm)
				}
				if arm != nil {
					if c == '|' {
						altPending = true
					} else if altPending && !space {
						arm.alternatives = append(arm.alternatives, pos)
						altPending = false
					}
				}
			}
			if !inPattern && depth == 1 && !bodyStarted && !space {
				bodyStarted, blockArm = true, c == '{'
			}

			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
				if depth == 0 {
					m.end = pos
					return m, true
				}
				if depth == 1 && c == '}' && blockArm && !inPattern {
					newArm()
					continue
				}
			case ',':
				if depth == 1 && !inPattern {
					newArm()
					continue
				}
			}
			if inPattern && arm != nil {
				arm.pattern.WriteByte(c)
			}
		}
		if inPattern && arm != nil {
			arm.pattern.WriteByte(' ')
		}
	}
	return nil, false
}
