# Index overlay: fresh answers between full reindexes

Status: Phases 1–3 (Go) and 5 built 2026-11-16 (93b9b24..d38e7ed on develop), Phase 4 (TypeScript) d436fc8. Results below.

## Problem

A SCIP index is exact for the commit it was built at and goes stale with the
first edit after it. Today there are two answers to that, and neither stops the
full indexer from running:

- **Reindex.** `ckb index`, `ckb mcp --watch`, the daemon. Every one of them
  runs the full SCIP indexer.
- **"Incremental" indexing** (`internal/incremental`). It is incremental only
  on the SQLite write side: `IndexIncrementalWithLang` runs the full indexer
  first (`indexer.go`, step 3 `RunIndexer`), then `ExtractDeltas` loads the
  whole new index and copies the changed documents (`extractor.go:41`).
  It saves write time, but the indexer still runs in full.

LIP was supposed to solve this and does not: it links calls across files by
name only, which measured at 2.4 % precision for the new callers it added
(0 % within depth 2). It was taken out of analyzeImpact in 378c884. The goal
behind it still stands: no full reindex after every small edit, without
answers that silently stop matching the code.

## What it costs today

Full index durations from `.ckb/index-meta.json` of the locally indexed repos:

| Repo | Indexer | Files | Duration |
|---|---|---|---|
| ContextCompressionEngine | scip-typescript | 15 | 0.5 s |
| CKB | scip-go + scip-typescript | 1,050 | 3.3 s |
| CognitiveVault | scip-typescript | 569 | 4.1 s |
| Lebenslauf | scip-typescript | 543 | 10 s |
| moltbot | scip-typescript | 3,039 | 10.1 s |
| nyxcore | scip-typescript | 481 | 12.3 s |
| LIP | rust-analyzer | 58 | 15.5 s |
| TasteHub | scip_dart | 339 | 18 s |
| Hollow-Keep | rust-analyzer + scip-typescript | 295 | 46.7 s |

On Go the overlay would save about 3 s. It pays off in TypeScript, Dart and
Rust repos, where a reindex costs 10–47 s of CPU and keeps the index stale for
that long. There is a second gain that the table does not show: code in the
middle of an edit often does not compile, and tree-sitter parses it anyway.
Whether scip-go, scip-typescript and rust-analyzer produce an index for code
that does not compile, and how much of the index survives, has **not been
checked** (see open question 3).

## What already exists

- **Changed-file set since the index commit**: `query/freshness.go`,
  `loadChangedSet` (git diff against `indexedCommit` + untracked files,
  cached 5 s). It already reports `changedSinceIndex` and `affectsResult` per
  result. The overlay applies to exactly this set.
- **Diff hunks**: `impact.ChangedFile` / `impact.ChangedHunk`
  (`impact/interfaces.go:71`). There is no line mapper yet.
- **Tree-sitter declarations**: `symbols.Extractor` (functions, types,
  methods with container and signature). No call sites, except
  `ExtractScriptCalls` for shell.
- **Raw-index consumers**: 28 calls to `SCIPAdapter.GetIndex()` across 20
  files (impact, navigation, deadcode, unwired, cycles, architecture, …). An
  overlay that only wraps the adapter's methods would miss them.

## Options

**A. Background reindex by default.** Turn `--watch` on, debounced. No loss
of precision, little code. The index stays stale for as long as the indexer
runs (10–47 s), the full indexer still burns CPU on every pause in typing, and
when the code does not compile right now the answers stay stale until it does.

**B. Overlay (this document).** SCIP stays the base. Only the files in the
changed set get a layer derived from the diff and tree-sitter, rebuilt in
milliseconds per file. A full reindex still runs, but rarely (on commit, on
idle, or on demand) and is no longer required for correct positions.

A and B do not exclude each other. B shortens A's stale window to zero and lets
A run far less often. The recommendation is B, built in the layers below, with
a measurement after every layer that decides whether the next one gets built.

## Design

### Where it applies: a patched index, not wrapped methods

When the engine loads or reloads the index, and when the changed set changes,
the overlay builds a **new `SCIPIndex` value**. Unchanged documents are shared
with the base, and each changed file's document is replaced by a
synthesized one. The derived structures (call graph, reference maps) are
rebuilt from that value. Loading measured 0.06–0.35 s for the whole index; the
rebuild after a patch should cost less. This needs measuring.

