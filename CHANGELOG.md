# Changelog

All notable changes to CKB will be documented in this file.

## [Unreleased]

### Security — command injection in the review coupling check

`ckb review` looked up when co-changed files were last modified by writing
their paths into a `sh -c` script. Go's `%q` quoting is not shell quoting, so a
path containing `$(…)`, backticks or a `"` ran as a command. Those paths come
from git history, which on a PR branch is whatever the PR author committed —
so running `ckb review` (or `reviewPR`) on an untrusted PR, e.g. in CI, could
execute code on the runner. The lookup now runs one `git log` with the paths as
argv and literal pathspecs; no shell is involved. Tab characters and pathspec
magic like `:(glob)` in file names no longer produce wrong dates either.

### Added — change intelligence and an activity ledger, no UI

Decided 2026-09-16 (see `docs/plans/change-intelligence-and-activity-ledger.md`):
CKB 1.x gets no web UI. Text first, evidence first. Three things instead:

- **`assessChange`** (MCP) and **`ckb changes`** (CLI): the post-change
  counterpart to `prepareChange`. Works on the uncommitted working tree by
  default (`--staged`, `--base <ref>` for the index or a range) and answers
  "what did I actually change and what should I verify before finishing":
  changed symbols, structured risk factors with evidence, blast radius,
  affected tests, downstream consumers with no reaching test, *possible*
  contract changes (a labeled heuristic: exported symbol lines changed, never
  called a breaking change), related ADRs, likely reviewers, and a confidence
  tier that names its reasons (no SCIP index, commits behind). This is
  `analyzeChange` grown up, not a third diff analysis: `analyzeChange` stays
  registered as a deprecated alias for two minor versions, and `ckb impact
  diff` stays as an alias of `ckb changes`. Tool count is now 111.
- **Activity ledger**: every MCP tool call is recorded in a `tool_calls`
  table in `.ckb/ckb.db` (schema v12) — session (from `CLAUDE_CODE_SESSION_ID`
  when run under Claude Code), consumer (from the MCP `clientInfo` handshake),
  tool, hashed and truncated params, primary target, duration, response
  bytes, truncation, error, and per-tool structured facts for `prepareChange`,
  `assessChange`, `analyzeImpact`, `findReferences`, `searchSymbols`. Written
  off the request path by a buffered single-writer goroutine; never adds
  latency, never surfaces an error to the client. Configurable via
  `activity.{enabled,retentionDays,storeParams}` (defaults true / 30 / true),
  pruned on server start. Read it with **`ckb activity`** (`--last`,
  `--session`, `--consumer`, `--tool`, `--since`, `--summary`, `--all`,
  `--json`). The feed reports calls and what they returned; it never narrates
  why the agent called something.
- **`ckb bench session <id>` / `ckb bench compare <a> <b>`**: joins a Claude
  Code transcript (tokens by class, turns, tool calls, files read) with the
  ledger rows of the same session into one record, and compares two. Produces
  neutral deltas; nothing is labeled "tokens saved".

### Fixed

- Config keys absent from an older `.ckb/config.json` now keep their default
  instead of the Go zero value. Every default-true flag (`backends.scip.enabled`,
  `review.blockBreakingChanges`, the new `activity.enabled`, …) silently flipped
  to false for configs written before the key existed.
- `SummarizePR`'s comment claimed it compared against the working tree; it runs
  `git diff base HEAD`. Comment fixed. `ckb review` still does not see unstaged
  changes; `ckb changes` does.
- `ckb impact diff` promised "who needs to review this" and never delivered.
  It does now.
- Working-tree reviewer suggestions no longer list git's "not committed yet"
  pseudo-author.

## [9.3.1] - 2026-09-13

### Fixed — linux binaries would not start on glibc < 2.39 ([#243](https://github.com/SimplyLiz/ckb/issues/243))

The 9.3.0 linux binaries declared a `GLIBC_2.39` requirement and so refused to
start on Ubuntu 22.04 LTS, Debian 12, RHEL 9 / Rocky 9 and Amazon Linux 2023 —
`version 'GLIBC_2.39' not found`. Nothing in the binary needed glibc 2.39.

9.3.0 moved the release to a native cgo matrix so the Cartographer fast tier
could be linked in. A cgo build's glibc requirement is set by the runner image,
not by the source: the linker stamps each undefined libc symbol with the version
the build host defines it at. `ubuntu-latest` had migrated to 24.04 (glibc 2.39),
and two weak symbols Rust's std references for its `posix_spawn` fast path —
`pidfd_spawnp`, `pidfd_getpid` — resolved against 2.39 and put a `GLIBC_2.39`
entry in `.gnu.version_r`, which the loader enforces before the process starts.
Both `linux-x64` and `linux-arm64` were affected.

The linux targets now build on `ubuntu-22.04` / `ubuntu-22.04-arm`, where those
symbols do not exist to link against; an unresolved weak symbol emits no version
requirement, so the floor drops to the next real one, **GLIBC_2.34**. That
covers RHEL 9 / Rocky 9 / Amazon Linux 2023 (2.34), Ubuntu 22.04 (2.35) and
Debian 12 (2.36). The Cartographer fast tier is unaffected — it stays linked.

`scripts/check-glibc-floor.go` now enforces the floor in CI and in the release
build, so a binary that cannot start on a supported distro fails the build
instead of shipping.

**Affected users:** upgrade with `npm install -g @tastehub/ckb@9.3.1` (or
`brew upgrade ckb`). No 9.2.0 downgrade needed any more.

## [9.3.0] - 2026-07-13

### Added — `ckb doctor` reports the Cartographer fast tier

`doctor` now includes a `cartographer` check that reports whether the vendored
structural engine (dependents, blast-radius, rollup — the SCIP-free fast tier)
is linked into this binary. CGO-free npm/Homebrew builds warn with a `make
build` fix hint; source builds report `linked (vX.Y.Z) — fast structural tier
active`. Previously a build silently ran without the fast tier with no way to
confirm it.

### Fixed — FFI crash (process abort) on very large repos

