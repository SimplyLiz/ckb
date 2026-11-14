# CKB Knowledge Store — Design Specification

Status: Draft
Date: 2026-09-17
Version: 0.4

Inputs:
1. `docs/specs/knowledge-store/00-survey-current-state.md` — evidence about the current state
2. `docs/specs/knowledge-store/01-data-model-decisions.md` — data model decisions
3. `docs/specs/knowledge-store/02-retrieval-decisions.md` — retrieval and agent-facing decisions
4. `docs/specs/knowledge-store/03-reconciliation.md` — rulings that resolve conflicts between 01 and 02

This document is self-contained. Where it summarizes an input, the input is cited as `NN §x.y` (e.g. `01 §3.2`); where it cites code, the citation is `path:line` and was read from the inputs, not re-derived. **All line numbers are as of commit `5554fd4`** and drift by 1–4 lines against later commits; the symbol named in each citation is the thing to look for.

Changes in 0.2, 0.3 and 0.4:

0.2:

- A1 — `node_fts` `body` column removed; body text moves to a `node_body` side table with its own external-content FTS (§6.2).
- A2 — `edge_type` tier pair becomes a bitmask (`src_tiers`/`dst_tiers`) so one edge type can span tiers and keep a write-time check (§5.2, §6.2, §6.3).
- A3 — ruling: edges are HEAD-only; the A12 validity window applies to `node` and `fact` (§7.2, Appendix C).
- A4 — evidence base weight is 1.00 for all three SCIP rows; freshness enters once, via §10.2 (§10.1).
- A5 — `source` and `repo_state` move to Phase 1 alongside the tables whose FKs point at them (§13.2).
- A6 — `carry_forward` added to `fact_key`, not `source` (§6.2, §7.3).
- A7 — `usage_overlay` DDL added (§6.2) and listed in the table disposition (§13.1).
- A8 — wire ↔ storage edge mapping table added, with the deliberately non-navigable types named (§9.2).
- A9 — `n:` id stability in §9.4 corrected to match §5.5.
- A10 — `parent_id` rule for nested symbols stated (§4.1).
- A11 — canonical ordering `src_id < dst_id` for undirected edge types (§6.3).
- A12 — blame-derived `owns` moves to index time; no request-path writes (§7.1).
- B1 — WAL figure unified at 74 MB (§2).
- B2 — §2.8 heading corrected to four bugs.
- B3 — §9.1 heading corrected to three new tools.
- B4 — duplicate "No vector store" bullets merged (§3.2).
- B5 — duplicate `cartographer_run` DDL removed from §7.5.
- B6 — cross-references in §5.5 and §6.2 repointed at Appendix B / §7.5 / §14.3.
- B7 — distinct example ids in §9.2 and §9.3.
- B8 — missing `REFERENCES` added on `edge` and `node_alias` (§6.2).
- B9 — §9.4 now points at the full id-form list in §5.5.
- B10 — relationship between `UNIQUE(src_id, type, dst_id)` and `idx_edge_out` stated (§6.7).
- C1 — `localPkg` is a set discovered from go.work/go.mod (§5.1).
- C2 — derive-once enforcement mechanism made concrete (§7.4).
- C3 — edge row at 10× labelled an estimate pending Phase 0 re-measurement (§6.6).
- C4 — `index_meta` gains a *key*, not a column (§5.7).

0.3:

- N1 — `edges:["contains"]` with `direction:out|in` stated as an alias for `direction:down|up`; same walk either way (§9.2).
- N2 — §9.5 Q5's `changed-at (time)` replaced by `direction:time (A8)`, which is the actual mechanism (§9.5).
- N3 — §10.2's score formula split so the usage prior's 5% gate is explicit, matching §11.2 (§10.2).
- N4 — `resolve()` returns the split case of §5.3 as `alternatives[]` with `confidence < 1` (§5.5).
- N5 — `view_cache` shape cited from `schema.go:478-484` and the composite-key serialization stated (§8.5).
- N6 — §13.1 heading corrected from "~14" to 15 model tables, counted from §6.2 plus `node_alias` (§13.1).
- N7 — payload-efficiency baseline labelled as quoted from `02 V3` and unmeasured (§12.3).
- N8 — `arch.health` trend is the key's own temporal history, not a second key (§6.4).

0.4:

- V1 — Appendix B A2/A3/A10 signatures and budgets made concrete or labelled estimates; §9.5 edge notation defined; §12.3 targets and §6.6 comparison labelled as proposals/estimates.
- F1 — generation publication protocol replaces "atomically renames"; explicit handoff, reopen on epoch change (§7.7, §14.1).
- F2 — `repo_state.published`; readers resolve `asOf` only over published states; each pack pins one read transaction (§6.2, §7.7, Appendix C).
- F3 — `edge` loses `source_id`/`confidence`/`first_state`/`last_state`/`owner_file_id`; new `edge_evidence` table; `occurrence` gains `source_id` (§4.2, §6.2, §6.5, §6.7, §7.2, §9.2, §13.1).
- F4 — nodes are not versioned; A12 on `node` means "existed at `asOf`", historical attributes come from `node_change` replay (§5.3, §5.4, Appendix C).
- F5 — invalidation never deletes facts; one exact → carry-forward → stale → retention order stated once (§7.2, §7.3, Appendix C).
- F6 — FTS sync triggers added to the DDL, plus the bulk-load drop/rebuild/recreate pattern (§6.2).
- F7 — per-connection PRAGMAs via a connection-init hook, and a one-writer/N-reader pool policy (§7.7, §13.2).
- F8 — second caller bug (SCIP 3-element range decoded as an end line) added as bug #6 and folded into Phase 0 (§2.8, §13.2, §14.1).
- F9 — `n:` ids carry the epoch: `n:<db_epoch>.<node.id>` (§5.5, §5.7, §9.4, Appendix C).
- F10 — invalidation ownership is per evidence row, per producer; stated as a table (§7.2).
- F11 — "version equals short HEAD" removed; locality comes from the indexer's own module set; replace/vendor/GOWORK fixtures added (§5.1, §5.6, §14.1).
- F12 — `conf(fact)` defined separately from `score(fact)`; pack `Score` and the verdict read `conf` only (§10.2, §10.5, §11.3).
- F13 — new edge type `refers` (file → symbol) for references with no resolvable enclosing symbol (§6.3, §9.2, §13.1, §13.2).
- F14 — `CanonicalSymbolKey` needs a new lossless SCIP parser; the existing one drops the version field (§5.1).
- F15 — edge index column order corrected to `(src_id, type, weight DESC, dst_id)`; the v0.2 B10 sentence deleted (§6.2, §6.7, Appendix C).
- F16 — phase table fixed: `n:` ids move to Phase 1, A6 and module-map move to Phase 2 (§13.2).
- F17 — tier-1 `kind=package` containers are keyed by package path, not directory; `_test`/generics/workspace fixtures required (§4.1, §5.6).
- F18 — `rank.importance` and `closure.summary` added as derived fact keys; `idx_fact_text` added; A7 is num/text only (§6.4, §6.7, §7.1, Appendix B).
- F19 — pack-cache key widened and given a TTL (§8.5).
- F20 — `usage_overlay.node_b` for pair-keyed co-access; the own-session-exclusion promise is dropped (§6.2, §11.1, §11.2, §11.4).

---

## 1. Summary

CKB computes almost everything on every call and keeps almost nothing. Of 49 tables in the current schema, 5 hold data; the rest — architectural memory, developer intelligence, doc linking, telemetry — are schema-only in this repo's own database (`00 §1`). The call graph that three tools depend on (`getCallGraph`, `findReferences`, `analyzeImpact`) is not the persisted `callgraph` table (which has a NULL `caller_id` in all 48,326 rows) but an in-memory index rebuilt from the raw SCIP file on every process start, and the ranking graph behind it is rebuilt on every *call* (`02 §0.1`). Twenty of 28 extractors recompute their answer from scratch per query; the same fact — complexity, churn, dependency edges, ownership, test detection, risk, co-change — is computed by 2–5 independent implementations that can and do disagree (`00 §7`).

