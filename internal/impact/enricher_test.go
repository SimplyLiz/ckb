package impact

import "testing"

func TestMergeBlastRadius_NilExternal(t *testing.T) {
	static := &BlastRadius{
		ModuleCount:       3,
		FileCount:         5,
		UniqueCallerCount: 8,
		RiskLevel:         "medium",
	}
	got := MergeBlastRadius(static, nil)
	if got != static {
		t.Fatal("nil external should return static unchanged")
	}
}

func TestMergeBlastRadius_NilStatic(t *testing.T) {
	got := MergeBlastRadius(nil, &ExternalBlastRadius{})
	if got != nil {
		t.Fatal("nil static should return nil")
	}
}

func TestMergeBlastRadius_SemanticOnly(t *testing.T) {
	static := &BlastRadius{
		ModuleCount:       2,
		FileCount:         3,
		UniqueCallerCount: 4,
		RiskLevel:         "low",
	}
	external := &ExternalBlastRadius{
		SemanticItems: []ExternalSemanticItem{
			{FileURI: "file:///src/a.rs", SymbolURI: "sym:a", Similarity: 0.85, Source: "semantic"},
			{FileURI: "file:///src/b.rs", SymbolURI: "sym:b", Similarity: 0.72, Source: "semantic"},
		},
	}

	got := MergeBlastRadius(static, external)

	// UniqueCallerCount must stay SCIP-only
	if got.UniqueCallerCount != 4 {
		t.Errorf("UniqueCallerCount = %d, want 4 (SCIP-only)", got.UniqueCallerCount)
	}
	if got.StaticCallerCount != 4 {
		t.Errorf("StaticCallerCount = %d, want 4", got.StaticCallerCount)
	}
	if got.SemanticCallerCount != 2 {
		t.Errorf("SemanticCallerCount = %d, want 2", got.SemanticCallerCount)
	}
	if got.ConfirmedCount != 0 {
		t.Errorf("ConfirmedCount = %d, want 0", got.ConfirmedCount)
	}
	if len(got.SemanticCallers) != 2 {
		t.Fatalf("SemanticCallers len = %d, want 2", len(got.SemanticCallers))
	}
	for _, sc := range got.SemanticCallers {
		if sc.Tier != CouplingSemantic {
			t.Errorf("caller %s tier = %s, want semantic", sc.FileURI, sc.Tier)
		}
	}
	// RiskLevel stays SCIP-derived
	if got.RiskLevel != "low" {
		t.Errorf("RiskLevel = %s, want low", got.RiskLevel)
	}
}

func TestMergeBlastRadius_BothSource(t *testing.T) {
	static := &BlastRadius{
		ModuleCount:       1,
		FileCount:         2,
		UniqueCallerCount: 3,
		RiskLevel:         "low",
	}
	external := &ExternalBlastRadius{
		SemanticItems: []ExternalSemanticItem{
			{FileURI: "file:///src/confirmed.rs", Similarity: 0.91, Source: "both"},
			{FileURI: "file:///src/new.rs", Similarity: 0.78, Source: "semantic"},
		},
	}

	got := MergeBlastRadius(static, external)

	// "both" confirms a SCIP edge — doesn't inflate semantic count
	if got.SemanticCallerCount != 1 {
		t.Errorf("SemanticCallerCount = %d, want 1 (only pure semantic)", got.SemanticCallerCount)
	}
	if got.ConfirmedCount != 1 {
		t.Errorf("ConfirmedCount = %d, want 1", got.ConfirmedCount)
	}
	if len(got.SemanticCallers) != 2 {
		t.Fatalf("SemanticCallers len = %d, want 2 (both + semantic)", len(got.SemanticCallers))
	}

	// Check tiers
	tiers := map[CouplingTier]int{}
	for _, sc := range got.SemanticCallers {
		tiers[sc.Tier]++
	}
	if tiers[CouplingBoth] != 1 || tiers[CouplingSemantic] != 1 {
		t.Errorf("tier counts = %v, want both:1, semantic:1", tiers)
	}
}

