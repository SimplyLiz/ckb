---
description: "Review a branch, staged changes or a PR: CKB's structural checks first, then read only what CKB flags. Use for code review requests in a repo that has CKB."
when_to_use: "Reviewing a whole branch, the staged changes or a pull request: 'review my branch', 'review PR 123', 'is this ready to merge'. Not for one function, file or pasted snippet: invoking it runs ckb review over the whole diff first."
argument-hint: "[base-branch | staged | PR-number]"
allowed-tools: Bash("${CLAUDE_PLUGIN_ROOT}/scripts/ckb" review *)
---

Run a CKB-augmented code review optimized for minimal token usage.

## Input
$ARGUMENTS - Optional: base branch (default: main), or "staged" for staged changes, or a PR number

## Philosophy

CKB already answered the structural questions (secrets? breaking? dead code? test gaps?).
The LLM's job is ONLY what CKB can't do: semantic reasoning about correctness, design,
and intent. Every source line you read costs tokens — read only what CKB says is risky.

### CKB's blind spots (what the LLM must catch)

CKB runs 15 deterministic checks with AST rules, SCIP index, and git history.
It is structurally sound but semantically blind:

- **Logic errors**: wrong conditions (`>` vs `>=`), off-by-one, incorrect algorithm
- **Business logic**: domain-specific mistakes CKB has no context for
- **Design fitness**: wrong abstraction, leaky interface, coupling that metrics miss
- **Input validation**: missing bounds checks, nil guards outside AST patterns
- **Race conditions**: concurrency issues, mutex ordering, shared state
- **Resource leaks**: file handles, goroutines, connections not closed on all paths
- **Incomplete refactoring**: callers missed across module boundaries
- **Domain edge cases**: error paths, boundary conditions tests don't cover

CKB's scoring uses per-check caps (max -20) and per-rule caps (max -10), so a score
of 85 can still hide multiple capped warnings. The score never drops below 20 (80
points of deductions in all), so 20 means "at least that bad". `verdictReason` says
which checks decided the verdict; a pass needs no warning finding and a score of 50.
`limitations` lists what the index can't see in this diff (members reached by a
computed key, `obj[key]`): read those lines yourself, the callers and tests above
leave them out. HoldTheLine only flags changed lines,
so pre-existing issues interacting with new code won't surface.

## Phase 1: Structural scan (~1k tokens into context)

CKB already ran, before you read this, on what the skill's argument names: none
for the current branch against main, "staged" for the staged changes, a branch
name for that base, a PR number for that PR's base (and its branch if it is
checked out):

!`"${CLAUDE_PLUGIN_ROOT}/scripts/ckb" review --target='$ARGUMENTS' --compact || true`

If that is an error instead of JSON (no CKB setup here, an unknown base branch,
gh missing for a PR number), fix the cause or run `"${CLAUDE_PLUGIN_ROOT}/scripts/ckb" review --base=<branch>`
or `"${CLAUDE_PLUGIN_ROOT}/scripts/ckb" review --staged` with `--compact` yourself, then go on.
A `Note:` line before the JSON says what was reviewed instead of the PR.

Parse the JSON output to extract:
- `score`, `verdict`, `verdictReason` — overall quality and what decided it
- `activeChecks[]` — name + status + summary of each check that warned or failed;
  `passedChecks[]` — names of the checks that passed
- `findings[]` — the 30 most important (blocking first, then by severity): check,
  severity, file, line, message, ruleId; `remainingFindings` and
  `remainingBySeverity` count the rest. For those, run the review yourself with
  `--format=json` and read only the checks you need
- `limitations[]` — what the review could not see
- `narrative` — CKB AI-generated summary (if available)
- `prTier` — small/medium/large
- `reviewEffort` — estimated hours + complexity
- `reviewers[]` — suggested reviewers with expertise areas
- `health` — degraded/improved file counts

From the output, build three lists:
- **SKIP**: passed checks — don't touch these files or topics
- **INVESTIGATE**: warned/failed checks — these are your review scope
- **READ**: hotspot files + files with warn/fail findings — the only files you'll read

**Early exit**: Skip LLM ONLY when ALL conditions are met:
1. Score >= 90 (not 80 — per-check caps hide warnings at 80)
2. Zero warn/fail checks
3. Small change (< 100 lines of diff)
4. No new files (CKB has no SCIP history for them)

If ANY condition fails, proceed to Phase 2 — CKB's structural pass does NOT mean
the code is semantically correct.

## Phase 2: Targeted source reading (the only token-expensive step)

Do NOT read the full diff. Do NOT read every changed file.

**For files CKB flagged (INVESTIGATE list):**
Read only the changed hunks via `git diff <base>...HEAD -- <file>` (`<base>` as in
Phase 1, main by default), or `git diff --cached -- <file>` for staged changes.

**For new files** (CKB has no history — these are your biggest blind spot):
- If it's a new package/module: read the entry point and types/interfaces first,
  then follow references to understand the architecture before reading individual files
- If < 500 lines: read the file
- If > 500 lines: read the first 100 lines (types/imports) + functions CKB flagged
- Skip generated files, test files for existing tests, and config/CI/docs files

**For each file you read, look for exactly:**
- Logic errors (wrong condition, off-by-one, nil deref, race condition)
- Resource leaks (file handles, connections, goroutines not closed on error paths)
- Security issues (injection, auth bypass, secrets CKB's 26 patterns missed)
- Design problems (wrong abstraction, leaky interface, coupling metrics don't catch)
- Missing edge cases the tests don't cover
- Incomplete refactoring (callers that should have changed but didn't)

Do NOT look for: style, naming, formatting, documentation, test coverage —
CKB already checked these structurally.

## Phase 3: Write the review (be terse)

```markdown
## [APPROVE|REQUEST CHANGES|DISCUSS] — CKB score: [N]/100

[One sentence: what the PR does]

[If CKB provided narrative, include it here]

**PR tier:** [small/medium/large] | **Review effort:** [N]h ([complexity])
**Health:** [N] degraded, [N] improved

### Issues
1. **[must-fix|should-fix]** `file:line` — [issue in one sentence]
2. ...

### CKB passed (no review needed)
[comma-separated list of passed checks]

### CKB flagged (verified above)
[for each warn/fail finding: confirmed/false-positive + one-line reason]

### Suggested reviewers
[reviewer — expertise area]
```

If no issues found: just the header line + CKB passed list. Nothing else.

## Anti-patterns (token waste)

- Reading files CKB marked as pass — waste
- Reading generated files — waste
- Summarizing what the PR does in detail — waste (git log exists, CKB has narrative)
- Explaining why passed checks passed — waste
- Running MCP drill-down tools when CLI already gave enough signal — waste
- Reading test files to "verify test quality" — waste unless CKB flagged test-gaps
- Reading hotspot-only files with no findings — high churn does not mean needs review right now
- Trusting score >= 80 as "safe to skip" — dangerous (per-check caps hide warnings)
- Skipping new files because CKB did not flag them — CKB has no SCIP data for new files
- Reading every new file in a large new package — read entry point + types first, then follow refs
- Ignoring reviewEffort/prTier — these tell you how thorough to be
