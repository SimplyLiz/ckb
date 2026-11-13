package query

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
)

// constDeclRe matches Go const declarations like "ConstName = value" or "ConstName Type = value".
var constDeclRe = regexp.MustCompile(`^\s*([A-Z]\w*)\s+(?:\w+\s+)?=`)

// checkDeadCode finds dead code within the changed files using the SCIP index
// and additionally scans for unused constants via reference counting.
func (e *Engine) checkDeadCode(ctx context.Context, changedFiles []string, opts ReviewPROptions) (ReviewCheck, []ReviewFinding) {
	start := time.Now()

	// Build scope from changed file directories
	dirSet := make(map[string]bool)
	for _, f := range changedFiles {
		dirSet[filepath.Dir(f)] = true
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}

	minConf := opts.Policy.DeadCodeMinConfidence
	if minConf <= 0 {
		minConf = 0.8
	}

	resp, err := e.FindDeadCode(ctx, FindDeadCodeOptions{
		Scope:           dirs,
		MinConfidence:   minConf,
		IncludeExported: true,
		Limit:           50,
	})
	if err != nil {
		return ReviewCheck{
			Name:     "dead-code",
			Status:   "skip",
			Severity: "warning",
			Summary:  fmt.Sprintf("Could not analyze: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}, nil
	}

	// Filter to only items in the changed files
	changedSet := make(map[string]bool)
	for _, f := range changedFiles {
		changedSet[f] = true
	}

	var findings []ReviewFinding
	// Track already-reported locations to dedup with constant findings
	reported := make(map[string]bool) // "file:line"

	for _, item := range resp.DeadCode {
		if !changedSet[item.FilePath] {
			continue
		}
		// Grep-based verification: if the symbol name appears in other files
		// in the same package directory, it's likely referenced and not dead.
		// SCIP doesn't always capture cross-file references within cmd/ packages.
		if item.SymbolName != "" && symbolReferencedInPackage(e.repoRoot, item.FilePath, item.SymbolName) {
			continue
		}
		hint := ""
		if item.SymbolName != "" {
			hint = fmt.Sprintf("→ ckb explain %s", item.SymbolName)
		}
		key := fmt.Sprintf("%s:%d", item.FilePath, item.LineNumber)
		reported[key] = true
		findings = append(findings, ReviewFinding{
			Check:     "dead-code",
			Severity:  "warning",
			File:      item.FilePath,
			StartLine: item.LineNumber,
			Message:   fmt.Sprintf("Dead code: %s (%s) — %s", item.SymbolName, item.Kind, item.Reason),
			Category:  "dead-code",
			RuleID:    fmt.Sprintf("ckb/dead-code/%s", item.Category),
			Hint:      hint,
		})
	}

	// Phase 2: Scan for unused constants using FindReferences
	constFindings := e.findDeadConstants(ctx, changedFiles, reported)
	findings = append(findings, constFindings...)

	// Phase 3: Cartographer cross-project unreferenced exports.
	// Catches public symbols with no callers anywhere in the project —
	// stronger signal than SCIP's per-package analysis.
	if cartographer.Available() {
		unref, err := cartographer.UnreferencedSymbols(e.repoRoot)
		if err == nil {
			for _, f := range unref.Files {
				// A test file's functions are called by its runner, not by code.
				if !changedSet[f.Path] || isTestFilePath(f.Path) {
					continue
				}
				for _, sym := range f.Symbols {
					key := fmt.Sprintf("%s:sym:%s", f.Path, sym)
					if reported[key] {
						continue
					}
					// Cartographer counts uses from other files only, so a function
					// called in its own file came out "no callers project-wide". It has
					// to survive a word search before it's reported.
					if !plausiblyUnreferenced(e.repoRoot, f.Path, sym) {
						continue
					}
					reported[key] = true
					findings = append(findings, ReviewFinding{
						Check:    "dead-code",
						Severity: "warning",
						File:     f.Path,
						Message:  fmt.Sprintf("Unreferenced export: %s — no callers found project-wide", sym),
						Category: "dead-code",
						RuleID:   "ckb/dead-code/unreferenced-export",
					})
				}
			}
		}
	}

	status := "pass"
	summary := "No dead code in changed files"
	if len(findings) > 0 {
		status = "warn"
		summary = fmt.Sprintf("%d dead code item(s) found in changed files", len(findings))
	}

	return ReviewCheck{
		Name:     "dead-code",
		Status:   status,
		Severity: "warning",
		Summary:  summary,
		Duration: time.Since(start).Milliseconds(),
	}, findings
}

// findDeadConstants scans changed Go files for exported constants and checks
// if they have any references outside their declaration file.
// Requires SCIP index for reference counting — skips if unavailable to avoid
// tree-sitter fallback which isn't thread-safe for concurrent use.
func (e *Engine) findDeadConstants(ctx context.Context, changedFiles []string, alreadyReported map[string]bool) []ReviewFinding {
	// Skip constant scanning when SCIP is unavailable — the tree-sitter
	// fallback search path is not thread-safe for concurrent goroutine use.
	if e.scipAdapter == nil {
		return nil
	}
	var findings []ReviewFinding

	for _, file := range changedFiles {
		if ctx.Err() != nil {
			break
		}
		if !strings.HasSuffix(file, ".go") || isTestFilePath(file) {
			continue
		}

		consts := extractExportedConstants(filepath.Join(e.repoRoot, file))
		for _, c := range consts {
			if ctx.Err() != nil {
				break
			}
			// Skip if already reported by SCIP analysis
			key := fmt.Sprintf("%s:%d", file, c.line)
			if alreadyReported[key] {
				continue
			}

			// Resolve constant name to a symbol ID, then count references
			searchResp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
				Query: c.name,
				Scope: file,
				Limit: 5,
			})
			if err != nil || searchResp == nil || len(searchResp.Symbols) == 0 {
				continue
			}

			// Find the matching symbol by line
			symbolId := ""
			for _, sym := range searchResp.Symbols {
				if sym.Location != nil && sym.Location.StartLine == c.line {
					symbolId = sym.StableId
					break
				}
			}
			if symbolId == "" {
				// Fall back to first match with same name
				for _, sym := range searchResp.Symbols {
					if sym.Name == c.name {
						symbolId = sym.StableId
						break
					}
				}
			}
			if symbolId == "" {
				continue
			}

			refsResp, err := e.FindReferences(ctx, FindReferencesOptions{
				SymbolId: symbolId,
				Limit:    5,
			})
			if err != nil || refsResp == nil {
				continue
			}

			// Count references outside the declaration
			externalRefs := 0
			for _, ref := range refsResp.References {
				if ref.Location == nil {
					continue
				}
				// Skip the declaration itself
				if ref.Location.FileId == file && ref.Location.StartLine == c.line {
					continue
				}
				externalRefs++
			}

			if externalRefs == 0 {
				// Grep-based verification: SCIP may miss cross-file references
				// within the same package (e.g., cmd/ckb).
				if symbolReferencedInPackage(e.repoRoot, file, c.name) {
					continue
				}
				findings = append(findings, ReviewFinding{
					Check:     "dead-code",
					Severity:  "warning",
					File:      file,
					StartLine: c.line,
					Message:   fmt.Sprintf("Dead code: %s (constant) — no references found", c.name),
					Category:  "dead-code",
					RuleID:    "ckb/dead-code/unused-constant",
				})
			}
		}
	}

	return findings
}

