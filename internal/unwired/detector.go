package unwired

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/architecture"
	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
	"github.com/SimplyLiz/CodeMCP/internal/deadcode"
)

const (
	// defaultMaxNodes bounds the reachable set. It is a stop against runaway
	// walks, not a sample size: a set cut here makes everything not yet
	// reached look unwired, so it sits well above the symbol count of a large
	// repo. CKB itself has about 20,000 symbols.
	defaultMaxNodes = 100000
	defaultLimit    = 100
)

// Detector finds exported symbols that are never reachable from entrypoints.
type Detector struct {
	scipAdapter *scip.SCIPAdapter
	repoRoot    string
	logger      *slog.Logger
}

// NewDetector creates a new unwired module detector.
func NewDetector(scipAdapter *scip.SCIPAdapter, repoRoot string, logger *slog.Logger) *Detector {
	return &Detector{
		scipAdapter: scipAdapter,
		repoRoot:    repoRoot,
		logger:      logger,
	}
}

// BuildReachableSet performs BFS from entrypoint symbols to build the set of all
// transitively reachable symbol IDs. maxNodes caps the set; 0 means the default.
// The second result is true when the cap cut the walk short.
func (d *Detector) BuildReachableSet(ctx context.Context, entrypoints []architecture.Entrypoint, maxNodes int) (map[string]bool, bool) {
	index := d.scipAdapter.GetIndex()
	if index == nil {
		return nil, false
	}

	reachable := make(map[string]bool)
	queue := make([]string, 0, 256)
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	partial := false

	// Also detect additional entrypoints from package.json scripts
	extraEntrypoints := d.DetectScriptEntrypoints()
	allEntrypoints := append(entrypoints, extraEntrypoints...)

	// Seed the BFS queue from entrypoint files.
	// We seed with ALL symbols in entrypoint files (definitions AND references)
	// because entrypoint files often just import and call other modules (e.g.,
	// barrel files, server startup). Only seeding definitions would miss the
	// actual pipeline wiring.
	for _, ep := range allEntrypoints {
		if ep.Kind == architecture.EntrypointTest {
			continue
		}

		doc := index.GetDocument(ep.FileId)
		if doc == nil {
			doc = index.GetDocument(strings.TrimPrefix(ep.FileId, d.repoRoot+"/"))
		}
		if doc == nil {
			d.logger.Debug("Entrypoint document not found in SCIP index", "file", ep.FileId)
			continue
		}

		// Collect ALL function symbols from the entrypoint file —
		// both definitions and references. This ensures that imports like
		// `import { createApp } from './app'` followed by `createApp()`
		// seed the BFS with `createApp`.
		for _, occ := range doc.Occurrences {
			if isLocal(occ.Symbol) {
				continue
			}
			if index.IsCallable(occ.Symbol) {
				if !reachable[occ.Symbol] {
					reachable[occ.Symbol] = true
					queue = append(queue, occ.Symbol)
				}
			}
		}
	}

	d.logger.Debug("BFS seed", "entrypoints", len(allEntrypoints), "seedSymbols", len(queue))

	// Phase 1: Import-graph BFS — follow file-level imports transitively.
	// For each reachable symbol, find which file it's defined in, then add
	// ALL symbols referenced from that file to the reachable set. This
	// traverses module boundaries through imports, which is critical for
	// TypeScript/Python where the SCIP call graph is shallow.
	visitedFiles := make(map[string]bool)

	// Seed visited files from entrypoints
	for _, ep := range allEntrypoints {
		if ep.Kind != architecture.EntrypointTest {
			visitedFiles[ep.FileId] = true
			trimmed := strings.TrimPrefix(ep.FileId, d.repoRoot+"/")
			if trimmed != ep.FileId {
				visitedFiles[trimmed] = true
			}
		}
	}

	fileQueue := make([]string, 0, len(visitedFiles))
	for f := range visitedFiles {
		fileQueue = append(fileQueue, f)
	}
	sort.Strings(fileQueue)
	inits := initFiles(index)

	for len(fileQueue) > 0 {
		if ctx.Err() != nil {
			partial = true
			break
		}
		if len(reachable) >= maxNodes {
			partial = true
			d.logger.Debug("Reachable set budget exhausted (import BFS)", "maxNodes", maxNodes)
			break
		}

		filePath := fileQueue[0]
		fileQueue = fileQueue[1:]

		doc := index.GetDocument(filePath)
		if doc == nil {
			continue
		}

		// A Go package loads whole: when one of its files is reached, every
		// file with an init runs, referenced or not.
		if strings.HasSuffix(filePath, ".go") {
			for _, initFile := range inits[filepath.Dir(filePath)] {
				if !visitedFiles[initFile] {
					visitedFiles[initFile] = true
					fileQueue = append(fileQueue, initFile)
				}
			}
		}

		// Collect all symbols referenced in this file. A definition is not a
		// use: counting it would make everything defined in a file reachable
		// as soon as one of its symbols is, so an unused export next to a
		// used one would never show.
		for _, occ := range doc.Occurrences {
			if occ.Symbol == "" || isLocal(occ.Symbol) || occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				continue
			}

			// Mark symbol as reachable
			if !reachable[occ.Symbol] {
				reachable[occ.Symbol] = true
				queue = append(queue, occ.Symbol) // also feed into call-graph BFS
			}

			// If this is an import or reference to a symbol defined in another file,
			// add that file to the file queue
			if loc := findSymbolFile(occ.Symbol, index); loc != "" {
				if !visitedFiles[loc] {
					visitedFiles[loc] = true
					fileQueue = append(fileQueue, loc)
				}
			}
		}
	}

	d.logger.Debug("After import-graph BFS", "reachableSymbols", len(reachable), "visitedFiles", len(visitedFiles))

	// Phase 2: Call-graph BFS — walk callees for any remaining symbols
	// in the queue that have callee edges. This adds depth for Go and
	// other languages where SCIP has rich call graph data.
	for len(queue) > 0 {
		if ctx.Err() != nil {
			partial = true
			break
		}
		if len(reachable) >= maxNodes {
			partial = true
			d.logger.Debug("Reachable set budget exhausted (callee BFS)", "maxNodes", maxNodes)
			break
		}

		sym := queue[0]
		queue = queue[1:]

		callees, err := index.FindCallees(sym)
		if err != nil {
			continue
		}

		for _, callee := range callees {
			if !reachable[callee.SymbolID] {
				reachable[callee.SymbolID] = true
				queue = append(queue, callee.SymbolID)
			}
		}
	}

	d.logger.Debug("Reachable set built", "size", len(reachable), "partial", partial)
	return reachable, partial
}

