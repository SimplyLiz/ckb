package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func modernMeta() map[string]interface{} {
	return map[string]interface{}{
		metaKeyProtocolVersion:    modernProtocolVersion,
		metaKeyClientCapabilities: map[string]interface{}{},
		metaKeyClientInfo:         map[string]interface{}{"name": "test-client", "version": "1"},
	}
}

func modernMsg(method string, params map[string]interface{}) *MCPMessage {
	if params == nil {
		params = map[string]interface{}{}
	}
	params["_meta"] = modernMeta()
	return &MCPMessage{Jsonrpc: "2.0", Id: float64(1), Method: method, Params: params}
}

func resultMap(t *testing.T, resp *MCPMessage) map[string]interface{} {
	t.Helper()
	if resp == nil || resp.Error != nil {
		t.Fatalf("want result, got %+v", resp)
	}
	raw, _ := json.Marshal(resp.Result)
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModernDiscover(t *testing.T) {
	r := resultMap(t, newQuietServer().handleMessage(modernMsg("server/discover", nil)))
	if r["resultType"] != "complete" {
		t.Errorf("resultType = %v", r["resultType"])
	}
	if v, _ := r["supportedVersions"].([]interface{}); len(v) != 1 || v[0] != modernProtocolVersion {
		t.Errorf("supportedVersions = %v", r["supportedVersions"])
	}
	if r["ttlMs"] == nil || r["cacheScope"] == nil {
		t.Error("discover lacks caching hints")
	}
	instr, _ := r["instructions"].(string)
	if strings.Contains(instr, "expandToolset") || !strings.Contains(instr, "assessChange") {
		t.Errorf("modern instructions: %s", instr)
	}
	meta, _ := r["_meta"].(map[string]interface{})
	if meta[metaKeyServerInfo] == nil {
		t.Error("result _meta lacks serverInfo")
	}
}

func TestModernVersionAndCapabilityChecks(t *testing.T) {
	s := newQuietServer()

	old := modernMsg("tools/list", nil)
	old.Params.(map[string]interface{})["_meta"].(map[string]interface{})[metaKeyProtocolVersion] = "2027-01-01"
	resp := s.handleMessage(old)
	if resp.Error == nil || resp.Error.Code != UnsupportedProtocolVersion {
		t.Fatalf("want -32022, got %+v", resp.Error)
	}

	noCaps := modernMsg("tools/list", nil)
	delete(noCaps.Params.(map[string]interface{})["_meta"].(map[string]interface{}), metaKeyClientCapabilities)
	if resp := s.handleMessage(noCaps); resp.Error == nil || resp.Error.Code != InvalidParams {
		t.Fatalf("want -32602 without clientCapabilities, got %+v", resp.Error)
	}

	// server/discover without _meta is a modern request missing its version.
	bare := &MCPMessage{Jsonrpc: "2.0", Id: float64(2), Method: "server/discover"}
	if resp := s.handleMessage(bare); resp.Error == nil || resp.Error.Code != InvalidParams {
		t.Fatalf("want -32602 for bare discover, got %+v", resp.Error)
	}
}

func TestModernRemovedMethods(t *testing.T) {
	s := newQuietServer()
	for _, m := range []string{"ping", "initialize"} {
		if resp := s.handleMessage(modernMsg(m, nil)); resp.Error == nil || resp.Error.Code != MethodNotFound {
			t.Errorf("modern %s: want MethodNotFound, got %+v", m, resp)
		}
	}
	// Legacy ping is unaffected.
	if resp := s.handleMessage(&MCPMessage{Jsonrpc: "2.0", Id: float64(3), Method: "ping"}); resp.Error != nil {
		t.Errorf("legacy ping failed: %+v", resp.Error)
	}
}

func TestModernToolsList(t *testing.T) {
	r := resultMap(t, newQuietServer().handleMessage(modernMsg("tools/list", nil)))
	if r["resultType"] != "complete" || r["ttlMs"] == nil || r["cacheScope"] != "public" {
		t.Errorf("missing modern fields: resultType=%v ttlMs=%v cacheScope=%v", r["resultType"], r["ttlMs"], r["cacheScope"])
	}
	tools, _ := r["tools"].([]interface{})
	if len(tools) == 0 {
		t.Fatal("no tools")
	}
	sawRepo := false
	for _, raw := range tools {
		tool := raw.(map[string]interface{})
		name := tool["name"].(string)
		if connectionStateTools[name] {
			t.Errorf("%s must not be listed to a modern client", name)
		}
		props, _ := tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
		_, hasRepo := props["repo"]
		if repoFreeTool(name) == hasRepo {
			t.Errorf("%s: repo argument present=%v", name, hasRepo)
		}
		sawRepo = sawRepo || hasRepo
	}
	if !sawRepo {
		t.Error("no tool offers repo")
	}
}

func TestModernListDoesNotTouchSharedSchemas(t *testing.T) {
	s := newQuietServer()
	_ = s.handleMessage(modernMsg("tools/list", nil))
	for _, tool := range s.GetToolDefinitions() {
		if props, ok := tool.InputSchema["properties"].(map[string]interface{}); ok {
			if _, has := props["repo"]; has {
				t.Fatalf("%s: legacy definition gained repo", tool.Name)
			}
		}
	}
}

func TestModernStateToolsRefused(t *testing.T) {
	s := newQuietServer()
	for name := range connectionStateTools {
		resp := s.handleMessage(modernMsg("tools/call", map[string]interface{}{"name": name, "arguments": map[string]interface{}{}}))
		if resp.Error == nil || resp.Error.Code != InvalidParams {
			t.Errorf("%s: want InvalidParams, got %+v", name, resp)
		}
	}
	if s.IsExpanded() {
		t.Error("a modern call expanded the toolset")
	}
}

func TestModernToolCallResultType(t *testing.T) {
	resp := newQuietServer().handleMessage(modernMsg("tools/call", map[string]interface{}{
		"name": "annotationSet", "arguments": map[string]interface{}{},
	}))
	r := resultMap(t, resp)
	if r["resultType"] != "complete" || r["isError"] != true {
		t.Errorf("resultType=%v isError=%v", r["resultType"], r["isError"])
	}
}

func TestModernBadRepoArgIsToolError(t *testing.T) {
	resp := newQuietServer().handleMessage(modernMsg("tools/call", map[string]interface{}{
		"name": "getStatus", "arguments": map[string]interface{}{"repo": "/nonexistent/ckb-test-repo"},
	}))
	r := resultMap(t, resp)
	if r["isError"] != true {
		t.Errorf("want isError for missing repo, got %v", r)
	}
}

func TestModernListenAcknowledgesThenCompletes(t *testing.T) {
	s := newQuietServer()
	var out bytes.Buffer
	s.SetStdout(&out)
	resp := s.handleMessage(modernMsg("subscriptions/listen", map[string]interface{}{
		"notifications": map[string]interface{}{"toolsListChanged": true},
	}))
	var ack map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &ack); err != nil {
		t.Fatalf("no acknowledgement written: %q", out.String())
	}
	if ack["method"] != "notifications/subscriptions/acknowledged" {
		t.Errorf("ack method = %v", ack["method"])
	}
	meta := ack["params"].(map[string]interface{})["_meta"].(map[string]interface{})
	if meta[metaKeySubscriptionID] != float64(1) {
		t.Errorf("subscriptionId = %v", meta[metaKeySubscriptionID])
	}
	if r := resultMap(t, resp); r["resultType"] != "complete" {
		t.Errorf("listen result = %v", r)
	}
}

func TestModernResolveRepoArg(t *testing.T) {
	if _, err := resolveRepoArg("sub", ""); err == nil {
		t.Error("relative repo without a start repo must fail")
	}
	dir := t.TempDir()
	if _, err := resolveRepoArg(dir, ""); err == nil || !strings.Contains(err.Error(), "git") {
		t.Errorf("non-git dir: %v", err)
	}
}
