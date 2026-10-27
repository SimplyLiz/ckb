# Knowledge Store — Retrieval and agent-facing decisions (input document)

Status: architect decision document, 2026-09-17. Input to the design spec; not the spec itself.
Scope: retrieval, navigation, ranking, tool surface, learning loop, evaluation.
Companion: 01-data-model-decisions.md (tiers, identity, schema, ingestion).

## 0. Assumptions and requirements on the data model

### 0.1 Corrections to the survey, verified in code

| Survey claim | Verdict | Evidence |
|---|---|---|
| `callgraph.caller_id` is NULL in all rows | confirmed as a code path | `internal/incremental/updater.go:240-248` writes nil unless `edge.CallerID != ""` |
| PPR graph is per-process | worse: it is per call | `internal/query/symbols.go:663` calls `BuildGraphFromSCIP` inside `SearchSymbols`; `internal/query/ranking.go:217-229` → `graph.BuildFromSCIP` (`internal/graph/builder.go:33-84`) walks every document and occurrence. No memoization. |
| Cross-tier joins are string matching | confirmed | tests by glob `internal/query/compound.go:1350-1385`; ADRs by path-prefix walk `internal/query/annotation_integration.go:39-60`; module = `filepath.Dir` `compound.go:1696-1701` |
| Cartographer results are transient | confirmed | `internal/query/compound.go:1610-1626` |
| No embeddings unless LIP is present | confirmed; LIP is already two signals | query expansion `internal/query/query_expansion.go:30-60`; semantic re-rank `internal/query/lip_ranker.go:45-62`, gated at `symbols.go:671-676` |

### 0.2 Drift to fix before any new ranking layer

1. `docs/hybrid-retrieval.md:74-87` documents a fusion table (FTS .40 / PPR .30 / Hotspot .15 / Recency .10 / Exact .05) that production never executes. `FusionRanker.Rank` has no non-test caller; the live path is `RerankWithPPR` (`ranking.go:232-294`) with hard-coded `0.6*position + 0.4*ppr` (`ranking.go:277`); `HotspotScores`/`RecencyScores` in `RankOptions` (`ranking.go:114-128`) are never filled. Delete or wire; do not build on top.
2. PPR already computes explanation paths (`PPROptions.IncludePaths` default true, `ppr.go:37-52`) and `RerankWithPPR` drops them (`ranking.go:256-260`).

### 0.3 Access paths the store must make cheap

"Cheap" = one indexed query, no per-node loop, no string matching. p95 for this repo's scale (836 files, 19k symbols, 48k call edges).

| # | Access path | Signature | Why | Budget |
|---|---|---|---|---|
| A1 | resolve any handle → node id | `resolve(handle) → (nodeId, kind, confidence, alternatives[])`; accepts bare name, qualified name, file path, `path:line`, raw SCIP string, prior alias, short id | agents paste back whatever we printed; `Understand` re-runs full `SearchSymbols` to resolve (`compound.go:1083-1137`) | <2 ms |
| A2 | ancestors in one read | `ancestors(nodeId) → [file, module, repo]` | every pack attaches module context to every hit | <1 ms |
| A3 | top-K children by rank | `children(nodeId, kind, k, orderBy=importance)` precomputed importance | module packs need "the 10 symbols that matter" | <3 ms |
| A4 | typed edge scan both directions | `edges(nodeId, kinds[], dir, limit, orderBy=weight)` indexed `(src,kind)`, `(dst,kind)` | retires per-call `BuildFromSCIP` and in-memory `CallerIndex` | <3 ms, k≤200 |
| A5 | closure summary overlay | `closureSummary(nodeId) → {transitiveCallers, moduleSpread, maxDepth, fileCount}` | `AnalyzeImpact` walks depth-4 then truncates; ranking needs counts | <1 ms |
| A6 | batch overlay fetch | `overlays(nodeIds[], kinds[], asOf)`, index `(node_id, kind, repo_state)` | 50–500 nodes × 6 kinds per pack | <10 ms |
| A7 | reverse overlay lookup | `overlayTargets(kind, value) → nodeIds[]` | ADR → nodes, owner → nodes | <3 ms |
| A8 | node-level time travel | `changedSince(nodeId, sinceState)`, `changedNodes(stateA, stateB)` | the time axis; git diff is file-granular | <10 ms |
| A9 | FTS returning node ids | `fts(text, tierFilter, k) → [(nodeId, bm25)]` over symbols, doc chunks, module responsibility | candidate selection must not return strings to re-match | <5 ms |
| A10 | persisted PPR-ready edge list + top-N neighbours | `graphEdges(limit)`, `neighborsTopN(nodeId, n)` | load once per process or skip graph for small fanouts | load <200 ms |
| A11 | usage overlay batch read/write | as A6, namespace `usage:*`, written by rollup job | §5 | <5 ms |
| A12 | snapshot-consistent read | every path accepts `asOf=repoStateId` and reports the state served | packs must not mix states silently | — |
| A13 | cheap negative answers | `exists(nodeId)`, `hasEdges(nodeId, kind)` | `symbolExists`, negative fixtures | <1 ms |

