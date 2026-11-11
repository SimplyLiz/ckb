# Changelog

All notable changes to CKB will be documented in this file.

## [9.4.0] - 2026-11-08

### Added — the done check

- **`doneCheck` in `assessChange` and `ckb changes`, and `ckb changes --check`.**
  It lists each use of a changed declaration that the diff left as it was, with
  file, line and code, under the status `open`, `clear` or `not_assessed`.
  `--check` sets the exit code: 0 clear, 2 open, 3 not assessed, 1 still an error.
  It compares text and compiles nothing, and `clear` has to be earned: an index
  behind HEAD, a cut reference list, a failed lookup or a changed file outside the
  index all make it `not_assessed`. See `docs/features/done-check.md`.
- **Measured, including where it falls short.** On 30 SWE-Bench ProMax instances, the
  open uses found 25/28 of what the compiler says broke on Go, 23/23 on TS (nearly all
  one repo) and 8/17 on Rust. With the whole commit applied, 12 of 30 still report open
  uses, and none the compiler could judge had broken, so `open` doesn't mean broken.
  `docs/eval/change-prediction.md` has the numbers and the limits.
- **Rust: uses the changed symbol's references don't reach.** A struct whose fields
  changed lists its literals, its patterns without `..` and calls of a macro-generated
  `new`. A retargeted type alias lists the uses of functions whose signature names it.
  A removed `pub use` lists the imports still going through it. Such a site has `via`,
  saying why it is listed. Written from the Rust misses above and not yet measured: the
  8/17 is from before it. Since measured on a holdout of 12 axum, helix and ripgrep
  commits: no new file found (8/9 of what the compiler says broke, before and after, all
  from 3 axum commits, so recall is barely tested), and false alarms went up, from 343 to
  358 open uses, with one commit losing `clear`. All 17 sites it added were `-> &mut Foo {`
  read as a struct literal. That bug is fixed. A second holdout of 20 commits whose parents
  compile finds the same files with both binaries (13/13), with 38 fewer open uses: 43 fewer
  on one commit, 5 more from these rules, all false alarms. So they stay unproven outside the
  commits they came from.
