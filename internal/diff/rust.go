package diff

import (
	"regexp"
	"sort"
	"strings"
)

// This file reads the Rust declarations whose change the index can't show: what is in a
// struct's braces, and what a module re-exports. Text, not types, like the rest here.

// RustStructFields returns a struct's named fields, each as its name and type with
// attributes, comments and visibility removed, sorted. False means the file has no
// struct of that name with braces.
func RustStructFields(lines []string, name string) ([]string, bool) {
	re := regexp.MustCompile(`^\s*(pub(\s*\([^)]*\))?\s+)?struct\s+` + regexp.QuoteMeta(name) + `\b`)
	for i, text := range lines {
		m := re.FindStringIndex(text)
		if m == nil {
			continue
		}
		body, ok := bracedBody(lines, i, m[1])
		if !ok {
			return nil, false
		}
		// A lifetime's quote would read as the start of a string.
		body = strings.ReplaceAll(rustBlockCommentRe.ReplaceAllString(body, ""), "'", "")
		var fields []string
		for _, entry := range splitTopLevel(body, ',') {
			entry = rustAttrRe.ReplaceAllString(entry, "")
			entry, _ = stripVisibility(entry, "rust")
			if squeezed := squeeze(entry, "rust"); squeezed != "" {
				fields = append(fields, squeezed)
			}
		}
		sort.Strings(fields)
		return fields, true
	}
	return nil, false
}

var (
	rustAttrRe         = regexp.MustCompile(`#\[[^\]]*\]`)
	rustBlockCommentRe = regexp.MustCompile(`/\*.*?\*/`)
)

// maxRustBodyLines bounds how far a struct's or enum's body is read. 500 was too few: a
// clap `enum Commands` with a doc comment on every field runs 590 lines.
const maxRustBodyLines = 5000

// bracedBody returns the text between the first brace at or after column col of line
// start and its partner, with strings, chars and comments blanked: a `}` or `//` in an
// attribute's string is no part of the body's structure. A `;` or `(` first, outside
// generics and a where clause, means a unit or tuple struct, which has none. col is past the name, so the
// `(` of `pub(crate)` is behind it.
func bracedBody(lines []string, start, col int) (string, bool) {
	var body strings.Builder
	code := newRustCode()
	depth, angle := 0, 0
	where := false // A `(` in a where clause, `F: Fn(u8)`, is no tuple struct's
	for i := start; i < len(lines) && i < start+maxRustBodyLines; i++ {
		text := code.line(lines[i])
		j := 0
		if i == start {
			j = min(col, len(text))
		}
		for ; j < len(text); j++ {
			c := text[j]
			switch {
			case c == '{':
				depth++
				if depth == 1 {
					continue
				}
			case c == '}':
				depth--
				if depth == 0 {
					return body.String(), true
				}
			case depth == 0 && c == '<':
				angle++
			case depth == 0 && c == '>' && angle > 0 && (j == 0 || text[j-1] != '-'):
				angle--
			case depth == 0 && angle == 0 && strings.HasPrefix(text[j:], "where") && (j == 0 || !isIdentByte(text[j-1])) && (j+5 == len(text) || !isIdentByte(text[j+5])):
				where = true
			case depth == 0 && angle == 0 && (c == ';' || c == '(' && !where):
				return "", false
			}
			if depth > 0 {
				body.WriteByte(c)
			}
		}
		if depth > 0 {
			body.WriteByte(' ')
		}
	}
	return "", false
}

var (
	rustPathTailRe = regexp.MustCompile(`(?:[A-Za-z_]\w*::)+$`)
	// rustReturnArrowRe is a return arrow, with the reference or pointer it may return.
	rustReturnArrowRe = regexp.MustCompile(`->(?:\s*(?:&(?:'\w+\s*)?(?:mut\s*)?|\*(?:const|mut)\s*))*$`)
)

// IsRustLiteralUse says whether the name on a line builds or destructures a struct by its
// fields, in a way a changed field set breaks: without `..` to cover the rest.
func IsRustLiteralUse(lines []string, line, column int, name string) bool {
	_, hasRest, ok := rustLiteral(lines, line, column, name)
	return ok && !hasRest
}

