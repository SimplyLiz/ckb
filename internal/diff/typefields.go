package diff

import (
	"regexp"
	"sort"
	"strings"
)

// This file reads the Go and TypeScript declarations whose change the index can't show:
// what is between a type's braces. scip-go and scip-typescript give a type no enclosing
// range, so a field added to its body reaches no symbol, and a declaration compared by
// its first line reads as unchanged. Text, not types, like rust.go.

// maxLiteralSpan bounds how far a literal's braces are followed.
const maxLiteralSpan = 300

// TypeLanguage says which of these parsers a file gets: "go", "typescript", or "".
func TypeLanguage(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"),
		strings.HasSuffix(path, ".mts"), strings.HasSuffix(path, ".cts"):
		return "typescript"
	}
	return ""
}

var (
	// goStructDeclRe matches `type Name struct {` and `Name struct {` in a type group. A
	// field of anonymous struct type matches too; it names no type, and the index says so.
	goStructDeclRe = regexp.MustCompile(`^\s*(?:type\s+)?([A-Za-z_]\w*)(?:\[[^\]]*\])?\s+struct\s*\{`)
	goStructTagRe  = regexp.MustCompile("`[^`]*`")

	tsNamedTypeRe = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(interface|class)\s+([A-Za-z_$][\w$]*)`)
	tsAliasRe     = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?type\s+([A-Za-z_$][\w$]*)(?:<[^=]*>)?\s*=\s*\{`)
	// tsPropertyRe is a class member that is a property, not a method: a name, then `:`
	// or `=` before any parenthesis.
	tsPropertyRe = regexp.MustCompile(`^(?:(?:readonly|public|private|protected|static|declare|override|abstract|accessor)\s+)*#?[A-Za-z_$][\w$]*\s*[?!]?\s*[:=]`)
)

// DeclaredTypes lists the names a file declares with a braced body in the language:
// Go structs, TypeScript interfaces, classes and object type aliases. Order is the file's.
func DeclaredTypes(lines []string, lang string) []string {
	var names []string
	seen := make(map[string]bool)
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for _, text := range lines {
		switch lang {
		case "go":
			if m := goStructDeclRe.FindStringSubmatch(text); m != nil {
				add(m[1])
			}
		case "typescript":
			if m := tsNamedTypeRe.FindStringSubmatch(text); m != nil {
				add(m[2])
			} else if m := tsAliasRe.FindStringSubmatch(text); m != nil {
				add(m[1])
			}
		}
	}
	return names
}

// TypeFields returns a type's members as the language's parser reads them, sorted. False
// means the file has no braced type of that name.
func TypeFields(lines []string, lang, name string) ([]string, bool) {
	switch lang {
	case "go":
		return GoStructFields(lines, name)
	case "typescript":
		return TSTypeFields(lines, name)
	}
	return nil, false
}

// GoStructFields returns a struct's fields, each as its names and type without the tag
// and comments, sorted. A tag change alone doesn't count: no literal breaks on it.
func GoStructFields(lines []string, name string) ([]string, bool) {
	re := regexp.MustCompile(`^\s*(?:type\s+)?` + regexp.QuoteMeta(name) + `(?:\[[^\]]*\])?\s+struct\s*\{`)
	for i, text := range lines {
		if !re.MatchString(text) {
			continue
		}
		entries, ok := typeBodyEntries(lines, i, "")
		if !ok {
			return nil, false
		}
		var fields []string
		for _, entry := range entries {
			entry = goStructTagRe.ReplaceAllString(entry, "")
			if squeezed := squeeze(entry, "go"); squeezed != "" {
				fields = append(fields, squeezed)
			}
		}
		sort.Strings(fields)
		return fields, true
	}
	return nil, false
}

