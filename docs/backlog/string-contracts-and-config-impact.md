# String contracts across repos, and config files in impact

Status: wish, 2026-11-13, from a session working on a client/server pair (Madison).
Neither would have changed that session's outcome. Nothing built.

## 1. String constants as contracts across repos

The client holds constants the server has to match: URLs and paths, URL schemes,
header names (`x-madison-*`). Today nothing links the two sides, so renaming a header on
one side is a silent break no compiler and no test in the other repo sees.

What exists:

- Federation contracts (`internal/federation/contracts.go`) know only `proto`, `openapi`
  and `graphql` files. A header name in a Go constant or a TS string isn't one.
- Federation stays behind its preset with no new investment (decided 2026-11-09). This
  wish is the first concrete use case for it since then; it doesn't reverse that by itself.
- Decided 2026-11-13: not built. One wish that wouldn't have changed its session's
  outcome doesn't reopen federation. Revisit when a second client/server pair hits a
  silent wire-name break.

Shape, if built:

- Extract string literals that look like a wire name (header `x-…`, a path with `/api/`
  or `/v1/`, a `scheme://`) from both repos, keyed by value.
- A value found in two federated repos is an edge. In assessChange, a changed or removed
  literal with an edge names the other repo's file and line.
- Open: precision. Generic values (`/health`, `content-type`) match everywhere and must
  not count. Probably only `x-` headers and paths longer than one segment.

## 2. Config files as part of impact

`config/*.toml` and `*.example.toml` (and yaml/json the same way) aren't in the impact
analysis. What exists:

- Files are classified as `config` (`internal/query/navigation.go`), and the coupling
  analyzer says "Configuration often changes together" from git history. That's all.
- `goWireKeyChange` (`internal/query/impact.go`) already notes a changed `toml:"…"` /
  `yaml:"…"` / `json:"…"` tag on a Go field, but doesn't look where the key is written.

Shape, if built:

- Code side: when a tagged key changes or goes, search the repo's config files
  (tracked `.toml`, `.yaml`, `.yml`, `.json`, `*.example.*`) for the old key and list
  them as consumers, so `example.toml` shows up as an open site.
- Config side: when a diff removes or renames a key in a config file, find the field
  whose tag reads that key (and Python/TS readers like `config["key"]`, `cfg.get("key")`)
  and report them. A key nobody reads after the change is worth a line too.
- Open: nested tables (`[server] port` vs a `Port` field in a `Server` struct) need the
  struct path, not only the leaf key; a first version can match leaf key + file name.

Built 2026-11-13, code side only (`internal/query/donecheck_config.go`): a Go field whose
`json`/`yaml`/`toml` key is renamed or removed lists the lines of tracked `.json`,
`.yaml`/`.yml` and `.toml` files (the tag's format only; lockfiles and dependencies left
out, testdata kept) that still write the old key, as done-check open sites with
`via: toml key "port" -> "listen_port"`, and the files as the contract's consumers.
A file the diff took the key out of counts as updated. At most 20 lines per key.

Still open:

- ~~Nested tables~~ Built 2026-11-13 (`donecheck_config_paths.go`): the key is matched
  where its struct puts it. The fields holding the struct are found by text in the
  tracked Go files (`Server ServerConfig \`toml:"server"\``, exported fields only,
  of the struct's own package or one importing it, through `*`, `[]` and `map[K]`,
  which adds a key of any name), up to a struct no field holds, taken to be a file's
  top. `[server] port` is a site of `ServerConfig.Port`, `[client] port` isn't listed
  at all; an untagged parent uses the format's default key, an untagged embedded
  struct (json, toml) or `yaml:",inline"` adds none. Counted with the uses that rarely
  break (`leaf-key`): a key at the top of a file when parents were found (the struct
  may also be decoded on its own), a nested one when none were, and every line where
  a parent can't be read (an anonymous struct's field, a failed `git grep`). Renaming
  CKB's own `Config.Version` json key went from 11 likely sites (every package.json
  `"version"`) to 9 counted as rarely. Still open: which file decodes into which root
  (a root's keys match the top of every file of the format), inline TOML tables and
  YAML flow mappings, a module's root package as a parent's import.
- **A dropped tag** isn't listed: the format's default key is the field name, which
  encoding/json and BurntSushi/toml match case-insensitively, so the old key may still work.
- **The config side** (a diff that renames a key in a config file → the field and the
  `cfg["key"]` readers) isn't built.
- ~~A tag-only change still lists the field's Go uses~~ Fixed 2026-11-13: a Go field
  whose declaration differs only in its struct tag skips its Go uses; only the config
  lines are open sites and contract consumers. String literals of the old key in code
  (a test asserting `{"port":…}`) aren't searched.
