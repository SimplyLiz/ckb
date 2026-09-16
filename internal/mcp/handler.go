package mcp

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/errors"
)

// handleMessage processes an incoming MCP message and returns a response
func (s *MCPServer) handleMessage(msg *MCPMessage) *MCPMessage {
	// Handle responses (from client to our requests, e.g., roots/list)
	if msg.IsResponse() {
		s.handleResponse(msg)
		return nil
	}

	// Handle requests
	if msg.IsRequest() {
		return s.handleRequest(msg)
	}

	// Handle notifications (no response needed)
	if msg.IsNotification() {
		s.handleNotification(msg)
		return nil
	}

	// Invalid message
	return NewErrorMessage(msg.Id, InvalidRequest, "Invalid message: not a request or notification", nil)
}

// handleRequest handles a JSON-RPC request
func (s *MCPServer) handleRequest(msg *MCPMessage) *MCPMessage {
	s.logger.Debug("Handling request",
		"method", msg.Method,
		"id", msg.Id,
	)

	switch msg.Method {
	case "initialize":
		return s.handleInitializeRequest(msg)
	case "tools/list":
		return s.handleListToolsRequest(msg)
	case "tools/call":
		return s.handleCallToolRequest(msg)
	case "resources/list":
		return s.handleListResourcesRequest(msg)
	case "resources/read":
		return s.handleReadResourceRequest(msg)
	default:
		return NewErrorMessage(msg.Id, MethodNotFound, fmt.Sprintf("Method not found: %s", msg.Method), nil)
	}
}

// handleNotification handles a JSON-RPC notification
func (s *MCPServer) handleNotification(msg *MCPMessage) {
	s.logger.Debug("Handling notification",
		"method", msg.Method,
	)

	// Handle specific notifications if needed
	switch msg.Method {
	case "notifications/initialized":
		s.logger.Info("Client initialized")
		// v8.0: Request roots if client supports it
		if s.roots.IsClientSupported() {
			s.requestRoots()
		}
	case "notifications/roots/list_changed":
		// v8.0: Client is notifying us that roots have changed
		s.logger.Info("Client roots changed, requesting update")
		if s.roots.IsClientSupported() {
			s.requestRoots()
		}
	default:
		s.logger.Debug("Unknown notification",
			"method", msg.Method,
		)
	}
}

// handleResponse handles a JSON-RPC response (from client to our requests)
func (s *MCPServer) handleResponse(msg *MCPMessage) {
	// Extract the request ID
	var id int64
	switch v := msg.Id.(type) {
	case float64:
		id = int64(v)
	case int64:
		id = v
	case int:
		id = int64(v)
	default:
		s.logger.Warn("Received response with non-numeric ID",
			"id", msg.Id,
		)
		return
	}

	// Try to resolve a pending request
	if s.roots.ResolvePendingRequest(id, msg) {
		s.logger.Debug("Resolved pending request",
			"id", id,
		)
		return
	}

	s.logger.Warn("Received response for unknown request",
		"id", id,
	)
}

