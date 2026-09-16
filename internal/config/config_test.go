package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Check version
	if cfg.Version != 5 {
		t.Errorf("Version = %d, want 5", cfg.Version)
	}

	// Check backends are enabled
	if !cfg.Backends.Scip.Enabled {
		t.Error("SCIP backend should be enabled by default")
	}
	if !cfg.Backends.Lsp.Enabled {
		t.Error("LSP backend should be enabled by default")
	}
	if !cfg.Backends.Git.Enabled {
		t.Error("Git backend should be enabled by default")
	}

	// Check index path
	if cfg.Backends.Scip.IndexPath != "index.scip" {
		t.Errorf("SCIP IndexPath = %q, want %q", cfg.Backends.Scip.IndexPath, "index.scip")
	}

	// Check LSP servers
	if _, ok := cfg.Backends.Lsp.Servers["go"]; !ok {
		t.Error("LSP servers should include 'go'")
	}
	if _, ok := cfg.Backends.Lsp.Servers["typescript"]; !ok {
		t.Error("LSP servers should include 'typescript'")
	}

	// Check query policy
	if len(cfg.QueryPolicy.BackendPreferenceOrder) == 0 {
		t.Error("BackendPreferenceOrder should not be empty")
	}
	if cfg.QueryPolicy.MergeMode != "prefer-first" {
		t.Errorf("MergeMode = %q, want %q", cfg.QueryPolicy.MergeMode, "prefer-first")
	}

	// Check cache settings
	if cfg.Cache.QueryTtlSeconds <= 0 {
		t.Error("QueryTtlSeconds should be positive")
	}

	// Check budget settings
	if cfg.Budget.MaxModules <= 0 {
		t.Error("MaxModules should be positive")
	}
	if cfg.Budget.EstimatedMaxTokens <= 0 {
		t.Error("EstimatedMaxTokens should be positive")
	}

	// Check daemon defaults
	if cfg.Daemon.Port != 9120 {
		t.Errorf("Daemon.Port = %d, want 9120", cfg.Daemon.Port)
	}
	if cfg.Daemon.Bind != "localhost" {
		t.Errorf("Daemon.Bind = %q, want %q", cfg.Daemon.Bind, "localhost")
	}

	// Check telemetry is disabled by default
	if cfg.Telemetry.Enabled {
		t.Error("Telemetry should be disabled by default")
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		version int
		wantErr bool
	}{
		{"version 5", 5, false},
		{"version 6", 6, false},
		{"version 1 unsupported", 1, true},
		{"version 7 unsupported", 7, true},
		{"version 0 unsupported", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Version = tt.version

			err := cfg.Validate()

			if tt.wantErr && err == nil {
				t.Error("Validate() should return error for unsupported version")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate() returned unexpected error: %v", err)
			}

			// Check error type
			if err != nil {
				if _, ok := err.(*ConfigError); !ok {
					t.Errorf("Validate() error type = %T, want *ConfigError", err)
				}
			}
		})
	}
}

func TestConfigError_Error(t *testing.T) {
	err := &ConfigError{
		Field:   "version",
		Message: "unsupported version 99",
	}

	got := err.Error()
	want := "config error in field 'version': unsupported version 99"

	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLoadConfig_Default(t *testing.T) {
	// Create a temp directory without config
	tmpDir := t.TempDir()

	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Should return default config when no config file exists
	if cfg.Version != 5 {
		t.Errorf("Version = %d, want 5 (default)", cfg.Version)
	}
}

func TestLoadConfig_FromFile(t *testing.T) {
	// Create a temp directory with config
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	configContent := `{
		"version": 5,
		"repoRoot": ".",
		"backends": {
			"scip": {"enabled": true, "indexPath": "custom/index.scip"},
			"lsp": {"enabled": false},
			"git": {"enabled": true}
		},
		"budget": {
			"maxModules": 20,
			"maxSymbolsPerModule": 10
		}
	}`

	configPath := filepath.Join(ckbDir, "config.json")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Check custom values were loaded
	if cfg.Backends.Scip.IndexPath != "custom/index.scip" {
		t.Errorf("Scip.IndexPath = %q, want %q", cfg.Backends.Scip.IndexPath, "custom/index.scip")
	}
	if cfg.Backends.Lsp.Enabled {
		t.Error("LSP should be disabled per config")
	}
	if cfg.Budget.MaxModules != 20 {
		t.Errorf("Budget.MaxModules = %d, want 20", cfg.Budget.MaxModules)
	}
}

func TestConfig_Save(t *testing.T) {
	// Create a temp directory
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	cfg := DefaultConfig()
	cfg.Budget.MaxModules = 42

	err := cfg.Save(tmpDir)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Verify file was created
	configPath := filepath.Join(ckbDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file was not created")
	}

	// Load it back and verify
	loaded, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() after save error = %v", err)
	}

	if loaded.Budget.MaxModules != 42 {
		t.Errorf("Loaded Budget.MaxModules = %d, want 42", loaded.Budget.MaxModules)
	}
}

