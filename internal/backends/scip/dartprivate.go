package scip

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"
)

// scip_dart gives every library-private declaration, anything named with a
// leading underscore, a "local N" ID: `_SyncSettingsTileState`, its `_syncNow`
// and `_result`, a top-level `_boot()`. Flutter keeps nearly every State class
// private, so a Flutter app's widgets were missing from searchSymbols, and
// everything they call had no caller: a local ID is unique only within its
// document and never becomes a call graph node. The index carries no names
// for them (no DisplayName), but the source does, at the definition's range.
//
// nameDartPrivates gives such declarations the ID scip_dart gives their public
// neighbours, `<file>/_SyncSettingsTileState#_syncNow().`, in the one
// document that defines them. A reference from another file of the same
// library (a part file, `part 'a.g.dart';`) carries a local number of that
// document; linkDartParts gives it the declaration's ID when the library
// declares that private name once. Anything the source doesn't confirm (a file
// changed since indexing, a name that isn't one identifier) keeps its local ID.

var dartPrivateName = regexp.MustCompile(`^_[A-Za-z0-9_$]*$`)

// dartFile is what linkDartParts needs to know of one Dart document.
type dartFile struct {
	doc    *Document
	partOf string            // the library file a `part of '…';` names, "" for a library
	parts  []string          // the files its `part '…';` directives name
	decls  map[string]string // private name → ID declared here, "" when two share the name
	refs   map[string]string // local ID → the private name it spells, for locals used but not defined here
}

var (
	dartPartOf = regexp.MustCompile(`(?m)^\s*part\s+of\s+['"]([^'"]+)['"]\s*;`)
	dartPart   = regexp.MustCompile(`(?m)^\s*part\s+['"]([^'"]+)['"]\s*;`)
)

// nameDartPrivates renames doc's private declarations in place, reading the
// source from root, and records the leading lines of its top-level variables.
// Non-Dart documents and documents whose source can't be read are left alone
// and give nil.
func nameDartPrivates(doc *Document, root string) *dartFile {
	if root == "" || !strings.HasSuffix(doc.RelativePath, ".dart") {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc.RelativePath))) // #nosec G304 -- a document of the repo's own index
	if err != nil {
		return nil
	}
	lines := strings.Split(string(src), "\n")
	kinds := make(map[string]int32, len(doc.Symbols))
	for _, sym := range doc.Symbols {
		kinds[sym.Symbol] = sym.Kind
	}
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition != 0 && len(occ.Range) > 0 && isTopLevelVariable(occ.Symbol, kinds[occ.Symbol]) {
			if doc.dartLeads == nil {
				doc.dartLeads = make(map[int]int)
			}
			line := int(occ.Range[0])
			doc.dartLeads[line] = dartLeadStart(lines, line)
		}
	}
	renameDartPrivates(doc, lines, kinds)
	return describeDartFile(doc, lines, string(src))
}

// renameDartPrivates gives the local IDs of doc's private declarations the
// IDs their names call for.
func renameDartPrivates(doc *Document, lines []string, kinds map[string]int32) {
	prefix := dartFilePrefix(doc)
	if prefix == "" {
		return
	}
	hasLocalDecl := false
	for _, sym := range doc.Symbols {
		if IsLocalSymbol(sym.Symbol) && dartRenamedKind(sym.Kind) {
			hasLocalDecl = true
		}
	}
	if !hasLocalDecl {
		return
	}

	renamed := make(map[string]string)
	used := make(map[string]bool)
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition != 0 && !IsLocalSymbol(occ.Symbol) {
			used[occ.Symbol] = true
		}
	}

	// container is the type whose members follow in document order: scip_dart
	// writes a class's members right after its definition.
	container := ""
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 || occ.Symbol == "" {
			continue
		}
		if !IsLocalSymbol(occ.Symbol) {
			container = dartContainerAfter(occ.Symbol, container)
			continue
		}
		if _, done := renamed[occ.Symbol]; done {
			continue
		}
		kind := bindingscip.SymbolInformation_Kind(kinds[occ.Symbol])
		if !dartRenamedKind(int32(kind)) {
			continue
		}
		name := identifierAt(lines, occ.Range)
		if !dartPrivateName.MatchString(name) {
			continue
		}
		var id string
		switch kind {
		case bindingscip.SymbolInformation_Class, bindingscip.SymbolInformation_Enum,
			bindingscip.SymbolInformation_Mixin, bindingscip.SymbolInformation_Extension,
			bindingscip.SymbolInformation_TypeAlias:
			id = prefix + name + "#"
		case bindingscip.SymbolInformation_Method, bindingscip.SymbolInformation_StaticMethod,
			bindingscip.SymbolInformation_Constructor, bindingscip.SymbolInformation_Getter,
			bindingscip.SymbolInformation_Setter:
			if container == "" {
				continue
			}
			id = container + name + "()."
		case bindingscip.SymbolInformation_Field, bindingscip.SymbolInformation_StaticField:
			if container == "" {
				id = prefix + name + "."
			} else {
				id = container + name + "."
			}
		case bindingscip.SymbolInformation_Function:
			id = prefix + name + "()."
		case bindingscip.SymbolInformation_Variable, bindingscip.SymbolInformation_Constant:
			// Only a top-level declaration has a Kind here; a variable in a
			// body comes without one.
			id = prefix + name + "."
		}
		if id == "" || used[id] {
			continue
		}
		used[id] = true
		renamed[occ.Symbol] = id
		switch kind {
		case bindingscip.SymbolInformation_Class, bindingscip.SymbolInformation_Enum,
			bindingscip.SymbolInformation_Mixin, bindingscip.SymbolInformation_Extension:
			container = id
		case bindingscip.SymbolInformation_Function, bindingscip.SymbolInformation_Variable,
			bindingscip.SymbolInformation_Constant, bindingscip.SymbolInformation_TypeAlias:
			container = ""
		}
	}
	if len(renamed) == 0 {
		return
	}
	for _, occ := range doc.Occurrences {
		if id, ok := renamed[occ.Symbol]; ok {
			occ.Symbol = id
		}
	}
	for _, sym := range doc.Symbols {
		if id, ok := renamed[sym.Symbol]; ok {
			sym.Symbol = id
			if sym.DisplayName == "" {
				sym.DisplayName = SymbolSimpleName(id)
			}
		}
	}
}

