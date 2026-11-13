package deadcode

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	bindingscip "github.com/sourcegraph/scip/bindings/go/scip"

	"github.com/SimplyLiz/CodeMCP/internal/backends/scip"
)

// Analyzer detects dead code using SCIP index reference analysis.
type Analyzer struct {
	scipAdapter *scip.SCIPAdapter
	exclusions  *ExclusionRules
	logger      *slog.Logger
	repoRoot    string
}

// NewAnalyzer creates a new dead code analyzer.
func NewAnalyzer(scipAdapter *scip.SCIPAdapter, repoRoot string, logger *slog.Logger, excludePatterns []string) *Analyzer {
	return &Analyzer{
		scipAdapter: scipAdapter,
		exclusions:  NewExclusionRules(excludePatterns),
		logger:      logger,
		repoRoot:    repoRoot,
	}
}

// Analyze performs dead code detection with the given options.
func (a *Analyzer) Analyze(ctx context.Context, opts AnalyzerOptions) (*Result, error) {
	if a.scipAdapter == nil || !a.scipAdapter.IsAvailable() {
		return notAnalyzed("no SCIP index loaded; run 'ckb index'"), nil
	}

	// Get the underlying index for direct access
	index := a.scipAdapter.GetIndex()
	if index == nil {
		return notAnalyzed("SCIP index not loaded; run 'ckb index'"), nil
	}

	// Get all symbols
	allSymbols := a.scipAdapter.AllSymbols()
	if allSymbols == nil {
		a.logger.Debug("AllSymbols returned nil")
		return notAnalyzed("SCIP index has no symbols"), nil
	}

	a.logger.Debug("Starting dead code analysis",
		"totalSymbols", len(allSymbols),
		"includeExported", opts.IncludeExported,
		"includeUnexported", opts.IncludeUnexported,
		"scope", opts.Scope)

	var deadCode []DeadCodeItem
	totalAnalyzed := 0
	skippedNoId := 0
	skippedScope := 0
	skippedExport := 0
	skippedTest := 0
	skippedExclusion := 0

	rules := NewExportRules(a.repoRoot, index)
	skippedEntrypoint := 0
	skippedKind := 0
	skippedInterface := 0

	// A method nothing references by name is alive when its type satisfies
	// an interface someone uses. Reachable here means referenced.
	referenced := make(map[string]bool)
	isReferenced := func(symbol string) bool {
		if v, ok := referenced[symbol]; ok {
			return v
		}
		v := index.GetReferenceCount(symbol) > 0
		referenced[symbol] = v
		return v
	}
	interfaces := NewInterfaceReach(allSymbols, isReferenced)

	for _, sym := range allSymbols {
		// Skip if no symbol ID
		if sym.Symbol == "" || strings.HasPrefix(sym.Symbol, "local ") {
			skippedNoId++
			continue
		}

		// Parse symbol info
		symInfo := a.parseSymbolInfo(sym, rules)
		if symInfo.Name == "" || notADeclaration(symInfo.Kind) {
			skippedKind++
			continue
		}
		// `const f = () => …` has a term's ID and is a function.
		if symInfo.Kind == "term" && index.IsCallable(sym.Symbol) {
			symInfo.Kind = "function"
		}

		// A framework calls these without a reference in the index: a
		// Next.js page's default export, a route handler's GET.
		if opts.ExcludeSymbols[sym.Symbol] {
			skippedEntrypoint++
			continue
		}

		// Check if in scope
		if len(opts.Scope) > 0 && !a.isInScope(symInfo.FilePath, opts.Scope) {
			skippedScope++
			continue
		}

		// Check export filter
		if opts.IncludeExported && !symInfo.Exported {
			if !opts.IncludeUnexported {
				skippedExport++
				continue
			}
		}
		if opts.IncludeUnexported && symInfo.Exported {
			if !opts.IncludeExported {
				skippedExport++
				continue
			}
		}

		// Skip test files for dead code analysis (they test other code)
		if IsTestFile(symInfo.FilePath) {
			skippedTest++
			continue
		}

		// Build output and other people's code (.next/, .venv/)
		if IsHiddenPath(symInfo.FilePath) {
			skippedExclusion++
			continue
		}

		// Check exclusions
		if reason := a.exclusions.ShouldExclude(symInfo); reason != "" {
			skippedExclusion++
			continue
		}

		totalAnalyzed++

		// Get references
		refs, err := index.FindReferences(sym.Symbol, scip.ReferenceOptions{
			IncludeDefinition: false,
			IncludeTests:      true,
		})
		if err != nil {
			a.logger.Debug("Error finding references",
				"symbol", sym.Symbol,
				"error", err.Error())
			continue
		}

		// Categorize references
		stats := a.categorizeReferences(refs, sym.Symbol, symInfo.FilePath)

		// Classify as dead or not
		item, isDead := a.classifySymbol(symInfo, stats, opts)
		if isDead && symInfo.Kind == "method" && interfaces.Via(sym) != "" {
			skippedInterface++
			continue
		}
		if isDead && item.Confidence >= opts.MinConfidence {
			deadCode = append(deadCode, item)
		}
	}

	// Sort by confidence (highest first), then by file path
	sort.Slice(deadCode, func(i, j int) bool {
		if deadCode[i].Confidence != deadCode[j].Confidence {
			return deadCode[i].Confidence > deadCode[j].Confidence
		}
		return deadCode[i].FilePath < deadCode[j].FilePath
	})

	// Apply limit
	if opts.Limit > 0 && len(deadCode) > opts.Limit {
		deadCode = deadCode[:opts.Limit]
	}

	a.logger.Debug("Dead code analysis completed",
		"totalAnalyzed", totalAnalyzed,
		"deadCodeFound", len(deadCode),
		"skippedNoId", skippedNoId,
		"skippedScope", skippedScope,
		"skippedExport", skippedExport,
		"skippedTest", skippedTest,
		"skippedExclusion", skippedExclusion,
		"skippedKind", skippedKind,
		"skippedEntrypoint", skippedEntrypoint,
		"skippedInterface", skippedInterface)

	return &Result{
		DeadCode: deadCode,
		Summary:  a.computeSummary(deadCode, totalAnalyzed),
		Scope:    opts.Scope,
		Analyzed: true,
	}, nil
}

