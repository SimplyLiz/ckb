package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/mcp"
	"github.com/SimplyLiz/CodeMCP/internal/project"
)

// installTools names what provides the first word of an indexer's install
// command, for when that tool isn't installed either.
var installTools = map[string]string{
	"npm":      "Node.js",
	"pip":      "Python",
	"pip3":     "Python",
	"go":       "Go",
	"cargo":    "Rust (rustup)",
	"rustup":   "rustup",
	"gem":      "Ruby",
	"dotnet":   "the .NET SDK",
	"composer": "Composer",
	"cs":       "Coursier",
	"dart":     "the Dart SDK",
}

// installWithRe finds the "(install with: <command>)" an indexer-missing
// message carries.
var installWithRe = regexp.MustCompile(`\(install with: ([^)\n]+)\)`)

// withInstallPrereqs adds to each "(install with: npm install ...)" in text
// that the command's tool is missing too when it isn't on PATH, so the step
// named is one that works on this machine: "npm is not installed either;
// install Node.js first".
func withInstallPrereqs(text string, lookPath func(string) (string, error)) string {
	return installWithRe.ReplaceAllStringFunc(text, func(m string) string {
		cmd := installWithRe.FindStringSubmatch(m)[1]
		fields := strings.Fields(cmd)
		if len(fields) == 0 {
			return m
		}
		tool := fields[0]
		provider, known := installTools[tool]
		if !known {
			return m
		}
		if _, err := lookPath(tool); err == nil {
			return m
		}
		return "(install with: " + cmd + "; " + tool + " is not installed either, install " + provider + " first)"
	})
}

// diagnoseIndexOnPath is diagnoseIndex with the install commands its problem
// and note name checked against PATH, and with where each language came from, so a
// language picked up from a stray manifest is visible as such.
func diagnoseIndexOnPath(repoRoot string) mcp.IndexDiagnosis {
	d := diagnoseIndex(repoRoot)
	d.Note = withInstallPrereqs(d.Note, exec.LookPath)
	if d.Problem == "" {
		return d
	}
	d.Problem = withInstallPrereqs(d.Problem, exec.LookPath)
	if src := languageSources(repoRoot); src != "" {
		d.Problem += " " + src
	}
	return d
}

// languageSources says where the languages an index build covers come from,
// as indexedTargets resolves them ("Python from lisapc-mcp/pyproject.toml").
// Empty when there is no language.
func languageSources(repoRoot string) string {
	targets, ok := indexedTargets(repoRoot)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(targets))
	for _, t := range targets {
		from := t.Manifest
		if from == "" {
			from = "source files, no manifest"
		}
		parts = append(parts, fmt.Sprintf("%s from %s", project.LanguageDisplayName(t.Lang), from))
	}
	return "(Detected " + strings.Join(parts, "; ") + ".)"
}