func TestSupportedConfigVersions(t *testing.T) {
	if len(SupportedConfigVersions) == 0 {
		t.Error("SupportedConfigVersions should not be empty")
	}

	// Check that 5 and 6 are supported
	has5, has6 := false, false
	for _, v := range SupportedConfigVersions {
		if v == 5 {
			has5 = true
		}
		if v == 6 {
			has6 = true
		}
	}

	if !has5 {
		t.Error("SupportedConfigVersions should include 5")
	}
	if !has6 {
		t.Error("SupportedConfigVersions should include 6")
	}
}

func TestBackendsConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Test SCIP config
	if cfg.Backends.Scip.IndexPath == "" {
		t.Error("SCIP IndexPath should not be empty")
	}

	// Test LSP config
	if cfg.Backends.Lsp.WorkspaceStrategy == "" {
		t.Error("LSP WorkspaceStrategy should not be empty")
	}

	// Test LSP server configs
	for name, server := range cfg.Backends.Lsp.Servers {
		if server.Command == "" {
			t.Errorf("LSP server %q has empty command", name)
		}
	}
}

func TestQueryPolicyConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Check timeout settings
	if cfg.QueryPolicy.TimeoutMs == nil {
		t.Error("TimeoutMs should not be nil")
	}

	for backend, timeout := range cfg.QueryPolicy.TimeoutMs {
		if timeout <= 0 {
			t.Errorf("Timeout for %q = %d, should be positive", backend, timeout)
		}
	}

	// Check max in-flight settings
	if cfg.QueryPolicy.MaxInFlightPerBackend == nil {
		t.Error("MaxInFlightPerBackend should not be nil")
	}
}

func TestDaemonConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Check schedule settings
	if cfg.Daemon.Schedule.Refresh == "" {
		t.Error("Schedule.Refresh should not be empty")
	}
	if cfg.Daemon.Schedule.FederationSync == "" {
		t.Error("Schedule.FederationSync should not be empty")
	}

	// Check watch settings
	if cfg.Daemon.Watch.DebounceMs <= 0 {
		t.Error("Watch.DebounceMs should be positive")
	}
	if len(cfg.Daemon.Watch.IgnorePatterns) == 0 {
		t.Error("Watch.IgnorePatterns should have defaults")
	}
}

func TestTelemetryConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Telemetry should be opt-in
	if cfg.Telemetry.Enabled {
		t.Error("Telemetry should be disabled by default")
	}

	// Check aggregation defaults
	if cfg.Telemetry.Aggregation.BucketSize == "" {
		t.Error("Aggregation.BucketSize should have a default")
	}
	if cfg.Telemetry.Aggregation.RetentionDays <= 0 {
		t.Error("Aggregation.RetentionDays should be positive")
	}

	// Check dead code defaults
	if cfg.Telemetry.DeadCode.MinObservationDays <= 0 {
		t.Error("DeadCode.MinObservationDays should be positive")
	}
	if len(cfg.Telemetry.DeadCode.ExcludePatterns) == 0 {
		t.Error("DeadCode.ExcludePatterns should have defaults")
	}

	// Check attribute keys
	if len(cfg.Telemetry.Attributes.FunctionKeys) == 0 {
		t.Error("Attributes.FunctionKeys should have defaults")
	}
}

func TestActivityConfig(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Activity.Enabled {
		t.Error("Activity.Enabled should default to true")
	}
	if cfg.Activity.RetentionDays != 30 {
		t.Errorf("Activity.RetentionDays = %d, want 30", cfg.Activity.RetentionDays)
	}
	if !cfg.Activity.StoreParams {
		t.Error("Activity.StoreParams should default to true")
	}
}

func TestModulesConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Modules.Detection != "auto" {
		t.Errorf("Modules.Detection = %q, want %q", cfg.Modules.Detection, "auto")
	}

	// Check default ignore patterns
	if len(cfg.Modules.Ignore) == 0 {
		t.Error("Modules.Ignore should have defaults")
	}
}

func TestApplyEnvOverrides(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		validate func(t *testing.T, cfg *Config, overrides []EnvOverride)
	}{
		{
			name: "logging level override",
			envVars: map[string]string{
				"CKB_LOG_LEVEL": "debug",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Logging.Level != "debug" {
					t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "debug")
				}
				if len(overrides) != 1 {
					t.Errorf("len(overrides) = %d, want 1", len(overrides))
				}
			},
		},
		{
			name: "budget int override",
			envVars: map[string]string{
				"CKB_BUDGET_MAX_MODULES": "50",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Budget.MaxModules != 50 {
					t.Errorf("Budget.MaxModules = %d, want 50", cfg.Budget.MaxModules)
				}
			},
		},
		{
			name: "backend bool override",
			envVars: map[string]string{
				"CKB_BACKENDS_SCIP_ENABLED": "false",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Backends.Scip.Enabled {
					t.Error("Backends.Scip.Enabled should be false")
				}
			},
		},
		{
			name: "multiple overrides",
			envVars: map[string]string{
				"CKB_LOG_LEVEL":          "warn",
				"CKB_BUDGET_MAX_MODULES": "100",
				"CKB_TELEMETRY_ENABLED":  "true",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Logging.Level != "warn" {
					t.Errorf("Logging.Level = %q, want %q", cfg.Logging.Level, "warn")
				}
				if cfg.Budget.MaxModules != 100 {
					t.Errorf("Budget.MaxModules = %d, want 100", cfg.Budget.MaxModules)
				}
				if !cfg.Telemetry.Enabled {
					t.Error("Telemetry.Enabled should be true")
				}
				if len(overrides) != 3 {
					t.Errorf("len(overrides) = %d, want 3", len(overrides))
				}
			},
		},
		{
			name: "invalid int ignored",
			envVars: map[string]string{
				"CKB_BUDGET_MAX_MODULES": "not-a-number",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				// Should keep default value
				if cfg.Budget.MaxModules != 10 {
					t.Errorf("Budget.MaxModules = %d, want 10 (default)", cfg.Budget.MaxModules)
				}
				if len(overrides) != 0 {
					t.Errorf("len(overrides) = %d, want 0 (invalid value should be skipped)", len(overrides))
				}
			},
		},
		{
			name: "tier override",
			envVars: map[string]string{
				"CKB_TIER": "fast",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Tier != "fast" {
					t.Errorf("Tier = %q, want %q", cfg.Tier, "fast")
				}
			},
		},
		{
			name: "daemon port override",
			envVars: map[string]string{
				"CKB_DAEMON_PORT": "8080",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Daemon.Port != 8080 {
					t.Errorf("Daemon.Port = %d, want 8080", cfg.Daemon.Port)
				}
			},
		},
		{
			name: "cache ttl overrides",
			envVars: map[string]string{
				"CKB_CACHE_QUERY_TTL_SECONDS": "600",
				"CKB_CACHE_VIEW_TTL_SECONDS":  "7200",
			},
			validate: func(t *testing.T, cfg *Config, overrides []EnvOverride) {
				if cfg.Cache.QueryTtlSeconds != 600 {
					t.Errorf("Cache.QueryTtlSeconds = %d, want 600", cfg.Cache.QueryTtlSeconds)
				}
				if cfg.Cache.ViewTtlSeconds != 7200 {
					t.Errorf("Cache.ViewTtlSeconds = %d, want 7200", cfg.Cache.ViewTtlSeconds)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear any existing env vars
			for envVar := range envVarMappings {
				os.Unsetenv(envVar)
			}

			// Set test env vars
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}
			defer func() {
				for k := range tt.envVars {
					os.Unsetenv(k)
				}
			}()

			cfg := DefaultConfig()
			overrides := applyEnvOverrides(cfg)

			tt.validate(t, cfg, overrides)
		})
	}
}