// Analyze finds unwired exported symbols not in the reachable set.
func (d *Detector) Analyze(ctx context.Context, opts DetectorOptions, reachable map[string]bool) (*Result, error) {
	if reachable == nil {
		return &Result{
			Summary: Summary{ByKind: make(map[string]int)},
		}, nil
	}

	index := d.scipAdapter.GetIndex()
	if index == nil {
		return &Result{
			Summary: Summary{ByKind: make(map[string]int)},
		}, nil
	}

	allSymbols := d.scipAdapter.AllSymbols()
	if allSymbols == nil {
		return &Result{
			Summary: Summary{ByKind: make(map[string]int)},
		}, nil
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	minConf := opts.MinConfidence
	if minConf <= 0 {
		minConf = 0.80
	}

	exclusions := deadcode.NewExclusionRules(opts.ExcludePatterns)
	rules := deadcode.NewExportRules(d.repoRoot, index)
	interfaces := deadcode.NewInterfaceReach(allSymbols, func(symbol string) bool { return reachable[symbol] })
	viaInterface := 0

	// Group items by module (directory)
	moduleItems := make(map[string][]UnwiredItem)
	moduleExported := make(map[string]int)
	totalExported := 0
	totalUnwired := 0

	for _, sym := range allSymbols {
		if ctx.Err() != nil {
			break
		}

		info := parseSymbolInfo(sym, rules)
		if info == nil {
			continue
		}

		// Skip non-exported
		if !info.exported {
			continue
		}

		// Skip test files
		if deadcode.IsTestFile(info.filePath) {
			continue
		}

		// Skip generated files and anything under a hidden directory (.next/)
		if isGeneratedFile(info.filePath) || deadcode.IsHiddenPath(info.filePath) {
			continue
		}

		// Apply scope filter
		if len(opts.Scope) > 0 && !inScope(info.filePath, opts.Scope) {
			continue
		}

		// Skip non-callable symbols unless IncludeTypes is set
		callable := index.IsCallable(sym.Symbol)
		if callable && info.kind != "function" {
			info.kind = "function" // `const f = () => …` has a constant's ID
		}
		if !callable && !opts.IncludeTypes {
			continue
		}
		kind := info.kind
		if callable && strings.Contains(sym.Symbol, "#") {
			kind = "method"
		}

		// Skip exclusions (main, init, common interface methods, etc.)
		excl := exclusions.ShouldExclude(deadcode.SymbolInfo{
			Name:     info.name,
			Kind:     kind,
			FilePath: info.filePath,
			Exported: info.exported,
		})
		if excl != "" {
			continue
		}

		module := filepath.Dir(info.filePath)
		moduleExported[module]++
		totalExported++

		// Check if reachable
		if reachable[sym.Symbol] {
			continue
		}
		if kind == "method" && interfaces.Via(sym) != "" {
			viaInterface++
			continue
		}

		// This symbol is unwired — classify it
		confidence, reason := d.classifyUnwired(sym, index)
		if confidence < minConf {
			continue
		}

		item := UnwiredItem{
			SymbolID:       sym.Symbol,
			SymbolName:     info.name,
			Kind:           kind,
			FilePath:       info.filePath,
			LineNumber:     info.lineNumber,
			Module:         module,
			ReferenceCount: d.scipAdapter.GetReferenceCount(sym.Symbol),
			Confidence:     confidence,
			Reason:         reason,
			Exported:       true,
		}

		// Count test references
		item.TestReferences = d.countTestReferences(sym.Symbol, index)

		moduleItems[module] = append(moduleItems[module], item)
		totalUnwired++
	}

	// Build result grouped by module
	var modules []UnwiredModule
	byKind := make(map[string]int)

	for modPath, items := range moduleItems {
		// Sort by confidence desc, then by name so the order is stable
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Confidence != items[j].Confidence {
				return items[i].Confidence > items[j].Confidence
			}
			return items[i].SymbolName < items[j].SymbolName
		})

		for _, item := range items {
			byKind[item.Kind]++
		}

		modules = append(modules, UnwiredModule{
			Path:  modPath,
			Items: items,
			Summary: ModuleSummary{
				TotalExported: moduleExported[modPath],
				UnwiredCount:  len(items),
			},
		})
	}

	// Sort modules by unwired count desc, then by path
	sort.SliceStable(modules, func(i, j int) bool {
		if modules[i].Summary.UnwiredCount != modules[j].Summary.UnwiredCount {
			return modules[i].Summary.UnwiredCount > modules[j].Summary.UnwiredCount
		}
		return modules[i].Path < modules[j].Path
	})

	listed, truncated := cutToLimit(modules, limit)

	return &Result{
		UnwiredModules: listed,
		Summary: Summary{
			TotalExported:  totalExported,
			ReachableCount: len(reachable),
			UnwiredCount:   totalUnwired,
			UnwiredModules: len(modules),
			ViaInterface:   viaInterface,
			ByKind:         byKind,
		},
		ReachableCount: len(reachable),
		Truncated:      truncated,
	}, nil
}