The vendored engine aborted the whole process — including the CKB host, via the
FFI — on a full Linux-kernel checkout (~64k C/H files): two unbounded recursions
overflowed the stack. Recursive tree-sitter walkers on deeply nested (macro-
generated) C now run on a 256 MB-stack pool, recursive `tarjan_scc` cycle
detection is replaced with an iterative version, and the walkers carry a hard
depth ceiling (cap 50,000; the kernel's measured real peak is 3,348) as a belt
against adversarial/generated input. Verified: `Health("/tmp/linux")` via FFI
completes without crashing (regression test in `internal/cartographer`). This
directly de-risks the very-large-C++-repo use case.

### Changed — re-vendor CodeCartographer 4.0.0

Re-vendored the graph engine at CodeCartographer 4.0.0 (FFI/C-ABI unchanged, so
no bridge changes). Pulls upstream fixes CKB consumes: authoritative Rust
qualified-path import resolution (crate-internal edges were dropped as fuzzy,
making structural metrics measure docs instead of code), health/ranked-skeleton
now exclude the documentation subgraph, char-boundary-safe string truncation
(fixed a UTF-8 panic on non-ASCII indexed content), call-graph-resolved
`reach` caller/callee edges, and git churn/coupling noise filtering. FFI-tagged
tests green; full `-tags cartographer` build clean.

### Performance — scaling to very large C++ trees

Profiling on a 40k-file synthetic tree (and Godot) exposed three serial
bottlenecks in the vendored graph engine; all now parallelize or drop in
complexity:

- **Parallel edge resolution** — `rebuild_graph` resolves every file's imports
  via rayon against the immutable index (was single-threaded).
- **Parallel, index-based betweenness centrality** — betweenness was 98% of a
  graph rebuild (~3.14s of 3.2s on Godot). The Brandes pass per sampled source
  now runs over dense `Vec` buffers keyed by node index (was per-source
  `HashMap<&str,_>` reallocation) and the independent sources run across cores.
  Per-source contributions are summed in fixed source order, so results are
  **bit-identical regardless of core count** — deterministic across machines,
  not just across runs. On Godot: betweenness 3,140ms → 57ms, full rebuild
  3,197ms → 76ms (~42×), same 3182 bridges / 68 god-modules / 0 cycles.
- **Directory-level rollup** — the vendored engine can now fold the file-level
  graph to directory granularity at a given depth, aggregating cross-directory
  dependencies and running the full structural analysis on the folded graph, so
  bridges/cycles/god-modules/health describe subsystems rather than individual
  files. On Godot this turns 4336 files / 3182 bridges into 160 subsystems /
  139 bridges with actionable per-subsystem hints — the comprehensible view on
  huge trees. Available as `rebuild_graph_rolled_up(depth)` (CLI:
  `health --rollup <depth>`).
- **Deterministic import resolution** — resolver candidate lists were in
  HashMap order, so an ambiguous import could resolve to different targets
  run-to-run (invisible at file level, but it drifted directory-level edge
  counts). Candidate lists are now sorted and edge dedup keeps the strongest
  resolution (exact < suffix < fuzzy), making the graph fully deterministic.
- **Betweenness cache (incremental rebuild)** — centrality depends only on graph
  topology, so it's cached keyed by a fingerprint of the structural node+edge
  set. An edit that leaves imports unchanged (a body/comment/whitespace save —
  the common watch-mode case) reuses it and skips the whole Brandes pass. The
  cache survives graph invalidation and self-invalidates only when an import
  actually changes. The win grows with repo size: at 100× scale, where sampled
  betweenness re-inflates to seconds, a non-import edit stays near the ~40ms
  floor instead of paying the full recompute.
- **O(1) package-suffix fallback** — the qualified/Go package resolver's
  directory-suffix probe scanned *every* directory per unresolved import
  (O(dirs), a real cliff for repos with many external qualified imports). It now
  probes only directories sharing the import's last segment via a suffix index.
- Cartographer's CLI parse loops were also switched from serial to parallel
  (`iter`→`par_iter`), though CKB uses the parallel FFI path.

Measured: a cold 40k-file analysis went 56s → 12.7s (~4.4×); Godot (4.3k files)
16s → ~2s (~8×), with identical results.

### Changed

- **Re-vendored the Rust crate to upstream `CodeCartographer` 1.3.3** (from
  the interim `nyx-navigator` 1.1.0 fork), and dropped the `nyx`/`navigator`
  naming — the CKB side is `cartographer` again, tracking upstream's FFI.
  - Vendor path `third_party/nyx-navigator/` → `third_party/cartographer/`,
    synced straight from upstream `mapper-core/codecartographer/`.
  - Build tag `navigator` → `cartographer`; `make build` / `make test`
    resolve `build-cartographer` / `test-cartographer`. `make build-fast`
    (no Rust toolchain) unchanged.
  - Static lib `libnavigator.a` → `libcode_cartographer.a`; FFI prefix
    `navigator_*` → `codecartographer_*`; header `navigator.h` →
    `codecartographer.h`; sync script `scripts/sync-nyx-navigator.sh` →
    `scripts/sync-cartographer.sh`.
  - Go package `internal/navigator` → `internal/cartographer`; error type
    `NavigatorError` → `CartographerError`; status backend id
    `navigator` → `cartographer`.

### Gained from upstream 1.3.x (library-level; bridge surface unchanged)

The re-vendor pulls upstream's 1.2–1.3 work into the linked library:

- **Six new languages** — Java, C#, Ruby, Kotlin, Swift, PHP now have
  tree-sitter skeleton extraction, file-local call graphs, and class
  diagrams, on par with the original six. `skeleton_map`,
  `ranked_skeleton`, `reach_symbol`, and search cover all twelve.
- **Symbol-aware search corpus** — `query_context` / `answer_question`
  rank over a BM25 corpus of parsed symbols (name + qualified name +
  signature + doc-comment), not raw file bytes.
- **Scope-qualified `reach_symbol`** — renders `Object.get_class`,
  surfaces doc-comments, and filters call sites that qualify a different
  class.
- **Orientation-first repo map** — `ranked_skeleton` ranks by role +
  fan-out instead of pure import-PageRank, so entry points and domain
  core lead.
- **Perf** — O(N) import resolution and sampled betweenness fix the
  O(N²) hang on large C/C++ trees (Godot cold start ~2 min → ~12 s).

### Fixed — go.mod-aware import resolution (kills fabricated dependency edges)

The vendored resolver matched Go imports by bare filename stem, so
`internal/errors` resolved to an unrelated `internal/a2a/errors.go` and a
stdlib `sync` fabricated an edge to `internal/federation/sync.go`. Resolution
is now Go-module-aware: an import is internal only if it lives under the
`go.mod` module path, in which case it resolves to its package *directory*;
everything else (stdlib, third-party) produces no internal edge. Measured on
CKB itself: **~23% of dependency edges were fabricated (996 → 769)**, all **22
reported dependency cycles were artifacts (→ 0**, as Go forbids package import
cycles), bridges 244 → 187, and the architectural health score went 38 → 71.
This directly cleans up every import-graph-derived surface — `getArchitecture`,
`getBlastRadius`, `renderArchitecture`, `getModuleContext`, `analyzeImpact`,
`prepareChange`, and the `review` layers/arch-health checks.

As a safety net for the remaining languages (which still resolve heuristically),
every edge now carries a `resolution` confidence tag (`exact` | `suffix` |
`fuzzy`), exposed on `cartographer.GraphEdge`. All structural analysis is now
computed on non-`fuzzy` edges only — cycles, the "will create a cycle"
prediction, bridges, god modules, layer violations, and the blast radius — so
none of the import-graph-derived surfaces (`getArchitecture`, `getBlastRadius`,
`analyzeImpact`, `prepareChange`, the `review` layers/arch-health checks) can
rest on a low-confidence stem guess. The returned graph still carries every
edge, tagged, so consumers can decide. On the CKB side, `review`'s PR-split
clustering skips `fuzzy` edges so a split recommendation isn't driven by a
fabricated dependency.

The same namespace-exact idea was generalized beyond Go: any extensionless
import that carries directory structure (Python `from app.services import auth`,
monorepo/aliased `components/Button`) now resolves against its **full path** —
matching module files and package directories (preferring `__init__.py` /
`index.*` entry points) — instead of collapsing to the last stem. A qualified
import that matches nothing local is treated as external (no edge) rather than
fuzzy-guessed. The Python extractor was fixed to preserve import keywords so
dotted paths (`a.b.c`) normalize correctly. Net: `from app.services import auth`
and `from app.models import auth` resolve to their own packages instead of
colliding on the shared `auth` stem, and third-party imports stop fabricating
internal edges.

JS/TS relative imports (`./bar`, `../models/user`) now resolve against the
**source file's directory** — path-precise, `..` walks up, a bare package dir
uses its `index.*`. A relative path that isn't in the scan is a dangling ref
(no edge), never a fuzzy same-name match. `export … from './x'` re-exports are
now captured as dependencies. Bare package imports (`react`) stay external.

### Available for follow-up wiring (not yet exposed)

The vendored library exports FFI symbols not yet wired through the Go
bridge or MCP layer:

- `codecartographer_poll_changes(path, since_ms)` — incremental change
  polling; fits CKB's `mcp --watch` and the daemon file-watcher.
- `codecartographer_doc_index`, `codecartographer_doc_context`,
  `codecartographer_query_docs` — full doc-retrieval pipeline.

## [9.2.0] - 2026-04-25

### Added

- **`analyzeOutgoingImpact` — forward call graph** (MCP + CLI) — mirror
  of `analyzeImpact` answering *"what does this symbol call?"* instead of
  *"who calls it?"*. New `Engine.AnalyzeOutgoingImpact` drives off LIP
  v2.3.5's `query_outgoing_impact` RPC, folds the result through the same
  `ImpactItem` pipeline as the incoming side (with `direct-callee` /
  `transitive-callee` kinds), and surfaces semantically coupled callees
  alongside the static graph. Degrades cleanly when LIP isn't running:
  the response is empty with a provenance warning, never an error.
  Surfaces include `ckb impact outgoing <symbolId>` (with `--min-score`
  for the semantic threshold), the `analyzeOutgoingImpact` MCP tool, and
  a new `ProvenanceCLI.Warnings` field so LIP-degradation messages reach
  JSON consumers.
- **`symbolExists` MCP tool** — exact-match boolean oracle that returns
  `{exists, kind, location?}` for a fully-qualified symbol ID. Built for
  LLMs to ground references *before* they cite them in code, without
  spending tokens on a 20-result `searchSymbols` payload. Cheaper than
  `getSymbol` for the "does this thing actually exist" check.
- **LIP enrichment folds into `analyzeImpact`** — tier-1 tree-sitter
  callers that LIP discovers (when `scip-go` emits no `Call` roles, e.g.
  Go method dispatch) are now folded into the same `directImpact` /
  `transitiveImpact` lists as SCIP's own results, deduplicated by
  `(file, name)`. Driven by a new `BlastRadiusEnricher` interface so the
  fold path is the single source of truth for both incoming and outgoing
  impact analysis. Items LIP marks `edges_source=empty` are skipped (LIP
  signalling no static evidence); `tier1`, `scip_with_tier1_edges`, and
  `scip_only` all fold the same way. Risk score now picks up
  semantic-coupling signals via the same enricher pipeline.
- **`register_project_root` on LIP handshake** — Engine startup now
  registers the repo root with the daemon so LIP canonicalises file URIs
  against a known anchor, matching the v2.3.1 contract. Eliminates the
  URI-shape drift that previously caused tier-1 callers to dedup
  incorrectly against SCIP results.

### Changed

- **`analyzeImpact` risk score now weighted by bridge centrality** —
  `calculateAggregatedRisk` multiplies the weighted-mean score by
  `1 + max(BridgeScore)/1000` (capped at 2.0) over the changed files, so a
  change landing on a critical architectural path (high betweenness) is
  reported as riskier than the same-shape change in a leaf module. Implements
  the behaviour that `CARTOGRAPHER_STRATEGY.md` had already documented but
  the code was not actually doing. Bridge lookups match by both `Path` and
  `ModuleID`; if no changed file matches the graph, the multiplier is 1.0
  and no informational factor is appended. Only runs when the binary was
  built with `-tags cartographer` (graph is a no-op otherwise). A new
  `bridge_centrality` informational factor surfaces in `RiskScore.Factors`
  when the multiplier fires; its `Weight` is 0 because it applies
  multiplicatively, not as a weighted-mean input.

### Cartographer

- **Vendored Cartographer fully synced to upstream 3.0.0** — the
  vendored tree under `third_party/cartographer/mapper-core/cartographer/`
  was 391 lines behind on `diagram.rs` alone, and 10 `.rs` files plus
  `Cargo.toml` had drifted. Full sync brings in doc-node graph support
  (`cartographer_doc_index`, `cartographer_doc_context`, `cartographer_query_docs`
  FFI entry points — Go bindings can be added as a follow-up),
  LIP-style `Range` / `at_range` on `GraphEdge`, PascalCase bare-identifier
  resolution for doc backtick refs, and the overlays feature on diagrams.
  New `scripts/sync-cartographer.sh` is now the supported path for future
  syncs — rsync-based, explicit path list, emits next-step commands. No
  local patches needed against upstream.
- **Diagram overlays in `renderArchitecture` / `ckb diagram`** — the
  vendored `diagram.rs` was synced from upstream Cartographer, so the
  Mermaid/DOT output now decorates the base import graph with
  architectural signals: cycle members get a thick red border (pivots
  dashed), cycle-internal edges a heavy red arrow, layer violations pick
  up per-type dashed/dotted edge styling, and hot nodes
  (`hotspot_score ≥ 70`) get an orange border plus DOT size scaling.
  Mermaid is border-only for hot nodes (no sizing primitive). Cycle red
  takes precedence over hot orange on the same node — architectural
  signal wins over performance signal.
- **`renderArchitecture` MCP tool** — returns the project's module-level
  import graph as Mermaid or Graphviz (DOT), ready to paste into IDEs
  that render Mermaid inline (Cursor, Claude Desktop, VS Code markdown
  preview, GitHub). With `focus` set, returns an undirected BFS
  neighborhood around the anchor module to `depth` (default 2); without,
  returns the top-N most-connected nodes (default cap 40). Response
  includes `truncated: true` when the node cap kicked in. Backed by the
  new `cartographer_render_architecture` FFI export; CLI and MCP outputs
  are produced by the same shared renderer.
- Go binding `cartographer.RenderArchitecture()` in `internal/cartographer/bridge.go` (+ no-op stub for the no-tag build).

### Fixed

- **Vendored Cartographer `rebuild_graph` deadlock** — upstream
  `ApiState::rebuild_graph` held the `mapped_files` Mutex across its
  loop and then called `resolve_import_target`, which re-acquired the
  same non-reentrant `std::sync::Mutex`. Any project with a resolvable
  import deadlocked — the `cartographer diagram` / `cartographer health`
  CLIs hung, and the Go bridge's `cartographer.MapProject` would block
  any time CKB fed it a repo with imports. Fixed in the vendored tree
  (and contributed back upstream) by splitting the resolver: a public
  method that locks, and a private helper that takes the already-held
  map; `rebuild_graph` now calls the helper. Discovered during
  end-to-end smoke testing against CKB itself (1093 files). Regression
  test added upstream.
- **`localize-tree-sitter-symbols.sh` dropped grammar C parsers** — the
  script extracted archive members via `ar x`, which silently clobbers
  files when multiple members share a name. Cargo emits a `parser.o`
  and `scanner.o` per grammar crate (tree-sitter-c, -cpp, -rust, -go,
  etc.), so `ar x` left only the *last* grammar's C parser on disk,
  producing a localized archive missing `_tree_sitter_c` / `_tree_sitter_cpp`.
  The script now feeds the archive directly to `ld -r` with
  `-force_load` (Mach-O) / `--whole-archive` (ELF), which pulls every
  member in without touching the filesystem. The `rust_tree_sitter` C
  ABI refs to `_tree_sitter_c` and `_tree_sitter_cpp` now resolve
  inside the combined object as expected.
- **Tree-sitter symbol collisions at link time** — `libcartographer.a`
  previously exported its bundled tree-sitter runtime and grammar
  symbols, which collided with `go-tree-sitter` when building CKB with
  `-tags cartographer` (`ld: 246 duplicate symbols`). `make build-cartographer`
  now post-processes the archive via
  `scripts/localize-tree-sitter-symbols.sh` (vendored under
  `third_party/cartographer/mapper-core/cartographer/scripts/`), which
  partial-links archive members into one combined object and localizes
  `ts_*` / `tree_sitter_*`. `cartographer_*` FFI exports stay global.
  Beyond the duplicate-symbol error, this also rules out a silent
  memory-corruption class of bug where Cartographer's Rust code could
  have bound to the consumer's tree-sitter copy at global resolution
  time if the two versions' struct layouts ever drifted.

## [9.1.0] - 2026-04-16

### Added

- **LIP v2.1 utilisation** — three high-ROI LIP RPCs wired into the query
  engine, gated on the handshake's `supported_messages`:
  - `stream_context` (v2.1) → `explainFile` attaches up to 10
    semantically-related symbols (2048-token budget) in `facts.related`.
    New streaming transport reads N `symbol_info` frames + `end_stream`.
  - `query_expansion` (v1.6) → `searchSymbols` expands ≤ 2-token queries
    with up to 5 related terms before FTS5, recovering vocabulary-mismatch
    recall without touching precision on compound queries.
  - `explain_match` (v2.0) → semantic search hits carry up to two ranked
    evidence chunks with line ranges, text, and per-chunk scores (top-5
    hits, bounded round-trip cost).
- **`lip.Handshake` runs on engine startup** and the daemon's
  `supported_messages` list is stashed for feature gating
  (`Engine.lipSupports`). Daemon version and supported-count logged.
- **LIP index status probing** — `probeHandshake` now follows up with
  `IndexStatus` and caches the result. New `Engine.LIPStatus()` returns
  `{Reachable, IndexedFiles}` so consumers can distinguish "daemon down"
  from "daemon up, nothing indexed."
- **`ckb review` warns when LIP index is empty** — stderr advisory with
  `lip index <repo>` command when daemon is reachable but has no content.
  Suppressed in `--ci` to keep CI logs clean.
- `NoAutoFetch` option on `SummarizePROptions` and `SummarizeDiffOptions`
  for parity with `ReviewPROptions`.
- Troubleshooting section in `docs/plans/review-cicd.md` covering shallow
  CI clones, auth-failure remediation, air-gapped pipelines, and depth-0
  checkout alternatives.
- Auth-error detection on auto-fetch with clear remediation guidance.
- `ckb review --no-auto-fetch` flag for air-gapped pipelines.
- Test coverage for `GitAdapter.EnsureRef` — happy path, missing-ref
  auto-fetch, unreachable origin, and empty-input guard.
### Changed

- **LIP health: push-driven, not polled** — Engine opens a long-lived
  connection to the daemon at startup (`internal/lip/subscribe.go`) with
  `index_changed` frames and per-ping `index_status` snapshots instead of
  60 s TTL polling. Worst-case staleness drops from 60 s to ~3 s.
- **`lipFileURI` path normalisation** — handles absolute paths and
  already-prefixed `file://` URIs without producing malformed results.

### Fixed

- **Bug-pattern false positive on `sync.Mutex.Lock()`** — removed `"Lock"`
  from `LikelyReturnsError` heuristic patterns; `sync.Mutex.Lock` returns
  nothing and dominated real-world matches with false positives.
- **`err` shadowing in `subscribe.go`** — four shadow sites eliminated by
  reusing outer `err` or renaming to `pingErr`/`readErr` where scope
  isolation requires it.

- **LIP rerank: coherence gate + position-weighted seeding** (#209) — the
  Fast-tier semantic rerank (`internal/query/lip_ranker.go`) used to average the
  top-5 seed embeddings with uniform weight and always apply the result. When
  the top-5 pointed in different directions the centroid collapsed toward zero
  and amplified noise; when the top seed was strong the blend still diluted it.
  Seeds are now L2-normalised and position-weighted (`1/(rank+1)`), the
  resulting centroid norm is read as a coherence score in `[0, 1]`, and the
  rerank falls back to pure lexical order when coherence is below
  `MinCoherence` (default `0.35`). Blend weights, seed count, and threshold are
  surfaced as `RerankConfig` so future tuning does not need to touch call
  sites. Injected `embedBatchFn` makes the ranker unit-testable without a
  running daemon.
- **LIP rerank: gate on `!MixedModels`** (#208) — when the LIP index contains
  vectors from more than one embedding model (e.g. partial re-index during a
  model upgrade), cosine similarity across those vectors is mathematically
  meaningless. `RerankWithLIP` and `SemanticSearchWithLIP` now consult a
  cached `Engine.lipSemanticAvailable()` check (60 s TTL, single `IndexStatus`
  RPC) and fall back to lexical ranking when the daemon is down or reports
  `mixed_models`. A new `lip_mixed_models` degradation warning (70% capability)
  surfaces in response metadata so users learn *why* results look weaker
  instead of silently ranking on garbage.

## [9.0.1] - 2026-04-15

### Fixed

- **`ckb review` in shallow CI clones** — Azure Pipelines, GitHub Actions, and
  GitLab default to shallow single-branch checkouts, so `ckb review --base=main`
  failed with `exit 128` because the base ref was not present locally. The
  review path (and `summarizePr` / `summarizeDiff`) now auto-fetch the base ref
  from `origin` when it is missing, falling through to `origin/<branch>`. No
  pipeline changes required. No cost for full clones.
- **Opaque git errors** — `GitAdapter.executeGitCommand` previously wrapped git
  failures as "Git command failed: exit status 128" with git's actual stderr
  hidden in a details map. The stderr (e.g. `fatal: bad revision`) is now part
  of the error message, making CI failures diagnosable without reproduction.

## [9.0.0] - 2026-04-13

### Added

#### LIP v2.0 semantic integration

CKB now speaks the LIP v2.0 wire protocol correctly and integrates semantic
embeddings across the tool suite. The existing `internal/lip` client had the
wrong JSON discriminator (`"action"` instead of `"type"`) and wrong action
strings, meaning all LIP calls were silently failing. The client has been
rewritten with the correct Serde-tagged format and 25 new functions covering
LIP v1.5–v2.0.

**Wire protocol fix** — all requests now use `"type"` as the discriminator with
`snake_case` variant names matching Rust's
`#[serde(tag = "type", rename_all = "snake_case")]`. Field names corrected
throughout (e.g. `"symbol_uri"` not `"uri"` for annotation queries).

**New LIP client functions** (`internal/lip/client.go`):

| Function | LIP version | Purpose |
|---|---|---|
| `Handshake` | v1.5 | Protocol handshake, returns daemon + protocol versions |
| `BatchNearestByText` | v1.5 | Parallel nearest-neighbour for multiple queries |
| `NearestBySymbol` | v1.5 | Nearest neighbours by `lip://` symbol URI |
| `BatchAnnotationGet` | v1.5 | Bulk annotation lookup |
| `ReindexFiles` | v1.6 | Trigger reindex for specific URIs |
| `Similarity` | v1.6 | Cosine similarity between two files |
| `QueryExpansion` | v1.6 | Expand a query with semantically related terms |
| `Cluster` | v1.6 | Group files by semantic proximity |
| `ExportEmbeddings` | v1.6 | Raw embedding export |
| `NearestByContrast` | v1.7 | Like-URI minus unlike-URI retrieval |
| `Outliers` | v1.7 | Semantically isolated files |
| `SemanticDrift` | v1.7 | Cosine distance between two files |
| `SimilarityMatrix` | v1.7 | Pairwise similarity matrix |
| `FindSemanticCounterpart` | v1.7 | Best match for a file within a candidate set |
| `Coverage` | v1.7 | Embedding coverage stats by directory |
| `FindBoundaries` | v1.8 | Semantic boundary detection within a file |
| `SemanticDiff` | v1.8 | Compare two text blobs by embedding distance |
| `NearestInStore` | v1.8 | Nearest neighbours against an in-memory store |
| `NoveltyScore` | v1.8 | Per-file novelty (0–1, higher = fewer neighbours) |
| `ExtractTerminology` | v1.8 | Domain term extraction from a file set |
| `PruneDeleted` | v1.8 | Remove embeddings for deleted files |
| `GetCentroid` | v1.9 | Mean embedding vector for a file set |
| `StaleEmbeddings` | v1.9 | Files with out-of-date embeddings |
| `NearestByTextFiltered` | v1.9 | Nearest-by-text with glob filter + min score |
| `NearestByFileFiltered` | v1.9 | Nearest-by-file with glob filter + min score |
| `ExplainMatch` | v2.0 | Chunk-level explanation of why a file matched a query |

**Response types added**: `HandshakeInfo`, `CoverageInfo`, `DirCoverage`,
`BoundaryRange`, `SemanticDiffInfo`, `NoveltyInfo`, `NoveltyItem`, `TermItem`,
`ExplanationChunk`. `IndexStatusInfo` gains `MixedModels bool` and
`ModelsInIndex []string`. `FileStatusInfo` gains `EmbeddingModel string`.

#### `searchSymbols` — semantic fallback + re-ranking with filter

`SemanticSearchWithLIP` now accepts `filter string` and `minScore float32`
parameters and delegates to `NearestByTextFiltered`. The filter accepts glob
patterns (e.g. `"internal/api/**"`) to restrict semantic results to a subtree.
Call site in `symbols.go` updated; existing callers pass `"", 0` for unchanged
behaviour.

#### `reviewPR` — `semantic-novelty` check

A new `semantic-novelty` check runs alongside the existing 20 review checks.
It calls `NoveltyScore` on changed files and flags any with a score ≥ 0.7 as
"semantically novel" — files with few neighbours in the embedding index that
may lack test coverage. Degrades silently when LIP is unavailable; skipped
automatically when fewer than 2 files are changed.

#### `getAffectedTests` — semantic test discovery

After the SCIP-based test collection pass, a LIP pass runs
`NearestByFileFiltered(fileURI, 5, "*_test.go", 0.6)` for each changed source
file. Matching test files are added to the result with
`Reason: "semantic-proximity"` and `Confidence` set to the LIP score. Files
already found by SCIP are not duplicated.

#### `explainFile` — semantic boundary detection

After the existing symbol analysis, `toolExplainFile` calls
`FindBoundaries(fileURI, 0, 0, "")` (defaults: 30-line chunks, 0.3 threshold)
and appends a `semantic_boundaries` array to the response:
```json
[{"start_line": 1, "end_line": 45, "shift_magnitude": 0.71, "nearest_symbol": "handleAuth"}]
```
Silently omitted when LIP is unavailable or returns no boundaries.

#### `getArchitecture` — semantic coupling matrix

After structural architecture data is assembled, `toolGetArchitecture` collects
representative file URIs for each module, calls `SimilarityMatrix`, and adds a
`semantic_coupling` field:
```json
{"modules": ["internal/auth", "internal/api"], "matrix": [[1.0, 0.74], [0.74, 1.0]]}
```
Also calls `GetCentroid` over up to 500 repo files and records
`repo_centroid_included: N` in the response metadata. Both are silently omitted
when LIP is unavailable or fewer than 2 modules are embedded.

#### `doctor` — LIP coverage + stale embedding + model provenance

The LIP health section in `ckb doctor` now reports:
- Coverage: `N% embedded (Y/Z files)`
- Stale embeddings: count of files with out-of-date vectors
- Mixed-model warning when multiple embedding models are present in the index
- List of active embedding models

### Changed

- `NearestByFile` and `NearestByText` are now thin wrappers over
  `NearestByFileFiltered` and `NearestByTextFiltered` respectively
- `GetEmbedding` and `GetSymbolEmbedding` delegate to `GetEmbeddingsBatch`
  (the old `"embedding_get"` and `"symbol_embedding"` wire variants had no
  corresponding Rust enum variants)

---

## [8.5.0] - 2026-04-11

### Added

#### Cartographer bundled as git subtree (`third_party/cartographer/`)

Cartographer is now vendored directly into the repo instead of requiring a
sibling directory at `../../../../Cartographer/`. Contributors no longer need
two repos co-located. Update via:

```bash
git subtree pull --prefix third_party/cartographer \
  https://github.com/SimplyLiz/Cartographer.git master --squash
```

#### Three new MCP tools (Cartographer-backed)

**`detectShotgunSurgery`** — Detect files that historically required simultaneous
edits across many unrelated files. Ranked by co-change dispersion score.
```
detectShotgunSurgery(repo_path: "/path/to/repo", min_partners: 3, limit: 100)
```

**`getArchitecturalEvolution`** — Architectural health snapshots over git history.
Returns health score trend (improving/stable/degrading), debt indicators, and
recommendations.
```
getArchitecturalEvolution(repo_path: "/path/to/repo", days: 90)
```

**`getBlastRadius`** — Graph-theoretic blast radius for a file or module. Works
without a SCIP index; complements `analyzeImpact` for unindexed repos.
```
getBlastRadius(repo_path: "/path/to/repo", target: "src/core/engine.go", max_related: 50)
```

#### LIP semantic search (`GetEmbedding`)

`internal/lip` now exposes `GetEmbedding(uri, model)` — requests a
TurboQuant-quantized embedding vector from the LIP daemon for a given file URI.
Returns `[]float32` suitable for direct dot-product similarity ranking without
dequantization. Degrades silently when LIP is not running.

### Performance

#### SCIP loader: lazy CallerIndex — eliminates load-time regression on small indexes

The caller inverted index (`CallerIndex`) is now built on the first `FindCallers`
call rather than at `LoadIndex` time. This removes ~22k persistent heap objects
from the initial SCIP load on small indexes (1k docs), which were causing elevated
GC pressure and a measurable load-time regression. Medium/large indexes are
unaffected — the index is built once and cached thereafter.

**Benchmark impact vs v8.4.0 (small, 1k docs):** load alloc count is unchanged
(~375.6k in both versions — the CallerIndex for 1k docs is not large enough to
register in alloc counts). The win is GC liveness: ~22k heap objects that would
have been promoted to old-gen are no longer live after load. No change for
medium/large.

#### SCIP loader: `DiscardUnknown` proto decode

Both `proto.Unmarshal` calls in the document stream parser now use
`proto.UnmarshalOptions{DiscardUnknown: true}`. This skips the reflection-based
unknown-field accumulator, reducing allocations during SCIP file decode.

**Measured vs v8.4.0 (medium, 10k docs):**
- `B/op`: **909 MiB → 781 MiB (-14.10%)**
- `allocs/op`: **6.94M → 6.64M (-4.27%)**

Small and large indexes show no measurable change (unknown-field savings are
proportionally smaller there).

#### CallerIndex builder: generation-counter deduplication

`buildCallerIndex` now reuses the `ivs` interval slice across documents (resliced
to zero, grown only when needed) and replaces the per-document `map[edge]bool`
with a generation counter (`map[edge]uint64`). Eliminates ~2k per-load allocs on
the 1k-doc case and removes all per-document map allocs on medium/large.

#### `PopulateFromFullIndexStreaming`: two-pass streaming to prevent OOM on large repos

`PopulateFromFullIndex` has always called `LoadSCIPIndex` which materialises the
entire `*SCIPIndex` in memory before processing a single file. On a 50k-doc
monorepo this peaks at ~15 GB and causes sustained GC pressure (observed: 485s
first run vs a consistent 83s with streaming).

`PopulateFromFullIndexStreaming` replaces this with a two-pass strategy over
the on-disk SCIP file (via `scip.StreamDocuments`), never materialising the full
index:

- **Pass 1**: build the `symbol→file` map — one `*scippb.Document` live at a time,
  freed by GC before the next arrives. Peak live heap ≈ the symbolToFile map alone.
- **Pass 2**: stream documents again, extract deltas via the new proto-native
  `extractFileDeltaFromProto` (skips all `convertDocument` allocations), write SQL
  in 1000-file batches.

`extractFileDeltaFromProto` works directly on `*scippb.Document` so there are no
intermediate `*scip.Document` / `*scip.Occurrence` / `*scip.SymbolInformation`
allocations per document per pass.

**Benchmark vs `PopulateFromFullIndex` (50k docs, Apple M4 Pro, -count=2):**

| | current | streaming | delta |
|---|---|---|---|
| B/op | 15.69 GB | 15.23 GB | -2.9% |
| allocs/op | 166.4M | 181.8M | +9.3% |
| time (cold) | **485s** | **83s** | **-83%** |
| time (warm) | 122s | 83s | -32% |

The extra allocs/op come from two proto-unmarshal passes vs one (plus
`convertDocument` in the current path). The time improvement reflects reduced
GC pressure: streaming never has more than one document live at a time, so GC
never needs to scan or collect the 15 GB of live SCIPIndex data.

#### Incremental write path: major throughput improvements (landed in v8.4.0)

The following improvements shipped in v8.4.0 and are reflected in the v8.4.0
benchmark baseline. Documented here for completeness:

- **Parallel `extractFileDelta`**: GOMAXPROCS worker goroutines extract file
  deltas concurrently during `PopulateFromFullIndex`. Cuts large-repo population
  time by the number of available cores.
- **Batched transactions** (1000 files/tx): WAL stays bounded on 50k-file indexes
  instead of growing to multi-GB. Eliminates the 10h+ timeout on large repos.
- **`PRAGMA synchronous=OFF`** during bulk load: safe because a failed full index
  is always re-run from scratch.
- **Bulk INSERT for `file_symbols`**: 499-row multi-value `INSERT` batches reduce
  round-trips from 50k to ~100 for large repos.
- **Hoisted prepared statements** in `ApplyDelta`: `symbol`, `callgraph`, and
  `file_deps` statements prepared once per delta instead of once per file.

**Benchmark vs v8.2.1 (v8.4.0 baseline):**
- `ApplyDelta/large` (50k files): **50s → 42s (-16%)**
- `ExtractFileDelta/50syms`: **109µs → 90µs (-17%)**
- `GetDependencies/1000files`: **7.0ms → 6.3ms (-10%)**
- SCIP allocs geomean: **-12%** (backing-slice OccurrenceRef optimization)

#### SCIP loader: O(1) `FindCallers` via CallerIndex (landed in v8.4.0)

`FindCallers` was O(docs × funcs × occs). It now uses an inverted map built from
Documents, making every caller lookup O(1). The index uses a sorted interval scan
with early-break for function containment and a generation-counter for
cross-document edge deduplication.

## [8.3.0] - 2026-03-27

### Added

#### Compliance Audit (`ckb audit compliance`)
Full regulatory compliance auditing with 131 checks across 20 frameworks:

```bash
ckb audit compliance --framework=gdpr,iso27001    # Specific frameworks
ckb audit compliance --framework=all              # All 20 frameworks
ckb audit compliance --recommend                  # Auto-detect applicable frameworks
ckb audit compliance --framework=gdpr --ci        # CI mode with exit codes
```

**20 frameworks:** GDPR, CCPA, ISO 27701, EU AI Act, ISO 27001, NIST 800-53, OWASP ASVS, SOC 2, PCI DSS, HIPAA, DORA, NIS2, FDA 21 CFR Part 11, EU CRA, SBOM/SLSA, DO-178C, IEC 61508, ISO 26262, MISRA C, IEC 62443.

**Cross-framework mapping:** A single finding (e.g., hardcoded credential) automatically surfaces all applicable regulations with specific clause references and CWE IDs.

**Framework recommendation (`--recommend`):** Scans codebase for indicators (HTTP handlers, PII fields, database imports, payment SDKs) and recommends applicable frameworks with confidence scores.

**Output formats:** human, json, markdown, sarif.

**MCP tool:** `auditCompliance` — runs compliance audit via MCP using the persistent SCIP index.

#### MCP Tools: `listSymbols` and `getSymbolGraph`

**`listSymbols`** — Bulk symbol listing without search query:
```
listSymbols(scope: "src/services/", kinds: ["function"], minLines: 30, sortBy: "complexity")
```
Returns complete symbol inventory with body ranges (`lines`, `endLine`) and complexity metrics (`cyclomatic`, `cognitive`). Replaces exploring 40 files one-by-one.

**`getSymbolGraph`** — Batch call graph for multiple symbols:
```
getSymbolGraph(symbolIds: [...30], depth: 1, direction: "callers")
```
Returns deduplicated nodes and edges with complexity per node. One call replaces 30 serial `getCallGraph` calls.

#### `searchSymbols` Enhancements

- **Complexity metrics:** Results now include `lines`, `cyclomatic`, `cognitive` per symbol via tree-sitter enrichment
- **Server-side filtering:** `minLines`, `minComplexity`, `excludePatterns` params — filter 80% of noise server-side instead of client-side
- **`batchGet` with `includeCounts`:** Returns `referenceCount`, `callerCount`, `calleeCount` per symbol (parallel SCIP lookups)

#### Symbol Body Ranges (`startLine`, `endLine`, `lines`)

`searchSymbols`, `explore` keySymbols, and `getSymbolGraph` now return full body ranges via tree-sitter enrichment. Consumers no longer need to read source files for brace-matching.

#### Explore keySymbols Improvements

- Functions rank above struct fields (behavioral analysis priority)
- Tree-sitter supplement fills in functions when SCIP returns only types
- Per-symbol `cyclomatic` and `cognitive` complexity

#### `getFileComplexity` in Refactor Preset

Previously only available in `full` preset (96 tools). Now in `refactor` (39 tools).

### Fixed

#### Bug-Pattern False Positives (42 → 0)
- **defer-in-loop:** Recognize `func(){}()` closure pattern as correct (defer fires per iteration)
- **discarded-error:** Skip closure bodies in IIFE patterns; add `singleReturnNew` allowlist (NewScanner, NewReader, etc.); add `noErrorMethods` (Scan, WriteHeader, WriteJSON, WriteError, BadRequest, NotFound, InternalError)
- **missing-defer-close:** Remove NewReader/NewWriter from resource-opening functions (bufio wrappers don't need Close)
- **nil-after-deref:** 30-line gap threshold filters cross-scope false matches
- **shadowed-err:** Only flag when outer `err` is standalone function-body-level `:=`; treat if/for/switch initializer `:=` as scoped

All fixes use `FindNodesSkipping` — scope-aware tree-sitter node search that stops recursion at `func_literal` boundaries.

#### Secrets Scanner
- Shell variable interpolation (`${VAR:-default}`, `${VAR:?error}`) in Docker Compose URLs no longer flagged as password_in_url
- Shell environment leak: `env -i` wrapper prevents user profile (.zshrc) from corrupting subprocess output

#### Test-Gap Detection
- `vi.mock`/`jest.mock` module-level mocking recognized — functions covered by module mocks no longer flagged
- Barrel/re-export files (`export * from '...'`) skipped — pure re-exports have no logic to test

#### Coupling Check
- Expanded noise filter: test files, dependency manifests (go.mod, package.json), documentation, generated directories (dist/, build/, l10n/, __generated__/)
- Generated file suffixes: .pb.go, .pb.h, .pb.cc, .pb.ts, _grpc.pb.go, _pb2.py, .g.dart, .freezed.dart, .mocks.dart, _string.go, wire_gen.go, _mock.go, .bundle.js, .arb, .d.ts
- Flutter l10n false positive fixed (#185): .arb files excluded from coupling analysis

#### Compliance Audit FP Reduction (11,356 → ~50 findings)
- Deep-nesting: threshold 4→6, reset at function boundaries, 3-per-file cap
- Dead-code: skip Go files (handled by AST-based bug-patterns)
- Dynamic-memory: skip garbage-collected languages
- Global-state: exclude regexp.MustCompile, errors.New, sync primitives
- Swallowed-errors: remove overly broad `_ = obj.Method()` pattern
- Eval-injection: skip Go and .github/ directories
- Insecure-random: inline import scanning for crypto/rand vs math/rand; skip import lines
- Path-traversal: skip filepath.Join, HasPrefix comparisons, testdata/
- Non-FIPS-crypto: skip strings.Contains pattern matching
- SQL injection (PCI DSS): add parameterized query detection, #nosec support
- TODO detection: case-sensitive TEMP, skip "Stub:/Placeholder:/Note:" comments, require comment context

#### FTS Empty Query Bug
`FTS.Search("")` returned empty results (early return for empty query). Added `listAll()` method that queries `symbols_fts_content` directly. Fixes `listSymbols` and `searchSymbols("")` returning 0 on MCP.

#### MCP Server Warmup
Changed warmup from `SearchSymbols("", 1)` (cached empty results before SCIP loaded) to `RefreshFTS()` (populates FTS from SCIP without caching search results).

#### IEC 61508 Tree-Sitter Crash
`complexityExceededCheck` bypassed thread-safe `AnalyzeFileComplexity()` wrapper, calling `ComplexityAnalyzer.AnalyzeFile()` directly — SIGABRT when concurrent checks hit CGO.

#### Daemon API Endpoints (7 stubs → implementations)
- Schedule list/detail/cancel via scheduler.ListSchedules()
- Repo list/detail via repos.LoadRegistry()
- Federation list/detail via federation.List()/LoadConfig()
- CLI daemon status: HTTP health query with version/uptime display

#### Query Engine Stubs (4 → implementations)
- Ownership refresh: CODEOWNERS parsing + git-blame analysis
- Hotspot refresh: git churn data with 90-day window
- Responsibility refresh: module responsibility extraction
- Ownership history: storage table query

### Changed
- Score calculation: floor is 0 (not 20), per-rule deduction cap of 10 documented
- `LikelyReturnsError`: removed "Scan" from error patterns, added `singleReturnNew` and `noErrorMethods` maps
- Generated file detection: 20+ new patterns (protobuf, Go generators, Dart/Flutter, GraphQL, bundlers)
- Per-check findings cap (50 max) in compliance engine
- Compliance config: `DefaultDaemonPort` constant replaces hardcoded 9120

### Performance
- `batchGet` with `includeCounts`: parallel reference/caller/callee lookups (10-concurrent semaphore)
- FTS multiplier: 2x → 10x when filters active (handles SCIP struct field flooding)
- MCP index warmup: background `RefreshFTS()` on engine init

## [8.2.0] - 2026-03-21

### Added

#### Unified PR Review Engine (`ckb review`)
A comprehensive code review command that orchestrates 20 quality checks in parallel:

```bash
ckb review --base=main              # Human-readable review
ckb review --base=main --ci         # CI mode (exit 0=pass, 1=fail, 2=warn)
ckb review --base=main --post=123   # Post as PR comment
ckb review --staged                 # Review staged changes
ckb review --checks=secrets,breaking,bug-patterns  # Specific checks only
```

**20 checks:** breaking changes (SCIP), secrets, tests, complexity (tree-sitter), health scoring (8-factor weighted), coupling (git co-change), hotspots (churn ranking), risk scoring, dead code (SCIP + grep verification), test gaps, blast radius (SCIP, framework-filtered), bug patterns (10 AST rules), PR split suggestion, comment/code drift, format consistency, critical paths, traceability, reviewer independence, generated file detection, change classification.

**7 output formats:** human, json, markdown, sarif, codeclimate, github-actions, compliance.

#### Bug Pattern Detection (10 AST Rules)
Tree-sitter-based bug detection with differential analysis (only new issues reported):

- `defer-in-loop` — resource leak from deferred calls in loops
- `unreachable-code` — statements after return/panic
- `empty-error-branch` — `if err != nil { }` with no handling
- `unchecked-type-assert` — `x.(string)` without comma-ok
- `self-assignment` — `x = x` (likely typo)
- `nil-after-deref` — variable used before nil check
- `identical-branches` — if/else with same body
- `shadowed-err` — `err` redeclared with `:=` in inner scope
- `discarded-error` — error return value ignored (with receiver-type allowlist for strings.Builder, bytes.Buffer, hash.Hash)
- `missing-defer-close` — resource opened without defer Close()

All 10 rules validated against known-buggy and clean-code corpus tests.

#### HoldTheLine Enforcement
Findings are post-filtered to only changed lines when `HoldTheLine: true` (default). Pre-existing issues on unchanged lines are suppressed. Test-gap and hotspot findings are exempt (file-level concerns).

#### Multi-Provider LLM Narrative (`--llm`)
Optional AI-powered review narrative that replaces the deterministic summary:

```bash
ckb review --base=main --llm   # Requires ANTHROPIC_API_KEY or GEMINI_API_KEY
```

- Auto-detects provider from environment (Gemini or Anthropic)
- Self-enrichment: CKB verifies own findings via `findReferences` and `analyzeImpact` before sending to LLM
- Triage field on enriched findings (`confirmed`/`likely-fp`/`verify`) guides LLM reasoning
- LLM identifies CKB false positives and deprioritizes framework noise

#### Finding Dismissal Store
Users can dismiss findings by editing `.ckb/review-dismissals.json`:

```json
{"dismissals": [{"ruleId": "ckb/hotspots/volatile-file", "file": "cmd/ckb/daemon.go", "reason": "Expected churn"}]}
```

Dismissed findings are filtered from all future reviews.

#### MCP Tool: `reviewPR`
New MCP tool with compact mode for AI consumers:

```
reviewPR(baseBranch: "main", compact: true)
```

Compact mode returns ~1k tokens instead of ~30k — verdict, non-pass checks, top 10 findings, health summary. Reduces AI assistant context usage by 97%.

Supports `staged`, `scope`, `compact`, `failOnLevel`, `criticalPaths` parameters.

#### Claude Code Skill (`/ckb-review`)
`ckb setup --tool=claude-code` now installs a `/ckb-review` slash command that orchestrates CKB's structural analysis with LLM semantic review. Interactive setup prompts for skill installation.

#### PR Comment Posting (`--post`)
```bash
ckb review --base=main --post=123   # Posts markdown review as PR comment via gh CLI
```

#### CI Integration
- GitHub Actions workflow with SARIF upload, PR comments, and inline annotations
- GitLab CI with CodeClimate report
- GitHub Action (`action/ckb-review/action.yml`)

#### Noise Reduction
- Framework symbol filter for blast-radius (skips variables/constants — works across Go, C++, Java, Python via SCIP symbol kinds)
- Hotspot findings capped to top 10 by churn score
- Complexity findings require +5 cyclomatic delta minimum
- Per-rule score cap (10 points max per ruleId)
- Receiver-type allowlist for discarded-error (strings.Builder, bytes.Buffer, hash.Hash)
- Dead-code grep verification catches cross-package references SCIP misses

### Fixed
- `daemon.go`: `followLogs()` deadlocked on EOF (`select{}` → sleep+poll)
- `daemon.go`: `file.Seek()` error silently ignored
- `handlers_review.go`: `context.Background()` → `context.WithTimeout(r.Context(), 5min)`
- `cmd/ckb/review.go`: err shadow at postReviewComment
- `cmd/ckb/setup.go`: err shadow at promptInstallSkills
- Config merge: `DeadCodeMinConfidence` and `TestGapMinLines` overrides from config file now work (default values no longer block merge)
- Go bumped to 1.26.1 (4 stdlib CVEs)
- gosec findings annotated/resolved across codebase

### Changed
- Version: 8.1.0 → 8.2.0
- Schema version: 8.2
- `complexity.findNodes` exported as `FindNodes` for use by bug-pattern rules
- `LLMConfig` added to config with `Provider`, `APIKey`, `Model` fields
- MCP `reviewPR` tool description updated (20 checks, staged/scope/compact params)
- CLAUDE.md updated with review documentation

### Performance
- Tree-sitter checks serialized with proper mutex discipline (cgo safety)
- Hotspot scores pre-computed once and shared between checks
- Health check subprocess calls reduced ~60%
- Batch git-blame operations for repo metrics

## [8.1.0] - 2026-01-31

### Added

#### Coverage Configuration Options
Coverage file detection is now configurable via `.ckb/config.json`:

```json
{
  "coverage": {
    "paths": ["coverage/custom-lcov.info"],
    "autoDetect": true,
    "maxAge": "168h"
  }
}
```

- `paths`: Custom paths to check for coverage files
- `autoDetect`: Use language-specific auto-detection (default: true)
- `maxAge`: Max age before marking as stale (default: 7 days)

#### Orphaned Index Detection
`ckb doctor` now includes an `orphaned-indexes` check that scans for indexes pointing to repos that no longer exist:

```
$ ckb doctor

✓ orphaned-indexes: Index cache: 234 MB (12 repos), 2 orphaned
  → ckb cache clean --orphaned
```

#### Test Mapping (`ckb affected-tests`)
New command to find tests affected by current changes:

```bash
$ ckb affected-tests

Affected Tests
──────────────────────────────────────────────────────────

Found 8 test files:
  • 5 direct (test references changed code)
  • 3 transitive (test uses affected code)

Run command:
  go test ./internal/query/... ./internal/diff/...
```

**Features:**
- Maps changed symbols to affected test files via SCIP
- Finds corresponding test files by naming convention (e.g., `foo.go` → `foo_test.go`)
- Generates language-appropriate run commands
- `--format=list` for CI integration

#### --include-tests Flag Wiring
The `--include-tests` flag now works end-to-end in `ckb impact diff`:
- Properly sets `IsTest` flag on references based on file path
- Filters test files from changed symbols when `--include-tests=false`

#### Dependency Cycle Detection (`findCycles`)
Detect circular dependencies in module, directory, or file dependency graphs using Tarjan's SCC algorithm:

```bash
# Via MCP
findCycles { "granularity": "directory", "targetPath": "internal/" }
```

- Uses Tarjan's strongly connected components to find real cycles
- Recommends which edge to break (lowest coupling cost)
- Severity classification: size ≥5 = high, ≥3 = medium, 2 = low
- Available in `refactor` preset

#### Move/Relocate Change Type
`prepareChange` and `planRefactor` now support `changeType: "move"` with a `targetPath` parameter:

```bash
prepareChange { "target": "internal/old/handler.go", "changeType": "move", "targetPath": "pkg/handler.go" }
```

- Scans all source files for import path references that need updating
- Detects target directory conflicts (existing files with same name)
- Generates move-specific refactoring steps in `planRefactor`

#### Extract Variable Flow Analysis
`prepareChange` with `changeType: "extract"` now provides tree-sitter-based variable flow analysis when CGO is available:

- Identifies parameters (variables defined outside selection, used inside)
- Identifies return values (variables defined inside, used after selection)
- Classifies local variables (defined and consumed within selection)
- Generates language-appropriate function signatures (Go, Python, JS/TS)
- Graceful degradation: falls back to line-count heuristics without CGO

#### Suggested Refactoring Detection (`suggestRefactorings`)
Proactive detection of refactoring opportunities by combining existing analyzers in parallel:

```bash
suggestRefactorings { "scope": "internal/query", "minSeverity": "medium" }
```

- **Complexity**: High cyclomatic/cognitive functions → `extract_function`, `simplify_function`
- **Coupling**: Highly correlated file pairs → `reduce_coupling`, `split_file`
- **Dead code**: Unused symbols → `remove_dead_code`
- **Test gaps**: High-risk untested code → `add_tests`
- Each suggestion includes severity, effort estimate, and priority score
- Available in `refactor` preset

## [8.0.2] - 2026-01-22

### Added

#### Grok Support in `ckb setup`
Grok is now a supported AI coding tool in the setup wizard:

```bash
ckb setup --tool=grok          # project-level (.grok/settings.json)
ckb setup --tool=grok --global # global (~/.grok/user-settings.json)
```

Uses `grok mcp add` CLI when available, falls back to file-based configuration. Grok's MCP format includes `name` and `transport` fields alongside the standard `command`/`args`.

#### MCP Registry Support
Added `mcpName` field to npm package.json for publishing to the official MCP Registry (`io.github.simplyliz/ckb`).

#### Compound Tool NFR Scenarios
NFR test suite expanded from 28 to 39 scenarios, adding coverage for v8.0 compound tools:
- `explore` (small, large) — aggregates explainFile + searchSymbols + callGraph + hotspots
- `understand` (small, large) — aggregates symbol detail + findReferences + callGraph
- `prepareChange` (small, large) — aggregates impact + affectedTests + coupling
- `batchGet` (small, large) — batch symbol retrieval (up to 50)
- `batchSearch` (small, medium, large) — multiple concurrent searches

### Changed

#### Dynamic NFR Baselines
NFR tests now compare PR results against the base branch (dynamic baseline) instead of static hardcoded values. Two parallel CI jobs run the tests on both HEAD and base, then a comparison job reports regressions. This catches real regressions relative to the target branch rather than drifting static numbers.

#### NFR Tests Scope
NFR tests now only run on PRs targeting `main` (develop → main merges), reducing CI noise on feature branches.

## [8.0.1] - 2026-01-22

### Improved

#### Human-Readable Output by Default
All CLI commands now default to `--format=human` instead of `--format=json`. This makes the CLI more friendly for interactive use while still supporting `--format=json` for scripting and automation.

#### Quieter Indexer Output
External SCIP indexers (scip-go, scip-typescript, etc.) no longer spam stdout during `ckb index`. Output is now captured and only shown on error or when using `-v` verbose mode.

#### Better Error Messages
- `ckb dead-code` now clearly indicates it's for telemetry-based analysis and suggests using `ckb telemetry dead-code`
- `ckb impact diff` no longer shows confusing "Symbol not found: diff" error; instead provides helpful guidance
- Symbol not found errors now suggest using `ckb search` to find valid symbol IDs

#### Index Missing Guidance
`ckb status` now shows helpful guidance when no SCIP index is found:
- Lists commands that work without index (git-based): `hotspots`, `ownership`, `reviewers`, `diff-summary`, `pr-summary`
- Lists commands that need SCIP index: `search`, `refs`, `callgraph`, `impact`, `dead-code`, `trace`, `explain`

### Fixed
- Consistent `--format=human` support for `diff-summary`, `concepts`, and `impact diff` commands

---

## [8.0.0] - 2026-01-21

**Theme:** Reliability, clarity, and compound operations for AI workflows.

### Added

#### Compound Operations (5 New Tools)

Reduce AI tool calls by 60-70% with smart aggregation tools that combine multiple primitives into single, focused operations.

**`explore` — Area Exploration**

Comprehensive exploration of files, directories, or modules. Replaces the common pattern of `explainFile` → `searchSymbols` → `getCallGraph` → `getHotspots`.

```json
{
  "target": "internal/query",
  "depth": "standard",    // "shallow" | "standard" | "deep"
  "focus": "structure"    // "structure" | "dependencies" | "changes"
}
```

Returns: module overview, key symbols (ranked by importance), dependencies, recent changes, hotspots, and drilldown suggestions.

**`understand` — Symbol Deep-Dive**

Complete symbol understanding with ambiguity handling. Replaces `searchSymbols` → `getSymbol` → `explainSymbol` → `findReferences` → `getCallGraph`.

```json
{
  "query": "HandleRequest",
  "includeReferences": true,
  "includeCallGraph": true,
  "maxReferences": 50
}
```

Returns: full symbol detail, explanation, references grouped by file, callers/callees, related tests, and disambiguation info when multiple matches exist.

**`prepareChange` — Pre-Change Analysis**

Impact analysis before modifying code. Combines `analyzeImpact` + `getAffectedTests` + `analyzeCoupling` + risk calculation.

```json
{
  "target": "ckb:repo:sym:abc123",
  "changeType": "modify"    // "modify" | "rename" | "delete" | "extract"
}
```

Returns: direct dependents, transitive impact metrics, related tests, co-change files, and risk assessment with severity levels and mitigation suggestions.

**`batchGet` — Multiple Symbols by ID**

Retrieve up to 50 symbols in a single call. Returns results and errors keyed by symbol ID.

**`batchSearch` — Multiple Searches**

Execute up to 10 symbol searches in one call. Each query can have its own kind filter and scope.

#### SSE Streaming

Real-time feedback for long-running operations via Server-Sent Events.

**Protocol:**
```json
// Request with streaming
{
  "name": "findReferences",
  "arguments": {
    "symbolId": "ckb:repo:sym:abc123",
    "stream": true,
    "chunkSize": 20
  }
}

// Initial response
{
  "streamId": "abc123",
  "streaming": true,
  "meta": { "chunkSize": 20 }
}

// MCP notifications: ckb/streamMeta, ckb/streamChunk, ckb/streamProgress, ckb/streamComplete
```

**Streamable Tools:**
- `findReferences` — Stream references in chunks with progress updates
- `searchSymbols` — Stream symbol search results

**Event Types:**
| Event | Purpose |
|-------|---------|
| `meta` | Stream metadata (total count, chunk size, backends) |
| `chunk` | Batch of items with sequence number |
| `progress` | Phase updates with percentage |
| `done` | Stream complete with summary |
| `error` | Error with code and remediation |

#### Enhanced `getStatus`

System health with actionable remediation guidance.

```json
{
  "backends": {
    "scip": { "status": "available", "latencyMs": 12 },
    "git": { "status": "available" },
    "lsp": {
      "status": "unavailable",
      "reason": "No LSP server configured",
      "remediation": "Configure LSP server in .ckb/config.json"
    }
  },
  "index": {
    "fresh": false,
    "commitsBehind": 3,
    "lastIndexed": "2h ago",
    "symbolCount": 4521,
    "fileCount": 156
  },
  "overallHealth": "degraded",
  "suggestions": [
    "Run 'ckb index' to refresh stale index",
    "Configure LSP for enhanced code intelligence"
  ]
}
```

**Health Tiers:**
- `available` — Backend working normally
- `degraded` — Backend available but with warnings
- `unavailable` — Backend not available, includes remediation

#### `reindex` Tool

Trigger index refresh via MCP with scope control.

```json
// Input
{ "scope": "full", "async": false }

// Output
{
  "status": "action_required",
  "message": "Index is 3 commits behind. Run 'ckb index' to refresh."
}
```

Status values: `skipped`, `action_required`, `started`, `completed`

#### Structured Error Codes

All MCP errors now include actionable remediation guidance.

| Code | When | Remediation |
|------|------|-------------|
| `AMBIGUOUS_QUERY` | Multiple symbols match | Narrow with scope, kind, or more specific name |
| `PARTIAL_RESULT` | Some backends failed | Result incomplete; check backend health |
| `INVALID_PARAMETER` | Bad input | Check parameter format |
| `RESOURCE_NOT_FOUND` | Symbol/file doesn't exist | Verify ID or path |
| `PRECONDITION_FAILED` | Required condition not met | Check index freshness, backend availability |
| `OPERATION_FAILED` | General failure | Check logs, retry |

#### Response Metadata

All tool responses now include structured metadata for AI transparency.

**ConfidenceFactor:** Explains why a confidence score was assigned
```json
{
  "score": 0.85,
  "tier": "medium",
  "factors": [
    { "factor": "scip_exact_match", "weight": 0.9 },
    { "factor": "index_slightly_stale", "weight": -0.05 }
  ]
}
```

**CacheInfo:** Cache hit/miss transparency
```json
{
  "hit": true,
  "tier": "query_cache",
  "age": "45s",
  "key": "findReferences:abc123"
}
```

#### Code Analysis Tools

**`findDeadCode`** — Static dead code detection

Identifies symbols with no references (excluding test files, entrypoints, and interface implementations).

```json
{
  "candidates": [
    {
      "symbolId": "ckb:repo:sym:abc123",
      "name": "unusedHelper",
      "kind": "function",
      "file": "internal/util/helpers.go",
      "confidence": 0.95,
      "reason": "No references found"
    }
  ],
  "excludedReasons": {
    "entrypoint": 12,
    "interface_impl": 8,
    "test_only": 23
  }
}
```

**`getAffectedTests`** — Test coverage mapping

Maps changed symbols to affected test files.

```json
{
  "symbolId": "ckb:repo:sym:abc123",
  "affectedTests": [
    { "file": "auth/handler_test.go", "confidence": 0.95, "reason": "direct_reference" },
    { "file": "api/routes_test.go", "confidence": 0.75, "reason": "transitive" }
  ],
  "runCommand": "go test ./internal/auth/... ./internal/api/..."
}
```

**`compareAPI`** — Breaking change detection

Compares API surface between commits/branches.

```json
{
  "base": "main",
  "head": "HEAD",
  "breaking": [
    {
      "symbol": "ValidateToken",
      "change": "renamed",
      "newName": "ValidateUserToken",
      "affectedCallers": 12
    }
  ],
  "additions": [...],
  "compatible": true
}
```

#### Golden Test Suite

Multi-language test fixtures for regression testing across Go, TypeScript, Python, and Rust.

### Changed

- All MCP tool handlers now use structured `CkbError` instead of raw `fmt.Errorf`
- `getStatus` response includes streaming capabilities info
- Confidence scores now include explanation factors via `FromProvenance()`
- Cached responses include cache tier and age information

### Files Added

**Compound Operations:**
- `internal/query/compound.go` — `Explore()`, `Understand()`, `PrepareChange()`, `BatchGet()`, `BatchSearch()`
- `internal/query/compound_test.go` — Compound operation tests
- `internal/mcp/tool_impls_compound.go` — MCP handlers for compound tools

**Streaming:**
- `internal/streaming/stream.go` — Core Stream type with event sending, heartbeat
- `internal/streaming/chunker.go` — Generic chunking by count and byte size
- `internal/streaming/mcp.go` — MCP notification writer for streams
- `internal/mcp/streaming.go` — StreamingHandler type, registry, wrapForStreaming
- `internal/mcp/tool_impls_streaming.go` — Streaming implementations

**Error Handling:**
- `internal/errors/codes.go` — Error code taxonomy with constructors
- `internal/errors/remediation.go` — Remediation message generation

**Metadata:**
- `internal/envelope/confidence.go` — ConfidenceFactor type and FromProvenance()
- `internal/envelope/cache.go` — CacheInfo type

## [7.6.0]

### Added

#### Real Transitive Impact Analysis
The `analyzeImpact` tool now provides real transitive caller analysis using call graph traversal, replacing the previous stub implementation.

**What's New:**
- **Transitive callers**: See not just direct callers, but callers-of-callers up to depth 4
- **Blast radius summary**: Quick metrics showing module count, file count, unique callers, and risk level
- **Distance tracking**: Each transitive caller includes its distance from the target symbol
- **Confidence decay**: Confidence decreases with depth (0.85 → 0.75 → 0.65 for depths 2/3/4)

**Example Output:**
```json
{
  "blastRadius": {
    "moduleCount": 4,
    "fileCount": 12,
    "uniqueCallerCount": 18,
    "riskLevel": "high"
  },
  "transitiveImpact": [
    { "kind": "transitive_caller", "symbolId": "...", "distance": 2, "confidence": 0.85 },
    { "kind": "transitive_caller", "symbolId": "...", "distance": 3, "confidence": 0.75 }
  ]
}
```

**Blast Radius Thresholds:**
| Level | Criteria |
|-------|----------|
| Low | ≤2 modules AND ≤5 callers |
| High | >5 modules OR >20 callers |
| Medium | Everything in between |

**Usage:**
```bash
# CLI
ckb impact <symbol-id> --depth 3

# MCP
analyzeImpact({ symbolId: "...", depth: 3 })
```

**Files Changed:**
- `internal/impact/types.go` — Added `BlastRadius` struct with classification
- `internal/impact/analyzer.go` — `TransitiveCallerProvider` interface, transitive analysis
- `internal/query/impact.go` — `scipCallerProvider` using SCIP call graph
- `internal/mcp/tool_impls.go` — Added `blastRadius` to MCP output

## [7.5.0]

### Added

#### Change Impact Analysis
Analyze the impact of code changes from git diffs *before* committing. This feature answers: "What downstream code might break?"

**CLI:**
```bash
ckb impact diff                # Analyze working tree changes
ckb impact diff --staged       # Analyze only staged changes
ckb impact diff --base=main    # Compare against a branch
ckb impact diff --depth=3      # Deeper transitive analysis
ckb impact diff --strict       # Fail if index is stale
```

**MCP Tool:** `analyzeChange`

**Key Features:**
- **Git diff parsing** — Uses `sourcegraph/go-diff` to parse unified diffs into structured hunks
- **Symbol mapping** — Maps changed lines to SCIP symbol definitions with confidence scoring
- **Confidence levels** — 1.0 (exact definition), 0.8 (body change), 0.7 (reference), 0.3 (file-level)
- **Aggregated risk** — Weighted factors: symbols changed (20%), direct impact (30%), transitive impact (20%), module spread (30%)
- **Index staleness** — Warns when SCIP index is behind HEAD; `--strict` mode fails if stale
- **Recommendations** — Actionable suggestions (review, test, split) based on analysis

**Files Added:**
- `internal/impact/interfaces.go` — Core types (ChangedSymbol, ParsedDiff, ChangeType)
- `internal/diff/gitdiff.go` — Git diff parser with source file filtering
- `internal/diff/symbolmap.go` — Diff-to-symbol mapper with confidence scoring
- `internal/diff/scipadapter.go` — SCIP index adapter for symbol lookup

**Files Changed:**
- `internal/query/impact.go` — Added `AnalyzeChangeSet()` engine method
- `internal/mcp/tools.go` — Added `analyzeChange` tool definition
- `internal/mcp/tool_impls.go` — Added `analyzeChange` handler
- `cmd/ckb/impact.go` — Added `ckb impact diff` subcommand

See [[Change-Impact-Analysis]] in the wiki for full documentation.

#### Token Efficiency Visibility
Users can now see CKB's token savings compared to bloated MCP servers:

**Startup Banner:**
```
CKB MCP Server v7.5.0
  Active tools: 14 / 76 (18%)
  Estimated context: ~1k tokens
  Preset: core
```

**getStatus Response:**
```json
"preset": {
  "active": "core",
  "exposed": 14,
  "total": 76,
  "estimatedTokens": 1529,
  "fullPresetTokens": 9040,
  "tokenSavingsPercent": 83
}
```

This addresses community feedback about MCP tools consuming 50-80k tokens before conversations even start. CKB's preset system delivers 83% token reduction while maintaining full functionality.

**Preset Discoverability (`--list-presets`):**
```
$ ckb mcp --list-presets

Available presets:

  PRESET        TOOLS         TOKENS  DESCRIPTION
  ------        -----         ------  -----------
  core             14     ~2k tokens  Quick navigation, search, impact analysis (default)
  review           19     ~2k tokens  Code review with ownership and PR summaries
  refactor         19     ~2k tokens  Refactoring analysis with coupling and dead code
  federation       28     ~3k tokens  Multi-repo queries and cross-repo visibility
  docs             20     ~2k tokens  Documentation-symbol linking and coverage
  ops              25     ~2k tokens  Diagnostics, daemon, webhooks, jobs
  full             76     ~9k tokens  Complete feature set (all tools)

Use: ckb mcp --preset=<name>
```

**Future:** Per-tool token breakdown (`--tokens` flag showing individual tool costs) planned for a later release.

**Files Changed:**
- `cmd/ckb/mcp.go` — Multi-line startup banner with token info, `--list-presets` flag
- `internal/mcp/server.go` — Added `EstimateActiveTokens()`, `EstimateFullTokens()` methods
- `internal/mcp/presets.go` — Added `FormatTokens()`, `GetAllPresetInfo()`, `PresetDescriptions`
- `internal/mcp/tool_impls.go` — Token fields in getStatus response

#### Auto Index Updates
Automatic index maintenance across all CKB interfaces—keep your index fresh without manual intervention:

**Watch Mode (CLI):**
```bash
# Watch for changes and auto-reindex (standalone)
ckb index --watch
ckb index --watch --watch-interval 15s

# Watch with MCP server (existing, now configurable)
ckb mcp --watch
ckb mcp --watch --watch-interval 1m
```

**Daemon File Watcher:**
The daemon's file watcher now triggers actual incremental refreshes instead of just logging. When git changes are detected, the daemon queues and executes an incremental update.

**Webhook API:**
```bash
# Trigger incremental refresh via HTTP
curl -X POST http://localhost:9120/api/v1/refresh

# Force full reindex
curl -X POST http://localhost:9120/api/v1/refresh -d '{"full": true}'

# Specify repository
curl -X POST http://localhost:9120/api/v1/refresh -d '{"repo": "/path/to/repo"}'
```

**Response:**
```json
{
  "status": "queued",
  "repo": "/path/to/repo",
  "type": "incremental"
}
```

**Index Staleness Visibility:**
- `ckb status` now shows commits behind HEAD and index age
- MCP `getStatus` response includes `index.commitsBehind`, `index.indexAge`, `index.reason`
- Fresh indexes show ✓, stale indexes show ⚠ with specific reason

**Files Added:**
- `internal/daemon/refresh.go` — RefreshManager for incremental/full reindex
- `cmd/ckb/status_test.go` — Status type tests
- `internal/daemon/refresh_test.go` — RefreshManager unit tests

**Files Changed:**
- `cmd/ckb/index.go` — Added `--watch` and `--watch-interval` flags
- `cmd/ckb/mcp.go` — Added `--watch-interval` flag (min 5s, max 5m)
- `cmd/ckb/status.go` — Added staleness display with commits behind
- `internal/daemon/daemon.go` — Connected file watcher to RefreshManager
- `internal/daemon/server.go` — Added `/api/v1/refresh` endpoint
- `internal/index/metadata.go` — Added `Staleness` type and `GetStaleness()` method
- `internal/mcp/tool_impls.go` — Added index staleness to `getStatus` response
- `CLAUDE.md` — Added "Keeping Your Index Fresh" section

See [[Index-Management]] in the wiki for detailed documentation.

#### Multi-Language Incremental Indexing
Incremental indexing now supports multiple languages via a unified indexer registry:

**Supported languages:**
- Go (scip-go)
- TypeScript/JavaScript (scip-typescript)
- Python (scip-python)
- Dart (scip_dart)
- Rust (rust-analyzer)

**Features:**
- Automatic indexer detection and path resolution (including `~/go/bin`)
- Graceful degradation with install hints when indexer is missing
- Language-specific `SupportsIncremental` flag for safe fallback
- Unified `IndexIncrementalWithLang(ctx, since, lang)` API

**Usage:**
```bash
# Auto-detects language and uses incremental if available
ckb index

# Incremental not available message includes install command
Incremental not available: scip-python not installed (run: pip install scip-python)
```

## [7.4.0]

### Added

#### Tool Presets & Pagination
Token-optimized tool discovery reducing context overhead by up to 83%:

**Presets:**
| Preset | Tools | Tokens | Use Case |
|--------|------:|-------:|----------|
| `core` (default) | 14 | ~1,531 | Essential navigation and analysis |
| `review` | 19 | ~2,294 | Code review: PR summary, ownership |
| `refactor` | 19 | ~2,216 | Refactoring: coupling, dead code |
| `docs` | 20 | ~2,093 | Documentation: coverage, staleness |
| `ops` | 25 | ~2,366 | Operations: jobs, webhooks, metrics |
| `federation` | 28 | ~3,122 | Multi-repo: cross-repo search |
| `full` | 76 | ~9,043 | All tools (legacy behavior) |

**Features:**
- **MCP-compliant pagination** — `tools/list` cursor-based pagination per spec
- **Core-first ordering** — Page 1 always contains functional toolset for non-paginating clients
- **Cursor invalidation** — Cursor rejected when preset or toolset changes
- **`expandToolset` meta-tool** — Dynamic preset expansion with rate limiting (once per session)
- **`tools.listChanged` capability** — Enables dynamic tool list updates

**CLI:**
```bash
ckb mcp                      # Default: core preset (14 tools)
ckb mcp --preset=review      # Code review workflow
ckb mcp --preset=full        # All 76 tools (legacy)
```

**Setup Wizard:**
- `ckb setup` now prompts for preset selection
- `--preset` flag for non-interactive configuration

**Files Added:**
- `internal/mcp/presets.go` — Preset definitions and core-first ordering
- `internal/mcp/cursor.go` — MCP-compliant cursor pagination

**Files Changed:**
- `internal/mcp/server.go` — Preset management and toolset hash
- `internal/mcp/handler.go` — Paginated `handleListTools`
- `internal/mcp/tools.go` — `expandToolset` tool definition
- `internal/mcp/tool_impls.go` — `expandToolset` handler with rate limiting
- `internal/mcp/capabilities.go` — `tools.listChanged: true`
- `cmd/ckb/mcp.go` — `--preset` flag
- `cmd/ckb/setup.go` — Preset selection in wizard

#### Wide-Result Metrics Tracking
Infrastructure for monitoring tool output sizes and truncation rates:

- **`getWideResultMetrics` tool** — Expose wide-result statistics
- **SQLite persistence** — Historical tracking for optimization work
- **Per-tool aggregation** — Invocations, bytes, tokens, truncations
- **Response byte tracking** — Actual JSON payload size for each tool response
- **`ckb metrics` CLI** — View aggregated metrics with `--days`, `--tool`, `--format` flags
- **`ckb metrics export`** — Export versioned metrics to JSON for cross-version comparison

Tracked tools: searchSymbols, findReferences, analyzeImpact, getCallGraph, getHotspots, summarizePr

**Telemetry Findings:**
| Tool | Truncation Rate | Needs Frontier? |
|------|-----------------|-----------------|
| searchSymbols | 45% | Yes |
| getHotspots | 50% | Yes |
| findReferences | 18% | No |
| getCallGraph | 0% | No |
| analyzeImpact | 0% | No |

**Files Added:**
- `internal/mcp/wide_result_metrics.go` — In-memory aggregation with DB persistence
- `internal/storage/metrics_store.go` — SQLite metrics storage
- `cmd/ckb/metrics.go` — CLI metrics command

### Performance

#### SCIP Backend Optimizations
Major performance improvements to the SCIP backend through pre-computed indexes:

| Operation | Before | After | Improvement |
|-----------|--------|-------|-------------|
| FindReferences | 340μs | 2.5μs | **136x faster** |
| SearchSymbols | 930μs | 136μs | **7x faster** |
| FindSymbolLocation | 70μs | 28ns | **2,500x faster** |
| GetCachedSymbol | 210ns | 7.5ns | **28x faster** |

**Changes:**
- **RefIndex**: Inverted reference index built during SCIP load for O(1) reference lookups instead of O(n×m) scans
- **ConvertedSymbols Cache**: Pre-converted symbols avoid repeated parsing of SCIP identifiers, visibility inference, and location lookups
- **ContainerIndex**: Maps occurrence positions to containing symbols for O(1) containment lookup instead of O(n²) nested loops
- **Fast Location Lookup**: `findSymbolLocationFast` uses RefIndex for O(k) definition lookup where k = number of occurrences
- **RateLimiter Cleanup**: Added graceful shutdown with `Stop()` method to prevent goroutine leaks

**Files Changed:**
- `internal/backends/scip/loader.go` — Added `OccurrenceRef`, `RefIndex`, `ConvertedSymbols`, `ContainerIndex` to `SCIPIndex`
- `internal/backends/scip/references.go` — `FindReferences` uses inverted index, added `findContainingSymbolFast`
- `internal/backends/scip/symbols.go` — Added `GetCachedSymbol`, `findSymbolLocationFast`, cached `SearchSymbols`
- `internal/backends/limiter.go` — Added `done` channel and `Stop()` method

**Tests Added:**
- `internal/backends/scip/performance_test.go` — 11 unit tests + 10 benchmarks
- `internal/backends/limiter_test.go` — 5 unit tests + 1 benchmark

#### Git Backend Optimizations
Major performance improvement to `getHotspots` by consolidating git commands:

| Operation | Before | After | Improvement |
|-----------|--------|-------|-------------|
| getHotspots | 26.7s | 498ms | **53x faster** |

**Problem:** For each changed file, ran 4 separate git commands (rev-list, shortlog, log × 2).
With 100+ files = 400+ process spawns.

**Solution:** Single `git log --format=%H|%an|%aI --numstat` command parses all data in one pass.

**Files Changed:**
- `internal/backends/git/churn.go` — Rewrote `GetHotspots` to use single git command

### Added

#### Standardized Response Envelope
All 76 MCP tool responses now include structured metadata in a consistent envelope format:

**Envelope Schema:**
```json
{
  "schemaVersion": "1.0",
  "data": { },
  "meta": {
    "confidence": { "score": 0.85, "tier": "medium", "reasons": [] },
    "provenance": { "backends": ["scip", "git"], "repoStateId": "..." },
    "freshness": { "indexAge": { "commitsBehind": 3, "staleReason": "behind-head" } },
    "truncation": { "isTruncated": true, "shown": 10, "total": 47, "reason": "max-symbols" }
  },
  "warnings": [],
  "suggestedNextCalls": [{ "tool": "findReferences", "params": {...}, "reason": "..." }]
}
```

**Key Features:**
- **Confidence Tiers** — Results scored as high (≥0.95), medium (0.70-0.94), low (0.30-0.69), or speculative (<0.30)
- **Provenance Tracking** — See which backends (SCIP, LSP, git) contributed to results
- **Freshness Info** — Know how stale your index is (commits behind, uncommitted changes)
- **Truncation Metadata** — See how many results were trimmed and why
- **Suggested Next Calls** — AI-friendly drilldown suggestions as structured tool calls
- **Cross-repo Marking** — Federation queries automatically marked as speculative tier

**Files Added:**
- `internal/envelope/envelope.go` — Core types (Response, Meta, Confidence, etc.)
- `internal/envelope/builder.go` — Fluent builder API
- `internal/envelope/confidence.go` — Score to tier mapping
- `internal/envelope/envelope_test.go` — Comprehensive tests
- `internal/mcp/tool_helpers.go` — Convenience wrappers for tool implementations
- `internal/mcp/tool_helpers_test.go` — Tool helper tests

**Files Changed:**
- `internal/mcp/tools.go` — Updated ToolHandler signature
- `internal/mcp/handler.go` — Updated handleCallTool for envelope format
- All `internal/mcp/tool_impls*.go` files — Refactored to return envelope responses

#### Update Notifications
Automatic update checking for all installation methods:

- **GitHub Releases API** — Single source of truth for all install methods (npm, go install, binary)
- **Deferred notification** — Shows at command START from cache (instant, no HTTP during execution)
- **Background refresh** — Cache updated asynchronously for next run
- **24-hour cache** — Checks GitHub at most once per day, stored in `~/.ckb/update-check.json`
- **Smart upgrade message** — npm users see `npm update`, others see GitHub releases URL
- **Protocol-safe** — Skips `mcp` and `serve` commands to avoid breaking protocols

**Disable with:**
```bash
export CKB_NO_UPDATE_CHECK=1
```

**Example output (npm install):**
```
╭─────────────────────────────────────────────────────╮
│  Update available: 7.3.0 → 7.4.0                    │
│  Run: npm update -g @tastehub/ckb                   │
╰─────────────────────────────────────────────────────╯
```

**Example output (go install / binary):**
```
╭─────────────────────────────────────────────────────╮
│  Update available: 7.3.0 → 7.4.0                    │
│  https://github.com/SimplyLiz/CodeMCP/releases      │
╰─────────────────────────────────────────────────────╯
```

#### Hybrid Retrieval with PPR
Graph-based retrieval enhancement using Personalized PageRank:

**Results:**
| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Recall@10 | 62.1% | 100% | +61% |
| MRR | 0.546 | 0.914 | +67% |

**Components:**
- **Eval Suite** — `ckb eval` command measures recall@K, MRR, latency
- **PPR Algorithm** — Personalized PageRank over SCIP symbol graph
- **Seed Expansion** — Expands struct fields to include methods for better cross-module discovery
- **Combined Scoring** — FTS position + PPR score fusion (0.6/0.4 weights)

See Wiki for full documentation.

### Files Added
- `internal/update/check.go` — Core update check logic with npm registry API
- `internal/update/cache.go` — 24-hour cache in `~/.ckb/update-check.json`
- `internal/update/check_test.go` — Tests for version comparison and caching
- `cmd/ckb/eval.go` — Eval CLI command
- `internal/eval/suite.go` — Eval framework with metrics
- `internal/eval/fixtures/*.json` — Test fixtures
- `internal/graph/ppr.go` — PPR algorithm with seed expansion
- `internal/graph/builder.go` — Graph construction from SCIP
- `internal/query/ranking.go` — PPR-based reranking

## [7.3.0]

### Added

#### npm Package Improvements
Better npmjs.com presence and npx reliability:

- **README on npmjs.com** - Package now displays full README on npm registry
- **LICENSE included** - license file bundled with npm package
- **Issue tracker link** - "Report a bug" link on npm page
- **npx sandbox fix** - Node shim auto-detects repo root, fixing #1 support issue

**How the npx fix works:**
The Node.js shim walks up from `process.cwd()` looking for `.ckb/` or `.git/` and sets `CKB_REPO` automatically. This means `npx @tastehub/ckb` now works from subdirectories and MCP clients that don't guarantee working directory.

#### Incremental Indexing v4 (Production-Grade)
Fast, reliable incremental indexing for large codebases:

**Delta Artifacts:**
- **`ckb diff` command** - Generate delta manifests between snapshots
- **CI-generated diffs** - O(delta) ingestion instead of O(N) comparison
- **Delta validation** - Schema version, base snapshot, hash verification
- **`POST /delta/ingest`** - Ingest delta artifacts via API
- **`POST /delta/validate`** - Validate without applying

**FTS5 Search:**
- **SQLite FTS5** - Instant full-text search (replaces LIKE scans)
- **Automatic triggers** - Real-time sync with symbol changes
- **FTS maintenance** - Rebuild, vacuum, integrity-check
- **LIKE fallback** - Graceful degradation for edge cases

**Operational Hardening:**
- **Compaction scheduler** - Automatic snapshot cleanup, journal pruning, FTS vacuum
- **`GET /health/detailed`** - Per-repo metrics, storage info, memory usage
- **`GET /metrics`** - Prometheus metrics (counters, histograms, gauges)
- **Load shedding** - Priority endpoints, circuit breakers, adaptive shedding

#### Language Quality Assessment
Per-language quality metrics and environment detection:

**Quality Tiers:**
- **Tier 1 (Full)** - Go: full support, all features, stable
- **Tier 2 (Standard)** - TypeScript, JavaScript, Python: full support, known edge cases
- **Tier 3 (Basic)** - Rust, Java, Kotlin, C++, Ruby, Dart: basic support, callgraph may be incomplete
- **Tier 4 (Experimental)** - C#, PHP: experimental

**New Endpoints:**
- **`GET /meta/languages`** - Language quality dashboard with tier info, metrics, recommendations
- **`GET /meta/python-env`** - Python venv detection with activation recommendations
- **`GET /meta/typescript-monorepo`** - TypeScript monorepo detection (pnpm, lerna, nx, yarn)

**Environment Detection:**
- Python virtual environment detection (`.venv`, `venv`, `env`, `VIRTUAL_ENV`)
- Python package managers (pyproject.toml, requirements.txt, Pipfile)
- TypeScript monorepo workspaces with per-package tsconfig status

#### Remote Federation Client (v3 Federation Phase 5)
Connect to remote CKB index servers and query them alongside local repositories—transforming federation from local-only aggregation to a distributed code intelligence network.

**Core Features:**
- **Remote Server Management** — Add, remove, enable, disable remote CKB index servers
- **Hybrid Queries** — Search symbols across local federation repos AND remote servers in parallel
- **Source Attribution** — Results show whether they came from "local" or a named remote server
- **Graceful Degradation** — Queries succeed even when some remotes are unavailable

**Caching:**
- Repository list cached for 1 hour
- Metadata cached for 1 hour
- Symbol searches cached for 15 minutes
- Refs and call graph always fresh (not cached)
- Configurable per-server cache TTL

**HTTP Client:**
- Bearer token authentication with environment variable expansion (`${VAR}`)
- Exponential backoff retry logic (max 3 retries)
- Configurable timeouts per server
- Response body limiting (10MB max)

**CLI Commands:**
```bash
# Add a remote CKB index server
ckb federation add-remote <federation> <name> --url=<url> [--token=<token>] [--cache-ttl=1h] [--timeout=30s]

# Remove a remote server
ckb federation remove-remote <federation> <name>

# List remote servers
ckb federation list-remote <federation> [--json]

# Sync metadata from remote server(s)
ckb federation sync-remote <federation> [name] [--json]

# Check remote server status
ckb federation status-remote <federation> <name> [--json]

# Enable/disable remote server
ckb federation enable-remote <federation> <name>
ckb federation disable-remote <federation> <name>
```

**MCP Tools (7 new):**
- `federationAddRemote` — Add a remote server to a federation
- `federationRemoveRemote` — Remove a remote server
- `federationListRemote` — List remote servers in a federation
- `federationSyncRemote` — Sync metadata from remote servers
- `federationStatusRemote` — Get status of a remote server
- `federationSearchSymbolsHybrid` — Search symbols across local + remote
- `federationListAllRepos` — List repos from local and remote sources

**Configuration:**
```toml
[[remote_servers]]
name = "prod"
url = "https://ckb.company.com"
token = "${CKB_PROD_TOKEN}"   # Environment variable expansion
cache_ttl = "1h"
timeout = "30s"
enabled = true
```

#### Authentication & API Keys (v3 Federation Phase 4)
Scoped API key authentication for the index server, enabling secure multi-tenant access with fine-grained permissions.

**Scoped API Keys:**
- **read** — GET requests, symbol lookup, search
- **write** — POST requests, upload indexes, create repos
- **admin** — Full access including token management and deletions

**Per-Repository Restrictions:**
- Limit keys to specific repos using glob patterns (e.g., `myorg/*`)
- Prevents cross-tenant data access in shared deployments

**Rate Limiting:**
- Token bucket algorithm with configurable limits per key
- Returns `429 Too Many Requests` with `Retry-After` header
- Customizable default limits and burst sizes

**Token Management CLI:**
```bash
# Create a new token
ckb token create --name "CI Upload" --scopes write
ckb token create --name "Read-only" --scopes read --repos "myorg/*"
ckb token create --name "Admin" --scopes admin --expires 30d

# List all tokens
ckb token list
ckb token list --show-revoked

# Revoke a token
ckb token revoke ckb_key_abc123

# Rotate a token (new secret, same ID)
ckb token rotate ckb_key_abc123
```

**Token Format:**
- Token: `ckb_sk_` prefix + 64 hex chars (shown once at creation)
- Key ID: `ckb_key_` prefix + 16 hex chars (used for management)
- Secure bcrypt hashing for storage

**Configuration:**
```toml
[index_server.auth]
enabled = true
require_auth = true                    # false = unauthenticated gets read-only
legacy_token = "${CKB_LEGACY_TOKEN}"   # Backward compatibility

[[index_server.auth.static_keys]]
id = "ci-upload"
name = "CI Upload Key"
token = "${CI_CKB_TOKEN}"
scopes = ["write"]
repo_patterns = ["myorg/*"]
rate_limit = 100

[index_server.auth.rate_limiting]
enabled = true
default_limit = 60                     # Requests per minute
burst_size = 10
```

**HTTP Headers:**
- `Authorization: Bearer <token>` — Authentication
- `X-RateLimit-Key: <key_id>` — Rate limit tracking (response)
- `Retry-After: <seconds>` — When rate limited (response)

**Error Responses:**
- `401 Unauthorized` — Missing/invalid/expired/revoked token
- `403 Forbidden` — Insufficient scope or repo not allowed
- `429 Too Many Requests` — Rate limited

**Backward Compatibility:**
- Legacy single-token mode still works via `legacy_token` config
- When `require_auth = false`, unauthenticated requests get read-only access

#### Enhanced Uploads (v3 Federation Phase 3)
Compression support, progress reporting, and incremental (delta) updates for the index upload system. Reduces upload sizes by 70-90% for typical updates.

**Compression Support:**
- **gzip** — `Content-Encoding: gzip` for 60-80% compression
- **zstd** — `Content-Encoding: zstd` for 70-90% compression (faster than gzip)
- Automatic decompression on the server
- Response includes `compression_ratio` showing savings

**Progress Reporting:**
- Logs progress at 10MB intervals for large uploads
- Includes bytes received, MB count, and percentage when Content-Length is known

**Delta Uploads (Incremental):**
- `POST /index/repos/{repo}/upload/delta` — Upload only changed files
- Requires `X-CKB-Base-Commit` header matching current index
- Returns 409 Conflict with `current_commit` if base doesn't match
- Suggests full upload when >50% files changed (configurable)
- Reuses existing incremental infrastructure for efficient processing

**Configuration:**
```toml
[index_server]
enable_compression = true           # Default true
supported_encodings = ["gzip", "zstd"]
enable_delta_upload = true          # Default true
delta_threshold_percent = 50        # Suggest full upload if >N% changed
```

**Delta Upload Example:**
```bash
curl -X POST http://localhost:8080/index/repos/company/core-lib/upload/delta \
  -H "Content-Type: application/octet-stream" \
  -H "Content-Encoding: gzip" \
  -H "X-CKB-Base-Commit: abc123" \
  -H "X-CKB-Target-Commit: def456" \
  -H 'X-CKB-Changed-Files: [{"path":"src/main.go","change_type":"modified"}]' \
  --data-binary @partial-index.scip.gz
```

#### Index Upload (v3 Federation Phase 2)
Push SCIP indexes to the index server via HTTP, eliminating the need for local filesystem paths. This transforms CKB from a "bring your database" model to a centralized index hosting service.

**REST API Endpoints:**
- `POST /index/repos` — Create a new repo ready for upload
- `POST /index/repos/{repo}/upload` — Upload SCIP index file (supports gzip, zstd compression)
- `POST /index/repos/{repo}/upload/delta` — Delta upload (incremental changes only)
- `DELETE /index/repos/{repo}` — Delete an uploaded repo

**Upload Features:**
- Stream large files (100MB+) without memory issues
- Auto-create repos on first upload (configurable)
- Metadata headers: `X-CKB-Commit`, `X-CKB-Language`, `X-CKB-Indexer-Name`
- Full SCIP processing: symbols, refs, call graph extraction
- Compression support: gzip and zstd
- Progress logging for large uploads

**Configuration:**
```toml
[index_server]
enabled = true
data_dir = "~/.ckb-server"      # Server data directory
max_upload_size = 524288000     # 500MB default
allow_create_repo = true        # Allow repo creation via API
enable_compression = true       # Accept compressed uploads
enable_delta_upload = true      # Enable incremental updates
```

**Data Directory Structure:**
```
~/.ckb-server/
├── repos/
│   └── company-core-lib/
│       ├── ckb.db        # SQLite database
│       └── meta.json     # Repo metadata
└── uploads/              # Temp directory for uploads
```

#### Remote Index Serving (v3 Federation Phase 1)
Serve symbol indexes over HTTP for remote federation clients. This enables cross-repository code intelligence without requiring clients to have direct database access.

**Core Features:**
- **Index Server Mode** — New `--index-server` flag for `ckb serve` enables remote index endpoints
- **Multi-Repo Support** — Serve multiple repositories from a single CKB instance
- **TOML Configuration** — Configure repos, privacy settings, and pagination limits via config file
- **Read-Only Connections** — Index server opens databases in read-only mode for safety

**REST API Endpoints:**
- `GET /index/repos` — List all indexed repositories
- `GET /index/repos/{repo}/meta` — Repository metadata and capabilities
- `GET /index/repos/{repo}/files` — List files with cursor pagination
- `GET /index/repos/{repo}/symbols` — List symbols with filtering and pagination
- `GET /index/repos/{repo}/symbols/{id}` — Get single symbol by ID
- `POST /index/repos/{repo}/symbols:batchGet` — Batch get multiple symbols
- `GET /index/repos/{repo}/refs` — List references (call edges) with pagination
- `GET /index/repos/{repo}/callgraph` — List call graph edges with filtering
- `GET /index/repos/{repo}/search/symbols` — Search symbols by name
- `GET /index/repos/{repo}/search/files` — Search files by path

**Security & Privacy:**
- **HMAC-Signed Cursors** — Pagination cursors are signed to prevent tampering
- **Privacy Redaction** — Per-repo controls for exposing paths, docs, and signatures
- **Path Prefix Stripping** — Remove sensitive path prefixes from responses

**CLI:**
- `ckb serve --index-server` — Enable index-serving endpoints
- `ckb serve --index-config <path>` — Load configuration from TOML file

**Configuration Example:**
```toml
[index_server]
enabled = true
max_page_size = 10000

[[repos]]
id = "company/core-lib"
name = "Core Library"
path = "/repos/core-lib"

[default_privacy]
expose_paths = true
expose_docs = true
expose_signatures = true
```

#### Doc-Symbol Linking
Bridge documentation and code with automatic symbol detection:

**Core Features:**
- **Backtick detection** - Automatically detect `Symbol.Name` references in markdown
- **Directive support** - `<!-- ckb:symbol -->` for explicit references, `<!-- ckb:module -->` for module linking
- **Suffix resolution** - Resolve `UserService.Auth` to full SCIP symbol ID with confidence scoring
- **Staleness detection** - Find broken references when symbols are deleted or renamed

**v1.1 Enhancements:**
- **CI enforcement** - `--fail-under` flag for `ckb docs coverage` to enforce minimum coverage in CI
- **Rename detection** - Detect when documented symbols are renamed via alias chain, suggest new names
- **known_symbols directive** - `<!-- ckb:known_symbols Engine, Start -->` allows single-segment detection
- **Fence symbol scanning** - Extract identifiers from fenced code blocks using tree-sitter (8 languages)

**CLI Commands:**
- `ckb docs index` - Scan and index documentation for symbol references
- `ckb docs symbol <name>` - Find docs referencing a symbol
- `ckb docs file <path>` - Show symbols in a document
- `ckb docs stale [path]` - Check for stale references (or `--all` for all docs)
- `ckb docs coverage` - Documentation coverage statistics
- `ckb docs module <id>` - Find docs linked to a module

**MCP Tools:**
- `indexDocs` - Scan and index documentation
- `getDocsForSymbol` - Find docs referencing a symbol
- `getSymbolsInDoc` - List symbols in a document
- `getDocsForModule` - Find docs linked to a module
- `checkDocStaleness` - Check for stale references
- `getDocCoverage` - Coverage statistics

#### Multi-Repo Management
Quick context switching between multiple repositories in MCP sessions:

**Core Features:**
- **Global registry** - Named repo shortcuts stored at `~/.ckb/repos.json`
- **Smart --repo flag** - Auto-detects if argument is a path or registry name
- **Multi-engine support** - Up to 5 engines in memory with LRU eviction
- **Per-repo config** - Each engine loads its own `.ckb/config.json`
- **Repo state tracking** - `valid`, `uninitialized`, `missing` states

**CLI Commands:**
- `ckb repo add [name] [path]` - Register a repository (path defaults to cwd)
- `ckb repo list` - List repos grouped by state
- `ckb repo remove <name>` - Unregister a repo
- `ckb repo rename <old> <new>` - Rename a repo alias
- `ckb repo default [name]` - Get or set default repo
- `ckb repo info [name]` - Show detailed repo info
- `ckb repo which` - Print current repo (for scripts)
- `ckb repo check` - Validate all registered repos

**MCP Tools:**
- `listRepos` - List registered repos with state and active status
- `switchRepo` - Switch active repo context
- `getActiveRepo` - Get current repo info

**Command Flags:**
- `ckb mcp --repo <name>` - Start MCP with specific repo active
- `ckb serve --repo <name>` - Start HTTP server for specific repo

#### Incremental Indexing (Go only)
Index updates in seconds instead of full reindex—O(changed files) instead of O(entire repo).

**Core Features:**
- **Git-based change detection** — Uses `git diff -z` with NUL separators for accurate tracking
- **Rename support** — Properly tracks `git mv` with old path cleanup
- **Delta extraction** — Only processes SCIP documents for changed files
- **Delete+insert pattern** — Clean updates without complex diffing logic
- **Index state tracking** — Tracks "partial" vs "full" state with staleness warnings

#### Incremental Callgraph (v1.1)
Extends incremental indexing with call graph maintenance—outgoing calls from changed files are always accurate.

- **Call edge extraction** — Extracts caller→callee edges during incremental updates
- **Tiered callable detection** — Uses `SymbolInformation.Kind` first, falls back to `().` heuristic
- **Caller resolution** — Resolves enclosing function for each call site via line range matching
- **Location-anchored storage** — Call edges stored with `(caller_file, line, col, callee_id)` for precision
- **Caller-owned edges** — Edges deleted and rebuilt with their owning file (no stale outgoing calls)

#### Transitive Invalidation (v2)
Tracks file-level dependencies and automatically queues dependent files for rescanning when their dependencies change.

- **File dependency tracking** — `file_deps` table tracks which files reference symbols from other files
- **Rescan queue** — `rescan_queue` table with BFS depth tracking and attempt counting
- **Four invalidation modes:**
  - `none` — Disabled (no dependency tracking)
  - `lazy` — Enqueue dependents, drain on next full reindex (default)
  - `eager` — Enqueue and drain immediately with configurable budgets
  - `deferred` — Enqueue and drain periodically in background
- **Budget-limited draining** — `MaxRescanFiles` (default: 200) and `MaxRescanMs` (default: 1500ms) limits
- **Cascade depth control** — `Depth` setting limits BFS traversal (default: 1 = direct dependents only)

**Accuracy Guarantees:**
| Query Type | After Incremental | After Queue Drained |
|------------|-------------------|---------------------|
| Go to definition | Always accurate | Always accurate |
| Find refs FROM changed files | Always accurate | Always accurate |
| Find refs TO changed symbols | May be stale | Accurate |
| Call graph (callees/outgoing) | Always accurate | Always accurate |
| Call graph (callers/incoming) | May be stale | Accurate |

**Automatic Fallback:**
- Falls back to full reindex when >50% files changed
- Falls back on schema version mismatch
- Falls back when no tracked commit exists

**CLI Changes:**
- `ckb index` — Incremental by default for Go projects
- `ckb index --force` — Force full reindex when accuracy is critical

**Configuration (`.ckb/config.json`):**
```json
{
  "incremental": {
    "threshold": 50,
    "indexTests": false,
    "excludes": ["vendor", "testdata"]
  },
  "transitive": {
    "enabled": true,
    "mode": "lazy",
    "depth": 1,
    "maxRescanFiles": 200,
    "maxRescanMs": 1500
  }
}
```

### Files Added

**Incremental Indexing v4:**
- `internal/diff/` - Delta artifact generation
  - `types.go` - Delta JSON schema types
  - `generator.go` - Delta generation (compare two DBs)
  - `validator.go` - Delta validation logic
  - `hasher.go` - Canonical hash computation
- `internal/storage/fts.go` - FTS5 maintenance (rebuild, vacuum, integrity-check)
- `internal/daemon/compaction.go` - Compaction scheduler
- `internal/api/metrics.go` - Prometheus metrics exporter
- `internal/api/middleware_load.go` - Load shedding middleware
- `internal/api/handlers_delta.go` - Delta ingestion endpoints
- `cmd/ckb/diff.go` - `ckb diff` CLI command

**Language Quality:**
- `internal/project/quality.go` - Language quality assessment module
- `internal/api/handlers_quality.go` - Language quality API endpoints

**Remote Federation Client:**
- `internal/federation/` - Remote federation client
  - `remote_types.go` — Response types matching index server API
  - `remote_config.go` — Remote server configuration and env var expansion
  - `remote_client.go` — HTTP client with retry logic and all API methods
  - `remote_cache.go` — Caching wrapper with TTL management
  - `hybrid.go` — Local + remote query merging engine
  - `remote_test.go` — Tests for remote client and configuration
- `cmd/ckb/federation_remote.go` - CLI commands for remote federation
- `internal/mcp/tool_impls_v74.go` - MCP tool implementations for remote federation
- `internal/api/` - Remote index serving and upload
  - `index_config.go` — Configuration types and TOML loading (Phase 3: compression, delta config)
  - `index_types.go` — API response types
  - `index_cursor.go` — HMAC-signed cursor pagination
  - `index_repos.go` — Repository handle management (Phase 1 + 2 + 3)
  - `index_redaction.go` — Privacy redaction logic
  - `index_queries.go` — Database queries for symbols, files, refs, callgraph
  - `index_storage.go` — Server data directory management (Phase 2)
  - `index_processor.go` — SCIP processing pipeline (Phase 2 + 3 delta processing)
  - `handlers_index.go` — HTTP handlers for all index endpoints
  - `handlers_upload.go` — HTTP handlers with compression/progress (Phase 2 + 3)
  - `handlers_upload_delta.go` — Delta upload handler (Phase 3)
  - `handlers_index_test.go` — Tests for cursors, redaction, handlers
  - `handlers_upload_test.go` — Tests for upload, compression, delta (Phase 2 + 3)

**Doc-Symbol Linking:**
- `internal/docs/` - New package for doc-symbol linking
  - `types.go` - Core types (Document, DocReference, StalenessReport, etc.)
  - `scanner.go` - Markdown scanning with backtick/directive/fence detection
  - `resolver.go` - Symbol resolution with suffix matching
  - `staleness.go` - Staleness checking with rename detection
  - `indexer.go` - Document indexing orchestration
  - `store.go` - SQLite persistence for documents and references
  - `coverage.go` - Coverage analysis
  - `fence_parser.go` - Tree-sitter identifier extraction from fences
- `cmd/ckb/docs.go` - CLI commands
- `internal/query/docs.go` - Query engine integration
- `internal/mcp/handlers_docs.go` - MCP tool handlers
- `internal/incremental/` — New package for incremental indexing
  - `types.go` — Core types (FileState, ChangeSet, FileDelta, DeltaStats, CallEdge, TransitiveConfig)
  - `store.go` — SQLite persistence for indexed_files, file_symbols, index_meta
  - `detector.go` — Git-based and hash-based change detection
  - `extractor.go` — SCIP delta extraction for changed files only
  - `updater.go` — Database updates with delete+insert pattern
  - `deps.go` — Transitive invalidation with file dependency tracking and rescan queue
  - `indexer.go` — Orchestration and state management
  - `indexer_test.go`, `deps_test.go`, `types_test.go` — Tests

### Changed
- `internal/federation/config.go` — Added RemoteServers field to Config struct
- `internal/federation/index.go` — Schema v3 with remote_servers, remote_repos, remote_cache tables
- `internal/mcp/tools.go` — Registered 7 new MCP tools for remote federation
- `internal/api/server.go` — Added IndexRepoManager, NewServer now returns error
- `internal/api/routes.go` — Added /index/* route registration
- `cmd/ckb/serve.go` — Added --index-server and --index-config flags
- `internal/storage/schema.go` — Schema v8 with callgraph, file_deps, and rescan_queue tables
- `cmd/ckb/index.go` — Incremental indexing flow with `--force` flag

## [7.2.0]

### Added

#### `ckb setup` - Multi-Tool MCP Configuration
- Interactive setup wizard for configuring CKB with AI coding tools
- Support for 6 AI tools:
  - **Claude Code** - `.mcp.json` (project) or `claude mcp add` (global)
  - **Cursor** - `.cursor/mcp.json` (project/global)
  - **Windsurf** - `~/.codeium/mcp_config.json` (global only)
  - **VS Code** - `.vscode/mcp.json` (project) or `code --add-mcp` (global)
  - **OpenCode** - `opencode.json` (project/global)
  - **Claude Desktop** - Platform-specific paths (global only)
- `--tool` flag to skip interactive menu
- `--npx` flag for portable npx-based setup
- Windows path support for Windsurf and Claude Desktop

#### `ckb index` - Extended Language Support
- Added 5 new languages:
  - **C/C++** via scip-clang with `--compdb` flag for compile_commands.json
  - **Dart** via scip-dart
  - **Ruby** via scip-ruby with sorbet/config validation
  - **C#** via scip-dotnet with *.csproj detection
  - **PHP** via scip-php with vendor/bin check
- Bounded-depth glob scanning for nested project detection
- Language-specific validation and prerequisite checks

#### Smart Indexing
- **Skip-if-fresh**: `ckb index` automatically skips reindexing when index matches current repo state
- **Freshness tracking**: Detects commits behind HEAD and uncommitted changes to tracked files
- **Index metadata**: Persists index info to `.ckb/index-meta.json` (commit hash, file count, duration)
- **Lock file**: Prevents concurrent indexing with flock-based `.ckb/index.lock`

#### `ckb status` - Index Freshness Display
- New "Index Status" section showing freshness with commit hash
- Shows stale reasons: "3 commit(s) behind HEAD", "uncommitted changes detected"
- Displays file count for fresh indexes

#### `ckb mcp --watch` - Auto-Reindex Mode
- New `--watch` flag for poll-based auto-reindexing
- Polls every 30 seconds, reindexes when stale
- Uses lock file to prevent conflicts with manual `ckb index`
- Logs reindex activity to stderr

#### Explicit Analysis Tiers
- User-controllable analysis tiers: **fast**, **standard**, **full**
- CLI flag: `ckb search "foo" --tier=fast`
- Environment variable: `CKB_TIER=standard`
- Config file: Add `"tier": "standard"` to `.ckb/config.json`
- Tier display in `ckb status` shows mode (explicit vs auto-detected)
- Precedence: CLI flag > env var > config > auto-detect

#### `ckb doctor --tier` - Tier-Aware Diagnostics
- New `--tier` flag for tier-specific tool requirement checks
- Shows per-language tool status (installed, version, path)
- Displays missing tools with OS-specific install commands
- Validates prerequisites (go.mod, package.json, Cargo.toml, etc.)
- Accepts both naming conventions: `basic`/`fast`, `enhanced`/`standard`, `full`
- Capability matrix showing which features are available per language
- JSON output with `--format json` for scripting

### Changed

- Tier names rebranded: Basic → **Fast**, Enhanced → **Standard**, Full → **Full**

- Multi-language detection now errors instead of silently defaulting to a language

### Fixed

- Fixed Kotlin indexer URL in documentation
- Fixed PHP indexer URL in documentation

## [7.1.0] - 2024-12-XX

Zero-Friction Operation - CKB v7.1 enables code intelligence without requiring a SCIP index upfront.

### Added

#### Tree-sitter Symbol Fallback
- Symbol extraction for 8 languages (Go, TypeScript, JavaScript, TSX, Python, Rust, Java, Kotlin)
- `searchSymbols` works without SCIP index
- Results include `Source: "treesitter"` and `Confidence: 0.7` for transparency

#### `ckb index` Command
- Auto-detects project language from manifests (go.mod, package.json, Cargo.toml, etc.)
- Checks if SCIP indexer is installed, shows install instructions if not
- `--force` flag for re-indexing, `--dry-run` to preview
- Language-specific troubleshooting tips on failure

#### Universal MCP Documentation
- Setup instructions for Claude Code, Cursor, Windsurf, VS Code, OpenCode, Claude Desktop
- Windows `cmd /c` wrapper instructions

### Files Added
- `internal/symbols/treesitter.go` - Tree-sitter symbol extraction
- `internal/symbols/treesitter_test.go` - Tests for all 8 languages
- `internal/project/detect.go` - Language and indexer detection

## [7.0.0] - 2024-12-XX

### Added
- Initial npm package release via `@tastehub/ckb`
- 58 MCP tools for code intelligence

## [6.5.0] - 2024-12-XX

### Added

#### Developer Intelligence
- **Symbol Origins** — `explainOrigin`: Why does this code exist? Git history, linked issues/PRs
- **Co-change Coupling** — `analyzeCoupling`: Find files that historically change together
- **LLM Export** — `exportForLLM`: Token-efficient codebase summaries with importance ranking
- **Risk Audit** — `auditRisk`: 8-factor scoring (complexity, coverage, bus factor, security, staleness, errors, coupling, churn)

## [6.4.0] - 2024-12-XX

### Added

#### Runtime Observability
- **OpenTelemetry Integration** — `getTelemetryStatus`: See real call counts, not just static analysis
- **Dead Code Confidence** — `findDeadCodeCandidates`: Find symbols with zero runtime calls
- **Observed Callers** — `getObservedUsage`: Enrich impact analysis with production data

## [6.3.0] - 2024-12-XX

### Added

#### Contract-Aware Analysis
- **API Boundary Detection** — `listContracts`: Protobuf and OpenAPI contract discovery
- **Consumer Tracking** — Three evidence tiers for cross-repo dependencies
- **Cross-Repo Impact** — `analyzeContractImpact`: "What breaks if I change this shared API?"
- **Contract Dependencies** — `getContractDependencies`: See consumers and dependencies

## [6.2.0] - 2024-12-XX

### Added

#### Federation & Cross-Repository
- **Federation** — Query across multiple repos organization-wide
- **Federation Tools** — `listFederations`, `federationStatus`, `federationSearchModules`, `federationSearchOwnership`, `federationGetHotspots`
- **Daemon Mode** — Always-on service with HTTP API, scheduled tasks, file watching, webhooks
- **Daemon Tools** — `daemonStatus`, `listSchedules`, `listWebhooks`
- **Tree-sitter Complexity** — `getFileComplexity`: Language-agnostic cyclomatic/cognitive complexity for 7 languages

## [6.1.0] - 2024-12-XX

### Added

#### Production Ready
- **Background Jobs** — Queue long operations, track progress, cancel jobs
- **Job Tools** — `getJobStatus`, `listJobs`, `cancelJob`
- **CI/CD Integration** — `summarizePr`: PR risk analysis, ownership drift detection
- **Ownership Drift** — `getOwnershipDrift`: CODEOWNERS vs actual ownership

## [6.0.0] - 2024-12-XX

### Added

#### Architectural Memory
- **Ownership Intelligence** — `getOwnership`: CODEOWNERS + git blame with time-weighted analysis
- **Module Responsibilities** — `getModuleResponsibilities`: What does this module do?
- **Architectural Decisions** — `recordDecision`, `getDecisions`: ADRs with full-text search
- **Module Annotations** — `annotateModule`: Add module metadata
- **Architecture Refresh** — `refreshArchitecture`: Rebuild architectural model

## [5.2.0] - 2024-12-XX

### Added

#### Discovery & Flow
- **Usage Tracing** — `traceUsage`: How is this symbol reached?
- **Entrypoints** — `listEntrypoints`: System entrypoints (API, CLI, jobs)
- **File Orientation** — `explainFile`: File-level orientation
- **Path Explanation** — `explainPath`: Why does this path exist?
- **Diff Summary** — `summarizeDiff`: What changed, what might break?
- **Architecture Overview** — `getArchitecture`: Module dependency overview
- **Hotspots** — `getHotspots`: Volatile areas with trends
- **Key Concepts** — `listKeyConcepts`: Domain concepts in codebase
- **Recently Relevant** — `recentlyRelevant`: What matters now?

## [5.1.0] - 2024-12-XX

### Added

#### Core Navigation
- **Symbol Search** — `searchSymbols`: Find symbols by name with filtering
- **Symbol Details** — `getSymbol`: Get symbol details
- **References** — `findReferences`: Find all usages
- **Symbol Explanation** — `explainSymbol`: AI-friendly symbol explanation
- **Symbol Justification** — `justifySymbol`: Keep/investigate/remove verdict
- **Call Graph** — `getCallGraph`: Caller/callee relationships
- **Module Overview** — `getModuleOverview`: Module statistics
- **Impact Analysis** — `analyzeImpact`: Change risk analysis
- **System Status** — `getStatus`: System health
- **Diagnostics** — `doctor`: System diagnostics
