package query

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/impact"
)

// A Go interface method has no references from the types implementing it: they
// declare a method of the same name and never mention the interface. So its
// consumers came out as its callers alone, and Backend.Priority, called nowhere
// and implemented four times, read as nothing depending on it. These are the
// implementations, found the way the done check finds them (goImplementations).

// goInterfaceDescriptor matches the last descriptor of a scip-go interface method,
// `Backend#Priority.` (an interface's methods have no `()`), or a type, `Backend#`.
var goInterfaceDescriptor = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)#(?:([A-Za-z_][A-Za-z0-9_]*)\.)?$`)

// goImplementationRefs returns, for a Go interface or interface method declared in
// file, its implementations as references of kind implements, the method's
// definition on each type (the type's for an interface). Those that only match by
// name (a one-method interface whose package the type's doesn't import, see
// goImplementations) come second: they break only where some third package uses
// them as the interface, so they aren't counted as consumers.
func (e *Engine) goImplementationRefs(symbolID, file string) (refs, nameMatch []impact.Reference) {
	if !strings.HasPrefix(symbolID, "scip-go ") || file == "" || e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil, nil
	}
	m := goInterfaceDescriptor.FindStringSubmatch(symbolID)
	if m == nil {
		return nil, nil
	}
	typeName, method := m[1], m[2]

	// Most targets matching the pattern are struct types and fields: the file on
	// disk settles that without asking git. (An interface the edit removed has no
	// implementations to break.)
	content, err := os.ReadFile(filepath.Join(e.repoRoot, file)) // #nosec G304 -- a file of the repo
	if err != nil || !goDeclaresInterface(string(content), typeName) {
		return nil, nil
	}
	// The method sets are the index's, so the declarations are read as they were
	// when it was built: after an edit, assessChange asks about the old interface.
	read := e.indexedFileReader()
	ifaceLines := read(file)
	if ifaceLines == nil {
		return nil, nil
	}
	ifaces, ok := goInterfaces(ifaceLines)
	if !ok {
		return nil, nil
	}
	iface, ok := ifaces[typeName]
	if !ok || len(iface.methods) == 0 {
		return nil, nil // Not an interface, or one every type satisfies
	}
	if _, has := iface.methods[method]; method != "" && !has {
		return nil, nil
	}

	lines := make(map[string][]string)
	sigOf := func(path, typeName, method string) (string, bool) {
		if _, done := lines[path]; !done {
			lines[path] = read(path)
		}
		if lines[path] == nil {
			return "", false
		}
		return goMethodIn(lines[path], typeName, method)
	}
	sees := make(map[string]bool)
	impls, _ := goImplementations(e.scipAdapter, file, typeName, iface, nil, sigOf,
		func(found *scip.GoTypeMethods) bool { return goSeesPackage(e.repoRoot, sees, found, file) })

	for _, impl := range impls {
		site, symbol, name := impl.found.Type, impl.typeID, impl.typeName
		if method != "" {
			site, ok = impl.found.Methods[method]
			if !ok {
				continue
			}
			symbol, name = site.Symbol, impl.typeName+"."+method
		} else if site.Path == "" {
			site = firstMethodSite(impl.found)
		}
		if site.Path == "" {
			continue
		}
		ref := impact.Reference{
			Kind:       impact.RefImplements,
			Location:   &impact.Location{FileId: site.Path, StartLine: site.Line, StartColumn: site.Column},
			IsTest:     isTestFilePath(site.Path),
			FromSymbol: symbol,
			FromName:   name,
			FromModule: filepath.Dir(site.Path),
		}
		if impl.nameOnly {
			nameMatch = append(nameMatch, ref)
		} else {
			refs = append(refs, ref)
		}
	}
	return refs, nameMatch
}

// goDeclaresInterface is a quick look for "type Name interface" in Go source,
// grouped declarations ("type ( Name interface") included.
func goDeclaresInterface(src, name string) bool {
	return regexp.MustCompile(`(?m)^\s*(?:type\s+)?` + regexp.QuoteMeta(name) + `(?:\[[^\]]*\])?\s+interface\b`).MatchString(src)
}

// indexedFileReader reads repo files as of the indexed commit, or from disk where
// the index has none or git can't show the file. nil for a file it can't read.
func (e *Engine) indexedFileReader() func(path string) []string {
	commit := ""
	if idx := e.scipAdapter.GetIndex(); idx != nil {
		commit = idx.IndexedCommit
	}
	return func(path string) []string {
		if commit != "" {
			cmd := exec.Command("git", "show", commit+":"+path) // #nosec G204 -- commit and path come from the index
			cmd.Dir = e.repoRoot
			if out, err := cmd.Output(); err == nil {
				return strings.Split(string(out), "\n")
			}
		}
		content, err := os.ReadFile(filepath.Join(e.repoRoot, path)) // #nosec G304 -- a file of the repo
		if err != nil {
			return nil
		}
		return strings.Split(string(content), "\n")
	}
}

// firstMethodSite is where a type without a definition of its own in the index
// is defined: its first method, by name.
func firstMethodSite(found *scip.GoTypeMethods) scip.DefinitionSite {
	var first string
	for name := range found.Methods {
		if first == "" || name < first {
			first = name
		}
	}
	return found.Methods[first]
}

// implementationItems lists implementation references as impact items.
func implementationItems(refs []impact.Reference) []ImpactItem {
	var items []ImpactItem
	for _, ref := range refs {
		item := ImpactItem{
			StableId:   ref.FromSymbol,
			Name:       ref.FromName,
			Kind:       kindImplementation,
			Distance:   1,
			ModuleId:   ref.FromModule,
			Confidence: 0.5,
		}
		if ref.Location != nil {
			item.Location = &LocationInfo{FileId: ref.Location.FileId, StartLine: ref.Location.StartLine, StartColumn: ref.Location.StartColumn}
		}
		items = append(items, item)
	}
	return items
}

// kindImplementation is the ImpactItem kind of an implementation.
const kindImplementation = string(impact.ImplementsInterface)
