package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

var (
	setupGlobal  bool
	setupNpx     bool
	setupTool    string
	setupPreset  string
	setupNoIndex bool
	setupNoWatch bool
)

// aiTool represents an AI coding tool that supports MCP
type aiTool struct {
	ID              string
	Name            string
	SupportsGlobal  bool
	SupportsProject bool
	GlobalUsesCmd   bool   // true = use CLI command, false = write file
	Format          string // "mcpServers" | "servers" | "mcp"
}

var aiTools = []aiTool{
	{ID: "claude-code", Name: "Claude Code", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: true, Format: "mcpServers"},
	{ID: "cursor", Name: "Cursor", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: false, Format: "mcpServers"},
	{ID: "windsurf", Name: "Windsurf", SupportsGlobal: true, SupportsProject: false, GlobalUsesCmd: false, Format: "mcpServers"},
	{ID: "vscode", Name: "VS Code", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: true, Format: "servers"},
	{ID: "opencode", Name: "OpenCode", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: false, Format: "mcp"},
	{ID: "grok", Name: "Grok", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: true, Format: "grokServers"},
	{ID: "claude-desktop", Name: "Claude Desktop", SupportsGlobal: true, SupportsProject: false, GlobalUsesCmd: false, Format: "mcpServers"},
	// Codex CLI supports project-local MCP config for trusted projects
	// (<repo>/.codex/config.toml) in addition to the user-global
	// ~/.codex/config.toml. Default (non --global) setup writes the
	// project-local file; --global writes the user-global one.
	{ID: "codex", Name: "Codex", SupportsGlobal: true, SupportsProject: true, GlobalUsesCmd: false, Format: "codexToml"},
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Configure CKB for AI coding tools",
	Long: `Sets up CKB as an MCP server for AI coding tools.

Supports: Claude Code, Cursor, Windsurf, VS Code, OpenCode, Grok, Claude Desktop, Codex

For project-scope setups, this also makes sure the project itself is ready:
it runs 'ckb init' if .ckb/ is missing and 'ckb index' if there's no usable
index yet, so 'ckb setup' alone is enough to get full code intelligence —
no separate init/index dance required. Use --no-index to skip that step.

Generated MCP server configs include --watch by default, so the server
keeps the index fresh during the session. Use --no-watch to opt out.

Examples:
  ckb setup                    # Interactive setup
  ckb setup --tool=cursor      # Configure for Cursor
  ckb setup --tool=grok        # Configure for Grok
  ckb setup --tool=codex       # Configure for Codex CLI
  ckb setup --tool=vscode --global  # Configure VS Code globally
  ckb setup --npx              # Use npx for portable setup
  ckb setup --no-index         # Skip auto-init/index for this project`,
	RunE: runSetup,
}

func init() {
	setupCmd.Flags().BoolVar(&setupGlobal, "global", false, "Configure globally for all projects")
	setupCmd.Flags().BoolVar(&setupNpx, "npx", false, "Use npx @tastehub/ckb for portable setup")
	setupCmd.Flags().StringVar(&setupTool, "tool", "", "AI tool to configure (claude-code, cursor, windsurf, vscode, opencode, grok, claude-desktop, codex)")
	setupCmd.Flags().StringVar(&setupPreset, "preset", "", "Tool preset: core (default), review, refactor, federation, docs, ops, full")
	setupCmd.Flags().BoolVar(&setupNoIndex, "no-index", false, "Skip auto-init/index for project-scope setups")
	setupCmd.Flags().BoolVar(&setupNoWatch, "no-watch", false, "Don't add --watch to the generated MCP server command")
	rootCmd.AddCommand(setupCmd)
}

// Config types for different formats

// mcpServersConfig is used by Claude Code, Cursor, Windsurf, Claude Desktop
type mcpServersConfig struct {
	McpServers map[string]mcpServer `json:"mcpServers"`
}

type mcpServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// vsCodeConfig is used by VS Code (.vscode/mcp.json)
type vsCodeConfig struct {
	Servers map[string]vsCodeServer `json:"servers"`
}

type vsCodeServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// openCodeConfig is used by OpenCode
type openCodeConfig struct {
	Mcp map[string]openCodeMcpEntry `json:"mcp"`
}

type openCodeMcpEntry struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

// grokMcpEntry is used by Grok (.grok/settings.json)
type grokMcpEntry struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args"`
}

func runSetup(cmd *cobra.Command, args []string) error {
	// Determine the CKB command to use
	var ckbCommand string
	var ckbArgs []string

	if setupNpx {
		ckbCommand = "npx"
		ckbArgs = []string{"-y", "@tastehub/ckb", "mcp"}
	} else {
		// Find the current ckb binary
		ckbPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("failed to find ckb binary: %w", err)
		}
		// Resolve symlinks
		ckbPath, err = filepath.EvalSymlinks(ckbPath)
		if err != nil {
			return fmt.Errorf("failed to resolve ckb path: %w", err)
		}
		ckbCommand = ckbPath
		ckbArgs = []string{"mcp"}
	}

	// Keep the index fresh for the life of the MCP session by default — a
	// developer shouldn't have to know 'ckb index --watch' exists.
	if !setupNoWatch {
		ckbArgs = append(ckbArgs, "--watch")
	}

	// Select tool
	var selectedTool *aiTool
	if setupTool != "" {
		// Find tool by ID
		for i := range aiTools {
			if aiTools[i].ID == setupTool {
				selectedTool = &aiTools[i]
				break
			}
		}
		if selectedTool == nil {
			return fmt.Errorf("unknown tool: %s. Valid options: claude-code, cursor, windsurf, vscode, opencode, grok, claude-desktop, codex", setupTool)
		}
	} else {
		// Interactive tool selection
		tool, err := selectTool()
		if err != nil {
			return err
		}
		selectedTool = tool
	}

	// Determine scope
	global := setupGlobal
	if !setupGlobal && setupTool == "" {
		// Ask for scope if tool supports both and not specified via flag
		if selectedTool.SupportsGlobal && selectedTool.SupportsProject {
			scope, err := selectScope(selectedTool)
			if err != nil {
				return err
			}
			global = scope
		} else if selectedTool.SupportsGlobal && !selectedTool.SupportsProject {
			global = true
		} else {
			global = false
		}
	}

	// Validate scope
	if global && !selectedTool.SupportsGlobal {
		return fmt.Errorf("%s does not support global configuration", selectedTool.Name)
	}
	if !global && !selectedTool.SupportsProject {
		fmt.Printf("%s only supports global configuration. Configuring globally.\n\n", selectedTool.Name)
		global = true
	}

	// Determine preset
	preset := setupPreset
	if preset == "" && setupTool == "" {
		// Interactive preset selection
		var err error
		preset, err = selectPreset()
		if err != nil {
			return err
		}
	}

	// Validate preset if provided
	if preset != "" {
		validPresets := map[string]bool{
			"core": true, "review": true, "refactor": true,
			"federation": true, "docs": true, "ops": true, "full": true,
		}
		if !validPresets[preset] {
			return fmt.Errorf("unknown preset: %s. Valid options: core, review, refactor, federation, docs, ops, full", preset)
		}
		// Add preset to args (only if not "core" which is the default)
		if preset != "core" {
			ckbArgs = append(ckbArgs, "--preset="+preset)
		}
	}

	// Configure the AI tool FIRST, before any indexing. A slow or interrupted
	// index build must never leave the developer without a working MCP
	// config — the agent should be usable (Git-based features at minimum)
	// the moment this step finishes, regardless of what happens next.
	if err := configureTool(selectedTool, global, ckbCommand, ckbArgs); err != nil {
		return err
	}

	// Project-scope configs point an AI tool at *this* repo, so make sure the
	// repo itself is usable too: init .ckb/ if missing, index if there's no
	// usable index yet. This runs AFTER the config is written (see above).
	// Global configs aren't tied to a project, so skip.
	if !global {
		if err := ensureProjectReady(); err != nil {
			return err
		}
	}

	// Offer to install skills in interactive mode
	if setupTool == "" && selectedTool.ID == "claude-code" {
		if skillErr := promptInstallSkills(); skillErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not install skills: %v\n", skillErr)
		}
	}

	// Post-setup hint: LIP semantic search (optional, non-interactive)
	fmt.Println()
	fmt.Println("Optional: enable semantic search with LIP v2.0 (requires Rust/cargo):")
	fmt.Println("  cargo install lip-cli && lip daemon --socket ~/.local/share/lip/lip.sock && lip index .")
	fmt.Println("  https://lip-sigma.vercel.app  —  once running, CKB picks it up automatically.")

	return nil
}

