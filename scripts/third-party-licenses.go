//go:build ignore

// third-party-licenses.go writes THIRD-PARTY-LICENSES: the license text of every
// module that is linked into the ckb binary, as LICENSE §4 promises.
//
// The module list is the union of `go list -deps ./cmd/ckb` over the release
// targets, with cgo set the way release.yml builds each one (cgo pulls in
// go-tree-sitter and its C grammars; some modules are linked on one OS only,
// e.g. mousetrap on Windows). Test-only and tool-only modules in go.mod are not
// linked and do not appear. The Go
// standard library and runtime are linked too, so the toolchain's own LICENSE
// is the first entry.
//
// Usage:
//
//	go run scripts/third-party-licenses.go          # rewrite the file
//	go run scripts/third-party-licenses.go --check  # CI: non-zero if it is stale
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const out = "THIRD-PARTY-LICENSES"

type module struct {
	Path, Version, Dir string
}

func main() {
	check := len(os.Args) > 1 && os.Args[1] == "--check"

	mods, err := linkedModules()
	if err != nil {
		fail(err)
	}
	text, err := render(mods)
	if err != nil {
		fail(err)
	}

	if check {
		have, _ := os.ReadFile(out)
		if !bytes.Equal(have, text) {
			fail(fmt.Errorf("%s is stale: run `go run scripts/third-party-licenses.go`", out))
		}
		return
	}
	if err := os.WriteFile(out, text, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("%s: %d modules\n", out, len(mods))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "third-party-licenses:", err)
	os.Exit(1)
}

// targets mirrors the build matrix in .github/workflows/release.yml.
var targets = []struct{ goos, goarch, cgo string }{
	{"linux", "amd64", "1"},
	{"linux", "arm64", "1"},
	{"darwin", "amd64", "1"},
	{"darwin", "arm64", "1"},
	{"windows", "amd64", "0"},
}

// linkedModules lists the non-main modules that provide packages to ./cmd/ckb
// on any release target.
func linkedModules() ([]module, error) {
	seen := map[string]module{}
	for _, t := range targets {
		cmd := exec.Command("go", "list", "-deps",
			"-f", "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}",
			"./cmd/ckb")
		cmd.Env = append(os.Environ(), "GOOS="+t.goos, "GOARCH="+t.goarch, "CGO_ENABLED="+t.cgo)
		cmd.Stderr = os.Stderr
		raw, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list (%s/%s): %w", t.goos, t.goarch, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(raw))
		for sc.Scan() {
			f := strings.Split(sc.Text(), "\t")
			if len(f) != 3 {
				continue
			}
			seen[f[0]] = module{Path: f[0], Version: f[1], Dir: f[2]}
		}
	}
	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

// licenseFiles returns the license and notice files at the root of a module.
func licenseFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := strings.ToUpper(e.Name())
		if strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") ||
			strings.HasPrefix(n, "COPYING") || strings.HasPrefix(n, "NOTICE") ||
			strings.HasPrefix(n, "PATENTS") {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	return files
}

// identify names the licenses in a file from its text (a file may carry
// several, as yaml.v3 does with libyaml's MIT and its own Apache-2.0). It only
// labels the summary; the full text below is what counts.
func identify(text string) string {
	t := strings.ToLower(text)
	var found []string
	if strings.Contains(t, "apache license") && strings.Contains(t, "version 2.0") {
		found = append(found, "Apache-2.0")
	}
	if strings.Contains(t, "mozilla public license") {
		found = append(found, "MPL-2.0")
	}
	if strings.Contains(t, "permission to use, copy, modify, and/or distribute") {
		found = append(found, "ISC")
	}
	if strings.Contains(t, "permission is hereby granted, free of charge") {
		found = append(found, "MIT")
	}
	if strings.Contains(t, "redistribution and use in source and binary forms") {
		if strings.Contains(t, "neither the name") || strings.Contains(t, "names of its contributors") {
			found = append(found, "BSD-3-Clause")
		} else {
			found = append(found, "BSD-2-Clause")
		}
	}
	if len(found) == 0 {
		return "see text"
	}
	return strings.Join(found, " + ")
}

const rule = "=============================================================================="

func render(mods []module) ([]byte, error) {
	type entry struct {
		name, license string
		texts         []string
	}
	var entries []entry

	gr, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return nil, fmt.Errorf("go env GOROOT: %w", err)
	}
	goroot := strings.TrimSpace(string(gr))
	if b, err := os.ReadFile(filepath.Join(goroot, "LICENSE")); err == nil {
		entries = append(entries, entry{"Go standard library and runtime", identify(string(b)), []string{string(b)}})
	} else {
		return nil, fmt.Errorf("Go toolchain LICENSE: %w", err)
	}

	for _, m := range mods {
		files := licenseFiles(m.Dir)
		if len(files) == 0 {
			return nil, fmt.Errorf("%s %s: no license file in %s", m.Path, m.Version, m.Dir)
		}
		e := entry{name: m.Path + " " + m.Version}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			e.texts = append(e.texts, string(b))
			if e.license == "" || e.license == "see text" {
				e.license = identify(string(b))
			}
		}
		entries = append(entries, e)
	}

	byLicense := map[string][]string{}
	for _, e := range entries {
		byLicense[e.license] = append(byLicense[e.license], e.name)
	}
	licenses := make([]string, 0, len(byLicense))
	for l := range byLicense {
		licenses = append(licenses, l)
	}
	sort.Strings(licenses)

	var b strings.Builder
	b.WriteString("THIRD-PARTY LICENSES\n\n")
	b.WriteString("The ckb binary statically links the following Go modules, including the C\n")
	b.WriteString("sources of tree-sitter and its grammars that github.com/smacker/go-tree-sitter\n")
	b.WriteString("vendors. Each is licensed under the terms reproduced below.\n\n")
	b.WriteString("Generated by `go run scripts/third-party-licenses.go`; CI fails when it is stale.\n\n")
	fmt.Fprintf(&b, "Total third-party components: %d\n\n", len(entries))
	b.WriteString(rule + "\nSUMMARY BY LICENSE\n" + rule + "\n\n")
	for _, l := range licenses {
		fmt.Fprintf(&b, "[%s]  (%d)\n", l, len(byLicense[l]))
		for _, n := range byLicense[l] {
			fmt.Fprintf(&b, "    %s\n", n)
		}
		b.WriteString("\n")
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\n%s  (%s)\n%s\n\n", rule, e.name, e.license, rule)
		for i, t := range e.texts {
			if i > 0 {
				b.WriteString("\n------------------------------------------------------------------------------\n\n")
			}
			b.WriteString(strings.TrimRight(strings.ReplaceAll(t, "\r\n", "\n"), "\n") + "\n")
		}
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}
