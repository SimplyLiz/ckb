# Value-derived types (zod and friends) and agent-shaped output

Status: built 2026-11-12, all seven steps, on `develop`. The numbers are in the
CHANGELOG under 9.4.0, and `scripts/eval-change/replay.py` repeats them. Left open:

- The replay's open sites went from 61 to 212. Since 2026-11-12 the 165 that rarely
  break (annotations, reads of a wider key; 128 of them one key) are counted per symbol
  and not listed, so 47 are listed, with 13 exact hits (9 before) and 34 on lines `tsc`
  accepts (52 before). Listing them would add one line within three of a break and one
  file, both by coincidence. Whether an agent does better with the counts than with the
  list is unmeasured.
- Four broken lines have no site: an index access with a variable of a changed type, and
  callbacks whose parameter type is inferred from one. Nothing on the line names what
  changed, so text can't find them.
- Step 2 follows `typeof` from a value to its alias, and through the values that hold
  it. It does not follow an alias into another alias (`type A = Partial<Profile>`).
- `as const` arrays aren't read for their members the way `z.enum` is, so a Record keyed
  by one is listed whether or not it lacks a key.
- A spread or a computed key in a schema stops the reader, as does a base schema from
  another file. A literal that sets a key under a quoted name reads as lacking it. valibot, yup and the others in "Scope" get the
  `typeof` rule only.
- `ckb refs` is no faster: 0.26 of its 0.31 s is starting the engine.
- One app, two commits. No holdout.

## Why

Replaying a real data-model change (a Next.js app, zod schemas, commit "custom sections")
with only the three model files applied, against `tsc` as truth:

- `tsc` reported 23 new error lines in 10 files. The done check listed 61 open sites in
  15 files. 9 sites sit exactly on an error line, 14 within three lines. 9 error lines
  have no site near them, and two broken files are not named at all.
- Four of those nine are `Property 'custom' is missing in type …` at object literals typed
  `Profile`. `Profile` is `z.infer<typeof profileSchema>`, and the change was one line
  inside `profileSchema = z.object({…})`. That line belongs to no definition, so the
  change is a file-level entry and `Profile` never counts as changed.
- The compact answer was 15.5 KB, 9.3 KB of it open sites as JSON objects. The same
  sites grouped by symbol and file as text are 4.7 KB.
- `ckb refs` prints `file:line (reference)` with no code line, lists import lines next to
  real uses, and takes 0.66 s where `git grep` takes 0.03 s and shows the line.

The Go fix for initializers (`goextent.go`, b0b40d6) has no TypeScript counterpart. The
Go recall sample, re-run at b0b40d6, is unchanged: 9/9 compiler-broken files, open sites
at 90% precision.

## Scope

The gap is not zod. It is any type whose shape comes from a value. In the app measured:

| Pattern | Count in `src/` |
|---|---|
| `z.infer<typeof xSchema>` | 46 |
| `ReturnType<typeof fn>` | 24 |
| `(typeof X)[number]` over an `as const` array | 20 |
| `typeof table.$inferSelect` / `$inferInsert` (drizzle) | 12 |

So the work has two layers: a generic one that covers all four rows, and a zod-specific
one that makes the answer say *what* changed.

Same mechanism, other libraries (the generic layer covers them, none measured yet):
valibot `InferOutput`, yup `InferType`, io-ts `TypeOf`, typebox `Static`, superstruct
`Infer`, arktype `typeof T.infer`, effect `Schema.Type`, tRPC `typeof appRouter` with
`inferRouterOutputs`, Redux `ReturnType<typeof store.getState>`.

Out of scope, because no index edge exists and only co-change history sees them:
string-keyed coupling (Go struct tags, serde renames, SQL column names against a schema,
JSON keys read by an importer), and generated clients (Prisma, GraphQL and OpenAPI
codegen), where the source of truth is not a file the index has symbols for.

## Steps

Each step: a failing test first, then the fix, then the measurement named under it. A
step that does not move its number is reported as such and not claimed as a benefit.

### 1. TS const initializers have an extent

`internal/diff`: the TypeScript counterpart of `withGoValueExtents`. A changed line inside
`export const x = …` (object, array, call chain, arrow function) belongs to `x`. First
check what scip-typescript gives a `const` (EnclosingRange or name only); if it already
has the range, find out why the lines still fell through.

