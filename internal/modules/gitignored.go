package modules

import (
	"os/exec"
	"strings"
)

// GitIgnoredDirs are the repo-relative directories git ignores as a whole (scratch,
// build output, virtualenvs), without a trailing slash. A directory walk skips them:
// they aren't the repo's code. Outside a git repo, or if git fails, there are none.
func GitIgnoredDirs(repoRoot string) map[string]bool {
	cmd := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory") // #nosec G204 -- fixed git subcommand
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	dirs := make(map[string]bool)
	for _, path := range strings.Split(string(out), "\x00") {
		if dir, ok := strings.CutSuffix(path, "/"); ok && dir != "" {
			dirs[dir] = true
		}
	}
	return dirs
}
