package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Preset names
const (
	PresetCore       = "core"
	PresetReview     = "review"
	PresetRefactor   = "refactor"
	PresetFederation = "federation"
	PresetDocs       = "docs"
	PresetOps        = "ops"
	PresetFull       = "full"
)

// DefaultPreset is the default preset for new sessions
const DefaultPreset = PresetCore

// Presets defines the tool sets for each preset.
// Core must enable one complete workflow without expansion.
// Default workflow: "Investigate & Assess Impact"
var Presets = map[string][]string{
	// Core: 24 tools - enables "Investigate & Assess Impact" workflow completely
	// v8.0: Added compound tools (explore, understand, prepareChange, batchGet, batchSearch)
	// to reduce tool calls by 60-70% for common workflows
	PresetCore: {
		// v8.0 Compound Tools (preferred for AI workflows)
		"explore",       // Replaces: explainFile → searchSymbols → getCallGraph → getHotspots
		"understand",    // Replaces: searchSymbols → getSymbol → explainSymbol → findReferences → getCallGraph
		"prepareChange", // Replaces: analyzeImpact + getAffectedTests + analyzeCoupling + risk
		"batchGet",      // Multiple symbols in one call
		"batchSearch",   // Multiple searches in one call

		// Discovery & Search (granular fallback)
		"searchSymbols",
		"symbolExists",
		"getSymbol",

		// Navigation & Understanding (granular fallback)
		"explainSymbol",
		"explainFile",
		"explainPath",
		"findReferences",
		"getCallGraph",
		"traceUsage", // Enables debug workflow

		// Architecture & Orientation
		"getArchitecture",
		"getModuleOverview",
		"getModuleResponsibilities",
		"listKeyConcepts", // Enables architecture exploration

		// Impact & Risk (granular fallback)
		"analyzeImpact",
		"getHotspots",

		// LLM Integration
		"exportForLLM",

		// System
		"getStatus",
		"switchProject", // v8.1: Dynamic project switching

		// v8.1 Refactoring compound tool
		"planRefactor",

		// Meta (always included)
		"expandToolset",
	},

	// Review: core + code review tools
	PresetReview: {
		// Core tools
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "planRefactor", "expandToolset",
		// Review-specific
		"summarizeDiff",
		"summarizePr",
		"getOwnership",
		"getOwnershipDrift",
		"recentlyRelevant",
		"scanSecrets",        // Secret detection for PR reviews
		"reviewPR",           // Unified PR review with quality gates
		"getAffectedTests",   // Tests covering changed code
		"analyzeTestGaps",    // Untested functions in changed files
		"compareAPI",         // Breaking API changes
		"findDeadCode",       // Dead code in changes
		"findUnwiredModules", // Exported symbols not reachable from entrypoints
		"auditRisk",          // Multi-factor risk scoring
		"assessChange",       // Change analysis (post-change counterpart to prepareChange)
		"analyzeChange",      // Deprecated alias of assessChange
		"getFileComplexity",  // File complexity for review
		"listEntrypoints",    // Key entry points in changed code
		"auditCompliance",    // Regulatory compliance audit
	},

	// Refactor: core + refactoring analysis tools
	PresetRefactor: {
		// Core tools
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "expandToolset",
		// Refactor-specific
		"justifySymbol",
		"analyzeCoupling",
		"findDeadCodeCandidates",
		"findDeadCode",       // v7.6: Static dead code detection (no telemetry needed)
		"findUnwiredModules", // Exported symbols not reachable from entrypoints
		"getAffectedTests",   // v7.6: Find tests affected by changes
		"compareAPI",         // v7.6: Breaking change detection
		"auditRisk",
		"explainOrigin",
		"scanSecrets",           // v8.0: Secret detection for security audits
		"analyzeTestGaps",       // v8.1: Test gap analysis
		"planRefactor",          // v8.1: Unified refactor planning
		"findCycles",            // v8.1: Dependency cycle detection
		"suggestRefactorings",   // v8.1: Proactive refactoring suggestions
		"getFileComplexity",     // v8.3: File complexity for health pipeline
		"listSymbols",           // v8.3: Bulk symbol listing with complexity
		"getSymbolGraph",        // v8.3: Batch call graph
		"analyzeStructuralPerf", // v8.5: Loop call sites in hot files
	},

	// Federation: core + federation + contract tools
	PresetFederation: {
		// Core tools
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "planRefactor", "expandToolset",
		// Federation-specific
		"listFederations",
		"federationStatus",
		"federationRepos",
		"federationSearchModules",
		"federationSearchOwnership",
		"federationSearchDecisions",
		"federationGetHotspots",
		"federationSync",
		"federationAddRemote",
		"federationRemoveRemote",
		"federationListRemote",
		"federationSyncRemote",
		"federationStatusRemote",
		"federationSearchSymbolsHybrid",
		"federationListAllRepos",
		// Contracts
		"listContracts",
		"analyzeContractImpact",
		"getContractDependencies",
		"getContractStats",
		"suppressContractEdge",
		"verifyContractEdge",
	},

	// Docs: core + doc-symbol linking + decision tools
	PresetDocs: {
		// Core tools
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "planRefactor", "expandToolset",
		// Docs-specific
		"indexDocs",
		"getDocsForSymbol",
		"getSymbolsInDoc",
		"getDocsForModule",
		"checkDocStaleness",
		"getDocCoverage",
		// Decisions (ADRs)
		"getDecisions",
		"recordDecision",
		"annotateModule",
	},

	// Ops: core + operational + telemetry tools
	PresetOps: {
		// Core tools
		"explore", "understand", "prepareChange", "batchGet", "batchSearch",
		"searchSymbols", "symbolExists", "getSymbol", "explainSymbol", "explainFile", "explainPath",
		"findReferences", "getCallGraph", "traceUsage",
		"getArchitecture", "getModuleOverview", "getModuleResponsibilities", "listKeyConcepts",
		"analyzeImpact", "getHotspots", "exportForLLM",
		"getStatus", "switchProject", "planRefactor", "expandToolset",
		// Ops-specific
		"doctor",
		"reindex",
		"refreshArchitecture",
		"daemonStatus",
		"listJobs",
		"getJobStatus",
		"cancelJob",
		"listSchedules",
		"runSchedule",
		"listWebhooks",
		"testWebhook",
		"webhookDeliveries",
		"getWideResultMetrics",
		// Telemetry
		"getTelemetryStatus",
		"getObservedUsage",
		// Multi-repo
		"getActiveRepo",
		"listRepos",
		"switchRepo",
	},

	// Full: all tools (wildcard)
	PresetFull: {"*"},
}

