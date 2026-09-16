package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// DefaultDaemonPort is the default port the CKB daemon listens on.
const DefaultDaemonPort = 9120

// EnvOverride records an environment variable override that was applied
type EnvOverride struct {
	EnvVar    string      // e.g., "CKB_BUDGET_MAX_MODULES"
	Path      string      // e.g., "budget.maxModules"
	Value     interface{} // The parsed value that was applied
	FromValue string      // Original string value from env
}

// LoadResult contains the loaded config plus metadata about how it was loaded
type LoadResult struct {
	Config       *Config
	ConfigPath   string        // Path to config file that was loaded (empty if defaults used)
	EnvOverrides []EnvOverride // Environment variable overrides that were applied
	UsedDefaults bool          // True if no config file was found
}

// Config represents the complete CKB configuration (v5 schema, extended in v6.2.1)
type Config struct {
	Version  int    `json:"version" mapstructure:"version"`
	RepoRoot string `json:"repoRoot" mapstructure:"repoRoot"`

	Backends      BackendsConfig      `json:"backends" mapstructure:"backends"`
	QueryPolicy   QueryPolicyConfig   `json:"queryPolicy" mapstructure:"queryPolicy"`
	LspSupervisor LspSupervisorConfig `json:"lspSupervisor" mapstructure:"lspSupervisor"`
	Modules       ModulesConfig       `json:"modules" mapstructure:"modules"`
	ImportScan    ImportScanConfig    `json:"importScan" mapstructure:"importScan"`
	Cache         CacheConfig         `json:"cache" mapstructure:"cache"`
	Budget        BudgetConfig        `json:"budget" mapstructure:"budget"`
	BackendLimits BackendLimitsConfig `json:"backendLimits" mapstructure:"backendLimits"`
	Privacy       PrivacyConfig       `json:"privacy" mapstructure:"privacy"`
	Logging       LoggingConfig       `json:"logging" mapstructure:"logging"`

	// v6.2.1 Daemon mode
	Daemon   DaemonConfig    `json:"daemon" mapstructure:"daemon"`
	Webhooks []WebhookConfig `json:"webhooks" mapstructure:"webhooks"`

	// v6.4 Telemetry
	Telemetry TelemetryConfig `json:"telemetry" mapstructure:"telemetry"`

	// v7.2 Analysis tier
	// Values: "auto", "fast", "standard", "full"
	Tier string `json:"tier,omitempty" mapstructure:"tier"`

	// v8.1 Change Impact Analysis
	Coverage CoverageConfig `json:"coverage" mapstructure:"coverage"`

	// v8.2 Unified PR Review
	Review ReviewConfig `json:"review" mapstructure:"review"`

	// v8.2 LLM integration
	LLM LLMConfig `json:"llm" mapstructure:"llm"`

	// v8.3 Compliance auditing
	Compliance ComplianceConfig `json:"compliance" mapstructure:"compliance"`

	// Activity ledger (MCP tool-call audit trail)
	Activity ActivityConfig `json:"activity" mapstructure:"activity"`
}

// ActivityConfig controls the MCP activity ledger (tool_calls table).
type ActivityConfig struct {
	Enabled       bool `json:"enabled" mapstructure:"enabled"`
	RetentionDays int  `json:"retentionDays" mapstructure:"retentionDays"`
	StoreParams   bool `json:"storeParams" mapstructure:"storeParams"`
}

// ComplianceConfig configures compliance audit behavior (v8.3)
type ComplianceConfig struct {
	PIIFieldPatterns     []string `json:"piiFieldPatterns,omitempty" mapstructure:"piiFieldPatterns"`
	AIComponentPaths     []string `json:"aiComponentPaths,omitempty" mapstructure:"aiComponentPaths"`
	SILLevel             int      `json:"silLevel,omitempty" mapstructure:"silLevel"`
	SpecialCategoryPaths []string `json:"specialCategoryPaths,omitempty" mapstructure:"specialCategoryPaths"`
	DefaultFrameworks    []string `json:"defaultFrameworks,omitempty" mapstructure:"defaultFrameworks"`
}

// CoverageConfig contains coverage file configuration (v8.1)
type CoverageConfig struct {
	Paths      []string `json:"paths" mapstructure:"paths"`           // Custom paths to check for coverage files
	AutoDetect bool     `json:"autoDetect" mapstructure:"autoDetect"` // Use language-specific auto-detection (default: true)
	MaxAge     string   `json:"maxAge" mapstructure:"maxAge"`         // Max age before marking as stale (default: "168h" = 7 days)
}

