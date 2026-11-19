package secrets

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Scanner scans files for exposed secrets.
type Scanner struct {
	repoRoot  string
	logger    *slog.Logger
	patterns  []Pattern
	allowlist *Allowlist
	external  *ExternalScanner
}

// NewScanner creates a new secret scanner.
func NewScanner(repoRoot string, logger *slog.Logger) *Scanner {
	return &Scanner{
		repoRoot: repoRoot,
		logger:   logger,
		patterns: BuiltinPatterns,
	}
}

// Scan performs a secret scan with the given options.
func (s *Scanner) Scan(ctx context.Context, opts ScanOptions) (*ScanResult, error) {
	start := time.Now()

	// Set defaults
	if opts.RepoRoot == "" {
		opts.RepoRoot = s.repoRoot
	}
	if opts.Scope == "" {
		opts.Scope = ScopeWorkdir
	}
	if opts.MinEntropy == 0 {
		opts.MinEntropy = 3.5
	}
	if len(opts.ExcludePaths) == 0 {
		opts.ExcludePaths = DefaultExcludePaths()
	}

	// Load allowlist if needed. Without it nothing is suppressed, not even
	// by an allowlist an earlier Scan on this scanner loaded.
	var allowlist *Allowlist
	if opts.ApplyAllowlist {
		al, err := LoadAllowlist(opts.RepoRoot)
		if err != nil {
			s.logger.Warn("Failed to load allowlist", "error", err)
		} else {
			allowlist = al
		}
	}
	s.allowlist = allowlist

	// Collect findings from different sources
	var allFindings []SecretFinding
	sources := make([]SourceInfo, 0)

	// Run builtin scanner. Staged and history scopes read git diffs instead of
	// the working tree; every scope goes through here so the result carries
	// the same metadata, allowlist and severity filter whichever was asked.
	filesScanned := 0
	if opts.Scope == ScopeHistory || opts.Scope == ScopeCommitRange || opts.Scope == ScopeStaged {
		git := NewGitScanner(opts.RepoRoot)
		var gitFindings []SecretFinding
		var err error
		if opts.Scope == ScopeStaged {
			gitFindings, filesScanned, err = git.scanStaged(ctx)
		} else {
			gitFindings, filesScanned, err = git.scanHistory(ctx, opts)
		}
		if err != nil {
			return nil, fmt.Errorf("git %s scan failed: %w", opts.Scope, err)
		}
		allFindings = append(allFindings, gitFindings...)
		sources = append(sources, SourceInfo{Name: "builtin", Findings: len(gitFindings)})
	} else if !opts.PreferExternal || (!opts.UseGitleaks && !opts.UseTrufflehog) {
		builtinFindings, n, err := s.scanBuiltin(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("builtin scan failed: %w", err)
		}
		filesScanned = n
		allFindings = append(allFindings, builtinFindings...)
		sources = append(sources, SourceInfo{
			Name:     "builtin",
			Findings: len(builtinFindings),
		})
		s.logger.Debug("Builtin scan complete",
			"findings", len(builtinFindings),
			"files", filesScanned)
	}

	// Run external tools if requested
	if s.external != nil {
		if opts.UseGitleaks {
			if available, version := s.external.IsAvailable(ctx, GitleaksConfig); available {
				findings, err := s.external.RunGitleaks(ctx, opts)
				if err != nil {
					s.logger.Warn("Gitleaks failed", "error", err)
				} else {
					allFindings = append(allFindings, findings...)
					sources = append(sources, SourceInfo{
						Name:     "gitleaks",
						Version:  version,
						Findings: len(findings),
					})
				}
			}
		}
		if opts.UseTrufflehog {
			if available, version := s.external.IsAvailable(ctx, TrufflehogConfig); available {
				findings, err := s.external.RunTrufflehog(ctx, opts)
				if err != nil {
					s.logger.Warn("Trufflehog failed", "error", err)
				} else {
					allFindings = append(allFindings, findings...)
					sources = append(sources, SourceInfo{
						Name:     "trufflehog",
						Version:  version,
						Findings: len(findings),
					})
				}
			}
		}
	}

	// Deduplicate findings
	allFindings = deduplicateFindings(allFindings)

	// Apply allowlist: inline markers first, then .ckb/secrets-allowlist.json
	// and .gitleaks.toml. An inline marker holds even when the allowlist file
	// failed to load.
	suppressed := 0
	if opts.ApplyAllowlist {
		var filtered []SecretFinding
		for i := range allFindings {
			isSuppressed, suppressID := allFindings[i].allowedInline, InlineAllowID
			if !isSuppressed {
				isSuppressed, suppressID = allowlist.IsSuppressed(&allFindings[i])
			}
			if isSuppressed {
				allFindings[i].Suppressed = true
				allFindings[i].SuppressID = suppressID
				suppressed++
			} else {
				filtered = append(filtered, allFindings[i])
			}
		}
		allFindings = filtered
	}

	// Apply severity filter
	if opts.MinSeverity != "" {
		minWeight := opts.MinSeverity.Weight()
		var filtered []SecretFinding
		for _, f := range allFindings {
			if f.Severity.Weight() >= minWeight {
				filtered = append(filtered, f)
			}
		}
		allFindings = filtered
	}

	// Sort findings by severity (critical first), then by file
	sort.Slice(allFindings, func(i, j int) bool {
		if allFindings[i].Severity.Weight() != allFindings[j].Severity.Weight() {
			return allFindings[i].Severity.Weight() > allFindings[j].Severity.Weight()
		}
		if allFindings[i].File != allFindings[j].File {
			return allFindings[i].File < allFindings[j].File
		}
		return allFindings[i].Line < allFindings[j].Line
	})

	// Build summary
	summary := buildSummary(allFindings)
	summary.FilesScanned = filesScanned

	return &ScanResult{
		RepoRoot:   opts.RepoRoot,
		Scope:      opts.Scope,
		ScannedAt:  time.Now(),
		Duration:   time.Since(start).String(),
		Findings:   allFindings,
		Summary:    summary,
		Sources:    sources,
		Suppressed: suppressed,
	}, nil
}

