# `symbolExists` — purpose-built grounding check for LLM consumers

**Status:** Built in 9.2.0 (136a7d8): `symbolExists` MCP tool, `internal/query/symbol_exists.go`; the acceptance criteria below are its tests
**Priority:** Medium — unblocks a growing class of LLM-grounding consumers without changing the MCP surface much
**Filed by:** ArchReview (persona-swarm evidence pipeline)
**Filed on:** 2026-04-24
**Related:** `searchSymbols`, SCIP method indexing, FTS5 tokenization

---

## Summary

LLM-backed tools are starting to use CKB as a ground-truth oracle: "before you claim this finding, is the symbol you named real?" ArchReview's persona swarm does exactly this. The current path is:

```ts
const res = await bridge.searchSymbols(name, undefined, 10);
const exists = res.symbols.some((s) => s.name === name);
```

This works for top-level `const`/`function`/`class`/`interface`/`type` declarations. It **silently fails** for class methods, private methods, and object-property-style declarations (tRPC procedures, Express handlers, config records). Three consumer-visible symptoms in a recent smoke run:

| Queried name | Real declaration in the codebase | `symbol_exists` returned |
|---|---|---|
| `saveReport` | `async saveReport(…)` method on `ReportPersistenceService` (TypeScript class) | `exists: false` |
| `trackUsage` | `private trackUsage(usage)` method on `OpenAIService` | `exists: false` |
| `setApiKey` | `setApiKey: t.procedure.mutation(…)` — tRPC procedure-property | `exists: false` |

All three exist. `searchSymbols` either returns no match on a bare-name query for them, or returns matches whose `.name` field does not equal the query string after exact comparison. Either way, the consumer cannot distinguish "name doesn't exist" from "name exists but CKB indexes it under a different shape."

ArchReview's validator layer labels these as `rejected` (hallucination), which is the wrong verdict — the finding is real, the indexing is just not expressive enough to confirm it.

---

## Why this matters for CKB positioning

LLM-grounding is a different traffic shape than IDE/search traffic. An LLM consumer asks **one question per claim**: does this symbol exist, yes or no. They don't need ranked results, they don't care about `visibility`, `moduleId`, or `ranking` — they need a cheap boolean and the minimum metadata to distinguish "doesn't exist" from "exists but indexed shape doesn't match."

Today every consumer has to:

1. Call `searchSymbols` (returning up to 20 rich results).
2. Reimplement a client-side equality filter.
3. Invent their own receiver-suffix fallback (or accept false `rejected` verdicts).
4. Independently handle the SCIP staleness warning.

A purpose-built endpoint removes 4 opportunities for each consumer to get this subtly wrong and turns a fuzzy ranking API into an authoritative `{ exists: bool }` oracle.

---

## Proposed

### 1. New MCP tool: `symbolExists`

```typescript
interface SymbolExistsInput {
  name: string;               // bare name, e.g. "saveReport"
  // Optional narrowing — all omitted = search across everything
  kinds?: string[];           // ["method", "function", "class", "property", …]
  scope?: string;             // file-path prefix, e.g. "packages/server/src/"
  includeExternal?: boolean;  // default false — don't match node_modules
}

interface SymbolExistsResult {
  exists: boolean;
  matches: number;            // how many declarations share this bare name
  kinds: string[];            // distinct kinds of matching declarations
                              // e.g. ["method", "function"] — useful signal to the caller
  receivers?: string[];       // enclosing names when matches are methods/properties
                              // e.g. ["ReportPersistenceService", "OpenAIService"]
  staleIndex?: boolean;       // surfaces the SCIP staleness warning in-band,
                              // so callers don't have to parse log lines
  provenance: Provenance;     // existing type — repoStateId, durationMs, etc.
}
```

Semantics that differ from `searchSymbols`:

- **Exact-name match, not FTS ranking.** `name` is compared against the leaf/descriptor name of every indexed symbol. No fuzzy expansion, no LIP broadening.
- **Matches class methods by bare leaf.** A class method `ReportPersistenceService.saveReport` matches a query for `saveReport`, returning `receivers: ["ReportPersistenceService"]`.
- **Matches object-property declarations.** A property declaration like `setApiKey: t.procedure.…` inside a record should count as existing — the SCIP index does have this node; today `searchSymbols` just doesn't surface it when queried by bare name.
- **Cheap payload.** No `Location`, no `Visibility`, no ranking. Just the four boolean/count fields plus optional receivers. Suitable for LLM grounding at 1 RPS per finding without blowing up token budgets when inlined into prompts.

### 2. Fix `searchSymbols` class-method indexing as the foundation

Whatever fix `symbolExists` needs, `searchSymbols` benefits from as well. Current suspicion from reading `convertSymbolToFTSRecord` in `internal/query/fts.go:82`:

- `name` is set to `symInfo.DisplayName` → falls back to `scipId.GetSimpleName()`.
- For a TypeScript class method, `DisplayName` is typically empty, and `GetSimpleName()` should return the bare leaf (`saveReport`).
- But either `scip-typescript` is not emitting `SymbolInformation` for class methods (so they never reach the FTS index), or the FTS5 tokenizer with `unicode61` is splitting camelCase identifiers such that a bare-name query over FTS doesn't retrieve them.

