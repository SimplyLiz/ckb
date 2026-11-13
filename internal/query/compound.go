// Package query provides compound tools for v8.0 that aggregate multiple granular queries.
// These reduce AI tool calls by 60-70% for common exploration workflows.
package query

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/cartographer"
	"github.com/SimplyLiz/CodeMCP/internal/complexity"
	"github.com/SimplyLiz/CodeMCP/internal/coupling"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
	"github.com/SimplyLiz/CodeMCP/internal/modules"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// ExploreDepth controls the thoroughness of exploration.
type ExploreDepth string

const (
	ExploreShallow  ExploreDepth = "shallow"
	ExploreStandard ExploreDepth = "standard"
	ExploreDeep     ExploreDepth = "deep"
)

// ExploreFocus controls which aspects to emphasize.
type ExploreFocus string

const (
	FocusStructure    ExploreFocus = "structure"
	FocusDependencies ExploreFocus = "dependencies"
	FocusChanges      ExploreFocus = "changes"
)

// ExploreOptions controls explore behavior.
type ExploreOptions struct {
	Target string       // file, directory, or module path
	Depth  ExploreDepth // shallow, standard, deep
	Focus  ExploreFocus // structure, dependencies, changes
}

// ExploreResponse provides comprehensive area exploration.
type ExploreResponse struct {
	AINavigationMeta
	Overview      *ExploreOverview     `json:"overview"`
	KeySymbols    []ExploreSymbol      `json:"keySymbols"`
	Dependencies  *ExploreDependencies `json:"dependencies,omitempty"`
	RecentChanges []ExploreChange      `json:"recentChanges,omitempty"`
	Hotspots      []ExploreHotspot     `json:"hotspots,omitempty"`
	Suggestions   []string             `json:"suggestions,omitempty"`
	Health        *ExploreHealth       `json:"health"`
}

// ExploreOverview provides high-level information about the target.
type ExploreOverview struct {
	TargetType     string         `json:"targetType"` // file, directory, module
	Path           string         `json:"path"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	FileCount      int            `json:"fileCount,omitempty"`
	SymbolCount    int            `json:"symbolCount,omitempty"`
	LineCount      int            `json:"lineCount,omitempty"`
	Language       string         `json:"language,omitempty"`
	Languages      map[string]int `json:"languages,omitempty"` // file count per language; populated by Cartographer
	Role           string         `json:"role,omitempty"`      // core, glue, test, config
	Responsibility string         `json:"responsibility,omitempty"`
}

// ExploreSymbol represents a key symbol in the explored area.
type ExploreSymbol struct {
	StableId   string  `json:"stableId"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	File       string  `json:"file,omitempty"`
	Line       int     `json:"line,omitempty"`
	EndLine    int     `json:"endLine,omitempty"`
	Lines      int     `json:"lines,omitempty"`      // body line count (endLine - line + 1)
	Cyclomatic int     `json:"cyclomatic,omitempty"` // cyclomatic complexity
	Cognitive  int     `json:"cognitive,omitempty"`  // cognitive complexity
	Visibility string  `json:"visibility"`
	Importance float64 `json:"importance"`       // ranking score
	Reason     string  `json:"reason,omitempty"` // why it's important
}

// ExploreDependencies describes imports and exports.
type ExploreDependencies struct {
	Imports      []ExploreDependency `json:"imports,omitempty"`
	Exports      []ExploreDependency `json:"exports,omitempty"`
	InternalDeps []string            `json:"internalDeps,omitempty"`
	ExternalDeps []string            `json:"externalDeps,omitempty"`
	Truncated    bool                `json:"truncated,omitempty"` // True if limits were applied
}

// ExploreDependency represents a single dependency.
type ExploreDependency struct {
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Kind     string `json:"kind,omitempty"` // package, module, file
	Internal bool   `json:"internal"`
}

// ExploreChange represents a recent change in the area.
type ExploreChange struct {
	CommitHash   string `json:"commitHash"`
	Message      string `json:"message"`
	Author       string `json:"author"`
	Date         string `json:"date"`
	FilesChanged int    `json:"filesChanged"`
}

// ExploreHotspot represents a volatile area.
type ExploreHotspot struct {
	File      string  `json:"file"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"` // high-churn, high-coupling, complex
	ChurnRate float64 `json:"churnRate,omitempty"`
}

// ExploreHealth summarizes backend status for this query.
type ExploreHealth struct {
	ScipAvailable bool     `json:"scipAvailable"`
	GitAvailable  bool     `json:"gitAvailable"`
	OverallStatus string   `json:"overallStatus"` // healthy, degraded, limited
	Warnings      []string `json:"warnings,omitempty"`
}

// Explore provides comprehensive area exploration.
// Replaces: explainFile → searchSymbols → getCallGraph → getHotspots
func (e *Engine) Explore(ctx context.Context, opts ExploreOptions) (*ExploreResponse, error) {
	startTime := time.Now()

	// Set defaults
	if opts.Depth == "" {
		opts.Depth = ExploreStandard
	}
	if opts.Focus == "" {
		opts.Focus = FocusStructure
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Determine target type
	targetType, absPath, err := e.resolveExploreTarget(opts.Target)
	if err != nil {
		return nil, err
	}

	// Collect results from sub-queries in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex

	var overview *ExploreOverview
	var keySymbols []ExploreSymbol
	var dependencies *ExploreDependencies
	var recentChanges []ExploreChange
	var hotspots []ExploreHotspot
	var warnings []string

	// Health check
	health := &ExploreHealth{
		ScipAvailable: e.scipAdapter != nil && e.scipAdapter.IsAvailable(),
		GitAvailable:  e.gitAdapter != nil && e.gitAdapter.IsAvailable(),
	}
	if health.ScipAvailable && health.GitAvailable {
		health.OverallStatus = "healthy"
	} else if health.ScipAvailable || health.GitAvailable {
		health.OverallStatus = "degraded"
	} else {
		health.OverallStatus = "limited"
	}

	// 1. Get overview based on target type
	wg.Add(1)
	go func() {
		defer wg.Done()
		ov, err := e.buildExploreOverview(ctx, targetType, absPath, opts.Target)
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("overview: %s", err.Error()))
			mu.Unlock()
			return
		}
		mu.Lock()
		overview = ov
		mu.Unlock()
	}()

	// 2. Get key symbols
	wg.Add(1)
	go func() {
		defer wg.Done()
		limit := 10
		if opts.Depth == ExploreDeep {
			limit = 20
		} else if opts.Depth == ExploreShallow {
			limit = 5
		}
		syms, err := e.getExploreSymbols(ctx, targetType, absPath, opts.Target, limit)
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("symbols: %s", err.Error()))
			mu.Unlock()
			return
		}
		mu.Lock()
		keySymbols = syms
		mu.Unlock()
	}()

	// 3. Get dependencies (if standard or deep, or focus is dependencies)
	if opts.Depth != ExploreShallow || opts.Focus == FocusDependencies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deps := e.getExploreDependencies(ctx, targetType, absPath, opts.Target)
			mu.Lock()
			dependencies = deps
			mu.Unlock()
		}()
	}

	// 4. Get recent changes (if git available and not shallow, or focus is changes)
	if health.GitAvailable && (opts.Depth != ExploreShallow || opts.Focus == FocusChanges) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit := 5
			if opts.Focus == FocusChanges {
				limit = 10
			}
			changes, err := e.getExploreChanges(ctx, absPath, opts.Target, limit)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("changes: %s", err.Error()))
				mu.Unlock()
				return
			}
			mu.Lock()
			recentChanges = changes
			mu.Unlock()
		}()
	}

	// 5. Get hotspots (if git available and deep or focus is changes)
	if health.GitAvailable && (opts.Depth == ExploreDeep || opts.Focus == FocusChanges) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hs, err := e.getExploreHotspots(ctx, absPath, opts.Target, 5)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("hotspots: %s", err.Error()))
				mu.Unlock()
				return
			}
			mu.Lock()
			hotspots = hs
			mu.Unlock()
		}()
	}

	// Wait for all queries
	wg.Wait()

	// Generate suggestions based on results
	suggestions := e.generateExploreSuggestions(keySymbols, hotspots, opts)

	// Build provenance
	var backendContribs []BackendContribution
	if health.ScipAvailable {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip",
			Available: true,
			Used:      true,
		})
	}
	if health.GitAvailable {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "git",
			Available: true,
			Used:      true,
		})
	}

	completeness := CompletenessInfo{
		Score:  0.9,
		Reason: "compound-explore",
	}
	if !health.ScipAvailable {
		completeness.Score = 0.5
		completeness.Reason = "limited-scip-unavailable"
	}

	// Add degradation warnings
	degradationWarnings := e.GetDegradationWarnings()
	for _, dw := range degradationWarnings {
		warnings = append(warnings, dw.Message)
	}

	health.Warnings = warnings

	return &ExploreResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "explore",
			Resolved:      &ResolvedTarget{SymbolId: opts.Target, ResolvedFrom: "path", Confidence: 1.0},
			Provenance:    e.buildProvenance(repoState, "head", startTime, backendContribs, completeness),
		},
		Overview:      overview,
		KeySymbols:    keySymbols,
		Dependencies:  dependencies,
		RecentChanges: recentChanges,
		Hotspots:      hotspots,
		Suggestions:   suggestions,
		Health:        health,
	}, nil
}

// resolveExploreTarget determines the target type and absolute path.
func (e *Engine) resolveExploreTarget(target string) (string, string, error) {
	// Normalize path
	absPath := target
	if !filepath.IsAbs(target) {
		absPath = filepath.Join(e.repoRoot, target)
	}
	absPath = filepath.Clean(absPath)

	// Security: verify path is within repo root
	repoRootClean := filepath.Clean(e.repoRoot)
	if !strings.HasPrefix(absPath, repoRootClean+string(filepath.Separator)) && absPath != repoRootClean {
		return "", "", errors.NewCkbError(errors.InvalidParameter, fmt.Sprintf("path outside repository: %s", target), nil, nil, nil)
	}

	// Check if path exists
	info, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return "", "", errors.NewCkbError(errors.ResourceNotFound, fmt.Sprintf("path not found: %s", target), nil, nil, nil)
	}
	if err != nil {
		return "", "", e.wrapError(err, errors.InternalError)
	}

	if info.IsDir() {
		return "directory", absPath, nil
	}
	return "file", absPath, nil
}

