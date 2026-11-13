package architecture

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/git"
	"github.com/SimplyLiz/CodeMCP/internal/config"
	"github.com/SimplyLiz/CodeMCP/internal/modules"
)

// ArchitectureGenerator generates architecture views of the repository
type ArchitectureGenerator struct {
	repoRoot      string
	config        *config.Config
	importScanner *modules.ImportScanner
	logger        *slog.Logger
	limits        *ArchitectureLimits
	cache         *ArchitectureCache
	gitAdapter    *git.GitAdapter // Optional: for metrics computation
}

// GeneratorOptions contains options for architecture generation
type GeneratorOptions struct {
	Depth               int  // Depth of dependency analysis (default 2)
	IncludeExternalDeps bool // Include external dependencies in graph (default false)
	Refresh             bool // Force refresh, bypass cache
	MaxFilesScanned     int  // Override default max files limit

	// v8.0: Granularity options
	Granularity    Granularity // "module", "directory", "file" (default: "module")
	InferModules   bool        // Infer modules from directory structure when no explicit modules exist (default: true)
	TargetPath     string      // Optional path to focus on (relative to repo root)
	MaxDepth       int         // Max directory depth for directory/file views (default: 4)
	IncludeMetrics bool        // Include aggregate metrics per directory (complexity, churn)
}

// DefaultGeneratorOptions returns the default generator options
func DefaultGeneratorOptions() *GeneratorOptions {
	return &GeneratorOptions{
		Depth:               2,
		IncludeExternalDeps: false,
		Refresh:             false,
		MaxFilesScanned:     0, // Use limit from config
		Granularity:         GranularityModule,
		InferModules:        true,
		MaxDepth:            4,
	}
}

// NewArchitectureGenerator creates a new architecture generator
func NewArchitectureGenerator(
	repoRoot string,
	cfg *config.Config,
	importScanner *modules.ImportScanner,
	logger *slog.Logger,
) *ArchitectureGenerator {
	limits := DefaultLimits()

	// Override limits from config if available
	if cfg.BackendLimits.MaxFilesScanned > 0 {
		limits.MaxFilesScanned = cfg.BackendLimits.MaxFilesScanned
	}
	if cfg.Budget.MaxModules > 0 {
		limits.MaxModules = cfg.Budget.MaxModules
	}

	return &ArchitectureGenerator{
		repoRoot:      repoRoot,
		config:        cfg,
		importScanner: importScanner,
		logger:        logger,
		limits:        limits,
		cache:         NewArchitectureCache(),
	}
}

// SetGitAdapter sets the git adapter for metrics computation (optional)
func (g *ArchitectureGenerator) SetGitAdapter(adapter *git.GitAdapter) {
	g.gitAdapter = adapter
}

// Generate generates the complete architecture view
func (g *ArchitectureGenerator) Generate(ctx context.Context, repoStateId string, opts *GeneratorOptions) (*ArchitectureResponse, error) {
	startTime := time.Now()

	// Use default options if not provided
	if opts == nil {
		opts = DefaultGeneratorOptions()
	}

	// Dispatch based on granularity (v8.0)
	switch opts.Granularity {
	case GranularityFile:
		return g.generateFileLevel(ctx, repoStateId, opts, startTime)
	case GranularityDirectory:
		return g.generateDirectoryLevel(ctx, repoStateId, opts, startTime)
	default:
		// GranularityModule - existing behavior with optional inferred modules
		return g.generateModuleLevel(ctx, repoStateId, opts, startTime)
	}
}

