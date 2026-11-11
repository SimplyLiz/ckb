package diff

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// This file compares a definition's declaration across a diff: did its signature
// change, or only its body? It reads text, not types, so it is a heuristic. It
// knows Go, TypeScript/JavaScript and Rust; anything else gets no answer.

// DeclarationContextLines is how much context a diff needs for a multi-line
// signature to be readable from it.
const DeclarationContextLines = 12

const maxDeclarationLines = 40

// declarationLanguage returns the syntax to read a file with, or "".
func declarationLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return "ts"
	case ".rs":
		return "rust"
	}
	return ""
}

// fileText is what a diff shows of a file: the text of both sides, which lines the
// change itself touched, and which old line each unchanged new line was. oldAll and
// newAll are the whole sides where they could be read, and otherwise the same as
// oldLines and newLines: what lies outside every hunk, such as the `const (` a
// constant sits under, is only there.
type fileText struct {
	oldLines, newLines     map[int]string
	oldAll, newAll         map[int]string
	changedOld, changedNew map[int]bool
	oldToNew, newToOld     map[int]int
}

// fileSides is a changed file's whole text on both sides of the diff, 1-based by
// position. A side is nil if it couldn't be read.
type fileSides struct {
	old, new []string
}

func readFileText(file *impact.ChangedFile, whole *fileSides) *fileText {
	t := &fileText{
		oldLines: make(map[int]string), newLines: make(map[int]string),
		changedOld: make(map[int]bool), changedNew: make(map[int]bool),
		oldToNew: make(map[int]int), newToOld: make(map[int]int),
	}
	for i := range file.Hunks {
		hunk := &file.Hunks[i]
		for j, text := range hunk.OldText {
			t.oldLines[hunk.OldStart+j] = text
		}
		for j, text := range hunk.NewText {
			t.newLines[hunk.NewStart+j] = text
		}
		for _, line := range hunk.Removed {
			t.changedOld[line] = true
		}
		for _, line := range hunk.Added {
			t.changedNew[line] = true
		}
		// What isn't changed is context, in the same order on both sides.
		oldLine, newLine := hunk.OldStart, hunk.NewStart
		for oldLine < hunk.OldStart+len(hunk.OldText) && newLine < hunk.NewStart+len(hunk.NewText) {
			switch {
			case t.changedOld[oldLine]:
				oldLine++
			case t.changedNew[newLine]:
				newLine++
			default:
				t.oldToNew[oldLine] = newLine
				t.newToOld[newLine] = oldLine
				oldLine++
				newLine++
			}
		}
	}
	t.oldAll, t.newAll = t.oldLines, t.newLines
	if whole != nil {
		t.oldAll = wholeSide(t.oldLines, whole.old)
		t.newAll = wholeSide(t.newLines, whole.new)
	}
	return t
}

// wholeSide returns a side's whole text, if it agrees with every line the hunks show
// of it. If not, it is some other revision of the file, and the hunks are all there is.
func wholeSide(hunkLines map[int]string, whole []string) map[int]string {
	if whole == nil {
		return hunkLines
	}
	for line, text := range hunkLines {
		if line < 1 || line > len(whole) || whole[line-1] != text {
			return hunkLines
		}
	}
	all := make(map[int]string, len(whole))
	for i, text := range whole {
		all[i+1] = text
	}
	return all
}

