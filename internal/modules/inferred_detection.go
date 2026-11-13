package modules

import (
	"bufio"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InferredDirectory represents a directory detected as an interesting architectural unit
// through heuristic scoring when no explicit module boundaries exist.
type InferredDirectory struct {
	// Path is the repo-relative path to the directory
	Path string `json:"path"`

	// Score is the calculated interestingness score (higher = more significant)
	Score float64 `json:"score"`

	// HasIndexFile indicates the directory contains an index file (index.ts, mod.rs, __init__.py, etc.)
	HasIndexFile bool `json:"hasIndexFile"`

	// FileCount is the number of source files in this directory (non-recursive)
	FileCount int `json:"fileCount"`

	// Language is the dominant language detected in this directory
	Language string `json:"language"`

	// IsSemantic indicates the directory name matches common architectural patterns
	IsSemantic bool `json:"isSemantic"`

	// Depth is the directory depth from the repo root (1 = top-level)
	Depth int `json:"depth"`

	// IsIntermediate indicates this directory was added to complete the hierarchy
	// (contains no direct source files, only subdirectories)
	IsIntermediate bool `json:"isIntermediate,omitempty"`
}

// InferOptions configures the directory inference algorithm
type InferOptions struct {
	// MaxDepth limits how deep to scan (default: 4)
	MaxDepth int

	// MinScore is the minimum score to include a directory (default: 2.0)
	MinScore float64

	// MaxDirectories caps the number of directories returned (default: 50)
	MaxDirectories int

	// IgnoreDirs specifies directories to skip (e.g., node_modules, vendor)
	IgnoreDirs []string

	// TargetPath optionally focuses on a subdirectory
	TargetPath string

	// Logger for debug output
	Logger *slog.Logger
}

// DefaultInferOptions returns sensible defaults for directory inference
func DefaultInferOptions() InferOptions {
	return InferOptions{
		MaxDepth:       4,
		MinScore:       2.0,
		MaxDirectories: 50,
		IgnoreDirs:     []string{"node_modules", "vendor", ".git", "__pycache__", ".next", "dist", "build", "target"},
	}
}

// IndexFilePatterns maps languages to their index file patterns
var IndexFilePatterns = map[string][]string{
	LanguageTypeScript: {"index.ts", "index.tsx"},
	LanguageJavaScript: {"index.js", "index.jsx", "index.mjs"},
	LanguageRust:       {"mod.rs", "lib.rs"},
	LanguagePython:     {"__init__.py"},
	LanguageGo:         {}, // Go uses package-level imports, no explicit index files
	LanguageDart:       {}, // Dart uses library declarations
	LanguageJava:       {}, // Java uses package-info.java but it's rare
	LanguageKotlin:     {}, // Kotlin doesn't have index files
}

// SemanticDirectoryNames are common meaningful directory names in software projects
var SemanticDirectoryNames = map[string]bool{
	// Source organization
	"src": true, "lib": true, "app": true, "web": true, "api": true,
	"core": true, "common": true, "shared": true,

	// Architecture layers
	"components": true, "hooks": true, "utils": true, "helpers": true,
	"services": true, "models": true, "controllers": true, "views": true,
	"pages": true, "routes": true, "middleware": true, "handlers": true,
	"providers": true, "stores": true, "actions": true, "reducers": true,

	// Configuration and infrastructure
	"config": true, "configs": true, "settings": true,
	"types": true, "interfaces": true, "schemas": true,
	"database": true, "db": true, "migrations": true,

	// Testing
	"tests": true, "test": true, "__tests__": true, "spec": true,
	"e2e": true, "integration": true, "unit": true,

	// Go-specific
	"internal": true, "pkg": true, "cmd": true,

	// Assets and resources
	"assets": true, "public": true, "static": true,
	"templates": true, "layouts": true,

	// Domain-specific
	"features": true, "modules": true, "domains": true,
	"entities": true, "repositories": true, "usecases": true,
}

// DetectInferredDirectories finds meaningful directory units when no explicit modules exist.
// It uses a scoring algorithm to identify architecturally significant directories.
func DetectInferredDirectories(repoRoot string, opts InferOptions) ([]*InferredDirectory, error) {
	if opts.MaxDepth == 0 {
		opts.MaxDepth = 4
	}
	if opts.MinScore == 0 {
		opts.MinScore = 2.0
	}
	if opts.MaxDirectories == 0 {
		opts.MaxDirectories = 50
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	// Build ignore map for fast lookup
	ignoreMap := make(map[string]bool)
	for _, dir := range opts.IgnoreDirs {
		ignoreMap[dir] = true
	}

	// Determine scan root
	scanRoot := repoRoot
	if opts.TargetPath != "" {
		scanRoot = filepath.Join(repoRoot, opts.TargetPath)
	}

	var directories []*InferredDirectory
	ignoredByGit := GitIgnoredDirs(repoRoot)

	// Walk the directory tree up to MaxDepth
	err := filepath.WalkDir(scanRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// WalkDir passes errors for unreadable directories; return nil to continue walking
			return nil //nolint:nilerr // intentional: skip unreadable directories
		}

		if !d.IsDir() {
			return nil
		}

		// Get relative path from repo root
		relPath, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			// Can't compute relative path; skip this directory
			return nil //nolint:nilerr // intentional: skip paths we can't process
		}
		if relPath == "." {
			return nil // Skip root itself
		}

		// Calculate depth
		depth := strings.Count(relPath, string(os.PathSeparator)) + 1

		// Skip if too deep
		if depth > opts.MaxDepth {
			return filepath.SkipDir
		}

		// Skip ignored directories
		dirName := filepath.Base(path)
		if ignoreMap[dirName] || strings.HasPrefix(dirName, ".") || ignoredByGit[filepath.ToSlash(relPath)] {
			return filepath.SkipDir
		}

		// Score this directory
		dir := scoreDirectory(path, relPath, depth)
		if dir.Score >= opts.MinScore {
			directories = append(directories, dir)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Sort by score descending, then by path for stability
	sort.Slice(directories, func(i, j int) bool {
		if directories[i].Score != directories[j].Score {
			return directories[i].Score > directories[j].Score
		}
		return directories[i].Path < directories[j].Path
	})

	// Apply cap
	if len(directories) > opts.MaxDirectories {
		directories = directories[:opts.MaxDirectories]
	}

	// Prune nested directories where parent is included and child doesn't have strong signals
	directories = pruneNestedDirectories(directories)

	// Fill in missing intermediate directories for complete hierarchy
	directories = fillIntermediateDirectories(directories)

	return directories, nil
}

// scoreDirectory calculates a score for a directory based on various heuristics
func scoreDirectory(absPath, relPath string, depth int) *InferredDirectory {
	dir := &InferredDirectory{
		Path:  relPath,
		Depth: depth,
	}

	dirName := filepath.Base(relPath)

	// Score 1: Check for index file (+3)
	dir.HasIndexFile, dir.Language = hasIndexFile(absPath)
	if dir.HasIndexFile {
		dir.Score += 3.0
	}

	// Score 2: Check for 3+ source files (+2)
	fileCount, detectedLang := countSourceFiles(absPath)
	dir.FileCount = fileCount
	if dir.FileCount >= 3 {
		dir.Score += 2.0
	}

	// Set language if not already set from index file
	if dir.Language == "" || dir.Language == LanguageUnknown {
		dir.Language = detectedLang
	}

	// Score 3: Semantic directory name (+2)
	if SemanticDirectoryNames[strings.ToLower(dirName)] {
		dir.IsSemantic = true
		dir.Score += 2.0
	}

	// Score 4: Consistent language (+1) - if all files are same language
	if dir.FileCount > 0 && detectedLang != LanguageUnknown {
		dir.Score += 1.0
	}

	// Score 5: Top-level position (+1)
	if depth == 1 {
		dir.Score += 1.0
	}

	return dir
}

// hasIndexFile checks if a directory contains an index file and returns the language
func hasIndexFile(absPath string) (bool, string) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return false, ""
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())

		for lang, patterns := range IndexFilePatterns {
			for _, pattern := range patterns {
				if name == pattern {
					return true, lang
				}
			}
		}
	}

	return false, ""
}

