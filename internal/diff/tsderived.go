package diff

import (
	"regexp"
	"strings"
)

// A TypeScript type can take its shape from a value: `z.infer<typeof schema>`,
// `ReturnType<typeof fn>`, `(typeof KEYS)[number]`, `typeof table.$inferSelect`. The
// index has a reference from the type to the value and nothing that says the type
// follows it. These read the text around such a reference.

var (
	tsTypeDeclRe  = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(?:type|interface)\s+([A-Za-z_$][\w$]*)`)
	tsValueDeclRe = regexp.MustCompile(`^(?:export\s+)?(?:declare\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	tsKeyRe       = regexp.MustCompile(`^\s*["']?([A-Za-z_$][\w$]*)["']?\s*:`)
	// tsFunctionValueRe matches an initializer that is a function: its body uses values
	// without taking their shape.
	tsFunctionValueRe = regexp.MustCompile(`^(?:async\b|function\b|\(|<|[A-Za-z_$][\w$]*\s*=>)`)
)

// maxTSDeclLookback bounds how far back the start of a declaration is looked for.
const maxTSDeclLookback = 200

// tsContinuesStatement says whether a line that starts at the left margin belongs to
// the statement above it: a closing bracket, a union member, a comment.
func tsContinuesStatement(text string) bool {
	return text == "" || strings.IndexByte(" \t})]|&>/*.?:", text[0]) >= 0
}

// TSTypeofAlias returns the type alias or interface whose declaration reads `typeof
// name` at this place: `type Profile = z.infer<typeof profileSchema>`. False if the
// name isn't under typeof there, or the statement is something else, such as a
// variable annotated with the type.
func TSTypeofAlias(lines []string, line, column int, name string) (string, bool) {
	text := rawLine(lines, line)
	at := nameIndex(text, column, name)
	if at < 0 {
		return "", false
	}
	before := strings.TrimRight(text[:at], " \t(")
	if lastWordOf(before) != "typeof" {
		return "", false
	}
	for l := line; l >= 1 && l > line-maxTSDeclLookback; l-- {
		current := rawLine(lines, l)
		if m := tsTypeDeclRe.FindStringSubmatch(current); m != nil {
			return m[1], true
		}
		if !tsContinuesStatement(current) {
			return "", false
		}
	}
	return "", false
}

// TSEnclosingValue returns the module-level const, let or var whose initializer holds
// this line, and the object key the line sits under, if it starts with one:
// `profile: profileSchema,` inside `export const projectSchema = z.object({…})` gives
// projectSchema and profile. A value that is a function is none: it uses what its body
// names, its type doesn't follow from it.
func TSEnclosingValue(lines []string, line int) (name, key string, ok bool) {
	for l := line; l >= 1 && l > line-maxTSDeclLookback; l-- {
		current := rawLine(lines, l)
		m := tsValueDeclRe.FindStringSubmatch(current)
		if m == nil {
			if tsContinuesStatement(current) {
				continue
			}
			return "", "", false
		}
		shown := make(map[int]string, len(lines)-l+1)
		for i := l; i <= len(lines); i++ {
			shown[i] = lines[i-1]
		}
		src := newTSSource(shown)
		if src.valueEnd(l) < line {
			return "", "", false
		}
		if tsFunctionValueRe.MatchString(strings.TrimSpace(src.initializer(l))) {
			return "", "", false
		}
		if l != line {
			if k := tsKeyRe.FindStringSubmatch(rawLine(lines, line)); k != nil {
				key = k[1]
			}
		}
		return m[1], key, true
	}
	return "", "", false
}

// initializer returns what follows the `=` of the variable statement on line, to the
// end of the text. Empty if nothing is assigned on that line or the one after.
func (s *tsSource) initializer(line int) string {
	text := s.text[s.starts[line]:]
	at := -1
	tsScan(text, func(i, depth int) bool {
		if depth != 0 || text[i] != '=' {
			return text[i] != ';' || depth != 0
		}
		next, before := byte(0), byte(0)
		if i+1 < len(text) {
			next = text[i+1]
		}
		if i > 0 {
			before = text[i-1]
		}
		if next == '=' || next == '>' || strings.IndexByte("=!<>", before) >= 0 {
			return true
		}
		at = i + 1
		return false
	})
	if at < 0 {
		return ""
	}
	return text[at:]
}

// TSRecordLiteral finds an object literal keyed by the named type: `: Record<Name, V> =
// {`, also under Partial or Readonly, or returned as one. A member added to the union
// leaves it a key short. It returns the literal's last line.
func TSRecordLiteral(lines []string, line, column int, name string) (end int, ok bool) {
	text := rawLine(lines, line)
	at := nameIndex(text, column, name)
	if at < 0 {
		return 0, false
	}
	before := strings.TrimRight(text[:at], " \t")
	if !strings.HasSuffix(before, "Record<") {
		return 0, false
	}
	// Close every `<` still open before the name, then expect `= {` or, for a return
	// type, `{` or `=> (`.
	rest := text[at+len(name):]
	open := strings.Count(text[:at], "<") - strings.Count(strings.ReplaceAll(text[:at], "=>", ""), ">")
	i := 0
	for ; i < len(rest) && open > 0; i++ {
		switch rest[i] {
		case '<':
			open++
		case '>':
			if i == 0 || rest[i-1] != '=' {
				open--
			}
		}
	}
	if open > 0 {
		return 0, false
	}
	after := strings.TrimLeft(rest[i:], " \t")
	switch {
	case strings.HasPrefix(after, "=>"):
		after = strings.TrimLeft(after[2:], " \t(")
	case strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "=="):
		after = strings.TrimLeft(after[1:], " \t")
	case strings.HasPrefix(after, "{") && strings.HasSuffix(strings.TrimRight(text[:strings.LastIndex(text[:at], "Record<")], " \t"), "):"):
	default:
		return 0, false
	}
	if !strings.HasPrefix(after, "{") {
		return 0, false
	}
	return braceEnd(lines, line, len(text)-len(after)), true
}

