# Knowledge Store — Data model decisions (input document)

Status: architect decision document, 2026-09-17. Input to the design spec; not the spec itself.
Scope: tiers, identity, SQLite schema, ingestion, invalidation, migration.
Companion: 02-retrieval-decisions.md. Evidence: 00-survey-current-state.md, re-verified below.

## 0. Re-verification and new findings

Confirmed: `caller_id` NULL in 48,326/48,326 callgraph rows; scip-go symbols embed the indexed commit (`.ckb/index-meta.json` commitHash `1ce495bd…`, every `file_symbols.symbol_id` carries `1ce495bdb45a` as the SCIP package-version field); `symbol_mappings`, `symbol_aliases`, `modules`, `dependency_edges` empty; `lip_annotations` absent; 2,683 go-build noise rows.

Correction: module id format is defined, `ckb:mod:<hex(sha256(normalizedPath)[:8])>` (`internal/modules/detection.go:362-374`). The table is empty because nothing calls the detector in the index path.

New, load-bearing:

1. **Root cause of NULL caller_id.** `resolveCallerSymbol` (`internal/incremental/extractor.go:627-631`) filters candidates on `sym.Kind == "function" || "method"`. `mapSymbolKind` (`extractor.go:216-223`) returns `"unknown"` because scip-go does not set `SymbolInformation.Kind` (stated at `extractor.go:471` and `internal/backends/scip/callgraph.go:256`). The callee side has the descriptor fallback `isFunctionSymbol` (`extractor.go:450`); the caller side does not. The in-memory `buildCallerIndex` (`callgraph.go:245-289`) uses the fallback and therefore works. ~5-line fix; highest-leverage change in the store.
2. **Two writers, incompatible JSON in one column.** `internal/api/index_processor.go:690-692` writes `{"file_path","line","col"}` into `symbol_mappings.location_json`; `internal/identity/repository.go:114` marshals `identity.Location` with tags `path`/`line`/`column` (`identity/id.go:39-45`). The delete at `index_processor.go:216` uses `json_extract('$.file_path')` and cannot match the other writer's rows. Argument for typed columns on the spine.
3. **Backend-anchoring policy is dead code.** `identity.GetBackendIdRole` (`internal/identity/anchors.go:34`) tests prefix `"scip:"`; real symbols start with `"scip-go "`. Every real symbol is classified resolver-only, the opposite of the documented intent (`anchors.go:19-21`).
4. **Ownership resolution does no matching.** `OwnershipRepository.GetMatchingPattern` (`internal/storage/repositories.go:854-874`) returns every row; the caller globs in Go.
5. **Symbol end lines are inferred.** `extractor.go:207` takes `EndLine` from the definition occurrence's identifier range; `buildFunctionRanges` (`callgraph.go:280-285`) substitutes "next function start − 1, else start + DefaultMaxFunctionLines". Any stored `end_line` is a heuristic and must be labelled.
6. **Measurements (read-only on the live DB):** avg `length(file_symbols.symbol_id)` 126 bytes (max 164); distinct `(caller_file, callee_id)` pairs 17,208 of 48,326 (2.8× duplication); distinct callee_id 6,809; 4-hop recursive CTE over file_deps (3,802 edges) ~20–40 ms; 2-hop symbol walk via today's file-granular caller join 0.27 s with fan-out 13 → 1,249 → 4,533. The 126-byte key, stored in row plus 2–3 indexes across three tables, is why the DB is 48 MB + 74 MB WAL for 19k symbols.

## 1. Tier taxonomy

**1.1 Six node kinds, four of them a containment spine.**

