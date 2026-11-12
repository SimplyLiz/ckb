package query

import (
	"fmt"
	"os"
	"path/filepath"
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
	out := &ReferenceLines{Symbol: name, Total: resp.TotalCount, Truncated: resp.Truncated}
	isType := strings.HasSuffix(symbolID, "#")
	files := make(map[string][]string)
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
		lines, read := files[path]
		if !read {
			if content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path))); err == nil {
				lines = splitFileLines(string(content))
			}
			files[path] = lines
		}
		use := diff.ReferenceUse(diff.TypeLanguage(path), lines, r.Location.StartLine, r.Location.StartColumn, name, isType, r.Kind == "definition")
		if use == diff.RefImport {
			out.Imports++
			continue
		}
		if r.IsTest {
			out.Tests++
		}
		code := ""
		if r.Location.StartLine >= 1 && r.Location.StartLine <= len(lines) {
			code = strings.TrimSpace(lines[r.Location.StartLine-1])
		}
		// The file moved on since the index was built: the line is no longer the use.
		if lines != nil && !strings.Contains(code, name) {
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
	b.WriteString(summary + "\n")
	return b.String()
}