// classifyDeclarationIn compares the declaration of def across the diff. The returned
// text is what was compared, without comments. An empty result means no answer.
func classifyDeclarationIn(text *fileText, deleted bool, def *SymbolDefInfo, name string, side indexSide, changeType impact.ChangeType, path string) (change impact.DeclarationChange, before, after string) {
	lang := declarationLanguage(path)
	if lang == "" || name == "" || changeType == impact.ChangeAdded {
		return "", "", ""
	}
	if len(text.oldLines) == 0 && len(text.newLines) == 0 {
		return "", "", "" // A hunk built by hand has no text
	}

	// The hunks decide what the change touched; the whole file, where there is one,
	// is read for what a declaration's own lines don't say.
	indexLines := text.oldLines
	indexAll, otherAll := text.oldAll, text.newAll
	indexChanged, otherChanged := text.changedOld, text.changedNew
	toOther := text.oldToNew
	if side == indexMatchesNew {
		indexLines = text.newLines
		indexAll, otherAll = text.newAll, text.oldAll
		indexChanged, otherChanged = text.changedNew, text.changedOld
		toOther = text.newToOld
	}

	line := def.DefLine
	if line == 0 {
		line = def.StartLine
	}
	first, known := indexLines[line]
	if !known {
		// No hunk reaches the declaration, so the change is further into the body.
		if changeType == impact.ChangeDeleted || deleted {
			return impact.DeclRemoved, "", ""
		}
		return impact.DeclUnchanged, "", ""
	}
	if !containsWord(first, name) {
		return "", "", ""
	}
	first = withGoGroupKeyword(indexAll, line, first, lang)

	indexDecl := readDeclaration(indexAll, line, lang)
	indexDecl.text = withGoGroupKeyword(indexAll, line, indexDecl.text, lang)

	otherLine, inContext := toOther[line]
	if !inContext {
		otherLine = findDeclaration(otherAll, otherChanged, first, name, lang)
		if otherLine == 0 {
			if side == indexMatchesOld {
				return impact.DeclRemoved, indexDecl.text, ""
			}
			return "", "", "" // New in this change: nothing can depend on it yet
		}
	}
	otherDecl := readDeclaration(otherAll, otherLine, lang)
	otherDecl.text = withGoGroupKeyword(otherAll, otherLine, otherDecl.text, lang)

	before, after = indexDecl.text, otherDecl.text
	if side == indexMatchesNew {
		before, after = after, before
	}
	if !indexDecl.complete || !otherDecl.complete {
		// The diff stops before the declaration does. All we can say is whether the
		// part it shows was touched.
		if !anyChanged(indexChanged, line, indexDecl.lines) && !anyChanged(otherChanged, otherLine, otherDecl.lines) {
			return impact.DeclUnchanged, before, after
		}
		return "", before, after
	}
	return compareDeclarations(before, after, name, lang), before, after
}

func anyChanged(changed map[int]bool, start, count int) bool {
	for i := 0; i < count; i++ {
		if changed[start+i] {
			return true
		}
	}
	return false
}

// declaration is the text of a declaration read from a diff.
type declaration struct {
	text     string // With comments removed and line breaks turned into spaces
	lines    int
	complete bool // False if the diff ended before the declaration did
}

var (
	typeDeclRe    = regexp.MustCompile(`^(export\s+|pub(\([^)]*\))?\s+|declare\s+)*type\s`)
	arrowValueRe  = regexp.MustCompile(`^\s*(async\b|function\b|\(|<|[A-Za-z_$][\w$]*\s*=>)`)
	continuesRe   = regexp.MustCompile(`(=>|->|[:,|&=]|\bwhere)\s*$`)
	continuedByRe = regexp.MustCompile(`^(->|=>|where\b|[:{|&+])`)
)

