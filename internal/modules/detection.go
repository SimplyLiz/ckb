package modules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/paths"
)

// ManifestFile represents a manifest file that can be used to detect modules
type ManifestFile struct {
	// FileName is the name of the manifest file
	FileName string
	// Language is the language associated with this manifest
	Language string
	// Priority is used to break ties when multiple manifests are found in the same directory
	Priority int
}

// ManifestFiles is the list of manifest files to search for, in priority order
var ManifestFiles = []ManifestFile{
	{FileName: ManifestPackageJSON, Language: LanguageTypeScript, Priority: 10},
	{FileName: ManifestPubspecYaml, Language: LanguageDart, Priority: 10},
	{FileName: ManifestGoMod, Language: LanguageGo, Priority: 10},
	{FileName: ManifestCargoToml, Language: LanguageRust, Priority: 10},
	{FileName: ManifestPyprojectToml, Language: LanguagePython, Priority: 10},
	{FileName: ManifestSetupPy, Language: LanguagePython, Priority: 5},
	{FileName: ManifestPomXML, Language: LanguageJava, Priority: 10},
	{FileName: ManifestBuildGradle, Language: LanguageJava, Priority: 9},
	{FileName: ManifestBuildGradleKts, Language: LanguageKotlin, Priority: 9},
}

// ConventionDirectory represents a convention-based directory that might contain a module
type ConventionDirectory struct {
	DirName  string
	Language string
}

// ConventionDirectories is the list of convention-based directories to check
var ConventionDirectories = []ConventionDirectory{
	{DirName: "src", Language: LanguageUnknown},
	{DirName: "lib", Language: LanguageUnknown},
	{DirName: "internal", Language: LanguageGo},
	{DirName: "pkg", Language: LanguageGo},
}

// DetectionResult represents the result of module detection
type DetectionResult struct {
	Modules         []*Module
	DetectionMethod string // "explicit", "manifest", "convention", "fallback"
}

// DetectModules detects modules in a repository using the cascading resolution order
func DetectModules(repoRoot string, explicitRoots []string, ignoreDirs []string, stateId string, logger *slog.Logger) (*DetectionResult, error) {
	return DetectModulesWithDeclaration(repoRoot, explicitRoots, ignoreDirs, "", stateId, logger)
}

// DetectModulesWithDeclaration detects modules with optional MODULES.toml support
func DetectModulesWithDeclaration(repoRoot string, explicitRoots []string, ignoreDirs []string, declarationFile string, stateId string, logger *slog.Logger) (*DetectionResult, error) {
	// Step 0: Check for MODULES.toml (v6.0 declared modules)
	declaredModules, err := LoadDeclaredModules(repoRoot, declarationFile, stateId)
	if err != nil {
		logger.Warn("Failed to load MODULES.toml", "error", err.Error())
		// Continue with fallback detection
	}
	if len(declaredModules) > 0 {
		logger.Debug("Loaded declared modules from MODULES.toml", "count", len(declaredModules))
		return &DetectionResult{
			Modules:         declaredModules,
			DetectionMethod: "declared",
		}, nil
	}

	// Step 1: Check explicit config
	if len(explicitRoots) > 0 {
		explicitModules, explicitErr := detectExplicitModules(repoRoot, explicitRoots, stateId, logger)
		if explicitErr != nil {
			return nil, explicitErr
		}
		return &DetectionResult{
			Modules:         explicitModules,
			DetectionMethod: "explicit",
		}, nil
	}

	// Step 2: Try manifest-based detection
	modules, err := detectManifestModules(repoRoot, ignoreDirs, stateId, logger)
	if err != nil {
		return nil, err
	}
	if len(modules) > 0 {
		return &DetectionResult{
			Modules:         modules,
			DetectionMethod: "manifest",
		}, nil
	}

	// Step 3: Try language convention detection
	modules, err = detectConventionModules(repoRoot, ignoreDirs, stateId, logger)
	if err != nil {
		return nil, err
	}
	if len(modules) > 0 {
		return &DetectionResult{
			Modules:         modules,
			DetectionMethod: "convention",
		}, nil
	}

	// Step 4: Fallback to top-level directories
	modules, err = detectFallbackModules(repoRoot, ignoreDirs, stateId, logger)
	if err != nil {
		return nil, err
	}
	return &DetectionResult{
		Modules:         modules,
		DetectionMethod: "fallback",
	}, nil
}