| Tier | id | Canonical key | This repo → 10× |
|---|---|---|---|
| repo | 0 | repo slug | 1 |
| container | 1 | normalized repo-relative root path; `kind ∈ {repo-root, component, package, directory}`, self-nesting via `parent_id` | ~150 → 1,500 |
| file | 2 | normalized repo-relative path | 836 → 8,400 |
| symbol | 3 | canonical SCIP key (§2) | 19k → 190k |
| doc | 4 | repo-relative path (+`#anchor`) | ~200 → 2,000 |
| actor | 5 | `email:…` / `gh:…` / `team:@org/x` | ~10 → 200 |

Tiers 0–3 form the spine: exactly one `parent_id`, one canonical key, containment is a tree. Tiers 4–5 hang off it via typed edges.

**1.2 Directory is not a tier.** It is a container kind. One self-nesting container hierarchy instead of two competing ones; "module of X" and "directory of X" are the same walk at different depths.

**1.3 Occurrence is not a node tier; it is a located edge instance.** `edge` holds the distinct relation `(src, type, dst)`; `occurrence` holds its locations `(edge_id, file_id, line, col, role)`. 48,326 call-site rows collapse to 17,208 distinct file-level pairs; k-hop walks stop paying for duplicates. An occurrence remains addressable as `(edge_id, line, col)`.

**1.4 Annotation "tiers" are facts and documents, not tiers.** Fact overlay: `(node, key, value, source, state, confidence)` for churn, hotspot, complexity, risk, coverage, telemetry, Cartographer metrics, LIP confidence, LLM summaries. Document nodes (tier 4): ADRs, design docs, README sections, LLM notes-as-artifacts, because they have text, join FTS, and are edge endpoints. Ownership splits: the CODEOWNERS rule is a fact on the repo; the resolved owner is an `owns` edge from actor to file/container. Materializing the resolution removes the full scan.

**1.5 "3D" means level × relation × state.**
- Rejected entity × abstraction-level × time: level is a property of the node, not an independent coordinate; and no axis for relation type, which is what the owner asked for.
- Rejected entity × tier × evidence-source: same degeneracy, and drops time.
- **Level** (vertical): repo ⊃ container ⊃ file ⊃ symbol via `parent_id` and denormalized `file_id`/`container_id`. Never string-matched.
- **Relation** (horizontal): typed edges, intra-level (calls, imports, depends_on, cochanges) and cross-level (defines, documents, owns, tests, decides).
- **State** (depth / evidence): every edge and fact carries `(source_id, state_id, observed_at, confidence)`.
The cell: what does relation R (or fact K) say about node N, as of state S, according to source P. All three coordinates are indexed. Provenance is part of the primary key, which is what makes "evidence first" structural.

## 2. Identity

**2.1 Integer surrogate node id, one canonical natural key per tier.** `node(id INTEGER PRIMARY KEY, tier, nkey, …) UNIQUE(tier, nkey)`. Only `id` appears in edges, facts, occurrences, FTS rowids. `nkey` is legible and survives a DB rebuild. Integer keys: a call edge goes from ~126+35 bytes ×(row + 3 indexes) to ~20 bytes; at 10× roughly 30 MB instead of 240 MB for the call graph alone.

**2.2 Canonical symbol key = SCIP symbol with the local package version normalized away.**
```go
// internal/identity/canonical.go
func CanonicalSymbolKey(scipSymbol string, localPkg string) string
```
Parse with `scip.ParseSCIPIdentifier` (`internal/backends/scip/ids.go:37-67`); if the package is the repo's own module (or version equals short HEAD), replace field 4 with `.` (SCIP's unspecified-version placeholder); external packages keep their version.
```
live:      scip-go gomod github.com/SimplyLiz/CodeMCP 1ce495bdb45a `…/internal/a2a`/TaskStatusUpdateEvent#
canonical: scip-go gomod github.com/SimplyLiz/CodeMCP .            `…/internal/a2a`/TaskStatusUpdateEvent#
```
Today a reindex at a new commit changes the identity of every symbol, which is why `PopulateFromFullIndexStreaming` must delete and rewrite everything (`updater.go:405-413`). With commit-free keys an unchanged symbol keeps its node id and every attached fact survives. The raw string is kept as `node.scip_raw` for calling back into the SCIP index, never as a key.

