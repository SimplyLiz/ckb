package mcp

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
// reach for CKB, the expandToolset rule, and how to get to reviewPR. MCP
// clients such as Codex truncate the initialize "instructions" field at 512
// characters (https://learn.chatgpt.com/ko-KR/docs/extend/mcp), so this
// block is sized to land inside that budget on its own — see
// TestInstructionsCoreFitsIn512Bytes. Everything after it in
// instructionsText is optional detail that may be cut.
//
// The PR-review sentence is deliberately state-independent: instructions are
// generated once, at initialize (see handleInitialize below), and never
// resent after expandToolset changes the active preset — a session that
// expands mid-conversation is still holding the text handed out at startup.
// So instead of branching on "does the starting preset have reviewPR"
// (which goes stale the moment expandToolset runs, and reads wrong for a
// session that expanded to something other than review/full), the sentence
// covers every reachable state in one line: call reviewPR if it's already
// there; otherwise, if this session hasn't spent its one allowed expansion
// yet, call expandToolset for it; otherwise (already expanded, still no
// reviewPR) expanding again would just be rejected, so the only real move
// left is restarting the server with the right preset.
//
// Every tool named here (prepareChange, searchSymbols, findReferences,
// expandToolset, reviewPR) needs no preset branching to stay accurate:
// prepareChange/searchSymbols/findReferences/expandToolset are present in
// every preset (see coreToolOrder in presets.go), and the reviewPR sentence
// itself is written to hold regardless of preset or expansion state.
const instructionsCore = `CKB is code intelligence for this repo: symbols, parsers, git history ` +
	`(not grep). Before editing, call prepareChange for callers, tests, and risk. ` +
	`Find symbols via searchSymbols, then findReferences with the symbolId. ` +
	`Reviewing a PR: call reviewPR if you have it; otherwise, if not yet expanded, ` +
	`call expandToolset once with preset "review" or "full" and a reason (it replaces ` +
	`your preset); already expanded without reviewPR? Restart CKB with --preset=review.`

// instructionsText builds the InitializeResult.Instructions string.
//
// instructionsCore always comes first (see its doc comment for why) and is
// self-contained: it is not parameterized by preset or expansion state, so
// this function needs no branching to stay accurate as a session's toolset
// changes after initialize. The rest is optional detail, safe to truncate,
// covering tools not named in the core sentence.
func instructionsText() string {
	return instructionsCore +
		"\n\nNew to the code, call explore or understand to get oriented. For just " +
		"callers and impact (no test/risk detail), call analyzeImpact instead. " +
		"Results come from the index, parsers, and git history; searchSymbols " +
		"searches text first. Check getStatus if the index looks stale or missing. " +
		"CKB never edits your source files."
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
		Instructions: instructionsText(),
	}

	return result, nil
}
