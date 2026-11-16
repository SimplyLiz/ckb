# Index overlay: fresh answers between full reindexes

Status: draft, 2026-11-15. No code yet.

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
4. Does `internal/incremental` stay? Its indexer half is replaced by the overlay.
   Whether anything outside the package reads the SQLite tables it writes
   (file symbols, file deps, call edges, rescan queue) has not been checked;
   if nothing does, it goes.