// TSLiteralKeyLine returns the line on which the literal spanning first..last sets the
// given key, or 0: `profile: {` in a literal of the type that holds the profile. Where
// the key comes up at several depths the outermost counts; in a function returning the
// type that is the returned literal's.
func TSLiteralKeyLine(lines []string, first, last int, key string) int {
	if key == "" || first < 1 || last > len(lines) || last < first {
		return 0
	}
	text := strings.Join(lines[first-1:last], "\n")
	found, foundDepth := 0, 0
	var before byte // Last byte of code ahead of the one visited: comments don't count
	tsScan(text, func(i, depth int) bool {
		previous := before
		if c := text[i]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			before = c
		}
		// A key follows `{` or `,`; anything else is a value, a ternary or a type.
		if depth < 1 || (previous != '{' && previous != ',') || (found > 0 && depth >= foundDepth) {
			return true
		}
		if !strings.HasPrefix(text[i:], key) || (i > 0 && isWordByte(text[i-1])) {
			return true
		}
		rest := text[i+len(key):]
		if rest != "" && isWordByte(rest[0]) {
			return true
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest != "" && strings.IndexByte(":,\n}", rest[0]) < 0 {
			return true
		}
		found, foundDepth = first+strings.Count(text[:i], "\n"), depth
		return true
	})
	return found
}

// maxTSConstructionLines bounds how far the construction of a value is read.
const maxTSConstructionLines = 2000

// TSLiteralsMissing looks at the object literals that build a value of the named type
// at this place: the one assigned or returned, each element of an array of them. With a
// path, it goes on to the literal each of them holds under those keys. It returns the
// lines of the literals that set none of the given keys and have no spread that could.
// read is false if nothing is built here in a way that can be read; a cast is read, and
// has nothing missing: the compiler accepts it short of a key.
func TSLiteralsMissing(lines []string, line, column int, name string, path, keys []string) (at []int, read bool) {
	first := rawLine(lines, line)
	start := nameIndex(first, column, name)
	if start < 0 || line < 1 {
		return nil, false
	}
	last := min(len(lines), line+maxTSConstructionLines)
	text := strings.Join(lines[line-1:last], "\n")
	before := strings.TrimRight(first[:start], " \t")
	switch lastWordOf(before) {
	case "as":
		return nil, true
	case "satisfies", "interface", "class", "type", "enum", "extends", "implements", "import", "typeof", "keyof", "new":
		return nil, false
	}

	i := start + len(name)
	skip := func() {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
	}
	if i < len(text) && text[i] == '<' {
		close := matchingBracket(text[i:], '<', '>')
		if close < 0 {
			return nil, false
		}
		i += close + 1
	}
	for strings.HasPrefix(text[i:], "[]") {
		i += 2
	}
	// A Record keyed by the name: on to the end of its type arguments.
	if strings.HasSuffix(before, "Record<") {
		for depth := 1; i < len(text) && depth > 0; i++ {
			switch text[i] {
			case '<':
				depth++
			case '>':
				if text[i-1] != '=' {
					depth--
				}
			}
		}
	}
	skip()

	var literals []int // Offsets of the `{` of each literal built here
	body := -1         // Offset of a function body's `{`
	switch {
	case strings.HasPrefix(text[i:], "=>"):
		i += 2
		skip()
		paren := false
		for i < len(text) && text[i] == '(' {
			paren = true
			i++
			skip()
		}
		switch {
		case i < len(text) && text[i] == '{' && !paren:
			body = i
		case i < len(text) && (text[i] == '{' || text[i] == '['):
			literals = tsLiteralsAt(text, i)
		default:
			return nil, false
		}
	case strings.HasPrefix(text[i:], "=") && !strings.HasPrefix(text[i:], "=="):
		i++
		for i < len(text) && strings.IndexByte(" \t\r\n", text[i]) >= 0 {
			i++
		}
		if i >= len(text) || (text[i] != '{' && text[i] != '[') {
			return nil, false
		}
		literals = tsLiteralsAt(text, i)
	case strings.HasPrefix(text[i:], "{") && strings.HasSuffix(before, ":") && strings.HasSuffix(strings.TrimRight(before[:len(before)-1], " \t"), ")"):
		body = i
	case strings.HasPrefix(text[i:], ">") && strings.HasSuffix(before, "<"):
		i++
		skip()
		if i >= len(text) || text[i] != '{' {
			return nil, false
		}
		literals = []int{i}
	default:
		return nil, false
	}
	if body >= 0 {
		literals = tsReturnedLiterals(text, body)
	}

	for _, key := range path {
		var inner []int
		for _, open := range literals {
			props, _ := tsObjectProps(text, open)
			if value, ok := props[key]; ok && value >= 0 {
				inner = append(inner, tsLiteralsAt(text, value)...)
			}
		}
		literals = inner
	}
	for _, open := range literals {
		props, spread := tsObjectProps(text, open)
		if spread {
			continue
		}
		for _, key := range keys {
			if _, ok := props[key]; !ok {
				at = append(at, line+strings.Count(text[:open], "\n"))
				break
			}
		}
	}
	return at, true
}