// ValidPresets returns all valid preset names
func ValidPresets() []string {
	return []string{
		PresetCore,
		PresetReview,
		PresetRefactor,
		PresetFederation,
		PresetDocs,
		PresetOps,
		PresetFull,
	}
}

// IsValidPreset checks if a preset name is valid
func IsValidPreset(preset string) bool {
	_, ok := Presets[preset]
	return ok
}

// GetPresetTools returns the tool names for a preset
func GetPresetTools(preset string) []string {
	tools, ok := Presets[preset]
	if !ok {
		return Presets[PresetCore]
	}
	return tools
}

// coreToolOrder defines the order of core tools (must appear first on page 1)
// v8.0: Compound tools come first (preferred for AI workflows)
var coreToolOrder = []string{
	// v8.0 Compound Tools (preferred for AI workflows)
	"explore",
	"understand",
	"prepareChange",
	"batchGet",
	"batchSearch",
	// Granular tools (fallback)
	"searchSymbols",
	"symbolExists",
	"getSymbol",
	"explainSymbol",
	"explainFile",
	"explainPath",
	"findReferences",
	"getCallGraph",
	"traceUsage",
	"getArchitecture",
	"getModuleOverview",
	"getModuleResponsibilities",
	"listKeyConcepts",
	"analyzeImpact",
	"getHotspots",
	"exportForLLM",
	"getStatus",
	"switchProject",
	"planRefactor",
	"expandToolset",
}