That way all 28 `GetIndex()` consumers and every adapter method see the same
corrected data without being touched. The base index on disk is never
modified.

### Layer 0: line remap from the diff (exact, language-independent)

For each changed file, compute an old→new line map from the hunks:

- Lines outside every hunk move by the hunk offsets above them. Their SCIP
  occurrences keep their symbol IDs and get the new line. **This is
  exact:** the text of those lines did not change.
- Occurrences on deleted or replaced lines are dropped.
- Deleted files: document removed. Renames: document moves to the new path,
  remapped like a modification.

Layer 0 alone fixes the most common staleness after an edit: positions that
have shifted (definition locations, reference lines, `openSites` lines,
enclosing ranges) and references the edit removed. It needs no parser and adds
no guessed edges.

What Layer 0 cannot do: anything on added lines. New declarations, new calls
and changed signatures are missing.

### Layer 1: declarations on added lines (tree-sitter)

Parse the changed file and keep the declarations that start on added lines:

- If name + container + kind match a declaration the base document had in
  this file, it is the same symbol: reuse its SCIP ID and update its range.
  This covers an edited body or signature and a declaration moved within the
  file.
- Otherwise it is a new symbol. Build its ID in the indexer's own scheme from
  the package/module prefix of the file's other symbols (for scip-go:
  `scip-go gomod <module> <version> <pkg>/Name().`). If the prefix cannot be
  determined, give it an ID in a marked `ckb-overlay` scheme, which can never
  collide with a SCIP ID.
- Base declarations whose definition line was removed in Layer 0 and that do
  not reappear: the symbol is gone from this file. **References to it in
  unchanged files stay**: they are exactly the sites that still need to be
  updated, and the done check needs them.

### Layer 2: references on added lines, resolved or omitted

This is where LIP failed, so the rule is the opposite of LIP's: **an
identifier that cannot be resolved gets no edge.** No name bridge into the
whole repo.

Resolution order, for each identifier at a call or reference site on an added
line:

1. Local or file-level declaration in the same file (Layer 1 result).
2. Qualified reference `pkg.Name` where `pkg` is an import of this file. The
   import path gives the package prefix and the base index has the symbol under
   it: exact for functions, types, variables and constants.
3. Unqualified name in the same package (Go) or module (TS/Python with a
   named import): the package's documents in the base index.
4. Method call `x.M()` where the receiver's type is known from a declaration on
   a visible line (`var x T`, a parameter `x T`, `x := T{…}`, `x := NewT(…)`
   with a known return type): `T.M`, including the receiver's embedded types
   via the base index.
5. Anything else (method on an unknown receiver, a chain, an interface value
   whose type is not visible): **no edge**. The count of unresolved sites per
   file goes into the output, so it is visible.

Edges from Layer 2 carry `source: overlay`. Like the semantic callers, they
are listed but **do not count** toward `UniqueCallerCount`, risk or the budget
ranking until a measurement shows their precision.

Per language: Go first (imports are explicit, packages are directories, and an
oracle exists). TypeScript next (explicit imports; the slow indexer is where
the payoff is). Dart and Rust after a measurement.

### Output

- `IndexFreshness` gets `overlay: {files, layers, unresolvedSites}`. The hint
  changes from "reindex" to "N changed files are served from the overlay" as
  long as the overlay covers them.
- Items from Layer 1/2 are marked in JSON (`source`) and compact text (one
  marker per item, as with the LIP sources from 1fc4568).
- `getStatus` and the session-start hook report the overlay state next to the
  index state.

### When the base is rebuilt

- On a HEAD change (commit, checkout), and only when the changed set has grown
  past a threshold (files or share of the repo), in the background as `--watch`
  does now.
- Never just because the working tree is dirty. That is the point.
- A file the overlay cannot parse (a tree-sitter error inside a hunk) falls
  back to Layer 0 for that file and says so.

## Measurement: the oracle is a fresh index

Unlike with LIP, there is an exact oracle in every repo: the SCIP index of the
next commit.

Replay over real history, per repo: for each of the last N commits with a small
diff (≤ 10 files), build the index at the parent, apply the commit's diff as
the overlay, and compare against a full index built at the commit:

- definition locations of all symbols in changed files (Layer 0/1)
- references and callers of the changed symbols (Layer 0/2), precision and
  recall separately
- the analyzeImpact item list and the done check's `openSites`, overlay vs
  fresh, as the end-to-end measure

Gates:
- Layer 0 must match the fresh index **exactly** on unchanged lines. Every
  difference is a bug.
- Layer 2 edges count toward impact only above a precision to be set
  beforehand (proposal: ≥ 95 % within depth 2, the bar the SCIP edges set).
  Below that they stay listed or get dropped.

Repos: CKB (Go), Lebenslauf (TS, replay data already outside the repo), Rubik
(Python, src layout), Hollow-Keep (Rust, the slowest indexer).

## Phases

1. Line mapper from hunks + patched `SCIPIndex` + rebuilding the derived
   structures. Replay harness with Layer 0 against the fresh index on CKB.
2. Layer 1 for Go; replay.
3. Layer 2 for Go; replay; decide whether the edges count.
4. TypeScript, Layers 1–2; replay on Lebenslauf.
5. Rebuild policy (`--watch` semantics change: HEAD-driven, not dirty-driven).

Each phase ships on its own; Phase 1 is useful without the rest.

## Not in scope

- The SQLite tables of `internal/incremental` and FTS. FTS stays as stale as it
  is today; it gets its own entry once the overlay is in place.
- Cross-file effects of a changed signature on unchanged callers (type errors).
  That is the done check's job, and it already gets the old references.
- LIP. No overlay feature builds on it.

## Decisions (2026-11-16)

1. **Rebuild trigger is the overlay's own uncertainty, not a file count.**
   Five changed files can already make the overlay inexact if they hold
   much-used symbols. The overlay knows what it could not do, so it decides:
   rebuild in the background when unresolved Layer 2 sites pass a threshold,
   when a changed declaration has callers in unchanged files and its signature
   changed, when a file fell back to Layer 0 (parse error in a hunk), or on a
   HEAD change. Thresholds come from the replay measurement.
2. **`--watch` becomes the default**, with the HEAD/uncertainty-driven
   semantics above, built together with the overlay.

## Open questions

3. Do scip-go, scip-typescript and rust-analyzer produce an index for code that
   does not compile, and how much of it? This decides how large the second
   gain is, and needs checking before Phase 4.

   **Checked 2026-11-16** (one sample per indexer, one error site per run, raw
   data in `~/Work/Projects/CKB/overlay-measure-2026-11-16/indexer-broken/`).
   Versions: scip-go 0.2.3, scip-typescript 0.4.0, rust-analyzer 1.99.0.
   Repos: SkeletonMapper (Go, 16 docs), ContextCompressionEngine (TS, 21 docs),
   LIP (Rust, 62 docs). Type error = a string assigned to an int-typed local,
   syntax error = an unclosed `if … {` in a function body.

   | Run | Exit | Index written | Docs | Occurrences | Broken file occ. |
   |---|---|---|---|---|---|
   | scip-go clean / type / syntax | 0 / 0 / 0 | yes | 16 | 1,393 / 1,395 / 1,394 | 97 / 99 / 98 |
   | scip-typescript clean / type / syntax | 0 / 0 / 0 | yes | 21 | 11,649 / 11,650 / 11,650 | 653 / 654 / 655 |
   | rust-analyzer clean / type / syntax | 0 / 0 / 0 | yes | 62 | 57,637 / 57,638 / 56,239 | 2,146 / 2,147 / 748 |

   None of the three refuses to index broken code; a type error costs nothing
   in any of them. A syntax error costs what follows the unclosed brace:
   nothing measurable in scip-go, two cross-file references in scip-typescript
   (the function after the brace stopped being an export, so `compress.ts`'s
   import and call of it became `local 0`), and 65 % of the broken file in
   rust-analyzer (1,398 occurrences, 335 symbols; other files' counts
   unchanged). So the "code does not compile" gain is small for Go and TS
   (their indexers already cope) and real only for Rust syntax errors, which is
   also where tree-sitter's error recovery is the alternative. One sample each;
   good enough to order the work, not to quote as a rate.

