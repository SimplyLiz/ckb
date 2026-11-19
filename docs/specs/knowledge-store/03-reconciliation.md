# Knowledge Store — Reconciliation of the two decision documents

Status: orchestrator notes, 2026-09-17. Resolves the conflicts and open questions between
01-data-model-decisions.md and 02-retrieval-decisions.md. The spec must follow these rulings.

## R-1 What crosses the MCP boundary (conflict: 01 §2.1 vs 02 N3)

Ruling: the wire format is **`id` + `handle`**, canonical natural key on request.

- `id` = `"n:<node.id in base36>"`. Stable for the life of the database. Cheap to echo, cheap to resolve.
- `handle` = a short, human-legible form derived from the canonical key: for symbols `<container short name>.<Name>` (e.g. `query.Engine.FindReferences`), for files the repo-relative path, for containers the container path, for docs the path+anchor, for actors the display handle. Handles are not unique; they are for reading, not for resolving.
- `nkey` (full canonical key, e.g. the commit-free SCIP string) is returned only under `include:["rawIds"]`, in a single `ids` map at the end of the response.
- `resolve()` (02 A1) accepts all of: `n:…` ids, canonical keys, raw SCIP strings (commit-bearing), handles, bare names, `path:line`, and any `old_nkey` from `node_alias`. A stale `n:` id after a database rebuild returns `unknown-id` together with the resolution by handle, so agents recover in one call.
- Database rebuild (schema migration, `ckb index --full --rebuild`) re-mints integer ids. That is acceptable because it is rare and every response carries the handle. The database stores a `db_epoch` in `index_meta`; the envelope reports it so a client can detect a rebuild.

Why not the full key on the wire: 01 measured 126 bytes average per key, 02 measured 40–60 tokens per key. A 50-row answer would spend more tokens on identifiers than on facts. Why not integers only: evidence-first. The handle gives the agent something it can sanity-check; the `ids` map gives it the exact key when it needs one.

## R-2 Answers to 02 §7.2 open questions from 01

| Question | Answer |
|---|---|
| Does the canonical node id survive a reindex at a new commit? | Yes. Canonical symbol key strips the local package version (01 §2.2); `node.id` is immortal; renames mutate in place with `node_alias` (01 §2.4). Usage priors and pack caches are viable. |
| Node-level change log keyed by repo state? | Partially in 01 (`first_state`/`last_state` on node, edge, fact). Not enough for "signature changed at state S". **Add** a `node_change(node_id, state_id, kind, old_value, new_value, source_id)` table with `kind ∈ {created, deleted, renamed, moved, signature, doc_comment, body_hash}`; written by the index run when it upserts a node whose tracked columns differ. Indexed `(node_id, state_id)` and `(state_id)`. This is access path A8. |
| Overlays versioned or latest-wins? | Versioned per `fact_key.temporal`. Temporal keys keep history subject to a pruning policy (R-5); non-temporal keys keep only the latest state per (node, key, source). `supersededCount` in 02 E3 is derived from the count of older rows. |
| Are call-site nodes first-class? | No (01 §1.3). Occurrences are located edge instances. "Which of these 30 callers matter" uses `edge.weight` = number of occurrences plus the caller's own closure summary (A5); call-site lists come from `occurrence` on demand. |
| Who writes `usage_overlay` under multi-process WAL? | The rollup runs inside a `ckb mcp` process that holds a lease row in `index_meta` (`usage_rollup_lease`, expiry 2 h). Short transactions, batches of ≤500 rows. `ckb daemon` takes the lease when present. The request path never writes usage rows. |
| Per-consumer budget defaults via `clientInfo`? | Yes. A small table keyed by `clientInfo.name` gives default pack budgets; unknown clients get the 2,000 default. Stored in config, not in the database. |
| Are the discarded intermediates kept? | `risk.factors` stored as a json fact (01 §4.4). Telemetry match quality stored as `fact.confidence` plus a `telemetry.match_quality` text fact. The depth-4 impact graph is not stored; it is recomputed from `calls` edges, which 01 §3.4 measured as cheap once the caller bug is fixed. Hotspot `projection30d` becomes a derived temporal fact. |

