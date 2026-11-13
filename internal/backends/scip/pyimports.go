package scip

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	scippb "github.com/sourcegraph/scip/bindings/go/scip"
)

// scip-python resolves an import the way pyright does: from the importing file's
// directory and the configured search paths. A script that reaches a sibling
// directory through sys.path.insert(...) gets no occurrence at all for what it
// imports from there: not the module, not a single use of its names. Every reference,
// caller, consumer and affected test of those names then misses the file, and nothing
// says so. RepairPythonImports reads such files and adds what the indexer left out.
//
// It only acts on an import of a module that is one of the index's own documents
// (found by file name, as a sys.path entry finds it) when the importing document has
// no occurrence of that module's symbols at all: an import the indexer resolved is
// never touched. The uses it adds are read off the text: `alias.name` after
// `import m as alias`, the bare name after `from m import name`, and one hop through
// a module that imported it (`r.lc.price` where r imports m as lc). Only names m
// declares at its top level are added.

// pyRepairDoc is what the first pass keeps of one Python document.
type pyRepairDoc struct {
	path     string
	prefix   string            // The module's symbol prefix, up to and including "`/"
	defs     map[string]string // Top-level name -> symbol
	prefixes map[string]bool   // Module prefixes this document has occurrences of
	utf8     bool              // Columns count bytes rather than UTF-16 units
	starts   map[[2]int32]bool // Starts of the occurrences of a real symbol it already has
	reads    map[int32]bool    // Lines with a read of a real symbol, which on an import line is the import
}

// pyImport is one module an import statement names.
type pyImport struct {
	module string
	alias  string   // `import m as alias`; the module name without `as`
	names  []pyName // `from m import a as b`; empty for `import m`
	line   int      // 0-based line of the module name
	col    int      // Byte column of the module name
	from   bool     // A `from m import` statement
	star   bool     // `from m import *`: every top-level name of m
	span   [2]int   // First and last line of the statement
	target *pyRepairDoc
}

// pyName is a name a `from` import brings in.
type pyName struct {
	name, local string
	line, col   int
}