func TestLoadConfigWithDetails(t *testing.T) {
	// Create a temp directory without config
	tmpDir := t.TempDir()

	// Clear env vars
	os.Unsetenv("CKB_CONFIG_PATH")
	os.Unsetenv("CKB_LOG_LEVEL")

	result, err := LoadConfigWithDetails(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfigWithDetails() error = %v", err)
	}

	if !result.UsedDefaults {
		t.Error("UsedDefaults should be true when no config file exists")
	}

	if result.ConfigPath != "" {
		t.Errorf("ConfigPath = %q, want empty string", result.ConfigPath)
	}
}

func TestLoadConfigWithDetails_EnvConfigPath(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "custom-config.json")
	configContent := `{
		"version": 5,
		"budget": {"maxModules": 99}
	}`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Set CKB_CONFIG_PATH
	os.Setenv("CKB_CONFIG_PATH", configPath)
	defer os.Unsetenv("CKB_CONFIG_PATH")

	result, err := LoadConfigWithDetails(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfigWithDetails() error = %v", err)
	}

	if result.ConfigPath != configPath {
		t.Errorf("ConfigPath = %q, want %q", result.ConfigPath, configPath)
	}

	if result.Config.Budget.MaxModules != 99 {
		t.Errorf("Budget.MaxModules = %d, want 99", result.Config.Budget.MaxModules)
	}
}

func TestLoadConfigWithDetails_EnvOverridesApplied(t *testing.T) {
	tmpDir := t.TempDir()

	// Set env vars
	os.Setenv("CKB_BUDGET_MAX_MODULES", "42")
	os.Setenv("CKB_LOG_LEVEL", "error")
	defer func() {
		os.Unsetenv("CKB_BUDGET_MAX_MODULES")
		os.Unsetenv("CKB_LOG_LEVEL")
	}()

	result, err := LoadConfigWithDetails(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfigWithDetails() error = %v", err)
	}

	// Check overrides were applied
	if result.Config.Budget.MaxModules != 42 {
		t.Errorf("Budget.MaxModules = %d, want 42", result.Config.Budget.MaxModules)
	}
	if result.Config.Logging.Level != "error" {
		t.Errorf("Logging.Level = %q, want %q", result.Config.Logging.Level, "error")
	}

	// Check overrides are recorded
	if len(result.EnvOverrides) != 2 {
		t.Errorf("len(EnvOverrides) = %d, want 2", len(result.EnvOverrides))
	}
}

func TestGetSupportedEnvVars(t *testing.T) {
	vars := GetSupportedEnvVars()

	if len(vars) == 0 {
		t.Error("GetSupportedEnvVars() should return non-empty list")
	}

	// Check some expected vars are present
	hasLogLevel := false
	hasBudgetMaxModules := false
	for _, v := range vars {
		if v == "CKB_LOG_LEVEL" || v == "CKB_LOGGING_LEVEL" {
			hasLogLevel = true
		}
		if v == "CKB_BUDGET_MAX_MODULES" {
			hasBudgetMaxModules = true
		}
	}

	if !hasLogLevel {
		t.Error("GetSupportedEnvVars() should include CKB_LOG_LEVEL or CKB_LOGGING_LEVEL")
	}
	if !hasBudgetMaxModules {
		t.Error("GetSupportedEnvVars() should include CKB_BUDGET_MAX_MODULES")
	}
}

