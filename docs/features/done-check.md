# Done check

The question at the end of a change: which uses of a declaration I changed did I leave
as they were? `assessChange` answers it in `doneCheck`, and `ckb changes` prints it.

```bash
ckb changes            # a "Done check" section with file, line and code
ckb changes --check    # the same, and the exit code: 0 clear, 2 open, 3 not assessed, 1 error
```

```json
"doneCheck": {
  "status": "open",
  "diff": "34ca51627afe",
  "openSites": [
    {"symbol": "Save", "change": "changed", "file": "b/b.go", "line": 6,
     "code": "func Run() int { return lib.Save(1) }", "consumer": "Run"}
  ],
  "openCount": 1,
  "updatedCount": 1,
  "note": "..."
}
```

`openCount` counts every use left as it was. `openSites` lists those likely to break, at
most 100; the TypeScript annotations and reads that rarely break are counted per symbol
in `rarely` and listed in `rarelySites` (see below).

It decides per use, not per file. A file the diff touched only in a comment keeps its open
call, and `consumerFiles.contractChanged` and the risk floor follow the same answer. A call
counts as touched if the diff changed any line of it, including an argument on a later line.

`diff` hashes the diff that was checked. Any edit changes it, so a result from before the
last edit can be told from a current one.

## Status

| Status | Means |
|---|---|
| `open` | At least one use was left as it was. Some may need no edit: an added optional parameter breaks nobody |
| `clear` | None was, and the lookup could have found them. Nothing else is claimed |
| `not_assessed` | None was found, but the lookup can't vouch for that |

`not_assessed` is what you get without a SCIP index, or while the first one is building;
with the index behind HEAD or its commit unknown; when a reference list was cut or a lookup
failed; when a changed declaration can't be compared; and when a changed file isn't in the
index. Uncommitted edits don't count against it, since they are what the check looks at.

An import of a changed name doesn't count as open, as the import stays valid. An import of
a removed name does.

## Rust: what the changed symbol's references don't reach

Three kinds of change break code that never names the changed symbol, so a site can carry
`via`, saying why it is listed:

| Change | What is listed | `via` |
|---|---|---|
| A struct's fields differ | Literals and patterns without `..`, and calls of a `new` a macro generates from the fields, which the index knows only by its calls (Salsa's `Foo::new(db, a, b)`) | `fields of Foo changed` |
| A type alias changed or went away | Uses of each function whose signature names it. Their callers hold the new type without spelling the alias. One level, aliases only | `signature names R, which changed` |
| A `pub use` went away | Imports through that module, named or by glob, found by text on the new side | `prelude no longer re-exports it` |

These come from reading what commits did to the uses the compiler rejected, and were checked
against three of them. See `docs/eval/change-prediction.md` for what has and hasn't been
measured.

## Go and TypeScript: a type whose members changed

