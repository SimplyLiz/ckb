## CKB Review: 🟡 WARN — 68/100

**25 files** (+480 changes) · **3 modules** · `Go` `TypeScript`
**22 reviewable** · 3 generated (excluded) · **2 safety-critical**

> Changes 25 files across 3 modules (Go, TypeScript). 2 breaking API changes detected; 2 safety-critical files changed. 2 safety-critical files need focused review.

| Check | Status | Detail |
|-------|--------|--------|
| breaking | 🔴 FAIL | 2 breaking API changes detected |
| critical | 🔴 FAIL | 2 safety-critical files changed |
| complexity | 🟡 WARN | +8 cyclomatic (engine.go) |
| coupling | 🟡 WARN | 2 missing co-change files |
| secrets | ✅ PASS | No secrets detected |
| tests | ✅ PASS | 12 tests cover the changes |
| risk | ✅ PASS | Risk score: 0.42 (low) |
| hotspots | ✅ PASS | No volatile files touched |
| generated | ℹ️ INFO | 3 generated files detected and excluded |

### Top Risks

- 2 breaking API changes
- Critical path touched

<details><summary>Findings (7 actionable, 1 informational)</summary>

| Severity | File | Finding |
|----------|------|---------|
| 🔴 | `api/handler.go:42` | Removed public function HandleAuth() |
| 🔴 | `api/middleware.go:15` | Changed signature of ValidateToken() |
| 🔴 | `drivers/hw/plc_comm.go:78` | Safety-critical path changed (pattern: drivers/**) |
| 🔴 | `protocol/modbus.go` | Safety-critical path changed (pattern: protocol/**) |
| 🟡 | `internal/query/engine.go:155` | Complexity 12→20 in parseQuery() |
| 🟡 | `internal/query/engine.go` | Missing co-change: engine_test.go (87% co-change rate) |
| 🟡 | `protocol/modbus.go` | Missing co-change: modbus_test.go (91% co-change rate) |

</details>

<details><summary>Change Breakdown</summary>

| Category | Files | Review Priority |
|----------|-------|-----------------|
| generated | 3 | ⚪ Skip (review source) |
| modified | 10 | 🟡 Standard review |
| new | 5 | 🔴 Full review |
| refactoring | 3 | 🟡 Verify correctness |
| test | 4 | 🟡 Verify coverage |

</details>

<details><summary>✂️ Suggested PR Split (3 clusters)</summary>

| Cluster | Files | Changes | Independent |
|---------|-------|---------|-------------|
| API Handler Refactor | 8 | +240 −120 | ✅ |
| Protocol Update | 5 | +130 −60 | ✅ |
| Driver Changes | 12 | +80 −30 | ❌ |

</details>

<details><summary>Code Health — 2 degraded</summary>

**Degraded:**

| File | Before | After | Delta | Grade | Confidence |
|------|--------|-------|-------|-------|------------|
| `api/handler.go` | 82 | 70 | -12 | B→B | 100% |
| `internal/query/engine.go` | 75 | 68 | -7 | B→C | 80% |

**Improved:** 1 file(s)

2 degraded · 1 improved · avg -4.7

</details>

**Estimated review:** ~95min (complex)

**Reviewers** (has lines in % of changed files): @alice 85% · @bob 45%

<!-- ckb-review-marker -->
