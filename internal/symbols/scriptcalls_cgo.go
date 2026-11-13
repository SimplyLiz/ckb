//go:build cgo

package symbols

import (
	"context"
	"os"
	"path/filepath"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/SimplyLiz/CodeMCP/internal/complexity"
)

// ExtractScriptCalls returns the edges from one shell script to the repository
// files it sources or runs. Files that aren't shell scripts (by extension or
// shebang) give no edges. Only targets that exist are returned.
func (e *Extractor) ExtractScriptCalls(ctx context.Context, repoRoot, path string) ([]ScriptCall, error) {
	if lang, ok := complexity.LanguageFromPath(path); !ok || lang != complexity.LangBash {
		return nil, nil
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return e.ExtractScriptCallsSource(ctx, repoRoot, path, source)
}

// ExtractScriptCallsSource is ExtractScriptCalls for source already read.
// path is still used to resolve relative targets.
func (e *Extractor) ExtractScriptCallsSource(ctx context.Context, repoRoot, path string, source []byte) ([]ScriptCall, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return nil, err
	}

	root, err := e.parser.Parse(ctx, source, complexity.LangBash)
	if err != nil {
		return nil, err
	}
	return resolveScriptCalls(absRoot, absPath, filepath.ToSlash(rel), shellCommands(root, source)), nil
}

// ExtractScriptCallsDirectory returns the script-call edges of every shell
// script under root, visiting the same files as ExtractDirectoryWithOptions.
// opts.RepoRoot (default root) is what targets are resolved against and made
// relative to.
func (e *Extractor) ExtractScriptCallsDirectory(ctx context.Context, root string, opts DirOptions) ([]ScriptCall, error) {
	files, err := listSourceFiles(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = root
	}
	var all []ScriptCall
	for _, p := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.Filter != nil && !opts.Filter(p) {
			continue
		}
		calls, err := e.ExtractScriptCalls(ctx, repoRoot, p)
		if err != nil {
			continue // unreadable or unparsable: skip, like ExtractDirectory
		}
		all = append(all, calls...)
	}
	return all, nil
}

// shellCommands lists every simple command in the tree, including those inside
// functions, conditions, pipelines and command substitutions.
func shellCommands(root *sitter.Node, source []byte) []scriptCommand {
	var cmds []scriptCommand
	for _, n := range findNodes(root, []string{"command"}) {
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		cmd := scriptCommand{
			name: nameNode.Content(source),
			line: int(n.StartPoint().Row) + 1,
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			if n.FieldNameForChild(i) == "argument" {
				cmd.args = append(cmd.args, n.Child(i).Content(source))
			}
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}
