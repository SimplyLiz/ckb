# CKB — Roadmap

Opened 2026-09-22. Findings from a read-only audit of `internal/lip` against the LIP
daemon it talks to (`~/Work/Protocols/LIP`, `develop` at `932dd9d`, v2.3.6). Each item was
re-checked by hand on both sides before being written down.

Item-sized work with a full write-up goes in `docs/backlog/` as before; this file is the
short list and points there.

---

## 1. The LIP health ping has never worked

`internal/lip/subscribe.go` sends its keepalive as `{"type": "index_status"}`
(`subscribe.go:111`). That tag does not exist on the daemon. LIP tags `ClientMessage` with
serde's snake_case of the variant name, and the variant is `QueryIndexStatus`
(`LIP/bindings/rust/src/query_graph/types.rs:1029`), so the correct tag is
`query_index_status`. The daemon parses the frame as an unknown variant and answers
`unknown_message`.

The response side is wrong too, and independently: the handler waits for
`case "index_status"` (`subscribe.go:178`), while the daemon's reply variant is
`IndexStatusResult` → tag `index_status_result`
(`LIP/.../types.rs:451-452`). Even with the request fixed, the reply would fall through.

Net effect: `Subscribe()`'s health path never delivers a `HealthEvent`. Nothing breaks
loudly, because this package is deliberately built to degrade in silence — its own
docstring says operations "degrade silently when LIP is not running — callers must never
treat LIP unavailability as a fatal error" (`client.go:1-3`). That design is right, and it
is exactly why this stayed invisible.

The same docstring states the rule the ping violates: "every request must include a
`type` field whose value is the snake_case of the Rust enum variant name"
(`client.go:6-8`).

**To do.** Fix both tags. Then add the one test that would have caught it: assert that
every `type` string this package sends appears in the daemon's
`HandshakeResult.supported_messages`, which `Handshake()` already fetches
(`client.go:998-1011`). That turns a whole class of silent typo into a startup error, and
it costs one test.

## 2. Decide what to do with the LIP surface we do not use

CKB speaks the embedding, blast-radius, `stream_context` and similarity side of LIP. It
never sends: `query_definition`, `query_references`, `query_hover`,
`query_workspace_symbols`, `query_document_symbols`, `query_dead_symbols`, `manifest`,
`delta`, `load_slice`, `batch_query`, `similar_symbols`, `query_stale_files`,
`register_tier3_source`, `reindex_stale`, `batch_file_status`, `query_abi_hash`,
`annotation_set`, `annotation_list`, `annotation_workspace_list` — no hits anywhere in
`internal/lip/*.go`.

Two of those are worth a second look rather than a shrug:

- **`batch_query`** runs N sub-queries under a single db-lock acquisition, one socket
  round-trip instead of N. CKB's own compression layer exists to cut answer cost; this
  cuts call cost on the same path.
- **`annotation_*`** persists key/value notes on symbols that survive file changes and
  daemon restarts. CKB has no equivalent and currently reimplements nothing in its place.

**To do.** One backlog entry per candidate, not a sweep.

## 3. The SCIP→URI fallback diverges from LIP's

`SCIPSymbolToURI` (`internal/lip/blast_radius.go:57-79`) mirrors LIP's
`scip_symbol_to_lip_uri` (`LIP/tools/lip-cli/src/cmd/import.rs:873-893`) on the main path:
a 5-field SCIP symbol becomes `lip://<scheme>/<manager>/<pkg>@<version>/<descpath>`,
identical on both sides.

The fallback differs. For anything that is not a 5-field symbol, LIP encodes an opaque
form (`lip://scip/{sanitised}`, `import.rs:886-893`); CKB returns the input unchanged
(`blast_radius.go:73-74`). The Go docstring documents this as "Mirrors …" for the main
path only, so it reads as deliberate — but it means the two sides address the same odd
symbol by two different names, and a lookup will simply find nothing.

**To do.** Confirm the intent. If deliberate, say so in the comment and note what the
caller is expected to do with a non-matching symbol. If not, port the opaque encoder — it
is nine lines.

## 4. The protocol name in our docstring is wrong

`client.go:1` — "LIP (Liz Indexing Protocol)". The protocol is **Linked Incremental
Protocol**. Cosmetic, no wire effect, but this is the first line a reader of the package
sees, and the name has been settled upstream since the v2.3.6 debrand.

## 5. Staleness check

`internal/lip` was last touched 2026-04-25 (`f63ed9a`; before it, `cf7ff98`
"add QueryOutgoingImpact client against v2.3.3 shape"). LIP's HEAD is 2026-07-13, but the
only commit in between is the debrand/version bump — `types.rs` and `session.rs` were not
touched in that window. **There is no wire drift**, and `Handshake()` gates features off
`supported_messages` rather than a hardcoded version, so this client ages well by
construction.

Worth re-running this check whenever LIP's `PROTOCOL_VERSION` moves; it has moved once
since v1.5 (1→2 at v2.1).

---

**Not audited in this pass:** `internal/query/lip_ranker.go`, `lip_health.go`,
`lip_stream_context.go`, `storage/lip_annotations.go`, `cmd/lipcheck/main.go` were located
but not read line by line. The findings above rest on `internal/lip/*.go` and the daemon's
own source, not on those.