// readDeclaration reads a declaration starting at start: everything up to its body
// or, for a value, its initializer.
func readDeclaration(lines map[int]string, start int, lang string) declaration {
	var out strings.Builder
	depth, angle := 0, 0
	inBlockComment := false
	seenAssign := false
	isTypeDecl := false
	lineCount := 0

	finish := func(complete bool) declaration {
		return declaration{text: strings.TrimSpace(out.String()), lines: lineCount, complete: complete}
	}

	for line := start; line < start+maxDeclarationLines; line++ {
		text, ok := lines[line]
		if !ok {
			return finish(false)
		}
		lineCount++
		if line == start {
			isTypeDecl = typeDeclRe.MatchString(strings.TrimSpace(text))
		}

		var quote byte
		for i := 0; i < len(text); i++ {
			c := text[i]
			if inBlockComment {
				if c == '*' && i+1 < len(text) && text[i+1] == '/' {
					inBlockComment = false
					i++
				}
				continue
			}
			if quote != 0 {
				out.WriteByte(c)
				if c == '\\' && i+1 < len(text) {
					i++
					out.WriteByte(text[i])
				} else if c == quote {
					quote = 0
				}
				continue
			}
			if c == '/' && i+1 < len(text) {
				if text[i+1] == '/' {
					break
				}
				if text[i+1] == '*' {
					inBlockComment = true
					i++
					continue
				}
			}
			prev := lastSignificant(out.String())
			switch c {
			case '"', '`':
				quote = c
			case '\'':
				if lang == "ts" { // In Rust it is a lifetime
					quote = c
				}
			case '(', '[':
				depth++
			case ')', ']':
				depth--
			case '<':
				if lang != "go" && (isWordByte(prev) || prev == 0) {
					angle++
				}
			case '>':
				if angle > 0 && prev != '=' && prev != '-' {
					angle--
				}
				if prev == '=' && depth == 0 && angle == 0 && seenAssign {
					out.WriteByte(c)
					return finish(true) // The arrow of `f = (a) => body`
				}
			case '{':
				if depth == 0 && angle == 0 {
					if lang == "ts" && strings.IndexByte(":|&<,", prev) >= 0 || (isTypeDecl && prev == '=') {
						depth++ // An object type, not a body
						break
					}
					return finish(true)
				}
				depth++
			case '}':
				depth--
			case ';':
				if depth == 0 && angle == 0 {
					return finish(true)
				}
			case '=':
				if depth == 0 && angle == 0 && !isComparison(text, i, prev) && !isTypeDecl {
					if lang != "ts" || !arrowValueRe.MatchString(text[i+1:]) {
						return finish(true)
					}
					seenAssign = true
				}
			}
			out.WriteByte(c)
		}
		out.WriteByte(' ')

		if depth <= 0 && angle <= 0 && !inBlockComment {
			next, hasNext := lines[line+1]
			if continuesRe.MatchString(out.String()) || (hasNext && continuedByRe.MatchString(strings.TrimSpace(next))) {
				continue
			}
			return finish(true)
		}
	}
	return finish(false)
}

func lastSignificant(s string) byte {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != ' ' && s[i] != '\t' {
			return s[i]
		}
	}
	return 0
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isComparison(text string, i int, prev byte) bool {
	if i+1 < len(text) && (text[i+1] == '=' || text[i+1] == '>') {
		return true
	}
	return strings.IndexByte("=!<>", prev) >= 0 && prev != 0
}

var (
	declKeywordRe  = regexp.MustCompile(`\b(func|fn|function|const|let|var|static|type|class|interface|enum|struct|trait|union)\b`)
	modifierOnlyRe = regexp.MustCompile(`^\s*((export|default|declare|abstract|public|private|protected|static|readonly|override|async|get|set|pub(\([^)]*\))?|unsafe|mut|ref)\s+)*$`)
	goReceiverRe   = regexp.MustCompile(`^\s*func\s*\(\s*(?:[A-Za-z_]\w*\s+)?\*?([A-Za-z_]\w*)`)
)

// findDeclaration looks among a side's changed lines for the declaration matching
// the one on the line given: same name, same sort of thing, same receiver.
func findDeclaration(lines map[int]string, changed map[int]bool, like, name, lang string) int {
	best := 0
	for line := range changed {
		text, ok := lines[line]
		if ok {
			text = withGoGroupKeyword(lines, line, text, lang)
		}
		if !ok || !containsWord(text, name) || !declaresSame(text, like, name, lang) {
			continue
		}
		if best == 0 || line < best {
			best = line
		}
	}
	return best
}