**2.3 Replace `identity/fingerprint.go` as the ID basis; keep it as a rename signal.** Rejected `ckb:<repo>:sym:<sha256(container|name|kind|arity|sig)>`: includes signature and arity so any signature change mints a new id; `ExtractArity` counts commas; parts are sorted before hashing; opaque; never ran (0 rows). Keep `ComputeStableFingerprint` narrowed to (container, name, kind) as `MatchKey` for proposing `node_alias` rows. Fix or delete `identity/anchors.go`.

**2.4 Renames: mutate the node in place, version the key.**
```sql
CREATE TABLE node_alias (
  tier INTEGER NOT NULL, old_nkey TEXT NOT NULL,
  node_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  reason TEXT NOT NULL,            -- renamed|moved|merged|split|fuzzy
  confidence REAL NOT NULL, source_id INTEGER NOT NULL, from_state INTEGER NOT NULL,
  PRIMARY KEY (tier, old_nkey, node_id)
);
CREATE INDEX idx_alias_node ON node_alias(node_id);
```
Resolution is one lookup on `node(tier,nkey)`, else one on `node_alias`. No chains, no depth limit, no cycle detection. Rejected the existing alias-chain design (`identity/resolution.go`, `AliasChainMaxDepth = 3`, cycle detection, `AliasChainTooDeep`) because a rename would mint a new id and every edge and fact would have to be rewritten. Cases: file rename (nkey/path updated + alias row from git similarity), symbol rename (MatchKey + location proximity + the file's own rename), container move (plus `parent_id` and descendants' `container_id` rewritten), merge (two old keys → one node), split (one old key → two nodes, confidence < 1, returned as ambiguous). Deletion is a tombstone: `node.state='deleted'`, `last_state` frozen.

**2.5 A node reference is an integer; edge type implies the tier pair.** No `target_id TEXT + target_type TEXT` anywhere. `src_tier`/`dst_tier` live once in `edge_type` and are a write-time validity check.

## 3. Storage substrate

**3.1 Hybrid.** Typed, integer-keyed spine (`node`, `edge`, `occurrence`) for the hot, high-volume, fixed-shape data; generic narrow EAV `fact` for the open-ended overlay. Rejected pure property graph (metric reads scan a table dominated by 1.5M edges; FTS external content wants a stable integer rowid). Rejected fully typed per-tier tables (that is the current schema: 49 tables, 5 with data, 20 of 28 extractors never persisted).

