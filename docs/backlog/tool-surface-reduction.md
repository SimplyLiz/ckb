# Reducing the MCP tool surface

**Status:** Backlog
**Target:** its own minor release, with a deprecation window like `analyzeChange`
**Scope:** 111 tools today, 79 if everything below is done

---

## Why

The cost is not the default context: `core` is about 5k tokens, only `full`
reaches about 18k. The cost is that an agent has to choose between near-identical
tools, and that every tool is one more thing to keep correct.

Removing a tool breaks callers, MCP and A2A alike. Tool counts are also repeated
in README, CLAUDE.md and the changelog.

## Remove from MCP: admin tools with a CLI command (17)

These set up or operate CKB. An agent working on code does not need them.

| Group | Tools | CLI |
|---|---|---|
| Webhooks | `listWebhooks`, `testWebhook`, `webhookDeliveries` | `ckb webhooks` |
| Daemon | `listSchedules`, `runSchedule`, `daemonStatus` | `ckb daemon` |
| Jobs | `listJobs`, `cancelJob` | `ckb jobs` |
| Federation admin | `federationAddRemote`, `federationRemoveRemote`, `federationListRemote`, `federationSyncRemote`, `federationStatusRemote`, `federationSync` | `ckb federation` |
| Contract curation | `suppressContractEdge`, `verifyContractEdge` | `ckb contracts` |
| Debug | `getWideResultMetrics` | `ckb metrics` |

`getJobStatus` stays for as long as `refreshArchitecture` has `async`.

## Merge or remove: duplicates (15)

| Tool | Goes to | Confidence |
|---|---|---|
| `explainSymbol` | `understand`, which already calls it (check `understand` has a light mode) | high |
| `analyzeChange` | removed at the end of its announced window, `assessChange` replaces it | certain |
| `annotationSet`, `annotationGet`, `annotationList` | removed: only these three tools read the data, and it sits in the unversioned DB | high |
| `scanPerformance` | a hidden-coupling filter on `analyzeCoupling`; `analyzeStructuralPerf` then takes the name | high |
| `switchRepo`, `getActiveRepo` | `switchProject` also accepts a registered name, active repo goes to `getStatus` | high |
| `getBlastRadius` | a fallback inside `analyzeImpact`; it exists for repos without SCIP but is only reachable in `full` | high |
| `contextHealth` | removed: it scores LLM context, not code, and Cartographer's own MCP server has it | high |
| `getSymbol` | `batchGet` with one ID | medium, descriptions compared only |
| `getSymbolGraph` | `getCallGraph` accepts several symbols | medium, descriptions compared only |
| `federationSearchModules`, `federationSearchOwnership`, `federationSearchDecisions`, `federationGetHotspots` | one `federationSearch(kind)`, saves 3 | medium |

`explainFile` is **not** a duplicate of `explore`: it returns role, imports and
exports, and `explore` does not call it.

## Compare response fields first (not counted)

- `getModuleOverview` against `explore`: `explore` calls it, but the part read so
  far only takes the responsibility from it
- `summarizePr` against `summarizeDiff`
- `recentlyRelevant` against `getHotspots`

## Shrink `core` from 26 to about 19

`getSymbol` and `explainSymbol` go with the merges above. Move to other presets:

- `exportForLLM`, `planRefactor`
- `getModuleOverview`, if the comparison above allows it
- `explainPath`: `classifyPathRole` only receives the relative path
- `listKeyConcepts`: check whether "semantic clustering" still describes it

## Tools in no preset

After the cuts above, `analyzeOutgoingImpact`, `detectShotgunSurgery`,
`getArchitecturalEvolution`, `renderArchitecture` and `queryContext` remain
reachable through `full` only. Each goes into a preset or out.

## Decided: federation and contracts stay in MCP (2026-11-09)

They stay behind the `federation` preset, with the cuts above and no new
investment. About 10 tools remain.

- They are not in `core`, so default sessions pay nothing for them.
- `analyzeContractImpact` fits the change-safety positioning: a two-repo smoke
  test found the consumer of a changed proto in the other repo, which grep
  does not do.
- Removing them would save little and break the few who use them.

A later trim to 6 or 7 tools is possible: merge `federationStatus` and
`federationRepos`, fold symbol search into `federationSearch(kind)`.

Known bug: after `ckb init`, `ckb federation add` warns that the repository
database is missing and advises `ckb init` again. The database only exists after
`ckb index` or a first query, so the advice loops.

## Open decisions

- **Review check count.** The code has 24 `checkEnabled` checks, some of them
  conditional. README and CLAUDE.md say 21, parts of `docs/` still say 15. The
  tool descriptions no longer state a number.
