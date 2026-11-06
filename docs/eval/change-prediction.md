# Change prediction: does `ckb changes` name what a signature change breaks?

"Before your agent changes code, CKB tells it what can break." This measures that
claim on real commits, against what `git grep` gives an agent for free.

**Result (CKB at 046f946; Go is this repo, TS and Rust are aggregates from two private
repos):** CKB now names every file the compiler says broke, in all three languages, and
every test edited with the change. Its new `contractChanged` list is more precise than
grep on Go and TS and ties on Rust. Its other lists are still mostly noise next to grep.
Risk now catches every breaking commit, but 7 of the 12 non-breaking ones are `high` too.

| Non-test files the compiler says broke | Go, n=8 | TS, n=5 | Rust, n=1 |
|---|---|---|---|
| Recall, CKB before | 9/9 | 6/6 | 1/1 |
| Recall, CKB after, every list below | 9/9 | 6/6 | 1/1 |
| Recall, grep | 9/9 | 5/6 | 1/1 |
| Precision, `ckb` (`affectedSymbols`), before | 31% | 35% | 33% |
| Precision, `ckb`, after | 31% | 40% | 33% |
| Precision, `ckb_files` (`consumerFiles`, new) | 30% | 19% | 25% |
| Precision, `ckb_direct_only`, before | 69% | 67% | n/a (found 0/1) |
| Precision, `ckb_direct_only`, after | 64% | 67% | 33% |
| Precision, `ckb_contract` (`contractChanged`, new) | 90% | 75% | 100% |
| Precision, grep | 82% | 31% | 100% |

| All 26 commits (Go 8, TS 15, Rust 3) | Broke something | Broke nothing |
|---|---|---|
| Before: `low` | 10 | 10 |
| Before: `medium` | 2 | 2 |
| Before: `high` | 2 | 0 |
| After: `low` | 0 | 3 |
| After: `medium` | 0 | 2 |
| After: `high` | 14 | 7 |

| Test files edited with the change, naming the symbol | Before | After | grep |
|---|---|---|---|
| Found, all languages | 2/7 | 7/7 | 6/7 |
| Predicted test files, for that | 6 | 15 | 10 |

The done check and a public run on SWE-Bench ProMax are further down, under "Done check:
results" and "ProMax: results".

How far to trust it:

- **All numbers are small, and precision is pooled over few files.** At most 2 non-test
  files broke per commit, so this says little about wide changes.
- **Risk is partly separated, not solved.** No breaking commit is below `high` now, which
  was the defect. But high is 14 right and 7 wrong, and Go can't show separation at all,
  since all 8 of its commits broke. The compiler let those 7 through for reasons a text
  comparison can't see. The rule was chosen before any of these numbers and not changed
  after them.
- **"A body edit isn't alarmist" isn't measured here.** The sample selects signature
  changes, so it holds almost no body-only change. That claim rests on the rule and on
  unit tests.
- **`ckb_contract` isn't the list the eval started with.** It was added because of the
  first result. Its rule is the risk rule, also fixed before any number on it existed.
- **Where CKB is still worse than grep,** all in precision: `consumerFiles` on all three
  languages, `affectedSymbols` and the direct lists on Go and Rust, and test files on Go
  (67% against 100%, though `contractChangedTests` is 2/2 at 100%). Its recall is at least
  grep's everywhere here. grep is also handed the right name, which CKB has to work out.

Tool, sample and raw results for this repo: `scripts/eval-change/` (`samples/ckb.json`,
`results/ckb.jsonl`, and `results/ckb-b550825.jsonl` for the first measurement).

## Method

For each sampled commit, in a throwaway clone at its parent:

1. Build the SCIP index with a CKB built from the commit under test (046f946 here).
2. Write only the file(s) defining the changed symbol from the commit.
3. Run `ckb changes --format json`. Predicted = files of `affectedSymbols`, and for
   binaries that have it also the lists in `consumerFiles`.
4. Run `git grep -l -w <symbol>` at the parent. Same filter, same metrics.

Scored against three kinds of truth. Non-test and test files are scored separately,
because the CLI leaves test references out of `affectedSymbols` and puts them in
`affectedTests`.

| Truth | What it is | Weakness |
|---|---|---|
| all | Other non-test source files modified in the commit | Includes unrelated edits: 15 of the 28 files here never mention the symbol |
| mention | Those whose changed lines mention the symbol | Favors grep by construction; includes newly added callers and word collisions |
| compiler | Files with more diagnostics once only the defining file is applied | Only build breaks, not behaviour |