// TSTypeFields returns the members of an interface, an object type alias, or a class's
// properties, sorted. Methods of a class are left out: a body edit is not a shape change.
func TSTypeFields(lines []string, name string) ([]string, bool) {
	quoted := regexp.QuoteMeta(name)
	namedRe := regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(interface|class)\s+` + quoted + `\b`)
	aliasRe := regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?type\s+` + quoted + `(?:<[^=]*>)?\s*=\s*\{`)
	for i, text := range lines {
		isClass := false
		if m := namedRe.FindStringSubmatch(text); m != nil {
			isClass = m[1] == "class"
		} else if !aliasRe.MatchString(text) {
			continue
		}
		entries, ok := typeBodyEntries(lines, i, ";,")
		if !ok {
			return nil, false
		}
		var fields []string
		for _, entry := range entries {
			if isClass && !tsPropertyRe.MatchString(entry) {
				continue
			}
			if squeezed := squeeze(entry, "typescript"); squeezed != "" {
				fields = append(fields, squeezed)
			}
		}
		sort.Strings(fields)
		return fields, true
	}
	return nil, false
}

// TypeSpan returns the 1-based first and last line of a type's declaration and body in
// the language, or false if the file has no braced type of that name.
func TypeSpan(lines []string, lang, name string) (first, last int, ok bool) {
	quoted := regexp.QuoteMeta(name)
	var re *regexp.Regexp
	switch lang {
	case "go":
		re = regexp.MustCompile(`^\s*(?:type\s+)?` + quoted + `(?:\[[^\]]*\])?\s+struct\s*\{`)
	case "typescript":
		re = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:(?:interface|class)\s+` + quoted + `\b|type\s+` + quoted + `(?:<[^=]*>)?\s*=\s*\{)`)
	default:
		return 0, 0, false
	}
	for i, text := range lines {
		if !re.MatchString(text) {
			continue
		}
		_, end, ok := typeBody(lines, i, "")
		if !ok {
			return 0, 0, false
		}
		return i + 1, end + 1, true
	}
	return 0, 0, false
}

// typeBodyEntries returns the entries between the first brace at or after start and its
// partner: the text at depth one, split at newlines and at the separators, without line
// comments. Nested braces stay inside their entry.
func typeBodyEntries(lines []string, start int, seps string) ([]string, bool) {
	entries, _, ok := typeBody(lines, start, seps)
	return entries, ok
}

// typeBody is typeBodyEntries, and the 0-based line of the closing brace.
func typeBody(lines []string, start int, seps string) ([]string, int, bool) {
	var entries []string
	var entry strings.Builder
	flush := func() {
		if text := strings.TrimSpace(entry.String()); text != "" {
			entries = append(entries, text)
		}
		entry.Reset()
	}
	depth := 0
	var quote byte
	for i := start; i < len(lines) && i < start+500; i++ {
		text := lines[i]
		for j := 0; j < len(text); j++ {
			c := text[j]
			if quote != 0 {
				if c == '\\' {
					j++
				} else if c == quote {
					quote = 0
				}
				if depth > 0 {
					entry.WriteByte(c)
				}
				continue
			}
			switch {
			case c == '/' && j+1 < len(text) && text[j+1] == '/':
				j = len(text)
				continue
			case c == '"' || c == '\'' || c == '`':
				quote = c
			case c == '{':
				depth++
				if depth == 1 {
					continue
				}
			case c == '}':
				depth--
				if depth == 0 {
					flush()
					return entries, i, true
				}
			case depth == 1 && strings.IndexByte(seps, c) >= 0:
				flush()
				continue
			}
			if depth > 0 {
				entry.WriteByte(c)
			}
		}
		if depth == 1 {
			flush()
		} else if depth > 1 {
			entry.WriteByte(' ')
		}
	}
	return nil, 0, false
}

// IsLiteralUse says whether the name on a line builds a value of the type by its
// fields, in a way a changed field set may leave incomplete. The language picks the
// reading; an empty language is Rust, as the first of these was.
func IsLiteralUse(lang string, lines []string, line, column int, name string) bool {
	switch lang {
	case "go":
		_, ok := goLiteral(lines, line, column, name)
		return ok
	case "typescript":
		_, ok := tsConstruction(lines, line, column, name)
		return ok
	}
	return IsRustLiteralUse(lines, line, column, name)
}

// LiteralEnd returns the last line of the literal starting at the name, or line.
func LiteralEnd(lang string, lines []string, line, column int, name string) int {
	return literalEnd(lang, lines, line, column, name)
}