func TestFoldExternalStaticItems_NilExternal(t *testing.T) {
	direct := []ImpactItem{{StableId: "a", Name: "A", Kind: DirectCaller}}
	gotD, gotT := FoldExternalStaticItems(direct, nil, nil, "/repo")
	if len(gotD) != 1 || gotD[0].StableId != "a" {
		t.Errorf("nil external should pass direct through unchanged, got %+v", gotD)
	}
	if gotT != nil {
		t.Errorf("nil external should pass transitive through unchanged, got %+v", gotT)
	}
}

func TestFoldExternalStaticItems_EmptyEdgesSource(t *testing.T) {
	direct := []ImpactItem{{StableId: "a", Name: "A", Kind: DirectCaller}}
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceEmpty,
		DirectItems: []ExternalItem{
			{FileURI: "lip://local//repo/b.go", SymbolURI: "lip://local//repo/b.go#B", Distance: 1, Confidence: 0.9},
		},
	}
	gotD, _ := FoldExternalStaticItems(direct, nil, external, "/repo")
	if len(gotD) != 1 {
		t.Errorf("EdgesSource=empty should skip fold; got %d direct items, want 1", len(gotD))
	}
}

func TestFoldExternalStaticItems_SkipsEmptySymbolURI(t *testing.T) {
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipOnly,
		DirectItems: []ExternalItem{
			{FileURI: "lip://local//repo/phase4.go", SymbolURI: "", Distance: 1, Confidence: 0.95},
			{FileURI: "lip://local//repo/ok.go", SymbolURI: "lip://local//repo/ok.go#Ok", Distance: 1, Confidence: 0.95},
		},
	}
	gotD, _ := FoldExternalStaticItems(nil, nil, external, "/repo")
	if len(gotD) != 1 {
		t.Fatalf("expected 1 direct item after skipping empty-SymbolURI, got %d", len(gotD))
	}
	if gotD[0].Name != "Ok" {
		t.Errorf("kept item name = %q, want Ok", gotD[0].Name)
	}
	if gotD[0].Location == nil || gotD[0].Location.FileId != "ok.go" {
		t.Errorf("kept item FileId = %v, want ok.go (repo-relative like SCIP)", gotD[0].Location)
	}
}

func TestFoldExternalStaticItems_DedupAgainstSCIP(t *testing.T) {
	// SCIP already knows about callgraph.go:RenderTree. LIP rediscovers it.
	// After fold, we should NOT get a duplicate.
	direct := []ImpactItem{
		{StableId: "scip-sym", Name: "RenderTree", Kind: DirectCaller, Distance: 1,
			Location: &Location{FileId: "cmd/ckb/callgraph.go"}},
	}
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipOnly,
		DirectItems: []ExternalItem{
			// Same file + name as SCIP → dedup
			{FileURI: "lip://local//repo/cmd/ckb/callgraph.go",
				SymbolURI: "lip://local//repo/cmd/ckb/callgraph.go#RenderTree",
				Distance:  1, Confidence: 0.95},
			// Novel caller — keep
			{FileURI: "lip://local//repo/cmd/ckb/impact.go",
				SymbolURI: "lip://local//repo/cmd/ckb/impact.go#doImpact",
				Distance:  1, Confidence: 0.95},
		},
	}
	gotD, _ := FoldExternalStaticItems(direct, nil, external, "/repo")
	if len(gotD) != 2 {
		t.Fatalf("want 2 items (1 SCIP + 1 novel LIP), got %d: %+v", len(gotD), gotD)
	}
	if gotD[0].Name != "RenderTree" || gotD[1].Name != "doImpact" {
		t.Errorf("wrong items: %q, %q", gotD[0].Name, gotD[1].Name)
	}
}

func TestFoldExternalStaticItems_DedupBetweenLIPDirectAndTransitive(t *testing.T) {
	// LIP emits the same caller in both lists (shouldn't happen, but guard).
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipWithTier1Edges,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Distance: 1, Confidence: 0.95},
		},
		TransitiveItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Distance: 2, Confidence: 0.85},
		},
	}
	gotD, gotT := FoldExternalStaticItems(nil, nil, external, "/repo")
	if len(gotD) != 1 || len(gotT) != 0 {
		t.Errorf("want direct=1 trans=0 after cross-list dedup, got direct=%d trans=%d", len(gotD), len(gotT))
	}
}

