package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// passScoreFloor is the score below which a review is not a pass, whatever the
// checks said. The score is 100 minus a deduction per finding (error 10, warning
// 3, info 1; at most 20 per check, 10 per rule and 80 in all, so 20 is the floor),
// and a review that lost half of it has too much to look at to be called clean.
const passScoreFloor = 50

// explainVerdict returns the verdict and why. The checks decide it, as
// determineVerdict does, but a pass also needs no warning or error finding and a
// score of at least passScoreFloor: a check can pass and still report warnings,
// and a "pass" at 20/100 told the reader nothing. Findings never make a review
// fail on their own under the default policy; that stays the checks' decision.
func explainVerdict(checks []ReviewCheck, findings []ReviewFinding, score int, policy *ReviewPolicy) (string, string) {
	failLevel := policy.FailOnLevel
	if failLevel == "" {
		failLevel = "error"
	}
	if failLevel == "none" {
		return "pass", "failOnLevel is none"
	}

	var failed, warned []string
	for _, c := range checks {
		switch c.Status {
		case "fail":
			failed = append(failed, c.Name)
		case "warn":
			warned = append(warned, c.Name)
		}
	}
	verdict := determineVerdict(checks, policy)
	switch {
	case len(failed) > 0:
		return verdict, "failed: " + strings.Join(failed, ", ")
	case len(warned) > 0:
		return verdict, "warned: " + strings.Join(warned, ", ")
	}

	serious := 0
	for _, f := range findings {
		if f.Severity == "error" || f.Severity == "warning" {
			serious++
		}
	}
	var why []string
	if serious > 0 {
		why = append(why, fmt.Sprintf("%d warning or error finding(s) in passing checks", serious))
	}
	if score < passScoreFloor {
		why = append(why, fmt.Sprintf("score %d is below %d", score, passScoreFloor))
	}
	if len(why) == 0 {
		return "pass", "no check warned"
	}
	verdict = "warn"
	if failLevel == "warning" {
		verdict = "fail"
	}
	return verdict, "no check warned, but " + strings.Join(why, " and ")
}

// ReviewLimitation is something the review could not see, said where it applies.
type ReviewLimitation struct {
	Kind     string   `json:"kind"`
	Message  string   `json:"message"`
	Examples []string `json:"examples,omitempty"` // "file:line: code"
}

// computedAccess matches a member reached by a computed key, profile[kind] or
// WORDS[kind]: a base ending in a name or a call, then a bracketed identifier.
var computedAccess = regexp.MustCompile(`[\w$)\]]\??\.?\[([A-Za-z_$][\w$.]*)\]`)

// notAComputedMember are keys that are almost always an index or a type position.
var notAComputedMember = map[string]bool{"i": true, "j": true, "k": true, "n": true, "idx": true, "index": true, "number": true, "string": true}

// dynamicAccessLanguages are where obj[key] reaches a member by name.
var dynamicAccessLanguages = map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true, ".mts": true, ".cts": true, ".py": true, ".rb": true}

// dynamicAccessLimitation looks through the added lines of a unified diff for
// members reached by a computed key. A SCIP index records no reference for them,
// so callers, affected tests and blast radius leave out whatever is used that way.
// That is where a change to the shape of an object goes unseen.
func dynamicAccessLimitation(rawDiff string, maxExamples int) *ReviewLimitation {
	var examples []string
	total := 0
	file := ""
	line := 0
	for _, l := range strings.Split(rawDiff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			continue
		case strings.HasPrefix(l, "@@"):
			// @@ -a,b +c,d @@
			if plus := strings.Index(l, " +"); plus >= 0 {
				rest := l[plus+2:]
				if end := strings.IndexAny(rest, ", "); end >= 0 {
					rest = rest[:end]
				}
				line, _ = strconv.Atoi(rest)
			}
			continue
		case strings.HasPrefix(l, "-"):
			continue
		case strings.HasPrefix(l, "+"):
			if dynamicAccessLanguages[strings.ToLower(filepath.Ext(file))] && !isTestFilePath(file) && hasComputedAccess(l[1:]) {
				total++
				if len(examples) < maxExamples {
					examples = append(examples, fmt.Sprintf("%s:%d: %s", file, line, strings.TrimSpace(l[1:])))
				}
			}
		}
		line++
	}
	if total == 0 {
		return nil
	}
	return &ReviewLimitation{
		Kind: "dynamic-access",
		Message: fmt.Sprintf("%d added line(s) reach a member through a computed key (obj[key]). "+
			"The index records no reference for that, so callers, tests and blast radius above leave out what is used this way: read these by hand.", total),
		Examples: examples,
	}
}

func hasComputedAccess(code string) bool {
	trimmed := strings.TrimSpace(code)
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "*") {
		return false
	}
	for _, m := range computedAccess.FindAllStringSubmatch(code, -1) {
		key := m[1]
		if notAComputedMember[key] || (len(key) == 1 && key[0] >= 'A' && key[0] <= 'Z') {
			continue // An index, or a type parameter in Profile[K]
		}
		return true
	}
	return false
}
