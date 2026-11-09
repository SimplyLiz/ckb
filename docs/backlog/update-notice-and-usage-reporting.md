# Post-update notice and opt-in usage reporting

**Status:** Notice built for 9.4.0 (`internal/update/notice.go`). Usage reporting comes after it.

---

## Post-update notice

Shown once, on the first run of a new version. It says what is new, states the
license kindly, and asks for feedback.

### Behaviour

- After the update, not before: only the new binary knows its own features.
- Information only, never a prompt, so CI and scripts cannot hang.
- Stderr, and only on a terminal, like the update box in `cmd/ckb/main.go`.
- Never in `ckb mcp` or `ckb serve`: output there breaks the protocol.
- Off with `CKB_NO_NOTICE=1`.
- Fresh installs record the version silently. `~/.ckb/update-check.json` or
  `~/.ckb/ckb.db` existing marks an upgrade; older versions have no "last seen
  version" to read.
- Users who only run CKB through an MCP client never see it. One plain line in
  `ckb setup` reaches some of them. No license text goes into agent context.
- A test keeps the notice's version equal to `internal/version/version.go`, so
  a release cannot ship last release's news.

### Text

```text
CKB 9.4.0 is here. Thanks for updating!

What's new
  • Done check: `ckb changes --check` lists every use of a changed
    declaration your diff left untouched
  • findReferences over MCP is far smaller: grouped by file by default
  • Upgrades no longer need a fresh .ckb

CKB is built by one developer, and I want it to stay free for the people
who make software on their own or are just getting started. It's free for
personal use, open source projects, and organizations under $25k annual
revenue.

If your organization is above that, please get a license. It's what keeps
CKB going, and I appreciate it: https://codeknowledge.dev/pricing

I'd love to hear from you, good or bad:
  GitHub   https://github.com/SimplyLiz/ckb/discussions

(Shown once per version. Silence it with CKB_NO_NOTICE=1)
```

The wording has to match `LICENSE`: free use is not limited to private
individuals.

### Decided

- Discord does not exist yet. Until there is a permanent invite link, the line
  stays out and only GitHub Discussions is linked.
- Solo freelancers count as organizations: free under $25k annual revenue, a
  license above it. `LICENSE` still says only "personal, non-commercial" and
  "organizations", so it needs a sentence saying so. Not written yet.

## Opt-in usage reporting

Wanted because the tool is otherwise blind: the activity ledger is local, so
downloads say nothing about which tools get used.

- Off by default. Asked once in `ckb setup`, never during MCP.
- Sent: version, OS, preset, call counts per tool. Not sent: paths, symbols,
  parameters, repo names.
- Never used for license enforcement, and the prompt says so.
- Named `usageReporting`: `telemetry` already means the OpenTelemetry feature.
- Needs a collection endpoint, a privacy notice and a decision on IP addresses,
  with a legal check before launch.
- Cheaper first step: `ckb activity --share` prints an anonymous summary that
  users post themselves. No infrastructure.

Opt-in data over-represents friendly users. It still answers whether anyone
uses a given tool.