func TestApplyOverride_AllPaths(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		value    interface{}
		validate func(t *testing.T, cfg *Config) bool
	}{
		// Tier
		{"tier", "tier", "fast", func(t *testing.T, cfg *Config) bool { return cfg.Tier == "fast" }},
		// Logging
		{"logging.level", "logging.level", "debug", func(t *testing.T, cfg *Config) bool { return cfg.Logging.Level == "debug" }},
		{"logging.format", "logging.format", "json", func(t *testing.T, cfg *Config) bool { return cfg.Logging.Format == "json" }},
		// Cache
		{"cache.queryTtlSeconds", "cache.queryTtlSeconds", 600, func(t *testing.T, cfg *Config) bool { return cfg.Cache.QueryTtlSeconds == 600 }},
		{"cache.viewTtlSeconds", "cache.viewTtlSeconds", 7200, func(t *testing.T, cfg *Config) bool { return cfg.Cache.ViewTtlSeconds == 7200 }},
		{"cache.negativeTtlSeconds", "cache.negativeTtlSeconds", 120, func(t *testing.T, cfg *Config) bool { return cfg.Cache.NegativeTtlSeconds == 120 }},
		// Budget
		{"budget.maxModules", "budget.maxModules", 50, func(t *testing.T, cfg *Config) bool { return cfg.Budget.MaxModules == 50 }},
		{"budget.maxSymbolsPerModule", "budget.maxSymbolsPerModule", 10, func(t *testing.T, cfg *Config) bool { return cfg.Budget.MaxSymbolsPerModule == 10 }},
		{"budget.maxImpactItems", "budget.maxImpactItems", 30, func(t *testing.T, cfg *Config) bool { return cfg.Budget.MaxImpactItems == 30 }},
		{"budget.maxDrilldowns", "budget.maxDrilldowns", 10, func(t *testing.T, cfg *Config) bool { return cfg.Budget.MaxDrilldowns == 10 }},
		{"budget.estimatedMaxTokens", "budget.estimatedMaxTokens", 8000, func(t *testing.T, cfg *Config) bool { return cfg.Budget.EstimatedMaxTokens == 8000 }},
		// Backends
		{"backends.scip.enabled", "backends.scip.enabled", false, func(t *testing.T, cfg *Config) bool { return cfg.Backends.Scip.Enabled == false }},
		{"backends.lsp.enabled", "backends.lsp.enabled", false, func(t *testing.T, cfg *Config) bool { return cfg.Backends.Lsp.Enabled == false }},
		{"backends.git.enabled", "backends.git.enabled", false, func(t *testing.T, cfg *Config) bool { return cfg.Backends.Git.Enabled == false }},
		// Telemetry
		{"telemetry.enabled", "telemetry.enabled", true, func(t *testing.T, cfg *Config) bool { return cfg.Telemetry.Enabled == true }},
		// Daemon
		{"daemon.port", "daemon.port", 8080, func(t *testing.T, cfg *Config) bool { return cfg.Daemon.Port == 8080 }},
		{"daemon.bind", "daemon.bind", "0.0.0.0", func(t *testing.T, cfg *Config) bool { return cfg.Daemon.Bind == "0.0.0.0" }},
		// Privacy
		{"privacy.mode", "privacy.mode", "redacted", func(t *testing.T, cfg *Config) bool { return cfg.Privacy.Mode == "redacted" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			result := applyOverride(cfg, tt.path, tt.value)

			if !result {
				t.Errorf("applyOverride() returned false for path %q", tt.path)
			}

			if !tt.validate(t, cfg) {
				t.Errorf("applyOverride() did not set value correctly for path %q", tt.path)
			}
		})
	}
}

func TestApplyOverride_InvalidPaths(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		value interface{}
	}{
		{"unknown top-level", "unknown", "value"},
		{"invalid logging path", "logging", "value"},
		{"invalid cache path", "cache", 100},
		{"invalid budget path", "budget", 100},
		{"invalid backends path", "backends.unknown.enabled", true},
		{"invalid telemetry path", "telemetry", true},
		{"invalid daemon path", "daemon", 8080},
		{"invalid privacy path", "privacy", "mode"},
		// Wrong types
		{"tier wrong type", "tier", 123},
		{"logging.level wrong type", "logging.level", 123},
		{"logging.format wrong type", "logging.format", 123},
		{"cache.queryTtlSeconds wrong type", "cache.queryTtlSeconds", "string"},
		{"budget.maxModules wrong type", "budget.maxModules", "string"},
		{"backends.scip.enabled wrong type", "backends.scip.enabled", "string"},
		{"telemetry.enabled wrong type", "telemetry.enabled", "string"},
		{"daemon.port wrong type", "daemon.port", "string"},
		{"daemon.bind wrong type", "daemon.bind", 123},
		{"privacy.mode wrong type", "privacy.mode", 123},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			result := applyOverride(cfg, tt.path, tt.value)

			if result {
				t.Errorf("applyOverride() should return false for invalid path %q", tt.path)
			}
		})
	}
}

func TestApplyOverride_PartialPaths(t *testing.T) {
	cfg := DefaultConfig()

	// Test paths with insufficient parts
	tests := []struct {
		path  string
		value interface{}
	}{
		{"logging", "value"},
		{"cache", 100},
		{"budget", 100},
		{"backends.scip", true},
		{"backends", true},
		{"telemetry", true},
		{"daemon", 8080},
		{"privacy", "mode"},
	}

	for _, tt := range tests {
		result := applyOverride(cfg, tt.path, tt.value)
		if result {
			t.Errorf("applyOverride() should return false for incomplete path %q", tt.path)
		}
	}
}