The compiler truth is the one to read. It is the only one that says what actually
breaks, and the only one where a file can break without naming the symbol. `gopls check`
type-checks every package even when a dependency has errors.

### Changes from the original plan

The plan was recall against the commit's other files, with a rule on declaration lines.

- **Added the compiler truth.** Reading the misses showed the two commit-based truths
  are mostly noise: nearly every "miss" was an unrelated edit or a new caller. This was
  added after seeing first results, but it doesn't change the sample.
- **Signatures are compared from the file, not the diff line,** so multi-line parameter
  lists count and reformatting doesn't. Decided before any result.
- **Removed functions count**, unless a changed file still declares the name. Decided
  before any result. None landed in this repo's sample.
- **A second run with `budget.maxImpactItems` lifted** on the same index, to separate the
  cap from the analysis.
- **The rule asks for a mention on a changed line,** which is stricter than "references
  the name and changed", so a caller really was updated.

### Changes since the first measurement

Same sample, same rule, same commands: the regenerated private samples came out in the
same order, from the same heads. What changed is what gets scored.

- **`ckb` still means the files of `affectedSymbols`,** so it compares directly with the
  first run in `results/ckb-b550825.jsonl`. The CLI moved tests out of that list, which is
  part of why TS precision there rose from 35% to 40%: non-test files fill the budget.
- **New rows, same scoring:** `ckb_files` (`consumerFiles.direct` and `.transitive`),
  `ckb_files_direct`, and `ckb_contract` (`consumerFiles.contractChanged`: direct users,
  outside the change, of a symbol whose declaration changed or went away).
- **The report classifies direct files against the compiler:** broke, uses the target but
  didn't break, or only uses another symbol in the applied file.
- **Test files count `affectedTests` as before.** A test named after the changed file is
  now labelled `naming-convention`; `ckb_seen_using` leaves those out.

## Done check: rules fixed before measuring

Written and committed before the first run, on the same sample with the same commands.

- **Only the defining file applied.** `ckb_open` is the non-test files of
  `doneCheck.openSites`, minus the defining files, scored against the compiler truth per
  file, like every other list here. Test files score apart, against the broken tests.
- **Whole commit applied.** Every file of the commit goes on top of its parent, with the
  index still at the parent. A commit counts as complete, so each open use is a candidate
  false alarm. The new number is how many commits still report one, and how many open
  uses that is.
- **A candidate is decided by the compiler.** It is a false alarm if the checker reports
  no more diagnostics for that file than at the parent, and otherwise not, since a commit
  can be incomplete. Rust's truth is a lower bound here too, so a Rust "false alarm" may
  sit behind a crate that stopped the check.
- **Kinds are read from the changed symbol's SCIP suffix:** `#` is a type, `().` a function
  or method, the rest other.
- **Types get a decision, not a tweak after the fact.** If types account for more than a
  third of the open uses with the whole commit applied, the check gets narrowed for them
  or labelled, and the same sample is measured again. Both numbers are then reported, not
  only the second.
- **`clear` is not scored as a success on its own.** Status counts are reported as they
  come, `not_assessed` included.

### Done check: results, Go, this repo only

The 8 commits of this repo. The private TS and Rust repos weren't rerun, as their samples
aren't in this checkout; the ProMax run below is what covers those languages.

| Only the defining file applied, against the compiler | Recall | Precision |
|---|---|---|
| `ckb_open`, non-test files | 9/9 | 90% (9 of 10) |
| `ckb_open`, test files | 2/2 | 100% (2 of 2) |
| grep, non-test files | 9/9 | 82% (9 of 11) |

`ckb_contract` scores the same as `ckb_open` here, as it now follows the same check. One
right file per commit in most of these, so read it as "doesn't lose anything", not as a
measured lead over grep.

| Whole commit applied, 8 commits | `clear` | `not_assessed` | `open` |
|---|---|---|---|
| First run (98dffff) | 3 | 4 | 1 |
| After the fix below (6a9ca38) | 5 | 2 | 1 |

- **The first run found a bug in the check.** `CHANGELOG.md` and `README.md` counted as
  changed files outside the index, which took the `clear` from commits that had earned it.
  Files that can't hold a use no longer count. Both runs stay in `results/`.