**3.2 Schema.**
```sql
-- evidence axis
CREATE TABLE repo_state (
  state_id INTEGER PRIMARY KEY, state_key TEXT NOT NULL UNIQUE,  -- repostate.RepoStateID
  head_commit TEXT NOT NULL, dirty INTEGER NOT NULL, computed_at INTEGER NOT NULL, indexer TEXT
);
CREATE TABLE source (
  source_id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,  -- 'scip-go','tree-sitter','git','cartographer@4.0.2','codeowners','lip','llm:<model>','human','ckb-derived'
  kind TEXT NOT NULL,          -- index|heuristic|vcs|declared|model|human|derived
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
  src_tier INTEGER NOT NULL, dst_tier INTEGER NOT NULL,
  directed INTEGER NOT NULL DEFAULT 1, derived INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE edge (
  id INTEGER PRIMARY KEY,
  src_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  dst_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  type INTEGER NOT NULL REFERENCES edge_type(type_id),
  weight REAL, source_id INTEGER NOT NULL REFERENCES source(source_id),
  confidence REAL NOT NULL DEFAULT 1.0,
  first_state INTEGER NOT NULL, last_state INTEGER NOT NULL,
  owner_file_id INTEGER,                       -- file whose reindex owns this edge
  UNIQUE(src_id, type, dst_id)
);
CREATE INDEX idx_edge_out ON edge(src_id, type, dst_id, weight DESC);
CREATE INDEX idx_edge_in  ON edge(dst_id, type, src_id, weight DESC);
CREATE INDEX idx_edge_own ON edge(owner_file_id);
CREATE TABLE occurrence (
  edge_id INTEGER NOT NULL REFERENCES edge(id) ON DELETE CASCADE,
  file_id INTEGER NOT NULL REFERENCES node(id) ON DELETE CASCADE,
  line INTEGER NOT NULL, col INTEGER NOT NULL, end_col INTEGER,
  role INTEGER NOT NULL,                       -- SCIP SymbolRoles bitmask
  PRIMARY KEY (edge_id, file_id, line, col)
);
CREATE INDEX idx_occ_file ON occurrence(file_id, line);
-- overlay
CREATE TABLE fact_key (
  key TEXT PRIMARY KEY, value_type TEXT NOT NULL,   -- num|text|json
  temporal INTEGER NOT NULL DEFAULT 0, unit TEXT, derived_from TEXT   -- JSON array of input keys
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
CREATE INDEX idx_fact_state ON fact(state_id);
-- search
CREATE VIRTUAL TABLE node_fts USING fts5(
  name, signature, doc_comment, body,
  content='node', content_rowid='id', tokenize='unicode61 remove_diacritics 2'
);
```
Seed edge types: `defines` (file→symbol, scip), `calls`/`references`/`implements`/`embeds`/`type_of` (symbol→symbol, scip), `imports` (file→file, scip; replaces file_deps), `depends_on` (container→container, cartographer or derived from imports), `tests` (symbol→symbol, derived), `documents` (doc→symbol|container), `decides` (doc(adr)→container|symbol), `owns` (actor→file|container, codeowners/blame), `authored` (actor→file, git), `cochanges` (file↔file, git, undirected, weight=coupling), `hidden_coupling` (file↔file, cartographer), `violates_layer`/`in_cycle` (container→container, cartographer), `near` (symbol→symbol, lip).

**3.3 No containment closure table; denormalize.** `parent_id` for the tree plus `file_id`/`container_id` on every symbol. The flagship join becomes one statement:
```sql
SELECT s.nkey, f.path, c.name, o.name AS owner, hs.num_value AS hotspot
FROM node s JOIN node f ON f.id = s.file_id JOIN node c ON c.id = s.container_id
LEFT JOIN edge e ON e.dst_id = f.id AND e.type = :owns
LEFT JOIN node o ON o.id = e.src_id
LEFT JOIN fact hs ON hs.node_id = f.id AND hs.key = 'hotspot.score' AND hs.state_id = :state
WHERE s.id = :sym;
```

**3.4 Recursive CTE for k ≤ 3; in-process adjacency only for whole-graph algorithms.** k ≤ 3 interactive expansion: recursive CTE over `edge` with per-hop LIMIT. PPR and Tarjan: in-process adjacency built from `SELECT src_id, dst_id, type, weight FROM edge` (~1.5M ints, ~40 MB, sub-second), not from mmapped SCIP documents per call. `SCIPIndex.CallerIndex` stays only as cold-start fallback. A bounded LRU of expanded neighbourhoods keyed `(node_id, type-set, depth, state_id)` replaces caching at the JSON boundary.

**3.5 FTS rowid is the node id.** `content='node', content_rowid='id'`. Today `symbols_fts_content` has its own rowid plus `id TEXT UNIQUE` (`schema.go:1330-1340`), so each hit costs a second 126-byte probe. One FTS table over symbol, doc, container tiers; subsumes `decisions_fts` and `responsibilities_fts`.

**3.6 Size at 10×:** node ~60 MB, node_fts ~40, edge (~900k pairs) ~54, occurrence (~500k) ~20, fact (~500k) ~30; total ~200 MB vs ~480 MB extrapolated for today's schema holding less. Keep the existing bulk-load pattern (`synchronous=OFF`, 128 MB cache, `wal_autocheckpoint=0`, batches of 1000, `wal_checkpoint(TRUNCATE)` at end, `updater.go:380-405`).

