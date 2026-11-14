# eval-change

Does `ckb changes` (the assessChange analysis) name the files a signature change
breaks? This takes real commits that changed a function and updated its callers,
hides the callers, and checks what CKB predicts. `git grep` on the same name is the
baseline, because that is what an agent gets for free.

The current numbers are in `docs/eval/change-prediction.md`; the raw output behind them
is `results/ckb.jsonl`, and `results/ckb-b550825.jsonl` is the first measurement.

## Run it

Python 3 stdlib. Needs `git`, a `ckb` binary and the language's SCIP indexer.

```bash
go build -o /tmp/ckb ./cmd/ckb

# The sample is fixed before anything is measured. samples/ckb.json is the
# committed sample for this repo; regenerate only to measure a different one.
python3 scripts/eval-change/eval.py select --repo . --lang go --head <sha> \
    --count 12 --spare 8 --name ckb --out scripts/eval-change/samples/ckb.json

python3 scripts/eval-change/eval.py run --sample scripts/eval-change/samples/ckb.json \
    --ckb /tmp/ckb --ckb-commit "$(git rev-parse --short HEAD)" --out /tmp/ckb.results.jsonl

python3 scripts/eval-change/eval.py report --results /tmp/ckb.results.jsonl
```

`select --exclude <sample>` skips commits an earlier sample already used, and
`--must-compile` keeps only commits whose parent passes the compiler, in sampling order,
because a commit without a working compiler has no compiler truth. It runs the repo's
build scripts.

To judge a fix: build the fixed binary, `run` the same sample into a new results
file, `report`, compare. `run` resumes an existing results file, so start a new one.

Other repos: `--index-lang` sets `ckb index --lang`, and `--prep` runs a command in
the clone before each index (`pnpm install ...`). For a private repo, keep the sample
and results outside this checkout, and use `report --redact`: they contain symbol and
file names.

`run` works in one clone under a temp dir, with `CKB_HOME` and `CKB_NO_UPDATE_CHECK`
set, and never touches the source repo beyond cloning it. Run it from the repo root:
`samples/ckb.json` names the repo as `.`. Indexes run one at a time, each waiting for
`--min-free-gb` of reclaimable memory. The temp dir goes away at the end unless `--keep`.

SWE-Bench ProMax has no license on its card or in its repository, so it isn't vendored.
With a local copy of `swe-bench-promax.json`, `promax` applies the same selection rule to
each Go, TS and Rust instance and writes one sample per repo, plus `outcomes.json`:

```bash
python3 scripts/eval-change/eval.py promax --data /path/to/swe-bench-promax.json --work /tmp/promax
```

## What it measures

For each sampled commit: index at the parent, write only the file(s) defining the
symbol from the commit, run `ckb changes --format json`.

| | |
|---|---|
| predicted | files of `affectedSymbols`, minus the applied files |
| truth (all) | other non-test source files modified in the commit |
| truth (mention) | those whose changed lines mention the symbol |
| truth (compiler) | files with more diagnostics once only the defining file is applied: `gopls check`, `tsc`, `cargo check` |
| baseline | `git grep -l -w <symbol>` at the parent, same filter |

A last step puts the whole commit on the parent, over the same index, and runs it
again: the commit counts as complete, so each `doneCheck` open use is a candidate false
alarm, and a new compiler run decides it.

Also: whether `changedSymbols` named the symbol, the risk level, test files via
`affectedTests`, a second run with `budget.maxImpactItems` lifted, and a class for
every missed file, from the index's own `ckb refs`.

Tools in the report, all scored the same way:

| | |
|---|---|
| `ckb` | files of `affectedSymbols`, the budgeted item list. The original measure |
| `ckb_direct_only` | those at distance 1 |
| `ckb_uncapped` | `ckb` with the item budget lifted |
| `ckb_files` | `consumerFiles.direct` and `.transitive`, from a binary that has them |
| `ckb_files_direct` | `consumerFiles.direct` |
| `ckb_contract` | `consumerFiles.contractChanged`: direct users of a symbol whose declaration changed or went away, at a use the change left as it was (older results: files outside the change) |
| `ckb_open` | non-test files of `doneCheck.openSites`, the check of each use |
| `grep` | `git grep -l -w <symbol>` |