// ReviewConfig contains PR review policy defaults (v8.2)
type ReviewConfig struct {
	// Policy defaults (can be overridden per-invocation)
	BlockBreakingChanges bool    `json:"blockBreakingChanges" mapstructure:"blockBreakingChanges"` // Fail on breaking API changes
	BlockSecrets         bool    `json:"blockSecrets" mapstructure:"blockSecrets"`                 // Fail on detected secrets
	RequireTests         bool    `json:"requireTests" mapstructure:"requireTests"`                 // Warn if no tests cover changes
	MaxRiskScore         float64 `json:"maxRiskScore" mapstructure:"maxRiskScore"`                 // Maximum risk score (0 = disabled)
	MaxComplexityDelta   int     `json:"maxComplexityDelta" mapstructure:"maxComplexityDelta"`     // Maximum complexity delta (0 = disabled)
	MaxFiles             int     `json:"maxFiles" mapstructure:"maxFiles"`                         // Maximum file count (0 = disabled)
	FailOnLevel          string  `json:"failOnLevel" mapstructure:"failOnLevel"`                   // error, warning, none

	// Generated file detection
	GeneratedPatterns []string `json:"generatedPatterns" mapstructure:"generatedPatterns"` // Glob patterns for generated files
	GeneratedMarkers  []string `json:"generatedMarkers" mapstructure:"generatedMarkers"`   // Comment markers (e.g., "DO NOT EDIT")

	// Safety-critical paths
	CriticalPaths []string `json:"criticalPaths" mapstructure:"criticalPaths"` // Glob patterns requiring extra scrutiny

	// Traceability (commit-to-ticket linkage)
	TraceabilityPatterns         []string `json:"traceabilityPatterns" mapstructure:"traceabilityPatterns"`                 // Regex: ["JIRA-\\d+", "#\\d+"]
	TraceabilitySources          []string `json:"traceabilitySources" mapstructure:"traceabilitySources"`                   // Where to look: commit-message, branch-name
	RequireTraceability          bool     `json:"requireTraceability" mapstructure:"requireTraceability"`                   // Enforce ticket references
	RequireTraceForCriticalPaths bool     `json:"requireTraceForCriticalPaths" mapstructure:"requireTraceForCriticalPaths"` // Enforce for critical paths only

	// Reviewer independence
	RequireIndependentReview bool `json:"requireIndependentReview" mapstructure:"requireIndependentReview"` // Author != reviewer
	MinReviewers             int  `json:"minReviewers" mapstructure:"minReviewers"`                         // Minimum reviewer count

	// Analyzer thresholds (v8.2)
	MaxBlastRadiusDelta   int     `json:"maxBlastRadiusDelta" mapstructure:"maxBlastRadiusDelta"`     // 0 = disabled
	MaxFanOut             int     `json:"maxFanOut" mapstructure:"maxFanOut"`                         // 0 = disabled
	DeadCodeMinConfidence float64 `json:"deadCodeMinConfidence" mapstructure:"deadCodeMinConfidence"` // default 0.8
	TestGapMinLines       int     `json:"testGapMinLines" mapstructure:"testGapMinLines"`             // default 5
}

// LLMConfig contains LLM API configuration for narrative generation (v8.2).
type LLMConfig struct {
	Provider string `json:"provider" mapstructure:"provider"` // "anthropic" (default), "gemini"
	APIKey   string `json:"apiKey" mapstructure:"apiKey"`     // API key (or use ANTHROPIC_API_KEY / GEMINI_API_KEY env)
	Model    string `json:"model" mapstructure:"model"`       // Model ID (provider-specific default if empty)
}

// BackendsConfig contains backend-specific configuration
type BackendsConfig struct {
	Scip ScipConfig `json:"scip" mapstructure:"scip"`
	Lsp  LspConfig  `json:"lsp" mapstructure:"lsp"`
	Git  GitConfig  `json:"git" mapstructure:"git"`
}

// ScipConfig contains SCIP backend configuration
type ScipConfig struct {
	Enabled   bool   `json:"enabled" mapstructure:"enabled"`
	IndexPath string `json:"indexPath" mapstructure:"indexPath"`
}

// LspConfig contains LSP backend configuration
type LspConfig struct {
	Enabled           bool                    `json:"enabled" mapstructure:"enabled"`
	WorkspaceStrategy string                  `json:"workspaceStrategy" mapstructure:"workspaceStrategy"`
	Servers           map[string]LspServerCfg `json:"servers" mapstructure:"servers"`
}

// LspServerCfg contains configuration for a single LSP server
type LspServerCfg struct {
	Command string   `json:"command" mapstructure:"command"`
	Args    []string `json:"args" mapstructure:"args"`
}