## 4. Ingestion and invalidation

**4.1 Materialization policy.**

| Data | When |
|---|---|
| node (all tiers), parent/file/container pointers, defines, calls, references, imports | index time, full + incremental |
| occurrence | index time |
| complexity.* (tree-sitter) | index time per changed file (four extractors redo this today) |
| churn.*, cochanges, authored | index time; refreshed when HEAD moved > N commits since last git pass |
| owns from CODEOWNERS | index time |
| owns from blame | first request per file, then persisted |
| Cartographer container nodes, depends_on, in_cycle, violates_layer, health/bridge/god-module facts | index time when built with `-tags cartographer` (§4.3) |
| hotspot.score, risk.score, risk.factors | index time, derived, `source='ckb-derived'` |
| documents, decides, doc nodes | index time if docs indexing enabled, else on IndexDocs |
| telemetry → facts + calls edges source=telemetry | on ingest command |
| LIP near edges | never eagerly; written opportunistically with expiry |
| LLM notes | never computed; explicit tool call, stored in a versioned file, projected into node/fact (plan: curated knowledge lives in versioned files, `change-intelligence-and-activity-ledger.md:218-219`) |
| impact closures, blast radius, k-hop expansions, transitive closure of calls | never (query-shaped; edge indexes make them cheap; closure is n²) |

**4.2 Invalidation unit is the file node.** Hash change → `DELETE FROM edge WHERE owner_file_id = :f` (occurrences cascade), delete file-derived facts for the file and its symbols, re-extract, upsert; symbol ids survive (commit-free keys). Vanished symbols → tombstone. Inbound edges untouched (caller-owned invariant, `incremental/types.go:53-55`). Transitive: existing file_deps BFS (`incremental/deps.go`) becomes a walk over `imports` edges; `rescan_queue` finally gets fed. State-scoped facts: valid for their `state_id`; carried forward to a new state only if the source declares it carry-forwardable; otherwise the reader falls back to latest state ≤ requested and reports the gap via `Freshness.IndexAge{CommitsBehind, StaleReason}` (`envelope.go:41-51`).

**4.3 Freezing Cartographer.**
```sql
CREATE TABLE cartographer_run (
  run_id INTEGER PRIMARY KEY, version TEXT NOT NULL, state_id INTEGER NOT NULL REFERENCES repo_state(state_id),
  started_at INTEGER NOT NULL, duration_ms INTEGER NOT NULL, functions TEXT NOT NULL   -- JSON: bridge calls covered
);
```
Outputs of MapProject, Health, CheckLayers, GitChurn, GitCochange, HiddenCoupling, ShotgunSurgery, UnreferencedSymbols become ordinary node/edge/fact rows with `source='cartographer@<ver>'` and the run's state. `reviewPR` reads instead of re-running. Cartographer `ModuleID`/`Path` resolved to node ids at ingest by path, mapping persisted, never re-derived per query. Staleness: newest run's state ≠ current → serve labelled stale with commit delta, or re-run behind `ckb index --refresh-arch`.

**4.4 Derive-once registry.** New package `internal/derive`: one function per derived key declaring `inputs []FactKey` and `output FactKey`, matching `fact_key.derived_from`; topologically sorted and evaluated once per index run. Complexity (4 impls → 4 readers), churn (3 → 1), dependency edges (5 representations → imports + derived depends_on), risk (3 formulas → one score plus stored `risk.factors`), test detection (3 → one `is_test` fact), co-change (2 → one edge type with source). Enforced by a test that fails if a package outside `internal/derive` computes a registered key.

## 5. Migration