// RepairPythonImports adds occurrences for the imports scip-python couldn't resolve,
// as described above, gives the occurrences on import lines the import role instead
// of scip-python's read, and rewrites the index at indexPath in place. It returns how
// many occurrences and import lines it changed. An index without Python documents, or
// with nothing to change, is left as it is.
func RepairPythonImports(repoRoot, indexPath string) (int, error) {
	data, err := os.ReadFile(indexPath) // #nosec G304 -- path from the index plan
	if err != nil {
		return 0, err
	}

	docs, err := scanPythonDocs(data)
	if err != nil || len(docs) == 0 {
		return 0, err
	}
	byModule := make(map[string][]*pyRepairDoc)
	for _, d := range docs {
		name := strings.TrimSuffix(strings.TrimSuffix(path.Base(d.path), ".pyi"), ".py")
		if name != "__init__" {
			byModule[name] = append(byModule[name], d)
		}
	}

	// Per document: its source, the imports in it, and which of them didn't resolve.
	type fileImports struct {
		lines      []string
		imports    []pyImport
		unresolved map[string]*pyRepairDoc // alias of an `import m as alias` that didn't resolve -> m
	}
	files := make(map[string]*fileImports, len(docs))
	for _, d := range docs {
		content, readErr := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(d.path))) // #nosec G304 -- a document of the index under the repo root
		if readErr != nil {
			continue
		}
		raw := strings.Split(string(content), "\n")
		f := &fileImports{lines: raw, unresolved: make(map[string]*pyRepairDoc)}
		for _, imp := range parsePythonImports(maskPython(raw)) {
			imp.target = resolvePyModule(byModule[imp.module], d, string(content))
			if imp.target == nil {
				continue
			}
			f.imports = append(f.imports, imp)
			if !imp.from && !d.prefixes[imp.target.prefix] {
				f.unresolved[imp.alias] = imp.target
			}
		}
		files[d.path] = f
	}

	additions := make(map[string][]*scippb.Occurrence)
	importLines := make(map[string]map[int32]bool)
	total := 0
	for _, d := range docs {
		f := files[d.path]
		if f == nil {
			continue
		}
		masked := maskPython(f.lines)
		// scip-python gives the names of an import statement the read role, which
		// outside a function reads as a caller at module level. They're imports.
		for line := range pythonImportLines(masked) {
			if d.reads[line] {
				if importLines[d.path] == nil {
					importLines[d.path] = make(map[int32]bool)
				}
				importLines[d.path][line] = true
				total++
			}
		}
		if len(f.imports) == 0 {
			continue
		}
		add := func(line, col int, name, symbol string, roles int32) {
			start := [2]int32{int32(line), int32(pyColumn(f.lines[line], col, d.utf8))} // #nosec G115 -- line and column of a source file
			if d.starts[start] {
				return
			}
			d.starts[start] = true
			end := int32(pyColumn(f.lines[line], col+len(name), d.utf8)) // #nosec G115 -- column of a source file
			additions[d.path] = append(additions[d.path], &scippb.Occurrence{
				Range:       []int32{start[0], start[1], end},
				Symbol:      symbol,
				SymbolRoles: roles,
			})
			total++
		}
		// `alias.name` for every module reached through an alias: the file's own
		// unresolved imports, and those of a module it imported (one hop).
		qualified := make(map[string]*pyRepairDoc)
		for alias, target := range f.unresolved {
			qualified[regexp.QuoteMeta(alias)] = target
		}
		for _, imp := range f.imports {
			if imp.from {
				continue
			}
			if via := files[imp.target.path]; via != nil {
				for alias, target := range via.unresolved {
					if target != d && !d.prefixes[target.prefix] {
						qualified[regexp.QuoteMeta(imp.alias)+`\s*\.\s*`+regexp.QuoteMeta(alias)] = target
					}
				}
			}
		}
		for pattern, target := range qualified {
			if target == d {
				continue
			}
			re := regexp.MustCompile(`(^|[^\w.])` + pattern + `\s*\.\s*([A-Za-z_]\w*)`)
			for line, text := range masked {
				for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
					name := text[m[4]:m[5]]
					if symbol, ok := target.defs[name]; ok {
						add(line, m[4], name, symbol, SymbolRoleReadAccess)
					}
				}
			}
		}
		// `from m import name`: the import itself and each bare use of the name.
		for _, imp := range f.imports {
			if !imp.from || d.prefixes[imp.target.prefix] || imp.target == d {
				continue
			}
			names := imp.names
			if imp.star {
				for name := range imp.target.defs {
					if !strings.HasPrefix(name, "_") {
						names = append(names, pyName{name: name, local: name, line: -1})
					}
				}
			}
			for _, n := range names {
				symbol, ok := imp.target.defs[n.name]
				if !ok || d.defs[n.local] != "" {
					continue // Not the target's top-level name, or shadowed by the file's own
				}
				if n.line >= 0 {
					add(n.line, n.col, n.name, symbol, SymbolRoleImport)
				}
				re := regexp.MustCompile(`(^|[^\w.])(` + regexp.QuoteMeta(n.local) + `)\b`)
				for line, text := range masked {
					if line >= imp.span[0] && line <= imp.span[1] {
						continue
					}
					for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
						add(line, m[4], n.local, symbol, SymbolRoleReadAccess)
					}
				}
			}
		}
	}
	if total == 0 {
		return 0, nil
	}
	if err := rewriteDocuments(indexPath, data, additions, importLines); err != nil {
		return 0, err
	}
	return total, nil
}