4. Does `internal/incremental` stay? Its indexer half is replaced by the overlay.
   Whether anything outside the package reads the SQLite tables it writes
   (file symbols, file deps, call edges, rescan queue) has not been checked;
   if nothing does, it goes.

   **Checked 2026-11-16** (grep over SQL text in `cmd/` and `internal/`, CKB
   `prepareChange` on `internal/incremental/store.go`: all 20 affected uses are
   inside the package). Row counts in CKB's own `.ckb/ckb.db`: callgraph
   59,285, indexed_files 1,014, file_symbols 22,449, file_deps 4,950,
   rescan_queue 0.

   - `file_symbols`, `file_deps`, `rescan_queue`: no reader outside
     `internal/incremental`. Only the schema (`storage/schema.go`) names them.
   - `indexed_files`, `callgraph`, `index_meta`: read by the remote index
     server in `internal/api` (`index_repos.go:113` opens `.ckb/ckb.db`
     read-only; `index_queries.go:266,337,434,602` serve
     `/index/repos/:repo/files` and `/callgraph`, `index_repos.go:290–334` the
     meta and counts). That server is what federation's remote client talks to
     (`internal/federation/remote_client.go`).
   - `ckb diff` (`internal/diff/generator.go:236`) also selects from
     `callgraph`, but asks for `caller_file_id` and `language`, which neither
     the local schema nor the upload schema (`index_processor.go:460`) has. On
     a copy of CKB's own DB it fails: `failed to load new call graph: SQL logic
     error: no such column: caller_file_id`. It reads nothing today.
   - Writers outside the package: `ckb index` (`cmd/ckb/index.go:542`,
     `populateIncrementalTracking` after every full index) and the daemon
     (`internal/daemon/refresh.go:170,376`).

   Proposal (Lisa's call, nothing removed): the incremental *indexer* path
   (`IndexIncrementalWithLang`) and the three unread tables go once the
   overlay is in; `indexed_files` + `callgraph` + `index_meta` stay as long as
   the remote index server exists, filled by the populate step after a full
   index, and federation is behind its preset with no new investment, so
   dropping the server would let all of it go. `ckb diff` is broken either way.

## Results (2026-11-16)

Replay harness and raw data: `~/Work/Projects/CKB/overlay-measure-2026-11-16/replay/`
(outside the repo; `main.go` is built into the module with `go build -overlay`). The last 30
first-parent commits on develop with at most 10 changed files and at least one Go file: index
built with scip-go 0.2.3 at the parent, the commit's diff served as the overlay (a checkout of
the commit, index at the parent), compared with an index built at the commit. Symbols are
compared without the package version (scip-go writes the commit into it) and without the
number of a local.

| Measure | Layer 0 | + Layer 1 | + Layer 2 | + types |
|---|---|---|---|---|
| Occurrences on unchanged lines that differ (of 130,951) | 0 | 0 | 0 | 0 |
| Definitions in changed files at the fresh index's place (of 7,669) | 7,501 | 7,669 | 7,669 | 7,669 |
| Function extents that differ (of 1,679) | 1 | 0 | 0 | 0 |
| Overlay references identical to fresh (symbol, role, range) | – | – | 1,343/1,343 | 1,411/1,411 |
| References to the changed symbols found (of 1,122) | 703 | 703 | 1,044 | 1,051 |
| Callers of the changed symbols found (of 797) | 534 | 535 | 761 | 768 |
| analyzeImpact items (depth 2) only Layer 2 brings, identical | – | – | 940/946 | 956/962 |
| Unresolved sites (no edge) | – | – | 888 | 820 |
| Overlay build, median / max | 31 / 98 ms | 28 / 49 ms | 39 / 71 ms | 39 / 89 ms |

- **Layer 0 gate: met.** No difference on unchanged lines; the one extent difference was `git diff`
  pairing one function's `}` with another's (identical lines), which Layer 1's extents fix.
- **Layer 2 gate (≥ 95 % within depth 2): met on CKB, so the edges count** toward callers, risk
  and the budget. The 6 items that are not identical are the same symbols the fresh index has,
  reached transitively because a direct reference stayed unresolved. Precision of the references
  themselves: 100 %, 0 at a position where the fresh index has nothing. One repo, one language.
- `PatchIndex` against a rebuild of the derived structures on CKB's own index (1,014 documents,
  12 replaced): 29 ms vs 192 ms, equal in every structure.
- Done check: `AnalyzeChangeSet` pins the base (below), so the overlay does not change it.
  Base vs fresh differed in one commit (dc4c1f9a), which is the old-side/new-side difference the
  done check already had.

Rebuild trigger (decision 1): any changed declaration line whose symbol unchanged files use, a
changed file the overlay can only remap, a new file it lacks, or 25 or more unresolved sites
(up to 20 the impact recall stayed ≥ 94 %; from 25 on it fell below 95 % in 10 of 14 commits).
Over the 30 commits (each as one overlay) 17 need no rebuild, all with ≥ 96 % of the changed
symbols' references found; every commit under 95 % triggers one.

### TypeScript (Phase 4, 2026-11-16)

Harness: `~/Work/Projects/CKB/overlay-measure-2026-11-16/replay-ts/` (the Go harness with
scip-typescript 0.4.0 `index --infer-tsconfig`, the main checkout's `node_modules` linked into
every checkout). Lebenslauf, the last 30 first-parent commits with at most 10 files and at
least one `.ts`/`.tsx` file (2026-11-12 to 16), 543 indexed files.

scip-typescript numbers two kinds of IDs with counters: object-literal properties
(`experience0:`) and type-literal members (`Caret#typeLiteral40:entry.`), the latter from the
file's local counter. Any local added above them renumbers them, and the counter can be
advanced by another file's indexing, so no per-file parser reproduces them. Layer 1 leaves them
out; Layer 0 keeps their old numbers, consistently at the definition and at every use (also in
unchanged files), so the graph stays connected under the old name. The replay compares symbols
with those numbers normalized.