// generateModuleLevel generates the module-level architecture view (existing behavior)
func (g *ArchitectureGenerator) generateModuleLevel(ctx context.Context, repoStateId string, opts *GeneratorOptions, startTime time.Time) (*ArchitectureResponse, error) {
	// Check cache first unless refresh is requested
	if !opts.Refresh {
		if cached, found := g.cache.Get(repoStateId); found {
			g.logger.Debug("Using cached architecture",
				"repoStateId", repoStateId,
				"age", time.Since(cached.ComputedAt).Seconds(),
			)
			return cached.Response, nil
		}
	}

	g.logger.Info("Generating architecture view",
		"repoStateId", repoStateId,
		"includeExternalDeps", opts.IncludeExternalDeps,
		"depth", opts.Depth,
	)

	// Step 1: Detect modules
	detectionResult, err := modules.DetectModules(
		g.repoRoot,
		g.config.Modules.Roots,
		g.config.Modules.Ignore,
		repoStateId,
		g.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("module detection failed: %w", err)
	}

	g.logger.Debug("Detected modules",
		"count", len(detectionResult.Modules),
		"method", detectionResult.DetectionMethod,
	)

	// Check module count limit
	if limitErr := g.limits.checkModuleCount(len(detectionResult.Modules)); limitErr != nil {
		g.logger.Warn("Module count exceeds limit, truncating",
			"detected", len(detectionResult.Modules),
			"limit", g.limits.MaxModules,
		)
		detectionResult.Modules = detectionResult.Modules[:g.limits.MaxModules]
	}

	// Step 2: Aggregate module statistics
	moduleSummaries, err := g.AggregateModules(detectionResult.Modules)
	if err != nil {
		return nil, fmt.Errorf("module aggregation failed: %w", err)
	}

	// Step 3: Scan imports and build dependency graph
	importsByModule, err := g.scanImportsForModules(ctx, detectionResult.Modules)
	if err != nil {
		return nil, fmt.Errorf("import scanning failed: %w", err)
	}

	dependencyGraph, err := g.BuildDependencyGraph(detectionResult.Modules, importsByModule, opts)
	if err != nil {
		return nil, fmt.Errorf("dependency graph building failed: %w", err)
	}

	// Step 4: Detect entrypoints
	entrypoints, err := g.DetectEntrypoints(detectionResult.Modules)
	if err != nil {
		return nil, fmt.Errorf("entrypoint detection failed: %w", err)
	}

	// Build response
	response := &ArchitectureResponse{
		Modules:         moduleSummaries,
		DependencyGraph: dependencyGraph,
		Entrypoints:     entrypoints,
		Granularity:     GranularityModule,
		DetectionMethod: detectionResult.DetectionMethod,
	}

	// Cache the response
	g.cache.Set(repoStateId, response)

	duration := time.Since(startTime)
	g.logger.Info("Architecture generation completed",
		"durationMs", duration.Milliseconds(),
		"modules", len(moduleSummaries),
		"dependencies", len(dependencyGraph),
		"entrypoints", len(entrypoints),
	)

	return response, nil
}

// generateDirectoryLevel generates directory-level architecture view
func (g *ArchitectureGenerator) generateDirectoryLevel(ctx context.Context, repoStateId string, opts *GeneratorOptions, startTime time.Time) (*ArchitectureResponse, error) {
	g.logger.Info("Generating directory-level architecture view",
		"repoStateId", repoStateId,
		"targetPath", opts.TargetPath,
		"maxDepth", opts.MaxDepth,
	)

	// Detect interesting directories using the inference algorithm
	inferOpts := modules.DefaultInferOptions()
	inferOpts.MaxDepth = opts.MaxDepth
	inferOpts.TargetPath = opts.TargetPath
	inferOpts.IgnoreDirs = append(inferOpts.IgnoreDirs, g.config.Modules.Ignore...)
	inferOpts.Logger = g.logger

	directories, err := modules.DetectInferredDirectories(g.repoRoot, inferOpts)
	if err != nil {
		return nil, fmt.Errorf("directory detection failed: %w", err)
	}

	g.logger.Debug("Detected directories",
		"count", len(directories),
	)

	// Apply limits
	maxDirectories := 50 // Hard cap for directory-level view
	if len(directories) > maxDirectories {
		directories = directories[:maxDirectories]
	}

	// Scan all imports from the repository
	allImports, err := g.scanAllImports(ctx, opts.TargetPath)
	if err != nil {
		return nil, fmt.Errorf("import scanning failed: %w", err)
	}

	// Build directory-level edges
	dirEdges := g.buildDirectoryEdges(directories, allImports, opts)

	// Convert InferredDirectory to DirectorySummary
	dirSummaries := make([]DirectorySummary, 0, len(directories))
	for _, dir := range directories {
		// Calculate LOC
		loc, _ := modules.AggregateDirectoryStats(g.repoRoot, dir)

		summary := DirectorySummary{
			Path:           dir.Path,
			FileCount:      dir.FileCount,
			Language:       dir.Language,
			LOC:            loc,
			Role:           inferDirectoryRole(dir.Path),
			HasIndexFile:   dir.HasIndexFile,
			IsIntermediate: dir.IsIntermediate,
		}
		dirSummaries = append(dirSummaries, summary)
	}

	// Compute edge counts
	incomingCounts := make(map[string]int)
	outgoingCounts := make(map[string]int)
	for _, edge := range dirEdges {
		outgoingCounts[edge.From]++
		incomingCounts[edge.To]++
	}
	for i := range dirSummaries {
		dirSummaries[i].IncomingEdges = incomingCounts[dirSummaries[i].Path]
		dirSummaries[i].OutgoingEdges = outgoingCounts[dirSummaries[i].Path]
	}

	// Compute aggregate metrics if requested
	if opts.IncludeMetrics {
		metricsCalc := NewMetricsCalculator(g.repoRoot, g.gitAdapter)
		if err := metricsCalc.ComputeDirectoryMetrics(ctx, dirSummaries); err != nil {
			g.logger.Warn("Failed to compute directory metrics",
				"error", err.Error(),
			)
			// Continue without metrics - graceful degradation
		}
	}

	response := &ArchitectureResponse{
		Directories:           dirSummaries,
		DirectoryDependencies: dirEdges,
		Granularity:           GranularityDirectory,
		DetectionMethod:       "inferred",
	}

	duration := time.Since(startTime)
	g.logger.Info("Directory-level architecture generation completed",
		"durationMs", duration.Milliseconds(),
		"directories", len(dirSummaries),
		"dependencies", len(dirEdges),
	)

	return response, nil
}

