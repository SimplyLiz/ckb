package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// dirFilter says which directories below a repo root belong to another
// project, so a manifest in them must not decide this one's language: a nested
// git repository (a checkout of another project, with a .git of its own) and a
// directory the repo's git ignores (scratch, a local clone, build output). One
// detection run shares one filter, so git is asked once.
type dirFilter struct {
	root    string
	once    sync.Once
	ignored map[string]bool // root-relative, slash-separated, no trailing slash
}

func newDirFilter(root string) *dirFilter {
	return &dirFilter{root: root}
}

// skip reports whether the directory at path (rel to the root) is another
// project's. The root itself never is.
func (f *dirFilter) skip(path, rel string) bool {
	if f == nil || rel == "." || rel == "" {
		return false
	}
	// .git is a directory in a clone and a file in a submodule or worktree.
	if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
		return true
	}
	f.once.Do(f.loadIgnored)
	return f.ignored[filepath.ToSlash(rel)]
}

// loadIgnored lists the ignored directories once. --directory names an
// ignored directory instead of every file below it, so a big node_modules or
// build tree costs one line. Outside a git repo nothing is ignored.
func (f *dirFilter) loadIgnored() {
	f.ignored = map[string]bool{}
	cmd := exec.Command("git", "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory") // #nosec G204 -- fixed git subcommand
	cmd.Dir = f.root
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, entry := range strings.Split(string(out), "\x00") {
		if dir, ok := strings.CutSuffix(entry, "/"); ok && dir != "" {
			f.ignored[dir] = true
		}
	}
}
