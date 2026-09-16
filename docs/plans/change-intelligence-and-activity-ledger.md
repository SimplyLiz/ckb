# Plan: Change Intelligence + Activity Ledger (CKB 1.x)

Status: draft, 2026-09-16. Supersedes the "Codebase Intelligence Console" UI idea for 1.x.

## Decision

No web UI in 1.x. **Text first, evidence first, visualization later.** A UI ("CKB
Control") is a 2.0 item on top of the new knowledge/evidence model. For 1.x we build
three things, in this order:

1. **Activity ledger** for every MCP tool call, plus `ckb activity`.
2. **`assessChange`** (MCP) and **`ckb changes`** (CLI): the post-change counterpart
   to `prepareChange`, working on the uncommitted working tree by default.
3. **Benchmark instrumentation**: join the ledger with Claude Code transcripts so
   agent-vs-agent+CKB can be measured on real sessions.

Why (short): every standalone codebase-visualization product died on "nobody opens
the map after week one", a UI makes inferred heuristics look like ground truth, a
second toolchain in the Go release pipeline is the most expensive single decision,
and CKB has zero per-call audit substrate today. Details in the 2026-09-16 review.

## What already exists (do not rebuild)

| Need | Exists | Where |
|---|---|---|
| Working-tree diff → changed SCIP symbols, risk, blast radius, modules, generic recommendations | yes | `Engine.AnalyzeChangeSet` (`internal/query/impact.go:961`), MCP `analyzeChange`, CLI `ckb impact diff` (defaults to `git diff`, i.e. uncommitted tree) |
| Per-symbol ADR linkage | yes, but discarded | `AnalyzeImpact` fills `RelatedDecisions` via `getRelatedDecisions(moduleID)` (`annotation_integration.go:40`); `AnalyzeChangeSet` calls `AnalyzeImpact` per symbol and drops the field |
| Likely reviewers | yes, branch-only | `getSuggestedReviewers` (`internal/query/pr.go:291`), wired into `SummarizePR`/`ReviewPR` only |
| Test selection | yes, review-only | `checkAffectedTests`, `AnalyzeTestGaps` (`review_testgaps.go`); `prepareChange`'s `getPrepareTests` is a same-directory glob |
| Breaking changes | ref-to-ref only | `CompareAPI` (`breaking.go:58`, `HEAD~1..HEAD`), `checkBreakingChanges` (review, `base..head`). Neither sees the working tree |
| Standard confidence envelope | yes, not used by AnalyzeChangeSet | `internal/envelope` (`Confidence{Score,Tier,Reasons,Factors}`); AnalyzeChangeSet uses ad hoc `CompletenessInfo` |
| Per-call tool metrics | partial | `wide_result_metrics` table, 7 tools, aggregates only, no params/result/session, no retention |
| Client identity | logged, not stored | `handleInitialize` (`capabilities.go:34`) logs `clientInfo` and forgets it |
| Session id | not in CKB, but in env | `ckb mcp` children inherit `CLAUDE_CODE_SESSION_ID` and `CLAUDE_CODE_ENTRYPOINT` from Claude Code (verified on 10 running processes) |
| Choke point for all tool calls | yes | `handleCallTool` (`internal/mcp/handler.go:320`), handler invoked at ~L377; no timing or byte count there today |
| Storage | SQLite WAL | `internal/storage/db.go:43`; `engine.DB()` reachable from MCP via `SetMetricsDB` pattern |

Known gaps this plan closes as side effects:

- `ckb review` cannot see unstaged changes (only `base..head` or `--staged`).
- `SummarizePR` comment at `pr.go:101` claims "compare against working tree"; the code
  runs `git diff base HEAD`. Fix the comment or the behavior, not neither.
- `ckb impact diff` help text promises "Who needs to review this?" and does not deliver.

## 1. Activity ledger

### Table `tool_calls` (in `.ckb/ckb.db`)

| column | notes |
|---|---|
| id | autoincrement |
| ts | unix ms, call start |
| session_id | `CLAUDE_CODE_SESSION_ID` if set, else `<hostname>:<pid>:<process start>` |
| consumer | `clientInfo.name`/`version` from initialize (store it on `MCPServer`), fallback `CLAUDE_CODE_ENTRYPOINT`, else "unknown" |
| tool | tool name |
| params_hash | sha256 of canonical JSON, for grouping repeated calls |
| params | canonical JSON, truncated to 4 KB, string values over 512 B replaced by `"<N bytes>"` |
| target | best-effort primary argument (symbolId / file / query), extracted per tool from a small table of "primary param" names, else NULL |
| duration_ms | wall time around the handler call |
| response_bytes | len of marshalled envelope |
| truncated | from envelope `Truncation` if present |
| error | error string or NULL |
| facts | JSON, optional, per-tool structured counts (see below) |

Indexes: `(ts)`, `(session_id, ts)`, `(tool, ts)`.

### Write path

Wrap the handler call in `handleCallTool` with a start time; after marshalling, write
one row. Writes go through a buffered channel with a single writer goroutine, flushed
on a ticker and on server shutdown, so the ledger never adds latency to a tool call.
Errors writing the ledger are logged at debug and never surface to the client.

`facts` is the honest version of "27 symbols traversed": tools may attach a
`map[string]int` via a helper (e.g. `ledger.Facts(ctx).Set("symbols", n)`). v1 fills
it for `prepareChange`, `assessChange`, `analyzeImpact`, `findReferences`,
`searchSymbols`. Tools that do not call the helper get `facts = NULL`, and
`ckb activity` prints nothing for them. Never invent counts at the generic layer.

The existing `wide_result_metrics` writers keep working in v1. Once the ledger has a
release behind it, `ckb metrics` reads from `tool_calls` and the old table is dropped.

### Config and retention

```json
"activity": { "enabled": true, "retentionDays": 30, "storeParams": true }
```

Prune on MCP server start (`DELETE WHERE ts < now - retention`). `storeParams=false`
stores only `params_hash` and `target`. Rows are local and never leave the machine.

### `ckb activity`

```
ckb activity                  # last 20 calls in this repo, human format
ckb activity --last 100
ckb activity --session        # current CLAUDE_CODE_SESSION_ID only
ckb activity --consumer claude-code
ckb activity --tool prepareChange
ckb activity --since 2h
ckb activity --json
ckb activity --summary        # per-tool: calls, p50/p95 ms, avg bytes, error rate, truncation rate
```

Human format per row: time, consumer, tool, target, then facts if present, then bytes
and ms. No causal narrative ("because a module boundary was crossed"): CKB sees calls,
not intent.

### Multi-repo

The MCP engine cache switches the active repo per call; each row lands in the active
repo's `.ckb/ckb.db`. `ckb activity --all` iterates `~/.ckb/repos.json`. Good enough
for 1.x.

## 2. `assessChange` and `ckb changes`

### Positioning

- `prepareChange(target)`: before touching code. Input is a symbol or file.
- `assessChange()`: after touching code. Input is the working tree (default), the
  index (`staged`), or a range (`base`). Answers "what did I actually change and what
  should I verify before I finish".
- `ckb review`: PR gate against a base branch, 21 checks, CI. Unchanged in scope.

`assessChange` is **`analyzeChange` grown up, not a third implementation.** Extend
`AnalyzeChangeSet`; register `assessChange` as the tool name; keep `analyzeChange` as
a deprecated alias for two minor versions (same handler). Same for the CLI:
`ckb changes` is the new name, `ckb impact diff` stays as an alias.

### Response (additions to `AnalyzeChangeSetResponse`)

```
change          branch, base, mode (working-tree|staged|range), files, changed symbols (exists)
risk            score, level, factors (exists; factors become structured {name, weight, evidence})
impact          blast radius, direct/transitive consumers, modules (exists)
affected_tests  NEW: tests that reach changed symbols (reuse checkAffectedTests logic),
                plus downstream consumers with no reaching test
contracts       NEW: public symbols whose lines changed, flagged "possible contract change"
                (v1 heuristic, labeled as such; real signature diff in v2, see below)
decisions       NEW: surface RelatedDecisions already computed per symbol, dedup by ADR id
ownership       NEW: getSuggestedReviewers over the changed files
recommendations exists; keep generic text but attach the finding it derives from
confidence      route through internal/envelope Confidence, tier from SCIP freshness
                and IndexStaleness (already computed)
```

### `ckb changes` human output

Matches the mock in the 2026-09-16 discussion: risk line, counts, affected tests with
status, decisions, likely reviewers, potential issues. Every inferred item carries its
label (`inferred`, `heuristic`, `stale index`) inline. Flags: `--json`, `--verbose`,
`--staged`, `--base <ref>`, `--repo <name>`.

### The hard part: contracts on the working tree

`CompareAPI` compares two git refs via SCIP, and the SCIP index reflects the last
indexed state, not the edited files. Options, in order of preference:

1. Cartographer `diff_skeleton`/`semidiff` on the changed files against `HEAD`
   content. No reindex needed, signature-level. Build-tag gated like `ArchImpact`.
2. Fallback without Cartographer: "public symbol touched" heuristic, clearly labeled.

v1 ships option 2 for all builds and option 1 where Cartographer is linked. Do not
call the heuristic "breaking change" anywhere in output.

### Test-gap synthesis

"3 downstream paths lack direct test coverage" = for each direct consumer of a changed
symbol, check whether any test symbol reaches it (call graph, depth 2, reuse
`checkAffectedTests` traversal). Report the count and list up to 5. This is new logic
but small; it composes existing traversals.

## 3. Benchmark instrumentation

Goal: agent alone vs agent+CKB on the same tasks, measured on real Claude Code
sessions, not on synthetic tool-call counts.

- **Join key**: `CLAUDE_CODE_SESSION_ID`. The ledger has it per row. Claude Code
  transcripts live at `~/.claude/projects/<cwd-slug>/<sessionId>.jsonl` and carry per
  message `usage` (input, cache read/write, output tokens) and every `tool_use` block
  (including `Read`/`Grep` calls and `mcp__ckb__*` calls).
- **`ckb bench session <sessionId>`**: reads both sources, emits one record: total
  tokens by class, turns, tool calls by tool, files read by the agent, CKB calls, CKB
  bytes delivered, CKB facts sums. `--json`.
- **`ckb bench compare A B`**: two session records side by side.
- **Protocol** (separate doc later): fixed task list on 3–5 OSS repos per the
  2026-09-14 positioning decision, each task run with and without the CKB MCP server
  configured, same model, same prompt, outcome judged by tests passing.
- Check `internal/eval` (`ckb eval`, retrieval quality) before adding a package; the
  session join is a new concern and probably a sibling package `internal/bench`, not an
  extension of retrieval eval.

Nothing in this section claims "tokens saved". The comparison produces the number;
the UI mock's "1,840 tokens avoided" never appears as a per-call figure.

## Sequence and rough size

| Step | Size | Depends on |
|---|---|---|
| 1a. Ledger table, write path, clientInfo capture, config, retention | S (2–3 d) | – |
| 1b. `ckb activity` incl. `--summary` | S (1–2 d) | 1a |
| 2a. `assessChange`/`ckb changes` v1: rename+alias, decisions, ownership, envelope, structured risk factors, contracts heuristic | M (3–5 d) | – |
| 2b. affected tests + downstream test gaps | M (2–4 d) | 2a |
| 3a. `ckb bench session` / `compare` | M (3–4 d) | 1a |
| 3b. benchmark protocol run on OSS repos | M, mostly wall time | 3a, 2a |
| 2c. Cartographer signature diff for contracts | M–L | 2a, Cartographer API check |
| cleanup: `ckb metrics` on ledger, drop `wide_result_metrics`; fix `pr.go:101`; fix `impact diff` help | S | 1b |

1a → 2a → 1b → 3a is the critical path to a first real measurement. 2b and 2c can
follow once `ckb changes` is in daily use on CKB itself.

## Non-goals for 1.x

- Any browser UI, `go:embed`, or JS toolchain.
- Per-call "tokens avoided".
- Narrating agent intent in the activity feed.
- Cross-repo blast radius (federation has no edge graph, only lists).
- Ownership override / curated-knowledge editing (belongs to the 2.0 knowledge model,
  and must live in versioned files, not `.ckb/ckb.db`).