func TestLoadConfigFromPath_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "bad-config.json")

	// Write invalid JSON
	if err := os.WriteFile(configPath, []byte("{ invalid json }"), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	_, err := loadConfigFromPath(configPath)
	if err == nil {
		t.Error("loadConfigFromPath() should return error for invalid JSON")
	}
}

func TestLoadConfigFromPath_NotFound(t *testing.T) {
	_, err := loadConfigFromPath("/nonexistent/path/config.json")
	if err == nil {
		t.Error("loadConfigFromPath() should return error for nonexistent file")
	}
}

func TestLoadConfigWithDetails_InvalidConfigPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Set invalid CKB_CONFIG_PATH
	os.Setenv("CKB_CONFIG_PATH", "/nonexistent/config.json")
	defer os.Unsetenv("CKB_CONFIG_PATH")

	_, err := LoadConfigWithDetails(tmpDir)
	if err == nil {
		t.Error("LoadConfigWithDetails() should return error for nonexistent CKB_CONFIG_PATH")
	}
}

func TestLoadConfigWithDetails_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	// Write invalid JSON
	configPath := filepath.Join(ckbDir, "config.json")
	if err := os.WriteFile(configPath, []byte("{ invalid }"), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Clear CKB_CONFIG_PATH to use default location
	os.Unsetenv("CKB_CONFIG_PATH")

	_, err := LoadConfigWithDetails(tmpDir)
	if err == nil {
		t.Error("LoadConfigWithDetails() should return error for invalid JSON config")
	}
}

func TestApplyEnvOverrides_InvalidBool(t *testing.T) {
	// Clear env vars first
	for envVar := range envVarMappings {
		os.Unsetenv(envVar)
	}

	// Set invalid bool
	os.Setenv("CKB_BACKENDS_SCIP_ENABLED", "not-a-bool")
	defer os.Unsetenv("CKB_BACKENDS_SCIP_ENABLED")

	cfg := DefaultConfig()
	originalValue := cfg.Backends.Scip.Enabled
	overrides := applyEnvOverrides(cfg)

	// Should keep original value
	if cfg.Backends.Scip.Enabled != originalValue {
		t.Error("applyEnvOverrides() should not change value for invalid bool")
	}

	// Should not record override
	for _, ov := range overrides {
		if ov.EnvVar == "CKB_BACKENDS_SCIP_ENABLED" {
			t.Error("applyEnvOverrides() should not record override for invalid bool")
		}
	}
}

func TestApplyEnvOverrides_AllEnvVars(t *testing.T) {
	// Clear all env vars first
	for envVar := range envVarMappings {
		os.Unsetenv(envVar)
	}

	// Set all supported env vars with valid values
	testValues := map[string]string{
		"CKB_LOG_LEVEL":                     "warn",
		"CKB_LOG_FORMAT":                    "json",
		"CKB_LOGGING_LEVEL":                 "error", // Will be overridden by CKB_LOG_LEVEL in iteration order
		"CKB_LOGGING_FORMAT":                "human",
		"CKB_TIER":                          "full",
		"CKB_CACHE_QUERY_TTL_SECONDS":       "500",
		"CKB_CACHE_VIEW_TTL_SECONDS":        "5000",
		"CKB_CACHE_NEGATIVE_TTL_SECONDS":    "100",
		"CKB_BUDGET_MAX_MODULES":            "25",
		"CKB_BUDGET_MAX_SYMBOLS_PER_MODULE": "15",
		"CKB_BUDGET_MAX_IMPACT_ITEMS":       "40",
		"CKB_BUDGET_MAX_DRILLDOWNS":         "8",
		"CKB_BUDGET_ESTIMATED_MAX_TOKENS":   "6000",
		"CKB_BACKENDS_SCIP_ENABLED":         "false",
		"CKB_BACKENDS_LSP_ENABLED":          "false",
		"CKB_BACKENDS_GIT_ENABLED":          "false",
		"CKB_TELEMETRY_ENABLED":             "true",
		"CKB_DAEMON_PORT":                   "9999",
		"CKB_DAEMON_BIND":                   "127.0.0.1",
		"CKB_PRIVACY_MODE":                  "redacted",
	}

	for k, v := range testValues {
		os.Setenv(k, v)
	}
	defer func() {
		for k := range testValues {
			os.Unsetenv(k)
		}
	}()

	cfg := DefaultConfig()
	overrides := applyEnvOverrides(cfg)

	// Should have many overrides
	if len(overrides) < 10 {
		t.Errorf("applyEnvOverrides() returned %d overrides, expected at least 10", len(overrides))
	}

	// Verify some values were applied
	if cfg.Budget.MaxModules != 25 {
		t.Errorf("Budget.MaxModules = %d, want 25", cfg.Budget.MaxModules)
	}
	if cfg.Daemon.Port != 9999 {
		t.Errorf("Daemon.Port = %d, want 9999", cfg.Daemon.Port)
	}
	if cfg.Privacy.Mode != "redacted" {
		t.Errorf("Privacy.Mode = %q, want %q", cfg.Privacy.Mode, "redacted")
	}
}