| Measure | Result |
|---|---|
| Layer 1 alone against the index at HEAD (539 files that parse, every comparable definition) | 6,138/6,138 symbol, range and EnclosingRange identical; file ranges 539/539 |
| Of all global definitions there, synthesized / counter IDs left out | 6,138 / 9,133 (7,621 object props, 1,512 type-literal members) |
| Occurrences on unchanged lines that differ (of 107,916), counters normalized | 138: 120 counter IDs whose name changed with them, 15 locals and 3 parameters whose symbol changed because a declaration or type on another line changed |
| Definitions with stable IDs in changed files at the fresh index's place | 4,045/4,057, 0 moved; the 12 are in the one changed file tree-sitter cannot parse |
| Overlay references identical to fresh (symbol, range) | 1,147/1,147, 0 where fresh has nothing |
| analyzeImpact items only Layer 2 brings, identical | 543/543 (3 lost) |
| References to the changed symbols found | 1,414/1,846 (77 %) |
| Callers of the changed symbols found | 511/749 (68 %) |
| Unresolved sites (no edge) | 1,614 |
| Overlay build, median / max | 47 / 92 ms |

The 40 references and 67 impact items the overlay has beyond the fresh index all belong to one
symbol in one commit, ``Sep().(`{ text, between }`)typeLiteralN:between.``: scip-typescript
writes a destructured parameter's text into the ID, the commit changed that text, and the uses
in unchanged files keep the old ID — the sites a done check needs, under the name the base had.

Rebuild trigger with the Go thresholds: 10 of 30 commits need no rebuild, all with 100 % of the
changed symbols' references found; every commit under 95 % triggers one (two of them under the
unresolved threshold, through a file that got Layer 0 only). The edges count toward impact like
Go's: precision 100 % on references and items.

