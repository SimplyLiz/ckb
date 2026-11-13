package query

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A Go interface method renamed or given another signature, or a method added to the
// interface: every type that implements it stops compiling, and none of them refers to
// the interface method. Its references don't reach them, so the check said clear while
// the build broke on each implementation. This lists them.
//
// Two sources. The index's implementation relationships, which scip-go only writes for
// types in the interface's own package, and the method sets in the index: a concrete
// type defining every method the interface had before the diff, with the same parameter
// and result types, counts as an implementation. Names and type text, nothing
// type-checked, so a type that only happens to match counts too.

// implIndex is what the implementation lookup needs from the index.
type implIndex interface {
	TypeSymbolIn(path, name string) string
	GoMethodsNamed(names []string) map[string]*scip.GoTypeMethods
	Implementations(symbolID string) []string
}

// goInterface is an interface's explicit methods, by name, as normalized signatures.
type goInterface struct {
	methods     map[string]string
	first, last int // 1-based lines of the declaration
}

// checkImplementations compares the interfaces of each changed Go file on both sides
// and lists the implementations the change leaves behind, each with `via` naming the
// interface.
func (c *doneChecker) checkImplementations(adapter referenceFinder) []rustOpen {
	index, _ := adapter.(implIndex)
	var open []rustOpen
	for _, file := range c.orderedFiles() {
		filePath := file.OldPath
		if !strings.HasSuffix(filePath, ".go") || filePath != file.NewPath || file.Deleted || file.IsNew {
			continue
		}
		oldLines := c.readOld(file, filePath)
		newLines, ok := c.readNew(file, filePath)
		if oldLines == nil || !ok {
			c.cut = true
			continue
		}
		before, ok := goInterfaces(oldLines)
		if !ok {
			c.failed++ // The old side doesn't parse, so what its interfaces needed is unknown
			continue
		}
		if len(before) == 0 {
			continue
		}
		after, ok := goInterfaces(newLines)
		if !ok {
			c.failed++ // The new side doesn't parse, so what its interfaces need is unknown
			continue
		}
		names := make([]string, 0, len(before))
		for name := range before {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			now, still := after[name]
			if !still {
				continue // A renamed or removed interface is a changed symbol with references
			}
			c.compared = append(c.compared, comparedSpan{path: filePath, first: now.first, last: now.last})
			change := compareInterface(before[name].methods, now.methods)
			if len(change.changed) == 0 && len(change.added) == 0 {
				continue // Removing a method breaks callers, not implementations
			}
			if index == nil || len(before[name].methods) == 0 {
				c.failed++ // No index, or every type implemented it: nothing to enumerate
				continue
			}
			files := make(map[string]bool)
			c.checkInterface(index, filePath, name, before[name], change, files)
			if len(files) > 0 {
				open = append(open, rustOpen{name: name, change: impact.DeclChanged, files: files})
			}
		}
	}
	return open
}

// interfaceChange is what the diff did to an interface's method set.
type interfaceChange struct {
	changed, added, removed []string
}

