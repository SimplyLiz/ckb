# CKB — Code Knowledge Backend

**Know your code. Change it safely. Ship with confidence.**

[![npm version](https://img.shields.io/npm/v/@tastehub/ckb.svg)](https://www.npmjs.com/package/@tastehub/ckb)
[![Documentation](https://img.shields.io/badge/docs-wiki-blue.svg)](https://github.com/SimplyLiz/CodeMCP/wiki)

CKB transforms your codebase into a queryable knowledge base. Ask questions, understand impact, find owners, detect dead code—all through CLI, API, or AI assistants.

> Think of it as a senior engineer who knows every line of code, every decision, and every owner—available 24/7 to answer your questions.

---

## Instant Answers to Hard Questions

| Question | Without CKB | With CKB |
|----------|-------------|----------|
| "What breaks if I change this?" | Grep and hope | Precise blast radius with risk score |
| "Who should review this PR?" | Guess from git blame | Data-driven reviewer suggestions |
| "Is this code still used?" | Delete and see what breaks | Confidence-scored dead code detection |
| "What tests should I run?" | Run everything (30 min) | Run affected tests only (2 min) |
| "How does this system work?" | Read code for hours | Query architecture instantly |
| "Who owns this code?" | Search CODEOWNERS manually | Ownership with drift detection |
| "Are there exposed secrets?" | Manual grep for patterns | Automated scanning with 26 patterns |

---

## What You Can Do

🔍 **Understand** — Semantic search, call graphs, usage tracing, architecture maps

⚡ **Analyze** — Impact analysis, risk scoring, hotspot detection, coupling analysis

🛡️ **Protect** — Affected test detection, breaking change warnings, PR risk assessment

🔐 **Secure** — Secret detection, credential scanning, security-sensitive code identification

👥 **Collaborate** — Ownership lookup, reviewer suggestions, architectural decisions (ADRs)

📊 **Improve** — Dead code detection, tech debt tracking, documentation coverage

🚀 **Compound Operations** — Single-call tools (`explore`, `understand`, `prepareChange`) reduce AI tool calls by 60-70%

🔗 **Integrate** — CLI, HTTP API, MCP for AI tools, CI/CD pipelines, custom scripts

---

## Try It Now

```bash
# See what's risky in your codebase
ckb hotspots

# Check impact before changing code
ckb impact diff

# Find tests to run for your changes
ckb affected-tests --output=command

# Scan for exposed secrets
ckb scan-secrets

# Get reviewers for your PR
ckb reviewers

# Check architecture at a glance
ckb arch
```

---

## Works Everywhere

| AI Assistants | CI/CD | Your Tools |
|---------------|-------|------------|
| Claude Code, Cursor, Windsurf, VS Code | GitHub Actions, GitLab CI | CLI, HTTP API, Scripts |

**83% token reduction** with smart presets—load only the tools you need.

```bash
# One command to connect to Claude Code
ckb setup
```

> **Building your own tools?** Use CKB as a backend via CLI, HTTP API, or MCP. See the **[Integration Guide](https://github.com/SimplyLiz/CodeMCP/wiki/Integration-Guide)** for examples in Node.js, Python, Go, and shell scripts.

---

## Learn More

| Resource | Description |
|----------|-------------|
| 📖 **[Features Guide](https://github.com/SimplyLiz/CodeMCP/wiki/Features)** | Complete feature list with examples |
| 💬 **[Prompt Cookbook](https://github.com/SimplyLiz/CodeMCP/wiki/Prompt-Cookbook)** | Real prompts for real problems |
| 🔌 **[Integration Guide](https://github.com/SimplyLiz/CodeMCP/wiki/Integration-Guide)** | Use CKB in your own tools and scripts |
| ⚡ **[Impact Analysis](https://github.com/SimplyLiz/CodeMCP/wiki/Impact-Analysis)** | Blast radius, affected tests, PR risk |
| 🔧 **[CI/CD Integration](https://github.com/SimplyLiz/CodeMCP/wiki/CI-CD-Integration)** | GitHub Actions, GitLab CI templates |

---

## Quick Start

### Option 1: npm (Recommended)

```bash
# Install globally
npm install -g @tastehub/ckb

# Or run directly with npx (no install needed)
npx @tastehub/ckb init
```

### Option 2: Build from Source

```bash
git clone https://github.com/SimplyLiz/CodeMCP.git
cd CodeMCP
go build -o ckb ./cmd/ckb
```

### Setup

```bash
# 1. Initialize in your project
cd /path/to/your/project
ckb init   # or: npx @tastehub/ckb init

# 2. Generate SCIP index (optional but recommended)
ckb index  # auto-detects language and runs appropriate indexer

# 3. Connect to Claude Code
ckb setup  # creates .mcp.json automatically

# Or manually:
claude mcp add --transport stdio ckb -- npx @tastehub/ckb mcp
```

**Token efficiency shown at startup:**
```
CKB MCP Server v8.0.0
  Active tools: 14 / 76 (18%)
  Estimated context: ~1k tokens
  Preset: core
```

Now Claude can answer questions like:
- *"What calls the HandleRequest function?"*
- *"How is ProcessPayment reached from the API?"*
- *"What's the blast radius if I change UserService?"*
- *"Who owns the internal/api module?"*
- *"Is this legacy code still used?"*

## Why CKB?

| Without CKB | With CKB |
|-------------|----------|
| AI greps for patterns | AI navigates semantically |
| "I found 47 matches for Handler" | "HandleRequest is called by 3 routes via CheckoutService" |
| Guessing at impact | Knowing the blast radius with risk scores |
| Reading entire files for context | Getting exactly what's relevant |
| "Who owns this?" → search CODEOWNERS | Instant ownership with reviewer suggestions |
| "Is this safe to change?" → hope | Hotspot trends + impact analysis |

## Three Ways to Use It

| Interface | Best For |
|-----------|----------|
| **[MCP](https://github.com/SimplyLiz/CodeMCP/wiki/MCP-Integration)** | AI-assisted development — Claude, Cursor, Windsurf, VS Code, OpenCode |
| **[CLI](https://github.com/SimplyLiz/CodeMCP/wiki/User-Guide)** | Quick lookups from terminal, scripting |
| **[HTTP API](https://github.com/SimplyLiz/CodeMCP/wiki/API-Reference)** | IDE plugins, CI integration, custom tooling |

## How Indexing Works

CKB uses **SCIP indexes** to understand your code. Think of it like a database that knows where every function is defined, who calls it, and how everything connects.

### The Basics

```bash
# 1. Generate an index (auto-detects language)
ckb index

# 2. Check if your index is fresh
ckb status
```

Without an index, CKB still works using tree-sitter parsing (basic mode), but with an index you get:
- Cross-file references ("who calls this function?")
- Precise impact analysis
- Call graph navigation

### Language Support

Not all languages are equal. CKB classifies languages into **quality tiers** based on indexer maturity:

| Tier | Quality | Languages |
|------|---------|-----------|
| **Tier 1** | Full support, all features | Go |
| **Tier 2** | Full support, minor edge cases | TypeScript, JavaScript, Python |
| **Tier 3** | Basic support, call graph may be incomplete | Rust, Java, Kotlin, C++, Ruby, Dart |
| **Tier 4** | Experimental | C#, PHP |

**Key limitations:**
- **Incremental indexing** is Go-only. Other languages require full reindex.
- **TypeScript monorepos** may need `--infer-tsconfig` flag
- **C/C++** requires `compile_commands.json`
- **Python** works best with activated virtual environment

Run `ckb doctor --tier standard` to check if your language tools are properly installed.

See **[Language Support](https://github.com/SimplyLiz/CodeMCP/wiki/Language-Support)** for indexer installation, known issues, and the full feature matrix.

### Keeping Your Index Fresh

Your index becomes stale when you make commits. CKB offers several ways to stay current:

| Method | Command | When to Use |
|--------|---------|-------------|
| Manual | `ckb index` | One-off updates, scripts |
| Watch mode | `ckb index --watch` | Auto-refresh during development |
| MCP watch | `ckb mcp --watch` | Auto-refresh in AI sessions |
| CI webhook | `POST /api/v1/refresh` | Trigger from CI/CD |

**Quick start for AI sessions:**
```bash
ckb mcp --watch  # Auto-reindexes every 30s when stale
```

**Check staleness:**
```bash
ckb status
# Shows: "5 commits behind HEAD" or "Up to date"
```

For Go projects, CKB uses **incremental indexing**—only changed files are processed, making updates fast.

See the **[Index Management Guide](https://github.com/SimplyLiz/CodeMCP/wiki/Index-Management)** for complete documentation.

## Features

| Feature | Description |
|---------|-------------|
| [**Compound Operations**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#compound-operations) | `explore`, `understand`, `prepareChange` — single-call tools that reduce AI overhead by 60-70% |
| [**Code Navigation**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#code-navigation--discovery) | Semantic search, call graphs, trace usage, find entrypoints |
| [**Impact Analysis**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#impact-analysis--safety) | Blast radius, risk scoring, affected tests, breaking changes (`compareAPI`) |
| [**Architecture**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#architectural-understanding) | Module overview, ADRs, dependency graphs, explain origin |
| [**Ownership**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#ownership--review) | CODEOWNERS + git blame, reviewer suggestions, drift detection |
| [**Code Quality**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#code-quality--risk) | Dead code detection (`findDeadCode`), coupling analysis, complexity |
| [**Security**](https://github.com/SimplyLiz/CodeMCP/wiki/Security) | Secret detection, credential scanning, allowlists |
| [**Documentation**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#documentation-intelligence) | Doc-symbol linking, staleness detection, coverage metrics |
| [**Multi-Repo**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#multi-repo--federation) | Federation, API contracts, remote index serving |
| [**Runtime**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#runtime-intelligence) | OpenTelemetry integration, observed usage, production dead code |
| [**Streaming**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#streaming) | SSE streaming for `findReferences`, `searchSymbols` with real-time progress |
| [**Automation**](https://github.com/SimplyLiz/CodeMCP/wiki/Features#automation--cicd) | Daemon mode, watch mode, webhooks, incremental indexing |

📖 **[Full Features Guide](https://github.com/SimplyLiz/CodeMCP/wiki/Features)** — Detailed documentation with examples

📋 **[Changelog](https://github.com/SimplyLiz/CodeMCP/blob/main/CHANGELOG.md)** — Version history

## CLI

```bash
ckb status          # System health (with remediation suggestions)
ckb search Handler  # Find symbols
ckb impact diff     # Analyze changes
ckb affected-tests  # Tests to run
ckb hotspots        # Risky areas
ckb arch            # Architecture overview
ckb reviewers       # PR reviewers
ckb mcp             # Start MCP server
```

**v8.0 Compound Operations (via MCP):**
```bash
# These tools combine multiple queries into single calls
explore      # Area exploration: symbols, dependencies, hotspots
understand   # Symbol deep-dive: refs, callers, explanation
prepareChange # Pre-change analysis: impact, tests, risk
batchGet     # Fetch up to 50 symbols at once
batchSearch  # Run up to 10 searches at once
```

📖 **[User Guide](https://github.com/SimplyLiz/CodeMCP/wiki/User-Guide)** — All CLI commands and options

## HTTP API

```bash
# Start the HTTP server
ckb serve --port 8080

# Example calls
curl http://localhost:8080/health
curl http://localhost:8080/status
curl "http://localhost:8080/search?q=NewServer"
curl http://localhost:8080/architecture
curl "http://localhost:8080/ownership?path=internal/api"
curl http://localhost:8080/hotspots

# Index Server Mode (v7.3) - serve indexes to remote clients
ckb serve --port 8080 --index-server --index-config config.toml

# Index server endpoints
curl http://localhost:8080/index/repos
curl http://localhost:8080/index/repos/company%2Fcore-lib/meta
curl "http://localhost:8080/index/repos/company%2Fcore-lib/symbols?limit=100"
curl "http://localhost:8080/index/repos/company%2Fcore-lib/search/symbols?q=Handler"

# Upload endpoints (with compression + auth)
curl -X POST http://localhost:8080/index/repos \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ckb_xxx" \
  -d '{"id":"my-org/my-repo","name":"My Repo"}'

gzip -c index.scip | curl -X POST http://localhost:8080/index/repos/my-org%2Fmy-repo/upload \
  -H "Content-Encoding: gzip" \
  -H "Authorization: Bearer ckb_xxx" \
  --data-binary @-

# Token management (index server admin)
ckb token create --name "ci-upload" --scope upload    # Create API key
ckb token list                                         # List all tokens
ckb token revoke ckb_xxx                              # Revoke a token
ckb token rotate ckb_xxx                              # Rotate (new secret, same ID)
```

## MCP Integration

CKB works with any MCP-compatible AI coding tool.

<details>
<summary><strong>Claude Code</strong></summary>

```bash
# Auto-configure for current project
npx @tastehub/ckb setup

# Or add globally for all projects
npx @tastehub/ckb setup --global
```

Or manually add to `.mcp.json`:
```json
{
  "mcpServers": {
    "ckb": {
      "command": "npx",
      "args": ["@tastehub/ckb", "mcp"]
    }
  }
}
```

</details>

<details>
<summary><strong>Cursor</strong></summary>

Add to `~/.cursor/mcp.json` (global) or `.cursor/mcp.json` (project):
```json
{
  "mcpServers": {
    "ckb": {
      "command": "npx",
      "args": ["@tastehub/ckb", "mcp"]
    }
  }
}
```

</details>

<details>
<summary><strong>Windsurf</strong></summary>

Add to `~/.codeium/windsurf/mcp_config.json`:
```json
{
  "mcpServers": {
    "ckb": {
      "command": "npx",
      "args": ["@tastehub/ckb", "mcp"]
    }
  }
}
```

</details>

<details>
<summary><strong>VS Code</strong></summary>

Add to your VS Code `settings.json`:
```json
{
  "mcp": {
    "servers": {
      "ckb": {
        "type": "stdio",
        "command": "npx",
        "args": ["@tastehub/ckb", "mcp"]
      }
    }
  }
}
```

</details>

<details>
<summary><strong>OpenCode</strong></summary>

Add to `opencode.json` in project root:
```json
{
  "mcp": {
    "ckb": {
      "type": "local",
      "command": ["npx", "@tastehub/ckb", "mcp"],
      "enabled": true
    }
  }
}
```

</details>

<details>
<summary><strong>Claude Desktop</strong></summary>

Claude Desktop doesn't have a project context, so you must specify the repository path.

**Automatic setup** (recommended):
```bash
cd /path/to/your/repo
ckb setup --tool=claude-desktop
```

**Manual configuration** — add to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):
```json
{
  "mcpServers": {
    "ckb": {
      "command": "npx",
      "args": ["-y", "@tastehub/ckb", "mcp"],
      "env": {
        "CKB_REPO": "/path/to/your/repo"
      }
    }
  }
}
```

The `CKB_REPO` environment variable tells CKB which repository to analyze. Claude Desktop can only work with one repository at a time.

</details>

<details>
<summary><strong>Windows</strong></summary>

Use `cmd /c` wrapper in any config above:
```json
{
  "mcpServers": {
    "ckb": {
      "command": "cmd",
      "args": ["/c", "npx", "@tastehub/ckb", "mcp"]
    }
  }
}
```

</details>

<details>
<summary><strong>Presets (Token Optimization)</strong></summary>

CKB exposes 110 tools, but most sessions only need a subset. Use presets to reduce token overhead by up to 83%:

```bash
# List all available presets with tool counts and token estimates
ckb mcp --list-presets

# Default: core preset (14 essential tools)
ckb mcp

# Workflow-specific presets
ckb mcp --preset=core        # 14 tools - search, explain, impact (default)
ckb mcp --preset=review      # 19 tools - core + diff, ownership
ckb mcp --preset=refactor    # 19 tools - core + coupling, dead code
ckb mcp --preset=federation  # 28 tools - core + cross-repo
ckb mcp --preset=docs        # 20 tools - core + doc-symbol linking
ckb mcp --preset=ops         # 25 tools - core + jobs, webhooks, metrics
ckb mcp --preset=full        # 110 tools - all tools (legacy)
```

In MCP config:
```json
{
  "mcpServers": {
    "ckb": {
      "command": "npx",
      "args": ["@tastehub/ckb", "mcp", "--preset=review"]
    }
  }
}
```

The AI can dynamically expand the toolset mid-session using the `expandToolset` tool.

</details>

## Under the Hood

CKB orchestrates multiple code intelligence backends:

- **SCIP** — Precise, pre-indexed symbol data (fastest)
- **LSP** — Real-time language server queries
- **Git** — Blame, history, churn analysis, ownership

Results are merged intelligently and compressed for LLM context limits.

Persistent knowledge survives across sessions:
- **Module Registry** — Boundaries, responsibilities, tags
- **Ownership Registry** — CODEOWNERS + git-blame with time decay
- **Hotspot Tracker** — Historical snapshots with trend analysis
- **Decision Log** — ADRs with full-text search

## Who Should Use CKB?

- **Developers using AI assistants** — Give your AI tools superpowers
- **Teams with large codebases** — Navigate complexity efficiently
- **Anyone doing refactoring** — Understand impact before changing
- **Code reviewers** — See the full picture of changes
- **Tech leads** — Track architectural health over time

## Limitations (Honest Take)

**CKB excels at:**
- Static code navigation—finding definitions, references, call graphs
- Impact analysis for safe refactoring
- Ownership lookup (CODEOWNERS + git blame)
- Architecture and module understanding

**CKB won't help with:**
- Dynamic dispatch / runtime behavior (use debugger)
- Generated code that isn't indexed
- Code generation, linting, or formatting
- Cross-repo calls (use [federation](https://github.com/SimplyLiz/CodeMCP/wiki/Federation) for this)

> CKB is static analysis, not magic. Always verify critical decisions by reading the actual code.

📖 **[Practical Limits](https://github.com/SimplyLiz/CodeMCP/wiki/Practical-Limits)** — Full guide on accuracy, blind spots, and when to trust results

## Documentation

See the **[Full Documentation Wiki](https://github.com/SimplyLiz/CodeMCP/wiki)** for:

- [Quick Start](https://github.com/SimplyLiz/CodeMCP/wiki/Quick-Start) — Step-by-step installation
- [Prompt Cookbook](https://github.com/SimplyLiz/CodeMCP/wiki/Prompt-Cookbook) — Real prompts for real problems
- [Language Support](https://github.com/SimplyLiz/CodeMCP/wiki/Language-Support) — SCIP indexers and support tiers
- [Practical Limits](https://github.com/SimplyLiz/CodeMCP/wiki/Practical-Limits) — Accuracy notes, blind spots
- [User Guide](https://github.com/SimplyLiz/CodeMCP/wiki/User-Guide) — CLI commands and best practices
- [Index Management](https://github.com/SimplyLiz/CodeMCP/wiki/Index-Management) — How indexing works, auto-refresh methods
- [Incremental Indexing](https://github.com/SimplyLiz/CodeMCP/wiki/Incremental-Indexing) — Fast index updates for Go projects
- [Doc-Symbol Linking](https://github.com/SimplyLiz/CodeMCP/wiki/Doc-Symbol-Linking) — Symbol detection in docs, staleness checking
- [Authentication](https://github.com/SimplyLiz/CodeMCP/wiki/Authentication) — API tokens, scopes, rate limiting
- [MCP Integration](https://github.com/SimplyLiz/CodeMCP/wiki/MCP-Integration) — Claude Code setup, 110 tools
- [API Reference](https://github.com/SimplyLiz/CodeMCP/wiki/API-Reference) — HTTP API documentation
- [Daemon Mode](https://github.com/SimplyLiz/CodeMCP/wiki/Daemon-Mode) — Always-on service with scheduler, webhooks
- [Configuration](https://github.com/SimplyLiz/CodeMCP/wiki/Configuration) — All options including MODULES.toml
- [Architecture](https://github.com/SimplyLiz/CodeMCP/wiki/Architecture) — System design and components
- [Security](https://github.com/SimplyLiz/CodeMCP/wiki/Security) — Secret detection, credential scanning
- [Telemetry](https://github.com/SimplyLiz/CodeMCP/wiki/Telemetry) — Runtime observability, dead code detection
- [Federation](https://github.com/SimplyLiz/CodeMCP/wiki/Federation) — Cross-repository queries
- [CI/CD Integration](https://github.com/SimplyLiz/CodeMCP/wiki/CI-CD-Integration) — GitHub Actions, PR analysis

## Requirements

**Using npm (recommended):**
- Node.js 16+
- Git

**Building from source:**
- Go 1.21+
- Git

**Optional (for enhanced analysis):**
- SCIP indexer for your language (scip-go, scip-typescript, etc.) — run `ckb index` to auto-install

## License

Free for personal use. Commercial/enterprise use requires a license. See [LICENSE](LICENSE) for details.
