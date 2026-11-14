package mcp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// pathParams is the ordered list of tool parameter names that may contain file paths.
// We check these in priority order and return the first path-like value found.
var pathParams = []string{"filePath", "path", "targetPath", "target", "moduleId"}

// extractPathHint extracts a file path hint from tool parameters.
// Returns the first path-like value found, or "" if none.
func extractPathHint(toolParams map[string]interface{}) string {
	for _, key := range pathParams {
		val, ok := toolParams[key].(string)
		if !ok || val == "" {
			continue
		}

		// "target" is overloaded: sometimes a symbol name like "MCPServer.GetEngine".
		// Only treat it as a path if it contains a path separator.
		if key == "target" && !strings.Contains(val, "/") && !strings.Contains(val, "\\") {
			continue
		}

		return val
	}
	return ""
}

// resolveRepoForPath resolves a path hint to a git repo root.
// Returns the repo root path or "" if the hint cannot be resolved.
func (s *MCPServer) resolveRepoForPath(pathHint string) string {
	if pathHint == "" {
		return ""
	}

	// Absolute path: resolve directly
	if filepath.IsAbs(pathHint) {
		return repos.FindGitRoot(pathHint)
	}

	activeRoot := ""
	if eng := s.engine(); eng != nil {
		activeRoot = eng.GetRepoRoot()
	}
	return resolveRelativeHint(pathHint, activeRoot, s.GetRootPaths())
}

// resolveRelativeHint resolves a relative path hint to a git root. The active
// repo goes first: a relative path that exists there means the caller is
// working in it. Trying the client roots first undid every switchProject into
// a linked worktree — the same relative file exists in the main checkout the
// client is rooted in, so the next call with a filePath silently switched back
// to the main checkout's index.
func resolveRelativeHint(pathHint, activeRoot string, clientRoots []string) string {
	candidates := make([]string, 0, len(clientRoots)+1)
	if activeRoot != "" {
		candidates = append(candidates, activeRoot)
	}
	candidates = append(candidates, clientRoots...)

	for _, root := range candidates {
		candidate := filepath.Join(root, pathHint)
		if _, err := os.Stat(candidate); err == nil {
			if gitRoot := repos.FindGitRoot(candidate); gitRoot != "" {
				return gitRoot
			}
		}
	}
	return ""
}
