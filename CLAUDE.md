# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build
go build -o ckb ./cmd/ckb

# Run all tests
go test ./...

# Run tests for a specific package
go test ./internal/query/...

# Run a single test
go test ./internal/query/... -run TestSearchSymbols

# Run tests with verbose output
go test -v ./...

# Run with coverage
go test -cover ./...

# Lint (if golangci-lint installed)
golangci-lint run
```

## Running the Application

```bash
# Initialize CKB in a project directory
./ckb init

# Generate SCIP index (auto-detects language)
./ckb index

# Check system status
./ckb status

# Run diagnostics
./ckb doctor

# Start HTTP API server
./ckb serve --port 8080

# Start MCP server (for AI tool integration)
./ckb mcp

# Start A2A protocol server (for agent-to-agent communication)
./ckb a2a --port 8081

# Find exported symbols not reachable from entrypoints
./ckb unwired
./ckb unwired --scope internal/cost,internal/multisampling
./ckb unwired --format json

# Run PR review (21 quality checks)
./ckb review
./ckb review --base=develop --format=markdown
./ckb review --checks=breaking,secrets,health --ci

# Run compliance audit (131 checks across 20 regulatory frameworks)
./ckb audit compliance --framework=gdpr
./ckb audit compliance --framework=gdpr,iso27001,owasp-asvs
./ckb audit compliance --framework=all --min-confidence=0.7 --format=sarif
./ckb audit compliance --framework=pci-dss,hipaa --ci --fail-on=error
./ckb audit compliance --framework=iec61508 --sil-level=3

# Auto-configure AI tool integration (interactive)
./ckb setup

# Configure for specific AI tools
./ckb setup --tool=claude-code
./ckb setup --tool=cursor
./ckb setup --tool=grok
./ckb setup --tool=vscode
```

## Release Process

Releases are fully automated via `.github/workflows/release.yml`, triggered by pushing a `v*` tag.

**Steps to release:**
1. Bump version in `internal/version/version.go`, `npm/package.json`, `testdata/review/sarif.json`
2. Update `CHANGELOG.md`
3. Merge to main, tag `vX.Y.Z`, push the tag
4. The pipeline handles everything else:
   - Runs `go test -race ./...`
   - GoReleaser builds cross-platform binaries and uploads to GitHub Releases
   - Updates Homebrew tap (`SimplyLiz/homebrew-ckb`)
   - Publishes `@tastehub/ckb` + 5 platform packages to npm

**Do not manually `npm publish`** — the pipeline does it with checksummed binaries from GoReleaser.

## npm Distribution (v7.0)

CKB is also available via npm:

```bash
# Install globally
npm install -g @tastehub/ckb

# Or run directly with npx
npx @tastehub/ckb init
npx @tastehub/ckb setup
```

## MCP Integration

CKB provides 110 code intelligence tools via MCP. Supports Claude Code, Cursor, Windsurf, VS Code, OpenCode, Grok, and Claude Desktop.

```bash
# Auto-configure (interactive)
ckb setup

# Configure for specific tool
ckb setup --tool=claude-code
ckb setup --tool=cursor --global

# Or manually add to .mcp.json
claude mcp add ckb -- npx @tastehub/ckb mcp
```

`ckb setup --tool=claude-code` also installs the `/ckb-review` and `/ckb-audit` slash commands for Claude Code, which orchestrate CKB's structural analysis with LLM semantic review.

### Key MCP Tools

**Navigation:** `searchSymbols`, `getSymbol`, `findReferences`, `getCallGraph`, `traceUsage`, `listEntrypoints`

**Understanding:** `explainSymbol`, `explainFile`, `explainPath`, `getArchitecture`, `listKeyConcepts`

**Impact & Risk:** `analyzeImpact`, `justifySymbol`, `getHotspots`, `summarizeDiff`, `summarizePr`

**Ownership:** `getOwnership`, `getModuleResponsibilities`, `getOwnershipDrift`

**Decisions:** `recordDecision`, `getDecisions`, `annotateModule`

**Federation (v6.2+):** `listFederations`, `federationSearchModules`, `federationGetHotspots`

**Contracts (v6.3):** `listContracts`, `analyzeContractImpact`, `getContractDependencies`

**Telemetry (v6.4):** `getTelemetryStatus`, `getObservedUsage`, `findDeadCodeCandidates`

**Intelligence (v6.5):** `explainOrigin`, `analyzeCoupling`, `exportForLLM`, `auditRisk`

**Code Analysis (v7.6):** `findDeadCode` (static dead code detection), `findUnwiredModules` (entrypoint reachability — detects "built but never plugged in" modules), `getAffectedTests`, `compareAPI`

**Compound Operations (v8.0):** `explore`, `understand`, `prepareChange`, `batchGet`, `batchSearch`

**Streaming (v8.0):** `findReferences` and `searchSymbols` support SSE streaming with `stream: true`

**Index Management (v8.0):** `reindex` (trigger index refresh), enhanced `getStatus` with health tiers

**PR Review (v8.2):** `reviewPR` — unified review with 21 quality checks (breaking, secrets, tests, complexity, health, coupling, hotspots, risk, critical-path, traceability, independence, generated, classify, split, dead-code, unwired, test-gaps, blast-radius, comment-drift, format-consistency, bug-patterns); optional `--llm` flag for Claude-powered narrative

## A2A Integration

CKB also supports the A2A (Agent-to-Agent) protocol v0.3, enabling agent-to-agent communication over HTTP. All 110 MCP tools are exposed as A2A skills.

```bash
# Start the A2A server
ckb a2a --port 8081