// generateFileLevel generates file-level architecture view
func (g *ArchitectureGenerator) generateFileLevel(ctx context.Context, repoStateId string, opts *GeneratorOptions, startTime time.Time) (*ArchitectureResponse, error) {
	g.logger.Info("Generating file-level architecture view",
		"repoStateId", repoStateId,
		"targetPath", opts.TargetPath,
	)

	// Scan all imports from the repository
	allImports, err := g.scanAllImports(ctx, opts.TargetPath)
	if err != nil {
		return nil, fmt.Errorf("import scanning failed: %w", err)
	}

	// Build file summaries and edges
	fileSummaries, fileEdges := g.buildFileLevelData(allImports, opts)

	// Apply limits
	maxFiles := 200 // Hard cap for file-level view
	maxEdges := 500
	if len(fileSummaries) > maxFiles {
		fileSummaries = fileSummaries[:maxFiles]
	}
	if len(fileEdges) > maxEdges {
		fileEdges = fileEdges[:maxEdges]
	}

	response := &ArchitectureResponse{
		Files:            fileSummaries,
		FileDependencies: fileEdges,
		Granularity:      GranularityFile,
		DetectionMethod:  "import-scan",
	}

	duration := time.Since(startTime)
	g.logger.Info("File-level architecture generation completed",
		"durationMs", duration.Milliseconds(),
		"files", len(fileSummaries),
		"dependencies", len(fileEdges),
	)

	return response, nil
}

// scanAllImports scans all imports from the repository or a target path
func (g *ArchitectureGenerator) scanAllImports(_ context.Context, targetPath string) ([]*modules.ImportEdge, error) {
	scanPath := g.repoRoot
	if targetPath != "" {
		scanPath = filepath.Join(g.repoRoot, targetPath)
	}

	imports, err := g.importScanner.ScanDirectory(
		scanPath,
		g.repoRoot,
		g.config.Modules.Ignore,
	)
	if err != nil {
		return nil, err
	}

	return imports, nil
}

// edgeAccumulator tracks import counts and symbols for a directory edge
type edgeAccumulator struct {
	edge       DirectoryDependencyEdge
	symbolsSet map[string]bool // Track unique symbols
}