// buildExploreOverview creates the overview for the target.
func (e *Engine) buildExploreOverview(ctx context.Context, targetType, absPath, relTarget string) (*ExploreOverview, error) {
	overview := &ExploreOverview{
		TargetType: targetType,
		Path:       relTarget,
		Name:       filepath.Base(relTarget),
	}

	if targetType == "file" {
		// Single file overview
		overview.Language = detectLanguage(relTarget)
		overview.Role = classifyFileRole(relTarget)
		overview.LineCount = countFileLines(absPath)
		overview.SymbolCount = 0 // Will be updated by symbol search
	} else {
		// Directory overview.
		// Try Cartographer first — it has a pre-built, ignore-aware file list with
		// language metadata. Fall back to an OS walk when it's not available.
		cartographerUsed := false
		if cartographer.Available() {
			if graph, cerr := cartographer.MapProject(e.repoRoot); cerr == nil {
				cartographerUsed = true
				langs := make(map[string]int)
				fileCount := 0
				prefix := relTarget + "/"
				for _, node := range graph.Nodes {
					if strings.HasPrefix(node.Path, prefix) || node.Path == relTarget {
						fileCount++
						if node.Language != "" {
							langs[node.Language]++
						}
					}
				}
				overview.FileCount = fileCount
				if len(langs) > 0 {
					overview.Languages = langs
				}
			}
		}
		if !cartographerUsed {
			fileCount := 0
			//nolint:errcheck // intentionally ignore walk errors to count accessible files
			_ = filepath.Walk(absPath, func(path string, info os.FileInfo, walkErr error) error {
				if walkErr != nil {
					return nil //nolint:nilerr // skip inaccessible files, continue walk
				}
				if info.IsDir() {
					if skipExploreDirectory(info.Name()) {
						return filepath.SkipDir
					}
					return nil
				}
				fileCount++
				return nil
			})
			overview.FileCount = fileCount
		}

		// Get module overview if available
		modResp, err := e.GetModuleOverview(ctx, ModuleOverviewOptions{Path: relTarget})
		if err == nil && modResp != nil {
			overview.Responsibility = modResp.Module.Responsibility
			overview.SymbolCount = modResp.Size.SymbolCount
		}
	}

	return overview, nil
}

// getExploreSymbols retrieves key symbols for the target.
func (e *Engine) getExploreSymbols(ctx context.Context, targetType, absPath, relTarget string, limit int) ([]ExploreSymbol, error) {
	if e.scipAdapter == nil || !e.scipAdapter.IsAvailable() {
		return nil, nil
	}

	// Search for symbols in the target scope
	searchResp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: "",
		Scope: relTarget,
		Limit: limit * 2, // Request more for ranking
	})
	if err != nil {
		return nil, err
	}

	// Supplement with tree-sitter functions when SCIP/FTS returns none.
	// FTS returns only type/field definitions for empty-query file-scoped
	// searches; tree-sitter reliably extracts function declarations.
	hasFunctions := false
	for _, sym := range searchResp.Symbols {
		if sym.Kind == "function" || sym.Kind == "method" {
			hasFunctions = true
			break
		}
	}
	if !hasFunctions && e.treesitterExtractor != nil {
		var tsSyms []SearchResultItem
		if targetType == "file" {
			// ExtractFile for single-file targets (searchWithTreesitter uses
			// ExtractDirectory which fails on file paths)
			e.tsMu.Lock()
			rawSyms, tsErr := e.treesitterExtractor.ExtractFile(ctx, absPath)
			e.tsMu.Unlock()
			if tsErr == nil {
				relFile, _ := filepath.Rel(e.repoRoot, absPath)
				for _, sym := range rawSyms {
					if sym.Kind != "function" && sym.Kind != "method" {
						continue
					}
					tsSyms = append(tsSyms, SearchResultItem{
						Name: sym.Name,
						Kind: sym.Kind,
						Location: &LocationInfo{
							FileId:    relFile,
							StartLine: sym.Line,
							EndLine:   sym.EndLine,
						},
						Visibility: &VisibilityInfo{
							Visibility: inferVisibility(sym.Name, sym.Kind, relFile),
							Confidence: 0.5,
							Source:     "treesitter",
						},
					})
				}
			}
		} else {
			tsSyms, _ = e.searchWithTreesitter(ctx, SearchSymbolsOptions{
				Query: "",
				Scope: relTarget,
				Kinds: []string{"function", "method"},
				Limit: limit * 2,
			})
		}
		searchResp.Symbols = append(searchResp.Symbols, tsSyms...)
	}

	// Convert and rank symbols
	symbols := make([]ExploreSymbol, 0, len(searchResp.Symbols))
	for _, sym := range searchResp.Symbols {
		importance := calculateSymbolImportance(sym)
		reason := inferImportanceReason(sym, importance)

		file := ""
		line := 0
		endLine := 0
		if sym.Location != nil {
			file = sym.Location.FileId
			line = sym.Location.StartLine
			endLine = sym.Location.EndLine
		}

		visibility := "internal"
		if sym.Visibility != nil {
			visibility = sym.Visibility.Visibility
		}

		lines := 0
		if endLine > 0 && line > 0 && endLine >= line {
			lines = endLine - line + 1
		}

		// A test's fixture class isn't what the code under it is about, unless
		// the tests are what's being explored.
		if file != "" && isTestFilePath(file) && !isTestFilePath(relTarget+"/") {
			importance /= 2
			reason = "test " + reason
		}

		symbols = append(symbols, ExploreSymbol{
			StableId:   sym.StableId,
			Name:       sym.Name,
			Kind:       sym.Kind,
			File:       file,
			Line:       line,
			EndLine:    endLine,
			Lines:      lines,
			Visibility: visibility,
			Importance: importance,
			Reason:     reason,
		})
	}

	// Sort by importance
	// Ties (tree-sitter functions carry no ranking, so they all score alike) go to
	// the bigger body: a 40-line run() says more about a directory than a getter.
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].Importance != symbols[j].Importance {
			return symbols[i].Importance > symbols[j].Importance
		}
		if symbols[i].Lines != symbols[j].Lines {
			return symbols[i].Lines > symbols[j].Lines
		}
		return symbols[i].File+symbols[i].Name < symbols[j].File+symbols[j].Name
	})

	// Apply limit
	if len(symbols) > limit {
		symbols = symbols[:limit]
	}

	// Enrich with per-symbol complexity from tree-sitter
	e.enrichSymbolComplexity(ctx, symbols)

	return symbols, nil
}

// enrichSymbolComplexity adds cyclomatic/cognitive complexity to ExploreSymbols
// by running tree-sitter analysis on their source files.
func (e *Engine) enrichSymbolComplexity(ctx context.Context, symbols []ExploreSymbol) {
	if e.complexityAnalyzer == nil {
		return
	}

	// Group symbols by file
	byFile := make(map[string][]int) // file → indices
	for i, s := range symbols {
		if s.File != "" && s.Line > 0 {
			byFile[s.File] = append(byFile[s.File], i)
		}
	}

	for file, indices := range byFile {
		absPath := filepath.Join(e.repoRoot, file)
		fc, err := e.complexityAnalyzer.GetFileComplexityFull(ctx, absPath)
		if err != nil || fc == nil {
			continue
		}

		// Build lookup by (name, startLine)
		type key struct {
			name string
			line int
		}
		cxMap := make(map[key]complexity.ComplexityResult)
		for _, fn := range fc.Functions {
			cxMap[key{fn.Name, fn.StartLine}] = fn
		}

		for _, idx := range indices {
			s := &symbols[idx]
			// Strip Container# prefix for matching
			matchName := s.Name
			if hashIdx := strings.LastIndex(matchName, "#"); hashIdx >= 0 {
				matchName = matchName[hashIdx+1:]
			}
			if cr, ok := cxMap[key{matchName, s.Line}]; ok {
				s.Cyclomatic = cr.Cyclomatic
				s.Cognitive = cr.Cognitive
			}
		}
	}
}

// calculateSymbolImportance computes importance score for ranking.
// Prioritizes functions/methods and top-level types over struct fields,
// since consumers use keySymbols for understanding behavior (SRP, complexity)
// not data shape (which they can get from getSymbol).
func calculateSymbolImportance(sym SearchResultItem) float64 {
	score := 0.0

	// Struct fields (Container#Field) are implementation details, not key symbols.
	// SCIP labels them as kind=class, but they should rank below functions.
	isStructField := strings.Contains(sym.Name, "#")

	// Visibility weight
	if sym.Visibility != nil {
		switch sym.Visibility.Visibility {
		case "public":
			score += 40
		case "internal":
			score += 20
		case "private":
			score += 10
		}
	}

	// Kind weight — functions/methods rank highest for behavioral analysis
	if isStructField {
		score += 5 // Minimal: fields are discoverable via getSymbol on the type
	} else {
		switch sym.Kind {
		case "function", "method":
			score += 35
		case "class", "interface", "struct", "type":
			score += 25
		case "constant", "variable":
			score += 15
		}
	}

	// Add existing ranking score if available (SCIP results have this,
	// tree-sitter results don't — functions from tree-sitter get a flat
	// bonus to compensate, keeping them competitive with SCIP types).
	if sym.Ranking != nil {
		score += sym.Ranking.Score * 0.2
	} else if sym.Kind == "function" || sym.Kind == "method" {
		score += 15 // Compensate for missing ranking data
	}

	return score
}

// inferImportanceReason explains why a symbol is important.
func inferImportanceReason(sym SearchResultItem, importance float64) string {
	isStructField := strings.Contains(sym.Name, "#")
	if isStructField {
		return "field"
	}
	switch sym.Kind {
	case "function", "method":
		if sym.Visibility != nil && sym.Visibility.Visibility == "public" {
			return "exported function"
		}
		return "function"
	case "class", "interface", "struct", "type":
		if sym.Visibility != nil && sym.Visibility.Visibility == "public" {
			return "exported type"
		}
		return "key type"
	}
	if sym.Visibility != nil && sym.Visibility.Visibility == "public" {
		return "exported"
	}
	return ""
}

