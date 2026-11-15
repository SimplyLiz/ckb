package tier

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ToolStatus represents the status of a single tool check.
type ToolStatus struct {
	Name         string   `json:"name"`
	Found        bool     `json:"found"`
	Path         string   `json:"path,omitempty"`
	Version      string   `json:"version,omitempty"`
	MinVersion   string   `json:"minVersion,omitempty"`
	VersionOK    bool     `json:"versionOk"`
	Error        string   `json:"error,omitempty"`
	InstallCmd   string   `json:"installCmd,omitempty"`
	Provider     string   `json:"provider,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// LanguageToolStatus represents the tool status for a language.
type LanguageToolStatus struct {
	Language     Language        `json:"language"`
	DisplayName  string          `json:"displayName"`
	ToolTier     AnalysisTier    `json:"toolTier"`    // Based on installed tools
	RuntimeTier  AnalysisTier    `json:"runtimeTier"` // After attempting LSP/runtime checks
	Tools        []ToolStatus    `json:"tools"`
	Missing      []ToolStatus    `json:"missing,omitempty"`
	Capabilities map[string]bool `json:"capabilities"`
}

// ToolDetector detects installed tools for tier validation.
type ToolDetector struct {
	runner  ExecRunner
	timeout time.Duration

	mu    sync.Mutex
	cache map[string]ToolStatus
}

// NewToolDetector creates a new detector with the given runner.
func NewToolDetector(runner ExecRunner, timeout time.Duration) *ToolDetector {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &ToolDetector{
		runner:  runner,
		timeout: timeout,
		cache:   make(map[string]ToolStatus),
	}
}

// CheckTool checks if a tool is installed and returns its status.
func (d *ToolDetector) CheckTool(ctx context.Context, req IndexerRequirement) ToolStatus {
	d.mu.Lock()
	if cached, ok := d.cache[req.Binary]; ok {
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	status := ToolStatus{
		Name:       req.Name,
		MinVersion: req.MinVersion,
		InstallCmd: req.GetInstallCommand(),
		Provider:   string(req.Provider),
	}

	// Convert capabilities to strings
	for _, cap := range req.Capabilities {
		status.Capabilities = append(status.Capabilities, string(cap))
	}

	// Check if binary exists
	path, err := d.runner.LookPath(req.Binary)
	binary := req.Binary
	if err != nil {
		if alt := findAltPath(req.AltPaths); alt != "" {
			path, binary, err = alt, alt, nil
		}
	}
	if err != nil {
		status.Found = false
		status.Error = "not found in PATH"
		d.cacheResult(req.Binary, status)
		return status
	}

	status.Found = true
	status.Path = path

	// Get version if we have version args
	if len(req.VersionArgs) > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, d.timeout)
		defer cancel()

		stdout, stderr, err := d.runner.Run(timeoutCtx, binary, req.VersionArgs...)
		if err != nil {
			// Tool exists but version check failed - still usable
			status.Error = "version check failed"
			status.VersionOK = req.MinVersion == ""
		} else {
			// Parse version from output
			output := stdout
			if output == "" {
				output = stderr
			}
			status.Version = parseVersion(output)

			// Check version requirement
			if req.MinVersion != "" {
				status.VersionOK = versionAtLeast(status.Version, req.MinVersion)
				if !status.VersionOK {
					status.Error = fmt.Sprintf("version %s < required %s", status.Version, req.MinVersion)
				}
			} else {
				status.VersionOK = true
			}
		}
	} else {
		status.VersionOK = true
	}

	d.cacheResult(req.Binary, status)
	return status
}

func (d *ToolDetector) cacheResult(key string, status ToolStatus) {
	d.mu.Lock()
	d.cache[key] = status
	d.mu.Unlock()
}

// ClearCache clears the detection cache.
func (d *ToolDetector) ClearCache() {
	d.mu.Lock()
	d.cache = make(map[string]ToolStatus)
	d.mu.Unlock()
}

// DetectLanguageTier detects the available tier for a language.
func (d *ToolDetector) DetectLanguageTier(ctx context.Context, lang Language) LanguageToolStatus {
	status := LanguageToolStatus{
		Language:     lang,
		DisplayName:  lang.DisplayName(),
		ToolTier:     TierBasic, // Start at basic
		RuntimeTier:  TierBasic,
		Tools:        []ToolStatus{},
		Missing:      []ToolStatus{},
		Capabilities: make(map[string]bool),
	}

	// Check enhanced tier tools
	enhancedReqs := GetIndexerRequirements(lang, TierEnhanced)
	enhancedReady := len(enhancedReqs) > 0 // Need at least one requirement

	for _, req := range enhancedReqs {
		toolStatus := d.CheckTool(ctx, req)
		status.Tools = append(status.Tools, toolStatus)

		if !toolStatus.Found || (toolStatus.MinVersion != "" && !toolStatus.VersionOK) {
			enhancedReady = false
			status.Missing = append(status.Missing, toolStatus)
		} else {
			// Add capabilities from this tool
			for _, cap := range req.Capabilities {
				status.Capabilities[string(cap)] = true
			}
		}
	}

	if enhancedReady {
		status.ToolTier = TierEnhanced
		status.RuntimeTier = TierEnhanced
	}

	// Check full tier tools
	fullReqs := GetIndexerRequirements(lang, TierFull)
	fullReady := enhancedReady && len(fullReqs) > 0

	for _, req := range fullReqs {
		toolStatus := d.CheckTool(ctx, req)
		status.Tools = append(status.Tools, toolStatus)

		if !toolStatus.Found || (toolStatus.MinVersion != "" && !toolStatus.VersionOK) {
			fullReady = false
			status.Missing = append(status.Missing, toolStatus)
		} else {
			// Add capabilities from this tool
			for _, cap := range req.Capabilities {
				status.Capabilities[string(cap)] = true
			}
		}
	}

	if fullReady {
		status.ToolTier = TierFull
		// RuntimeTier stays at Enhanced until LSP is actually tested
	}

	// Always have definitions capability at minimum
	status.Capabilities[string(CapDefinitions)] = true

	return status
}

// DetectAllLanguages detects tiers for multiple languages concurrently.
func (d *ToolDetector) DetectAllLanguages(ctx context.Context, languages []Language) map[Language]LanguageToolStatus {
	results := make(map[Language]LanguageToolStatus)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Bounded concurrency
	sem := make(chan struct{}, 4)

	for _, lang := range languages {
		wg.Add(1)
		go func(l Language) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			status := d.DetectLanguageTier(ctx, l)

			mu.Lock()
			results[l] = status
			mu.Unlock()
		}(lang)
	}

	wg.Wait()
	return results
}

// parseVersion extracts a version number from command output.
func parseVersion(output string) string {
	// Try common patterns
	patterns := []string{
		`v?(\d+\.\d+\.\d+)`,      // 1.2.3 or v1.2.3
		`(\d+\.\d+)`,             // 1.2
		`version[:\s]+(\S+)`,     // version: x.y.z
		`(\d+\.\d+\.\d+-[\w.]+)`, // 1.2.3-beta.1
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(output); len(matches) > 1 {
			return matches[1]
		}
	}

	// Return first word if nothing else matches
	fields := strings.Fields(output)
	if len(fields) > 0 {
		return fields[0]
	}

	return ""
}

// versionAtLeast checks if version >= minVersion (simple semver comparison).
func versionAtLeast(version, minVersion string) bool {
	if version == "" || minVersion == "" {
		return true
	}

	// Clean up version strings
	version = strings.TrimPrefix(version, "v")
	minVersion = strings.TrimPrefix(minVersion, "v")

	// Split into parts
	vParts := strings.Split(version, ".")
	mParts := strings.Split(minVersion, ".")

	// Compare each part
	for i := 0; i < len(mParts); i++ {
		if i >= len(vParts) {
			return false
		}

		// Extract numeric part (ignore suffixes like -beta)
		vNum := extractNumeric(vParts[i])
		mNum := extractNumeric(mParts[i])

		if vNum > mNum {
			return true
		}
		if vNum < mNum {
			return false
		}
	}

	return true
}

// extractNumeric extracts the leading numeric part of a version component.
func extractNumeric(s string) int {
	var num int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			num = num*10 + int(c-'0')
		} else {
			break
		}
	}
	return num
}

// SortedLanguages returns languages in deterministic order.
func SortedLanguages(results map[Language]LanguageToolStatus) []Language {
	languages := make([]Language, 0, len(results))
	for lang := range results {
		languages = append(languages, lang)
	}
	sort.Slice(languages, func(i, j int) bool {
		return string(languages[i]) < string(languages[j])
	})
	return languages
}

// SortedTools returns tools in deterministic order.
func SortedTools(tools []ToolStatus) []ToolStatus {
	sorted := make([]ToolStatus, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// findAltPath returns the first of paths (see IndexerRequirement.AltPaths)
// that is an executable file, or "".
func findAltPath(paths []string) string {
	home, _ := os.UserHomeDir()
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, "~/"):
			if home == "" {
				continue
			}
			p = filepath.Join(home, p[2:])
		case strings.HasPrefix(p, "$"):
			name, rest, _ := strings.Cut(p[1:], "/")
			v := os.Getenv(name)
			if v == "" {
				continue
			}
			p = filepath.Join(v, rest)
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