// ensureProjectReady makes project-scope 'ckb setup' self-sufficient: it runs
// the same init logic as 'ckb init' if .ckb/ is missing, then the same index
// logic as 'ckb index' if there's no usable index yet, so a developer never
// has to know those commands exist. Neither step is allowed to fail setup:
//   - init failure is surfaced (a broken .ckb/ means the MCP server can't work)
//   - index failure/skip is only ever reported as a one-line note, because
//     Git-based features (hotspots, ownership, diffs) work without SCIP.
//
// Called from runSetup AFTER configureTool has already written the MCP
// config, specifically so that a slow indexer (or a developer hitting
// Ctrl-C on it) never leaves setup without a usable config file on disk.
func ensureProjectReady() error {
	if setupNoIndex {
		// Still make sure .ckb/ exists even if indexing itself is skipped —
		// the MCP server needs it to start at all.
		return ensureCkbInitialized()
	}

	if err := ensureCkbInitialized(); err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	fmt.Println("Checking code index...")
	fmt.Println("  (safe to Ctrl-C — your MCP config is already saved and usable; Git-based")
	fmt.Println("  features work immediately, and 'ckb mcp --watch' builds the SCIP index on")
	fmt.Println("  its own once an indexer is available, or run 'ckb index' any time)")
	result := performIndex(cwd)

	switch result.Outcome {
	case indexOutcomeIndexerMissing, indexOutcomeIndexerRequirementMissing:
		fmt.Printf("Note: %s\n", result.Message)
		fmt.Println("  Git-based features (hotspots, ownership, diffs) still work without it.")
		fmt.Println("  Install the indexer above, then run 'ckb index' any time to enable full code intelligence.")
	case indexOutcomeNoLanguageDetected:
		fmt.Println("Note: could not detect a supported language — skipping code index.")
		fmt.Println("  Git-based features (hotspots, ownership, diffs) still work.")
	case indexOutcomeMultipleLanguages:
		fmt.Println("Note: multiple languages detected — run 'ckb index --lang <lang>' to index manually.")
	case indexOutcomeIndexingFailed, indexOutcomeError:
		fmt.Printf("Note: indexing did not complete (%s) — continuing setup.\n", result.Message)
		fmt.Println("  Git-based features (hotspots, ownership, diffs) still work. Run 'ckb index' to retry.")
	case indexOutcomeIndexed, indexOutcomeUpToDate, indexOutcomeSkippedLargeRepo:
		// Already printed its own detail above.
	}
	fmt.Println()

	return nil
}

// ensureCkbInitialized runs the same logic as 'ckb init' if .ckb/ doesn't
// exist yet in the current directory. It's idempotent — safe to call every
// time 'ckb setup' runs.
func ensureCkbInitialized() error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	ckbDir := filepath.Join(cwd, ".ckb")
	if _, statErr := os.Stat(ckbDir); statErr == nil {
		return nil // already initialized
	}

	fmt.Println("No .ckb/ directory found — initializing CKB for this project...")
	if err := runInit(nil, nil); err != nil {
		return fmt.Errorf("failed to initialize CKB: %w", err)
	}
	fmt.Println()
	return nil
}

func selectTool() (*aiTool, error) {
	fmt.Println("\nSelect AI tool to configure:")
	fmt.Println()
	for i, tool := range aiTools {
		fmt.Printf("  %d. %s\n", i+1, tool.Name)
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Enter choice [1-%d]: ", len(aiTools))
		input, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(aiTools) {
			fmt.Printf("Invalid choice. Please enter a number between 1 and %d.\n", len(aiTools))
			continue
		}

		return &aiTools[choice-1], nil
	}
}

func promptInstallSkills() error {
	fmt.Println("\nCKB provides a /ckb-review slash command for Claude Code that orchestrates")
	fmt.Println("CKB's structural analysis with your LLM review — 15 checks in 5 seconds,")
	fmt.Println("then focused semantic review on what CKB flags.")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Install /ckb-review skill? [Y/n]: ")
	input, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" || input == "y" || input == "yes" {
		return installClaudeCodeSkills()
	}

	fmt.Println("Skipped. You can install later with: ckb setup --tool=claude-code")
	return nil
}

func selectScope(tool *aiTool) (bool, error) {
	fmt.Println("\nConfigure scope:")
	fmt.Println()
	fmt.Println("  1. Project (current directory only)")
	fmt.Println("  2. Global (applies to all projects)")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter choice [1-2]: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return false, nil
		case "2":
			return true, nil
		default:
			fmt.Println("Invalid choice. Please enter 1 or 2.")
		}
	}
}

// presetInfo holds display info for a preset
type presetInfo struct {
	id          string
	name        string
	description string
}

var presets = []presetInfo{
	{id: "core", name: "Core", description: "Essential tools for navigation and analysis (14 tools, recommended)"},
	{id: "review", name: "Review", description: "Code review focused: PR summary, hotspots, ownership (19 tools)"},
	{id: "refactor", name: "Refactor", description: "Refactoring focused: impact analysis, coupling, complexity (19 tools)"},
	{id: "docs", name: "Docs", description: "Documentation focused: doc coverage, staleness checks (20 tools)"},
	{id: "ops", name: "Ops", description: "Operations: jobs, webhooks, scheduling, metrics (25 tools)"},
	{id: "federation", name: "Federation", description: "Multi-repo analysis and cross-repo search (28 tools)"},
	{id: "full", name: "Full", description: "All available tools (76 tools)"},
}

func selectPreset() (string, error) {
	fmt.Println("\nSelect tool preset:")
	fmt.Println()
	for i, p := range presets {
		fmt.Printf("  %d. %s - %s\n", i+1, p.name, p.description)
	}
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("Enter choice [1-%d] (default: 1): ", len(presets))
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		if input == "" {
			return "core", nil // default
		}

		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(presets) {
			fmt.Printf("Invalid choice. Please enter a number between 1 and %d.\n", len(presets))
			continue
		}

		return presets[choice-1].id, nil
	}
}