// literalEnd returns the last line of the literal starting at the name, or line.
func literalEnd(lang string, lines []string, line, column int, name string) int {
	var end int
	var ok bool
	switch lang {
	case "go":
		end, ok = goLiteral(lines, line, column, name)
	case "typescript":
		end, ok = tsConstruction(lines, line, column, name)
	default:
		end, _, ok = rustLiteral(lines, line, column, name)
	}
	if !ok {
		return line
	}
	return end
}

// goLiteral finds a composite literal starting at the name: `Name{`, `&Name{`, `[]Name{`
// or `Name[T]{`, with the brace right after the name, as gofmt writes it. `func f() Name {`
// has a space there and is a body. `Name{}` builds nothing by its fields and is left out.
func goLiteral(lines []string, line, column int, name string) (end int, ok bool) {
	text := rawLine(lines, line)
	start := nameIndex(text, column, name)
	if start < 0 {
		return 0, false
	}
	rest := text[start+len(name):]
	if strings.HasPrefix(rest, "[") {
		close := matchingBracket(rest, '[', ']')
		if close < 0 {
			return 0, false
		}
		rest = rest[close+1:]
	}
	if !strings.HasPrefix(rest, "{") {
		return 0, false
	}
	inside := strings.TrimSpace(rest[1:])
	if inside == "" {
		inside = strings.TrimSpace(rawLine(lines, line+1))
	}
	if strings.HasPrefix(inside, "}") {
		return 0, false
	}
	// `[]Name{` and `map[K]Name{` hold elements: only a `{` in the first one builds a
	// value by its fields. `[]Name{existing}` names a value that was built elsewhere.
	if strings.HasSuffix(trimQualifier(text[:start]), "]") {
		first := inside
		if at := strings.IndexByte(first, ','); at >= 0 {
			first = first[:at]
		}
		if !strings.Contains(first, "{") {
			return 0, false
		}
	}
	return braceEnd(lines, line, len(text)-len(rest)), true
}

