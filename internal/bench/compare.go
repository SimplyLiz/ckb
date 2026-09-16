package bench

import "time"

// Delta is a single metric compared between two sessions. Naming is
// deliberately neutral ("delta", not "saved"/"avoided"): plan section 3 is
// explicit that nothing here should claim tokens were saved - the number is
// produced, not narrated.
type Delta struct {
	Metric  string  `json:"metric"`
	A       float64 `json:"a"`
	B       float64 `json:"b"`
	Delta   float64 `json:"delta"`   // B - A
	Percent float64 `json:"percent"` // (B - A) / A * 100; 0 when A is 0
}

// Comparison is the side-by-side result of comparing two SessionRecords.
type Comparison struct {
	SessionA string  `json:"sessionA"`
	SessionB string  `json:"sessionB"`
	Metrics  []Delta `json:"metrics"`
}

// Compare produces a neutral side-by-side comparison of two SessionRecords.
// It covers: total tokens, context volume (input + cache read + cache
// creation), output tokens, turns, total tool calls, agent file reads, agent
// searches, CKB calls, and duration.
func Compare(a, b SessionRecord) Comparison {
	c := Comparison{SessionA: a.SessionID, SessionB: b.SessionID}

	totalToolCallsA := sumCounts(a.ToolCalls)
	totalToolCallsB := sumCounts(b.ToolCalls)

	contextA := float64(a.Tokens.Input + a.Tokens.CacheRead + a.Tokens.CacheCreation)
	contextB := float64(b.Tokens.Input + b.Tokens.CacheRead + b.Tokens.CacheCreation)

	c.Metrics = []Delta{
		delta("tokens.total", float64(a.Tokens.Total), float64(b.Tokens.Total)),
		delta("tokens.context", contextA, contextB),
		delta("tokens.output", float64(a.Tokens.Output), float64(b.Tokens.Output)),
		delta("turns", float64(a.Turns), float64(b.Turns)),
		delta("toolCalls.total", float64(totalToolCallsA), float64(totalToolCallsB)),
		delta("agentFileReads", float64(a.AgentFileReads), float64(b.AgentFileReads)),
		delta("agentSearches", float64(a.AgentSearches), float64(b.AgentSearches)),
		delta("ckbCalls", float64(a.CKBCalls), float64(b.CKBCalls)),
		delta("durationMs", float64(a.Duration/time.Millisecond), float64(b.Duration/time.Millisecond)),
	}

	return c
}

func sumCounts(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func delta(metric string, a, b float64) Delta {
	d := Delta{Metric: metric, A: a, B: b, Delta: b - a}
	if a != 0 {
		d.Percent = (b - a) / a * 100
	}
	return d
}
