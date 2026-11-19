package activity

import (
	"testing"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

func TestFollowups(t *testing.T) {
	min := int64(time.Minute / time.Millisecond)
	calls := []storage.ToolCall{
		// Session a: assessChange, then paging it, then a findReferences much later.
		{ID: 3, SessionID: "a", Tool: "assessChange", Ts: 2 * min, Params: `{"focus":"done","offset":50}`},
		{ID: 1, SessionID: "a", Tool: "assessChange", Ts: 0, Params: `{"focus":"done"}`},
		{ID: 4, SessionID: "a", Tool: "findReferences", Ts: 30 * min, Params: `{"symbolId":"x"}`},
		// Session b: assessChange followed by getAffectedTests.
		{ID: 5, SessionID: "b", Tool: "assessChange", Ts: 0},
		{ID: 6, SessionID: "b", Tool: "getAffectedTests", Ts: min},
		// Another session's call between doesn't count as a follow-up.
		{ID: 2, SessionID: "c", Tool: "searchSymbols", Ts: min},
		{ID: 7, Tool: "assessChange", Ts: 0},
	}
	r := Followups(calls, "assessChange", FollowupWindow)
	if r.Calls != 3 || r.Sessions != 2 || r.NoSession != 1 {
		t.Fatalf("calls %d sessions %d noSession %d", r.Calls, r.Sessions, r.NoSession)
	}
	want := map[string]int{
		"assessChange{focus=done,offset}": 1,
		NoFollowup:                        1, // findReferences came after the window
		"getAffectedTests":                1,
	}
	if len(r.Followups) != len(want) {
		t.Fatalf("followups %+v", r.Followups)
	}
	for _, f := range r.Followups {
		if want[f.Next] != f.Count {
			t.Errorf("%s: %d, want %d", f.Next, f.Count, want[f.Next])
		}
	}
}

func TestCallLabel(t *testing.T) {
	for params, want := range map[string]string{
		"":                                       "assessChange",
		`{"includeTests":true}`:                  "assessChange",
		`{"format":"full","offset":0}`:           "assessChange{format=full}",
		`{"focus":"tests","limit":5,"offset":3}`: "assessChange{focus=tests,offset}",
		`not json`:                               "assessChange",
	} {
		if got := CallLabel(storage.ToolCall{Tool: "assessChange", Params: params}); got != want {
			t.Errorf("%q: got %q, want %q", params, got, want)
		}
	}
}
