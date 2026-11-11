package query

import (
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// splitQualifiedQuery splits "Backend.Priority" or "Backend#Priority()" into
// container and member. ok is false for anything without a qualifier and for
// SCIP IDs and free text (they contain spaces).
func splitQualifiedQuery(q string) (container, member string, ok bool) {
	q = strings.TrimSpace(q)
	if q == "" || strings.ContainsAny(q, " \t") {
		return "", "", false
	}
	q = strings.TrimSuffix(q, "()")
	i := strings.LastIndexAny(q, ".#")
	if i <= 0 || i == len(q)-1 {
		return "", "", false
	}
	container, member = q[:i], q[i+1:]
	// "pkg.Type.Method" qualifies by the innermost container.
	if j := strings.LastIndexAny(container, ".#/"); j >= 0 {
		container = container[j+1:]
	}
	return container, member, container != ""
}

// symbolContainer is the enclosing type of a search result, read from its
// SCIP ID; "" when the ID isn't one or the symbol has no enclosing type.
func symbolContainer(item SearchResultItem) string {
	id, err := scip.ParseSCIPIdentifier(item.StableId)
	if err != nil {
		return ""
	}
	return id.GetContainerName()
}

// qualifiedMatches returns the indexes of results whose name is member and
// whose enclosing type, or failing that package directory, is container.
func qualifiedMatches(items []SearchResultItem, query string) []int {
	container, member, ok := splitQualifiedQuery(query)
	if !ok {
		return nil
	}
	var byType, byModule []int
	for i, it := range items {
		if !strings.EqualFold(it.Name, member) {
			continue
		}
		if c := symbolContainer(it); c != "" {
			if strings.EqualFold(c, container) {
				byType = append(byType, i)
			}
			continue
		}
		if it.ModuleId != "" && strings.EqualFold(filepath.Base(it.ModuleId), container) {
			byModule = append(byModule, i)
		}
	}
	if len(byType) > 0 {
		return byType
	}
	return byModule
}

// symbolModuleID is the module of a symbol: what the backend says, else the
// directory of its file, the same rule search results use.
func symbolModuleID(moduleID, path string) string {
	if moduleID != "" || path == "" {
		return moduleID
	}
	return filepath.Dir(path)
}
