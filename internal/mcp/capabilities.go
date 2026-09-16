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
	// per the MCP spec; clients such as Claude Code pass it to the model.
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
	"getStatus",
	"reviewPR",
}

// instructionsCore is the "consumer contract v0" critical path: when to
// reach for CKB and the expandToolset rule. MCP clients such as Codex
// truncate the initialize "instructions" field at 512 characters
// (https://learn.chatgpt.com/ko-KR/docs/extend/mcp), so this block is sized
// to land inside that budget on its own — see
// TestInstructionsCoreFitsIn512Bytes. Everything after it in
// instructionsText is optional detail that may be cut.
//
// Every tool named here (prepareChange, explore, understand, searchSymbols,
// findReferences, expandToolset) is present in *every* preset, including
// the default "core" one — see coreToolOrder in presets.go — so this text
// needs no preset branching to stay accurate.
const instructionsCore = `CKB is code intelligence for this repo: symbol index, parsers, git history ` +
	`(not just grep). Before editing code, call prepareChange for callers, tests, ` +
	`and risk. New to the code, call explore or understand to get oriented. To find ` +
	`a symbol, call searchSymbols, then findReferences with its symbolId for usages. ` +
	`Need a tool outside your current preset: call expandToolset once per session ` +
	`with a preset and reason -- it replaces your active preset, so pick "full" if ` +
	`a task spans areas (e.g. review + refactor).`

// instructionsText builds the InitializeResult.Instructions string.
//
// instructionsCore always comes first (see its doc comment for why). The
// rest names only tools that, like the core block, are present in every
// preset (analyzeImpact, getStatus) — except the reviewPR line, which is the
// one part of this text that is genuinely preset-dependent: reviewPR only
// ships in the "review" and "full" presets, so sessions on any other preset
// are told to expandToolset for it instead of being told to just call it.
func instructionsText(preset string) string {
	reviewLine := `Reviewing a PR: call expandToolset with preset "review" and a reason to get reviewPR, the unified quality gate.`
	if presetHasTool(preset, "reviewPR") {
		reviewLine = `Reviewing a PR: call reviewPR, the unified quality gate (breaking changes, secrets, dead code, test gaps, risk).`
	}

	return fmt.Sprintf(
		instructionsCore+
			"\n\nFor just callers and impact (no test/risk detail), call analyzeImpact instead."+
			"\n\n%s"+
			"\n\nResults come from the index, parsers, and git history; searchSymbols searches text first. "+
			"Check getStatus if the index looks stale or missing. CKB never edits your source files.",
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