func promptRepoPath() (string, error) {
	cwd, _ := os.Getwd()

	fmt.Println("\nClaude Desktop needs to know which repository to analyze.")
	fmt.Printf("Current directory: %s\n\n", cwd)
	fmt.Println("  1. Use current directory")
	fmt.Println("  2. Enter a different path")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("Enter choice [1-2]: ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}

		input = strings.TrimSpace(input)
		switch input {
		case "1":
			return cwd, nil
		case "2":
			fmt.Print("Enter repository path: ")
			path, err := reader.ReadString('\n')
			if err != nil {
				return "", fmt.Errorf("failed to read input: %w", err)
			}
			path = strings.TrimSpace(path)

			// Expand ~ to home directory
			if strings.HasPrefix(path, "~/") {
				home, _ := os.UserHomeDir()
				path = filepath.Join(home, path[2:])
			}

			// Validate path exists
			if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
				fmt.Printf("Path does not exist: %s\n", path)
				continue
			}

			// Convert to absolute path
			absPath, absErr := filepath.Abs(path)
			if absErr != nil {
				return path, nil //nolint:nilerr // fallback to original path if abs fails
			}
			return absPath, nil
		default:
			fmt.Println("Invalid choice. Please enter 1 or 2.")
		}
	}
}

func configureTool(tool *aiTool, global bool, ckbCommand string, ckbArgs []string) error {
	// Handle tools that use CLI commands for global setup
	if global && tool.GlobalUsesCmd {
		switch tool.ID {
		case "claude-code":
			return configureClaudeCodeGlobal(ckbCommand, ckbArgs)
		case "vscode":
			return configureVSCodeGlobal(ckbCommand, ckbArgs)
		case "grok":
			_, err := configureGrokGlobal(ckbCommand, ckbArgs)
			return err
		}
	}

	// Get config path
	configPath := getConfigPath(tool.ID, global)
	if configPath == "" {
		return fmt.Errorf("could not determine config path for %s", tool.Name)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Claude Desktop needs special handling - prompt for repo path
	var repoPath string
	if tool.ID == "claude-desktop" {
		var err error
		repoPath, err = promptRepoPath()
		if err != nil {
			return err
		}
	}

	// Write config based on format
	var err error
	switch tool.Format {
	case "mcpServers":
		if tool.ID == "claude-desktop" && repoPath != "" {
			err = writeMcpServersConfigWithEnv(configPath, ckbCommand, ckbArgs, map[string]string{
				"CKB_REPO": repoPath,
			})
		} else {
			err = writeMcpServersConfig(configPath, ckbCommand, ckbArgs)
		}
	case "servers":
		err = writeVSCodeConfig(configPath, ckbCommand, ckbArgs)
	case "mcp":
		err = writeOpenCodeConfig(configPath, ckbCommand, ckbArgs, setupNpx)
	case "grokServers":
		err = writeGrokConfig(configPath, ckbCommand, ckbArgs)
	case "codexToml":
		err = writeCodexConfig(configPath, ckbCommand, ckbArgs)
	default:
		err = fmt.Errorf("unknown format: %s", tool.Format)
	}

	if err != nil {
		return err
	}

	fmt.Printf("\n✓ Added CKB to %s\n", configPath)
	fmt.Printf("  Command: %s %s\n", ckbCommand, strings.Join(ckbArgs, " "))
	if repoPath != "" {
		fmt.Printf("  Repository: %s\n", repoPath)
	}
	fmt.Printf("\nRestart %s to load the new configuration.\n", tool.Name)

	return nil
}

func getConfigPath(toolID string, global bool) string {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	switch toolID {
	case "claude-code":
		if global {
			// Fallback path when CLI is not available
			return filepath.Join(home, ".claude.json")
		}
		return filepath.Join(cwd, ".mcp.json")

	case "cursor":
		if global {
			return filepath.Join(home, ".cursor", "mcp.json")
		}
		return filepath.Join(cwd, ".cursor", "mcp.json")

	case "windsurf":
		// Probe multiple locations, prefer existing, default to official path
		var candidates []string
		if runtime.GOOS == "windows" {
			base := filepath.Join(os.Getenv("USERPROFILE"), ".codeium")
			candidates = []string{
				filepath.Join(base, "mcp_config.json"),
				filepath.Join(base, "windsurf", "mcp_config.json"),
			}
		} else {
			candidates = []string{
				filepath.Join(home, ".codeium", "mcp_config.json"),
				filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"),
			}
		}
		for _, path := range candidates {
			if _, err := os.Stat(path); err == nil { // #nosec G703 -- path is internally constructed
				return path
			}
		}
		return candidates[0] // Default to official path

	case "vscode":
		if global {
			return "" // Use CLI command
		}
		return filepath.Join(cwd, ".vscode", "mcp.json")

	case "opencode":
		if global {
			return filepath.Join(home, ".config", "opencode", "opencode.json")
		}
		return filepath.Join(cwd, "opencode.json")

	case "grok":
		if global {
			return filepath.Join(home, ".grok", "user-settings.json")
		}
		return filepath.Join(cwd, ".grok", "settings.json")

	case "claude-desktop":
		if runtime.GOOS == "windows" {
			return filepath.Join(os.Getenv("APPDATA"), "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")

	case "codex":
		if global {
			return filepath.Join(home, ".codex", "config.toml")
		}
		// Trusted-project scope: Codex reads <repo>/.codex/config.toml when the
		// project directory has been trusted in the user's global config.
		return filepath.Join(cwd, ".codex", "config.toml")
	}

	return ""
}

func writeMcpServersConfig(path, command string, args []string) error {
	return writeMcpServersConfigWithEnv(path, command, args, nil)
}

func writeMcpServersConfigWithEnv(path, command string, args []string, env map[string]string) error {
	// Read existing config or create new
	config := mcpServersConfig{
		McpServers: make(map[string]mcpServer),
	}

	if data, err := os.ReadFile(path); err == nil { // #nosec G703 -- path is internally constructed
		if jsonErr := json.Unmarshal(data, &config); jsonErr != nil {
			fmt.Printf("Warning: existing config is invalid, will overwrite\n")
			config.McpServers = make(map[string]mcpServer)
		}
	}

	// Add or update CKB entry
	server := mcpServer{
		Command: command,
		Args:    args,
	}
	if len(env) > 0 {
		server.Env = env
	}
	config.McpServers["ckb"] = server

	// Write config
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0644) // #nosec G703 -- non-sensitive config file
}

func writeVSCodeConfig(path, command string, args []string) error {
	// Read existing config or create new
	config := vsCodeConfig{
		Servers: make(map[string]vsCodeServer),
	}

	if data, err := os.ReadFile(path); err == nil { // #nosec G703 -- path is internally constructed
		if jsonErr := json.Unmarshal(data, &config); jsonErr != nil {
			fmt.Printf("Warning: existing config is invalid, will overwrite\n")
			config.Servers = make(map[string]vsCodeServer)
		}
	}

	// Add or update CKB entry
	config.Servers["ckb"] = vsCodeServer{
		Type:    "stdio",
		Command: command,
		Args:    args,
	}

	// Write config
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0644) // #nosec G703 -- non-sensitive config file
}