A1, A4, A6, A12 are load-bearing. A5, A8, A10, A11 are optimizations.

## 1. What "RAG-like" means here

**R1 — The retrieval unit is a fact, not a passage.** `(nodeId, factKind, value, source, confidence, repoState, edgeProvenance?)`. Packs are sets of facts. Dedup, budgeting, ranking, staleness operate on one record type; no stage can emit text selected by similarity alone.
Rejected: chunk-and-embed the repo (violates `docs/ideas.md:384-386`; the agent reads code better than we chunk it; vector store breaks single binary). Rejected: LLM summaries as primary retrieval targets (overlay only, confidence ≤0.3).

**R2 — Seven stages, each a package boundary:** `intent → seed → expand → gather → rank → pack → explain`.

| Stage | Does | Existing | New |
|---|---|---|---|
| intent | deterministic classification into an intent enum; rules + param shape; no LLM | — | `internal/retrieval/intent` |
| seed | ≤50 candidate nodes: A1 exact, A9 FTS, path/glob (`compound.go:331-360`), git-recency, optional LIP `NearestByText`/`NearestBySymbol` (`lip/client.go:319,369`), `QueryExpansion` (`client.go:650`) | FTS, LIP, resolvers | seed budgeter, per-intent seed mix |
| expand | typed-edge frontier under per-intent edge budget (A4), plus PPR for diffuse fanout, seeded from the store | `graph.PPR`, `DefaultEdgeWeights` | expansion planner, frontier cap |
| gather | one batched fetch per overlay kind (A6); structural facts only | overlay repositories | batch gather |
| rank | evidence weight × intent relevance × freshness × redundancy (§4) | retires `RerankWithPPR`; keeps `PPRResult.Path` as provenance | `internal/retrieval/rank` |
| pack | token-budgeted assembly, section quotas, explicit truncation report | `internal/compression` (count-based head truncation today), `internal/envelope` | token-aware packer |
| explain | per-fact provenance, per-section asOf + confidence, envelope `Confidence{Score,Tier,Reasons,Factors}` | `envelope.go:22-34` | evidence → Factors mapping |

Pack cache: `view_cache` keyed `(intent, sortedSeedIds, budget, repoStateId)` — already state-keyed, currently 0 rows.

**R3 — Embeddings admitted in exactly two places, labelled.** seed (candidates carry `origin`), rank (tie-break ≤0.05). Forbidden in gather. Every node in a pack carries `origin: structural | semantic-confirmed | semantic-only`; semantic-only goes to a separate `leads[]`, ≤5 % of budget, never in an answer section, never counted in confidence. Generalizes `SemanticCallerInfo{Tier, Confidence}` (`impact.go:113-134`).
Rejected: semantic-only hits in the body with a low score. An LLM discounts by section placement, not by a number in a field.

**R4 — Hard caps.** Seeds ≤50, frontier ≤500, packed facts ≤120, default budget 2,000 tokens, max 6,000 without explicit `budget`.

## 2. What the agent actually asks

### 2.1 Token arithmetic (~4 bytes/token, `wide_result_metrics.go:164-168`, see risk 7.8)

| Element | Tokens |
|---|---|
| identity line (handle, kind, file:line, visibility) | ~25 |
| edge row | ~15 |
| overlay fact with source + asOf | ~25 |
| signature | 30–60 |
| doc/ADR reference | ~20 |
| one raw SCIP symbol string | 40–60 |
| JSON overhead per section | ~30 |

2,000 tokens ≈ 70–100 facts. A 50-item response echoing raw SCIP ids pays 2,000–3,000 tokens in identifiers alone (→ N3).
Worth 500 tokens: locate, ownership/reviewers, "is there an ADR", "what changed here", existence checks. Worth 5,000: pre-change safety pack for a public symbol with >20 cross-module dependents; first-contact module onboarding. Nothing else.

### 2.2 The nine questions