- **Go and TypeScript: a type whose members changed lists the places that build it.**
  scip-go and scip-typescript give a type no enclosing range, so a field added to a
  struct or an interface reached no symbol: the change was a file-level entry at low
  confidence, risk stayed low, and the done check said `clear`. Adding a field to
  `impact.ChangedSymbol`, built by literals in seven files, said exactly that. Now each
  Go struct and TypeScript interface, class or object type alias declared in a changed
  file is read on both sides, and when its members differ (a tag alone doesn't count) the
  sites that build it are listed with `via: fields of Foo changed`: Go composite literals
  (`Foo{`, `&Foo{`, `[]Foo{{`, `map[K]Foo{"a": {`, not `Foo{}` or `[]Foo{existing}`),
  and TypeScript constructions (`: Foo = {`, `: Foo[] = [`, `): Foo {` and `): Foo => ({`
  as a function's return, `<Foo>{`, `} as Foo`, `} satisfies Foo`). An edit to an
  existing member no longer makes the check `not_assessed` for "a declaration that can't
  be compared": the type's body was compared, and the check says so. Text, not types,
  like the Rust rules, and like them not yet measured on a holdout.
- **`coChangeCheck` in `assessChange` and `ckb changes`: the files that usually change
  with the touched files, and were left alone.** The done check follows references,
  so it can't see an importer that moves with an editor by habit rather than by type,
  and outside Rust it doesn't see an added field at all. This check reads git history
  instead: the last 400 commits of the changed files, skipping commits of more than 50
  files, and lists every file that was in at least half of a changed file's commits
  (3 at least) and isn't in the diff, with `file`, `with`, `together`, `of` and
  `lastTogether`. Status `open`, `clear`, or `not_assessed` when no changed file has
  3 commits yet. It is history, not code: a listed file may need nothing. Works for
  every language and for files that aren't code.
- **`assessChange` is in the default preset,** and in every preset built on core,
  so core goes from 25 tools to 26. The first 512 bytes of the MCP instructions now
  end on it.

### Added — MCP tool annotations

- **Every MCP tool says whether it changes anything.** `tools/list` now carries
  `annotations`: `readOnlyHint: true` on the 93 tools that only read the index, git and
  the cache, so Claude Code runs them in parallel with other read-only calls instead of
  one after another. The 18 that change something say so, with `destructiveHint`,
  `idempotentHint` and `openWorldHint`: the index and `.ckb` database (`reindex`,
  `annotateModule`, contract edges…), an ADR file (`recordDecision`), session state
  (`expandToolset`, `switchRepo`, `switchProject`, kept out of parallel runs because
  every later call depends on them), federation config, and the daemon's schedules and
  webhooks. The three read-only federation tools that ask a remote server carry
  `openWorldHint: true`. Costs about 1 KB on the default preset's `tools/list`.
- **Large whole-result tools stay inline in Claude Code.** Claude Code moves a text
  result over 50,000 characters to a file and shows the model a preview. `assessChange`
  ran to 101k characters on a five-commit diff of CKB, `reviewPR` to 60k. Those two,
  `analyzeChange`, `findUnwiredModules`, `findDeadCode` and `summarizeDiff` now set
  `_meta["anthropic/maxResultSizeChars"]` to 150,000. `exportForLLM` (408k) keeps the
  default: an export that size belongs in a file.
- **`prepareChange` and `assessChange` load upfront** (`_meta["anthropic/alwaysLoad"]`).
  The server instructions tell the agent to call them around every edit; deferred
  behind tool search, they cost a search first.
- **`doneCheck` and `coChangeCheck` come right after `summary`** in `assessChange` and
  `analyzeChange`. They sat behind 63k characters of `changedSymbols` and
  `affectedTests`, so a preview of a large result never reached them. Now they start
  at character 156.

### Added — MCP 2026-07-28

- **CKB speaks the stateless MCP revision next to the old handshake.** A request whose
  `_meta` names `2026-07-28`, and `server/discover`, take the new path; everything else
  works as before. `server/discover` answers with the supported version, capabilities,
  instructions and caching hints; results carry `resultType` and `serverInfo`;
  `tools/list` and `resources/list` carry `ttlMs` and `cacheScope`; other versions get
  `-32022` with the supported list; a request without `clientCapabilities` is invalid
  params; `subscriptions/listen` is acknowledged and closed at once, since nothing a
  modern client sees changes while CKB runs.
- **The revision forbids state across requests, so two things work differently for
  it.** `tools/list` is the preset CKB was started with, and `expandToolset` is not
  offered: a wider set means `--preset=review` or `--preset=full`. `switchProject` and
  `switchRepo` are replaced by an optional `repo` argument on every repo-bound tool (a
  path, relative to the start repo, or a registered name), and every call starts from
  the start repo, so a file path from another repo in one call no longer redirects the
  next.

### Fixed — MCP protocol

- **Switching repositories switches the index and git with them.** `ckb init` writes
  `"repoRoot": "."`, and the SCIP, git and LSP backends read it as the process's working
  directory, not the repository. An engine for another repo (`switchProject`,
  `switchRepo`, a file path from another repo) looked for that repo's `index.scip` in
  the start directory and ran git there: after "Successfully switched to Lebenslauf",
  `getStatus` said "Index not found" and `getHotspots` listed CKB's own `CHANGELOG.md`
  and `internal/query/impact.go`. The same made `ckb status` from a subdirectory report
  no SCIP index in an indexed repo. The engine now resolves a relative `repoRoot`
  against its repository.
- **CKB answers the MCP revision the client asks for.** `initialize` always said
  `2024-11-05`, whatever the client sent. It now agrees on 2025-11-25, 2025-06-18 or
  2024-11-05, and answers 2025-11-25 to anything else. 2025-03-26 is not offered: it
  requires JSON-RPC batching, which CKB does not read, and 2025-06-18 dropped batching
  again.
- **`ping` gets an answer.** It returned "Method not found"; every revision since
  2024-11-05 requires an empty result.
- **A failing tool says so.** The error envelope came back as an ordinary result;
  it now carries `isError: true`. Calling a tool that does not exist is reported as
  invalid params instead of an internal server error.
- **`resources/read` returns JSON.** The text was a Go struct dump (`&{...}`) labelled
  `application/json`, so every `ckb://` resource was unreadable.

### Added — a notice after updating

- **The first run of a new release says what's new, once.** It also states the
  license and asks for feedback. Stderr only, only on a terminal, never in `ckb mcp`,
  `ckb serve` or shell completion, and off with `CKB_NO_NOTICE=1`. The news belong
  to the minor release, so going straight from 9.3 to 9.4.1 still shows them and a
  patch update doesn't repeat them. A fresh install records its version silently;
  `~/.ckb/update-check.json` or `~/.ckb/ckb.db` from an older version marks an
  upgrade. An upgrade first run from a script stays pending for the first run a
  person reads, and two runs started together show it once.
- **Homebrew shows the same text as caveats** after install and upgrade, which also
  reaches people who only run CKB through an MCP client. Interactive `ckb setup`
  prints one line about the license for them.
- The update box tells Homebrew installs to run `brew upgrade ckb`, no longer takes
  `2>/dev/null` for a terminal, and a flag in front of the command (`ckb -v mcp`) no
  longer gets past the protocol-server check.

### Fixed — first contact with a repo

- **`ckb changes` and `assessChange` no longer call a change low risk when they
  could not look.** Without a SCIP index there is no consumer graph, so "0
  consumers" meant nobody checked, and the headline still read `Risk: LOW`. The
  risk level is now `not_assessed` (`Risk: NOT ASSESSED`, "downstream consumers
  unknown") unless another backend did find consumers. **`riskScore.level`,
  `summary.estimatedRisk` and `blastRadius.riskLevel` can now hold this value**;
  anything switching on them has to handle it.
- **Nested projects index.** With a manifest in a subdirectory
  (`engine/Cargo.toml`, `svc/go.mod`) the indexer wrote `engine/index.scip` while
  CKB checked the repo root, so `ckb index` failed with "index.scip was not
  created". The index now lands at the configured path and its document paths
  are rewritten to be repo-relative, so references resolve. This goes through
  `ckb index`, `--watch` and MCP `reindex`.
- **Nested projects always get a full index.** Incremental indexers run from the
  repo root, found no project there and reported an empty success over a stale
  index.
- **Incremental indexing sees changes outside Go.** Change detection only looked
  at `.go` files, so in a TypeScript, JavaScript, Python, Dart or Rust repo with a
  `.ckb/ckb.db`, `ckb index` after a commit said "Index is up to date", skipped the
  indexer and still stamped the index with HEAD: stale, and counted as fresh. On a
  TS repo, a function added in a new commit was in neither `index.scip` nor
  `ckb.db`; it now is, through an incremental update of the one changed file. This
  goes through `ckb index`, `--watch`, the daemon and MCP `reindex`. Tracking after
  a full index also kept only documents whose language was `go` or unset, so a
  rust-analyzer index (`rust`) tracked no file at all.
- **The update box only goes to a terminal.** Agents and scripts capture stderr
  with the result and were filtering the box out of every call by hand.

### Fixed — found by running CKB on its own repo

Three changes with known breakage, checked against the compiler: a new parameter on
`query.NewEngine` (20 call sites), a renamed field `backends.SearchOptions.MaxResults`
(17) and a renamed method on the `backends.Backend` interface (4 implementations).

- **The done check lists the implementations a changed Go interface leaves behind.**
  Renaming, re-typing or adding an interface method said `clear` before, because an
  implementation isn't a reference. It now finds them through the index's
  implementation relations and, since scip-go only records those within a package,
  through types whose methods match the interface's as it was before the diff. The
  sites carry `via: "implements Backend"`. Interface rename: 4/4, added method: 2/2
  (the two types without it). If the lookup can't run, the status is `not_assessed`.
- **A struct literal is listed once.** When a field changed, a literal that names it
  was listed for the field and again as "fields of X changed". The literal's own entry
  now only stays when no field use inside its braces is open, for instance a literal
  that leaves the field out. The field rename went from 30 sites back to 17.
- **A symbol ID from before the last commit still resolves.** scip-go puts the commit
  hash into every ID, so each commit invalidated the IDs an agent held. MCP and A2A
  calls now retry without the version field, take only a unique match, and say so in a
  `SYMBOL_ID_VERSION_REMAPPED` warning. The HTTP API does the same and names both IDs
  in an `X-CKB-Symbol-Id-Remapped` header; the CLI (`symbol`, `refs`, `impact`,
  `impact prepare`, `impact outgoing`, `callgraph`, `justify`, `trace`) prints a note on
  stderr.
- **`prepareChange` lists the tests that use the target.** Before, it listed the first
  20 test files of the module, alphabetically; for `NewEngine` none of the 14 that call
  it. It now uses the same derivation as `assessChange`: direct, then transitive, then
  same-module files as filler, each labelled by its real reason.
- **`prepareChange` on a file counts what the file declares.** `internal/backends/backend.go`
  gave 0 affected files; with its types, fields and methods it is 134 callers in 9 modules.
- **Co-changed files carry their path.** `coChangeFiles` said `symbols.go`; it says
  `internal/query/symbols.go`. That also fixes review's split graph, which compared the
  basename against full paths and never matched, and `explainOrigin`'s co-changes.
- **`assessChange` answers in `format: "compact"` by default** (`ckb changes
  --format=compact`): no symbol IDs, open sites as `file:line`. 7.5 KB instead of 25.8 KB
  for the `NewEngine` change. `format: "full"` returns the previous answer.
- **`assessChange` takes a `focus`**: `done` (open sites, co-change files left alone),
  `tests` (affected test files and `runTests`, a command that runs them) or `risk`
  (score, `riskFactors`, contract changes, consumer files). The summary still covers
  everything. `offset` and `limit` page the open sites or test files, and `more` names
  the next offset. Same flags on `ckb changes --format=compact`. `riskScore` is
  rounded to two places (it read `0.6599999999999999`).
- **`ckb activity --followups <tool>`** counts which CKB call came next in the same
  session after each call of the tool, labelled by `focus`, `format`, `changeType` and
  `offset`. It shows what callers had to fetch after an answer; `--all` reads every
  registered repo. Only CKB calls are in the ledger, not the file reads in between.
- **A Go constant moved into a `const (...)` block is not removed.** Inside the block the
  line has no keyword, so the declaration wasn't found on the other side and its uses
  were listed as open sites under `definition-removed`. A `const`, `var` or `type`
  block the diff shows now lends its keyword. Found by running `assessChange` on the
  change that added `focus`. Test names and affected symbols carry short names, not SCIP IDs, and
  `affectedBy` names the changed symbol that selected the test.
- **A declaration is read from the whole file, not only the hunks.** A constant moved
  out of a long `const (` block whose opening line the hunk doesn't reach was still
  `removed`: the block's keyword was invisible. Both sides of each changed file are
  read now (`git show` at the diff's base or merge base, the index with `--staged`, the
  working tree), and only if they agree with every line the hunks show; a diff passed
  in from outside whose files don't match falls back to the hunks. Multi-line
  signatures that ran past the diff's context are compared in full as well.
- **A Go declaration moved to another file of its package is not removed.** Callers
  name the package, so moving a function, method, type or constant between files
  changed nothing for them, yet the old file's side listed every use as an open site.
  The declaration is now looked up in the diff's other files of the same directory and
  compared there: unchanged, or `changed` if its signature changed on the way. Into
  another directory, or between a package and its external `_test` package, it stays
  `removed`.
- **A Go struct's literals are open where the compiler rejects them.** A field added to a
  struct listed every composite literal of it, though a keyed literal still compiles. Now
  a positional literal is open on any field change, and a keyed one only if it sets a
  field that went away or changed type; the elements of `[]T{...}` and `map[K]T{...}`
  are read one by one. TypeScript keeps the old rule.
- **A Go type's rename carries its methods and the signatures naming it.** Exporting a
  type and moving it to another package (`exportRules` → `deadcode.ExportRules`) listed
  each unchanged `r.line(...)` as a use of a removed method, and `parseSymbolInfo(...,
  rules)` as a changed signature. A method found again on a type the diff declares, and
  a signature whose only difference is the new name, now follow the rename; the type
  itself stays `removed`, so code still naming it is still listed.
- **A parameter isn't a declaration of its name.** `func (r *T) line(...) (text string,
  file *sourceFile, ok bool)` counted as declaring `file`, so a declaration looked for on
  the other side of the diff could be paired with the wrong one. The name now has to
  follow its keyword (Go, TypeScript and Rust).
- **A server older than its binary says so in every answer.** `meta.staleServer`, checked
  at most every 30 s, instead of only in `getStatus`. Both now say to reconnect the
  server (`/mcp` in Claude Code); `getStatus` said `/clear`, which doesn't restart it.
- **`prepareChange` with `rename` or `delete` gets the floor `assessChange` applies.**
  With production callers the risk is at least high (0.6), as `assessChange` scores the
  same change once it is in a diff. The `NewEngine` case read low 0.15 before the edit
  and high 0.6 after it. `modify` keeps the count-based score, since it can't tell a
  body edit from a signature change.
- **`affected_count` no longer counts direct callers twice.** The compact `prepareChange`
  and `ckb impact prepare` added the direct callers to a total that already held them.
- **`understand "Backend.Priority"` means Backend's `Priority`**, not the first method of
  that name, and the symbol has its module again (it read "in module  –").

### Fixed — what CKB says about a change

These change what `assessChange`, `prepareChange`, `analyzeImpact` and
`searchSymbols` report, so expect different numbers than in 9.3.

- **`analyzeImpact` counted no callers for indexers without access roles.**
  scip-dart and scip-typescript mark an occurrence with no read or write role,
  so every call was `unknown`: the report said "0 direct caller(s)" and scored
  the risk on that. A reference inside a function is now a caller; one outside
  any function stays `unknown`, since it is as likely an import.
- **Blast radius inflated by SCIP locals.** A local function's `local N` ID is
  unique per document only, and was treated as global: on a Flutter app, 41 of
  108 impacts landed in generated localization code. Locals no longer enter the
  call graph; what a local function calls is attributed to the function around it.
- **Callers for indexers without `EnclosingRange`.** References in scip-dart
  output had no enclosing symbol. Function extents are now inferred, as the
  caller index already did.
- **One risk level per `analyzeImpact` answer.** `blastRadius.riskLevel` was a
  size class from module and caller counts, while `riskScore.level` weighed
  visibility, callers, spread and kind, so the same response said `medium` next
  to `high`. It now carries the score's level, as `prepareChange` already did;
  the counts are unchanged.
- **`contractChanged` checks the use, not the file.** A file counted as handled as
  soon as the diff touched it, so a comment edit hid a call the compiler still
  rejects, and took the risk floor with it. A consumer inside the diff now counts
  only if its use was changed. The wording said "outside this diff"; it now says
  "left as it was".
- **`findReferences` returns the source line.** Every `context` read
  `// Context at file:line:col`, from a stub that was never filled in. A line that
  no longer holds the symbol's name comes back empty, because the index can be older
  than the file.
- **The preset counts in the READMEs and in `ckb setup` are right again.** Some were
  off by sixty tools.
- **Changed symbols are the definitions you changed.** Every symbol merely
  referenced on a changed line used to count as changed, so editing a line that
  called `logger.Info` charged the change with `logger.Info`'s blast radius.
  Locals and packages counted too. A changed symbol is now the innermost
  definition around the changed line.
- **Deletions are reported.** Only added lines were mapped, so a pure deletion
  produced nothing. It now maps to its function, a deleted definition appears as
  `deleted` with its callers, a deleted symbol still used elsewhere is at least
  high risk (`deleted_with_consumers`), and a deletion the index can't resolve
  is flagged instead of dropped.
- **Callers count as callers.** SCIP has no "call" role, so every call arrived
  as a read and was classified as a type dependency. `analyzeImpact` said "0
  direct callers", low risk, for a function with real callers.
- **SCIP symbol kinds use the protocol's enum.** Two hand-written tables had the
  wrong numbers (a real `Function`, 17, mapped to `interface`).
- **`not_assessed` everywhere it applies.** `prepareChange`, `analyzeImpact` and
  `planRefactor` said low risk without a consumer graph too. The `ckb impact
  diff` output no longer prints a checkmark over it.
- **Comment and whitespace changes** get confidence 0.3 with a note.
- **Module spread** counts direct and transitive callers; the more-than-5-modules
  factor could never fire before.
- **Members of unexported Go types** are no longer public API, in the SCIP and
  the search path.
- **A caller is no longer lost to a local variable in it, or to another blank
  consumer.** rust-analyzer gives `let id = save(x)` an enclosing range, so the
  local counted as the reference's container and the caller came back blank.
  Consumers were then deduped on that blank ID, across the whole change: the first
  one survived and every other file-level consumer (imports, top-level uses, and
  those lost callers) vanished without a trace. Locals and parameters are no longer
  containers, blank consumers dedupe per file, and a consumer that is direct for one
  changed symbol stays direct when another changed symbol reaches it first.
- **Tests that use a changed symbol are in `affectedTests`, always.** `ckb changes`
  and `assessChange` dropped test references unless asked, and only saw a test file
  when it sat in the first 20 consumers. `includeTests` now only decides whether test
  code also counts as a consumer: by default it no longer inflates `affectedSymbols`,
  the consumer counts or the risk, including transitive callers in test files, which
  were counted before. `ckb impact` keeps tests out of its consumer lists the same
  way. `reason` is `direct` only for a test seen using the symbol; a test merely named
  after the changed file now says `naming-convention`. Entries list `testNames`, and
  test detection covers `.spec.*`, `__tests__/`, root `tests/`, `e2e/` and Rust's
  `tests.rs`.
- **A cut consumer list no longer looks complete, and no consumer file is lost to
  it.** `budget.maxImpactItems` (20) was applied per symbol and again per change, and
  the CLI JSON had no field saying so. `consumerFiles` (`direct`, `transitive`,
  `tests`, `total`) now lists every file using a changed symbol in `ckb changes`,
  `ckb impact`, `assessChange` and `analyzeImpact`, up to 300 paths, with `truncated`
  and a `note` if even that was cut or the lookup itself stopped short. The detailed
  items stay budgeted, once per change, and `truncated` and `truncationInfo` are in
  the CLI JSON and the human output. `summary` counts are now the real ones rather
  than counts of the per-symbol samples, so a wide change reports more consumers,
  and can score higher, than before.
- **Risk now looks at what the change did to the declaration.** It scored counts of
  symbols, consumers and modules, so a changed signature with one caller came out
  `low` next to a broken build, while the "possible contract change" flag fired on
  any edit of an exported symbol, including a body edit, and never on an unexported
  one whose callers break all the same. The diff is now read for each changed
  symbol's old and new declaration, in Go, TypeScript/JavaScript and Rust:
  `declaration` on `changedSymbols` says `unchanged`, `compatible` (a new optional
  parameter, wider visibility), `changed` or `removed`. A changed or removed
  declaration with production consumers outside the diff is at least `high`
  (`signature_changed_with_consumers`, alongside the existing
  `deleted_with_consumers`), an exported one without any is at least `medium`
  (`public_contract_changed`), and a body edit gets no floor and no contract flag.
  `contracts` entries now say which (`signature-changed`, `definition-removed`,
  `compatible-signature-change`, with `before` and `after`) and only fall back on
  visibility for code that can't be read. `consumerFiles.contractChanged` and
  `contractChangedTests` name the files using such a symbol: the ones to look at
  first. This compares text and compiles nothing, so it stays labelled a heuristic.
- **`ckb changes` and `ckb impact diff` see staged edits.** Both promise the
  uncommitted working tree but ran a bare `git diff`, which compares against the
  index, so anything `git add`ed looked unchanged.
- **A comment-only change has no blast radius.** It was listed with its note, then
  analyzed like any other change anyway, consumers and all.
- **`ckb changes` with nothing changed says so** instead of `Risk: LOW`, as does
  `ckb impact diff`. In JSON the risk is `not_assessed`.
- **`ckb impact` with a tree-sitter ID says an index is needed.** Search without a
  SCIP index hands out `ts-` IDs. Impact then said "not found, use `ckb search`",
  which returns the same ID. It can't analyze those without an index, so it now
  says that and how to get one.
- **A widely used consumer no longer looks untested.** `testGaps` read a budgeted
  consumer list, which cuts transitive items first, so past 20 direct callers a test
  reaching it through one of them was gone.
- **The affected-tests command uses vitest where the repo does.** It suggested
  `npx jest` in every JavaScript repo without a jest config, vitest projects included.
  A vitest config or a vitest dependency now gives `npx vitest run`.
- **The review's blast-radius check looks at what changed.** It took every symbol
  of each changed file, in diff order, until 30 were taken, so on a 40-file Next.js
  branch the first component file filled all 30 slots and `listOf` in
  `src/lib/cv/entries.ts`, changed and called from eight places, was never looked at.
  The summary didn't say so. It now counts callers for the changed functions and
  methods, spreads the 30 slots over the files (each file's best-referenced first),
  and says "30 of 52 changed functions checked" when it had to cut. Types no longer
  take slots; they have references, not callers, and always reported 0. Without a
  SCIP index the check says it skipped instead of passing. It shares one change-set
  analysis with the tests check instead of computing it twice.
- **The review's tests check missed two thirds of the tests.** On the same branch it
  found 24 test files; 63 import a changed module directly. A change inside an object
  literal, such as a new action in a zustand store, is attributed to the literal's
  members, so a test reaching the store through `useEditor` used no changed symbol.
  A test that uses something defined in a changed file now counts as well, with
  `reason: uses-changed-file`, confidence 0.5 and `summary.fileLevelFiles`, and the
  summary says how many tests rest on which: 70 on that branch, 23 through a changed
  symbol. A test helper under `tests/` (`tests/render/fixtures.ts`) is test code but
  no test, and is no longer listed as one in JavaScript and TypeScript, where
  vitest and jest only run `*.test.*`, `*.spec.*` and `__tests__/`.
- **A review whose checks all pass is not a pass at 20/100.** The score is 100 minus
  a deduction per finding, the verdict looked only at check statuses, and the two
  never met: warning findings in a passing check, or enough of them to sit on the
  score's floor of 20, still said `pass`. A pass now also needs no warning or error
  finding and a score of at least 50; otherwise the review is `warn`. Findings never
  make it `fail` on their own. `verdictReason` says which checks decided it, or why
  passing checks weren't enough. The `split` check was added after the verdict was
  set and never counted; it does now.
- **The review says what the index can't see.** A member reached by a computed key
  (`profile[kind]`, `WORDS[kind]`) has no SCIP reference, so callers, tests and
  blast radius leave it out, and on the branch above that is where the real problem
  sat. When the diff adds such lines in JavaScript, TypeScript, Python or Ruby,
  `limitations` names them, up to five, and the human and markdown output print them
  under "Not seen".
- **Coupling no longer reports docs and sweeping commits.** On that branch 9.3
  reported 59 co-change gaps, 56 of them from the hidden-coupling pass, which had no
  floor and warned about a 14% "coupling", and nine involving Markdown
  (`docs/roadmap.md` with `HeroCards.tsx`). Docs (`docs/`, `.md`, `.mdx`, `.rst`,
  `.adoc`, `.txt`) and test helpers under a root `tests/` are now noise on both
  sides, the hidden pass uses the same 70% floor as the regular one, and
  `ckb coupling` / `analyzeCoupling` leave out commits touching more than 50 files,
  which made every file in them 100% coupled to every other (Cartographer already
  did). On that branch two findings are left, both code files.
- **A review diffs from where the branch forked.** `ckb review`, `summarizePr` and
  `ckb changes --base` ran `git diff main HEAD`, which compares the two tips: once
  `main` moved on, every file it changed since the fork was in the review, its
  changes reversed, as if the branch had undone them. They now diff from
  `git merge-base`, and so do the "before" sides of complexity, health, bug
  patterns and the API comparison. Without a merge base (a shallow clone, unrelated
  histories) they fall back to the tip and log a warning.
- **Coupling says the same thing twice in a row.** A file's partners came out of a
  map, were sorted by rate alone and cut at five, and only then were the files the
  change already touched dropped. With more than five partners at the same rate,
  each run kept a different five: on that branch four runs gave 0, 1, 1 and 0
  findings. Ties are now ordered by count and path, and the cut comes after the
  changed files are gone, so both missing partners are found on every run.
- **`ckb unwired` finds unwired code outside Go.** It only counted names starting
  with a capital letter as exported, so lowercase TypeScript, Python and Rust
  functions were never candidates. Neither scip-typescript nor rust-analyzer
  records `export` or `pub`, so for those the definition line decides: `export`
  (or a local `export { … }`, `export default` or CommonJS export) in TS/JS, a
  bare `pub` in Rust, no leading underscore in Python. A member counts if its
  class does. Not counted: module-private functions, `pub(crate)` and private
  `fn`, which the compiler's own unused-code checks already cover. Go keeps the
  capital-letter rule. Two more fixes were needed for this to report anything.
  The import walk counted every definition in a reachable file as reachable, so
  an unused export sitting next to a used one never showed. And every Go and
  Rust item had an empty `filePath`, because neither indexer puts the file in
  the symbol ID. So `ckb review`'s unwired check, which keeps only items in
  changed files, could never fire on those repos, `_test.go` symbols got past
  the test filter, and everything landed in module `.`. File and line now come
  from the definition. Limit: a Rust `pub fn` that shares its line with `impl …
  {` is not seen.

### Fixed — `ckb unwired` and `findUnwiredModules`

- **A Go `init` runs whether or not anything references its file.** The walk from
  `main.go` followed references only, and a cobra subcommand registers itself from
  an `init` in a file nothing references, so every subcommand and everything under
  it read as unwired: the HTTP API, the A2A server, the compliance frameworks. When
  a Go file is reached, the files of its package that define an `init` are reached
  too. On CKB the headline went from 1,938 unwired symbols to 620.
- **A method called through an interface counts as reached.** `c.Run()` references
  `Check#Run`, not `piiInLogsCheck#Run`, so 704 of the 1,530 listed methods were the
  `ID`, `Name`, `Run`, `Severity` and `Article` of compliance checks that run on every
  audit. A method is reached through an interface when the interface or that method
  of it is reachable and so is the type: by the implementation relationship where the
  indexer wrote one (scip-typescript; scip-go for a fraction), and for Go by method
  set, as the language does it. `summary.viaInterface` says how many were left out
  this way (782 on CKB). A type nobody builds is still unwired, popular interface or
  not. The list of common interface methods (`String`, `Error`, `Close`, …) was
  never consulted: it asked for kind `method` and was handed `function`.
- **A Go reference never led into the file that defines it.** The guess from the
  symbol ID read scip-go's `` `example.com/m/pkg` `` as a TypeScript file name, so
  the import walk stopped at the first file on Go and only the callee walk went
  on. The definition index decides now. That exposed a scan of the whole index per
  callee in `FindCallees` and `findSymbolLocation`, three minutes on CKB, now half a
  second; `getCallGraph`, `findImplementations` and `getSymbol` go through the same
  lookup.
- **`--limit` cut modules, not items.** The default of 100 showed `internal/api` alone,
  156 items, and dropped the other 73 modules. The cut now takes one item per module
  in turn, so every module keeps its top items, each module says `… and N more`, and
  the result says `truncated` with the full counts in `summary`. `maxNodes` was read
  and never passed on; it is now, and its default is 100,000 instead of 10,000, since
  a cut walk makes everything not yet reached look unwired (CKB has 20,000 symbols
  and the fixed walk reaches 15,000). `local N` symbols no longer count.
- **A manifest in a hidden directory is not a module.** `.claude/worktrees/*/go.mod`
  made agent worktrees entrypoints of this repo and `.github/actions/*/package.json`
  a module. Module and language detection skip hidden directories and `third_party/`.
  On CKB `ckb index` still asks for `--lang`: `npm/package.json` is a real second
  manifest.

- **A Next.js app had no entrypoints, so nothing was unwired.** The walk started from
  the architecture's entrypoint list, which knows `main`, `cmd/` and manifest
  mains, and a Next.js app has none of those: 16,500 symbols, 105 entrypoints in
  `ckb entrypoints`, and `ckb unwired` said 0 reachable, 0 exported, 0 unwired, which
  the review read as a pass. The files a framework calls by convention (pages,
  layouts, route handlers, `proxy.ts`) seed the walk now. On a 375-file Next.js app
  that is 82 entrypoints, 9,555 reachable symbols and 21 unwired. With no
  entrypoint at all the CLI says so and what it looks for, and the review check is
  `skip`, not `pass`. `tests/support/*.ts` is test code: a `tests/` directory at the
  repo root counted only with a leading slash, as did `test/`, `testdata/` and
  `__tests__/`, so vitest helpers were the biggest unwired module. Nothing under a
  hidden directory (`.next/dev/types`) is a module.

### Fixed — `ckb dead-code` and `findDeadCode`

The analysis read everything about a symbol from its ID, which told it the wrong
things in every language. Checked on this repo and on a Next.js app.

- **The file was a guess from the symbol ID, and wrong.** A scip-go ID carries the
  package path (`` `github.com/SimplyLiz/CodeMCP/internal/query` ``), not a file, and a
  scip-typescript ID quotes the file in backticks. Every Go finding was filed under
  `github.com/SimplyLiz`, every TypeScript one under the quoted descriptor, so
  `IsTestFile` never matched a test, the scope filter matched nothing, and the
  review's dead-code check, which looks up findings by changed file, never found one
  in any repo: it was `pass` on a Next.js branch with 2,296 changed lines. File and
  line come from the definition now, the same source `ckb unwired` moved to, and
  each finding has its `symbolId` (it was always `""`).
- **Every kind was `unknown`.** The kind table mapped SCIP's descriptor numbers
  (1 package, 2 type, 3 term …) where the field holds `SymbolInformation.Kind`
  (a constant is 8, a method 26), so a Go constant was `attr` and nothing else was
  anything. The real enum is read now, and when the indexer wrote no kind
  (scip-typescript never does) the descriptor decides: `name().` is a function,
  `Type#name().` a method, `Type#` a type, `Type#name.` a field, an ALL_CAPS term a
  constant. `const f = () => …` is a function, as it already was for unwired.
- **Exported meant a capital letter, in every language.** TypeScript's `startSync`
  was never analyzed, `Home` was. `export`, `pub` and the Python underscore rule
  decide now; the rules are shared with `ckb unwired` and live in one place.
- **A use elsewhere in the same file counted as a self-reference.** "Only
  referenced by itself (recursive but never called)" at 95% went to every type used
  in its own file: `FindCyclesResponse`, `ComplianceConfig`, 361 of 440 findings on
  the Next.js app. A self-reference is a use inside the symbol's own body now; a use
  elsewhere in the file is a use.
- **What a framework calls is not dead.** A Next.js page's default export, a
  layout, a route handler's `GET`, `proxy`, `onRequestError` in `instrumentation.ts`,
  and what Next.js reads rather than calls (`metadata`, `viewport`, `dynamic`,
  `revalidate`, `runtime`, …) have no reference in the index and were the top of
  every list: 51 of the first 100 findings. The entrypoint detection from `ckb
  entrypoints` marks them.
- **Members and literals are not declarations.** scip-typescript writes a symbol
  for every object literal member (`{ ArrowDown: … }` is `ArrowDown0:`), every type
  literal member (`Attachment#typeLiteral0:name`), every parameter; a struct field
  is set in a literal or read through JSON, which the index does not record as a
  reference; an interface's method specification is a contract, not code. None of
  these are listed any more. Methods are matched against interfaces the way
  `ckb unwired` does it: `Severity`, `Article` and `ID` of 770 compliance checks
  were "dead" because `c.Severity()` references the interface's method.
- **Numbers.** CKB: 1,723 findings all filed under one wrong path, now 339 with
  files and lines (`AcquireAdaptive`, `GetFileStatus`, `NewFileCursor`: exported,
  zero references). The Next.js app: 117 findings of which 51 were pages and route
  handlers and the rest object literal keys, now 18, six of them the app's own
  `export const instant`, which a loader of its own reads. The review's dead-code
  check on that branch finds 2, both on unchanged lines.
- **`ckb review` printed "Usually changed with: " and nothing after it** for a file
  with exactly one missing co-change: the first message of a group was dropped on
  the way in. The golden file had enshrined it.
- **`ckb entrypoints` says when it cut.** "Total Entrypoints: 105" over a list of 30
  looked like a count of 30 with `page (1)` under it; it now says `showing 30, raise
  --limit for more`, and the groups come in a fixed order.

### Fixed — search and navigation

- **`ckb refs` no longer cuts the tests off.** References were sorted by path and
  cut at `--limit`, after the backend had already cut at twice that in its own
  order, so `tests/` came last and went first: a store with 334 references came
  back with 100 from `src/` and none of the 13 test files using it. The cut now
  takes every file's first reference before any file's second (51 files, all 13
  tests), and the CLI JSON says `truncated`. `--include-tests` never did anything,
  tests are always included; its help now says so. The help also says that
  `totalReferences` is what MCP calls `totalCount`.
- **A symbol ID with the wrong suffix says which one exists.** SCIP ends a type or
  type alias in `#`, a value in `.` and a function in `().`, and a hand-copied
  `ListKind.` was plainly "not found". `refs`, `impact` and the tools behind them
  now add "did you mean …ListKind#?".
- **`ckb entrypoints` knows Next.js.** Entrypoints came from names alone (`main` under
  `cmd/`, `Handle*`, `Run*`), so a Next.js app had none of its pages or route
  handlers: on a 16.x app with 21 pages and 49 route handlers it listed three
  functions that happened to be called `handle`. Files Next.js calls by convention
  are now entrypoints, as `detectionBasis: framework-convention`: `page`, `layout`,
  `template`, `loading`, `error`, `not-found`, `global-error` and `default` under
  `app/` (type `page`), `route` handlers and `pages/api/` (type `api`), the pages
  router, and `middleware`, `proxy` and `instrumentation` at the root, each by the
  export Next.js calls (default, `GET`/`POST`/..., `export { handler as GET }`).
  That app now has 105.
- **`ckb trace` finds paths through components.** A node's callees were cut at 30
  before anything else, and a React component calls `String#trim` and `Array#map`
  as often as anything, so the project's own callees fell off and the path with
  them. External declarations no longer take those places, and trace starts from up
  to 200 entrypoints instead of 50. With no path it said "Entrypoint set
  unavailable" even when there were entrypoints; it now says which.
- **`const f = () => …` is a function.** SCIP names it like any constant (`f.`), and
  only IDs ending in `().` counted, so its calls had no caller: on a TypeScript app
  with 589 such functions, `setList` had 0 callers though `removeEntry` calls it,
  and `callgraph removeEntry` listed 30 callees, read from everything between its
  name and the next `function` (`Math#random`, `fixLevels`, …), where it has 3. A constant counts as a
  function when the indexer gives no Kind and its EnclosingRange starts after the
  name, which is how scip-typescript marks an arrow or function initializer; a plain
  constant has none, and rust-analyzer's `const` range starts before the name.
  Callers, callees, `impact`, `trace`, blast radius (52 changed functions on that
  branch became 79, and `isCustomKey` with 18 callers is among the findings now) and
  `unwired` all see them. Not covered: a store built by a call, such as zustand's
  `create(...)`, is still a constant, and its actions' calls belong to its members.
- **The stored call graph has callers.** The `callgraph` table that `ckb index`
  fills for incremental updates, and that the index server and `ckb diff` read, had
  a caller on no edge at all: 0 of 48,326 on CKB itself, 0 of 11,441 on a
  TypeScript app. Its kind mapping used LSP's numbers, where 6 is a method and 12 a
  function; in SCIP they are Boolean and EnumMember. A symbol without a Kind came
  out `unknown` and was never a caller, and a Rust function (Kind 17) was never a
  callee, while enum members were. The ID check also took `run().(x)`, a
  parameter, for a call. The table and the index server's upload now use the SCIP
  backend's own rule, function-valued constants included, with each function's end
  taken from its EnclosingRange: 45,369 of 45,643 edges on CKB have a caller, 6,769
  of 10,248 on that app (the rest are mostly anonymous test callbacks). A call to a
  function-valued constant from
  another file is still missed there, since telling it from a plain constant needs
  its definition. The PageRank graph behind result ranking uses `IsCallable` too.
- **`switchProject` into a linked worktree stays there.** The next call with a
  relative `filePath` resolved it against the client's root first, found the same file
  in the main checkout and switched back to the main checkout's index, without saying so.
  The active repo now goes first.
- **Symbol kinds in search.** The FTS table read SCIP kinds against the wrong
  enum, so a Dart class was reported as a `variable`. Existing databases rebuild
  on first use.
- **`findReferences` on a class includes calls of its unnamed constructor**
  (`` `<constructor>` ``, `` `<init>` ``). They resolve to the constructor symbol
  and left the class's reference list short.
- **Search results have real locations.** The FTS path only set the file, so
  `searchSymbols` returned `startLine: 0` for everything.
- **Locals and files outside the repo are gone from results.** `ckb search y`
  found a local variable, and scip-go's generated test mains showed up with paths
  into the Go build cache.
- **Name matches survive a result cap.** `Serve` could lose `ServeHTTP` behind
  container-qualified aliases, `newserver` lost `NewServer` behind its members,
  and exact-case matches now beat case-folded ones of the same kind.
- **Rust is first class.** rust-analyzer `impl#[Foo]bar()` members are methods of
  `Foo`, and raw symbol IDs work in `impact` and `callgraph` for every indexer,
  not only those starting with `scip-`.
- **Handler detection** no longer matches `serverConfig`. Python module symbols
  (`__init__:`) and file-level pseudo-symbols are no longer callers.

### Fixed — what the tool descriptions promise

- `analyzeImpact`, `prepareChange` and `compareAPI` no longer send agents to
  `analyzeChange`, which is deprecated and missing from the `core` preset. They
  name `assessChange`.
- `assessChange` now sits right after `prepareChange` in the tool list, instead
  of behind `expandToolset`.
- `reviewPR` and `summarizePr` no longer state a check count; the number had
  gone stale.
- `searchSymbols` describes what it does, full-text search with a SCIP fallback,
  instead of "semantic code search". `scanPerformance` says that it detects
  hidden coupling.

### Fixed — upgrades and setup

- **Upgrading needs no fresh `.ckb`.** The derived symbol cache, FTS content and
  cached answers are versioned and rebuilt automatically.
- **`ckb init` keeps `.ckb/` and `index.scip` out of `git status`**, through
  `.git/info/exclude`.
- **The daemon handles nested projects** and uses the repo's language for
  incremental refresh.
- **`ckb federation add` no longer loops on `ckb init`.** A repository with `.ckb`
  but no database was told to run `ckb init` again, which doesn't create one. It
  now says `ckb index`.

### Changed

- **`findReferences` over MCP answers per file by default:** `files` with each
  file's lines, in place of one entry per reference. On six symbols of a Flutter
  app that is 8 KB instead of 46 KB. `detail: "full"` returns the previous list,
  and `ckb refs --compact` gives the CLI the same form; its JSON is unchanged.
- **`getCallGraph` has a response budget.** A widely used symbol returned 250
  nodes and about 46k tokens. It now returns the 20 nodes closest to the symbol,
  says in `truncation` how many were cut, and takes a `limit`. `ckb callgraph`
  allows 200.

### Added

- **MCP `instructions`** tell agents when to reach for CKB
  ([#246](https://github.com/SimplyLiz/ckb/pull/246)).
- **Change prediction is measured.** `scripts/eval-change` hides a commit's callers and
  scores `ckb changes` and `git grep` against the compiler, on Go, TypeScript and Rust.
  `docs/eval/change-prediction.md` has the method, before and after, and where CKB is
  still worse than grep.

### Output changes consumers will notice

- `findReferences` over MCP returns `files` by default; `references` needs
  `detail: "full"`. `blastRadius.riskLevel` in `analyzeImpact` equals
  `riskScore.level`.
- `changedSymbols`: new `note`, fewer entries, `file:` entries for lines no
  definition could be found for; deleted symbols list old-file lines.
- `riskFactors` may include `deleted_with_consumers`.
- Risk level `not_assessed` can appear in `prepareChange`, `analyzeImpact`,
  `planRefactor`, `assessChange`.
- `kind` values for symbols resolved through SCIP change where the old tables
  were wrong; `startColumn` is 1-based on search results.
- TypeScript call graphs change: parameters no longer count as functions.
- `findUnwiredModules` / `ckb unwired`: items have `filePath`, `lineNumber` and
  a real `module` for every language; TS, Python and Rust repos report more
  items, and `ckb review` can now raise unwired findings on Go and Rust.

### Changed — the automatic first index stays cheap

`ckb mcp` builds a first index in the background in a repo that has none. The
index lock is per repo, so every session opened in a different repo would have
started an indexer at the same time. Now only one first index runs per machine
(`~/.ckb/first-index/`); a session that finds it taken skips its build and the
next session in that repo tries again. Linked git worktrees are neither
initialized nor indexed automatically; `ckb index` there still works.

### Security — command injection in the review coupling check

`ckb review` looked up when co-changed files were last modified by writing
their paths into a `sh -c` script. Go's `%q` quoting is not shell quoting, so a
path containing `$(…)`, backticks or a `"` ran as a command. Those paths come
from git history, which on a PR branch is whatever the PR author committed —
so running `ckb review` (or `reviewPR`) on an untrusted PR, e.g. in CI, could
execute code on the runner. The lookup now runs one `git log` with the paths as
argv and literal pathspecs; no shell is involved. Tab characters and pathspec
magic like `:(glob)` in file names no longer produce wrong dates either.

### Added — change intelligence and an activity ledger, no UI

Decided 2026-09-16 (see `docs/plans/change-intelligence-and-activity-ledger.md`):
CKB 1.x gets no web UI. Text first, evidence first. Three things instead:

- **`assessChange`** (MCP) and **`ckb changes`** (CLI): the post-change
  counterpart to `prepareChange`. Works on the uncommitted working tree by
  default (`--staged`, `--base <ref>` for the index or a range) and answers
  "what did I actually change and what should I verify before finishing":
  changed symbols, structured risk factors with evidence, blast radius,
  affected tests, downstream consumers with no reaching test, *possible*
  contract changes (a labeled heuristic: exported symbol lines changed, never
  called a breaking change), related ADRs, likely reviewers, and a confidence
  tier that names its reasons (no SCIP index, commits behind). This is
  `analyzeChange` grown up, not a third diff analysis: `analyzeChange` stays
  registered as a deprecated alias for two minor versions, and `ckb impact
  diff` stays as an alias of `ckb changes`. Tool count is now 111.
- **Activity ledger**: every MCP tool call is recorded in a `tool_calls`
  table in `.ckb/ckb.db` (schema v12) — session (from `CLAUDE_CODE_SESSION_ID`
  when run under Claude Code), consumer (from the MCP `clientInfo` handshake),
  tool, hashed and truncated params, primary target, duration, response
  bytes, truncation, error, and per-tool structured facts for `prepareChange`,
  `assessChange`, `analyzeImpact`, `findReferences`, `searchSymbols`. Written
  off the request path by a buffered single-writer goroutine; never adds
  latency, never surfaces an error to the client. Configurable via
  `activity.{enabled,retentionDays,storeParams}` (defaults true / 30 / true),
  pruned on server start. Read it with **`ckb activity`** (`--last`,
  `--session`, `--consumer`, `--tool`, `--since`, `--summary`, `--all`,
  `--json`). The feed reports calls and what they returned; it never narrates
  why the agent called something.
- **`ckb bench session <id>` / `ckb bench compare <a> <b>`**: joins a Claude
  Code transcript (tokens by class, turns, tool calls, files read) with the
  ledger rows of the same session into one record, and compares two. Produces
  neutral deltas; nothing is labeled "tokens saved".
- `backends.treesitter.exclude` in `.ckb/config.json`: a list of globs the
  tree-sitter symbol fallback (used by `searchSymbols` when no SCIP index is
  loaded) must not index. Globs are matched against the repo-relative path with
  `/` separators. Two forms are supported: `dir/**` (the directory and
  everything below it, compared on whole path segments, so `build/**` does not
  match `builder/`) and anything else is a Go `path.Match` pattern against the
  full path (`*` does not cross `/`, so use `src/*.min.js`, not `*.min.js`, for
  nested files). No leading `**`, no negation. Invalid patterns are rejected at
  config load.
- **Per-result index freshness in SCIP-backed responses.** `findReferences`,
  `analyzeImpact`, `prepareChange`, `getCallGraph` and `findDeadCode` now say
  whether the loaded SCIP index is stale *for this query*, so you no longer have
  to call `getStatus` first. `meta.indexFreshness`:
  ```json
  "indexFreshness": {
    "indexedCommit": "75d7ab3",
    "commitsBehind": 3,
    "changedSinceIndex": ["src/lib/cv/store.ts"],
    "affectsResult": true,
    "hint": "1 result file(s) changed since indexing — call reindex for exact results"
  }
  ```
  `changedSinceIndex` is every file changed (committed, uncommitted or
  untracked) since the indexed commit, capped at 50 (`changedSinceIndexTruncated`
  when cut). `affectsResult` is true only when one of those files appears in the
  result; then `meta.confidence.score` is multiplied by 0.7. An old index over
  code that has not changed keeps its confidence. If the index records no
  commit, `indexedCommit` is empty and the hint says freshness cannot be
  determined. The block is omitted when SCIP did not answer.
  When the indexer records no commit in the SCIP metadata (scip-typescript
  does not), the commit `ckb index` wrote to `.ckb/index-meta.json` is used,
  so `indexedCommit` is not empty for TypeScript repos.
- **MCP `reindex` builds and loads the index.** It used to answer with
  guidance ("run `ckb index` in your terminal") and `skipped` whenever the
  file on disk looked fresh — even when the running server had nothing loaded.
  It now runs the `ckb index` code path in-process (incremental where the
  language supports it, else full; same plan builder, lock and metadata as the
  CLI) with a timeout (`timeoutSeconds`, default 300), then reloads the index
  into the running engine. Returns `status` `reindexed` | `reloaded` (file was
  fresh, server had not loaded it) | `skipped` (fresh and loaded; pass
  `scope: "full"` to rebuild anyway) | `busy` (another run in progress for
  this repo, or the index lock is held) | `failed` | `action_required` (no
  indexer, or repo above the automatic size threshold), plus `mode`,
  `durationMs`, `symbols`, `documents`, `commitsBehindBefore`. The tool is
  now in the `review` and `refactor` presets as well as `ops`, since that is
  where the impact tools that need a current index live. `getStatus.index`
  gains `loaded` and reports `fresh: false` when the file on disk is not what
  the server serves.

### Fixed

- Config keys absent from an older `.ckb/config.json` now keep their default
  instead of the Go zero value. Every default-true flag (`backends.scip.enabled`,
  `review.blockBreakingChanges`, the new `activity.enabled`, …) silently flipped
  to false for configs written before the key existed.
- `SummarizePR`'s comment claimed it compared against the working tree; it runs
  `git diff base HEAD`. Comment fixed. `ckb review` still does not see unstaged
  changes; `ckb changes` does.
- `ckb impact diff` promised "who needs to review this" and never delivered.
  It does now.
- Working-tree reviewer suggestions no longer list git's "not committed yet"
  pseudo-author.
- A running `ckb mcp` / `ckb serve` now picks up an index that `ckb index`
  built or rebuilt after it started ([#134](https://github.com/SimplyLiz/ckb/issues/134)).
  The engine compares the index file's size and mtime with what it loaded
  (one `os.Stat` before each tool call and in `getStatus`) and reloads on a
  change, clearing caches and refreshing FTS; concurrent requests share one
  reload. Before, a plain `ckb mcp` served whatever it loaded at startup — for
  a fresh project no index at all — until the client reconnected, while
  `getStatus` reported the index as fresh.
- `findReferences` reported `totalCount` capped at twice the requested limit
  (10 for `limit: 5` on a 17-reference symbol), so agents sized blast radius
  from a number that was really the fetch cap. The SCIP adapter now counts
  references without the cap and the engine reports the real total;
  `searchSymbols` sets `totalIsLowerBound` when a backend stopped at its cap.
- `getStatus` reported a configured but idle LSP server as "No servers
  running" with a remediation to edit a config that was already right, and
  `overallHealth` went `degraded`. LSP servers start on first use; status now
  lists configured servers per language ("typescript: configured, starts on
  demand", "running", or "gopls not found on PATH") and only reports the
  backend unavailable when no configured binary is on PATH.
- `searchSymbols` without a SCIP index no longer returns symbols from
  git-ignored directories such as `dist/` or `build/` (minified bundles ranked
  ahead of real `src/` symbols in Next.js repos). The tree-sitter fallback now
  lists files with `git ls-files -co --exclude-standard` when the repo is a git
  work tree, so nested `.gitignore` files are honoured and untracked files that
  are not ignored are still searched. Outside git (or if git fails) it falls
  back to the old directory walk, which does not read `.gitignore`; use
  `backends.treesitter.exclude` there. Dot-directories, `node_modules`,
  `vendor` and `__pycache__` are still skipped in both modes.
- `ckb index` (and the MCP `reindex` tool) reported "Index is up to date.
  Nothing to do." when the incremental tracking tables were intact but
  `index.scip` itself had been deleted — leaving metadata that claimed a
  current index while no index existed. A missing index file now forces a
  full rebuild.

### Added — Claude Code plugin

CKB installs into Claude Code as a plugin: `claude plugin marketplace add SimplyLiz/ckb`
then `claude plugin install ckb@lisa`. One install covers every repo and brings the
MCP server (`npx -y @tastehub/ckb mcp`) plus `/ckb:review` and `/ckb:audit`; the
plugin version follows CKB releases, so the skills update with them. `ckb setup`
is unchanged and remains the way in when `ckb` is installed; using both starts
the server twice.

The two skill texts now live in `claude-plugin/skills/` and are embedded into
`ckb setup`, so the plugin and the slash commands cannot drift. Both gained an
npx fallback: they shell out to `ckb`, which users who set up via
`npx @tastehub/ckb setup` never had on PATH, so the commands failed silently.

### Changed

- Go toolchain 1.26.6 → 1.26.10 (`go.mod`; CI and release builds read it via
  `go-version-file`). Picks up the stdlib fixes in `net/http`, `net/textproto`,
  `crypto/tls` and `crypto/x509` that govulncheck flagged as reachable. The
  glibc floor (2.34, [#243](https://github.com/SimplyLiz/ckb/issues/243)) is
  unaffected: it comes from the pinned `ubuntu-22.04` runners, and
  `scripts/check-glibc-floor.go` still verifies every linux binary.
- Dependencies with open HIGH advisories bumped: `github.com/klauspost/compress`
  1.18.2 → 1.18.7, `golang.org/x/net` 0.58.0 → 0.60.0 (both DoS), and
  `undici` 6.27.0 → 6.29.1 in the PR-analysis GitHub Action (bundle rebuilt).

### Changed — unresolved dependent names/symbolIds now serialize as `""`, not `"unknown"`

`directDependents[].name` / `.symbolId` (and other fields that report an
enclosing/caller symbol CKB couldn't resolve — e.g. a package-level
reference outside any function, or a module/namespace-level SCIP
"enclosing symbol" with no short name) previously fell back to the string
`"unknown"` when resolution failed, while `symbolId` was left `""` — an
inconsistent pair that also collided with the real symbol kind value
`"unknown"` (`KindUnknown`) elsewhere in responses. Both fields now use the
same empty-string sentinel (`""`) for "not resolved."

**If your integration checks for the literal string `"unknown"` to detect
an unresolved name, update it to check for `""` instead** — this is a
compatibility-affecting behavior change, even though the JSON key itself
is still always present (schemaVersion 1 fields are not `omitempty`).

## [9.3.1] - 2026-09-13

### Fixed — linux binaries would not start on glibc < 2.39 ([#243](https://github.com/SimplyLiz/ckb/issues/243))

The 9.3.0 linux binaries declared a `GLIBC_2.39` requirement and so refused to
start on Ubuntu 22.04 LTS, Debian 12, RHEL 9 / Rocky 9 and Amazon Linux 2023 —
`version 'GLIBC_2.39' not found`. Nothing in the binary needed glibc 2.39.

9.3.0 moved the release to a native cgo matrix so the Cartographer fast tier
could be linked in. A cgo build's glibc requirement is set by the runner image,
not by the source: the linker stamps each undefined libc symbol with the version
the build host defines it at. `ubuntu-latest` had migrated to 24.04 (glibc 2.39),
and two weak symbols Rust's std references for its `posix_spawn` fast path —
`pidfd_spawnp`, `pidfd_getpid` — resolved against 2.39 and put a `GLIBC_2.39`
entry in `.gnu.version_r`, which the loader enforces before the process starts.
Both `linux-x64` and `linux-arm64` were affected.

The linux targets now build on `ubuntu-22.04` / `ubuntu-22.04-arm`, where those
symbols do not exist to link against; an unresolved weak symbol emits no version
requirement, so the floor drops to the next real one, **GLIBC_2.34**. That
covers RHEL 9 / Rocky 9 / Amazon Linux 2023 (2.34), Ubuntu 22.04 (2.35) and
Debian 12 (2.36). The Cartographer fast tier is unaffected — it stays linked.

`scripts/check-glibc-floor.go` now enforces the floor in CI and in the release
build, so a binary that cannot start on a supported distro fails the build
instead of shipping.

**Affected users:** upgrade with `npm install -g @tastehub/ckb@9.3.1` (or
`brew upgrade ckb`). No 9.2.0 downgrade needed any more.

## [9.3.0] - 2026-07-13

### Added — `ckb doctor` reports the Cartographer fast tier

`doctor` now includes a `cartographer` check that reports whether the vendored
structural engine (dependents, blast-radius, rollup — the SCIP-free fast tier)
is linked into this binary. CGO-free npm/Homebrew builds warn with a `make
build` fix hint; source builds report `linked (vX.Y.Z) — fast structural tier
active`. Previously a build silently ran without the fast tier with no way to
confirm it.

### Fixed — FFI crash (process abort) on very large repos

The vendored engine aborted the whole process — including the CKB host, via the
FFI — on a full Linux-kernel checkout (~64k C/H files): two unbounded recursions
overflowed the stack. Recursive tree-sitter walkers on deeply nested (macro-
generated) C now run on a 256 MB-stack pool, recursive `tarjan_scc` cycle
detection is replaced with an iterative version, and the walkers carry a hard
depth ceiling (cap 50,000; the kernel's measured real peak is 3,348) as a belt
against adversarial/generated input. Verified: `Health("/tmp/linux")` via FFI
completes without crashing (regression test in `internal/cartographer`). This
directly de-risks the very-large-C++-repo use case.

### Changed — re-vendor CodeCartographer 4.0.0

Re-vendored the graph engine at CodeCartographer 4.0.0 (FFI/C-ABI unchanged, so
no bridge changes). Pulls upstream fixes CKB consumes: authoritative Rust
qualified-path import resolution (crate-internal edges were dropped as fuzzy,
making structural metrics measure docs instead of code), health/ranked-skeleton
now exclude the documentation subgraph, char-boundary-safe string truncation
(fixed a UTF-8 panic on non-ASCII indexed content), call-graph-resolved
`reach` caller/callee edges, and git churn/coupling noise filtering. FFI-tagged
tests green; full `-tags cartographer` build clean.

### Performance — scaling to very large C++ trees

Profiling on a 40k-file synthetic tree (and Godot) exposed three serial
bottlenecks in the vendored graph engine; all now parallelize or drop in
complexity:

- **Parallel edge resolution** — `rebuild_graph` resolves every file's imports
  via rayon against the immutable index (was single-threaded).
- **Parallel, index-based betweenness centrality** — betweenness was 98% of a
  graph rebuild (~3.14s of 3.2s on Godot). The Brandes pass per sampled source
  now runs over dense `Vec` buffers keyed by node index (was per-source
  `HashMap<&str,_>` reallocation) and the independent sources run across cores.
  Per-source contributions are summed in fixed source order, so results are
  **bit-identical regardless of core count** — deterministic across machines,
  not just across runs. On Godot: betweenness 3,140ms → 57ms, full rebuild
  3,197ms → 76ms (~42×), same 3182 bridges / 68 god-modules / 0 cycles.
- **Directory-level rollup** — the vendored engine can now fold the file-level
  graph to directory granularity at a given depth, aggregating cross-directory
  dependencies and running the full structural analysis on the folded graph, so
  bridges/cycles/god-modules/health describe subsystems rather than individual
  files. On Godot this turns 4336 files / 3182 bridges into 160 subsystems /
  139 bridges with actionable per-subsystem hints — the comprehensible view on
  huge trees. Available as `rebuild_graph_rolled_up(depth)` (CLI:
  `health --rollup <depth>`).
- **Deterministic import resolution** — resolver candidate lists were in
  HashMap order, so an ambiguous import could resolve to different targets
  run-to-run (invisible at file level, but it drifted directory-level edge
  counts). Candidate lists are now sorted and edge dedup keeps the strongest
  resolution (exact < suffix < fuzzy), making the graph fully deterministic.
- **Betweenness cache (incremental rebuild)** — centrality depends only on graph
  topology, so it's cached keyed by a fingerprint of the structural node+edge
  set. An edit that leaves imports unchanged (a body/comment/whitespace save —
  the common watch-mode case) reuses it and skips the whole Brandes pass. The
  cache survives graph invalidation and self-invalidates only when an import
  actually changes. The win grows with repo size: at 100× scale, where sampled
  betweenness re-inflates to seconds, a non-import edit stays near the ~40ms
  floor instead of paying the full recompute.
- **O(1) package-suffix fallback** — the qualified/Go package resolver's
  directory-suffix probe scanned *every* directory per unresolved import
  (O(dirs), a real cliff for repos with many external qualified imports). It now
  probes only directories sharing the import's last segment via a suffix index.
- Cartographer's CLI parse loops were also switched from serial to parallel
  (`iter`→`par_iter`), though CKB uses the parallel FFI path.

Measured: a cold 40k-file analysis went 56s → 12.7s (~4.4×); Godot (4.3k files)
16s → ~2s (~8×), with identical results.

### Changed

- **Re-vendored the Rust crate to upstream `CodeCartographer` 1.3.3** (from
  the interim `nyx-navigator` 1.1.0 fork), and dropped the `nyx`/`navigator`
  naming — the CKB side is `cartographer` again, tracking upstream's FFI.
  - Vendor path `third_party/nyx-navigator/` → `third_party/cartographer/`,
    synced straight from upstream `mapper-core/codecartographer/`.
  - Build tag `navigator` → `cartographer`; `make build` / `make test`
    resolve `build-cartographer` / `test-cartographer`. `make build-fast`
    (no Rust toolchain) unchanged.
  - Static lib `libnavigator.a` → `libcode_cartographer.a`; FFI prefix
    `navigator_*` → `codecartographer_*`; header `navigator.h` →
    `codecartographer.h`; sync script `scripts/sync-nyx-navigator.sh` →
    `scripts/sync-cartographer.sh`.
  - Go package `internal/navigator` → `internal/cartographer`; error type
    `NavigatorError` → `CartographerError`; status backend id
    `navigator` → `cartographer`.

### Gained from upstream 1.3.x (library-level; bridge surface unchanged)

The re-vendor pulls upstream's 1.2–1.3 work into the linked library:

- **Six new languages** — Java, C#, Ruby, Kotlin, Swift, PHP now have
  tree-sitter skeleton extraction, file-local call graphs, and class
  diagrams, on par with the original six. `skeleton_map`,
  `ranked_skeleton`, `reach_symbol`, and search cover all twelve.
- **Symbol-aware search corpus** — `query_context` / `answer_question`
  rank over a BM25 corpus of parsed symbols (name + qualified name +
  signature + doc-comment), not raw file bytes.
- **Scope-qualified `reach_symbol`** — renders `Object.get_class`,
  surfaces doc-comments, and filters call sites that qualify a different
  class.
- **Orientation-first repo map** — `ranked_skeleton` ranks by role +
  fan-out instead of pure import-PageRank, so entry points and domain
  core lead.
- **Perf** — O(N) import resolution and sampled betweenness fix the
  O(N²) hang on large C/C++ trees (Godot cold start ~2 min → ~12 s).

### Fixed — go.mod-aware import resolution (kills fabricated dependency edges)

The vendored resolver matched Go imports by bare filename stem, so
`internal/errors` resolved to an unrelated `internal/a2a/errors.go` and a
stdlib `sync` fabricated an edge to `internal/federation/sync.go`. Resolution
is now Go-module-aware: an import is internal only if it lives under the
`go.mod` module path, in which case it resolves to its package *directory*;
everything else (stdlib, third-party) produces no internal edge. Measured on
CKB itself: **~23% of dependency edges were fabricated (996 → 769)**, all **22
reported dependency cycles were artifacts (→ 0**, as Go forbids package import
cycles), bridges 244 → 187, and the architectural health score went 38 → 71.
This directly cleans up every import-graph-derived surface — `getArchitecture`,
`getBlastRadius`, `renderArchitecture`, `getModuleContext`, `analyzeImpact`,
`prepareChange`, and the `review` layers/arch-health checks.

As a safety net for the remaining languages (which still resolve heuristically),
every edge now carries a `resolution` confidence tag (`exact` | `suffix` |
`fuzzy`), exposed on `cartographer.GraphEdge`. All structural analysis is now
computed on non-`fuzzy` edges only — cycles, the "will create a cycle"
prediction, bridges, god modules, layer violations, and the blast radius — so
none of the import-graph-derived surfaces (`getArchitecture`, `getBlastRadius`,
`analyzeImpact`, `prepareChange`, the `review` layers/arch-health checks) can
rest on a low-confidence stem guess. The returned graph still carries every
edge, tagged, so consumers can decide. On the CKB side, `review`'s PR-split
clustering skips `fuzzy` edges so a split recommendation isn't driven by a
fabricated dependency.

The same namespace-exact idea was generalized beyond Go: any extensionless
import that carries directory structure (Python `from app.services import auth`,
monorepo/aliased `components/Button`) now resolves against its **full path** —
matching module files and package directories (preferring `__init__.py` /
`index.*` entry points) — instead of collapsing to the last stem. A qualified
import that matches nothing local is treated as external (no edge) rather than
fuzzy-guessed. The Python extractor was fixed to preserve import keywords so
dotted paths (`a.b.c`) normalize correctly. Net: `from app.services import auth`
and `from app.models import auth` resolve to their own packages instead of
colliding on the shared `auth` stem, and third-party imports stop fabricating
internal edges.

JS/TS relative imports (`./bar`, `../models/user`) now resolve against the
**source file's directory** — path-precise, `..` walks up, a bare package dir
uses its `index.*`. A relative path that isn't in the scan is a dangling ref
(no edge), never a fuzzy same-name match. `export … from './x'` re-exports are
now captured as dependencies. Bare package imports (`react`) stay external.

### Available for follow-up wiring (not yet exposed)

The vendored library exports FFI symbols not yet wired through the Go
bridge or MCP layer:

- `codecartographer_poll_changes(path, since_ms)` — incremental change
  polling; fits CKB's `mcp --watch` and the daemon file-watcher.
- `codecartographer_doc_index`, `codecartographer_doc_context`,
  `codecartographer_query_docs` — full doc-retrieval pipeline.

## [9.2.0] - 2026-04-25

### Added

- **`analyzeOutgoingImpact` — forward call graph** (MCP + CLI) — mirror
  of `analyzeImpact` answering *"what does this symbol call?"* instead of
  *"who calls it?"*. New `Engine.AnalyzeOutgoingImpact` drives off LIP
  v2.3.5's `query_outgoing_impact` RPC, folds the result through the same
  `ImpactItem` pipeline as the incoming side (with `direct-callee` /
  `transitive-callee` kinds), and surfaces semantically coupled callees
  alongside the static graph. Degrades cleanly when LIP isn't running:
  the response is empty with a provenance warning, never an error.
  Surfaces include `ckb impact outgoing <symbolId>` (with `--min-score`
  for the semantic threshold), the `analyzeOutgoingImpact` MCP tool, and
  a new `ProvenanceCLI.Warnings` field so LIP-degradation messages reach
  JSON consumers.
- **`symbolExists` MCP tool** — exact-match boolean oracle that returns
  `{exists, kind, location?}` for a fully-qualified symbol ID. Built for
  LLMs to ground references *before* they cite them in code, without
  spending tokens on a 20-result `searchSymbols` payload. Cheaper than
  `getSymbol` for the "does this thing actually exist" check.
- **LIP enrichment folds into `analyzeImpact`** — tier-1 tree-sitter
  callers that LIP discovers (when `scip-go` emits no `Call` roles, e.g.
  Go method dispatch) are now folded into the same `directImpact` /
  `transitiveImpact` lists as SCIP's own results, deduplicated by
  `(file, name)`. Driven by a new `BlastRadiusEnricher` interface so the
  fold path is the single source of truth for both incoming and outgoing
  impact analysis. Items LIP marks `edges_source=empty` are skipped (LIP
  signalling no static evidence); `tier1`, `scip_with_tier1_edges`, and
  `scip_only` all fold the same way. Risk score now picks up
  semantic-coupling signals via the same enricher pipeline.
- **`register_project_root` on LIP handshake** — Engine startup now
  registers the repo root with the daemon so LIP canonicalises file URIs
  against a known anchor, matching the v2.3.1 contract. Eliminates the
  URI-shape drift that previously caused tier-1 callers to dedup
  incorrectly against SCIP results.

### Changed

- **`analyzeImpact` risk score now weighted by bridge centrality** —
  `calculateAggregatedRisk` multiplies the weighted-mean score by
  `1 + max(BridgeScore)/1000` (capped at 2.0) over the changed files, so a
  change landing on a critical architectural path (high betweenness) is
  reported as riskier than the same-shape change in a leaf module. Implements
  the behaviour that `CARTOGRAPHER_STRATEGY.md` had already documented but
  the code was not actually doing. Bridge lookups match by both `Path` and
  `ModuleID`; if no changed file matches the graph, the multiplier is 1.0
  and no informational factor is appended. Only runs when the binary was
  built with `-tags cartographer` (graph is a no-op otherwise). A new
  `bridge_centrality` informational factor surfaces in `RiskScore.Factors`
  when the multiplier fires; its `Weight` is 0 because it applies
  multiplicatively, not as a weighted-mean input.

### Cartographer

- **Vendored Cartographer fully synced to upstream 3.0.0** — the
  vendored tree under `third_party/cartographer/mapper-core/cartographer/`
  was 391 lines behind on `diagram.rs` alone, and 10 `.rs` files plus
  `Cargo.toml` had drifted. Full sync brings in doc-node graph support
  (`cartographer_doc_index`, `cartographer_doc_context`, `cartographer_query_docs`
  FFI entry points — Go bindings can be added as a follow-up),
  LIP-style `Range` / `at_range` on `GraphEdge`, PascalCase bare-identifier
  resolution for doc backtick refs, and the overlays feature on diagrams.
  New `scripts/sync-cartographer.sh` is now the supported path for future
  syncs — rsync-based, explicit path list, emits next-step commands. No
  local patches needed against upstream.
- **Diagram overlays in `renderArchitecture` / `ckb diagram`** — the
  vendored `diagram.rs` was synced from upstream Cartographer, so the
  Mermaid/DOT output now decorates the base import graph with
  architectural signals: cycle members get a thick red border (pivots
  dashed), cycle-internal edges a heavy red arrow, layer violations pick
  up per-type dashed/dotted edge styling, and hot nodes
  (`hotspot_score ≥ 70`) get an orange border plus DOT size scaling.
  Mermaid is border-only for hot nodes (no sizing primitive). Cycle red
  takes precedence over hot orange on the same node — architectural
  signal wins over performance signal.
- **`renderArchitecture` MCP tool** — returns the project's module-level
  import graph as Mermaid or Graphviz (DOT), ready to paste into IDEs
  that render Mermaid inline (Cursor, Claude Desktop, VS Code markdown
  preview, GitHub). With `focus` set, returns an undirected BFS
  neighborhood around the anchor module to `depth` (default 2); without,
  returns the top-N most-connected nodes (default cap 40). Response
  includes `truncated: true` when the node cap kicked in. Backed by the
  new `cartographer_render_architecture` FFI export; CLI and MCP outputs
  are produced by the same shared renderer.
- Go binding `cartographer.RenderArchitecture()` in `internal/cartographer/bridge.go` (+ no-op stub for the no-tag build).

### Fixed

- **Vendored Cartographer `rebuild_graph` deadlock** — upstream
  `ApiState::rebuild_graph` held the `mapped_files` Mutex across its
  loop and then called `resolve_import_target`, which re-acquired the
  same non-reentrant `std::sync::Mutex`. Any project with a resolvable
  import deadlocked — the `cartographer diagram` / `cartographer health`
  CLIs hung, and the Go bridge's `cartographer.MapProject` would block
  any time CKB fed it a repo with imports. Fixed in the vendored tree
  (and contributed back upstream) by splitting the resolver: a public
  method that locks, and a private helper that takes the already-held
  map; `rebuild_graph` now calls the helper. Discovered during
  end-to-end smoke testing against CKB itself (1093 files). Regression
  test added upstream.
- **`localize-tree-sitter-symbols.sh` dropped grammar C parsers** — the
  script extracted archive members via `ar x`, which silently clobbers
  files when multiple members share a name. Cargo emits a `parser.o`
  and `scanner.o` per grammar crate (tree-sitter-c, -cpp, -rust, -go,
  etc.), so `ar x` left only the *last* grammar's C parser on disk,
  producing a localized archive missing `_tree_sitter_c` / `_tree_sitter_cpp`.
  The script now feeds the archive directly to `ld -r` with
  `-force_load` (Mach-O) / `--whole-archive` (ELF), which pulls every
  member in without touching the filesystem. The `rust_tree_sitter` C
  ABI refs to `_tree_sitter_c` and `_tree_sitter_cpp` now resolve
  inside the combined object as expected.
- **Tree-sitter symbol collisions at link time** — `libcartographer.a`
  previously exported its bundled tree-sitter runtime and grammar
  symbols, which collided with `go-tree-sitter` when building CKB with
  `-tags cartographer` (`ld: 246 duplicate symbols`). `make build-cartographer`
  now post-processes the archive via
  `scripts/localize-tree-sitter-symbols.sh` (vendored under
  `third_party/cartographer/mapper-core/cartographer/scripts/`), which
  partial-links archive members into one combined object and localizes
  `ts_*` / `tree_sitter_*`. `cartographer_*` FFI exports stay global.
  Beyond the duplicate-symbol error, this also rules out a silent
  memory-corruption class of bug where Cartographer's Rust code could
  have bound to the consumer's tree-sitter copy at global resolution
  time if the two versions' struct layouts ever drifted.

## [9.1.0] - 2026-04-16

### Added

- **LIP v2.1 utilisation** — three high-ROI LIP RPCs wired into the query
  engine, gated on the handshake's `supported_messages`:
  - `stream_context` (v2.1) → `explainFile` attaches up to 10
    semantically-related symbols (2048-token budget) in `facts.related`.
    New streaming transport reads N `symbol_info` frames + `end_stream`.
  - `query_expansion` (v1.6) → `searchSymbols` expands ≤ 2-token queries
    with up to 5 related terms before FTS5, recovering vocabulary-mismatch
    recall without touching precision on compound queries.
  - `explain_match` (v2.0) → semantic search hits carry up to two ranked
    evidence chunks with line ranges, text, and per-chunk scores (top-5
    hits, bounded round-trip cost).
- **`lip.Handshake` runs on engine startup** and the daemon's
  `supported_messages` list is stashed for feature gating
  (`Engine.lipSupports`). Daemon version and supported-count logged.
- **LIP index status probing** — `probeHandshake` now follows up with
  `IndexStatus` and caches the result. New `Engine.LIPStatus()` returns
  `{Reachable, IndexedFiles}` so consumers can distinguish "daemon down"
  from "daemon up, nothing indexed."
- **`ckb review` warns when LIP index is empty** — stderr advisory with
  `lip index <repo>` command when daemon is reachable but has no content.
  Suppressed in `--ci` to keep CI logs clean.
- `NoAutoFetch` option on `SummarizePROptions` and `SummarizeDiffOptions`
  for parity with `ReviewPROptions`.
- Troubleshooting section in `docs/plans/review-cicd.md` covering shallow
  CI clones, auth-failure remediation, air-gapped pipelines, and depth-0
  checkout alternatives.
- Auth-error detection on auto-fetch with clear remediation guidance.
- `ckb review --no-auto-fetch` flag for air-gapped pipelines.
- Test coverage for `GitAdapter.EnsureRef` — happy path, missing-ref
  auto-fetch, unreachable origin, and empty-input guard.
### Changed

- **LIP health: push-driven, not polled** — Engine opens a long-lived
  connection to the daemon at startup (`internal/lip/subscribe.go`) with
  `index_changed` frames and per-ping `index_status` snapshots instead of
  60 s TTL polling. Worst-case staleness drops from 60 s to ~3 s.
- **`lipFileURI` path normalisation** — handles absolute paths and
  already-prefixed `file://` URIs without producing malformed results.

### Fixed

- **Bug-pattern false positive on `sync.Mutex.Lock()`** — removed `"Lock"`
  from `LikelyReturnsError` heuristic patterns; `sync.Mutex.Lock` returns
  nothing and dominated real-world matches with false positives.
- **`err` shadowing in `subscribe.go`** — four shadow sites eliminated by
  reusing outer `err` or renaming to `pingErr`/`readErr` where scope
  isolation requires it.

- **LIP rerank: coherence gate + position-weighted seeding** (#209) — the
  Fast-tier semantic rerank (`internal/query/lip_ranker.go`) used to average the
  top-5 seed embeddings with uniform weight and always apply the result. When
  the top-5 pointed in different directions the centroid collapsed toward zero
  and amplified noise; when the top seed was strong the blend still diluted it.
  Seeds are now L2-normalised and position-weighted (`1/(rank+1)`), the
  resulting centroid norm is read as a coherence score in `[0, 1]`, and the
  rerank falls back to pure lexical order when coherence is below
  `MinCoherence` (default `0.35`). Blend weights, seed count, and threshold are
  surfaced as `RerankConfig` so future tuning does not need to touch call
  sites. Injected `embedBatchFn` makes the ranker unit-testable without a
  running daemon.
- **LIP rerank: gate on `!MixedModels`** (#208) — when the LIP index contains
  vectors from more than one embedding model (e.g. partial re-index during a
  model upgrade), cosine similarity across those vectors is mathematically
  meaningless. `RerankWithLIP` and `SemanticSearchWithLIP` now consult a
  cached `Engine.lipSemanticAvailable()` check (60 s TTL, single `IndexStatus`
  RPC) and fall back to lexical ranking when the daemon is down or reports
  `mixed_models`. A new `lip_mixed_models` degradation warning (70% capability)
  surfaces in response metadata so users learn *why* results look weaker
  instead of silently ranking on garbage.

## [9.0.1] - 2026-04-15

### Fixed

- **`ckb review` in shallow CI clones** — Azure Pipelines, GitHub Actions, and
  GitLab default to shallow single-branch checkouts, so `ckb review --base=main`
  failed with `exit 128` because the base ref was not present locally. The
  review path (and `summarizePr` / `summarizeDiff`) now auto-fetch the base ref
  from `origin` when it is missing, falling through to `origin/<branch>`. No
  pipeline changes required. No cost for full clones.
- **Opaque git errors** — `GitAdapter.executeGitCommand` previously wrapped git
  failures as "Git command failed: exit status 128" with git's actual stderr
  hidden in a details map. The stderr (e.g. `fatal: bad revision`) is now part
  of the error message, making CI failures diagnosable without reproduction.

## [9.0.0] - 2026-04-13

### Added

#### LIP v2.0 semantic integration

CKB now speaks the LIP v2.0 wire protocol correctly and integrates semantic
embeddings across the tool suite. The existing `internal/lip` client had the
wrong JSON discriminator (`"action"` instead of `"type"`) and wrong action
strings, meaning all LIP calls were silently failing. The client has been
rewritten with the correct Serde-tagged format and 25 new functions covering
LIP v1.5–v2.0.

**Wire protocol fix** — all requests now use `"type"` as the discriminator with
`snake_case` variant names matching Rust's
`#[serde(tag = "type", rename_all = "snake_case")]`. Field names corrected
throughout (e.g. `"symbol_uri"` not `"uri"` for annotation queries).

**New LIP client functions** (`internal/lip/client.go`):

| Function | LIP version | Purpose |
|---|---|---|
| `Handshake` | v1.5 | Protocol handshake, returns daemon + protocol versions |
| `BatchNearestByText` | v1.5 | Parallel nearest-neighbour for multiple queries |
| `NearestBySymbol` | v1.5 | Nearest neighbours by `lip://` symbol URI |
| `BatchAnnotationGet` | v1.5 | Bulk annotation lookup |
| `ReindexFiles` | v1.6 | Trigger reindex for specific URIs |
| `Similarity` | v1.6 | Cosine similarity between two files |
| `QueryExpansion` | v1.6 | Expand a query with semantically related terms |
| `Cluster` | v1.6 | Group files by semantic proximity |
| `ExportEmbeddings` | v1.6 | Raw embedding export |
| `NearestByContrast` | v1.7 | Like-URI minus unlike-URI retrieval |
| `Outliers` | v1.7 | Semantically isolated files |
| `SemanticDrift` | v1.7 | Cosine distance between two files |
| `SimilarityMatrix` | v1.7 | Pairwise similarity matrix |
| `FindSemanticCounterpart` | v1.7 | Best match for a file within a candidate set |
| `Coverage` | v1.7 | Embedding coverage stats by directory |
| `FindBoundaries` | v1.8 | Semantic boundary detection within a file |
| `SemanticDiff` | v1.8 | Compare two text blobs by embedding distance |
| `NearestInStore` | v1.8 | Nearest neighbours against an in-memory store |
| `NoveltyScore` | v1.8 | Per-file novelty (0–1, higher = fewer neighbours) |
| `ExtractTerminology` | v1.8 | Domain term extraction from a file set |
| `PruneDeleted` | v1.8 | Remove embeddings for deleted files |
| `GetCentroid` | v1.9 | Mean embedding vector for a file set |
| `StaleEmbeddings` | v1.9 | Files with out-of-date embeddings |
| `NearestByTextFiltered` | v1.9 | Nearest-by-text with glob filter + min score |
| `NearestByFileFiltered` | v1.9 | Nearest-by-file with glob filter + min score |
| `ExplainMatch` | v2.0 | Chunk-level explanation of why a file matched a query |

**Response types added**: `HandshakeInfo`, `CoverageInfo`, `DirCoverage`,
`BoundaryRange`, `SemanticDiffInfo`, `NoveltyInfo`, `NoveltyItem`, `TermItem`,
`ExplanationChunk`. `IndexStatusInfo` gains `MixedModels bool` and
`ModelsInIndex []string`. `FileStatusInfo` gains `EmbeddingModel string`.

#### `searchSymbols` — semantic fallback + re-ranking with filter

`SemanticSearchWithLIP` now accepts `filter string` and `minScore float32`
parameters and delegates to `NearestByTextFiltered`. The filter accepts glob
patterns (e.g. `"internal/api/**"`) to restrict semantic results to a subtree.
Call site in `symbols.go` updated; existing callers pass `"", 0` for unchanged
behaviour.

#### `reviewPR` — `semantic-novelty` check

A new `semantic-novelty` check runs alongside the existing 20 review checks.
It calls `NoveltyScore` on changed files and flags any with a score ≥ 0.7 as
"semantically novel" — files with few neighbours in the embedding index that
may lack test coverage. Degrades silently when LIP is unavailable; skipped
automatically when fewer than 2 files are changed.

#### `getAffectedTests` — semantic test discovery

After the SCIP-based test collection pass, a LIP pass runs
`NearestByFileFiltered(fileURI, 5, "*_test.go", 0.6)` for each changed source
file. Matching test files are added to the result with
`Reason: "semantic-proximity"` and `Confidence` set to the LIP score. Files
already found by SCIP are not duplicated.

#### `explainFile` — semantic boundary detection

After the existing symbol analysis, `toolExplainFile` calls
`FindBoundaries(fileURI, 0, 0, "")` (defaults: 30-line chunks, 0.3 threshold)
and appends a `semantic_boundaries` array to the response:
```json
[{"start_line": 1, "end_line": 45, "shift_magnitude": 0.71, "nearest_symbol": "handleAuth"}]
```
Silently omitted when LIP is unavailable or returns no boundaries.

#### `getArchitecture` — semantic coupling matrix

After structural architecture data is assembled, `toolGetArchitecture` collects
representative file URIs for each module, calls `SimilarityMatrix`, and adds a
`semantic_coupling` field:
```json
{"modules": ["internal/auth", "internal/api"], "matrix": [[1.0, 0.74], [0.74, 1.0]]}
```
Also calls `GetCentroid` over up to 500 repo files and records
`repo_centroid_included: N` in the response metadata. Both are silently omitted
when LIP is unavailable or fewer than 2 modules are embedded.

#### `doctor` — LIP coverage + stale embedding + model provenance

The LIP health section in `ckb doctor` now reports:
- Coverage: `N% embedded (Y/Z files)`
- Stale embeddings: count of files with out-of-date vectors
- Mixed-model warning when multiple embedding models are present in the index
- List of active embedding models

### Changed

- `NearestByFile` and `NearestByText` are now thin wrappers over
  `NearestByFileFiltered` and `NearestByTextFiltered` respectively
- `GetEmbedding` and `GetSymbolEmbedding` delegate to `GetEmbeddingsBatch`
  (the old `"embedding_get"` and `"symbol_embedding"` wire variants had no
  corresponding Rust enum variants)

---

## [8.5.0] - 2026-04-11

### Added

#### Cartographer bundled as git subtree (`third_party/cartographer/`)

Cartographer is now vendored directly into the repo instead of requiring a
sibling directory at `../../../../Cartographer/`. Contributors no longer need
two repos co-located. Update via:

```bash
git subtree pull --prefix third_party/cartographer \
  https://github.com/SimplyLiz/Cartographer.git master --squash
```

#### Three new MCP tools (Cartographer-backed)

**`detectShotgunSurgery`** — Detect files that historically required simultaneous
edits across many unrelated files. Ranked by co-change dispersion score.
```
detectShotgunSurgery(repo_path: "/path/to/repo", min_partners: 3, limit: 100)
```

**`getArchitecturalEvolution`** — Architectural health snapshots over git history.
Returns health score trend (improving/stable/degrading), debt indicators, and
recommendations.
```
getArchitecturalEvolution(repo_path: "/path/to/repo", days: 90)
```

**`getBlastRadius`** — Graph-theoretic blast radius for a file or module. Works
without a SCIP index; complements `analyzeImpact` for unindexed repos.
```
getBlastRadius(repo_path: "/path/to/repo", target: "src/core/engine.go", max_related: 50)
```

#### LIP semantic search (`GetEmbedding`)

`internal/lip` now exposes `GetEmbedding(uri, model)` — requests a
TurboQuant-quantized embedding vector from the LIP daemon for a given file URI.
Returns `[]float32` suitable for direct dot-product similarity ranking without
dequantization. Degrades silently when LIP is not running.

### Performance

#### SCIP loader: lazy CallerIndex — eliminates load-time regression on small indexes

The caller inverted index (`CallerIndex`) is now built on the first `FindCallers`
call rather than at `LoadIndex` time. This removes ~22k persistent heap objects
from the initial SCIP load on small indexes (1k docs), which were causing elevated
GC pressure and a measurable load-time regression. Medium/large indexes are
unaffected — the index is built once and cached thereafter.

**Benchmark impact vs v8.4.0 (small, 1k docs):** load alloc count is unchanged
(~375.6k in both versions — the CallerIndex for 1k docs is not large enough to
register in alloc counts). The win is GC liveness: ~22k heap objects that would
have been promoted to old-gen are no longer live after load. No change for
medium/large.

#### SCIP loader: `DiscardUnknown` proto decode

Both `proto.Unmarshal` calls in the document stream parser now use
`proto.UnmarshalOptions{DiscardUnknown: true}`. This skips the reflection-based
unknown-field accumulator, reducing allocations during SCIP file decode.

**Measured vs v8.4.0 (medium, 10k docs):**
- `B/op`: **909 MiB → 781 MiB (-14.10%)**
- `allocs/op`: **6.94M → 6.64M (-4.27%)**

Small and large indexes show no measurable change (unknown-field savings are
proportionally smaller there).

#### CallerIndex builder: generation-counter deduplication

`buildCallerIndex` now reuses the `ivs` interval slice across documents (resliced
to zero, grown only when needed) and replaces the per-document `map[edge]bool`
with a generation counter (`map[edge]uint64`). Eliminates ~2k per-load allocs on
the 1k-doc case and removes all per-document map allocs on medium/large.

#### `PopulateFromFullIndexStreaming`: two-pass streaming to prevent OOM on large repos

`PopulateFromFullIndex` has always called `LoadSCIPIndex` which materialises the
entire `*SCIPIndex` in memory before processing a single file. On a 50k-doc
monorepo this peaks at ~15 GB and causes sustained GC pressure (observed: 485s
first run vs a consistent 83s with streaming).

`PopulateFromFullIndexStreaming` replaces this with a two-pass strategy over
the on-disk SCIP file (via `scip.StreamDocuments`), never materialising the full
index:

- **Pass 1**: build the `symbol→file` map — one `*scippb.Document` live at a time,
  freed by GC before the next arrives. Peak live heap ≈ the symbolToFile map alone.
- **Pass 2**: stream documents again, extract deltas via the new proto-native
  `extractFileDeltaFromProto` (skips all `convertDocument` allocations), write SQL
  in 1000-file batches.

`extractFileDeltaFromProto` works directly on `*scippb.Document` so there are no
intermediate `*scip.Document` / `*scip.Occurrence` / `*scip.SymbolInformation`
allocations per document per pass.

**Benchmark vs `PopulateFromFullIndex` (50k docs, Apple M4 Pro, -count=2):**

| | current | streaming | delta |
|---|---|---|---|
| B/op | 15.69 GB | 15.23 GB | -2.9% |
| allocs/op | 166.4M | 181.8M | +9.3% |
| time (cold) | **485s** | **83s** | **-83%** |
| time (warm) | 122s | 83s | -32% |

The extra allocs/op come from two proto-unmarshal passes vs one (plus
`convertDocument` in the current path). The time improvement reflects reduced
GC pressure: streaming never has more than one document live at a time, so GC
never needs to scan or collect the 15 GB of live SCIPIndex data.

#### Incremental write path: major throughput improvements (landed in v8.4.0)

The following improvements shipped in v8.4.0 and are reflected in the v8.4.0
benchmark baseline. Documented here for completeness:

- **Parallel `extractFileDelta`**: GOMAXPROCS worker goroutines extract file
  deltas concurrently during `PopulateFromFullIndex`. Cuts large-repo population
  time by the number of available cores.
- **Batched transactions** (1000 files/tx): WAL stays bounded on 50k-file indexes
  instead of growing to multi-GB. Eliminates the 10h+ timeout on large repos.
- **`PRAGMA synchronous=OFF`** during bulk load: safe because a failed full index
  is always re-run from scratch.
- **Bulk INSERT for `file_symbols`**: 499-row multi-value `INSERT` batches reduce
  round-trips from 50k to ~100 for large repos.
- **Hoisted prepared statements** in `ApplyDelta`: `symbol`, `callgraph`, and
  `file_deps` statements prepared once per delta instead of once per file.

**Benchmark vs v8.2.1 (v8.4.0 baseline):**
- `ApplyDelta/large` (50k files): **50s → 42s (-16%)**
- `ExtractFileDelta/50syms`: **109µs → 90µs (-17%)**
- `GetDependencies/1000files`: **7.0ms → 6.3ms (-10%)**
- SCIP allocs geomean: **-12%** (backing-slice OccurrenceRef optimization)

#### SCIP loader: O(1) `FindCallers` via CallerIndex (landed in v8.4.0)

`FindCallers` was O(docs × funcs × occs). It now uses an inverted map built from
Documents, making every caller lookup O(1). The index uses a sorted interval scan
with early-break for function containment and a generation-counter for
cross-document edge deduplication.

## [8.3.0] - 2026-03-27

### Added

#### Compliance Audit (`ckb audit compliance`)
Full regulatory compliance auditing with 131 checks across 20 frameworks:

```bash
ckb audit compliance --framework=gdpr,iso27001    # Specific frameworks
ckb audit compliance --framework=all              # All 20 frameworks
ckb audit compliance --recommend                  # Auto-detect applicable frameworks
ckb audit compliance --framework=gdpr --ci        # CI mode with exit codes
```

**20 frameworks:** GDPR, CCPA, ISO 27701, EU AI Act, ISO 27001, NIST 800-53, OWASP ASVS, SOC 2, PCI DSS, HIPAA, DORA, NIS2, FDA 21 CFR Part 11, EU CRA, SBOM/SLSA, DO-178C, IEC 61508, ISO 26262, MISRA C, IEC 62443.

**Cross-framework mapping:** A single finding (e.g., hardcoded credential) automatically surfaces all applicable regulations with specific clause references and CWE IDs.

**Framework recommendation (`--recommend`):** Scans codebase for indicators (HTTP handlers, PII fields, database imports, payment SDKs) and recommends applicable frameworks with confidence scores.

**Output formats:** human, json, markdown, sarif.

**MCP tool:** `auditCompliance` — runs compliance audit via MCP using the persistent SCIP index.

#### MCP Tools: `listSymbols` and `getSymbolGraph`

**`listSymbols`** — Bulk symbol listing without search query:
```
listSymbols(scope: "src/services/", kinds: ["function"], minLines: 30, sortBy: "complexity")
```
Returns complete symbol inventory with body ranges (`lines`, `endLine`) and complexity metrics (`cyclomatic`, `cognitive`). Replaces exploring 40 files one-by-one.

**`getSymbolGraph`** — Batch call graph for multiple symbols:
```
getSymbolGraph(symbolIds: [...30], depth: 1, direction: "callers")
```
Returns deduplicated nodes and edges with complexity per node. One call replaces 30 serial `getCallGraph` calls.

#### `searchSymbols` Enhancements

- **Complexity metrics:** Results now include `lines`, `cyclomatic`, `cognitive` per symbol via tree-sitter enrichment
- **Server-side filtering:** `minLines`, `minComplexity`, `excludePatterns` params — filter 80% of noise server-side instead of client-side
- **`batchGet` with `includeCounts`:** Returns `referenceCount`, `callerCount`, `calleeCount` per symbol (parallel SCIP lookups)

#### Symbol Body Ranges (`startLine`, `endLine`, `lines`)

`searchSymbols`, `explore` keySymbols, and `getSymbolGraph` now return full body ranges via tree-sitter enrichment. Consumers no longer need to read source files for brace-matching.

#### Explore keySymbols Improvements

- Functions rank above struct fields (behavioral analysis priority)
- Tree-sitter supplement fills in functions when SCIP returns only types
- Per-symbol `cyclomatic` and `cognitive` complexity

#### `getFileComplexity` in Refactor Preset

Previously only available in `full` preset (96 tools). Now in `refactor` (39 tools).

### Fixed

#### Bug-Pattern False Positives (42 → 0)
- **defer-in-loop:** Recognize `func(){}()` closure pattern as correct (defer fires per iteration)
- **discarded-error:** Skip closure bodies in IIFE patterns; add `singleReturnNew` allowlist (NewScanner, NewReader, etc.); add `noErrorMethods` (Scan, WriteHeader, WriteJSON, WriteError, BadRequest, NotFound, InternalError)
- **missing-defer-close:** Remove NewReader/NewWriter from resource-opening functions (bufio wrappers don't need Close)
- **nil-after-deref:** 30-line gap threshold filters cross-scope false matches
- **shadowed-err:** Only flag when outer `err` is standalone function-body-level `:=`; treat if/for/switch initializer `:=` as scoped

All fixes use `FindNodesSkipping` — scope-aware tree-sitter node search that stops recursion at `func_literal` boundaries.

#### Secrets Scanner
- Shell variable interpolation (`${VAR:-default}`, `${VAR:?error}`) in Docker Compose URLs no longer flagged as password_in_url
- Shell environment leak: `env -i` wrapper prevents user profile (.zshrc) from corrupting subprocess output

#### Test-Gap Detection
- `vi.mock`/`jest.mock` module-level mocking recognized — functions covered by module mocks no longer flagged
- Barrel/re-export files (`export * from '...'`) skipped — pure re-exports have no logic to test

#### Coupling Check
- Expanded noise filter: test files, dependency manifests (go.mod, package.json), documentation, generated directories (dist/, build/, l10n/, __generated__/)
- Generated file suffixes: .pb.go, .pb.h, .pb.cc, .pb.ts, _grpc.pb.go, _pb2.py, .g.dart, .freezed.dart, .mocks.dart, _string.go, wire_gen.go, _mock.go, .bundle.js, .arb, .d.ts
- Flutter l10n false positive fixed (#185): .arb files excluded from coupling analysis

#### Compliance Audit FP Reduction (11,356 → ~50 findings)
- Deep-nesting: threshold 4→6, reset at function boundaries, 3-per-file cap
- Dead-code: skip Go files (handled by AST-based bug-patterns)
- Dynamic-memory: skip garbage-collected languages
- Global-state: exclude regexp.MustCompile, errors.New, sync primitives
- Swallowed-errors: remove overly broad `_ = obj.Method()` pattern
- Eval-injection: skip Go and .github/ directories
- Insecure-random: inline import scanning for crypto/rand vs math/rand; skip import lines
- Path-traversal: skip filepath.Join, HasPrefix comparisons, testdata/
- Non-FIPS-crypto: skip strings.Contains pattern matching
- SQL injection (PCI DSS): add parameterized query detection, #nosec support
- TODO detection: case-sensitive TEMP, skip "Stub:/Placeholder:/Note:" comments, require comment context

#### FTS Empty Query Bug
`FTS.Search("")` returned empty results (early return for empty query). Added `listAll()` method that queries `symbols_fts_content` directly. Fixes `listSymbols` and `searchSymbols("")` returning 0 on MCP.

#### MCP Server Warmup
Changed warmup from `SearchSymbols("", 1)` (cached empty results before SCIP loaded) to `RefreshFTS()` (populates FTS from SCIP without caching search results).

#### IEC 61508 Tree-Sitter Crash
`complexityExceededCheck` bypassed thread-safe `AnalyzeFileComplexity()` wrapper, calling `ComplexityAnalyzer.AnalyzeFile()` directly — SIGABRT when concurrent checks hit CGO.

#### Daemon API Endpoints (7 stubs → implementations)
- Schedule list/detail/cancel via scheduler.ListSchedules()
- Repo list/detail via repos.LoadRegistry()
- Federation list/detail via federation.List()/LoadConfig()
- CLI daemon status: HTTP health query with version/uptime display

#### Query Engine Stubs (4 → implementations)
- Ownership refresh: CODEOWNERS parsing + git-blame analysis
- Hotspot refresh: git churn data with 90-day window
- Responsibility refresh: module responsibility extraction
- Ownership history: storage table query

### Changed
- Score calculation: floor is 0 (not 20), per-rule deduction cap of 10 documented
- `LikelyReturnsError`: removed "Scan" from error patterns, added `singleReturnNew` and `noErrorMethods` maps
- Generated file detection: 20+ new patterns (protobuf, Go generators, Dart/Flutter, GraphQL, bundlers)
- Per-check findings cap (50 max) in compliance engine
- Compliance config: `DefaultDaemonPort` constant replaces hardcoded 9120

### Performance
- `batchGet` with `includeCounts`: parallel reference/caller/callee lookups (10-concurrent semaphore)
- FTS multiplier: 2x → 10x when filters active (handles SCIP struct field flooding)
- MCP index warmup: background `RefreshFTS()` on engine init

## [8.2.0] - 2026-03-21

### Added

#### Unified PR Review Engine (`ckb review`)
A comprehensive code review command that orchestrates 20 quality checks in parallel:

```bash
ckb review --base=main              # Human-readable review
ckb review --base=main --ci         # CI mode (exit 0=pass, 1=fail, 2=warn)
ckb review --base=main --post=123   # Post as PR comment
ckb review --staged                 # Review staged changes
ckb review --checks=secrets,breaking,bug-patterns  # Specific checks only
```

**20 checks:** breaking changes (SCIP), secrets, tests, complexity (tree-sitter), health scoring (8-factor weighted), coupling (git co-change), hotspots (churn ranking), risk scoring, dead code (SCIP + grep verification), test gaps, blast radius (SCIP, framework-filtered), bug patterns (10 AST rules), PR split suggestion, comment/code drift, format consistency, critical paths, traceability, reviewer independence, generated file detection, change classification.

**7 output formats:** human, json, markdown, sarif, codeclimate, github-actions, compliance.

#### Bug Pattern Detection (10 AST Rules)
Tree-sitter-based bug detection with differential analysis (only new issues reported):

- `defer-in-loop` — resource leak from deferred calls in loops
- `unreachable-code` — statements after return/panic
- `empty-error-branch` — `if err != nil { }` with no handling
- `unchecked-type-assert` — `x.(string)` without comma-ok
- `self-assignment` — `x = x` (likely typo)
- `nil-after-deref` — variable used before nil check
- `identical-branches` — if/else with same body
- `shadowed-err` — `err` redeclared with `:=` in inner scope
- `discarded-error` — error return value ignored (with receiver-type allowlist for strings.Builder, bytes.Buffer, hash.Hash)
- `missing-defer-close` — resource opened without defer Close()

All 10 rules validated against known-buggy and clean-code corpus tests.

#### HoldTheLine Enforcement
Findings are post-filtered to only changed lines when `HoldTheLine: true` (default). Pre-existing issues on unchanged lines are suppressed. Test-gap and hotspot findings are exempt (file-level concerns).

#### Multi-Provider LLM Narrative (`--llm`)
Optional AI-powered review narrative that replaces the deterministic summary:

```bash
ckb review --base=main --llm   # Requires ANTHROPIC_API_KEY or GEMINI_API_KEY
```

- Auto-detects provider from environment (Gemini or Anthropic)
- Self-enrichment: CKB verifies own findings via `findReferences` and `analyzeImpact` before sending to LLM
- Triage field on enriched findings (`confirmed`/`likely-fp`/`verify`) guides LLM reasoning
- LLM identifies CKB false positives and deprioritizes framework noise

#### Finding Dismissal Store
Users can dismiss findings by editing `.ckb/review-dismissals.json`:

```json
{"dismissals": [{"ruleId": "ckb/hotspots/volatile-file", "file": "cmd/ckb/daemon.go", "reason": "Expected churn"}]}
```

Dismissed findings are filtered from all future reviews.

#### MCP Tool: `reviewPR`
New MCP tool with compact mode for AI consumers:

```
reviewPR(baseBranch: "main", compact: true)
```

Compact mode returns ~1k tokens instead of ~30k — verdict, non-pass checks, top 10 findings, health summary. Reduces AI assistant context usage by 97%.

Supports `staged`, `scope`, `compact`, `failOnLevel`, `criticalPaths` parameters.

#### Claude Code Skill (`/ckb-review`)
`ckb setup --tool=claude-code` now installs a `/ckb-review` slash command that orchestrates CKB's structural analysis with LLM semantic review. Interactive setup prompts for skill installation.

#### PR Comment Posting (`--post`)
```bash
ckb review --base=main --post=123   # Posts markdown review as PR comment via gh CLI
```

#### CI Integration
- GitHub Actions workflow with SARIF upload, PR comments, and inline annotations
- GitLab CI with CodeClimate report
- GitHub Action (`action/ckb-review/action.yml`)

#### Noise Reduction
- Framework symbol filter for blast-radius (skips variables/constants — works across Go, C++, Java, Python via SCIP symbol kinds)
- Hotspot findings capped to top 10 by churn score
- Complexity findings require +5 cyclomatic delta minimum
- Per-rule score cap (10 points max per ruleId)
- Receiver-type allowlist for discarded-error (strings.Builder, bytes.Buffer, hash.Hash)
- Dead-code grep verification catches cross-package references SCIP misses

### Fixed
- `daemon.go`: `followLogs()` deadlocked on EOF (`select{}` → sleep+poll)
- `daemon.go`: `file.Seek()` error silently ignored
- `handlers_review.go`: `context.Background()` → `context.WithTimeout(r.Context(), 5min)`
- `cmd/ckb/review.go`: err shadow at postReviewComment
- `cmd/ckb/setup.go`: err shadow at promptInstallSkills
- Config merge: `DeadCodeMinConfidence` and `TestGapMinLines` overrides from config file now work (default values no longer block merge)
- Go bumped to 1.26.1 (4 stdlib CVEs)
- gosec findings annotated/resolved across codebase

### Changed
- Version: 8.1.0 → 8.2.0
- Schema version: 8.2
- `complexity.findNodes` exported as `FindNodes` for use by bug-pattern rules
- `LLMConfig` added to config with `Provider`, `APIKey`, `Model` fields
- MCP `reviewPR` tool description updated (20 checks, staged/scope/compact params)
- CLAUDE.md updated with review documentation

### Performance
- Tree-sitter checks serialized with proper mutex discipline (cgo safety)
- Hotspot scores pre-computed once and shared between checks
- Health check subprocess calls reduced ~60%
- Batch git-blame operations for repo metrics

## [8.1.0] - 2026-01-31

### Added

#### Coverage Configuration Options
Coverage file detection is now configurable via `.ckb/config.json`:

```json
{
  "coverage": {
    "paths": ["coverage/custom-lcov.info"],
    "autoDetect": true,
    "maxAge": "168h"
  }
}
```

- `paths`: Custom paths to check for coverage files
- `autoDetect`: Use language-specific auto-detection (default: true)
- `maxAge`: Max age before marking as stale (default: 7 days)

#### Orphaned Index Detection
`ckb doctor` now includes an `orphaned-indexes` check that scans for indexes pointing to repos that no longer exist:

```
$ ckb doctor

✓ orphaned-indexes: Index cache: 234 MB (12 repos), 2 orphaned
  → ckb cache clean --orphaned
```

#### Test Mapping (`ckb affected-tests`)
New command to find tests affected by current changes:

```bash
$ ckb affected-tests

Affected Tests
──────────────────────────────────────────────────────────

Found 8 test files:
  • 5 direct (test references changed code)
  • 3 transitive (test uses affected code)

Run command:
  go test ./internal/query/... ./internal/diff/...
```

**Features:**
- Maps changed symbols to affected test files via SCIP
- Finds corresponding test files by naming convention (e.g., `foo.go` → `foo_test.go`)
- Generates language-appropriate run commands
- `--format=list` for CI integration

#### --include-tests Flag Wiring
The `--include-tests` flag now works end-to-end in `ckb impact diff`:
- Properly sets `IsTest` flag on references based on file path
- Filters test files from changed symbols when `--include-tests=false`

#### Dependency Cycle Detection (`findCycles`)
Detect circular dependencies in module, directory, or file dependency graphs using Tarjan's SCC algorithm:

```bash
# Via MCP
findCycles { "granularity": "directory", "targetPath": "internal/" }
```

- Uses Tarjan's strongly connected components to find real cycles
- Recommends which edge to break (lowest coupling cost)
- Severity classification: size ≥5 = high, ≥3 = medium, 2 = low
- Available in `refactor` preset

#### Move/Relocate Change Type
`prepareChange` and `planRefactor` now support `changeType: "move"` with a `targetPath` parameter:

```bash
prepareChange { "target": "internal/old/handler.go", "changeType": "move", "targetPath": "pkg/handler.go" }
```

- Scans all source files for import path references that need updating
- Detects target directory conflicts (existing files with same name)
- Generates move-specific refactoring steps in `planRefactor`

#### Extract Variable Flow Analysis
`prepareChange` with `changeType: "extract"` now provides tree-sitter-based variable flow analysis when CGO is available:

- Identifies parameters (variables defined outside selection, used inside)
- Identifies return values (variables defined inside, used after selection)
- Classifies local variables (defined and consumed within selection)
- Generates language-appropriate function signatures (Go, Python, JS/TS)
- Graceful degradation: falls back to line-count heuristics without CGO

#### Suggested Refactoring Detection (`suggestRefactorings`)
Proactive detection of refactoring opportunities by combining existing analyzers in parallel:

```bash
suggestRefactorings { "scope": "internal/query", "minSeverity": "medium" }
```

- **Complexity**: High cyclomatic/cognitive functions → `extract_function`, `simplify_function`
- **Coupling**: Highly correlated file pairs → `reduce_coupling`, `split_file`
- **Dead code**: Unused symbols → `remove_dead_code`
- **Test gaps**: High-risk untested code → `add_tests`
- Each suggestion includes severity, effort estimate, and priority score
- Available in `refactor` preset

## [8.0.2] - 2026-01-22

### Added

#### Grok Support in `ckb setup`
Grok is now a supported AI coding tool in the setup wizard:

```bash
ckb setup --tool=grok          # project-level (.grok/settings.json)
ckb setup --tool=grok --global # global (~/.grok/user-settings.json)
```

Uses `grok mcp add` CLI when available, falls back to file-based configuration. Grok's MCP format includes `name` and `transport` fields alongside the standard `command`/`args`.

#### MCP Registry Support
Added `mcpName` field to npm package.json for publishing to the official MCP Registry (`io.github.simplyliz/ckb`).

#### Compound Tool NFR Scenarios
NFR test suite expanded from 28 to 39 scenarios, adding coverage for v8.0 compound tools:
- `explore` (small, large) — aggregates explainFile + searchSymbols + callGraph + hotspots
- `understand` (small, large) — aggregates symbol detail + findReferences + callGraph
- `prepareChange` (small, large) — aggregates impact + affectedTests + coupling
- `batchGet` (small, large) — batch symbol retrieval (up to 50)
- `batchSearch` (small, medium, large) — multiple concurrent searches

### Changed

#### Dynamic NFR Baselines
NFR tests now compare PR results against the base branch (dynamic baseline) instead of static hardcoded values. Two parallel CI jobs run the tests on both HEAD and base, then a comparison job reports regressions. This catches real regressions relative to the target branch rather than drifting static numbers.

#### NFR Tests Scope
NFR tests now only run on PRs targeting `main` (develop → main merges), reducing CI noise on feature branches.

## [8.0.1] - 2026-01-22

### Improved

#### Human-Readable Output by Default
All CLI commands now default to `--format=human` instead of `--format=json`. This makes the CLI more friendly for interactive use while still supporting `--format=json` for scripting and automation.

#### Quieter Indexer Output
External SCIP indexers (scip-go, scip-typescript, etc.) no longer spam stdout during `ckb index`. Output is now captured and only shown on error or when using `-v` verbose mode.

#### Better Error Messages
- `ckb dead-code` now clearly indicates it's for telemetry-based analysis and suggests using `ckb telemetry dead-code`
- `ckb impact diff` no longer shows confusing "Symbol not found: diff" error; instead provides helpful guidance
- Symbol not found errors now suggest using `ckb search` to find valid symbol IDs

#### Index Missing Guidance
`ckb status` now shows helpful guidance when no SCIP index is found:
- Lists commands that work without index (git-based): `hotspots`, `ownership`, `reviewers`, `diff-summary`, `pr-summary`
- Lists commands that need SCIP index: `search`, `refs`, `callgraph`, `impact`, `dead-code`, `trace`, `explain`

### Fixed
- Consistent `--format=human` support for `diff-summary`, `concepts`, and `impact diff` commands

---

## [8.0.0] - 2026-01-21

**Theme:** Reliability, clarity, and compound operations for AI workflows.

### Added

#### Compound Operations (5 New Tools)

Reduce AI tool calls by 60-70% with smart aggregation tools that combine multiple primitives into single, focused operations.

**`explore` — Area Exploration**

Comprehensive exploration of files, directories, or modules. Replaces the common pattern of `explainFile` → `searchSymbols` → `getCallGraph` → `getHotspots`.

```json
{
  "target": "internal/query",
  "depth": "standard",    // "shallow" | "standard" | "deep"
  "focus": "structure"    // "structure" | "dependencies" | "changes"
}
```

Returns: module overview, key symbols (ranked by importance), dependencies, recent changes, hotspots, and drilldown suggestions.

**`understand` — Symbol Deep-Dive**

Complete symbol understanding with ambiguity handling. Replaces `searchSymbols` → `getSymbol` → `explainSymbol` → `findReferences` → `getCallGraph`.

```json
{
  "query": "HandleRequest",
  "includeReferences": true,
  "includeCallGraph": true,
  "maxReferences": 50
}
```

Returns: full symbol detail, explanation, references grouped by file, callers/callees, related tests, and disambiguation info when multiple matches exist.

**`prepareChange` — Pre-Change Analysis**

Impact analysis before modifying code. Combines `analyzeImpact` + `getAffectedTests` + `analyzeCoupling` + risk calculation.

```json
{
  "target": "ckb:repo:sym:abc123",
  "changeType": "modify"    // "modify" | "rename" | "delete" | "extract"
}
```

Returns: direct dependents, transitive impact metrics, related tests, co-change files, and risk assessment with severity levels and mitigation suggestions.

**`batchGet` — Multiple Symbols by ID**

Retrieve up to 50 symbols in a single call. Returns results and errors keyed by symbol ID.

**`batchSearch` — Multiple Searches**

Execute up to 10 symbol searches in one call. Each query can have its own kind filter and scope.

#### SSE Streaming

Real-time feedback for long-running operations via Server-Sent Events.

**Protocol:**
```json
// Request with streaming
{
  "name": "findReferences",
  "arguments": {
    "symbolId": "ckb:repo:sym:abc123",
    "stream": true,
    "chunkSize": 20
  }
}

// Initial response
{
  "streamId": "abc123",
  "streaming": true,
  "meta": { "chunkSize": 20 }
}

// MCP notifications: ckb/streamMeta, ckb/streamChunk, ckb/streamProgress, ckb/streamComplete
```

**Streamable Tools:**
- `findReferences` — Stream references in chunks with progress updates
- `searchSymbols` — Stream symbol search results

**Event Types:**
| Event | Purpose |
|-------|---------|
| `meta` | Stream metadata (total count, chunk size, backends) |
| `chunk` | Batch of items with sequence number |
| `progress` | Phase updates with percentage |
| `done` | Stream complete with summary |
| `error` | Error with code and remediation |

#### Enhanced `getStatus`

System health with actionable remediation guidance.

```json
{
  "backends": {
    "scip": { "status": "available", "latencyMs": 12 },
    "git": { "status": "available" },
    "lsp": {
      "status": "unavailable",
      "reason": "No LSP server configured",
      "remediation": "Configure LSP server in .ckb/config.json"
    }
  },
  "index": {
    "fresh": false,
    "commitsBehind": 3,
    "lastIndexed": "2h ago",
    "symbolCount": 4521,
    "fileCount": 156
  },
  "overallHealth": "degraded",
  "suggestions": [
    "Run 'ckb index' to refresh stale index",
    "Configure LSP for enhanced code intelligence"
  ]
}
```

**Health Tiers:**
- `available` — Backend working normally
- `degraded` — Backend available but with warnings
- `unavailable` — Backend not available, includes remediation

#### `reindex` Tool

Trigger index refresh via MCP with scope control.

```json
// Input
{ "scope": "full", "async": false }

// Output
{
  "status": "action_required",
  "message": "Index is 3 commits behind. Run 'ckb index' to refresh."
}
```

Status values: `skipped`, `action_required`, `started`, `completed`

#### Structured Error Codes

All MCP errors now include actionable remediation guidance.

| Code | When | Remediation |
|------|------|-------------|
| `AMBIGUOUS_QUERY` | Multiple symbols match | Narrow with scope, kind, or more specific name |
| `PARTIAL_RESULT` | Some backends failed | Result incomplete; check backend health |
| `INVALID_PARAMETER` | Bad input | Check parameter format |
| `RESOURCE_NOT_FOUND` | Symbol/file doesn't exist | Verify ID or path |
| `PRECONDITION_FAILED` | Required condition not met | Check index freshness, backend availability |
| `OPERATION_FAILED` | General failure | Check logs, retry |

#### Response Metadata

All tool responses now include structured metadata for AI transparency.

**ConfidenceFactor:** Explains why a confidence score was assigned
```json
{
  "score": 0.85,
  "tier": "medium",
  "factors": [
    { "factor": "scip_exact_match", "weight": 0.9 },
    { "factor": "index_slightly_stale", "weight": -0.05 }
  ]
}
```

**CacheInfo:** Cache hit/miss transparency
```json
{
  "hit": true,
  "tier": "query_cache",
  "age": "45s",
  "key": "findReferences:abc123"
}
```

#### Code Analysis Tools

**`findDeadCode`** — Static dead code detection

Identifies symbols with no references (excluding test files, entrypoints, and interface implementations).

```json
{
  "candidates": [
    {
      "symbolId": "ckb:repo:sym:abc123",
      "name": "unusedHelper",
      "kind": "function",
      "file": "internal/util/helpers.go",
      "confidence": 0.95,
      "reason": "No references found"
    }
  ],
  "excludedReasons": {
    "entrypoint": 12,
    "interface_impl": 8,
    "test_only": 23
  }
}
```

**`getAffectedTests`** — Test coverage mapping

Maps changed symbols to affected test files.

```json
{
  "symbolId": "ckb:repo:sym:abc123",
  "affectedTests": [
    { "file": "auth/handler_test.go", "confidence": 0.95, "reason": "direct_reference" },
    { "file": "api/routes_test.go", "confidence": 0.75, "reason": "transitive" }
  ],
  "runCommand": "go test ./internal/auth/... ./internal/api/..."
}
```

**`compareAPI`** — Breaking change detection

Compares API surface between commits/branches.

```json
{
  "base": "main",
  "head": "HEAD",
  "breaking": [
    {
      "symbol": "ValidateToken",
      "change": "renamed",
      "newName": "ValidateUserToken",
      "affectedCallers": 12
    }
  ],
  "additions": [...],
  "compatible": true
}
```

#### Golden Test Suite

Multi-language test fixtures for regression testing across Go, TypeScript, Python, and Rust.

### Changed

- All MCP tool handlers now use structured `CkbError` instead of raw `fmt.Errorf`
- `getStatus` response includes streaming capabilities info
- Confidence scores now include explanation factors via `FromProvenance()`
- Cached responses include cache tier and age information

### Files Added

**Compound Operations:**
- `internal/query/compound.go` — `Explore()`, `Understand()`, `PrepareChange()`, `BatchGet()`, `BatchSearch()`
- `internal/query/compound_test.go` — Compound operation tests
- `internal/mcp/tool_impls_compound.go` — MCP handlers for compound tools

**Streaming:**
- `internal/streaming/stream.go` — Core Stream type with event sending, heartbeat
- `internal/streaming/chunker.go` — Generic chunking by count and byte size
- `internal/streaming/mcp.go` — MCP notification writer for streams
- `internal/mcp/streaming.go` — StreamingHandler type, registry, wrapForStreaming
- `internal/mcp/tool_impls_streaming.go` — Streaming implementations

**Error Handling:**
- `internal/errors/codes.go` — Error code taxonomy with constructors
- `internal/errors/remediation.go` — Remediation message generation

**Metadata:**
- `internal/envelope/confidence.go` — ConfidenceFactor type and FromProvenance()
- `internal/envelope/cache.go` — CacheInfo type

## [7.6.0]

### Added

#### Real Transitive Impact Analysis
The `analyzeImpact` tool now provides real transitive caller analysis using call graph traversal, replacing the previous stub implementation.

**What's New:**
- **Transitive callers**: See not just direct callers, but callers-of-callers up to depth 4
- **Blast radius summary**: Quick metrics showing module count, file count, unique callers, and risk level
- **Distance tracking**: Each transitive caller includes its distance from the target symbol
- **Confidence decay**: Confidence decreases with depth (0.85 → 0.75 → 0.65 for depths 2/3/4)

**Example Output:**
```json
{
  "blastRadius": {
    "moduleCount": 4,
    "fileCount": 12,
    "uniqueCallerCount": 18,
    "riskLevel": "high"
  },
  "transitiveImpact": [
    { "kind": "transitive_caller", "symbolId": "...", "distance": 2, "confidence": 0.85 },
    { "kind": "transitive_caller", "symbolId": "...", "distance": 3, "confidence": 0.75 }
  ]
}
```

**Blast Radius Thresholds:**
| Level | Criteria |
|-------|----------|
| Low | ≤2 modules AND ≤5 callers |
| High | >5 modules OR >20 callers |
| Medium | Everything in between |

**Usage:**
```bash
# CLI
ckb impact <symbol-id> --depth 3

# MCP
analyzeImpact({ symbolId: "...", depth: 3 })
```

**Files Changed:**
- `internal/impact/types.go` — Added `BlastRadius` struct with classification
- `internal/impact/analyzer.go` — `TransitiveCallerProvider` interface, transitive analysis
- `internal/query/impact.go` — `scipCallerProvider` using SCIP call graph
- `internal/mcp/tool_impls.go` — Added `blastRadius` to MCP output

## [7.5.0]

### Added

#### Change Impact Analysis
Analyze the impact of code changes from git diffs *before* committing. This feature answers: "What downstream code might break?"

**CLI:**
```bash
ckb impact diff                # Analyze working tree changes
ckb impact diff --staged       # Analyze only staged changes
ckb impact diff --base=main    # Compare against a branch
ckb impact diff --depth=3      # Deeper transitive analysis
ckb impact diff --strict       # Fail if index is stale
```

**MCP Tool:** `analyzeChange`

**Key Features:**
- **Git diff parsing** — Uses `sourcegraph/go-diff` to parse unified diffs into structured hunks
- **Symbol mapping** — Maps changed lines to SCIP symbol definitions with confidence scoring
- **Confidence levels** — 1.0 (exact definition), 0.8 (body change), 0.7 (reference), 0.3 (file-level)
- **Aggregated risk** — Weighted factors: symbols changed (20%), direct impact (30%), transitive impact (20%), module spread (30%)
- **Index staleness** — Warns when SCIP index is behind HEAD; `--strict` mode fails if stale
- **Recommendations** — Actionable suggestions (review, test, split) based on analysis

**Files Added:**
- `internal/impact/interfaces.go` — Core types (ChangedSymbol, ParsedDiff, ChangeType)
- `internal/diff/gitdiff.go` — Git diff parser with source file filtering
- `internal/diff/symbolmap.go` — Diff-to-symbol mapper with confidence scoring
- `internal/diff/scipadapter.go` — SCIP index adapter for symbol lookup

**Files Changed:**
- `internal/query/impact.go` — Added `AnalyzeChangeSet()` engine method
- `internal/mcp/tools.go` — Added `analyzeChange` tool definition
- `internal/mcp/tool_impls.go` — Added `analyzeChange` handler
- `cmd/ckb/impact.go` — Added `ckb impact diff` subcommand

See [[Change-Impact-Analysis]] in the wiki for full documentation.

#### Token Efficiency Visibility
Users can now see CKB's token savings compared to bloated MCP servers:

**Startup Banner:**
```
CKB MCP Server v7.5.0
  Active tools: 14 / 76 (18%)
  Estimated context: ~1k tokens
  Preset: core
```

**getStatus Response:**
```json
"preset": {
  "active": "core",
  "exposed": 14,
  "total": 76,
  "estimatedTokens": 1529,
  "fullPresetTokens": 9040,
  "tokenSavingsPercent": 83
}
```

This addresses community feedback about MCP tools consuming 50-80k tokens before conversations even start. CKB's preset system delivers 83% token reduction while maintaining full functionality.

**Preset Discoverability (`--list-presets`):**
```
$ ckb mcp --list-presets

Available presets:

  PRESET        TOOLS         TOKENS  DESCRIPTION
  ------        -----         ------  -----------
  core             14     ~2k tokens  Quick navigation, search, impact analysis (default)
  review           19     ~2k tokens  Code review with ownership and PR summaries
  refactor         19     ~2k tokens  Refactoring analysis with coupling and dead code
  federation       28     ~3k tokens  Multi-repo queries and cross-repo visibility
  docs             20     ~2k tokens  Documentation-symbol linking and coverage
  ops              25     ~2k tokens  Diagnostics, daemon, webhooks, jobs
  full             76     ~9k tokens  Complete feature set (all tools)

Use: ckb mcp --preset=<name>
```

**Future:** Per-tool token breakdown (`--tokens` flag showing individual tool costs) planned for a later release.

**Files Changed:**
- `cmd/ckb/mcp.go` — Multi-line startup banner with token info, `--list-presets` flag
- `internal/mcp/server.go` — Added `EstimateActiveTokens()`, `EstimateFullTokens()` methods
- `internal/mcp/presets.go` — Added `FormatTokens()`, `GetAllPresetInfo()`, `PresetDescriptions`
- `internal/mcp/tool_impls.go` — Token fields in getStatus response

#### Auto Index Updates
Automatic index maintenance across all CKB interfaces—keep your index fresh without manual intervention:

**Watch Mode (CLI):**
```bash
# Watch for changes and auto-reindex (standalone)
ckb index --watch
ckb index --watch --watch-interval 15s

# Watch with MCP server (existing, now configurable)
ckb mcp --watch
ckb mcp --watch --watch-interval 1m
```

**Daemon File Watcher:**
The daemon's file watcher now triggers actual incremental refreshes instead of just logging. When git changes are detected, the daemon queues and executes an incremental update.

**Webhook API:**
```bash
# Trigger incremental refresh via HTTP
curl -X POST http://localhost:9120/api/v1/refresh

# Force full reindex
curl -X POST http://localhost:9120/api/v1/refresh -d '{"full": true}'

# Specify repository
curl -X POST http://localhost:9120/api/v1/refresh -d '{"repo": "/path/to/repo"}'
```

**Response:**
```json
{
  "status": "queued",
  "repo": "/path/to/repo",
  "type": "incremental"
}
```

**Index Staleness Visibility:**
- `ckb status` now shows commits behind HEAD and index age
- MCP `getStatus` response includes `index.commitsBehind`, `index.indexAge`, `index.reason`
- Fresh indexes show ✓, stale indexes show ⚠ with specific reason

**Files Added:**
- `internal/daemon/refresh.go` — RefreshManager for incremental/full reindex
- `cmd/ckb/status_test.go` — Status type tests
- `internal/daemon/refresh_test.go` — RefreshManager unit tests

**Files Changed:**
- `cmd/ckb/index.go` — Added `--watch` and `--watch-interval` flags
- `cmd/ckb/mcp.go` — Added `--watch-interval` flag (min 5s, max 5m)
- `cmd/ckb/status.go` — Added staleness display with commits behind
- `internal/daemon/daemon.go` — Connected file watcher to RefreshManager
- `internal/daemon/server.go` — Added `/api/v1/refresh` endpoint
- `internal/index/metadata.go` — Added `Staleness` type and `GetStaleness()` method
- `internal/mcp/tool_impls.go` — Added index staleness to `getStatus` response
- `CLAUDE.md` — Added "Keeping Your Index Fresh" section

See [[Index-Management]] in the wiki for detailed documentation.

#### Multi-Language Incremental Indexing
Incremental indexing now supports multiple languages via a unified indexer registry:

**Supported languages:**
- Go (scip-go)
- TypeScript/JavaScript (scip-typescript)
- Python (scip-python)
- Dart (scip_dart)
- Rust (rust-analyzer)

**Features:**
- Automatic indexer detection and path resolution (including `~/go/bin`)
- Graceful degradation with install hints when indexer is missing
- Language-specific `SupportsIncremental` flag for safe fallback
- Unified `IndexIncrementalWithLang(ctx, since, lang)` API

**Usage:**
```bash
# Auto-detects language and uses incremental if available
ckb index

# Incremental not available message includes install command
Incremental not available: scip-python not installed (run: pip install scip-python)
```

## [7.4.0]

### Added

#### Tool Presets & Pagination
Token-optimized tool discovery reducing context overhead by up to 83%:

**Presets:**
| Preset | Tools | Tokens | Use Case |
|--------|------:|-------:|----------|
| `core` (default) | 14 | ~1,531 | Essential navigation and analysis |
| `review` | 19 | ~2,294 | Code review: PR summary, ownership |
| `refactor` | 19 | ~2,216 | Refactoring: coupling, dead code |
| `docs` | 20 | ~2,093 | Documentation: coverage, staleness |
| `ops` | 25 | ~2,366 | Operations: jobs, webhooks, metrics |
| `federation` | 28 | ~3,122 | Multi-repo: cross-repo search |
| `full` | 76 | ~9,043 | All tools (legacy behavior) |

**Features:**
- **MCP-compliant pagination** — `tools/list` cursor-based pagination per spec
- **Core-first ordering** — Page 1 always contains functional toolset for non-paginating clients
- **Cursor invalidation** — Cursor rejected when preset or toolset changes
- **`expandToolset` meta-tool** — Dynamic preset expansion with rate limiting (once per session)
- **`tools.listChanged` capability** — Enables dynamic tool list updates

**CLI:**
```bash
ckb mcp                      # Default: core preset (14 tools)
ckb mcp --preset=review      # Code review workflow
ckb mcp --preset=full        # All 76 tools (legacy)
```

**Setup Wizard:**
- `ckb setup` now prompts for preset selection
- `--preset` flag for non-interactive configuration

**Files Added:**
- `internal/mcp/presets.go` — Preset definitions and core-first ordering
- `internal/mcp/cursor.go` — MCP-compliant cursor pagination

**Files Changed:**
- `internal/mcp/server.go` — Preset management and toolset hash
- `internal/mcp/handler.go` — Paginated `handleListTools`
- `internal/mcp/tools.go` — `expandToolset` tool definition
- `internal/mcp/tool_impls.go` — `expandToolset` handler with rate limiting
- `internal/mcp/capabilities.go` — `tools.listChanged: true`
- `cmd/ckb/mcp.go` — `--preset` flag
- `cmd/ckb/setup.go` — Preset selection in wizard

#### Wide-Result Metrics Tracking
Infrastructure for monitoring tool output sizes and truncation rates:

- **`getWideResultMetrics` tool** — Expose wide-result statistics
- **SQLite persistence** — Historical tracking for optimization work
- **Per-tool aggregation** — Invocations, bytes, tokens, truncations
- **Response byte tracking** — Actual JSON payload size for each tool response
- **`ckb metrics` CLI** — View aggregated metrics with `--days`, `--tool`, `--format` flags
- **`ckb metrics export`** — Export versioned metrics to JSON for cross-version comparison

Tracked tools: searchSymbols, findReferences, analyzeImpact, getCallGraph, getHotspots, summarizePr

**Telemetry Findings:**
| Tool | Truncation Rate | Needs Frontier? |
|------|-----------------|-----------------|
| searchSymbols | 45% | Yes |
| getHotspots | 50% | Yes |
| findReferences | 18% | No |
| getCallGraph | 0% | No |
| analyzeImpact | 0% | No |

**Files Added:**
- `internal/mcp/wide_result_metrics.go` — In-memory aggregation with DB persistence
- `internal/storage/metrics_store.go` — SQLite metrics storage
- `cmd/ckb/metrics.go` — CLI metrics command

### Performance

#### SCIP Backend Optimizations
Major performance improvements to the SCIP backend through pre-computed indexes:

| Operation | Before | After | Improvement |
|-----------|--------|-------|-------------|
| FindReferences | 340μs | 2.5μs | **136x faster** |
| SearchSymbols | 930μs | 136μs | **7x faster** |
| FindSymbolLocation | 70μs | 28ns | **2,500x faster** |
| GetCachedSymbol | 210ns | 7.5ns | **28x faster** |

**Changes:**
- **RefIndex**: Inverted reference index built during SCIP load for O(1) reference lookups instead of O(n×m) scans
- **ConvertedSymbols Cache**: Pre-converted symbols avoid repeated parsing of SCIP identifiers, visibility inference, and location lookups
- **ContainerIndex**: Maps occurrence positions to containing symbols for O(1) containment lookup instead of O(n²) nested loops
- **Fast Location Lookup**: `findSymbolLocationFast` uses RefIndex for O(k) definition lookup where k = number of occurrences
- **RateLimiter Cleanup**: Added graceful shutdown with `Stop()` method to prevent goroutine leaks

**Files Changed:**
- `internal/backends/scip/loader.go` — Added `OccurrenceRef`, `RefIndex`, `ConvertedSymbols`, `ContainerIndex` to `SCIPIndex`
- `internal/backends/scip/references.go` — `FindReferences` uses inverted index, added `findContainingSymbolFast`
- `internal/backends/scip/symbols.go` — Added `GetCachedSymbol`, `findSymbolLocationFast`, cached `SearchSymbols`
- `internal/backends/limiter.go` — Added `done` channel and `Stop()` method

**Tests Added:**
- `internal/backends/scip/performance_test.go` — 11 unit tests + 10 benchmarks
- `internal/backends/limiter_test.go` — 5 unit tests + 1 benchmark

#### Git Backend Optimizations
Major performance improvement to `getHotspots` by consolidating git commands:

| Operation | Before | After | Improvement |
|-----------|--------|-------|-------------|
| getHotspots | 26.7s | 498ms | **53x faster** |

**Problem:** For each changed file, ran 4 separate git commands (rev-list, shortlog, log × 2).
With 100+ files = 400+ process spawns.

**Solution:** Single `git log --format=%H|%an|%aI --numstat` command parses all data in one pass.

**Files Changed:**
- `internal/backends/git/churn.go` — Rewrote `GetHotspots` to use single git command

### Added

#### Standardized Response Envelope
All 76 MCP tool responses now include structured metadata in a consistent envelope format:

**Envelope Schema:**
```json
{
  "schemaVersion": "1.0",
  "data": { },
  "meta": {
    "confidence": { "score": 0.85, "tier": "medium", "reasons": [] },
    "provenance": { "backends": ["scip", "git"], "repoStateId": "..." },
    "freshness": { "indexAge": { "commitsBehind": 3, "staleReason": "behind-head" } },
    "truncation": { "isTruncated": true, "shown": 10, "total": 47, "reason": "max-symbols" }
  },
  "warnings": [],
  "suggestedNextCalls": [{ "tool": "findReferences", "params": {...}, "reason": "..." }]
}
```

**Key Features:**
- **Confidence Tiers** — Results scored as high (≥0.95), medium (0.70-0.94), low (0.30-0.69), or speculative (<0.30)
- **Provenance Tracking** — See which backends (SCIP, LSP, git) contributed to results
- **Freshness Info** — Know how stale your index is (commits behind, uncommitted changes)
- **Truncation Metadata** — See how many results were trimmed and why
- **Suggested Next Calls** — AI-friendly drilldown suggestions as structured tool calls
- **Cross-repo Marking** — Federation queries automatically marked as speculative tier

**Files Added:**
- `internal/envelope/envelope.go` — Core types (Response, Meta, Confidence, etc.)
- `internal/envelope/builder.go` — Fluent builder API
- `internal/envelope/confidence.go` — Score to tier mapping
- `internal/envelope/envelope_test.go` — Comprehensive tests
- `internal/mcp/tool_helpers.go` — Convenience wrappers for tool implementations
- `internal/mcp/tool_helpers_test.go` — Tool helper tests

**Files Changed:**
- `internal/mcp/tools.go` — Updated ToolHandler signature
- `internal/mcp/handler.go` — Updated handleCallTool for envelope format
- All `internal/mcp/tool_impls*.go` files — Refactored to return envelope responses

#### Update Notifications
Automatic update checking for all installation methods:

- **GitHub Releases API** — Single source of truth for all install methods (npm, go install, binary)
- **Deferred notification** — Shows at command START from cache (instant, no HTTP during execution)
- **Background refresh** — Cache updated asynchronously for next run
- **24-hour cache** — Checks GitHub at most once per day, stored in `~/.ckb/update-check.json`
- **Smart upgrade message** — npm users see `npm update`, others see GitHub releases URL
- **Protocol-safe** — Skips `mcp` and `serve` commands to avoid breaking protocols

**Disable with:**
```bash
export CKB_NO_UPDATE_CHECK=1
```

**Example output (npm install):**
```
╭─────────────────────────────────────────────────────╮
│  Update available: 7.3.0 → 7.4.0                    │
│  Run: npm update -g @tastehub/ckb                   │
╰─────────────────────────────────────────────────────╯
```

**Example output (go install / binary):**
```
╭─────────────────────────────────────────────────────╮
│  Update available: 7.3.0 → 7.4.0                    │
│  https://github.com/SimplyLiz/CodeMCP/releases      │
╰─────────────────────────────────────────────────────╯
```

#### Hybrid Retrieval with PPR
Graph-based retrieval enhancement using Personalized PageRank:

**Results:**
| Metric | Before | After | Improvement |
|--------|--------|-------|-------------|
| Recall@10 | 62.1% | 100% | +61% |
| MRR | 0.546 | 0.914 | +67% |

**Components:**
- **Eval Suite** — `ckb eval` command measures recall@K, MRR, latency
- **PPR Algorithm** — Personalized PageRank over SCIP symbol graph
- **Seed Expansion** — Expands struct fields to include methods for better cross-module discovery
- **Combined Scoring** — FTS position + PPR score fusion (0.6/0.4 weights)

See Wiki for full documentation.

### Files Added
- `internal/update/check.go` — Core update check logic with npm registry API
- `internal/update/cache.go` — 24-hour cache in `~/.ckb/update-check.json`
- `internal/update/check_test.go` — Tests for version comparison and caching
- `cmd/ckb/eval.go` — Eval CLI command
- `internal/eval/suite.go` — Eval framework with metrics
- `internal/eval/fixtures/*.json` — Test fixtures
- `internal/graph/ppr.go` — PPR algorithm with seed expansion
- `internal/graph/builder.go` — Graph construction from SCIP
- `internal/query/ranking.go` — PPR-based reranking

## [7.3.0]

### Added

#### npm Package Improvements
Better npmjs.com presence and npx reliability:

- **README on npmjs.com** - Package now displays full README on npm registry
- **LICENSE included** - license file bundled with npm package
- **Issue tracker link** - "Report a bug" link on npm page
- **npx sandbox fix** - Node shim auto-detects repo root, fixing #1 support issue

**How the npx fix works:**
The Node.js shim walks up from `process.cwd()` looking for `.ckb/` or `.git/` and sets `CKB_REPO` automatically. This means `npx @tastehub/ckb` now works from subdirectories and MCP clients that don't guarantee working directory.

#### Incremental Indexing v4 (Production-Grade)
Fast, reliable incremental indexing for large codebases:

**Delta Artifacts:**
- **`ckb diff` command** - Generate delta manifests between snapshots
- **CI-generated diffs** - O(delta) ingestion instead of O(N) comparison
- **Delta validation** - Schema version, base snapshot, hash verification
- **`POST /delta/ingest`** - Ingest delta artifacts via API
- **`POST /delta/validate`** - Validate without applying

**FTS5 Search:**
- **SQLite FTS5** - Instant full-text search (replaces LIKE scans)
- **Automatic triggers** - Real-time sync with symbol changes
- **FTS maintenance** - Rebuild, vacuum, integrity-check
- **LIKE fallback** - Graceful degradation for edge cases

**Operational Hardening:**
- **Compaction scheduler** - Automatic snapshot cleanup, journal pruning, FTS vacuum
- **`GET /health/detailed`** - Per-repo metrics, storage info, memory usage
- **`GET /metrics`** - Prometheus metrics (counters, histograms, gauges)
- **Load shedding** - Priority endpoints, circuit breakers, adaptive shedding

#### Language Quality Assessment
Per-language quality metrics and environment detection:

**Quality Tiers:**
- **Tier 1 (Full)** - Go: full support, all features, stable
- **Tier 2 (Standard)** - TypeScript, JavaScript, Python: full support, known edge cases
- **Tier 3 (Basic)** - Rust, Java, Kotlin, C++, Ruby, Dart: basic support, callgraph may be incomplete
- **Tier 4 (Experimental)** - C#, PHP: experimental

**New Endpoints:**
- **`GET /meta/languages`** - Language quality dashboard with tier info, metrics, recommendations
- **`GET /meta/python-env`** - Python venv detection with activation recommendations
- **`GET /meta/typescript-monorepo`** - TypeScript monorepo detection (pnpm, lerna, nx, yarn)

**Environment Detection:**
- Python virtual environment detection (`.venv`, `venv`, `env`, `VIRTUAL_ENV`)
- Python package managers (pyproject.toml, requirements.txt, Pipfile)
- TypeScript monorepo workspaces with per-package tsconfig status

#### Remote Federation Client (v3 Federation Phase 5)
Connect to remote CKB index servers and query them alongside local repositories—transforming federation from local-only aggregation to a distributed code intelligence network.

**Core Features:**
- **Remote Server Management** — Add, remove, enable, disable remote CKB index servers
- **Hybrid Queries** — Search symbols across local federation repos AND remote servers in parallel
- **Source Attribution** — Results show whether they came from "local" or a named remote server
- **Graceful Degradation** — Queries succeed even when some remotes are unavailable

**Caching:**
- Repository list cached for 1 hour
- Metadata cached for 1 hour
- Symbol searches cached for 15 minutes
- Refs and call graph always fresh (not cached)
- Configurable per-server cache TTL

**HTTP Client:**
- Bearer token authentication with environment variable expansion (`${VAR}`)
- Exponential backoff retry logic (max 3 retries)
- Configurable timeouts per server
- Response body limiting (10MB max)

**CLI Commands:**
```bash
# Add a remote CKB index server
ckb federation add-remote <federation> <name> --url=<url> [--token=<token>] [--cache-ttl=1h] [--timeout=30s]

# Remove a remote server
ckb federation remove-remote <federation> <name>

# List remote servers
ckb federation list-remote <federation> [--json]

# Sync metadata from remote server(s)
ckb federation sync-remote <federation> [name] [--json]

# Check remote server status
ckb federation status-remote <federation> <name> [--json]

# Enable/disable remote server
ckb federation enable-remote <federation> <name>
ckb federation disable-remote <federation> <name>
```

**MCP Tools (7 new):**
- `federationAddRemote` — Add a remote server to a federation
- `federationRemoveRemote` — Remove a remote server
- `federationListRemote` — List remote servers in a federation
- `federationSyncRemote` — Sync metadata from remote servers
- `federationStatusRemote` — Get status of a remote server
- `federationSearchSymbolsHybrid` — Search symbols across local + remote
- `federationListAllRepos` — List repos from local and remote sources

**Configuration:**
```toml
[[remote_servers]]
name = "prod"
url = "https://ckb.company.com"
token = "${CKB_PROD_TOKEN}"   # Environment variable expansion
cache_ttl = "1h"
timeout = "30s"
enabled = true
```

#### Authentication & API Keys (v3 Federation Phase 4)
Scoped API key authentication for the index server, enabling secure multi-tenant access with fine-grained permissions.

**Scoped API Keys:**
- **read** — GET requests, symbol lookup, search
- **write** — POST requests, upload indexes, create repos
- **admin** — Full access including token management and deletions

**Per-Repository Restrictions:**
- Limit keys to specific repos using glob patterns (e.g., `myorg/*`)
- Prevents cross-tenant data access in shared deployments

**Rate Limiting:**
- Token bucket algorithm with configurable limits per key
- Returns `429 Too Many Requests` with `Retry-After` header
- Customizable default limits and burst sizes

**Token Management CLI:**
```bash
# Create a new token
ckb token create --name "CI Upload" --scopes write
ckb token create --name "Read-only" --scopes read --repos "myorg/*"
ckb token create --name "Admin" --scopes admin --expires 30d

# List all tokens
ckb token list
ckb token list --show-revoked

# Revoke a token
ckb token revoke ckb_key_abc123

# Rotate a token (new secret, same ID)
ckb token rotate ckb_key_abc123
```

**Token Format:**
- Token: `ckb_sk_` prefix + 64 hex chars (shown once at creation)
- Key ID: `ckb_key_` prefix + 16 hex chars (used for management)
- Secure bcrypt hashing for storage

**Configuration:**
```toml
[index_server.auth]
enabled = true
require_auth = true                    # false = unauthenticated gets read-only
legacy_token = "${CKB_LEGACY_TOKEN}"   # Backward compatibility

[[index_server.auth.static_keys]]
id = "ci-upload"
name = "CI Upload Key"
token = "${CI_CKB_TOKEN}"
scopes = ["write"]
repo_patterns = ["myorg/*"]
rate_limit = 100

[index_server.auth.rate_limiting]
enabled = true
default_limit = 60                     # Requests per minute
burst_size = 10
```

**HTTP Headers:**
- `Authorization: Bearer <token>` — Authentication
- `X-RateLimit-Key: <key_id>` — Rate limit tracking (response)
- `Retry-After: <seconds>` — When rate limited (response)

**Error Responses:**
- `401 Unauthorized` — Missing/invalid/expired/revoked token
- `403 Forbidden` — Insufficient scope or repo not allowed
- `429 Too Many Requests` — Rate limited

**Backward Compatibility:**
- Legacy single-token mode still works via `legacy_token` config
- When `require_auth = false`, unauthenticated requests get read-only access

#### Enhanced Uploads (v3 Federation Phase 3)
Compression support, progress reporting, and incremental (delta) updates for the index upload system. Reduces upload sizes by 70-90% for typical updates.

**Compression Support:**
- **gzip** — `Content-Encoding: gzip` for 60-80% compression
- **zstd** — `Content-Encoding: zstd` for 70-90% compression (faster than gzip)
- Automatic decompression on the server
- Response includes `compression_ratio` showing savings

**Progress Reporting:**
- Logs progress at 10MB intervals for large uploads
- Includes bytes received, MB count, and percentage when Content-Length is known

**Delta Uploads (Incremental):**
- `POST /index/repos/{repo}/upload/delta` — Upload only changed files
- Requires `X-CKB-Base-Commit` header matching current index
- Returns 409 Conflict with `current_commit` if base doesn't match
- Suggests full upload when >50% files changed (configurable)
- Reuses existing incremental infrastructure for efficient processing

**Configuration:**
```toml
[index_server]
enable_compression = true           # Default true
supported_encodings = ["gzip", "zstd"]
enable_delta_upload = true          # Default true
delta_threshold_percent = 50        # Suggest full upload if >N% changed
```

**Delta Upload Example:**
```bash
curl -X POST http://localhost:8080/index/repos/company/core-lib/upload/delta \
  -H "Content-Type: application/octet-stream" \
  -H "Content-Encoding: gzip" \
  -H "X-CKB-Base-Commit: abc123" \
  -H "X-CKB-Target-Commit: def456" \
  -H 'X-CKB-Changed-Files: [{"path":"src/main.go","change_type":"modified"}]' \
  --data-binary @partial-index.scip.gz
```

#### Index Upload (v3 Federation Phase 2)
Push SCIP indexes to the index server via HTTP, eliminating the need for local filesystem paths. This transforms CKB from a "bring your database" model to a centralized index hosting service.

**REST API Endpoints:**
- `POST /index/repos` — Create a new repo ready for upload
- `POST /index/repos/{repo}/upload` — Upload SCIP index file (supports gzip, zstd compression)
- `POST /index/repos/{repo}/upload/delta` — Delta upload (incremental changes only)
- `DELETE /index/repos/{repo}` — Delete an uploaded repo

**Upload Features:**
- Stream large files (100MB+) without memory issues
- Auto-create repos on first upload (configurable)
- Metadata headers: `X-CKB-Commit`, `X-CKB-Language`, `X-CKB-Indexer-Name`
- Full SCIP processing: symbols, refs, call graph extraction
- Compression support: gzip and zstd
- Progress logging for large uploads

**Configuration:**
```toml
[index_server]
enabled = true
data_dir = "~/.ckb-server"      # Server data directory
max_upload_size = 524288000     # 500MB default
allow_create_repo = true        # Allow repo creation via API
enable_compression = true       # Accept compressed uploads
enable_delta_upload = true      # Enable incremental updates
```

**Data Directory Structure:**
```
~/.ckb-server/
├── repos/
│   └── company-core-lib/
│       ├── ckb.db        # SQLite database
│       └── meta.json     # Repo metadata
└── uploads/              # Temp directory for uploads
```

#### Remote Index Serving (v3 Federation Phase 1)
Serve symbol indexes over HTTP for remote federation clients. This enables cross-repository code intelligence without requiring clients to have direct database access.

**Core Features:**
- **Index Server Mode** — New `--index-server` flag for `ckb serve` enables remote index endpoints
- **Multi-Repo Support** — Serve multiple repositories from a single CKB instance
- **TOML Configuration** — Configure repos, privacy settings, and pagination limits via config file
- **Read-Only Connections** — Index server opens databases in read-only mode for safety

**REST API Endpoints:**
- `GET /index/repos` — List all indexed repositories
- `GET /index/repos/{repo}/meta` — Repository metadata and capabilities
- `GET /index/repos/{repo}/files` — List files with cursor pagination
- `GET /index/repos/{repo}/symbols` — List symbols with filtering and pagination
- `GET /index/repos/{repo}/symbols/{id}` — Get single symbol by ID
- `POST /index/repos/{repo}/symbols:batchGet` — Batch get multiple symbols
- `GET /index/repos/{repo}/refs` — List references (call edges) with pagination
- `GET /index/repos/{repo}/callgraph` — List call graph edges with filtering
- `GET /index/repos/{repo}/search/symbols` — Search symbols by name
- `GET /index/repos/{repo}/search/files` — Search files by path

**Security & Privacy:**
- **HMAC-Signed Cursors** — Pagination cursors are signed to prevent tampering
- **Privacy Redaction** — Per-repo controls for exposing paths, docs, and signatures
- **Path Prefix Stripping** — Remove sensitive path prefixes from responses

**CLI:**
- `ckb serve --index-server` — Enable index-serving endpoints
- `ckb serve --index-config <path>` — Load configuration from TOML file

**Configuration Example:**
```toml
[index_server]
enabled = true
max_page_size = 10000

[[repos]]
id = "company/core-lib"
name = "Core Library"
path = "/repos/core-lib"

[default_privacy]
expose_paths = true
expose_docs = true
expose_signatures = true
```

#### Doc-Symbol Linking
Bridge documentation and code with automatic symbol detection:

**Core Features:**
- **Backtick detection** - Automatically detect `Symbol.Name` references in markdown
- **Directive support** - `<!-- ckb:symbol -->` for explicit references, `<!-- ckb:module -->` for module linking
- **Suffix resolution** - Resolve `UserService.Auth` to full SCIP symbol ID with confidence scoring
- **Staleness detection** - Find broken references when symbols are deleted or renamed

**v1.1 Enhancements:**
- **CI enforcement** - `--fail-under` flag for `ckb docs coverage` to enforce minimum coverage in CI
- **Rename detection** - Detect when documented symbols are renamed via alias chain, suggest new names
- **known_symbols directive** - `<!-- ckb:known_symbols Engine, Start -->` allows single-segment detection
- **Fence symbol scanning** - Extract identifiers from fenced code blocks using tree-sitter (8 languages)

**CLI Commands:**
- `ckb docs index` - Scan and index documentation for symbol references
- `ckb docs symbol <name>` - Find docs referencing a symbol
- `ckb docs file <path>` - Show symbols in a document
- `ckb docs stale [path]` - Check for stale references (or `--all` for all docs)
- `ckb docs coverage` - Documentation coverage statistics
- `ckb docs module <id>` - Find docs linked to a module

**MCP Tools:**
- `indexDocs` - Scan and index documentation
- `getDocsForSymbol` - Find docs referencing a symbol
- `getSymbolsInDoc` - List symbols in a document
- `getDocsForModule` - Find docs linked to a module
- `checkDocStaleness` - Check for stale references
- `getDocCoverage` - Coverage statistics

#### Multi-Repo Management
Quick context switching between multiple repositories in MCP sessions:

**Core Features:**
- **Global registry** - Named repo shortcuts stored at `~/.ckb/repos.json`
- **Smart --repo flag** - Auto-detects if argument is a path or registry name
- **Multi-engine support** - Up to 5 engines in memory with LRU eviction
- **Per-repo config** - Each engine loads its own `.ckb/config.json`
- **Repo state tracking** - `valid`, `uninitialized`, `missing` states

**CLI Commands:**
- `ckb repo add [name] [path]` - Register a repository (path defaults to cwd)
- `ckb repo list` - List repos grouped by state
- `ckb repo remove <name>` - Unregister a repo
- `ckb repo rename <old> <new>` - Rename a repo alias
- `ckb repo default [name]` - Get or set default repo
- `ckb repo info [name]` - Show detailed repo info
- `ckb repo which` - Print current repo (for scripts)
- `ckb repo check` - Validate all registered repos

**MCP Tools:**
- `listRepos` - List registered repos with state and active status
- `switchRepo` - Switch active repo context
- `getActiveRepo` - Get current repo info

**Command Flags:**
- `ckb mcp --repo <name>` - Start MCP with specific repo active
- `ckb serve --repo <name>` - Start HTTP server for specific repo

#### Incremental Indexing (Go only)
Index updates in seconds instead of full reindex—O(changed files) instead of O(entire repo).

**Core Features:**
- **Git-based change detection** — Uses `git diff -z` with NUL separators for accurate tracking
- **Rename support** — Properly tracks `git mv` with old path cleanup
- **Delta extraction** — Only processes SCIP documents for changed files
- **Delete+insert pattern** — Clean updates without complex diffing logic
- **Index state tracking** — Tracks "partial" vs "full" state with staleness warnings

#### Incremental Callgraph (v1.1)
Extends incremental indexing with call graph maintenance—outgoing calls from changed files are always accurate.

- **Call edge extraction** — Extracts caller→callee edges during incremental updates
- **Tiered callable detection** — Uses `SymbolInformation.Kind` first, falls back to `().` heuristic
- **Caller resolution** — Resolves enclosing function for each call site via line range matching
- **Location-anchored storage** — Call edges stored with `(caller_file, line, col, callee_id)` for precision
- **Caller-owned edges** — Edges deleted and rebuilt with their owning file (no stale outgoing calls)

#### Transitive Invalidation (v2)
Tracks file-level dependencies and automatically queues dependent files for rescanning when their dependencies change.

- **File dependency tracking** — `file_deps` table tracks which files reference symbols from other files
- **Rescan queue** — `rescan_queue` table with BFS depth tracking and attempt counting
- **Four invalidation modes:**
  - `none` — Disabled (no dependency tracking)
  - `lazy` — Enqueue dependents, drain on next full reindex (default)
  - `eager` — Enqueue and drain immediately with configurable budgets
  - `deferred` — Enqueue and drain periodically in background
- **Budget-limited draining** — `MaxRescanFiles` (default: 200) and `MaxRescanMs` (default: 1500ms) limits
- **Cascade depth control** — `Depth` setting limits BFS traversal (default: 1 = direct dependents only)

**Accuracy Guarantees:**
| Query Type | After Incremental | After Queue Drained |
|------------|-------------------|---------------------|
| Go to definition | Always accurate | Always accurate |
| Find refs FROM changed files | Always accurate | Always accurate |
| Find refs TO changed symbols | May be stale | Accurate |
| Call graph (callees/outgoing) | Always accurate | Always accurate |
| Call graph (callers/incoming) | May be stale | Accurate |

**Automatic Fallback:**
- Falls back to full reindex when >50% files changed
- Falls back on schema version mismatch
- Falls back when no tracked commit exists

**CLI Changes:**
- `ckb index` — Incremental by default for Go projects
- `ckb index --force` — Force full reindex when accuracy is critical

**Configuration (`.ckb/config.json`):**
```json
{
  "incremental": {
    "threshold": 50,
    "indexTests": false,
    "excludes": ["vendor", "testdata"]
  },
  "transitive": {
    "enabled": true,
    "mode": "lazy",
    "depth": 1,
    "maxRescanFiles": 200,
    "maxRescanMs": 1500
  }
}
```

### Files Added

**Incremental Indexing v4:**
- `internal/diff/` - Delta artifact generation
  - `types.go` - Delta JSON schema types
  - `generator.go` - Delta generation (compare two DBs)
  - `validator.go` - Delta validation logic
  - `hasher.go` - Canonical hash computation
- `internal/storage/fts.go` - FTS5 maintenance (rebuild, vacuum, integrity-check)
- `internal/daemon/compaction.go` - Compaction scheduler
- `internal/api/metrics.go` - Prometheus metrics exporter
- `internal/api/middleware_load.go` - Load shedding middleware
- `internal/api/handlers_delta.go` - Delta ingestion endpoints
- `cmd/ckb/diff.go` - `ckb diff` CLI command

**Language Quality:**
- `internal/project/quality.go` - Language quality assessment module
- `internal/api/handlers_quality.go` - Language quality API endpoints

**Remote Federation Client:**
- `internal/federation/` - Remote federation client
  - `remote_types.go` — Response types matching index server API
  - `remote_config.go` — Remote server configuration and env var expansion
  - `remote_client.go` — HTTP client with retry logic and all API methods
  - `remote_cache.go` — Caching wrapper with TTL management
  - `hybrid.go` — Local + remote query merging engine
  - `remote_test.go` — Tests for remote client and configuration
- `cmd/ckb/federation_remote.go` - CLI commands for remote federation
- `internal/mcp/tool_impls_v74.go` - MCP tool implementations for remote federation
- `internal/api/` - Remote index serving and upload
  - `index_config.go` — Configuration types and TOML loading (Phase 3: compression, delta config)
  - `index_types.go` — API response types
  - `index_cursor.go` — HMAC-signed cursor pagination
  - `index_repos.go` — Repository handle management (Phase 1 + 2 + 3)
  - `index_redaction.go` — Privacy redaction logic
  - `index_queries.go` — Database queries for symbols, files, refs, callgraph
  - `index_storage.go` — Server data directory management (Phase 2)
  - `index_processor.go` — SCIP processing pipeline (Phase 2 + 3 delta processing)
  - `handlers_index.go` — HTTP handlers for all index endpoints
  - `handlers_upload.go` — HTTP handlers with compression/progress (Phase 2 + 3)
  - `handlers_upload_delta.go` — Delta upload handler (Phase 3)
  - `handlers_index_test.go` — Tests for cursors, redaction, handlers
  - `handlers_upload_test.go` — Tests for upload, compression, delta (Phase 2 + 3)

**Doc-Symbol Linking:**
- `internal/docs/` - New package for doc-symbol linking
  - `types.go` - Core types (Document, DocReference, StalenessReport, etc.)
  - `scanner.go` - Markdown scanning with backtick/directive/fence detection
  - `resolver.go` - Symbol resolution with suffix matching
  - `staleness.go` - Staleness checking with rename detection
  - `indexer.go` - Document indexing orchestration
  - `store.go` - SQLite persistence for documents and references
  - `coverage.go` - Coverage analysis
  - `fence_parser.go` - Tree-sitter identifier extraction from fences
- `cmd/ckb/docs.go` - CLI commands
- `internal/query/docs.go` - Query engine integration
- `internal/mcp/handlers_docs.go` - MCP tool handlers
- `internal/incremental/` — New package for incremental indexing
  - `types.go` — Core types (FileState, ChangeSet, FileDelta, DeltaStats, CallEdge, TransitiveConfig)
  - `store.go` — SQLite persistence for indexed_files, file_symbols, index_meta
  - `detector.go` — Git-based and hash-based change detection
  - `extractor.go` — SCIP delta extraction for changed files only
  - `updater.go` — Database updates with delete+insert pattern
  - `deps.go` — Transitive invalidation with file dependency tracking and rescan queue
  - `indexer.go` — Orchestration and state management
  - `indexer_test.go`, `deps_test.go`, `types_test.go` — Tests

### Changed
- `internal/federation/config.go` — Added RemoteServers field to Config struct
- `internal/federation/index.go` — Schema v3 with remote_servers, remote_repos, remote_cache tables
- `internal/mcp/tools.go` — Registered 7 new MCP tools for remote federation
- `internal/api/server.go` — Added IndexRepoManager, NewServer now returns error
- `internal/api/routes.go` — Added /index/* route registration
- `cmd/ckb/serve.go` — Added --index-server and --index-config flags
- `internal/storage/schema.go` — Schema v8 with callgraph, file_deps, and rescan_queue tables
- `cmd/ckb/index.go` — Incremental indexing flow with `--force` flag

## [7.2.0]

### Added

#### `ckb setup` - Multi-Tool MCP Configuration
- Interactive setup wizard for configuring CKB with AI coding tools
- Support for 6 AI tools:
  - **Claude Code** - `.mcp.json` (project) or `claude mcp add` (global)
  - **Cursor** - `.cursor/mcp.json` (project/global)
  - **Windsurf** - `~/.codeium/mcp_config.json` (global only)
  - **VS Code** - `.vscode/mcp.json` (project) or `code --add-mcp` (global)
  - **OpenCode** - `opencode.json` (project/global)
  - **Claude Desktop** - Platform-specific paths (global only)
- `--tool` flag to skip interactive menu
- `--npx` flag for portable npx-based setup
- Windows path support for Windsurf and Claude Desktop

#### `ckb index` - Extended Language Support
- Added 5 new languages:
  - **C/C++** via scip-clang with `--compdb` flag for compile_commands.json
  - **Dart** via scip-dart
  - **Ruby** via scip-ruby with sorbet/config validation
  - **C#** via scip-dotnet with *.csproj detection
  - **PHP** via scip-php with vendor/bin check
- Bounded-depth glob scanning for nested project detection
- Language-specific validation and prerequisite checks

#### Smart Indexing
- **Skip-if-fresh**: `ckb index` automatically skips reindexing when index matches current repo state
- **Freshness tracking**: Detects commits behind HEAD and uncommitted changes to tracked files
- **Index metadata**: Persists index info to `.ckb/index-meta.json` (commit hash, file count, duration)
- **Lock file**: Prevents concurrent indexing with flock-based `.ckb/index.lock`

#### `ckb status` - Index Freshness Display
- New "Index Status" section showing freshness with commit hash
- Shows stale reasons: "3 commit(s) behind HEAD", "uncommitted changes detected"
- Displays file count for fresh indexes

#### `ckb mcp --watch` - Auto-Reindex Mode
- New `--watch` flag for poll-based auto-reindexing
- Polls every 30 seconds, reindexes when stale
- Uses lock file to prevent conflicts with manual `ckb index`
- Logs reindex activity to stderr

#### Explicit Analysis Tiers
- User-controllable analysis tiers: **fast**, **standard**, **full**
- CLI flag: `ckb search "foo" --tier=fast`
- Environment variable: `CKB_TIER=standard`
- Config file: Add `"tier": "standard"` to `.ckb/config.json`
- Tier display in `ckb status` shows mode (explicit vs auto-detected)
- Precedence: CLI flag > env var > config > auto-detect

#### `ckb doctor --tier` - Tier-Aware Diagnostics
- New `--tier` flag for tier-specific tool requirement checks
- Shows per-language tool status (installed, version, path)
- Displays missing tools with OS-specific install commands
- Validates prerequisites (go.mod, package.json, Cargo.toml, etc.)
- Accepts both naming conventions: `basic`/`fast`, `enhanced`/`standard`, `full`
- Capability matrix showing which features are available per language
- JSON output with `--format json` for scripting

### Changed

- Tier names rebranded: Basic → **Fast**, Enhanced → **Standard**, Full → **Full**

- Multi-language detection now errors instead of silently defaulting to a language

### Fixed

- Fixed Kotlin indexer URL in documentation
- Fixed PHP indexer URL in documentation

## [7.1.0] - 2024-12-XX

Zero-Friction Operation - CKB v7.1 enables code intelligence without requiring a SCIP index upfront.

### Added

#### Tree-sitter Symbol Fallback
- Symbol extraction for 8 languages (Go, TypeScript, JavaScript, TSX, Python, Rust, Java, Kotlin)
- `searchSymbols` works without SCIP index
- Results include `Source: "treesitter"` and `Confidence: 0.7` for transparency

#### `ckb index` Command
- Auto-detects project language from manifests (go.mod, package.json, Cargo.toml, etc.)
- Checks if SCIP indexer is installed, shows install instructions if not
- `--force` flag for re-indexing, `--dry-run` to preview
- Language-specific troubleshooting tips on failure

#### Universal MCP Documentation
- Setup instructions for Claude Code, Cursor, Windsurf, VS Code, OpenCode, Claude Desktop
- Windows `cmd /c` wrapper instructions

### Files Added
- `internal/symbols/treesitter.go` - Tree-sitter symbol extraction
- `internal/symbols/treesitter_test.go` - Tests for all 8 languages
- `internal/project/detect.go` - Language and indexer detection

## [7.0.0] - 2024-12-XX

### Added
- Initial npm package release via `@tastehub/ckb`
- 58 MCP tools for code intelligence

## [6.5.0] - 2024-12-XX

### Added

#### Developer Intelligence
- **Symbol Origins** — `explainOrigin`: Why does this code exist? Git history, linked issues/PRs
- **Co-change Coupling** — `analyzeCoupling`: Find files that historically change together
- **LLM Export** — `exportForLLM`: Token-efficient codebase summaries with importance ranking
- **Risk Audit** — `auditRisk`: 8-factor scoring (complexity, coverage, bus factor, security, staleness, errors, coupling, churn)

## [6.4.0] - 2024-12-XX

### Added

#### Runtime Observability
- **OpenTelemetry Integration** — `getTelemetryStatus`: See real call counts, not just static analysis
- **Dead Code Confidence** — `findDeadCodeCandidates`: Find symbols with zero runtime calls
- **Observed Callers** — `getObservedUsage`: Enrich impact analysis with production data

## [6.3.0] - 2024-12-XX

### Added

#### Contract-Aware Analysis
- **API Boundary Detection** — `listContracts`: Protobuf and OpenAPI contract discovery
- **Consumer Tracking** — Three evidence tiers for cross-repo dependencies
- **Cross-Repo Impact** — `analyzeContractImpact`: "What breaks if I change this shared API?"
- **Contract Dependencies** — `getContractDependencies`: See consumers and dependencies

## [6.2.0] - 2024-12-XX

### Added

#### Federation & Cross-Repository
- **Federation** — Query across multiple repos organization-wide
- **Federation Tools** — `listFederations`, `federationStatus`, `federationSearchModules`, `federationSearchOwnership`, `federationGetHotspots`
- **Daemon Mode** — Always-on service with HTTP API, scheduled tasks, file watching, webhooks
- **Daemon Tools** — `daemonStatus`, `listSchedules`, `listWebhooks`
- **Tree-sitter Complexity** — `getFileComplexity`: Language-agnostic cyclomatic/cognitive complexity for 7 languages

## [6.1.0] - 2024-12-XX

### Added

#### Production Ready
- **Background Jobs** — Queue long operations, track progress, cancel jobs
- **Job Tools** — `getJobStatus`, `listJobs`, `cancelJob`
- **CI/CD Integration** — `summarizePr`: PR risk analysis, ownership drift detection
- **Ownership Drift** — `getOwnershipDrift`: CODEOWNERS vs actual ownership

## [6.0.0] - 2024-12-XX

### Added

#### Architectural Memory
- **Ownership Intelligence** — `getOwnership`: CODEOWNERS + git blame with time-weighted analysis
- **Module Responsibilities** — `getModuleResponsibilities`: What does this module do?
- **Architectural Decisions** — `recordDecision`, `getDecisions`: ADRs with full-text search
- **Module Annotations** — `annotateModule`: Add module metadata
- **Architecture Refresh** — `refreshArchitecture`: Rebuild architectural model

## [5.2.0] - 2024-12-XX

### Added

#### Discovery & Flow
- **Usage Tracing** — `traceUsage`: How is this symbol reached?
- **Entrypoints** — `listEntrypoints`: System entrypoints (API, CLI, jobs)
- **File Orientation** — `explainFile`: File-level orientation
- **Path Explanation** — `explainPath`: Why does this path exist?
- **Diff Summary** — `summarizeDiff`: What changed, what might break?
- **Architecture Overview** — `getArchitecture`: Module dependency overview
- **Hotspots** — `getHotspots`: Volatile areas with trends
- **Key Concepts** — `listKeyConcepts`: Domain concepts in codebase
- **Recently Relevant** — `recentlyRelevant`: What matters now?

## [5.1.0] - 2024-12-XX

### Added

#### Core Navigation
- **Symbol Search** — `searchSymbols`: Find symbols by name with filtering
- **Symbol Details** — `getSymbol`: Get symbol details
- **References** — `findReferences`: Find all usages
- **Symbol Explanation** — `explainSymbol`: AI-friendly symbol explanation
- **Symbol Justification** — `justifySymbol`: Keep/investigate/remove verdict
- **Call Graph** — `getCallGraph`: Caller/callee relationships
- **Module Overview** — `getModuleOverview`: Module statistics
- **Impact Analysis** — `analyzeImpact`: Change risk analysis
- **System Status** — `getStatus`: System health
- **Diagnostics** — `doctor`: System diagnostics
