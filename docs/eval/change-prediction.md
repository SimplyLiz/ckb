# Change prediction: does `ckb changes` name what a signature change breaks?

"Before your agent changes code, CKB tells it what can break." This measures that
claim on real commits, against what `git grep` gives an agent for free.

**Result on this repo (Go, n=8, CKB at b550825):** CKB named the changed symbol in 8/8
commits and found every non-test file the compiler says broke (9/9). So did grep (9/9),
with far fewer wrong files: precision 31% against 82% (69% for CKB's direct callers
alone). Test files are where CKB is worse: 1 of the 2 broken test files, against 2/2.
Risk was `low` on 7 of 8 commits, all of which broke the build.

n=8 is small, and the sample's breakage is narrow: at most 2 non-test files per commit.
This says little about the wide changes where a call graph should beat grep.

Tool, sample and raw results: `scripts/eval-change/` (`samples/ckb.json`,
`results/ckb.jsonl`). A rerun reproduced every number below.

## Method

For each sampled commit, in a throwaway clone at its parent:

1. Build the SCIP index with a CKB built from the commit under test (b550825 here).
2. Write only the file(s) defining the changed symbol from the commit.
3. Run `ckb changes --format json`. Predicted = files of `affectedSymbols`.
4. Run `git grep -l -w <symbol>` at the parent. Same filter, same metrics.

Scored against three kinds of truth. Non-test and test files are scored separately,
because the CLI leaves test references out of `affectedSymbols` by default.

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

Attempted 8, measured 8; status counts: {'ok': 8}.
Target symbol named in changedSymbols: 8/8 (named but zero consumers predicted: 0; flagged as possible contract change: 5). CKB found fewer truth files than grep in 0 commits, more in 3. affectedSymbols cut by the item cap in 1 commits, which hid 1 truth files in 1 commits and 1 compiler-broken files in 1.

### Truth = all other non-test source files modified in the commit

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | empty | full recall | zero recall |
|---|---|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 59% | 46% | 59% | 45% | 13/28 | 29 | 0 | 2 | 0 |
| ckb_direct_only | 8 | 47% | 32% | 86% | 69% | 9/28 | 13 | 0 | 2 | 0 |
| ckb_uncapped | 8 | 65% | 50% | 61% | 47% | 14/28 | 30 | 0 | 3 | 0 |
| grep | 8 | 47% | 32% | 90% | 82% | 9/28 | 11 | 0 | 2 | 0 |

### Truth = those whose changed lines mention the symbol

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | empty | full recall | zero recall |
|---|---|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 100% | 100% | 40% | 28% | 8/8 | 29 | 0 | 8 | 0 |
| ckb_direct_only | 8 | 100% | 100% | 82% | 62% | 8/8 | 13 | 0 | 8 | 0 |
| ckb_uncapped | 8 | 100% | 100% | 40% | 27% | 8/8 | 30 | 0 | 8 | 0 |
| grep | 8 | 100% | 100% | 85% | 73% | 8/8 | 11 | 0 | 8 | 0 |

### Truth = files the compiler says broke (8 commits checked, 0 with no breakage)

| Tool | n | recall (mean) | recall (pooled) | precision (mean) | precision (pooled) | hit/truth | predicted files | zero recall |
|---|---|---|---|---|---|---|---|---|
| ckb | 8 | 100% | 100% | 42% | 31% | 9/9 | 29 | 0 |
| ckb_direct_only | 8 | 100% | 100% | 86% | 69% | 9/9 | 13 | 0 |
| ckb_uncapped | 8 | 100% | 100% | 41% | 30% | 9/9 | 30 | 0 |
| grep | 8 | 100% | 100% | 90% | 82% | 9/9 | 11 | 0 |
| test files: ckb | 2 | 50% | 50% | 50% | 50% | 1/2 | 2 | 1 |
| test files: grep | 2 | 100% | 100% | 100% | 100% | 2/2 | 2 | 0 |

Compiler-broken files missed by CKB, by class (test files included):

| Class | commits | files |
|---|---|---|
| item-cap | 1 | 1 |

### Test files edited in the commit and mentioning the symbol (2 commits)

| Tool | recall (mean) | hit/truth | predicted test files |
|---|---|---|---|
| ckb | 50% | 1/2 | 2 |
| grep | 100% | 2/2 | 2 |

Of 1 missed, 1 are references the index has (`ckb refs --include-tests`).

### Risk level against what the commit had to touch

| Risk | commits | median truth files | median mentioning files | median compile-broken files | max | commits with none broken |
|---|---|---|---|---|---|---|
| low | 7 | 3 | 1 | 1 | 2 | 0 |
| medium | 1 | 2 | 1 | 2 | 2 | 0 |