This is being built now because the root causes are cheap to fix and the payoff compounds: two small fixes in `internal/incremental/extractor.go` — the caller kind filter and the SCIP range decoding — repair the caller graph (`01 §0.1`, §2.2, bug #6 in §2.8); a single function (`CanonicalSymbolKey`) removes the reason every reindex currently discards every fact by making the reindex-stable node id possible at all (`01 §2.2`). Everything else in this spec — the schema, the retrieval pipeline, the tool surface — depends on that one change existing first.

**The three axes.** The store organizes all data as a cube: **level** (repo ⊃ container ⊃ file ⊃ symbol, vertical, via `parent_id`), **relation** (typed edges — calls, imports, owns, tests, documents — horizontal), and **state** (evidence: `source`, `repo_state`, `observed_at`, `confidence`, carried on every edge and fact). A cell answers: what does relation R (or fact K) say about node N, as of state S, according to source P (`01 §1.5`). All three coordinates are indexed; provenance is part of the primary key, which is what makes "evidence first" a structural property instead of a slogan.

**The single unlocking change.** Today a SCIP symbol for a Go package embeds the indexed commit hash, so a reindex at a new commit changes the identity of every symbol and `PopulateFromFullIndexStreaming` must delete and rewrite the whole database every time (`01 §2.2`, `updater.go:405-413`). `CanonicalSymbolKey(scipSymbol, localPkgs)` strips the local package's version field to SCIP's unspecified-version placeholder (`.`), leaving external package versions untouched. With that key, an unchanged symbol keeps its integer node id across reindexes, and every fact and edge attached to it survives without recomputation. This is Phase 0, ships with no schema change, and unlocks everything that follows (`03 R-4`).

**The three first materializations** (`01 §5.3`):
1. Symbol-level `calls` edges with a real caller (fixes the NULL `caller_id` bug; retires the per-call in-memory rebuild for the hot path).
2. The containment spine (`parent_id`, `container_id`, `file_id` on every node) plus `owns` edges, replacing five ad hoc "module = path prefix" / "owner = glob match" computations with one indexed join.
3. The fact plane for complexity, churn, hotspot and risk, computed once per index run with provenance and a stored factor breakdown, replacing 2–4 independent per-query implementations of each.

**What the agent gets.** Three new MCP tools replace the bulk of the current 110-tool surface for navigation and context: `ckbFind` (intent-aware candidate selection, no bodies), `ckbNavigate` (one call moves up/down/sideways/time from any node), and `ckbContext` (intent + budget → a token-budgeted, provenance-carrying pack). `prepareChange` and `assessChange` keep their names and their place as the product's front door; they are reimplemented as `ckbContext` calls with a fixed intent (`02 N1`). Old tool names become thin aliases for two minor versions (`02 N5`). The retrieval unit throughout is a fact, not a passage — no stage in the pipeline selects text by embedding similarity alone (`02 R1`).

---

## 2. Problem statement

All numbers in this section are measured on this repo's own `.ckb/ckb.db` (48 MB + 74 MB WAL, 798 files, scip-go) or read from the cited source lines (`00`, `01 §0`). `00` recorded 71 MB of WAL at an earlier checkpoint; 74 MB is the re-measurement in `01 §0.1` item 6 and is the figure used throughout.

### 2.1 Schema-only tables

| Table(s) | Rows | Writer exists? | Ever called in the index/MCP path? |
|---|---|---|---|
| `callgraph` | 48,326 | yes, `internal/incremental/updater.go:79,446,616` | yes, but see §2.2 |
| `symbols_fts_content` | 19,104 | yes, `internal/storage/fts.go` | yes |
| `file_symbols` | 18,535 | yes, `internal/incremental/updater.go` | yes |
| `file_deps` | 3,802 | yes, `internal/incremental/updater.go` | yes |
| `indexed_files` | 836 | yes, `internal/incremental/updater.go` | yes |
| `query_cache` | 176 | yes, `internal/storage/cache.go` | yes |
| `symbol_mappings`, `symbol_aliases` | 0 | yes, `internal/identity/repository.go:120`, `internal/api/index_processor.go:235` | no caller in the MCP/CLI path |
| `modules`, `dependency_edges` | 0 | yes, `internal/storage/repositories.go:418,561` | no caller in the index path |
| `hotspot_snapshots`, `ownership`, `ownership_history`, `responsibilities`, `decisions` | 0 | yes, `repositories.go:794,1152,1385,1622` | never invoked by `ckb index` or any MCP tool |
| `coupling_cache`, `risk_scores` | 0 | yes | same |
| `docs`, `doc_references`, `doc_modules`, `symbol_suffixes` | 0 | only `IndexDocs` (`cmd/ckb/docs.go:174`, MCP `tool_impls_v73.go:213`) | never run in this repo |
| `view_cache` | 0 | yes | never populated |
| `lip_annotations` | table does not exist | created lazily by `internal/storage/lip_annotations.go:35` | only when LIP is present |
| `rescan_queue` | 0 | designed | unused |

Of 49 tables across 11 schema versions, five SCIP-derived tables and the query cache carry all the data. The "architectural memory" (schema v2), developer intelligence (v4), doc linking (v5) and telemetry (v3) layers are schema-only in practice. Every MCP answer about hotspots, ownership, coupling, risk, and modules is computed from scratch per call and discarded (`00 §1`).

### 2.2 The call graph is not where it looks like it is

- `callgraph` has 48,326 rows; `caller_id` is NULL in **all** of them.
- **Root cause** (`01 §0.1`, item 1): `resolveCallerSymbol` (`internal/incremental/extractor.go:627-631`) filters candidates on `sym.Kind == "function" || "method"`. `mapSymbolKind` (`extractor.go:216-223`) returns `"unknown"` because scip-go never sets `SymbolInformation.Kind` (`extractor.go:471`, `internal/backends/scip/callgraph.go:256`). The callee side has a descriptor-based fallback, `isFunctionSymbol` (`extractor.go:450`); the caller side has no such fallback, so every caller candidate is filtered out and `updater.go:244` writes `nil`. The in-memory `buildCallerIndex` (`callgraph.go:245-289`) happens to use the same fallback and therefore works — which is why the bug is invisible to anyone using the live tools. This is called out in `01` as "~5-line fix; highest-leverage change in the store." **The kind filter is not the only defect**: the range decoding underneath it is also wrong (bug #6, §2.8), and enclosing-body attribution cannot work until both are fixed.
- 2,683 of the 48,326 rows have `caller_file` pointing into `~/Library/Caches/go-build/...` — test-binary documents from scip-go, noise that is never filtered (`00 §3`).
- The call graph actually used by `getCallGraph`, `findReferences`, `analyzeImpact` is `SCIPIndex.CallerIndex`, built in memory on first `FindCallers` from the SCIP documents (`internal/backends/scip/callgraph.go:140-200`). It is rebuilt in every process (each `ckb mcp` start) and never persisted.
- `internal/graph/builder.go:33 BuildFromSCIP` builds a *second* in-memory graph (call/container/module edges with weights) for Personalized PageRank. The survey (`00 §3`) calls this "rebuilt per process"; `02 §0.1` corrects that — it is worse: `internal/query/symbols.go:663` calls `BuildGraphFromSCIP` *inside* `SearchSymbols`, so the PPR graph is rebuilt on every **call**, with no memoization (`internal/query/ranking.go:217-229`).

### 2.3 Identity: commit-bearing keys, 126 bytes average

- Live symbol key example: `scip-go gomod github.com/SimplyLiz/CodeMCP 1ce495bdb45a \`…/internal/a2a\`/TaskStatusUpdateEvent#`. The embedded commit hash (`1ce495bdb45a`) means a Go module-version symbol's identity changes with every reindex at a new commit (`00 §2`).
- Because identity is not stable, `PopulateFromFullIndexStreaming` must delete and rewrite `indexed_files` / `file_symbols` / `callgraph` / `file_deps` on every full index (`updater.go:405-413`).
- Measured: average `length(file_symbols.symbol_id)` is 126 bytes (max 164). Stored in row plus 2–3 indexes across three tables, this is the single largest contributor to the 48 MB + 74 MB WAL database for 19k symbols (`01 §0.1`, item 6).
- The *designed* stable key (`ckb:<repo>:sym:<sha256(container|name|kind|arity|sig)>`, `internal/identity/fingerprint.go`) is documented in MCP tool descriptions (`internal/mcp/tools.go:96`) but `symbol_mappings` is empty — it has never run in the live path (`00 §2`).

### 2.4 Cartographer: rescanned every call, never persisted

Cartographer (Rust, cgo static lib) has ~100 call sites across `review_arch_health.go`, `review_layers.go`, `review_blastradius.go`, `review_deadcode.go`, `ownership.go:248`, `navigation.go:740,2307,2758`, `architecture.go:240`, `compound.go:1614`, `compound_refactor.go:227`, `mcp/tool_impls_v86.go`, `skeleton.go`. Every result is transient — consumed inside one request, serialized into the response, never written to SQLite (`00 §5`, `internal/query/compound.go:1610-1626`). Cartographer is itself stateless per call (it re-scans the tree). So a `reviewPR` re-runs `MapProject`, `GitChurn`, `GitCochange`, `UnreferencedSymbols` on the whole repo **every time** it is invoked.

### 2.5 Twenty of 28 extractors recompute per query

Persisting (when their producers are invoked, which the index path does not do): hotspots, ownership, decisions, modules, docs, audit/risk_scores, telemetry, federation. Recomputing per call: complexity, impact, deadcode, unwired, explain, breaking, testgap, secrets, suggest, perf, extract, export, cycles, architecture, envelope, tier, compression (`00 §7`).

### 2.6 The same fact, computed 2–5 different ways

| Fact | Independent implementations |
|---|---|
| complexity | hotspots, audit, suggest, deadcode — 4 implementations of the score |
| churn | hotspots (30/90d) vs suggest vs Cartographer `GitChurn` |
| dependency edges | modules classifier, `dependency_edges` table, cycles adjacency, Cartographer `MapProject.Edges`, `file_deps` — 5 representations |
| ownership | `ownership` table, `modules.owner_ref`, federated_ownership |
| test detection | testgap, deadcode, understand — 3 heuristics |
| risk | audit, hotspots composite, impact `RiskScore` — 3 formulas |
| co-change | `internal/coupling` (git log) and Cartographer `GitCochange` |

Valuable intermediates are thrown away entirely: the full depth-4 impact graph, `hotspot.projection30d`, telemetry match-quality factors, the risk factor breakdown (only the composite is stored), the module import→target mapping computed during classification (`00 §7`).

### 2.7 Cross-tier joins are string matching, unindexed, unvalidated

`explore`, `understand`, `prepareChange`, `planRefactor`, `exportForLLM`, `explainSymbol`, `explainFile`, `getArchitecture`, `analyzeImpact` each fan out to 3–6 extractors and stitch results by: SCIP symbol string → file path (via symbol location), file path → module (path prefix, `internal/query/compound.go:1696-1701` uses `filepath.Dir`), file path → owner (glob, tests by glob at `compound.go:1350-1385`), file path → hotspot (string equality), symbol → tests (file-name heuristic), symbol → docs (suffix match). ADRs are attached by a path-prefix walk (`internal/query/annotation_integration.go:39-60`). None of these joins is indexed; none is validated; no table has a foreign key across key spaces (`00 §8`, `00 §2`).

### 2.8 Five more concrete bugs found while re-verifying the survey (`01 §0.1`)

| # | Bug | Evidence |
|---|---|---|
| 2 | Two writers put incompatible JSON shapes in one column. `index_processor.go:690-692` writes `{"file_path","line","col"}` into `symbol_mappings.location_json`; `identity/repository.go:114` marshals `identity.Location` with tags `path`/`line`/`column` (`identity/id.go:39-45`). The delete at `index_processor.go:216` uses `json_extract('$.file_path')` and can never match the other writer's rows. | direct read of both writers |
| 3 | `identity.GetBackendIdRole` (`internal/identity/anchors.go:34`) tests prefix `"scip:"`; real symbols start with `"scip-go "`. Every real symbol is classified resolver-only — the opposite of the documented intent (`anchors.go:19-21`). | direct read |
| 4 | `OwnershipRepository.GetMatchingPattern` (`internal/storage/repositories.go:854-874`) returns every row; the caller globs in Go — no matching happens in the query. | direct read |
| 5 | Symbol end lines are inferred, not measured. `extractor.go:207` takes `EndLine` from the definition occurrence's identifier range; `buildFunctionRanges` (`callgraph.go:280-285`) substitutes "next function start − 1, else start + DefaultMaxFunctionLines". | direct read |
| 6 | **A three-element SCIP range is decoded as an end line.** Both persisted extractors do `sym.EndLine = int(occ.Range[2]) + 1` whenever `len(occ.Range) >= 3` (`internal/incremental/extractor.go:208` and `:522`), but for a three-element range `Range[2]` is the end **column** — the same file that parses ranges correctly documents the two formats (`internal/backends/scip/symbols.go:238-239`). Single-line definitions therefore get an end line equal to their end column. `resolveCallerSymbol`'s inference then only fires when `endLine == 0 \|\| endLine == sym.StartLine` (`extractor.go:652`), so the garbage value is never corrected and the enclosing-body range check below it never matches. Caller attribution stays broken even after bug #1's kind filter is fixed. | direct read of all three sites |

---

## 3. Goals and non-goals

### 3.1 Goals

- Give every node (repo, container, file, symbol, doc, actor) a stable integer id that survives a reindex, so facts and edges accumulate instead of being deleted and recomputed (`01 §2`).
- Replace string-matched cross-tier joins with indexed integer joins on a denormalized containment spine (`01 §3.3`).
- Materialize each derived fact exactly once per index run, with provenance, via a single `internal/derive` registry, enforced by a test (`01 §4.4`).
- Persist Cartographer output so `reviewPR` and friends read instead of rescanning the whole repo (`01 §4.3`).
- Shrink the agent-facing tool surface to a handful of composable primitives that make the nine most common questions cheap and predictable (`02 N1`, `02 §2.2`).
- Make evidence, freshness and confidence structural — part of the primary key and the response envelope, not an afterthought (`01 §1.5`, `02 E3`, `02 E4`).
- Keep the existing product front door (`prepareChange`, `assessChange`) working throughout the migration; every phase ships with MCP still functioning (`01 §5.2`).

### 3.2 Non-goals

- **No vector store, no chunking, no embedding-only hits in an answer body.** Embeddings are admitted in exactly two pipeline stages (seed, rank tie-break), never as the primary retrieval mechanism, and the repo stays a single Go binary; semantic-only results are quarantined to a separate `leads[]` list capped at 5% of budget (`02 R1`, `02 R3`; rejects "chunk-and-embed the repo", `docs/ideas.md:384-386`).
- **No UI in 1.x.** Text first, evidence first (`00 §10`; product decision, see `project_no_ui_in_1x`).
- **No new toolchain.** Pure-Go SQLite (`modernc.org/sqlite`); cgo stays scoped to the optional Cartographer static lib (`00 §10`; decision recorded in `docs/plans/change-intelligence-and-activity-ledger.md`).
- **No LLM in the retrieval path.** `intent` classification is deterministic rules, not an LLM call (`02 R2`, "no LLM" in the `intent` stage row). LLM notes are an explicit, out-of-band tool call, never computed automatically (`01 §4.1`).
- **No behavioural data in risk scores.** Usage signals feed retrieval *priors* only, capped at `w_usage ≤ 0.10`, applied only when structural scores are within 5%, and are explicitly excluded from anything entering a risk score or change-safety verdict (`02 L1`, `L2`, `L3`).
- No closure/transitive-closure tables materialized eagerly — k-hop expansions stay query-shaped because edge indexes make them cheap and the closure is n² to store (`01 §4.1`). The four-integer `closure.summary` fact (§6.4, §7.1) is a summary of a closure, not a closure.

---

## 4. Concepts and the three axes

### 4.1 Six node kinds, four of them a containment spine

| Tier | id | Canonical key | This repo → 10× |
|---|---|---|---|
| repo | 0 | repo slug | 1 |
| container | 1 | `kind ∈ {repo-root, component, package, directory}`; `directory` keys on the normalized repo-relative path, `package` keys on the **package path** (§4.1 ruling below), self-nesting via `parent_id` | ~150 → 1,500 |
| file | 2 | normalized repo-relative path | 836 → 8,400 |
| symbol | 3 | canonical SCIP key (§5) | 19k → 190k |
| doc | 4 | repo-relative path (+`#anchor`) | ~200 → 2,000 |
| actor | 5 | `email:…` / `gh:…` / `team:@org/x` | ~10 → 200 |

Tiers 0–3 form the **spine**: exactly one `parent_id`, one canonical key, containment is a tree. Tiers 4–5 hang off the spine via typed edges, not via containment (`01 §1.1`).

**Ruling — a `package` container is a package, not a directory.** Two container kinds can occupy the same directory: `directory` is keyed by path and is the filesystem tree, `package` is keyed by the indexer's package path and is what symbols point at through `container_id`. A Go directory holding both `foo` and `foo_test` yields **two** `package` containers under **one** `directory` container; both `_test` variants keep their own identity, which is what scip-go itself distinguishes. Rationale: keying packages by directory would silently merge an external test package into the package under test, and every ownership, responsibility and `depends_on` fact computed on the merged container would be attributed to the wrong unit. The `_test`-package fixture is required before Step 0 ships (§5.6).

**Ruling — `parent_id` for nested symbols.** A symbol's `parent_id` is the **enclosing symbol node** when its SCIP descriptor nests (a method inside a type, a func inside a func), otherwise the **file node**. `file_id` is always the file node and `container_id` always the package/container node, regardless of nesting depth. Rationale: `parent_id` is the tree, so it must express the deepest containment available, while the denormalized pointers exist precisely so a reader never has to walk the tree to get file or module — one integer join either way.

### 4.2 Node, edge, occurrence, fact, source, repo_state

- **node** — one row per repo/container/file/symbol/doc/actor. Carries the tier, the canonical natural key (`nkey`), containment pointers, and the state it was first/last seen in. See §6.2 for the full DDL.
- **edge** — a distinct typed relation `(src_id, type, dst_id)` between two nodes, with a denormalized `weight`. It carries no provenance of its own.
- **edge_evidence** — one row per `(edge, source)`: the confidence, the first/last state the source observed the relation in, the owning file, and that source's weight. One relation can be asserted by several sources at once — SCIP and telemetry both say `A calls B` — and each keeps its own evidence (§6.2).
- **occurrence** — a *location* of an edge, attributed to the source that saw it: `(edge_id, source_id, file_id, line, col, role)`. Multiple occurrences can back one edge (e.g. a symbol called from five call sites collapses to one `calls` edge with five occurrence rows).
- **fact** — a narrow EAV row: `(node_id, key, value, source, state, confidence)`, for open-ended overlay data (churn, hotspot, complexity, risk, coverage, telemetry, Cartographer metrics, LIP confidence, LLM summaries).
- **source** — a catalog of who produced a row: `scip-go`, `tree-sitter`, `git`, `cartographer@4.0.2`, `codeowners`, `lip`, `telemetry`, `llm:<model>`, `human`, `ckb-derived`, each with a `kind` (index/heuristic/vcs/declared/observed/model/human/derived) and a `default_confidence`. `observed` is telemetry's kind, and §9.2's `calls` / `observed-calling` split is a predicate on it.
- **repo_state** — one row per observed state of the repository (a specific `head_commit`, dirty or not, `computed_at`, which indexer produced it). This is `repostate.RepoStateID` given a table.

### 4.3 The cube cell

"3D" means **level × relation × state**, not entity/abstraction/time or entity/tier/evidence — both alternatives were rejected because level is a property of a node, not an independent coordinate, and neither alternative has a coordinate for relation type, which is what the store exists to serve (`01 §1.5`).

- **Level** (vertical): repo ⊃ container ⊃ file ⊃ symbol via `parent_id` and the denormalized `file_id`/`container_id` columns. Never string-matched.
- **Relation** (horizontal): typed edges — intra-level (`calls`, `imports`, `depends_on`, `cochanges`) and cross-level (`defines`, `documents`, `owns`, `tests`, `decides`).
- **State** (depth / evidence): every fact carries `(source_id, state_id, observed_at, confidence)`; every edge carries the same through its `edge_evidence` rows, one per asserting source.

A **cell** is the answer to: what does relation R (or fact K) say about node N, as of state S, according to source P. All three coordinates are indexed; provenance is part of the primary key. That is what makes "evidence first" structural rather than a documentation promise (`01 §1.5`).

### 4.4 Why directory is not a tier

Directory is a **container kind**, not a separate tier. One self-nesting container hierarchy replaces two competing ones ("module of X" and "directory of X" become the same walk at different depths) (`01 §1.2`).

### 4.5 Why occurrence is not a node tier

An occurrence is a *located edge instance*, not a node. `edge` holds the distinct relation; `occurrence` holds its locations. Collapsing locations onto edges is not cosmetic: today's `callgraph` has 48,326 call-site rows that collapse to 17,208 distinct file-level `(caller_file, callee_id)` pairs (2.8× duplication, measured on this repo, `01 §0.1` item 6) — k-hop walks stop paying for the duplicates once they walk `edge` instead of raw occurrences. An occurrence remains individually addressable as `(edge_id, line, col)` for "what is at this line" queries (`01 §1.3`).

Call-site nodes are explicitly **not** first-class (`03 R-2`): "which of these 30 callers matter" is answered by `edge.weight` (for SCIP evidence, the occurrence count) plus the caller's `closure.summary` fact (access path A5, §6.4); raw call-site lists come from `occurrence` on demand.

### 4.6 Why annotations are facts and documents, not tiers

The overlay `(node, key, value, source, state, confidence)` fact table carries churn, hotspot, complexity, risk, coverage, telemetry, Cartographer metrics, LIP confidence, and LLM summaries. Document nodes are tier-4, not facts, because they have text, join full-text search, and are themselves edge endpoints (ADRs, design docs, README sections, LLM notes-as-artifacts). Ownership splits across both: the CODEOWNERS rule itself is a **fact** on the repo; the *resolved* owner is an **`owns` edge** from actor to file/container — materializing the resolution is what removes today's full-table scan-and-glob-in-Go (`01 §1.4`, and see bug #4 in §2.8 above).

---

## 5. Identity

### 5.1 Canonical symbol key

```go
// internal/identity/canonical.go
func CanonicalSymbolKey(scipSymbol string, localPkgs map[string]struct{}) string
```

If the package is one of the repo's own modules, replace field 4 with `.` (SCIP's unspecified-version placeholder); external packages keep their real version.

**Ruling — locality is what the indexer treated as local, never a hash comparison.** `localPkgs` is the set of module paths the SCIP index itself indexed as local: the packages whose `Document`s appear in the index, cross-checked against the indexer's `Metadata.ToolInfo`. A `replace` directive resolved to a path inside the repository makes that module local; a vendored external module stays external; `GOWORK` and parent workspaces are honoured automatically because the indexer already resolved them before emitting documents. v0.3's alternative test — "or its version equals short HEAD" — is **removed**: it contradicted the sentence next to it ("a package not in the set is external"), and it is not proof of locality anyway, since scip-go assigns the index version to unversioned local replacements, so an external module replaced to an unversioned local path would carry the same string without being one of ours. Deriving the set from `go.work`/`go.mod` alone is likewise not enough, for the same replacement reason; the parse of those files is a cross-check on the indexer's answer, not the answer. The per-indexer analogue in §5.6 is likewise a set (workspace `package.json`s, reactor `pom.xml` modules), and `replace` / `vendor` / `GOWORK` each need a fixture (§5.6).

**Ruling — the existing SCIP parser is not the one to use.** `scip.ParseSCIPIdentifier` (`internal/backends/scip/ids.go:37-67`) is **lossy**: `SCIPIdentifier` has no version field (`ids.go:11-26`) and the five-part branch assigns `parts[4]` to `Descriptor` while dropping `parts[3]` entirely (`ids.go:59-63`, whose own comment says "`parts[3]` is version") — the exact field canonicalization has to rewrite. `CanonicalSymbolKey` uses a new lossless pair in `internal/identity/canonical.go`:

```go
func ParseSCIPSymbol(s string) (scheme, manager, name, version, descriptor string, err error)
func FormatSCIPSymbol(scheme, manager, name, version, descriptor string) string
```

`Format(Parse(s)) == s` for every well-formed symbol, descriptor escaping (backticks around package paths with spaces) preserved byte-for-byte. Malformed input is returned **unchanged** as the key, plus an `index.warning` fact (§6.4) on the file node naming the symbol, so one bad symbol degrades one row instead of failing an index run.

```
live:      scip-go gomod github.com/SimplyLiz/CodeMCP 1ce495bdb45a `…/internal/a2a`/TaskStatusUpdateEvent#
canonical: scip-go gomod github.com/SimplyLiz/CodeMCP .            `…/internal/a2a`/TaskStatusUpdateEvent#
```

Today a reindex at a new commit changes the identity of every symbol, which is why `PopulateFromFullIndexStreaming` must delete and rewrite everything (`updater.go:405-413`). With commit-free keys, an unchanged symbol keeps its node id and every attached fact survives a reindex. The raw string is kept as `node.scip_raw` for calling back into the SCIP index — never as a key (`01 §2.2`).

`identity/fingerprint.go`'s designed key (`ckb:<repo>:sym:<sha256(container|name|kind|arity|sig)>`) is **rejected as the id basis**: it includes signature and arity, so any signature change mints a new id; `ExtractArity` counts commas; parts are sorted before hashing; it is opaque; and it has never run (0 rows). `ComputeStableFingerprint` is kept, narrowed to `(container, name, kind)`, renamed `MatchKey`, and used only to *propose* `node_alias` rows, not to mint ids. `identity/anchors.go` must be fixed or deleted (bug #3, §2.8) (`01 §2.3`).

### 5.2 Integer surrogate ids

`node(id INTEGER PRIMARY KEY, tier, nkey, …) UNIQUE(tier, nkey)`. Only `id` appears in edges, facts, occurrences, and FTS rowids. `nkey` stays legible and survives a DB rebuild. A call edge shrinks from ~126+35 bytes × (row + 3 indexes) to ~20 bytes; at 10× that is roughly 30 MB instead of 240 MB for the call graph alone (`01 §2.1`).

A node reference is always an integer; **edge type constrains the tier pair** — there is no `target_id TEXT + target_type TEXT` anywhere in the new schema. The constraint lives once, on `edge_type`, as two tier **bitmasks** (`src_tiers`, `dst_tiers`; bit *i* set = tier *i* permitted), checked at write time as `(1 << node.tier) & mask != 0` (`01 §2.5`).

**Ruling — bitmask, not one type per tier pair.** Three relations legitimately span tiers (`documents` → symbol|container, `decides` → container|symbol, `owns` → file|container). Splitting them into `documents_symbol`/`documents_container`/… would double the catalog and force the wire vocabulary (§9.2) to expose the split, while a nullable `dst_tier` would drop the write-time check entirely; a bitmask keeps one type per relation, one wire name, and an exact check that is a single integer AND.

### 5.3 node_alias and in-place rename

```sql
CREATE TABLE node_alias (
  tier INTEGER NOT NULL, old_nkey TEXT NOT NULL,
  node_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  reason TEXT NOT NULL,            -- renamed|moved|merged|split|fuzzy
  confidence REAL NOT NULL,
  source_id INTEGER NOT NULL REFERENCES source(source_id),
  from_state INTEGER NOT NULL REFERENCES repo_state(state_id),
  PRIMARY KEY (tier, old_nkey, node_id)
);
CREATE INDEX idx_alias_node ON node_alias(node_id);
```

A rename **mutates the node in place and versions the key** — it does not mint a new id. Resolution is one lookup on `node(tier, nkey)`, else one on `node_alias`. No chains, no depth limit, no cycle detection.

**Ruling — nodes are not versioned; there is no historical node snapshot.** A `node` row holds the attributes as of the **latest state it was observed in**, and nothing older. `first_state`/`last_state` therefore answer *existence* — "this node existed at `asOf`" — and nothing else; A12 against `node` is an existence predicate, not an as-of projection (Appendix C). Historical attributes — the previous name, the previous signature, the previous parent — are reconstructed by replaying `node_change` backwards from the current row, which is why that table stores both `old_value` and `new_value` (§6.2, access path A8). A caller that asks for a node's signature as of an old state gets today's signature plus the `node_change` rows between, and is told so; it never gets a silently reconstructed row. Rationale: versioning nodes means a row per node per state — 190k symbols × every indexed commit — to serve one access path (A8) that `node_change` already serves at a fraction of the size.

This rejects the existing alias-chain design (`identity/resolution.go`, `AliasChainMaxDepth = 3`, cycle detection, `AliasChainTooDeep`): under that design a rename mints a new id and every edge and fact attached to the old id has to be rewritten, which is exactly the cost this store exists to remove (`01 §2.4`).

Cases handled by `node_alias`:

| Case | Mechanism |
|---|---|
| file rename | `nkey`/`path` updated in place + alias row from git similarity detection |
| symbol rename | `MatchKey` + location proximity + the file's own rename |
| container move | `parent_id` rewritten, plus every descendant's `container_id` rewritten |
| merge (two old keys → one node) | two `node_alias` rows pointing at the same `node_id` |
| split (one old key → two nodes) | confidence < 1, returned as ambiguous |

### 5.4 Tombstones

Deletion is a tombstone, not a row delete: `node.state = 'deleted'`, `last_state` frozen at the **last state the node was observed in** — *not* the state it was missing from. Inbound edges are left untouched (this matches the existing caller-owned invariant at `incremental/types.go:53-55`) (`01 §2.4`, `01 §4.2`).

**Ruling — the interval is closed on the last sighting, and the gap lives in `node_change`.** If a node is present at state 7 and absent at state 8, `last_state = 7` and a `node_change(kind='deleted', state_id=8)` row is written. `first_state <= :asOf AND last_state >= :asOf` is therefore true exactly for the states the node was observed in, and false at 8 — the off-by-one in v0.3 ("frozen at the state it disappeared in") would have reported the node as live at the state that proved it gone.

**Reappearance.** A tombstoned node whose key comes back is the **same row**: `state` flips back to `'active'`, `last_state` advances, and a second `node_change(kind='created')` row is written. `first_state` is not rewritten, so the validity interval spans the gap and A12 will report the node as existing during it. The gap is visible **only** through `node_change` — the `deleted`/`created` pair bracketing it. A consumer that needs gap-accurate existence (rare: change archaeology, not retrieval) reads `node_change`, not the interval. Minting a fresh node id instead would throw away every fact and edge attached to a symbol that was moved out and back in one refactor, which is the cost this store exists to remove.

### 5.5 What crosses the wire (ruling: `03 R-1`)

`01 §2.1` argued for the legible canonical key on the wire; `02 N3` argued for short ids with a per-response id map. **03 rules**: the wire format is **`id` + `handle`**, canonical key on request only.

- `id` = `"n:<db_epoch in base36>.<node.id in base36>"`, e.g. `n:4.3f7`. Stable for the life of the database *generation*. Cheap to echo, cheap to resolve.

  **Ruling — the epoch is in the id, not only in the envelope.** An epoch-free `n:3f7` held by a client is indistinguishable from a freshly minted `n:3f7` in a rebuilt database that happens to have handed integer 4,747 to something else entirely — the id would resolve, silently, to the wrong node. Carrying the epoch in the identifier makes that unrepresentable: A1 compares the epoch against `index_meta('db_epoch')` and, on a mismatch, **rejects the id** with `unknown-id` rather than dereferencing it. The cost is 2–3 characters per id; the alternative — requiring every ID-bearing request to also carry the epoch as a separate parameter — puts the check on the caller, and a caller that forgets it gets silent corruption instead of an error.
- `handle` = a short, human-legible form derived from the canonical key: for symbols `<container short name>.<Name>` (e.g. `query.Engine.FindReferences`), for files the repo-relative path, for containers the container path, for docs the path+anchor, for actors the display handle. Handles are **not unique** — they are for reading, not for resolving.
- `nkey` (the full canonical key, e.g. the commit-free SCIP string) is returned only under `include:["rawIds"]`, in a single `ids` map at the end of the response.
- `resolve()` (access path A1, Appendix B) accepts **all** of: `n:…` ids, canonical keys, raw SCIP strings (commit-bearing), handles, bare names, `path:line`, and any `old_nkey` from `node_alias`. An `n:` id whose epoch is not the current `db_epoch` returns `unknown-id` together with the resolution by handle, so an agent recovers in one call. When `node_alias` returns more than one row for a key, `resolve()` returns the highest-confidence match as `nodeId` and the rest in `alternatives[]` with `confidence < 1` — this is the split case of §5.3, and the only way an alias lookup populates `alternatives[]`.
- A database rebuild (schema migration, `ckb index --full --rebuild`) increments `db_epoch` and re-mints integer ids. That is acceptable because it is rare, every response carries the handle, and every cached id names the epoch it was minted under. `db_epoch` is also reported in the envelope — but detection does **not** depend on a client noticing that, because the store itself rejects a mismatched id (above).

Why not the full key on the wire: `01` measured 126 bytes average per key; `02` measured 40–60 tokens per key. A 50-row answer would spend more tokens on identifiers than on facts. Why not integers only: evidence-first — the handle gives the agent something it can sanity-check, and the `ids` map gives it the exact key when it needs one.

### 5.6 Per-indexer canonical policy (ruling: `03 R-3`)

The canonical key strategy for non-Go indexers stays an **open, Phase-1 spike** — see §14. `03 R-3` fixes the shape of the policy table so Phase 1 has a concrete starting point:

| Indexer | Local-package version rule |
|---|---|
| scip-go | version = short commit hash → rewritten to `.` |
| scip-typescript | version = `package.json` version of the local package → rewritten to `.` |
| scip-java | version = maven coordinate version of the local module → rewritten to `.` |
| unknown indexer | no rewrite; warn |

Each row says what the version field *holds* for a local package; **whether a package is local** is decided by §5.1's rule (the indexer's own set of local modules), never by inspecting the version.

