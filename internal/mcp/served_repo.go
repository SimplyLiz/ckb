package mcp

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// servedRepoHints names the repo this server answers for: the active engine's
// root, under its registry name when it has one. getStatus used to resolve the
// repo again on every call from the process directory and the global registry,
// so its note named the registry's default, which any 'ckb init' or 'ckb repo
// use' in another terminal or session rewrites: within one session it jumped
// between repos the server never served.
func (s *MCPServer) servedRepoHints() (hints, suggestions []string) {
	eng := s.engine()
	if eng == nil || eng.GetRepoRoot() == "" {
		return nil, nil
	}
	root := normalizePath(eng.GetRepoRoot())
	name := filepath.Base(root)
	if reg, err := repos.LoadRegistry(); err == nil {
		if entry, _ := reg.GetByPath(root); entry != nil {
			name = entry.Name
		}
	}

	if _, err := os.Stat(filepath.Join(root, ".ckb", "config.json")); err != nil {
		return []string{fmt.Sprintf("Serving repo '%s' (%s) with the default config: it has no .ckb/config.json.", name, root)},
			[]string{fmt.Sprintf("Run 'cd %s && ckb init' to write a config for this repository", root)}
	}
	return []string{fmt.Sprintf("Serving repo '%s' (%s).", name, root)}, nil
}
