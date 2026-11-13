package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// AllowlistEntry defines a suppression rule.
type AllowlistEntry struct {
	ID          string       `json:"id"`
	Type        string       `json:"type"`                  // "path", "path-regex", "pattern", "hash", "rule"
	Value       string       `json:"value"`                 // Path glob, regex, finding hash, or rule name
	Reason      string       `json:"reason"`                // Why it's suppressed
	SecretTypes []SecretType `json:"secretTypes,omitempty"` // Limit a path/pattern entry to these secret types
	Rules       []string     `json:"rules,omitempty"`       // Limit a path/pattern entry to these rule names
	CreatedAt   string       `json:"createdAt,omitempty"`
	CreatedBy   string       `json:"createdBy,omitempty"`
}

// AllowlistFile is the structure of the allowlist JSON file.
type AllowlistFile struct {
	Version string           `json:"version"`
	Entries []AllowlistEntry `json:"entries"`
}

// Allowlist manages secret suppression rules.
type Allowlist struct {
	entries []AllowlistEntry

	// Compiled patterns for efficient matching
	pathPatterns  []*pathMatcher
	pathRegexes   []*pathRegexMatcher // For gitleaks-style regex paths
	valuePatterns []*regexp.Regexp
	valueScopes   []entryScope      // parallel to valuePatterns; missing = every finding
	hashes        map[string]string // hash -> entry ID
	rules         map[string]string // rule name -> entry ID
}

type pathMatcher struct {
	pattern string
	entryID string
	scope   entryScope
}

type pathRegexMatcher struct {
	regex   *regexp.Regexp
	entryID string
	scope   entryScope
}

// entryScope limits an entry to findings of some rules or secret types, so a
// path can be allowed for one noisy rule without hiding a real key there.
// The zero value applies to every finding.
type entryScope struct {
	rules map[string]bool
	types map[SecretType]bool
}

func newEntryScope(e AllowlistEntry) entryScope {
	var sc entryScope
	if len(e.Rules) > 0 {
		sc.rules = make(map[string]bool, len(e.Rules))
		for _, r := range e.Rules {
			sc.rules[r] = true
		}
	}
	if len(e.SecretTypes) > 0 {
		sc.types = make(map[SecretType]bool, len(e.SecretTypes))
		for _, t := range e.SecretTypes {
			sc.types[t] = true
		}
	}
	return sc
}

// covers reports whether the entry applies to f: with both rules and types
// set, either one naming the finding is enough.
func (sc entryScope) covers(f *SecretFinding) bool {
	if sc.rules == nil && sc.types == nil {
		return true
	}
	return sc.rules[f.Rule] || sc.types[f.Type]
}

// LoadAllowlist loads the allowlist from multiple sources:
// 1. .ckb/secrets-allowlist.json (CKB native format)
// 2. .gitleaks.toml (gitleaks compatibility)
func LoadAllowlist(repoRoot string) (*Allowlist, error) {
	var allEntries []AllowlistEntry

	// Load CKB native allowlist
	path := filepath.Join(repoRoot, ".ckb", "secrets-allowlist.json")
	data, err := os.ReadFile(path) //nolint:gosec // G304: Path is constructed from repoRoot + fixed filename
	if err == nil {
		var file AllowlistFile
		if unmarshalErr := json.Unmarshal(data, &file); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		allEntries = append(allEntries, file.Entries...)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Load gitleaks config for compatibility
	// Errors are intentionally ignored - gitleaks config is optional
	gitleaksEntries, _ := loadGitleaksConfig(repoRoot)
	if gitleaksEntries != nil {
		allEntries = append(allEntries, gitleaksEntries...)
	}

	al := &Allowlist{
		entries: allEntries,
		hashes:  make(map[string]string),
		rules:   make(map[string]string),
	}

	al.compile()
	return al, nil
}

// compile pre-compiles patterns for efficient matching.
func (al *Allowlist) compile() {
	for _, e := range al.entries {
		switch e.Type {
		case "path":
			al.pathPatterns = append(al.pathPatterns, &pathMatcher{
				pattern: e.Value,
				entryID: e.ID,
				scope:   newEntryScope(e),
			})
		case "path-regex":
			// Gitleaks-style regex path patterns
			if re, err := regexp.Compile(e.Value); err == nil {
				al.pathRegexes = append(al.pathRegexes, &pathRegexMatcher{
					regex:   re,
					entryID: e.ID,
					scope:   newEntryScope(e),
				})
			}
		case "pattern":
			if re, err := regexp.Compile(e.Value); err == nil {
				al.valuePatterns = append(al.valuePatterns, re)
				al.valueScopes = append(al.valueScopes, newEntryScope(e))
			}
		case "hash":
			al.hashes[e.Value] = e.ID
		case "rule":
			al.rules[e.Value] = e.ID
		}
	}
}

// IsSuppressed checks if a finding should be suppressed.
func (al *Allowlist) IsSuppressed(f *SecretFinding) (bool, string) {
	if al == nil {
		return false, ""
	}

	// Check path patterns (glob style)
	for _, pm := range al.pathPatterns {
		if !pm.scope.covers(f) {
			continue
		}
		if matched, _ := filepath.Match(pm.pattern, f.File); matched {
			return true, pm.entryID
		}
		// Also try with ** for recursive matching
		if matchGlob(pm.pattern, f.File) {
			return true, pm.entryID
		}
	}

	// Check path regex patterns (gitleaks style)
	for _, prm := range al.pathRegexes {
		if !prm.scope.covers(f) {
			continue
		}
		if prm.regex.MatchString(f.File) {
			return true, prm.entryID
		}
	}

	// Check rule suppression
	if id, ok := al.rules[f.Rule]; ok {
		return true, id
	}

	// Check value patterns
	for i, re := range al.valuePatterns {
		if i < len(al.valueScopes) && !al.valueScopes[i].covers(f) {
			continue
		}
		if re.MatchString(f.RawMatch) {
			// Find the corresponding entry ID
			idx := 0
			for _, e := range al.entries {
				if e.Type == "pattern" {
					if idx == i {
						return true, e.ID
					}
					idx++
				}
			}
			return true, ""
		}
	}

	// Check hash
	hash := hashFinding(f)
	if id, ok := al.hashes[hash]; ok {
		return true, id
	}

	return false, ""
}

// hashFinding creates a stable hash for a finding.
func hashFinding(f *SecretFinding) string {
	// Hash based on file, line, and raw match
	data := f.File + ":" + f.RawMatch
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:8]) // Use first 8 bytes for brevity
}

