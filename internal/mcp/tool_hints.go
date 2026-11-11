package mcp

// ToolAnnotations are the MCP tool annotations (spec 2025-03-26 and later).
// They are hints, not guarantees. Claude Code documents two effects
// (https://code.claude.com/docs/en/mcp.md, "Mark tools that are safe to call
// twice"): readOnlyHint lets it run the call in parallel with other read-only
// calls, and idempotentHint lets it repeat a call that was pending when an
// HTTP connection closed. Nothing else is documented to change behaviour, so
// read-only tools carry only readOnlyHint to keep tools/list small.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// toolWriteHint describes a tool that changes something: its own index, the
// .ckb database, a file in the repo, session state, or something outside.
type toolWriteHint struct {
	destructive bool // removes or stops something that existed
	idempotent  bool // a second call with the same arguments changes nothing more
	openWorld   bool // reaches outside this machine (remote servers, webhooks)
}

// writingTools lists every tool that is not read-only. Every tool not named
// here is read-only: it reads the index, git and the cache, and the cache
// writes behind it do not count as changing the environment.
//
// Session state counts as a change on purpose. expandToolset, switchRepo and
// switchProject change what every following call sees, so running one in
// parallel with a read would race.
var writingTools = map[string]toolWriteHint{
	// Session state
	"expandToolset": {idempotent: true},
	"switchRepo":    {idempotent: true},
	"switchProject": {idempotent: true},

	// CKB's own index and database
	"reindex":              {idempotent: true},
	"refreshArchitecture":  {idempotent: true},
	"indexDocs":            {idempotent: true},
	"annotateModule":       {idempotent: true},
	"annotationSet":        {idempotent: true},
	"suppressContractEdge": {idempotent: true},
	"verifyContractEdge":   {idempotent: true},
	"cancelJob":            {destructive: true, idempotent: true},

	// Writes an ADR file under docs/decisions; a second call writes a second one.
	"recordDecision": {},

	// Federation config and sync
	"federationSync":         {idempotent: true},
	"federationAddRemote":    {},
	"federationRemoveRemote": {destructive: true, idempotent: true},
	"federationSyncRemote":   {idempotent: true, openWorld: true},

	// Daemon: a schedule runs whatever it is configured to run, a webhook test
	// sends a request.
	"runSchedule": {openWorld: true},
	"testWebhook": {openWorld: true},
}

// readOnlyOpenWorld are read-only tools that ask a remote server.
var readOnlyOpenWorld = map[string]bool{
	"federationStatusRemote":        true,
	"federationSearchSymbolsHybrid": true,
	"federationListAllRepos":        true,
}

func boolPtr(b bool) *bool { return &b }

// annotationsFor returns the annotations for a tool name.
func annotationsFor(name string) *ToolAnnotations {
	if w, ok := writingTools[name]; ok {
		return &ToolAnnotations{
			ReadOnlyHint:    boolPtr(false),
			DestructiveHint: boolPtr(w.destructive),
			IdempotentHint:  boolPtr(w.idempotent),
			OpenWorldHint:   boolPtr(w.openWorld),
		}
	}
	a := &ToolAnnotations{ReadOnlyHint: boolPtr(true)}
	if readOnlyOpenWorld[name] {
		a.OpenWorldHint = boolPtr(true)
	}
	return a
}

// annotateTools sets the annotations on every tool definition.
func annotateTools(tools []Tool) {
	for i := range tools {
		tools[i].Annotations = annotationsFor(tools[i].Name)
	}
}
