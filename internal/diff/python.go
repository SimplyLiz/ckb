package diff

import (
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// Python declarations, read the way declaration.go reads the others: text, not
// types. A def or class runs to the colon that opens its body, a module or class
// value to its `=`. Decorators above a def or class are part of it, since
// @property or @staticmethod changes how every caller has to use it.
//
// Visibility is the name (a leading underscore is private), as in Go, so another
// name is another symbol and stripVisibility has nothing to strip.
//
// Unlike the other languages a parameter's name is part of the signature: any
// caller may pass it by keyword. Only names before a `/` (positional-only) and a
// method's leading self or cls are not.

var pythonDefOrClassRe = regexp.MustCompile(`^(async\s+)?def\s|^class\s`)

// readPythonDeclaration reads the declaration starting at start: a def or class
// up to the colon of its body, a value up to its `=` (`X: int = 1` is `X: int`),
// with comments left out and line breaks turned into spaces.
func readPythonDeclaration(lines map[int]string, start int) declaration {
	var out strings.Builder
	depth := 0
	lineCount := 0
	var quote string // The open string's delimiter: ', ", ''' or """

	finish := func(complete bool) declaration {
		return declaration{text: strings.TrimSpace(out.String()), lines: lineCount, complete: complete}
	}
	first, ok := lines[start]
	if !ok {
		return finish(false)
	}
	isDef := pythonDefOrClassRe.MatchString(strings.TrimSpace(first))

	for line := start; line < start+maxDeclarationLines; line++ {
		text, ok := lines[line]
		if !ok {
			return finish(false)
		}
		lineCount++
		continued := false
		for i := 0; i < len(text); i++ {
			c := text[i]
			if quote != "" {
				if c == '\\' && i+1 < len(text) {
					out.WriteByte(c)
					out.WriteByte(text[i+1])
					i++
					continue
				}
				if strings.HasPrefix(text[i:], quote) {
					out.WriteString(quote)
					i += len(quote) - 1
					quote = ""
					continue
				}
				out.WriteByte(c)
				continue
			}
			switch c {
			case '#':
				i = len(text) // A comment runs to the end of the line
				continue
			case '"', '\'':
				quote = string(c)
				if strings.HasPrefix(text[i:], strings.Repeat(string(c), 3)) {
					quote = strings.Repeat(string(c), 3)
				}
				out.WriteString(quote)
				i += len(quote) - 1
				continue
			case '\\':
				if strings.TrimSpace(text[i+1:]) == "" {
					continued = true // An explicit line continuation
					i = len(text)
					continue
				}
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			case ':':
				if depth == 0 && isDef {
					return finish(true) // The body starts
				}
			case '=':
				if depth == 0 && !isDef && !isComparison(text, i) && !pythonAugmented(text, i) {
					return finish(true) // The value starts
				}
			}
			out.WriteByte(c)
		}
		out.WriteByte(' ')
		if depth <= 0 && quote == "" && !continued && !isDef {
			return finish(true) // A value with no `=`: `X: int`
		}
	}
	return finish(false)
}

// pythonAugmented says whether the `=` at i ends `+=`, `:=` and the like: not a
// declaration's value. `==`, `<=`, `>=` and `!=` are isComparison's.
func pythonAugmented(text string, i int) bool {
	return i > 0 && strings.IndexByte("+-*/%&|^@:", text[i-1]) >= 0
}

// withPythonDecorators puts the decorators right above a def or class in front of
// its text: `@property def f(self)`. Only single-line decorators count; reading
// stops at a blank line, a comment or anything else.
func withPythonDecorators(lines map[int]string, line int, text string) string {
	if text == "" {
		return text
	}
	var decorators []string
	for l := line - 1; l > 0 && len(decorators) < 8; l-- {
		prev, ok := lines[l]
		if !ok {
			break
		}
		trimmed := strings.TrimSpace(prev)
		if !strings.HasPrefix(trimmed, "@") {
			break
		}
		if i := strings.Index(trimmed, " #"); i > 0 {
			trimmed = strings.TrimSpace(trimmed[:i])
		}
		decorators = append([]string{trimmed}, decorators...)
	}
	if len(decorators) == 0 {
		return text
	}
	return strings.Join(decorators, " ") + " " + text
}

// parsePythonParameters reads a def's parameter list, one parameter per entry with
// its name, annotation and whether a call can leave it out. The `*` and `/` markers
// are entries too, so moving one shifts the parameters after it and reads as a
// change. A class's list is its bases: every one can be added at the end without
// breaking a subclass or an isinstance check, so all are optional.
func parsePythonParameters(entries []string, isClass bool) []parameter {
	params := make([]parameter, 0, len(entries))
	positionalOnly := -1
	for i, entry := range entries {
		if entry == "/" {
			positionalOnly = i
		}
	}
	for i, entry := range entries {
		if isClass {
			params = append(params, parameter{typ: squeeze(entry, "python"), optional: true})
			continue
		}
		params = append(params, parsePythonParameter(entry, i < positionalOnly || i == 0 && isSelfOrCls(entry)))
	}
	return params
}

func isSelfOrCls(entry string) bool {
	name := strings.TrimSpace(splitTopLevel(entry, ':')[0])
	return name == "self" || name == "cls"
}

// parsePythonParameter reads one entry: `a`, `a: int`, `a=1`, `a: int = 1`,
// `*args`, `**kwargs`, `*` or `/`. anonymous drops the name, for a parameter no
// call can pass by keyword.
func parsePythonParameter(entry string, anonymous bool) parameter {
	entry = strings.TrimSpace(entry)
	if entry == "*" || entry == "/" {
		return parameter{typ: entry, optional: true}
	}
	optional := false
	if parts := splitTopLevel(entry, '='); len(parts) > 1 {
		entry, optional = parts[0], true
	}
	parts := splitTopLevel(entry, ':')
	name := strings.TrimSpace(parts[0])
	annotation := ""
	if len(parts) > 1 {
		annotation = squeeze(strings.Join(parts[1:], ":"), "python")
	}
	switch {
	case strings.HasPrefix(name, "**"):
		// Its name can't be passed: a keyword of that name lands in it.
		return parameter{typ: "**:" + annotation, optional: true}
	case strings.HasPrefix(name, "*"):
		return parameter{typ: "*:" + annotation, optional: true}
	}
	if anonymous {
		name = ""
	}
	return parameter{typ: name + ":" + annotation, optional: optional}
}

var pythonKeywordRe = regexp.MustCompile(`(^|\s)((async\s+)?def|class)\s`)

// parsePythonDeclaration is parseDeclaration for Python. The decorators are read
// apart from the def, so a name inside one, `@cache(f) def f(a)`, isn't taken
// for the def's; they go into the prefix, so any change to them is a change.
func parsePythonDeclaration(text, name string) parsedDeclaration {
	var parsed parsedDeclaration
	decorators := ""
	if loc := pythonKeywordRe.FindStringIndex(text); loc != nil && strings.HasPrefix(strings.TrimSpace(text), "@") {
		decorators, text = text[:loc[0]], text[loc[0]:]
	}
	text = strings.TrimSpace(text)
	open, closeAt := parameterParens(text, name, "python")
	if open < 0 {
		parsed.prefix = squeeze(decorators, "python") + squeeze(text, "python")
		return parsed
	}
	parsed.isFunction = true
	parsed.prefix = squeeze(decorators, "python") + squeeze(text[:open], "python")
	parsed.suffix = squeeze(text[closeAt+1:], "python")
	var entries []string
	for _, entry := range splitTopLevel(text[open+1:closeAt], ',') {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	parsed.params = parsePythonParameters(entries, strings.HasPrefix(text, "class"))
	return parsed
}

// PythonRenamedKeywords returns the old names of the parameters a def renamed,
// when renaming is all it did: same order, annotations and defaults. Then a call
// passing its arguments by position works as before, and only one that passes an
// old name as a keyword breaks. Nil when anything else changed too.
func PythonRenamedKeywords(before, after, name string) []string {
	a, b := parsePythonDeclaration(before, name), parsePythonDeclaration(after, name)
	if !a.isFunction || !b.isFunction || a.prefix != b.prefix || a.suffix != b.suffix || len(a.params) != len(b.params) {
		return nil
	}
	var renamed []string
	for i := range a.params {
		oldName, oldAnnotation, _ := strings.Cut(a.params[i].typ, ":")
		newName, newAnnotation, _ := strings.Cut(b.params[i].typ, ":")
		if oldAnnotation != newAnnotation || a.params[i].optional != b.params[i].optional {
			return nil
		}
		if oldName != newName {
			if oldName == "" || oldName == "*" || oldName == "/" || newName == "" {
				return nil
			}
			renamed = append(renamed, oldName)
		}
	}
	return renamed
}

var pythonKeywordArgRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s*=`)

// PythonCallPassesKeyword says whether the call of name on line (1-based) of
// lines may pass one of keywords: it names one (`f(a, model=x)`), or unpacks a
// mapping (`f(**opts)`) whose keys can't be read. A use that isn't a call, such
// as the function handed on as a value, and a call that can't be read to its
// closing parenthesis, may too.
func PythonCallPassesKeyword(lines []string, line int, name string, keywords []string) bool {
	if line < 1 || line > len(lines) || len(keywords) == 0 {
		return true
	}
	var text strings.Builder
	for l := line; l <= len(lines) && l < line+maxDeclarationLines; l++ {
		text.WriteString(lines[l-1])
		text.WriteByte('\n')
	}
	s := text.String()
	open := pythonCallOpen(s, name)
	if open < 0 {
		return true
	}
	closeAt := pythonClosingParen(s, open)
	if closeAt < 0 {
		return true
	}
	wanted := make(map[string]bool, len(keywords))
	for _, k := range keywords {
		wanted[k] = true
	}
	for _, arg := range splitTopLevel(s[open+1:closeAt], ',') {
		arg = strings.TrimSpace(stripPythonComments(arg))
		if strings.HasPrefix(arg, "**") {
			return true
		}
		if m := pythonKeywordArgRe.FindStringSubmatch(arg); m != nil && !strings.HasPrefix(arg[len(m[0]):], "=") && wanted[m[1]] {
			return true
		}
	}
	return false
}

// pythonClosingParen is the index of the parenthesis closing the one at open, or
// -1. Strings and comments don't count.
func pythonClosingParen(s string, open int) int {
	depth := 0
	var quote byte
	for i := open; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case '"', '\'':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// stripPythonComments drops the `# ...` ends of an argument's lines.
func stripPythonComments(arg string) string {
	lines := strings.Split(arg, "\n")
	for i, l := range lines {
		if j := strings.IndexByte(l, '#'); j >= 0 && !strings.ContainsAny(l[:j], `"'`) {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, " ")
}

// withPythonDecoratorExtents starts each def and class at its first decorator:
// scip-python's range starts at the `def`, so a decorator changed or removed above
// it would belong to whatever encloses it, the class or nothing.
func withPythonDecoratorExtents(defs []*SymbolDefInfo, lines map[int]string) []*SymbolDefInfo {
	if len(lines) == 0 {
		return defs
	}
	out, copied := defs, false
	for i, def := range defs {
		start := def.StartLine
		if !pythonDefOrClassRe.MatchString(strings.TrimSpace(lines[start])) {
			continue
		}
		for start > 1 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "@") {
			start--
		}
		if start == def.StartLine {
			continue
		}
		if !copied {
			out, copied = append([]*SymbolDefInfo(nil), defs...), true
		}
		extended := *def
		extended.StartLine = start
		out[i] = &extended
	}
	return out
}

// pythonDecoratedDef is the def or class a block that only adds decorators sits
// right above, or nil. Inserted before a def, they are part of it, though an
// insertion right before a definition is otherwise outside it.
func pythonDecoratedDef(block impact.ChangeBlock, side indexSide, defs []*SymbolDefInfo, lines map[int]string) *SymbolDefInfo {
	added, line := block.Added, block.OldLine+len(block.Removed)
	if side == indexMatchesNew {
		added, line = block.Removed, block.NewLine+len(block.Added)
	}
	if len(added) == 0 {
		return nil
	}
	for _, text := range added {
		if !strings.HasPrefix(strings.TrimSpace(text), "@") {
			return nil
		}
	}
	// Decorators already there between the block and the def are the def's too.
	for strings.HasPrefix(strings.TrimSpace(lines[line]), "@") {
		line++
	}
	if !pythonDefOrClassRe.MatchString(strings.TrimSpace(lines[line])) {
		return nil
	}
	for _, def := range defs {
		if def.StartLine <= line && line <= def.EndLine && (def.DefLine == line || def.DefLine == 0 && def.StartLine == line) {
			return def
		}
	}
	return nil
}

// pythonCallOpen is the index of the parenthesis right after the first use of
// name in s, the reference's, or -1 when that use isn't a call.
func pythonCallOpen(s, name string) int {
	for offset := 0; offset < len(s); {
		i := strings.Index(s[offset:], name)
		if i < 0 {
			return -1
		}
		start, end := offset+i, offset+i+len(name)
		if (start == 0 || !isIdentByte(s[start-1])) && (end == len(s) || !isIdentByte(s[end])) {
			rest := strings.TrimLeft(s[end:], " \t")
			if strings.HasPrefix(rest, "(") {
				return len(s) - len(rest)
			}
			return -1
		}
		offset = start + 1
	}
	return -1
}

// PythonIsCall says whether the use of name on line (1-based) is a call: `Root()`,
// not `class Sub(Root)` or `isinstance(x, Root)`.
func PythonIsCall(lines []string, line int, name string) bool {
	if line < 1 || line > len(lines) {
		return true // Can't be read: count it
	}
	return pythonCallOpen(lines[line-1], name) >= 0
}

// PythonInitClass is the class whose __init__ symbolID is, or "": constructing the
// class calls it, and scip-python records `Root()` as a use of Root, not of
// Root.__init__.
func PythonInitClass(symbolID string) string {
	if class, ok := strings.CutSuffix(symbolID, "#__init__()."); ok && strings.HasPrefix(symbolID, "scip-python ") {
		return class + "#"
	}
	return ""
}