func writeOpenCodeConfig(path, command string, args []string, useNpx bool) error {
	// Read existing config or create new
	config := openCodeConfig{
		Mcp: make(map[string]openCodeMcpEntry),
	}

	if data, err := os.ReadFile(path); err == nil { // #nosec G703 -- path is internally constructed
		if jsonErr := json.Unmarshal(data, &config); jsonErr != nil {
			fmt.Printf("Warning: existing config is invalid, will overwrite\n")
			config.Mcp = make(map[string]openCodeMcpEntry)
		}
	}

	// Build command array for OpenCode format. command/args already fully
	// describe the invocation (npx vs binary, plus --watch/--preset flags) —
	// useNpx is kept only so callers can assert their intent; re-deriving the
	// npx array here used to silently drop --watch/--preset for npx setups.
	cmdArray := append([]string{command}, args...)
	if useNpx && command != "npx" {
		cmdArray = append([]string{"npx", "-y", "@tastehub/ckb"}, args...)
	}

	// Add or update CKB entry
	config.Mcp["ckb"] = openCodeMcpEntry{
		Type:    "local",
		Command: cmdArray,
		Enabled: true,
	}

	// Write config
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0644) // #nosec G703 -- non-sensitive config file
}

func writeGrokConfig(path, command string, args []string) error {
	// Read existing config preserving other fields
	var raw map[string]json.RawMessage
	if data, err := os.ReadFile(path); err == nil { // #nosec G703 -- path is internally constructed
		if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil {
			fmt.Printf("Warning: existing config is invalid, will overwrite\n")
			raw = make(map[string]json.RawMessage)
		}
	} else {
		raw = make(map[string]json.RawMessage)
	}

	// Parse existing mcpServers or create new
	mcpServers := make(map[string]grokMcpEntry)
	if existing, ok := raw["mcpServers"]; ok {
		_ = json.Unmarshal(existing, &mcpServers)
	}

	// Add or update CKB entry
	mcpServers["ckb"] = grokMcpEntry{
		Name:      "ckb",
		Transport: "stdio",
		Command:   command,
		Args:      args,
	}

	// Marshal mcpServers back
	mcpData, err := json.Marshal(mcpServers)
	if err != nil {
		return fmt.Errorf("failed to marshal mcpServers: %w", err)
	}
	raw["mcpServers"] = mcpData

	// Write config
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(path, data, 0644) // #nosec G703 -- non-sensitive config file
}

// codexTableHeader is the TOML table Codex CLI reads CKB's MCP server config
// from: [mcp_servers.ckb] in either ~/.codex/config.toml (--global) or
// <repo>/.codex/config.toml (project scope, trusted projects).
const codexTableHeader = "[mcp_servers.ckb]"

// writeCodexConfig upserts CKB's [mcp_servers.ckb] table into Codex's
// config.toml (global or project-scoped). Codex's config.toml is a file
// another tool owns and may hand-edit (comments, other [mcp_servers.*]
// tables, unrelated top-level settings) — CKB has no business reformatting
// any of that. So rather than decode-then-fully-reencode the whole file
// through a TOML library (which would normalize formatting and drop
// comments), this does a surgical textual replace of just the CKB table's
// own keys, leaving everything else — including [mcp_servers.ckb.env]
// subtables — untouched byte-for-byte. The result is validated as TOML
// before being written; if that fails, nothing is written.
func writeCodexConfig(path, command string, args []string) error {
	var existing string
	if data, err := os.ReadFile(path); err == nil { // #nosec G703 -- path is internally constructed
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}

	// Refuse to touch a file we can't parse. Editing it blind risks turning
	// a pre-existing syntax error into data loss.
	if strings.TrimSpace(existing) != "" {
		var probe any
		if _, err := toml.Decode(existing, &probe); err != nil {
			return fmt.Errorf(
				"%s is not valid TOML, leaving it untouched (%w)\nAdd this table manually:\n\n%s",
				path, err, codexManualTOMLSnippet(command, args),
			)
		}
	}

	// Codex's config.toml is written by whatever OS the user is on; preserve
	// CRLF line endings if that's what's already there.
	crlf := strings.Contains(existing, "\r\n")
	normalized := existing
	if crlf {
		normalized = strings.ReplaceAll(existing, "\r\n", "\n")
	}

	updated := upsertCodexMCPServer(normalized, command, args)

	// Belt-and-suspenders: validate what we're about to write is still valid
	// TOML before it touches disk. upsertTOMLTable only ever does line-level
	// surgery, but if that surgery ever produces something unparsable, abort
	// rather than hand Codex a broken config.
	var probe any
	if _, err := toml.Decode(updated, &probe); err != nil {
		return fmt.Errorf(
			"generated config for %s would not be valid TOML, aborting without writing (%w)\nAdd this table manually:\n\n%s",
			path, err, codexManualTOMLSnippet(command, args),
		)
	}

	if crlf {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}

	return writeFileAtomic(path, []byte(updated), 0644)
}

// codexManualTOMLSnippet renders the [mcp_servers.ckb] table CKB would have
// written, for error messages that ask the user to add it by hand.
func codexManualTOMLSnippet(command string, args []string) string {
	return codexTableHeader + "\n" + strings.Join(codexBodyKeys(command, args), "\n") + "\n"
}

// codexBodyKeys renders the bare key = value lines (no header) for CKB's
// Codex MCP server entry.
func codexBodyKeys(command string, args []string) []string {
	quotedArgs := make([]string, len(args))
	for i, a := range args {
		quotedArgs[i] = tomlQuoteString(a)
	}
	return []string{
		fmt.Sprintf("command = %s", tomlQuoteString(command)),
		fmt.Sprintf("args = [%s]", strings.Join(quotedArgs, ", ")),
	}
}

// tomlTargetPath is the dotted key path CKB's MCP server table lives at.
var tomlTargetPath = []string{"mcp_servers", "ckb"}

// upsertCodexMCPServer replaces the bare command/args keys of the
// [mcp_servers.ckb] table in content, or appends the whole table if not
// present. All other content (other tables, comments, key order, and any
// [mcp_servers.ckb.*] subtables such as a user-configured .env block) is
// left untouched.
func upsertCodexMCPServer(content, command string, args []string) string {
	return upsertTOMLTable(content, tomlTargetPath, codexTableHeader, codexBodyKeys(command, args))
}

// tomlArrayHeaderRe matches an array-of-tables header, e.g. "[[foo]]".
var tomlArrayHeaderRe = regexp.MustCompile(`^\s*\[\[`)

// tomlHeaderRe matches a single-bracket TOML table header line, capturing
// the bracket contents. Tolerates leading/trailing whitespace and a
// trailing inline comment (# ...). Never matches array-of-tables headers
// (tomlArrayHeaderRe is checked first).
var tomlHeaderRe = regexp.MustCompile(`^\s*\[([^\[\]]*)\]\s*(#.*)?$`)

// tomlBareKeyRe matches the key portion of a bare "key = value" TOML line,
// including quoted keys ("command" = ... or 'command' = ...).
var tomlBareKeyRe = regexp.MustCompile(`^\s*("([^"]*)"|'([^']*)'|[A-Za-z0-9_-]+)\s*=`)

// parseTOMLHeaderPath parses a TOML table header line into its dotted key
// path, honoring quoted keys — [mcp_servers."ckb"], ["mcp_servers".ckb], and
// [mcp_servers.ckb] all parse to the same path. Returns ok=false if the line
// isn't a single-bracket table header line.
func parseTOMLHeaderPath(line string) (path []string, ok bool) {
	if tomlArrayHeaderRe.MatchString(line) {
		return nil, false
	}
	m := tomlHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	inner := strings.TrimSpace(m[1])
	if inner == "" {
		return nil, false
	}
	return splitTOMLKeyPath(inner)
}