// tsConstruction finds a place that builds an object as the named type: `: Name = {`,
// `: Name[] = [`, `): Name {` and `): Name => (` as a function's return, `<Name>{`,
// and `} as Name` or `} satisfies Name`. The declaration itself, an import, `extends`
// and `implements` are not constructions.
func tsConstruction(lines []string, line, column int, name string) (end int, ok bool) {
	text := rawLine(lines, line)
	start := nameIndex(text, column, name)
	if start < 0 {
		return 0, false
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import{") || strings.HasPrefix(trimmed, "export {") || strings.HasPrefix(trimmed, "export type {") {
		return 0, false
	}
	before := strings.TrimRight(text[:start], " \t")
	lastWord := lastWordOf(before)
	switch lastWord {
	case "interface", "class", "type", "enum", "extends", "implements", "import", "typeof", "keyof", "new":
		return 0, false
	case "as", "satisfies":
		return line, true
	}

	rest := text[start+len(name):]
	if strings.HasPrefix(rest, "<") {
		close := matchingBracket(rest, '<', '>')
		if close < 0 {
			return 0, false
		}
		rest = rest[close+1:]
	}
	for strings.HasPrefix(rest, "[]") {
		rest = rest[2:]
	}
	rest = strings.TrimLeft(rest, " \t")
	offset := len(text) - len(rest)

	switch {
	case strings.HasPrefix(rest, "=>"):
		after := strings.TrimLeft(rest[2:], " \t(")
		if strings.HasPrefix(after, "{") || strings.HasPrefix(after, "[") {
			at := len(text) - len(after)
			return braceEnd(lines, line, at), true
		}
		return 0, false
	case strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "=="):
		after := strings.TrimLeft(rest[1:], " \t")
		if strings.HasPrefix(after, "{") || strings.HasPrefix(after, "[") {
			at := len(text) - len(after)
			return braceEnd(lines, line, at), true
		}
		return 0, false
	case strings.HasPrefix(rest, "{"):
		// `): Name {` is a function returning Name; `<Name>{` a cast of a literal.
		if strings.HasSuffix(before, ":") && strings.HasSuffix(strings.TrimRight(before[:len(before)-1], " \t"), ")") {
			return braceEnd(lines, line, offset), true
		}
		if strings.HasSuffix(before, "<") && strings.HasPrefix(rest, "{") {
			return braceEnd(lines, line, offset), true
		}
		return 0, false
	case strings.HasPrefix(rest, ">") && strings.HasSuffix(before, "<"):
		after := strings.TrimLeft(rest[1:], " \t")
		if strings.HasPrefix(after, "{") {
			return braceEnd(lines, line, len(text)-len(after)), true
		}
	}
	return 0, false
}

// trimQualifier removes a trailing `pkg.` from a text, so what precedes a qualified name
// can be read.
func trimQualifier(text string) string {
	for strings.HasSuffix(text, ".") {
		end := len(text) - 1
		start := end
		for start > 0 && isWordByte(text[start-1]) {
			start--
		}
		if start == end {
			break
		}
		text = text[:start]
	}
	return text
}

// lastWordOf returns the identifier a text ends with, or "".
func lastWordOf(text string) string {
	end := len(text)
	start := end
	for start > 0 && isWordByte(text[start-1]) {
		start--
	}
	return text[start:end]
}

// matchingBracket returns the index of the bracket closing the one at text[0], or -1.
func matchingBracket(text string, open, close byte) int {
	depth := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// braceEnd returns the line on which the bracket at text[offset] of line closes, skipping
// strings and line comments, or the last line looked at.
func braceEnd(lines []string, line, offset int) int {
	text := rawLine(lines, line)
	if offset >= len(text) {
		return line
	}
	open := text[offset]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	case '(':
		close = ')'
	default:
		return line
	}
	depth := 0
	var quote byte
	scan := text[offset:]
	for current := line; current <= line+maxLiteralSpan; current++ {
		if current != line {
			scan = rawLine(lines, current)
		}
		for i := 0; i < len(scan); i++ {
			c := scan[i]
			if quote != 0 {
				if c == '\\' {
					i++
				} else if c == quote {
					quote = 0
				}
				continue
			}
			switch c {
			case '/':
				if i+1 < len(scan) && scan[i+1] == '/' {
					i = len(scan)
				}
			case '"', '\'', '`':
				quote = c
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return current
				}
			}
		}
	}
	return line
}

var goFieldNamesRe = regexp.MustCompile(`^([A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*)\s+(\S.*)$`)

// GoStructFieldTypes returns a struct's fields by name, each with its type squeezed and
// without the tag. An embedded field is named by its type, without pointer or package.
// False means the file has no struct of that name.
func GoStructFieldTypes(lines []string, name string) (map[string]string, bool) {
	re := regexp.MustCompile(`^\s*(?:type\s+)?` + regexp.QuoteMeta(name) + `(?:\[[^\]]*\])?\s+struct\s*\{`)
	for i, text := range lines {
		if !re.MatchString(text) {
			continue
		}
		entries, ok := typeBodyEntries(lines, i, "")
		if !ok {
			return nil, false
		}
		fields := make(map[string]string)
		for _, entry := range entries {
			entry = strings.TrimSpace(goStructTagRe.ReplaceAllString(entry, ""))
			if entry == "" {
				continue
			}
			if m := goFieldNamesRe.FindStringSubmatch(entry); m != nil && !goTypeKeywords[m[1]] {
				for _, field := range strings.Split(m[1], ",") {
					fields[strings.TrimSpace(field)] = squeeze(m[2], "go")
				}
				continue
			}
			embedded := strings.TrimPrefix(entry, "*")
			if dot := strings.LastIndexByte(embedded, '.'); dot >= 0 {
				embedded = embedded[dot+1:]
			}
			if at := strings.IndexByte(embedded, '['); at >= 0 {
				embedded = embedded[:at]
			}
			fields[embedded] = squeeze(entry, "go")
		}
		return fields, true
	}
	return nil, false
}

// GoBrokenFields compares a struct's fields across a diff: the ones a keyed literal
// can no longer set as it did, because they went away or changed type. changed is
// false if the field set is the same.
func GoBrokenFields(before, after map[string]string) (broken map[string]bool, changed bool) {
	broken = make(map[string]bool)
	for field, typ := range before {
		if now, ok := after[field]; !ok || now != typ {
			broken[field] = true
		}
	}
	return broken, len(broken) > 0 || len(after) != len(before)
}

var goKeyRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s*:`)

// GoLiteralKeys reads the composite literal of the named type at a use: the field keys
// it sets, sorted, and whether any value in it is positional. Of `[]Name{` and
// `map[K]Name{` it reads each element's braces. A keyed literal compiles after a field
// is added; a positional one has to list every field. False means no literal is there.
func GoLiteralKeys(lines []string, line, column int, name string) (keys []string, positional bool, ok bool) {
	end, ok := goLiteral(lines, line, column, name)
	if !ok {
		return nil, false, false
	}
	text := rawLine(lines, line)
	start := nameIndex(text, column, name)
	rest := text[start+len(name):]
	if strings.HasPrefix(rest, "[") {
		rest = rest[matchingBracket(rest, '[', ']')+1:]
	}
	var body strings.Builder
	body.WriteString(rest)
	for l := line + 1; l <= end; l++ {
		body.WriteString("\n")
		body.WriteString(rawLine(lines, l))
	}
	content, ok := goBraceContent(body.String())
	if !ok {
		return nil, false, false
	}
	holder := strings.HasSuffix(trimQualifier(text[:start]), "]")
	seen := make(map[string]bool)
	read := func(content string) {
		for _, entry := range goSplitEntries(content) {
			if m := goKeyRe.FindStringSubmatch(entry); m != nil {
				seen[m[1]] = true
			} else {
				positional = true
			}
		}
	}
	if !holder {
		read(content)
	} else {
		for _, element := range goSplitEntries(content) {
			if at := goTopLevelBrace(element); at >= 0 {
				if inner, ok := goBraceContent(element[at:]); ok && strings.TrimSpace(inner) != "" {
					read(inner)
				}
			}
		}
	}
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, positional, true
}

// goScan walks Go source outside strings, runes and comments, calling visit with each
// byte's index and the bracket depth before it. visit returns false to stop.
func goScan(s string, visit func(i, depth int) bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			for j := i + 1; j < len(s); j++ {
				if s[j] == '\\' && c != '`' {
					j++
				} else if s[j] == c {
					i = j
					break
				}
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += end + 3
			} else {
				i = len(s)
			}
			continue
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
	}
}

