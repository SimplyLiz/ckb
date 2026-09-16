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
	}

	return result, nil
}