// splitTOMLKeyPath splits a dotted TOML key path into its component keys,
// respecting basic ("...") and literal ('...') quoted segments that may
// themselves contain dots.
func splitTOMLKeyPath(s string) ([]string, bool) {
	var parts []string
	var cur strings.Builder
	var inQuote byte

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote != 0:
			cur.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
		case c == '"' || c == '\'':
			inQuote = c
			cur.WriteByte(c)
		case c == '.':
			parts = append(parts, unquoteTOMLKey(strings.TrimSpace(cur.String())))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if inQuote != 0 {
		return nil, false // unterminated quote — malformed, don't treat as a match
	}

	last := strings.TrimSpace(cur.String())
	parts = append(parts, unquoteTOMLKey(last))

	for _, p := range parts {
		if p == "" {
			return nil, false
		}
	}
	return parts, true
}

// unquoteTOMLKey strips quotes from a single TOML key segment, if present.
func unquoteTOMLKey(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unq, err := strconv.Unquote(s); err == nil {
			return unq
		}
	}
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// tomlBareKeyName returns the key name of a bare "key = value" line, or ""
// if the line isn't such an assignment (blank, comment, or a table header).
func tomlBareKeyName(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	m := tomlBareKeyRe.FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	if m[2] != "" {
		return m[2]
	}
	if m[3] != "" {
		return m[3]
	}
	return strings.TrimSpace(m[1])
}

func tomlPathEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// upsertTOMLTable finds the table at targetPath (matched via parsed header
// key paths, so quoting/comments/whitespace variations all resolve to the
// same table) and rewrites only its own bare "command"/"args" keys —
// anything else in that table's bare-key block (comments, other keys) is
// preserved, and any subtable of targetPath (e.g. [mcp_servers.ckb.env],
// which may hold user-configured env vars) is left completely untouched,
// since it lives past the bare-key block boundary. If targetPath isn't
// found, headerLine + bodyKeys are appended at EOF.
func upsertTOMLTable(content string, targetPath []string, headerLine string, bodyKeys []string) string {
	if strings.TrimSpace(content) == "" {
		return headerLine + "\n" + strings.Join(bodyKeys, "\n") + "\n"
	}

	lines := strings.Split(content, "\n")
	headerIdx := -1
	blockEnd := len(lines) // exclusive: end of the bare-key block under our header

	for i, line := range lines {
		path, ok := parseTOMLHeaderPath(line)
		if headerIdx == -1 {
			if ok && tomlPathEqual(path, targetPath) {
				headerIdx = i
			}
			continue
		}
		// Any table header after ours — including a subtable of ours like
		// [mcp_servers.ckb.env] — ends the block of bare keys we're allowed
		// to rewrite.
		if ok {
			blockEnd = i
			break
		}
	}

	if headerIdx == -1 {
		trimmed := strings.TrimRight(content, "\n")
		return trimmed + "\n\n" + headerLine + "\n" + strings.Join(bodyKeys, "\n") + "\n"
	}

	// Keep every line in the existing bare-key block except command/args —
	// re-running setup updates those in place without disturbing other keys
	// or comments a user may have added inside the table.
	var kept []string
	for _, line := range lines[headerIdx+1 : blockEnd] {
		switch tomlBareKeyName(line) {
		case "command", "args":
			continue
		}
		kept = append(kept, line)
	}

	newBlock := make([]string, 0, 1+len(bodyKeys)+len(kept))
	newBlock = append(newBlock, lines[headerIdx]) // keep the original header line verbatim
	newBlock = append(newBlock, bodyKeys...)
	newBlock = append(newBlock, kept...)

	result := make([]string, 0, headerIdx+len(newBlock)+(len(lines)-blockEnd))
	result = append(result, lines[:headerIdx]...)
	result = append(result, newBlock...)
	result = append(result, lines[blockEnd:]...)

	return strings.Join(result, "\n")
}

// tomlQuoteString renders s as a TOML basic string. Go's quoting rules
// (backslash/quote escaping) are a compatible subset of TOML's for the plain
// ASCII command/arg values CKB ever writes here (binary paths, flags).
func tomlQuoteString(s string) string {
	return strconv.Quote(s)
}

// writeFileAtomic writes data to path by writing a temp file in the same
// directory and renaming it into place, so a process interrupted mid-write
// (or a concurrent writer) can never leave path partially written.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ckb-setup-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op once the rename below has succeeded
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, perm); err != nil { // #nosec G703 -- perm is caller-controlled, not user input
		return fmt.Errorf("failed to set permissions on temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to move temp file into place: %w", err)
	}
	return nil
}

func configureGrokGlobal(ckbCommand string, ckbArgs []string) (bool, error) {
	// Try using grok mcp add command first
	if isGrokAvailable() {
		newCommand := formatCommand(ckbCommand, ckbArgs)

		// Check existing config before calling CLI
		existing, _ := getGrokMcpConfig()
		if existing != nil {
			existingCommand := formatCommand(existing.Command, existing.Args)

			if existingCommand == newCommand {
				fmt.Println("CKB is already configured correctly.")
				fmt.Printf("  Command: %s\n", existingCommand)
				return false, nil
			}

			// Different config - warn and update
			fmt.Println("CKB is already configured with a different path:")
			fmt.Printf("  Current:  %s\n", existingCommand)
			fmt.Printf("  New:      %s\n", newCommand)

			if isNpxCommand(existing.Command) && !isNpxCommand(ckbCommand) {
				fmt.Println("\n  Note: Switching from npx to local binary.")
			} else if !isNpxCommand(existing.Command) && isNpxCommand(ckbCommand) {
				fmt.Println("\n  Note: Switching from local binary to npx.")
			}

			fmt.Println("\nUpdating configuration...")

			// Remove existing entry first
			removeCmd := exec.Command("grok", "mcp", "remove", "ckb")
			removeCmd.Stdout = os.Stdout
			removeCmd.Stderr = os.Stderr
			if err := removeCmd.Run(); err != nil {
				return false, fmt.Errorf("failed to remove existing CKB config from Grok: %w", err)
			}
		}

		cmdArgs := []string{"mcp", "add", "ckb", "--transport", "stdio", "--command", ckbCommand}
		for _, arg := range ckbArgs {
			cmdArgs = append(cmdArgs, "--args", arg)
		}

		fmt.Printf("Running: grok %s\n", formatArgs(cmdArgs))

		execCmd := exec.Command("grok", cmdArgs...) // #nosec G204 //nolint:gosec // hardcoded command, args are trusted config
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			return false, fmt.Errorf("failed to add CKB to Grok: %w", err)
		}

		fmt.Println("\n✓ CKB added to Grok globally.")
		fmt.Println("Restart Grok to load the new configuration.")
		return true, nil
	}

	// Fallback to writing ~/.grok/user-settings.json
	fmt.Println("Grok CLI not found, using fallback configuration...")
	configPath := getConfigPath("grok", true)
	if err := writeGrokConfig(configPath, ckbCommand, ckbArgs); err != nil {
		return false, err
	}

	fmt.Printf("\n✓ Added CKB to %s\n", configPath)
	fmt.Printf("  Command: %s %s\n", ckbCommand, strings.Join(ckbArgs, " "))
	fmt.Println("\nRestart Grok to load the new configuration.")

	return true, nil
}