// cutToLimit keeps at most limit items, taken one per module in turn so
// every module keeps its top items and the result shows the breadth rather
// than the first module alone. Each module's summary keeps its full count.
func cutToLimit(modules []UnwiredModule, limit int) ([]UnwiredModule, bool) {
	total := 0
	for _, mod := range modules {
		total += len(mod.Items)
	}
	if limit <= 0 || total <= limit {
		return modules, false
	}
	keep := make([]int, len(modules))
	for left := limit; left > 0; {
		gave := false
		for i, mod := range modules {
			if left == 0 {
				break
			}
			if keep[i] < len(mod.Items) {
				keep[i]++
				left--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	var listed []UnwiredModule
	for i, mod := range modules {
		if keep[i] == 0 {
			continue
		}
		mod.Items = mod.Items[:keep[i]]
		listed = append(listed, mod)
	}
	return listed, true
}

// classifyUnwired determines confidence and reason for an unwired symbol.
func (d *Detector) classifyUnwired(sym *scip.SymbolInformation, index *scip.SCIPIndex) (float64, string) {
	refCount := index.GetReferenceCount(sym.Symbol)
	testRefs := d.countTestReferences(sym.Symbol, index)

	switch {
	case refCount == 0:
		// No references at all — also dead code, but we still flag it
		return 0.95, "exported with no references and not reachable from any entrypoint"
	case testRefs == refCount:
		// Only referenced from tests
		return 0.95, "only referenced from tests, not reachable from any entrypoint"
	case refCount > 0 && testRefs < refCount:
		// Has non-test references but still not reachable — the LLMRouter pattern
		// Other modules reference it, but nothing connects to the pipeline
		return 0.85, "has references but not transitively reachable from any entrypoint"
	default:
		return 0.80, "not reachable from any entrypoint"
	}
}

// countTestReferences counts how many references to a symbol are from test files.
func (d *Detector) countTestReferences(symbolID string, index *scip.SCIPIndex) int {
	count := 0
	refs, err := index.FindReferences(symbolID, scip.ReferenceOptions{IncludeDefinition: false, IncludeTests: true})
	if err != nil {
		return 0
	}
	for _, ref := range refs {
		if deadcode.IsTestFile(ref.Location.FileId) {
			count++
		}
	}
	return count
}

// DetectScriptEntrypoints finds additional entrypoints from package.json scripts.
// This catches patterns like `"dev": "bun run src/server.ts"` that the standard
// entrypoint detection misses.
func (d *Detector) DetectScriptEntrypoints() []architecture.Entrypoint {
	packageJSON := filepath.Join(d.repoRoot, "package.json")
	data, err := os.ReadFile(packageJSON)
	if err != nil {
		return nil
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var entrypoints []architecture.Entrypoint

	// Look for file paths in script commands (dev, start, serve, main)
	for name, cmd := range pkg.Scripts {
		if name == "test" || name == "lint" || name == "lint:fix" ||
			name == "format" || name == "check" || name == "build" {
			continue
		}

		// Extract .ts/.js file paths from the command
		for _, part := range strings.Fields(cmd) {
			if (strings.HasSuffix(part, ".ts") || strings.HasSuffix(part, ".js")) &&
				!strings.HasPrefix(part, "-") && !strings.HasPrefix(part, "node_modules") {
				if !seen[part] {
					seen[part] = true
					kind := architecture.EntrypointServer
					if strings.Contains(name, "cli") || name == "bin" {
						kind = architecture.EntrypointCLI
					}
					entrypoints = append(entrypoints, architecture.Entrypoint{
						FileId: part,
						Name:   filepath.Base(part),
						Kind:   kind,
					})
				}
			}
		}
	}

	return entrypoints
}

// isLocal says whether a symbol is a document-local one (`local 3`): a
// parameter or a variable in a body, which means nothing across files and
// would fill the reachable set with collisions.
func isLocal(symbol string) bool {
	return strings.HasPrefix(symbol, "local ")
}

// --- Symbol parsing helpers ---

type symbolInfo struct {
	name       string
	kind       string
	filePath   string
	lineNumber int
	exported   bool
}

func parseSymbolInfo(sym *scip.SymbolInformation, rules *deadcode.ExportRules) *symbolInfo {
	if sym == nil || sym.Symbol == "" {
		return nil
	}

	name := sym.DisplayName
	if name == "" {
		name = deadcode.ExtractName(sym.Symbol)
	}
	if name == "" {
		return nil
	}

	// Find definition location
	filePath := ""
	lineNumber := 0
	// Symbol ID contains the file path for scip-go: "scip-go gomod pkg/file.go/SymbolName"
	parts := strings.Fields(sym.Symbol)
	if len(parts) >= 3 {
		// Extract path from the descriptor portion
		for _, p := range parts[2:] {
			if strings.Contains(p, "/") && (strings.HasSuffix(p, ".go") || strings.Contains(p, ".go/") ||
				strings.HasSuffix(p, ".ts") || strings.Contains(p, ".ts/") ||
				strings.HasSuffix(p, ".py") || strings.Contains(p, ".py/") ||
				strings.HasSuffix(p, ".rs") || strings.Contains(p, ".rs/")) {
				// Extract just the file path portion
				if idx := strings.Index(p, ".go/"); idx >= 0 {
					filePath = p[:idx+3]
				} else if idx := strings.Index(p, ".ts/"); idx >= 0 {
					filePath = p[:idx+3]
				} else if idx := strings.Index(p, ".py/"); idx >= 0 {
					filePath = p[:idx+3]
				} else if idx := strings.Index(p, ".rs/"); idx >= 0 {
					filePath = p[:idx+3]
				} else {
					filePath = p
				}
				break
			}
		}
	}

	// The definition occurrence is the reliable source: rust-analyzer IDs
	// carry no file path at all, and none of the IDs carry a line.
	if path, line := rules.Site(sym.Symbol); path != "" {
		filePath, lineNumber = path, line
	}

	kind := "type"
	if scip.IsFunctionSymbol(sym.Symbol) {
		kind = "function"
	}

	exported := rules.Exported(sym.Symbol, name)

	return &symbolInfo{
		name:       name,
		kind:       kind,
		filePath:   filePath,
		lineNumber: lineNumber,
		exported:   exported,
	}
}

// findSymbolFile returns the file path where a symbol is defined.
// Returns empty string if not found.
func findSymbolFile(symbolID string, index *scip.SCIPIndex) string {
	// The definition occurrence is the one source every indexer has. scip-go
	// puts the package path in the ID, not the file, and the guess from the ID
	// below used to read `example.com/m/pkg` as a TypeScript file name, so no
	// Go reference ever led into the file that defines it.
	if index.DefinitionIndex != nil {
		if ref := index.DefinitionIndex[symbolID]; ref != nil && ref.Doc != nil {
			return ref.Doc.RelativePath
		}
		// Every definition in the index is in that map, so a symbol missing
		// from it is defined outside the repo: the standard library, a
		// dependency. Scanning the documents for it would find nothing and
		// cost a pass over the index per external reference.
		return ""
	}

	// Next: the file as the symbol ID spells it, e.g.
	//   scip-typescript npm pkg 1.0.0 src/router/`app.ts`/createApp().
	if sym := index.GetSymbol(symbolID); sym != nil {
		if path := extractFileFromSymbolID(symbolID); path != "" {
			return path
		}
	}

	// Slow path: scan documents for definition occurrence
	for _, doc := range index.Documents {
		for _, occ := range doc.Occurrences {
			if occ.Symbol == symbolID && occ.SymbolRoles&scip.SymbolRoleDefinition != 0 {
				return doc.RelativePath
			}
		}
	}
	return ""
}

// extractFileFromSymbolID extracts the file path from a SCIP symbol ID.
// TypeScript: scip-typescript npm pkg 1.0.0 src/router/`app.ts`/createApp().
// Go: scip-go gomod github.com/pkg v1.0.0 src/file.go/Symbol().
func extractFileFromSymbolID(symbolID string) string {
	// TypeScript pattern: backtick-quoted file names. scip-go quotes the
	// package path the same way; a path with a slash in it is not a file name.
	if idx := strings.Index(symbolID, "`"); idx >= 0 {
		end := strings.Index(symbolID[idx+1:], "`")
		if end >= 0 && !strings.Contains(symbolID[idx+1:idx+1+end], "/") {
			// Extract the path prefix before the backtick + the filename
			// e.g., "scip-typescript npm llmrouter 0.1.0 src/router/`app.ts`/createApp()."
			// We want "src/router/app.ts"
			prefix := symbolID[:idx]
			fileName := symbolID[idx+1 : idx+1+end]

			// Find the last space before the backtick to get the path start
			parts := strings.Fields(prefix)
			if len(parts) > 0 {
				dirPart := parts[len(parts)-1]
				return dirPart + fileName
			}
		}
	}

	// Go pattern: look for .go/ in the symbol ID
	for _, ext := range []string{".go/", ".ts/", ".py/", ".rs/", ".js/"} {
		if idx := strings.Index(symbolID, ext); idx >= 0 {
			// Walk backward to find the start of the path
			pathEnd := idx + len(ext) - 1 // include the extension, not the trailing /
			pathPart := symbolID[:pathEnd]
			// Find the last space (after version)
			if spaceIdx := strings.LastIndex(pathPart, " "); spaceIdx >= 0 {
				return pathPart[spaceIdx+1:]
			}
		}
	}

	return ""
}

func isGeneratedFile(path string) bool {
	base := filepath.Base(path)
	return strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, "_generated.go") ||
		strings.HasSuffix(base, "_gen.go") ||
		strings.HasPrefix(base, "zz_generated") ||
		base == "wire_gen.go"
}

func inScope(filePath string, scope []string) bool {
	for _, s := range scope {
		if strings.HasPrefix(filePath, s) {
			return true
		}
	}
	return false
}