// GitConfig contains Git backend configuration
type GitConfig struct {
	Enabled bool `json:"enabled" mapstructure:"enabled"`
}

// QueryPolicyConfig contains query execution policy
type QueryPolicyConfig struct {
	BackendPreferenceOrder []string       `json:"backendPreferenceOrder" mapstructure:"backendPreferenceOrder"`
	AlwaysUse              []string       `json:"alwaysUse" mapstructure:"alwaysUse"`
	MaxInFlightPerBackend  map[string]int `json:"maxInFlightPerBackend" mapstructure:"maxInFlightPerBackend"`
	CoalesceWindowMs       int            `json:"coalesceWindowMs" mapstructure:"coalesceWindowMs"`
	MergeMode              string         `json:"mergeMode" mapstructure:"mergeMode"`
	SupplementThreshold    float64        `json:"supplementThreshold" mapstructure:"supplementThreshold"`
	TimeoutMs              map[string]int `json:"timeoutMs" mapstructure:"timeoutMs"`
}

// LspSupervisorConfig contains LSP supervisor configuration
type LspSupervisorConfig struct {
	MaxTotalProcesses    int `json:"maxTotalProcesses" mapstructure:"maxTotalProcesses"`
	QueueSizePerLanguage int `json:"queueSizePerLanguage" mapstructure:"queueSizePerLanguage"`
	MaxQueueWaitMs       int `json:"maxQueueWaitMs" mapstructure:"maxQueueWaitMs"`
}

// ModulesConfig contains module detection configuration
type ModulesConfig struct {
	Detection string   `json:"detection" mapstructure:"detection"`
	Roots     []string `json:"roots" mapstructure:"roots"`
	Ignore    []string `json:"ignore" mapstructure:"ignore"`
}

// ImportScanConfig contains import scanning configuration
type ImportScanConfig struct {
	Enabled          bool                   `json:"enabled" mapstructure:"enabled"`
	MaxFileSizeBytes int                    `json:"maxFileSizeBytes" mapstructure:"maxFileSizeBytes"`
	ScanTimeoutMs    int                    `json:"scanTimeoutMs" mapstructure:"scanTimeoutMs"`
	SkipBinary       bool                   `json:"skipBinary" mapstructure:"skipBinary"`
	CustomPatterns   map[string]interface{} `json:"customPatterns" mapstructure:"customPatterns"`
}

// CacheConfig contains cache configuration
type CacheConfig struct {
	QueryTtlSeconds    int `json:"queryTtlSeconds" mapstructure:"queryTtlSeconds"`
	ViewTtlSeconds     int `json:"viewTtlSeconds" mapstructure:"viewTtlSeconds"`
	NegativeTtlSeconds int `json:"negativeTtlSeconds" mapstructure:"negativeTtlSeconds"`
}

// BudgetConfig contains response budget configuration
type BudgetConfig struct {
	MaxModules          int `json:"maxModules" mapstructure:"maxModules"`
	MaxSymbolsPerModule int `json:"maxSymbolsPerModule" mapstructure:"maxSymbolsPerModule"`
	MaxImpactItems      int `json:"maxImpactItems" mapstructure:"maxImpactItems"`
	MaxDrilldowns       int `json:"maxDrilldowns" mapstructure:"maxDrilldowns"`
	EstimatedMaxTokens  int `json:"estimatedMaxTokens" mapstructure:"estimatedMaxTokens"`
}

// BackendLimitsConfig contains backend limits
type BackendLimitsConfig struct {
	MaxRefsPerQuery    int `json:"maxRefsPerQuery" mapstructure:"maxRefsPerQuery"`
	MaxFilesScanned    int `json:"maxFilesScanned" mapstructure:"maxFilesScanned"`
	MaxUnionModeTimeMs int `json:"maxUnionModeTimeMs" mapstructure:"maxUnionModeTimeMs"`
}

// PrivacyConfig contains privacy settings
type PrivacyConfig struct {
	Mode string `json:"mode" mapstructure:"mode"`
}

// LoggingConfig contains logging configuration
type LoggingConfig struct {
	Format     string           `json:"format" mapstructure:"format"`
	Level      string           `json:"level" mapstructure:"level"`
	MCP        string           `json:"mcp,omitempty" mapstructure:"mcp"`               // per-subsystem override
	API        string           `json:"api,omitempty" mapstructure:"api"`               // per-subsystem override
	Index      string           `json:"index,omitempty" mapstructure:"index"`           // per-subsystem override
	MaxSize    string           `json:"maxSize,omitempty" mapstructure:"maxSize"`       // e.g., "10MB"
	MaxBackups int              `json:"maxBackups,omitempty" mapstructure:"maxBackups"` // rotated file count
	Remote     *RemoteLogConfig `json:"remote,omitempty" mapstructure:"remote"`         // remote aggregator
}