Results from an older binary still report, without the `consumerFiles` rows.

## What it can't tell you

- **Rust compiler truth is a lower bound.** `cargo check` stops at a crate with errors,
  so if the defining crate no longer builds alone, its dependents report nothing, and rustc
  stops at resolution errors before type-checking. Each Rust record therefore also has
  `broken_deep`: the commit's version of each blamed file goes in and the compiler runs
  again, up to three rounds, counting only files the commit edits. `report --deep-truth`
  scores against it. Pass `--check` and `--check-prefix` for a nested manifest. `gopls` and
  `tsc` don't have this problem.
- **Truth (mention) favors grep by construction.** The sample requires a changed line
  naming the symbol, and a file with that line almost always contains the word, so
  grep recall there is about 100% before anything runs. Consumers that break without
  naming the symbol (through a type, an interface or a re-export) only show up in the
  compiler truth. It also counts new callers and same-word locals as misses.
- **Truth (all) contains unrelated edits.** A commit also touches files for other
  reasons, which caps recall below 100% for every tool.
- **Precision is indicative only.** A consumer not edited in that commit isn't wrong:
  an added optional parameter breaks nobody and may still be a correct warning. The
  commit shows what the author edited, not what would have failed to build.
- **Grep is given the answer.** It gets the name of the symbol that changed. CKB has
  to work that out from a diff, and a human or agent running grep has to choose.
- **Signature changes only.** A behaviour change with an unchanged signature is out of
  scope. So are added and deleted callers, Go interface methods, TS class methods and
  commits over 30 files.
- **Applying the whole defining file.** Other edits in that file come along, so
  predictions can include consumers of other symbols.
- **Nothing here is about agents.** It measures what the output contains, not whether
  an agent reads it or acts on it.

## False alarms: `sweep.py`

`eval.py` asks whether the done check finds what a change breaks. `sweep.py` asks the
opposite: what it reports that isn't broken. It replays commits that build: each is
indexed at its parent, applied to the working tree whole, and checked with `ckb
changes`. A commit that builds (for Go, also vets) broke no use, so each open site is a
false alarm, and each `not_assessed` note names a doubt to read.

```bash
git clone . /tmp/sweep-clone          # A scratch clone: it is reset for every commit
python3 scripts/eval-change/sweep.py --ckb ./ckb --repo /tmp/sweep-clone \
    --out /tmp/sweep.jsonl $(git rev-list --first-parent -80 develop)
python3 scripts/eval-change/sweep.py --report /tmp/sweep.jsonl
```

It finds false alarms only, never a missed break: the commits compile, so there is
nothing to miss. The 9.4.0 numbers in the CHANGELOG come from this, over the last 80
commits of `develop`.

## One TypeScript change against `tsc`: `replay.py`

`eval.py` samples signature changes. `replay.py` takes one commit you pick, for a change
the sample rule doesn't cover: a data model whose types are read off values, say. A
throwaway clone is indexed at the parent, only the named files are written from the
commit, and `ckb changes` is scored against `npx tsc --noEmit` before and after: every
error line that is new is a use the change broke.

```bash
python3 scripts/eval-change/replay.py --ckb /tmp/ckb --repo ~/app \
    --parent <sha> --commit <sha> --work /tmp/replay-app --show \
    src/lib/schema.ts src/lib/types.ts
```

It prints how many broken lines have an open site on them, within three lines, or none,
how many of the first `--top` sites are on a broken line, and the size of the compact
answer. `--expect-clear` is for a commit that breaks nothing: exit 1 unless the check is
clear. The work dir keeps the clone, both `tsc` outputs (reused: they don't depend on the
binary) and each run's answer. It holds the app's symbol and file names, so for a private
app keep it outside this repo. `node_modules` is linked from the app's checkout.

The 9.4.0 numbers for value-derived types come from two commits of one app, picked
because they showed the gap. That is a reproduction, not a holdout.

## Five other repos: `select_replay.py` and `replay_repo.py`

