---
description: "Compliance audit against one or more regulatory frameworks: CKB's deterministic checks first, then triage findings for applicability and false positives."
argument-hint: "[framework[,framework...] | all]"
---

Run a CKB-augmented compliance audit optimized for minimal token usage.

## Input
$ARGUMENTS - Optional: framework(s) to audit (default: auto-detect from repo context). Examples: "gdpr", "gdpr,pci-dss,hipaa", "all"

## Running CKB

The commands below call `ckb`. If it is not on PATH, run them through npx instead,
replacing `ckb` with `npx -y @tastehub/ckb`.

## Philosophy

CKB already ran deterministic checks across 20 regulatory frameworks, mapped every finding
to a specific regulation article, and assigned confidence scores. The LLM's job is ONLY what
CKB can't do: assess whether findings are real compliance risks or false positives given the
repo's actual purpose, and prioritize remediation by business impact.

### Available frameworks (20 total)

**Privacy:** gdpr, ccpa, iso27701
**AI:** eu-ai-act
**Security:** iso27001, nist-800-53, owasp-asvs, soc2, hipaa
**Industry:** pci-dss, dora, nis2, fda-21cfr11, eu-cra
**Supply chain:** sbom-slsa
**Safety:** iec61508, iso26262, do-178c
**Coding:** misra, iec62443

### CKB's blind spots (what the LLM must catch)

CKB maps code patterns to regulation articles using AST + regex + tree-sitter. It is
structurally correct but contextually blind:

- **Business context**: CKB flags PII patterns in a healthcare app and a game engine equally
- **Architecture awareness**: a finding in dead/test code vs production code has different weight
- **Compensating controls**: CKB can't see infrastructure-level encryption, WAFs, or IAM policies
- **Regulatory applicability**: CKB flags HIPAA in a repo that doesn't handle PHI
- **Risk prioritization**: 50 findings need ordering by actual business/legal exposure
- **Cross-reference noise**: the same hardcoded credential maps to 6 frameworks — that's 1 fix, not 6

## Phase 1: Structural scan (~2k tokens into context)

```bash
ckb audit compliance --framework=$ARGUMENTS --format=json --min-confidence=0.7 2>/dev/null
```

For large repos, scope to a specific path to reduce noise:
```bash
ckb audit compliance --framework=$ARGUMENTS --scope=src/api --format=json --min-confidence=0.7 2>/dev/null
```

If no framework specified, pick based on repo context:
- Has health/patient/medical code — hipaa,gdpr
- Has payment/billing/card code — pci-dss,soc2
- EU company or processes EU data — gdpr,dora,nis2
- AI/ML code — eu-ai-act
- Safety-critical/embedded — iec61508,iso26262,misra
- General SaaS — iso27001,soc2,owasp-asvs
- If unsure — iso27001,owasp-asvs (broadest applicability)

From the JSON output, extract:
- `score`, `verdict` (pass/warn/fail)
- `coverage[]` — per-framework scores with passed/warned/failed/skipped check counts
- `findings[]` — with check, severity, file, startLine, message, suggestion, confidence, CWE
- `checks[]` — per-check status and summary
- `summary` — total findings by severity, files scanned

Note:
- **Per-framework scores**: which frameworks are clean vs problematic
- **Finding count by severity**: errors are your priority
- **CWE references**: cross-reference with known vulnerability databases
- **Confidence scores**: low confidence (< 0.7) findings are likely false positives

**Early exit**: If verdict=pass and all framework scores >= 90, write a one-line summary and stop.

## Phase 2: Triage findings (targeted reads only)

Do NOT read every flagged file. Group findings by root cause first:

1. **Deduplicate cross-framework findings** — a hardcoded secret flagged by GDPR, PCI DSS, HIPAA, and ISO 27001 is one fix
2. **Check for dominant category** — if > 50% of findings are one category (e.g., "sql-injection"), investigate that category systemically rather than checking each file individually
3. **Check applicability** — does this repo actually fall under the flagged framework? (e.g., HIPAA findings in a non-healthcare repo)
4. **Read only error-severity files** — warnings and info can wait
5. **For each error finding**, read just the flagged lines (not the whole file) and assess:
   - Is this a real compliance risk or a pattern false positive?
   - Are there compensating controls elsewhere? (check imports, config, middleware)
   - What's the remediation effort: one-liner fix vs architectural change?

## Phase 3: Write the audit summary (be terse)

```markdown
## [COMPLIANT|NEEDS REMEDIATION|NON-COMPLIANT] — CKB score: [N]/100

[One sentence: what frameworks were audited and overall posture]

### Critical findings (must remediate)
1. **[framework]** `file:line` Art. [X] — [issue + remediation in one sentence]
2. ...

### Not applicable (false positives from context)
[List findings CKB flagged but that don't apply to this repo, with one-line reason]

### Cross-framework deduplication
[N findings deduplicated to M root causes]

### Framework scores
| Framework | Score | Status | Checks |
|-----------|-------|--------|--------|
| [name]    | [N]   | [pass/warn/fail] | [passed]/[total] |
```

If fully compliant: just the header + framework scores. Nothing else.

## Anti-patterns (token waste)

- Reading every flagged file — waste (group by root cause, read only errors)
- Treating cross-framework duplicates as separate issues — waste (1 code fix = 1 issue)
- Explaining what each regulation requires — waste (CKB already mapped articles)
- Re-checking frameworks CKB scored at 100 — waste
- Auditing frameworks that don't apply to this repo — waste
- Reading low-confidence findings (< 0.7) — waste (likely false positives)
- Suggesting infrastructure controls for code-level findings — out of scope
- Using wrong framework IDs (use pci-dss not pcidss, owasp-asvs not owaspasvs) — CKB error