// buildDirectoryEdges aggregates file-level imports into directory-level edges
func (g *ArchitectureGenerator) buildDirectoryEdges(directories []*modules.InferredDirectory, imports []*modules.ImportEdge, opts *GeneratorOptions) []DirectoryDependencyEdge {
	// Build a set of known directory paths for fast lookup
	dirSet := make(map[string]bool)
	for _, dir := range directories {
		dirSet[dir.Path] = true
	}

	// Get Go module prefix to resolve Go package paths
	goModulePrefix := g.getGoModulePrefix()

	// Aggregate edges by (fromDir, toDir)
	edgeMap := make(map[string]*edgeAccumulator)
	// Python imports by bare module name, including those through sys.path, are
	// resolved to their file; the path matching below only knows directories.
	pyResolver := modules.NewImportClassifier(&modules.ModuleContext{RepoRoot: g.repoRoot, Language: modules.LanguagePython})

	for _, imp := range imports {
		fromDir := extractDirectoryPath(imp.From)

		// Resolve the import target to a directory path
		toDir := g.resolveImportToDirectory(imp, goModulePrefix, dirSet)
		if toDir == "" && strings.HasSuffix(imp.From, ".py") {
			if file := pyResolver.ResolveImport(imp.To, imp.From); file != "" && dirSet[extractDirectoryPath(file)] {
				toDir = extractDirectoryPath(file)
			}
		}
		if toDir == "" {
			continue // Could not resolve
		}

		// Skip self-references
		if fromDir == toDir {
			continue
		}

		// Skip if external and not including external deps
		if imp.Kind == modules.ExternalDependency && !opts.IncludeExternalDeps {
			continue
		}

		key := fromDir + ":" + toDir
		if acc, exists := edgeMap[key]; exists {
			acc.edge.ImportCount++
			// Track symbol (avoid duplicates)
			symbol := extractSymbolFromImport(imp)
			if symbol != "" && !acc.symbolsSet[symbol] {
				acc.symbolsSet[symbol] = true
			}
		} else {
			acc := &edgeAccumulator{
				edge: DirectoryDependencyEdge{
					From:        fromDir,
					To:          toDir,
					Kind:        imp.Kind,
					ImportCount: 1,
				},
				symbolsSet: make(map[string]bool),
			}
			symbol := extractSymbolFromImport(imp)
			if symbol != "" {
				acc.symbolsSet[symbol] = true
			}
			edgeMap[key] = acc
		}
	}

	// Convert map to slice, populating symbols array
	edges := make([]DirectoryDependencyEdge, 0, len(edgeMap))
	for _, acc := range edgeMap {
		// Build symbols slice from set (limit to 10 for readability)
		if len(acc.symbolsSet) > 0 {
			acc.edge.Symbols = make([]string, 0, min(len(acc.symbolsSet), 10))
			for symbol := range acc.symbolsSet {
				if len(acc.edge.Symbols) >= 10 {
					break
				}
				acc.edge.Symbols = append(acc.edge.Symbols, symbol)
			}
			sort.Strings(acc.edge.Symbols)
		}
		// Set deprecated Strength for backwards compatibility
		acc.edge.Strength = acc.edge.ImportCount
		edges = append(edges, acc.edge)
	}

	// Sort by importCount descending
	sortDirectoryEdges(edges)

	// Apply limit
	maxEdges := 200
	if len(edges) > maxEdges {
		edges = edges[:maxEdges]
	}

	return edges
}

// extractSymbolFromImport extracts a symbol/package name from an import
func extractSymbolFromImport(imp *modules.ImportEdge) string {
	// For Go: extract package name from path (last segment)
	// e.g., "github.com/SimplyLiz/CodeMCP/internal/query" -> "query"
	path := imp.To
	if path == "" {
		return ""
	}

	// Get last segment of the path
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash >= 0 && lastSlash < len(path)-1 {
		return path[lastSlash+1:]
	}

	// For JS/TS: RawImport might contain symbol names
	// e.g., "{ foo, bar }" or "Foo"
	if imp.RawImport != "" && strings.Contains(imp.RawImport, "{") {
		// Extract names from "{ foo, bar, baz }"
		raw := strings.Trim(imp.RawImport, "{} \t")
		if raw != "" {
			parts := strings.Split(raw, ",")
			if len(parts) > 0 {
				// Return first symbol (cleaned)
				return strings.TrimSpace(parts[0])
			}
		}
	}

	return path
}

