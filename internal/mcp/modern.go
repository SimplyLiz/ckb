package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SimplyLiz/CodeMCP/internal/repos"
)

// MCP revision 2026-07-28 ("modern") next to the initialize-based revisions
// ("legacy"). A request is modern when its params._meta names a protocol
// version, or when it is server/discover; everything else takes the legacy
// path unchanged. Spec: https://modelcontextprotocol.io/specification/2026-07-28
//
// The revision forbids state that spans requests on a connection: "Servers
// MUST NOT rely on prior requests over the same connection to establish
// context", and a stdio process does not count as a session. Two legacy
// features are exactly that, and a modern client does not get them:
//
//   - expandToolset changes what tools/list returns, which tools/list "MUST NOT"
//     do as a side effect of other requests. Modern tools/list is the preset
//     the process was started with (--preset); a wider set means a restart.
//   - switchProject and switchRepo change which repo later calls run against.
//     Modern repo-bound tools take an optional `repo` argument instead, and
//     each call starts from the repo the process was started in.
const modernProtocolVersion = "2026-07-28"

const (
	metaKeyProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaKeyClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaKeyClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaKeyServerInfo         = "io.modelcontextprotocol/serverInfo"
	metaKeySubscriptionID     = "io.modelcontextprotocol/subscriptionId"
)

// UnsupportedProtocolVersion is the 2026-07-28 error for a request whose
// _meta names a revision this server does not speak.
const UnsupportedProtocolVersion = -32022

// modernListTTLMs is the cache lifetime given for tools/list and
// resources/list. Both are fixed for the life of the process, so the number
// only bounds how long a client keeps a list from a server that was
// restarted with another preset.
const modernListTTLMs = 3600000

// connectionStateTools exist only for legacy clients.
var connectionStateTools = map[string]bool{
	"expandToolset": true,
	"switchProject": true,
	"switchRepo":    true,
}

// repoFreeTools do not run against a repository's index, so the modern
// schema does not offer them a `repo` argument.
func repoFreeTool(name string) bool {
	if strings.HasPrefix(name, "federation") {
		return true
	}
	switch name {
	case "listRepos", "getActiveRepo", "listFederations",
		"daemonStatus", "listSchedules", "runSchedule",
		"listWebhooks", "testWebhook", "webhookDeliveries":
		return true
	}
	return false
}

const repoArgDescription = "Repository to run against: a path (absolute, or relative to the repo CKB was started in) or a name from 'ckb repo list'. Default: the repo CKB was started in, or the repo of the file paths in the other arguments."

// modernRequest reports whether msg uses the 2026-07-28 request shape.
func modernRequest(msg *MCPMessage) bool {
	if msg.Method == "server/discover" {
		return true
	}
	_, ok := requestMeta(msg)[metaKeyProtocolVersion]
	return ok
}

func requestMeta(msg *MCPMessage) map[string]interface{} {
	params, _ := msg.Params.(map[string]interface{})
	meta, _ := params["_meta"].(map[string]interface{})
	return meta
}

// handleModernRequest answers a 2026-07-28 request. It never reads or leaves
// behind state another request depends on.
func (s *MCPServer) handleModernRequest(msg *MCPMessage) *MCPMessage {
	meta := requestMeta(msg)
	version, _ := meta[metaKeyProtocolVersion].(string)
	if version == "" {
		return NewErrorMessage(msg.Id, InvalidParams,
			"Missing _meta[\""+metaKeyProtocolVersion+"\"]", nil)
	}
	if version != modernProtocolVersion {
		return NewErrorMessage(msg.Id, UnsupportedProtocolVersion,
			fmt.Sprintf("Unsupported protocol version %q", version),
			map[string]interface{}{
				"supported": []string{modernProtocolVersion},
				"requested": version,
			})
	}
	if _, ok := meta[metaKeyClientCapabilities].(map[string]interface{}); !ok {
		return NewErrorMessage(msg.Id, InvalidParams,
			"Missing _meta[\""+metaKeyClientCapabilities+"\"]", nil)
	}
	s.noteModernClient(meta)

	params, _ := msg.Params.(map[string]interface{})
	if params == nil {
		params = map[string]interface{}{}
	}

	var result map[string]interface{}
	var errMsg *MCPMessage
	switch msg.Method {
	case "server/discover":
		result = s.modernDiscover()
	case "tools/list":
		result, errMsg = s.modernListTools(msg, params)
	case "tools/call":
		result, errMsg = s.modernCallTool(msg, params)
	case "resources/list":
		result, errMsg = s.modernListResources(msg, params)
	case "resources/read":
		if r := s.handleReadResourceRequest(msg); r.Error != nil {
			errMsg = r
		} else {
			result, _ = r.Result.(map[string]interface{})
		}
	case "subscriptions/listen":
		return s.modernListen(msg)
	default:
		// initialize, ping and the other removed methods land here too.
		return NewErrorMessage(msg.Id, MethodNotFound,
			fmt.Sprintf("Method not found in MCP %s: %s", modernProtocolVersion, msg.Method), nil)
	}
	if errMsg != nil {
		return errMsg
	}
	return NewResultMessage(msg.Id, s.completeResult(result))
}