// tsLiteralsAt returns the object literals at an offset: the one that starts there, or
// each one that is an element of the array that does.
func tsLiteralsAt(text string, at int) []int {
	if at >= len(text) {
		return nil
	}
	switch text[at] {
	case '{':
		return []int{at}
	case '[':
		var elements []int
		rest := text[at:]
		var before byte
		tsScan(rest, func(i, depth int) bool {
			previous := before
			if c := rest[i]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				before = c
			}
			if depth == 1 && rest[i] == ']' {
				return false
			}
			if depth == 1 && rest[i] == '{' && (previous == '[' || previous == ',') {
				elements = append(elements, at+i)
			}
			return true
		})
		return elements
	}
	return nil
}

// tsReturnedLiterals returns the object literals a function body returns, the body
// opening at the given offset.
func tsReturnedLiterals(text string, open int) []int {
	var literals []int
	rest := text[open:]
	tsScan(rest, func(i, depth int) bool {
		if depth == 1 && rest[i] == '}' {
			return false
		}
		if !strings.HasPrefix(rest[i:], "return") || (i > 0 && isWordByte(rest[i-1])) {
			return true
		}
		j := i + len("return")
		if j < len(rest) && isWordByte(rest[j]) {
			return true
		}
		for j < len(rest) && strings.IndexByte(" \t\r\n(", rest[j]) >= 0 {
			j++
		}
		literals = append(literals, tsLiteralsAt(text, open+j)...)
		return true
	})
	return literals
}

// tsObjectProps reads the object literal opening at the given offset: each key it sets
// with the offset its value starts at (-1 for a shorthand or a method), and whether it
// spreads another object into itself.
func tsObjectProps(text string, open int) (props map[string]int, spread bool) {
	props = make(map[string]int)
	rest := text[open:]
	var before byte
	tsScan(rest, func(i, depth int) bool {
		previous := before
		if c := rest[i]; c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			before = c
		}
		if depth == 1 && rest[i] == '}' {
			return false
		}
		if depth != 1 || (previous != '{' && previous != ',') {
			return true
		}
		if strings.HasPrefix(rest[i:], "...") {
			spread = true
			return true
		}
		name := tsIdentRe.FindString(rest[i:])
		if name == "" {
			return true
		}
		j := i + len(name)
		for j < len(rest) && (rest[j] == ' ' || rest[j] == '\t') {
			j++
		}
		if j < len(rest) && rest[j] == ':' {
			j++
			for j < len(rest) && strings.IndexByte(" \t\r\n", rest[j]) >= 0 {
				j++
			}
			props[name] = open + j
		} else {
			props[name] = -1
		}
		return true
	})
	return props, spread
}

// TSIsPropertyWrite says whether the name on a line is a key an object literal sets,
// `key: value` or the shorthand `key,`, and not a read such as `x.key`. A destructuring
// pattern looks the same and counts as a write.
func TSIsPropertyWrite(lines []string, line int, name string) bool {
	text := rawLine(lines, line)
	at := wordIndex(text, name)
	if at < 0 {
		return false
	}
	before := strings.TrimRight(text[:at], " \t")
	if strings.HasSuffix(before, ".") || strings.HasSuffix(before, "[\"") || strings.HasSuffix(before, "['") {
		return false
	}
	if before != "" && strings.IndexByte("{,", before[len(before)-1]) < 0 {
		return false
	}
	after := strings.TrimLeft(text[at+len(name):], " \t")
	return after == "" || strings.IndexByte(":,}", after[0]) >= 0
}