// dartRenamedKind reports whether a local of this Kind is a declaration
// nameDartPrivates can name.
func dartRenamedKind(kind int32) bool {
	switch bindingscip.SymbolInformation_Kind(kind) {
	case bindingscip.SymbolInformation_Class, bindingscip.SymbolInformation_Enum,
		bindingscip.SymbolInformation_Mixin, bindingscip.SymbolInformation_Extension,
		bindingscip.SymbolInformation_TypeAlias,
		bindingscip.SymbolInformation_Method, bindingscip.SymbolInformation_StaticMethod,
		bindingscip.SymbolInformation_Constructor, bindingscip.SymbolInformation_Getter,
		bindingscip.SymbolInformation_Setter,
		bindingscip.SymbolInformation_Field, bindingscip.SymbolInformation_StaticField,
		bindingscip.SymbolInformation_Function,
		bindingscip.SymbolInformation_Variable, bindingscip.SymbolInformation_Constant:
		return true
	}
	return false
}

// describeDartFile collects what linkDartParts needs of doc: its part
// directives, the private names it declares, and the private names its
// unresolved locals spell.
func describeDartFile(doc *Document, lines []string, src string) *dartFile {
	f := &dartFile{doc: doc, decls: make(map[string]string), refs: make(map[string]string)}
	dir := path.Dir(doc.RelativePath)
	if m := dartPartOf.FindStringSubmatch(src); m != nil {
		if !strings.Contains(m[1], ":") {
			f.partOf = path.Join(dir, m[1])
		}
	} else {
		for _, m := range dartPart.FindAllStringSubmatch(src, -1) {
			if !strings.Contains(m[1], ":") {
				f.parts = append(f.parts, path.Join(dir, m[1]))
			}
		}
	}
	defined := make(map[string]bool)
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 || occ.Symbol == "" {
			continue
		}
		defined[occ.Symbol] = true
		if IsLocalSymbol(occ.Symbol) {
			continue
		}
		name := SymbolSimpleName(occ.Symbol)
		if !dartPrivateName.MatchString(name) {
			continue
		}
		if id, seen := f.decls[name]; seen && id != occ.Symbol {
			f.decls[name] = ""
		} else {
			f.decls[name] = occ.Symbol
		}
	}
	for _, occ := range doc.Occurrences {
		if !IsLocalSymbol(occ.Symbol) || defined[occ.Symbol] {
			continue
		}
		name := identifierAt(lines, occ.Range)
		if prev, seen := f.refs[occ.Symbol]; seen && prev != name {
			name = "" // occurrences that disagree: the source changed
		}
		f.refs[occ.Symbol] = name
	}
	for local, name := range f.refs {
		if !dartPrivateName.MatchString(name) {
			delete(f.refs, local)
		}
	}
	return f
}