`select_replay.py` picks a repo's commits from git alone: from HEAD back, a commit that
changes an exported declaration (a function's head, any line of a type or const) named
in another file. `replay_repo.py` runs `replay.py` on them in order and skips, before CKB
runs, a commit whose parent the compiler rejects. `replay.py --lang rust` takes `cargo
check` as truth; `--undo-compiler-writes` restores tracked files the compiler wrote.

Measured 2026-11-12 with the rules frozen at f2c74dc: two TypeScript and three Rust repos
of one machine, chosen by a written rule (commits in the last three months, HEAD builds,
20 source files or more; no Go repo qualified), five commits each.

| | TS-A | TS-B | Rust-A | Rust-B | Rust-C | all |
|---|---|---|---|---|---|---|
| commits measured (parent broken, skipped) | 5 | 5 (1) | 5 (1) | 5 | 5 | 25 (2) |
| commits that break a line | 0 | 1 | 1 | 0 | 1 | 3 |
| broken lines / files | 0 | 1 / 1 | 1 / 1 | 0 | 1 / 1 | 3 / 3 |
| exact, within 3, no site, file not named | – | 1, 1, 0, 0 | 1, 1, 0, 0 | – | 1, 1, 0, 0 | 3, 3, 0, 0 |
| listed sites (rarely counted) | 0 | 36 (6) | 1 | 0 | 8 | 45 (6) |
| listed sites the compiler doesn't flag | – | 32 | 0 | – | 7 | 39 (87%) |
| clear / open / not assessed | 0 / 0 / 5 | 2 / 2 / 1 | 3 / 1 / 1 | 5 / 0 / 0 | 1 / 4 / 0 | 11 / 7 / 7 |
| **clear while the compiler reports errors** | 0 | 0 | 0 | 0 | 0 | **0** |
| compact answer, mean bytes | 359 | 2654 | 1554 | 746 | 1613 | 1385 |
| `ckb index` seconds | 3 | 1–2 | 17–50 | 2–3 | 4–5 | |

TS-A's five `not_assessed` come from a tracked `tsconfig.tsbuildinfo` that `tsc` rewrites
and the check takes for an unindexed code file; with that write undone all five are
`clear`, as the compiler agrees. Three broken lines are too few to say much about recall.
Of the four open checks the compiler finds nothing in, two stopped at name resolution
(rustc then type-checks nothing, so the truth is missing) and one has unresolved imports
of files the commit added.

`ckb refs` against `git grep -n -w`, 21 symbols: 157 lines against 402, 17 KB against
39 KB, 0.11 s against 0.007 s (median). No line CKB lists is missing from grep. Of grep's
245 extra lines 22 are imports (counted by CKB), 29 comments or strings, 194 other; read
by sample, those are mostly other symbols of the same name, plus two kinds of real miss:
files outside the tsconfig's `include`, and TypeScript lines the import heuristic takes
for part of an import block.

These repos and commits are no holdout any more.

### After the fixes from that run (c066d78)

Same 25 commits, `replay_repo.py --name fix2` (each name keeps its own runs file), TS-A
without `--undo-compiler-writes`:

| | TS-A | TS-B | Rust-A | Rust-B | Rust-C | all |
|---|---|---|---|---|---|---|
| exact, within 3, no site, file not named | – | 1, 1, 0, 0 | 1, 1, 0, 0 | – | 1, 1, 0, 0 | 3, 3, 0, 0 |
| listed sites (rarely counted) | 0 | 30 (6) | 1 | 0 | 4 (4) | 35 (10) |
| clear / open / not assessed | 5 / 0 / 0 | 2 / 2 / 1 | 4 / 1 / 0 | 5 / 0 / 0 | 1 / 4 / 0 | 17 / 7 / 1 |
| **clear while the compiler reports errors** | 0 | 0 | 0 | 0 | 0 | **0** |

The six `not_assessed` that went `clear` (tsbuildinfo, a TypeScript constructor, a
removed entry of a Rust `use` list) have no compiler error either. TS-B's remaining one
is new: six test files outside the tsconfig's `include` name a changed declaration,
which no index and no `tsc` with that tsconfig looks at. TS-B lost six empty `X[] = []`,
Rust-C moved four reads of an array whose length changed to the counted sites; the
compiler flagged none of them. The Lebenslauf replay is unchanged, the Go sample stays at
9/9, and `sweep.py` over the last 30 commits of `develop` shows the outside-index doubt
once, on a `//go:build ignore` file that is now skipped.

### Recall: only the defining file applied (`--rule users`, rules at dd8e79a)

The declaration rule mostly picks changes that break nothing (3 of 25 above). `select_replay.py
--rule users` is eval.py's Go rule: a changed declaration counts only when the same commit
also edits another non-test source file whose diff names it, and only the defining files are
applied, so those users are left at the parent. Same heads, every candidate whose parent
builds, `replay_repo.py --name users --keep 20`:

| | TS-A | TS-B | Rust-A | Rust-B | Rust-C | all |
|---|---|---|---|---|---|---|
| candidates (parent broken, skipped) | 2 | 3 (1) | 16 (1) | 0 | 8 | 29 (2) |
| commits measured / that break a line | 2 / 0 | 2 / 1 | 15 / 9 | – | 8 / 1 | 27 / 11 |
| broken lines | 0 | 1 | 10 | – | 1 | 12 |
| exact, within 3, missed, file not named | – | 1, 1, 0, 0 | 9, 9, 1, 1 | – | 1, 1, 0, 0 | 11, 11, 1, 1 |
| clear / open | 2 / 0 | 1 / 1 | 3 / 12 | – | 4 / 4 | 10 / 17 |
| listed sites (on an error line) | 0 | 19 (4) | 59 (9) | – | 20 (1) | 98 (14) |
| **clear while the compiler reports errors** | 0 | 0 | **1** | – | 0 | **1** |

Rust-B has no commit that edits a user of a changed declaration. The one miss is the false
clear: a variant added to a Rust `pub enum`, in a hunk that starts inside the enum and doesn't
reach its `pub enum` line, is read as a body change and `unchanged`; an exhaustive `match` in
another file breaks (E0004). Rust truth is a lower bound, so of the 84 listed sites the
compiler doesn't flag, some sit in crates it never checked. 11 of 12 is one sample of
11 breaking commits, nine of them from one repo.

After 1bef158 (`--name users2`, same sample): a variant added to an enum lists every `match`
over it with no `_` or binding arm and no arm for the variant. That case goes from false clear
to open with one site, exactly on the error line; none of the other 26 commits changes. 12 of
12 exact, 99 listed sites (15 on an error line), no clear while the compiler reports errors.
That fix was written for the case it is measured on, so it shows the case is covered, not
that recall is 12 of 12 elsewhere.

### A fresh Rust sample (`users3`, `variants`, rules at daebc62)

Rust repos of the same machine that were in no eval before, chosen by the rule in the
private PLAN: the three with the most `--rule users` candidates (Rust-D 20, Rust-E 8,
Rust-F 7). To measure the enum variant check on commits it wasn't built from,
`select_replay.py --rule variants` takes the users rule for a `pub enum` that gains a
variant; over all eight new repos it finds six, two of them with a broken parent. Binary
built at daebc62 (done-check code at 47eefef, with `Self::`/glob arms and nested patterns),
nothing changed during the run.

| | Rust-D | Rust-E | Rust-F | all |
|---|---|---|---|---|
| candidates (parent broken, skipped) | 20 (6) | 8 | 7 | 35 (6) |
| commits measured / that break a line | 14 / 10 | 8 / 2 | 7 / 5 | 29 / 17 |
| broken lines | 11 | 2 | 21 | 34 |
| exact, within 3, missed, file not named | 7, 7, 4, 4 | 2, 2, 0, 0 | 21, 21, 0, 0 | 30, 30, 4, 4 |
| clear / open | 5 / 9 | 4 / 4 | 1 / 6 | 10 / 19 |
| listed sites (on an error line) | 44 (7) | 72 (2) | 59 (21) | 175 (30) |
| **clear while the compiler reports errors** | **2** | 0 | 0 | **2** |
| compact answer, mean bytes | 1416 | 1895 | 2387 | |
| `ckb index` seconds | 5–15 | 4–17 | 4–10 | |

All four misses in Rust-D are one cause: a field added to a struct, and a literal of it in
another file without `..` (E0063). The literal runs 48 to 61 lines, and the Rust literal
reader gives up after 40 (`maxCallSpan`), so the use isn't a literal and the check says
nothing; in two commits that is the only broken line, so they are false clears. 30 of 34
is one sample; 21 of the 30 come from one repo.

`variants`: four commits measured, two break a line. Only one new error is a
non-exhaustive match (E0004), spelled `E::A` at the top of the arms; it is listed exactly.
No `Self::`, glob or nested arm occurs in the sample, so those three paths are covered by
tests only. One miss: `let E::A { .. } = x;` over an enum that had one variant becomes
refutable (E0005) when it gains another, and the check doesn't look at `let` patterns.
The other broken commit's four lines are struct literals, all exact.

After 13f6bb6 (`--name users3fix`, same sample): a Rust struct literal is followed up to
1000 lines. The four misses are exact, both false clears are open, 34 of 34; one listed
site goes away, a 46-line literal in the defining file whose new field the diff added
(the compiler agrees). Nothing else moves, `variants` included. As with users2, the fix is
fitted to the case it is measured on.

The E0005 miss (`--name variantsfix2`, `variants` sample only): a plain `let` whose
pattern names a variant of an enum that had exactly one is open at the `let`; `if let`,
`while let` and `let … else` aren't. The commit's two broken lines are both exact now, no
miss. One more site is listed, a `let` over the same variant in an integration test: cargo
never checks that target, because the library it depends on has errors, and the commit
itself turned exactly that line into a `let … else`. So it is a break the compiler's lower
bound hides, not a false open. The other three `variants` commits don't move. Fitted to
its case as well; the rule only reaches enums that had one variant.

The Rust literal reader skips strings, raw strings, chars and comments since then. A scan
of every `Name {` in the eval clones found real literals it misread: a `..` in a string
(`"../game"`, `"HEAD~1..HEAD"`) taken for the rest, which makes a literal without one look
covered, a `//` in a string cutting the line, and brackets in a raw-string regex ending
the literal on its first line. One of them is a struct whose fields did change in its
history, but in that commit every literal sat in the defining file and was updated. So no
measured number depends on it: `users3strfix` is the same as `users3fix` in all 29
commits.

### `Self::`, glob and nested arms on real commits (`arms`, rule at 87b8b8e)

`select_replay.py --rule arms` over every local repo with Rust files (23, the CKB repo
aside; a prior eval is no reason to leave one out here, since none of the three paths was
built from a real commit): an enum gains a variant, and at the parent an arm over it is
spelled `Self::A`, `A` behind `use E::*`, or `E::A` inside another pattern. Eleven commits
in four repos (Rust-A 6, Rust-B 2, Rust-F 1, and Rust-G 2, a repo new to the README). Binary built at bfd0368, rule and
binary frozen before the run.

| | commits | arms (self / glob / nested) | E0004 | listed at an arm's match |
|---|---|---|---|---|
| frozen run | 11 | 40 / 11 / 64 | 0 | 0 |

No commit leaves an arm's match incomplete: the arms sit in the defining file, which is
applied, and each commit gave them the new variant; the two nested arms in other files
have an `Err(e)` arm that covers them. So the frozen run shows no false open at these
paths, and nothing about whether they find an open match. Three commits break other
lines (E0061, E0609, struct literals); those are listed as before. One false open from
another check: a `static` whose initializer grew, its type unchanged. The declaration
reader took the `> =` after `LazyLock<Vec<…>>` for `>=` and read on into the
initializer; fixed, Rust-A 62969bad49 is clear now, and the TypeScript replay
(`const X: Record<K, string> = {`) moves no number.

Afterwards, as a probe and not part of the measurement: the commit's defining file at
the parent with the new variant's arms deleted, one commit per shape. `Self::` (Rust-B,
two arms deleted) lists both matches at their lines, no other. Glob (Rust-F) lists the
match. Nested (Rust-G) said clear, from two bugs: `bracedBody` stopped after 500 lines
and the enum, a clap `Commands` of 590, had no variants (f1fe922); and `TypeSymbolIn`
wanted `/Commands#`, but a name at a crate's root is `… 1.5.0 Commands#`, so no struct or
enum of a `lib.rs` or `main.rs` was found at all (0fc1276). After both the nested match is
counted, as `nested` under rarely. That match (`Some(Commands::X)` arms and `None`, no
`Some(_)`) can't compile, so a nested match no unguarded arm covers is now listed at its
`match` line like any other, and the probe rerun lists `match cli.command {`; a `Self(_)`
arm counts as covering whatever type it is in. A rerun of users3, variants and arms with both fixes
(`--name users3root` etc.) moves none of the 44 commits.