**Fixtures required before Step 0 ships for a language** (`01 §7.1`, ruling `03 R-3`; extended here by F11/F17). One golden fixture per language is not enough — every fixture is a pinned-indexer run (indexer binary version recorded in the fixture) whose expected output is the canonical key set:

| Fixture | Proves |
|---|---|
| plain package | the baseline rewrite: local version → `.`, external version verbatim |
| external test package (`foo` + `foo_test` in one directory) | two `package` containers under one `directory` container (§4.1); `_test` identity preserved |
| generic type + an instantiation of it | declaration and reference resolve to the **same** node; instantiation descriptors do not mint a second symbol |
| `go.work` with two modules (and the same run with `GOWORK=off`) | both modules land in `localPkgs`; the workspace, not the root `go.mod`, decides |
| `replace` to an in-repo path | the replaced module is local and gets `.`, even though its declared version is a real semver |
| `vendor/` directory present | vendored external modules stay external and keep their versions |

Until a language has all six, its canonical key is "no rewrite; warn" (the unknown-indexer row above).

### 5.7 db_epoch

`index_meta` is a key/value table (`internal/storage/schema.go:1143-1146`), so this is a **key**, not a column: `index_meta('db_epoch')`, incremented on every full rebuild that re-mints integer ids. Every `n:` id carries it (§5.5), so A1 rejects an id minted under a previous epoch instead of dereferencing it; the envelope reports it as well, for clients that want to invalidate a whole cache at once rather than one id at a time (`03 R-1`).

---

## 6. Schema

### 6.1 Storage substrate

**Hybrid**: a typed, integer-keyed spine (`node`, `edge`, `occurrence`) for hot, high-volume, fixed-shape data, plus a generic narrow EAV `fact` table for the open-ended overlay.

Rejected: a pure property graph — a metric read would scan a table dominated by ~1.5M edges, and FTS wants a stable integer rowid, which a property graph doesn't naturally give it. Rejected: fully typed per-tier tables — that is the *current* schema, and it is 49 tables with 5 populated and 20 of 28 extractors never persisted (`01 §3.1`).

### 6.2 Full schema (`01 §3.2`, plus `node_change` and `cartographer_run` from `03`/`01 §4.3`, plus `node_body`/`body_fts`, `usage_overlay` and `edge_evidence` — see the rulings below this block)

This block is the **single source** for the schema. Every column named anywhere else in this spec — §6.5, §6.7, §7.x, §9.2, Appendix C — is a column declared here.

```sql
-- evidence axis
CREATE TABLE repo_state (
  state_id INTEGER PRIMARY KEY, state_key TEXT NOT NULL UNIQUE,  -- repostate.RepoStateID
  head_commit TEXT NOT NULL, dirty INTEGER NOT NULL, computed_at INTEGER NOT NULL, indexer TEXT,
  published INTEGER NOT NULL DEFAULT 0   -- 0 = an index run is still writing this state; readers ignore it (§7.7)
);
CREATE INDEX idx_state_published ON repo_state(published, state_id DESC);
CREATE TABLE source (
  source_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,  -- 'scip-go','tree-sitter','git','cartographer@4.0.2','codeowners','lip','telemetry','llm:<model>','human','ckb-derived'
  kind TEXT NOT NULL,          -- index|heuristic|vcs|declared|observed|model|human|derived
  version TEXT, default_confidence REAL NOT NULL
);

-- level axis
CREATE TABLE node (
  id INTEGER PRIMARY KEY, tier INTEGER NOT NULL, nkey TEXT NOT NULL,
  parent_id INTEGER REFERENCES node(id),
  container_id INTEGER REFERENCES node(id),   -- denormalized, tiers 2,3
  file_id INTEGER REFERENCES node(id),        -- denormalized, tier 3
  name TEXT NOT NULL, kind TEXT, lang TEXT, path TEXT,
  start_line INTEGER, end_line INTEGER, end_line_src TEXT,   -- 'exact'|'inferred'
  signature TEXT, doc_comment TEXT,
  scip_raw TEXT,                               -- live SCIP string, never a key
  first_state INTEGER NOT NULL REFERENCES repo_state(state_id),
  last_state INTEGER NOT NULL REFERENCES repo_state(state_id),
  state TEXT NOT NULL DEFAULT 'active',        -- active|deleted
  UNIQUE(tier, nkey)
);
CREATE INDEX idx_node_parent ON node(parent_id, tier);
CREATE INDEX idx_node_path ON node(path, tier);
CREATE INDEX idx_node_container ON node(container_id, tier);
CREATE INDEX idx_node_file ON node(file_id);
CREATE INDEX idx_node_live ON node(tier, state, last_state);

-- relation axis
CREATE TABLE edge_type (
  type_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
  src_tiers INTEGER NOT NULL, dst_tiers INTEGER NOT NULL,   -- tier bitmask, bit i = tier i permitted (§5.2)
  directed INTEGER NOT NULL DEFAULT 1, derived INTEGER NOT NULL DEFAULT 0
);
-- `edge` is relation identity only: no source, no confidence, no states. See `edge_evidence`.
CREATE TABLE edge (
  id INTEGER PRIMARY KEY,
  src_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  dst_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  type INTEGER NOT NULL REFERENCES edge_type(type_id),
  weight REAL,                                 -- denormalized MAX over edge_evidence.weight (§6.2 ruling)
  UNIQUE(src_id, type, dst_id)
);
CREATE INDEX idx_edge_out ON edge(src_id, type, weight DESC, dst_id);
CREATE INDEX idx_edge_in  ON edge(dst_id, type, weight DESC, src_id);

-- one row per (relation, asserting source): the evidence axis of the relation plane
CREATE TABLE edge_evidence (
  edge_id INTEGER NOT NULL REFERENCES edge(id) ON DELETE CASCADE,
  source_id INTEGER NOT NULL REFERENCES source(source_id),
  confidence REAL NOT NULL DEFAULT 1.0,
  weight REAL,                                 -- this source's own weight (occurrence count, coupling, …)
  first_state INTEGER NOT NULL REFERENCES repo_state(state_id),   -- first observed by this source
  last_state  INTEGER NOT NULL REFERENCES repo_state(state_id),   -- last confirmed; not a time-travel window (§7.2)
  owner_file_id INTEGER REFERENCES node(id) ON DELETE CASCADE,    -- file whose reindex replaces this row; NULL = producer-owned (§7.2)
  PRIMARY KEY (edge_id, source_id)
);
CREATE INDEX idx_evidence_own ON edge_evidence(owner_file_id);
CREATE INDEX idx_evidence_source ON edge_evidence(source_id, edge_id);

CREATE TABLE occurrence (
  edge_id INTEGER NOT NULL,
  source_id INTEGER NOT NULL,
  file_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  line INTEGER NOT NULL, col INTEGER NOT NULL, end_col INTEGER,
  role INTEGER NOT NULL,                       -- SCIP SymbolRoles bitmask
  PRIMARY KEY (edge_id, source_id, file_id, line, col),
  FOREIGN KEY (edge_id, source_id) REFERENCES edge_evidence(edge_id, source_id) ON DELETE CASCADE
);
CREATE INDEX idx_occ_file ON occurrence(file_id, line);

-- overlay
CREATE TABLE fact_key (
  key TEXT PRIMARY KEY, value_type TEXT NOT NULL,   -- num|text|json
  temporal INTEGER NOT NULL DEFAULT 0, unit TEXT, derived_from TEXT,  -- JSON array of input keys
  carry_forward INTEGER NOT NULL DEFAULT 0          -- valid at a later state without recomputation (§7.3)
);
CREATE TABLE fact (
  node_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  key TEXT NOT NULL REFERENCES fact_key(key),
  num_value REAL, text_value TEXT, json_value TEXT,
  source_id INTEGER NOT NULL REFERENCES source(source_id),
  state_id INTEGER NOT NULL REFERENCES repo_state(state_id),
  observed_at INTEGER NOT NULL, confidence REAL NOT NULL,
  PRIMARY KEY (node_id, key, state_id, source_id)
);
CREATE INDEX idx_fact_key ON fact(key, num_value DESC);
CREATE INDEX idx_fact_text ON fact(key, text_value);   -- access path A7 (§6.7)
CREATE INDEX idx_fact_state ON fact(state_id);

-- search
CREATE VIRTUAL TABLE node_fts USING fts5(
  name, signature, doc_comment,
  content='node', content_rowid='id', tokenize='unicode61 remove_diacritics 2'
);
-- external-content FTS is NOT self-maintaining: every content write needs a paired index write (§6.2 ruling)
CREATE TRIGGER node_fts_ai AFTER INSERT ON node BEGIN
  INSERT INTO node_fts(rowid, name, signature, doc_comment)
  VALUES (new.id, new.name, new.signature, new.doc_comment);
END;
CREATE TRIGGER node_fts_ad AFTER DELETE ON node BEGIN
  INSERT INTO node_fts(node_fts, rowid, name, signature, doc_comment)
  VALUES ('delete', old.id, old.name, old.signature, old.doc_comment);
END;
CREATE TRIGGER node_fts_au AFTER UPDATE ON node BEGIN
  INSERT INTO node_fts(node_fts, rowid, name, signature, doc_comment)
  VALUES ('delete', old.id, old.name, old.signature, old.doc_comment);
  INSERT INTO node_fts(rowid, name, signature, doc_comment)
  VALUES (new.id, new.name, new.signature, new.doc_comment);
END;

-- body text (doc chunks, README/responsibility prose) lives off the hot table (§6.2 ruling)
CREATE TABLE node_body (
  node_id INTEGER PRIMARY KEY REFERENCES node(id) ON DELETE CASCADE,
  body TEXT NOT NULL
);
CREATE VIRTUAL TABLE body_fts USING fts5(
  body,
  content='node_body', content_rowid='node_id', tokenize='unicode61 remove_diacritics 2'
);
CREATE TRIGGER body_fts_ai AFTER INSERT ON node_body BEGIN
  INSERT INTO body_fts(rowid, body) VALUES (new.node_id, new.body);
END;
CREATE TRIGGER body_fts_ad AFTER DELETE ON node_body BEGIN
  INSERT INTO body_fts(body_fts, rowid, body) VALUES ('delete', old.node_id, old.body);
END;
CREATE TRIGGER body_fts_au AFTER UPDATE ON node_body BEGIN
  INSERT INTO body_fts(body_fts, rowid, body) VALUES ('delete', old.node_id, old.body);
  INSERT INTO body_fts(rowid, body) VALUES (new.node_id, new.body);
END;

-- node-level change log (access path A8; answers "01 §7 / 02 §7" open question, ruling 03 R-2)
CREATE TABLE node_change (
  node_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  state_id INTEGER NOT NULL REFERENCES repo_state(state_id),
  kind TEXT NOT NULL,        -- created|deleted|renamed|moved|signature|doc_comment|body_hash
  old_value TEXT, new_value TEXT,
  source_id INTEGER NOT NULL REFERENCES source(source_id)
);
CREATE INDEX idx_change_node ON node_change(node_id, state_id);
CREATE INDEX idx_change_state ON node_change(state_id);

-- Cartographer freeze (01 §4.3 / 03 R-4 Phase 3)
CREATE TABLE cartographer_run (
  run_id INTEGER PRIMARY KEY, version TEXT NOT NULL, state_id INTEGER NOT NULL REFERENCES repo_state(state_id),
  started_at INTEGER NOT NULL, duration_ms INTEGER NOT NULL, functions TEXT NOT NULL   -- JSON: bridge calls covered
);

-- behavioural priors (§11.4); never read in `gather`, never a fact about code
CREATE TABLE usage_overlay (
  node_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  node_b INTEGER REFERENCES node(id) ON DELETE CASCADE,   -- pair rows (usage:coaccess); NULL for single-node kinds
  kind TEXT NOT NULL,                          -- usage:access|usage:coaccess|usage:followThrough|usage:refinement
  value REAL NOT NULL,
  window_start INTEGER NOT NULL, window_end INTEGER NOT NULL
);
-- NULLs are distinct in a SQLite unique index, so the key is expressed over ifnull(node_b,0)
CREATE UNIQUE INDEX idx_usage_pk ON usage_overlay(node_id, kind, window_start, ifnull(node_b, 0));
CREATE INDEX idx_usage_kind ON usage_overlay(kind, value DESC);
CREATE INDEX idx_usage_pair ON usage_overlay(node_b, kind, value DESC);   -- the other half of a coaccess lookup
```

