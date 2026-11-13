// Package project provides language and indexer detection for repositories.
package project

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Language represents a programming language.
type Language string

const (
	LangGo         Language = "go"
	LangTypeScript Language = "typescript"
	LangJavaScript Language = "javascript"
	LangPython     Language = "python"
	LangRust       Language = "rust"
	LangJava       Language = "java"
	LangKotlin     Language = "kotlin"
	LangCpp        Language = "cpp"
	LangDart       Language = "dart"
	LangRuby       Language = "ruby"
	LangCSharp     Language = "csharp"
	LangPHP        Language = "php"
	LangUnknown    Language = "unknown"
)

// Detection scan limits
const (
	maxScanDepth    = 3
	maxFilesToCheck = 100
)

// IndexerInfo contains information about a SCIP indexer.
type IndexerInfo struct {
	Command        string // Command to run the indexer
	InstallCommand string // Command to install the indexer
	CheckCommand   string // Command to check if installed (usually just the binary name)
	OutputFile     string // Expected output file (default: index.scip)
}

// ProjectConfig stores detected project information.
type ProjectConfig struct {
	// Language is the primary language; Indexer and ManifestPath are its.
	Language     Language  `json:"language"`
	Indexer      string    `json:"indexer"`
	ManifestPath string    `json:"manifestPath"`
	DetectedAt   time.Time `json:"detectedAt"`

	// Languages are all languages the last index was built for, Language
	// first. Empty in a project.json written before multi-language indexing,
	// which always names one language: see IndexLanguages.
	Languages []Language `json:"languages,omitempty"`
	// LanguageFromFlag means 'ckb index --lang' chose the language, so a
	// background reindex keeps to it instead of detecting all languages.
	LanguageFromFlag bool `json:"languageFromFlag,omitempty"`
}

// IndexLanguages returns the languages the saved config says to index:
// Languages, or Language alone for a config from before multi-language indexing.
func (c *ProjectConfig) IndexLanguages() []Language {
	if len(c.Languages) > 0 {
		return c.Languages
	}
	if c.Language != "" && c.Language != LangUnknown {
		return []Language{c.Language}
	}
	return nil
}

// manifestInfo maps marker files to languages
type manifestInfo struct {
	pattern string   // File pattern (can contain * for glob)
	lang    Language // Language this indicates
}

// manifests in priority order - first match wins for primary language
var manifests = []manifestInfo{
	// Exact matches first (faster)
	{"go.mod", LangGo},
	{"package.json", LangTypeScript}, // Refined to JS/TS below
	{"Cargo.toml", LangRust},
	{"pyproject.toml", LangPython},
	{"requirements.txt", LangPython},
	{"setup.py", LangPython},
	{"pom.xml", LangJava},
	{"build.gradle", LangJava},
	{"build.gradle.kts", LangKotlin},
	{"pubspec.yaml", LangDart},
	{"composer.json", LangPHP},
	{"Gemfile", LangRuby},
	// C/C++ - only detect if compile_commands.json exists
	{"compile_commands.json", LangCpp},
	{"build/compile_commands.json", LangCpp},
	// Glob patterns (slower, searched with bounded depth)
	{"*.gemspec", LangRuby},
	{"*.csproj", LangCSharp},
	{"*.sln", LangCSharp},
}

// DetectLanguage detects the primary language of a project from manifest files.
// Returns the language, manifest path, and whether detection succeeded.
func DetectLanguage(root string) (Language, string, bool) {
	lang, manifest, _ := DetectAllLanguages(root)
	if lang == LangUnknown {
		return LangUnknown, "", false
	}
	return lang, manifest, true
}

// DetectAllLanguages detects all languages present in a project.
// Returns primary language, its manifest, and a list of all detected languages.
func DetectAllLanguages(root string) (Language, string, []Language) {
	detected := make(map[Language]string) // lang -> manifest path
	filter := newDirFilter(root)

	for _, m := range manifests {
		if _, exists := detected[m.lang]; exists {
			continue // Already found this language
		}

		if strings.Contains(m.pattern, "*") {
			// Glob pattern - search with bounded depth
			found := findWithDepth(root, m.pattern, filter)
			if len(found) > 0 {
				relPath, _ := filepath.Rel(root, found[0])
				detected[m.lang] = relPath
			}
		} else if strings.Contains(m.pattern, "/") {
			// Path with directory - check exact location
			fullPath := filepath.Join(root, m.pattern)
			if _, err := os.Stat(fullPath); err == nil {
				detected[m.lang] = m.pattern
			}
		} else {
			// Exact filename - search root and subdirectories up to maxScanDepth.
			// This catches monorepo layouts like src/cli/go.mod or packages/api/package.json.
			if found := findManifestIn(root, m.pattern, filter); found != "" {
				detected[m.lang] = found
			}
		}
	}

	if len(detected) == 0 {
		if lang, from := detectBySourceFiles(root); lang != LangUnknown {
			return lang, from, []Language{lang}
		}
		return LangUnknown, "", nil
	}

	// Build list preserving priority order
	var allLangs []Language
	var primaryLang Language
	var primaryManifest string

	for _, m := range manifests {
		if manifest, ok := detected[m.lang]; ok {
			if primaryLang == "" {
				primaryLang = m.lang
				primaryManifest = manifest
				// Refine TypeScript vs JavaScript
				if m.pattern == "package.json" {
					primaryLang = detectJSorTS(root)
				}
			}
			// Add to list if not already there
			found := false
			for _, l := range allLangs {
				if l == m.lang {
					found = true
					break
				}
			}
			if !found {
				allLangs = append(allLangs, m.lang)
			}
		}
	}

	return primaryLang, primaryManifest, allLangs
}