// detectExplicitModules detects modules from explicit configuration
func detectExplicitModules(repoRoot string, explicitRoots []string, stateId string, logger *slog.Logger) ([]*Module, error) {
	var modules []*Module

	for _, root := range explicitRoots {
		absPath := filepath.Join(repoRoot, root)

		// Check if path exists
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			logger.Warn("Explicit module root does not exist", "root", root)
			continue
		}

		// Try to detect manifest in this directory
		manifest, language := detectManifestInDir(absPath)

		// If no manifest found, detect language from file extensions
		if language == LanguageUnknown {
			language = detectLanguageFromFiles(absPath)
		}

		// Generate module ID
		moduleID := generateModuleID(repoRoot, root)

		// Extract name from path or manifest
		name := extractModuleName(absPath, manifest, root)

		module := NewModule(moduleID, name, root, manifest, language, stateId)
		modules = append(modules, module)

		logger.Debug("Detected explicit module",
			"id", moduleID,
			"name", name,
			"rootPath", root,
			"manifestType", manifest,
			"language", language,
		)
	}

	return modules, nil
}

// detectLanguageFromFiles scans a directory for source files and infers the language
func detectLanguageFromFiles(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return LanguageUnknown
	}

	langCounts := make(map[string]int)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".go":
			langCounts[LanguageGo]++
		case ".ts", ".tsx":
			langCounts[LanguageTypeScript]++
		case ".js", ".jsx":
			langCounts[LanguageJavaScript]++
		case ".dart":
			langCounts[LanguageDart]++
		case ".py":
			langCounts[LanguagePython]++
		case ".rs":
			langCounts[LanguageRust]++
		case ".java":
			langCounts[LanguageJava]++
		case ".kt", ".kts":
			langCounts[LanguageKotlin]++
		}
	}

	// Return the language with the most files
	maxCount := 0
	bestLang := LanguageUnknown
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			bestLang = lang
		}
	}

	return bestLang
}

