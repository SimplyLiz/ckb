package mcp

import (
	"context"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
)

// toolDetectShotgunSurgery ranks files by co-change dispersion (see
// query.Engine.ShotgunSurgery).
func (s *MCPServer) toolDetectShotgunSurgery(params map[string]interface{}) (*envelope.Response, error) {
	var limit, minPartners int
	if v, ok := params["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if v, ok := params["min_partners"].(float64); ok && v > 0 {
		minPartners = int(v)
	}
	return NewToolResponse().Data(s.engine().ShotgunSurgery(context.Background(), minPartners, limit)).Build(), nil
}