**Ruling — relation identity is separate from evidence.** One row in `edge` is one relation; one row in `edge_evidence` is one source's claim about it. v0.3 put `source_id`, `confidence`, `first_state`, `last_state` and `owner_file_id` on `edge` *and* kept `UNIQUE(src_id, type, dst_id)` — which cannot hold: the moment telemetry observes a call SCIP already resolved, one of the two writers overwrites the other's provenance, and §9.2's `calls` / `observed-calling` split (one filters telemetry out, the other filters everything else out) silently loses whichever row lost the race. With the split, both rows exist, `calls` reads evidence where `source.kind != 'observed'`, `observed-calling` reads evidence where it is, and neither is lossy.

Consequences, all of them load-bearing:

- **`edge.weight` is denormalized**: it is `MAX(edge_evidence.weight)` over the edge's evidence rows, rewritten in the same transaction as any evidence write. It exists so `idx_edge_out`/`idx_edge_in` can pre-sort A4's result without joining (§6.7); it is never the value a consumer reasons about — that is the per-source `edge_evidence.weight`.
- **`edge` has no states.** "Does this relation exist at HEAD" is "does it have at least one evidence row"; an edge whose last evidence row is deleted is deleted with it (§7.2).
- **`occurrence` is attributed**, because a call site is evidence from exactly one source; its foreign key is the composite `(edge_id, source_id)` into `edge_evidence`, so retracting one source's evidence takes its occurrences with it and leaves the other source's alone.
- **Confidence is per evidence row**, which is where §10.1's weight table was always pointing: `SCIP def/ref at HEAD = 1.00` and `telemetry observed = 0.75` are two numbers about one relation, and now there are two rows to carry them.

**Ruling — an external-content FTS table is not self-maintaining.** FTS5 external-content tables index nothing on their own: a write to the content table is invisible to the index unless something writes the paired index row. The current schema already knows this — `internal/storage/fts.go:60-82` defines exactly the `_ai`/`_au`/`_ad` trigger trio for `symbols_fts_content` — and the v0.3 DDL simply forgot to carry it over. Both FTS tables therefore ship with the three triggers above, in the same transaction as the table they follow, so insert/update/delete synchronization is transactional by construction and `ON DELETE CASCADE` from `node` fires `node_fts_ad`/`body_fts_ad` without any application code.

**Bulk load.** Triggers are per-row and would dominate a full index run, so the bulk path is the one the repo already uses (`fts.go:170,208` — `ftsDropTriggers` before, `ftsCreateTriggers` after): drop the three triggers, load `node`/`node_body`, `INSERT INTO node_fts(node_fts) VALUES('rebuild')` and the same for `body_fts`, recreate the triggers — all inside the generation's write transaction, so a crashed run never leaves a live database with triggers missing.

**Tombstones are not filtered by FTS.** A tombstoned node keeps its row and therefore its FTS entry; the A9 query joins `node` and filters `state = 'active'` (§6.7, Appendix B). Deleting the index entry instead would make "find the symbol that used to be called X" impossible, which is a query §9.5 Q5 needs.

**Ruling — `body` is not a `node` column.** FTS5 external-content tables read every declared column by name from the content table, so `node_fts(…, body, content='node')` would fail at `INSERT INTO node_fts(node_fts) VALUES('rebuild')`. Body text is also the one field the hot path never needs: `node` is read on every resolve, ancestor walk and edge expansion. It therefore lives in `node_body`, one row per node that has text, with its own external-content FTS sharing the node id as rowid — so a body hit and a name hit both return an integer that joins directly to `node`.

`node_change` is written by the index run when it upserts a node whose tracked columns differ from the previous state; it is access path A8 (Appendix B; ruling `03 R-2`, §14.3). `cartographer_run` records provenance for the batch of node/edge/fact rows a `MapProject`/`Health`/`GitChurn`/… run produces; see §7.5.

### 6.3 Edge-type catalog

Tier columns are the `src_tiers`/`dst_tiers` bitmasks of §5.2; a cell listing two tiers is one type whose mask has two bits set.

| name | src_tiers → dst_tiers | directed | source | Replaces |
|---|---|---|---|---|
| `defines` | file → symbol | yes | scip | — |
| `calls` | symbol → symbol | yes | scip | `callgraph` (with a real caller) |
| `references` | symbol → symbol | yes | scip | — |
| `refers` | file → symbol | yes | scip | the file-scoped half of `RefIndex` / `Reference{FromFile,ToSymbolID}` |
| `implements` | symbol → symbol | yes | scip | — |
| `embeds` | symbol → symbol | yes | scip | — |
| `type_of` | symbol → symbol | yes | scip | — |
| `imports` | file → file | yes | scip | `file_deps` |
| `depends_on` | container → container | yes | cartographer or derived from `imports` | `dependency_edges` |
| `tests` | symbol → symbol | yes | derived | testgap/deadcode/understand heuristics (3 → 1) |
| `documents` | doc → symbol\|container | yes | index-time if docs enabled | `doc_references` |
| `decides` | doc(kind=adr) → container\|symbol | yes | index-time | `decisions.affected_modules` |
| `owns` | actor → file\|container | yes | codeowners / blame | `ownership` glob scan |
| `authored` | actor → file | yes | git | — |
| `cochanges` | file ↔ file | no | git, weight=coupling | `coupling_cache` and Cartographer `GitCochange` (2 → 1) |
| `hidden_coupling` | file ↔ file | no | cartographer | Cartographer `HiddenCoupling` |
| `violates_layer` | container → container | yes | cartographer | Cartographer `LayerViolations` |
| `in_cycle` | container → container | yes | cartographer | Cartographer `Cycles` |
| `near` | symbol → symbol | yes | lip | LIP nearest-neighbour |

`edge_type.derived = 1` for edges computed from other edges (e.g. `depends_on` derived from `imports`) so the derive-once registry (§7.4) can enforce single computation.

**Ruling — `references` is symbol → symbol, `refers` is file → symbol, and `findReferences` reads both.** Not every reference has an enclosing symbol. A package-level `var x = pkg.Thing{…}`, a composite literal in a top-level initializer, a reference inside a construct whose enclosing declaration the extractor cannot resolve — all of these are real references with no symbol to hang the source end on. That is not hypothetical: the live reference path indexes references by *file*, not by caller (`internal/incremental/extractor.go:240-244` builds `Reference{FromFile, ToSymbolID}`, and `FindReferences` reads `RefIndex`, not `CallerIndex` — `internal/backends/scip/references.go:12,16`). A symbol-endpoint-only `references` type would make those rows unrepresentable and silently shrink `findReferences`' answer the day the readers switch over.

So: the writer emits `references` when the enclosing symbol resolves and `refers` from the file node when it does not — never both for one occurrence. The wire name `references` (§9.2) walks both types and merges; a `refers` result reports the file as its source end and carries `origin: structural` like any other SCIP row. `refers` is deliberately *not* a separate wire name: the agent asked "who references X", and "this file does, we could not narrow it further" is an answer to that question, not a different one. Reader parity against the current `RefIndex` output is the Phase 1 acceptance criterion for the switch (§13.2).

**Ruling — canonical ordering for undirected types.** For `directed = 0` types (`cochanges`, `hidden_coupling`) the writer normalizes to `src_id < dst_id` before insert, and a reader asking for neighbours of N unions `idx_edge_out` and `idx_edge_in`. Without the rule `UNIQUE(src_id, type, dst_id)` admits both orientations of the same pair, which would store the relation twice and make `edge.weight` mean half of itself.

### 6.4 Seed fact-key catalog

`value_type` and `temporal` are assigned per `01`'s migration disposition ("hotspot_snapshots, risk_scores → fact (**temporal**)", `01 §5.1`) and `03 R-2`'s ruling that temporal keys keep history subject to the retention policy while non-temporal keys keep only the latest row per `(node, key, source)`. A key is marked `temporal` when its value is meaningful as a trend (churn windows, hotspot/risk scores and projections, coverage, telemetry, Cartographer health) and non-temporal when it is a structural classification recomputed fresh each run (test detection, responsibility summaries, per-run Cartographer node scores, match-quality labels).

`carry_forward` is seeded as `NOT temporal` across this catalog, but it is a separate column because the two answer different questions: `temporal` says whether older rows are worth keeping, `carry_forward` says whether a row may be *served* at a later state it was not computed at (§7.3). A temporal key's absence at a state is itself data — "no churn measured at this state" is not "the 30-day churn is what it was last week".

| key | value_type | temporal | unit | derived_from | Notes |
|---|---|---|---|---|---|
| `churn.commits.30d` | num | 1 | count | — | git, rolling window |
| `churn.commits.90d` | num | 1 | count | — | git, rolling window |
| `churn.authors.30d` | num | 1 | count | — | git, rolling window |
| `complexity.cyclomatic` | num | 1 | count | — | tree-sitter, one of 4 former impls (`01 §4.4`) |
| `complexity.cognitive` | num | 1 | count | — | tree-sitter |
| `hotspot.score` | num | 1 | score | `["churn.commits.30d","complexity.cyclomatic"]` | replaces `hotspot_snapshots` |
| `hotspot.projection30d` | num | 1 | score | `["hotspot.score"]` | previously discarded (`00 §7`); now a derived temporal fact (`03 R-2`) |
| `risk.score` | num | 1 | score | `["hotspot.score","complexity.cyclomatic","coverage.line"]` | replaces `risk_scores`, one of 3 former formulas |
| `risk.factors` | json | 1 | — | same as `risk.score` | previously discarded composite-only; now stored (`03 R-2`) |
| `coverage.line` | num | 1 | ratio | — | `coverage_snapshots` |
| `is_test` | num | 0 | bool | — | one of 3 former heuristics (`01 §4.4`) |
| `responsibility.summary` | text | 0 | — | — | from README/code extraction |
| `telemetry.calls.30d` | num | 1 | count | — | on ingest command |
| `telemetry.match_quality` | text | 0 | — | — | stored alongside `fact.confidence` (`03 R-2`) |
| `arch.health` | num | 1 | score | — | Cartographer `Health`; the trend is this key's own temporal history, which replaces Cartographer `Evolution(days)` |
| `arch.bridge_score` | num | 0 | score | — | Cartographer `MapProject` node field, latest run only |
| `arch.cohesion` | num | 0 | score | — | Cartographer `GodModules.CohesionScore` |
| `arch.instability` | num | 0 | score | — | Cartographer node metadata |
| `arch.god_module` | num | 0 | bool | — | Cartographer `GodModules` membership |
| `rank.importance` | num | 0 | score | `["hotspot.score"]` | derived (`internal/derive`), from in-degree of `calls`+`references` plus hotspot; **this is A3's `orderBy=importance`** (§6.7, Appendix B) |
| `closure.summary` | json | 0 | — | — | derived; `{transitiveCallers, moduleSpread, maxDepth, fileCount}` — the *counts*, not the closure (§7.1); written only for symbols with in-degree > N; **this is A5** |
| `index.warning` | text | 0 | — | — | on a file node: a symbol the canonical parser could not parse (§5.1). Non-fatal; surfaced by `ckb doctor` |

### 6.5 The flagship cross-tier SELECT

No containment closure table; `parent_id` for the tree plus `file_id`/`container_id` denormalized on every symbol node turn the current 5-way string-matched join (`00 §8`) into one statement (`01 §3.3`):

```sql
SELECT s.nkey, f.path, c.name,
       o.name AS owner, ev.confidence AS owner_conf, ev.source_id AS owner_source,
       hs.num_value AS hotspot
FROM node s
JOIN node f ON f.id = s.file_id
JOIN node c ON c.id = s.container_id
LEFT JOIN edge e          ON e.dst_id = f.id AND e.type = :owns
LEFT JOIN edge_evidence ev ON ev.edge_id = e.id                 -- provenance of the ownership claim
LEFT JOIN node o          ON o.id = e.src_id
LEFT JOIN fact hs         ON hs.node_id = f.id AND hs.key = 'hotspot.score' AND hs.state_id = :state
WHERE s.id = :sym;
```

The `edge_evidence` join is not optional decoration: `owns` is asserted by two sources with different confidences — CODEOWNERS (`declared`, 0.90) and git blame (`heuristic`) — so the row that says *who* owns the file is only usable with the row that says *how we know*. A caller wanting a single owner adds `AND ev.confidence = (SELECT MAX(confidence) FROM edge_evidence WHERE edge_id = e.id)`; a caller wanting to show the disagreement omits it and gets one row per source. Without the join the query returns the same owner name whether CODEOWNERS declared it or blame guessed it, which is precisely the conflation §4.3 exists to prevent.

### 6.6 Size estimates at 10×

| Table | Rows at 10× | Size |
|---|---|---|
| node | — | ~60 MB |
| node_fts | — | ~40 MB |
| edge | ~900k pairs (**estimate**) | ~30 MB |
| edge_evidence | ~950k (**estimate**: ~1.05 rows/edge — only telemetry and co-change routinely double up) | ~34 MB |
| occurrence | ~500k | ~22 MB |
| fact | ~500k | ~30 MB |
| **Total** | | **~215 MB**, vs ~480 MB extrapolated in `01 §3.6` for today's schema holding *less* data — an estimate, not a measurement |

The edge row is extrapolated from today's *file-level* 17,208 distinct pairs; distinct **symbol-level** call pairs cannot be counted until the caller bug is fixed (§14.1 item 5). The `edge`/`edge_evidence` split (§6.2) moves five columns off `edge` and adds one row per edge, so the pair is a wash on bytes and a win on the two indexes that matter — `idx_edge_out`/`idx_edge_in` now cover a five-column table instead of a ten-column one. The evidence-rows-per-edge ratio is the number to re-measure. Re-measure both at the end of Phase 0 before committing to edge/occurrence sizing.

Bulk-load pattern is kept as-is: `synchronous=OFF`, 128 MB cache, `wal_autocheckpoint=0`, batches of 1000, `wal_checkpoint(TRUNCATE)` at end (`updater.go:380-405`).

### 6.7 Indexes and why each exists

| Index | Serves |
|---|---|
| `UNIQUE(tier, nkey)` on `node` | access path A1: `resolve()` exact lookup |
| `idx_node_parent(parent_id, tier)` | ancestor/descendant walks (A2, A3) |
| `idx_node_path(path, tier)` | file/container lookup by path string (e.g. resolving `path:line` handles) |
| `idx_node_container(container_id, tier)` | "symbols in this container" (A3) without a join through `parent_id` |
| `idx_node_file(file_id)` | "symbols in this file", `occurrence`-adjacent lookups |
| `idx_node_live(tier, state, last_state)` | excluding tombstoned nodes cheaply; time-travel base case |
| `UNIQUE(src_id, type, dst_id)` on `edge` | one row per distinct relation; with the §6.3 ordering rule this also collapses both orientations of an undirected pair |
| `idx_edge_out(src_id, type, weight DESC, dst_id)` | access path A4 outbound, genuinely pre-sorted by weight for truncation |
| `idx_edge_in(dst_id, type, weight DESC, src_id)` | access path A4 inbound (e.g. callers) |
| `idx_evidence_own(owner_file_id)` on `edge_evidence` | file-node invalidation: `DELETE FROM edge_evidence WHERE owner_file_id = :f` (§7.2) |
| `idx_evidence_source(source_id, edge_id)` | §9.2's source filters (`calls` vs `observed-calling`), and wholesale replacement of one producer's evidence (§7.2) |
| `PRIMARY KEY (edge_id, source_id)` on `edge_evidence` | the evidence rows of one edge are one contiguous range: the join in §6.5 and the `MAX(weight)` recompute are both a single seek |
| `idx_occ_file(file_id, line)` | "what is at this line" (`01 §6`) |
| `idx_fact_key(key, num_value DESC)` | access path A6/top-N-by-metric without a scan; also A3 (`ORDER BY` over `rank.importance`, §6.4) |
| `idx_fact_text(key, text_value)` | access path A7 reverse lookup: value → nodes, for text-valued keys |
| `idx_fact_state(state_id)` | state-scoped reads and pruning sweeps (§7.5 retention) |
| `idx_state_published(published, state_id DESC)` | resolving `asOf` over published generations only, and finding an interrupted run's unpublished state (§7.7) |
| `idx_alias_node(node_id)` | reverse lookup: "what old keys point at this node" |
| `idx_change_node(node_id, state_id)`, `idx_change_state(state_id)` | access path A8, `changedSince`/`changedNodes` |
| `idx_usage_kind(kind, value DESC)` | A11 top-N prior lookup without a scan (§11.4) |
| `idx_usage_pair(node_b, kind, value DESC)` | the `node_b` side of a `usage:coaccess` pair lookup (§11.1) |
| `idx_usage_pk(node_id, kind, window_start, ifnull(node_b,0))` unique | one rollup row per node (or pair) per kind per window; the `ifnull` is because SQLite treats NULLs as distinct in a unique index, so a plain `PRIMARY KEY` over a nullable `node_b` would not constrain the single-node kinds at all |
| FTS `content_rowid='id'` on `node_fts`, `content_rowid='node_id'` on `body_fts` | one probe per hit instead of today's two (`symbols_fts_content` has its own rowid plus a 126-byte `id TEXT UNIQUE`, `internal/storage/schema.go:1334-1344`); both FTS rowids *are* node ids, so a hit joins to `node` with no lookup table |

**Ruling — weight comes before the opposite endpoint (corrects v0.2 B10).** v0.3 declared `idx_edge_out(src_id, type, dst_id, weight DESC)` and claimed the trailing `weight DESC` made A4's `LIMIT` index-order. It does not. SQLite orders an index by its columns left to right, so that index delivers matching rows sorted by `dst_id` — an arbitrary integer — and `weight` only breaks ties *within one `dst_id`*, of which there is exactly one row. Every A4 read would have been a full scan of the `(src_id, type)` range plus a sort, which is the cost the index was supposed to remove. The order is therefore `(src_id, type, weight DESC, dst_id)`: an equality seek on `(src_id, type)` followed by a descending-weight scan the `LIMIT` stops early. `dst_id` stays last so the index still covers the endpoint without a table lookup.