// detectManifestModules walks the repository and finds all manifest files
func detectManifestModules(repoRoot string, ignoreDirs []string, stateId string, logger *slog.Logger) ([]*Module, error) {
	var modules []*Module
	ignoreMap := make(map[string]bool)
	for _, dir := range ignoreDirs {
		ignoreMap[dir] = true
	}

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip ignored and hidden directories. A manifest under .claude/,
		// .github/ or .codex/ is a tool's checkout or action, not a module of
		// this repo: it used to make agent worktrees show up as entrypoints.
		if info.IsDir() {
			relPath, _ := filepath.Rel(repoRoot, path)
			if relPath != "." && (strings.HasPrefix(info.Name(), ".") || shouldIgnore(relPath, ignoreMap)) {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if this is a manifest file
		for _, mf := range ManifestFiles {
			if info.Name() == mf.FileName {
				dir := filepath.Dir(path)
				relPath, _ := filepath.Rel(repoRoot, dir)
				relPath = paths.NormalizePath(relPath)

				moduleID := generateModuleID(repoRoot, relPath)
				name := extractModuleName(path, mf.FileName, relPath)

				module := NewModule(moduleID, name, relPath, mf.FileName, mf.Language, stateId)
				modules = append(modules, module)

				logger.Debug("Detected manifest module",
					"id", moduleID,
					"name", name,
					"rootPath", relPath,
					"manifestType", mf.FileName,
				)

				// Don't descend into this module's subdirectories
				return filepath.SkipDir
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return modules, nil
}

// detectConventionModules detects modules based on language conventions
func detectConventionModules(repoRoot string, ignoreDirs []string, stateId string, logger *slog.Logger) ([]*Module, error) {
	var modules []*Module
	ignoreMap := make(map[string]bool)
	for _, dir := range ignoreDirs {
		ignoreMap[dir] = true
	}

	for _, conv := range ConventionDirectories {
		convPath := filepath.Join(repoRoot, conv.DirName)
		if info, err := os.Stat(convPath); err == nil && info.IsDir() {
			if shouldIgnore(conv.DirName, ignoreMap) {
				continue
			}

			relPath := conv.DirName
			moduleID := generateModuleID(repoRoot, relPath)
			name := conv.DirName
			language := conv.Language

			module := NewModule(moduleID, name, relPath, ManifestNone, language, stateId)
			modules = append(modules, module)

			logger.Debug("Detected convention module",
				"id", moduleID,
				"name", name,
				"rootPath", relPath,
			)
		}
	}

	return modules, nil
}

// detectFallbackModules detects modules using top-level directories as fallback
func detectFallbackModules(repoRoot string, ignoreDirs []string, stateId string, logger *slog.Logger) ([]*Module, error) {
	var modules []*Module
	ignoreMap := make(map[string]bool)
	for _, dir := range ignoreDirs {
		ignoreMap[dir] = true
	}

	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirName := entry.Name()

		// Skip hidden directories and ignored directories
		if strings.HasPrefix(dirName, ".") || shouldIgnore(dirName, ignoreMap) {
			continue
		}

		relPath := dirName
		moduleID := generateModuleID(repoRoot, relPath)
		name := dirName

		module := NewModule(moduleID, name, relPath, ManifestNone, LanguageUnknown, stateId)
		modules = append(modules, module)

		logger.Debug("Detected fallback module",
			"id", moduleID,
			"name", name,
			"rootPath", relPath,
		)
	}

	return modules, nil
}

// detectManifestInDir checks for manifest files in a specific directory
func detectManifestInDir(dir string) (string, string) {
	for _, mf := range ManifestFiles {
		manifestPath := filepath.Join(dir, mf.FileName)
		if _, err := os.Stat(manifestPath); err == nil {
			return mf.FileName, mf.Language
		}
	}
	return ManifestNone, LanguageUnknown
}

// generateModuleID generates a stable module ID based on the repo and module path
func generateModuleID(repoRoot, modulePath string) string {
	// Normalize path
	normalizedPath := paths.NormalizePath(modulePath)

	// Use a simple scheme: ckb:mod:<hash>
	// Hash the normalized path for uniqueness
	// NOTE: Must match GenerateStableModuleID in declaration.go
	hash := sha256.Sum256([]byte(normalizedPath))
	hashStr := hex.EncodeToString(hash[:8]) // Use first 8 bytes for shorter ID

	return fmt.Sprintf("ckb:mod:%s", hashStr)
}

// extractModuleName extracts the module name from manifest or path
func extractModuleName(manifestPath, manifestType, fallbackPath string) string {
	// Try to extract name from manifest file
	switch manifestType {
	case ManifestPackageJSON:
		return extractNameFromPackageJSON(manifestPath)
	case ManifestPubspecYaml:
		return extractNameFromPubspec(manifestPath)
	case ManifestGoMod:
		return extractNameFromGoMod(manifestPath)
	case ManifestCargoToml:
		return extractNameFromCargoToml(manifestPath)
	}

	// Fallback to directory name
	if fallbackPath != "" {
		parts := strings.Split(fallbackPath, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
	}

	return "unknown"
}

// extractNameFromPackageJSON extracts name from package.json
func extractNameFromPackageJSON(path string) string {
	// If path is a directory, look for package.json in it
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "package.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return filepath.Base(filepath.Dir(path))
	}

	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return filepath.Base(filepath.Dir(path))
	}

	if pkg.Name != "" {
		return pkg.Name
	}

	return filepath.Base(filepath.Dir(path))
}

// extractNameFromPubspec extracts name from pubspec.yaml
func extractNameFromPubspec(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "pubspec.yaml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return filepath.Base(filepath.Dir(path))
	}

	// Simple YAML parsing for name field
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}

	return filepath.Base(filepath.Dir(path))
}

// extractNameFromGoMod extracts module name from go.mod
func extractNameFromGoMod(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "go.mod")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return filepath.Base(filepath.Dir(path))
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "module ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				// Return the last part of the module path
				modulePath := parts[1]
				pathParts := strings.Split(modulePath, "/")
				return pathParts[len(pathParts)-1]
			}
		}
	}

	return filepath.Base(filepath.Dir(path))
}

// extractNameFromCargoToml extracts name from Cargo.toml
func extractNameFromCargoToml(path string) string {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, "Cargo.toml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return filepath.Base(filepath.Dir(path))
	}

	// Simple TOML parsing for name field in [package] section
	lines := strings.Split(string(data), "\n")
	inPackageSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "[package]" {
			inPackageSection = true
			continue
		}

		if inPackageSection && strings.HasPrefix(trimmed, "name") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				name := strings.TrimSpace(parts[1])
				// Remove quotes
				name = strings.Trim(name, `"'`)
				return name
			}
		}

		// Stop at next section
		if inPackageSection && strings.HasPrefix(trimmed, "[") {
			break
		}
	}

	return filepath.Base(filepath.Dir(path))
}

// shouldIgnore checks if a directory should be ignored
func shouldIgnore(relPath string, ignoreMap map[string]bool) bool {
	// Check exact match
	if ignoreMap[relPath] {
		return true
	}

	// Check if any parent directory is ignored
	parts := strings.Split(relPath, string(filepath.Separator))
	for _, part := range parts {
		if ignoreMap[part] {
			return true
		}
	}

	return false
}