// sourceOnlyLanguages are the languages whose indexer runs without a manifest:
// scip-python indexes a directory of scripts, scip-typescript infers a tsconfig.
var sourceOnlyLanguages = map[string]Language{
	".py":  LangPython,
	".ts":  LangTypeScript,
	".tsx": LangTypeScript,
	".js":  LangJavaScript,
	".jsx": LangJavaScript,
	".mjs": LangJavaScript,
}

// detectBySourceFiles picks the language of most tracked source files when the repo
// has no manifest at all, such as a directory of Python scripts. Only git-tracked
// files count, so ignored scratch and installed packages don't decide. The second
// result says what the choice came from, for the "Detected ... (from ...)" line; it
// holds no slash, so nothing takes it for a nested manifest.
func detectBySourceFiles(root string) (Language, string) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return LangUnknown, ""
	}
	counts := make(map[Language]int)
	exts := make(map[Language]string)
	for _, path := range strings.Split(string(out), "\x00") {
		ext := strings.ToLower(filepath.Ext(path))
		lang, ok := sourceOnlyLanguages[ext]
		if !ok || strings.Contains("/"+path, "/node_modules/") {
			continue
		}
		counts[lang]++
		if exts[lang] == "" {
			exts[lang] = ext
		}
	}
	best, bestCount := LangUnknown, 0
	for _, lang := range []Language{LangPython, LangTypeScript, LangJavaScript} {
		if counts[lang] > bestCount {
			best, bestCount = lang, counts[lang]
		}
	}
	if best == LangUnknown {
		return LangUnknown, ""
	}
	return best, fmt.Sprintf("%d tracked %s files, no manifest", bestCount, exts[best])
}

// skipManifestDir says whether a directory below the root can hold no manifest
// of this project: dependencies, vendored code under vendor/ or third_party/,
// and every hidden directory. Tools keep their own checkouts there (.claude/
// worktrees, .github/ actions), and those used to add languages to the list.
func skipManifestDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "third_party", "__pycache__", "venv":
		return true
	}
	return false
}

// findManifest searches for an exact filename in root and subdirectories up to maxScanDepth.
// Returns the relative path to the first match, or empty string.
// Root is checked first (fast path). Among subdirectories, WalkDir visits in
// lexical order, so at equal depth the alphabetically-first path wins.
// Skips example, test, doc, and vendor directories to avoid false detections,
// and nested git repos and gitignored directories (see dirFilter).
func findManifest(root, filename string) string {
	return findManifestIn(root, filename, newDirFilter(root))
}

// findManifestIn is findManifest with a filter shared across one detection run.
func findManifestIn(root, filename string, filter *dirFilter) string {
	// Check root first (fast path)
	if _, err := os.Stat(filepath.Join(root, filename)); err == nil {
		return filename
	}

	// Search subdirectories with bounded depth
	var result string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if result != "" {
			return fs.SkipAll // Already found
		}

		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > maxScanDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if skipManifestDir(d.Name()) || filter.skip(path, rel) {
				return filepath.SkipDir
			}
			switch d.Name() {
			case "dist", "build",
				"examples", "example", "testdata", "fixtures",
				"docs", "doc", "documentation":
				return filepath.SkipDir
			}
			return nil
		}

		if d.Name() == filename {
			result = rel
			return fs.SkipAll
		}
		return nil
	})
	return result
}

// FindManifestForLanguage searches for the manifest file of a specific language.
// Used when --lang is specified to locate the module directory for indexing.
func FindManifestForLanguage(root string, lang Language) string {
	filter := newDirFilter(root)
	for _, m := range manifests {
		if m.lang != lang {
			continue
		}
		if strings.Contains(m.pattern, "*") {
			found := findWithDepth(root, m.pattern, filter)
			if len(found) > 0 {
				relPath, _ := filepath.Rel(root, found[0])
				return relPath
			}
		} else {
			if found := findManifestIn(root, m.pattern, filter); found != "" {
				return found
			}
		}
	}
	return ""
}