`UNIQUE(src_id, type, dst_id)` stays as a separate constraint — it is the relation-identity rule and it is not an access path. The two cannot be folded together: a unique index that includes `weight` would no longer constrain `(src_id, type, dst_id)`.

**Multiple requested types.** `edges(nodeId, kinds[], …)` with more than one kind is **one index scan per type, merged by weight in Go**, not a single `type IN (…)` scan — an `IN` on the second column breaks the descending-weight ordering the same way `dst_id` did. With A4's `k ≤ 200` cap and at most twelve wire types, the merge is a bounded k-way heap over ≤12 already-sorted streams; the planner cannot do better and doing it in SQL would cost a sort over the union.

---

## 7. Ingestion, derivation and invalidation

### 7.1 Materialization policy

| Data | When |
|---|---|
| node (all tiers), parent/file/container pointers, `defines`, `calls`, `references`, `refers`, `imports` | index time, full + incremental |
| occurrence, `edge_evidence` | index time, in the same transaction as the edge they attest |
| `complexity.*` (tree-sitter) | index time, per changed file (four extractors redo this today — collapses to one) |
| `churn.*`, `cochanges`, `authored` | index time; refreshed when HEAD has moved > N commits since the last git pass |
| `owns` from CODEOWNERS | index time |
| `owns` from blame | index time, for files with no CODEOWNERS match, on the same git pass as `churn.*` |
| Cartographer container nodes, `depends_on`, `in_cycle`, `violates_layer`, health/bridge/god-module facts | index time when built with `-tags cartographer` (§7.3) |
| `hotspot.score`, `risk.score`, `risk.factors`, `rank.importance` | index time, derived, `source='ckb-derived'` |
| `closure.summary` (counts only — see the ruling below) | index time, derived, for symbols whose `calls`+`references` in-degree exceeds N |
| `documents`, `decides`, doc nodes | index time if docs indexing enabled, else on `IndexDocs` |
| telemetry → facts + `calls` edges `source=telemetry` | on ingest command |
| LIP `near` edges | never eagerly; written opportunistically with expiry |
| LLM notes | never computed automatically; explicit tool call, stored in a versioned file, projected into node/fact (§7.6) |
| impact closures, blast radius, k-hop expansions, the transitive closure of calls *itself* | never — query-shaped; edge indexes make them cheap; the closure is n² to store |

**Ruling — the closure is not stored, its summary is.** v0.3 said closures are never materialized and, two appendices later, promised A5 `closureSummary(nodeId)` in under 1 ms. Both are right about different objects, and the spec never said so. The **closure** — the set of transitive callers — stays query-shaped and unstored, for the n² reason. The **summary** — `{transitiveCallers, moduleSpread, maxDepth, fileCount}`, four integers — is a `closure.summary` json fact (§6.4), recomputed once per index run by `internal/derive` for symbols whose in-degree exceeds N, and read by A5 as a single indexed `fact` row. Four integers per hot symbol is not n²; recomputing them inside `rank` for every candidate in every pack is what the store exists to stop. Symbols below the threshold have no row and A5 computes their counts directly, which is cheap precisely because their in-degree is small.

**Ruling — nothing in this table is written on the request path.** Blame-derived `owns` was the last exception; it moves to index time so that the store has exactly two writers, the index run and the usage rollup under its lease (§11.4), and neither one is an MCP request. The blame pass is bounded by the files CODEOWNERS does not already match and shares the git walk that `churn.*` and `authored` already pay for, so "compute once per index run" costs nothing extra.

### 7.2 File-node invalidation

The invalidation unit is the **file node**:

1. Hash change detected on a file → `DELETE FROM edge_evidence WHERE owner_file_id = :f` (occurrences cascade via the composite FK). Evidence rows with `owner_file_id IS NULL` are untouched — they belong to a different producer (see the ownership table below).
2. **No fact rows are deleted.** The index run writes facts for the *new* state; the old state's facts stay exactly where they are. Retention (§14.1 item 11) is the only thing that ever removes a fact.
3. Re-extract, upsert. Symbol ids **survive** because keys are commit-free (§5.1) — an unchanged symbol keeps its node id across the re-extraction.
4. Vanished symbols are tombstoned (§5.4), not deleted.
5. Inbound edges are left untouched (caller-owned invariant, `incremental/types.go:53-55`).
6. An `edge` row whose last `edge_evidence` row is gone is deleted; an `edge` with evidence remaining keeps its identity and gets `weight` recomputed as `MAX(edge_evidence.weight)`.
7. Transitive invalidation reuses the existing `file_deps` BFS (`incremental/deps.go`), now walking `imports` edges instead of the `file_deps` table; `rescan_queue` finally gets fed real work.

**Ruling — invalidation never deletes a fact.** v0.3's step 2 ("delete file-derived facts for the file and its symbols") destroyed the thing the fact plane exists for. `churn.commits.30d` is temporal, retained for 30 days of states (§14.1 item 11) and read as a *trend* by §9.5 Q5 — and it is file-derived, so every reindex of a hot file would have deleted the history that makes the trend a trend. It also broke the stale-fallback path in §7.3, which serves the latest state ≤ the requested one: that row is by definition an older file-derived fact. Facts are append-per-state; the primary key `(node_id, key, state_id, source_id)` already makes a new state a new row rather than an overwrite, so "write the new state" is the whole operation. Nothing needs the delete.

**Ruling — ownership is per evidence row, per producer.** v0.3 hung invalidation on one `owner_file_id` per edge, citing the caller-owned invariant at `incremental/types.go:53-55`. That invariant is about caller-owned *call* edges; it does not extend to a `cochanges` edge derived from the whole git history, an `owns` edge derived from a root CODEOWNERS file, or a `depends_on` edge aggregated from every file in two containers. With `edge_evidence` (§6.2) each producer's claim carries its own ownership:

| Producer (`source`) | Edge types | Owned by | Replaced when |
|---|---|---|---|
| scip | `defines`, `calls`, `references`, `refers`, `imports`, `implements`, `embeds`, `type_of` | `owner_file_id` = the file containing the occurrence | that file is reindexed (step 1) |
| git | `cochanges`, `authored` | `owner_file_id IS NULL` | the git pass runs: all evidence for `(source_id, type)` is replaced wholesale, keyed by `source_id` + the new `state_id` |
| codeowners | `owns` (declared) | `owner_file_id` = the CODEOWNERS file node | the CODEOWNERS file changes — one file, so step 1 covers it |
| git blame | `owns` (heuristic) | `owner_file_id IS NULL` | the blame pass runs; wholesale, as git above |
| cartographer | `depends_on`, `in_cycle`, `violates_layer`, `hidden_coupling` | `owner_file_id IS NULL` | a new `cartographer_run` completes; wholesale by `source_id` (§7.5) |
| telemetry | `calls` (observed) | `owner_file_id IS NULL` | an ingest command runs; wholesale by `source_id` and window |
| derived | `depends_on` (from `imports`), `tests` | `owner_file_id IS NULL` | the derive pass runs, after its inputs (§7.4) |
| lip | `near` | `owner_file_id IS NULL` | expiry (§7.1); never by file reindex |

Two consequences worth stating. A **multi-file contribution survives one file's reindex**: a container-level `depends_on` aggregated over 40 files is one producer-owned evidence row, so reindexing one of the 40 does not touch it — it is stale until the derive pass reruns, and §7.3's freshness labelling is what says so. And an edge asserted by two producers **loses only the retracted half**: reindexing a file removes the SCIP evidence for `A calls B` and leaves the telemetry evidence, so `observed-calling` (§9.2) still answers while `calls` correctly does not.

**Ruling — edges are HEAD-only; nodes and facts carry history.** Step 1 hard-deletes evidence, so a relation no longer asserted at HEAD is gone: `edge_evidence.first_state` means "first observed by this source" and `last_state` means "last confirmed by this source" — a provenance and staleness pair, **not** a time-travel window. The A12 validity-window predicate therefore applies to `node` and `fact` only (Appendix C). Two reasons: `UNIQUE(src_id, type, dst_id)` cannot hold a closed-window row and a reopened one for the same pair without putting a state in the key and paying for it on every A4 read, and no access path needs it — Q5 `history` (§9.5) and A8 are node-level, backed by `node_change`. Edge time travel is out of scope for 1.x.

### 7.3 State-scoped facts and carry-forward

A fact is valid for its `state_id`. **One policy, four steps, in this order** — every reader (A6, A12, `gather`, the pack) does exactly this and nothing else:

1. **Exact state.** `WHERE key = :k AND state_id = :asOf`. Hit → serve it, `freshness = 1.0` (§10.4), presentation `current`.
2. **Carry-forward.** Miss, and `fact_key.carry_forward = 1` → serve the newest row with `state_id <= :asOf`, `freshness = 1.0`, presentation `current`. The key's own catalog row asserts the value did not change, so this is not staleness.
3. **Stale fallback.** Miss, and `carry_forward = 0` → serve the newest row with `state_id <= :asOf` anyway, marked `stale`: `freshness` per §10.4, `asOf` set to the state actually served, and `Freshness.IndexAge{CommitsBehind, StaleReason}` populated (`envelope.go:41-51`). Never silently; §10.4's "freshness is shown, never hidden" is this step's contract.
4. **Nothing at or before `:asOf`** → the fact is absent, and absence is reported as labelled absence, not an omitted section (§9.7 rule 6, §14.2 item 6).

Retention (§14.1 item 11) is the only process that removes a row, and it runs *after* the index run, so step 3 can always find whatever retention has left. Invalidation removes nothing (§7.2). Appendix C's A6 and A12 entries restate this same order; if they and this paragraph ever disagree, this paragraph is the spec.

**Ruling — carry-forward is a property of the key, not the producer.** `is_test` is as true at the next commit as it was at the last one whether git, tree-sitter or a heuristic produced it; `churn.commits.30d` is stale the moment HEAD moves, from any producer. Putting the flag on `source` would force one answer for every key a producer writes.

### 7.4 Derive-once registry

New package `internal/derive`: one function per derived key, declaring `inputs []FactKey` and `output FactKey`, matching `fact_key.derived_from`. Functions are topologically sorted and evaluated once per index run.

| Fact | Before | After |
|---|---|---|
| complexity | 4 implementations | 4 readers of 1 derived value |
| churn | 3 implementations | 1 |
| dependency edges | 5 representations | `imports` (structural) + one derived `depends_on` |
| risk | 3 formulas | 1 score + stored `risk.factors` |
| test detection | 3 heuristics | 1 `is_test` fact |
| co-change | 2 (internal/coupling + Cartographer) | 1 edge type, tagged by source |

Enforcement, concretely: `fact` rows are written through exactly one function, `derive.WriteFacts`, and a test in `internal/derive` walks the repo's ASTs (`go/packages`, the same load the complexity extractor already does) and fails the build if any package other than `internal/derive` and `internal/storage` (1) contains a string literal matching `INSERT INTO fact`/`INSERT OR REPLACE INTO fact`, or (2) calls a `storage` method whose name writes `fact`. A second table-driven test asserts that the set of `output` keys declared by the registry equals the set of keys in `fact_key` with a non-null `derived_from`, so a new derived key cannot be added without a registered owner.

### 7.5 Cartographer freeze

`cartographer_run` (DDL in §6.2) is the provenance row for one Cartographer pass.

Outputs of `MapProject`, `Health`, `CheckLayers`, `GitChurn`, `GitCochange`, `HiddenCoupling`, `ShotgunSurgery`, `UnreferencedSymbols` become ordinary node/edge/fact rows with `source='cartographer@<ver>'` and the run's state. `reviewPR` reads instead of re-running. Cartographer `ModuleID`/`Path` is resolved to node ids at ingest time by path; the mapping is persisted, never re-derived per query.

**Replacement**: a Cartographer pass owns its rows as a producer, not per file — its `edge_evidence` rows carry `owner_file_id IS NULL` and are replaced wholesale by `source_id` when a new run completes (§7.2). Reindexing one file never invalidates them.

**Staleness rule**: if the newest `cartographer_run`'s state ≠ the current state, serve the data labelled stale with a commit delta, or re-run explicitly behind `ckb index --refresh-arch`.

### 7.6 LLM notes shape (reserved, ruling `03 R-3`)

LLM notes live at `.ckb/notes/<node handle-slug>.md` with YAML front matter: `nodeKey`, `author`, `model`, `promptVersion`, `createdAt`, `confidence`. They are projected into a tier-4 (doc) node plus a `documents` edge on index. This matches the plan that curated knowledge lives in versioned files (`change-intelligence-and-activity-ledger.md:218-219`). **Out of scope for the first three phases** — the spec only reserves the shape now.

### 7.7 Concurrency and WAL (ruling `03 R-5`)

v0.3 said a full rebuild "writes `.ckb/ckb.db.next` and atomically renames on completion", and that incremental runs commit in batches of ≤1,000 rows. Neither is safe as stated: renaming over an open SQLite database leaves every attached reader on the old inode with a journal/WAL pair that now belongs to a different file (the corruption mode SQLite documents), and a sequence of independent transactions publishes partial generations — A12 promises a snapshot-consistent read, and WAL gives that guarantee *per transaction*, not across a run. Both need a protocol, not an atomic syscall.

**Full rebuild — generation handoff.**

1. The run builds `.ckb/ckb.db.next` from scratch, finishing with `wal_checkpoint(TRUNCATE)` so the generation is one self-contained file with no live WAL.
2. It writes the new `db_epoch` into the new file's `index_meta` (§5.7) and, last, `rename(.ckb/ckb.db.next → .ckb/ckb.db)`.
3. **Readers do not discover this by the rename.** Each `ckb mcp` process reads `index_meta('db_epoch')` at open and, on every request boundary, compares it against the epoch in `.ckb/ckb.db.generation` — a small marker file the rebuild writes *before* the rename, read with a stat-and-read that never touches the database. A changed epoch means: finish the in-flight read transaction, close the connection pool, reopen, continue. `n:` ids from the old epoch are rejected by A1 (§5.5), so a client mid-conversation gets `unknown-id` and re-resolves by handle rather than reading a wrong node.
4. The renamed-over inode survives until the last fd closes, so a reader that has not yet noticed keeps serving a coherent old generation instead of a torn new one. This is the property the bare rename was reaching for and did not have.

**Incremental runs — unpublished states.** A rebuild is not the common case; an incremental run is, and it cannot use a second file. Instead it publishes a *state*, not a file:

- The run allocates one `repo_state` row with `published = 0` and tags every node, edge, evidence, occurrence, fact and change row it writes with that `state_id`.
- Readers resolve `asOf` — and "HEAD" — only over `repo_state WHERE published = 1` (index `idx_state_published`, §6.7). Rows belonging to an unpublished state are invisible: they are in the database, they are not in any answer.
- The run's **last** transaction is `UPDATE repo_state SET published = 1 WHERE state_id = :s`. That single-row commit is the publication, and it is atomic, so no reader ever sees half a generation. Batching stays at ≤1,000 rows per transaction for WAL health, and now costs nothing in consistency.
- **Recovery.** A crashed run leaves exactly one unpublished state. The next run deletes it first (`DELETE FROM repo_state WHERE published = 0`, cascading to every row that referenced it) and starts its own. No repair pass, no partial-generation detection heuristic — the invariant is "at most one unpublished state exists, and it is garbage".

**Each pack pins one read transaction.** A pack that issues twelve queries across A1/A4/A6 must see one state in all twelve. The retrieval pipeline therefore takes a single `sql.Conn` from the reader pool, opens a read transaction for the pack's lifetime, and releases it at `explain`. Without this, a publication landing mid-pack would give the `callers` section one state and the `tests` section another — silently, which is precisely what A12 forbids.

**Per-connection PRAGMAs and pool policy.** `internal/storage/db.go:36-56` opens a `sql.DB` and then runs its seven PRAGMAs through `conn.Exec` — which hands them to *one* pooled connection, whichever the pool happened to give out. `foreign_keys` and `busy_timeout` are per-connection settings, so every other connection the pool opens has foreign keys **off** and no busy timeout. The store's cascades (`ON DELETE CASCADE` from `node`, from `edge`, from `edge_evidence`) are load-bearing in §7.2, so this must be fixed before anything relies on them:

- PRAGMAs are set through a modernc connection-init hook, or equivalently a DSN carrying `_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)`, so they apply to every connection the pool ever opens.
- **Two pools.** A writer pool with `SetMaxOpenConns(1)` — one writer, which SQLite requires anyway and which makes "one writer per derived key" (§7.4) enforceable rather than aspirational — and a reader pool of N connections, `query_only` where the driver supports it.
- A startup assertion reads `PRAGMA foreign_keys` back on a freshly-opened connection and fails loudly if it is 0. The current bug is invisible precisely because nothing checks.

