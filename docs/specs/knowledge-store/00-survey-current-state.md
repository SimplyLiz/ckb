# Knowledge Store — Survey of the current state (2026-09-17)

Evidence base for the knowledge-store design spec. Everything below was read from
the code or measured on this repo's own `.ckb/ckb.db` (48 MB + 71 MB WAL,
798 files, scip-go). File references are `path:line`.

## 1. What is actually populated

Row counts in this repo's database (the most heavily used CKB instance we have):

| Table | Rows | Written by |
|---|---|---|
| callgraph | 48,326 | `internal/incremental/updater.go:79,446,616` |
| symbols_fts_content | 19,104 | `internal/storage/fts.go` (bulk at index load) |
| file_symbols | 18,535 | `internal/incremental/updater.go` |
| file_deps | 3,802 | `internal/incremental/updater.go` |
| indexed_files | 836 | `internal/incremental/updater.go` |
| query_cache | 176 | `internal/storage/cache.go` |
| symbol_mappings, symbol_aliases | 0 | writers exist (`internal/identity/repository.go:120`, `internal/api/index_processor.go:235`) but nothing in the MCP/CLI path calls them |
| modules, dependency_edges | 0 | `internal/storage/repositories.go:418,561` — no caller in the index path |
| hotspot_snapshots, ownership, ownership_history, responsibilities, decisions | 0 | writers in `repositories.go:794,1152,1385,1622`, never invoked by `ckb index` or any MCP tool |
| coupling_cache, risk_scores | 0 | same |
| docs, doc_references, doc_modules, symbol_suffixes | 0 | only `IndexDocs` (`cmd/ckb/docs.go:174`, MCP `tool_impls_v73.go:213`) writes; never run here |
| view_cache | 0 | — |
| lip_annotations | table does not exist | created lazily by `internal/storage/lip_annotations.go:35` when LIP is used |
| rescan_queue | 0 | designed, unused |

Conclusion: of 49 tables across 11 schema versions, five SCIP-derived tables and the
query cache carry all the data. The "architectural memory" (schema v2), developer
intelligence (v4), doc linking (v5) and telemetry (v3) layers are schema-only in
practice. Every MCP answer about hotspots, ownership, coupling, risk, modules is
computed from scratch per call and discarded.

## 2. Identity: three key spaces, one of them empty

- **Designed key**: `ckb:<repo>:sym:<sha256(container|name|kind|arity|sig)>`
  (`internal/identity/fingerprint.go`), with alias chains for renames. MCP tool
  descriptions promise this format (`internal/mcp/tools.go:96`). `symbol_mappings`
  is empty, so the designed key is not in the live path.
- **Live key**: raw SCIP symbol strings, e.g.
  `scip-go gomod github.com/SimplyLiz/CodeMCP 1ce495bdb45a `github.com/SimplyLiz/CodeMCP/internal/a2a`/TaskStatusUpdateEvent#`.
  Used by `file_symbols.symbol_id`, `callgraph.callee_id`, `symbols_fts_content.id`,
  `doc_references.symbol_id`, `observed_usage.symbol_id`, and returned to MCP
  clients as `symbolId`. Note the embedded commit hash (`1ce495bdb45a`): the SCIP
  symbol for a Go package changes identity with every reindex at a new commit for
  scip-go module-version symbols — the key is not stable across index runs.
- **File path** (repo-relative string): `indexed_files`, `file_deps`,
  `coupling_cache`, `risk_scores`, `docs`, `hotspot_snapshots.target_id` (when
  target_type='file'), ownership via glob patterns.
- **Module id** (string, format unclear because the table is empty): `modules`,
  `dependency_edges`, `doc_modules`, `decisions.affected_modules` (JSON array),
  `responsibilities.target_id` (when target_type='module').
- **Untyped target_id + target_type** columns (`hotspot_snapshots`,
  `responsibilities`) mix file/module/symbol keys in one column with no constraint.
- No table has a foreign key across key spaces. Every cross-tier join in
  `internal/query/compound.go` is done in Go by string matching (path prefix,
  suffix match, glob, name match).

## 3. The call graph is not where it looks like it is

