package impact

import (
	"context"
	"path/filepath"
	"strings"
)

// BlastRadiusEnricher supplements SCIP-derived blast radius with external data
// (e.g., LIP embedding-based semantic coupling). Implementations must be safe
// for concurrent use and degrade gracefully — returning nil signals "unavailable".
type BlastRadiusEnricher interface {
	// EnrichBatch takes changed file URIs and returns per-symbol blast radius
	// from the external source. The map key is the symbol URI (e.g.,
	// "lip://local/src/auth.rs#validate_token"). Returns nil when the source
	// is unavailable.
	EnrichBatch(ctx context.Context, changedFileURIs []string) (map[string]*ExternalBlastRadius, error)
}

// ExternalBlastRadius is what an enricher returns per symbol.
type ExternalBlastRadius struct {
	// DirectItems are callers the external source found via static analysis.
	// These overlap with SCIP's results and are used to confirm edges.
	DirectItems []ExternalItem
	// TransitiveItems are transitive callers from the external source.
	TransitiveItems []ExternalItem
	// SemanticItems are callers found via embedding similarity that may not
	// appear in any static call graph (dynamic dispatch, macros, etc.).
	SemanticItems []ExternalSemanticItem
	// RiskLevel is the external source's own risk assessment.
	RiskLevel string
	// EdgesSource is the provenance for DirectItems/TransitiveItems. Values
	// mirror LIP v2.3.1: "tier1" (tree-sitter AST), "scip_with_tier1_edges"
	// (SCIP symbols, Tier-1 edges back-filled), "scip_only" (SCIP call edges
	// as-is), "empty" (no static edges available). An unset value means the
	// source didn't report provenance — treat as fold-eligible.
	EdgesSource string
}

// Edge-source values for ExternalBlastRadius.EdgesSource.
const (
	EdgesSourceTier1              = "tier1"
	EdgesSourceScipWithTier1Edges = "scip_with_tier1_edges" // #nosec G101 -- provenance label, not a credential
	EdgesSourceScipOnly           = "scip_only"
	EdgesSourceEmpty              = "empty"
)

// ExternalItem is a static caller from an external blast radius source.
type ExternalItem struct {
	FileURI    string
	SymbolURI  string
	Distance   int
	Confidence float64
}

// ExternalSemanticItem is a semantically coupled symbol from an enricher.
type ExternalSemanticItem struct {
	FileURI    string
	SymbolURI  string
	Similarity float32 // cosine similarity
	Source     string  // "semantic" or "both"
}

// MergeBlastRadius blends SCIP-derived blast radius with enricher data.
//
// Design invariant: UniqueCallerCount stays SCIP-only so that reviewPR
// thresholds (callerCount >= 3, callerCount > maxFanOut) are never inflated
// by embedding noise. Semantic callers are additive in SemanticCallerCount
// and SemanticCallers — they inform humans, not thresholds.
//
// Items with source=="both" confirm that a SCIP static edge also has embedding
// evidence. These bump ConfirmedCount but don't change UniqueCallerCount.
func MergeBlastRadius(static *BlastRadius, external *ExternalBlastRadius) *BlastRadius {
	if static == nil {
		return nil
	}
	if external == nil {
		return static
	}

	merged := *static // shallow copy
	merged.StaticCallerCount = static.UniqueCallerCount

	// Build a set of files SCIP already knows about (from static callers).
	// We use file URIs because SCIP symbol IDs and LIP symbol URIs use different
	// schemes — file URI is the stable join key.
	staticFiles := make(map[string]struct{})
	for _, item := range external.DirectItems {
		staticFiles[item.FileURI] = struct{}{}
	}
	for _, item := range external.TransitiveItems {
		staticFiles[item.FileURI] = struct{}{}
	}

	var semanticCallers []EnrichedCaller
	confirmed := 0
	seen := make(map[string]struct{}) // dedup by file URI

	for _, si := range external.SemanticItems {
		if _, dup := seen[si.FileURI]; dup {
			continue
		}
		seen[si.FileURI] = struct{}{}

		switch si.Source {
		case "both":
			// Confirms a SCIP edge — record but don't inflate counts
			confirmed++
			semanticCallers = append(semanticCallers, EnrichedCaller{
				SymbolURI:  si.SymbolURI,
				FileURI:    si.FileURI,
				Tier:       CouplingBoth,
				Confidence: 0.95,
				Similarity: si.Similarity,
			})
		case "semantic":
			// New coupling not in SCIP — advisory
			semanticCallers = append(semanticCallers, EnrichedCaller{
				SymbolURI:  si.SymbolURI,
				FileURI:    si.FileURI,
				Tier:       CouplingSemantic,
				Confidence: float64(si.Similarity), // cosine similarity as confidence proxy
				Similarity: si.Similarity,
			})
		}
	}

	// Count only pure semantic (not "both") as additional callers
	pureSemanticCount := 0
	for _, c := range semanticCallers {
		if c.Tier == CouplingSemantic {
			pureSemanticCount++
		}
	}

	merged.SemanticCallerCount = pureSemanticCount
	merged.ConfirmedCount = confirmed
	merged.SemanticCallers = semanticCallers
	// RiskLevel stays SCIP-derived. Semantic coupling informs the human, not the threshold.
	return &merged
}