**WAL.** Checkpoint after every incremental batch group (addresses the WAL-growth risk — today's 74 MB WAL against a 48 MB database), and `wal_checkpoint(TRUNCATE)` at the end of a full rebuild before the handoff.

A shadow-DB-and-swap approach was considered in `01 §7.6` as a candidate for concurrency between `ckb index` and `ckb mcp`; the protocol above supersedes it.

---

## 8. Retrieval pipeline

### 8.1 The fact is the retrieval unit (`02 R1`)

`(nodeId, factKind, value, source, confidence, repoState, edgeProvenance?)`. Packs are sets of facts. Dedup, budgeting, ranking and staleness all operate on this one record type; no stage emits text selected by similarity alone.

Rejected: chunk-and-embed the repo (violates `docs/ideas.md:384-386`; the agent reads code better than CKB chunks it; a vector store breaks the single-binary constraint). Rejected: LLM summaries as primary retrieval targets — overlay only, confidence capped at ≤0.3.

### 8.2 Seven stages

| Stage | Does | Existing | New |
|---|---|---|---|
| intent | deterministic classification into an intent enum; rules + param shape; **no LLM** | — | `internal/retrieval/intent` |
| seed | ≤50 candidate nodes: A1 exact, A9 FTS, path/glob (`compound.go:331-360`), git-recency, optional LIP `NearestByText`/`NearestBySymbol` (`lip/client.go:319,369`), `QueryExpansion` (`client.go:650`) | FTS, LIP, resolvers | seed budgeter, per-intent seed mix |
| expand | typed-edge frontier under a per-intent edge budget (A4), plus PPR for diffuse fanout, seeded from the store | `graph.PPR`, `DefaultEdgeWeights` | expansion planner, frontier cap |
| gather | one batched fetch per overlay kind (A6); structural facts only | overlay repositories | batch gather |
| rank | evidence weight × intent relevance × freshness × redundancy (§10) | retires `RerankWithPPR`; keeps `PPRResult.Path` as provenance | `internal/retrieval/rank` |
| pack | token-budgeted assembly, section quotas, explicit truncation report | `internal/compression` (count-based head truncation today), `internal/envelope` | token-aware packer |
| explain | per-fact provenance, per-section `asOf` + confidence, envelope `Confidence{Score,Tier,Reasons,Factors}` | `envelope.go:22-34` | evidence → Factors mapping |

### 8.3 Embeddings policy (`02 R3`)

Embeddings are admitted in exactly **two** places, both labelled:

1. **seed** — candidates carry an `origin` tag.
2. **rank** — tie-break only, ≤0.05 weight.

**Forbidden in gather.** Every node in a pack carries `origin: structural | semantic-confirmed | semantic-only`. `semantic-only` results go to a separate `leads[]`, capped at ≤5% of budget, never in an answer section, never counted toward confidence. This generalizes the existing `SemanticCallerInfo{Tier, Confidence}` (`impact.go:113-134`).

Rejected: semantic-only hits in the body with a low score — an LLM consumer discounts by section placement, not by a number buried in a field.

### 8.4 Hard caps (`02 R4`)

Seeds ≤50, frontier ≤500, packed facts ≤120, default budget 2,000 tokens, max 6,000 without an explicit `budget`.

### 8.5 Pack cache

`view_cache` keyed `(intent, sortedSeedIds, budget, sortedInclude[], repoStateId, dbEpoch, rankConfigHash)` — currently 0 rows because nothing populates it. The table is `view_cache(key TEXT PRIMARY KEY, value_json, expires_at, state_id TEXT, created_at)` (`internal/storage/schema.go:478-484`): the composite key is serialized into `key`, and `repoStateId` also lands in `state_id` so the retention sweep can drop stale packs by state.

**Ruling — the key covers everything that changes the bytes; freshness is a TTL, not an invalidation graph.** v0.3's four-part key omitted three things that change the response: `include[]` (a pack with `rawIds` is a different payload), `db_epoch` (every `n:` id in a cached pack names an epoch, so a rebuilt database's cache entries are not merely stale but wrong), and the ranking configuration (a weight change must not be masked by a warm cache — this one matters most during the §12.3 two-mode eval). `rankConfigHash` is a hash of the `w_*` weights and the §8.4 caps.

Overlays refreshed without a repo-state change — a usage rollup (§11.4), a Cartographer re-run (§7.5), a telemetry ingest — are handled by **`expires_at` = created + 10 minutes**, not by tracking which seeds' subtrees a write touched. Of the two options, this is the one that ships: dependency-tracked invalidation needs a write-side index from every fact row to every pack whose seed subtree contains it, which is a second graph to maintain and get wrong, to save re-packing something that costs a few milliseconds and is bounded by §8.4's caps anyway. A 10-minute ceiling on overlay staleness is well inside the half-life of every signal that can move without a state change.

### 8.6 Drift to fix before adding any new ranking layer (`02 §0.2`)

1. `docs/hybrid-retrieval.md:74-87` documents a fusion table (FTS .40 / PPR .30 / Hotspot .15 / Recency .10 / Exact .05) that production never executes. `FusionRanker.Rank` has no non-test caller; the live path is `RerankWithPPR` (`ranking.go:232-294`) with a hard-coded `0.6*position + 0.4*ppr` (`ranking.go:277`); `HotspotScores`/`RecencyScores` in `RankOptions` (`ranking.go:114-128`) are never filled. **Delete or wire before building on top; do not add a new layer over undocumented behavior.**
2. PPR already computes explanation paths (`PPROptions.IncludePaths` default `true`, `ppr.go:37-52`) and `RerankWithPPR` drops them (`ranking.go:256-260`) — wire this through so PPR's proximity paths are available to the `explain` stage as edge-level provenance.

---

## 9. Agent-facing surface

### 9.1 Three new tools, not twenty (`02 N1`)

Today: 110 tools, page size 40 (`mcp/cursor.go:13`), core preset ~28 (`mcp/presets.go:29-76`), `EstimateActiveTokens` (`mcp/server.go:421-431`).

| Tool | Purpose | Replaces |
|---|---|---|
| `ckbFind` | intent-aware candidate selection; ranked node refs + why; no bodies | most of `searchSymbols`, `symbolExists`, `listKeyConcepts` |
| `ckbNavigate` | one call moves up/down/sideways/time from any node | `getCallGraph`, `findReferences`, `traceUsage`, `getModuleOverview`, `explainPath`, `recentlyRelevant` |
| `ckbContext` | intent + budget → pack | `explore`, `understand`, `explainSymbol`, `explainFile`, `getArchitecture`, `exportForLLM` |
| `prepareChange` / `assessChange` | names unchanged, reimplemented as `ckbContext(intent=…)` | product front door |

**Core preset target: 10–14 tools.** Acceptance criterion: `EstimateActiveTokens()` for the core preset is −50% with **no regression** in tokens-to-answer (§12).

Rejected: one tool per movement direction (600–900 tokens per session just in the tool list; multiplies wrong-tool-choice failures). Rejected: one mega-tool with a string-op parameter (defeats schema validation).

### 9.2 ckbNavigate

```json
{"from":"internal/query/symbols.go:412","direction":"out","edges":["calls","implements"],
 "depth":2,"limit":30,"since":null,"budget":600,"include":["location","module"]}
```

`direction` ∈ `up | down | out | in | time`. `edges` is a closed vocabulary of 12 values. Per-direction defaults mean `{"from":X,"direction":"in"}` is always a valid call.

**Wire name → storage.** The wire vocabulary is the agent's; the catalog in §6.3 is the store's. This table is the whole mapping — a wire name that is not here is not a valid `edges` value. Every filter in the last column is a predicate on **`edge_evidence` joined to `source`** (§6.2), never on `edge`, which carries no provenance: `JOIN edge_evidence ev ON ev.edge_id = e.id JOIN source sr ON sr.source_id = ev.source_id WHERE …`. An edge with evidence from two sources appears under both wire names, with each name reporting its own confidence and `asOf`.

| Wire name | Storage type (§6.3) | Walk | Evidence filter (`edge_evidence` ⋈ `source`) |
|---|---|---|---|
| `calls` | `calls` | `src → dst` | `sr.kind != 'observed'` — any evidence except telemetry's |
| `references` | `references` **and** `refers` (§6.3) | `src → dst`; a `refers` row's source end is a file node | — |
| `implements` | `implements` | `src → dst` | — |
| `imports` | `imports` from a file node, `depends_on` from a container node | `src → dst` | — |
| `contains` | not an edge — `parent_id` / `container_id` / `file_id` walk (§4.1) | down, or `⁻¹` for up | — |
| `co-changes-with` | `cochanges` | either orientation (undirected, §6.3) | — |
| `tested-by` | `tests` | `dst → src` (reversed) | — |
| `owned-by` | `owns` | `dst → src` (reversed) | — |
| `documented-in` | `documents` | `dst → src` (reversed) | — |
| `decided-in` | `decides` | `dst → src` (reversed) | — |
| `observed-calling` | `calls` | `src → dst` | `sr.kind = 'observed'` — telemetry evidence only |
| `similar-to` | `near` | `src → dst` | `lip`; always labelled, since it's the semantic edge |

`calls` and `observed-calling` are the pair that motivated splitting evidence out of `edge` (§6.2): they are complementary filters over the *same* relation, so with one `source_id` per edge each would have been lossy in whichever direction the last writer won.

`edges:["contains"]` with `direction:out|in` is an alias for `direction:down|up` — both forms are accepted and the store executes the same `parent_id`/`container_id`/`file_id` walk.

**Deliberately not navigable.** `defines` is reached as `contains` from a file node, so exposing both would give the agent two names for one walk. `embeds` and `type_of` have no wire name in v1 — they are SCIP-precise but no fixture in §9.5 needs them; they get one when one does. `authored`, `hidden_coupling`, `violates_layer` and `in_cycle` are read as `ckbContext` sections (ownership, arch), not as movements: they answer "what is true of this node", not "where do I go from here".

Row shape:

```json
{"id":"n:4.3f7","handle":"query.Engine.FindReferences","edge":"calls","depth":1,
 "at":"internal/query/symbols.go:812","module":"internal/query",
 "weight":0.91,"origin":"structural","evidence":"scip:reference","asOf":"head@1ce495b"}
```

### 9.3 ckbContext

```json
{"target":"SearchSymbols","intent":"understand","budget":2000,"include":["tests","docs"]}
```

Response shape: `schemaVersion`, `data.intent`, `data.focus{id,handle,kind,at}`, `data.verdict`, `data.sections[{name,asOf,conf,total,shown,facts[]}]`, `data.budget{requested,used,dropped[]}`, `data.leads[]`, `meta.confidence{score,tier,reasons,factors}`. `intent` is optional; an inferred intent is echoed back so the caller can see what was assumed.

Example response, for the request above, applying the response-shape rules in §9.7 (verdict first, counts before lists, short handles, group by module, omit empty, `asOf`/`conf` per section, truncation as a fact):

```json
{
  "schemaVersion": "1.0",
  "data": {
    "intent": "understand",
    "focus": {"id": "n:4.9c2", "handle": "query.Engine.SearchSymbols", "kind": "symbol", "at": "internal/query/symbols.go:412"},
    "verdict": "Public API, 214 callers across 7 modules; no failing tests reach it; ADR-0031 documents its ranking contract.",
    "sections": [
      {
        "name": "callers", "asOf": "head@1ce495b", "conf": 0.93, "total": 214, "shown": 12,
        "facts": [
          {"id": "n:4.7a1", "handle": "mcp.tool_impls.SearchSymbolsHandler", "module": "internal/mcp", "edge": "calls", "weight": 0.91, "origin": "structural"},
          {"id": "n:4.5c3", "handle": "compound.Understand", "module": "internal/query", "edge": "calls", "weight": 0.88, "origin": "structural"}
        ]
      },
      {
        "name": "tests", "asOf": "head@1ce495b", "conf": 0.80, "total": 3, "shown": 3,
        "facts": [
          {"id": "n:4.4e2", "handle": "query.TestSearchSymbols_Ranking", "module": "internal/query", "edge": "tested-by", "origin": "structural"}
        ]
      },
      {
        "name": "docs", "asOf": "head@1ce495b", "conf": 0.90, "total": 1, "shown": 1,
        "facts": [
          {"id": "n:4.d01", "handle": "ADR-0031", "edge": "documented-in", "origin": "structural"}
        ]
      }
    ],
    "budget": {"requested": 2000, "used": 1840, "dropped": [{"section": "callers", "kept": 12, "total": 214, "reason": "budget"}]},
    "leads": []
  },
  "meta": {
    "confidence": {"score": 0.87, "tier": "high", "reasons": ["1 section below high tier (tests, 0.80)"], "factors": [{"source": "exact", "impact": 0.6}, {"source": "declared", "impact": 0.2}]}
  }
}
```

### 9.4 Short ids with a per-response id map (`02 N3`)

`id:"n:4.9c2"` — `n:<db_epoch>.<node.id>`, both base36, stable for the life of the database generation and re-minted only by a rebuild, which bumps the epoch so a stale id is *rejected* rather than misresolved (§5.5, §5.7; resolvable via A1) — plus `handle`. Raw SCIP strings appear only under `include:["rawIds"]`. Saves 40–60 tokens per referenced symbol relative to today's raw-SCIP-string responses; this is independent of the store itself. A1 must accept **all** the id forms listed in §5.5 **forever**, because agents cache ids across sessions.

### 9.5 The nine questions

Notation: `E` walks the edge with its stored direction (`direction:out`), `E⁻¹` against it (`direction:in`); `contains` / `contains⁻¹` are `direction:down` / `up` (§9.2). The number in parentheses is the depth.

| # | Question | Intent | Tiers | Edges (depth) | Budget | Sections in order | Omit |
|---|---|---|---|---|---|---|---|
| Q1 | What does X do, who depends on it | `understand` | symbol, file, module, docs, churn, ownership | contains⁻¹ (1), calls⁻¹ (1, counts to 3), calls (1), implements (1), documented-in (1) | 2,000 | identity; one-line purpose; signature; callers by module (top 12 + counts); callees (top 8); tests reaching it; overlays; docs/ADRs; leads | bodies; per-file reference lines; stdlib callees; zero-edge items |
| Q2 | If I change X, what breaks, which tests | `prepare-change` | + tests, co-change, ADR, risk, telemetry | calls⁻¹ (3, summarized beyond 1), contains (1), co-changes-with (1), tested-by (2), decided-in (1) | 3,000 (5,000 if moduleSpread>3 and public) | verdict line (risk + 2 driving factors); breaking surface; direct dependents by module; transitive summary (counts); tests to run as runnable command; untested downstream paths; co-changed files; ADRs; reviewers; arch impact | full transitive lists; generic advice |
| Q3 | Where is the code for concept Y | `locate` | FTS over symbol+doc+responsibility, module, file | seed only + contains⁻¹ | 500 | ranked candidates (handle, file:line, why, origin); one "start here" | everything else |
| Q4 | Shape of this module, where do I add | `module-map` | module, file, symbol, arch, ownership, responsibility | contains (2), imports/imports⁻¹ (1), owned-by (1) | 1,500 (3,000 deep) | responsibility + role; file table with symbol counts/roles; public surface; module edges; entry points; conventions; hotspots | per-symbol detail; private symbols; imports >10 |
| Q5 | What changed recently here and why | `history` | node change log, git, ADR, churn | `direction:time` (A8), co-changes-with (1), decided-in (1) | 1,000 | last N structural changes at node; commits with authors; ADRs dated near; churn trend; co-changed neighbours | full messages; diffs |
| Q6 | Is there an ADR/doc about this | `docs` | docs, decisions, module | documented-in (1), decided-in (1), then ancestors fallback | 400 | direct hits; module-level hits labelled inherited; "none found" is valid | bodies |
| Q7 | How does A reach B | `path` | symbol, file, module | calls BFS ≤6, k-shortest 3 | 1,200 | shortest path as numbered chain with file:line; ≤2 alternatives summarized; per-hop confidence; "no path" + what was searched | node detail beyond handle+location |
| Q8 | I finished editing, what did I touch, what to verify | `assess-change` | working-tree diff, symbol, tests, contracts, ADR, ownership | as Q2, seeded from changed nodes | 2,500 | changed nodes by file; contract changes (labelled heuristic without reindex); tests to run; consumers with no reaching test; ADRs; reviewers; stale-index warning | unchanged code |
| Q9 | Everything about X in N tokens | `focus` | all | intent-blended | N | allocator over Q1/Q2/Q4/Q5/Q6 sections by intent prior; returns `budget.used` and `budget.dropped[]` | what the allocator drops, named |

### 9.6 Token arithmetic (`02 §2.1`)

~4 bytes/token (`wide_result_metrics.go:164-168`; calibration flagged as risk, see §14).

| Element | Tokens |
|---|---|
| identity line (handle, kind, file:line, visibility) | ~25 |
| edge row | ~15 |
| overlay fact with source + asOf | ~25 |
| signature | 30–60 |
| doc/ADR reference | ~20 |
| one raw SCIP symbol string | 40–60 |
| JSON overhead per section | ~30 |

2,000 tokens ≈ 70–100 facts. A 50-item response echoing raw SCIP ids pays 2,000–3,000 tokens in identifiers alone — this is why rule N3 (short ids + id map) exists. Worth 500 tokens: locate, ownership/reviewers, "is there an ADR", "what changed here", existence checks. Worth 5,000: a pre-change safety pack for a public symbol with >20 cross-module dependents; first-contact module onboarding. Nothing else.

### 9.7 Response-shape rules for an LLM consumer

1. **Verdict first**: section 1 is one actionable sentence; the rest is evidence for it.
2. **Counts before lists**: `{"total":214,"modules":7,"shown":12,"items":[…]}`, never a bare truncated array (`UnderstandReferences` does this today, `compound.go:1004-1009`; `PrepareChangeResponse` does not — inconsistency to fix).
3. **Short handles in the body**, one id map at the end (N3).
4. **Group by module**, order by evidence weight within group.
5. **Omit empty fields.**
6. **Every section header carries `asOf` and `conf`**; not per fact.
7. **No generated prose advice** (`ExploreResponse.Suggestions`, `PrepareRisk.Suggestions` are examples to remove); use `suggestedNextCalls` (`envelope.go:95-100`) instead.
8. **Truncation is a fact**: `dropped: [{"section":"references","kept":8,"total":214,"reason":"budget"}]`.

### 9.8 Deprecation by aliasing (`02 N5`)

`understand` → `ckbContext(intent=understand)`; `explore` → `module-map`; `getCallGraph` → `ckbNavigate`. Thin adapters live for two minor versions before removal.

---

## 10. Evidence, ranking, freshness, confidence

### 10.1 Evidence weight table (`02 E1`)

| Source | Class | Base | Envelope tier |
|---|---|---|---|
| SCIP def/ref at HEAD | exact | 1.00 | high |
| SCIP, N commits behind | exact-stale | 1.00 | medium |
| SCIP, file dirty | exact-preedit | 1.00 | medium |
| ADR, CODEOWNERS, module annotation | declared | 0.90 | high |
| telemetry observed | observed | 0.75 | medium |
| LIP semantic, structurally confirmed | confirmed | 0.80 | medium |
| Cartographer resolved edge | structural-heuristic | 0.60 | low |
| tree-sitter derived | heuristic | 0.55 | low |
| git co-change (≥5, corr ≥0.4) | correlational | 0.50 | low |
| Cartographer scores | heuristic-score | 0.40 | low |
| LLM note | generated | 0.30 | speculative (must carry model id + prompt version) |
| LIP unconfirmed | speculative | 0.25 | speculative, leads only |

