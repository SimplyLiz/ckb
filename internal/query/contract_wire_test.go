package query

import "testing"

func TestGoWireKeyChange(t *testing.T) {
	cases := []struct {
		name, before, after string
		removed             bool
		want                string
	}{
		{"renamed field, key gone", "OriginalCount int `json:\"originalCount\"`", "", true,
			`json key "originalCount" gone (serialized: readers outside the code don't compile)`},
		{"retagged", "Count int `json:\"count,omitempty\" yaml:\"count\"`", "Count int `json:\"total,omitempty\" yaml:\"count\"`", false,
			`json key "count" -> "total" (serialized: readers outside the code don't compile)`},
		{"type change, same key", "Count int `json:\"count\"`", "Count int64 `json:\"count\"`", false, ""},
		{"untagged field says nothing", "Count int", "", true, ""},
		{"skipped field", "Secret string `json:\"-\"`", "", true, ""},
		{"tag dropped", "Count int `json:\"count\"`", "Count int", false,
			`json key "count" now the format's default (serialized: readers outside the code don't compile)`},
	}
	for _, c := range cases {
		if got := goWireKeyChange(c.before, c.after, c.removed); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