// buildFileLevelData builds file summaries and edges from imports
func (g *ArchitectureGenerator) buildFileLevelData(imports []*modules.ImportEdge, opts *GeneratorOptions) ([]FileSummary, []FileDependencyEdge) {
	// Track unique files and their statistics
	fileStats := make(map[string]*FileSummary)

	// Build edges
	edges := make([]FileDependencyEdge, 0, len(imports))

	for _, imp := range imports {
		// Skip external if not including
		if imp.Kind == modules.ExternalDependency && !opts.IncludeExternalDeps {
			continue
		}

		// Track source file
		if _, exists := fileStats[imp.From]; !exists {
			fileStats[imp.From] = &FileSummary{
				Path:     imp.From,
				Language: detectLanguageFromPath(imp.From),
			}
		}
		fileStats[imp.From].OutgoingEdges++

		// Track target file (if local)
		if imp.Kind == modules.LocalFile || imp.Kind == modules.LocalModule {
			if _, exists := fileStats[imp.To]; !exists {
				fileStats[imp.To] = &FileSummary{
					Path:     imp.To,
					Language: detectLanguageFromPath(imp.To),
				}
			}
			fileStats[imp.To].IncomingEdges++
		}

		// Create edge
		edge := FileDependencyEdge{
			From:     imp.From,
			To:       imp.To,
			Kind:     imp.Kind,
			Line:     imp.Line,
			Resolved: imp.Kind == modules.LocalFile || imp.Kind == modules.LocalModule,
		}
		edges = append(edges, edge)
	}

	// Convert map to slice and sort by edge count
	files := make([]FileSummary, 0, len(fileStats))
	for _, f := range fileStats {
		files = append(files, *f)
	}
	sortFileSummaries(files)

	return files, edges
}

// extractDirectoryPath extracts the directory path from a file path
func extractDirectoryPath(filePath string) string {
	lastSlash := -1
	for i := len(filePath) - 1; i >= 0; i-- {
		if filePath[i] == '/' {
			lastSlash = i
			break
		}
	}
	if lastSlash <= 0 {
		return "."
	}
	return filePath[:lastSlash]
}

// getGoModulePrefix reads go.mod and returns the module path prefix (e.g., "ckb" or "github.com/user/repo")
func (g *ArchitectureGenerator) getGoModulePrefix() string {
	goModPath := filepath.Join(g.repoRoot, "go.mod")
	file, err := os.Open(goModPath)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				return parts[1] // e.g., "ckb" or "github.com/user/repo"
			}
		}
	}
	return ""
}

// resolveImportToDirectory resolves an import path to a known directory path
func (g *ArchitectureGenerator) resolveImportToDirectory(imp *modules.ImportEdge, goModulePrefix string, dirSet map[string]bool) string {
	importPath := imp.To

	// For external dependencies, return as-is (they won't match dirSet anyway)
	if imp.Kind == modules.ExternalDependency {
		return extractDirectoryPath(importPath)
	}

	// For relative imports (./foo, ../bar), resolve relative to the importing file
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		fromDir := extractDirectoryPath(imp.From)
		resolved := filepath.Clean(filepath.Join(fromDir, importPath))
		// Check if it matches a known directory
		if dirSet[resolved] {
			return resolved
		}
		// Try parent directory (import might be to a file, not directory)
		parentDir := extractDirectoryPath(resolved)
		if dirSet[parentDir] {
			return parentDir
		}
		return resolved
	}

	// For Go package imports (e.g., "github.com/SimplyLiz/CodeMCP/internal/query"), strip the module prefix
	if goModulePrefix != "" && strings.HasPrefix(importPath, goModulePrefix+"/") {
		// Strip module prefix: "github.com/SimplyLiz/CodeMCP/internal/query" -> "internal/query"
		repoRelative := strings.TrimPrefix(importPath, goModulePrefix+"/")
		if dirSet[repoRelative] {
			return repoRelative
		}
		// The import might be to a package, try the path as-is
		return repoRelative
	}

	// For other imports, try to match against known directories
	// Check if any directory is a suffix match
	for dir := range dirSet {
		if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
			return dir
		}
	}

	// Fallback: extract directory from import path
	toDir := extractDirectoryPath(importPath)
	if dirSet[toDir] {
		return toDir
	}

	return ""
}