func isGrokAvailable() bool {
	_, err := exec.LookPath("grok")
	return err == nil
}

func configureClaudeCodeGlobal(ckbCommand string, ckbArgs []string) error {
	// Try using claude mcp add command first
	if isClaudeAvailable() {
		changed, err := claudeMcpAdd(ckbCommand, ckbArgs)
		if err != nil {
			return err
		}

		if changed {
			fmt.Println("\n✓ CKB configured for Claude Code globally.")
			fmt.Println("Restart Claude Code to load the new configuration.")
		}
	} else {
		// Fallback to writing ~/.claude.json
		fmt.Println("Claude CLI not found, using fallback configuration...")
		configPath := getConfigPath("claude-code", true)
		if err := writeMcpServersConfig(configPath, ckbCommand, ckbArgs); err != nil {
			return err
		}

		fmt.Printf("\n✓ Added CKB to %s\n", configPath)
		fmt.Printf("  Command: %s %s\n", ckbCommand, strings.Join(ckbArgs, " "))
		fmt.Println("\nRestart Claude Code to load the new configuration.")
		fmt.Println("\nTip: Install Claude CLI for better integration: https://claude.ai/code")
	}

	// Install /review skill as user-level command
	if err := installClaudeCodeSkills(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not install skills: %v\n", err)
	}

	return nil
}

// installClaudeCodeSkills writes CKB's Claude Code slash commands to ~/.claude/commands/.
func installClaudeCodeSkills() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	commandsDir := filepath.Join(home, ".claude", "commands")
	if err = os.MkdirAll(commandsDir, 0755); err != nil {
		return err
	}

	skills := []struct {
		filename string
		content  string
		name     string
	}{
		{"ckb-review.md", ckbReviewSkill, "/ckb-review"},
		{"ckb-audit.md", ckbAuditSkill, "/ckb-audit"},
	}

	for _, s := range skills {
		skillPath := filepath.Join(commandsDir, s.filename)
		if existing, readErr := os.ReadFile(skillPath); readErr == nil {
			if string(existing) == s.content {
				continue // Already up to date
			}
		}
		if writeErr := os.WriteFile(skillPath, []byte(s.content), 0644); writeErr != nil {
			return writeErr
		}
		fmt.Printf("✓ Installed %s skill at %s\n", s.name, skillPath)
	}
	return nil
}

// ckbReviewSkill is the embedded /ckb-review slash command for Claude Code.
const ckbReviewSkill = `Run a CKB-augmented code review optimized for minimal token usage.

## Input
$ARGUMENTS - Optional: base branch (default: main), or "staged" for staged changes, or a PR number

## Philosophy

CKB already answered the structural questions (secrets? breaking? dead code? test gaps?).
The LLM's job is ONLY what CKB can't do: semantic reasoning about correctness, design,
and intent. Every source line you read costs tokens — read only what CKB says is risky.

### CKB's blind spots (what the LLM must catch)

CKB runs 15 deterministic checks with AST rules, SCIP index, and git history.
It is structurally sound but semantically blind:

- **Logic errors**: wrong conditions (` + "`" + `>` + "`" + ` vs ` + "`" + `>=` + "`" + `), off-by-one, incorrect algorithm
- **Business logic**: domain-specific mistakes CKB has no context for
- **Design fitness**: wrong abstraction, leaky interface, coupling that metrics miss
- **Input validation**: missing bounds checks, nil guards outside AST patterns
- **Race conditions**: concurrency issues, mutex ordering, shared state
- **Resource leaks**: file handles, goroutines, connections not closed on all paths
- **Incomplete refactoring**: callers missed across module boundaries
- **Domain edge cases**: error paths, boundary conditions tests don't cover

CKB's scoring uses per-check caps (max -20) and per-rule caps (max -10), so a score
of 85 can still hide multiple capped warnings. HoldTheLine only flags changed lines,
so pre-existing issues interacting with new code won't surface.

## Phase 1: Structural scan (~1k tokens into context)

` + "```" + `bash
ckb review --base=main --format=json --compact 2>/dev/null
` + "```" + `

If a PR number was given:
` + "```" + `bash
BASE=$(gh pr view $ARGUMENTS --json baseRefName -q .baseRefName)
ckb review --base=$BASE --format=json --compact 2>/dev/null
` + "```" + `

If "staged" was given:
` + "```" + `bash
ckb review --staged --format=json --compact 2>/dev/null
` + "```" + `

Parse the JSON output to extract:
- ` + "`" + `score` + "`" + `, ` + "`" + `verdict` + "`" + ` — overall quality
- ` + "`" + `checks[]` + "`" + ` — status + summary per check (15 checks: breaking, secrets, tests, complexity,
  coupling, hotspots, risk, health, dead-code, test-gaps, blast-radius, comment-drift,
  format-consistency, bug-patterns, split)
- ` + "`" + `findings[]` + "`" + ` — severity + file + message + ruleId
- ` + "`" + `narrative` + "`" + ` — CKB AI-generated summary (if available)
- ` + "`" + `prTier` + "`" + ` — small/medium/large
- ` + "`" + `reviewEffort` + "`" + ` — estimated hours + complexity
- ` + "`" + `reviewers[]` + "`" + ` — suggested reviewers with expertise areas
- ` + "`" + `healthReport` + "`" + ` — degraded/improved file counts

From the output, build three lists:
- **SKIP**: passed checks — don't touch these files or topics
- **INVESTIGATE**: warned/failed checks — these are your review scope
- **READ**: hotspot files + files with warn/fail findings — the only files you'll read

**Early exit**: Skip LLM ONLY when ALL conditions are met:
1. Score >= 90 (not 80 — per-check caps hide warnings at 80)
2. Zero warn/fail checks
3. Small change (< 100 lines of diff)
4. No new files (CKB has no SCIP history for them)

If ANY condition fails, proceed to Phase 2 — CKB's structural pass does NOT mean
the code is semantically correct.

## Phase 2: Targeted source reading (the only token-expensive step)

Do NOT read the full diff. Do NOT read every changed file.

**For files CKB flagged (INVESTIGATE list):**
Read only the changed hunks via ` + "`" + `git diff main...HEAD -- <file>` + "`" + `.

**For new files** (CKB has no history — these are your biggest blind spot):
- If it's a new package/module: read the entry point and types/interfaces first,
  then follow references to understand the architecture before reading individual files
- If < 500 lines: read the file
- If > 500 lines: read the first 100 lines (types/imports) + functions CKB flagged
- Skip generated files, test files for existing tests, and config/CI/docs files

**For each file you read, look for exactly:**
- Logic errors (wrong condition, off-by-one, nil deref, race condition)
- Resource leaks (file handles, connections, goroutines not closed on error paths)
- Security issues (injection, auth bypass, secrets CKB's 26 patterns missed)
- Design problems (wrong abstraction, leaky interface, coupling metrics don't catch)
- Missing edge cases the tests don't cover
- Incomplete refactoring (callers that should have changed but didn't)

Do NOT look for: style, naming, formatting, documentation, test coverage —
CKB already checked these structurally.

## Phase 3: Write the review (be terse)

` + "```" + `markdown
## [APPROVE|REQUEST CHANGES|DISCUSS] — CKB score: [N]/100

[One sentence: what the PR does]

[If CKB provided narrative, include it here]

**PR tier:** [small/medium/large] | **Review effort:** [N]h ([complexity])
**Health:** [N] degraded, [N] improved

### Issues
1. **[must-fix|should-fix]** ` + "`" + `file:line` + "`" + ` — [issue in one sentence]
2. ...

### CKB passed (no review needed)
[comma-separated list of passed checks]

### CKB flagged (verified above)
[for each warn/fail finding: confirmed/false-positive + one-line reason]

### Suggested reviewers
[reviewer — expertise area]
` + "```" + `

If no issues found: just the header line + CKB passed list. Nothing else.

## Anti-patterns (token waste)

- Reading files CKB marked as pass — waste
- Reading generated files — waste
- Summarizing what the PR does in detail — waste (git log exists, CKB has narrative)
- Explaining why passed checks passed — waste
- Running MCP drill-down tools when CLI already gave enough signal — waste
- Reading test files to "verify test quality" — waste unless CKB flagged test-gaps
- Reading hotspot-only files with no findings — high churn does not mean needs review right now
- Trusting score >= 80 as "safe to skip" — dangerous (per-check caps hide warnings)
- Skipping new files because CKB did not flag them — CKB has no SCIP data for new files
- Reading every new file in a large new package — read entry point + types first, then follow refs
- Ignoring reviewEffort/prTier — these tell you how thorough to be
`