// RemoteLogConfig contains remote log aggregation settings (e.g., Loki)
type RemoteLogConfig struct {
	Type          string            `json:"type" mapstructure:"type"`               // "loki"
	Endpoint      string            `json:"endpoint" mapstructure:"endpoint"`       // e.g., "http://localhost:3100"
	Labels        map[string]string `json:"labels,omitempty" mapstructure:"labels"` // static labels
	BatchSize     int               `json:"batchSize,omitempty" mapstructure:"batchSize"`
	FlushInterval string            `json:"flushInterval,omitempty" mapstructure:"flushInterval"` // e.g., "5s"
}

// DaemonConfig contains daemon mode configuration (v6.2.1)
type DaemonConfig struct {
	Port     int                  `json:"port" mapstructure:"port"`
	Bind     string               `json:"bind" mapstructure:"bind"`
	LogLevel string               `json:"logLevel" mapstructure:"logLevel"`
	LogFile  string               `json:"logFile" mapstructure:"logFile"`
	Auth     DaemonAuthConfig     `json:"auth" mapstructure:"auth"`
	Watch    DaemonWatchConfig    `json:"watch" mapstructure:"watch"`
	Schedule DaemonScheduleConfig `json:"schedule" mapstructure:"schedule"`
}

// DaemonAuthConfig contains daemon authentication configuration
type DaemonAuthConfig struct {
	Enabled   bool   `json:"enabled" mapstructure:"enabled"`
	Token     string `json:"token" mapstructure:"token"`
	TokenFile string `json:"tokenFile" mapstructure:"tokenFile"`
}

// DaemonWatchConfig contains file watching configuration
type DaemonWatchConfig struct {
	Enabled        bool     `json:"enabled" mapstructure:"enabled"`
	DebounceMs     int      `json:"debounceMs" mapstructure:"debounceMs"`
	IgnorePatterns []string `json:"ignorePatterns" mapstructure:"ignorePatterns"`
	Repos          []string `json:"repos" mapstructure:"repos"`
}

// DaemonScheduleConfig contains scheduler configuration
type DaemonScheduleConfig struct {
	Refresh         string `json:"refresh" mapstructure:"refresh"`
	FederationSync  string `json:"federationSync" mapstructure:"federationSync"`
	HotspotSnapshot string `json:"hotspotSnapshot" mapstructure:"hotspotSnapshot"`
}

// WebhookConfig contains webhook configuration (v6.2.1)
type WebhookConfig struct {
	ID      string            `json:"id" mapstructure:"id"`
	URL     string            `json:"url" mapstructure:"url"`
	Secret  string            `json:"secret" mapstructure:"secret"`
	Events  []string          `json:"events" mapstructure:"events"`
	Format  string            `json:"format" mapstructure:"format"`
	Headers map[string]string `json:"headers" mapstructure:"headers"`
}

// TelemetryConfig contains runtime telemetry configuration (v6.4)
type TelemetryConfig struct {
	Enabled         bool                       `json:"enabled" mapstructure:"enabled"`
	ServiceMap      map[string]string          `json:"serviceMap" mapstructure:"serviceMap"`           // service name -> repo ID
	ServicePatterns []TelemetryServicePattern  `json:"servicePatterns" mapstructure:"servicePatterns"` // regex patterns
	Aggregation     TelemetryAggregationConfig `json:"aggregation" mapstructure:"aggregation"`
	DeadCode        TelemetryDeadCodeConfig    `json:"deadCode" mapstructure:"deadCode"`
	Privacy         TelemetryPrivacyConfig     `json:"privacy" mapstructure:"privacy"`
	Attributes      TelemetryAttributesConfig  `json:"attributes" mapstructure:"attributes"`
}

// TelemetryServicePattern contains regex pattern for service mapping
type TelemetryServicePattern struct {
	Pattern string `json:"pattern" mapstructure:"pattern"` // regex pattern
	Repo    string `json:"repo" mapstructure:"repo"`       // replacement (can use $1, $2, etc.)
}

// TelemetryAggregationConfig contains aggregation settings
type TelemetryAggregationConfig struct {
	BucketSize          string `json:"bucketSize" mapstructure:"bucketSize"` // "daily" | "weekly" | "monthly"
	RetentionDays       int    `json:"retentionDays" mapstructure:"retentionDays"`
	MinCallsToStore     int    `json:"minCallsToStore" mapstructure:"minCallsToStore"`
	StoreCallers        bool   `json:"storeCallers" mapstructure:"storeCallers"`
	MaxCallersPerSymbol int    `json:"maxCallersPerSymbol" mapstructure:"maxCallersPerSymbol"`
}