// scanPythonDocs is the first pass: the Python documents and what the repair needs
// of each, without keeping their occurrences.
func scanPythonDocs(data []byte) ([]*pyRepairDoc, error) {
	var docs []*pyRepairDoc
	err := eachIndexField(data, func(num protowire.Number, payload []byte) error {
		if num != indexFieldDocument {
			return nil
		}
		var doc scippb.Document
		if err := proto.Unmarshal(payload, &doc); err != nil {
			return fmt.Errorf("parsing document: %w", err)
		}
		if !strings.HasSuffix(doc.RelativePath, ".py") && !strings.HasSuffix(doc.RelativePath, ".pyi") {
			return nil
		}
		d := &pyRepairDoc{
			path:     doc.RelativePath,
			defs:     make(map[string]string),
			prefixes: make(map[string]bool),
			utf8:     doc.PositionEncoding == scippb.PositionEncoding_UTF8CodeUnitOffsetFromLineStart,
			starts:   make(map[[2]int32]bool, len(doc.Occurrences)),
			reads:    make(map[int32]bool),
		}
		for _, occ := range doc.Occurrences {
			// An unresolved name gets a `local N` occurrence, which the repair's own
			// occurrence goes beside: only a real symbol there means it resolved.
			if len(occ.Range) >= 2 && !strings.HasPrefix(occ.Symbol, "local ") {
				d.starts[[2]int32{occ.Range[0], occ.Range[1]}] = true
				if isImportRead(occ) {
					d.reads[occ.Range[0]] = true
				}
			}
			prefix, rest, ok := pyModulePrefix(occ.Symbol)
			if !ok {
				continue
			}
			d.prefixes[prefix] = true
			if occ.SymbolRoles&SymbolRoleDefinition == 0 {
				continue
			}
			if rest == "__init__:" {
				d.prefix = prefix
				continue
			}
			if name := pyTopLevelName(rest); name != "" {
				d.defs[name] = occ.Symbol
				if d.prefix == "" {
					d.prefix = prefix
				}
			}
		}
		if d.prefix != "" {
			delete(d.prefixes, d.prefix)
			docs = append(docs, d)
		}
		return nil
	})
	return docs, err
}

// pyModulePrefix splits a scip-python symbol of the repo's own code into its module
// prefix ("scip-python python . <version> `a.b`/") and the descriptors after it.
func pyModulePrefix(symbol string) (string, string, bool) {
	if !strings.HasPrefix(symbol, "scip-python ") {
		return "", "", false
	}
	at := strings.Index(symbol, "`/")
	if at < 0 || !strings.Contains(symbol[:at], " `") {
		return "", "", false
	}
	return symbol[:at+2], symbol[at+2:], true
}

// pyTopLevelName is the name a module-level descriptor declares: `price().`,
// `PRICES.` or `Call#`, not a method, parameter or nested name.
func pyTopLevelName(rest string) string {
	for _, suffix := range []string{"().", "#", "."} {
		if name, ok := strings.CutSuffix(rest, suffix); ok {
			if name != "" && !strings.ContainsAny(name, "#.()/:`[") {
				return name
			}
			return ""
		}
	}
	return ""
}

// resolvePyModule picks the document an import of a module name reaches. One
// document of that name is it; with several, the one beside the importer, else the
// only one whose directory the importer spells (as sys.path.insert does).
func resolvePyModule(candidates []*pyRepairDoc, importer *pyRepairDoc, content string) *pyRepairDoc {
	var others []*pyRepairDoc
	for _, c := range candidates {
		if c != importer {
			others = append(others, c)
		}
	}
	switch len(others) {
	case 0:
		return nil
	case 1:
		return others[0]
	}
	dir := path.Dir(importer.path)
	var spelled []*pyRepairDoc
	for _, c := range others {
		if path.Dir(c.path) == dir {
			return c
		}
		if base := path.Base(path.Dir(c.path)); base != "." && strings.Contains(content, `"`+base+`"`) {
			spelled = append(spelled, c)
		}
	}
	if len(spelled) == 1 {
		return spelled[0]
	}
	return nil
}

var (
	pyImportStmt = regexp.MustCompile(`^(\s*)import\s+`)
	pyFromStmt   = regexp.MustCompile(`^\s*from\s+([A-Za-z_]\w*)\s+import\s+`)
	pyImportItem = regexp.MustCompile(`([A-Za-z_][\w.]*)(?:\s+as\s+([A-Za-z_]\w*))?`)
)