// requestRoots sends a roots/list request to the client
func (s *MCPServer) requestRoots() {
	id := s.roots.NextRequestID()
	responseCh := s.roots.RegisterPendingRequest(id)

	// Send the request
	request := &MCPMessage{
		Jsonrpc: "2.0",
		Id:      id,
		Method:  "roots/list",
	}

	if err := s.writeMessage(request); err != nil {
		s.logger.Error("Failed to send roots/list request",
			"error", err.Error(),
		)
		s.roots.CancelPendingRequest(id)
		return
	}

	s.logger.Debug("Sent roots/list request",
		"id", id,
	)

	// Handle response in a goroutine to not block the message loop
	go func() {
		select {
		case msg, ok := <-responseCh:
			if !ok {
				// Channel was closed (timeout or shutdown)
				return
			}

			// Check for error response
			if msg.Error != nil {
				// Handle -32601 Method Not Found specially
				if msg.Error.Code == MethodNotFound {
					// Client doesn't actually support roots despite capability
					s.roots.SetClientSupported(false)
					s.logger.Info("Client does not support roots/list, disabling roots feature")
				} else {
					s.logger.Warn("roots/list request failed",
						"code", msg.Error.Code,
						"error", msg.Error.Message,
					)
				}
				return
			}

			// Parse roots from result
			roots := parseRootsResponse(msg.Result)
			if roots == nil {
				s.logger.Warn("Failed to parse roots/list response")
				return
			}

			s.roots.SetRoots(roots)
			s.logger.Info("Updated roots from client",
				"count", len(roots),
			)

			// Log individual roots for debugging
			for _, root := range roots {
				s.logger.Debug("Root",
					"uri", root.URI,
					"name", root.Name,
					"path", root.Path(),
				)
			}

			// v8.0: Switch to client's root if it differs from current repo
			// This fixes repo confusion when using a binary from a different location
			if len(roots) > 0 {
				clientRoot := roots[0].Path()
				s.switchToClientRoot(clientRoot)
			}

		case <-time.After(rootsRequestTimeout):
			// Timeout - cancel the pending request and log
			s.roots.CancelPendingRequest(id)
			s.logger.Warn("roots/list request timed out",
				"timeout", rootsRequestTimeout.String(),
			)
		}
	}()
}

// handleInitializeRequest handles the initialize request
func (s *MCPServer) handleInitializeRequest(msg *MCPMessage) *MCPMessage {
	params, ok := msg.Params.(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
	}

	result, err := s.handleInitialize(params)
	if err != nil {
		return NewErrorMessage(msg.Id, InternalError, err.Error(), nil)
	}

	return NewResultMessage(msg.Id, result)
}

// handleListToolsRequest handles the tools/list request
func (s *MCPServer) handleListToolsRequest(msg *MCPMessage) *MCPMessage {
	params, ok := msg.Params.(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
	}

	result, err := s.handleListTools(params)
	if err != nil {
		return NewErrorMessage(msg.Id, InternalError, err.Error(), nil)
	}

	return NewResultMessage(msg.Id, result)
}

// handleCallToolRequest handles the tools/call request
func (s *MCPServer) handleCallToolRequest(msg *MCPMessage) *MCPMessage {
	params, ok := msg.Params.(map[string]interface{})
	if !ok {
		return NewErrorMessage(msg.Id, InvalidParams, "Invalid params: expected object", nil)
	}

	result, err := s.handleCallTool(params)
	if err != nil {
		return NewErrorMessage(msg.Id, InternalError, err.Error(), nil)
	}

	return NewResultMessage(msg.Id, result)
}

// handleListResourcesRequest handles the resources/list request
func (s *MCPServer) handleListResourcesRequest(msg *MCPMessage) *MCPMessage {
	params, ok := msg.Params.(map[string]interface{})
	if !ok {
		params = make(map[string]interface{})
	}

	result, err := s.handleListResources(params)
	if err != nil {
		return NewErrorMessage(msg.Id, InternalError, err.Error(), nil)
	}

	return NewResultMessage(msg.Id, result)
}

// handleReadResourceRequest handles the resources/read request
func (s *MCPServer) handleReadResourceRequest(msg *MCPMessage) *MCPMessage {
	params, ok := msg.Params.(map[string]interface{})
	if !ok {
		return NewErrorMessage(msg.Id, InvalidParams, "Invalid params: expected object", nil)
	}

	result, err := s.handleReadResource(params)
	if err != nil {
		return NewErrorMessage(msg.Id, InternalError, err.Error(), nil)
	}

	return NewResultMessage(msg.Id, result)
}