| # | Question | Intent | Tiers | Edges (depth) | Budget | Sections in order | Omit |
|---|---|---|---|---|---|---|---|
| Q1 | What does X do, who depends on it | `understand` | symbol, file, module, docs, churn, ownership | contains⁻¹ (1), calls⁻¹ (1, counts to 3), calls (1), implements (1), documented-in (1) | 2,000 | identity; one-line purpose; signature; callers by module (top 12 + counts); callees (top 8); tests reaching it; overlays; docs/ADRs; leads | bodies; per-file reference lines; stdlib callees; zero-edge items |
| Q2 | If I change X, what breaks, which tests | `prepare-change` | + tests, co-change, ADR, risk, telemetry | calls⁻¹ (3, summarized beyond 1), contains (1), co-changes-with (1), tested-by (2), decided-in (1) | 3,000 (5,000 if moduleSpread>3 and public) | verdict line (risk + 2 driving factors); breaking surface; direct dependents by module; transitive summary (counts); tests to run as runnable command; untested downstream paths; co-changed files; ADRs; reviewers; arch impact | full transitive lists; generic advice |
| Q3 | Where is the code for concept Y | `locate` | FTS over symbol+doc+responsibility, module, file | seed only + contains⁻¹ | 500 | ranked candidates (handle, file:line, why, origin); one "start here" | everything else |
| Q4 | Shape of this module, where do I add | `module-map` | module, file, symbol, arch, ownership, responsibility | contains (2), imports/imports⁻¹ (1), owned-by (1) | 1,500 (3,000 deep) | responsibility + role; file table with symbol counts/roles; public surface; module edges; entry points; conventions; hotspots | per-symbol detail; private symbols; imports >10 |
| Q5 | What changed recently here and why | `history` | node change log, git, ADR, churn | changed-at (time), co-changes-with (1), decided-in (1) | 1,000 | last N structural changes at node; commits with authors; ADRs dated near; churn trend; co-changed neighbours | full messages; diffs |
| Q6 | Is there an ADR/doc about this | `docs` | docs, decisions, module | documented-in (1), decided-in (1), then ancestors fallback | 400 | direct hits; module-level hits labelled inherited; "none found" is valid | bodies |
| Q7 | How does A reach B | `path` | symbol, file, module | calls BFS ≤6, k-shortest 3 | 1,200 | shortest path as numbered chain with file:line; ≤2 alternatives summarized; per-hop confidence; "no path" + what was searched | node detail beyond handle+location |
| Q8 | I finished editing, what did I touch, what to verify | `assess-change` | working-tree diff, symbol, tests, contracts, ADR, ownership | as Q2 seeded from changed nodes | 2,500 | changed nodes by file; contract changes (labelled heuristic without reindex); tests to run; consumers with no reaching test; ADRs; reviewers; stale-index warning | unchanged code |
| Q9 | Everything about X in N tokens | `focus` | all | intent-blended | N | allocator over Q1/Q2/Q4/Q5/Q6 sections by intent prior; returns `budget.used` and `budget.dropped[]` | what the allocator drops, named |

### 2.3 Response-shape rules for an LLM consumer

1. Verdict first: section 1 is one actionable sentence; the rest is evidence for it.
2. Counts before lists: `{"total":214,"modules":7,"shown":12,"items":[…]}` never a bare truncated array (`UnderstandReferences` does this, `compound.go:1004-1009`; `PrepareChangeResponse` does not).
3. Short handles in the body, one id map at the end (N3).
4. Group by module, order by evidence weight within group.
5. Omit empty fields.
6. Every section header carries `asOf` and `conf`; not per fact.
7. No generated prose advice (`ExploreResponse.Suggestions`, `PrepareRisk.Suggestions`); use `suggestedNextCalls` (`envelope.go:95-100`).
8. Truncation is a fact: `dropped: [{"section":"references","kept":8,"total":214,"reason":"budget"}]`.

## 3. Navigation primitives

**N1 — Four tools, not twenty; shrink the core preset.** Today: 110 tools, page size 40 (`mcp/cursor.go:13`), core preset ~28 (`mcp/presets.go:29-76`), `EstimateActiveTokens` (`mcp/server.go:421-431`).

| Tool | Purpose | Replaces |
|---|---|---|
| `ckbFind` | intent-aware candidate selection; ranked node refs + why; no bodies | most `searchSymbols`, `symbolExists`, `listKeyConcepts` |
| `ckbNavigate` | one call moves up/down/sideways/time from any node | `getCallGraph`, `findReferences`, `traceUsage`, `getModuleOverview`, `explainPath`, `recentlyRelevant` |
| `ckbContext` | intent + budget → pack | `explore`, `understand`, `explainSymbol`, `explainFile`, `getArchitecture`, `exportForLLM` |
| `prepareChange` / `assessChange` | names unchanged, reimplemented as `ckbContext(intent=…)` | product front door |