// parsePythonImports finds `import m [as a], ...` and `from m import (a [as b], ...)`
// of plain module names; relative and dotted modules are left to the indexer. lines
// are masked: no strings or comments.
func parsePythonImports(lines []string) []pyImport {
	var out []pyImport
	for i := 0; i < len(lines); i++ {
		text := lines[i]
		if m := pyFromStmt.FindStringSubmatchIndex(text); m != nil {
			imp := pyImport{module: text[m[2]:m[3]], line: i, col: m[2], from: true, span: [2]int{i, i}}
			rest, at, line := text[m[1]:], m[1], i
			if strings.HasPrefix(strings.TrimSpace(rest), "*") {
				imp.star = true
				out = append(out, imp)
				continue
			}
			open := strings.HasPrefix(strings.TrimSpace(rest), "(")
			for {
				for _, item := range pyImportItem.FindAllStringSubmatchIndex(rest, -1) {
					name := rest[item[2]:item[3]]
					if strings.Contains(name, ".") || name == "import" {
						continue
					}
					local := name
					if item[4] >= 0 {
						local = rest[item[4]:item[5]]
					}
					imp.names = append(imp.names, pyName{name: name, local: local, line: line, col: at + item[2]})
				}
				if !open || strings.Contains(rest, ")") || line+1 >= len(lines) {
					break
				}
				line++
				rest, at = lines[line], 0
			}
			imp.span[1] = line
			i = line
			out = append(out, imp)
			continue
		}
		if m := pyImportStmt.FindStringIndex(text); m != nil {
			rest := text[m[1]:]
			for _, item := range pyImportItem.FindAllStringSubmatchIndex(rest, -1) {
				name := rest[item[2]:item[3]]
				if strings.Contains(name, ".") {
					continue
				}
				alias := name
				if item[4] >= 0 {
					alias = rest[item[4]:item[5]]
				}
				out = append(out, pyImport{module: name, alias: alias, line: i, col: m[1] + item[2], span: [2]int{i, i}})
			}
		}
	}
	return out
}

// maskPython blanks comments and the contents of strings, keeping every column, so
// a name inside a string or after # isn't taken for a use.
func maskPython(lines []string) []string {
	out := make([]string, len(lines))
	var quote string // The open string's delimiter, carried over lines only when triple
	for i, line := range lines {
		b := []byte(line)
		for j := 0; j < len(b); j++ {
			if quote != "" {
				if b[j] == '\\' && len(quote) == 1 && j+1 < len(b) {
					b[j], b[j+1] = ' ', ' '
					j++
					continue
				}
				if strings.HasPrefix(string(b[j:]), quote) {
					j += len(quote) - 1
					quote = ""
					continue
				}
				b[j] = ' '
				continue
			}
			switch b[j] {
			case '#':
				for k := j; k < len(b); k++ {
					b[k] = ' '
				}
				j = len(b)
			case '"', '\'':
				quote = string(b[j])
				if j+2 < len(b) && b[j+1] == b[j] && b[j+2] == b[j] {
					quote = strings.Repeat(quote, 3)
				}
				j += len(quote) - 1
			}
		}
		if len(quote) == 1 {
			quote = "" // An unterminated short string ends with its line
		}
		out[i] = string(b)
	}
	return out
}

// pyColumn converts a byte column into the document's position encoding: bytes, or
// the UTF-16 units scip-python (pyright) counts by default.
func pyColumn(line string, byteCol int, utf8Cols bool) int {
	if byteCol > len(line) {
		byteCol = len(line)
	}
	if utf8Cols {
		return byteCol
	}
	units := 0
	for _, r := range line[:byteCol] {
		if r == utf8.RuneError {
			units++
			continue
		}
		units += len(utf16.Encode([]rune{r}))
	}
	return units
}

