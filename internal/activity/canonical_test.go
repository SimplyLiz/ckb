package activity

import (
	"strings"
	"testing"
)

func TestHashParamsDeterministic(t *testing.T) {
	a := map[string]interface{}{"b": 1, "a": "x"}
	b := map[string]interface{}{"a": "x", "b": 1}

	if hashParams(a) != hashParams(b) {
		t.Errorf("expected key-order-independent hash, got different hashes")
	}
	if hashParams(a) == "" {
		t.Errorf("expected non-empty hash")
	}
}

func TestHashParamsDiffersOnValue(t *testing.T) {
	a := map[string]interface{}{"target": "foo"}
	b := map[string]interface{}{"target": "bar"}

	if hashParams(a) == hashParams(b) {
		t.Errorf("expected different hashes for different values")
	}
}

func TestSanitizeForHashingTruncatesLongStrings(t *testing.T) {
	big := strings.Repeat("x", maxParamStringBytes+1)
	params := map[string]interface{}{"body": big, "short": "ok"}

	sanitized, ok := sanitizeForHashing(params).(map[string]interface{})
	if !ok {
		t.Fatal("expected a map back")
	}
	if sanitized["short"] != "ok" {
		t.Errorf("expected short string untouched, got %v", sanitized["short"])
	}
	placeholder, ok := sanitized["body"].(string)
	if !ok || placeholder == big {
		t.Fatalf("expected long string replaced, got %v", sanitized["body"])
	}
	if !strings.Contains(placeholder, "bytes") {
		t.Errorf("expected placeholder to mention bytes, got %q", placeholder)
	}
}

func TestSanitizeForHashingRecursesIntoNestedStructures(t *testing.T) {
	big := strings.Repeat("y", maxParamStringBytes+10)
	params := map[string]interface{}{
		"list": []interface{}{
			map[string]interface{}{"nested": big},
		},
	}

	sanitized, ok := sanitizeForHashing(params).(map[string]interface{})
	if !ok {
		t.Fatal("expected a map back")
	}
	list, ok := sanitized["list"].([]interface{})
	if !ok || len(list) == 0 {
		t.Fatalf("expected a non-empty list, got %v", sanitized["list"])
	}
	nestedMap, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a nested map, got %v", list[0])
	}
	nested, ok := nestedMap["nested"].(string)
	if !ok || nested == big {
		t.Fatalf("expected nested long string to be replaced, got %v", nestedMap["nested"])
	}
}

func TestCanonicalParamsJSONTruncation(t *testing.T) {
	params := map[string]interface{}{"target": strings.Repeat("a", 10)}
	canonical, err := canonicalParamsJSON(params)
	if err != nil {
		t.Fatalf("canonicalParamsJSON failed: %v", err)
	}
	truncated := truncateBytes(string(canonical), 5)
	if len(truncated) > 5 {
		t.Errorf("expected truncated length <= 5, got %d", len(truncated))
	}
}

func TestTruncateBytesUTF8Safe(t *testing.T) {
	s := "héllo" // 'é' is 2 bytes in UTF-8
	// Truncating to 2 bytes would land mid-rune if not careful (h=1 byte, é=2 bytes -> boundary at 3).
	out := truncateBytes(s, 2)
	if len(out) > 2 {
		t.Fatalf("expected at most 2 bytes, got %d (%q)", len(out), out)
	}
	// Result must be valid UTF-8 (no partial rune).
	for _, r := range out {
		if r == '�' {
			t.Fatalf("truncation produced invalid UTF-8: %q", out)
		}
	}
}

func TestExtractTargetPriority(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{"symbolId wins", map[string]interface{}{"symbolId": "sym-1", "query": "q"}, "sym-1"},
		{"query used for searchSymbols-shaped params", map[string]interface{}{"query": "Engine"}, "Engine"},
		{"target used for prepareChange-shaped params", map[string]interface{}{"target": "auth/session.go"}, "auth/session.go"},
		{"no candidate keys", map[string]interface{}{"limit": 10}, ""},
		{"non-string candidate skipped", map[string]interface{}{"symbolId": 123, "query": "q"}, "q"},
		{"nil params", nil, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTarget(tc.params)
			if got != tc.want {
				t.Errorf("extractTarget(%v) = %q, want %q", tc.params, got, tc.want)
			}
		})
	}
}