`Base` is the weight of the *source*, never of its age: the three SCIP rows are all 1.00 because SCIP at a stale state is still SCIP. Staleness enters exactly once, as the `freshness` term in §10.2 (`0.98^commits`, `0.7` dirty — §10.4). The rows differ only in envelope tier, which is what makes a stale exact fact presentable-but-labelled rather than silently discounted twice.

### 10.2 Confidence and score are two different numbers (`02 E2`)

**Ruling — `conf` measures how much the fact is believed; `score` measures how much this pack wants it.** v0.3 had one number doing both jobs, and §10.5 then averaged it into the pack's `Score`, which is presented to the caller as *confidence*. The result was that asking a question the retriever considered highly relevant, about a node many sessions had visited, raised the reported confidence of an answer whose underlying evidence had not changed at all — behavioural popularity laundered into a trust signal, in direct violation of `02 L3` and §11.3. They are separated here permanently.

```
conf(fact)  = w_evidence(source)          # §10.1, the source's base weight
            × freshness(factState, queryState)   # §10.4
            × row_confidence              # fact.confidence / edge_evidence.confidence (§6.2)

score(fact) = conf(fact)
            × w_intent(factKind, intent)  # relevance to the question asked
            × (1 − redundancy(fact, packed))     # §10.3
            + w_usage × usagePrior(node)  # w_usage ≤ 0.10; added only if
                                          # |score(fact) − score(best)| ≤ 0.05 · score(best)
                                          # computed before the prior is added (§11.2)
```

- **`conf` composes the stored row confidence explicitly.** Every fact and every `edge_evidence` row carries its own `confidence` (§6.2) and v0.3's formula never referenced it; a `w_evidence`-only product silently pinned every row from a source to that source's default. A CODEOWNERS entry the parser half-matched and one it matched exactly are both `declared`/0.90 by source, and they are not equally believable.
- **`conf` contains no `w_intent` and no `w_usage`.** Neither term is evidence about the world: `w_intent` is a statement about the *question*, `usagePrior` a statement about *other sessions*. A fact's believability does not change because someone asked differently.
- **`score` is ordering and inclusion only.** It decides what enters the pack and in what order, and it is never reported as a confidence anywhere in the envelope.

The usage gate is what keeps usage a tie-breaker rather than a ranking signal: outside the 5% band the prior is simply not added (§11.2). `w_intent` replaces the fusion table from §8.6 item 1: relevance is a function of the question, not a fixed blend.

### 10.3 Redundancy rule

One fact per `(node, factKind)` from the highest-weight source; other sources are appended as `alsoFrom:[…]`. +0.05 per independent corroborating source, capped at +0.10.

### 10.4 Freshness function and presentation states (`02 E3`)

```
freshness = 1.0                    if equal state
          = max(0.5, 0.98^commits) if behind by `commits`
          = 0.7                    if file dirty
```

| State | Meaning | Presentation |
|---|---|---|
| current | state matches query state | shown normally |
| stale | behind, kept | `asOf` + `staleBy`, sorted below current |
| superseded | replaced by a newer fact | omitted, `supersededCount` shown |

Freshness is **shown, never hidden**. Section header shows the oldest state it contains.

### 10.5 Pack confidence on the existing envelope (`02 E4`)

- `Score` = token-share-weighted mean of packed facts' **`conf`**, never of `score` (§10.2).
- `Tier` = minimum tier among load-bearing sections.
- `Reasons` = two largest negative factors.
- `Factors` = one entry per source class used, signed impact.

**The verdict line and the confidence tier read `conf` only.** `data.verdict` (§9.3), the `meta.confidence` block, and the refuse-above-medium rule for a behind-and-dirty index (§14.2 item 1) are all computed from `conf`; `w_intent` and `w_usage` do not appear in any of them. A pack whose facts were reordered by intent relevance reports the same confidence as one that was not, because the same rows are in it.

Rejected: a single `Meta.Accuracy` string — not decomposable, not actionable for a caller deciding whether to trust a verdict. Rejected: averaging `score` — see the ruling in §10.2.

This also answers `01 §7.10` (confidence composition "not defined anywhere in code") — ruling `03 R-3`: row confidence enters through `conf = w_evidence × freshness × row_confidence`; pack confidence is E4 above.

---

## 11. Learning loop (ledger → priors)

### 11.1 Signals (`02 L1`)

Fed back as retrieval **priors only, never as facts about code**:

- `usage:access` — decayed target counts. One row per node: `node_b IS NULL`.
- `usage:coaccess(a,b)` — same session, ≤5 calls apart. This is a **pair**, and it is stored as one: `usage_overlay.node_b` holds the second endpoint (§6.2), canonically ordered `node_id < node_b` as for undirected edges (§6.3). v0.3's `(node_id, kind, value, …)` shape could not express it at all — a co-access score collapsed onto one of its two endpoints is a popularity count, not a pairing, and the prior it feeds ("you looked at A, so B is probably relevant") needs the pair or it is nothing.
- `usage:followThrough` — `prepareChange(X)` then `assessChange` whose changed nodes ∩ predicted blast radius. This is a precision proxy for blast-radius ranking and the **only closed loop** in the product.
- `usage:refinement` — search followed by an overlapping search; feeds §12 metrics, not ranking.

### 11.2 Caps (`02 L2`)

`w_usage ≤ 0.10`, applied only when structural scores are within 5%. Half-life 14 days. Normalized per repo.

**Ruling — the "a session's own calls never feed its own priors" promise is dropped.** It was unenforceable as specified: the rollup aggregates over a window and the per-session attribution is gone by the time a reader looks, so honouring it needs per-session partial sums kept for the current window and a reader that subtracts its own — a second write path, per-session rows in a table whose whole justification is that it is small, and a subtraction that is wrong the moment a session spans a rollup boundary. The simpler option is to not promise it, and the caps already carry the risk it was guarding: `w_usage ≤ 0.10` inside a 5% band cannot move an ordering that structural evidence has not already made a tie, priors are off by default (§11.5) and always off in eval (§12.3), so a self-confirming loop has no room to close. Self-reinforcement is measured, not asserted away: the priors-on vs priors-off comparison in Phase 3 (§13.2) is what would show it.

### 11.3 Never inferred from the ledger (`02 L3`)

Code quality; anything entering a risk score or change-safety verdict (`usage:` is readable in `rank`, **unreadable in `gather`**, enforced by type); ownership; intent narration; tokens saved; cross-repo/cross-user aggregation; prompt or file content.

The type-level boundary is necessary but was not sufficient in v0.3: `usagePrior` also reached the verdict *numerically*, through a single blended score that §10.5 then averaged into the reported confidence. §10.2's `conf`/`score` split closes that path — `w_usage` is a term in `score` only, and nothing that reports confidence or produces a verdict reads `score`.

### 11.4 Rollup, not request-path scans (`02 L4`)

`usage_overlay(node_id, node_b, kind, value, window_start, window_end)` (DDL in §6.2; `node_b` is NULL for the single-node kinds and the second endpoint for `usage:coaccess`), written on MCP start plus an hourly ticker. Writer ownership (ruling `03 R-2`): the rollup runs inside a `ckb mcp` process holding a lease under the `index_meta` key `usage_rollup_lease` (expiry 2h); short transactions, batches of ≤500 rows; `ckb daemon` takes the lease when present; the request path never writes usage rows.

### 11.5 Default off (`02 L5`)

Off by default until measured; **always off in eval** (§12).

### 11.6 Behavioural-data boundary (`02 §7.10`)

Behavioural data is developer data: local-only, opt-out, excluded from export and federation, aggregate-only. This must be stated in user docs.

---

## 12. Evaluation

Today `internal/eval` calls only `SearchSymbols` (`eval/suite.go:227-238`); metrics are recall@K/MRR/latency; fixture types are needle/ranking/expansion (`02 §6`).

### 12.1 Six fixture types (`02 V1`)

| Type | Input | Ground truth | Metric |
|---|---|---|---|
| locate | NL description | expected nodes | tokens-to-target, calls-to-target |
| pack | question + intent + budget | required fact set + forbidden facts | fact recall, payload efficiency (required/delivered), budget adherence |
| impact | changed symbol | files touched in the real commit | blast-radius recall/precision; test-selection recall |
| crosstier | question needing ≥2 tiers | expected node | hops vs minimal; edge-walk correctness |
| staleness | dirty tree / behind index | which facts carry which label | stale-label accuracy |
| negative | no answer exists | "none" | cost of no (≤300 tokens) |

### 12.2 Metrics (`02 V2`)

`TokensToTarget{p50,p90}`, `CallsToAnswer`, `FactRecall`, `PayloadEfficiency`, `PackTokens`, `BudgetOverrunPct`, `StaleLabelAccuracy`, `CostOfNo`; keep `RecallAtK`, `MRR`, `AvgLatency`.

### 12.3 Two-mode harness (`02 V3`)

Every fixture runs in `mode=legacy` and `mode=store`, same repo-state, report delta. The legacy baseline must be captured **before** the store lands.

Proposed public targets: tokens-to-target −50%, fact recall ≥0.9 at 2k, payload efficiency ≥0.5 (today estimated 0.15–0.25 in `02 V3`; unmeasured — the Phase 0 legacy baseline measures it), impact recall ≥0.8, cost-of-no ≤300. These are proposals carried from `02 V3`, not derived from a measurement; the Phase 0 baseline is what turns them into targets or replaces them.

### 12.4 Ground truth (`02 V4`)

Mined then hand-checked: ~40 locate fixtures for CKB itself, ~20 each for two OSS repos. `internal/bench` stays a separate concern.

---

## 13. Migration and phasing

### 13.1 Table disposition (49 → 16 model tables plus caches) (`01 §5.1`)

The 16 are the 15 declared in §6.2 — `repo_state`, `source`, `node`, `edge_type`, `edge`, `edge_evidence`, `occurrence`, `fact_key`, `fact`, `node_fts`, `node_body`, `body_fts`, `node_change`, `cartographer_run`, `usage_overlay` — plus `node_alias` (§5.3).

**Become the model:**

| Old table | New shape |
|---|---|
| `indexed_files` | `node` tier 2 |
| `file_symbols` | `node` tier 3 + `file_id` + `defines` |
| `callgraph` | `calls` + `edge_evidence` + `occurrence` |
| `file_deps` | `imports` |
| `SCIPIndex.RefIndex` (in-memory, `references.go:12,16`) | `references` (symbol → symbol) + `refers` (file → symbol), §6.3 |
| `symbols_fts_*` | `node` + `node_fts` |
| `modules`/`dependency_edges` | `node` tier 1 + `depends_on` |
| `module_renames`, `symbol_mappings`, `symbol_aliases` | `node`/`node_alias` |
| `hotspot_snapshots`, `risk_scores` | `fact` (temporal) |
| `coupling_cache` | `cochanges` |
| `ownership`/`ownership_history` | `node` tier 5 + `owns` + `fact` |
| `responsibilities` | `fact` + `node_body`/`body_fts` (prose is body text, not a `node` column — §6.2) |
| `decisions` | `node` tier 4 kind=adr + `decides` |
| `docs`/`doc_references`/`doc_modules` | `node` tier 4 + `documents` + `node_body`/`body_fts` |
| `observed_usage`/`observed_callers` | `fact` + `calls`(source=telemetry) |
| `coverage_snapshots` | `fact` |
| `lip_annotations` | `fact`(source=lip) |

**Dropped:** `symbol_suffixes`, `docs_meta`.

**Stay:** `schema_version`, `index_meta`, `query_cache`, `view_cache`, `negative_cache`, `rescan_queue`, `telemetry_sync_log`, `observed_unmatched`, `wide_result_metrics` (until the activity ledger replaces it).

**New in §6.2, with no old-table predecessor:** `edge_evidence` (relation identity split from provenance, §6.2), `node_body` + `body_fts` (body text off the hot table, §6.2), `usage_overlay` (behavioural priors, §11.4), `cartographer_run` (Cartographer provenance, §7.5).

### 13.2 Unified phase table (ruling `03 R-4`, merging `01 §5.2` and `02 §8`)

| Phase | Data model (01) | Retrieval (02) | Ships |
|---|---|---|---|
| 0 | Step 0: canonical symbol key (lossless parser, §5.1), accept both forms, return canonical; fix **both** caller bugs in the persisted extractor — the kind filter (§2.2) and the SCIP range decoding (bug #6, §2.8) — and attribute callers by enclosing body; drop go-build rows at ingest | fix ranking doc/code drift; token estimator calibration; **handles + `ids` map on existing tools** (no `n:` ids — they need `node`); extend `internal/eval` with the six fixture types; capture the legacy baseline | stable symbol ids across reindex; persisted call graph with callers; measurable baseline |
| 1 | Steps 1–2: `repo_state` (incl. `published`), `source`, `node`, `node_alias`, `node_fts` + triggers, `node_body`/`body_fts` + triggers, `edge_type`, `edge`, `edge_evidence`, `occurrence`; connection-init PRAGMAs and the writer/reader pool split (§7.7); dual-write, switch readers | `n:<epoch>.<id>` ids; A1/A2/A4/A12/A13; `ckbFind`; `ckbNavigate` (up/down/out/in); **pack v1 is `understand` + `locate` only**; evidence weights → envelope Factors | search-plus-context in one call; call graph as index lookups |
| 2 | Steps 3–4: `fact_key`, `fact`, `internal/derive`, containers (package + directory, §4.1), actors, `owns`, `depends_on`, `node_change` | A5, A6, A7, A8; `module-map`; `direction:"time"`; prepare-change and assess-change intents on the store; staleness labelling | the flagship cross-tier join as one SELECT; change safety on the store |
| 3 | Step 5: Cartographer freeze, `cartographer_run` | usage rollup + priors (default off); priors-on vs priors-off eval | `reviewPR` reads instead of rescanning; measured learning loop |
| 4 | Step 6: docs, ADRs, notes, LIP, telemetry as overlays; v13 drops superseded tables | tool surface consolidation; core preset 10–14; aliases for old names; retire `exportForLLM`'s parser | one store, one surface |

**Phase 0 has no schema change** and splits into two independent halves that can both start immediately (`03 R-4`).

`repo_state` and `source` ship in Phase 1, not Phase 2: `node.first_state`/`last_state` and `edge_evidence.source_id`/`first_state`/`last_state` are `NOT NULL REFERENCES` those tables (§6.2), so Phase 1 cannot create its own tables without them. They are two small catalogs, and making their FKs nullable for one phase would mean a migration and a nullability bug surface for no gain.

**Ruling — no phase promises something a later phase builds.** v0.3's table had three of these. Phase 0 promised `n:<node.id>` ids while stating it makes no schema change — but `node` is Phase 1, so there is no integer to name; Phase 0 therefore ships the *handle* and the `ids` map on the existing tools, which is where most of the token saving lives anyway (§9.6) and which needs no store at all. Phase 1 promised A6 (`overlays(nodeIds[], kinds[], asOf)`) and the `module-map` pack, but `fact`/`fact_key` and container nodes are Phase 2 — both move to Phase 2, and Phase 1's pack v1 is `understand` + `locate`, the two intents that need only nodes and edges. Each phase's acceptance criteria are narrowed to match: Phase 1 is accepted on reader parity for `findReferences` (`references` ∪ `refers` vs today's `RefIndex`, §6.3) and `getCallGraph`, not on pack quality, which has no fact plane to be good yet.

### 13.3 Three most valuable first materializations (recap, `01 §5.3`)

1. Symbol-level `calls` edges with a real caller.
2. The containment spine denormalized onto every node, plus `owns` edges.
3. The fact plane for complexity/churn/hotspot/risk, computed once with provenance and stored factor breakdown.

---

## 14. Risks and open questions

Merged from `01 §7` and `02 §7`; each item marked **Resolved** (with the `03` ruling) or **Open** (with an owner).

### 14.1 From `01 §7`

| # | Risk / question | Status |
|---|---|---|
| 1 | Canonical key for non-Go indexers (scip-typescript semver, scip-java maven coordinates, python/ruby unknown) | **Open** (owner: data model). `03 R-3` explicitly rules it stays open and sets it as Phase 1's first spike, with the per-indexer policy table in §5.6 as the starting point. **Extended by F11/F17**: locality is read from the indexer's own local-module set, never from version equality, and the fixture set is six per language — plain, `_test` package, generic + instantiation, `go.work`/`GOWORK`, `replace`-to-local, `vendor/` (§5.6). |
| 2 | Symbol end lines are inferred; investigate SCIP `enclosing_range` or tree-sitter via Cartographer | **Partly answered** (owner: data model). Bug #6 (§2.8) shows that today's end lines are not merely inferred but *wrong* for three-element ranges, and Phase 0 fixes the decoding and switches enclosing-body attribution to SCIP `enclosing_range` where the indexer emits it, falling back to the next-definition heuristic where it does not. What stays open is the fallback: scip-go does not emit `enclosing_range` for every symbol, so some end lines remain inferred. |
| 3 | Local symbols (`local N`) skipped today (`extractor.go:194`); key format should reserve room for file-scoped keys | **Open** (owner: data model). Not addressed by `03`. |
| 4 | Cartographer module identity (per file, directory, or manifest) unverified; check against a real `MapProject` run before Step 5 | **Open** (owner: data model). Not addressed by `03`. |
| 5 | Distinct symbol-level call pairs unmeasured until the caller bug is fixed; re-measure before committing to edge/occurrence sizing | **Open** (owner: data model). Not addressed by `03`. |
| 6 | Concurrency between `ckb index` and `ckb mcp` | **Resolved** — `03 R-5` as corrected by F1/F2 (§7.7): a full rebuild is a *generation handoff* (checkpoint → generation marker → rename → readers detect the epoch change and reopen), not a bare rename over an open database; an incremental run publishes an **unpublished `repo_state`** that readers ignore until its last transaction sets `published = 1`, so batching never exposes a partial generation. Also resolved here: per-connection PRAGMAs (`foreign_keys`, `busy_timeout`) through a connection-init hook, one writer connection and N readers, and one pinned read transaction per pack. |
| 7 | WAL growth (74 MB against 48 MB today); explicit checkpoint policy needed | **Resolved** — `03 R-5`: WAL checkpoint after every incremental batch group, and `wal_checkpoint(TRUNCATE)` before a full rebuild's handoff (§7.7). |
| 8 | Multi-repo: shared DB vs federation overlay is a product decision | **Resolved** — `03 R-5`: one database per repo stays; cross-repo relations stay in federation as lists; tier 0 exists so a future shared store needs no key change. |
| 9 | LLM notes: file format and path unspecified; projection direction file → DB is fixed | **Resolved** — `03 R-3`: `.ckb/notes/<node handle-slug>.md` with YAML front matter; projected into a tier-4 node + `documents` edge on index; out of scope for the first three phases. |
| 10 | Confidence composition from row to response: not defined anywhere in code | **Resolved** — `03 R-3`, pointing at `02 E4` (pack confidence formula, §10.5). |
| 11 | Fact retention for temporal keys: pruning policy is a product choice | **Resolved** — `03 R-5`: temporal facts keep every state for 30 days, then one row per ISO week for one year, then one per month, pruned by the index run; non-temporal facts keep only the latest per `(node, key, source)`. Per F5 this is the **only** process that deletes a fact — invalidation does not (§7.2), and §7.3's four-step read order is written against what retention leaves behind. |

