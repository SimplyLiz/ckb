package modules

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/SimplyLiz/CodeMCP/internal/paths"
)

// ModuleContext provides context for import classification
type ModuleContext struct {
	// RepoRoot is the repository root path
	RepoRoot string

	// Modules is the list of detected modules
	Modules []*Module

	// Language is the language of the file being analyzed
	Language string

	// WorkspacePackages is a set of known workspace package names
	WorkspacePackages map[string]bool

	// DeclaredDependencies is a set of declared external dependencies
	DeclaredDependencies map[string]bool
}

// ImportClassifier classifies import edges
type ImportClassifier struct {
	context *ModuleContext

	// pyModules maps a Python module name to the tracked files of that name, built
	// on the first import that needs it (one reaching through sys.path).
	pyModulesOnce sync.Once
	pyModules     map[string][]string

	// goModule is the module path in the repo's go.mod, read once.
	goModuleOnce sync.Once
	goModule     string
}

// NewImportClassifier creates a new import classifier
func NewImportClassifier(ctx *ModuleContext) *ImportClassifier {
	return &ImportClassifier{
		context: ctx,
	}
}

// ClassifyImport classifies an import string based on its pattern and context
func (c *ImportClassifier) ClassifyImport(importStr string, fromFile string) ImportEdgeKind {
	// 1. Check for relative paths (./foo, ../bar)
	if strings.HasPrefix(importStr, "./") || strings.HasPrefix(importStr, "../") {
		return LocalFile
	}

	// 2. Check for stdlib imports
	if c.isStdlib(importStr) {
		return Stdlib
	}

	// 3. Check if it's a workspace package
	if c.context.WorkspacePackages != nil && c.context.WorkspacePackages[importStr] {
		return WorkspacePackage
	}

	// 4. Try to resolve as local module
	if c.isLocalModule(importStr, fromFile) {
		return LocalModule
	}

	// 5. Check if it's an external dependency
	if c.context.DeclaredDependencies != nil && c.isDeclaredDependency(importStr) {
		return ExternalDependency
	}

	// 6. Default to unknown
	return Unknown
}

// ClassifyEdge classifies an import edge, setting its Kind and adjusting Confidence
func (c *ImportClassifier) ClassifyEdge(edge *ImportEdge) {
	edge.Kind = c.ClassifyImport(edge.RawImport, edge.From)

	// Adjust confidence based on classification
	switch edge.Kind {
	case LocalFile, Stdlib:
		edge.Confidence = 1.0 // High confidence
	case LocalModule, WorkspacePackage:
		edge.Confidence = 0.9 // Very high confidence
	case ExternalDependency:
		edge.Confidence = 0.95 // High confidence if declared
	case Unknown:
		edge.Confidence = 0.5 // Low confidence
	}
}

// isStdlib checks if an import is a standard library import
func (c *ImportClassifier) isStdlib(importStr string) bool {
	switch c.context.Language {
	case LanguageDart:
		return strings.HasPrefix(importStr, "dart:")
	case LanguageTypeScript, LanguageJavaScript:
		return c.isNodeBuiltin(importStr)
	case LanguageGo:
		return c.isGoStdlib(importStr)
	case LanguagePython:
		return c.isPythonStdlib(importStr)
	case LanguageRust:
		return c.isRustStdlib(importStr)
	default:
		return false
	}
}

// isNodeBuiltin checks if an import is a Node.js builtin
func (c *ImportClassifier) isNodeBuiltin(importStr string) bool {
	// Node.js builtin modules
	builtins := map[string]bool{
		"assert": true, "buffer": true, "child_process": true, "cluster": true,
		"crypto": true, "dgram": true, "dns": true, "events": true,
		"fs": true, "http": true, "http2": true, "https": true,
		"net": true, "os": true, "path": true, "perf_hooks": true,
		"process": true, "querystring": true, "readline": true, "stream": true,
		"string_decoder": true, "timers": true, "tls": true, "tty": true,
		"url": true, "util": true, "v8": true, "vm": true, "zlib": true,
	}

	// Check for node: prefix
	if strings.HasPrefix(importStr, "node:") {
		return true
	}

	// Check bare name
	return builtins[importStr]
}