// FoldExternalStaticItems folds an enricher's DirectItems / TransitiveItems
// into SCIP-derived ImpactItem lists so LIP's tier-1 tree-sitter callers
// (which SCIP misses when scip-go emits no Call roles) become first-class
// impact items rather than sitting in a parallel summary field.
//
// Behaviour:
//   - external == nil OR EdgesSource == "empty" → returns (direct, transitive)
//     unchanged. "empty" means LIP had no static call-edge evidence to
//     contribute; folding nothing is correct.
//   - Items with SymbolURI == "" are skipped (Phase-4 file-only fallback
//     from LIP — legitimate file-level evidence but no symbol identity to
//     dedup against).
//   - Remaining items are deduped against the existing SCIP items by
//     (absolute file path, symbol name). LIP's tier-1 URIs carry absolute
//     paths (lip://local//<abs>#<name>); SCIP ImpactItem.Location.FileId
//     is joined onto repoRoot when relative. Items already present on the
//     SCIP side are dropped — we never inflate caller counts with evidence
//     SCIP already recorded.
//
// The function does not reclassify EdgesSource values — callers decide
// whether the provenance is trustworthy before calling. All non-Empty
// sources (tier1, scip_with_tier1_edges, scip_only) fold the same way.
func FoldExternalStaticItems(
	direct, transitive []ImpactItem,
	external *ExternalBlastRadius,
	repoRoot string,
) (foldedDirect, foldedTransitive []ImpactItem) {
	return foldExternalItemsWithKinds(direct, transitive, external, repoRoot, DirectCaller, TransitiveCaller)
}

// FoldExternalCalleeItems is the forward-direction twin of
// FoldExternalStaticItems, tagging folded items with DirectCallee /
// TransitiveCallee. Used by Engine.AnalyzeOutgoingImpact to fold LIP's
// query_outgoing_impact result into the shared ImpactItem pipeline.
//
// Unlike the incoming path there is typically no SCIP-derived list to merge
// against — callers usually pass nil for direct/transitive and get back a
// pure LIP-derived set. Dedup semantics are identical to the incoming fold,
// so passing non-nil inputs is also supported for future SCIP forward BFS.
func FoldExternalCalleeItems(
	direct, transitive []ImpactItem,
	external *ExternalBlastRadius,
	repoRoot string,
) (foldedDirect, foldedTransitive []ImpactItem) {
	return foldExternalItemsWithKinds(direct, transitive, external, repoRoot, DirectCallee, TransitiveCallee)
}