// TelemetryDeadCodeConfig contains dead code detection settings
type TelemetryDeadCodeConfig struct {
	Enabled            bool     `json:"enabled" mapstructure:"enabled"`
	MinObservationDays int      `json:"minObservationDays" mapstructure:"minObservationDays"`
	ExcludePatterns    []string `json:"excludePatterns" mapstructure:"excludePatterns"`   // path patterns
	ExcludeFunctions   []string `json:"excludeFunctions" mapstructure:"excludeFunctions"` // function name patterns
}

// TelemetryPrivacyConfig contains privacy settings
type TelemetryPrivacyConfig struct {
	RedactCallerNames  bool `json:"redactCallerNames" mapstructure:"redactCallerNames"`
	LogUnmatchedEvents bool `json:"logUnmatchedEvents" mapstructure:"logUnmatchedEvents"`
}

// TelemetryAttributesConfig contains attribute key mappings for OTEL compatibility
type TelemetryAttributesConfig struct {
	FunctionKeys  []string `json:"functionKeys" mapstructure:"functionKeys"`
	NamespaceKeys []string `json:"namespaceKeys" mapstructure:"namespaceKeys"`
	FileKeys      []string `json:"fileKeys" mapstructure:"fileKeys"`
	LineKeys      []string `json:"lineKeys" mapstructure:"lineKeys"`
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Version:  5,
		RepoRoot: ".",
		Backends: BackendsConfig{
			Scip: ScipConfig{
				Enabled:   true,
				IndexPath: "index.scip",
			},
			Lsp: LspConfig{
				Enabled:           true,
				WorkspaceStrategy: "repo-root",
				Servers: map[string]LspServerCfg{
					"typescript": {
						Command: "typescript-language-server",
						Args:    []string{"--stdio"},
					},
					"dart": {
						Command: "dart",
						Args:    []string{"language-server"},
					},
					"go": {
						Command: "gopls",
						Args:    []string{},
					},
					"python": {
						Command: "pylsp",
						Args:    []string{},
					},
				},
			},
			Git: GitConfig{
				Enabled: true,
			},
		},
		QueryPolicy: QueryPolicyConfig{
			BackendPreferenceOrder: []string{"scip", "glean", "lsp"},
			AlwaysUse:              []string{"git"},
			MaxInFlightPerBackend: map[string]int{
				"scip": 10,
				"lsp":  3,
				"git":  5,
			},
			CoalesceWindowMs:    50,
			MergeMode:           "prefer-first",
			SupplementThreshold: 0.8,
			TimeoutMs: map[string]int{
				"scip": 5000,
				"lsp":  15000,
				"git":  5000,
			},
		},
		LspSupervisor: LspSupervisorConfig{
			MaxTotalProcesses:    4,
			QueueSizePerLanguage: 10,
			MaxQueueWaitMs:       200,
		},
		Modules: ModulesConfig{
			Detection: "auto",
			Roots:     []string{},
			Ignore:    []string{"node_modules", "build", ".dart_tool", "vendor"},
		},
		ImportScan: ImportScanConfig{
			Enabled:          true,
			MaxFileSizeBytes: 1000000,
			ScanTimeoutMs:    30000,
			SkipBinary:       true,
			CustomPatterns:   map[string]interface{}{},
		},
		Cache: CacheConfig{
			QueryTtlSeconds:    300,
			ViewTtlSeconds:     3600,
			NegativeTtlSeconds: 60,
		},
		Budget: BudgetConfig{
			MaxModules:          10,
			MaxSymbolsPerModule: 5,
			MaxImpactItems:      20,
			MaxDrilldowns:       5,
			EstimatedMaxTokens:  4000,
		},
		BackendLimits: BackendLimitsConfig{
			MaxRefsPerQuery:    10000,
			MaxFilesScanned:    5000,
			MaxUnionModeTimeMs: 60000,
		},
		Privacy: PrivacyConfig{
			Mode: "normal",
		},
		Logging: LoggingConfig{
			Format: "human",
			Level:  "info",
		},
		Daemon: DaemonConfig{
			Port:     DefaultDaemonPort,
			Bind:     "localhost",
			LogLevel: "info",
			LogFile:  "", // Default: ~/.ckb/daemon/daemon.log
			Auth: DaemonAuthConfig{
				Enabled:   true,
				Token:     "", // Will check CKB_DAEMON_TOKEN env var
				TokenFile: "",
			},
			Watch: DaemonWatchConfig{
				Enabled:        true,
				DebounceMs:     5000,
				IgnorePatterns: []string{"*.log", "node_modules/**", ".git/**", "**/*.tmp"},
				Repos:          []string{}, // Empty = all federated repos
			},
			Schedule: DaemonScheduleConfig{
				Refresh:         "every 4h",
				FederationSync:  "every 1h",
				HotspotSnapshot: "daily",
			},
		},
		Webhooks: []WebhookConfig{},
		Coverage: CoverageConfig{
			Paths:      []string{}, // Empty = use auto-detection only
			AutoDetect: true,
			MaxAge:     "168h", // 7 days
		},
		Review: ReviewConfig{
			BlockBreakingChanges: true,
			BlockSecrets:         true,
			RequireTests:         false,
			MaxRiskScore:         0.7,
			MaxComplexityDelta:   0, // disabled by default
			MaxFiles:             0, // disabled by default
			FailOnLevel:          "error",
			GeneratedPatterns:    []string{},
			GeneratedMarkers:     []string{},
			CriticalPaths:        []string{},
		},
		Telemetry: TelemetryConfig{
			Enabled:         false, // Explicit opt-in required
			ServiceMap:      map[string]string{},
			ServicePatterns: []TelemetryServicePattern{},
			Aggregation: TelemetryAggregationConfig{
				BucketSize:          "weekly",
				RetentionDays:       180,
				MinCallsToStore:     1,
				StoreCallers:        false,
				MaxCallersPerSymbol: 20,
			},
			DeadCode: TelemetryDeadCodeConfig{
				Enabled:            true,
				MinObservationDays: 90,
				ExcludePatterns: []string{
					"**/test/**",
					"**/testdata/**",
					"**/*_test.go",
					"**/*.test.ts",
					"**/migrations/**",
					"**/scripts/**",
				},
				ExcludeFunctions: []string{
					"*Migration*",
					"*Backup*",
					"*Recover*",
					"*Cleanup*",
					"*Scheduled*",
					"*Cron*",
				},
			},
			Privacy: TelemetryPrivacyConfig{
				RedactCallerNames:  false,
				LogUnmatchedEvents: true,
			},
			Attributes: TelemetryAttributesConfig{
				FunctionKeys:  []string{"code.function", "code.function.name", "faas.name", "span.name"},
				NamespaceKeys: []string{"code.namespace", "code.module", "package.name"},
				FileKeys:      []string{"code.filepath", "code.filename", "source.file"},
				LineKeys:      []string{"code.lineno", "code.line_number"},
			},
		},
		Activity: ActivityConfig{
			Enabled:       true,
			RetentionDays: 30,
			StoreParams:   true,
		},
	}
}