// isGoStdlib checks if an import is from Go standard library
func (c *ImportClassifier) isGoStdlib(importStr string) bool {
	// Go stdlib packages don't have dots in their first path component
	parts := strings.Split(importStr, "/")
	if len(parts) > 0 {
		firstPart := parts[0]
		// If it contains a dot, it's likely a domain name (external)
		if strings.Contains(firstPart, ".") {
			return false
		}
		// Common stdlib packages
		stdlibPrefixes := []string{
			"archive", "bufio", "builtin", "bytes", "compress", "container",
			"context", "crypto", "database", "debug", "encoding", "errors",
			"expvar", "flag", "fmt", "go", "hash", "html", "image", "index",
			"io", "log", "math", "mime", "net", "os", "path", "plugin",
			"reflect", "regexp", "runtime", "sort", "strconv", "strings",
			"sync", "syscall", "testing", "text", "time", "unicode", "unsafe",
		}
		for _, prefix := range stdlibPrefixes {
			if firstPart == prefix {
				return true
			}
		}
	}
	return false
}

// isPythonStdlib checks if an import is from Python standard library
func (c *ImportClassifier) isPythonStdlib(importStr string) bool {
	// Common Python stdlib modules (not exhaustive)
	stdlibModules := map[string]bool{
		"os": true, "sys": true, "re": true, "json": true, "math": true,
		"time": true, "datetime": true, "collections": true, "itertools": true,
		"functools": true, "pathlib": true, "typing": true, "asyncio": true,
		"subprocess": true, "threading": true, "multiprocessing": true,
		"socket": true, "http": true, "urllib": true, "email": true,
		"logging": true, "unittest": true, "pickle": true, "csv": true,
		"xml": true, "html": true, "sqlite3": true, "hashlib": true,
	}

	// Get the base module name (first part)
	parts := strings.Split(importStr, ".")
	if len(parts) > 0 {
		return stdlibModules[parts[0]]
	}
	return false
}

// isRustStdlib checks if an import is from Rust standard library
func (c *ImportClassifier) isRustStdlib(importStr string) bool {
	// Rust stdlib crates
	stdlibCrates := map[string]bool{
		"std": true, "core": true, "alloc": true, "proc_macro": true,
		"test": true,
	}

	// Get the first component
	parts := strings.Split(strings.TrimSpace(importStr), "::")
	if len(parts) > 0 {
		return stdlibCrates[parts[0]]
	}
	return false
}

// isLocalModule checks if an import resolves to a local module within the repo
func (c *ImportClassifier) isLocalModule(importStr string, fromFile string) bool {
	resolved := c.resolveImport(importStr, fromFile)
	if resolved == "" {
		return false
	}

	// Check if resolved path is within repo
	absPath := filepath.Join(c.context.RepoRoot, resolved)
	return paths.IsWithinRepo(absPath, c.context.RepoRoot)
}

// ResolveImport resolves an import to the repo-relative file it reaches, or "".
func (c *ImportClassifier) ResolveImport(importStr string, fromFile string) string {
	return c.resolveImport(importStr, fromFile)
}

// resolveImport attempts to resolve an import to a file path
func (c *ImportClassifier) resolveImport(importStr string, fromFile string) string {
	// Language-specific resolution logic
	switch c.context.Language {
	case LanguageTypeScript, LanguageJavaScript:
		return c.resolveNodeImport(importStr, fromFile)
	case LanguageDart:
		return c.resolveDartImport(importStr, fromFile)
	case LanguageGo:
		return c.resolveGoImport(importStr)
	case LanguagePython:
		return c.resolvePythonImport(importStr, fromFile)
	default:
		return ""
	}
}

