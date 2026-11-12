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
	tsScan(text, func(i, depth int) bool {
		if depth < 1 || (found > 0 && depth >= foundDepth) || !strings.HasPrefix(text[i:], key) || (i > 0 && isWordByte(text[i-1])) {
			return true
		}
		rest := text[i+len(key):]
		if rest != "" && isWordByte(rest[0]) {
			return true
		}
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" || rest[0] != ':' {
			if rest != "" && rest[0] != ',' && rest[0] != '\n' && rest[0] != '}' {
				return true
			}
		}
		// A key follows `{` or `,`; anything else is a value, a ternary or a type.
		previous := strings.TrimRight(text[:i], " \t\n\r")
		if previous == "" || strings.IndexByte("{,", previous[len(previous)-1]) < 0 {
			return true
		}
		found, foundDepth = first+strings.Count(text[:i], "\n"), depth
		return true
	})
	return found
}