// handleListTools returns the list of available tools with pagination support.
// MCP spec: cursor-based pagination for tools/list.
// Page 1 always contains the complete core toolset + expandToolset meta-tool.
func (s *MCPServer) handleListTools(params map[string]interface{}) (interface{}, error) {
	// Extract cursor from params (optional)
	var cursor string
	if c, ok := params["cursor"].(string); ok {
		cursor = c
	}

	// Get current state
	preset := s.GetActivePreset()
	toolsetHash := s.GetToolsetHash()

	// Decode and validate cursor
	offset, err := DecodeToolsCursor(cursor, preset, toolsetHash)
	if err != nil {
		// Return MCP error code -32602 (Invalid params) for invalid cursor
		return nil, errors.NewInvalidParameterError("cursor", err.Error())
	}

	// Get filtered and ordered tools
	filteredTools := s.GetFilteredTools()

	// Paginate
	pageTools, nextCursor, err := PaginateTools(filteredTools, offset, DefaultPageSize, preset, toolsetHash)
	if err != nil {
		return nil, err
	}

	// Build response
	result := map[string]interface{}{
		"tools": pageTools,
	}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	}

	return result, nil
}

// handleCallTool executes a tool
func (s *MCPServer) handleCallTool(params map[string]interface{}) (interface{}, error) {
	toolName, ok := params["name"].(string)
	if !ok {
		return nil, errors.NewInvalidParameterError("name", "")
	}

	toolParams, ok := params["arguments"].(map[string]interface{})
	if !ok {
		toolParams = make(map[string]interface{})
	}

	handler, exists := s.tools[toolName]
	if !exists {
		return nil, errors.NewResourceNotFoundError("tool", toolName)
	}

	s.logger.Info("Calling tool",
		"tool", toolName,
		"params", toolParams,
	)

	// v8.1: Auto-resolve active repository from file paths in params
	if pathHint := extractPathHint(toolParams); pathHint != "" {
		if repoRoot := s.resolveRepoForPath(pathHint); repoRoot != "" {
			_ = s.ensureActiveEngine(repoRoot)
		}
	}

	// v8.0: Check for streaming request
	if streamResp, err := s.wrapForStreaming(toolName, toolParams); streamResp != nil || err != nil {
		if err != nil {
			errResp := envelope.New().Data(nil).Error(err).Build()
			jsonBytes, _ := json.Marshal(errResp)
			return map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": string(jsonBytes),
					},
				},
			}, nil
		}
		// Return streaming initial response
		jsonBytes, err := json.Marshal(streamResp)
		if err != nil {
			return nil, errors.NewOperationError("marshal streaming response", err)
		}
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(jsonBytes),
				},
			},
		}, nil
	}

	toolStart := time.Now()
	result, err := handler(toolParams)
	if err != nil {
		// Wrap error in envelope format
		errResp := envelope.New().Data(nil).Error(err).Build()
		jsonBytes, _ := json.Marshal(errResp)
		s.recordActivity(toolName, toolParams, toolStart, jsonBytes, nil, err)
		return map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(jsonBytes),
				},
			},
		}, nil
	}

	// Marshal the envelope response to JSON
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return nil, errors.NewOperationError("marshal response", err)
	}

	s.recordActivity(toolName, toolParams, toolStart, jsonBytes, result, nil)

	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(jsonBytes),
			},
		},
	}, nil
}

// handleListResources returns the list of available resources
func (s *MCPServer) handleListResources(params map[string]interface{}) (interface{}, error) {
	resources, templates := s.GetResourceDefinitions()
	return map[string]interface{}{
		"resources":         resources,
		"resourceTemplates": templates,
	}, nil
}

// handleReadResource reads a resource by URI
func (s *MCPServer) handleReadResource(params map[string]interface{}) (interface{}, error) {
	uri, ok := params["uri"].(string)
	if !ok {
		return nil, errors.NewInvalidParameterError("uri", "")
	}

	s.logger.Info("Reading resource",
		"uri", uri,
	)

	result, err := s.handleResourceRead(uri)
	if err != nil {
		return nil, errors.NewOperationError("resource read", err)
	}

	return map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"uri":      uri,
				"mimeType": "application/json",
				"text":     fmt.Sprintf("%v", result),
			},
		},
	}, nil
}