// ckbAuditSkill is the embedded /ckb-audit slash command for Claude Code.
const ckbAuditSkill = `Run a CKB-augmented compliance audit optimized for minimal token usage.

## Input
$ARGUMENTS - Optional: framework(s) to audit (default: auto-detect from repo context). Examples: "gdpr", "gdpr,pci-dss,hipaa", "all"

## Philosophy

CKB already ran deterministic checks across 20 regulatory frameworks, mapped every finding
to a specific regulation article, and assigned confidence scores. The LLM's job is ONLY what
CKB can't do: assess whether findings are real compliance risks or false positives given the
repo's actual purpose, and prioritize remediation by business impact.

### Available frameworks (20 total)

**Privacy:** gdpr, ccpa, iso27701
**AI:** eu-ai-act
**Security:** iso27001, nist-800-53, owasp-asvs, soc2, hipaa
**Industry:** pci-dss, dora, nis2, fda-21cfr11, eu-cra
**Supply chain:** sbom-slsa
**Safety:** iec61508, iso26262, do-178c
**Coding:** misra, iec62443

### CKB's blind spots (what the LLM must catch)

CKB maps code patterns to regulation articles using AST + regex + tree-sitter. It is
structurally correct but contextually blind:

- **Business context**: CKB flags PII patterns in a healthcare app and a game engine equally
- **Architecture awareness**: a finding in dead/test code vs production code has different weight
- **Compensating controls**: CKB can't see infrastructure-level encryption, WAFs, or IAM policies
- **Regulatory applicability**: CKB flags HIPAA in a repo that doesn't handle PHI
- **Risk prioritization**: 50 findings need ordering by actual business/legal exposure
- **Cross-reference noise**: the same hardcoded credential maps to 6 frameworks — that's 1 fix, not 6

## Phase 1: Structural scan (~2k tokens into context)

` + "```" + `bash
ckb audit compliance --framework=$ARGUMENTS --format=json --min-confidence=0.7 2>/dev/null
` + "```" + `

For large repos, scope to a specific path to reduce noise:
` + "```" + `bash
ckb audit compliance --framework=$ARGUMENTS --scope=src/api --format=json --min-confidence=0.7 2>/dev/null
` + "```" + `

If no framework specified, pick based on repo context:
- Has health/patient/medical code — hipaa,gdpr
- Has payment/billing/card code — pci-dss,soc2
- EU company or processes EU data — gdpr,dora,nis2
- AI/ML code — eu-ai-act
- Safety-critical/embedded — iec61508,iso26262,misra
- General SaaS — iso27001,soc2,owasp-asvs
- If unsure — iso27001,owasp-asvs (broadest applicability)

From the JSON output, extract:
- ` + "`" + `score` + "`" + `, ` + "`" + `verdict` + "`" + ` (pass/warn/fail)
- ` + "`" + `coverage[]` + "`" + ` — per-framework scores with passed/warned/failed/skipped check counts
- ` + "`" + `findings[]` + "`" + ` — with check, severity, file, startLine, message, suggestion, confidence, CWE
- ` + "`" + `checks[]` + "`" + ` — per-check status and summary
- ` + "`" + `summary` + "`" + ` — total findings by severity, files scanned

Note:
- **Per-framework scores**: which frameworks are clean vs problematic
- **Finding count by severity**: errors are your priority
- **CWE references**: cross-reference with known vulnerability databases
- **Confidence scores**: low confidence (< 0.7) findings are likely false positives

**Early exit**: If verdict=pass and all framework scores >= 90, write a one-line summary and stop.

## Phase 2: Triage findings (targeted reads only)

Do NOT read every flagged file. Group findings by root cause first:

1. **Deduplicate cross-framework findings** — a hardcoded secret flagged by GDPR, PCI DSS, HIPAA, and ISO 27001 is one fix
2. **Check for dominant category** — if > 50% of findings are one category (e.g., "sql-injection"), investigate that category systemically rather than checking each file individually
3. **Check applicability** — does this repo actually fall under the flagged framework? (e.g., HIPAA findings in a non-healthcare repo)
4. **Read only error-severity files** — warnings and info can wait
5. **For each error finding**, read just the flagged lines (not the whole file) and assess:
   - Is this a real compliance risk or a pattern false positive?
   - Are there compensating controls elsewhere? (check imports, config, middleware)
   - What's the remediation effort: one-liner fix vs architectural change?

## Phase 3: Write the audit summary (be terse)

` + "```" + `markdown
## [COMPLIANT|NEEDS REMEDIATION|NON-COMPLIANT] — CKB score: [N]/100

[One sentence: what frameworks were audited and overall posture]

### Critical findings (must remediate)
1. **[framework]** ` + "`" + `file:line` + "`" + ` Art. [X] — [issue + remediation in one sentence]
2. ...

### Not applicable (false positives from context)
[List findings CKB flagged but that don't apply to this repo, with one-line reason]

### Cross-framework deduplication
[N findings deduplicated to M root causes]

### Framework scores
| Framework | Score | Status | Checks |
|-----------|-------|--------|--------|
| [name]    | [N]   | [pass/warn/fail] | [passed]/[total] |
` + "```" + `

If fully compliant: just the header + framework scores. Nothing else.

## Anti-patterns (token waste)

- Reading every flagged file — waste (group by root cause, read only errors)
- Treating cross-framework duplicates as separate issues — waste (1 code fix = 1 issue)
- Explaining what each regulation requires — waste (CKB already mapped articles)
- Re-checking frameworks CKB scored at 100 — waste
- Auditing frameworks that don't apply to this repo — waste
- Reading low-confidence findings (< 0.7) — waste (likely false positives)
- Suggesting infrastructure controls for code-level findings — out of scope
- Using wrong framework IDs (use pci-dss not pcidss, owasp-asvs not owaspasvs) — CKB error
`

