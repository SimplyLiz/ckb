// Package testdata provides synthetic fixtures for deterministic NFR testing.
// These fixtures allow token budget tests to run in CI without a SCIP index.
package testdata

import (
	"fmt"
	"strings"
)

// Fixture size tiers
const (
	TierSmall  = "small"
	TierMedium = "medium"
	TierLarge  = "large"
)

// SymbolFixture represents a synthetic symbol for testing.
type SymbolFixture struct {
	StableID    string
	Name        string
	Kind        string
	FilePath    string
	Line        int
	Description string
}

// ReferenceFixture represents a synthetic reference.
type ReferenceFixture struct {
	FilePath string
	Line     int
	Column   int
	Kind     string
}

// HotspotFixture represents a synthetic hotspot.
type HotspotFixture struct {
	FilePath string
	Score    float64
	Churn    int
	Recency  string
}

// CallGraphNodeFixture represents a node in a call graph.
type CallGraphNodeFixture struct {
	SymbolID string
	Name     string
	Kind     string
	Callers  []string
	Callees  []string
}

// ImpactNodeFixture represents a node in impact analysis.
type ImpactNodeFixture struct {
	SymbolID   string
	Name       string
	Kind       string
	FilePath   string
	Depth      int
	RiskLevel  string
	Dependents int
}

// ModuleFixture represents a module in architecture.
type ModuleFixture struct {
	Path         string
	Name         string
	FileCount    int
	SymbolCount  int
	Dependencies []string
	Dependents   []string
}

// UsagePathFixture represents a usage trace path.
type UsagePathFixture struct {
	Entrypoint string
	Target     string
	Path       []string
	Depth      int
}

// DiffFileFixture represents a file change in a diff summary.
type DiffFileFixture struct {
	Path         string
	Status       string
	Additions    int
	Deletions    int
	RiskLevel    string
	AffectedSyms []string
}

// DiffSummaryFixture represents a summarizeDiff response.
type DiffSummaryFixture struct {
	Files         []DiffFileFixture
	TotalAdded    int
	TotalDeleted  int
	FilesChanged  int
	RiskSummary   map[string]int
	AffectedPaths []string
}

// EntrypointFixture represents a system entrypoint.
type EntrypointFixture struct {
	SymbolID    string
	Name        string
	Kind        string
	FilePath    string
	Line        int
	EntryKind   string
	Centrality  float64
	Description string
}

// GenerateSymbols creates n synthetic symbols.
func GenerateSymbols(n int) []SymbolFixture {
	symbols := make([]SymbolFixture, n)
	kinds := []string{"function", "class", "method", "interface", "variable"}

	for i := 0; i < n; i++ {
		symbols[i] = SymbolFixture{
			StableID:    fmt.Sprintf("ckb:test:sym:%08x", i),
			Name:        fmt.Sprintf("Symbol%d", i),
			Kind:        kinds[i%len(kinds)],
			FilePath:    fmt.Sprintf("internal/module%d/file%d.go", i/10, i%10),
			Line:        (i % 500) + 1,
			Description: fmt.Sprintf("Test symbol %d for NFR testing", i),
		}
	}
	return symbols
}

// GenerateReferences creates n synthetic references.
func GenerateReferences(n int) []ReferenceFixture {
	refs := make([]ReferenceFixture, n)
	kinds := []string{"read", "write", "call"}

	for i := 0; i < n; i++ {
		refs[i] = ReferenceFixture{
			FilePath: fmt.Sprintf("internal/module%d/file%d.go", i/20, i%20),
			Line:     (i % 1000) + 1,
			Column:   (i % 80) + 1,
			Kind:     kinds[i%len(kinds)],
		}
	}
	return refs
}

// GenerateHotspots creates n synthetic hotspots.
func GenerateHotspots(n int) []HotspotFixture {
	hotspots := make([]HotspotFixture, n)

	for i := 0; i < n; i++ {
		hotspots[i] = HotspotFixture{
			FilePath: fmt.Sprintf("internal/module%d/file%d.go", i/10, i%10),
			Score:    1.0 - float64(i)/float64(n),
			Churn:    100 - i,
			Recency:  fmt.Sprintf("%dd ago", i+1),
		}
	}
	return hotspots
}

// GenerateCallGraph creates a synthetic call graph with the given depth.
func GenerateCallGraph(rootSymbol string, depth int, branching int) []CallGraphNodeFixture {
	nodes := make([]CallGraphNodeFixture, 0)
	generateCallGraphLevel(rootSymbol, depth, branching, &nodes, 0)
	return nodes
}

