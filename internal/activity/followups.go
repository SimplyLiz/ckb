package activity

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/storage"
)

// FollowupWindow is how long after a call the next one in its session still
// counts as its follow-up.
const FollowupWindow = 10 * time.Minute

// NoFollowup labels a call with no other CKB call in its session within the window.
const NoFollowup = "(none)"

// Followup is how often a call of the tool was followed by Next.
type Followup struct {
	Next  string `json:"next"`
	Count int    `json:"count"`
}

// FollowupReport answers which CKB call comes after a tool: what callers needed
// that the tool's answer didn't give them. Only CKB calls are in the ledger;
// file reads and searches in between are invisible here (ckb bench session
// joins the client transcript for that).
type FollowupReport struct {
	Tool      string     `json:"tool"`
	Calls     int        `json:"calls"`
	Sessions  int        `json:"sessions"`
	Followups []Followup `json:"followups"`
	// NoSession counts calls recorded without a session id, left out because
	// their neighbours can't be told apart.
	NoSession int `json:"noSession,omitempty"`
}

// Followups builds a FollowupReport for tool from calls in any order.
func Followups(calls []storage.ToolCall, tool string, window time.Duration) *FollowupReport {
	report := &FollowupReport{Tool: tool}
	bySession := make(map[string][]storage.ToolCall)
	for _, c := range calls {
		if c.SessionID == "" {
			if c.Tool == tool {
				report.NoSession++
			}
			continue
		}
		bySession[c.SessionID] = append(bySession[c.SessionID], c)
	}

	counts := make(map[string]int)
	for _, session := range bySession {
		sort.SliceStable(session, func(i, j int) bool {
			if session[i].Ts != session[j].Ts {
				return session[i].Ts < session[j].Ts
			}
			return session[i].ID < session[j].ID
		})
		used := false
		for i, c := range session {
			if c.Tool != tool {
				continue
			}
			used = true
			report.Calls++
			next := NoFollowup
			if i+1 < len(session) && time.Duration(session[i+1].Ts-c.Ts)*time.Millisecond <= window {
				next = CallLabel(session[i+1])
			}
			counts[next]++
		}
		if used {
			report.Sessions++
		}
	}

	for next, n := range counts {
		report.Followups = append(report.Followups, Followup{Next: next, Count: n})
	}
	sort.Slice(report.Followups, func(i, j int) bool {
		a, b := report.Followups[i], report.Followups[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Next < b.Next
	})
	return report
}

// labelParams are the arguments that say what a call was after, as opposed to
// which symbol or file it was about.
var labelParams = []string{"focus", "format", "changeType", "offset"}

// CallLabel names a call by its tool and the arguments that shape its answer,
// e.g. assessChange{focus=done,offset}. Offset is only marked present: the
// number says nothing about what was wanted.
func CallLabel(c storage.ToolCall) string {
	var params map[string]interface{}
	if c.Params == "" || json.Unmarshal([]byte(c.Params), &params) != nil {
		return c.Tool
	}
	var parts []string
	for _, key := range labelParams {
		v, ok := params[key]
		if !ok || v == nil || v == "" {
			continue
		}
		if key == "offset" {
			if f, isNum := v.(float64); isNum && f == 0 {
				continue
			}
			parts = append(parts, "offset")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, v))
	}
	if len(parts) == 0 {
		return c.Tool
	}
	return c.Tool + "{" + strings.Join(parts, ",") + "}"
}