type constInfo struct {
	name string
	line int
}

// extractExportedConstants parses a Go file for exported const declarations.
func extractExportedConstants(absPath string) []constInfo {
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var consts []constInfo
	scanner := bufio.NewScanner(f)
	inConst := false
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Track const blocks
		if strings.HasPrefix(trimmed, "const (") || trimmed == "const (" {
			inConst = true
			continue
		}
		if inConst && trimmed == ")" {
			inConst = false
			continue
		}

		// Single const: "const Name = ..."
		if strings.HasPrefix(trimmed, "const ") && !inConst {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				name := parts[1]
				if isExported(name) {
					consts = append(consts, constInfo{name: name, line: lineNum})
				}
			}
			continue
		}

		// Inside const block
		if inConst {
			m := constDeclRe.FindStringSubmatch(trimmed)
			if m != nil && isExported(m[1]) {
				consts = append(consts, constInfo{name: m[1], line: lineNum})
			}
		}
	}

	return consts
}

// isExported returns true if name starts with an uppercase letter.
func isExported(name string) bool {
	if len(name) == 0 {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

// symbolReferencedInPackage checks whether symbolName appears in other Go files
// within the same package directory as filePath. This catches cross-file references
// that SCIP may miss (e.g., within cmd/ packages).
func symbolReferencedInPackage(repoRoot, filePath, symbolName string) bool {
	dir := filepath.Dir(filePath)
	absDir := filepath.Join(repoRoot, dir)
	absFile := filepath.Join(repoRoot, filePath)

	// Use grep to search for the symbol name in sibling .go files, excluding
	// the declaring file itself and test files.
	cmd := exec.Command("grep", "-rl", "--include=*.go", symbolName, absDir)
	out, err := cmd.Output()
	if err != nil {
		return false // grep found nothing or errored
	}

	base := filepath.Base(absFile)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matchBase := filepath.Base(line)
		// Skip the declaring file and test files
		if matchBase == base {
			continue
		}
		if strings.HasSuffix(matchBase, "_test.go") {
			continue
		}
		// Found a reference in another non-test file in the same package
		return true
	}
	return false
}

// plausiblyUnreferenced says whether a name a declaring file exports is used nowhere
// a word search can see: once in its own file (the declaration) and in no other
// tracked file of its language. Dunder names (`__init__`, scip-python's module
// symbol among them) are never a finding. A failed search keeps the finding out:
// a warning nobody can check is worse than a missed one.
func plausiblyUnreferenced(repoRoot, file, name string) bool {
	if name == "" || (strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")) {
		return false
	}
	args := []string{"grep", "-c", "-w", "-F", "-e", name, "--"}
	args = append(args, languagePathspecs(file)...)
	cmd := exec.Command("git", args...) // #nosec G204 -- fixed git subcommand; name and pathspecs are passed as arguments
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		return errors.As(err, &exit) && exit.ExitCode() == 1 // 1: no match at all
	}
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		at := strings.LastIndex(line, ":")
		if at < 0 {
			continue
		}
		n, convErr := strconv.Atoi(line[at+1:])
		if convErr != nil {
			return false
		}
		if line[:at] != filepath.ToSlash(file) {
			return false // Named in another file of its language
		}
		total += n
	}
	return total <= 1
}

// languagePathspecs are the git pathspecs of the files that can use a name declared
// in file: its extension, and for JavaScript and TypeScript the whole family.
func languagePathspecs(file string) []string {
	ext := strings.ToLower(filepath.Ext(file))
	switch ext {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return []string{"*.ts", "*.tsx", "*.mts", "*.cts", "*.js", "*.jsx", "*.mjs", "*.cjs"}
	case ".py", ".pyi":
		return []string{"*.py", "*.pyi"}
	case "":
		return []string{filepath.ToSlash(file)}
	}
	return []string{"*" + ext}
}