func generateCallGraphLevel(symbolID string, depth int, branching int, nodes *[]CallGraphNodeFixture, level int) {
	if level >= depth {
		return
	}

	callers := make([]string, 0, branching)
	callees := make([]string, 0, branching)

	for i := 0; i < branching; i++ {
		callerID := fmt.Sprintf("%s_caller%d_L%d", symbolID, i, level)
		calleeID := fmt.Sprintf("%s_callee%d_L%d", symbolID, i, level)
		callers = append(callers, callerID)
		callees = append(callees, calleeID)
	}

	*nodes = append(*nodes, CallGraphNodeFixture{
		SymbolID: symbolID,
		Name:     fmt.Sprintf("Function_%d", len(*nodes)),
		Kind:     "function",
		Callers:  callers,
		Callees:  callees,
	})

	// Recurse for callees
	for _, callee := range callees {
		generateCallGraphLevel(callee, depth, branching/2+1, nodes, level+1)
	}
}

// GenerateImpactNodes creates n synthetic impact analysis nodes.
func GenerateImpactNodes(n int, maxDepth int) []ImpactNodeFixture {
	nodes := make([]ImpactNodeFixture, n)
	riskLevels := []string{"high", "medium", "low"}

	for i := 0; i < n; i++ {
		nodes[i] = ImpactNodeFixture{
			SymbolID:   fmt.Sprintf("ckb:test:sym:%08x", i),
			Name:       fmt.Sprintf("AffectedSymbol%d", i),
			Kind:       "function",
			FilePath:   fmt.Sprintf("internal/module%d/file%d.go", i/10, i%10),
			Depth:      i % maxDepth,
			RiskLevel:  riskLevels[i%len(riskLevels)],
			Dependents: (n - i) / 2,
		}
	}
	return nodes
}

// GenerateModules creates n synthetic modules for architecture.
func GenerateModules(n int) []ModuleFixture {
	modules := make([]ModuleFixture, n)

	for i := 0; i < n; i++ {
		deps := make([]string, 0, 3)
		dependents := make([]string, 0, 3)

		// Create some dependencies (modules depend on lower-numbered modules)
		for j := 0; j < 3 && i-j-1 >= 0; j++ {
			deps = append(deps, fmt.Sprintf("internal/module%d", i-j-1))
		}
		// Create some dependents (higher-numbered modules depend on this)
		for j := 1; j <= 3 && i+j < n; j++ {
			dependents = append(dependents, fmt.Sprintf("internal/module%d", i+j))
		}

		modules[i] = ModuleFixture{
			Path:         fmt.Sprintf("internal/module%d", i),
			Name:         fmt.Sprintf("module%d", i),
			FileCount:    10 + i%20,
			SymbolCount:  50 + i%100,
			Dependencies: deps,
			Dependents:   dependents,
		}
	}
	return modules
}

// GenerateUsagePaths creates n synthetic usage trace paths.
func GenerateUsagePaths(n int, maxDepth int) []UsagePathFixture {
	paths := make([]UsagePathFixture, n)
	entrypoints := []string{"main", "handleRequest", "processJob", "apiHandler", "cliCommand"}

	for i := 0; i < n; i++ {
		depth := (i % maxDepth) + 1
		path := make([]string, depth)
		for j := 0; j < depth; j++ {
			path[j] = fmt.Sprintf("ckb:test:sym:%08x", i*10+j)
		}

		paths[i] = UsagePathFixture{
			Entrypoint: entrypoints[i%len(entrypoints)],
			Target:     fmt.Sprintf("ckb:test:sym:%08x", i),
			Path:       path,
			Depth:      depth,
		}
	}
	return paths
}

// GenerateDiffSummary creates a synthetic diff summary with n files.
func GenerateDiffSummary(n int) DiffSummaryFixture {
	files := make([]DiffFileFixture, n)
	statuses := []string{"modified", "added", "deleted", "renamed"}
	riskLevels := []string{"high", "medium", "low"}
	totalAdded := 0
	totalDeleted := 0
	riskSummary := map[string]int{"high": 0, "medium": 0, "low": 0}
	affectedPaths := make([]string, 0, n)

	for i := 0; i < n; i++ {
		additions := (i*17 + 5) % 200
		deletions := (i*11 + 3) % 150
		riskLevel := riskLevels[i%len(riskLevels)]
		path := fmt.Sprintf("internal/module%d/file%d.go", i/10, i%10)

		affectedSyms := make([]string, (i%5)+1)
		for j := range affectedSyms {
			affectedSyms[j] = fmt.Sprintf("ckb:test:sym:%08x", i*10+j)
		}

		files[i] = DiffFileFixture{
			Path:         path,
			Status:       statuses[i%len(statuses)],
			Additions:    additions,
			Deletions:    deletions,
			RiskLevel:    riskLevel,
			AffectedSyms: affectedSyms,
		}

		totalAdded += additions
		totalDeleted += deletions
		riskSummary[riskLevel]++
		affectedPaths = append(affectedPaths, path)
	}

	return DiffSummaryFixture{
		Files:         files,
		TotalAdded:    totalAdded,
		TotalDeleted:  totalDeleted,
		FilesChanged:  n,
		RiskSummary:   riskSummary,
		AffectedPaths: affectedPaths,
	}
}

