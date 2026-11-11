package mcp

import (
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
)

// symbolIDParams are the tool arguments that carry a symbol ID. "target" and
// "symbol" also accept names and paths; those never resolve as SCIP IDs, so
// they pass through untouched.
var symbolIDParams = []string{"symbolId", "target", "symbol"}

// WarnSymbolIDVersionRemapped marks a response whose symbol ID came from an
// older index and was matched to the current one ignoring the package version.
const WarnSymbolIDVersionRemapped = "SYMBOL_ID_VERSION_REMAPPED"

// remapSymbolIDs rewrites, in place, symbol IDs in tool arguments that the
// loaded index doesn't know but has under another package version (scip-go
// uses the commit hash as version, so IDs go stale on every commit). It returns
// one warning per rewritten ID for the response. The emitted ID format is not
// changed; only lookups become tolerant.
func (s *MCPServer) remapSymbolIDs(params map[string]interface{}) []envelope.Warning {
	if !hasSCIPLikeID(params) {
		return nil // don't load the engine for tools that take no symbol ID
	}
	eng := s.engine()
	if eng == nil {
		return nil
	}
	var warnings []envelope.Warning
	remap := func(id string) string {
		got, ok := eng.ResolveSymbolIDAcrossVersions(id)
		if ok {
			warnings = append(warnings, envelope.Warning{
				Code:    WarnSymbolIDVersionRemapped,
				Message: "resolvedFrom " + id + " to " + got + ": same symbol, the index was rebuilt at another package version",
			})
		}
		return got
	}
	for _, key := range symbolIDParams {
		if v, ok := params[key].(string); ok && v != "" {
			params[key] = remap(v)
		}
	}
	if arr, ok := params["symbolIds"].([]interface{}); ok {
		for i, v := range arr {
			if id, ok := v.(string); ok && id != "" {
				arr[i] = remap(id)
			}
		}
	}
	return warnings
}

// withWarnings appends warnings to a tool response.
func withWarnings(resp *envelope.Response, warnings []envelope.Warning) *envelope.Response {
	if resp != nil && len(warnings) > 0 {
		resp.Warnings = append(resp.Warnings, warnings...)
	}
	return resp
}

// hasSCIPLikeID reports whether any symbol-ID argument has the space-separated
// shape of a SCIP ID.
func hasSCIPLikeID(params map[string]interface{}) bool {
	for _, key := range symbolIDParams {
		if v, ok := params[key].(string); ok && strings.Contains(v, " ") {
			return true
		}
	}
	if arr, ok := params["symbolIds"].([]interface{}); ok {
		for _, v := range arr {
			if id, ok := v.(string); ok && strings.Contains(id, " ") {
				return true
			}
		}
	}
	return false
}