// LoadConfig loads configuration from .ckb/config.json
// For more detailed loading info (env overrides, config path), use LoadConfigWithDetails
func LoadConfig(repoRoot string) (*Config, error) {
	result, err := LoadConfigWithDetails(repoRoot)
	if err != nil {
		return nil, err
	}
	return result.Config, nil
}

// SaveConfig saves configuration to .ckb/config.json
func SaveConfig(repoRoot string, cfg *Config) error {
	configPath := filepath.Join(repoRoot, ".ckb", "config.json")

	// Ensure .ckb directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal config to JSON with indentation
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// LoadConfigWithDetails loads configuration and returns detailed info about how it was loaded
func LoadConfigWithDetails(repoRoot string) (*LoadResult, error) {
	result := &LoadResult{}

	// Check for CKB_CONFIG_PATH override
	configPath := os.Getenv("CKB_CONFIG_PATH")
	if configPath != "" {
		// Load from specified path
		cfg, err := loadConfigFromPath(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load config from CKB_CONFIG_PATH=%s: %w", configPath, err)
		}
		result.Config = cfg
		result.ConfigPath = configPath
	} else {
		// Load from default location
		v := viper.New()

		// Set defaults
		v.SetDefault("version", 5)
		v.SetDefault("repoRoot", ".")

		// Configure viper
		v.SetConfigName("config")
		v.SetConfigType("json")
		v.AddConfigPath(filepath.Join(repoRoot, ".ckb"))

		// Read config file
		if err := v.ReadInConfig(); err != nil {
			// If config doesn't exist, use default config
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				result.Config = DefaultConfig()
				result.UsedDefaults = true
			} else {
				return nil, err
			}
		} else {
			// Unmarshal into config struct
			var cfg Config
			if err := v.Unmarshal(&cfg); err != nil {
				return nil, err
			}
			result.Config = &cfg
			result.ConfigPath = v.ConfigFileUsed()
		}
	}

	// Apply environment variable overrides
	result.EnvOverrides = applyEnvOverrides(result.Config)

	return result, nil
}