// GenerateEntrypoints creates n synthetic entrypoints.
func GenerateEntrypoints(n int) []EntrypointFixture {
	entrypoints := make([]EntrypointFixture, n)
	kinds := []string{"api_handler", "cli_command", "main", "job_handler", "webhook"}
	funcKinds := []string{"function", "method"}

	for i := 0; i < n; i++ {
		entrypoints[i] = EntrypointFixture{
			SymbolID:    fmt.Sprintf("ckb:test:sym:%08x", i),
			Name:        fmt.Sprintf("Handle%s%d", strings.Title(kinds[i%len(kinds)]), i),
			Kind:        funcKinds[i%len(funcKinds)],
			FilePath:    fmt.Sprintf("internal/handlers/handler%d.go", i),
			Line:        (i % 500) + 10,
			EntryKind:   kinds[i%len(kinds)],
			Centrality:  1.0 - float64(i)/float64(n+1),
			Description: fmt.Sprintf("Entry point %d - %s handler", i, kinds[i%len(kinds)]),
		}
	}
	return entrypoints
}

// ChangedSymbolFixture represents a symbol changed in a diff.
type ChangedSymbolFixture struct {
	SymbolID   string
	Name       string
	File       string
	ChangeType string
	Lines      []int
	Confidence float64
}

// AffectedTestFixture represents an affected test file.
type AffectedTestFixture struct {
	FilePath   string
	Reason     string
	AffectedBy []string
	Confidence float64
}

// ChangeSetFixture represents a change set analysis result.
type ChangeSetFixture struct {
	ChangedSymbols  []ChangedSymbolFixture
	AffectedSymbols []ImpactNodeFixture
	ModulesAffected []ModuleFixture
	RiskLevel       string
	RiskScore       float64
}

// AffectedTestsFixture represents an affected tests response.
type AffectedTestsFixture struct {
	Tests      []AffectedTestFixture
	TotalFiles int
	RunCommand string
	Confidence float64
}

// GenerateChangedSymbols creates n synthetic changed symbols for change set analysis.
func GenerateChangedSymbols(n int) []ChangedSymbolFixture {
	symbols := make([]ChangedSymbolFixture, n)
	changeTypes := []string{"modified", "added", "deleted"}

	for i := 0; i < n; i++ {
		symbols[i] = ChangedSymbolFixture{
			SymbolID:   fmt.Sprintf("ckb:test:sym:%08x", i),
			Name:       fmt.Sprintf("ChangedSymbol%d", i),
			File:       fmt.Sprintf("internal/module%d/file%d.go", i/10, i%10),
			ChangeType: changeTypes[i%len(changeTypes)],
			Lines:      []int{(i % 100) + 1, (i % 100) + 10},
			Confidence: 0.8 + float64(i%3)*0.1,
		}
	}
	return symbols
}

// GenerateAffectedTests creates n synthetic affected test files.
func GenerateAffectedTests(n int) []AffectedTestFixture {
	tests := make([]AffectedTestFixture, n)
	reasons := []string{"direct", "transitive", "coverage"}

	for i := 0; i < n; i++ {
		affectedBy := make([]string, (i%3)+1)
		for j := range affectedBy {
			affectedBy[j] = fmt.Sprintf("ckb:test:sym:%08x", i*5+j)
		}

		tests[i] = AffectedTestFixture{
			FilePath:   fmt.Sprintf("internal/module%d/file%d_test.go", i/10, i%10),
			Reason:     reasons[i%len(reasons)],
			AffectedBy: affectedBy,
			Confidence: 0.7 + float64(i%4)*0.1,
		}
	}
	return tests
}