// completeResult adds what every 2026-07-28 result carries.
func (s *MCPServer) completeResult(result map[string]interface{}) map[string]interface{} {
	if result == nil {
		result = map[string]interface{}{}
	}
	result["resultType"] = "complete"
	meta, _ := result["_meta"].(map[string]interface{})
	if meta == nil {
		meta = map[string]interface{}{}
	}
	meta[metaKeyServerInfo] = ServerInfo{Name: "ckb", Version: s.version}
	result["_meta"] = meta
	return result
}

// noteModernClient keeps the client name for the activity ledger's
// "consumer" column. It is bookkeeping, not context for later requests.
func (s *MCPServer) noteModernClient(meta map[string]interface{}) {
	info, _ := meta[metaKeyClientInfo].(map[string]interface{})
	name, _ := info["name"].(string)
	if name == "" {
		return
	}
	version, _ := info["version"].(string)
	s.mu.Lock()
	s.clientName, s.clientVersion = name, version
	s.mu.Unlock()
}

func (s *MCPServer) modernDiscover() map[string]interface{} {
	return map[string]interface{}{
		"supportedVersions": []string{modernProtocolVersion},
		"capabilities": ServerCapabilities{
			Tools:     &ToolsCapability{},
			Resources: &ResourcesCapability{},
		},
		"instructions": modernInstructionsText(),
		"ttlMs":        modernListTTLMs,
		"cacheScope":   "public",
	}
}

func (s *MCPServer) modernListTools(msg *MCPMessage, params map[string]interface{}) (map[string]interface{}, *MCPMessage) {
	raw, err := s.handleListTools(params)
	if err != nil {
		return nil, NewErrorMessage(msg.Id, InvalidParams, err.Error(), nil)
	}
	result := raw.(map[string]interface{})
	tools, _ := result["tools"].([]Tool)
	out := make([]Tool, 0, len(tools))
	for _, t := range tools {
		if connectionStateTools[t.Name] {
			continue
		}
		if !repoFreeTool(t.Name) {
			t.InputSchema = withRepoArg(t.InputSchema)
		}
		out = append(out, t)
	}
	result["tools"] = out
	result["ttlMs"] = modernListTTLMs
	result["cacheScope"] = "public"
	return result, nil
}

// withRepoArg returns a copy of schema with an optional `repo` property.
// The definitions are shared, so the original is not touched.
func withRepoArg(schema map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(schema)+1)
	for k, v := range schema {
		out[k] = v
	}
	props := map[string]interface{}{}
	if old, ok := schema["properties"].(map[string]interface{}); ok {
		for k, v := range old {
			props[k] = v
		}
	}
	if _, taken := props["repo"]; !taken {
		props["repo"] = map[string]interface{}{"type": "string", "description": repoArgDescription}
	}
	out["properties"] = props
	if out["type"] == nil {
		out["type"] = "object"
	}
	return out
}