func declaresSame(text, like, name, lang string) bool {
	prefix := beforeWord(text, name)
	likePrefix := beforeWord(like, name)
	keyword := lastMatch(declKeywordRe, prefix)
	likeKeyword := lastMatch(declKeywordRe, likePrefix)
	if lang == "go" {
		a, b := goReceiverRe.FindStringSubmatch(text), goReceiverRe.FindStringSubmatch(like)
		if (a == nil) != (b == nil) || (a != nil && a[1] != b[1]) {
			return false
		}
	}
	if keyword != likeKeyword {
		// A function rewritten as a const arrow, or back, is still that function.
		functionLike := map[string]bool{"function": true, "const": true, "let": true, "var": true}
		if lang != "ts" || !functionLike[keyword] || !functionLike[likeKeyword] {
			return false
		}
	}
	if keyword != "" {
		return namesFollowKeyword(prefix, keyword)
	}
	if !modifierOnlyRe.MatchString(prefix) {
		return false // `return name(x)`: a use, not a declaration
	}
	rest := strings.TrimSpace(text[len(prefix)+len(name):])
	return rest != "" && strings.IndexByte("(<:=?,", rest[0]) >= 0 || lang == "go"
}

var (
	afterFuncKeywordRe  = regexp.MustCompile(`^\s*(\([^()]*\)\s*|\*\s*)?$`)
	afterValueKeywordRe = regexp.MustCompile(`^\s*(mut\s+)?([A-Za-z_$][\w$]*\s*,\s*)*$`)
)

// namesFollowKeyword says whether the declaration's keyword is right before its name:
// nothing in between but a Go receiver, a generator's `*`, Rust's `static mut`, or the
// other names of a `var a, b`. `func open(file string)` declares open, not file.
func namesFollowKeyword(prefix, keyword string) bool {
	between := prefix[strings.LastIndex(prefix, keyword)+len(keyword):]
	switch keyword {
	case "func", "fn", "function":
		return afterFuncKeywordRe.MatchString(between)
	case "const", "let", "var", "static":
		return afterValueKeywordRe.MatchString(between)
	}
	return strings.TrimSpace(between) == ""
}

func beforeWord(text, name string) string {
	for offset := 0; ; {
		i := strings.Index(text[offset:], name)
		if i < 0 {
			return text
		}
		start := offset + i
		end := start + len(name)
		if (start == 0 || !isIdentByte(text[start-1])) && (end == len(text) || !isIdentByte(text[end])) {
			return text[:start]
		}
		offset = start + 1
	}
}

