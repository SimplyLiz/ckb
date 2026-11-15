package scip

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
// document that defines them. A reference from a part file carries its own
// local number and stays local. Anything the source doesn't confirm (a file
// changed since indexing, a name that isn't one identifier) keeps its local ID.

var dartPrivateName = regexp.MustCompile(`^_[A-Za-z0-9_$]*$`)

// nameDartPrivates renames doc's private declarations in place, reading the
// source from root. Non-Dart documents are left alone.
func nameDartPrivates(doc *Document, root string) {
	if root == "" || !strings.HasSuffix(doc.RelativePath, ".dart") {
		return
	}
	prefix := dartFilePrefix(doc)
	if prefix == "" {
		return
	}
	kinds := make(map[string]int32, len(doc.Symbols))
	hasLocalDecl := false
	for _, sym := range doc.Symbols {
		kinds[sym.Symbol] = sym.Kind
		if IsLocalSymbol(sym.Symbol) && dartRenamedKind(sym.Kind) {
			hasLocalDecl = true
		}
	}
	if !hasLocalDecl {
		return
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(doc.RelativePath))) // #nosec G304 -- a document of the repo's own index
	if err != nil {
		return
	}
	lines := strings.Split(string(src), "\n")

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
func identifierAt(lines []string, rng []int32) string {
	if len(rng) != 3 {
		return ""
	}
	line, start, end := int(rng[0]), int(rng[1]), int(rng[2])
	if line < 0 || line >= len(lines) || start < 0 || end <= start || end > len(lines[line]) {
		return ""
	}
	return lines[line][start:end]
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