Three later fixes rerun on the same 44 (`--name users3dec`, `variantsdec`, `armsdec`;
binary at 85b49bc): a nested match no arm covers is listed instead of counted (ebc110b),
a declaration of a generic type ends at its `=` (3f33153), and the body of a `pub(crate)`
struct or enum is read at all, with its strings skipped (85b49bc). One commit moves:
Rust-A 62969bad49 goes from one false open to clear. 50/50 error lines exact before and
after, open sites 215 → 214. Nothing else moves, so on these 44 the nested and `pub(crate)`
fixes show no effect; they rest on tests and the probe only.

### `pub(crate)` types on real commits (`scoped`, rule at 1861c4d)

85b49bc made the body of a `pub(crate)`/`pub(super)` struct or enum readable at all;
before it every such type had no fields or variants, a false clear. `select_replay.py
--rule scoped` looks for the case: such a type gains a named field or a variant, and
another non-test file of its crate names it. Each candidate stores `check_dir`, the crate
`cargo check` runs in, which `replay_repo.py` takes unless `--check-dir` is given.

Over all 23 local repos with Rust files, whole history (`--scan 2000`): **0 candidates**.
At HEAD those repos hold 8 crate-scoped structs or enums in all, and none of them gained
a member after the commit that created it. A probe repo with one such commit is found
(`check_dir` and users right), so the zero is the corpus, not the rule. The binary run
at HEAD and the comparison binary at 0fc1276 had nothing to measure. The `pub(crate)`
fix still rests on tests only; measuring it needs repos that use crate visibility, which
none here does.