// parseSymbolInfo extracts information from a SCIP symbol.
func (a *Analyzer) parseSymbolInfo(sym *scip.SymbolInformation, rules *ExportRules) SymbolInfo {
	name := sym.DisplayName
	// If DisplayName is empty, try to extract from the symbol ID
	if name == "" {
		name = ExtractName(sym.Symbol)
	}

	// Kind is optional in SCIP and scip-typescript leaves it out; the
	// descriptor suffix says what the symbol is.
	kind := kindToString(sym.Kind)
	if kind == "unknown" {
		kind = kindFromID(sym.Symbol)
	}

	info := SymbolInfo{
		SymbolID: sym.Symbol,
		Name:     name,
		Kind:     kind,
		Exported: false,
	}

	// Parse documentation
	if len(sym.Documentation) > 0 {
		info.Documentation = strings.Join(sym.Documentation, "\n")
	}

	// The definition occurrence is the reliable source for the file: the ID
	// of a scip-typescript symbol quotes the path in backticks and carries
	// no line at all.
	if rules != nil {
		info.FilePath, info.LineNumber = rules.Site(sym.Symbol)
	}
	if info.FilePath == "" {
		// SCIP symbol format: scip-go gomod ... pkg/path/file.go/SymbolName
		info.FilePath = extractFilePathFromSymbol(sym.Symbol)
	}

	// Exported: `export` in TypeScript, `pub` in Rust, no underscore in
	// Python, a capital letter in Go. Locals and parameters never are.
	switch {
	case kind == "local" || kind == "parameter":
		info.Exported = false
	case rules != nil:
		info.Exported = rules.Exported(sym.Symbol, name)
	default:
		info.Exported = isExported(name, kind)
	}

	return info
}

// kindFromID names a symbol's kind from its descriptors: "method" for
// Type#name()., "function" for name()., "type" for Type#, "field" for Type#name.,
// "constant" for an ALL_CAPS term, "term" for any other top-level one. A meta
// descriptor anywhere (an object literal or type literal member, a module scope)
// makes it "meta", a parameter descriptor "parameter", a local "local".
func kindFromID(symbolID string) string {
	sym, err := bindingscip.ParseSymbol(symbolID)
	if err != nil || len(sym.Descriptors) == 0 {
		return "unknown"
	}
	descs := sym.Descriptors
	nested := ""
	for _, d := range descs {
		switch d.Suffix {
		case bindingscip.Descriptor_Meta:
			return "meta"
		case bindingscip.Descriptor_Parameter, bindingscip.Descriptor_TypeParameter:
			nested = "parameter"
		case bindingscip.Descriptor_Local:
			nested = "local"
		}
	}
	if nested != "" {
		return nested
	}
	last := descs[len(descs)-1]
	ownedByType := len(descs) >= 2 && descs[len(descs)-2].Suffix == bindingscip.Descriptor_Type
	switch last.Suffix {
	case bindingscip.Descriptor_Method:
		if ownedByType {
			return "method"
		}
		return "function"
	case bindingscip.Descriptor_Type:
		return "type"
	case bindingscip.Descriptor_Term:
		if ownedByType {
			return "field"
		}
		if len(last.Name) > 1 && last.Name == strings.ToUpper(last.Name) {
			return "constant"
		}
		return "term"
	case bindingscip.Descriptor_Namespace:
		return "package"
	case bindingscip.Descriptor_Macro:
		return "macro"
	}
	return "unknown"
}

