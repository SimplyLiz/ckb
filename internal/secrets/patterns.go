package secrets

import "regexp"

// Pattern defines a secret detection pattern.
type Pattern struct {
	Name        string
	Type        SecretType
	Severity    Severity
	Regex       *regexp.Regexp
	MinEntropy  float64 // Minimum entropy (0 = disabled)
	Description string
	Examples    []string // For testing
}

// The parts of a URL's authority, for the patterns that look for a password in
// one. Userinfo holds no whitespace, quotes or delimiters (RFC 3986), so a
// match can't run from "http://host:8080" to an '@' later on the line, such
// as the "$@" of a shell command. A password may contain ':'; a user may not.
const (
	urlUser = "[^\\s:/?#@'\"`<>]+"
	urlPass = "[^\\s/?#@'\"`<>]"
	urlHost = "[^\\s/?#@'\"`<>]+"
)

// BuiltinPatterns contains all builtin secret detection patterns.
// These are based on well-known secret formats from various providers.
var BuiltinPatterns = []Pattern{
	// ============ AWS ============
	{
		Name:        "aws_access_key_id",
		Type:        SecretTypeAWSAccessKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`(?:^|[^A-Z0-9])((?:A3T[A-Z0-9]|AKIA|ABIA|ACCA|AGPA|AIDA|AIPA|AKIA|ANPA|ANVA|APKA|AROA|ASCA|ASIA)[A-Z0-9]{16})(?:[^A-Z0-9]|$)`),
		Description: "AWS Access Key ID",
		Examples:    []string{"AKIAIOSFODNN7EXAMPLE"},
	},
	{
		Name:        "aws_secret_key",
		Type:        SecretTypeAWSSecretKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`(?i)(?:aws[_-]?)?secret[_-]?(?:access[_-]?)?key['":\s=]+['"]?([A-Za-z0-9/+=]{40})['"]?`),
		MinEntropy:  3.5,
		Description: "AWS Secret Access Key",
	},

	// ============ GitHub ============
	{
		Name:        "github_pat",
		Type:        SecretTypeGitHubPAT,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`),
		Description: "GitHub Personal Access Token",
		// Note: Example omitted to avoid triggering security scanners
	},
	{
		Name:        "github_oauth",
		Type:        SecretTypeGitHubOAuth,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`gho_[A-Za-z0-9]{36,}`),
		Description: "GitHub OAuth Access Token",
	},
	{
		Name:        "github_app",
		Type:        SecretTypeGitHubApp,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`(?:ghu|ghs)_[A-Za-z0-9]{36,}`),
		Description: "GitHub App Token",
	},
	{
		Name:        "github_refresh",
		Type:        SecretTypeGitHubRefresh,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`ghr_[A-Za-z0-9]{36,}`),
		Description: "GitHub Refresh Token",
	},
	{
		Name:        "github_fine_grained",
		Type:        SecretTypeGitHubPAT,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`github_pat_[A-Za-z0-9]{22}_[A-Za-z0-9]{59}`),
		Description: "GitHub Fine-Grained Personal Access Token",
	},

	// ============ Stripe ============
	{
		Name:        "stripe_live_secret",
		Type:        SecretTypeStripeLiveKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`),
		Description: "Stripe Live Secret Key",
	},
	{
		Name:        "stripe_live_restricted",
		Type:        SecretTypeStripeLiveKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`rk_live_[A-Za-z0-9]{24,}`),
		Description: "Stripe Live Restricted Key",
	},
	{
		Name:        "stripe_test_secret",
		Type:        SecretTypeStripeTestKey,
		Severity:    SeverityLow,
		Regex:       regexp.MustCompile(`sk_test_[A-Za-z0-9]{24,}`),
		Description: "Stripe Test Secret Key",
	},

	// ============ Slack ============
	{
		Name:        "slack_bot_token",
		Type:        SecretTypeSlackBotToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`xoxb-[0-9]{10,13}-[0-9]{10,13}-[A-Za-z0-9]{24}`),
		Description: "Slack Bot Token",
	},
	{
		Name:        "slack_user_token",
		Type:        SecretTypeSlackUserToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`xoxp-[0-9]{10,13}-[0-9]{10,13}-[0-9]{10,13}-[A-Za-z0-9]{32}`),
		Description: "Slack User Token",
	},
	{
		Name:        "slack_webhook",
		Type:        SecretTypeSlackWebhook,
		Severity:    SeverityMedium,
		Regex:       regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Z0-9]{8,}/B[A-Z0-9]{8,}/[A-Za-z0-9]{24}`),
		Description: "Slack Webhook URL",
	},
	{
		Name:        "slack_app_token",
		Type:        SecretTypeSlackBotToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`xapp-[0-9]+-[A-Z0-9]+-[0-9]+-[A-Za-z0-9]+`),
		Description: "Slack App Token",
	},

	// ============ Private Keys ============
	{
		Name:        "private_key_rsa",
		Type:        SecretTypePrivateKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`-----BEGIN (?:RSA )?PRIVATE KEY-----`),
		Description: "RSA Private Key",
	},
	{
		Name:        "private_key_ec",
		Type:        SecretTypePrivateKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`-----BEGIN EC PRIVATE KEY-----`),
		Description: "EC Private Key",
	},
	{
		Name:        "private_key_openssh",
		Type:        SecretTypePrivateKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`-----BEGIN OPENSSH PRIVATE KEY-----`),
		Description: "OpenSSH Private Key",
	},
	{
		Name:        "private_key_dsa",
		Type:        SecretTypePrivateKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`-----BEGIN DSA PRIVATE KEY-----`),
		Description: "DSA Private Key",
	},
	{
		Name:        "private_key_pgp",
		Type:        SecretTypePrivateKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`-----BEGIN PGP PRIVATE KEY BLOCK-----`),
		Description: "PGP Private Key",
	},

	// ============ JWT ============
	{
		Name:        "jwt_token",
		Type:        SecretTypeJWT,
		Severity:    SeverityMedium,
		Regex:       regexp.MustCompile(`eyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*`),
		MinEntropy:  3.0,
		Description: "JSON Web Token",
	},

	// ============ Google ============
	{
		Name:        "google_api_key",
		Type:        SecretTypeGoogleAPIKey,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`AIza[A-Za-z0-9_-]{35}`),
		Description: "Google API Key",
	},

	// ============ NPM ============
	{
		Name:        "npm_token",
		Type:        SecretTypeNPMToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),
		Description: "NPM Access Token",
	},

	// ============ PyPI ============
	{
		Name:        "pypi_token",
		Type:        SecretTypePyPIToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`pypi-[A-Za-z0-9_-]{100,}`),
		Description: "PyPI API Token",
	},

	// ============ Generic Patterns (require entropy check) ============
	{
		Name:        "generic_api_key",
		Type:        SecretTypeGenericAPIKey,
		Severity:    SeverityMedium,
		Regex:       regexp.MustCompile(`(?i)(?:api[_-]?key|apikey)['":\s=]+['"]?([A-Za-z0-9_-]{20,64})['"]?`),
		MinEntropy:  3.5,
		Description: "Generic API Key",
	},
	{
		Name:        "generic_secret",
		Type:        SecretTypeGenericSecret,
		Severity:    SeverityMedium,
		Regex:       regexp.MustCompile(`(?i)(?:secret|password|passwd|pwd|token)\s*[=:]\s*['"]([A-Za-z0-9!@#$%^&*()_+\-=]{8,64})['"]`),
		MinEntropy:  3.0,
		Description: "Generic Secret or Password",
	},
	{
		Name:        "password_in_url",
		Type:        SecretTypePasswordInURL,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`://` + urlUser + `:(` + urlPass + `{3,})@` + urlHost),
		MinEntropy:  2.5,
		Description: "Password in URL",
	},
	{
		Name:        "basic_auth_header",
		Type:        SecretTypeGenericSecret,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`(?i)authorization['":\s=]+['"]?basic\s+([A-Za-z0-9+/=]{20,})['"]?`),
		Description: "Basic Auth Header",
	},
	{
		Name:        "bearer_token",
		Type:        SecretTypeGenericSecret,
		Severity:    SeverityMedium,
		Regex:       regexp.MustCompile(`(?i)authorization['":\s=]+['"]?bearer\s+([A-Za-z0-9._-]{20,})['"]?`),
		MinEntropy:  3.0,
		Description: "Bearer Token",
	},

	// ============ Azure (v8.1) ============
	{
		Name:        "azure_storage_key",
		Type:        SecretTypeAzureStorageKey,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`(?i)AccountKey\s*=\s*([A-Za-z0-9+/=]{88})`),
		Description: "Azure Storage Account Key",
	},
	{
		Name:        "azure_connection_string",
		Type:        SecretTypeAzureConnectionStr,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`DefaultEndpointsProtocol=https;AccountName=[^;\s]+;AccountKey=[A-Za-z0-9+/=]{88}`),
		Description: "Azure Storage Connection String",
	},

	// ============ GCP (v8.1) ============
	{
		Name:        "gcp_service_account",
		Type:        SecretTypeGCPServiceAccount,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`"type"\s*:\s*"service_account"[\s\S]*"private_key"\s*:\s*"-----BEGIN`),
		Description: "GCP Service Account Key (JSON)",
	},
	{
		Name:        "gcp_oauth_token",
		Type:        SecretTypeGCPOAuthToken,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{50,}`),
		Description: "GCP OAuth Access Token",
	},

	// ============ Heroku (v8.1) ============
	{
		Name:        "heroku_api_key",
		Type:        SecretTypeHerokuAPIKey,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`(?i)(?:heroku[_-]?api[_-]?key|HEROKU_API_KEY)['":\s=]+['"]?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})['"]?`),
		Description: "Heroku API Key",
	},

	// ============ Twilio (v8.1) ============
	{
		Name:        "twilio_account_sid",
		Type:        SecretTypeTwilioSID,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`AC[a-f0-9]{32}`),
		Description: "Twilio Account SID",
	},
	{
		Name:        "twilio_auth_token",
		Type:        SecretTypeTwilioAuthToken,
		Severity:    SeverityCritical,
		Regex:       regexp.MustCompile(`(?i)(?:twilio[_-]?auth[_-]?token|TWILIO_AUTH_TOKEN)['":\s=]+['"]?([a-f0-9]{32})['"]?`),
		MinEntropy:  3.0,
		Description: "Twilio Auth Token",
	},

	// ============ SendGrid (v8.1) ============
	{
		Name:        "sendgrid_api_key",
		Type:        SecretTypeSendGridAPIKey,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`SG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}`),
		Description: "SendGrid API Key",
	},

	// ============ Database Connection Strings (v8.1) ============
	{
		Name:        "mongodb_uri",
		Type:        SecretTypeMongoDBURI,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`mongodb(?:\+srv)?://` + urlUser + `:(` + urlPass + `+)@` + urlHost),
		MinEntropy:  2.5,
		Description: "MongoDB Connection URI with Password",
	},
	{
		Name:        "postgres_uri",
		Type:        SecretTypePostgresURI,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`postgres(?:ql)?://` + urlUser + `:(` + urlPass + `+)@` + urlHost),
		MinEntropy:  2.5,
		Description: "PostgreSQL Connection URI with Password",
	},
	{
		Name:        "mysql_uri",
		Type:        SecretTypeMySQLURI,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`mysql://` + urlUser + `:(` + urlPass + `+)@` + urlHost),
		MinEntropy:  2.5,
		Description: "MySQL Connection URI with Password",
	},
	{
		Name:        "redis_uri",
		Type:        SecretTypeRedisURI,
		Severity:    SeverityHigh,
		Regex:       regexp.MustCompile(`redis://(?:(?:` + urlUser + `)?:)?(` + urlPass + `+)@` + urlHost),
		MinEntropy:  2.5,
		Description: "Redis Connection URI with Password",
	},
}

// GetPatternByName returns a pattern by name.
func GetPatternByName(name string) *Pattern {
	for i := range BuiltinPatterns {
		if BuiltinPatterns[i].Name == name {
			return &BuiltinPatterns[i]
		}
	}
	return nil
}

// GetPatternsByType returns all patterns for a given secret type.
func GetPatternsByType(t SecretType) []Pattern {
	var result []Pattern
	for _, p := range BuiltinPatterns {
		if p.Type == t {
			result = append(result, p)
		}
	}
	return result
}

// GetPatternsBySeverity returns all patterns at or above a given severity.
func GetPatternsBySeverity(minSeverity Severity) []Pattern {
	minWeight := minSeverity.Weight()
	var result []Pattern
	for _, p := range BuiltinPatterns {
		if p.Severity.Weight() >= minWeight {
			result = append(result, p)
		}
	}
	return result
}