func foldExternalItemsWithKinds(
	direct, transitive []ImpactItem,
	external *ExternalBlastRadius,
	repoRoot string,
	directKind, transitiveKind ImpactKind,
) (foldedDirect, foldedTransitive []ImpactItem) {
	if external == nil || external.EdgesSource == EdgesSourceEmpty {
		return direct, transitive
	}

	seen := make(map[string]struct{}, len(direct)+len(transitive))
	for _, it := range direct {
		seen[impactItemDedupKey(it, repoRoot)] = struct{}{}
	}
	for _, it := range transitive {
		seen[impactItemDedupKey(it, repoRoot)] = struct{}{}
	}

	foldedDirect = direct
	foldedTransitive = transitive

	for _, ei := range external.DirectItems {
		item, key, ok := externalItemToImpactItem(ei, directKind)
		if !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		foldedDirect = append(foldedDirect, item)
	}
	for _, ei := range external.TransitiveItems {
		item, key, ok := externalItemToImpactItem(ei, transitiveKind)
		if !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		foldedTransitive = append(foldedTransitive, item)
	}
	return foldedDirect, foldedTransitive
}

// externalItemToImpactItem parses a LIP ExternalItem into an ImpactItem
// and its dedup key. Returns ok=false for items with empty SymbolURI or a
// URI that doesn't carry a `#<name>` fragment.
func externalItemToImpactItem(ei ExternalItem, kind ImpactKind) (ImpactItem, string, bool) {
	if ei.SymbolURI == "" {
		return ImpactItem{}, "", false
	}
	absPath, name, ok := splitLIPSymbolURI(ei.SymbolURI, ei.FileURI)
	if !ok {
		return ImpactItem{}, "", false
	}
	distance := ei.Distance
	if distance == 0 {
		if kind == DirectCaller || kind == DirectCallee {
			distance = 1
		} else {
			distance = 2
		}
	}
	item := ImpactItem{
		StableId:   ei.SymbolURI,
		Name:       name,
		Kind:       kind,
		Distance:   distance,
		Confidence: ei.Confidence,
		Location:   &Location{FileId: absPath},
	}
	return item, dedupKey(absPath, name), true
}

// splitLIPSymbolURI parses a lip://local//<abs>#<name> URI into (abs, name).
// Falls back to the companion file_uri when the symbol URI has no fragment,
// which happens for Phase-4 file-only items LIP emits — but those should
// already be filtered by the caller via the empty-SymbolURI check, so a
// fragment-less URI here is treated as unparseable.
func splitLIPSymbolURI(symURI, fileURI string) (absPath, name string, ok bool) {
	hash := strings.LastIndex(symURI, "#")
	if hash < 0 {
		return "", "", false
	}
	filePart := symURI[:hash]
	name = symURI[hash+1:]
	if name == "" {
		return "", "", false
	}
	absPath = stripLIPLocalPrefix(filePart)
	if absPath == "" && fileURI != "" {
		absPath = stripLIPLocalPrefix(fileURI)
	}
	if absPath == "" {
		return "", "", false
	}
	return absPath, name, true
}

// stripLIPLocalPrefix converts lip://local//<abs> or lip://local/<rel>
// back to a filesystem path. Non-lip://local URIs (e.g. scip-go) are
// returned unchanged — they won't match any SCIP FileId but the dedup
// key will still be unique, so LIP items are safely additive.
func stripLIPLocalPrefix(uri string) string {
	const p = "lip://local/"
	if !strings.HasPrefix(uri, p) {
		return uri
	}
	rest := uri[len(p):]
	// LIP writes lip://local//<abs> (double slash) when the path is
	// absolute. After stripping the single-slash prefix, a leading
	// slash survives and marks an absolute path. Relative paths come
	// through as plain "foo/bar.go".
	return rest
}

// impactItemDedupKey produces the (absolute path, name) key used for
// cross-source dedup. Location.FileId may be repo-relative (the common
// SCIP case) or absolute — filepath.IsAbs + filepath.Join handles both.
func impactItemDedupKey(it ImpactItem, repoRoot string) string {
	path := ""
	if it.Location != nil {
		path = it.Location.FileId
	}
	if path != "" && !filepath.IsAbs(path) && repoRoot != "" {
		path = filepath.Join(repoRoot, path)
	}
	return dedupKey(path, it.Name)
}

func dedupKey(absPath, name string) string {
	return absPath + "#" + name
}
