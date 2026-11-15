package indexplan

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// flutterSDKDep matches the `sdk: flutter` line pubspec.yaml carries under a
// flutter dependency; only the flutter tool can resolve that package.
var flutterSDKDep = regexp.MustCompile(`(?m)^\s+sdk:\s*["']?flutter["']?\s*$`)

// DepsCommand is the command that fetches what plan's indexer needs before it
// can run, or "" when nothing is missing. Today that is Dart only: scip_dart
// reads .dart_tool/package_config.json and exits with "Unable to locate
// packageConfig" without it, which is the state of every fresh clone. An IDE
// runs `pub get` on opening the project; CKB does the same instead of failing
// and asking the user to.
func DepsCommand(plan *Plan) string {
	if plan == nil || plan.Lang != project.LangDart {
		return ""
	}
	if hasPackageConfig(plan.IndexerDir) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(plan.IndexerDir, "pubspec.yaml"))
	if err != nil {
		return ""
	}
	if flutterSDKDep.Match(data) {
		return "flutter pub get"
	}
	return "dart pub get"
}

// hasPackageConfig reports whether dir or a parent has a package config: a
// pub workspace member resolves into its workspace root's .dart_tool.
func hasPackageConfig(dir string) bool {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".dart_tool", "package_config.json")); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// FetchDeps runs DepsCommand(plan) in the indexer's directory. It returns the
// command it ran ("" if none was needed) and, on failure, an error carrying the
// tool's last lines of output.
func FetchDeps(plan *Plan) (string, error) {
	command := DepsCommand(plan)
	if command == "" {
		return "", nil
	}
	parts := strings.Fields(command)
	if _, err := exec.LookPath(parts[0]); err != nil {
		return command, fmt.Errorf("%s needs %s, which is not on PATH: run '%s' in %s yourself", project.LanguageDisplayName(plan.Lang), parts[0], command, plan.IndexerDir)
	}
	cmd := exec.Command(parts[0], parts[1:]...) // #nosec G204 //nolint:gosec // fixed command, see DepsCommand
	cmd.Dir = plan.IndexerDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return command, fmt.Errorf("'%s' failed: %v%s", command, err, lastLines(out.String(), 5))
	}
	return command, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	return "\n  " + strings.Join(lines, "\n  ")
}