// maxRustLiteralLines bounds how far a struct literal's braces are followed. A call's
// 40 lines are too few: a struct of fifty fields is built in as many lines.
const maxRustLiteralLines = 1000

// rustLiteral finds the literal or pattern starting at the name, and returns its last
// line and whether it has `..`. `impl Foo {`, `-> Foo {` and `-> &mut Foo {` have a brace
// after the name too, but what follows them is a body.
func rustLiteral(lines []string, line, column int, name string) (end int, hasRest, ok bool) {
	text := rawLine(lines, line)
	start := nameIndex(text, column, name)
	if start < 0 {
		return 0, false, false
	}
	before := strings.TrimSpace(rustPathTailRe.ReplaceAllString(strings.TrimRight(text[:start], " "), ""))
	if strings.HasPrefix(strings.TrimSpace(text), "impl") || rustReturnArrowRe.MatchString(before) ||
		strings.HasSuffix(before, " for") || before == "for" {
		return 0, false, false
	}
	rest := strings.TrimLeft(text[start+len(name):], " ")
	if strings.HasPrefix(rest, "::<") {
		close := strings.Index(rest, ">")
		if close < 0 {
			return 0, false, false
		}
		rest = strings.TrimLeft(rest[close+1:], " ")
	}
	if !strings.HasPrefix(rest, "{") {
		return 0, false, false
	}

	depth := 0
	code := newRustCode()
	scan := code.line(rest)
	for current := line; current <= line+maxRustLiteralLines; current++ {
		if current != line {
			scan = code.line(rawLine(lines, current))
		}
		for i := 0; i < len(scan); i++ {
			switch scan[i] {
			case '{', '(', '[':
				depth++
			case '}', ')', ']':
				depth--
				if depth == 0 {
					return current, hasRest, true
				}
			case '.':
				if depth == 1 && i+1 < len(scan) && scan[i+1] == '.' {
					hasRest = true
				}
			}
		}
	}
	return 0, false, false
}

var (
	rustReexportRe = regexp.MustCompile(`^\s*pub(\s*\([^)]*\))?\s+use\s+`)
	rustAliasRe    = regexp.MustCompile(`^(pub(\s*\([^)]*\))?\s+)?type\s`)
	rustUseItemRe  = regexp.MustCompile(`([A-Za-z_]\w*)(?:\s+as\s+([A-Za-z_]\w*))?\s*$`)
)

// IsRustTypeAlias says whether a declaration's text is a type alias.
func IsRustTypeAlias(declaration string) bool {
	return rustAliasRe.MatchString(strings.TrimSpace(declaration))
}

// RustReexports returns the names a file re-exports with `pub use`. A glob adds nothing,
// since what it brought in has no name here.
func RustReexports(lines []string) map[string]bool {
	names := make(map[string]bool)
	for i := 0; i < len(lines); i++ {
		if !rustReexportRe.MatchString(lines[i]) {
			continue
		}
		var statement strings.Builder
		for ; i < len(lines); i++ {
			statement.WriteString(lines[i])
			statement.WriteByte(' ')
			if strings.Contains(lines[i], ";") {
				break
			}
		}
		text := rustReexportRe.ReplaceAllString(statement.String(), "")
		if end := strings.Index(text, ";"); end >= 0 {
			text = text[:end]
		}
		collectUseNames(text, names)
	}
	return names
}

func collectUseNames(tree string, names map[string]bool) {
	tree = strings.TrimSpace(tree)
	if open := strings.Index(tree, "{"); open >= 0 {
		if closeAt := strings.LastIndex(tree, "}"); closeAt > open {
			for _, item := range splitTopLevel(tree[open+1:closeAt], ',') {
				collectUseNames(item, names)
			}
		}
		return
	}
	if m := rustUseItemRe.FindStringSubmatch(tree); m != nil {
		name := m[1]
		if m[2] != "" {
			name = m[2]
		}
		if name != "self" && name != "super" && name != "crate" {
			names[name] = true
		}
	}
}
