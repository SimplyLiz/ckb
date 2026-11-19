// Package activity implements the MCP tool-call ledger: a non-blocking
// recorder that writes one row per tool invocation to the tool_calls table,
// plus an honest, opt-in "facts" extractor that reads structured counts
// straight out of a tool's own response — never invented at this layer.
package activity

import (
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// FactsExtractor inspects a tool's response Data (post-handler, pre-marshal)
// and returns a small map of named counts, or nil if it can't say anything
// with confidence. Extractors must never fabricate numbers: any doubt, any
// unexpected shape, any panic — return nil.
type FactsExtractor func(data interface{}) map[string]int

// factsExtractors maps tool name to its extractor. Tools without an entry
// here get facts = NULL; ckb activity prints nothing for them.
var factsExtractors = map[string]FactsExtractor{
	"prepareChange":  extractPrepareChangeFacts,
	"analyzeChange":  extractChangeSetFacts,
	"assessChange":   extractChangeSetFacts, // assessChange is analyzeChange's successor name (same response shape)
	"analyzeImpact":  extractAnalyzeImpactFacts,
	"findReferences": extractFindReferencesFacts,
	"searchSymbols":  extractSearchSymbolsFacts,
}

// ExtractFacts runs the registered extractor for tool (if any) against data,
// recovering from any panic so a bug in an extractor never affects the
// caller's tool response. Returns nil when there is no extractor, the data
// shape is unrecognized, or the extractor found nothing to report.
func ExtractFacts(tool string, data interface{}) (facts map[string]int) {
	extractor, ok := factsExtractors[tool]
	if !ok || data == nil {
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			facts = nil
		}
	}()

	return extractor(data)
}

func extractPrepareChangeFacts(data interface{}) map[string]int {
	resp, ok := data.(*query.PrepareChangeResponse)
	if !ok || resp == nil {
		return nil
	}

	facts := map[string]int{
		"dependents": len(resp.DirectDependents),
		"tests":      len(resp.RelatedTests),
		"cochange":   len(resp.CoChangeFiles),
	}
	return facts
}

func extractChangeSetFacts(data interface{}) map[string]int {
	resp, ok := data.(*query.AnalyzeChangeSetResponse)
	if !ok || resp == nil {
		return nil
	}

	facts := map[string]int{
		"changed":  len(resp.ChangedSymbols),
		"affected": len(resp.AffectedSymbols),
		"modules":  len(resp.ModulesAffected),
	}
	return facts
}

func extractAnalyzeImpactFacts(data interface{}) map[string]int {
	m, ok := data.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}

	facts := map[string]int{}
	if n, ok := sliceLen(m["directImpact"]); ok {
		facts["direct"] = n
	}
	if n, ok := sliceLen(m["transitiveImpact"]); ok {
		facts["transitive"] = n
	}
	if blast, ok := m["blastRadius"].(map[string]interface{}); ok {
		if n, ok := intFromAny(blast["moduleCount"]); ok {
			facts["modules"] = n
		}
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func extractFindReferencesFacts(data interface{}) map[string]int {
	m, ok := data.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}

	if n, ok := intFromAny(m["totalCount"]); ok {
		return map[string]int{"references": n}
	}
	if n, ok := sliceLen(m["references"]); ok {
		return map[string]int{"references": n}
	}
	return nil
}

func extractSearchSymbolsFacts(data interface{}) map[string]int {
	m, ok := data.(map[string]interface{})
	if !ok || m == nil {
		return nil
	}

	if n, ok := intFromAny(m["totalCount"]); ok {
		return map[string]int{"results": n}
	}
	if n, ok := sliceLen(m["symbols"]); ok {
		return map[string]int{"results": n}
	}
	return nil
}

// intFromAny extracts an int from the handful of numeric types tool handlers
// actually use (int, int64, float64). Returns ok=false for anything else.
func intFromAny(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

// sliceLen returns the length of v if it's a slice-shaped value, else false.
func sliceLen(v interface{}) (int, bool) {
	switch s := v.(type) {
	case []interface{}:
		return len(s), true
	case []map[string]interface{}:
		return len(s), true
	default:
		return 0, false
	}
}