func configureVSCodeGlobal(ckbCommand string, ckbArgs []string) error {
	// Check if code command is available
	if _, err := exec.LookPath("code"); err != nil {
		return fmt.Errorf("VS Code CLI (code) not found. Please ensure VS Code is installed and 'code' is in your PATH")
	}

	newCommand := formatCommand(ckbCommand, ckbArgs)

	// Check existing config before calling CLI
	existing, _ := getVSCodeGlobalMcpConfig()
	if existing != nil {
		existingCommand := formatCommand(existing.Command, existing.Args)

		if existingCommand == newCommand {
			fmt.Println("CKB is already configured correctly.")
			fmt.Printf("  Command: %s\n", existingCommand)
			return nil
		}

		// Different config - warn and proceed (code --add-mcp overwrites)
		fmt.Println("CKB is already configured with a different path:")
		fmt.Printf("  Current:  %s\n", existingCommand)
		fmt.Printf("  New:      %s\n", newCommand)

		if isNpxCommand(existing.Command) && !isNpxCommand(ckbCommand) {
			fmt.Println("\n  Note: Switching from npx to local binary.")
		} else if !isNpxCommand(existing.Command) && isNpxCommand(ckbCommand) {
			fmt.Println("\n  Note: Switching from local binary to npx.")
		}

		fmt.Println("\nUpdating configuration...")
	}

	// Build the MCP server JSON
	serverConfig := map[string]any{
		"name":    "ckb",
		"type":    "stdio",
		"command": ckbCommand,
		"args":    ckbArgs,
	}

	jsonBytes, err := json.Marshal(serverConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal server config: %w", err)
	}

	fmt.Printf("Running: code --add-mcp '%s'\n", string(jsonBytes))

	execCmd := exec.Command("code", "--add-mcp", string(jsonBytes)) // #nosec G204 //nolint:gosec // hardcoded command, jsonBytes is trusted config
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	if err = execCmd.Run(); err != nil {
		return fmt.Errorf("failed to add CKB to VS Code: %w", err)
	}

	fmt.Println("\n✓ CKB added to VS Code globally.")
	fmt.Println("Restart VS Code to load the new configuration.")

	return nil
}

func isClaudeAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// claudeConfigEntry represents an MCP server entry in Claude's config
type claudeConfigEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// getClaudeMcpConfig reads the existing ckb MCP config from ~/.claude.json
func getClaudeMcpConfig() (*claudeConfigEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(home, ".claude.json")
	data, err := os.ReadFile(configPath) // #nosec G703 -- path is internally constructed
	if err != nil {
		return nil, err // File doesn't exist or can't read
	}

	var config struct {
		McpServers map[string]claudeConfigEntry `json:"mcpServers"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	if entry, ok := config.McpServers["ckb"]; ok {
		return &entry, nil
	}
	return nil, nil // Not configured
}

// getGrokMcpConfig reads the existing ckb MCP config from ~/.grok/user-settings.json
func getGrokMcpConfig() (*grokMcpEntry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(home, ".grok", "user-settings.json")
	data, err := os.ReadFile(configPath) // #nosec G703 -- path is internally constructed
	if err != nil {
		return nil, err // File doesn't exist or can't read
	}

	var raw map[string]json.RawMessage
	if err = json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	mcpServersRaw, ok := raw["mcpServers"]
	if !ok {
		return nil, nil
	}

	var mcpServers map[string]grokMcpEntry
	if err = json.Unmarshal(mcpServersRaw, &mcpServers); err != nil {
		return nil, err
	}

	if entry, ok := mcpServers["ckb"]; ok {
		return &entry, nil
	}
	return nil, nil // Not configured
}

// vsCodeMcpEntry represents an MCP server entry in VS Code's user settings
type vsCodeMcpEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// getVSCodeGlobalMcpConfig reads the existing ckb MCP config from VS Code's user settings.json
func getVSCodeGlobalMcpConfig() (*vsCodeMcpEntry, error) {
	var settingsPath string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		settingsPath = filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json")
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		settingsPath = filepath.Join(home, ".config", "Code", "User", "settings.json")
	case "windows":
		settingsPath = filepath.Join(os.Getenv("APPDATA"), "Code", "User", "settings.json")
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	data, err := os.ReadFile(settingsPath) // #nosec G703 -- path is internally constructed
	if err != nil {
		return nil, err // File doesn't exist or can't read
	}

	var raw map[string]json.RawMessage
	if err = json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	mcpRaw, ok := raw["mcp"]
	if !ok {
		return nil, nil
	}

	var mcpSection map[string]json.RawMessage
	if err = json.Unmarshal(mcpRaw, &mcpSection); err != nil {
		return nil, err
	}

	serversRaw, ok := mcpSection["servers"]
	if !ok {
		return nil, nil
	}

	var servers map[string]vsCodeMcpEntry
	if err = json.Unmarshal(serversRaw, &servers); err != nil {
		return nil, err
	}

	if entry, ok := servers["ckb"]; ok {
		return &entry, nil
	}
	return nil, nil // Not configured
}

// formatCommand returns a human-readable command string
func formatCommand(command string, args []string) string {
	if len(args) > 0 {
		return command + " " + strings.Join(args, " ")
	}
	return command
}

// isNpxCommand checks if a command is using npx
func isNpxCommand(command string) bool {
	return command == "npx" || strings.HasSuffix(command, "/npx")
}

// claudeMcpAdd adds ckb to Claude Code, handling the case where it already exists.
// Returns (changed, error) where changed indicates if the config was modified.
func claudeMcpAdd(ckbCommand string, ckbArgs []string) (bool, error) {
	// Check existing config first
	existing, _ := getClaudeMcpConfig()

	newCommand := formatCommand(ckbCommand, ckbArgs)

	if existing != nil {
		existingCommand := formatCommand(existing.Command, existing.Args)

		// Check if already configured with the same command
		if existingCommand == newCommand {
			fmt.Println("CKB is already configured correctly.")
			fmt.Printf("  Command: %s\n", existingCommand)
			return false, nil
		}

		// Different paths - warn and update
		fmt.Println("CKB is already configured with a different path:")
		fmt.Printf("  Current:  %s\n", existingCommand)
		fmt.Printf("  New:      %s\n", newCommand)

		// Extra warning for npx vs binary mismatch
		if isNpxCommand(existing.Command) && !isNpxCommand(ckbCommand) {
			fmt.Println("\n  Note: Switching from npx to local binary.")
		} else if !isNpxCommand(existing.Command) && isNpxCommand(ckbCommand) {
			fmt.Println("\n  Note: Switching from local binary to npx.")
		}

		fmt.Println("\nUpdating configuration...")

		// Remove existing entry
		removeCmd := exec.Command("claude", "mcp", "remove", "ckb", "--scope", "user")
		removeCmd.Stdout = os.Stdout
		removeCmd.Stderr = os.Stderr
		if err := removeCmd.Run(); err != nil {
			return false, fmt.Errorf("failed to remove existing CKB config: %w", err)
		}
	}

	// Add new entry
	cmdArgs := []string{"mcp", "add", "--transport", "stdio", "ckb", "--scope", "user", "--"}
	cmdArgs = append(cmdArgs, ckbCommand)
	cmdArgs = append(cmdArgs, ckbArgs...)

	execCmd := exec.Command("claude", cmdArgs...)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	if err := execCmd.Run(); err != nil {
		return false, fmt.Errorf("failed to add CKB to Claude: %w", err)
	}

	return true, nil
}

func formatArgs(args []string) string {
	result := ""
	for i, arg := range args {
		if i > 0 {
			result += " "
		}
		result += arg
	}
	return result
}