// GenerateChangeSet creates a synthetic change set analysis.
func GenerateChangeSet(changedCount, affectedCount int) ChangeSetFixture {
	riskLevels := []string{"low", "medium", "high", "critical"}
	riskIdx := 0
	if changedCount > 50 {
		riskIdx = 3
	} else if changedCount > 20 {
		riskIdx = 2
	} else if changedCount > 10 {
		riskIdx = 1
	}

	return ChangeSetFixture{
		ChangedSymbols:  GenerateChangedSymbols(changedCount),
		AffectedSymbols: GenerateImpactNodes(affectedCount, 3),
		ModulesAffected: GenerateModules(changedCount / 10),
		RiskLevel:       riskLevels[riskIdx],
		RiskScore:       float64(riskIdx) * 0.25,
	}
}

// GenerateAffectedTestsFixture creates a synthetic affected tests result.
func GenerateAffectedTestsFixture(n int) AffectedTestsFixture {
	tests := GenerateAffectedTests(n)
	return AffectedTestsFixture{
		Tests:      tests,
		TotalFiles: n,
		RunCommand: fmt.Sprintf("go test ./internal/module0/... ./internal/module1/..."),
		Confidence: 0.85,
	}
}

// FixtureSet contains fixtures for a specific size tier.
type FixtureSet struct {
	Tier          string
	Symbols       []SymbolFixture
	References    []ReferenceFixture
	Hotspots      []HotspotFixture
	CallGraph     []CallGraphNodeFixture
	ImpactNodes   []ImpactNodeFixture
	Modules       []ModuleFixture
	UsagePaths    []UsagePathFixture
	DiffSummary   DiffSummaryFixture
	Entrypoints   []EntrypointFixture
	ChangeSet     ChangeSetFixture
	AffectedTests AffectedTestsFixture
}

// SmallFixtures returns fixtures for small result sets.
func SmallFixtures() *FixtureSet {
	return &FixtureSet{
		Tier:          TierSmall,
		Symbols:       GenerateSymbols(20),
		References:    GenerateReferences(50),
		Hotspots:      GenerateHotspots(10),
		CallGraph:     GenerateCallGraph("root", 2, 3),
		ImpactNodes:   GenerateImpactNodes(10, 2),
		Modules:       GenerateModules(5),
		UsagePaths:    GenerateUsagePaths(5, 3),
		DiffSummary:   GenerateDiffSummary(10),
		Entrypoints:   GenerateEntrypoints(20),
		ChangeSet:     GenerateChangeSet(10, 20),
		AffectedTests: GenerateAffectedTestsFixture(5),
	}
}

// MediumFixtures returns fixtures for medium result sets.
func MediumFixtures() *FixtureSet {
	return &FixtureSet{
		Tier:          TierMedium,
		Symbols:       GenerateSymbols(100),
		References:    GenerateReferences(500),
		Hotspots:      GenerateHotspots(50),
		CallGraph:     GenerateCallGraph("root", 3, 4),
		ImpactNodes:   GenerateImpactNodes(40, 3),
		Modules:       GenerateModules(15),
		UsagePaths:    GenerateUsagePaths(20, 4),
		DiffSummary:   GenerateDiffSummary(50),
		Entrypoints:   GenerateEntrypoints(50),
		ChangeSet:     GenerateChangeSet(100, 200), // 100 changed symbols
		AffectedTests: GenerateAffectedTestsFixture(20),
	}
}

// LargeFixtures returns fixtures for large result sets (stress test).
func LargeFixtures() *FixtureSet {
	return &FixtureSet{
		Tier:          TierLarge,
		Symbols:       GenerateSymbols(500),
		References:    GenerateReferences(5000),
		Hotspots:      GenerateHotspots(200),
		CallGraph:     GenerateCallGraph("root", 4, 5),
		ImpactNodes:   GenerateImpactNodes(100, 4),
		Modules:       GenerateModules(30),
		UsagePaths:    GenerateUsagePaths(50, 5),
		DiffSummary:   GenerateDiffSummary(100),
		Entrypoints:   GenerateEntrypoints(100),
		ChangeSet:     GenerateChangeSet(500, 1000),
		AffectedTests: GenerateAffectedTestsFixture(50),
	}
}

// ExtraLargeFixtures returns fixtures for extra large result sets (1000 symbols).
func ExtraLargeFixtures() *FixtureSet {
	return &FixtureSet{
		Tier:          "xlarge",
		Symbols:       GenerateSymbols(1000),
		References:    GenerateReferences(10000),
		Hotspots:      GenerateHotspots(500),
		CallGraph:     GenerateCallGraph("root", 5, 6),
		ImpactNodes:   GenerateImpactNodes(200, 5),
		Modules:       GenerateModules(50),
		UsagePaths:    GenerateUsagePaths(100, 6),
		DiffSummary:   GenerateDiffSummary(200),
		Entrypoints:   GenerateEntrypoints(200),
		ChangeSet:     GenerateChangeSet(1000, 2000), // 1000 changed symbols
		AffectedTests: GenerateAffectedTestsFixture(100),
	}
}