// resolveNodeImport resolves a Node.js/TypeScript import
func (c *ImportClassifier) resolveNodeImport(importStr string, fromFile string) string {
	// Check if it's a local path
	if strings.HasPrefix(importStr, "./") || strings.HasPrefix(importStr, "../") {
		fromDir := filepath.Dir(filepath.Join(c.context.RepoRoot, fromFile))
		resolved := filepath.Join(fromDir, importStr)
		rel, err := filepath.Rel(c.context.RepoRoot, resolved)
		if err != nil {
			return ""
		}
		return paths.NormalizePath(rel)
	}

	// Check if it's a workspace package (monorepo)
	// This would require parsing package.json workspaces, which is complex
	// For now, we rely on the WorkspacePackages map being pre-populated

	return ""
}

// resolveDartImport resolves a Dart import
func (c *ImportClassifier) resolveDartImport(importStr string, fromFile string) string {
	// Dart package imports: package:foo/bar.dart
	if strings.HasPrefix(importStr, "package:") {
		// Extract package name
		parts := strings.SplitN(strings.TrimPrefix(importStr, "package:"), "/", 2)
		if len(parts) > 0 {
			packageName := parts[0]
			// Check if this is a local package in the workspace
			if c.context.WorkspacePackages != nil && c.context.WorkspacePackages[packageName] {
				return "" // Will be classified as WorkspacePackage
			}
		}
		return ""
	}

	// Relative imports
	if strings.HasPrefix(importStr, "../") || strings.HasPrefix(importStr, "./") {
		fromDir := filepath.Dir(filepath.Join(c.context.RepoRoot, fromFile))
		resolved := filepath.Join(fromDir, importStr)
		rel, err := filepath.Rel(c.context.RepoRoot, resolved)
		if err != nil {
			return ""
		}
		return paths.NormalizePath(rel)
	}

	return ""
}

// resolveGoImport resolves a Go import
func (c *ImportClassifier) resolveGoImport(importStr string) string {
	c.goModuleOnce.Do(func() {
		content, err := os.ReadFile(filepath.Join(c.context.RepoRoot, "go.mod")) // #nosec G304 -- the repo's go.mod
		if err != nil {
			return
		}
		for _, line := range strings.Split(string(content), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "module" {
				c.goModule = strings.Trim(fields[1], `"`)
				return
			}
		}
	})
	if c.goModule == "" {
		return ""
	}
	if importStr == c.goModule {
		return "."
	}
	if rest, ok := strings.CutPrefix(importStr, c.goModule+"/"); ok {
		return rest
	}
	return ""
}

// resolvePythonImport resolves a Python import
func (c *ImportClassifier) resolvePythonImport(importStr string, fromFile string) string {
	// Python imports are complex due to sys.path
	// For now, we use simple heuristics
	// If the import can be resolved to a file in the repo, it's local

	fromDir := filepath.Dir(filepath.Join(c.context.RepoRoot, fromFile))

	// Try to find a .py file matching the import
	// Convert import to path: foo.bar.baz -> foo/bar/baz.py
	importPath := strings.ReplaceAll(importStr, ".", string(filepath.Separator)) + ".py"
	resolved := filepath.Join(fromDir, importPath)

	if _, err := os.Stat(resolved); err == nil {
		rel, err := filepath.Rel(c.context.RepoRoot, resolved)
		if err != nil {
			return ""
		}
		return paths.NormalizePath(rel)
	}

	// A top-level package of the repo: `import pkg.mod` from anywhere.
	if _, err := os.Stat(filepath.Join(c.context.RepoRoot, importPath)); err == nil {
		return paths.NormalizePath(filepath.ToSlash(importPath))
	}

	// A script that puts a sibling directory on sys.path imports its modules by
	// bare name. The file of that name is it: the only one, or the only one whose
	// directory the importer spells.
	if strings.Contains(importStr, ".") {
		return ""
	}
	content, err := os.ReadFile(filepath.Join(c.context.RepoRoot, fromFile)) // #nosec G304 -- a file of the repo being scanned
	if err != nil || !strings.Contains(string(content), "sys.path") {
		return ""
	}
	c.pyModulesOnce.Do(c.loadPythonModules)
	candidates := c.pyModules[importStr]
	if len(candidates) == 1 {
		return candidates[0]
	}
	var spelled []string
	for _, candidate := range candidates {
		if dir := filepath.Base(filepath.Dir(candidate)); dir != "." && strings.Contains(string(content), `"`+dir+`"`) {
			spelled = append(spelled, candidate)
		}
	}
	if len(spelled) == 1 {
		return spelled[0]
	}
	return ""
}