// matchGlob matches a pattern with ** support.
func matchGlob(pattern, path string) bool {
	// Simple ** handling
	if pattern == "**" {
		return true
	}

	// Convert ** to regex-like matching
	parts := splitPattern(pattern)
	// Split the path into segments (filepath.SplitList splits a PATH-style
	// list on ':' and left "scripts/**" unable to match "scripts/a/b.sh").
	pathParts := strings.Split(filepath.ToSlash(path), "/")

	return matchParts(parts, pathParts)
}

func splitPattern(pattern string) []string {
	// Split by path separator but keep **
	var parts []string
	current := ""

	for i := 0; i < len(pattern); i++ {
		if pattern[i] == '/' || pattern[i] == filepath.Separator {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(pattern[i])
		}
	}
	if current != "" {
		parts = append(parts, current)
	}

	return parts
}

func matchParts(pattern, path []string) bool {
	pi, pathi := 0, 0

	for pi < len(pattern) && pathi < len(path) {
		if pattern[pi] == "**" {
			// ** matches zero or more path segments
			if pi == len(pattern)-1 {
				return true
			}
			// Try matching rest of pattern against remaining path
			for i := pathi; i <= len(path); i++ {
				if matchParts(pattern[pi+1:], path[i:]) {
					return true
				}
			}
			return false
		}

		// Regular glob match
		matched, _ := filepath.Match(pattern[pi], path[pathi])
		if !matched {
			return false
		}

		pi++
		pathi++
	}

	// Check if we consumed both
	return pi == len(pattern) && pathi == len(path)
}

// GenerateHash generates a suppression hash for a finding.
// This can be used to create allowlist entries.
func GenerateHash(f *SecretFinding) string {
	return hashFinding(f)
}

// gitleaksConfig represents the structure of .gitleaks.toml
type gitleaksConfig struct {
	Allowlist gitleaksAllowlist `toml:"allowlist"`
}

type gitleaksAllowlist struct {
	Description string   `toml:"description"`
	Paths       []string `toml:"paths"`
	Regexes     []string `toml:"regexes"`
	Commits     []string `toml:"commits"`
	StopWords   []string `toml:"stopwords"`
}

// loadGitleaksConfig loads allowlist entries from .gitleaks.toml if it exists.
// This provides compatibility with gitleaks configuration.
func loadGitleaksConfig(repoRoot string) ([]AllowlistEntry, error) {
	// Try multiple possible locations
	paths := []string{
		filepath.Join(repoRoot, ".gitleaks.toml"),
		filepath.Join(repoRoot, "gitleaks.toml"),
	}

	var config gitleaksConfig
	var found bool

	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: Path constructed from repoRoot + fixed filename
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}

		if _, err := toml.Decode(string(data), &config); err != nil {
			return nil, err
		}
		found = true
		break
	}

	if !found {
		return nil, nil
	}

	var entries []AllowlistEntry

	// Convert path patterns (gitleaks uses regex for paths)
	for i, path := range config.Allowlist.Paths {
		entries = append(entries, AllowlistEntry{
			ID:     "gitleaks-path-" + string(rune('0'+i)),
			Type:   "path-regex",
			Value:  path,
			Reason: "From .gitleaks.toml allowlist",
		})
	}

	// Convert regex patterns
	for i, regex := range config.Allowlist.Regexes {
		entries = append(entries, AllowlistEntry{
			ID:     "gitleaks-regex-" + string(rune('0'+i)),
			Type:   "pattern",
			Value:  regex,
			Reason: "From .gitleaks.toml allowlist",
		})
	}

	return entries, nil
}
