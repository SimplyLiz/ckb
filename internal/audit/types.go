// Package audit provides risk analysis for codebases.
// It identifies risky code based on multiple signals including complexity,
// test coverage, bus factor, staleness, and security sensitivity.
package audit

import (
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/pathclass"
)

// RiskAnalysis is the result of a risk audit
type RiskAnalysis struct {
	Repo       string      `json:"repo"`
	AnalyzedAt time.Time   `json:"analyzedAt"`
	Items      []RiskItem  `json:"items"`
	Summary    RiskSummary `json:"summary"`
	QuickWins  []QuickWin  `json:"quickWins"`
	// LeftOut counts the files in generated or data paths (.ckb/config.json paths)
	// that weren't scored; nil when none were.
	LeftOut *pathclass.Tally `json:"leftOut,omitempty"`
}

// FunctionRisk contains per-function complexity metrics within a risky file.
type FunctionRisk struct {
	Name       string `json:"name"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
	Cyclomatic int    `json:"cyclomatic"`
	Cognitive  int    `json:"cognitive"`
	Lines      int    `json:"lines"`
}

// RiskItem represents a single file/module with risk assessment
type RiskItem struct {
	File               string         `json:"file"`
	Module             string         `json:"module,omitempty"`
	RiskScore          float64        `json:"riskScore"` // 0-100
	RiskLevel          string         `json:"riskLevel"` // "critical" | "high" | "medium" | "low"
	Factors            []RiskFactor   `json:"factors"`
	Recommendation     string         `json:"recommendation,omitempty"`
	FunctionComplexity []FunctionRisk `json:"functionComplexity,omitempty"`
}

// RiskFactor represents a contributing factor to the risk score
type RiskFactor struct {
	Factor       string  `json:"factor"`       // factor type
	Value        string  `json:"value"`        // display value
	Weight       float64 `json:"weight"`       // 0-1 weight
	Contribution float64 `json:"contribution"` // points contributed to score
}

// RiskSummary provides aggregate statistics
type RiskSummary struct {
	Critical       int             `json:"critical"`
	High           int             `json:"high"`
	Medium         int             `json:"medium"`
	Low            int             `json:"low"`
	TopRiskFactors []TopRiskFactor `json:"topRiskFactors"`
}

// TopRiskFactor represents a commonly occurring risk factor
type TopRiskFactor struct {
	Factor string `json:"factor"`
	Count  int    `json:"count"`
}

// QuickWin represents a low-effort, high-impact improvement
type QuickWin struct {
	Action string `json:"action"`
	Target string `json:"target"`
	Effort string `json:"effort"` // "low" | "medium" | "high"
	Impact string `json:"impact"` // "low" | "medium" | "high"
}

// AuditOptions configures the risk audit
type AuditOptions struct {
	RepoRoot  string  // Repository root path
	MinScore  float64 // Minimum risk score to include (default: 40)
	Limit     int     // Max items to return (default: 50)
	Factor    string  // Filter by specific factor (optional)
	QuickWins bool    // Only show quick wins
}

// RiskFactorType constants
const (
	FactorComplexity        = "complexity"
	FactorTestCoverage      = "test_coverage"
	FactorBusFactor         = "bus_factor"
	FactorStaleness         = "staleness"
	FactorSecuritySensitive = "security_sensitive"
	FactorErrorRate         = "error_rate"
	FactorCoChangeCoupling  = "co_change_coupling"
	FactorChurn             = "churn"
)

// RiskLevel constants
const (
	RiskLevelCritical = "critical"
	RiskLevelHigh     = "high"
	RiskLevelMedium   = "medium"
	RiskLevelLow      = "low"
)

// RiskWeights defines the weight for each factor
var RiskWeights = map[string]float64{
	FactorComplexity:        0.20,
	FactorTestCoverage:      0.20,
	FactorBusFactor:         0.15,
	FactorStaleness:         0.10,
	FactorSecuritySensitive: 0.15,
	FactorErrorRate:         0.10,
	FactorCoChangeCoupling:  0.05,
	FactorChurn:             0.05,
}

// SecurityKeywords are keywords that indicate security-sensitive code. Each
// matches identifier parts, not substrings (see matchSecurityKeywords), and
// .ckb/config.json audit.securityKeywords replaces the list for a repo.
//
// Bare "token", "key", "hash", "private" and "session" are not in it: they
// are a tokenizer's tokens, a dict's keys, a hash map, a Java modifier and
// requests.Session as often as they are credentials. The compounds that are
// credentials are listed instead.
var SecurityKeywords = []string{
	"password", "passwd", "secret", "credential",
	"auth", "authenticate", "authentication", "authorize", "authorization",
	"encrypt", "encrypted", "encryption", "decrypt", "decrypted", "decryption",
	"salt", "bcrypt", "hmac", "certificate", "oauth", "jwt",
	"apikey", "api_key", "access_key", "secret_key", "private_key", "signing_key",
	"access_token", "refresh_token", "auth_token", "bearer_token", "id_token",
	"session_token", "session_id", "csrf_token", "api_token", "hf_token", "github_token",
}

// GetRiskLevel returns the risk level based on score
func GetRiskLevel(score float64) string {
	switch {
	case score >= 80:
		return RiskLevelCritical
	case score >= 60:
		return RiskLevelHigh
	case score >= 40:
		return RiskLevelMedium
	default:
		return RiskLevelLow
	}
}
