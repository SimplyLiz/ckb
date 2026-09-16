package mcp

import "fmt"

// ServerCapabilities represents the capabilities exposed by the MCP server
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
}

// ToolsCapability represents the tools capability
type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourcesCapability represents the resources capability
type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// ServerInfo represents information about the CKB server
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult represents the result of the initialize request
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
	// Instructions is the MCP "consumer contract v0": guidance the client
	// injects into the model's context at session start, telling the agent
	// WHEN to reach for CKB instead of ad hoc grepping/guessing. Optional
	// per the MCP spec (2025-03-26+); supported by Claude Code and Cursor.
	Instructions string `json:"instructions,omitempty"`
}

// instructionToolNames are every tool name referenced by instructionsText.
// Kept as a single source of truth so a test can assert each one is a real,
// registered tool (see capabilities_test.go) — if a tool gets renamed here
// without updating instructionsText, the test catches the drift.
var instructionToolNames = []string{
	"prepareChange",
	"analyzeImpact",
	"explore",
	"understand",
	"searchSymbols",
	"findReferences",
	"expandToolset",
	"reviewPR",
}

// instructionsText builds the InitializeResult.Instructions string.
//
// It only names tools guaranteed to exist in the "core" preset (the default
// for new sessions — see DefaultPreset in presets.go), plus expandToolset
// (always present in every preset) and reviewPR (review preset only, so it
// is framed as something you reach via expandToolset unless the active
// preset already has it). This keeps the text correct regardless of which
// preset a session actually starts in, without needing to special-case every
// preset's tool list.
func instructionsText(preset string) string {
	reviewLine := "Reviewing a diff or PR: call expandToolset(\"review\") to get reviewPR, CKB's unified quality gate (breaking changes, secrets, dead code, test gaps, risk)."
	if presetHasTool(preset, "reviewPR") {
		reviewLine = "Reviewing a diff or PR: call reviewPR for CKB's unified quality gate (breaking changes, secrets, dead code, test gaps, risk)."
	}

	return fmt.Sprintf(
		"CKB is a read-only code-intelligence layer over this repo. Every result carries evidence (call sites, git history) and a confidence score — it's not a guess, so prefer it over assuming.\n\n"+
			"Before editing, refactoring, or deleting code: call prepareChange or analyzeImpact first to see what breaks, who calls it, and which tests cover it.\n\n"+
			"Before working in unfamiliar code or modules: call explore or understand to get oriented instead of reading files cold.\n\n"+
			"To locate a symbol or its usages: call searchSymbols or findReferences — prefer these over grep for semantic questions (they resolve identity across renames and return real call sites, not text matches).\n\n"+
			"%s\n\n"+
			"If a task needs a capability outside your current toolset (PR review, refactoring analysis, docs, ops, federation): call expandToolset once per session with the smallest preset that covers it.",
		reviewLine,
	)
}

// presetHasTool reports whether the given preset exposes toolName.
func presetHasTool(preset, toolName string) bool {
	tools := GetPresetTools(preset)
	if len(tools) == 1 && tools[0] == "*" {
		return true
	}
	for _, t := range tools {
		if t == toolName {
			return true
		}
	}
	return false
}

// handleInitialize handles the initialize request
func (s *MCPServer) handleInitialize(params map[string]interface{}) (*InitializeResult, error) {
	s.logger.Info("MCP server initializing",
		"clientInfo", params["clientInfo"],
	)

	// Capture client identity for the activity ledger's "consumer" field.
	if clientInfo, ok := params["clientInfo"].(map[string]interface{}); ok {
		name, _ := clientInfo["name"].(string)
		version, _ := clientInfo["version"].(string)
		s.mu.Lock()
		s.clientName = name
		s.clientVersion = version
		s.mu.Unlock()
	}

	// Parse client capabilities (v8.0: roots support)
	clientCaps := parseClientCapabilities(params)
	if clientCaps.Roots != nil {
		s.roots.SetClientSupported(true)
		s.roots.SetListChangedEnabled(clientCaps.Roots.ListChanged)
		s.logger.Info("Client supports roots",
			"listChanged", clientCaps.Roots.ListChanged,
		)
	}

	result := &InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities: ServerCapabilities{
			Tools: &ToolsCapability{
				ListChanged: true, // Enables expandToolset dynamic preset expansion
			},
			Resources: &ResourcesCapability{
				Subscribe:   false,
				ListChanged: false,
			},
		},
		ServerInfo: ServerInfo{
			Name:    "ckb",
			Version: s.version,
		},
		Instructions: instructionsText(s.GetActivePreset()),
	}

	return result, nil
}