## R-3 Answers to 01 §7 open questions from 02

| Question | Answer |
|---|---|
| Confidence composition (01 §7.10) | 02 E4: pack `Score` is the token-share-weighted mean of packed fact scores; `Tier` is the minimum over load-bearing sections; `Factors` one per source class used. Row confidence enters through `w_evidence × freshness`. |
| Canonical key for non-Go indexers (01 §7.1) | Stays open and is Phase 1's first spike: a `canonical_policy` per indexer name (scip-go: version = short commit → `.`; scip-typescript: version = package.json version of the local package → `.`; scip-java: maven coordinate version of the local module → `.`; unknown indexer: no rewrite, warn). One golden fixture per language before Step 0 ships for that language. |
| Where LLM notes live (01 §7.9) | `.ckb/notes/<node handle-slug>.md` with YAML front matter (`nodeKey`, `author`, `model`, `promptVersion`, `createdAt`, `confidence`); projected into a tier-4 node plus a `documents` edge on index. Out of scope for the first three phases; the spec only reserves the shape. |

## R-4 Unified phasing (merge 01 §5.2 and 02 §8)

| Phase | Data model (01) | Retrieval (02) | Ships |
|---|---|---|---|
| 0 | Step 0: canonical symbol key, accept both forms, return canonical; fix the caller-resolution bug in the persisted extractor; drop go-build rows at ingest | fix ranking doc/code drift; token estimator calibration; short ids + handle + `ids` map on existing tools; extend `internal/eval` with the six fixture types; capture the legacy baseline | stable symbol ids across reindex; persisted call graph with callers; measurable baseline |
| 1 | Steps 1–2: node, node_alias, node_fts, edge, occurrence, dual-write, switch readers | A1/A2/A4/A6/A12/A13; `ckbFind`; `ckbNavigate` (up/down/out/in); pack v1 with understand, locate, module-map; evidence weights → envelope Factors | search-plus-context in one call; call graph as index lookups |
| 2 | Steps 3–4: source, repo_state, fact, `internal/derive`, containers, actors, owns, depends_on, node_change | A5, A7, A8; `direction:"time"`; prepare-change and assess-change intents on the store; staleness labelling | the flagship cross-tier join as one SELECT; change safety on the store |
| 3 | Step 5: Cartographer freeze, `cartographer_run` | usage rollup + priors (default off); priors-on vs priors-off eval | reviewPR reads instead of rescanning; measured learning loop |
| 4 | Step 6: docs, ADRs, notes, LIP, telemetry as overlays; v13 drops superseded tables | tool surface consolidation; core preset 10–14; aliases for old names; retire exportForLLM's parser | one store, one surface |

Phase 0 has no schema change and two independent halves; both can start immediately.

## R-5 Rulings on product choices neither document could make

- **Fact retention**: temporal facts keep every state for 30 days, then one row per ISO week for one year, then one per month. Pruned by the index run. Non-temporal facts keep the latest per (node, key, source) only.
- **Multi-repo**: one database per repo stays. Cross-repo relations stay in federation as lists. Tier 0 exists so a future shared store needs no key change.
- **Index vs MCP concurrency**: the index run writes to `.ckb/ckb.db.next` and atomically renames on completion when the run is a full rebuild; incremental runs write in place with `busy_timeout` and batches ≤1,000 rows per transaction, never a whole-populate transaction. WAL checkpoint after every incremental batch group.
- **Naming**: the feature is called the **knowledge store** in code and docs (`internal/store`, `internal/derive`, `internal/retrieval`). "3D" is explanation, not a name: the three axes are level, relation, state.
- **Language of the spec**: English, matching every other document under docs/.