// notADeclaration reports the kinds dead code analysis has nothing to say
// about. A parameter, a local and a file's module symbol are not referenced
// by name. A meta descriptor is what scip-typescript writes for every object
// and type literal member ({ ArrowDown: … } becomes ArrowDown0:) and
// scip-python for a module's top-level scope. A field or property is data:
// set in a literal, read through JSON or a computed key, none of which the
// index records as a reference. An interface's method specification is a
// contract its callers reference, not code that runs.
func notADeclaration(kind string) bool {
	switch kind {
	case "parameter", "local", "package", "meta", "field", "method_spec":
		return true
	}
	return false
}

// extractFilePathFromSymbol extracts the file path from a SCIP symbol ID.
func extractFilePathFromSymbol(symbolID string) string {
	// SCIP symbol format varies, but we can often find the file path
	// For Go: scip-go gomod example.com/pkg v1.0.0 path/to/file.go/SymbolName
	parts := strings.Split(symbolID, " ")
	for _, part := range parts {
		if strings.HasSuffix(part, ".go") ||
			strings.HasSuffix(part, ".ts") ||
			strings.HasSuffix(part, ".js") ||
			strings.HasSuffix(part, ".py") {
			// Extract up to the file extension
			idx := strings.LastIndex(part, "/")
			if idx > 0 {
				// Check if this looks like a path
				for i := idx + 1; i < len(part); i++ {
					if part[i] == '/' {
						return part[:i]
					}
				}
			}
			return part
		}
	}

	// Fallback: try to find anything that looks like a path
	for _, part := range parts {
		if strings.Contains(part, "/") && !strings.HasPrefix(part, "scip-") {
			return part
		}
	}

	return ""
}

// isExported determines if a symbol is exported based on language conventions.
func isExported(name, kind string) bool {
	if name == "" {
		return false
	}

	// Skip local variables and parameters
	if kind == "variable" || kind == "parameter" {
		return false
	}

	// Go convention: uppercase first letter means exported
	firstChar := rune(name[0])
	return firstChar >= 'A' && firstChar <= 'Z'
}

// kindToString names a SCIP SymbolInformation kind in the vocabulary the
// exclusion rules use, or "unknown" when the indexer wrote none (scip-typescript
// never does) or one that says nothing about dead code.
func kindToString(kind int32) string {
	switch bindingscip.SymbolInformation_Kind(kind) {
	case bindingscip.SymbolInformation_Method, bindingscip.SymbolInformation_StaticMethod,
		bindingscip.SymbolInformation_SingletonMethod, bindingscip.SymbolInformation_TraitMethod,
		bindingscip.SymbolInformation_TypeClassMethod, bindingscip.SymbolInformation_Constructor,
		bindingscip.SymbolInformation_Getter, bindingscip.SymbolInformation_Setter,
		bindingscip.SymbolInformation_Accessor, bindingscip.SymbolInformation_MethodAlias:
		return "method"
	case bindingscip.SymbolInformation_MethodSpecification, bindingscip.SymbolInformation_AbstractMethod,
		bindingscip.SymbolInformation_PureVirtualMethod:
		return "method_spec"
	case bindingscip.SymbolInformation_Function, bindingscip.SymbolInformation_Macro,
		bindingscip.SymbolInformation_Delegate:
		return "function"
	case bindingscip.SymbolInformation_Class, bindingscip.SymbolInformation_Struct,
		bindingscip.SymbolInformation_Interface, bindingscip.SymbolInformation_Enum,
		bindingscip.SymbolInformation_Type, bindingscip.SymbolInformation_TypeAlias,
		bindingscip.SymbolInformation_Trait, bindingscip.SymbolInformation_Union,
		bindingscip.SymbolInformation_Protocol, bindingscip.SymbolInformation_Object,
		bindingscip.SymbolInformation_Message, bindingscip.SymbolInformation_Contract,
		bindingscip.SymbolInformation_Mixin, bindingscip.SymbolInformation_SingletonClass,
		bindingscip.SymbolInformation_TypeClass, bindingscip.SymbolInformation_AssociatedType,
		bindingscip.SymbolInformation_TypeFamily, bindingscip.SymbolInformation_DataFamily:
		return "type"
	case bindingscip.SymbolInformation_Constant, bindingscip.SymbolInformation_EnumMember,
		bindingscip.SymbolInformation_Value:
		return "constant"
	case bindingscip.SymbolInformation_Variable, bindingscip.SymbolInformation_StaticVariable:
		return "variable"
	case bindingscip.SymbolInformation_Field, bindingscip.SymbolInformation_StaticField,
		bindingscip.SymbolInformation_Property, bindingscip.SymbolInformation_StaticProperty,
		bindingscip.SymbolInformation_StaticDataMember, bindingscip.SymbolInformation_Key:
		return "field"
	case bindingscip.SymbolInformation_Parameter, bindingscip.SymbolInformation_SelfParameter,
		bindingscip.SymbolInformation_ThisParameter, bindingscip.SymbolInformation_ParameterLabel,
		bindingscip.SymbolInformation_TypeParameter, bindingscip.SymbolInformation_MethodReceiver:
		return "parameter"
	case bindingscip.SymbolInformation_Package, bindingscip.SymbolInformation_Namespace,
		bindingscip.SymbolInformation_Module, bindingscip.SymbolInformation_Library,
		bindingscip.SymbolInformation_File, bindingscip.SymbolInformation_PackageObject:
		return "package"
	}
	return "unknown"
}