// ToSearchSymbolsJSON converts symbols to searchSymbols response JSON.
func (f *FixtureSet) ToSearchSymbolsJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"symbols":[`)

	for i, sym := range f.Symbols {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"stableId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d},"description":"%s"}`,
			sym.StableID, sym.Name, sym.Kind, sym.FilePath, sym.Line, sym.Description,
		))
	}

	sb.WriteString(`],"truncated":false,"total":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.Symbols)))
	sb.WriteString(`}}`)
	return sb.String()
}

// ToFindReferencesJSON converts references to findReferences response JSON.
func (f *FixtureSet) ToFindReferencesJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"references":[`)

	for i, ref := range f.References {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"location":{"path":"%s","line":%d,"column":%d},"kind":"%s"}`,
			ref.FilePath, ref.Line, ref.Column, ref.Kind,
		))
	}

	sb.WriteString(`],"truncated":false,"total":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.References)))
	sb.WriteString(`}}`)
	return sb.String()
}

// ToGetHotspotsJSON converts hotspots to getHotspots response JSON.
func (f *FixtureSet) ToGetHotspotsJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"hotspots":[`)

	for i, h := range f.Hotspots {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"path":"%s","score":%.3f,"churn":%d,"recency":"%s"}`,
			h.FilePath, h.Score, h.Churn, h.Recency,
		))
	}

	sb.WriteString(`],"truncated":false,"total":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.Hotspots)))
	sb.WriteString(`}}`)
	return sb.String()
}

// ToGetCallGraphJSON converts call graph to getCallGraph response JSON.
func (f *FixtureSet) ToGetCallGraphJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"nodes":[`)

	for i, node := range f.CallGraph {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","kind":"%s","callers":[`,
			node.SymbolID, node.Name, node.Kind,
		))
		for j, caller := range node.Callers {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, caller))
		}
		sb.WriteString(`],"callees":[`)
		for j, callee := range node.Callees {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, callee))
		}
		sb.WriteString(`]}`)
	}

	sb.WriteString(`],"depth":2,"truncated":false}}`)
	return sb.String()
}

// ToAnalyzeImpactJSON converts impact nodes to analyzeImpact response JSON.
func (f *FixtureSet) ToAnalyzeImpactJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"rootSymbol":"ckb:test:sym:root","affectedSymbols":[`)

	for i, node := range f.ImpactNodes {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","kind":"%s","location":{"path":"%s"},"depth":%d,"riskLevel":"%s","dependentCount":%d}`,
			node.SymbolID, node.Name, node.Kind, node.FilePath, node.Depth, node.RiskLevel, node.Dependents,
		))
	}

	sb.WriteString(`],"maxDepth":4,"totalAffected":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.ImpactNodes)))
	sb.WriteString(`,"riskSummary":{"high":3,"medium":4,"low":3}}}`)
	return sb.String()
}

// ToGetArchitectureJSON converts modules to getArchitecture response JSON.
func (f *FixtureSet) ToGetArchitectureJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"modules":[`)

	for i, mod := range f.Modules {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"path":"%s","name":"%s","fileCount":%d,"symbolCount":%d,"dependencies":[`,
			mod.Path, mod.Name, mod.FileCount, mod.SymbolCount,
		))
		for j, dep := range mod.Dependencies {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, dep))
		}
		sb.WriteString(`],"dependents":[`)
		for j, dep := range mod.Dependents {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, dep))
		}
		sb.WriteString(`]}`)
	}

	sb.WriteString(`],"depth":2,"totalModules":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.Modules)))
	sb.WriteString(`}}`)
	return sb.String()
}