### Why truth files were missed (default output)

| Class | commits | files |
|---|---|---|
| coedit-no-mention | 6 | 15 |

### Per commit

Columns after the symbol: truth is all other non-test source files modified; hit/pred is truth files found over files predicted; "broken" is non-test files the compiler says broke.

| Commit | Symbol | Status | Named | Risk | truth | CKB hit/pred | uncapped hit/pred | grep hit/pred | mention truth | CKB hit | grep hit | broken | CKB hit | grep hit | broken tests | CKB hit | grep hit | Miss classes |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| c87117060 | `SuppressEdge` | ok | yes | low | 3 | 1/3 | 1/3 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | coedit-no-mention 2 |
| 42ee47555 | `NewServer` | ok | yes | medium | 2 | 1/4 | 2/5 | 1/2 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 1 | coedit-no-mention 1 |
| 73fe60256 | `getPrepareExtractDetail` | ok | yes | low | 4 | 3/3 | 3/3 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | 1 | coedit-no-mention 1 |
| 9bf57e14d | `NewSCIPExtractor` | ok | yes | low | 3 | 2/2 | 2/2 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | coedit-no-mention 1 |
| e90ac683a | `populateIncrementalTracking` | ok | yes | low | 1 | 1/2 | 1/2 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | - |
| 22b3a8e80 | `checkCouplingGaps` | ok | yes | low | 6 | 2/5 | 2/5 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | coedit-no-mention 4 |
| c3929ab7c | `QueryBlastRadiusBatch` | ok | yes | low | 2 | 2/9 | 2/9 | 2/3 | 1 | 1 | 1 | 2 | 2 | 2 | 0 | 0 | 0 | - |
| 0fec67c43 | `CORSMiddleware` | ok | yes | low | 7 | 1/1 | 1/1 | 1/1 | 1 | 1 | 1 | 1 | 1 | 1 | 0 | 0 | 0 | coedit-no-mention 6 |

## Findings

Certainty is from reading the code and the per-commit output.

**Test files are missing from the default output (certain).** `cmd/ckb/changes.go:61`
passes no `IncludeTests` and the command has no flag for it, so
`internal/query/impact.go:349` drops every test reference. The MCP tool defaults to the
same (`internal/mcp/tools.go:414`). `affectedTests` then only sees test files reached
transitively or matched by name. In `42ee47555`, `internal/api/api_test.go` calls
`NewServer` directly and breaks. `ckb refs --include-tests` lists it; `ckb changes`
doesn't. Grep finds it. Explains 1 commit here.

**The 20-item cap hides results, and the CLI doesn't say so (certain).** The cap is
`budget.maxImpactItems` (`internal/config/config.go:438`), applied per symbol
(`internal/query/impact.go:405`) and again for the whole change (`:1440`).
`ChangeSetResponseCLI` (`cmd/ckb/impact.go:538`) has no truncation field, so only
`summary` versus the list length gives it away. It cut 1 of 8 commits here (36 items to
20), hiding one file edited in that commit and, in the lifted run, the broken test file
above. Lifting the cap alone would only reach that test file through a transitive caller;
the direct reference is still dropped by the finding above.

**Risk doesn't track breakage (certain for the cause, n=8 for the effect).**
`calculateAggregatedRisk` (`internal/query/impact.go:1799`) scores counts of symbols,
consumers and modules. It never asks whether a signature changed, so a compile-breaking
change with one caller scores `low`. That reading of risk as blast radius is defensible,
but `low` next to a broken build is what an agent will act on.

**No output says a signature changed (certain).** `contracts` is a visibility heuristic
(`internal/query/impact.go:2178`): it flagged 5 of 8 here, and the other 3 are unexported
functions, whose callers broke just the same. The call site is never named as broken;
the agent gets a list of consumers to judge.

**Precision is mostly transitive callers (certain).** The list mixes direct and
transitive consumers, and a signature change only breaks direct ones: 69% direct against
31% overall. `distance` is in the output, so an agent can filter, if it knows to.

## What this can't tell us

- Whether CKB beats grep on wide changes. Commits over 30 files are excluded and no
  commit here broke more than 2 non-test files.
- Grep's cost. It is handed the right name, and CKB works it out from the diff; an agent
  has to know which name to grep, and an additive change gives it no compiler error.
- Go interface methods and TS class methods: the rule doesn't select them, so dispatch
  through an interface is not exercised.
- Behaviour changes with an unchanged signature. A compiler sees none of that.
- Whether agents read or act on the output.
- Generality. One repo, one author, 8 commits; all history from a repo CKB was developed
  in.

The tool also ran on private repos. Those results aren't published here.