Worth verifying by running `ckb doctor` against a TypeScript repo and inspecting whether `ReportPersistenceService.saveReport` has a row in `symbols_fts_content` with `name = "saveReport"`. If it doesn't, the fix is at ingest time. If it does, the fix is at query time (FTS tokenizer or exact-match path).

### 3. Document the limitation publicly until fixed

Right now the behavior is invisible to a consumer until their validator pipeline produces mysterious false rejections. A one-line note on the `searchSymbols` MCP tool description ("note: may not match class methods or record properties by bare name — see issue X") would save downstream integrators several hours of diagnosis.

---

## Acceptance criteria

A test fixture consisting of a small TypeScript file:

```ts
export const ENV_PATH = '/tmp/x';
export class ReportPersistenceService {
  async saveReport(r: Report): Promise<void> { /* … */ }
  private trackUsage(u: Usage): void { /* … */ }
}
export const settingsRouter = {
  setApiKey: t.procedure.mutation(async () => {}),
};
```

Then:

| Query | Expected result |
|---|---|
| `symbolExists({ name: "ENV_PATH" })` | `{ exists: true, matches: 1, kinds: ["const"] }` |
| `symbolExists({ name: "ReportPersistenceService" })` | `{ exists: true, matches: 1, kinds: ["class"] }` |
| `symbolExists({ name: "saveReport" })` | `{ exists: true, matches: 1, kinds: ["method"], receivers: ["ReportPersistenceService"] }` |
| `symbolExists({ name: "trackUsage" })` | `{ exists: true, matches: 1, kinds: ["method"], receivers: ["ReportPersistenceService"] }` |
| `symbolExists({ name: "setApiKey" })` | `{ exists: true, matches: 1, kinds: ["property"], receivers: ["settingsRouter"] }` |
| `symbolExists({ name: "fakeNameNobodyWrote" })` | `{ exists: false, matches: 0, kinds: [] }` |

Today, rows 3–5 come back as `{ exists: false }` from `searchSymbols` + client-side exact match.

---

## Consumer impact

ArchReview is the filing consumer. The persona-swarm evidence pipeline:

1. Pre-resolves ~100+ `symbol_exists` queries per review against CKB before the LLM runs.
2. Feeds resolved facts into LLM prompts as a `<ckb-facts>` block.
3. After the LLM returns findings, runs a validator that marks findings whose cited symbols failed to resolve as `rejected`.

Today ~20–30% of PE/DE findings end up in `rejected` because the cited symbol is a class method or tRPC procedure — false positives that look like LLM hallucinations but are really indexing gaps. The downstream user sees them as "ArchReview rejected 30% of its own findings" without the context to know the rejection was a tooling gap.

Other likely consumers for this API shape:

- Any RAG-over-code system using CKB as the ground-truth oracle for symbol grounding.
- Test-gap detectors that want to know "does function X exist before I complain there's no test for it."
- PR-review bots checking that identifiers in diff hunks correspond to real declarations after rename/move.

---

## What was considered but rejected

### Teach the consumer to do receiver-suffix fallback

ArchReview could, after a bare-name match returns empty, issue a second `searchSymbols` with `*.saveReport` and parse the result shape. Rejected because:

- Every consumer re-implements the same fallback, inconsistently.
- It doubles query volume for the most common "does this exist" check.
- It doesn't help with the SCIP staleness warning, which also needs in-band surfacing.
- Client-side workarounds mask the real gap, so CKB loses the signal that its API shape isn't fit for this use case.

### Make `searchSymbols` return methods by default

Rejected because `searchSymbols` is a ranked search API — its contract is "top-N most relevant matches for a fuzzy query," not "all matches for an exact name." Changing its behavior would affect IDE integrations and other consumers that rely on ranking. A separate endpoint with boolean semantics is safer.

### Use `getSymbol` with SCIP identifiers instead

`getSymbol` requires a full SCIP identifier — callers don't have those when the input is a bare name extracted from LLM output. The whole point of `symbolExists` is to answer "given this bare name, does *something* exist."

---

## Effort estimate

- **`symbolExists` endpoint**: Low-Medium. Reuses FTS5 index if `searchSymbols` is fixed; otherwise adds a direct `symbols` table query with exact-match WHERE clause. ~1–2 days once the indexing gap is diagnosed.
- **Indexing fix for class methods**: Medium. Needs verification whether scip-typescript emits the records, whether `convertSymbolToFTSRecord` stores them, and whether FTS5 tokenization handles camelCase identifiers. ~2–3 days of diagnosis + fix.
- **Documentation**: Trivial. One-line addition to the `searchSymbols` description; fuller note in the wiki.

---

## Related

- `internal/query/symbols.go:357` — `SearchSymbols` implementation, exact-match filter happens client-side
- `internal/query/fts.go:82` — `convertSymbolToFTSRecord`, where SCIP → FTS mapping decides what gets a `name`
- `internal/backends/scip/ids.go:89` — `GetSimpleName`, the function responsible for stripping the receiver from a SCIP descriptor
- `internal/mcp/tools.go:109` — `searchSymbols` MCP tool registration

ArchReview's consumer-side code (for reference when designing the new tool):
- `packages/server/src/services/ckb-bridge-executor.ts` — the `symbol_exists` case
- `docs/internal/personas_expereriment.md` — Phase 3.8 section documents the rejection-root-cause analysis from the smoke run that triggered this filing