// linkDartParts gives a document's locals that name a private declaration
// of another file of the same library that declaration's ID, and returns the
// documents it changed. A library is a file and the parts it or they name.
func linkDartParts(files []*dartFile) []*Document {
	owner := make(map[string]string) // part path → library path
	for _, f := range files {
		for _, p := range f.parts {
			owner[p] = f.doc.RelativePath
		}
	}
	library := func(f *dartFile) string {
		if f.partOf != "" {
			return f.partOf
		}
		if o, ok := owner[f.doc.RelativePath]; ok {
			return o
		}
		return f.doc.RelativePath
	}
	decls := make(map[string]map[string]string) // library → private name → ID
	for _, f := range files {
		lib := library(f)
		if decls[lib] == nil {
			decls[lib] = make(map[string]string)
		}
		for name, id := range f.decls {
			if prev, seen := decls[lib][name]; seen && prev != id {
				id = ""
			}
			decls[lib][name] = id
		}
	}
	var changed []*Document
	for _, f := range files {
		if len(f.refs) == 0 {
			continue
		}
		table := decls[library(f)]
		renamed := make(map[string]string)
		for local, name := range f.refs {
			if id := table[name]; id != "" {
				renamed[local] = id
			}
		}
		if len(renamed) == 0 {
			continue
		}
		for _, occ := range f.doc.Occurrences {
			if id, ok := renamed[occ.Symbol]; ok {
				occ.Symbol = id
			}
		}
		changed = append(changed, f.doc)
	}
	return changed
}

// dartLeadStart is the first line of the doc comment and annotations directly
// above the declaration on line def: comment lines, `@` lines and the
// argument lines of a multi-line annotation, up to a blank line or code. The
// tail of the declaration before (`  (ref) => SyncService.roundTimeout);`)
// is code and ends it.
func dartLeadStart(lines []string, def int) int {
	start, depth := def, 0
	for i := min(def, len(lines)) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			break
		}
		if depth == 0 && (strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*")) {
			start = i
			continue
		}
		opens, closes := dartBrackets(t)
		depth += closes - opens
		if depth < 0 {
			break
		}
		if strings.HasPrefix(t, "@") && depth == 0 {
			start = i
			continue
		}
		if depth == 0 {
			break // code, or a bracket closed by code
		}
		// inside brackets a line above has to open: an annotation's arguments
		// or the declaration before; only an `@` line commits them
	}
	return start
}

// dartBrackets counts the brackets a line opens and closes outside string
// literals and a trailing // comment.
func dartBrackets(line string) (opens, closes int) {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '/' && i+1 < len(line) && line[i+1] == '/':
			return opens, closes
		case c == '(' || c == '[' || c == '{':
			opens++
		case c == ')' || c == ']' || c == '}':
			closes++
		}
	}
	return opens, closes
}

// dartFilePrefix is the ID prefix of the document's top-level declarations,
// "scip-dart pub app 1.0.0 lib/`a.dart`/", taken from a symbol it defines.
func dartFilePrefix(doc *Document) string {
	marker := "`" + filepath.Base(doc.RelativePath) + "`/"
	for _, occ := range doc.Occurrences {
		if occ.SymbolRoles&SymbolRoleDefinition == 0 || IsLocalSymbol(occ.Symbol) {
			continue
		}
		if i := strings.Index(occ.Symbol, marker); i > 0 {
			return occ.Symbol[:i+len(marker)]
		}
	}
	return ""
}

// dartContainerAfter is the type whose members follow the definition of the
// global symbol id: id itself for a type, its type for a member, none for a
// top-level declaration.
func dartContainerAfter(id, container string) string {
	sym, err := bindingscip.ParseSymbol(id)
	if err != nil || len(sym.Descriptors) == 0 {
		return container
	}
	last := sym.Descriptors[len(sym.Descriptors)-1]
	if last.Suffix == bindingscip.Descriptor_Type {
		return id
	}
	if last.Suffix == bindingscip.Descriptor_Namespace {
		return container // the file's own symbol
	}
	if last.Suffix == bindingscip.Descriptor_Parameter || last.Suffix == bindingscip.Descriptor_TypeParameter {
		return container
	}
	if i := strings.LastIndex(id, "#"); i >= 0 {
		return id[:i+1] // a member: `_State#dispose().` names its private class
	}
	return ""
}

// identifierAt is the source text a single-line SCIP range covers, or "".
// scip_dart counts columns in UTF-16 code units, as the Dart analyzer does:
// `'½' + _unit` puts _unit at column 8, byte 9.
func identifierAt(lines []string, rng []int32) string {
	if len(rng) != 3 {
		return ""
	}
	line := int(rng[0])
	if line < 0 || line >= len(lines) {
		return ""
	}
	start, end := utf16Index(lines[line], int(rng[1])), utf16Index(lines[line], int(rng[2]))
	if start < 0 || end <= start {
		return ""
	}
	return lines[line][start:end]
}

// utf16Index is the byte offset of UTF-16 column col in s, -1 past its end.
func utf16Index(s string, col int) int {
	if col < 0 {
		return -1
	}
	units := 0
	for i, r := range s {
		if units == col {
			return i
		}
		if units > col {
			return -1 // inside a surrogate pair
		}
		units += utf16.RuneLen(r)
	}
	if units == col {
		return len(s)
	}
	return -1
}

// projectRootDir is the directory a SCIP project_root URI names, "" when it
// isn't a file URI of an existing directory.
func projectRootDir(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return ""
	}
	if info, err := os.Stat(u.Path); err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(u.Path)
}