func TestLoadConfig_WithEnvOverrides(t *testing.T) {
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	// Write a config file
	configContent := `{"version": 5, "budget": {"maxModules": 10}}`
	if err := os.WriteFile(filepath.Join(ckbDir, "config.json"), []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Clear env
	os.Unsetenv("CKB_CONFIG_PATH")
	os.Setenv("CKB_BUDGET_MAX_MODULES", "99")
	defer os.Unsetenv("CKB_BUDGET_MAX_MODULES")

	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	// Should have env override applied
	if cfg.Budget.MaxModules != 99 {
		t.Errorf("Budget.MaxModules = %d, want 99 (from env override)", cfg.Budget.MaxModules)
	}
}

func TestSave_ErrorHandling(t *testing.T) {
	cfg := DefaultConfig()

	// Try to save to a directory that doesn't exist
	// This should fail since the .ckb directory won't exist
	err := cfg.Save("/nonexistent/directory")
	if err == nil {
		t.Error("Save() should return error when directory doesn't exist")
	}
}

func TestLoadConfig_ErrorHandling(t *testing.T) {
	// Set CKB_CONFIG_PATH to an invalid file
	os.Setenv("CKB_CONFIG_PATH", "/nonexistent/config.json")
	defer os.Unsetenv("CKB_CONFIG_PATH")

	_, err := LoadConfig("/tmp")
	if err == nil {
		t.Error("LoadConfig() should return error for invalid CKB_CONFIG_PATH")
	}
}

func TestLoadConfigWithDetails_FromStandardLocation(t *testing.T) {
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatalf("Failed to create .ckb dir: %v", err)
	}

	// Write a config file
	configContent := `{"version": 5, "tier": "fast"}`
	if err := os.WriteFile(filepath.Join(ckbDir, "config.json"), []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Clear CKB_CONFIG_PATH
	os.Unsetenv("CKB_CONFIG_PATH")

	result, err := LoadConfigWithDetails(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfigWithDetails() error = %v", err)
	}

	if result.UsedDefaults {
		t.Error("UsedDefaults should be false when config file exists")
	}

	if result.ConfigPath == "" {
		t.Error("ConfigPath should be set when config file exists")
	}

	if result.Config.Tier != "fast" {
		t.Errorf("Tier = %q, want %q", result.Config.Tier, "fast")
	}
}

// A config.json written before the "activity" key existed must keep the
// activity defaults (enabled, 30 days, params stored), not the zero values.
func TestLoadConfig_MissingKeysKeepDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	ckbDir := filepath.Join(tmpDir, ".ckb")
	if err := os.MkdirAll(ckbDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ckbDir, "config.json"), []byte(`{"version": 5, "repoRoot": "."}`), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpDir)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Activity.Enabled || cfg.Activity.RetentionDays != 30 || !cfg.Activity.StoreParams {
		t.Errorf("Activity = %+v, want defaults {true 30 true}", cfg.Activity)
	}
	if !cfg.Backends.Scip.Enabled {
		t.Error("Backends.Scip.Enabled should keep its default (true) when the key is absent")
	}

	// Explicit values still win.
	if err := os.WriteFile(filepath.Join(ckbDir, "config.json"), []byte(`{"version": 5, "activity": {"enabled": false, "retentionDays": 7}}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Activity.Enabled || cfg.Activity.RetentionDays != 7 {
		t.Errorf("Activity = %+v, want {false 7 ...}", cfg.Activity)
	}
}