// goBraceContent returns what is between the first `{` in s and its partner.
func goBraceContent(s string) (string, bool) {
	open, closeAt := -1, -1
	goScan(s, func(i, depth int) bool {
		switch {
		case open < 0 && s[i] == '{':
			open = i
		case open >= 0 && s[i] == '}' && depth == 1:
			closeAt = i
			return false
		}
		return true
	})
	if open < 0 || closeAt < 0 {
		return "", false
	}
	return s[open+1 : closeAt], true
}

// goSplitEntries splits a literal's content at its top-level commas.
func goSplitEntries(content string) []string {
	var entries []string
	var current strings.Builder
	last := 0
	flush := func(upto int) {
		current.WriteString(content[last:upto])
		if entry := goLeadingComments(current.String()); entry != "" {
			entries = append(entries, entry)
		}
		current.Reset()
	}
	goScan(content, func(i, depth int) bool {
		if content[i] == ',' && depth == 0 {
			flush(i)
			last = i + 1
		}
		return true
	})
	flush(len(content))
	return entries
}

// goTopLevelBrace is the index of the first `{` outside any bracket, or -1.
func goTopLevelBrace(s string) int {
	at := -1
	goScan(s, func(i, depth int) bool {
		if s[i] == '{' && depth == 0 {
			at = i
			return false
		}
		return true
	})
	return at
}

// goLeadingComments drops the comments an entry starts with: a comment after the last
// comma is no entry, and one before a key doesn't make it positional.
func goLeadingComments(s string) string {
	for {
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "//"):
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
			} else {
				return ""
			}
		case strings.HasPrefix(s, "/*"):
			if end := strings.Index(s, "*/"); end >= 0 {
				s = s[end+2:]
			} else {
				return ""
			}
		default:
			return s
		}
	}
}