// countSourceFiles counts source files in a directory and returns the dominant language
func countSourceFiles(absPath string) (int, string) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return 0, LanguageUnknown
	}

	langCounts := make(map[string]int)
	totalCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".go":
			langCounts[LanguageGo]++
			totalCount++
		case ".ts", ".tsx":
			langCounts[LanguageTypeScript]++
			totalCount++
		case ".js", ".jsx", ".mjs":
			langCounts[LanguageJavaScript]++
			totalCount++
		case ".dart":
			langCounts[LanguageDart]++
			totalCount++
		case ".py":
			langCounts[LanguagePython]++
			totalCount++
		case ".rs":
			langCounts[LanguageRust]++
			totalCount++
		case ".java":
			langCounts[LanguageJava]++
			totalCount++
		case ".kt", ".kts":
			langCounts[LanguageKotlin]++
			totalCount++
		}
	}

	// Find dominant language
	dominantLang := LanguageUnknown
	maxCount := 0
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			dominantLang = lang
		}
	}

	return totalCount, dominantLang
}

// pruneNestedDirectories removes child directories when parent is included,
// unless the child has strong independent signals (index file or semantic name)
func pruneNestedDirectories(directories []*InferredDirectory) []*InferredDirectory {
	if len(directories) == 0 {
		return directories
	}

	// Build a map of included paths
	includedPaths := make(map[string]bool)
	for _, dir := range directories {
		includedPaths[dir.Path] = true
	}

	// Filter out nested directories without strong signals
	result := make([]*InferredDirectory, 0, len(directories))
	for _, dir := range directories {
		// Check if any parent is already included
		parentIncluded := false
		parts := strings.Split(dir.Path, string(os.PathSeparator))
		for i := 1; i < len(parts); i++ {
			parentPath := strings.Join(parts[:i], string(os.PathSeparator))
			if includedPaths[parentPath] {
				parentIncluded = true
				break
			}
		}

		// Keep if: no parent included, OR has strong signals (index file or semantic name with good file count)
		if !parentIncluded || dir.HasIndexFile || (dir.IsSemantic && dir.FileCount >= 3) {
			result = append(result, dir)
		}
	}

	return result
}

