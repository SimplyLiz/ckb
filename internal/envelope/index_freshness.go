package envelope

import "github.com/SimplyLiz/CodeMCP/internal/query"

// indexStaleMultiplier scales the confidence score when files in the result
// changed since the SCIP index was built.
const indexStaleMultiplier = 0.7

// WithIndexFreshness sets meta.indexFreshness. When the change affects this
// result, the confidence score is multiplied by 0.7 and the tier recomputed.
// An old index over untouched code leaves confidence alone. nil is a no-op.
func (b *Builder) WithIndexFreshness(f *query.IndexFreshness) *Builder {
	if f == nil {
		return b
	}
	if b.resp.Meta == nil {
		b.resp.Meta = &Meta{}
	}
	b.resp.Meta.IndexFreshness = f
	if f.AffectsResult && b.resp.Meta.Confidence != nil {
		c := b.resp.Meta.Confidence
		c.Score *= indexStaleMultiplier
		c.Tier = ScoreToTier(c.Score)
		c.Reasons = append(c.Reasons, "index-stale-for-result")
		c.Factors = append(c.Factors, ConfidenceFactor{
			Factor: "index_freshness",
			Status: "result_files_changed",
			Impact: -0.3,
		})
	}
	return b
}