### The probe, scripted (`replay.py --drop-arms`)

The arms probe above was done by hand twice. `replay.py --drop-arms V` does it: after the
commit's files are written, every arm over the variant V in them is deleted (the
alternative only, if the arm has others; guarded arms are left), the deleted arms are
listed in `dropped_arms`, and the new E0004 lines in the applied files, which `score()`
counts apart as the change's own, are scored against the listed sites
(`probe_e0004_lines`, `_listed`, `_missed`). A probe, not a measurement: the case is built.
Binary at 60618b0, `--name probe`:

| commit | shape | variants dropped | arms deleted | E0004 lines | listed exactly | other sites |
|---|---|---|---|---|---|---|
| Rust-B a5faa2ee89 | `Self::` | Lorentz | 6 | 4 | 4 | 0 |
| Rust-B 578eeab2bb | `Self::` | DotProduct | 4 | 4 | 4 | 0 |
| Rust-F b3ba83500c | glob | SetMarkers | 2 | 2 | 2 | 25 (the commit's other changes) |
| Rust-G cab4ddbda4 | nested | Answer, Reach | 2 | 1 | 1 | 0 |

Why the other deleted arms leave no E0004 (Rust-B: 6 and 4 arms, 4 errors each) is not
looked at here.

### One Rust lexer (rerun of the same 44)

The match, arm, `let` and declaration readers and the glob-use scan read through
`rustCode` (diff/rust_lex.go) now, which also says per byte whether it is code, string or
comment; the declaration reader keeps a string's text and drops comments. Three bugs
that go with it have tests: a `'{'` char in a declaration read as its body, `match` in a
comment above an arm taken for the match, a `|` in a comment inside a pattern taken for
an alternative. `--name *lex` against `*unread`: all 44 answers the same, site for site.