// loadConfigFromPath loads a config file from a specific path
func loadConfigFromPath(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G703 -- path is internally constructed
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid JSON in config file: %w", err)
	}

	return &cfg, nil
}

// envVarMapping defines the supported environment variable overrides
// Format: env var name -> (config path, type)
type envVarDef struct {
	path    string
	varType string // "string", "int", "bool"
}

var envVarMappings = map[string]envVarDef{
	// Logging (support both CKB_LOG_* and CKB_LOGGING_* for compatibility)
	"CKB_LOG_LEVEL":           {path: "logging.level", varType: "string"},
	"CKB_LOG_FORMAT":          {path: "logging.format", varType: "string"},
	"CKB_LOGGING_LEVEL":       {path: "logging.level", varType: "string"},
	"CKB_LOGGING_FORMAT":      {path: "logging.format", varType: "string"},
	"CKB_LOGGING_MCP":         {path: "logging.mcp", varType: "string"},
	"CKB_LOGGING_API":         {path: "logging.api", varType: "string"},
	"CKB_LOGGING_INDEX":       {path: "logging.index", varType: "string"},
	"CKB_LOGGING_MAX_SIZE":    {path: "logging.maxSize", varType: "string"},
	"CKB_LOGGING_MAX_BACKUPS": {path: "logging.maxBackups", varType: "int"},

	// Tier
	"CKB_TIER": {path: "tier", varType: "string"},

	// Cache
	"CKB_CACHE_QUERY_TTL_SECONDS":    {path: "cache.queryTtlSeconds", varType: "int"},
	"CKB_CACHE_VIEW_TTL_SECONDS":     {path: "cache.viewTtlSeconds", varType: "int"},
	"CKB_CACHE_NEGATIVE_TTL_SECONDS": {path: "cache.negativeTtlSeconds", varType: "int"},

	// Budget
	"CKB_BUDGET_MAX_MODULES":            {path: "budget.maxModules", varType: "int"},
	"CKB_BUDGET_MAX_SYMBOLS_PER_MODULE": {path: "budget.maxSymbolsPerModule", varType: "int"},
	"CKB_BUDGET_MAX_IMPACT_ITEMS":       {path: "budget.maxImpactItems", varType: "int"},
	"CKB_BUDGET_MAX_DRILLDOWNS":         {path: "budget.maxDrilldowns", varType: "int"},
	"CKB_BUDGET_ESTIMATED_MAX_TOKENS":   {path: "budget.estimatedMaxTokens", varType: "int"},

	// Backends
	"CKB_BACKENDS_SCIP_ENABLED": {path: "backends.scip.enabled", varType: "bool"},
	"CKB_BACKENDS_LSP_ENABLED":  {path: "backends.lsp.enabled", varType: "bool"},
	"CKB_BACKENDS_GIT_ENABLED":  {path: "backends.git.enabled", varType: "bool"},

	// Telemetry
	"CKB_TELEMETRY_ENABLED": {path: "telemetry.enabled", varType: "bool"},

	// Daemon
	"CKB_DAEMON_PORT": {path: "daemon.port", varType: "int"},
	"CKB_DAEMON_BIND": {path: "daemon.bind", varType: "string"},

	// Privacy
	"CKB_PRIVACY_MODE": {path: "privacy.mode", varType: "string"},
}

// applyEnvOverrides applies environment variable overrides to the config
func applyEnvOverrides(cfg *Config) []EnvOverride {
	var overrides []EnvOverride

	for envVar, def := range envVarMappings {
		value := os.Getenv(envVar)
		if value == "" {
			continue
		}

		var parsedValue interface{}
		var err error

		switch def.varType {
		case "string":
			parsedValue = value
		case "int":
			parsedValue, err = strconv.Atoi(value)
			if err != nil {
				// Skip invalid int values silently (could log warning)
				continue
			}
		case "bool":
			parsedValue, err = strconv.ParseBool(value)
			if err != nil {
				// Skip invalid bool values silently
				continue
			}
		}

		// Apply the override
		if applyOverride(cfg, def.path, parsedValue) {
			overrides = append(overrides, EnvOverride{
				EnvVar:    envVar,
				Path:      def.path,
				Value:     parsedValue,
				FromValue: value,
			})
		}
	}

	return overrides
}