// eachIndexField calls fn with every length-delimited top-level field of an index.
func eachIndexField(data []byte, fn func(protowire.Number, []byte) error) error {
	for b := data; len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("invalid field tag")
		}
		b = b[n:]
		size := protowire.ConsumeFieldValue(num, typ, b)
		if size < 0 {
			return fmt.Errorf("malformed field %d", num)
		}
		value := b[:size]
		b = b[size:]
		if typ != protowire.BytesType {
			continue
		}
		payload, consumed := protowire.ConsumeBytes(value)
		if consumed < 0 {
			return fmt.Errorf("malformed field %d", num)
		}
		if err := fn(num, payload); err != nil {
			return err
		}
	}
	return nil
}

// rewriteDocuments writes data back to indexPath with the occurrences appended to
// the documents they belong to, atomically, keeping every other field as it was.
func rewriteDocuments(indexPath string, data []byte, additions map[string][]*scippb.Occurrence, importLines map[string]map[int32]bool) (err error) {
	info, err := os.Stat(indexPath)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(indexPath), ".index-pyimports-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	out := bufio.NewWriter(tmp)
	for b := data; len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return fmt.Errorf("%s: invalid field tag", indexPath)
		}
		tag := b[:n]
		b = b[n:]
		size := protowire.ConsumeFieldValue(num, typ, b)
		if size < 0 {
			return fmt.Errorf("%s: malformed field %d", indexPath, num)
		}
		value := b[:size]
		b = b[size:]
		if typ == protowire.BytesType && num == indexFieldDocument {
			payload, _ := protowire.ConsumeBytes(value)
			var doc scippb.Document
			if err = proto.Unmarshal(payload, &doc); err != nil {
				return fmt.Errorf("%s: parsing document: %w", indexPath, err)
			}
			extra, imports := additions[doc.RelativePath], importLines[doc.RelativePath]
			if len(extra) > 0 || len(imports) > 0 {
				for _, occ := range doc.Occurrences {
					if len(occ.Range) >= 2 && imports[occ.Range[0]] && isImportRead(occ) {
						occ.SymbolRoles = occ.SymbolRoles&^SymbolRoleReadAccess | SymbolRoleImport
					}
				}
				doc.Occurrences = append(doc.Occurrences, extra...)
				sort.SliceStable(doc.Occurrences, func(i, j int) bool {
					a, c := doc.Occurrences[i].Range, doc.Occurrences[j].Range
					if len(a) < 2 || len(c) < 2 {
						return false
					}
					if a[0] != c[0] {
						return a[0] < c[0]
					}
					return a[1] < c[1]
				})
				rewritten, marshalErr := proto.Marshal(&doc)
				if marshalErr != nil {
					return marshalErr
				}
				value = protowire.AppendBytes(nil, rewritten)
			}
		}
		if _, err = out.Write(tag); err != nil {
			return err
		}
		if _, err = out.Write(value); err != nil {
			return err
		}
	}
	if err = out.Flush(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), indexPath)
}

// isImportRead is an occurrence scip-python writes for a name an import statement
// names: a read of a real symbol, not a definition and not yet an import.
func isImportRead(occ *scippb.Occurrence) bool {
	return occ.SymbolRoles&SymbolRoleReadAccess != 0 &&
		occ.SymbolRoles&(SymbolRoleDefinition|SymbolRoleImport|SymbolRoleWriteAccess) == 0 &&
		!strings.HasPrefix(occ.Symbol, "local ")
}

// pythonImportLines are the 0-based lines of import statements, including the
// continuation lines of a parenthesized `from m import (...)`, dotted and relative
// modules too. lines are masked.
func pythonImportLines(lines []string) map[int32]bool {
	out := make(map[int32]bool)
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, "import ") && !(strings.HasPrefix(text, "from ") && strings.Contains(text, " import")) {
			continue
		}
		out[int32(i)] = true // #nosec G115 -- line of a source file
		open := strings.Count(text, "(") - strings.Count(text, ")")
		for open > 0 && i+1 < len(lines) {
			i++
			out[int32(i)] = true // #nosec G115 -- line of a source file
			open += strings.Count(lines[i], "(") - strings.Count(lines[i], ")")
		}
	}
	return out
}