// loadPythonModules indexes the repo's tracked Python files by module name.
func (c *ImportClassifier) loadPythonModules() {
	c.pyModules = make(map[string][]string)
	cmd := exec.Command("git", "-C", c.context.RepoRoot, "ls-files", "-z", "--", "*.py") // #nosec G204 -- fixed git subcommand
	out, err := cmd.Output()
	if err != nil {
		return
	}
	for _, file := range strings.Split(string(out), "\x00") {
		name := strings.TrimSuffix(filepath.Base(file), ".py")
		if file != "" && name != "__init__" {
			c.pyModules[name] = append(c.pyModules[name], file)
		}
	}
}

// isDeclaredDependency checks if an import is a declared dependency
func (c *ImportClassifier) isDeclaredDependency(importStr string) bool {
	// For package-based imports, extract the package name
	switch c.context.Language {
	case LanguageTypeScript, LanguageJavaScript:
		// Extract package name from scoped or unscoped imports
		packageName := extractNpmPackageName(importStr)
		return c.context.DeclaredDependencies[packageName]

	case LanguageDart:
		if strings.HasPrefix(importStr, "package:") {
			parts := strings.SplitN(strings.TrimPrefix(importStr, "package:"), "/", 2)
			if len(parts) > 0 {
				return c.context.DeclaredDependencies[parts[0]]
			}
		}

	case LanguageGo:
		// For Go, check if the import path starts with any declared dependency
		for dep := range c.context.DeclaredDependencies {
			if strings.HasPrefix(importStr, dep) {
				return true
			}
		}

	case LanguagePython:
		// Get the base module name
		parts := strings.Split(importStr, ".")
		if len(parts) > 0 {
			return c.context.DeclaredDependencies[parts[0]]
		}

	case LanguageRust:
		// Get the crate name (first component)
		parts := strings.Split(strings.TrimSpace(importStr), "::")
		if len(parts) > 0 {
			return c.context.DeclaredDependencies[parts[0]]
		}
	}

	return c.context.DeclaredDependencies[importStr]
}

// extractNpmPackageName extracts the package name from a Node.js import
func extractNpmPackageName(importStr string) string {
	// Handle scoped packages: @scope/package/subpath -> @scope/package
	if strings.HasPrefix(importStr, "@") {
		parts := strings.SplitN(importStr, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
		return importStr
	}

	// Handle unscoped packages: package/subpath -> package
	parts := strings.Split(importStr, "/")
	if len(parts) > 0 {
		return parts[0]
	}

	return importStr
}

// BuildModuleContext builds a module context from detected modules
func BuildModuleContext(repoRoot string, modules []*Module, language string) *ModuleContext {
	ctx := &ModuleContext{
		RepoRoot:             repoRoot,
		Modules:              modules,
		Language:             language,
		WorkspacePackages:    make(map[string]bool),
		DeclaredDependencies: make(map[string]bool),
	}

	// Populate workspace packages and declared dependencies
	for _, module := range modules {
		// Add module name to workspace packages
		if module.Name != "" {
			ctx.WorkspacePackages[module.Name] = true
		}

		// Load declared dependencies from manifest
		loadDependencies(repoRoot, module, ctx)
	}

	return ctx
}

// loadDependencies loads dependencies from a module's manifest
func loadDependencies(repoRoot string, module *Module, ctx *ModuleContext) {
	manifestPath := filepath.Join(repoRoot, module.RootPath, module.ManifestType)

	switch module.ManifestType {
	case ManifestPackageJSON:
		loadPackageJSONDependencies(manifestPath, ctx)
	case ManifestPubspecYaml:
		loadPubspecDependencies(manifestPath, ctx)
	case ManifestCargoToml:
		loadCargoTomlDependencies(manifestPath, ctx)
	case ManifestGoMod:
		loadGoModDependencies(manifestPath, ctx)
	}
}

// loadPackageJSONDependencies loads dependencies from package.json
func loadPackageJSONDependencies(path string, ctx *ModuleContext) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}

	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}

	for dep := range pkg.Dependencies {
		ctx.DeclaredDependencies[dep] = true
	}
	for dep := range pkg.DevDependencies {
		ctx.DeclaredDependencies[dep] = true
	}
	for dep := range pkg.PeerDependencies {
		ctx.DeclaredDependencies[dep] = true
	}
	for dep := range pkg.OptionalDependencies {
		ctx.DeclaredDependencies[dep] = true
	}
}