// Budget limits for explore dependencies
const (
	maxExploreFiles   = 100 // Maximum files to scan for imports
	maxExploreImports = 100 // Maximum unique imports to collect
)

// skipDirectories returns true for directories that should be skipped during exploration
func skipExploreDirectory(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", ".git", ".next", "__pycache__", ".cache":
		return true
	}
	return false
}

// getExploreDependencies extracts import/export information.
func (e *Engine) getExploreDependencies(ctx context.Context, targetType, absPath, relTarget string) *ExploreDependencies {
	deps := &ExploreDependencies{}

	internal := e.exploreImportClassifier()
	if targetType == "file" {
		// For single file, parse imports
		imports := parseFileImports(absPath)
		for _, imp := range imports {
			isInternal := internal(imp, absPath)
			deps.Imports = append(deps.Imports, ExploreDependency{
				Name:     imp,
				Internal: isInternal,
			})
			if isInternal {
				deps.InternalDeps = append(deps.InternalDeps, imp)
			} else {
				deps.ExternalDeps = append(deps.ExternalDeps, imp)
			}
		}
	} else {
		// For directory, aggregate imports from files with limits
		importSet := make(map[string]string) // Import -> the first file naming it
		fileCount := 0
		truncated := false

		//nolint:errcheck // intentionally ignore walk errors to collect imports from accessible files
		_ = filepath.Walk(absPath, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil //nolint:nilerr // skip inaccessible files, continue walk
			}
			if info.IsDir() {
				// Skip common large/generated directories
				if skipExploreDirectory(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}

			// Limit files scanned
			if fileCount >= maxExploreFiles {
				truncated = true
				return filepath.SkipAll
			}

			// Limit imports collected
			if len(importSet) >= maxExploreImports {
				truncated = true
				return filepath.SkipAll
			}

			fileCount++
			for _, imp := range parseFileImports(path) {
				if _, seen := importSet[imp]; seen {
					continue
				}
				if len(importSet) < maxExploreImports {
					importSet[imp] = path
				} else {
					truncated = true
					break
				}
			}
			return nil
		})

		deps.Truncated = truncated

		for imp, from := range importSet {
			isInternal := internal(imp, from)
			deps.Imports = append(deps.Imports, ExploreDependency{
				Name:     imp,
				Internal: isInternal,
			})
			if isInternal {
				deps.InternalDeps = append(deps.InternalDeps, imp)
			} else {
				deps.ExternalDeps = append(deps.ExternalDeps, imp)
			}
		}
	}

	// Sort for deterministic output
	sort.Slice(deps.Imports, func(i, j int) bool {
		return deps.Imports[i].Name < deps.Imports[j].Name
	})
	sort.Strings(deps.InternalDeps)
	sort.Strings(deps.ExternalDeps)

	return deps
}

// exploreImportClassifier says whether an import in a file reaches the repo's own
// code: a relative path, a Go package of the module, a Python module of the repo
// (beside the file, from the root, or through sys.path). One classifier per language,
// so each reads go.mod or lists the Python files once.
func (e *Engine) exploreImportClassifier() func(imp, absFile string) bool {
	classifiers := make(map[string]*modules.ImportClassifier)
	return func(imp, absFile string) bool {
		if strings.HasPrefix(imp, ".") || strings.HasPrefix(imp, "/") {
			return true
		}
		var lang string
		switch strings.ToLower(filepath.Ext(absFile)) {
		case ".go":
			lang = modules.LanguageGo
		case ".py", ".pyi":
			lang = modules.LanguagePython
		default:
			return false
		}
		c := classifiers[lang]
		if c == nil {
			c = modules.NewImportClassifier(&modules.ModuleContext{RepoRoot: e.repoRoot, Language: lang})
			classifiers[lang] = c
		}
		rel, err := filepath.Rel(e.repoRoot, absFile)
		if err != nil {
			return false
		}
		return c.ResolveImport(imp, filepath.ToSlash(rel)) != ""
	}
}

// extractQuotedString extracts a string between double quotes from a line
func extractQuotedString(line string) string {
	start := strings.Index(line, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(line[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return line[start+1 : start+1+end]
}

// extractModuleSpecifier extracts the module specifier from a JS/TS import line
// Handles: import X from 'module', import 'module', import X from "module"
func extractModuleSpecifier(line string) string {
	// Look for 'from' keyword first (common case)
	if idx := strings.Index(line, " from "); idx >= 0 {
		rest := strings.TrimSpace(line[idx+6:])
		return extractJSString(rest)
	}

	// Handle: import 'module' or import "module" (side-effect imports)
	if strings.HasPrefix(line, "import ") {
		rest := strings.TrimSpace(line[7:])
		// Skip if it starts with { (destructuring) or identifier
		if strings.HasPrefix(rest, "'") || strings.HasPrefix(rest, `"`) {
			return extractJSString(rest)
		}
	}

	return ""
}

// extractJSString extracts a string from JS/TS (handles both ' and ")
func extractJSString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return ""
	}

	var quote byte
	if s[0] == '\'' {
		quote = '\''
	} else if s[0] == '"' {
		quote = '"'
	} else {
		return ""
	}

	end := strings.IndexByte(s[1:], quote)
	if end < 0 {
		return ""
	}

	result := s[1 : 1+end]

	// Validate: module specifiers don't contain spaces or special chars
	if strings.ContainsAny(result, " \t{}()") {
		return ""
	}

	return result
}

// parseFileImports extracts import statements from a file.
func parseFileImports(filePath string) []string {
	// Only parse source code files, skip JSON, markdown, etc.
	if !isSourceFile(filepath.Base(filePath)) {
		return nil
	}

	// Simple heuristic parsing - could be enhanced with tree-sitter
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	var imports []string
	lines := strings.Split(string(content), "\n")
	ext := strings.ToLower(filepath.Ext(filePath))

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Go imports (only for .go files)
		if ext == ".go" {
			if strings.HasPrefix(line, "import ") || (strings.HasPrefix(line, `"`) && strings.HasSuffix(line, `"`)) {
				if imp := extractQuotedString(line); imp != "" {
					imports = append(imports, imp)
				}
			}
			continue
		}

		// Python imports (only for .py files)
		if ext == ".py" {
			if strings.HasPrefix(line, "from ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					imports = append(imports, parts[1])
				}
			} else if strings.HasPrefix(line, "import ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					imp := strings.Split(parts[1], ",")[0]
					imports = append(imports, strings.TrimSpace(imp))
				}
			}
			continue
		}

		// JavaScript/TypeScript imports
		if strings.HasPrefix(line, "import ") {
			// import X from 'module' or import 'module'
			if imp := extractModuleSpecifier(line); imp != "" {
				imports = append(imports, imp)
			}
		}
	}

	return imports
}

// getExploreChanges retrieves recent changes for the target.
func (e *Engine) getExploreChanges(ctx context.Context, absPath, relTarget string, limit int) ([]ExploreChange, error) {
	if e.gitAdapter == nil || !e.gitAdapter.IsAvailable() {
		return nil, nil
	}

	history, err := e.gitAdapter.GetFileHistory(relTarget, limit)
	if err != nil {
		return nil, err
	}

	changes := make([]ExploreChange, 0, len(history.Commits))
	for _, commit := range history.Commits {
		changes = append(changes, ExploreChange{
			CommitHash:   commit.Hash[:8],
			Message:      commit.Message,
			Author:       commit.Author,
			Date:         commit.Timestamp,
			FilesChanged: 1, // Would need git show to get accurate count
		})
	}

	return changes, nil
}