// isInScope checks if a file path is within the given scope.
func (a *Analyzer) isInScope(filePath string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}

	for _, s := range scope {
		if strings.HasPrefix(filePath, s) {
			return true
		}
		// Also check with trailing slash
		if strings.HasPrefix(filePath, s+"/") {
			return true
		}
	}
	return false
}

// categorizeReferences categorizes references to a symbol.
func (a *Analyzer) categorizeReferences(refs []*scip.SCIPReference, symbolID, symbolFilePath string) ReferenceStats {
	stats := ReferenceStats{}

	symbolDir := filepath.Dir(symbolFilePath)

	for _, ref := range refs {
		if ref == nil || ref.Location == nil {
			continue
		}

		stats.Total++
		refPath := ref.Location.FileId

		// Self-reference: a use inside the symbol's own body (recursion).
		// A use elsewhere in the same file is a use.
		if ref.FromSymbol != "" && ref.FromSymbol == symbolID {
			stats.FromSelf++
			continue
		}

		// Test file check
		if IsTestFile(refPath) {
			stats.FromTests++
			continue
		}

		// External vs internal check
		refDir := filepath.Dir(refPath)
		if refDir != symbolDir {
			stats.External++
		} else {
			stats.Internal++
		}
	}

	return stats
}

// classifySymbol determines if a symbol is dead and returns the classification.
func (a *Analyzer) classifySymbol(sym SymbolInfo, stats ReferenceStats, opts AnalyzerOptions) (DeadCodeItem, bool) {
	item := DeadCodeItem{
		SymbolID:       sym.SymbolID,
		SymbolName:     sym.Name,
		Kind:           sym.Kind,
		FilePath:       sym.FilePath,
		LineNumber:     sym.LineNumber,
		ReferenceCount: stats.Total,
		TestReferences: stats.FromTests,
		SelfReferences: stats.FromSelf,
		Exported:       sym.Exported,
	}

	// Calculate non-self, non-definition references
	nonSelfRefs := stats.Total - stats.FromSelf

	// Zero references (excluding self) = definitely dead
	if nonSelfRefs == 0 {
		if stats.FromSelf > 0 {
			item.Category = CategorySelfOnly
			item.Reason = "Only referenced by itself (recursive but never called)"
			item.Confidence = 0.95
		} else {
			item.Category = CategoryZeroRefs
			item.Reason = "No references found"
			item.Confidence = 0.99
		}
		return item, true
	}

	// Only test references (unless excluded)
	if !opts.ExcludeTestOnly && stats.FromTests == nonSelfRefs {
		item.Category = CategoryTestOnly
		item.Reason = "Only referenced from test files"
		item.Confidence = 0.75
		return item, true
	}

	// Exported but only used internally
	if sym.Exported && stats.External == 0 && stats.Internal > 0 {
		item.Category = CategoryInternalExport
		item.Reason = "Exported but only used within same package"
		item.Confidence = 0.60
		return item, true
	}

	return item, false
}

// computeSummary calculates aggregate statistics for the results.
func (a *Analyzer) computeSummary(deadCode []DeadCodeItem, totalAnalyzed int) DeadCodeSummary {
	summary := DeadCodeSummary{
		TotalSymbols: totalAnalyzed,
		DeadCount:    0,
		ByKind:       make(map[string]int),
		ByCategory:   make(map[string]int),
	}

	estimatedLines := 0
	for _, item := range deadCode {
		if item.Confidence >= 0.9 {
			summary.DeadCount++
		} else {
			summary.SuspiciousCount++
		}

		summary.ByKind[item.Kind]++
		summary.ByCategory[string(item.Category)]++

		// Rough estimate: functions ~20 lines, types ~30 lines, others ~5 lines
		switch item.Kind {
		case "function", "method":
			estimatedLines += 20
		case "type", "class", "interface":
			estimatedLines += 30
		default:
			estimatedLines += 5
		}
	}

	summary.EstimatedLines = estimatedLines
	return summary
}