// ToTraceUsageJSON converts usage paths to traceUsage response JSON.
func (f *FixtureSet) ToTraceUsageJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"targetSymbol":"ckb:test:sym:target","paths":[`)

	for i, path := range f.UsagePaths {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"entrypoint":"%s","target":"%s","depth":%d,"steps":[`,
			path.Entrypoint, path.Target, path.Depth,
		))
		for j, step := range path.Path {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, step))
		}
		sb.WriteString(`]}`)
	}

	sb.WriteString(`],"totalPaths":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.UsagePaths)))
	sb.WriteString(`,"maxDepth":5}}`)
	return sb.String()
}

// ToSummarizeDiffJSON converts diff summary to summarizeDiff response JSON.
func (f *FixtureSet) ToSummarizeDiffJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"files":[`)

	for i, file := range f.DiffSummary.Files {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"path":"%s","status":"%s","additions":%d,"deletions":%d,"riskLevel":"%s","affectedSymbols":[`,
			file.Path, file.Status, file.Additions, file.Deletions, file.RiskLevel,
		))
		for j, sym := range file.AffectedSyms {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, sym))
		}
		sb.WriteString(`]}`)
	}

	sb.WriteString(`],"summary":{`)
	sb.WriteString(fmt.Sprintf(`"filesChanged":%d,`, f.DiffSummary.FilesChanged))
	sb.WriteString(fmt.Sprintf(`"totalAdditions":%d,`, f.DiffSummary.TotalAdded))
	sb.WriteString(fmt.Sprintf(`"totalDeletions":%d,`, f.DiffSummary.TotalDeleted))
	sb.WriteString(fmt.Sprintf(`"riskBreakdown":{"high":%d,"medium":%d,"low":%d}`,
		f.DiffSummary.RiskSummary["high"],
		f.DiffSummary.RiskSummary["medium"],
		f.DiffSummary.RiskSummary["low"]))
	sb.WriteString(`}}}`)
	return sb.String()
}

// ToListEntrypointsJSON converts entrypoints to listEntrypoints response JSON.
func (f *FixtureSet) ToListEntrypointsJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"entrypoints":[`)

	for i, ep := range f.Entrypoints {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d},"entryKind":"%s","centrality":%.3f,"description":"%s"}`,
			ep.SymbolID, ep.Name, ep.Kind, ep.FilePath, ep.Line, ep.EntryKind, ep.Centrality, ep.Description,
		))
	}

	sb.WriteString(`],"total":`)
	sb.WriteString(fmt.Sprintf("%d", len(f.Entrypoints)))
	sb.WriteString(`,"truncated":false}}`)
	return sb.String()
}

// ToAnalyzeChangeJSON converts change set to analyzeChange response JSON.
func (f *FixtureSet) ToAnalyzeChangeJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"summary":{`)
	sb.WriteString(fmt.Sprintf(`"filesChanged":%d,`, len(f.ChangeSet.ChangedSymbols)/5))
	sb.WriteString(fmt.Sprintf(`"symbolsChanged":%d,`, len(f.ChangeSet.ChangedSymbols)))
	sb.WriteString(fmt.Sprintf(`"directlyAffected":%d,`, len(f.ChangeSet.AffectedSymbols)/2))
	sb.WriteString(fmt.Sprintf(`"transitivelyAffected":%d,`, len(f.ChangeSet.AffectedSymbols)/2))
	sb.WriteString(fmt.Sprintf(`"estimatedRisk":"%s"`, f.ChangeSet.RiskLevel))
	sb.WriteString(`},"changedSymbols":[`)

	for i, sym := range f.ChangeSet.ChangedSymbols {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","file":"%s","changeType":"%s","lines":[%d,%d],"confidence":%.2f}`,
			sym.SymbolID, sym.Name, sym.File, sym.ChangeType, sym.Lines[0], sym.Lines[1], sym.Confidence,
		))
	}

	sb.WriteString(`],"affectedSymbols":[`)
	for i, sym := range f.ChangeSet.AffectedSymbols {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"stableId":"%s","name":"%s","kind":"%s","distance":%d,"confidence":0.8}`,
			sym.SymbolID, sym.Name, sym.Kind, sym.Depth,
		))
	}

	sb.WriteString(`],"modulesAffected":[`)
	for i, mod := range f.ChangeSet.ModulesAffected {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"moduleId":"%s","name":"%s","impactCount":%d}`,
			mod.Path, mod.Name, mod.FileCount,
		))
	}

	// consumerFiles: one file per affected symbol, up to the same bound the engine uses.
	files := len(f.ChangeSet.AffectedSymbols)
	truncated := files > maxConsumerFilesFixture
	if truncated {
		files = maxConsumerFilesFixture
	}
	sb.WriteString(`],"consumerFiles":{"direct":[`)
	for i := 0; i < files; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(`"internal/module%d/consumer_%d.go"`, i%10, i))
	}
	sb.WriteString(fmt.Sprintf(`],"total":%d,"truncated":%t}`, len(f.ChangeSet.AffectedSymbols), truncated))

	sb.WriteString(`,"riskScore":{"level":"`)
	sb.WriteString(f.ChangeSet.RiskLevel)
	sb.WriteString(fmt.Sprintf(`","score":%.2f}}}`, f.ChangeSet.RiskScore))
	return sb.String()
}

