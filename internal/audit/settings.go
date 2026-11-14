package audit

import (
	"context"
	"strings"
	"unicode"

	"github.com/SimplyLiz/CodeMCP/internal/config"
)

// settings is the per-repo tuning of the audit (.ckb/config.json "audit").
type settings struct {
	keywords []securityKeyword
	disabled map[string]bool
}

func newSettings(cfg *config.Config) settings {
	st := settings{disabled: make(map[string]bool)}
	words := SecurityKeywords
	if cfg != nil {
		if len(cfg.Audit.SecurityKeywords) > 0 {
			words = cfg.Audit.SecurityKeywords
		}
		for _, f := range cfg.Audit.DisabledFactors {
			st.disabled[strings.TrimSpace(f)] = true
		}
	}
	st.keywords = compileKeywords(words)
	return st
}

// securityKeyword is a keyword split into normalized identifier parts.
type securityKeyword struct {
	name  string
	parts []string
}

func compileKeywords(words []string) []securityKeyword {
	out := make([]securityKeyword, 0, len(words))
	for _, w := range words {
		parts := identifierParts(w)
		if len(parts) == 0 {
			continue
		}
		out = append(out, securityKeyword{name: strings.TrimSpace(w), parts: parts})
	}
	return out
}

// matchSecurityKeywords returns the keywords that occur in text, in keyword
// order. Text is read as identifiers (letters, digits, '_' and '-'), each split
// into lowercase parts at '_', '-' and camelCase boundaries, a trailing plural
// 's' dropped. A keyword matches a run of consecutive parts of one
// identifier: "access_token" matches getAccessToken but not "access the
// token", "auth" matches authHeader but not author.
func matchSecurityKeywords(text string, keywords []securityKeyword) []string {
	if len(keywords) == 0 {
		return nil
	}
	found := make([]bool, len(keywords))
	remaining := len(keywords)
	forEachIdentifier(text, func(ident string) bool {
		parts := identifierParts(ident)
		for i, kw := range keywords {
			if !found[i] && containsRun(parts, kw.parts) {
				found[i] = true
				remaining--
			}
		}
		return remaining > 0
	})
	var out []string
	for i, kw := range keywords {
		if found[i] {
			out = append(out, kw.name)
		}
	}
	return out
}

func containsRun(parts, run []string) bool {
	for i := 0; i+len(run) <= len(parts); i++ {
		ok := true
		for j := range run {
			if parts[i+j] != run[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func isIdentRune(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// forEachIdentifier calls fn for each identifier-like run in text until fn
// returns false.
func forEachIdentifier(text string, fn func(string) bool) {
	start := -1
	for i, r := range text {
		if isIdentRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			if !fn(text[start:i]) {
				return
			}
			start = -1
		}
	}
	if start >= 0 {
		fn(text[start:])
	}
}

// identifierParts splits an identifier into normalized parts:
// "getAccessToken" and "ACCESS_TOKENS" give [get access token] / [access token].
func identifierParts(ident string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, normalizePart(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(ident)
	for i, r := range rs {
		if r == '_' || r == '-' || unicode.IsSpace(r) {
			flush()
			continue
		}
		if unicode.IsUpper(r) && len(cur) > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			// fooBar | HTTPServer -> HTTP Server
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

func normalizePart(p string) string {
	p = strings.ToLower(p)
	if len(p) > 3 && strings.HasSuffix(p, "s") && !strings.HasSuffix(p, "ss") {
		p = p[:len(p)-1]
	}
	return p
}

// singleAuthorRepo reports whether git history up to HEAD has at most one
// human author (bots like dependabot[bot] don't count). In such a repo every
// file has bus factor 1, so the factor says nothing about one file over another.
func (a *Analyzer) singleAuthorRepo(ctx context.Context) bool {
	a.authorsOnce.Do(func() {
		out, err := a.executeGit(ctx, "shortlog", "-sn", "HEAD")
		if err != nil {
			return
		}
		n := 0
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(line, "[bot]") {
				continue
			}
			n++
		}
		a.singleAuthor = n == 1
	})
	return a.singleAuthor
}
