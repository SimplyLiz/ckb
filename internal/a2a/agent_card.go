package a2a

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/SimplyLiz/CodeMCP/internal/version"
)

// handleAgentCard serves the public agent card at /.well-known/agent-card.json.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	card := s.buildAgentCard()

	data, err := json.Marshal(card)
	if err != nil {
		writeA2AError(w, NewInternalError("failed to marshal agent card"))
		return
	}

	// ETag based on content hash
	hash := sha256.Sum256(data)
	etag := fmt.Sprintf(`"%s"`, hex.EncodeToString(hash[:8]))

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleExtendedAgentCard serves the authenticated extended agent card.
func (s *Server) handleExtendedAgentCard(w http.ResponseWriter, r *http.Request) {
	card := s.buildExtendedAgentCard()
	writeJSON(w, http.StatusOK, card)
}

// buildAgentCard generates the public agent card from the skill registry.
func (s *Server) buildAgentCard() AgentCard {
	baseURL := s.config.BaseURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s", s.config.Addr)
	}

	card := AgentCard{
		Name:        "CKB - Code Knowledge Backend",
		Description: "Language-agnostic codebase comprehension agent providing 111 code intelligence tools including symbol navigation, impact analysis, architecture exploration, PR review, and compliance auditing.",
		Version:     version.Version,
		Provider: &Provider{
			Organization: "TasteHub",
			URL:          "https://github.com/SimplyLiz/CodeMCP",
		},
		DocumentationURL: "https://github.com/SimplyLiz/CodeMCP#readme",
		SupportedInterfaces: []SupportedInterface{
			{
				URL:             baseURL,
				ProtocolBinding: BindingJSONRPC,
				ProtocolVersion: ProtocolVersion,
			},
			{
				URL:             baseURL,
				ProtocolBinding: BindingHTTPJSON,
				ProtocolVersion: ProtocolVersion,
			},
		},
		Capabilities: &Capabilities{
			Streaming:              true,
			PushNotifications:      true,
			ExtendedAgentCard:      true,
			StateTransitionHistory: true,
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Skills:             s.skills.AllSkills(),
	}

	// Add security scheme if auth token is configured
	if s.config.AuthToken != "" {
		card.SecuritySchemes = map[string]SecurityScheme{
			"bearer": {
				Type:   "http",
				Scheme: "bearer",
			},
		}
		card.Security = []map[string][]string{
			{"bearer": {}},
		}
	}

	return card
}

// buildExtendedAgentCard generates the authenticated extended card with input schemas.
func (s *Server) buildExtendedAgentCard() AgentCard {
	card := s.buildAgentCard()

	// Replace skills with extended versions that include input schemas
	extended := ExtendedSkills(s.mcpServer)
	card.Skills = make([]Skill, len(extended))
	for i, ext := range extended {
		card.Skills[i] = ext.Skill
		// Embed the input schema in the skill's metadata
		// (A2A spec doesn't have a standard field for this, so we use the extended card)
	}

	return card
}