// getExploreHotspots retrieves hotspots for the target.
func (e *Engine) getExploreHotspots(ctx context.Context, absPath, relTarget string, limit int) ([]ExploreHotspot, error) {
	// Use the existing hotspots API
	hsResp, err := e.GetHotspots(ctx, GetHotspotsOptions{
		Scope: relTarget,
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}

	hotspots := make([]ExploreHotspot, 0, len(hsResp.Hotspots))
	for _, hs := range hsResp.Hotspots {
		// Calculate score from ranking if available
		score := 0.5 // default
		if hs.Ranking != nil {
			score = hs.Ranking.Score / 100.0 // Normalize to 0-1
		}
		reason := "high-churn"
		if hs.RiskLevel == "high" {
			reason = "volatile"
		}
		hotspots = append(hotspots, ExploreHotspot{
			File:      hs.FilePath,
			Score:     score,
			Reason:    reason,
			ChurnRate: float64(hs.Churn.ChangeCount) / 30.0, // Changes per day
		})
	}

	return hotspots, nil
}

// generateExploreSuggestions creates actionable suggestions.
func (e *Engine) generateExploreSuggestions(symbols []ExploreSymbol, hotspots []ExploreHotspot, opts ExploreOptions) []string {
	var suggestions []string

	// Suggest drilling into key symbols
	if len(symbols) > 0 {
		suggestions = append(suggestions, fmt.Sprintf("Use 'understand %s' to deep-dive into key symbol", symbols[0].Name))
	}

	// Suggest reviewing hotspots
	if len(hotspots) > 0 && hotspots[0].Score > 0.7 {
		suggestions = append(suggestions, fmt.Sprintf("Review hotspot '%s' (high churn)", filepath.Base(hotspots[0].File)))
	}

	// Suggest deeper exploration if shallow
	if opts.Depth == ExploreShallow {
		suggestions = append(suggestions, "Use depth='standard' or 'deep' for more details")
	}

	return suggestions
}

// =============================================================================
// understand - Symbol Deep-Dive
// =============================================================================

// UnderstandOptions controls understand behavior.
type UnderstandOptions struct {
	Query             string // symbol name or ID
	IncludeReferences bool
	IncludeCallGraph  bool
	MaxReferences     int
}

// UnderstandResponse provides comprehensive symbol understanding.
type UnderstandResponse struct {
	AINavigationMeta
	Symbol       *SymbolInfo           `json:"symbol,omitempty"`
	Explanation  string                `json:"explanation"`
	References   *UnderstandReferences `json:"references,omitempty"`
	Callers      []UnderstandCaller    `json:"callers,omitempty"`
	Callees      []UnderstandCallee    `json:"callees,omitempty"`
	RelatedTests []UnderstandTest      `json:"relatedTests,omitempty"`
	Ambiguity    *UnderstandAmbiguity  `json:"ambiguity,omitempty"`
}

// UnderstandReferences groups references by file.
type UnderstandReferences struct {
	TotalCount int                       `json:"totalCount"`
	ByFile     []UnderstandReferenceFile `json:"byFile"`
	Truncated  bool                      `json:"truncated"`
}

// UnderstandReferenceFile groups references in a single file.
type UnderstandReferenceFile struct {
	File       string              `json:"file"`
	Count      int                 `json:"count"`
	References []UnderstandRefLine `json:"references"`
}

// UnderstandRefLine represents a single reference location.
type UnderstandRefLine struct {
	Line    int    `json:"line"`
	Kind    string `json:"kind"`
	Context string `json:"context,omitempty"`
}

// UnderstandCaller represents a caller of the symbol.
type UnderstandCaller struct {
	SymbolId string `json:"symbolId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// UnderstandCallee represents a callee of the symbol.
type UnderstandCallee struct {
	SymbolId string `json:"symbolId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	File     string `json:"file,omitempty"`
}

// UnderstandTest represents a test related to the symbol.
type UnderstandTest struct {
	File     string `json:"file"`
	Name     string `json:"name,omitempty"`
	Line     int    `json:"line,omitempty"`
	Relation string `json:"relation"` // imports, tests, references
}

// UnderstandAmbiguity describes multi-match scenarios.
type UnderstandAmbiguity struct {
	MatchCount int               `json:"matchCount"`
	TopMatches []UnderstandMatch `json:"topMatches"`
	Hint       string            `json:"hint"`
}

// UnderstandMatch represents a potential match for ambiguous queries.
type UnderstandMatch struct {
	SymbolId   string `json:"symbolId"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	ModuleId   string `json:"moduleId"`
	Visibility string `json:"visibility"`
}

// Understand provides comprehensive symbol deep-dive.
// Replaces: searchSymbols → getSymbol → explainSymbol → findReferences → getCallGraph
func (e *Engine) Understand(ctx context.Context, opts UnderstandOptions) (*UnderstandResponse, error) {
	startTime := time.Now()

	// Set defaults
	if opts.MaxReferences <= 0 {
		opts.MaxReferences = 50
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// First, search for the symbol
	searchResp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: opts.Query,
		Limit: 10,
	})
	if err != nil {
		return nil, err
	}

	// Check for ambiguity
	if len(searchResp.Symbols) == 0 {
		return nil, errors.NewCkbError(errors.SymbolNotFound, fmt.Sprintf("No symbols found matching '%s'", opts.Query), nil, nil, nil)
	}

	// If multiple matches, return ambiguity info unless exact match
	var targetSymbol *SearchResultItem
	var ambiguity *UnderstandAmbiguity

	if len(searchResp.Symbols) > 1 {
		// Check for exact match
		for i, sym := range searchResp.Symbols {
			if strings.EqualFold(sym.Name, opts.Query) || sym.StableId == opts.Query {
				targetSymbol = &searchResp.Symbols[i]
				break
			}
		}

		// "Container.Member": prefer the member of that container
		qualified := qualifiedMatches(searchResp.Symbols, opts.Query)
		if targetSymbol == nil && len(qualified) == 1 {
			targetSymbol = &searchResp.Symbols[qualified[0]]
		}

		// If no exact match, return ambiguity info
		if targetSymbol == nil {
			topMatches := make([]UnderstandMatch, 0, 5)
			for i, sym := range searchResp.Symbols {
				if i >= 5 {
					break
				}
				visibility := "internal"
				if sym.Visibility != nil {
					visibility = sym.Visibility.Visibility
				}
				topMatches = append(topMatches, UnderstandMatch{
					SymbolId:   sym.StableId,
					Name:       sym.Name,
					Kind:       sym.Kind,
					ModuleId:   sym.ModuleId,
					Visibility: visibility,
				})
			}
			ambiguity = &UnderstandAmbiguity{
				MatchCount: searchResp.TotalCount,
				TopMatches: topMatches,
				Hint:       "Add module scope or use full symbol ID to disambiguate",
			}
			best := 0
			if len(qualified) > 0 {
				best = qualified[0]
			}
			targetSymbol = &searchResp.Symbols[best] // Use best match
		}
	} else {
		targetSymbol = &searchResp.Symbols[0]
	}

	// Get detailed symbol info
	symbolResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: targetSymbol.StableId})
	if err != nil {
		return nil, err
	}

	// Get explanation
	explainResp, err := e.ExplainSymbol(ctx, ExplainSymbolOptions{SymbolId: targetSymbol.StableId})
	explanation := ""
	if err == nil && explainResp != nil {
		explanation = explainResp.Summary.Tldr
	}

	// Collect results in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex

	var references *UnderstandReferences
	var callers []UnderstandCaller
	var callees []UnderstandCallee
	var relatedTests []UnderstandTest
	var warnings []string

	// Get references if requested
	if opts.IncludeReferences {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs, err := e.getUnderstandReferences(ctx, targetSymbol.StableId, opts.MaxReferences)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("references: %s", err.Error()))
				mu.Unlock()
				return
			}
			mu.Lock()
			references = refs
			mu.Unlock()
		}()
	}

	// Get call graph if requested
	if opts.IncludeCallGraph {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, ce, err := e.getUnderstandCallGraph(ctx, targetSymbol.StableId)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("callgraph: %s", err.Error()))
				mu.Unlock()
				return
			}
			mu.Lock()
			callers = c
			callees = ce
			mu.Unlock()
		}()
	}

	// Get related tests
	wg.Add(1)
	go func() {
		defer wg.Done()
		tests := e.getUnderstandTests(ctx, targetSymbol.StableId, targetSymbol.ModuleId)
		mu.Lock()
		relatedTests = tests
		mu.Unlock()
	}()

	wg.Wait()

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip",
			Available: true,
			Used:      true,
		})
	}

	completeness := CompletenessInfo{
		Score:  0.9,
		Reason: "compound-understand",
	}

	// Build drilldowns
	drilldowns := []output.Drilldown{
		{Label: "Analyze impact", Query: fmt.Sprintf("prepareChange %s", targetSymbol.StableId)},
	}
	if references != nil && references.Truncated {
		drilldowns = append(drilldowns, output.Drilldown{
			Label: "Get all references",
			Query: fmt.Sprintf("findReferences %s --limit 200", targetSymbol.StableId),
		})
	}

	return &UnderstandResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "understand",
			Resolved:      &ResolvedTarget{SymbolId: targetSymbol.StableId, ResolvedFrom: "search", Confidence: 0.95},
			Provenance:    e.buildProvenance(repoState, "full", startTime, backendContribs, completeness),
			Drilldowns:    drilldowns,
		},
		Symbol:       symbolResp.Symbol,
		Explanation:  explanation,
		References:   references,
		Callers:      callers,
		Callees:      callees,
		RelatedTests: relatedTests,
		Ambiguity:    ambiguity,
	}, nil
}

// getUnderstandReferences retrieves and groups references by file.
func (e *Engine) getUnderstandReferences(ctx context.Context, symbolId string, limit int) (*UnderstandReferences, error) {
	refResp, err := e.FindReferences(ctx, FindReferencesOptions{
		SymbolId:     symbolId,
		IncludeTests: true,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}

	// Group by file
	byFile := make(map[string][]UnderstandRefLine)
	for _, ref := range refResp.References {
		if ref.Location == nil {
			continue
		}
		file := ref.Location.FileId
		byFile[file] = append(byFile[file], UnderstandRefLine{
			Line:    ref.Location.StartLine,
			Kind:    ref.Kind,
			Context: ref.Context,
		})
	}

	// Convert to sorted slice
	files := make([]UnderstandReferenceFile, 0, len(byFile))
	for file, refs := range byFile {
		// Sort refs by line
		sort.Slice(refs, func(i, j int) bool {
			return refs[i].Line < refs[j].Line
		})
		files = append(files, UnderstandReferenceFile{
			File:       file,
			Count:      len(refs),
			References: refs,
		})
	}

	// Sort files by count (most references first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Count > files[j].Count
	})

	return &UnderstandReferences{
		TotalCount: refResp.TotalCount,
		ByFile:     files,
		Truncated:  refResp.Truncated,
	}, nil
}

// getUnderstandCallGraph retrieves callers and callees.
func (e *Engine) getUnderstandCallGraph(ctx context.Context, symbolId string) ([]UnderstandCaller, []UnderstandCallee, error) {
	callResp, err := e.GetCallGraph(ctx, CallGraphOptions{
		SymbolId:  symbolId,
		Direction: "both",
		Depth:     1,
	})
	if err != nil {
		return nil, nil, err
	}

	var callers []UnderstandCaller
	var callees []UnderstandCallee

	for _, node := range callResp.Nodes {
		if node.Role == "caller" {
			loc := node.Location
			line := 0
			file := ""
			if loc != nil {
				line = loc.StartLine
				file = loc.FileId
			}
			callers = append(callers, UnderstandCaller{
				SymbolId: node.SymbolId,
				Name:     node.Name,
				Kind:     "", // Not in CallGraphNode
				File:     file,
				Line:     line,
			})
		} else if node.Role == "callee" {
			callees = append(callees, UnderstandCallee{
				SymbolId: node.SymbolId,
				Name:     node.Name,
				Kind:     "",
			})
		}
	}

	return callers, callees, nil
}

// getUnderstandTests finds tests related to a symbol.
func (e *Engine) getUnderstandTests(ctx context.Context, symbolId, moduleId string) []UnderstandTest {
	var tests []UnderstandTest

	// Look for test files in the same module
	if moduleId != "" {
		testPattern := filepath.Join(e.repoRoot, moduleId, "*_test.go")
		matches, _ := filepath.Glob(testPattern)
		for _, match := range matches {
			rel, _ := filepath.Rel(e.repoRoot, match)
			tests = append(tests, UnderstandTest{
				File:     rel,
				Relation: "same-module",
			})
		}

		// Also check for TypeScript/JavaScript tests
		for _, pattern := range []string{"*.test.ts", "*.test.js", "*.spec.ts", "*.spec.js"} {
			testPattern = filepath.Join(e.repoRoot, moduleId, pattern)
			matches, _ = filepath.Glob(testPattern)
			for _, match := range matches {
				rel, _ := filepath.Rel(e.repoRoot, match)
				tests = append(tests, UnderstandTest{
					File:     rel,
					Relation: "same-module",
				})
			}
		}
	}

	// Limit to 10 tests
	if len(tests) > 10 {
		tests = tests[:10]
	}

	return tests
}

// =============================================================================
// prepareChange - Pre-Change Analysis
// =============================================================================

// ChangeType describes the type of change being planned.
type ChangeType string

const (
	ChangeModify  ChangeType = "modify"
	ChangeRename  ChangeType = "rename"
	ChangeDelete  ChangeType = "delete"
	ChangeExtract ChangeType = "extract"
	ChangeMove    ChangeType = "move"
)

// PrepareChangeOptions controls prepareChange behavior.
type PrepareChangeOptions struct {
	Target     string     // symbol ID or file path
	ChangeType ChangeType // modify, rename, delete, extract, move
	TargetPath string     // destination path (for move operations)
	StartLine  int        // start line for extract operations (0 = whole file)
	EndLine    int        // end line for extract operations (0 = whole file)
}

// PrepareChangeResponse provides comprehensive pre-change analysis.
type PrepareChangeResponse struct {
	AINavigationMeta
	Target           *PrepareChangeTarget `json:"target"`
	DirectDependents []PrepareDependent   `json:"directDependents"`
	TransitiveImpact *PrepareTransitive   `json:"transitiveImpact"`
	RelatedTests     []PrepareTest        `json:"relatedTests"`
	CoChangeFiles    []PrepareCoChange    `json:"coChangeFiles,omitempty"`
	RiskAssessment   *PrepareRisk         `json:"riskAssessment"`
	RenameDetail     *RenameDetail        `json:"renameDetail,omitempty"`
	ExtractDetail    *ExtractDetail       `json:"extractDetail,omitempty"`
	MoveDetail       *MoveDetail          `json:"moveDetail,omitempty"`
	// ArchImpact is Cartographer's module-level impact simulation: predicted affected
	// modules, cycle risk, layer violations, and health delta. Only populated when the
	// binary is built with -tags cartographer.
	ArchImpact *cartographer.ImpactAnalysis `json:"archImpact,omitempty"`
}

// PrepareChangeTarget describes what will be changed.
type PrepareChangeTarget struct {
	SymbolId   string `json:"symbolId,omitempty"`
	Name       string `json:"name"`
	Kind       string `json:"kind"` // symbol, file, module
	Path       string `json:"path,omitempty"`
	ModuleId   string `json:"moduleId,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

// PrepareDependent describes a direct dependent.
type PrepareDependent struct {
	// SymbolId and Name identify the enclosing symbol (e.g. the caller
	// function) that contains the reference. "" (not "unknown") when the
	// backend couldn't resolve an enclosing symbol (e.g. a package-level
	// reference outside any function). These fields are NOT omitempty:
	// schemaVersion 1 documents them as always-present strings, so an
	// empty value is serialized as "" rather than dropping the key —
	// dropping it would be a breaking contract change for strict
	// consumers without a schema version bump.
	SymbolId string `json:"symbolId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	ModuleId string `json:"moduleId"`
}

// PrepareTransitive summarizes transitive impact.
type PrepareTransitive struct {
	// DirectCallers counts the direct consumers, all of them: directDependents is
	// cut to the response budget. Implementations are counted apart.
	DirectCallers int `json:"directCallers"`
	// Implementations counts the types implementing a Go interface or interface
	// method target, listed in directDependents as kind implements-interface.
	Implementations int `json:"implementations,omitempty"`
	// NameMatchImplementations counts types that have a one-method interface's
	// method but whose package doesn't import the interface's: not counted above.
	NameMatchImplementations int `json:"nameMatchImplementations,omitempty"`
	TotalCallers             int `json:"totalCallers"`
	ModuleSpread             int `json:"moduleSpread"`
	MaxDepth                 int `json:"maxDepth"`
}

// PrepareTest describes a test that may need updating.
type PrepareTest struct {
	File     string `json:"file"`
	Name     string `json:"name,omitempty"`
	Relation string `json:"relation"` // direct, transitive, coverage
}

// PrepareCoChange describes a file that historically changes together.
type PrepareCoChange struct {
	File        string  `json:"file"`
	Correlation float64 `json:"correlation"`
	CoChanges   int     `json:"coChanges"`
	IsHidden    bool    `json:"isHidden,omitempty"` // true = co-changes without any import edge
}

// PrepareRisk assesses the risk of the change.
type PrepareRisk struct {
	Level string  `json:"level"` // low, medium, high, critical, or RiskLevelNotAssessed
	Score float64 `json:"score"`
	// Basis says what the score assumes where that changes the answer: for
	// modify, that the declaration stays as it is.
	Basis       string   `json:"basis,omitempty"`
	Factors     []string `json:"factors"`
	Suggestions []string `json:"suggestions"`
}

// PrepareChange provides comprehensive pre-change analysis.
// Replaces: analyzeImpact + getAffectedTests + analyzeCoupling + risk calculation
func (e *Engine) PrepareChange(ctx context.Context, opts PrepareChangeOptions) (*PrepareChangeResponse, error) {
	startTime := time.Now()

	// Set defaults
	if opts.ChangeType == "" {
		opts.ChangeType = ChangeModify
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "full")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	// Determine target type and resolve
	target, err := e.resolvePrepareTarget(ctx, opts.Target)
	if err != nil {
		return nil, err
	}

	// Collect results in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex

	var directDependents []PrepareDependent
	var transitiveImpact *PrepareTransitive
	var relatedTests []PrepareTest
	var coChangeFiles []PrepareCoChange
	var renameDetail *RenameDetail
	var extractDetail *ExtractDetail
	var moveDetail *MoveDetail
	var riskFactors []string
	var warnings []string
	var archImpact *cartographer.ImpactAnalysis

	// Get impact analysis. A file target is the sum of what it declares: its
	// types, their fields and methods, its functions.
	var testConsumers []ImpactItem
	wg.Add(1)
	go func() {
		defer wg.Done()
		var imp *prepareImpact
		var err error
		switch {
		case target.SymbolId != "":
			imp, err = e.getPrepareImpact(ctx, target.SymbolId)
		case target.Kind == "file":
			imp, err = e.getPrepareFileImpact(ctx, target.Path)
		default:
			return
		}
		if err != nil {
			mu.Lock()
			warnings = append(warnings, fmt.Sprintf("impact: %s", err.Error()))
			mu.Unlock()
			return
		}
		if imp == nil {
			return
		}
		mu.Lock()
		directDependents = imp.dependents
		transitiveImpact = imp.transitive
		testConsumers = imp.tests
		riskFactors = append(riskFactors, imp.factors...)
		mu.Unlock()
	}()

	// Get co-change files if git available
	if e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path := target.Path
			if path == "" && target.ModuleId != "" {
				path = target.ModuleId
			}
			if path == "" {
				return
			}
			coChanges, err := e.getPrepareCoChanges(ctx, path)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("cochange: %s", err.Error()))
				mu.Unlock()
				return
			}
			mu.Lock()
			coChangeFiles = coChanges
			mu.Unlock()
		}()
	}

	// Get rename detail for rename operations
	if opts.ChangeType == ChangeRename && target.SymbolId != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rd := e.getPrepareRenameDetail(ctx, target.SymbolId)
			mu.Lock()
			renameDetail = rd
			mu.Unlock()
		}()
	}

	// Get extract detail for extract operations
	if opts.ChangeType == ChangeExtract {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ed := e.getPrepareExtractDetail(target, opts.StartLine, opts.EndLine)
			mu.Lock()
			extractDetail = ed
			mu.Unlock()
		}()
	}

	// Get move detail for move operations
	if opts.ChangeType == ChangeMove && opts.TargetPath != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			md := e.getPrepareMove(ctx, target, opts.TargetPath)
			mu.Lock()
			moveDetail = md
			mu.Unlock()
		}()
	}

	// Architectural impact simulation via Cartographer (module-level cascade,
	// cycle risk, layer violations). Falls through silently if not compiled in.
	if cartographer.Available() && target.Path != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ai, cerr := cartographer.SimulateChange(e.repoRoot, target.Path, "", ""); cerr == nil {
				mu.Lock()
				archImpact = ai
				if ai.PredictedImpact.WillCreateCycle {
					riskFactors = append(riskFactors, "Change may introduce a dependency cycle")
				}
				if len(ai.PredictedImpact.LayerViolations) > 0 {
					riskFactors = append(riskFactors, fmt.Sprintf("Change may create %d layer violation(s)", len(ai.PredictedImpact.LayerViolations)))
				}
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Tests come from the references the impact analysis found, so they wait for it.
	relatedTests = e.getPrepareTests(target, testConsumers)

	// Calculate risk assessment
	risk := e.calculatePrepareRisk(target, directDependents, transitiveImpact, relatedTests, coChangeFiles, riskFactors, opts.ChangeType)
	if !e.hasConsumerGraph() {
		markConsumersNotAssessed(risk, len(directDependents))
	}

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip",
			Available: true,
			Used:      true,
		})
	}
	if e.gitAdapter != nil && e.gitAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "git",
			Available: true,
			Used:      true,
		})
	}

	completeness := CompletenessInfo{
		Score:  0.9,
		Reason: "compound-preparechange",
	}

	return &PrepareChangeResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "prepareChange",
			Resolved:      &ResolvedTarget{SymbolId: target.SymbolId, ResolvedFrom: "id", Confidence: 1.0},
			Provenance:    e.buildProvenance(repoState, "full", startTime, backendContribs, completeness),
		},
		Target:           target,
		DirectDependents: directDependents,
		TransitiveImpact: transitiveImpact,
		RelatedTests:     relatedTests,
		CoChangeFiles:    coChangeFiles,
		RiskAssessment:   risk,
		RenameDetail:     renameDetail,
		ExtractDetail:    extractDetail,
		MoveDetail:       moveDetail,
		ArchImpact:       archImpact,
	}, nil
}

// resolvePrepareTarget resolves the target to a PrepareChangeTarget.
func (e *Engine) resolvePrepareTarget(ctx context.Context, target string) (*PrepareChangeTarget, error) {
	// Check if it's a file path
	absPath := target
	if !filepath.IsAbs(target) {
		absPath = filepath.Join(e.repoRoot, target)
	}
	absPath = filepath.Clean(absPath)

	if info, err := os.Stat(absPath); err == nil {
		// It's a file or directory
		relPath, _ := filepath.Rel(e.repoRoot, absPath)
		if info.IsDir() {
			return &PrepareChangeTarget{
				Name:     filepath.Base(relPath),
				Kind:     "module",
				Path:     relPath,
				ModuleId: relPath,
			}, nil
		}
		return &PrepareChangeTarget{
			Name:     filepath.Base(relPath),
			Kind:     "file",
			Path:     relPath,
			ModuleId: filepath.Dir(relPath),
		}, nil
	}

	// Try to resolve as symbol ID
	symbolResp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: target})
	if err != nil {
		return nil, errors.NewCkbError(errors.ResourceNotFound, fmt.Sprintf("Target not found: %s", target), nil, nil, nil)
	}

	if symbolResp.Symbol == nil {
		return nil, errors.NewCkbError(errors.ResourceNotFound, fmt.Sprintf("Symbol not found: %s", target), nil, nil, nil)
	}

	visibility := "internal"
	if symbolResp.Symbol.Visibility != nil {
		visibility = symbolResp.Symbol.Visibility.Visibility
	}

	path := ""
	if symbolResp.Symbol.Location != nil {
		path = symbolResp.Symbol.Location.FileId
	}

	// The SCIP backend doesn't resolve ModuleId on symbol lookups (see
	// backends/scip/adapter.go convertToSymbolResult: "Module ID is
	// resolved later by the query engine" — nothing downstream of
	// GetSymbol ever did that resolution). Left empty, getPrepareTests'
	// same-module test-file glob never runs (it's gated on
	// target.ModuleId != ""), so every symbol-based prepareChange call
	// reported "no tests found" regardless of actual test files sitting
	// right next to the target. Derive it from the file path the same
	// way the file/directory branches above already do.
	moduleId := symbolResp.Symbol.ModuleId
	if moduleId == "" && path != "" {
		moduleId = filepath.Dir(path)
	}

	return &PrepareChangeTarget{
		SymbolId:   symbolResp.Symbol.StableId,
		Name:       symbolResp.Symbol.Name,
		Kind:       symbolResp.Symbol.Kind,
		Path:       path,
		ModuleId:   moduleId,
		Visibility: visibility,
	}, nil
}

// prepareImpact is what prepareChange takes from impact analysis.
type prepareImpact struct {
	dependents []PrepareDependent
	transitive *PrepareTransitive
	// tests holds every consumer found, test code included: getPrepareTests
	// picks the test files out of it.
	tests   []ImpactItem
	factors []string
}

// getPrepareImpact retrieves impact analysis data for one symbol.
func (e *Engine) getPrepareImpact(ctx context.Context, symbolId string) (*prepareImpact, error) {
	// All items: the counts the risk is scored on come from the whole lists, not
	// the 20 the response budget keeps. Budgeted, a symbol with 40 callers counted
	// 20, and its transitive callers and module spread were whatever fit after them.
	impactResp, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
		SymbolId: symbolId,
		Depth:    2,
		AllItems: true,
	})
	if err != nil {
		return nil, err
	}

	// One entry per consumer, as assessChange counts them: a function calling the
	// target three times is one caller, not three. (AnalyzeImpact's own risk
	// factors are not passed on: prepareChange scores on its own, and their bare
	// names, "visibility", "direct-callers", ..., listed every one of them on every
	// call whatever their value.)
	direct, transitive := mergeConsumers(impactResp.DirectImpact, impactResp.TransitiveImpact)
	sortImpactItems(direct)
	sortImpactItems(transitive)
	imp := &prepareImpact{
		transitive: summarizeTransitive(&AnalyzeImpactResponse{DirectImpact: direct, TransitiveImpact: transitive,
			NameMatchImplementations: impactResp.NameMatchImplementations}),
		tests: allConsumers(direct, transitive, impactResp.TestImpact),
	}
	if limit := e.compressor.GetBudget().MaxImpactItems; limit > 0 && len(direct) > limit {
		direct = direct[:limit]
	}
	imp.dependents = toPrepareDependents(direct)
	return imp, nil
}

// maxPrepareFileSymbols bounds the impact analyses a file target runs, one per
// symbol it declares.
const maxPrepareFileSymbols = 300

// getPrepareFileImpact is the impact of changing a file: the consumers of every
// symbol it declares, types and their fields and methods included, outside the
// file itself. Without a SCIP index there is no symbol list to start from.
func (e *Engine) getPrepareFileImpact(ctx context.Context, path string) (*prepareImpact, error) {
	if !e.hasConsumerGraph() {
		return nil, nil
	}
	symbols, total := fileDeclaredSymbols(e.scipAdapter.GetIndex(), path, maxPrepareFileSymbols)

	var direct, transitive, tests []ImpactItem
	for _, sym := range symbols {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		resp, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{SymbolId: sym, Depth: 2, AllItems: true})
		if err != nil {
			continue // A symbol the index can't resolve has no consumers to add
		}
		direct = append(direct, outsideFile(resp.DirectImpact, path)...)
		transitive = append(transitive, outsideFile(resp.TransitiveImpact, path)...)
		tests = append(tests, resp.TestImpact...)
	}
	direct, transitive = mergeConsumers(direct, transitive)
	sortImpactItems(direct)
	sortImpactItems(transitive)

	imp := &prepareImpact{
		transitive: summarizeTransitive(&AnalyzeImpactResponse{DirectImpact: direct, TransitiveImpact: transitive}),
		tests:      allConsumers(direct, transitive, tests),
	}
	if total > len(symbols) {
		imp.factors = append(imp.factors, fmt.Sprintf("Impact covers the first %d of %d symbols the file declares", len(symbols), total))
	}
	if limit := e.compressor.GetBudget().MaxImpactItems; limit > 0 && len(direct) > limit {
		direct = direct[:limit]
	}
	imp.dependents = toPrepareDependents(direct)
	return imp, nil
}

// fileDeclaredSymbols returns the symbols a file defines, top-level ones (types,
// functions, variables) before members (fields, methods), at most limit of them,
// and how many there were. Locals and the namespace (the package or module
// itself, which every import names) are left out.
func fileDeclaredSymbols(idx *scip.SCIPIndex, path string, limit int) ([]string, int) {
	if idx == nil {
		return nil, 0
	}
	doc := idx.GetDocument(path)
	if doc == nil {
		return nil, 0
	}
	var top, members []string
	seen := make(map[string]bool)
	for _, sym := range doc.Symbols {
		if sym == nil || sym.Symbol == "" || seen[sym.Symbol] ||
			strings.HasPrefix(sym.Symbol, "local ") || strings.HasSuffix(sym.Symbol, "/") {
			continue
		}
		seen[sym.Symbol] = true
		if isMemberSymbol(sym.Symbol) {
			members = append(members, sym.Symbol)
		} else {
			top = append(top, sym.Symbol)
		}
	}
	all := append(top, members...)
	total := len(all)
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, total
}

// isMemberSymbol reports whether a SCIP symbol names a member of a type (a
// field or method: "pkg/Type#Field."), not a top-level declaration ("pkg/Type#",
// "pkg/Func().").
func isMemberSymbol(symbol string) bool {
	descriptor := symbol
	if i := strings.LastIndex(symbol, "/"); i >= 0 {
		descriptor = symbol[i+1:]
	}
	i := strings.Index(descriptor, "#")
	return i >= 0 && i < len(descriptor)-1
}

// outsideFile drops the consumers inside path: a file's own uses of what it
// declares are not something a change to it breaks elsewhere.
func outsideFile(items []ImpactItem, path string) []ImpactItem {
	out := make([]ImpactItem, 0, len(items))
	for _, item := range items {
		if item.Location != nil && item.Location.FileId == path {
			continue
		}
		out = append(out, item)
	}
	return out
}

func allConsumers(lists ...[]ImpactItem) []ImpactItem {
	var all []ImpactItem
	for _, list := range lists {
		all = append(all, list...)
	}
	return all
}

func toPrepareDependents(items []ImpactItem) []PrepareDependent {
	dependents := make([]PrepareDependent, 0, len(items))
	for _, imp := range items {
		line := 0
		file := ""
		if imp.Location != nil {
			line = imp.Location.StartLine
			file = imp.Location.FileId
		}
		dependents = append(dependents, PrepareDependent{
			SymbolId: imp.StableId,
			Name:     imp.Name,
			Kind:     imp.Kind,
			File:     file,
			Line:     line,
			ModuleId: imp.ModuleId,
		})
	}
	return dependents
}

// maxPrepareTests bounds relatedTests. Tests that reference the target are
// listed up to maxPrepareReferencingTests; same-module test files only fill the
// list up to maxPrepareTests.
const (
	maxPrepareTests            = 20
	maxPrepareReferencingTests = 50
)

// getPrepareTests finds the tests affected by the change: first the test files
// that reference the target ("direct") or reach it through other code
// ("transitive"), the same derivation assessChange uses, then, if there is room,
// the other test files in the target's module ("same-module"), which may not
// touch it at all.
func (e *Engine) getPrepareTests(target *PrepareChangeTarget, consumers []ImpactItem) []PrepareTest {
	return prepareTestList(e.deriveAffectedTests(nil, consumers, nil), e.sameModuleTestFiles(target))
}

// prepareTestList orders reference-derived tests direct before transitive, then
// by path, and fills up with same-module files not already listed.
func prepareTestList(referencing []AffectedTest, sameModule []string) []PrepareTest {
	rank := func(reason string) int {
		switch reason {
		case "direct":
			return 0
		case "transitive":
			return 1
		}
		return 2
	}
	sort.SliceStable(referencing, func(i, j int) bool {
		ri, rj := rank(referencing[i].Reason), rank(referencing[j].Reason)
		if ri != rj {
			return ri < rj
		}
		return referencing[i].FilePath < referencing[j].FilePath
	})

	tests := make([]PrepareTest, 0, len(referencing))
	listed := make(map[string]bool)
	for _, t := range referencing {
		if len(tests) >= maxPrepareReferencingTests {
			break
		}
		if listed[t.FilePath] {
			continue
		}
		listed[t.FilePath] = true
		tests = append(tests, PrepareTest{File: t.FilePath, Name: strings.Join(t.TestNames, ", "), Relation: t.Reason})
	}
	for _, path := range sameModule {
		if len(tests) >= maxPrepareTests {
			break
		}
		if listed[path] {
			continue
		}
		listed[path] = true
		tests = append(tests, PrepareTest{File: path, Relation: "same-module"})
	}
	return tests
}

// sameModuleTestFiles lists the test files in the target's module directory.
func (e *Engine) sameModuleTestFiles(target *PrepareChangeTarget) []string {
	if target.ModuleId == "" {
		return nil
	}
	var files []string
	for _, glob := range []string{"*_test.go", "*.test.ts", "*.test.js", "*.spec.ts", "*.spec.js"} {
		matches, _ := filepath.Glob(filepath.Join(e.repoRoot, target.ModuleId, glob))
		for _, match := range matches {
			rel, _ := filepath.Rel(e.repoRoot, match)
			files = append(files, rel)
		}
	}
	return files
}

// languageSupportsTestDiscovery reports whether getPrepareTests' glob-based
// test discovery actually covers the target's language. Discovery only
// recognizes Go (*_test.go) and TypeScript/JavaScript (*.test.ts/js,
// *.spec.ts/js) test file conventions. For every other language (Python,
// Rust, Java/Kotlin, C#, ...) an empty `tests` slice means "we never
// looked", not "there are no tests" — so calculatePrepareRisk must not turn
// it into a "No test files found" finding or score contribution for those
// languages; that would be a false claim, not an honest gap.
func (e *Engine) languageSupportsTestDiscovery(target *PrepareChangeTarget) bool {
	if ext := filepath.Ext(target.Path); ext != "" {
		switch ext {
		case ".go", ".ts", ".js":
			return true
		default:
			return false
		}
	}

	// No extension on the target itself (e.g. a directory/module target) —
	// infer from the module directory's contents rather than silently
	// assuming Go/TS/JS.
	if target.ModuleId == "" {
		return false
	}
	dir := filepath.Join(e.repoRoot, target.ModuleId)
	for _, ext := range []string{".go", ".ts", ".js"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "*"+ext))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}

// getPrepareCoChanges finds files that historically change together.
// When Cartographer is available, uses a single git-log pass (bot-filtered) and
// marks pairs that have no import edge as IsHidden. Falls back to the per-file
// coupling analyzer otherwise.
func (e *Engine) getPrepareCoChanges(ctx context.Context, path string) ([]PrepareCoChange, error) {
	if cartographer.Available() {
		// Single pass over git history — much faster than O(n) subprocess approach.
		pairs, err := cartographer.GitCochange(e.repoRoot, 0, 2)
		if err == nil {
			// Build hidden-coupling set for annotation (pairs with no import edge).
			hiddenPairs, _ := cartographer.HiddenCoupling(e.repoRoot, 0, 2)
			hiddenSet := make(map[string]bool, len(hiddenPairs)*2)
			for _, h := range hiddenPairs {
				hiddenSet[h.FileA+"\x00"+h.FileB] = true
				hiddenSet[h.FileB+"\x00"+h.FileA] = true
			}

			var coChanges []PrepareCoChange
			for _, p := range pairs {
				partner := ""
				if p.FileA == path {
					partner = p.FileB
				} else if p.FileB == path {
					partner = p.FileA
				}
				if partner == "" {
					continue
				}
				coChanges = append(coChanges, PrepareCoChange{
					File:        partner,
					Correlation: p.CouplingScore,
					CoChanges:   p.Count,
					IsHidden:    hiddenSet[path+"\x00"+partner],
				})
			}
			sort.Slice(coChanges, func(i, j int) bool {
				return coChanges[i].Correlation > coChanges[j].Correlation
			})
			if len(coChanges) > 10 {
				coChanges = coChanges[:10]
			}
			return coChanges, nil
		}
	}

	// Fallback: per-file coupling analyzer (O(1) git subprocess, regex-based).
	analyzer := coupling.NewAnalyzer(e.repoRoot, e.logger)
	result, err := analyzer.Analyze(ctx, coupling.AnalyzeOptions{
		Target:         path,
		MinCorrelation: 0.3,
		WindowDays:     365,
		Limit:          10,
		RepoRoot:       e.repoRoot,
	})
	if err != nil {
		return nil, err
	}

	coChanges := make([]PrepareCoChange, 0, len(result.Correlations))
	for _, cf := range result.Correlations {
		coChanges = append(coChanges, PrepareCoChange{
			File:        cf.FilePath,
			Correlation: cf.Correlation,
			CoChanges:   cf.CoChangeCount,
		})
	}
	return coChanges, nil
}

// markConsumersNotAssessed qualifies a prepareChange risk computed without a
// consumer graph, where "no dependents" means nobody looked. A "low" built on that
// is a clean bill of health nobody earned, so it becomes not assessed. Whatever
// doesn't need SCIP (co-change, visibility, change type, tests) stays in the
// factors. If that evidence alone reaches medium or above, the level stands as
// what it is, a lower bound.
func markConsumersNotAssessed(risk *PrepareRisk, knownDependents int) {
	if knownDependents > 0 {
		return // Found some other way: a real lower bound on the graph's answer
	}
	risk.Factors = append([]string{"Downstream consumers not assessed: no SCIP index"}, risk.Factors...)
	risk.Suggestions = append(risk.Suggestions, "Run 'ckb index' to see what depends on this")
	if risk.Level == "low" {
		risk.Level = RiskLevelNotAssessed
		risk.Score = 0
	}
}

// calculatePrepareRisk calculates the risk assessment.
func (e *Engine) calculatePrepareRisk(
	target *PrepareChangeTarget,
	dependents []PrepareDependent,
	transitive *PrepareTransitive,
	tests []PrepareTest,
	coChanges []PrepareCoChange,
	existingFactors []string,
	changeType ChangeType,
) *PrepareRisk {
	score := 0.0
	var factors []string
	var suggestions []string

	// dependents is cut to the response budget; the summary counts them all.
	callers, impls := 0, 0
	for _, d := range dependents {
		if d.Kind == kindImplementation {
			impls++
		} else {
			callers++
		}
	}
	if transitive != nil && transitive.DirectCallers+transitive.Implementations > callers+impls {
		callers, impls = transitive.DirectCallers, transitive.Implementations
	}
	described := describeDependents(callers, impls)

	// Factor: Number of direct dependents
	if callers+impls > 10 {
		score += 0.25
		factors = append(factors, fmt.Sprintf("High dependent count (%s)", described))
	} else if callers+impls > 5 {
		score += 0.15
		factors = append(factors, fmt.Sprintf("Moderate dependent count (%s)", described))
	}
	if transitive != nil && transitive.NameMatchImplementations > 0 {
		factors = append(factors, fmt.Sprintf("%d more type(s) have the method, in packages that don't import the interface's: not counted, they break only where used as it", transitive.NameMatchImplementations))
	}

	// Factor: Module spread
	if transitive != nil && transitive.ModuleSpread > 5 {
		score += 0.2
		factors = append(factors, fmt.Sprintf("High module spread (%d modules affected)", transitive.ModuleSpread))
		suggestions = append(suggestions, "Consider splitting into smaller changes")
	}

	// Factor: Public visibility
	if target.Visibility == "public" {
		score += 0.15
		factors = append(factors, "Public API change")
		suggestions = append(suggestions, "Ensure backward compatibility or bump major version")
	}

	// Factor: Test coverage. tests comes from getPrepareTests, which only
	// globs for *_test.go/*.test.ts/*.spec.ts files in the target's own
	// module directory — it doesn't check whether the target symbol is
	// itself referenced by any test, so word this as what it actually
	// measures rather than implying broader test-coverage knowledge we
	// don't have. Discovery itself is only implemented for Go and TS/JS
	// (see languageSupportsTestDiscovery): for any other language an empty
	// `tests` slice means discovery never ran, not that there are no
	// tests, so the factor is omitted rather than asserted.
	if len(tests) == 0 && e.languageSupportsTestDiscovery(target) {
		score += 0.2
		factors = append(factors, "No test files found in target module")
		suggestions = append(suggestions, "Add tests before modifying")
	}

	// Factor: Change type
	if changeType == ChangeDelete {
		score += 0.15
		factors = append(factors, "Deletion change type")
	} else if changeType == ChangeRename {
		score += 0.1
		factors = append(factors, "Rename change type")
	}

	// Factor: Co-change files
	if len(coChanges) > 5 {
		score += 0.1
		factors = append(factors, fmt.Sprintf("High coupling (%d co-change files)", len(coChanges)))
	}

	// Add existing factors
	factors = append(factors, existingFactors...)

	// Round at the output boundary: score is accumulated from binary
	// floats (0.25, 0.15, 0.2, ...) whose sum isn't exactly representable,
	// e.g. 0.15+0.2+0.15+0.2 prints as 0.7000000000000001 without this.
	// Two decimals is all the factor weights above carry meaning to
	// anyway. Level is derived from this same rounded value — deriving it
	// from the raw, unrounded score let the level disagree with the
	// displayed score at a threshold boundary (raw 0.7999999999999998
	// prints as "0.80" but would classify as "high", not "critical").
	roundedScore := math.Round(score*100) / 100

	// A rename or delete breaks every production caller left as it is, however
	// few there are, so it gets the floor assessChange applies to the same
	// change once it is in a diff (applyContractChangesToRisk). Without it the
	// two tools disagree on one change: prepareChange low, assessChange high.
	// modify has no floor: it can't tell a body edit from a signature change.
	// Implementations break on a rename of the interface method they implement,
	// not on its deletion (an extra method is no error) nor on a rename or delete
	// of the interface type (Go interfaces are satisfied structurally).
	broken := callers
	if changeType == ChangeRename && !strings.HasSuffix(target.SymbolId, "#") {
		broken += impls
	}
	if (changeType == ChangeDelete || changeType == ChangeRename) && broken > 0 && roundedScore < highRiskFloor {
		roundedScore = highRiskFloor
		factors = append(factors, fmt.Sprintf("%s breaks %s unless they change too", changeType, describeBroken(callers, broken-callers)))
	}

	// assessChange's bands, so the level before the edit is the one after it.
	level := riskLevelFor(roundedScore)

	return &PrepareRisk{
		Level:       level,
		Score:       roundedScore,
		Basis:       modifyRiskBasis(changeType, callers, impls, target.Visibility == "public", roundedScore),
		Factors:     factors,
		Suggestions: suggestions,
	}
}

// modifyRiskBasis says what a modify score assumes. Before the edit there is no
// telling a body change from a signature change, so the score is the one for an
// edit that keeps the declaration, counted from consumers. Once the edit is a
// diff, assessChange puts the floors of applyContractChangesToRisk on a changed
// declaration: high while production callers stay as they are, medium for an
// exported one without any. Where the score already reaches that floor there is
// nothing to qualify.
func modifyRiskBasis(changeType ChangeType, callers, impls int, public bool, score float64) string {
	if changeType != ChangeModify {
		return ""
	}
	const assumed = "Scored as an edit that keeps the declaration; assessChange rescores the real diff."
	switch {
	case callers+impls > 0 && score < highRiskFloor:
		return fmt.Sprintf("%s If the signature or type changes and the %s stay as they are, it is at least high (%.1f).", assumed, describeBroken(callers, impls), highRiskFloor)
	case callers+impls == 0 && public && score < mediumRiskFloor:
		return fmt.Sprintf("%s If the exported declaration changes, it is at least medium (%.1f): consumers in other repos can't be seen.", assumed, mediumRiskFloor)
	}
	return ""
}

// describeDependents names direct callers and implementations, the second only
// when there are any.
func describeDependents(callers, impls int) string {
	if impls == 0 {
		return fmt.Sprintf("%d direct callers", callers)
	}
	return fmt.Sprintf("%d direct callers, %d implementations", callers, impls)
}

// describeBroken names what a change breaks: production callers, implementations.
func describeBroken(callers, impls int) string {
	switch {
	case impls == 0:
		return fmt.Sprintf("%d production caller(s)", callers)
	case callers == 0:
		return fmt.Sprintf("%d implementation(s)", impls)
	}
	return fmt.Sprintf("%d production caller(s) and %d implementation(s)", callers, impls)
}

// =============================================================================
// Batch Operations
// =============================================================================

// BatchGetOptions controls batchGet behavior.
type BatchGetOptions struct {
	SymbolIds     []string // max 50
	IncludeCounts bool     // populate referenceCount, callerCount, calleeCount
}

// BatchGetResponse returns multiple symbols by ID.
type BatchGetResponse struct {
	AINavigationMeta
	Results map[string]*SymbolInfo `json:"results"`
	Errors  map[string]string      `json:"errors,omitempty"`
}

// BatchGet retrieves multiple symbols by ID in a single call.
func (e *Engine) BatchGet(ctx context.Context, opts BatchGetOptions) (*BatchGetResponse, error) {
	startTime := time.Now()

	// Limit to 50 symbols
	if len(opts.SymbolIds) > 50 {
		return nil, errors.NewCkbError(errors.InvalidParameter, "Maximum 50 symbols per batch", nil, nil, nil)
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	results := make(map[string]*SymbolInfo)
	errs := make(map[string]string)

	// Fetch symbols in parallel with limited concurrency
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 10) // Limit concurrency

	for _, id := range opts.SymbolIds {
		wg.Add(1)
		go func(symbolId string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			resp, err := e.GetSymbol(ctx, GetSymbolOptions{SymbolId: symbolId})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[symbolId] = err.Error()
			} else if resp.Symbol != nil {
				results[symbolId] = resp.Symbol
			} else {
				errs[symbolId] = "symbol not found"
			}
		}(id)
	}

	wg.Wait()

	// Populate reference/caller/callee counts if requested (parallel)
	if opts.IncludeCounts && e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		var countWg sync.WaitGroup
		countSem := make(chan struct{}, 10)
		for symId, info := range results {
			countWg.Add(1)
			go func(id string, si *SymbolInfo) {
				defer countWg.Done()
				countSem <- struct{}{}
				defer func() { <-countSem }()
				if refs, err := e.FindReferences(ctx, FindReferencesOptions{SymbolId: id, Limit: 500}); err == nil {
					si.ReferenceCount = refs.TotalCount
				}
				if cg, err := e.GetCallGraph(ctx, CallGraphOptions{SymbolId: id, Direction: "both", Depth: 1}); err == nil {
					for _, n := range cg.Nodes {
						switch n.Role {
						case "caller":
							si.CallerCount++
						case "callee":
							si.CalleeCount++
						}
					}
				}
			}(symId, info)
		}
		countWg.Wait()
	}

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId:   "scip",
			Available:   true,
			Used:        true,
			ResultCount: len(results),
		})
	}

	completeness := CompletenessInfo{
		Score:  float64(len(results)) / float64(len(opts.SymbolIds)),
		Reason: "batch-get",
	}

	return &BatchGetResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "batchGet",
			Provenance:    e.buildProvenance(repoState, "head", startTime, backendContribs, completeness),
		},
		Results: results,
		Errors:  errs,
	}, nil
}

// BatchSearchQuery represents a single search in a batch.
type BatchSearchQuery struct {
	Query string `json:"query"`
	Kind  string `json:"kind,omitempty"`
	Scope string `json:"scope,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// BatchSearchOptions controls batchSearch behavior.
type BatchSearchOptions struct {
	Queries []BatchSearchQuery
}

// BatchSearchResult contains results for a single search.
type BatchSearchResult struct {
	Query   string             `json:"query"`
	Symbols []SearchResultItem `json:"symbols"`
	Error   string             `json:"error,omitempty"`
}

// BatchSearchResponse returns multiple search results.
type BatchSearchResponse struct {
	AINavigationMeta
	Results []BatchSearchResult `json:"results"`
}

// BatchSearch performs multiple symbol searches in a single call.
func (e *Engine) BatchSearch(ctx context.Context, opts BatchSearchOptions) (*BatchSearchResponse, error) {
	startTime := time.Now()

	// Limit to 10 queries
	if len(opts.Queries) > 10 {
		return nil, errors.NewCkbError(errors.InvalidParameter, "Maximum 10 queries per batch", nil, nil, nil)
	}

	// Get repo state
	repoState, err := e.GetRepoState(ctx, "head")
	if err != nil {
		return nil, e.wrapError(err, errors.InternalError)
	}

	results := make([]BatchSearchResult, len(opts.Queries))

	// Search in parallel
	var wg sync.WaitGroup
	for i, q := range opts.Queries {
		wg.Add(1)
		go func(idx int, query BatchSearchQuery) {
			defer wg.Done()

			limit := query.Limit
			if limit <= 0 {
				limit = 10
			}

			var kinds []string
			if query.Kind != "" {
				kinds = []string{query.Kind}
			}

			resp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
				Query: query.Query,
				Scope: query.Scope,
				Kinds: kinds,
				Limit: limit,
			})

			if err != nil {
				results[idx] = BatchSearchResult{
					Query: query.Query,
					Error: err.Error(),
				}
			} else {
				results[idx] = BatchSearchResult{
					Query:   query.Query,
					Symbols: resp.Symbols,
				}
			}
		}(i, q)
	}

	wg.Wait()

	// Build provenance
	var backendContribs []BackendContribution
	if e.scipAdapter != nil && e.scipAdapter.IsAvailable() {
		backendContribs = append(backendContribs, BackendContribution{
			BackendId: "scip",
			Available: true,
			Used:      true,
		})
	}

	completeness := CompletenessInfo{
		Score:  0.9,
		Reason: "batch-search",
	}

	return &BatchSearchResponse{
		AINavigationMeta: AINavigationMeta{
			CkbVersion:    version.Version,
			SchemaVersion: 1,
			Tool:          "batchSearch",
			Provenance:    e.buildProvenance(repoState, "head", startTime, backendContribs, completeness),
		},
		Results: results,
	}, nil
}

// summarizeTransitive builds the caller summary for prepareChange. Spread
// covers the modules of direct AND transitive callers; counting only the
// transitive list left it at 0 for the common depth-1 case.
func summarizeTransitive(resp *AnalyzeImpactResponse) *PrepareTransitive {
	moduleSet := make(map[string]bool)
	maxDepth := 0
	impls := 0
	for _, imp := range resp.DirectImpact {
		if imp.Kind == kindImplementation {
			impls++
		}
	}
	for _, list := range [][]ImpactItem{resp.DirectImpact, resp.TransitiveImpact} {
		for _, imp := range list {
			if imp.ModuleId != "" {
				moduleSet[imp.ModuleId] = true
			}
			if imp.Distance > maxDepth {
				maxDepth = imp.Distance
			}
		}
	}
	return &PrepareTransitive{
		DirectCallers:            len(resp.DirectImpact) - impls,
		Implementations:          impls,
		NameMatchImplementations: len(resp.NameMatchImplementations),
		TotalCallers:             len(resp.TransitiveImpact) + len(resp.DirectImpact),
		ModuleSpread:             len(moduleSet),
		MaxDepth:                 maxDepth,
	}
}