func TestFoldExternalStaticItems_AbsoluteFileIdPassthrough(t *testing.T) {
	// When SCIP already stores an absolute FileId, dedup should still match
	// LIP's absolute URI — filepath.Join shouldn't double-prefix.
	direct := []ImpactItem{
		{Name: "X", Kind: DirectCaller, Distance: 1,
			Location: &Location{FileId: "/repo/x.go"}},
	}
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceTier1,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/x.go#X", Distance: 1, Confidence: 0.95},
		},
	}
	gotD, _ := FoldExternalStaticItems(direct, nil, external, "/repo")
	if len(gotD) != 1 {
		t.Errorf("abs FileId dedup failed: got %d items, want 1", len(gotD))
	}
}

func TestFoldExternalStaticItems_DistanceDefault(t *testing.T) {
	// LIP sometimes omits Distance=0. Direct items should default to 1,
	// transitive to 2.
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipOnly,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Confidence: 0.95},
		},
		TransitiveItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/b.go#B", Confidence: 0.85},
		},
	}
	gotD, gotT := FoldExternalStaticItems(nil, nil, external, "/repo")
	if len(gotD) != 1 || gotD[0].Distance != 1 {
		t.Errorf("direct distance default = %d, want 1", gotD[0].Distance)
	}
	if len(gotT) != 1 || gotT[0].Distance != 2 {
		t.Errorf("transitive distance default = %d, want 2", gotT[0].Distance)
	}
}

func TestFoldExternalCalleeItems_TagsCalleeKinds(t *testing.T) {
	// Mirrors the Caller test pattern, but asserts items are tagged with
	// DirectCallee / TransitiveCallee rather than the caller kinds. Guards
	// against accidental cross-wiring of foldExternalItemsWithKinds.
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipWithTier1Edges,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Distance: 1, Confidence: 0.95},
		},
		TransitiveItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/b.go#B", Distance: 2, Confidence: 0.85},
		},
	}
	gotD, gotT := FoldExternalCalleeItems(nil, nil, external, "/repo")
	if len(gotD) != 1 || gotD[0].Kind != DirectCallee {
		t.Errorf("direct kind = %q, want %q", gotD[0].Kind, DirectCallee)
	}
	if len(gotT) != 1 || gotT[0].Kind != TransitiveCallee {
		t.Errorf("transitive kind = %q, want %q", gotT[0].Kind, TransitiveCallee)
	}
}

func TestFoldExternalCalleeItems_NilAndEmpty(t *testing.T) {
	// Short-circuits mirror the caller twin — nil external and "empty"
	// EdgesSource both must leave the input lists unchanged.
	seed := []ImpactItem{{Name: "seed", Kind: DirectCallee, Distance: 1}}

	gotD, gotT := FoldExternalCalleeItems(seed, nil, nil, "/repo")
	if len(gotD) != 1 || len(gotT) != 0 {
		t.Errorf("nil external: direct=%d trans=%d, want 1/0", len(gotD), len(gotT))
	}

	gotD, _ = FoldExternalCalleeItems(seed, nil, &ExternalBlastRadius{
		EdgesSource: EdgesSourceEmpty,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Distance: 1},
		},
	}, "/repo")
	if len(gotD) != 1 {
		t.Errorf("empty EdgesSource: direct=%d, want 1 (unchanged)", len(gotD))
	}
}

func TestFoldExternalCalleeItems_DistanceDefault(t *testing.T) {
	// Distance=0 from LIP should default to 1 for direct, 2 for transitive —
	// same semantics as the caller path.
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceScipOnly,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/a.go#A", Confidence: 0.95},
		},
		TransitiveItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/b.go#B", Confidence: 0.85},
		},
	}
	gotD, gotT := FoldExternalCalleeItems(nil, nil, external, "/repo")
	if gotD[0].Distance != 1 {
		t.Errorf("direct default distance = %d, want 1", gotD[0].Distance)
	}
	if gotT[0].Distance != 2 {
		t.Errorf("transitive default distance = %d, want 2", gotT[0].Distance)
	}
}