### 14.2 From `02 §7`

| # | Risk / question | Status |
|---|---|---|
| 1 | Confidently stale: prepare/assess must refuse a verdict above medium confidence when the index is behind and changed files are dirty | **Resolved** — stated as a decided response rule in `02 §7.1` itself; carried into §10.4/§10.5 of this spec (freshness always shown; `Tier` = minimum over load-bearing sections). |
| 2 | Self-confirming ranking from the learning loop | **Resolved** — `02 L2` caps usage weight at ≤0.10 and requires scores within 5%; `02 L5` keeps eval priors-off forever (§11.5, §12.3); F12 keeps `w_usage` out of `conf` entirely, so a prior can reorder a pack but never raise its reported confidence (§10.2, §10.5). The "a session's own calls never feed its own priors" clause is **withdrawn** as unenforceable after aggregation (§11.2); the caps plus the priors-on/priors-off measurement in Phase 3 carry the risk instead. |
| 3 | `ckbContext` becomes the new "everything" tool | **Resolved** — hard 6k cap (§8.4), `BudgetOverrunPct` metric (§12.2), `ckbFind` deliberately kept cheap and body-free (§9.1). |
| 4 | Identity churn: SCIP symbols embed a commit hash; alias chain never exercised; `symbol_mappings` empty | **Resolved** — `03 R-2`: canonical symbol key (§5.1) strips the local version; `node.id` is immortal across reindex; renames mutate in place with `node_alias` (§5.3). |
| 5 | Latency: the store only helps if A4/A6 are genuinely batched | **Open** (owner: retrieval). Implementation-diligence risk, not resolved by any ruling. |
| 6 | Cartographer is build-tag gated; LIP degrades silently — packs must show labelled absence, not a missing section | **Resolved** — stated as a decided response rule in `02 §7.6`; enforced by response-shape rule 5/6 (§9.7) and the freshness/absence labelling in §10.4. |
| 7 | Ranking doc/code drift (`02 §0.2`) must be fixed first | **Resolved** — `03 R-4` Phase 0 includes "fix ranking doc/code drift" explicitly (§13.2, §8.6). |
| 8 | Token estimation is bytes/4; identifiers tokenize worse; needs calibration against a real tokenizer offline | **Resolved** as planned work — `03 R-4` Phase 0 includes "token estimator calibration." Calibration itself has not been run; tracked as Phase 0 work, not a design gap. |
| 9 | `exportForLLM` has its own regex symbol parser (`internal/export/exporter.go:279-385`); re-point or retire | **Resolved** — `03 R-4` Phase 4: "retire exportForLLM's parser." |
| 10 | Behavioural data is developer data: local-only, opt-out, excluded from export and federation, aggregate-only; must be documented | **Resolved** as policy — `02 L1`–`L3` (§11.1–§11.3); documentation itself is a product action item tracked at ship time, not a design question. |

### 14.3 Open questions answered by `03 R-2` (originally listed at the end of `02 §7`)

| Question | Answer |
|---|---|
| Does the canonical node id survive a reindex at a new commit? | Yes — canonical symbol key strips the local package version (§5.1); `node.id` is immortal; renames mutate in place with `node_alias` (§5.3). Usage priors and pack caches are viable. |
| Node-level change log keyed by repo state? | `first_state`/`last_state` on `node`/`edge_evidence`/`fact` is not enough for "signature changed at state S" — on `node` they are an existence interval and nothing more (§5.3). `node_change` table added (§6.2), storing `old_value`/`new_value` so historical attributes are reconstructed by replay; this is access path A8. |
| Overlays versioned or latest-wins? | Versioned per `fact_key.temporal`. Temporal keys keep history subject to the retention policy (§14.1 item 11); non-temporal keys keep only the latest state per `(node, key, source)`. `supersededCount` (§10.4) is derived from the count of older rows. |
| Are call-site nodes first-class? | No (§4.5). "Which of these 30 callers matter" uses `edge.weight` plus the caller's own `closure.summary` fact (A5, §6.4 — the summary counts are stored, the closure is not, §7.1); call-site lists come from `occurrence` on demand. |
| Who writes `usage_overlay` under multi-process WAL? | The rollup runs inside a `ckb mcp` process holding a lease row in `index_meta` (`usage_rollup_lease`, expiry 2h); short transactions, batches ≤500 rows; `ckb daemon` takes the lease when present; the request path never writes usage rows (§11.4). |
| Per-consumer budget defaults via `clientInfo`? | Yes — a small table keyed by `clientInfo.name` gives default pack budgets; unknown clients get the 2,000 default. Stored in config, not in the database. |
| Are the discarded intermediates kept? | `risk.factors` stored as a JSON fact (§6.4). Telemetry match quality stored as `fact.confidence` plus a `telemetry.match_quality` text fact (§6.4). The depth-4 impact graph is not stored — recomputed from `calls` edges, measured cheap once the caller bug is fixed (§13.3). Hotspot `projection30d` becomes a derived temporal fact (§6.4). |

### 14.4 Contradictions between 01 and 02 that 03 did not settle

None found. `03 R-1` settles the wire-format conflict (01's ids-only vs 02's short-ids-with-map); `03 R-2` and `R-3` answer every open question each document posed to the other; `03 R-4` merges the two independently-numbered phase/step sequences into one table without any reordering conflict; `03 R-5` rules on the items neither document could decide alone (retention, multi-repo, concurrency, naming, spec language). The one item `03` leaves deliberately open — canonical keys for non-Go indexers — is a ruling to leave it open (with an assigned Phase-1 spike), not an unresolved contradiction between the two inputs.

---

## 15. Appendices

### Appendix A: Glossary

| Term | Definition |
|---|---|
| **tier** | One of the six node kinds: repo, container, file, symbol, doc, actor (§4.1). |
| **spine** | Tiers 0–3 (repo, container, file, symbol), connected by a single `parent_id` tree with one canonical key per node. |
| **node** | A row representing one entity at any tier; carries the canonical key, containment pointers, and validity window. |
| **edge** | A distinct typed relation `(src_id, type, dst_id)` between two nodes. Carries no provenance of its own; `weight` is a denormalized `MAX` over its evidence (§6.2). |
| **edge_evidence** | One row per `(edge, source)`: confidence, first/last observed state, owning file, and that source's weight. Two sources can assert one relation and keep separate evidence (§6.2). |
| **occurrence** | A location backing one source's evidence for an edge: `(edge_id, source_id, file_id, line, col, role)`. Not a node tier. |
| **fact** | A narrow EAV row `(node_id, key, value, source, state, confidence)` for open-ended overlay data. |
| **source** | Catalog of what produced a row (scip-go, tree-sitter, git, cartographer@version, codeowners, lip, telemetry, llm:<model>, human, ckb-derived). |
| **repo_state** | One observed state of the repository: a commit, dirty flag, timestamp, indexer, and a `published` flag. |
| **published state** | A `repo_state` whose index run committed successfully. Readers resolve `asOf` only over published states; an unpublished one is a run in flight or a crashed run's garbage (§7.7). |
| **generation** | One complete database file produced by a full rebuild, identified by its `db_epoch` and handed off by the protocol in §7.7 rather than by a bare rename. |
| **cube cell** | The answer to "what does relation R (or fact K) say about node N, as of state S, according to source P" — the level × relation × state intersection (§4.3). |
| **canonical symbol key** | A SCIP symbol string with the local package's version field normalized to `.` so identity survives reindex (§5.1). |
| **nkey** | The canonical natural key for a node; legible, survives a DB rebuild. |
| **handle** | A short, human-legible, non-unique label for a node, distinct from its id (§5.5). |
| **node_alias** | Table mapping old natural keys to the current `node_id` after a rename/move/merge/split (§5.3). |
| **tombstone** | `node.state='deleted'` with `last_state` frozen, instead of a row delete (§5.4). |
| **db_epoch** | Counter under the `index_meta` key `db_epoch`, incremented on every full rebuild that re-mints integer ids (§5.7). |
| **fact_key** | Catalog row declaring a fact's value type, whether it is temporal, whether it carries forward, its unit, and its derivation inputs (§6.4). |
| **derive-once registry** | `internal/derive`: one function per derived fact key, topologically evaluated once per index run, enforced by a test (§7.4). |
| **cartographer_run** | Provenance row for a batch of Cartographer-sourced node/edge/fact rows (§7.5). |
| **origin** | Tag on a pack item: `structural`, `semantic-confirmed`, or `semantic-only` (§8.3). |
| **leads[]** | Quarantine list for semantic-only pack results; capped at 5% of budget, never counted toward confidence (§8.3). |
| **freshness** | A [0,1] function of how far a fact's state is behind the query's state, or whether the source file is dirty (§10.4). |
| **conf** | How much a fact is believed: `w_evidence × freshness × row_confidence`. The only input to reported confidence and to verdicts (§10.2, §10.5). |
| **score** | How much *this pack* wants a fact: `conf × w_intent × (1 − redundancy)`, plus a capped usage prior. Ordering and inclusion only; never reported as confidence (§10.2). |
| **carry-forward** | A `fact_key` flag saying a row may be served at a later state it was not computed at, without a staleness label (§6.4, §7.3 step 2). |
| **envelope** | The response metadata block (`Confidence{Score,Tier,Reasons,Factors}`, freshness, `suggestedNextCalls`) attached to every MCP response (`internal/envelope`). |
| **PPR** | Personalized PageRank, used for graph-proximity re-ranking (`internal/graph/ppr.go`). |
| **LIP** | Liz Indexing Protocol — best-effort local daemon providing embedding nearest-neighbour queries; degrades silently when absent (`00 §6`). |
| **Cartographer** | Rust, tree-sitter + git based structural analysis engine, linked via cgo static lib, 13 languages, no type resolution (`00 §5`). |
| **SCIP** | Sourcegraph Code Intelligence Protocol — the index format CKB's enhanced-tier backends consume. |

### Appendix B: Access-path requirements A1–A13 with budgets (`02 §0.3`)

"Cheap" = one indexed query, no per-node loop, no string matching. Budgets are p95 for this repo's scale (836 files, 19k symbols, 48k call edges).

| # | Access path | Signature | Why | Budget |
|---|---|---|---|---|
| A1 | resolve any handle → node id | `resolve(handle) → (nodeId, kind, confidence, alternatives[])`; accepts bare name, qualified name, file path, `path:line`, raw SCIP string, prior alias, short id | agents paste back whatever we printed; `Understand` re-runs full `SearchSymbols` to resolve today (`compound.go:1083-1137`) | <2 ms |
| A2 | ancestors in one read | `ancestors(nodeId) → [file, package container, directory containers…, repo]` — the full `parent_id` chain, innermost first; "module" in the packs means the package container (§4.1) | every pack attaches module context to every hit | <1 ms |
| A3 | top-K children by rank | `children(nodeId, kind, k, orderBy=importance)`; **importance is the `rank.importance` fact** (§6.4), read as a children scan via `idx_node_container(container_id, tier)` joined to the `fact` PK — not computed here. `idx_fact_key` is global, so the <3 ms holds for containers up to a few hundred symbols; a covering `(container_id, key, num_value DESC)` index is deferred until a container over that size is measured | module packs need "the 10 symbols that matter" | <3 ms (estimate) |
| A4 | typed edge scan both directions | `edges(nodeId, kinds[], dir, limit, orderBy=weight)`; one scan per requested type on `idx_edge_out`/`idx_edge_in`, merged by weight in Go (§6.7) | retires per-call `BuildFromSCIP` and in-memory `CallerIndex` | <3 ms, k≤200 |
| A5 | closure summary overlay | `closureSummary(nodeId) → {transitiveCallers, moduleSpread, maxDepth, fileCount}`; **one `closure.summary` json fact** (§6.4) for symbols above the in-degree threshold, computed on the spot below it (§7.1) | `AnalyzeImpact` walks depth-4 then truncates; ranking needs counts | <1 ms |
| A6 | batch overlay fetch | `overlays(nodeIds[], kinds[], asOf)` over `fact`'s PK prefix `(node_id, key, state_id)`, applying §7.3's four-step read order | 50–500 nodes × 6 kinds per pack | <10 ms |
| A7 | reverse overlay lookup | `overlayTargets(kind, value) → nodeIds[]`, **num and text values only**: `idx_fact_key(key, num_value DESC)` and `idx_fact_text(key, text_value)` (§6.7) | ADR → nodes, owner → nodes | <3 ms |
| A8 | node-level time travel | `changedSince(nodeId, sinceState)`, `changedNodes(stateA, stateB)` | the time axis; git diff is file-granular | <10 ms |
| A9 | FTS returning node ids | `fts(text, tierFilter, k) → [(nodeId, bm25)]` over `node_fts` (name/signature/doc comment) and `body_fts` (doc chunks, module responsibility); both rowids are node ids, both kept in sync by triggers, and the query joins `node` to filter `state = 'active'` (§6.2) | candidate selection must not return strings to re-match | <5 ms |
| A10 | persisted PPR-ready edge list + top-N neighbours | `graphEdges(limit)` = one ordered scan of `idx_edge_out` for the graph edge types, `LIMIT :limit`; `neighborsTopN` = A4 with `k = n` | load once per process or skip graph for small fanouts | load <200 ms (asserted; measured in Phase 1) |
| A11 | usage overlay batch read/write | as A6, namespace `usage:*`, written by rollup job; pair kinds read through `idx_usage_pair(node_b, …)` as well as `idx_usage_kind` (§6.7) | §11 | <5 ms |
| A12 | snapshot-consistent read | every path accepts `asOf=repoStateId`, resolves it over **published** states only, runs inside the pack's single pinned read transaction (§7.7), and reports the state served | packs must not mix states silently | — |
| A13 | cheap negative answers | `exists(nodeId)`, `hasEdges(nodeId, kind)` | `symbolExists`, negative fixtures | <1 ms |

A1, A4, A6, A12 are **load-bearing**. A5, A8, A10, A11 are optimizations.

### Appendix C: A1 / A4 / A6 / A12 mapped to the schema

| Access path | Schema mapping |
|---|---|
| **A1** `resolve(handle)` | **Epoch check first**: an `n:<epoch>.<id>` whose epoch ≠ `index_meta('db_epoch')` (§5.7) is rejected with `unknown-id` and never dereferenced; the response carries the handle resolution so the caller recovers in one call (§5.5). Primary: `SELECT id, tier FROM node WHERE tier=? AND nkey=?` against `UNIQUE(tier, nkey)` (§6.2). Fallback: `SELECT node_id FROM node_alias WHERE tier=? AND old_nkey=?` using `idx_alias_node`/the `node_alias` primary key (§5.3). `path:line` handles resolve via `idx_node_path(path, tier)` plus `start_line`/`end_line` range check. |
| **A4** `edges(nodeId, kinds[], dir, limit, orderBy=weight)` | Outbound: `idx_edge_out(src_id, type, weight DESC, dst_id)`. Inbound: `idx_edge_in(dst_id, type, weight DESC, src_id)`. `weight` precedes the opposite endpoint, so an equality seek on the first two columns is followed by a descending-weight scan the `LIMIT` stops early — with the endpoint last the scan would be ordered by endpoint id and `LIMIT` would need a sort (§6.7). Several requested types = one scan per type, merged by weight in Go. Source-filtered variants (`calls` vs `observed-calling`, §9.2) add `JOIN edge_evidence ev ON ev.edge_id = e.id JOIN source sr ON sr.source_id = ev.source_id` and predicate on `sr.kind`; `edge` itself has no `source_id` (§6.2). |
| **A6** `overlays(nodeIds[], kinds[], asOf)` | `fact` table's primary key `(node_id, key, state_id, source_id)` has `node_id` as the leading column, so a batch `WHERE node_id IN (...) AND key IN (...) AND state_id = :asOf` is a set of PK range scans; `idx_fact_key(key, num_value DESC)` supports the complementary "top-N by metric" direction and `idx_fact_text(key, text_value)` the A7 reverse direction (§6.7). Misses follow §7.3's order and nothing else: **exact state → carry-forward (`fact_key.carry_forward = 1`, unlabelled) → stale fallback (newest `state_id <= :asOf`, labelled, `Freshness.StaleReason` set) → labelled absence**. Invalidation never removes a fact (§7.2), so the fallback row is present whenever retention has not pruned it. |
| **A12** `asOf=repoStateId` on every path | `repo_state(state_id, state_key, head_commit, dirty, computed_at, indexer, published)` is the anchor, and **only `published = 1` states are resolvable** — an index run in flight is invisible until its final transaction publishes (§7.7, `idx_state_published`). The whole pack runs in one pinned read transaction, so every section sees the same state. **Nodes**: `... AND first_state <= :asOf AND last_state >= :asOf` is an **existence** predicate — "this node existed at `asOf`" — and *not* a projection of its attributes; a `node` row always holds the latest observed name, signature and parent, and historical attributes come from replaying `node_change` (A8, §5.3). **Facts**: §7.3's four-step order, as in A6 above. **Edges are HEAD-only** (§7.2): evidence is hard-deleted on invalidation, so an `asOf` earlier than HEAD returns HEAD's edges labelled with the state actually served, not a reconstruction. The envelope reports the state actually served on every response. |

---

*End of specification.*