// maxConsumerFilesFixture mirrors query.MaxConsumerFiles; a test in package mcp
// keeps the two equal.
const maxConsumerFilesFixture = 300

// MaxConsumerFilesFixture exposes the bound the fixtures use.
func MaxConsumerFilesFixture() int { return maxConsumerFilesFixture }

// ToExploreJSON converts fixtures to an explore compound response JSON.
// Aggregates: explainFile + searchSymbols + getCallGraph + getHotspots
func (f *FixtureSet) ToExploreJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"file":{`)
	sb.WriteString(`"path":"internal/module0/file0.go","role":"implementation",`)
	sb.WriteString(fmt.Sprintf(`"symbolCount":%d,"lineCount":500,`, len(f.Symbols)))
	sb.WriteString(`"description":"Core module implementation file"},`)

	// Embed symbols
	sb.WriteString(`"symbols":[`)
	for i, sym := range f.Symbols {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"stableId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d}}`,
			sym.StableID, sym.Name, sym.Kind, sym.FilePath, sym.Line,
		))
	}
	sb.WriteString(`],`)

	// Embed call graph
	sb.WriteString(`"callGraph":{"nodes":[`)
	for i, node := range f.CallGraph {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","kind":"%s","callers":[`,
			node.SymbolID, node.Name, node.Kind,
		))
		for j, caller := range node.Callers {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, caller))
		}
		sb.WriteString(`],"callees":[`)
		for j, callee := range node.Callees {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, callee))
		}
		sb.WriteString(`]}`)
	}
	sb.WriteString(`]},`)

	// Embed hotspots
	sb.WriteString(`"hotspots":[`)
	for i, h := range f.Hotspots {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"path":"%s","score":%.3f,"churn":%d}`,
			h.FilePath, h.Score, h.Churn,
		))
	}
	sb.WriteString(`]}}`)
	return sb.String()
}

// ToUnderstandJSON converts fixtures to an understand compound response JSON.
// Aggregates: searchSymbols + getSymbol + explainSymbol + findReferences + getCallGraph
func (f *FixtureSet) ToUnderstandJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"symbol":{`)

	// Symbol detail
	if len(f.Symbols) > 0 {
		sym := f.Symbols[0]
		sb.WriteString(fmt.Sprintf(
			`"stableId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d},`,
			sym.StableID, sym.Name, sym.Kind, sym.FilePath, sym.Line,
		))
		sb.WriteString(fmt.Sprintf(
			`"description":"%s","signature":"func %s() error"`,
			sym.Description, sym.Name,
		))
	}
	sb.WriteString(`},`)

	// Explanation
	sb.WriteString(`"explanation":{"summary":"Core function handling primary business logic",`)
	sb.WriteString(`"usage":"Called from API handlers and CLI commands",`)
	sb.WriteString(`"history":"Modified 12 times, last change 3 days ago"},`)

	// References
	sb.WriteString(`"references":[`)
	for i, ref := range f.References {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"location":{"path":"%s","line":%d,"column":%d},"kind":"%s"}`,
			ref.FilePath, ref.Line, ref.Column, ref.Kind,
		))
	}
	sb.WriteString(`],`)

	// Call graph
	sb.WriteString(`"callGraph":{"nodes":[`)
	for i, node := range f.CallGraph {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","callers":[`,
			node.SymbolID, node.Name,
		))
		for j, caller := range node.Callers {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, caller))
		}
		sb.WriteString(`],"callees":[`)
		for j, callee := range node.Callees {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, callee))
		}
		sb.WriteString(`]}`)
	}
	sb.WriteString(fmt.Sprintf(`]},"totalReferences":%d}}`, len(f.References)))
	return sb.String()
}

