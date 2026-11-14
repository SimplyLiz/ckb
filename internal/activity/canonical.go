package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// maxParamStringBytes is the threshold above which a string value in params
// is replaced by a "<N bytes>" placeholder before serialization.
const maxParamStringBytes = 512

// maxStoredParamsBytes is the maximum length, in bytes, of the stored
// canonical params JSON. Longer output is truncated.
const maxStoredParamsBytes = 4096

// targetCandidateKeys lists primary-argument param names in priority order.
// The first key present in a tool's params wins; this is deliberately a flat
// list rather than a per-tool table because MCP tool params already use these
// conventional names (e.g. prepareChange's "target", analyzeImpact's
// "symbolId", searchSymbols's "query").
var targetCandidateKeys = []string{
	"symbolId", "symbol", "file", "filePath", "path", "query", "target", "moduleId", "id",
}

// sanitizeForHashing returns a copy of v with any string longer than
// maxParamStringBytes replaced by a "<N bytes>" placeholder. Safe for nil.
func sanitizeForHashing(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		if len(val) > maxParamStringBytes {
			return fmt.Sprintf("<%d bytes>", len(val))
		}
		return val
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, vv := range val {
			out[k] = sanitizeForHashing(vv)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, vv := range val {
			out[i] = sanitizeForHashing(vv)
		}
		return out
	default:
		return v
	}
}

// canonicalParamsJSON returns the sanitized, canonical JSON encoding of params.
// encoding/json sorts map keys, so this is deterministic for the
// map[string]interface{}/[]interface{}/scalar shapes MCP params always have.
func canonicalParamsJSON(params map[string]interface{}) ([]byte, error) {
	if params == nil {
		params = map[string]interface{}{}
	}
	sanitized := sanitizeForHashing(params)
	return json.Marshal(sanitized)
}

// hashParams returns the sha256 hex digest of the canonical (sanitized) JSON
// encoding of params, for grouping repeated calls regardless of storage mode.
func hashParams(params map[string]interface{}) string {
	canonical, err := canonicalParamsJSON(params)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// truncateBytes truncates s to at most n bytes on a valid UTF-8 boundary.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	b := s[:n]
	// Back off until we're not in the middle of a multi-byte rune.
	for len(b) > 0 && !utf8.ValidString(b) {
		b = b[:len(b)-1]
	}
	return b
}

// extractTarget returns the best-effort primary argument for a tool call,
// or "" if none of the candidate keys are present with a string value.
func extractTarget(params map[string]interface{}) string {
	for _, key := range targetCandidateKeys {
		if v, ok := params[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
