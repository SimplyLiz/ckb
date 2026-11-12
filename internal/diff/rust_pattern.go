package diff

import (
	"regexp"
	"strings"
)

// Where a name sits inside a match arm's pattern, and whether another arm takes
// whatever is at that place. `Some(E::A) =>` is at step 0 of `Some`; `Some(_) =>` and
// `Some(x) =>` cover it, `None =>` and `Ok(_) =>` don't.

// rustStep is one step from a pattern into one of its parts: the n-th element of a
// tuple, tuple struct or slice, or a struct pattern's field.
type rustStep struct {
	ctor  string // The constructor's last path segment, "" for a plain tuple
	kind  byte   // '(', '[' or '{'
	index int
	field string
}

// rustPat is a parsed pattern. Offsets are into the alternative's text.
type rustPat struct {
	start, end int
	headEnd    int // End of the path or token before any parentheses or braces
	head       string
	kind       byte // 0 for a token, '(' '[' '{' for the parts, '|' for alternatives
	parts      []*rustPat
	fields     map[string]*rustPat
	rest       int // Index of `..` among the parts, -1 for none
	hasRest    bool
}

var rustWildRe = regexp.MustCompile(`^(?:_|(?:ref\s+)?(?:mut\s+)?[a-z_][a-z0-9_]*)$`)

func (p *rustPat) wild() bool { return p.kind == 0 && rustWildRe.MatchString(p.head) }

// text is the source between two positions, lines joined by a newline.
func (m *parsedRustMatch) text(from, to rustPos) string {
	if from.line == to.line {
		return m.lines[from.line-1][from.col:to.col]
	}
	var b strings.Builder
	b.WriteString(m.lines[from.line-1][from.col:])
	for l := from.line + 1; l < to.line; l++ {
		b.WriteByte('\n')
		b.WriteString(m.lines[l-1])
	}
	b.WriteByte('\n')
	b.WriteString(m.lines[to.line-1][:to.col])
	return b.String()
}

// armPatterns splits an arm's pattern into its top-level alternatives, without the
// guard. guardAt says where the guard starts, -1 for an arm without one.
func armPatterns(text string) (alts []string, offsets []int, guardAt int) {
	guardAt = -1
	depth, from := 0, 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch {
		case c == '"' || c == '\'' && isRustCharLiteral(text, i):
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case depth == 0 && c == '|':
			alts, offsets = append(alts, text[from:i]), append(offsets, from)
			from = i + 1
		case depth == 0 && strings.HasPrefix(text[i:], "if") && (i == 0 || !isIdentByte(text[i-1])) && (i+2 == len(text) || !isIdentByte(text[i+2])):
			guardAt = i
			return append(alts, text[from:i]), append(offsets, from), guardAt
		}
	}
	return append(alts, text[from:]), append(offsets, from), guardAt
}

// parseRustPattern reads one pattern, alternatives included, from s at i up to one of
// the stop bytes at its own depth.
func parseRustPattern(s string, i int, stop string) (*rustPat, int) {
	first, i := parseRustPatternOne(s, i, stop+"|")
	if i >= len(s) || s[i] != '|' {
		return first, i
	}
	or := &rustPat{start: first.start, kind: '|', parts: []*rustPat{first}, rest: -1}
	for i < len(s) && s[i] == '|' {
		var alt *rustPat
		alt, i = parseRustPatternOne(s, i+1, stop+"|")
		or.parts = append(or.parts, alt)
	}
	or.end, or.headEnd = i, or.start
	return or, i
}

func parseRustPatternOne(s string, i int, stop string) (*rustPat, int) {
	for i < len(s) && isRustSpace(s[i]) {
		i++
	}
	// A reference pattern is the pattern it points to.
	if i < len(s) && s[i] == '&' {
		i++
		if strings.HasPrefix(s[i:], "mut ") {
			i += 4
		}
		return parseRustPatternOne(s, i, stop)
	}
	p := &rustPat{start: i, rest: -1}
	j := i
	for j < len(s) && !strings.ContainsRune("(){}[],@"+stop, rune(s[j])) {
		j++
	}
	p.head, p.headEnd = strings.TrimSpace(s[i:j]), j
	if j < len(s) && s[j] == '@' {
		// `x @ Some(_)` is what follows the binding.
		return parseRustPatternOne(s, j+1, stop)
	}
	if j >= len(s) || !strings.ContainsRune("([{", rune(s[j])) {
		p.end = j
		return p, j
	}
	p.kind = s[j]
	closing := map[byte]byte{'(': ')', '[': ']', '{': '}'}[p.kind]
	i = j + 1
	if p.kind == '{' {
		p.fields = make(map[string]*rustPat)
	}
	for i < len(s) {
		for i < len(s) && (isRustSpace(s[i]) || s[i] == ',') {
			i++
		}
		if i >= len(s) || s[i] == closing {
			break
		}
		if strings.HasPrefix(s[i:], "..") && (i+2 >= len(s) || !isIdentByte(s[i+2]) && s[i+2] != '=') {
			p.hasRest, p.rest = true, len(p.parts)
			i += 2
			continue
		}
		if p.kind == '{' {
			k := i
			for k < len(s) && !strings.ContainsRune(":,}", rune(s[k])) {
				k++
			}
			name := strings.TrimSpace(s[i:k])
			name = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(name, "ref "), "mut "))
			if k < len(s) && s[k] == ':' {
				var field *rustPat
				field, i = parseRustPattern(s, k+1, ",}")
				p.fields[name] = field
			} else {
				p.fields[name] = &rustPat{start: i, end: k, headEnd: k, head: "_", rest: -1} // Shorthand binds it
				i = k
			}
			continue
		}
		var part *rustPat
		part, i = parseRustPattern(s, i, ","+string(closing))
		p.parts = append(p.parts, part)
	}
	if i < len(s) {
		i++
	}
	p.end = i
	return p, i
}

func isRustSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func lastSegment(path string) string {
	path = strings.TrimSpace(path)
	if at := strings.LastIndex(path, "::"); at >= 0 {
		return strings.TrimSpace(path[at+2:])
	}
	return path
}

// find returns the steps from p to the pattern whose path holds offset at.
func (p *rustPat) find(at int) ([]rustStep, bool) {
	if at < p.start || at >= p.end && p.end > p.start {
		return nil, false
	}
	if p.kind == '|' {
		for _, alt := range p.parts {
			if steps, ok := alt.find(at); ok {
				return steps, true
			}
		}
		return nil, false
	}
	if at < p.headEnd {
		return nil, true
	}
	step := rustStep{ctor: lastSegment(p.head), kind: p.kind}
	if p.kind == '{' {
		for name, field := range p.fields {
			if steps, ok := field.find(at); ok {
				step.field = name
				return append([]rustStep{step}, steps...), true
			}
		}
		return nil, false
	}
	for n, part := range p.parts {
		if steps, ok := part.find(at); ok {
			step.index = n
			if p.hasRest && n >= p.rest {
				step.index = -1 // Behind a `..`: the place isn't fixed
			}
			return append([]rustStep{step}, steps...), true
		}
	}
	return nil, false
}

// takes says whether the pattern matches whatever is at the end of the steps.
func (p *rustPat) takes(steps []rustStep) bool {
	if p == nil {
		return false
	}
	if p.wild() {
		return true
	}
	if p.kind == '|' {
		for _, alt := range p.parts {
			if alt.takes(steps) {
				return true
			}
		}
		return false
	}
	if len(steps) == 0 {
		return false
	}
	step := steps[0]
	if p.kind != step.kind || lastSegment(p.head) != step.ctor {
		return false
	}
	if p.kind == '{' {
		field, ok := p.fields[step.field]
		if !ok {
			return p.hasRest // `..` leaves the field to anything
		}
		return field.takes(steps[1:])
	}
	if step.index < 0 {
		return true // Can't tell which part it lines up with: count it as taken
	}
	if p.hasRest && step.index >= p.rest {
		return true
	}
	if step.index >= len(p.parts) {
		return false
	}
	return p.parts[step.index].takes(steps[1:])
}

// nestedPath returns the steps from an arm's top-level alternative to the pattern the
// position is in. False when it isn't in a pattern, as in a guard.
func (m *parsedRustMatch) nestedPath(ref rustPos) ([]rustStep, bool) {
	for _, arm := range m.arms {
		if ref.before(arm.start) || !ref.before(arm.end) {
			continue
		}
		at := len(m.text(arm.start, ref))
		alts, offsets, guardAt := armPatterns(m.text(arm.start, arm.end))
		if guardAt >= 0 && at >= guardAt {
			return nil, false
		}
		for n, alt := range alts {
			if at < offsets[n] || at > offsets[n]+len(alt) {
				continue
			}
			p, _ := parseRustPattern(alt, 0, "")
			return p.find(at - offsets[n])
		}
		return nil, false
	}
	return nil, false
}

// covers says whether an arm without a guard takes whatever is at the end of the steps.
func (m *parsedRustMatch) covers(steps []rustStep) bool {
	for _, arm := range m.arms {
		alts, _, guardAt := armPatterns(m.text(arm.start, arm.end))
		if guardAt >= 0 {
			continue
		}
		for _, alt := range alts {
			if p, _ := parseRustPattern(alt, 0, ""); p.takes(steps) {
				return true
			}
		}
	}
	return false
}
