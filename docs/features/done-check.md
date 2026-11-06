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
  and many of those may still compile. Outside Rust, an added field lists nothing, though a
  positional struct literal breaks on it, and a retargeted type isn't followed through the
  functions naming it.
- **Rust, still not seen:** an added enum variant, which breaks a `match` without a wildcard;
  `Self { .. }` literals, which don't spell the struct's name; a `.method()` on a later line
  counts as untouched even when the commit changed that line.
- **Test-only symbols.** Unless `includeTests` is set, a changed helper in a test file isn't
  checked.
