package mcp

import (
	"fmt"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
)

// Tool represents a CKB tool exposed via MCP
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// ToolHandler is a function that handles a tool call and returns an envelope response.
type ToolHandler func(params map[string]interface{}) (*envelope.Response, error)

// expandToolsetDescFmt is the expandToolset tool description, minus the
// per-preset tool counts. It is formatted with fmt.Sprintf against
// len(GetPresetTools(...)) for each named preset (and the total tool count
// for "full") in GetToolDefinitions below, so the counts can never drift
// from presets.go the way the old hardcoded numbers did.
//
// "Replaces your current preset" (not "pick the smallest preset that has
// the tools you need") matches the one-expansion-per-session rule enforced
// in toolExpandToolset: a second call is rejected outright, so there is no
// "start small, expand again later" path — the only real choice is "full"
// vs. the one preset that covers the whole task.
const expandToolsetDescFmt = "Switch to a larger toolset for a specific workflow. This replaces your " +
	"current preset — one expansion per session, a second call is rejected — so pick " +
	"\"full\" if the task spans multiple presets (e.g. review + refactor) rather than " +
	"the smallest one that fits right now. Presets (each includes all core tools plus):\n" +
	"• review (%d tools): reviewPR, auditCompliance, scanSecrets, analyzeTestGaps, getAffectedTests, compareAPI, findDeadCode, findUnwiredModules, auditRisk, getOwnership — use for PR reviews, test coverage analysis, compliance audits, security\n" +
	"• refactor (%d tools): analyzeCoupling, findCycles, suggestRefactorings, findDeadCode, findUnwiredModules, compareAPI, explainOrigin — use for refactoring, dependency analysis, dead code removal\n" +
	"• federation (%d tools): federationSearch*, listContracts, analyzeContractImpact — use for multi-repo queries, cross-repo analysis\n" +
	"• docs (%d tools): indexDocs, getDocsForSymbol, checkDocStaleness, getDecisions, recordDecision — use for documentation, ADRs\n" +
	"• ops (%d tools): doctor, reindex, daemonStatus, listJobs, webhooks, telemetry — use for diagnostics, daemon management\n" +
	"• full (%d tools): everything — use when the task spans multiple presets"