scip-go and scip-typescript give a type no enclosing range, so a field added inside a
struct or an interface reaches no symbol, and the type's first line reads as unchanged.
Each Go struct and each TypeScript interface, class or object type alias declared in a
changed file is read on both sides instead. When its members differ (a struct tag alone
doesn't count, and a class's methods don't), the places that build the type are listed
with `via: fields of Foo changed`:

| Language | What is listed | Not listed |
|---|---|---|
| Go | Composite literals: `Foo{`, `&Foo{`, `[]Foo{{`, `map[K]Foo{"a": {`, `Foo[T]{`. The brace has to follow the name directly, as gofmt writes it | `Foo{}`, `[]Foo{existing}`, `func f() Foo {`, the receiver, the declaration |
| TypeScript | `: Foo = {`, `: Foo[] = [`, `): Foo {` and `): Foo => ({` as a function's return, `<Foo>{`, `} as Foo`, `} satisfies Foo` | imports, parameters, `extends`, `implements`, `= await load()`, `Promise<Foo>`, the declaration |

A literal the diff touched anywhere between its braces counts as updated, and so does a
function returning the type whose body the diff touched. A keyed Go literal still compiles
without the new field, so a listed site may need nothing; it is listed because the question
is whether the field was handled where the type is built. An edit to an existing member
no longer makes the check `not_assessed` for "a declaration that can't be compared": the
type's body was compared, and that explains the line.

## TypeScript: a type that takes its shape from a value

`type Profile = z.infer<typeof profileSchema>` changes when a key is added to the schema,
and no line of `Profile` moves. scip-typescript gives a const only the line of its name,
so the edit used to be a file-level entry and the check said `clear`. Now:

- A changed line inside a module-level `const`, `let` or `var` belongs to it.
- `typeof` carries the change to the alias or interface that reads it, whatever wraps
  it: `z.infer<…>`, `ReturnType<…>`, `(typeof KEYS)[number]`, `typeof table.$inferSelect`.
  It also reaches the values whose initializer holds the changed one, four levels deep
  (a schema inside a schema), but not a function that merely uses it.
- For a zod schema the text says what changed, and that decides what is open:

| Change to a key | Contract line | Open |
|---|---|---|
| added with `.default(…)`, or added required | `+ custom (required in output, default [])` | literals of the type that lack the key and have no spread, also under the key of a type that holds it |
| added `.optional()` | `+ note (optional)` | nothing |
| a validator changed (`.max`, `.regex`) | none | nothing |
| removed | `- nick` | every place that names the key |
| retyped | `~ kind: A -> B`, with `(wider)` if B holds A | the places that name it; for a wider type only the reads |
| `.optional()` added to an existing key | `~ name: now optional` | the reads |

  `z.enum`, `z.union` and `z.discriminatedUnion` are compared by member: one added opens
  the `Record` keyed by the type, one removed every literal. A chain the reader can't
  follow, such as `.transform(…)` or a base schema from another file, and any value that
  isn't a zod schema, lists every literal of the type as before; the site's `via` says
  what stopped it.
- Sites of a changed TypeScript type or key carry `use` and are ordered by it: `key`
  (`Record<T, …>`, `[K in T]`, `K extends T`) and `index` (`x[y.key]`) first, then
  `literal`, then `switch`, then `annotation` and `read`.
- The last two rarely break, so they aren't in `openSites`. Each changed symbol or key
  with such uses gets one line instead: how many, in how many files, by use. They are
  still open: a symbol with nothing else keeps the check `open`, and `openCount`
  counts them.

| Where | Listed sites | Rare ones |
|---|---|---|
| compact (`assessChange`, `ckb changes --format=compact`) | `openSites`, 50 a page | one line per symbol under "rarely break"; focus `rarely` lists them, 50 a page, with `offset` |
| full JSON (`format: "full"`, `--format=json`) | `openSites`, cut at 100, `truncated` | `rarely` (the counts) and `rarelySites` (each one, uncut), `rarelyCount` |
| `ckb changes` | the first 20, `--verbose` all | one line per symbol; `--verbose` lists them |

  The rare ones are a list of their own under a focus because `offset` already pages
  the focused list: they page the same way as open sites and tests, and need no second
  paging parameter.

Text, like everything here. Four kinds of break stay out of reach: an index access with a
variable of a changed type, a callback whose parameter type is inferred from one, an
alias of an alias (`Partial<Profile>`), and a key set under a quoted name, which reads as
missing.

## Files that usually change together

References reach a use of a changed declaration. They don't reach an importer that
moves with an editor by habit, and outside Rust they don't reach an added field at all.
`coChangeCheck` reads git history instead, and `ckb changes` prints it as "Usually
changed with":

```json
"coChangeCheck": {
  "status": "open",
  "openFiles": [
    {"file": "cv/importer.go", "with": "cv/editor.go", "together": 5, "of": 6,
     "share": 0.83, "lastTogether": "2026-11-06"}
  ],
  "openCount": 1,
  "commitsRead": 6,
  "note": "..."
}
```

It reads the last 400 commits that touched a changed file, skipping any commit of more
than 50 files, and counts for every other file how many of a changed file's commits it
was in. A file is listed when it was in at least half of them, 3 at least, and the
diff doesn't touch it. A commit that touched a changed file alone counts too: it is
evidence that the file moves on its own. `with` names the changed file it moves with
most, and `share` is `together / of`.

It is `not_assessed` when no changed file has 3 commits yet, and `clear` otherwise.
It is history, not code: a listed file may need nothing this time, and a file that
needs an edit may never have moved with the changed one before. It works for every
language, and for files that aren't code.

## What it can't tell you

It compares text and compiles nothing, so it says "open", never "safe".

- **A touched use can still be wrong.** Changing the line says nothing about the new
  arguments being right.
- **New callers in the diff aren't in the index,** so a call the diff adds with the old
  signature is not listed.
- **Interface implementers aren't references.** A changed interface method doesn't list the
  types implementing it.
- **Behaviour isn't looked at.** A declaration with the same text and a new meaning changes
  nothing here.
- **Other repositories aren't visible.** An exported symbol keeps its medium-risk floor for
  that reason, even when the check is clear.
- **Types are uneven.** A changed struct field lists the uses of that field, which is what
  you want. A retargeted type (`type ID int` to `type ID string`) lists every use of the type,
  and many of those may still compile. In Go and TypeScript a changed member set lists the
  literals and constructions above, but not a function whose signature names the type, and
  a retargeted type isn't followed through the functions naming it. Python has none of this.
- **Rust, still not seen:** an added enum variant, which breaks a `match` without a wildcard;
  `Self { .. }` literals, which don't spell the struct's name; a `.method()` on a later line
  counts as untouched even when the commit changed that line.
- **Test-only symbols.** Unless `includeTests` is set, a changed helper in a test file isn't
  checked.
