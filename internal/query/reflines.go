package query

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/diff"
)

// maxReferenceCode bounds a line of code in a reference list.
const maxReferenceCode = 200

// ReferenceLines is a symbol's references in grep's shape: `file:line: code`, one per
// reference, each with what the line does with the symbol. A position alone sends the
// reader to the file; the line usually answers the question.
type ReferenceLines struct {
	Symbol string          `json:"symbol"`
	Lines  []ReferenceLine `json:"lines"`
	// Imports counts the references that only import the symbol. They aren't listed:
	// every file that uses it has one.
	Imports   int  `json:"imports,omitempty"`
	Total     int  `json:"total"`
	Tests     int  `json:"tests,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
	// Stale counts the lines that no longer name the symbol: the file changed since
	// the index was built, and the use is somewhere else in it now.
	Stale int `json:"stale,omitempty"`
	// OutsideIndex are files of the symbol's language that spell its name and that the
	// index doesn't cover, such as ones a tsconfig leaves out: whether they use it, only
	// reading them says.
	OutsideIndex []string `json:"outsideIndex,omitempty"`
}

// refStale marks a line the index points at that doesn't name the symbol anymore.
const refStale = "stale"

// ReferenceLine is one reference.
type ReferenceLine struct {
	File string `json:"file"`
	Line int    `json:"line"`
	// Use is def, call, literal, write, type or read, or stale where the line no
	// longer names the symbol.
	Use  string `json:"use"`
	Code string `json:"code"`
	// In is the definition the reference sits in, where the index says.
	In     string `json:"in,omitempty"`
	IsTest bool   `json:"isTest,omitempty"`
}

// BuildReferenceLines reads the line of each reference from the working tree and
// classifies it. A file that can't be read keeps its position, without code.
func BuildReferenceLines(repoRoot, symbolID string, resp *FindReferencesResponse) *ReferenceLines {
	name := scip.SymbolSimpleName(symbolID)
	if name == "" {
		name = leafName(symbolID)
	}
	// A constructor's uses spell its type; in TypeScript its declaration says `constructor`.
	out := &ReferenceLines{Symbol: name, Total: resp.TotalCount, Truncated: resp.Truncated}
	declared := name
	if class := scip.ConstructorType(symbolID); class != "" {
		name, declared = class, "constructor"
	}
	// A Go package is named by its import path; its uses spell the package name.
	importPath := ""
	if strings.HasSuffix(symbolID, "/") && strings.Contains(name, "/") {
		importPath, name = name, goPackageName(name)
		out.Symbol, declared = name, name
	}
	isType := strings.HasSuffix(symbolID, "#")
	files := make(map[string][]string)
	aliases := make(map[string][]string)
	read := func(path string) []string {
		lines, ok := files[path]
		if !ok {
			if content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path))); err == nil {
				lines = splitFileLines(string(content))
				aliases[path] = importAliases(string(content), name, importPath)
			}
			files[path] = lines
		}
		return lines
	}
	// The binding an import line gives the symbol, where it is another name:
	// `import Foo from`, `import { a as b }`, `from m import a as b`. The index
	// points at it, so the token there is the name the file's uses spell.
	for _, r := range resp.References {
		if r.Location == nil || r.Kind == "definition" {
			continue
		}
		path := r.Location.FileId
		lines := read(path)
		if diff.ReferenceUse(diff.TypeLanguage(path), lines, r.Location.StartLine, r.Location.StartColumn, name, isType, false) != diff.RefImport {
			continue
		}
		if b := identifierAt(lines, r.Location.StartLine, r.Location.StartColumn); b != "" && b != name && !slices.Contains(aliases[path], b) {
			aliases[path] = append(aliases[path], b)
		}
	}
	seen := make(map[string]bool)
	for _, r := range resp.References {
		if r.Location == nil {
			continue
		}
		path := r.Location.FileId
		key := fmt.Sprintf("%s:%d", path, r.Location.StartLine)
		if seen[key] {
			continue // Twice on one line is one line to read
		}
		seen[key] = true
		lines := read(path)
		code := ""
		if r.Location.StartLine >= 1 && r.Location.StartLine <= len(lines) {
			code = strings.TrimSpace(lines[r.Location.StartLine-1])
		}
		// A use through `import { name as alias }` spells the alias.
		spelled := name
		if !strings.Contains(code, name) {
			for _, a := range aliases[path] {
				if containsWord(code, a) {
					spelled = a
					break
				}
			}
		}
		use := diff.ReferenceUse(diff.TypeLanguage(path), lines, r.Location.StartLine, r.Location.StartColumn, spelled, isType, r.Kind == "definition")
		if use == diff.RefImport {
			out.Imports++
			continue
		}
		if r.IsTest {
			out.Tests++
		}
		// The file moved on since the index was built: the line is no longer the use.
		if lines != nil && !strings.Contains(code, spelled) && !(r.Kind == "definition" && strings.Contains(code, declared)) {
			use = refStale
			out.Stale++
		}
		if len(code) > maxReferenceCode {
			code = strings.ToValidUTF8(code[:maxReferenceCode], "") + " …"
		}
		out.Lines = append(out.Lines, ReferenceLine{File: path, Line: r.Location.StartLine, Use: use, Code: code, In: r.From, IsTest: r.IsTest})
	}
	return out
}

// Text prints the references one per line, and a last line that counts them. With
// bySymbol they come under the definition each sits in.
func (r *ReferenceLines) Text(bySymbol bool) string {
	var b strings.Builder
	line := func(indent string, l ReferenceLine) {
		fmt.Fprintf(&b, "%s%s:%d: [%s] %s\n", indent, l.File, l.Line, l.Use, l.Code)
	}
	if !bySymbol {
		for _, l := range r.Lines {
			line("", l)
		}
	} else {
		var order []string
		groups := make(map[string][]ReferenceLine)
		for _, l := range r.Lines {
			head := l.In
			if head == "" {
				head = "(top level)"
			}
			if l.IsTest {
				head += " (test)"
			}
			if _, ok := groups[head]; !ok {
				order = append(order, head)
			}
			groups[head] = append(groups[head], l)
		}
		for _, head := range order {
			b.WriteString(head + "\n")
			for _, l := range groups[head] {
				line("  ", l)
			}
		}
	}

	for _, path := range r.OutsideIndex {
		fmt.Fprintf(&b, "%s: [not in index]\n", path)
	}
	summary := r.Symbol + ": " + plural(r.Total, "reference")
	if r.Tests > 0 {
		summary += fmt.Sprintf(", %d in tests", r.Tests)
	}
	if r.Imports > 0 {
		summary += "; " + plural(r.Imports, "import") + " not listed"
	}
	if r.Truncated {
		summary += "; the list is cut, raise the limit for the rest"
	}
	if r.Stale > 0 {
		summary += fmt.Sprintf("; %d marked stale no longer name it, the file changed since the index was built: run 'ckb index'", r.Stale)
	}
	if n := len(r.OutsideIndex); n > 0 {
		summary += fmt.Sprintf("; %s the index doesn't cover name it, marked [not in index]: only reading them says whether they use it", plural(n, "file"))
	}
	b.WriteString(summary + "\n")
	return b.String()
}

// FilesOutsideIndex lists the files of the symbol's language that spell its name and
// that the index doesn't cover. Nil without an index or a definition to take the
// language from.
func (e *Engine) FilesOutsideIndex(symbolID string) []string {
	if e.scipAdapter == nil {
		return nil
	}
	index := e.scipAdapter.GetIndex()
	if index == nil || index.DefinitionIndex == nil {
		return nil
	}
	def := index.DefinitionIndex[symbolID]
	names := scip.SpelledNames(symbolID)
	if def == nil || def.Doc == nil || len(names) == 0 {
		return nil
	}
	family := sourceFamily(def.Doc.RelativePath)
	if family == "" {
		return nil
	}
	// The first is what a use spells: the type, for a constructor.
	outside, _ := filesOutsideIndex(e.repoRoot, map[string]string{names[0]: family}, func(path string) bool {
		return index.GetDocument(path) != nil
	})
	return outside
}

// importAliases are the names a file gives name with `name as alias` (an import or
// export renaming in TypeScript and Python, a `use` in Rust) and, for a Go package,
// with `alias "importPath"`.
func importAliases(content, name, importPath string) []string {
	var out []string
	add := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			if a := m[1]; a != name && a != "_" && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	if name != "" && strings.Contains(content, name) {
		add(regexp.MustCompile(`(?:^|[^\w$])` + regexp.QuoteMeta(name) + `\s+as\s+([A-Za-z_$][\w$]*)`))
	}
	if importPath != "" && strings.Contains(content, importPath) {
		add(regexp.MustCompile(`(?m)(?:^|[\s(])([A-Za-z_]\w*)\s+"` + regexp.QuoteMeta(importPath) + `"`))
	}
	return out
}

// goPackageName is the name a Go import path's package is used by without an
// alias: its last element, the one before a major version (`…/v2`), and
// gopkg.in's `yaml.v3` without the version.
func goPackageName(importPath string) string {
	parts := strings.Split(strings.TrimSuffix(importPath, "/"), "/")
	last := parts[len(parts)-1]
	if len(parts) > 1 && goMajorVersion.MatchString(last) {
		last = parts[len(parts)-2]
	}
	if i := strings.Index(last, ".v"); i > 0 && goMajorVersion.MatchString(last[i+1:]) {
		last = last[:i]
	}
	return last
}

var goMajorVersion = regexp.MustCompile(`^v[0-9]+$`)

// identifierAt is the identifier at a 1-based line and column, or "".
func identifierAt(lines []string, line, column int) string {
	if line < 1 || line > len(lines) || column < 1 {
		return ""
	}
	text := lines[line-1]
	isIdent := func(c byte) bool {
		return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	start := column - 1
	if start >= len(text) || !isIdent(text[start]) {
		return ""
	}
	end := start
	for end < len(text) && isIdent(text[end]) {
		end++
	}
	return text[start:end]
}

// containsWord reports whether word appears in text as a whole identifier.
func containsWord(text, word string) bool {
	isIdent := func(c byte) bool {
		return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		j += i
		end := j + len(word)
		if (j == 0 || !isIdent(text[j-1])) && (end == len(text) || !isIdent(text[end])) {
			return true
		}
		i = j + 1
	}
}
