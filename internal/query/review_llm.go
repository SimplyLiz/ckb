package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultAnthropicModel = "claude-sonnet-4-20250514"
	defaultGeminiModel    = "gemini-2.5-flash"
	anthropicAPIURL       = "https://api.anthropic.com/v1/messages"
	geminiAPIBaseURL      = "https://generativelanguage.googleapis.com/v1beta/models"
	anthropicAPIVersion   = "2023-06-01"
)

// llmProvider resolves which LLM provider, key, and model to use.
func (e *Engine) llmProvider() (provider, apiKey, model string, err error) {
	provider = "anthropic"
	if e.config != nil && e.config.LLM.Provider != "" {
		provider = strings.ToLower(e.config.LLM.Provider)
	}

	// Resolve API key: config → env (provider-specific) → env (generic)
	if e.config != nil && e.config.LLM.APIKey != "" {
		apiKey = e.config.LLM.APIKey
	}
	if apiKey == "" {
		switch provider {
		case "gemini":
			apiKey = os.Getenv("GEMINI_API_KEY")
		default:
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	if apiKey == "" {
		// Auto-detect from environment
		if key := os.Getenv("GEMINI_API_KEY"); key != "" {
			apiKey = key
			provider = "gemini"
		} else if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			apiKey = key
			provider = "anthropic"
		}
	}
	if apiKey == "" {
		return "", "", "", fmt.Errorf("no API key: set GEMINI_API_KEY or ANTHROPIC_API_KEY (or config.llm.apiKey)")
	}

	// Resolve model
	model = ""
	if e.config != nil && e.config.LLM.Model != "" {
		model = e.config.LLM.Model
	}
	if model == "" {
		switch provider {
		case "gemini":
			model = defaultGeminiModel
		default:
			model = defaultAnthropicModel
		}
	}

	return provider, apiKey, model, nil
}

// generateLLMNarrative enriches findings with CKB tool context, then calls
// the configured LLM to produce a prioritized, contextual review narrative.
func (e *Engine) generateLLMNarrative(ctx context.Context, resp *ReviewPRResponse) (string, error) {
	provider, apiKey, model, err := e.llmProvider()
	if err != nil {
		return "", err
	}

	// Phase 1: Enrich findings using CKB's own tools (0 tokens)
	enriched := e.enrichFindings(ctx, resp)

	// Phase 2: Build prompt with enriched data
	promptJSON, err := json.Marshal(enriched)
	if err != nil {
		return "", fmt.Errorf("failed to marshal prompt data: %w", err)
	}

	systemPrompt := `You are CKB, a code intelligence review tool. You receive pre-computed analysis from 15 deterministic checks plus enrichment from CKB's own symbol resolution tools.

Your job:
1. Prioritize: which findings actually matter for this PR?
2. Verify: do the enriched details confirm or contradict the finding?
3. Synthesize: write a 3-5 sentence review narrative

Rules:
- If a "dead-code" finding has references in the enrichment, it's a false positive — say so
- If blast-radius callers are all CLI flag registrations, downgrade importance
- Focus on findings that indicate real bugs or design issues
- Be direct and specific. No markdown formatting.
- End with a one-line recommendation for the reviewer.

The "triage" field on findings indicates CKB's confidence:
- "confirmed": CKB verified this is a real issue
- "likely-fp": CKB found evidence this may be a false positive — explain why
- "verify": CKB can't determine — use your judgment
- empty: no enrichment data available

When triage is "likely-fp", explain what the enrichment found and why the finding may not be real.`

	userPrompt := "Review this PR analysis and write a prioritized narrative:\n\n" + string(promptJSON)

	// Phase 3: Call LLM
	httpCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	switch provider {
	case "gemini":
		return callGemini(httpCtx, apiKey, model, systemPrompt, userPrompt)
	default:
		return callAnthropic(httpCtx, apiKey, model, systemPrompt, userPrompt)
	}
}

// enrichedReview is the data sent to the LLM — pre-verified by CKB's own tools.
type enrichedReview struct {
	Verdict  string            `json:"verdict"`
	Score    int               `json:"score"`
	PRTier   string            `json:"prTier"`
	Summary  ReviewSummary     `json:"summary"`
	Checks   []enrichedCheck   `json:"checks"`
	Findings []enrichedFinding `json:"findings"`
	Health   *enrichedHealth   `json:"health,omitempty"`
}

type enrichedCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type enrichedFinding struct {
	Check      string  `json:"check"`
	Severity   string  `json:"severity"`
	File       string  `json:"file"`
	StartLine  int     `json:"startLine,omitempty"`
	Message    string  `json:"message"`
	RuleID     string  `json:"ruleId,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	// Enrichment from CKB tools (filled by enrichFindings)
	Context string `json:"context,omitempty"` // Additional context from CKB tools
	Triage  string `json:"triage,omitempty"`  // "verify", "likely-fp", "confirmed" — set by enrichment
}

type enrichedHealth struct {
	Degraded     int     `json:"degraded"`
	Improved     int     `json:"improved"`
	AverageDelta float64 `json:"averageDelta"`
}

// enrichFindings uses CKB's own query engine to verify and contextualize
// findings before sending them to the LLM. This is the "zero token" enrichment
// step — all done locally using SCIP index, git, and tree-sitter.
func (e *Engine) enrichFindings(ctx context.Context, resp *ReviewPRResponse) *enrichedReview {
	result := &enrichedReview{
		Verdict: resp.Verdict,
		Score:   resp.Score,
		PRTier:  resp.PRTier,
		Summary: resp.Summary,
	}

	// Enriched checks
	for _, c := range resp.Checks {
		result.Checks = append(result.Checks, enrichedCheck{
			Name:    c.Name,
			Status:  c.Status,
			Summary: c.Summary,
		})
	}

	// Health
	if resp.HealthReport != nil {
		result.Health = &enrichedHealth{
			Degraded:     resp.HealthReport.Degraded,
			Improved:     resp.HealthReport.Improved,
			AverageDelta: resp.HealthReport.AverageDelta,
		}
	}

	// Enrich top findings (cap at 15 to keep prompt small)
	topFindings := resp.Findings
	if len(topFindings) > 15 {
		topFindings = topFindings[:15]
	}

	for _, f := range topFindings {
		ef := enrichedFinding{
			Check:      f.Check,
			Severity:   f.Severity,
			File:       f.File,
			StartLine:  f.StartLine,
			Message:    f.Message,
			RuleID:     f.RuleID,
			Confidence: f.Confidence,
		}

		// Enrich based on finding type
		switch f.Check {
		case "dead-code":
			ef.Context = e.enrichDeadCode(ctx, f)
			if strings.Contains(ef.Context, "FALSE POSITIVE") {
				ef.Triage = "likely-fp"
			} else if strings.Contains(ef.Context, "Confirmed") {
				ef.Triage = "confirmed"
			}
		case "blast-radius":
			ef.Context = e.enrichBlastRadius(ctx, f)
			if strings.HasPrefix(f.File, "cmd/") {
				ef.Triage = "likely-fp"
			} else {
				ef.Triage = "verify"
			}
		case "coupling":
			ef.Context = e.enrichCoupling(ctx, f)
			ef.Triage = "verify"
		case "complexity":
			ef.Context = e.enrichComplexity(ctx, f)
			// Parse delta from message like "Complexity 54→67 (+13 cyclomatic)"
			if idx := strings.Index(f.Message, "(+"); idx >= 0 {
				deltaStr := f.Message[idx+2:]
				if end := strings.IndexByte(deltaStr, ' '); end >= 0 {
					deltaStr = deltaStr[:end]
				}
				delta := 0
				fmt.Sscanf(deltaStr, "%d", &delta)
				if delta >= 10 {
					ef.Triage = "verify"
				}
			}
		}

		result.Findings = append(result.Findings, ef)
	}

	return result
}

// enrichDeadCode verifies a dead-code finding by searching for references.
func (e *Engine) enrichDeadCode(ctx context.Context, f ReviewFinding) string {
	// Extract symbol name from message like "Dead code: FormatSARIF (constant)"
	name := f.Message
	if idx := strings.Index(name, ":"); idx >= 0 {
		name = strings.TrimSpace(name[idx+1:])
	}
	if idx := strings.Index(name, "("); idx >= 0 {
		name = strings.TrimSpace(name[:idx])
	}

	// Search for references using CKB's own engine
	resp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: name,
		Limit: 5,
	})
	if err != nil || resp == nil || len(resp.Symbols) == 0 {
		return "Could not resolve symbol — treat as potentially dead"
	}

	// Try to find references
	for _, sym := range resp.Symbols {
		if sym.Name == name {
			refs, err := e.FindReferences(ctx, FindReferencesOptions{
				SymbolId: sym.StableId,
				Limit:    10,
			})
			if err != nil {
				continue
			}
			if refs != nil && refs.TotalCount > 0 {
				locations := []string{}
				for _, ref := range refs.References {
					if ref.Location != nil && len(locations) < 3 {
						locations = append(locations, fmt.Sprintf("%s:%d", ref.Location.FileId, ref.Location.StartLine))
					}
				}
				return fmt.Sprintf("ACTUALLY HAS %d reference(s): %s — likely FALSE POSITIVE",
					refs.TotalCount, strings.Join(locations, ", "))
			}
			return "Confirmed: 0 references found"
		}
	}
	return "Symbol not found in index"
}

// enrichBlastRadius adds caller context to blast-radius findings.
func (e *Engine) enrichBlastRadius(ctx context.Context, f ReviewFinding) string {
	// Extract symbol name from "Fan-out: daemonCmd has 7 callers"
	name := f.Message
	if strings.HasPrefix(name, "Fan-out: ") {
		name = strings.TrimPrefix(name, "Fan-out: ")
		if idx := strings.Index(name, " has "); idx >= 0 {
			name = name[:idx]
		}
	}

	resp, err := e.SearchSymbols(ctx, SearchSymbolsOptions{
		Query: name,
		Limit: 1,
	})
	if err != nil || resp == nil || len(resp.Symbols) == 0 {
		return ""
	}

	// Check if this is a CLI command/flag variable (common FP source)
	if strings.HasPrefix(f.File, "cmd/") {
		return fmt.Sprintf("Symbol '%s' is in cmd/ package — callers are likely CLI registrations, not real fan-out", name)
	}

	sym := resp.Symbols[0]
	impact, err := e.AnalyzeImpact(ctx, AnalyzeImpactOptions{
		SymbolId: sym.StableId,
		Depth:    1,
	})
	if err != nil || impact == nil || impact.BlastRadius == nil {
		return ""
	}

	return fmt.Sprintf("Blast radius: %d files, %d modules, risk: %s",
		impact.BlastRadius.FileCount, impact.BlastRadius.ModuleCount, impact.BlastRadius.RiskLevel)
}

// enrichCoupling explains the co-change relationship.
func (e *Engine) enrichCoupling(ctx context.Context, f ReviewFinding) string {
	// The finding message already contains the co-change rate
	// Just add context about whether the missing file was actually modified recently
	if f.File == "" {
		return ""
	}
	return fmt.Sprintf("File %s is in this PR but its co-change partner is not. Check if the partner needs updates.", f.File)
}

// enrichComplexity adds function-level detail.
func (e *Engine) enrichComplexity(ctx context.Context, f ReviewFinding) string {
	// Already has good detail in the message ("Complexity 54→67 (+13 cyclomatic) in SummarizePR()")
	// Just flag if the delta is very high
	if strings.Contains(f.Message, "+1") && !strings.Contains(f.Message, "+1") {
		return "Minor increase — unlikely to affect maintainability"
	}
	return ""
}

// --- Provider implementations ---

func callAnthropic(ctx context.Context, apiKey, model, systemPrompt, userPrompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model":      model,
		"max_tokens": 512,
		"system":     systemPrompt,
		"messages": []map[string]interface{}{
			{"role": "user", "content": userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API returned %d: %.200s", httpResp.StatusCode, string(respBody))
	}

	return parseAnthropicResponse(respBody)
}

func parseAnthropicResponse(body []byte) (string, error) {
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	for _, block := range result.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in response")
}

func callGemini(ctx context.Context, apiKey, model, systemPrompt, userPrompt string) (string, error) {
	url := fmt.Sprintf("%s/%s:generateContent?key=%s", geminiAPIBaseURL, model, apiKey)

	reqBody := map[string]interface{}{
		"system_instruction": map[string]interface{}{
			"parts": []map[string]string{
				{"text": systemPrompt},
			},
		},
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": userPrompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": 1024,
			"temperature":     0.3,
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request failed: %w", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini API returned %d: %.200s", httpResp.StatusCode, string(respBody))
	}

	return parseGeminiResponse(respBody)
}

func parseGeminiResponse(body []byte) (string, error) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("no text content in gemini response")
}

// parseLLMResponse is a compatibility wrapper for tests.
func parseLLMResponse(body []byte) (string, error) {
	return parseAnthropicResponse(body)
}