- **Two false alarms, both in one commit.** `QueryBlastRadiusBatch` went from returning a
  map to a pointer, and two callers only do `raw, _ := ...; raw != nil`. It compiles, and
  no text comparison can tell. That is 2 open uses over 8 complete commits.
- **The two `not_assessed` left:** a changed file with no usable document in the index,
  and a changed symbol whose declaration can't be compared. Both say so in the note.
- **Types can't be judged from this.** The open uses are all functions, so the types rule
  above didn't fire. The sample selects function signature changes, though, so a type
  rarely gets the chance. Tried by hand on a small Go repo: a changed struct field lists only
  that field's uses, an added field lists nothing, and a retargeted type lists every use.
- **8 commits are too few for a rate.** This repo's history holds no more that qualify.

## Sample rule

Fixed in `samples/ckb.json` before anything was measured. A commit qualifies if:

- It is not a merge, has a parent, and changes at most 30 files. Renames count as a
  delete plus an add.
- A non-test, non-generated source file it modifies has a function whose signature
  changed, compared with whitespace and trailing commas removed. Go methods compare per
  receiver type, and a renamed receiver variable doesn't count. Or the function was
  removed outright.
- Another such file mentions that name on a changed non-comment line.

Excluded as generated: `vendor`, `node_modules`, `third_party`, `testdata`, `dist`,
`build`, `target`, `generated`, `.next` and `.claude` directories, `*.pb.go`,
`*_generated.go`, `*_gen.go`, `*.gen.*`, `*.d.ts`, and files headed "Code generated" or
"@generated".

If a commit changed several qualifying symbols, the one with the most mentioning files is
used, ties alphabetical. Only its defining files are applied, so the rest stay hidden and
count as truth. Rebased duplicates (same name, same signatures) are kept once. Candidates
are ordered by `sha256("ckb-eval-change-v1:" + sha)`; a commit that can't be measured
would be logged and replaced by the next.

Pool: 539 non-merge commits up to b550825. The target was 12, but **only 8 commits
qualify, so this is all of them, not a sample.** All 8 were measured.

## Results

This repo only. The TS and Rust numbers are in the first table above.

Attempted 8, measured 8; status counts: {'ok': 8}.
Target symbol named in changedSymbols: 8/8 (named but zero consumers predicted: 0; flagged as possible contract change: 8). CKB found fewer truth files than grep in 0 commits, more in 3. affectedSymbols cut by the item cap in 1 commits, which hid 1 truth files in 1 commits and 0 compiler-broken files in 0.

### Truth = all other non-test source files modified in the commit

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | empty | full recall | zero recall |
|---|---|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 59% | 46% | 59% | 45% | 13/28 | 29 | 0 | 2 | 0 |
| ckb_direct_only | 8 | 47% | 32% | 80% | 64% | 9/28 | 14 | 0 | 2 | 0 |
| ckb_uncapped | 8 | 65% | 50% | 61% | 47% | 14/28 | 30 | 0 | 3 | 0 |
| grep | 8 | 47% | 32% | 90% | 82% | 9/28 | 11 | 0 | 2 | 0 |
| ckb_files | 8 | 65% | 50% | 61% | 47% | 14/28 | 30 | 0 | 3 | 0 |
| ckb_files_direct | 8 | 53% | 36% | 82% | 67% | 10/28 | 15 | 0 | 3 | 0 |
| ckb_contract | 8 | 47% | 32% | 96% | 90% | 9/28 | 10 | 0 | 2 | 0 |

### Truth = those whose changed lines mention the symbol

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | empty | full recall | zero recall |
|---|---|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 100% | 100% | 40% | 28% | 8/8 | 29 | 0 | 8 | 0 |
| ckb_direct_only | 8 | 100% | 100% | 76% | 57% | 8/8 | 14 | 0 | 8 | 0 |
| ckb_uncapped | 8 | 100% | 100% | 40% | 27% | 8/8 | 30 | 0 | 8 | 0 |
| grep | 8 | 100% | 100% | 85% | 73% | 8/8 | 11 | 0 | 8 | 0 |
| ckb_files | 8 | 100% | 100% | 40% | 27% | 8/8 | 30 | 0 | 8 | 0 |
| ckb_files_direct | 8 | 100% | 100% | 75% | 53% | 8/8 | 15 | 0 | 8 | 0 |
| ckb_contract | 8 | 100% | 100% | 92% | 80% | 8/8 | 10 | 0 | 8 | 0 |