// GetToolDefinitions returns all tool definitions
func (s *MCPServer) GetToolDefinitions() []Tool {
	tools := []Tool{
		{
			Name:        "getStatus",
			Description: "Get CKB system status including backend health, cache stats, repository state, and usage hints for available capabilities.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "getWideResultMetrics",
			Description: "Get aggregated metrics for wide-result tools (findReferences, getCallGraph, etc). Shows truncation rates to inform Frontier mode decisions. Internal/debug tool.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "doctor",
			Description: "Diagnose CKB configuration issues and get suggested fixes",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "reindex",
			Description: "Trigger a refresh of the SCIP index without restarting CKB. Returns actionable guidance on how to refresh the index based on current staleness.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"full", "incremental"},
						"default":     "full",
						"description": "Reindex scope: 'full' for complete reindex, 'incremental' for changed files only (Go only)",
					},
					"async": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Return immediately and poll status (not yet implemented)",
					},
				},
			},
		},
		// Meta-tool for dynamic preset expansion. Description is filled in
		// below (after the full tool list is built) via expandToolsetDescFmt,
		// so the per-preset tool counts are computed from presets.go instead
		// of hardcoded here.
		{
			Name:        "expandToolset",
			Description: expandToolsetDescFmt,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"preset": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"review", "refactor", "federation", "docs", "ops", "full"},
						"description": "The preset to expand to. This replaces your current preset — one expansion per session — so pick \"full\" if your task spans multiple presets.",
					},
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "Why you need this preset (required to prevent accidental expansion)",
					},
				},
				"required": []string{"preset", "reason"},
			},
		},
		{
			Name:        "getSymbol",
			Description: "Get symbol metadata and location by stable ID",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID (ckb:<repo>:sym:<fingerprint>)",
					},
					"repoStateMode": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"head", "full"},
						"default":     "head",
						"description": "Whether to use HEAD commit only or full working tree state",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "searchSymbols",
			Description: "Semantic code search returning symbol types, locations, and relationships—more accurate than text-based grep/find. Note: may not match class methods or record properties by bare name — use symbolExists for authoritative boolean lookups.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (substring match, case-insensitive)",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Path prefix or module ID to limit search scope (e.g., 'src/services/', 'internal/query')",
					},
					"kinds": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of symbol kinds to filter (e.g., 'class', 'function')",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     20,
						"description": "Maximum number of results to return",
					},
					"minLines": map[string]interface{}{
						"type":        "number",
						"description": "Minimum body line count (filters trivial getters). Applied after tree-sitter enrichment.",
					},
					"minComplexity": map[string]interface{}{
						"type":        "number",
						"description": "Minimum cyclomatic complexity. Applied after tree-sitter enrichment.",
					},
					"excludePatterns": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Exclude symbols whose name contains any pattern (e.g., '#' for struct fields, '<anonymous>')",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "symbolExists",
			Description: "Boolean oracle for LLM grounding: answers whether a bare symbol name has any declaration in the index. Uses exact-match (not FTS ranking) so class methods and object-property declarations are found reliably. Returns exists, matches count, distinct kinds, and receiver names for methods/properties. Cheaper than searchSymbols — no locations, no ranking.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Bare symbol name to look up (e.g. \"saveReport\", \"ENV_PATH\")",
					},
					"kinds": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Optional kind filter (e.g. [\"method\", \"function\", \"class\", \"property\"])",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Optional file-path prefix to restrict search (e.g. \"packages/server/src/\")",
					},
					"includeExternal": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include symbols from node_modules (default false)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "listSymbols",
			Description: "Bulk list symbols in a scope without search query. Returns functions, types, and classes with body ranges and complexity metrics (lines, endLine, cyclomatic, cognitive). Use for complete symbol inventory — no search query needed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Path prefix to list symbols from (e.g., 'src/services/', 'internal/query/')",
					},
					"kinds": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Symbol kinds: 'function', 'method', 'class', 'type', 'interface' (default: function, method)",
					},
					"minLines": map[string]interface{}{
						"type":        "number",
						"default":     3,
						"description": "Minimum body line count (filters trivial getters/setters)",
					},
					"minComplexity": map[string]interface{}{
						"type":        "number",
						"default":     0,
						"description": "Minimum cyclomatic complexity to include",
					},
					"sortBy": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"complexity", "lines", "name"},
						"default":     "complexity",
						"description": "Sort order",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     50,
						"description": "Max results (default 50, max 200)",
					},
				},
			},
		},
		{
			Name:        "getSymbolGraph",
			Description: "Batch call graph for multiple symbols. Returns nodes and edges for all requested symbols in one call, replacing N serial getCallGraph calls with 1 batch call.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolIds": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Symbol IDs to get call graph for (max 30)",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     1,
						"description": "Call graph depth per symbol (1-3)",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"callers", "callees", "both"},
						"default":     "both",
						"description": "Direction to traverse",
					},
				},
				"required": []string{"symbolIds"},
			},
		},
		{
			Name:        "findReferences",
			Description: "Find all references to a symbol with completeness information",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Path prefix or module ID to limit search scope (e.g., 'src/services/', 'internal/query')",
					},
					"merge": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"prefer-first", "union"},
						"default":     "prefer-first",
						"description": "Backend merge strategy",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     100,
						"description": "Maximum number of references to return",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "getArchitecture",
			Description: "Get codebase architecture with module dependencies. For single-module projects, use granularity='directory' or 'file' for meaningful visualization.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     2,
						"description": "Maximum dependency depth to traverse",
					},
					"includeExternalDeps": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Whether to include external dependencies",
					},
					"refresh": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Force refresh of cached architecture",
					},
					"granularity": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"module", "directory", "file"},
						"default":     "module",
						"description": "Level of detail: 'module' (packages), 'directory' (folders), 'file' (individual files)",
					},
					"inferModules": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Infer modules from directory structure when no explicit package boundaries exist",
					},
					"targetPath": map[string]interface{}{
						"type":        "string",
						"description": "Optional path to focus on (relative to repo root)",
					},
					"includeMetrics": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include aggregate metrics (complexity, churn) per directory. Only applies to directory granularity.",
					},
				},
			},
		},
		{
			Name:        "analyzeImpact",
			Description: "Use this to check 'what breaks if I change X?' — returns callers, affected modules, and risk score for a single symbol. For full pre-change analysis with tests and coupling, use prepareChange instead. For analyzing a git diff of changes already made, use analyzeChange.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID to analyze",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     2,
						"description": "Maximum depth for transitive impact analysis",
					},
					"includeTelemetry": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include observed telemetry data in analysis (requires telemetry to be enabled)",
					},
					"telemetryPeriod": map[string]interface{}{
						"type":        "string",
						"default":     "90d",
						"description": "Time period for telemetry data (7d, 30d, 90d, all)",
						"enum":        []string{"7d", "30d", "90d", "all"},
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "analyzeOutgoingImpact",
			Description: "Use this to check 'what does X call?' — returns direct and transitive callees, plus embedding-similar semantically coupled symbols. Mirror of analyzeImpact in the forward direction. Requires a LIP daemon advertising query_outgoing_impact (v2.3.5+); when LIP is unavailable the response is empty with a provenance warning.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID to analyze",
					},
					"minScore": map[string]interface{}{
						"type":        "number",
						"default":     0.6,
						"description": "Minimum cosine similarity for semantic callees. 0 disables semantic enrichment.",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "assessChange",
			Description: "The post-change counterpart to prepareChange: use this AFTER changes are made to assess what you actually changed and what to verify before finishing. Defaults to the uncommitted working tree; also supports staged changes or a base-branch range. Answers: what might break? which tests should run? who needs to review? are there possible contract changes? For pre-change planning (before writing code), use prepareChange instead. For full PR review with quality gates, use reviewPR.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"diffContent": map[string]interface{}{
						"type":        "string",
						"description": "Raw git diff content. If empty, uses current working tree diff",
					},
					"staged": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "If true and no diffContent provided, analyze only staged changes (--cached)",
					},
					"baseBranch": map[string]interface{}{
						"type":        "string",
						"default":     "HEAD",
						"description": "Base branch for comparison when using git diff",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     2,
						"description": "Maximum depth for transitive impact analysis (1-4)",
					},
					"includeTests": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include test files in the analysis",
					},
					"strict": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Fail if SCIP index is stale",
					},
				},
			},
		},
		{
			Name:        "analyzeChange",
			Description: "Deprecated alias of assessChange (removed in two minor versions). Use this AFTER changes are made to analyze a git diff — answers: what might break? which tests should run? who needs to review? For pre-change planning (before writing code), use prepareChange instead. For full PR review with quality gates, use reviewPR.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"diffContent": map[string]interface{}{
						"type":        "string",
						"description": "Raw git diff content. If empty, uses current working tree diff",
					},
					"staged": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "If true and no diffContent provided, analyze only staged changes (--cached)",
					},
					"baseBranch": map[string]interface{}{
						"type":        "string",
						"default":     "HEAD",
						"description": "Base branch for comparison when using git diff",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     2,
						"description": "Maximum depth for transitive impact analysis (1-4)",
					},
					"includeTests": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include test files in the analysis",
					},
					"strict": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Fail if SCIP index is stale",
					},
				},
			},
		},
		{
			Name:        "explainSymbol",
			Description: "Get an AI-friendly explanation of a symbol including usage, history, and summary",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID to explain",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "justifySymbol",
			Description: "Get a keep/investigate/remove verdict for a symbol based on usage analysis",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The stable symbol ID to justify",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "getCallGraph",
			Description: "Get a lightweight call graph showing callers and callees of a symbol",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The root symbol ID for the call graph",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"callers", "callees", "both"},
						"default":     "both",
						"description": "Which direction to traverse",
					},
					"depth": map[string]interface{}{
						"type":        "number",
						"default":     1,
						"description": "Maximum depth to traverse (1-4)",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "getModuleOverview",
			Description: "Get a high-level overview of a module including size and recent activity",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the module directory",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Optional friendly name for the module",
					},
				},
			},
		},
		{
			Name:        "explainFile",
			Description: "Get lightweight orientation for a file including role, symbols, and key relationships",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file (relative or absolute)",
					},
				},
				"required": []string{"filePath"},
			},
		},
		{
			Name:        "listEntrypoints",
			Description: "List system entrypoints (API handlers, CLI mains, jobs) with ranking signals",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"moduleFilter": map[string]interface{}{
						"type":        "string",
						"description": "Optional filter to specific module",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     30,
						"description": "Maximum number of entrypoints to return",
					},
				},
			},
		},
		{
			Name:        "traceUsage",
			Description: "Trace how a symbol is reached from system entrypoints. Returns causal paths, not just neighbors.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The target symbol ID to trace usage for",
					},
					"maxPaths": map[string]interface{}{
						"type":        "number",
						"default":     10,
						"description": "Maximum number of paths to return",
					},
					"maxDepth": map[string]interface{}{
						"type":        "number",
						"default":     5,
						"description": "Maximum path depth to traverse (1-5)",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "summarizeDiff",
			Description: "Use this for a quick summary of what changed — compresses diffs into 'what changed, what might break'. Supports commit ranges, single commits, or time windows. For a full PR review with quality gates and scoring, use reviewPR instead. For branch-vs-branch comparison, use summarizePr.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"commitRange": map[string]interface{}{
						"type":        "object",
						"description": "Commit range selector (base..head)",
						"properties": map[string]interface{}{
							"base": map[string]interface{}{
								"type":        "string",
								"description": "Base commit hash or ref",
							},
							"head": map[string]interface{}{
								"type":        "string",
								"description": "Head commit hash or ref",
							},
						},
						"required": []string{"base", "head"},
					},
					"commit": map[string]interface{}{
						"type":        "string",
						"description": "Single commit hash to analyze",
					},
					"timeWindow": map[string]interface{}{
						"type":        "object",
						"description": "Time window selector",
						"properties": map[string]interface{}{
							"start": map[string]interface{}{
								"type":        "string",
								"description": "Start date (ISO8601)",
							},
							"end": map[string]interface{}{
								"type":        "string",
								"description": "End date (ISO8601)",
							},
						},
						"required": []string{"start"},
					},
				},
			},
		},
		{
			Name:        "getHotspots",
			Description: "Find files that deserve attention based on churn, coupling, and recency. Highlights volatile areas that may need review.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timeWindow": map[string]interface{}{
						"type":        "object",
						"description": "Time period to analyze (default: 30 days)",
						"properties": map[string]interface{}{
							"start": map[string]interface{}{
								"type":        "string",
								"description": "Start date (ISO8601 or YYYY-MM-DD)",
							},
							"end": map[string]interface{}{
								"type":        "string",
								"description": "End date (ISO8601 or YYYY-MM-DD)",
							},
						},
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Module path to focus on",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     20,
						"description": "Maximum number of hotspots to return (max 50)",
					},
				},
			},
		},
		{
			Name:        "explainPath",
			Description: "Explain why a path exists and what role it plays. Returns role classification (core, glue, legacy, test-only, config, unknown) with reasoning.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "Path to explain (relative or absolute)",
					},
					"contextHint": map[string]interface{}{
						"type":        "string",
						"description": "Optional context hint (e.g., 'from traceUsage')",
					},
				},
				"required": []string{"filePath"},
			},
		},
		{
			Name:        "listKeyConcepts",
			Description: "Discover main ideas/concepts in the codebase through semantic clustering. Helps understand domain vocabulary.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     12,
						"description": "Maximum number of concepts to return (max 12)",
					},
				},
			},
		},
		{
			Name:        "recentlyRelevant",
			Description: "Find what matters now - files/symbols with recent activity that may need attention.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"timeWindow": map[string]interface{}{
						"type":        "object",
						"description": "Time period to analyze (default: 7 days)",
						"properties": map[string]interface{}{
							"start": map[string]interface{}{
								"type":        "string",
								"description": "Start date (ISO8601 or YYYY-MM-DD)",
							},
							"end": map[string]interface{}{
								"type":        "string",
								"description": "End date (ISO8601 or YYYY-MM-DD)",
							},
						},
					},
					"moduleFilter": map[string]interface{}{
						"type":        "string",
						"description": "Module path to focus on",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     20,
						"description": "Maximum results to return",
					},
				},
			},
		},
		// v6.0 Architectural Memory tools
		{
			Name:        "refreshArchitecture",
			Description: "Rebuild the architectural model from sources. Use this to refresh ownership, modules, hotspots, or responsibilities data. Heavy operation (up to 30s). Use async=true for background processing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"all", "modules", "ownership", "hotspots", "responsibilities"},
						"default":     "all",
						"description": "What to refresh: 'all' (default), 'modules', 'ownership', 'hotspots', or 'responsibilities'",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Force refresh even if data is fresh",
					},
					"dryRun": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Preview what would be refreshed without making changes",
					},
					"async": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Run refresh in background and return immediately with a job ID. Use getJobStatus to check progress.",
					},
				},
			},
		},
		{
			Name:        "getOwnership",
			Description: "Get ownership information for files or paths. Returns owners from CODEOWNERS and git-blame with confidence scores. Use to identify who to contact for code review or questions.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File or directory path to get ownership for",
					},
					"includeBlame": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Whether to include git-blame ownership analysis",
					},
					"includeHistory": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Whether to include recent ownership change history",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "getModuleResponsibilities",
			Description: "Get responsibilities for modules. Returns what each module does, its capabilities, and how confident we are in this assessment. Extracted from README files, doc comments, and code analysis.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"moduleId": map[string]interface{}{
						"type":        "string",
						"description": "Specific module ID to get responsibilities for. Omit to get all modules.",
					},
					"includeFiles": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Whether to include file-level responsibilities",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum number of modules to return",
					},
				},
			},
		},
		{
			Name:        "recordDecision",
			Description: "Record an architectural decision (ADR). Creates both a markdown file and database entry. Use to document design decisions, rationale, and consequences.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{
						"type":        "string",
						"description": "Short title for the decision (e.g., 'Use PostgreSQL for persistence')",
					},
					"context": map[string]interface{}{
						"type":        "string",
						"description": "Background and forces driving the decision",
					},
					"decision": map[string]interface{}{
						"type":        "string",
						"description": "What was decided and why",
					},
					"consequences": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "List of consequences (positive and negative) of this decision",
					},
					"affectedModules": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "List of module IDs affected by this decision",
					},
					"alternatives": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "List of alternatives that were considered",
					},
					"author": map[string]interface{}{
						"type":        "string",
						"description": "Author of the decision",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"proposed", "accepted", "deprecated", "superseded"},
						"default":     "proposed",
						"description": "Status of the decision",
					},
				},
				"required": []string{"title", "context", "decision", "consequences"},
			},
		},
		{
			Name:        "getDecisions",
			Description: "Get architectural decisions (ADRs). Returns recorded decisions with their status, affected modules, and file paths. Use to understand past architectural choices.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type":        "string",
						"description": "Specific decision ID (e.g., 'ADR-001'). Returns single decision with full details.",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"proposed", "accepted", "deprecated", "superseded"},
						"description": "Filter by status",
					},
					"moduleId": map[string]interface{}{
						"type":        "string",
						"description": "Filter by affected module",
					},
					"search": map[string]interface{}{
						"type":        "string",
						"description": "Search in title and ID",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum number of decisions to return",
					},
				},
			},
		},
		{
			Name:        "annotateModule",
			Description: "Add or update module metadata (responsibilities, tags, boundaries). Enhances architectural understanding without modifying code.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"moduleId": map[string]interface{}{
						"type":        "string",
						"description": "Module ID (typically the directory path)",
					},
					"responsibility": map[string]interface{}{
						"type":        "string",
						"description": "One-sentence description of what this module does",
					},
					"capabilities": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "List of capabilities provided by this module",
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Tags for categorization (e.g., 'core', 'infrastructure', 'api')",
					},
					"publicPaths": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Paths intended as public API boundaries",
					},
					"internalPaths": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Paths intended to be internal/private",
					},
				},
				"required": []string{"moduleId"},
			},
		},
		// v6.1 Job management tools
		{
			Name:        "getJobStatus",
			Description: "Get the status and result of a background job. Use this to check on async operations like refreshArchitecture.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"jobId": map[string]interface{}{
						"type":        "string",
						"description": "The job ID returned from an async operation",
					},
				},
				"required": []string{"jobId"},
			},
		},
		{
			Name:        "listJobs",
			Description: "List recent background jobs. Use to find job IDs or check overall job history.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"queued", "running", "completed", "failed", "cancelled"},
						"description": "Filter by job status",
					},
					"type": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"refresh_architecture", "analyze_impact", "export"},
						"description": "Filter by job type",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum number of jobs to return",
					},
				},
			},
		},
		{
			Name:        "cancelJob",
			Description: "Cancel a queued or running background job.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"jobId": map[string]interface{}{
						"type":        "string",
						"description": "The job ID to cancel",
					},
				},
				"required": []string{"jobId"},
			},
		},
		// v6.1 CI/CD tools
		{
			Name:        "summarizePr",
			Description: "Use this for a PR summary with risk assessment — compares branches and returns affected modules, hotspots, and suggested reviewers. Lighter than reviewPR (no quality gates or scoring). For full review with 15 checks, use reviewPR. For commit-level diffs, use summarizeDiff.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"baseBranch": map[string]interface{}{
						"type":        "string",
						"description": "Base branch to compare against (default: 'main')",
					},
					"headBranch": map[string]interface{}{
						"type":        "string",
						"description": "Head branch (default: current HEAD)",
					},
					"includeOwnership": map[string]interface{}{
						"type":        "boolean",
						"description": "Include ownership analysis for reviewer suggestions (default: true)",
					},
				},
			},
		},
		{
			Name:        "getOwnershipDrift",
			Description: "Detect ownership drift by comparing CODEOWNERS declarations against actual git-blame ownership. Returns files where the declared owners differ significantly from who actually writes the code.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Module or directory path to analyze (default: entire repo)",
					},
					"threshold": map[string]interface{}{
						"type":        "number",
						"description": "Drift score threshold to report (0-1, default: 0.3)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum files to return (default: 20)",
					},
				},
			},
		},
		// v6.2 Federation tools
		{
			Name:        "listFederations",
			Description: "List all federations (cross-repo collections) available in CKB. A federation is a named collection of repositories that can be queried together.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "federationStatus",
			Description: "Get detailed status of a federation including repos, compatibility checks, and sync state.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to get status for",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationRepos",
			Description: "List repositories in a federation with their paths, tags, and compatibility status.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"includeCompatibility": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include schema compatibility status for each repo",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationSearchModules",
			Description: "Search for modules across all repositories in a federation. Use for cross-repo architectural analysis.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to search",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (FTS)",
					},
					"repos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of repo IDs to filter to",
					},
					"tags": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional tags to filter modules",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum results to return",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationSearchOwnership",
			Description: "Search for ownership across all repositories in a federation. Find who owns code matching a path pattern.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to search",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path glob pattern (e.g., '**/auth/**')",
					},
					"repos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of repo IDs to filter to",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum results to return",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationGetHotspots",
			Description: "Get merged hotspots across all repositories in a federation. Returns the most volatile code areas across the organization.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"repos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of repo IDs to filter to",
					},
					"top": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Number of top hotspots to return",
					},
					"minScore": map[string]interface{}{
						"type":        "number",
						"default":     0.3,
						"description": "Minimum score threshold (0-1)",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationSearchDecisions",
			Description: "Search for architectural decisions (ADRs) across all repositories in a federation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to search",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query (FTS)",
					},
					"status": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{"proposed", "accepted", "deprecated", "superseded"},
						},
						"description": "Filter by decision status",
					},
					"repos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of repo IDs to filter to",
					},
					"module": map[string]interface{}{
						"type":        "string",
						"description": "Filter by affected module",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum results to return",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationSync",
			Description: "Sync federation index from repository data. This reads modules, ownership, hotspots, and decisions from each repository and stores summaries for cross-repo queries.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to sync",
					},
					"force": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Force sync even if data is fresh",
					},
					"dryRun": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Preview what would be synced without making changes",
					},
					"repos": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of repo IDs to sync (default: all)",
					},
				},
				"required": []string{"federation"},
			},
		},
		// v7.3 Remote Federation tools (Phase 5)
		{
			Name:        "federationAddRemote",
			Description: "Add a remote CKB index server to a federation. The remote server will be queried alongside local repositories.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name for the remote server",
					},
					"url": map[string]interface{}{
						"type":        "string",
						"description": "URL of the remote index server",
					},
					"token": map[string]interface{}{
						"type":        "string",
						"description": "Auth token (supports ${ENV_VAR} expansion)",
					},
					"cacheTtl": map[string]interface{}{
						"type":        "string",
						"default":     "1h",
						"description": "Cache TTL (e.g., 15m, 1h)",
					},
					"timeout": map[string]interface{}{
						"type":        "string",
						"default":     "30s",
						"description": "Request timeout",
					},
				},
				"required": []string{"federation", "name", "url"},
			},
		},
		{
			Name:        "federationRemoveRemote",
			Description: "Remove a remote server from a federation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the remote server to remove",
					},
				},
				"required": []string{"federation", "name"},
			},
		},
		{
			Name:        "federationListRemote",
			Description: "List remote servers configured in a federation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationSyncRemote",
			Description: "Sync metadata from remote server(s). If name is provided, syncs that server only; otherwise syncs all enabled servers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Optional server name to sync (default: all)",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "federationStatusRemote",
			Description: "Check remote server connectivity and status.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the remote server",
					},
				},
				"required": []string{"federation", "name"},
			},
		},
		{
			Name:        "federationSearchSymbolsHybrid",
			Description: "Search symbols across local federation and remote servers. Returns results with source attribution.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     100,
						"description": "Maximum results to return",
					},
					"language": map[string]interface{}{
						"type":        "string",
						"description": "Filter by language",
					},
					"kind": map[string]interface{}{
						"type":        "string",
						"description": "Filter by symbol kind",
					},
					"servers": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Optional list of server names to query (default: all enabled)",
					},
				},
				"required": []string{"federation", "query"},
			},
		},
		{
			Name:        "federationListAllRepos",
			Description: "List all repositories from local federation and remote servers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
				},
				"required": []string{"federation"},
			},
		},
		// v6.2.1 Daemon tools
		{
			Name:        "daemonStatus",
			Description: "Get CKB daemon status including health, uptime, and component states. Returns information about the always-on daemon service.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "listSchedules",
			Description: "List scheduled tasks in the daemon. Shows automated refresh schedules, federation syncs, and other recurring tasks.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"taskType": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"refresh", "federation_sync", "cleanup", "health_check"},
						"description": "Filter by task type",
					},
					"enabled": map[string]interface{}{
						"type":        "boolean",
						"description": "Filter by enabled status",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum schedules to return",
					},
				},
			},
		},
		{
			Name:        "runSchedule",
			Description: "Immediately run a scheduled task. Useful for testing schedules or triggering updates manually.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scheduleId": map[string]interface{}{
						"type":        "string",
						"description": "ID of the schedule to run",
					},
				},
				"required": []string{"scheduleId"},
			},
		},
		{
			Name:        "listWebhooks",
			Description: "List configured webhooks for CKB event notifications. Shows endpoints, events subscribed, and delivery status.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "testWebhook",
			Description: "Send a test event to a webhook endpoint. Useful for verifying webhook configuration.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"webhookId": map[string]interface{}{
						"type":        "string",
						"description": "ID of the webhook to test",
					},
				},
				"required": []string{"webhookId"},
			},
		},
		{
			Name:        "webhookDeliveries",
			Description: "Get delivery history for a webhook. Shows recent delivery attempts, successes, and failures.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"webhookId": map[string]interface{}{
						"type":        "string",
						"description": "ID of the webhook",
					},
					"status": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"queued", "pending", "delivered", "failed", "dead"},
						"description": "Filter by delivery status",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum deliveries to return",
					},
				},
				"required": []string{"webhookId"},
			},
		},
		// v6.3 Contract-Aware Impact Analysis tools
		{
			Name:        "listContracts",
			Description: "List API contracts (protobuf, OpenAPI) in a federation. Returns detected contracts with their visibility classification.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation to query",
					},
					"repoId": map[string]interface{}{
						"type":        "string",
						"description": "Filter to contracts from this repo",
					},
					"contractType": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"proto", "openapi"},
						"description": "Filter by contract type",
					},
					"visibility": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"public", "internal", "unknown"},
						"description": "Filter by visibility",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum contracts to return",
					},
				},
				"required": []string{"federation"},
			},
		},
		{
			Name:        "analyzeContractImpact",
			Description: "Analyze the impact of changing an API contract. Returns direct and transitive consumers, risk assessment, and ownership information.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"repoId": map[string]interface{}{
						"type":        "string",
						"description": "Repository containing the contract",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the contract file",
					},
					"includeHeuristic": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include tier 3 (heuristic) edges",
					},
					"includeTransitive": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include transitive consumers",
					},
					"maxDepth": map[string]interface{}{
						"type":        "integer",
						"default":     3,
						"description": "Maximum depth for transitive analysis",
					},
				},
				"required": []string{"federation", "repoId", "path"},
			},
		},
		{
			Name:        "getContractDependencies",
			Description: "Get contract dependencies for a repository. Shows both contracts this repo depends on and consumers of contracts this repo provides.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"repoId": map[string]interface{}{
						"type":        "string",
						"description": "Repository to analyze",
					},
					"moduleId": map[string]interface{}{
						"type":        "string",
						"description": "Optional module filter",
					},
					"direction": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"consumers", "dependencies", "both"},
						"default":     "both",
						"description": "Which direction to query",
					},
					"includeHeuristic": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include tier 3 (heuristic) edges",
					},
				},
				"required": []string{"federation", "repoId"},
			},
		},
		{
			Name:        "suppressContractEdge",
			Description: "Suppress a false positive contract dependency edge. The edge will be hidden from analysis results.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"edgeId": map[string]interface{}{
						"type":        "integer",
						"description": "ID of the edge to suppress",
					},
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "Reason for suppression",
					},
				},
				"required": []string{"federation", "edgeId"},
			},
		},
		{
			Name:        "verifyContractEdge",
			Description: "Mark a contract dependency edge as verified. Increases confidence in the edge.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
					"edgeId": map[string]interface{}{
						"type":        "integer",
						"description": "ID of the edge to verify",
					},
				},
				"required": []string{"federation", "edgeId"},
			},
		},
		{
			Name:        "getContractStats",
			Description: "Get contract statistics for a federation. Returns counts of contracts, edges, and breakdown by type.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Name of the federation",
					},
				},
				"required": []string{"federation"},
			},
		},
		// v6.2.2 Tree-sitter Complexity tools
		{
			Name:        "getFileComplexity",
			Description: "Get code complexity metrics for a source file using tree-sitter parsing. Returns cyclomatic and cognitive complexity for each function, plus file-level aggregates. Supports Go, JavaScript, TypeScript, Python, Rust, Java, and Kotlin.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "Path to the source file (relative or absolute)",
					},
					"includeFunctions": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include per-function complexity breakdown",
					},
					"sortBy": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"cyclomatic", "cognitive", "lines"},
						"default":     "cyclomatic",
						"description": "Sort functions by this metric (descending)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum number of functions to return (most complex first)",
					},
				},
				"required": []string{"filePath"},
			},
		},
		// v6.4 Telemetry tools
		{
			Name:        "getTelemetryStatus",
			Description: "Get telemetry system status including coverage metrics, last sync time, and unmapped services. Use this to check if telemetry is enabled and working correctly.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "getObservedUsage",
			Description: "Get runtime observed usage for a symbol. Returns call counts, trend direction, match quality, and optionally caller breakdown. Requires telemetry to be enabled.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolId": map[string]interface{}{
						"type":        "string",
						"description": "The symbol ID to get usage for",
					},
					"period": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"7d", "30d", "90d", "all"},
						"default":     "90d",
						"description": "Time period to analyze",
					},
					"includeCallers": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include caller service breakdown (if enabled)",
					},
				},
				"required": []string{"symbolId"},
			},
		},
		{
			Name:        "findDeadCodeCandidates",
			Description: "Find symbols that may be dead code based on observed runtime telemetry. Returns candidates with confidence scores. Only works with medium+ telemetry coverage.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"repoId": map[string]interface{}{
						"type":        "string",
						"description": "Repository ID to analyze. If omitted, analyzes current repo.",
					},
					"minConfidence": map[string]interface{}{
						"type":        "number",
						"default":     0.7,
						"description": "Minimum confidence threshold (0-1)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     100,
						"description": "Maximum candidates to return",
					},
				},
			},
		},
		// v7.6 Static Dead Code Detection (SCIP-based, no telemetry required)
		{
			Name:        "findDeadCode",
			Description: "Use this to find dead code — detects symbols with zero references, self-only references, test-only references, and over-exported symbols. Uses SCIP index for precision. For runtime-based dead code detection (what's actually called), use findDeadCodeCandidates with telemetry data.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Limit analysis to specific packages/paths (e.g., ['internal/legacy', 'pkg/utils'])",
					},
					"includeUnexported": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include unexported (private) symbols in analysis",
					},
					"minConfidence": map[string]interface{}{
						"type":        "number",
						"default":     0.7,
						"description": "Minimum confidence threshold (0-1). Higher = fewer false positives",
					},
					"excludePatterns": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Glob patterns to exclude (e.g., ['*_generated.go', 'mocks/*'])",
					},
					"includeTestOnly": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Report symbols only used by tests as dead code",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     100,
						"description": "Maximum results to return",
					},
				},
			},
		},
		// Unwired Module Detection
		{
			Name:        "findUnwiredModules",
			Description: "Find exported symbols that are never transitively reachable from application entrypoints (main, server, CLI). Detects the 'built but never plugged in' pattern where modules exist and are tested but aren't wired into the execution pipeline. Complements findDeadCode which only checks reference counts.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Limit analysis to specific packages/paths",
					},
					"minConfidence": map[string]interface{}{
						"type":        "number",
						"default":     0.80,
						"description": "Minimum confidence threshold (0-1)",
					},
					"includeTypes": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include type definitions (higher false positive rate)",
					},
					"excludePatterns": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Glob patterns to exclude",
					},
					"maxNodes": map[string]interface{}{
						"type":        "integer",
						"default":     10000,
						"description": "Max symbols in reachable set (budget for BFS traversal)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     100,
						"description": "Maximum results to return",
					},
				},
			},
		},
		// v7.6 Affected Tests Tool
		{
			Name:        "getAffectedTests",
			Description: "Use this to find which tests to run after code changes — traces from changed symbols to test files using SCIP analysis. Returns test files ranked by relevance. For finding untested code (gaps), use analyzeTestGaps instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"staged": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Only analyze staged changes (git add)",
					},
					"baseBranch": map[string]interface{}{
						"type":        "string",
						"default":     "HEAD",
						"description": "Base branch/commit to compare against",
					},
					"depth": map[string]interface{}{
						"type":        "integer",
						"default":     1,
						"description": "Maximum depth for transitive impact analysis (1-3)",
					},
					"useCoverage": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Use coverage data if available for more accurate mapping",
					},
				},
			},
		},
		// v7.6 Breaking Change Detection Tool
		{
			Name:        "compareAPI",
			Description: "Use this to detect breaking API changes between git refs — finds removed symbols, signature changes, visibility changes, and renames. Essential for release planning and backwards compatibility. For general change impact (not API-specific), use analyzeChange.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"baseRef": map[string]interface{}{
						"type":        "string",
						"default":     "HEAD~1",
						"description": "Base git ref for comparison (e.g., 'v1.0.0', 'main')",
					},
					"targetRef": map[string]interface{}{
						"type":        "string",
						"default":     "HEAD",
						"description": "Target git ref for comparison (e.g., 'HEAD', 'v2.0.0')",
					},
					"scope": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Limit analysis to specific packages/paths",
					},
					"includeMinor": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include non-breaking changes (additions) in output",
					},
				},
			},
		},
		// v6.5 Developer Intelligence tools
		{
			Name:        "explainOrigin",
			Description: "Explain why code exists: origin commit, evolution history, co-changes, and warnings. Answers 'why does this code exist?' with full context.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol": map[string]interface{}{
						"type":        "string",
						"description": "Symbol to explain (file path, file:line, or symbol name)",
					},
					"includeUsage": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include telemetry usage data if available",
					},
					"includeCoChange": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include co-change analysis",
					},
					"historyLimit": map[string]interface{}{
						"type":        "integer",
						"default":     10,
						"description": "Number of timeline entries to include",
					},
				},
				"required": []string{"symbol"},
			},
		},
		{
			Name:        "analyzeCoupling",
			Description: "Find files/symbols that historically change together. Reveals hidden coupling that static analysis misses.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "File or symbol to analyze",
					},
					"minCorrelation": map[string]interface{}{
						"type":        "number",
						"default":     0.3,
						"description": "Minimum correlation threshold (0-1)",
					},
					"windowDays": map[string]interface{}{
						"type":        "integer",
						"default":     365,
						"description": "Analysis window in days",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Maximum results to return",
					},
				},
				"required": []string{"target"},
			},
		},
		{
			Name:        "exportForLLM",
			Description: "Export codebase structure in LLM-friendly format. Includes symbols, complexity, usage, and ownership.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"federation": map[string]interface{}{
						"type":        "string",
						"description": "Export entire federation (optional)",
					},
					"includeUsage": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include telemetry usage data",
					},
					"includeOwnership": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include owner annotations",
					},
					"includeContracts": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include contract indicators",
					},
					"includeComplexity": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include complexity scores",
					},
					"minComplexity": map[string]interface{}{
						"type":        "integer",
						"description": "Only include symbols with complexity >= N",
					},
					"minCalls": map[string]interface{}{
						"type":        "integer",
						"description": "Only include symbols with calls/day >= N",
					},
					"maxSymbols": map[string]interface{}{
						"type":        "integer",
						"description": "Limit total symbols",
					},
				},
			},
		},
		{
			Name:        "auditRisk",
			Description: "Use this to find risky code areas — scores files/symbols using 8 weighted signals: complexity, test coverage, bus factor, staleness, security sensitivity, error rate, coupling, and churn. Returns risk scores with top contributing factors. For regulatory compliance risk, use auditCompliance instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"minScore": map[string]interface{}{
						"type":        "number",
						"default":     40,
						"description": "Minimum risk score to include (0-100)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum items to return",
					},
					"factor": map[string]interface{}{
						"type":        "string",
						"description": "Filter by specific risk factor",
						"enum":        []string{"complexity", "test_coverage", "bus_factor", "staleness", "security_sensitive", "error_rate", "co_change_coupling", "churn"},
					},
					"quickWins": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Only show quick wins (low effort, high impact)",
					},
				},
			},
		},
		// v8.6 cartographer context tools
		{
			Name:        "detectShotgunSurgery",
			Description: "Detect files exhibiting the shotgun surgery smell: a change to them historically required simultaneous edits across many unrelated files. Ranks results by co-change dispersion score. Use before large refactors to identify high-blast-radius files.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "Max number of commits to analyse (default 100)",
					},
					"min_partners": map[string]interface{}{
						"type":        "integer",
						"description": "Minimum co-change partner count to qualify as a suspect (default 3)",
					},
				},
			},
		},
		{
			Name:        "getArchitecturalEvolution",
			Description: "Show how architectural health (health score, debt indicators) has changed over git history. Returns snapshots ranked by time with a trend label (improving/stable/degrading) and actionable recommendations.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"days": map[string]interface{}{
						"type":        "integer",
						"description": "Number of days of git history to scan (default 90)",
					},
				},
			},
		},
		{
			Name:        "getBlastRadius",
			Description: "Graph-theoretic blast radius for a file or module: returns direct dependents and dependencies up to max_related hops. Works without a SCIP index; complements analyzeImpact for repos without indexing.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Repo-relative file path or module ID to analyse",
					},
					"max_related": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum related modules to return (default 50)",
					},
				},
				"required": []string{"target"},
			},
		},
		{
			Name:        "renderArchitecture",
			Description: "Render the project's module-level import graph as a Mermaid or Graphviz (DOT) diagram, ready to paste into IDEs that render Mermaid inline (Cursor, Claude Desktop, VS Code markdown preview, GitHub). With `focus` set, returns a BFS neighborhood (both imports and imported-by) around the given module to depth `depth`; without `focus`, returns the top-N most-connected nodes as an at-a-glance shape of the codebase. Response includes `truncated: true` when the node cap kicked in — tighten focus or lower depth to get a complete view.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"format": map[string]interface{}{
						"type":        "string",
						"description": "Output diagram format (default mermaid)",
						"enum":        []string{"mermaid", "dot"},
						"default":     "mermaid",
					},
					"focus": map[string]interface{}{
						"type":        "string",
						"description": "Optional anchor: module ID, repo-relative file path, or path suffix (e.g. 'server.rs'). When set, the diagram is a BFS neighborhood around this node; when absent, returns the top-N most-connected nodes.",
					},
					"depth": map[string]interface{}{
						"type":        "integer",
						"description": "BFS depth from `focus` over undirected import edges (default 2). Ignored when `focus` is absent.",
						"default":     2,
					},
					"max_nodes": map[string]interface{}{
						"type":        "integer",
						"description": "Cap on nodes rendered; response sets `truncated: true` if the cap was hit (default 40).",
						"default":     40,
					},
				},
			},
		},
		{
			Name:        "queryContext",
			Description: "Retrieve the most relevant code context for a task or question. Runs the cartographer PKG retrieval pipeline: BM25 content search → personalized PageRank skeleton → context health scoring. Returns a ready-to-use context bundle with token count and A–F quality grade. Use this before starting any non-trivial coding task.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Natural language description of the task or question (e.g. 'add pagination to the user list API')",
					},
					"budget": map[string]interface{}{
						"type":        "integer",
						"default":     8000,
						"description": "Token budget for the skeleton portion",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"default":     "claude",
						"description": "Target model family for context window sizing: claude (200K), gpt4 (128K), llama (128K), gpt35 (16K)",
						"enum":        []string{"claude", "gpt4", "llama", "gpt35"},
					},
					"maxSearchResults": map[string]interface{}{
						"type":        "integer",
						"default":     20,
						"description": "Max BM25 search hits used as PageRank personalization seeds",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "contextHealth",
			Description: "Score the quality of an LLM context bundle on 6 research-backed metrics: signal density, compression density, position health (U-shaped attention bias), entity density, utilisation headroom, and dedup ratio. Returns a composite 0–100 score graded A–F with per-metric breakdown and actionable recommendations.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"content": map[string]interface{}{
						"type":        "string",
						"description": "The context string to evaluate (what you would send to the LLM)",
					},
					"model": map[string]interface{}{
						"type":        "string",
						"default":     "claude",
						"description": "Target model family for window-size reference: claude (200K), gpt4 (128K), llama (128K), gpt35 (16K)",
						"enum":        []string{"claude", "gpt4", "llama", "gpt35"},
					},
					"signatureCount": map[string]interface{}{
						"type":        "integer",
						"description": "Number of symbol signatures in the content (improves signal density scoring)",
					},
				},
				"required": []string{"content"},
			},
		},
		// v8.0 Secret Detection
		{
			Name:        "scanSecrets",
			Description: "Use this to find exposed secrets (API keys, tokens, passwords) in specific files or paths. For a broader security/compliance check across regulations, use auditCompliance with iso27001 or owasp-asvs. Returns findings with redacted context, entropy scores, and confidence levels.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"workdir", "staged", "history"},
						"default":     "workdir",
						"description": "What to scan: workdir (current files), staged (git staged only), history (git commits)",
					},
					"paths": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Limit scan to these paths (glob patterns)",
					},
					"excludePaths": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Skip these paths (e.g., vendor/*, node_modules/*)",
					},
					"minSeverity": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"critical", "high", "medium", "low"},
						"default":     "low",
						"description": "Minimum severity to report",
					},
					"sinceCommit": map[string]interface{}{
						"type":        "string",
						"description": "For history scope: scan commits since this ref",
					},
					"maxCommits": map[string]interface{}{
						"type":        "integer",
						"default":     100,
						"description": "For history scope: maximum commits to scan",
					},
					"useGitleaks": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Use gitleaks if available (more comprehensive patterns)",
					},
					"useTrufflehog": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Use trufflehog if available (verified secrets detection)",
					},
					"preferExternal": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Prefer external tools over builtin when available",
					},
					"applyAllowlist": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Apply configured allowlist to filter false positives",
					},
				},
			},
		},
		// v8.3 Compliance Audit
		{
			Name:        "auditCompliance",
			Description: "Audit codebase against 20 regulatory compliance frameworks (131 checks). Use this when asked about compliance, regulations, or security posture. Specify frameworks by domain: privacy (gdpr, ccpa), security (iso27001, owasp-asvs, nist-800-53), payments (pci-dss), healthcare (hipaa), EU (dora, nis2, eu-cra, eu-ai-act), safety (iec61508, iso26262, misra), or 'all'. Returns verdict (pass/warn/fail), per-framework scores, findings mapped to specific regulation articles (e.g., 'Art. 32 GDPR', 'Req 6.2.4 PCI DSS 4.0') with CWE references and confidence scores. Cross-framework mapping means one finding shows all applicable regulations. Use --scope to limit scan to specific directories.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"frameworks": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Framework IDs to audit: gdpr, ccpa, iso27701, eu-ai-act, iso27001, nist-800-53, owasp-asvs, soc2, pci-dss, hipaa, dora, nis2, fda-21cfr11, eu-cra, sbom-slsa, iec61508, iso26262, do-178c, misra, iec62443, or 'all'",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Path prefix to limit scan scope (e.g., 'internal/auth/')",
					},
					"minConfidence": map[string]interface{}{
						"type":        "number",
						"default":     0.5,
						"description": "Minimum confidence threshold (0.0-1.0) to include findings",
					},
					"silLevel": map[string]interface{}{
						"type":        "number",
						"default":     2,
						"description": "SIL level for IEC 61508 checks (1-4)",
					},
					"checks": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Filter to specific check IDs",
					},
					"failOn": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"error", "warning", "none"},
						"default":     "error",
						"description": "Severity threshold for verdict failure",
					},
				},
				"required": []string{"frameworks"},
			},
		},
		// v8.2 Unified PR Review
		{
			Name:        "reviewPR",
			Description: "Run a comprehensive PR review with 15 quality checks. Use this FIRST when reviewing a PR — it gives you structural context so you can focus on what matters. Checks: breaking changes, secrets, test coverage, complexity, health, coupling, hotspots, risk, dead code, test gaps, blast radius, comment drift, format consistency, bug patterns, split suggestions. Returns verdict (pass/warn/fail), score (0-100), findings with file:line, health report, review effort estimate, PR tier, and suggested reviewers. Follow up with findReferences, analyzeImpact, or explainSymbol to drill into specific findings — the SCIP index stays loaded between calls.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"baseBranch": map[string]interface{}{
						"type":        "string",
						"default":     "main",
						"description": "Base branch to compare against",
					},
					"headBranch": map[string]interface{}{
						"type":        "string",
						"description": "Head branch (default: current branch)",
					},
					"checks": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Run only these checks (allowlist): breaking, secrets, tests, complexity, coupling, hotspots, risk, critical, generated, classify, split, health, traceability, independence, dead-code, test-gaps, blast-radius, comment-drift, format-consistency, bug-patterns",
					},
					"skip": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Skip these checks (denylist) — complement of 'checks'. Useful on large repos where a full SCIP re-index is expensive: skip=[\"dead-code\",\"blast-radius\",\"unwired\"] runs everything else at full accuracy.",
					},
					"staged": map[string]interface{}{
						"type":        "boolean",
						"description": "Review staged changes instead of branch diff",
					},
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Filter to path prefix (e.g., internal/query/) or symbol name",
					},
					"compact": map[string]interface{}{
						"type":        "boolean",
						"description": "Return compact response (~1k tokens) instead of full response (~30k tokens). Recommended for LLM consumers. Use full response only when you need raw finding details.",
					},
					"failOnLevel": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"error", "warning", "none"},
						"description": "Override when to fail: error (default), warning, or none",
					},
					"criticalPaths": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Glob patterns for safety-critical paths (e.g., drivers/**, protocol/**)",
					},
				},
			},
		},
		// v7.3 Doc-Symbol Linking tools
		{
			Name:        "getDocsForSymbol",
			Description: "Find documentation that references a symbol. Returns docs mentioning the symbol with context and line numbers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol": map[string]interface{}{
						"type":        "string",
						"description": "Symbol name or ID to search for (e.g., 'Engine.Start', 'internal/query.Engine')",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     10,
						"description": "Maximum number of doc references to return",
					},
				},
				"required": []string{"symbol"},
			},
		},
		{
			Name:        "getSymbolsInDoc",
			Description: "List all symbol references found in a documentation file. Shows resolution status and line numbers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the documentation file (relative to repo root)",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "getDocsForModule",
			Description: "Find documentation explicitly linked to a module via directives. Docs link to modules using <!-- ckb:module path/to/module --> directives.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"moduleId": map[string]interface{}{
						"type":        "string",
						"description": "Module ID (directory path) to find docs for",
					},
				},
				"required": []string{"moduleId"},
			},
		},
		{
			Name:        "checkDocStaleness",
			Description: "Check documentation for stale symbol references. A reference is stale if the symbol no longer exists, is ambiguous, or the language is not indexed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to check (optional, omit for all docs)",
					},
					"all": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Check all indexed documentation",
					},
				},
			},
		},
		{
			Name:        "indexDocs",
			Description: "Scan and index documentation for symbol references. By default uses incremental indexing (skips unchanged files).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"force": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Force re-index all docs even if unchanged",
					},
				},
			},
		},
		{
			Name:        "getDocCoverage",
			Description: "Get documentation coverage statistics. Reports how many symbols are documented and which high-centrality symbols are missing documentation.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"exportedOnly": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Only count exported/public symbols",
					},
					"topN": map[string]interface{}{
						"type":        "integer",
						"default":     10,
						"description": "Number of top undocumented symbols to return",
					},
				},
			},
		},
		// v7.3 Multi-Repo Management tools
		{
			Name:        "listRepos",
			Description: "List all registered repositories with their state and active status. Shows which repos are valid, uninitialized, or missing.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		{
			Name:        "switchRepo",
			Description: "Switch to a different repository. The repository must be registered and initialized. Use listRepos to see available repos.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the repository to switch to (from registry)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "getActiveRepo",
			Description: "Get information about the currently active repository including name, path, and state.",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		// v8.1 Dynamic project switching
		{
			Name:        "switchProject",
			Description: "Switch CKB to a different project directory. Use this when CKB is indexed for the wrong project. Accepts any git repository path — it does not need to be pre-registered.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Absolute path to the project directory (must be a git repository with CKB initialized)",
					},
				},
				"required": []string{"path"},
			},
		},
		// v8.0 Compound Tools - aggregate multiple queries to reduce tool calls
		{
			Name:        "explore",
			Description: "Use this FIRST when asked about a file, directory, or module — returns structure, key symbols, and change hotspots in one call. Best starting point for 'what is this?' or 'what's in this directory?' questions. For questions about a specific symbol, use understand instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "File, directory, or module path to explore",
					},
					"depth": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"shallow", "standard", "deep"},
						"default":     "standard",
						"description": "Exploration thoroughness: shallow (quick overview), standard (balanced), deep (comprehensive)",
					},
					"focus": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"structure", "dependencies", "changes"},
						"default":     "structure",
						"description": "Aspect to emphasize: structure (symbols/types), dependencies (imports/exports), changes (hotspots/history)",
					},
				},
				"required": []string{"target"},
			},
		},
		{
			Name:        "understand",
			Description: "Use this when asked about a specific symbol — returns full context (definition, references, call graph, explanation) in one call. Ideal for 'what does X do?' or 'how is X used?' questions. For file/directory-level questions, use explore instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Symbol name or ID to understand",
					},
					"includeReferences": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include reference information grouped by file",
					},
					"includeCallGraph": map[string]interface{}{
						"type":        "boolean",
						"default":     true,
						"description": "Include callers and callees",
					},
					"maxReferences": map[string]interface{}{
						"type":        "number",
						"default":     50,
						"description": "Maximum references to include",
					},
				},
				"required": []string{"query"},
			},
		},
		{
			Name:        "prepareChange",
			Description: "Use this BEFORE making a code change — returns blast radius, affected tests, coupled files, and risk score. Essential before modifying, renaming, deleting, or moving code. More comprehensive than analyzeImpact (includes tests + coupling). For analyzing changes already made, use analyzeChange instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "Symbol ID or file path to analyze",
					},
					"changeType": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"modify", "rename", "delete", "extract", "move"},
						"default":     "modify",
						"description": "Type of change being planned",
					},
					"targetPath": map[string]interface{}{
						"type":        "string",
						"description": "Destination path for move operations (required when changeType is 'move')",
					},
					"startLine": map[string]interface{}{
						"type":        "integer",
						"description": "Start line of extraction region (for extract operations)",
					},
					"endLine": map[string]interface{}{
						"type":        "integer",
						"description": "End line of extraction region (for extract operations)",
					},
					"format": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"full", "compact"},
						"default":     "full",
						"description": "Response format: 'full' (default) returns all details; 'compact' returns a token-efficient summary with top affected files and tests",
					},
				},
				"required": []string{"target"},
			},
		},
		{
			Name:        "batchGet",
			Description: "Retrieve multiple symbols by ID in a single call. Max 50 symbols. With includeCounts, also returns referenceCount, callerCount, calleeCount per symbol.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbolIds": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Array of symbol IDs to retrieve (max 50)",
					},
					"includeCounts": map[string]interface{}{
						"type":        "boolean",
						"default":     false,
						"description": "Include referenceCount, callerCount, calleeCount per symbol (adds SCIP lookups per symbol)",
					},
				},
				"required": []string{"symbolIds"},
			},
		},
		{
			Name:        "batchSearch",
			Description: "Perform multiple symbol searches in a single call. Max 10 queries.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"queries": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"query": map[string]interface{}{
									"type":        "string",
									"description": "Search query",
								},
								"kind": map[string]interface{}{
									"type":        "string",
									"description": "Optional kind filter",
								},
								"scope": map[string]interface{}{
									"type":        "string",
									"description": "Optional module scope",
								},
								"limit": map[string]interface{}{
									"type":        "number",
									"default":     10,
									"description": "Max results per query",
								},
							},
							"required": []string{"query"},
						},
						"description": "Array of search queries (max 10)",
					},
				},
				"required": []string{"queries"},
			},
		},
		// v8.1 Test Gap Analysis
		{
			Name:        "analyzeTestGaps",
			Description: "Use this to find untested functions — returns functions without test coverage, sorted by complexity (highest-risk first). For finding which existing tests cover specific changes, use getAffectedTests instead.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "File or directory path to analyze (relative to repo root)",
					},
					"minLines": map[string]interface{}{
						"type":        "integer",
						"default":     3,
						"description": "Minimum function lines to include (skips trivial getters/setters)",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"default":     50,
						"description": "Maximum number of gaps to return",
					},
				},
				"required": []string{"target"},
			},
		},
		// v8.1 Unified Refactor Planning
		{
			Name:        "planRefactor",
			Description: "Plan a refactoring operation with combined risk assessment, impact analysis, test strategy, and ordered steps. Aggregates prepareChange + auditRisk + analyzeTestGaps into a single call.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target": map[string]interface{}{
						"type":        "string",
						"description": "File path or symbol ID to refactor",
					},
					"changeType": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"modify", "rename", "delete", "extract", "move"},
						"default":     "modify",
						"description": "Type of refactoring change",
					},
					"targetPath": map[string]interface{}{
						"type":        "string",
						"description": "Destination path for move operations",
					},
				},
				"required": []string{"target"},
			},
		},
		// v8.1 Cycle Detection
		{
			Name:        "findCycles",
			Description: "Detect circular dependencies in module/directory/file dependency graphs. Uses Tarjan's SCC algorithm and suggests which edge to break (lowest import count).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"granularity": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"module", "directory", "file"},
						"default":     "directory",
						"description": "Level of analysis granularity",
					},
					"targetPath": map[string]interface{}{
						"type":        "string",
						"description": "Optional path to focus on (relative to repo root)",
					},
					"maxCycles": map[string]interface{}{
						"type":        "number",
						"default":     20,
						"description": "Maximum number of cycles to return",
					},
				},
			},
		},
		// v8.4 Performance scan
		{
			Name:        "scanPerformance",
			Description: "Scan for structural performance problems. Detects hidden coupling: file pairs that co-change frequently in git history but have no static import edge between them. Hidden coupling indicates implicit shared state or behavioral coupling that the dependency graph cannot see, and is the highest-signal structural risk for refactoring and maintenance cost.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"minCorrelation": map[string]interface{}{
						"type":        "number",
						"default":     0.3,
						"description": "Minimum co-change correlation threshold (0–1). Higher values return only the most tightly coupled pairs.",
					},
					"minCoChanges": map[string]interface{}{
						"type":        "number",
						"default":     3,
						"description": "Minimum number of shared commits. Filters out spurious pairs from low-activity files.",
					},
					"windowDays": map[string]interface{}{
						"type":        "number",
						"default":     365,
						"description": "Git history window in days.",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     50,
						"description": "Maximum number of hidden-coupling pairs to return.",
					},
					"scope": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Limit analysis to these repo-relative paths. Empty means whole repo.",
					},
				},
			},
		},
		// v8.5 Structural performance scan (loop call sites in hot files)
		{
			Name: "analyzeStructuralPerf",
			Description: "Detect structural performance anti-patterns in high-churn files. " +
				"Uses tree-sitter to find call expressions inside loop bodies — the primary " +
				"structural signal for O(n) and O(n²) hidden costs that do not appear in " +
				"profiling until production load. Hot files (frequently changed in git history) " +
				"are prioritized; loop call sites in system entrypoints are ranked higher. " +
				"Complements scanPerformance (which detects hidden coupling) by targeting " +
				"intra-file loop patterns rather than cross-file co-change.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"windowDays": map[string]interface{}{
						"type":        "number",
						"default":     90,
						"description": "Git history window in days for identifying hot files.",
					},
					"minChurnCount": map[string]interface{}{
						"type":        "number",
						"default":     3,
						"description": "Minimum number of commits for a file to be considered hot.",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     100,
						"description": "Maximum number of loop call sites to return.",
					},
					"scope": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
						},
						"description": "Limit analysis to these repo-relative paths. Empty means whole repo.",
					},
				},
			},
		},
		// v8.1 Suggested Refactorings
		{
			Name:        "suggestRefactorings",
			Description: "Proactively detect refactoring opportunities by analyzing complexity, coupling, dead code, and test gaps. Returns a prioritized list of actionable suggestions.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"scope": map[string]interface{}{
						"type":        "string",
						"description": "Directory or file path to analyze (relative to repo root)",
					},
					"minSeverity": map[string]interface{}{
						"type":        "string",
						"enum":        []string{"critical", "high", "medium", "low"},
						"default":     "low",
						"description": "Minimum severity level to include",
					},
					"types": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "string",
							"enum": []string{"extract_function", "split_file", "reduce_coupling", "remove_dead_code", "add_tests", "simplify_function"},
						},
						"description": "Filter by suggestion types (empty = all)",
					},
					"limit": map[string]interface{}{
						"type":        "number",
						"default":     50,
						"description": "Maximum number of suggestions to return",
					},
				},
			},
		},
		// v9.0 LIP symbol annotations
		{
			Name:        "annotationSet",
			Description: "Attach a key/value annotation to a symbol URI. Annotations survive context resets and are scoped to the symbol, not the module. Mirrors LIP AnnotationEntry.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol_uri": map[string]interface{}{"type": "string", "description": "LIP symbol URI, e.g. lip://local/src/foo.go#MyFunc"},
					"key":        map[string]interface{}{"type": "string", "description": "Annotation key"},
					"value":      map[string]interface{}{"type": "string", "description": "Annotation value (any string)"},
					"author_id":  map[string]interface{}{"type": "string", "description": "Author identifier (default: agent:ckb)"},
					"confidence": map[string]interface{}{"type": "number", "description": "Confidence 0-100 (default: 80)"},
				},
				"required": []string{"symbol_uri", "key", "value"},
			},
		},
		{
			Name:        "annotationGet",
			Description: "Retrieve a specific annotation for a symbol URI by key.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol_uri": map[string]interface{}{"type": "string"},
					"key":        map[string]interface{}{"type": "string"},
				},
				"required": []string{"symbol_uri", "key"},
			},
		},
		{
			Name:        "annotationList",
			Description: "List all annotations for a symbol URI.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"symbol_uri": map[string]interface{}{"type": "string"},
				},
				"required": []string{"symbol_uri"},
			},
		},
	}

	// Fill in the real per-preset tool counts now that the full tool list
	// exists (see expandToolsetDescFmt doc comment: this is what keeps the
	// counts from drifting the way the old hardcoded numbers did).
	for i := range tools {
		if tools[i].Name != "expandToolset" {
			continue
		}
		tools[i].Description = fmt.Sprintf(expandToolsetDescFmt,
			len(GetPresetTools(PresetReview)),
			len(GetPresetTools(PresetRefactor)),
			len(GetPresetTools(PresetFederation)),
			len(GetPresetTools(PresetDocs)),
			len(GetPresetTools(PresetOps)),
			len(tools),
		)
		break
	}

	return tools
}

// RegisterTools registers all tool handlers
func (s *MCPServer) RegisterTools() {
	s.tools["getStatus"] = s.toolGetStatus
	s.tools["getWideResultMetrics"] = s.toolGetWideResultMetrics
	s.tools["doctor"] = s.toolDoctor
	s.tools["reindex"] = s.toolReindex
	s.tools["expandToolset"] = s.toolExpandToolset
	s.tools["getSymbol"] = s.toolGetSymbol
	s.tools["searchSymbols"] = s.toolSearchSymbols
	s.tools["symbolExists"] = s.toolSymbolExists
	s.tools["listSymbols"] = s.toolListSymbols
	s.tools["getSymbolGraph"] = s.toolGetSymbolGraph
	s.tools["findReferences"] = s.toolFindReferences
	s.tools["getArchitecture"] = s.toolGetArchitecture
	s.tools["analyzeImpact"] = s.toolAnalyzeImpact
	s.tools["analyzeOutgoingImpact"] = s.toolAnalyzeOutgoingImpact
	s.tools["assessChange"] = s.toolAssessChange
	s.tools["analyzeChange"] = s.toolAnalyzeChange // deprecated alias, same underlying handler
	s.tools["explainSymbol"] = s.toolExplainSymbol
	s.tools["justifySymbol"] = s.toolJustifySymbol
	s.tools["getCallGraph"] = s.toolGetCallGraph
	s.tools["getModuleOverview"] = s.toolGetModuleOverview
	s.tools["explainFile"] = s.toolExplainFile
	s.tools["listEntrypoints"] = s.toolListEntrypoints
	s.tools["traceUsage"] = s.toolTraceUsage
	s.tools["summarizeDiff"] = s.toolSummarizeDiff
	s.tools["getHotspots"] = s.toolGetHotspots
	s.tools["explainPath"] = s.toolExplainPath
	s.tools["listKeyConcepts"] = s.toolListKeyConcepts
	s.tools["recentlyRelevant"] = s.toolRecentlyRelevant
	// v6.0 Architectural Memory tools
	s.tools["refreshArchitecture"] = s.toolRefreshArchitecture
	s.tools["getOwnership"] = s.toolGetOwnership
	s.tools["getModuleResponsibilities"] = s.toolGetModuleResponsibilities
	s.tools["recordDecision"] = s.toolRecordDecision
	s.tools["getDecisions"] = s.toolGetDecisions
	s.tools["annotateModule"] = s.toolAnnotateModule
	// v6.1 Job management tools
	s.tools["getJobStatus"] = s.toolGetJobStatus
	s.tools["listJobs"] = s.toolListJobs
	s.tools["cancelJob"] = s.toolCancelJob
	// v6.1 CI/CD tools
	s.tools["summarizePr"] = s.toolSummarizePr
	s.tools["getOwnershipDrift"] = s.toolGetOwnershipDrift
	// v6.2 Federation tools
	s.tools["listFederations"] = s.toolListFederations
	s.tools["federationStatus"] = s.toolFederationStatus
	s.tools["federationRepos"] = s.toolFederationRepos
	s.tools["federationSearchModules"] = s.toolFederationSearchModules
	s.tools["federationSearchOwnership"] = s.toolFederationSearchOwnership
	s.tools["federationGetHotspots"] = s.toolFederationGetHotspots
	s.tools["federationSearchDecisions"] = s.toolFederationSearchDecisions
	s.tools["federationSync"] = s.toolFederationSync
	// v7.3 Remote Federation tools
	s.tools["federationAddRemote"] = s.toolFederationAddRemote
	s.tools["federationRemoveRemote"] = s.toolFederationRemoveRemote
	s.tools["federationListRemote"] = s.toolFederationListRemote
	s.tools["federationSyncRemote"] = s.toolFederationSyncRemote
	s.tools["federationStatusRemote"] = s.toolFederationStatusRemote
	s.tools["federationSearchSymbolsHybrid"] = s.toolFederationSearchSymbolsHybrid
	s.tools["federationListAllRepos"] = s.toolFederationListAllRepos
	// v6.2.1 Daemon tools
	s.tools["daemonStatus"] = s.toolDaemonStatus
	s.tools["listSchedules"] = s.toolListSchedules
	s.tools["runSchedule"] = s.toolRunSchedule
	s.tools["listWebhooks"] = s.toolListWebhooks
	s.tools["testWebhook"] = s.toolTestWebhook
	s.tools["webhookDeliveries"] = s.toolWebhookDeliveries
	// v6.3 Contract-Aware Impact Analysis tools
	s.tools["listContracts"] = s.toolListContracts
	s.tools["analyzeContractImpact"] = s.toolAnalyzeContractImpact
	s.tools["getContractDependencies"] = s.toolGetContractDependencies
	s.tools["suppressContractEdge"] = s.toolSuppressContractEdge
	s.tools["verifyContractEdge"] = s.toolVerifyContractEdge
	s.tools["getContractStats"] = s.toolGetContractStats
	// v6.2.2 Tree-sitter Complexity tools
	s.tools["getFileComplexity"] = s.toolGetFileComplexity
	// v6.4 Telemetry tools
	s.tools["getTelemetryStatus"] = s.toolGetTelemetryStatus
	s.tools["getObservedUsage"] = s.toolGetObservedUsage
	s.tools["findDeadCodeCandidates"] = s.toolFindDeadCodeCandidates
	// v7.6 Static Dead Code Detection
	s.tools["findDeadCode"] = s.toolFindDeadCode
	s.tools["findUnwiredModules"] = s.toolFindUnwiredModules
	// v7.6 Affected Tests
	s.tools["getAffectedTests"] = s.toolGetAffectedTests
	// v7.6 Breaking Change Detection
	s.tools["compareAPI"] = s.toolCompareAPI
	// v6.5 Developer Intelligence tools
	s.tools["explainOrigin"] = s.toolExplainOrigin
	s.tools["analyzeCoupling"] = s.toolAnalyzeCoupling
	s.tools["exportForLLM"] = s.toolExportForLLM
	s.tools["auditRisk"] = s.toolAuditRisk
	// v8.0 Secret Detection
	s.tools["scanSecrets"] = s.toolScanSecrets
	// v8.3 Compliance Audit
	s.tools["auditCompliance"] = s.toolAuditCompliance
	// v8.2 Unified Review
	s.tools["reviewPR"] = s.toolReviewPR
	// v7.3 Doc-Symbol Linking tools
	s.tools["getDocsForSymbol"] = s.toolGetDocsForSymbol
	s.tools["getSymbolsInDoc"] = s.toolGetSymbolsInDoc
	s.tools["getDocsForModule"] = s.toolGetDocsForModule
	s.tools["checkDocStaleness"] = s.toolCheckDocStaleness
	s.tools["indexDocs"] = s.toolIndexDocs
	s.tools["getDocCoverage"] = s.toolGetDocCoverage
	// v7.3 Multi-Repo Management tools
	s.tools["listRepos"] = s.toolListRepos
	s.tools["switchRepo"] = s.toolSwitchRepo
	s.tools["getActiveRepo"] = s.toolGetActiveRepo
	// v8.1 Dynamic project switching
	s.tools["switchProject"] = s.toolSwitchProject
	// v8.0 Compound Tools
	s.tools["explore"] = s.toolExplore
	s.tools["understand"] = s.toolUnderstand
	s.tools["prepareChange"] = s.toolPrepareChange
	s.tools["batchGet"] = s.toolBatchGet
	s.tools["batchSearch"] = s.toolBatchSearch
	// v8.1 Test Gap Analysis
	s.tools["analyzeTestGaps"] = s.toolAnalyzeTestGaps
	// v8.1 Unified Refactor Planning
	s.tools["planRefactor"] = s.toolPlanRefactor
	// v8.1 Cycle Detection
	s.tools["findCycles"] = s.toolFindCycles
	// v8.1 Suggested Refactorings
	s.tools["suggestRefactorings"] = s.toolSuggestRefactorings
	// v8.4 Performance scan
	s.tools["scanPerformance"] = s.toolScanPerformance
	// v8.5 Structural performance scan (loop call sites)
	s.tools["analyzeStructuralPerf"] = s.toolAnalyzeStructuralPerf
	// v8.6 cartographer context tools
	s.tools["queryContext"] = s.toolQueryContext
	s.tools["contextHealth"] = s.toolContextHealth
	s.tools["detectShotgunSurgery"] = s.toolDetectShotgunSurgery
	s.tools["getArchitecturalEvolution"] = s.toolGetArchitecturalEvolution
	s.tools["getBlastRadius"] = s.toolGetBlastRadius
	s.tools["renderArchitecture"] = s.toolRenderArchitecture
	// v9.0 LIP symbol annotations
	s.tools["annotationSet"] = s.toolAnnotationSet
	s.tools["annotationGet"] = s.toolAnnotationGet
	s.tools["annotationList"] = s.toolAnnotationList

	// v8.0 Streaming support
	s.RegisterStreamableTools()
}