func lastMatch(re *regexp.Regexp, s string) string {
	all := re.FindAllString(s, -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// compareDeclarations decides what a difference between two declarations means for
// the code already using them. Formatting, comments, a receiver or parameter
// renamed: nothing. A new optional parameter, or wider visibility: existing uses keep
// working. Anything else, including a changed return type: they may not.
func compareDeclarations(before, after, name, lang string) impact.DeclarationChange {
	if squeeze(before, lang) == squeeze(after, lang) {
		return impact.DeclUnchanged
	}
	a, b := parseDeclaration(before, name, lang), parseDeclaration(after, name, lang)
	if a.prefix != b.prefix || a.suffix != b.suffix || a.isFunction != b.isFunction {
		return impact.DeclChanged
	}
	if b.visibility < a.visibility {
		return impact.DeclChanged // Narrowed
	}
	result := impact.DeclUnchanged
	if b.visibility > a.visibility {
		result = impact.DeclCompatible
	}
	if a.isFunction {
		switch compareParameters(a.params, b.params) {
		case impact.DeclChanged:
			return impact.DeclChanged
		case impact.DeclCompatible:
			result = impact.DeclCompatible
		}
	}
	return result
}

var (
	spaceRe         = regexp.MustCompile(`\s+`)
	trailingCommaRe = regexp.MustCompile(`,([)\]}>])`)
)

func squeeze(s, lang string) string {
	if lang == "go" {
		s = goReceiverVarRe.ReplaceAllString(s, "func(")
	}
	return trailingCommaRe.ReplaceAllString(spaceRe.ReplaceAllString(s, ""), "$1")
}

var (
	goReceiverVarRe = regexp.MustCompile(`^func\s*\(\s*[A-Za-z_]\w*\s+`)
	tsVisibilityRe  = regexp.MustCompile(`\b(export\s+default|export|public|protected|private)\s+`)
	rustVisRe       = regexp.MustCompile(`\bpub(\s*\([^)]*\))?\s+`)
)

type parsedDeclaration struct {
	prefix, suffix string // Squeezed, without visibility
	visibility     int    // Higher is wider
	isFunction     bool
	params         []parameter
}

type parameter struct {
	typ      string
	optional bool
}

func parseDeclaration(text, name, lang string) parsedDeclaration {
	var parsed parsedDeclaration
	text, parsed.visibility = stripVisibility(text, lang)

	open, closeAt := parameterParens(text, name, lang)
	if open < 0 {
		parsed.prefix = squeeze(text, lang)
		return parsed
	}
	parsed.isFunction = true
	parsed.prefix = squeeze(text[:open], lang)
	parsed.suffix = squeeze(text[closeAt+1:], lang)
	parsed.params = parseParameters(text[open+1:closeAt], lang)
	return parsed
}

func stripVisibility(text, lang string) (string, int) {
	switch lang {
	case "ts":
		rank := 2 // A method with no modifier is public; a function with none is the module's
		switch match := tsVisibilityRe.FindString(text); {
		case strings.HasPrefix(match, "export"), strings.HasPrefix(match, "public"):
			rank = 3
		case strings.HasPrefix(match, "protected"):
			rank = 1
		case strings.HasPrefix(match, "private"):
			rank = 0
		}
		return tsVisibilityRe.ReplaceAllString(text, ""), rank
	case "rust":
		rank := 1
		if match := rustVisRe.FindString(text); match != "" {
			rank = 3
			if strings.Contains(match, "(") {
				rank = 2
			}
		}
		return rustVisRe.ReplaceAllString(text, ""), rank
	}
	return text, 0 // In Go, visibility is the name, and another name is another symbol
}

// parameterParens finds the parameter list following the name, past any generics
// and, for `const f = (a) => ...`, the assignment. -1 means this isn't a function.
func parameterParens(text, name, lang string) (open, closeAt int) {
	start := len(beforeWord(text, name)) + len(name)
	if start > len(text) {
		return -1, -1
	}
	// `const f = (a) => ...` and `const f: T = (a) => ...`: the list is past the `=`.
	// `a: string` has none, and a function type there isn't this symbol's signature.
	rest := strings.TrimSpace(text[start:])
	if lang != "go" && (strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "=")) {
		parts := splitTopLevel(text[start:], '=')
		if len(parts) < 2 {
			return -1, -1
		}
		start += len(parts[0]) + 1
	}
	square, angle := 0, 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '[':
			square++
		case ']':
			square--
		case '<':
			if lang != "go" {
				angle++
			}
		case '>':
			if angle > 0 && text[i-1] != '-' && text[i-1] != '=' {
				angle--
			}
		case '(':
			if square == 0 && angle == 0 {
				depth := 0
				for j := i; j < len(text); j++ {
					switch text[j] {
					case '(', '[', '{':
						depth++
					case ')', ']', '}':
						depth--
						if depth == 0 {
							return i, j
						}
					}
				}
				return -1, -1
			}
		}
	}
	return -1, -1
}