// findWithDepth searches for files matching a glob pattern with bounded depth,
// outside the directories filter assigns to other projects.
func findWithDepth(root, pattern string, filter *dirFilter) []string {
	var results []string
	checked := 0

	// Check if pattern includes a path separator
	hasPathSep := strings.Contains(pattern, "/") || strings.Contains(pattern, string(os.PathSeparator))

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // Skip errors, continue walking
		}
		if checked >= maxFilesToCheck {
			return fs.SkipAll // Stop walk entirely
		}

		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth > maxScanDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip common non-project dirs, nested repos and ignored dirs
		if d.IsDir() && (skipManifestDir(d.Name()) || filter.skip(path, rel)) {
			return filepath.SkipDir
		}

		if !d.IsDir() {
			checked++

			var matched bool
			if hasPathSep {
				// Pattern has path component - match against relative path
				matched, _ = filepath.Match(pattern, rel)
			} else {
				// Simple pattern - match against basename only
				matched, _ = filepath.Match(pattern, d.Name())
			}

			if matched {
				results = append(results, path)
			}
		}
		return nil
	})
	return results
}

// detectJSorTS checks if a project is TypeScript or JavaScript.
func detectJSorTS(root string) Language {
	// Check for tsconfig.json
	if _, err := os.Stat(filepath.Join(root, "tsconfig.json")); err == nil {
		return LangTypeScript
	}
	// Check for .ts files
	if hasFileWithExt(root, ".ts") {
		return LangTypeScript
	}
	return LangJavaScript
}

// hasFileWithExt checks if any file with the given extension exists in the root.
func hasFileWithExt(root, ext string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ext {
			return true
		}
	}
	// Check src directory if exists
	srcDir := filepath.Join(root, "src")
	if entries, err := os.ReadDir(srcDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ext {
				return true
			}
		}
	}
	return false
}

// GetIndexerInfo returns the SCIP indexer information for a language.
func GetIndexerInfo(lang Language) *IndexerInfo {
	switch lang {
	case LangGo:
		return &IndexerInfo{
			Command:        "scip-go",
			InstallCommand: "go install github.com/sourcegraph/scip-go@latest",
			CheckCommand:   "scip-go",
			OutputFile:     "index.scip",
		}
	case LangTypeScript, LangJavaScript:
		return &IndexerInfo{
			Command:        "scip-typescript index --infer-tsconfig",
			InstallCommand: "npm install -g @sourcegraph/scip-typescript",
			CheckCommand:   "scip-typescript",
			OutputFile:     "index.scip",
		}
	case LangPython:
		return &IndexerInfo{
			Command:        "scip-python index .",
			InstallCommand: "npm install -g @sourcegraph/scip-python",
			CheckCommand:   "scip-python",
			OutputFile:     "index.scip",
		}
	case LangRust:
		return &IndexerInfo{
			Command:        "rust-analyzer scip .",
			InstallCommand: "rustup component add rust-analyzer",
			CheckCommand:   "rust-analyzer",
			OutputFile:     "index.scip",
		}
	case LangJava:
		return &IndexerInfo{
			Command:        "scip-java index",
			InstallCommand: "cs install scip-java",
			CheckCommand:   "scip-java",
			OutputFile:     "index.scip",
		}
	case LangKotlin:
		// Kotlin requires scip-java with Gradle integration
		return &IndexerInfo{
			Command:        "scip-java index",
			InstallCommand: "Kotlin requires scip-java with Gradle plugin. See: https://sourcegraph.github.io/scip-java/",
			CheckCommand:   "scip-java",
			OutputFile:     "index.scip",
		}
	case LangCpp:
		// Command built dynamically based on compile_commands.json location
		return &IndexerInfo{
			Command:        "scip-clang",
			InstallCommand: "Download from https://github.com/sourcegraph/scip-clang/releases",
			CheckCommand:   "scip-clang",
			OutputFile:     "index.scip",
		}
	case LangDart:
		return &IndexerInfo{
			Command:        "dart pub global run scip_dart ./",
			InstallCommand: "dart pub global activate scip_dart\nThen run: dart pub get",
			CheckCommand:   "dart",
			OutputFile:     "index.scip",
		}
	case LangRuby:
		// Command built dynamically based on Gemfile/sorbet presence
		return &IndexerInfo{
			Command:        "scip-ruby .",
			InstallCommand: "Download from https://github.com/sourcegraph/scip-ruby/releases",
			CheckCommand:   "scip-ruby",
			OutputFile:     "index.scip",
		}
	case LangCSharp:
		return &IndexerInfo{
			Command:        "scip-dotnet index",
			InstallCommand: "dotnet tool install --global scip-dotnet (requires .NET 8+)\nIf not found, ensure $HOME/.dotnet/tools is on PATH",
			CheckCommand:   "scip-dotnet",
			OutputFile:     "index.scip",
		}
	case LangPHP:
		return &IndexerInfo{
			Command:        "vendor/bin/scip-php",
			InstallCommand: "composer require --dev davidrjenni/scip-php (requires PHP 8.2+)",
			CheckCommand:   "vendor/bin/scip-php",
			OutputFile:     "index.scip",
		}
	default:
		return nil
	}
}