### Truth = files the compiler says broke (8 commits checked, 0 with no breakage)

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | zero recall |
|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 100% | 100% | 42% | 31% | 9/9 | 29 | 0 |
| ckb_direct_only | 8 | 100% | 100% | 80% | 64% | 9/9 | 14 | 0 |
| ckb_uncapped | 8 | 100% | 100% | 41% | 30% | 9/9 | 30 | 0 |
| grep | 8 | 100% | 100% | 90% | 82% | 9/9 | 11 | 0 |
| ckb_files | 8 | 100% | 100% | 41% | 30% | 9/9 | 30 | 0 |
| ckb_files_direct | 8 | 100% | 100% | 80% | 60% | 9/9 | 15 | 0 |
| ckb_contract | 8 | 100% | 100% | 96% | 90% | 9/9 | 10 | 0 |
| test files: ckb | 2 | 100% | 100% | 75% | 67% | 2/2 | 3 | 0 |
| test files: grep | 2 | 100% | 100% | 100% | 100% | 2/2 | 2 | 0 |
| test files: ckb_contract | 2 | 100% | 100% | 100% | 100% | 2/2 | 2 | 0 |

Direct files against the compiler (of commits checked): 9 broke, 1 use the target but didn't break, 4 only use another symbol in the applied file.

Compiler-broken files missed by CKB, by class (test files included):

| Class | commits | files |
|---|---|---|

### Test files edited in the commit and mentioning the symbol (2 commits)

| Tool | recall (mean) | hit/truth | predicted test files |
|---|---|---|---|
| ckb | 100% | 2/2 | 3 |
| grep | 100% | 2/2 | 2 |
| ckb_seen_using | 100% | 2/2 | 3 |

Of 0 missed, 0 are references the index has (`ckb refs --include-tests`).

### Risk level against what the commit had to touch

| Risk | commits | median truth files | median mentioning files | median compile-broken files | max | commits with none broken |
|---|---|---|---|---|---|---|
| high | 8 | 3.0 | 1.0 | 1.0 | 2 | 0 |

### Why truth files were missed (default output)

| Class | commits | files |
|---|---|---|
| coedit-no-mention | 6 | 15 |

### Per commit

Columns after the symbol: Declaration is what `ckb changes` says happened to the target; truth is all other non-test source files modified; hit/pred is truth files found over files predicted; "broken" is non-test files the compiler says broke.

| Commit | Symbol | Status | Named | Declaration | Risk | truth | CKB hit/pred | uncapped hit/pred | grep hit/pred | mention truth | CKB hit | grep hit | broken | CKB hit | direct hit/pred | contract hit/pred | grep hit/pred | broken tests | CKB hit | grep hit | Miss classes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| c87117060 | `SuppressEdge` | ok | yes | changed | high | 3 | 1/3 | 1/3 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/1 | 1/1 | 1/1 | 0 | 0 | 0 | coedit-no-mention 2 |
| 42ee47555 | `NewServer` | ok | yes | changed | high | 2 | 1/4 | 2/5 | 1/2 | 1 | 1 | 1 | 1 | 1 | 1/4 | 1/1 | 1/2 | 1 | 1 | 1 | coedit-no-mention 1 |
| 73fe60256 | `getPrepareExtractDetail` | ok | yes | changed | high | 4 | 3/3 | 3/3 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/1 | 1/1 | 1/1 | 1 | 1 | 1 | coedit-no-mention 1 |
| 9bf57e14d | `NewSCIPExtractor` | ok | yes | changed | high | 3 | 2/2 | 2/2 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/1 | 1/1 | 1/1 | 0 | 0 | 0 | coedit-no-mention 1 |
| e90ac683a | `populateIncrementalTracking` | ok | yes | changed | high | 1 | 1/2 | 1/2 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/2 | 1/1 | 1/1 | 0 | 0 | 0 | - |
| 22b3a8e80 | `checkCouplingGaps` | ok | yes | changed | high | 6 | 2/5 | 2/5 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/1 | 1/1 | 1/1 | 0 | 0 | 0 | coedit-no-mention 4 |
| c3929ab7c | `QueryBlastRadiusBatch` | ok | yes | changed | high | 2 | 2/9 | 2/9 | 2/3 | 1 | 1 | 1 | 2 | 2 | 2/3 | 2/3 | 2/3 | 0 | 0 | 0 | - |
| 0fec67c43 | `CORSMiddleware` | ok | yes | changed | high | 7 | 1/1 | 1/1 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1/1 | 1/1 | 1/1 | 0 | 0 | 0 | coedit-no-mention 6 |