// splitTopLevel splits on sep outside any brackets and strings.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
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
		case '"', '`', '\'':
			quote = c
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}':
			depth--
		case '>':
			if i == 0 || (s[i-1] != '-' && s[i-1] != '=') {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

func parseParameters(list, lang string) []parameter {
	var params []parameter
	var entries []string
	for _, entry := range splitTopLevel(list, ',') {
		if entry = strings.TrimSpace(entry); entry != "" {
			entries = append(entries, entry)
		}
	}
	if lang == "go" {
		return parseGoParameters(entries)
	}
	for _, entry := range entries {
		if lang == "rust" {
			params = append(params, parseRustParameter(entry))
		} else {
			params = append(params, parseTSParameter(entry))
		}
	}
	return params
}

var goTypeKeywords = map[string]bool{"chan": true, "map": true, "func": true, "struct": true, "interface": true}

var goNameThenTypeRe = regexp.MustCompile(`^([A-Za-z_]\w*)\s+(.+)$`)

// parseGoParameters resolves `a, b int` and unnamed lists to one type per parameter.
func parseGoParameters(entries []string) []parameter {
	types := make([]string, len(entries))
	named := make([]bool, len(entries))
	anyNamed := false
	for i, entry := range entries {
		if m := goNameThenTypeRe.FindStringSubmatch(entry); m != nil && !goTypeKeywords[m[1]] {
			types[i], named[i], anyNamed = m[2], true, true
		} else {
			types[i] = entry
		}
	}
	params := make([]parameter, len(entries))
	current := ""
	for i := len(entries) - 1; i >= 0; i-- {
		if named[i] || !anyNamed {
			current = types[i]
		}
		params[i] = parameter{typ: squeeze(current, "go"), optional: strings.HasPrefix(current, "...")}
	}
	return params
}

func parseTSParameter(entry string) parameter {
	rest := strings.HasPrefix(entry, "...")
	entry = strings.TrimPrefix(entry, "...")
	var assign string
	if parts := splitTopLevel(entry, '='); len(parts) > 1 && !strings.HasPrefix(parts[1], ">") {
		entry, assign = parts[0], parts[1]
	}
	parts := splitTopLevel(entry, ':')
	binding := strings.TrimSpace(parts[0])
	typ := ""
	if len(parts) > 1 {
		typ = squeeze(strings.Join(parts[1:], ":"), "ts")
	}
	return parameter{typ: typ, optional: rest || assign != "" || strings.HasSuffix(binding, "?")}
}

func parseRustParameter(entry string) parameter {
	for i := 0; i+1 < len(entry); i++ {
		if entry[i] == ':' && entry[i+1] != ':' && (i == 0 || entry[i-1] != ':') && !strings.ContainsAny(entry[:i], "(<[") {
			return parameter{typ: squeeze(entry[i+1:], "rust")}
		}
	}
	return parameter{typ: squeeze(entry, "rust")} // self, &self, &mut self
}

func compareParameters(before, after []parameter) impact.DeclarationChange {
	result := impact.DeclUnchanged
	if len(after) < len(before) {
		return impact.DeclChanged
	}
	for i := range before {
		if before[i].typ != after[i].typ {
			return impact.DeclChanged
		}
		if before[i].optional && !after[i].optional {
			return impact.DeclChanged
		}
		if !before[i].optional && after[i].optional {
			result = impact.DeclCompatible
		}
	}
	for _, extra := range after[len(before):] {
		if !extra.optional {
			return impact.DeclChanged
		}
		result = impact.DeclCompatible
	}
	return result
}

var (
	goGroupOpenRe = regexp.MustCompile(`^(const|var|type)\s*\(\s*(//.*)?$`)
	goDeclStartRe = regexp.MustCompile(`^\s*(const|var|type|func)\b`)
)

// withGoGroupKeyword gives a Go declaration inside a const, var or type block the
// block's keyword, so `const X = 1` and the line `X = 1` in a `const (` block read
// as the same declaration: moving a constant into a group is not removing it. Only
// a block opening the diff shows counts; when the hunk starts inside the block,
// neither side sees it and both stay as they are.
func withGoGroupKeyword(lines map[int]string, line int, text, lang string) string {
	if lang != "go" || goDeclStartRe.MatchString(text) {
		return text
	}
	for l := line - 1; ; l-- {
		prev, ok := lines[l]
		if !ok {
			return text
		}
		trimmed := strings.TrimSpace(prev)
		if m := goGroupOpenRe.FindStringSubmatch(trimmed); m != nil {
			return m[1] + " " + strings.TrimSpace(text)
		}
		if strings.HasPrefix(trimmed, ")") || (prev != "" && prev[0] != ' ' && prev[0] != '\t' && !strings.HasPrefix(trimmed, "//")) {
			return text // Left the block, or never was in one
		}
	}
}