Measure: in the replay, `profileSchema`, `sectionSchema`, `resumeSchema`, `projectSchema`
appear in `changedSymbols` instead of the file-level entry.

### 2. `typeof X` carries a change to the type that names it

A type alias whose right side mentions `typeof X` (directly, or inside `z.infer<…>`,
`ReturnType<…>`, `(typeof X)[number]`, `X.$inferSelect`) is changed when `X`'s
initializer is. The index has the reference from the alias to `X`; the rule is one hop
along it, repeated while the next symbol is again such an alias. Schemas nest
(`profileSchema` uses `customSectionSchema`), so a changed schema also reaches the
schemas whose initializer references it, with a depth limit.

The existing literal rule (`tsConstruction`, `IsLiteralUse`) then lists object literals
of the derived type.

Measure: the four `Property 'custom' is missing` lines are open sites, and `sample.ts`
is named. Count how many of the nine unmatched error lines remain, and what the step
adds in sites that `tsc` does not flag.

### 3. zod: say what changed

Read `z.object({…})` keys on both sides (text, like `typefields.go`) and report per key:
added, removed, type changed, and whether the key is required in the output type.
`.optional()` is optional in both; `.default(…)` is optional on input and required on
output, which is why the literals above broke. With that:

- the contract line reads `Profile: + custom (required in output, default [])`
- a key added as `.optional()` opens no literal sites
- a removed or retyped key opens the property accesses of that key, not every use of
  the type

Cover `z.object`, `.extend`, `.merge`, `.pick`, `.omit`, `.partial`, `z.enum`,
`z.union`, `z.discriminatedUnion`. Anything else in the chain: fall back to step 2 and
say so in the note.

Measure: site count and exact-line hits in the replay before and after; a second replay
on a commit that adds an optional key must come out clear.

### 4. Rank TS sites by how likely they break

For a widened union or changed alias, order sites: index access and `Record<…>` keyed by
the type, object literals, `switch` over it, then plain annotations. Annotations of a
widened type rarely break. Do not drop them; put them last and count them in `more`.

Measure: of the first 20 sites shown, how many are on a `tsc` error line (today 9 of 61
overall).

### 5. Compact output is text an agent reads

- Open sites grouped by symbol, then file, then `line: code`; no repeated keys.
- One line per changed contract with before and after.
- The MCP path escapes `<`, `>`, `&` as `<`; `internal/output/encoding.go` already
  turns that off, the compact path does not go through it.
- Tests: the ones that reference a changed symbol, by name; the rest as a count.
- The note for a file-level entry prints the path twice (`schema.ts:schema.ts`).

Measure: bytes for the replay's compact answer (today 15.5 KB), with the same sites.

### 6. `ckb refs` in grep's shape

`file:line: code`, one per reference, with the kind of use (call, type, literal, write,
import), imports folded into one count, grouped by enclosing symbol on request. Same for
the MCP `findReferences` result. Look at where the 0.6 s go; most is likely start-up and
opening the database, not the lookup.

Measure: bytes and wall time against `git grep -n -w` for five symbols, and whether an
agent still needs to open the file.

### 7. Small things found in review

- `goImportSpecRe` also matches `return "x"`. No path to it found; narrow the pattern or
  the comment.
- A Rust impl moved to a new file that no `mod` declares is reported as moved. Check the
  diff or the tree for the `mod` line.
- `go.mod`: `require` and `replace` changes are silent now. Decide whether a major
  version change is a doubt.

## Order

1, 2, 5 first: they close the measured miss and halve the answer. Then 3 and 4, which
need 1 and 2. 6 is independent and can run beside them. 7 any time.

## Replay

Throwaway clone at the parent of the model commit, `node_modules` linked from the working
checkout, `ckb index --lang ts`, model files written from the commit, `ckb changes
--format=json`, `npx tsc --noEmit` before and after, error lines diffed. The app is
private: keep sample and results out of this repo and report aggregates only. This should
become a `--lang ts` mode of `scripts/eval-change/sweep.py` or a small sibling script, so
the numbers above can be repeated.