## Findings

Certainty is from reading the code and the per-commit output. Each first says what the
first measurement found, then what it is now.

**Test files were missing from the default output (certain; fixed).**
`AnalyzeImpact` dropped test references unless asked, which explained 4 commits across the
languages. They are now always looked up and reported in `affectedTests`, and kept out of
the production lists, counts and risk.

**The 20-item cap hid results without saying so (certain; fixed for files).** It applied
per symbol and again per change. `consumerFiles` now lists every file, up to 300 paths, and
says if it was cut or the lookup stopped short. The CLI reports `truncated`. The transitive
walk is still bounded at 100 symbols per changed symbol, which sets `truncated`.

**Consumers vanished into a blank ID (certain; fixed).** rust-analyzer gives a `let`
statement's local an enclosing range, so a call assigned to a local had "local 12" as its
container, which the adapter blanks. Top-level uses and imports have no container at all.
All of them then deduped on `""` across the change, leaving one. That is why Rust found 0/1
directly before, and 1/1 now.

**Risk didn't track breakage (certain for the cause; partly fixed).** It now compares each
changed symbol's old and new declaration as text, in Go, TS and Rust. Changed or removed
with production consumers outside the diff is at least `high`, exported without any at
least `medium`, a compatible change or a body edit gets no floor. It is a labelled
heuristic: nothing is compiled. See the first section for what it does and doesn't separate.

**Direct precision is not mostly a defect (certain for these commits).** Against the
compiler, this repo's direct files are 9 broke, 1 uses the target without breaking, 4 only
use another symbol in the applied file. On TS it is 6, 16 and 3, on Rust 1, 2 and 3. These
are real consumers: the harness applies a whole file, and many declaration changes don't
break callers. Fixing the blank IDs made this number slightly worse on Go (69% to 64%)
because more real consumers are now found. Telling them apart is what `contractChanged`
is for.

## What this can't tell us

- Whether CKB beats grep on wide changes. Commits over 30 files are excluded and no
  commit here broke more than 2 non-test files.
- Grep's cost. It is handed the right name, and CKB works it out from the diff; an agent
  has to know which name to grep, and an additive change gives it no compiler error.
- Go interface methods and TS class methods: the rule doesn't select them, so dispatch
  through an interface is not exercised.
- Behaviour changes with an unchanged signature. A compiler sees none of that.
- Whether agents read or act on the output.
- Generality. Three repos, one author, 26 commits. n is 8, 5 and 1 for the commits that
  broke a non-test file, and this repo's history is one CKB was developed in.
- Rust inline test modules (`#[cfg(test)] mod tests`): they sit in production files, so
  they count as production consumers.
- A function moved to another file reads as removed. In the same Go package that breaks
  nobody, and it still scores `high` if it has consumers.

The TS and Rust rows are from two private repos, as aggregates only.

## Public eval on SWE-Bench ProMax: rules fixed before measuring

Written and committed before qualifying a single instance. The private TS and Rust repos
above can't be checked by anyone else; these can.