func (s *MCPServer) modernCallTool(msg *MCPMessage, params map[string]interface{}) (map[string]interface{}, *MCPMessage) {
	name, _ := params["name"].(string)
	if connectionStateTools[name] {
		return nil, NewErrorMessage(msg.Id, InvalidParams, fmt.Sprintf(
			"Tool %s is not available in MCP %s: it changes state for later requests. "+
				"Pass `repo` to a tool to pick a repository; start CKB with --preset=review or --preset=full for more tools.",
			name, modernProtocolVersion), nil)
	}

	// Every call starts from the repo the process was started in, so a path
	// hint in one call cannot redirect the next.
	home := s.homeRepoRoot()
	args, _ := params["arguments"].(map[string]interface{})
	target := home
	if repoArg, ok := args["repo"].(string); ok && repoArg != "" && !repoFreeTool(name) {
		root, err := resolveRepoArg(repoArg, home)
		if err != nil {
			return toolErrorResult(err), nil
		}
		target = root
		// The handlers do not know the argument; take it out so none
		// mistakes it for one of their own.
		trimmed := make(map[string]interface{}, len(args))
		for k, v := range args {
			if k != "repo" {
				trimmed[k] = v
			}
		}
		call := make(map[string]interface{}, len(params))
		for k, v := range params {
			call[k] = v
		}
		call["arguments"] = trimmed
		params = call
	}
	if target != "" {
		if err := s.ensureActiveEngine(target); err != nil {
			return toolErrorResult(fmt.Errorf("cannot open repository %s: %w", target, err)), nil
		}
	}

	r := s.handleCallToolRequest(&MCPMessage{Jsonrpc: "2.0", Id: msg.Id, Method: "tools/call", Params: params})
	if r.Error != nil {
		return nil, r
	}
	result, _ := r.Result.(map[string]interface{})
	return result, nil
}

// homeRepoRoot is the repository the process was started in. It is fixed
// the first time it is asked for, which happens before any modern call
// switches the engine.
func (s *MCPServer) homeRepoRoot() string {
	s.mu.RLock()
	home := s.homeRepoPath
	s.mu.RUnlock()
	if home != "" {
		return home
	}
	eng := s.engine()
	if eng == nil {
		return ""
	}
	home = normalizePath(eng.GetRepoRoot())
	s.mu.Lock()
	if s.homeRepoPath == "" {
		s.homeRepoPath = home
	}
	home = s.homeRepoPath
	s.mu.Unlock()
	return home
}

// resolveRepoArg turns a `repo` argument into a repository root: a name from
// the registry, or a path, absolute or relative to home.
func resolveRepoArg(arg, home string) (string, error) {
	if !strings.ContainsAny(arg, `/\`) && arg != "." && arg != ".." {
		if registry, err := repos.LoadRegistry(); err == nil {
			if entry, state, getErr := registry.Get(arg); getErr == nil {
				switch state {
				case repos.RepoStateMissing:
					return "", fmt.Errorf("repo %s: path does not exist: %s", arg, entry.Path)
				case repos.RepoStateUninitialized:
					return "", fmt.Errorf("repo %s is not initialized: run 'ckb init' in %s", arg, entry.Path)
				}
				return entry.Path, nil
			}
		}
	}
	path := arg
	if !filepath.IsAbs(path) {
		if home == "" {
			return "", fmt.Errorf("repo %q is relative and CKB has no start repository", arg)
		}
		path = filepath.Join(home, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("repo %s: %w", arg, err)
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	root := repos.FindGitRoot(path)
	if root == "" {
		return "", fmt.Errorf("repo %s is not inside a git repository", arg)
	}
	if _, err := os.Stat(filepath.Join(root, ".ckb")); err != nil {
		return "", fmt.Errorf("CKB is not initialized for %s: run 'ckb init' there", root)
	}
	return root, nil
}

func toolErrorResult(err error) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}

func (s *MCPServer) modernListResources(msg *MCPMessage, params map[string]interface{}) (map[string]interface{}, *MCPMessage) {
	r := s.handleListResourcesRequest(&MCPMessage{Jsonrpc: "2.0", Id: msg.Id, Method: msg.Method, Params: params})
	if r.Error != nil {
		return nil, r
	}
	result, _ := r.Result.(map[string]interface{})
	result["ttlMs"] = modernListTTLMs
	result["cacheScope"] = "public"
	return result, nil
}

// modernListen answers subscriptions/listen. Nothing this server lists
// changes during a modern session (the tool list is fixed at start), so
// there is nothing to deliver: it acknowledges, as the spec requires first,
// and ends the subscription at once with a complete result.
func (s *MCPServer) modernListen(msg *MCPMessage) *MCPMessage {
	ack := NewNotificationMessage("notifications/subscriptions/acknowledged", map[string]interface{}{
		"_meta": map[string]interface{}{metaKeySubscriptionID: msg.Id},
	})
	if err := s.writeMessage(ack); err != nil {
		s.logger.Warn("Failed to acknowledge subscription", "error", err.Error())
	}
	return NewResultMessage(msg.Id, s.completeResult(nil))
}