Core preset target: 10–14 tools. Acceptance: `EstimateActiveTokens()` for core −50 % with no regression in tokens-to-answer (§6).
Rejected: one tool per movement (600–900 tokens per session in tool list; multiplies wrong-tool failures). Rejected: one mega-tool with string op (defeats schema validation).

**N2 — `ckbNavigate` shape.**
```json
{"from":"internal/query/symbols.go:412","direction":"out","edges":["calls","implements"],
 "depth":2,"limit":30,"since":null,"budget":600,"include":["location","module"]}
```
`direction` ∈ up | down | out | in | time. `edges` closed vocabulary ≤12: calls, references, implements, contains, imports, co-changes-with, tested-by, owned-by, documented-in, decided-in, observed-calling, similar-to (always labelled). Per-direction defaults so `{"from":X,"direction":"in"}` is always valid. Row:
```json
{"id":"n:9c2","handle":"query.Engine.FindReferences","edge":"calls","depth":1,
 "at":"internal/query/symbols.go:812","module":"internal/query",
 "weight":0.91,"origin":"structural","evidence":"scip:reference","asOf":"head@1ce495b"}
```

**N3 — Short ids with a per-response id map.** `id:"n:9c2"` (stable within a repo-state, resolvable via A1) + `handle`. Raw SCIP strings only under `include:["rawIds"]`. Saves 40–60 tokens per referenced symbol; independent of the store. A1 must accept all three id forms forever because agents cache ids across sessions.

**N4 — `ckbContext` is the default entry point.**
```json
{"target":"SearchSymbols","intent":"understand","budget":2000,"include":["tests","docs"]}
```
Response: `schemaVersion`, `data.intent`, `data.focus{id,handle,kind,at}`, `data.verdict`, `data.sections[{name,asOf,conf,total,shown,facts[]}]`, `data.budget{requested,used,dropped[]}`, `data.leads[]`, `meta.confidence{score,tier,reasons,factors}`. `intent` optional; inferred intent is echoed.

**N5 — Deprecate by aliasing.** `understand` → `ckbContext(intent=understand)`, `explore` → `module-map`, `getCallGraph` → `ckbNavigate`. Thin adapters for two minor versions.

## 4. Ranking and evidence

**E1 — One evidence weight table.**

| Source | Class | Base | Envelope tier |
|---|---|---|---|
| SCIP def/ref at HEAD | exact | 1.00 | high |
| SCIP, N commits behind | exact-stale | 1.00 × decay(N) | medium |
| SCIP, file dirty | exact-preedit | 0.70 | medium |
| ADR, CODEOWNERS, module annotation | declared | 0.90 | high |
| telemetry observed | observed | 0.75 | medium |
| LIP semantic, structurally confirmed | confirmed | 0.80 | medium |
| Cartographer resolved edge | structural-heuristic | 0.60 | low |
| tree-sitter derived | heuristic | 0.55 | low |
| git co-change (≥5, corr ≥0.4) | correlational | 0.50 | low |
| Cartographer scores | heuristic-score | 0.40 | low |
| LLM note | generated | 0.30 | speculative (must carry model id + prompt version) |
| LIP unconfirmed | speculative | 0.25 | speculative, leads only |

**E2 — Score.**
```
score(fact) = w_evidence × w_intent(factKind, intent) × freshness(factState, queryState)
            × (1 − redundancy(fact, packed)) + w_usage × usagePrior(node)   # w_usage ≤ 0.10
```
`w_intent` replaces the fusion table: relevance is a function of the question. Redundancy: one fact per (node, factKind) from the highest-weight source, `alsoFrom:[…]` appended; +0.05 per independent corroborating source, cap +0.10.

**E3 — Freshness shown, never hidden.** `freshness = 1.0 if equal; max(0.5, 0.98^commits) if behind; 0.7 if file dirty`. States: current, stale (kept, `asOf` + `staleBy`, sorted below current), superseded (omitted, `supersededCount`). Section header shows the oldest state it contains.

**E4 — Pack confidence on the existing envelope.** `Score` = token-share-weighted mean of packed fact scores. `Tier` = minimum tier among load-bearing sections. `Reasons` = two largest negative factors. `Factors` = one per source class used, signed impact. Rejected: single `Meta.Accuracy` string.

## 5. Learning loop (ledger → priors)