- **Dataset:** SWE-Bench ProMax ([arXiv 2608.09802](https://arxiv.org/abs/2608.09802)), the
  73 Go (23), TypeScript (28) and Rust (22) instances. All of them, no sampling. No LLM is
  involved: the gold patch is the change, and CKB only predicts.
- **License:** neither the dataset card nor its repository names one; CC BY 4.0 on arXiv
  covers the paper. So no instance text or patch goes into this repo: the tool takes
  `--data` pointing at a local copy, and only instance ids, public file names and numbers
  are reported.
- **The commit is `patch` plus `test_patch` on `base_commit`.** Nothing else changes: same
  selection rule as above, 30-file limit included, same commands, same scoring, grep as
  the baseline.
- **Every instance gets an outcome,** and the counts are reported: qualifies, no signature
  change with a caller, over 30 files, could not be prepared, could not be indexed. The
  rule doesn't see Go interface methods or TS class methods, so an instance can be a real
  propagation task and still fall out. That is a limit of this eval, and the report says
  how many it was.
- **Instances aren't independent.** 25 of the 28 TS instances are one repo and 12 of the
  22 Rust ones another, so results are also given per repo.
- **A bad result is reported as it comes.** Nothing gets tuned against this sample in
  9.4.0. If a fix follows from it, the run before the fix stays in this document.

### ProMax: results

Binary at db89a5b, on 2026-11-06. Stripped per-instance data: `results/promax.jsonl` and
`results/promax-outcomes.json`.

**The short version: on Go and TS the check finds what breaks and lists little else. On
Rust it misses half. And a third of complete commits still report open uses, none of which
the compiler confirmed, so `open` can't be read as "broken".**

| Outcome | Go | TS | Rust |
|---|---|---|---|
| qualifies | 13 | 9 | 8 |
| no-signature-change-with-caller | 6 | 16 | 10 |
| over-30-files | 4 | 3 | 4 |

43 of the 73 fall outside this eval. All 30 that qualify were measured.

| Non-test files the compiler says broke | Commits | Recall, `ckb_open` | Precision | Recall, grep | Precision | Files listed, `ckb_open` / grep |
|---|---|---|---|---|---|---|
| Go | 13 | 25/28 | 100% | 21/28 | 88% | 25 / 24 |
| TS | 8 | 23/23 | 88% | 18/23 | 4% | 26 / 460 |
| of which angular | 8 | 23/23 | 88% | 18/23 | 4% | 26 / 460 |
| Rust | 7 | 8/17 | 40% | 8/17 | 7% | 20 / 122 |
| of which ruff | 6 | 8/16 | 80% | 8/16 | 7% | 10 / 114 |
| All | 28 | 56/68 | 79% | 47/68 | 8% | 71 / 606 |

Test files, all languages: `ckb_open` 14/24 with 15 listed, grep 11/24 with 180 listed.

| Whole commit applied | Commits | `clear` | `not_assessed` | `open` | Open uses | Compiler: no break | No compiler answer | Types / functions / other |
|---|---|---|---|---|---|---|---|---|
| Go | 13 | 9 | 1 | 3 | 27 | 27 | 0 | 0 / 9 / 18 |
| TS | 9 | 1 | 4 | 4 | 61 | 23 | 38 | 3 / 55 / 3 |
| of which angular | 8 | 1 | 4 | 3 | 23 | 23 | 0 | 3 / 17 / 3 |
| Rust | 8 | 0 | 3 | 5 | 116 | 32 | 84 | 43 / 22 / 51 |
| of which ruff | 6 | 0 | 3 | 3 | 30 | 30 | 0 | 3 / 7 / 20 |
| All | 30 | 10 | 8 | 12 | 204 | 82 | 122 | 46 / 86 / 72 |

How far to trust it:

- **False alarms are the weak point.** 12 of 30 complete commits report an open use, 204 in
  all. Of those the compiler could judge, 82 of 82 didn't break. Only 10 commits reach
  `clear`. An agent stopping on `--check` would be told to look at something on 12 of 30
  finished changes, and this eval doesn't say whether looking costs more than it saves.
- **A long tail.** One Rust commit accounts for 84 of the open uses and one TS commit for 38
  (a helper whose signature changed in a way its 38 callers didn't need to follow).
- **Rust is weak, and the compiler truth is a lower bound there.** Recall is 8/17. In the
  same runs `consumerFiles` reached 16/17 at 18% precision, so the index knew most of those
  files and the per-use check lost them. That comparison is the first thing to look into.
  The target symbol was named in 6 of 8.
- **The Go misses are one commit:** an interface method, which is the documented limit.
- **TS is nearly all one repo, and its compiler truth is noisy.** 8 of 9 are angular, where
  `tsc` at the root reports thousands of diagnostics before any change, up to about 64,000,
  so "more than at the parent" is all it can say. grep's 4% comes from short common names
  there, so don't read it as grep's general precision.
- **No compiler answer for two commits** (ant-design and qdrant: the checker didn't run), and
  those hold 122 of the 204 open uses. So they are candidates, not known false alarms.
- **Environment.** ruff pins toolchains without `rust-analyzer`, so its 6 ran with
  `RUSTUP_TOOLCHAIN=stable`, compiler included. TS dependencies were installed with
  `--ignore-scripts`.
- **Types didn't fire the rule:** 46 of 204 open uses overall, under the third set above,
  though 43 of 116 on Rust alone. Nothing was tuned against this sample.
- **It measures output, not agents.** Whether any of this helps an agent is what the
  benchmark after this release is for.