// detectLanguageFromPath detects language from file extension
func detectLanguageFromPath(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			ext := path[i:]
			switch ext {
			case ".go":
				return modules.LanguageGo
			case ".ts", ".tsx":
				return modules.LanguageTypeScript
			case ".js", ".jsx", ".mjs":
				return modules.LanguageJavaScript
			case ".py":
				return modules.LanguagePython
			case ".rs":
				return modules.LanguageRust
			case ".dart":
				return modules.LanguageDart
			case ".java":
				return modules.LanguageJava
			case ".kt", ".kts":
				return modules.LanguageKotlin
			}
			break
		}
	}
	return modules.LanguageUnknown
}

// sortDirectoryEdges sorts edges by importCount descending
func sortDirectoryEdges(edges []DirectoryDependencyEdge) {
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].ImportCount > edges[j].ImportCount
	})
}

// sortFileSummaries sorts files by total edge count descending
func sortFileSummaries(files []FileSummary) {
	sort.Slice(files, func(i, j int) bool {
		totalI := files[i].IncomingEdges + files[i].OutgoingEdges
		totalJ := files[j].IncomingEdges + files[j].OutgoingEdges
		return totalI > totalJ
	})
}

// scanImportsForModules scans all imports for all modules
func (g *ArchitectureGenerator) scanImportsForModules(ctx context.Context, mods []*modules.Module) (map[string][]*modules.ImportEdge, error) {
	result := make(map[string][]*modules.ImportEdge)

	for _, mod := range mods {
		// Check context for cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Scan imports for this module
		imports, err := g.importScanner.ScanDirectory(
			filepath.Join(g.repoRoot, mod.RootPath),
			g.repoRoot,
			g.config.Modules.Ignore,
		)
		if err != nil {
			g.logger.Warn("Failed to scan imports for module",
				"moduleId", mod.ID,
				"error", err.Error(),
			)
			continue
		}

		result[mod.ID] = imports
	}

	return result, nil
}

// GetCached retrieves a cached architecture response if available
func (g *ArchitectureGenerator) GetCached(repoStateId string) (*CachedArchitecture, bool) {
	return g.cache.Get(repoStateId)
}

// InvalidateCache removes cached architecture for a specific repo state
func (g *ArchitectureGenerator) InvalidateCache(repoStateId string) {
	g.cache.Invalidate(repoStateId)
}

// ClearCache clears all cached architectures
func (g *ArchitectureGenerator) ClearCache() {
	g.cache.Clear()
}

// inferDirectoryRole determines the semantic role of a directory based on its name
func inferDirectoryRole(path string) string {
	name := strings.ToLower(filepath.Base(path))
	pathLower := strings.ToLower(path)

	// Test directories (check first as test dirs can be anywhere)
	if containsAny(name, "test", "__test", "spec") || strings.Contains(pathLower, "/test/") || strings.Contains(pathLower, "/tests/") {
		return "test"
	}

	// Entrypoint directories (check early - cmd/* is always entrypoint in Go)
	if containsAny(name, "cmd", "bin", "main", "app") || strings.HasPrefix(pathLower, "cmd/") || strings.Contains(pathLower, "/cmd/") {
		return "entrypoint"
	}

	// API/Backend directories
	if containsAny(name, "api", "server", "backend", "handler", "controller", "route", "endpoint") {
		return "api"
	}

	// UI/Frontend directories
	if containsAny(name, "ui", "web", "frontend", "client", "component", "view", "page", "screen") {
		return "ui"
	}

	// Data layer directories
	if containsAny(name, "data", "db", "database", "model", "schema", "store", "repository", "entity") {
		return "data"
	}

	// Utility directories
	if containsAny(name, "util", "utils", "lib", "common", "shared", "helper", "tool") {
		return "util"
	}

	// Configuration directories
	if containsAny(name, "config", "conf", "setting") {
		return "config"
	}

	// Internal/core by convention
	if strings.Contains(pathLower, "/internal/") || strings.Contains(pathLower, "/pkg/") || strings.Contains(pathLower, "/src/") {
		return "core"
	}

	return "core"
}

// containsAny checks if s contains any of the substrings
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