// scanBuiltin scans files using builtin patterns.
func (s *Scanner) scanBuiltin(ctx context.Context, opts ScanOptions) ([]SecretFinding, int, error) {
	// Find files to scan
	files, err := s.findFiles(opts)
	if err != nil {
		return nil, 0, err
	}

	var findings []SecretFinding

	for _, file := range files {
		select {
		case <-ctx.Done():
			return findings, len(files), ctx.Err()
		default:
		}

		fileFindings, err := s.scanFile(file, opts.MinEntropy)
		if err != nil {
			s.logger.Debug("Failed to scan file", "file", file, "error", err)
			continue
		}
		findings = append(findings, fileFindings...)
	}

	return findings, len(files), nil
}

// findFiles returns files to scan based on options.
func (s *Scanner) findFiles(opts ScanOptions) ([]string, error) {
	var files []string

	// Build exclude patterns
	excludePatterns := append([]string{}, opts.ExcludePaths...)

	err := filepath.Walk(opts.RepoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip inaccessible
		}

		if info.IsDir() {
			name := info.Name()
			// Skip common non-source directories
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == "__pycache__" || name == ".ckb" || name == ".venv" ||
				name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(opts.RepoRoot, path)
		if err != nil {
			return nil //nolint:nilerr // skip file if relative path fails
		}

		// Check path filters
		if len(opts.Paths) > 0 {
			matched := false
			for _, pattern := range opts.Paths {
				if m, _ := filepath.Match(pattern, relPath); m {
					matched = true
					break
				}
				// Also try matching against the base name
				if m, _ := filepath.Match(pattern, info.Name()); m {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}

		// Check exclude patterns
		for _, pattern := range excludePatterns {
			if m, _ := filepath.Match(pattern, relPath); m {
				return nil
			}
			if m, _ := filepath.Match(pattern, info.Name()); m {
				return nil
			}
		}

		// Skip binary files and large files
		if info.Size() > 10*1024*1024 { // 10MB
			return nil
		}
		if isBinaryFile(path) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	return files, err
}

// scanFile scans a single file for secrets.
func (s *Scanner) scanFile(path string, minEntropy float64) ([]SecretFinding, error) {
	file, err := os.Open(path) //nolint:gosec // G304: Path comes from filepath.Walk within repoRoot
	if err != nil {
		return nil, err
	}
	defer file.Close()

	relPath, _ := filepath.Rel(s.repoRoot, path)
	if relPath == "" {
		relPath = path
	}

	// Documentation files need higher entropy — prose words match secret patterns
	// but have low entropy compared to real secrets
	isDocFile := isDocumentationFile(relPath)

	var findings []SecretFinding
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip very long lines (likely minified/encoded)
		if len(line) > 1000 {
			continue
		}

		// Check each pattern
		for _, pattern := range s.patterns {
			matches := pattern.Regex.FindAllStringSubmatchIndex(line, -1)
			if matches == nil {
				continue
			}

			for _, match := range matches {
				// Extract the matched secret
				var secret string
				if len(match) >= 4 {
					// Use first capture group if available
					secret = line[match[2]:match[3]]
				} else {
					secret = line[match[0]:match[1]]
				}

				// Check entropy for patterns that require it
				if pattern.MinEntropy > 0 {
					requiredEntropy := pattern.MinEntropy
					// Documentation files need higher entropy to avoid flagging prose
					if isDocFile && (pattern.Type == SecretTypeGenericSecret || pattern.Type == SecretTypeGenericAPIKey) {
						if requiredEntropy < 4.0 {
							requiredEntropy = 4.0
						}
					}
					entropy := ShannonEntropy(secret)
					if entropy < requiredEntropy {
						continue
					}
				}

				// Check for false positive indicators
				if isLikelyFalsePositive(line, secret) {
					continue
				}

				// Calculate confidence
				confidence := calculateConfidence(secret, pattern)

				findings = append(findings, SecretFinding{
					File:       relPath,
					Line:       lineNum,
					Column:     match[0] + 1,
					Type:       pattern.Type,
					Severity:   pattern.Severity,
					Match:      redactSecret(secret, 4),
					RawMatch:   secret,
					Context:    redactLine(line, match[0], match[1]),
					Rule:       pattern.Name,
					Confidence: confidence,
					Source:     "builtin",

					allowedInline: hasInlineAllow(line),
				})
			}
		}
	}

	return findings, scanner.Err()
}

// InlineAllowMarkers are the comments that allow a finding on their line:
// CKB's own and gitleaks', which CKB already honours the config of.
//
//	curl http://user:pw@localhost/ # ckb:allow-secret
var InlineAllowMarkers = []string{"ckb:allow-secret", "gitleaks:allow"}

// InlineAllowID is the suppressId of a finding allowed by an inline marker.
const InlineAllowID = "inline"

// hasInlineAllow reports whether line carries an inline allow marker.
func hasInlineAllow(line string) bool {
	for _, m := range InlineAllowMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

// calculateConfidence calculates a confidence score for a finding.
func calculateConfidence(secret string, pattern Pattern) float64 {
	confidence := 0.7 // Base confidence for pattern match

	// Increase for high entropy
	entropy := ShannonEntropy(secret)
	if entropy > 4.0 {
		confidence += 0.2
	} else if entropy > 3.5 {
		confidence += 0.1
	}

	// Decrease for patterns that commonly match placeholders
	if pattern.Type == SecretTypeGenericAPIKey || pattern.Type == SecretTypeGenericSecret {
		confidence -= 0.1
	}

	// Specific patterns are more reliable
	if pattern.Type == SecretTypeGitHubPAT || pattern.Type == SecretTypeAWSAccessKey {
		confidence += 0.1
	}

	// Clamp to [0, 1]
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	return confidence
}

// goStructDeclRe matches Go struct field declarations like:
//
//	Token     string `json:"token"`
//	Secret    string `json:"secret"`
//	Password  []byte
var goStructDeclRe = regexp.MustCompile(`(?i)\b(secret|token|password|passwd|pwd)\s+(string|bool|int|\[\]byte|\[\]string|\*?\w+Config)\b`)

// configKeyVarRe matches config/map key assignments where the value is a
// variable name (not a string literal), e.g.:
//
//	"token":      rawToken,
//	"new_token":  rawToken,
var configKeyVarRe = regexp.MustCompile(`(?i)["'](?:secret|token|password|passwd|pwd|new_token)["']\s*:\s*[a-zA-Z]\w*[,\s})]`)

// varRefRe matches when the captured "secret" is actually a variable or
// attribute reference rather than a literal value.  Examples:
//
//	api_key=self._settings.openai_api_key   (Python attribute chain)
//	apiKey: config.apiKey                    (JS/TS property access)
//	api_key=os.environ["KEY"]               (env lookup)
//	token = process.env.TOKEN               (Node env)
//	key := viper.GetString("api_key")       (Go config)
//
// varRefRe matches variable/attribute references. The first branch (dotted
// chain anchored with $) covers fully-qualified references like config.apiKey.
// Branches 2-4 handle partial captures where the scanner only grabs a prefix
// (e.g., "os.environ" from os.environ["KEY"]) — the $ anchor on branch 1
// would reject those because of trailing brackets/parens.
var varRefRe = regexp.MustCompile(
	`^[a-zA-Z_][\w]*(?:\.[\w]+)+$` + // dotted attr chain: self._settings.openai_api_key
		`|^os\.(?:environ|getenv)` + // Python os.environ / os.getenv (partial capture)
		`|^process\.env` + // Node process.env (partial capture)
		`|^(?:viper|config|cfg|settings|conf)\.`, // common config accessors (partial capture)
)

// isLikelyFalsePositive checks for common false positive patterns.
func isLikelyFalsePositive(line, secret string) bool {
	lineLower := strings.ToLower(line)

	// Go struct field declarations and config key→variable assignments are not secrets
	if goStructDeclRe.MatchString(line) {
		return true
	}
	if configKeyVarRe.MatchString(line) {
		return true
	}

	// The captured "secret" is a variable/attribute reference, not a literal
	if varRefRe.MatchString(strings.TrimSpace(secret)) {
		return true
	}

	// Check for test/example/dev indicators
	falsePositiveIndicators := []string{
		"example",
		"sample",
		"placeholder",
		"dummy",
		"test",
		"fake",
		"mock",
		"<your",
		"your_",
		"xxx",
		"changeme",
		"replace",
		"insert",
		"fixme",
		"todo",
		"local-dev",
		"dev-token",
		"dev-secret",
		"default",
		"template",
		"demo",
	}

	for _, indicator := range falsePositiveIndicators {
		if strings.Contains(lineLower, indicator) {
			return true
		}
	}

	// Shell/template variable interpolation — not a literal secret
	// Covers ${VAR}, ${VAR:-default}, ${VAR:?error}, $VAR, {{ .var }}
	if strings.Contains(secret, "${") ||
		strings.Contains(secret, "$(") ||
		strings.Contains(secret, "{{") ||
		(strings.HasPrefix(strings.TrimSpace(secret), "$") && !strings.ContainsAny(secret, " \t")) {
		return true
	}

	// Check for common test values
	secretLower := strings.ToLower(secret)
	if strings.HasPrefix(secretLower, "example") ||
		strings.HasPrefix(secretLower, "test") ||
		strings.Contains(secretLower, "xxxxxxxx") {
		return true
	}

	return false
}

// redactSecret partially hides a secret value.
func redactSecret(s string, keepPrefix int) string {
	if len(s) <= keepPrefix {
		return strings.Repeat("*", len(s))
	}
	return s[:keepPrefix] + strings.Repeat("*", len(s)-keepPrefix)
}

// redactLine redacts the secret portion of a line.
func redactLine(line string, start, end int) string {
	if start < 0 || end > len(line) || start >= end {
		return line
	}

	secretLen := end - start
	redacted := strings.Repeat("*", min(secretLen, 20))

	// Truncate context if too long
	maxContext := 100
	result := line[:start] + redacted + line[end:]
	if len(result) > maxContext {
		// Center around the redacted portion
		half := maxContext / 2
		centerStart := start + len(redacted)/2
		resultStart := max(0, centerStart-half)
		resultEnd := min(len(result), centerStart+half)
		result = "..." + result[resultStart:resultEnd] + "..."
	}

	return result
}

// isDocumentationFile checks if a file is documentation (markdown, text, rst).
func isDocumentationFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown", ".txt", ".rst", ".adoc", ".textile":
		return true
	}
	// Also check for common doc file names
	base := strings.ToLower(filepath.Base(path))
	return base == "readme" || base == "changelog" || base == "contributing" ||
		base == "license" || base == "authors"
}

// isBinaryFile checks if a file is likely binary.
func isBinaryFile(path string) bool {
	// Check by extension first
	ext := strings.ToLower(filepath.Ext(path))
	binaryExts := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".zip": true, ".tar": true, ".gz": true, ".rar": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".ico": true, ".pdf": true, ".doc": true, ".docx": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
		".mp3": true, ".mp4": true, ".avi": true, ".mov": true,
		".pyc": true, ".class": true, ".o": true, ".a": true,
	}
	if binaryExts[ext] {
		return true
	}

	// Check file content
	f, err := os.Open(path) //nolint:gosec // G304: Path comes from filepath.Walk within repoRoot
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	// Check for null bytes (indicates binary)
	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}

	return false
}

// deduplicateFindings removes duplicate findings.
func deduplicateFindings(findings []SecretFinding) []SecretFinding {
	seen := make(map[string]bool)
	var result []SecretFinding

	for _, f := range findings {
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.RawMatch)
		if !seen[key] {
			seen[key] = true
			result = append(result, f)
		}
	}

	return result
}

// buildSummary creates a summary from findings.
func buildSummary(findings []SecretFinding) ScanSummary {
	summary := ScanSummary{
		TotalFindings: len(findings),
		BySeverity:    make(map[Severity]int),
		ByType:        make(map[SecretType]int),
	}

	files := make(map[string]bool)
	for _, f := range findings {
		summary.BySeverity[f.Severity]++
		summary.ByType[f.Type]++
		files[f.File] = true
	}

	summary.FilesWithSecrets = len(files)

	return summary
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