# Agent card (discovery)
curl http://localhost:8081/.well-known/agent-card.json

# Health with index freshness
curl http://localhost:8081/health

# Invoke a skill
curl -X POST http://localhost:8081/message:send \
  -d '{"message":{"role":"ROLE_USER","parts":[{"text":"{\"skill\":\"getStatus\",\"params\":{}}"}]}}'
```

## Architecture Overview

CKB is a code intelligence orchestration layer with four interfaces (CLI, HTTP API, MCP, A2A) that all flow through a central query engine.

### Layer Structure

```
Interfaces (cmd/ckb/, internal/api/, internal/mcp/, internal/a2a/)
    ↓
Query Engine (internal/query/)
    ↓
Backend Orchestrator (internal/backends/orchestrator.go)
    ↓
Backends: SCIP, LSP, Git (internal/backends/{scip,lsp,git}/)
    ↓
Storage Layer (internal/storage/) - SQLite for caching and symbol mappings
```

### Key Packages

- **internal/query/**: Central query engine. `Engine` is the main entry point for all operations.
- **internal/backends/**: Backend abstraction. `Orchestrator` routes to SCIP/LSP/Git based on `QueryPolicy`.
- **internal/identity/**: Stable symbol IDs that survive refactoring. Uses fingerprinting and alias chains.
- **internal/compression/**: Response budget enforcement for LLM-optimized output.
- **internal/impact/**: Change impact analysis with visibility detection and risk scoring.
- **internal/mcp/**: Model Context Protocol server (110 tools).
- **internal/a2a/**: Agent-to-Agent protocol server (v0.3). Wraps MCP tools as A2A skills with task persistence, SSE streaming, push notifications.
- **internal/ownership/**: CODEOWNERS parsing + git-blame analysis with time decay.
- **internal/responsibilities/**: Module responsibility extraction from READMEs and code.
- **internal/hotspots/**: Churn tracking with trend analysis and projections.
- **internal/decisions/**: Architectural Decision Records (ADRs) with full-text search.
- **internal/federation/**: Cross-repository queries and unified visibility.
- **internal/daemon/**: Background service with scheduler, file watcher, webhooks.
- **internal/complexity/**: Tree-sitter based cyclomatic/cognitive complexity.
- **internal/telemetry/**: OpenTelemetry integration for runtime observability.
- **internal/audit/**: Multi-factor risk scoring (8 weighted factors).
- **internal/coupling/**: Co-change analysis from git history.
- **internal/streaming/**: SSE streaming infrastructure for long-running MCP operations.
- **internal/envelope/**: Response metadata (ConfidenceFactor, CacheInfo) for AI transparency.
- **internal/compliance/**: Regulatory compliance auditing (131 checks, 20 frameworks). Each framework is a subpackage (gdpr/, iso27001/, owaspasvs/, etc.) with checks that map findings to regulation articles.

### Data Flow

1. Request arrives via CLI/HTTP/MCP
2. Query engine checks cache (query cache → view cache → negative cache)
3. Orchestrator routes to backends via "backend ladder" (SCIP first, then LSP, then Git)
4. Results merged using configured strategy (prefer-first or union)
5. Compression applied to fit response budgets
6. Drilldown suggestions generated for truncated results

### Configuration

Runtime config stored in `.ckb/config.json`. Key settings:
- `queryPolicy.backendLadder`: Backend priority order
- `queryPolicy.mergeStrategy`: "prefer-first" or "union"
- `budget.*`: Response size limits for LLM optimization
- `cache.*`: TTL settings for each cache tier

### Symbol Identity System

Symbols get stable IDs: `ckb:<repo>:sym:<fingerprint>`

Fingerprint = hash(container + name + kind + signature)

When symbols are renamed, alias chains redirect old IDs to new ones (max depth: 3).

### Language Support

CKB works at different quality levels depending on available tooling:

- **Enhanced tier** (SCIP index): Go, TypeScript, Python, Rust, Java, Kotlin, C++, Dart, Ruby, C#
- **Basic tier** (LSP only): Any language with a language server
- **Minimal tier** (heuristics): Git-based features work for all languages

Run `ckb index` to auto-detect and run the appropriate SCIP indexer.

### Keeping Your Index Fresh (v7.5+)

CKB offers multiple ways to keep your index up-to-date:

```bash
# Manual (default)
ckb index                    # Run when needed

# Watch mode (CLI)
ckb index --watch            # Watch and auto-reindex
ckb index --watch --watch-interval 1m

# MCP watch mode
ckb mcp --watch              # Auto-reindex during IDE session
ckb mcp --watch --watch-interval 15s

# Background daemon
ckb daemon start             # Watches all registered repos

# Trigger from CI (v7.5+)
curl -X POST http://localhost:9120/api/v1/refresh
curl -X POST http://localhost:9120/api/v1/refresh -d '{"full":true}'

# Check freshness
ckb status                   # Shows commits behind, index age
```

The `getStatus` MCP tool now includes index freshness info (`index.fresh`, `index.commitsBehind`, `index.indexAge`).