// applyOverride applies a single override to the config struct
func applyOverride(cfg *Config, path string, value interface{}) bool {
	parts := strings.Split(path, ".")

	switch parts[0] {
	case "tier":
		if v, ok := value.(string); ok {
			cfg.Tier = v
			return true
		}
	case "logging":
		if len(parts) < 2 {
			return false
		}
		switch parts[1] {
		case "level":
			if v, ok := value.(string); ok {
				cfg.Logging.Level = v
				return true
			}
		case "format":
			if v, ok := value.(string); ok {
				cfg.Logging.Format = v
				return true
			}
		case "mcp":
			if v, ok := value.(string); ok {
				cfg.Logging.MCP = v
				return true
			}
		case "api":
			if v, ok := value.(string); ok {
				cfg.Logging.API = v
				return true
			}
		case "index":
			if v, ok := value.(string); ok {
				cfg.Logging.Index = v
				return true
			}
		case "maxSize":
			if v, ok := value.(string); ok {
				cfg.Logging.MaxSize = v
				return true
			}
		case "maxBackups":
			if v, ok := value.(int); ok {
				cfg.Logging.MaxBackups = v
				return true
			}
		}
	case "cache":
		if len(parts) < 2 {
			return false
		}
		switch parts[1] {
		case "queryTtlSeconds":
			if v, ok := value.(int); ok {
				cfg.Cache.QueryTtlSeconds = v
				return true
			}
		case "viewTtlSeconds":
			if v, ok := value.(int); ok {
				cfg.Cache.ViewTtlSeconds = v
				return true
			}
		case "negativeTtlSeconds":
			if v, ok := value.(int); ok {
				cfg.Cache.NegativeTtlSeconds = v
				return true
			}
		}
	case "budget":
		if len(parts) < 2 {
			return false
		}
		switch parts[1] {
		case "maxModules":
			if v, ok := value.(int); ok {
				cfg.Budget.MaxModules = v
				return true
			}
		case "maxSymbolsPerModule":
			if v, ok := value.(int); ok {
				cfg.Budget.MaxSymbolsPerModule = v
				return true
			}
		case "maxImpactItems":
			if v, ok := value.(int); ok {
				cfg.Budget.MaxImpactItems = v
				return true
			}
		case "maxDrilldowns":
			if v, ok := value.(int); ok {
				cfg.Budget.MaxDrilldowns = v
				return true
			}
		case "estimatedMaxTokens":
			if v, ok := value.(int); ok {
				cfg.Budget.EstimatedMaxTokens = v
				return true
			}
		}
	case "backends":
		if len(parts) < 3 {
			return false
		}
		switch parts[1] {
		case "scip":
			if parts[2] == "enabled" {
				if v, ok := value.(bool); ok {
					cfg.Backends.Scip.Enabled = v
					return true
				}
			}
		case "lsp":
			if parts[2] == "enabled" {
				if v, ok := value.(bool); ok {
					cfg.Backends.Lsp.Enabled = v
					return true
				}
			}
		case "git":
			if parts[2] == "enabled" {
				if v, ok := value.(bool); ok {
					cfg.Backends.Git.Enabled = v
					return true
				}
			}
		}
	case "telemetry":
		if len(parts) < 2 {
			return false
		}
		if parts[1] == "enabled" {
			if v, ok := value.(bool); ok {
				cfg.Telemetry.Enabled = v
				return true
			}
		}
	case "daemon":
		if len(parts) < 2 {
			return false
		}
		switch parts[1] {
		case "port":
			if v, ok := value.(int); ok {
				cfg.Daemon.Port = v
				return true
			}
		case "bind":
			if v, ok := value.(string); ok {
				cfg.Daemon.Bind = v
				return true
			}
		}
	case "privacy":
		if len(parts) < 2 {
			return false
		}
		if parts[1] == "mode" {
			if v, ok := value.(string); ok {
				cfg.Privacy.Mode = v
				return true
			}
		}
	}

	return false
}

// GetSupportedEnvVars returns a list of all supported environment variables
func GetSupportedEnvVars() []string {
	vars := make([]string, 0, len(envVarMappings))
	for v := range envVarMappings {
		vars = append(vars, v)
	}
	return vars
}

// Save writes the configuration to .ckb/config.json
func (c *Config) Save(repoRoot string) error {
	configPath := filepath.Join(repoRoot, ".ckb", "config.json")

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	// Write to file
	return os.WriteFile(configPath, data, 0644)
}

// SupportedConfigVersions lists config schema versions this code can handle
var SupportedConfigVersions = []int{5, 6}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Check version is supported
	supported := false
	for _, v := range SupportedConfigVersions {
		if c.Version == v {
			supported = true
			break
		}
	}
	if !supported {
		return &ConfigError{
			Field:   "version",
			Message: fmt.Sprintf("unsupported config version %d, supported versions: %v", c.Version, SupportedConfigVersions),
		}
	}

	// Add more validation as needed
	return nil
}

// ConfigError represents a configuration error
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return "config error in field '" + e.Field + "': " + e.Message
}
