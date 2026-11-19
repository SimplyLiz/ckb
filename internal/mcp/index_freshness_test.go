package mcp

import (
	"testing"

	"github.com/SimplyLiz/CodeMCP/internal/envelope"
	"github.com/SimplyLiz/CodeMCP/internal/query"
)

func freshnessProvenance() *query.Provenance {
	return &query.Provenance{
		Backends:     []query.BackendContribution{{BackendId: "scip", Available: true, Used: true}},
		Completeness: query.CompletenessInfo{Score: 1.0},
	}
}

func TestWithIndexFreshnessAffectsLowersConfidence(t *testing.T) {
	resp := NewToolResponse().Data(1).WithProvenance(freshnessProvenance()).
		WithIndexFreshness(&query.IndexFreshness{IndexedCommit: "abc", AffectsResult: true}).Build()
	if resp.Meta.IndexFreshness == nil {
		t.Fatal("indexFreshness missing")
	}
	c := resp.Meta.Confidence
	if c.Score < 0.69 || c.Score > 0.71 || c.Tier != envelope.TierMedium {
		t.Fatalf("expected ~0.7/medium, got %v/%s", c.Score, c.Tier)
	}
}

func TestWithIndexFreshnessUnaffectedKeepsConfidence(t *testing.T) {
	resp := NewToolResponse().Data(1).WithProvenance(freshnessProvenance()).
		WithIndexFreshness(&query.IndexFreshness{IndexedCommit: "abc", CommitsBehind: 9}).Build()
	if resp.Meta.IndexFreshness == nil {
		t.Fatal("indexFreshness missing")
	}
	if c := resp.Meta.Confidence; c.Score != 1.0 || c.Tier != envelope.TierHigh {
		t.Fatalf("confidence changed: %v/%s", c.Score, c.Tier)
	}
}

func TestWithIndexFreshnessNil(t *testing.T) {
	resp := NewToolResponse().Data(1).WithIndexFreshness(nil).Build()
	if resp.Meta != nil {
		t.Fatalf("expected no meta, got %+v", resp.Meta)
	}
}