**5.1 Disposition (49 → ~14 plus caches).** Become the model: indexed_files → node t2; file_symbols → node t3 + file_id + defines; callgraph → calls + occurrence; file_deps → imports; symbols_fts_* → node + node_fts; modules/dependency_edges → node t1 + depends_on; module_renames, symbol_mappings, symbol_aliases → node/node_alias; hotspot_snapshots, risk_scores → fact (temporal); coupling_cache → cochanges; ownership/ownership_history → node t5 + owns + fact; responsibilities → fact + node_fts; decisions → node t4 kind=adr + decides; docs/doc_references/doc_modules → node t4 + documents; observed_usage/observed_callers → fact + calls(source=telemetry); coverage_snapshots → fact; lip_annotations → fact(source=lip). Dropped: symbol_suffixes, docs_meta. Stay: schema_version, index_meta, query_cache, view_cache, negative_cache, rescan_queue, telemetry_sync_log, observed_unmatched, wide_result_metrics (until the ledger replaces it).

**5.2 Work order (each step ships alone, MCP keeps working).**
- Step 0: `CanonicalSymbolKey`; normalize on write in the extractor; accept raw and canonical on read at the MCP boundary; return canonical. No schema change.
- Step 1: node + node_alias + node_fts, dual-written; switch searchSymbols/getSymbol to node_fts.
- Step 2: edge + occurrence; fix the caller bug; drop go-build rows at ingest; switch getCallGraph/findReferences/analyzeImpact/traceUsage; demote buildCallerIndex to cold-start fallback. (materialization #1)
- Step 3: source + repo_state + fact + internal/derive; complexity/churn/hotspot/risk to index time. (materialization #3)
- Step 4: containers, actors, container_id/file_id denormalization, owns, depends_on. (materialization #2)
- Step 5: Cartographer freeze.
- Step 6: docs/ADRs/notes/LIP/telemetry as overlays; one v13 migration drops superseded tables.

**5.3 Three most valuable first materializations:** (1) symbol-level `calls` edges with a real caller; (2) the containment spine denormalized onto every node plus `owns` edges; (3) the fact plane for complexity/churn/hotspot/risk computed once with provenance and stored factor breakdown.

## 6. What the model makes cheap for retrieval

Seeds → neighbourhood in one statement (`idx_edge_out`/`_in`); FTS hit is already a node id; top-N by any metric without a scan (`idx_fact_key`); "everything about X" is four index ranges on one integer; ranking inputs are columns; every returned row names source/state/confidence; ids quotable across calls and reindex; truncation ordered by `edge.weight`; cross-state diff is a range scan on `first_state`/`last_state`; `idx_occ_file(file_id, line)` answers "what is at this line".

## 7. Risks and open questions

1. Canonical key for non-Go indexers (scip-typescript semver, scip-java maven coordinates, python/ruby unknown): needs a per-indexer policy table and one fixture per language. Highest-risk item.
2. Symbol end lines are inferred; investigate SCIP `enclosing_range` or tree-sitter via Cartographer.
3. Local symbols (`local N`) skipped today (`extractor.go:194`); key format should reserve room for file-scoped keys.
4. Cartographer module identity (per file, directory, or manifest) unverified; check against a real MapProject run before Step 5.
5. Distinct symbol-level call pairs unmeasured until the caller bug is fixed; re-measure before committing to edge/occurrence sizing.
6. Concurrency between `ckb index` and `ckb mcp`: `busy_timeout=5000` (`storage/db.go:46`) against a bulk load with `synchronous=OFF` and `wal_autocheckpoint=0`; shadow DB and swap is a candidate.
7. WAL growth (74 MB against 48 MB today); explicit checkpoint policy needed.
8. Multi-repo: shared DB vs federation overlay is a product decision.
9. LLM notes: file format and path unspecified; projection direction file → DB is fixed.
10. Confidence composition from row to response: not defined anywhere in code (answered in 02-retrieval-decisions.md E4).
11. Fact retention for temporal keys: pruning policy is a product choice.