Design facts:
- **tree-sitter, positions in UTF-16** (TypeScript's character offsets). The smacker grammar
  in go.mod (2024-08) rejects `typeof import("x")` types and `using` as an identifier: 12 of
  551 Lebenslauf files do not parse. A file with any syntax error stays at Layer 0, because the
  grammar's error recovery reads a function body's locals as top-level declarations
  (`Library.tsx`).
- **Renamed TS files stay at Layer 0 and ask for a rebuild**: every ID carries the path.
- **The file symbol is pinned at [0,0,0]**: Layer 0 moved it when a line was inserted above the
  first one and dropped it when the first line changed.
- **Layer 2 parses the files it resolves into** (exports, re-exports up to three levels,
  default exports, which top-level names are values or types); the index alone cannot tell a
  class from an interface. Module resolution: relative specifiers and `compilerOptions.paths`
  of the root `tsconfig.json` (`extends` not followed), extensions and `index` as with
  `moduleResolution: bundler`.
- Resolved: same-file top-level names not shadowed by any enclosing binding (conservatively:
  a local of the name in either namespace shadows), imported names and their module strings,
  `ns.x`, enum members, static members, `new C()` → ``C#`<constructor>`().``. Counted as
  unresolved: members of values whose type is not spelled, JSX attributes of repository
  components, import aliases. Not counted: locals, globals, names from packages.

#### Members of spelled types (2026-11-16, after Phase 4)

The open point "members of typed locals" was framed by the 1,614 unresolved sites. The 432
references Layer 2 missed are a different set: 372 of them name a counter-numbered symbol
(`typeLiteralN`), 4 a plain member of a typed binding. Layer 0 keeps a type literal's old
numbers, so a use on an added line can point at the number the declaring file's document has
(unique match on `owner#typeLiteral<digits>:name.`). Built: `x.m` for a parameter or local
declared `T`, `T<…>`, `T | undefined | null` (through `!`, `?.`, parentheses), `(v as T).m`;
JSX attributes through the component's first parameter (an object type in place, under the
parameter's ID, or a named type); keys of `const x: T = { … }` and `{ … } as T`. Only own
members of an interface, class or object-type alias; inherited members, unions of two
repository types and type parameters stay unresolved. Members of primitives and package types
no longer count as unresolved.

| Measure | d436fc8 | now |
|---|---|---|
| Overlay references identical to fresh | 1,147/1,147 | 1,303/1,303 |
| Impact items only Layer 2 brings, identical | 543/543 | 556/556 |
| References of the changed symbols found | 1,414/1,846 | 1,424/1,846 |
| Callers found | 511/749 | 518/749 |
| Unresolved sites | 1,614 | 1,421 |
| Commits without rebuild (threshold 25), all 100 % recall | 10 | 11 |
| Overlay build, median | 47 ms | 51 ms |

Small on recall because 383 of the 422 references still missing belong to symbols that have no
definition in the overlay at all: type-literal members and destructured parameters whose
declaration line the commit changed. Layer 0 drops the old number with the line, and Layer 1
cannot know the new one. Two commits that need a rebuild (86 % and 43 % recall) now sit at 27
unresolved sites, two above the threshold.

#### New type-literal members: placeholder numbers (2026-11-16, Lisa's go)

Layer 1 now defines a type literal's members on new lines under scip-typescript's owner form
(`T#`, `f().(p)`, ``f().(`{ a, b }`)``, `f().` for the return type, `f().[E]` for a
constraint): the number is the one the owner has among the base document's and the overlay's
definitions when that is exactly one (the uses Layer 0 kept, also in unchanged files, carry
it), else a placeholder above the file's highest. scip-typescript writes no definition for a
destructured parameter itself, only for its type's members, so neither does Layer 1. Layer 2
points destructured parameter names (`{ a }: T`, `{ a }: { a: X }`) and members of a parameter
with an object type (`l: { url }` → `l.url`) at them. A generic of a global or package type over
a repository type (`Record<K, { … }>`, `Partial<T>`) counts as unresolved instead of outside.

| Measure | 0e34443 | now |
|---|---|---|
| Overlay references identical to fresh (counters normalized) | 1,303/1,303 | 1,386/1,386 |
| Impact items only Layer 2 brings, identical | 556/556 | 761/784, the 23 others the same symbol fresh reaches directly |
| References of the changed symbols found | 1,424/1,846 (77 %) | 1,506/1,846 (82 %) |
| Callers found | 518/749 | 586/749 |
| Commits without rebuild, all 100 % recall | 11 | 11 |
| Overlay build, median | 51 ms | 52 ms |

First try reused only numbers Layer 0 had kept: where the commit rewrote a literal's line, the
new definition got a placeholder while the uses elsewhere kept the old number, splitting the
symbol (d795b9d7: 84 → 63 references). Reading the base document's numbers fixed it. The
`Record` rule came from 3664d533: 75 missing references to a new `Record<…, { Icon; label }>`
constant's members, 23 unresolved sites, no rebuild at 49 % recall; with the rule it rebuilds.
Definition places of counter IDs move by ±30 between runs of the same code (the replay
compares them normalized), stable definitions stay at 0 moved.

#### Types from inference and context; eight repositories (2026-11-16, evening)

The open points of the placeholder step (context-typed keys, call results, callback
parameters, inherited members) shared one missing piece: the type of an expression the syntax
does not annotate. Layer 2 now has a small type engine (`typescript_types.go`), exact or nothing:

- **Expression types**: annotations; initializers (`const x = await load()`); calls of
  repository functions with a spelled return type, or the join of their `return` statements
  (`find(…) ?? xs[0]`); `await`, `new C()`, `this` in a top-level class; `x[i]` of an array or
  record; destructured bindings (the member of the pattern's type); callback parameters (array
  methods, parameters of repository functions, zustand selectors).
- **Contexts** for object literals and function expressions: a variable's annotation,
  `as`/`satisfies`, an argument (also of `new C()`), a default value or field initializer, a
  return statement or arrow body, an enclosing key, an array element, `?:`/`??`/`||`, zustand's
  `create<T>()(init)` through `persist`/`devtools`/`immer`.
- **Members** in the declaring interface, class or object type, else in the types it
  `extends` (an inherited member's symbol is the base's).
- **Layer 1** names the members of a type literal of a top-level variable (`x.`), an interface
  property (`T#m.`) and a type argument (`x.Record:`, `f().Promise:`), the owners the index
  has and Layer 1 did not.
- **Grammar**: `using` as an identifier and `typeof import("…")` are rewritten to same-length
  tokens before parsing (`tsParseText`), so offsets and node texts stay those of the source.
  Grammar v0.23.2 (the newest; upstream has not moved since) fails on both too, and breaks
  tagged templates with type arguments. All 547 Lebenslauf files parse.
- **tsconfig**: the root `tsconfig.json`'s project and the projects it references,
  `extends` followed (relative, a package's config, TypeScript 5 arrays; paths resolve against
  the config that sets them, `baseUrl` against its own). A package's tsconfig nothing references
  is not read, because `ckb index` runs scip-typescript once at the root (`index
  --infer-tsconfig`) and the indexer never reads it either. The index cannot say how it was
  built: scip-typescript records no arguments.
- **Workspace packages** (`package.json` workspaces, `pnpm-workspace.yaml`) resolve to their
  files through `exports` (conditions `types`, `import`, `default`, `*` patterns), `types`,
  `typings`, `main`; the document's own file symbol gives the package-named IDs
  (`npm @madison/spur 0.1.0 src/…`).

The replay alone was too narrow. Running Layer 2 over every line of eight TypeScript
repositories against their own indexes (harness in
`overlay-measure-2026-11-16/oracle-l2/`, files whose source is not the indexed one skipped)
found rules that hold in scip-typescript and not in TypeScript's own checker:

- scip-typescript resolves an object literal's key through the contextual type's
  `getProperty`, and a union has none. So a literal whose context is `T | undefined` defines its
  own properties: optional and defaulted parameters (`p: T = {}` is `T | undefined` to a caller),
  `T | null` returns, every return of an async function (`T | PromiseLike<T>`), optional
  members. A member access looks through the same unions.
- A shorthand property (`{ a }`) is the contextual member when there is one, else the value,
  and the value of an imported name is a local in the index (Lebenslauf `schema: { user }`,
  nyxcore, moltbot). In a package's or a global's type the parse cannot tell whether the
  property exists: no edge, counted.
- A lower-case JSX tag is `JSX.IntrinsicElements`' member even when a value of that name is
  imported (`<table>` beside `import { table }`), wrong since Phase 4.
- `await import("m")` destructuring points at the module's declaration; a name the module
  re-exports (a barrel) is a local in the index.
- `filter((x): x is U => …)` narrows the element type: unknown.

| Repository | Edges before (e7c4455) | wrong | Edges now (09fac5f) | wrong |
|---|---|---|---|---|
| Lebenslauf | 12,870 | 27 | 15,305 | 0 |
| moltbot | 34,505 | 21 + 30 | 48,734 | 3 + 30 |
| FrugalRoute | 1,104 | 4 | 2,072 | 7 |
| ContextCompressionEngine | 40 | 0 | 75 | 0 |
| CognitiveVault | 689 | 0 | 1,018 | 0 |
| nyxcore | 6,414 | 1 | 9,236 | 0 |
| archreview | 2,514 | 0 | 3,466 | 0 |
| Madison-Server | 6,220 | 26 | 10,489 | 0 |

The 40 left (moltbot, FrugalRoute) sit on lines changed after the index was built (moltbot's
`Clawdbot`→`OpenClaw` rename, a test file one day younger than FrugalRoute's index): the
source filter checks definitions and non-local references, not locals. Unresolved sites on the
same files fell from 64,654 to 30,686.

Lebenslauf replay (30 commits, counters normalized):

| Measure | e7c4455 | now |
|---|---|---|
| Overlay references identical to fresh | 1,386/1,386 | 1,968/1,968 |
| Impact items only Layer 2 brings, identical | 761/784 | 1,093/1,120 (25 the same symbol reached transitively, 2 a renamed component's references Layer 0 kept) |
| References of the changed symbols found | 1,506/1,846 (82 %) | 1,765/1,846 (96 %) |
| Callers found | 586/749 | 710/749 |
| Unresolved sites | 1,419 | 648 |
| Commits without rebuild, all at 100 % recall | 11 | 19 |
| Overlay build, median | 52 ms | 40 ms |

The 81 references still missing: a function whose destructured parameter's text changed
(`Sep`, 38: the owner's ID carries the pattern text, so every use in an unchanged file names the
old one until the rebuild the changed signature asks for), discriminated unions
(`target.kind === "fassung" && target.last`), generic callbacks (`edit(id, (s) => s.x)`), and
members of values typed by a worker's or a parser's untyped result.

### What the build changed in the design

- **Go's Layers 1–2 use go/parser, not tree-sitter.** Exact for Go, positions in scip-go's byte
  columns, partial ASTs; build constraints decide which new files count, as for scip-go.
- **Layer 2 reads types from the declaring files.** The index has no types (scip-go's
  SymbolInformation carries none, and the loader drops signature documentation), so the type
  of `x.f`, of `f()` and promoted members comes from the declaring file's source, found
  through the index. Exact or nothing still holds.
- **Diff-driven analyses read the base.** The diff mapper and the done check read references
  as old-side lines and check them against the hunks' text; overlay positions would be
  misread. `AnalyzeChangeSet` pins the base for its duration (`SCIPAdapter.PinBase`).
- **An index built from a dirty tree records it** (`worktreeTree` in index-meta.json, a git tree
  written through a temporary git index), and the overlay diffs against that tree. Without it
  the overlay applied the uncommitted edits a second time; with watch rebuilding on dirty
  trees that would have been the normal case.
- **`--watch` by default** runs beside the startup paths in repos with `.ckb`; an explicit
  `--watch` keeps building a missing index anywhere.

### Closed after the first build (2026-11-16)

- Transitive impact items reached only over an overlay edge carry `source: overlay`: the
  caller index records which edges only overlay references produce, and an item at depth 2+
  is marked when the indexer's edges alone do not reach it within the depth.
- A new Go package takes its import path from the nearest `go.mod` and the module's version
  field from a package of that module in the index. Checked against scip-go on a scratch
  module (a new library package and a new `package main`): definitions identical; scip-go
  writes the imported package symbol twice per import (path and qualifier), the overlay once.
  External test packages (`_test`) stay uncovered.
- `changedSinceIndex` lists against the worktree snapshot of a dirty-built index.
- A modified Go file a build constraint now excludes leaves the index like a deleted file; its
  symbols used in unchanged files count as changed declarations (a rebuild reason).

### Open

- TypeScript: discriminated-union narrowing, generic functions' callbacks, the owner rename of
  a destructured parameter (rewriting unchanged documents' uses instead of a rebuild); repos
  indexed per workspace package (`--pnpm-workspaces`) would need the nearest tsconfig again, and
  the index does not say which way it was built.
- Replays on other repos (Lebenslauf, Rubik, Hollow-Keep) once their languages have layers.