func TestMergeBlastRadius_DedupByFile(t *testing.T) {
	static := &BlastRadius{
		UniqueCallerCount: 2,
		RiskLevel:         "low",
	}
	external := &ExternalBlastRadius{
		SemanticItems: []ExternalSemanticItem{
			{FileURI: "file:///src/dup.rs", Similarity: 0.80, Source: "semantic"},
			{FileURI: "file:///src/dup.rs", Similarity: 0.75, Source: "semantic"}, // same file
			{FileURI: "file:///src/other.rs", Similarity: 0.70, Source: "semantic"},
		},
	}

	got := MergeBlastRadius(static, external)

	if got.SemanticCallerCount != 2 {
		t.Errorf("SemanticCallerCount = %d, want 2 (deduped)", got.SemanticCallerCount)
	}
}

// Folded LIP items carry their origin; the SCIP items they are merged into
// stay unmarked, so a consumer can tell them apart.
func TestFoldExternalItems_MarksSource(t *testing.T) {
	scip := []ImpactItem{{StableId: "scip-sym", Name: "A", Kind: DirectCaller, Distance: 1,
		Location: &Location{FileId: "a.go"}}}
	for _, tc := range []struct{ edges, want string }{
		{EdgesSourceTier1, "lip:tier1"},
		{EdgesSourceScipOnly, "lip:scip_only"},
		{"", "lip"},
	} {
		external := &ExternalBlastRadius{
			EdgesSource:     tc.edges,
			DirectItems:     []ExternalItem{{FileURI: "lip://local//repo/b.go", SymbolURI: "lip://local//repo/b.go#B"}},
			TransitiveItems: []ExternalItem{{FileURI: "lip://local//repo/c.go", SymbolURI: "lip://local//repo/c.go#C"}},
		}
		gotD, gotT := FoldExternalStaticItems(scip, nil, external, "/repo")
		if len(gotD) != 2 || len(gotT) != 1 {
			t.Fatalf("edges %q: got %d direct, %d transitive; want 2, 1", tc.edges, len(gotD), len(gotT))
		}
		if gotD[0].Source != "" {
			t.Errorf("edges %q: SCIP item Source = %q, want empty", tc.edges, gotD[0].Source)
		}
		if gotD[1].Source != tc.want || gotT[0].Source != tc.want {
			t.Errorf("edges %q: LIP Source = %q / %q, want %q", tc.edges, gotD[1].Source, gotT[0].Source, tc.want)
		}
		calleeD, _ := FoldExternalCalleeItems(nil, nil, external, "/repo")
		if len(calleeD) != 1 || calleeD[0].Source != tc.want {
			t.Errorf("edges %q: callee Source = %+v, want %q", tc.edges, calleeD, tc.want)
		}
	}
}

func TestFoldExternalStaticItems_FileCountOnePerFile(t *testing.T) {
	// SCIP has a caller in a.go (relative); LIP adds another caller in the
	// same file (absolute) and one outside the repo. FileCount must see a.go once.
	direct := []ImpactItem{{StableId: "scip-a", Name: "A", Kind: DirectCaller, Distance: 1,
		Location: &Location{FileId: "pkg/a.go"}}}
	external := &ExternalBlastRadius{
		EdgesSource: EdgesSourceTier1,
		DirectItems: []ExternalItem{
			{SymbolURI: "lip://local//repo/pkg/a.go#B", Distance: 1, Confidence: 0.6},
			{SymbolURI: "lip://local//other/c.go#C", Distance: 1, Confidence: 0.6},
		},
	}
	gotD, _ := FoldExternalStaticItems(direct, nil, external, "/repo")
	if len(gotD) != 3 {
		t.Fatalf("want 3 items, got %d", len(gotD))
	}
	if got := gotD[1].Location.FileId; got != "pkg/a.go" {
		t.Errorf("in-repo LIP FileId = %q, want pkg/a.go", got)
	}
	if got := gotD[2].Location.FileId; got != "/other/c.go" {
		t.Errorf("out-of-repo LIP FileId = %q, want /other/c.go", got)
	}
	if br := RecomputeBlastRadius(gotD); br.FileCount != 2 {
		t.Errorf("FileCount = %d, want 2", br.FileCount)
	}
}