// loadPubspecDependencies loads dependencies from pubspec.yaml
func loadPubspecDependencies(path string, ctx *ModuleContext) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Simple YAML parsing for dependencies
	lines := strings.Split(string(data), "\n")
	inDepsSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "dependencies:" || trimmed == "dev_dependencies:" {
			inDepsSection = true
			continue
		}

		if inDepsSection {
			// Check if we've left the dependencies section
			if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(line, "  ") {
				inDepsSection = false
				continue
			}

			// Parse dependency line: "  package_name: version"
			if strings.Contains(trimmed, ":") {
				parts := strings.SplitN(trimmed, ":", 2)
				if len(parts) >= 1 {
					depName := strings.TrimSpace(parts[0])
					if depName != "" && !strings.HasPrefix(depName, "#") {
						ctx.DeclaredDependencies[depName] = true
					}
				}
			}
		}
	}
}

// loadCargoTomlDependencies loads dependencies from Cargo.toml
func loadCargoTomlDependencies(path string, ctx *ModuleContext) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Simple TOML parsing for dependencies
	lines := strings.Split(string(data), "\n")
	inDepsSection := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "[dependencies]" || trimmed == "[dev-dependencies]" {
			inDepsSection = true
			continue
		}

		if inDepsSection {
			// Check if we've left the dependencies section
			if strings.HasPrefix(trimmed, "[") {
				inDepsSection = false
				continue
			}

			// Parse dependency line: "crate_name = ..."
			if strings.Contains(trimmed, "=") {
				parts := strings.SplitN(trimmed, "=", 2)
				if len(parts) >= 1 {
					depName := strings.TrimSpace(parts[0])
					if depName != "" && !strings.HasPrefix(depName, "#") {
						ctx.DeclaredDependencies[depName] = true
					}
				}
			}
		}
	}
}

// loadGoModDependencies loads dependencies from go.mod
func loadGoModDependencies(path string, ctx *ModuleContext) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	inRequireBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "require (") {
			inRequireBlock = true
			continue
		}

		if inRequireBlock {
			if trimmed == ")" {
				inRequireBlock = false
				continue
			}

			// Parse require line: "module/path version"
			parts := strings.Fields(trimmed)
			if len(parts) >= 1 {
				modulePath := parts[0]
				if modulePath != "" && !strings.HasPrefix(modulePath, "//") {
					ctx.DeclaredDependencies[modulePath] = true
				}
			}
		} else if strings.HasPrefix(trimmed, "require ") {
			// Single-line require
			parts := strings.Fields(strings.TrimPrefix(trimmed, "require "))
			if len(parts) >= 1 {
				modulePath := parts[0]
				if modulePath != "" {
					ctx.DeclaredDependencies[modulePath] = true
				}
			}
		}
	}
}