// fillIntermediateDirectories adds missing parent directories to complete the hierarchy.
// This ensures visualization tools can draw proper hierarchical trees.
func fillIntermediateDirectories(directories []*InferredDirectory) []*InferredDirectory {
	if len(directories) == 0 {
		return directories
	}

	// Build a set of existing paths
	existingPaths := make(map[string]bool)
	for _, dir := range directories {
		existingPaths[dir.Path] = true
	}

	// Collect all needed intermediate paths
	intermediatePaths := make(map[string]bool)
	for _, dir := range directories {
		parts := strings.Split(dir.Path, string(os.PathSeparator))
		// Add all ancestor paths that don't exist
		for i := 1; i < len(parts); i++ {
			ancestorPath := strings.Join(parts[:i], string(os.PathSeparator))
			if !existingPaths[ancestorPath] && !intermediatePaths[ancestorPath] {
				intermediatePaths[ancestorPath] = true
			}
		}
	}

	// Create intermediate directory entries
	for path := range intermediatePaths {
		depth := strings.Count(path, string(os.PathSeparator)) + 1
		dirName := filepath.Base(path)
		dir := &InferredDirectory{
			Path:           path,
			Depth:          depth,
			FileCount:      0,
			IsIntermediate: true,
			IsSemantic:     SemanticDirectoryNames[strings.ToLower(dirName)],
		}
		directories = append(directories, dir)
	}

	// Sort by path for consistent ordering (parents before children)
	sort.Slice(directories, func(i, j int) bool {
		return directories[i].Path < directories[j].Path
	})

	return directories
}

// CountLinesInFile counts lines in a single file
func CountLinesInFile(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
	}
	return lineCount, scanner.Err()
}

// AggregateDirectoryStats calculates aggregate statistics for an inferred directory
func AggregateDirectoryStats(repoRoot string, dir *InferredDirectory) (loc int, err error) {
	absPath := filepath.Join(repoRoot, dir.Path)
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only count source files
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		isSource := false
		switch ext {
		case ".go", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".dart", ".py", ".rs", ".java", ".kt", ".kts":
			isSource = true
		}

		if isSource {
			filePath := filepath.Join(absPath, entry.Name())
			lines, err := CountLinesInFile(filePath)
			if err == nil {
				loc += lines
			}
		}
	}

	return loc, nil
}