// SaveConfig saves project configuration to .ckb/project.json.
func SaveConfig(root string, config *ProjectConfig) error {
	ckbDir := filepath.Join(root, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(ckbDir, "project.json"), data, 0644)
}

// LoadConfig loads project configuration from .ckb/project.json.
func LoadConfig(root string) (*ProjectConfig, error) {
	configPath := filepath.Join(root, ".ckb", "project.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config ProjectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// LanguageDisplayName returns a human-readable name for the language.
func LanguageDisplayName(lang Language) string {
	switch lang {
	case LangGo:
		return "Go"
	case LangTypeScript:
		return "TypeScript"
	case LangJavaScript:
		return "JavaScript"
	case LangPython:
		return "Python"
	case LangRust:
		return "Rust"
	case LangJava:
		return "Java"
	case LangKotlin:
		return "Kotlin"
	case LangCpp:
		return "C/C++"
	case LangDart:
		return "Dart"
	case LangRuby:
		return "Ruby"
	case LangCSharp:
		return "C#"
	case LangPHP:
		return "PHP"
	default:
		return "Unknown"
	}
}

// FindCompileCommands searches for compile_commands.json in common locations.
// Returns the path relative to root, or empty string if not found.
func FindCompileCommands(root string) string {
	locations := []string{
		"compile_commands.json",
		"build/compile_commands.json",
		"out/compile_commands.json",
		"cmake-build-debug/compile_commands.json",
		"cmake-build-release/compile_commands.json",
	}
	for _, loc := range locations {
		path := filepath.Join(root, loc)
		if _, err := os.Stat(path); err == nil {
			return loc // Return relative path
		}
	}

	// Try glob patterns: build/*/compile_commands.json, out/*/compile_commands.json
	patterns := []string{
		filepath.Join(root, "build", "*", "compile_commands.json"),
		filepath.Join(root, "out", "*", "compile_commands.json"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			rel, _ := filepath.Rel(root, matches[0])
			return rel
		}
	}

	return ""
}

// BuildCppCommand builds the scip-clang command with the correct compdb path.
// overrideCompdb allows specifying a custom path via --compdb flag.
func BuildCppCommand(root, overrideCompdb string) (string, error) {
	compdbPath := overrideCompdb
	if compdbPath == "" {
		compdbPath = FindCompileCommands(root)
	}
	if compdbPath == "" {
		return "", nil // Will trigger error in caller
	}

	return "scip-clang --compdb-path=" + compdbPath, nil
}

// BuildRubyCommand builds the appropriate scip-ruby command based on project setup.
func BuildRubyCommand(root string) (string, error) {
	hasGemfile := fileExists(filepath.Join(root, "Gemfile"))
	hasSorbetConfig := fileExists(filepath.Join(root, "sorbet", "config"))

	if hasGemfile {
		// Check bundle is available
		if _, err := exec.LookPath("bundle"); err != nil {
			return "", err // Will show install hint
		}

		if hasSorbetConfig {
			return "bundle exec scip-ruby", nil
		}
		return "bundle exec scip-ruby .", nil
	}
	// No Gemfile - use direct binary
	return "scip-ruby .", nil
}

// ValidatePHPSetup checks that PHP project is properly set up for indexing.
func ValidatePHPSetup(root string) (warning string, err error) {
	// composer.lock missing is a warning, not an error
	composerLock := filepath.Join(root, "composer.lock")
	if _, statErr := os.Stat(composerLock); os.IsNotExist(statErr) {
		warning = "composer.lock not found. Consider running: composer install"
	}

	// vendor/bin/scip-php missing is a hard error
	scipPhp := filepath.Join(root, "vendor", "bin", "scip-php")
	if _, statErr := os.Stat(scipPhp); os.IsNotExist(statErr) {
		return warning, os.ErrNotExist
	}

	return warning, nil
}

// fileExists is a helper to check if a file exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