**L1 — Feed back as retrieval priors only, never as facts about code.** Signals: `usage:access` (decayed target counts), `usage:coaccess(a,b)` (same session, ≤5 calls apart), `usage:followThrough` (prepareChange(X) then assessChange whose changed nodes ∩ predicted blast radius — precision proxy for blast-radius ranking, the only closed loop in the product), `usage:refinement` (search followed by overlapping search; feeds §6 metrics, not ranking).
**L2 — Capped, decayed, session-excluded.** `w_usage ≤ 0.10`, applied only when structural scores are within 5 %; half-life 14 days; a session's own calls never feed its own priors; normalized per repo.
**L3 — Never inferred from the ledger:** code quality; anything entering a risk score or change-safety verdict (`usage:` readable in rank, unreadable in gather, enforced by type); ownership; intent narration; tokens saved; cross-repo/cross-user aggregation; prompt or file content.
**L4 — Rollup, not request-path scans.** `usage_overlay(node_id, kind, value, window_start, window_end)` written on MCP start + hourly ticker.
**L5 — Off by default until measured; always off in eval.**

## 6. Evaluation

Today `internal/eval` calls only `SearchSymbols` (`eval/suite.go:227-238`); metrics recall@K/MRR/latency; types needle/ranking/expansion.

**V1 — Six fixture types.**

| Type | Input | Ground truth | Metric |
|---|---|---|---|
| locate | NL description | expected nodes | tokens-to-target, calls-to-target |
| pack | question + intent + budget | required fact set + forbidden facts | fact recall, payload efficiency (required/delivered), budget adherence |
| impact | changed symbol | files touched in the real commit | blast-radius recall/precision; test-selection recall |
| crosstier | question needing ≥2 tiers | expected node | hops vs minimal; edge-walk correctness |
| staleness | dirty tree / behind index | which facts carry which label | stale-label accuracy |
| negative | no answer exists | "none" | cost of no (≤300 tokens) |

**V2 — Metrics:** `TokensToTarget{p50,p90}`, `CallsToAnswer`, `FactRecall`, `PayloadEfficiency`, `PackTokens`, `BudgetOverrunPct`, `StaleLabelAccuracy`, `CostOfNo`; keep RecallAtK, MRR, AvgLatency.
**V3 — Two-mode harness:** every fixture in `mode=legacy` and `mode=store`, same repo-state, report delta. Capture the legacy baseline before the store lands. Proposed public targets: tokens-to-target −50 %, fact recall ≥0.9 at 2k, payload efficiency ≥0.5 (today est. 0.15–0.25), impact recall ≥0.8, cost-of-no ≤300.
**V4 — Ground truth mined then hand-checked**; ~40 locate fixtures for CKB, ~20 each for 2 OSS repos. `internal/bench` stays separate.

## 7. Risks and open questions

1. Confidently stale: prepare/assess refuse a verdict above medium when index is behind and changed files are dirty.
2. Self-confirming ranking: L2, eval priors-off forever.
3. `ckbContext` becomes the new "everything" tool: BudgetOverrunPct, hard 6k cap, `ckbFind` deliberately cheap.
4. Identity churn: SCIP symbols embed a commit hash; alias chain never exercised, `symbol_mappings` empty. Lands on the data model.
5. Latency: store only helps if A4/A6 are genuinely batched.
6. Cartographer is build-tag gated; LIP degrades silently: packs must show labelled absence, not a missing section.
7. Ranking doc/code drift (§0.2) first.
8. Token estimation is bytes/4; identifiers tokenize worse; calibrate per field against a real tokenizer offline.
9. `exportForLLM` has its own regex symbol parser (`internal/export/exporter.go:279-385`); re-point or retire.
10. Behavioural data is developer data: local-only, opt-out, excluded from export and federation, aggregate-only; say so in user docs.

Open questions for the data model: does the canonical node id survive reindex without alias lookup; is there a node-level change log keyed by repo-state; are overlays versioned or latest-wins; are call-site nodes first-class; who writes `usage_overlay` under multi-process WAL; per-consumer budget defaults via `clientInfo`; are the discarded intermediates (depth-4 impact graph, risk factor breakdown, telemetry match quality) kept.

## 8. Sequencing

| Phase | Work | Needs store | Size |
|---|---|---|---|
| 0 | fix ranking drift; calibrate token estimator; short ids + id map; extend eval with six fixture types; capture legacy baseline | no | M |
| 1 | A1/A2/A4/A6/A12; `ckbNavigate` (up/down/out/in); `ckbFind`; pack v1 with understand/locate/module-map; evidence weights → envelope Factors | yes | L |
| 2 | A8 time axis; prepare-change/assess-change on the store; staleness labels; A5 closure summaries | yes | L |
| 3 | usage rollup + priors (default off); priors-on vs off eval | yes | M |
| 4 | tool surface consolidation; core preset 10–14; aliases; retire exportForLLM's parser | yes | M |