func compareInterface(before, after map[string]string) interfaceChange {
	var change interfaceChange
	for name, sig := range before {
		if now, ok := after[name]; !ok {
			change.removed = append(change.removed, name)
		} else if now != sig {
			change.changed = append(change.changed, name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			change.added = append(change.added, name)
		}
	}
	sort.Strings(change.changed)
	sort.Strings(change.added)
	sort.Strings(change.removed)
	return change
}

// checkInterface classifies each implementation of one changed interface into files.
func (c *doneChecker) checkInterface(index implIndex, filePath, name string, iface goInterface, change interfaceChange, files map[string]bool) {
	oldNames := make([]string, 0, len(iface.methods))
	for method := range iface.methods {
		oldNames = append(oldNames, method)
	}
	sort.Strings(oldNames)

	ifaceSym := index.TypeSymbolIn(filePath, name)
	related := make(map[string]bool)
	if ifaceSym != "" {
		for _, id := range index.Implementations(ifaceSym) {
			related[receiverType(id)] = true
		}
		for _, method := range oldNames {
			for _, id := range index.Implementations(ifaceSym + method + ".") {
				related[receiverType(id)] = true
			}
		}
	}

	types := index.GoMethodsNamed(append(append([]string{}, oldNames...), change.added...))
	typeIDs := make([]string, 0, len(types))
	for id := range types {
		typeIDs = append(typeIDs, id)
	}
	sort.Strings(typeIDs)

	via := "implements " + name
	for _, typeID := range typeIDs {
		found := types[typeID]
		typeName := scip.SymbolSimpleName(typeID)
		if typeName == "" {
			continue
		}
		if !related[typeID] && !c.implementsByName(found, typeName, iface.methods, oldNames) {
			continue
		}
		// A one-method interface, String() or Close(), is satisfied by every type with
		// such a method, and the index's implementation relationships say no more
		// than that. One whose package can't even see the interface breaks only where
		// some third package uses it as one.
		nameOnly := len(iface.methods) == 1 && !c.goSeesPackage(found, filePath)
		methodSym := func(method string) impact.ChangedSymbol {
			id := ifaceSym + method + "."
			if ifaceSym == "" {
				id = fmt.Sprintf("iface:%s:%s.%s", filePath, name, method)
			}
			return impact.ChangedSymbol{SymbolID: id, Name: name + "." + method, File: filePath}
		}
		report := func(method string, site scip.DefinitionSite, use string, declChange impact.DeclarationChange) {
			ref := backends.Reference{
				Location:       backends.Location{Path: site.Path, Line: site.Line, Column: site.Column},
				FromSymbolName: typeName + "." + use,
			}
			c.checkReference(&ref, methodSym(method), useQuery{name: use, change: declChange, via: via, nameOnly: nameOnly}, files)
		}

		for _, method := range change.changed {
			if site, ok := found.Methods[method]; ok {
				report(method, site, method, impact.DeclChanged)
			}
		}
		if len(change.added) == 0 {
			continue
		}
		if len(change.removed) > 0 {
			// A rename, most likely: the old method is what has to change.
			for _, method := range change.removed {
				if site, ok := found.Methods[method]; ok {
					report(method, site, method, impact.DeclRemoved)
				}
			}
			continue
		}
		if c.definesAll(found, typeName, change.added) {
			c.updated++
			continue
		}
		if found.Type.Path != "" {
			report(change.added[0], found.Type, typeName, impact.DeclChanged)
		}
	}
}

// useNameMatch is an implementation of a one-method interface whose package doesn't
// import the interface's: it satisfies it, but may never be used as it. It is counted with the uses that rarely
// break, not listed.
const useNameMatch = "name-match"

// goSeesPackage says whether the type's package is the interface's or imports it.
// Unknown (no go.mod found, unreadable directory) counts as seeing it.
func (c *doneChecker) goSeesPackage(found *scip.GoTypeMethods, ifaceFile string) bool {
	typeFile := found.Type.Path
	if typeFile == "" {
		for _, site := range found.Methods {
			typeFile = site.Path
			break
		}
	}
	typeDir, ifaceDir := path.Dir(typeFile), path.Dir(ifaceFile)
	if typeFile == "" || typeDir == ifaceDir {
		return true
	}
	key := typeDir + "\x00" + ifaceDir
	if seen, ok := c.goSees[key]; ok {
		return seen
	}
	sees := true
	if importPath := goImportPath(c.repoRoot, ifaceDir); importPath != "" {
		sees = goDirImports(filepath.Join(c.repoRoot, typeDir), importPath)
	}
	if c.goSees == nil {
		c.goSees = make(map[string]bool)
	}
	c.goSees[key] = sees
	return sees
}

// goImportPath is the import path of a repo directory, from the nearest go.mod at or
// above it, or "".
func goImportPath(repoRoot, dir string) string {
	for d := dir; ; d = path.Dir(d) {
		if content, err := os.ReadFile(filepath.Join(repoRoot, d, "go.mod")); err == nil {
			for _, line := range strings.Split(string(content), "\n") {
				if module, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
					module = strings.Trim(strings.TrimSpace(module), `"`)
					rel, err := filepath.Rel(d, dir)
					if err != nil {
						return ""
					}
					if rel == "." {
						return module
					}
					return module + "/" + filepath.ToSlash(rel)
				}
			}
			return ""
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

// goDirImports says whether any Go file of the directory imports importPath.
// An unreadable directory counts as importing it.
func goDirImports(dir, importPath string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	quoted := []byte(`"` + importPath + `"`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- a file of the repo
		if err != nil || bytes.Contains(content, quoted) {
			return true
		}
	}
	return false
}

// receiverType is the type a method symbol belongs to, or the symbol itself for a type.
func receiverType(id string) string {
	if strings.HasSuffix(id, "#") {
		return id
	}
	if hash := strings.LastIndex(id, "#"); hash >= 0 {
		return id[:hash+1]
	}
	return id
}

// implementsByName says whether a type defines every method of the interface as it was,
// with the same signature where its declaration can be read.
func (c *doneChecker) implementsByName(found *scip.GoTypeMethods, typeName string, methods map[string]string, names []string) bool {
	for _, method := range names {
		site, ok := found.Methods[method]
		if !ok {
			return false
		}
		if sig, ok := c.goMethodSig(site.Path, typeName, method); ok && sig != methods[method] {
			return false
		}
	}
	return true
}

// definesAll says whether the type has each method now: in the index, or on the new side
// of a changed file in its package.
func (c *doneChecker) definesAll(found *scip.GoTypeMethods, typeName string, methods []string) bool {
	dir := path.Dir(found.Type.Path)
	if found.Type.Path == "" {
		for _, site := range found.Methods {
			dir = path.Dir(site.Path)
			break
		}
	}
	for _, method := range methods {
		if _, ok := found.Methods[method]; ok {
			continue
		}
		defined := false
		for _, file := range c.orderedFiles() {
			if file.Deleted || file.NewPath == "" || path.Dir(file.NewPath) != dir || !strings.HasSuffix(file.NewPath, ".go") {
				continue
			}
			lines, ok := c.readNew(file, file.NewPath)
			if !ok {
				continue
			}
			if _, ok := goMethodIn(lines, typeName, method); ok {
				defined = true
				break
			}
		}
		if !defined {
			return false
		}
	}
	return true
}

// goMethodSig is a method's normalized signature as the file defining it has it now.
func (c *doneChecker) goMethodSig(filePath, typeName, method string) (string, bool) {
	file := c.files[filePath]
	if file != nil && !file.Deleted {
		if lines := c.readOld(file, filePath); lines != nil {
			if sig, ok := goMethodIn(lines, typeName, method); ok {
				return sig, true
			}
		}
	}
	lines, ok := c.readNew(file, filePath)
	if !ok {
		return "", false
	}
	return goMethodIn(lines, typeName, method)
}

// goInterfaces returns the interfaces a Go file declares. False if it doesn't parse.
func goInterfaces(lines []string) (map[string]goInterface, bool) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "", strings.Join(lines, "\n"), parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}
	interfaces := make(map[string]goInterface)
	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok {
			return true
		}
		iface, ok := spec.Type.(*ast.InterfaceType)
		if !ok || iface.Methods == nil {
			return true
		}
		methods := make(map[string]string)
		for _, field := range iface.Methods.List {
			fn, ok := field.Type.(*ast.FuncType)
			if !ok {
				continue // Embedded interfaces and type constraints
			}
			for _, name := range field.Names {
				methods[name.Name] = goSignature(fn)
			}
		}
		interfaces[spec.Name.Name] = goInterface{
			methods: methods,
			first:   fset.Position(spec.Pos()).Line,
			last:    fset.Position(spec.End()).Line,
		}
		return true
	})
	return interfaces, true
}

// goMethodIn finds a method declared on a type in a Go file and returns its signature.
func goMethodIn(lines []string, typeName, method string) (string, bool) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "", strings.Join(lines, "\n"), parser.SkipObjectResolution)
	if err != nil {
		return "", false
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Name.Name != method {
			continue
		}
		if receiverName(fn.Recv.List[0].Type) == typeName {
			return goSignature(fn.Type), true
		}
	}
	return "", false
}

func receiverName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.ParenExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

// packageQualifier is the `pkg.` of a qualified type name, which differs between the
// interface's package and an implementation's.
var packageQualifier = regexp.MustCompile(`\b[\p{L}_][\p{L}\p{N}_]*\.`)

// goSignature is a function type's parameter and result types, without names or
// package qualifiers.
func goSignature(fn *ast.FuncType) string {
	return "(" + goFieldTypes(fn.Params) + ")(" + goFieldTypes(fn.Results) + ")"
}

func goFieldTypes(fields *ast.FieldList) string {
	if fields == nil {
		return ""
	}
	var parts []string
	for _, field := range fields.List {
		text := packageQualifier.ReplaceAllString(types.ExprString(field.Type), "")
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ",")
}