// ToPrepareChangeJSON converts fixtures to a prepareChange compound response JSON.
// Aggregates: analyzeImpact + getAffectedTests + coupling analysis
func (f *FixtureSet) ToPrepareChangeJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"target":"ckb:test:sym:00000000",`)
	sb.WriteString(fmt.Sprintf(`"changeType":"modify","riskScore":%.2f,`, f.ChangeSet.RiskScore))

	// Impact
	sb.WriteString(`"impact":{"affectedSymbols":[`)
	for i, node := range f.ImpactNodes {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"symbolId":"%s","name":"%s","kind":"%s","depth":%d,"riskLevel":"%s"}`,
			node.SymbolID, node.Name, node.Kind, node.Depth, node.RiskLevel,
		))
	}
	sb.WriteString(fmt.Sprintf(`],"totalAffected":%d},`, len(f.ImpactNodes)))

	// Affected tests
	sb.WriteString(`"affectedTests":[`)
	for i, test := range f.AffectedTests.Tests {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"filePath":"%s","reason":"%s","confidence":%.2f}`,
			test.FilePath, test.Reason, test.Confidence,
		))
	}
	sb.WriteString(`],`)

	// Coupling
	sb.WriteString(`"coupledFiles":[`)
	limit := len(f.Hotspots)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"path":"%s","couplingScore":%.3f,"cochangeCount":%d}`,
			f.Hotspots[i].FilePath, f.Hotspots[i].Score, f.Hotspots[i].Churn,
		))
	}
	sb.WriteString(`]}}`)
	return sb.String()
}

// ToBatchGetJSON converts fixtures to a batchGet compound response JSON.
// Simulates fetching multiple symbols by ID.
func (f *FixtureSet) ToBatchGetJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"symbols":[`)

	// Use up to 50 symbols (batchGet max)
	limit := len(f.Symbols)
	if limit > 50 {
		limit = 50
	}

	for i := 0; i < limit; i++ {
		sym := f.Symbols[i]
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"stableId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d},"description":"%s","signature":"func %s() error","container":"module%d"}`,
			sym.StableID, sym.Name, sym.Kind, sym.FilePath, sym.Line, sym.Description, sym.Name, i/10,
		))
	}

	sb.WriteString(fmt.Sprintf(`],"total":%d,"missing":[]}}`, limit))
	return sb.String()
}

// ToBatchSearchJSON converts fixtures to a batchSearch compound response JSON.
// Simulates multiple concurrent searches.
func (f *FixtureSet) ToBatchSearchJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"results":[`)

	// Simulate multiple search queries, each returning a subset of symbols
	numQueries := 3
	if len(f.Symbols) > 200 {
		numQueries = 10
	} else if len(f.Symbols) > 50 {
		numQueries = 5
	}

	perQuery := len(f.Symbols) / numQueries
	if perQuery > 100 {
		perQuery = 100
	}

	for q := 0; q < numQueries; q++ {
		if q > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(`{"query":"query%d","symbols":[`, q))

		start := q * perQuery
		end := start + perQuery
		if end > len(f.Symbols) {
			end = len(f.Symbols)
		}

		for i := start; i < end; i++ {
			sym := f.Symbols[i]
			if i > start {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(
				`{"stableId":"%s","name":"%s","kind":"%s","location":{"path":"%s","line":%d},"description":"%s"}`,
				sym.StableID, sym.Name, sym.Kind, sym.FilePath, sym.Line, sym.Description,
			))
		}

		sb.WriteString(fmt.Sprintf(`],"total":%d,"truncated":false}`, end-start))
	}

	sb.WriteString(`]}}`)
	return sb.String()
}

// ToGetAffectedTestsJSON converts affected tests to getAffectedTests response JSON.
func (f *FixtureSet) ToGetAffectedTestsJSON() string {
	var sb strings.Builder
	sb.WriteString(`{"schemaVersion":"1.0","data":{"tests":[`)

	for i, test := range f.AffectedTests.Tests {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(fmt.Sprintf(
			`{"filePath":"%s","reason":"%s","confidence":%.2f,"affectedBy":[`,
			test.FilePath, test.Reason, test.Confidence,
		))
		for j, sym := range test.AffectedBy {
			if j > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(fmt.Sprintf(`"%s"`, sym))
		}
		sb.WriteString(`]}`)
	}

	sb.WriteString(`],"summary":{`)
	sb.WriteString(fmt.Sprintf(`"totalFiles":%d,`, f.AffectedTests.TotalFiles))
	direct := 0
	trans := 0
	for _, t := range f.AffectedTests.Tests {
		if t.Reason == "direct" {
			direct++
		} else {
			trans++
		}
	}
	sb.WriteString(fmt.Sprintf(`"directFiles":%d,`, direct))
	sb.WriteString(fmt.Sprintf(`"transitiveFiles":%d`, trans))
	sb.WriteString(`},"runCommand":"`)
	sb.WriteString(f.AffectedTests.RunCommand)
	sb.WriteString(fmt.Sprintf(`","confidence":%.2f}}`, f.AffectedTests.Confidence))
	return sb.String()
}