- `callgraph` (48,326 rows): `caller_id` is NULL in **all** rows
  (`updater.go:244` writes nil when `edge.CallerID == ""`, which is always the case
  in the current extractor). The table stores callee + call-site location only.
- 2,683 rows have `caller_file` pointing into `~/Library/Caches/go-build/...`
  (test-binary documents from scip-go) — noise that is never filtered.
- The call graph actually used by `getCallGraph`, `findReferences`, `analyzeImpact`
  is `SCIPIndex.CallerIndex`, built in memory on first `FindCallers` from the SCIP
  documents (`internal/backends/scip/callgraph.go:140-200`, `buildCallerIndex`
  resolves callers by "occurrence inside a function range"). It is rebuilt in every
  process (each `ckb mcp` start) and never persisted.
- `internal/graph/builder.go:33 BuildFromSCIP` builds a second in-memory symbol
  graph (call, container, module edges with weights) for Personalized PageRank
  (`ppr.go`), per process, not persisted.

## 4. SCIP index lifecycle

- `.scip/index.scip` is mmapped and stream-parsed per process
  (`internal/backends/scip/loader.go`, `mmap_unix.go`, `streaming.go`).
- Derived per-symbol metadata (name, kind, docs, location, container index, sorted
  name index) is cached in `.ckb/scip_derived.gob` (8.3 MB), validated by
  mtime+size of the .scip file (`cache.go`).
- `RefIndex` (symbol → occurrences) and `DefinitionIndex` are in memory only.
- `PopulateFromFullIndexStreaming` (`internal/incremental/updater.go`) clears and
  rewrites indexed_files / file_symbols / callgraph / file_deps on every full index.
  Incremental invalidation (`file_deps` BFS, `rescan_queue`) is implemented but the
  queue is empty in practice.

## 5. Cartographer (Rust, cgo static lib, `-tags cartographer`)

Bridge: `internal/cartographer/bridge.go` (24 functions), library pinned by Makefile
to `third_party/cartographer/target/release/libcode_cartographer.a` v4.0.2, source at
`/Users/lisa/Work/Projects/Cartographer/mapper-core/codecartographer`.

What it delivers (all tree-sitter + git based, 13 languages, no type resolution):

| Function | Output (fields) |
|---|---|
| MapProject | Nodes{ModuleID, Path, Language, SignatureCount, IsBridge, BridgeScore, Degree, RiskLevel}, Edges{Source, Target, EdgeType, Resolution}, Cycles{Nodes, PivotNode, Severity}, GodModules{ModuleID, Degree, CohesionScore}, LayerViolations, Metadata{HealthScore, ArchitecturalDrift, …} |
| Health, CheckLayers, Evolution(days) | health score, layer violations, health snapshots + trend |
| SkeletonMap, RankedSkeleton(focus, budget), GetModuleContext | per-file imports + signatures, token estimates, rank, role |
| GitChurn, GitCochange, HiddenCoupling, ShotgunSurgery | file churn counts, co-change pairs with coupling score, co-changed-without-static-edge pairs, dispersion entropy |
| Semidiff(c1,c2) | per file: added/removed signatures |
| SimulateChange, BlastRadius | affected modules, will-create-cycle, risk level, health impact; dependents/dependencies |
| UnreferencedSymbols | per file symbol names with no reference |
| SearchContent, FindFiles, ExtractContent, BM25Search, QueryContext, ContextHealth | grep/BM25/context bundle with token health grades |
| RenderArchitecture | mermaid/dot |

How CKB uses it: ~100 call sites (`review_arch_health.go`, `review_layers.go`,
`review_blastradius.go`, `review_deadcode.go`, `ownership.go:248`, `navigation.go:740,2307,2758`,
`architecture.go:240`, `compound.go:1614`, `compound_refactor.go:227`,
`mcp/tool_impls_v86.go`, `skeleton.go`). **Every result is transient**: consumed
inside one request, serialized into the response, never written to SQLite.
Cartographer itself is stateless per call (re-scans the tree; optional
`.codecartographer_cache.json` / `.codecartographer_memory.json` for watch mode).
So a `reviewPR` re-runs MapProject, GitChurn, GitCochange, UnreferencedSymbols on
the whole repo every time.

