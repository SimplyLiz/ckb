//go:build ignore

// sync-version.go makes internal/version.Version the one place CKB's version is
// written down, and propagates it everywhere else that has to repeat it.
//
// Before this existed the version lived in four files. They drifted, silently:
// npm/package.json's optionalDependencies sat at 9.0.0 for three releases
// because the release pipeline rewrites them at publish time, so nothing ever
// surfaced the mismatch — it just generated Dependabot PRs for a field whose
// checked-in value is never used.
//
// Sites that do NOT appear here on purpose:
//   - testdata/review/sarif.json — the golden test normalizes the driver
//     version, so the fixture no longer pins one at all.
//   - the SARIF schema version (2.1.0) — a real constant, not our version.
//
// Usage:
//
//	go run scripts/sync-version.go 9.3.1   # set everywhere, source included
//	go run scripts/sync-version.go         # propagate the current source value
//	go run scripts/sync-version.go --check # verify agreement; non-zero if not
package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// versionSource holds the value every other site is derived from.
const versionSource = "internal/version/version.go"

var sourcePattern = regexp.MustCompile(`(Version = ")([^"]+)(")`)

// site is one file that has to repeat the version, and the pattern that finds
// every occurrence in it. Group 1 and 3 are kept, group 2 is the version.
type site struct {
	path    string
	pattern *regexp.Regexp
	what    string
}

var sites = []site{
	{
		path:    versionSource,
		pattern: sourcePattern,
		what:    "Go build-time default",
	},
	{
		path:    "npm/package.json",
		pattern: regexp.MustCompile(`("version": ")([^"]+)(",)`),
		what:    "npm package manifest",
	},
	{
		path:    "npm/package.json",
		pattern: regexp.MustCompile(`("@tastehub/ckb-[a-z0-9-]+": ")([^"]+)(")`),
		what:    "npm platform package pins",
	},
	{
		path:    "README.md",
		pattern: regexp.MustCompile(`(CKB MCP Server v)([0-9][^\s]*)()`),
		what:    "MCP banner sample output",
	},
}

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

func main() {
	args := os.Args[1:]
	check := false
	target := ""
	for _, a := range args {
		switch {
		case a == "--check":
			check = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "unknown flag %q\n", a)
			os.Exit(2)
		default:
			target = a
		}
	}
	if check && target != "" {
		fmt.Fprintln(os.Stderr, "--check takes no version argument")
		os.Exit(2)
	}

	current, err := readSource()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if target == "" {
		target = current
	} else if !semver.MatchString(target) {
		fmt.Fprintf(os.Stderr, "%q is not a semantic version (want e.g. 9.3.1)\n", target)
		os.Exit(2)
	}

	if check {
		os.Exit(runCheck(current))
	}
	if err := apply(target); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readSource() (string, error) {
	b, err := os.ReadFile(versionSource)
	if err != nil {
		return "", fmt.Errorf("read %s: %v", versionSource, err)
	}
	m := sourcePattern.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s: no `Version = \"...\"` found — has the package changed?", versionSource)
	}
	return string(m[2]), nil
}

func runCheck(want string) int {
	bad := false
	for _, s := range sites {
		found, err := occurrences(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		for _, got := range found {
			if got != want {
				fmt.Fprintf(os.Stderr, "%s (%s): has %s, want %s\n", s.path, s.what, got, want)
				bad = true
			}
		}
	}
	if bad {
		fmt.Fprintf(os.Stderr, "\nversion sites disagree with %s.\nRun: go run scripts/sync-version.go\n", versionSource)
		return 1
	}
	fmt.Printf("all version sites agree on %s\n", want)
	return 0
}

func occurrences(s site) ([]string, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %v", s.path, err)
	}
	ms := s.pattern.FindAllSubmatch(b, -1)
	if ms == nil {
		// A site that stops matching is a silent hole in the guarantee, not a
		// pass — the version could be anything and nothing would notice.
		return nil, fmt.Errorf("%s (%s): pattern no longer matches — fix scripts/sync-version.go", s.path, s.what)
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m[2]))
	}
	return out, nil
}

func apply(target string) error {
	for _, s := range sites {
		b, err := os.ReadFile(s.path)
		if err != nil {
			return fmt.Errorf("read %s: %v", s.path, err)
		}
		if !s.pattern.Match(b) {
			return fmt.Errorf("%s (%s): pattern no longer matches — fix scripts/sync-version.go", s.path, s.what)
		}
		updated := s.pattern.ReplaceAll(b, []byte("${1}"+target+"${3}"))
		if string(updated) == string(b) {
			fmt.Printf("  %-22s %s (unchanged)\n", s.path, s.what)
			continue
		}
		if err := os.WriteFile(s.path, updated, 0644); err != nil {
			return fmt.Errorf("write %s: %v", s.path, err)
		}
		fmt.Printf("  %-22s %s\n", s.path, s.what)
	}
	fmt.Printf("version is now %s\n", target)
	return nil
}
