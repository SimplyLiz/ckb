// Package secrets provides secret detection capabilities for CKB.
// It identifies exposed secrets (API keys, tokens, passwords) using
// builtin patterns and optional external tools (gitleaks, trufflehog).
package secrets

import "time"

// SecretType identifies the kind of secret detected.
type SecretType string

// #nosec G101 -- These are type identifiers for secret classification, not actual credentials
// nolint:gosec // G101: These are type identifiers for secret classification, not actual credentials
const (
	SecretTypeAWSAccessKey   SecretType = "aws_access_key"
	SecretTypeAWSSecretKey   SecretType = "aws_secret_key"
	SecretTypeGitHubPAT      SecretType = "github_pat"
	SecretTypeGitHubOAuth    SecretType = "github_oauth"
	SecretTypeGitHubApp      SecretType = "github_app"
	SecretTypeGitHubRefresh  SecretType = "github_refresh"
	SecretTypeStripeLiveKey  SecretType = "stripe_live_key"
	SecretTypeStripeTestKey  SecretType = "stripe_test_key"
	SecretTypeSlackBotToken  SecretType = "slack_bot_token"
	SecretTypeSlackUserToken SecretType = "slack_user_token"
	SecretTypeSlackWebhook   SecretType = "slack_webhook"
	SecretTypePrivateKey     SecretType = "private_key"
	SecretTypeJWT            SecretType = "jwt"
	SecretTypeGenericAPIKey  SecretType = "generic_api_key"
	SecretTypeGenericSecret  SecretType = "generic_secret"
	SecretTypePasswordInURL  SecretType = "password_in_url"
	SecretTypeGoogleAPIKey   SecretType = "google_api_key"
	SecretTypeNPMToken       SecretType = "npm_token"
	SecretTypePyPIToken      SecretType = "pypi_token"
	SecretTypeExternal       SecretType = "external" // From external tools

	// Cloud Providers (v8.1)
	SecretTypeAzureStorageKey    SecretType = "azure_storage_key"
	SecretTypeAzureConnectionStr SecretType = "azure_connection_string"
	SecretTypeGCPServiceAccount  SecretType = "gcp_service_account"
	SecretTypeGCPOAuthToken      SecretType = "gcp_oauth_token"
	SecretTypeHerokuAPIKey       SecretType = "heroku_api_key"
	SecretTypeTwilioSID          SecretType = "twilio_sid"
	SecretTypeTwilioAuthToken    SecretType = "twilio_auth_token"
	SecretTypeSendGridAPIKey     SecretType = "sendgrid_api_key"

	// Database Connection Strings (v8.1)
	SecretTypeMongoDBURI  SecretType = "mongodb_uri"
	SecretTypePostgresURI SecretType = "postgres_uri"
	SecretTypeMySQLURI    SecretType = "mysql_uri"
	SecretTypeRedisURI    SecretType = "redis_uri"
)

// Severity indicates the risk level of a finding.
type Severity string

const (
	SeverityCritical Severity = "critical" // Active production credentials, private keys
	SeverityHigh     Severity = "high"     // API keys, tokens with significant access
	SeverityMedium   Severity = "medium"   // Possible secrets, need verification
	SeverityLow      Severity = "low"      // Test keys, example values
)

// SeverityWeight returns a numeric weight for sorting.
func (s Severity) Weight() int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// ScanScope defines what to scan.
type ScanScope string

const (
	ScopeWorkdir     ScanScope = "workdir"      // Current working directory files
	ScopeStaged      ScanScope = "staged"       // Only git staged files
	ScopeHistory     ScanScope = "history"      // Git commit history
	ScopeCommitRange ScanScope = "commit_range" // Specific commit range
)

// SecretFinding represents a single detected secret.
type SecretFinding struct {
	File       string     `json:"file"`
	Line       int        `json:"line"`
	Column     int        `json:"column,omitempty"`
	Type       SecretType `json:"type"`
	Severity   Severity   `json:"severity"`
	Match      string     `json:"match"`      // Redacted/truncated match
	RawMatch   string     `json:"-"`          // Full match (not serialized)
	Context    string     `json:"context"`    // Surrounding line, redacted
	Rule       string     `json:"rule"`       // Pattern name that matched
	Confidence float64    `json:"confidence"` // 0-1, based on entropy + pattern
	Source     string     `json:"source"`     // "builtin", "gitleaks", "trufflehog"

	// Git-specific fields (only populated for history scans)
	Commit     string `json:"commit,omitempty"`
	Author     string `json:"author,omitempty"`
	CommitDate string `json:"commitDate,omitempty"`

	// Allowlist info
	Suppressed bool   `json:"suppressed,omitempty"`
	SuppressID string `json:"suppressId,omitempty"`

	// allowedInline is set when the line carries an allow marker
	// (see InlineAllowMarkers).
	allowedInline bool
}

// ScanOptions configures the secret scan.
type ScanOptions struct {
	RepoRoot     string    `json:"repoRoot"`
	Scope        ScanScope `json:"scope"`
	Paths        []string  `json:"paths,omitempty"`        // Limit to these paths (glob)
	ExcludePaths []string  `json:"excludePaths,omitempty"` // Skip these paths

	// Filtering
	Types       []SecretType `json:"types,omitempty"`       // Filter by secret type
	MinSeverity Severity     `json:"minSeverity,omitempty"` // Minimum severity

	// History options
	SinceCommit string `json:"sinceCommit,omitempty"` // Scan commits since
	UntilCommit string `json:"untilCommit,omitempty"` // Scan commits until
	MaxCommits  int    `json:"maxCommits,omitempty"`  // Limit for performance

	// External tool options
	UseGitleaks    bool `json:"useGitleaks,omitempty"`
	UseTrufflehog  bool `json:"useTrufflehog,omitempty"`
	PreferExternal bool `json:"preferExternal,omitempty"` // Use external if available

	// Detection tuning
	MinEntropy float64 `json:"minEntropy,omitempty"` // For generic patterns (default: 3.5)

	// Allowlist
	ApplyAllowlist bool `json:"applyAllowlist,omitempty"` // Default: true
}

// ScanResult contains the complete scan result.
type ScanResult struct {
	RepoRoot  string          `json:"repoRoot"`
	Scope     ScanScope       `json:"scope"`
	ScannedAt time.Time       `json:"scannedAt"`
	Duration  string          `json:"duration"`
	Findings  []SecretFinding `json:"findings"`
	Summary   ScanSummary     `json:"summary"`
	Sources   []SourceInfo    `json:"sources"`

	// Suppression info
	Suppressed int `json:"suppressed,omitempty"`
}

// ScanSummary provides aggregate statistics.
type ScanSummary struct {
	TotalFindings    int                `json:"totalFindings"`
	BySeverity       map[Severity]int   `json:"bySeverity"`
	ByType           map[SecretType]int `json:"byType"`
	FilesScanned     int                `json:"filesScanned"`
	FilesWithSecrets int                `json:"filesWithSecrets"`
}

// SourceInfo describes a detection source.
type SourceInfo struct {
	Name     string `json:"name"` // "builtin", "gitleaks", "trufflehog"
	Version  string `json:"version,omitempty"`
	Findings int    `json:"findings"`
}

// DefaultExcludePaths returns common paths to exclude from scanning.
func DefaultExcludePaths() []string {
	return []string{
		"vendor/*",
		"node_modules/*",
		".git/*",
		"*.min.js",
		"*.bundle.js",
		"go.sum",
		"package-lock.json",
		"yarn.lock",
		"pnpm-lock.yaml",
		"Cargo.lock",
		"poetry.lock",
	}
}