Not consumed by CKB: get_cycles/get_dependencies/get_dependents/get_project_graph
as standalone, get_symbol_context, reach_symbol, map_state_machine,
list_key_handlers, doc_index/doc_context/query_docs, watch_graph/poll_changes,
diff_skeleton, focused_skeleton.

## 6. LIP (Liz Indexing Protocol)

`internal/lip/client.go`: best-effort client to a local Rust daemon over a Unix
socket (`~/.local/share/lip/lip.sock`), length-prefixed JSON. Provides nearest-
neighbour (embedding) queries by symbol URI, blast-radius batches, subscriptions,
stream context. Results may be stored as `lip_annotations(symbol_uri, key, value,
author_id, confidence, timestamp_ms, expires_ms)`. Degrades silently when absent.
In this repo the table has never been created.

## 7. Extractors: 28, of which 8 persist and 20 recompute per query

Persisting (when their producers are invoked, which the index path does not do):
hotspots, ownership, decisions, modules, docs, audit/risk_scores, telemetry,
federation. Recomputing per call: complexity (tree-sitter), impact (depth-4
transitive walk, then truncated to top-K), deadcode, unwired (entrypoint
reachability walk), explain, breaking (checks out two refs), testgap, secrets,
suggest, perf, extract, export, cycles (Tarjan on full graph), architecture,
envelope, tier, compression.

Same fact derived independently in several places:
- complexity: hotspots, audit, suggest, deadcode (4 implementations of the score)
- churn: hotspots (30/90d) vs suggest vs Cartographer GitChurn
- dependency edges: modules classifier, `dependency_edges`, cycles adjacency,
  Cartographer MapProject.Edges, `file_deps` (5 representations)
- ownership: `ownership` table, `modules.owner_ref`, federated_ownership
- test detection: testgap, deadcode, understand (3 heuristics)
- risk: audit, hotspots composite, impact RiskScore (3 formulas)
- co-change: `internal/coupling` (git log) and Cartographer GitCochange

Valuable intermediates thrown away: full depth-4 impact graph, hotspot
projection30d, telemetry match-quality factors, risk factor breakdown (only the
composite is stored), module import→target mapping during classification.

## 8. Compound queries and how they join

`explore`, `understand`, `prepareChange`, `planRefactor`, `exportForLLM`,
`explainSymbol`, `explainFile`, `getArchitecture`, `analyzeImpact`
(`internal/query/compound*.go`, `impact.go`, `architecture.go`) each fan out to
3–6 extractors in parallel and stitch by: SCIP symbol string → file path (via
symbol location), file path → module (path prefix), file path → owner (glob),
file path → hotspot (string equality), symbol → tests (file-name heuristic),
symbol → docs (suffix match). None of these joins is indexed; none is validated.

Cache: query_cache (key+HEAD, 300 s), view_cache (key+repoStateId, 3600 s),
negative_cache (60 s), lazy expiry on miss. Nothing between "raw SCIP" and
"final JSON response" is cached.

## 9. Retrieval today

`searchSymbols`: FTS5 (BM25 over name/documentation/signature) with prefix and
substring fallback, then hybrid re-rank with PPR graph proximity
(`docs/hybrid-retrieval.md`: recall@10 100 %, MRR 0.91, 4.5 ms on the eval
fixtures). No embeddings unless LIP is present. No cross-tier expansion: a search
hit is a symbol; the file/module/owner/hotspot context is only attached if the
caller then invokes a compound tool.

## 10. Constraints that shape the design

- Pure-Go SQLite (`modernc.org/sqlite`), single binary, cgo only for Cartographer.
  No second toolchain in the release pipeline (decision in
  `docs/plans/change-intelligence-and-activity-ledger.md`).
- Text first, evidence first; no UI in 1.x. Confidence + provenance are product
  features (`internal/envelope`).
- Positioning is change safety: `prepareChange` / `assessChange` / `reviewPR`
  are the primary consumers.
- Index freshness is tracked by repoStateId (`internal/repostate`) and HEAD.
