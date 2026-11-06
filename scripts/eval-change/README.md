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
  so if the defining crate no longer builds alone, its dependents report nothing. Pass
  `--check` and `--check-prefix` for a nested manifest. `gopls` and `tsc` don't have this
  problem.
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