// FilterAndOrderTools filters tools by preset and orders them core-first.
// Returns tools in order: core tools first (in defined order), then remaining alphabetically.
func FilterAndOrderTools(allTools []Tool, preset string) []Tool {
	presetTools := GetPresetTools(preset)

	// Handle "full" preset
	if len(presetTools) == 1 && presetTools[0] == "*" {
		return orderToolsCoreFirst(allTools)
	}

	// Build lookup set for preset tools
	presetSet := make(map[string]bool)
	for _, name := range presetTools {
		presetSet[name] = true
	}

	// Filter tools
	filtered := make([]Tool, 0, len(presetTools))
	for _, tool := range allTools {
		if presetSet[tool.Name] {
			filtered = append(filtered, tool)
		}
	}

	return orderToolsCoreFirst(filtered)
}

// orderToolsCoreFirst orders tools with core tools first, then alphabetical
func orderToolsCoreFirst(tools []Tool) []Tool {
	// Build position map for core tools
	corePosition := make(map[string]int)
	for i, name := range coreToolOrder {
		corePosition[name] = i
	}

	// Build tool map
	toolMap := make(map[string]Tool)
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	result := make([]Tool, 0, len(tools))

	// First: add core tools in order
	for _, name := range coreToolOrder {
		if t, ok := toolMap[name]; ok {
			result = append(result, t)
			delete(toolMap, name)
		}
	}

	// Then: add remaining tools alphabetically
	remaining := make([]string, 0, len(toolMap))
	for name := range toolMap {
		remaining = append(remaining, name)
	}
	sort.Strings(remaining)

	for _, name := range remaining {
		result = append(result, toolMap[name])
	}

	return result
}

// ComputeToolsetHash computes a hash of tool definitions for cursor invalidation.
// Hash is based on name + description + inputSchema (all affect tokens).
func ComputeToolsetHash(tools []Tool) string {
	// Serialize in deterministic order
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	sort.Strings(names)

	// Build tool map
	toolMap := make(map[string]Tool)
	for _, t := range tools {
		toolMap[t.Name] = t
	}

	// Hash each tool's content
	h := sha256.New()
	for _, name := range names {
		t := toolMap[name]
		h.Write([]byte(t.Name))
		h.Write([]byte(t.Description))
		if t.InputSchema != nil {
			if data, err := json.Marshal(t.InputSchema); err == nil {
				h.Write(data)
			}
		}
		h.Write([]byte{0}) // separator
	}

	return hex.EncodeToString(h.Sum(nil))[:10]
}

// FormatTokens formats a token count for display (e.g., "~12k tokens")
// Uses rounding (+500) for values >= 1000 to give more accurate estimates.
func FormatTokens(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("~%dk tokens", (tokens+500)/1000)
	}
	return fmt.Sprintf("~%d tokens", tokens)
}

// PresetDescriptions provides human-readable descriptions for each preset
var PresetDescriptions = map[string]string{
	PresetCore:       "Quick navigation, search, impact analysis",
	PresetReview:     "Code review with ownership and PR summaries",
	PresetRefactor:   "Refactoring analysis with coupling and dead code",
	PresetFederation: "Multi-repo queries and cross-repo visibility",
	PresetDocs:       "Documentation-symbol linking and coverage",
	PresetOps:        "Diagnostics, daemon, webhooks, jobs",
	PresetFull:       "Complete feature set (all tools)",
}

// PresetInfo contains display information about a preset
type PresetInfo struct {
	Name        string
	ToolCount   int
	TokenCount  int
	Description string
	IsDefault   bool
}

// GetAllPresetInfo returns information about all presets including tool counts and token estimates.
// Requires tool definitions to calculate accurate token estimates.
func GetAllPresetInfo(allTools []Tool) []PresetInfo {
	presets := ValidPresets()
	infos := make([]PresetInfo, 0, len(presets))

	for _, name := range presets {
		filtered := FilterAndOrderTools(allTools, name)
		tokens := EstimateTokens(MeasureJSONSize(filtered))

		infos = append(infos, PresetInfo{
			Name:        name,
			ToolCount:   len(filtered),
			TokenCount:  tokens,
			Description: PresetDescriptions[name],
			IsDefault:   name == DefaultPreset,
		})
	}

	return infos
}
