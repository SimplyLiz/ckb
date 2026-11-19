package mcp

import (
	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/output"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

// ToolResponse is a convenience builder for MCP tool responses.
type ToolResponse struct {
	builder *envelope.Builder
}

// NewToolResponse creates a new tool response builder.
func NewToolResponse() *ToolResponse {
	return &ToolResponse{
		builder: envelope.New(),
	}
}

// Data sets the payload.
func (t *ToolResponse) Data(data interface{}) *ToolResponse {
	t.builder.Data(data)
	return t
}

// WithProvenance adds provenance metadata from a query response.
func (t *ToolResponse) WithProvenance(p *query.Provenance) *ToolResponse {
	t.builder.FromProvenance(p)
	return t
}

// WithIndexFreshness adds per-result SCIP index freshness (nil is a no-op) and
// lowers confidence when files in the result changed since indexing.
func (t *ToolResponse) WithIndexFreshness(f *query.IndexFreshness) *ToolResponse {
	t.builder.WithIndexFreshness(f)
	return t
}

// WithTruncation adds truncation info.
func (t *ToolResponse) WithTruncation(truncated bool, shown, total int, reason string) *ToolResponse {
	t.builder.WithTruncation(truncated, shown, total, reason)
	return t
}

// WithFreshness adds index freshness info.
func (t *ToolResponse) WithFreshness(commitsBehind int, staleReason string) *ToolResponse {
	t.builder.WithFreshness(commitsBehind, staleReason)
	return t
}

// WithDrilldowns converts drilldowns to suggested calls.
func (t *ToolResponse) WithDrilldowns(drilldowns []output.Drilldown) *ToolResponse {
	t.builder.SuggestCalls(drilldowns)
	return t
}

// WithBackend sets the active backend name and derives accuracy on the envelope.
// Emits a warn-level slog record when running in degraded mode (non-SCIP backend).
func (t *ToolResponse) WithBackend(backend string, logger interface {
	Warn(string, ...any)
}) *ToolResponse {
	t.builder.WithBackend(backend)
	if backend != "scip" {
		logger.Warn("Running in degraded mode",
			"backend", backend,
			"accuracy", envelope.AccuracyForBackend(backend),
		)
	}
	return t
}

// Warning adds a warning message.
func (t *ToolResponse) Warning(msg string) *ToolResponse {
	t.builder.Warning(msg)
	return t
}

// CrossRepo marks this as a cross-repo query.
func (t *ToolResponse) CrossRepo() *ToolResponse {
	t.builder.CrossRepo()
	return t
}

// Build returns the envelope response.
func (t *ToolResponse) Build() *envelope.Response {
	return t.builder.Build()
}

// OperationalResponse creates a simple envelope for operational tools.
// Use for tools like getStatus, doctor, daemonStatus that return factual state.
func OperationalResponse(data interface{}) *envelope.Response {
	return envelope.Operational(data)
}
