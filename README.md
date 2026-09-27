<div align="center">

# Squadron

**Declarative multi-agent AI workflows in HCL — not Python.**

[![Latest release](https://img.shields.io/github/v/release/mlund01/squadron?logo=github&color=22c55e)](https://github.com/mlund01/squadron/releases)
[![License](https://img.shields.io/github/license/mlund01/squadron?color=22c55e)](LICENSE)
[![Downloads](https://img.shields.io/github/downloads/mlund01/squadron/total?color=22c55e)](https://github.com/mlund01/squadron/releases)
[![Docs](https://img.shields.io/badge/docs-docs.squadron.sh-22c55e)](https://docs.squadron.sh)

[Documentation](https://docs.squadron.sh) · [Quick Start](https://docs.squadron.sh/getting-started/quickstart) · [Compare](https://docs.squadron.sh/compare/langgraph) · [FAQ](https://docs.squadron.sh/faq)

</div>

---

Squadron is a **declarative agent framework** for building and running AI workflows. You describe models, capabilities, and a mission task graph in HCL; one mission commander coordinates bounded task executors. Managed agents are a separate way to run independent sessions. Squadron's runtime handles orchestration, state, dependency resolution, conditional routing, persistence, and resume. No Python glue code, no LangChain-style call chains, no hand-rolled state machines.

It ships as a single Go binary. MIT-licensed. Supports Anthropic, OpenAI, Google Gemini, and Ollama, and speaks [Model Context Protocol](https://modelcontextprotocol.io) in both directions.

```hcl
model_provider "anthropic" {
  models = ["claude_haiku_4_5"]
}

mission "research" {
  commander { model = models.anthropic.claude_haiku_4_5 }
  models = [models.anthropic.claude_haiku_4_5]

  input "topic" { type = "string" }

  task "gather" {
    objective = "Find the top 5 papers on ${inputs.topic}"
    tools     = [builtins.http.get]
  }

  task "analyze" {
    depends_on = [tasks.gather]
    objective  = "Read each paper and extract the key findings"
    router {
      route { target = tasks.deep_dive; condition = "Findings warrant deeper investigation" }
      route { target = tasks.summarize; condition = "Findings are routine" }
    }
  }

  task "deep_dive" { objective = "Investigate the most promising lead in detail" }
  task "summarize" { objective = "Write a one-page summary" }
}
```

```bash
squadron mission research -c ./config --input topic="post-quantum cryptography"
```

That's the whole workflow. Diff it in a PR. Review it like infrastructure code.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash
```

Or grab a binary from [GitHub Releases](https://github.com/mlund01/squadron/releases). Full options in the [installation docs](https://docs.squadron.sh/getting-started/installation).

## Quick start

```bash
# Scaffold a starter project
squadron quickstart

# Connect this workspace worker to its Command Center
# (the first interactive run asks for the URL and runner credential)
squadron engage

# Add an Anthropic model provider from the workspace Settings page
```

Full walkthrough: [Quick Start](https://docs.squadron.sh/getting-started/quickstart).

## Why Squadron?

**Workflows as config, not code.** Your mission graph — executor models and tools, dependencies, branching, and budgets — lives in HCL you can diff in a pull request. Schedules are managed in the Squadron product. No imperative orchestration buried across Python modules.

**Resilient by default.** Squadron persists the mission commander, task attempts and transcripts, route decisions, and task results to SQLite (or Postgres). `squadron mission --resume <id>` reconciles the same in-flight attempt from its checkpoint; it does not blindly repeat an uncertain external side effect.

**Plugins in two languages.** Squadron's [plugin system](https://docs.squadron.sh/config/plugins) runs **Go or Python** plugins as gRPC subprocesses via [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin). Auto-built from local source with content-hash caching. Process-isolated, stateful across tasks (a Playwright plugin opens a browser once and reuses it across every task in every mission), and distributable via GitHub releases.

**MCP both directions.** Squadron [consumes any MCP server](https://docs.squadron.sh/config/mcp_tools) (npm packages, GitHub release binaries, HTTP, or local stdio) — auto-install handled. Squadron itself can also [run as an MCP server](https://docs.squadron.sh/config/mcp_host) so Claude Desktop, Claude Code, and Cursor can browse your missions and trigger runs.

**Mix model providers per task.** Use one configured model for the commander and give task executors an explicit model pool. Configure credentials and endpoints in workspace Settings, allow model keys with `model_provider` blocks, and narrow them per task when needed.

**Scheduled, webhook-triggered, budgeted.** Create [schedules](https://docs.squadron.sh/product/schedules) in the Squadron product with a permitted run-as identity. Missions can declare a `trigger` block (webhook) and a [`budget` block](https://docs.squadron.sh/config/missions/budgets) that halts a run when token or dollar caps are reached.

**Command Center.** Deploy Command Center as the workspace hub, then run `squadron engage` in the workspace to connect its runner. The first interactive run stores the Command Center URL and runner credential in `.squadron/command-center.json`; mission configuration remains separate.

## How does it compare?

| Tool | Style | Language | Best for |
|---|---|---|---|
| **Squadron** | Declarative | HCL config | Production agent pipelines, reviewable workflows, scheduled jobs |
| [LangGraph](https://docs.squadron.sh/compare/langgraph) | Imperative | Python | Custom runtime logic, deep LangChain integration |
| [CrewAI](https://docs.squadron.sh/compare/crewai) | Imperative | Python | Quick Python prototypes, role-based crews |
| [AutoGen](https://docs.squadron.sh/compare/autogen) | Conversational | Python | Multi-agent dialogue patterns, GroupChat |
| [n8n](https://docs.squadron.sh/compare/n8n) | Visual | GUI / JSON | Broad SaaS integration, non-LLM-first workflows |

Honest tradeoffs on each comparison page.

## What's in the box

- **Model providers** out of the box: [Anthropic, OpenAI, Google Gemini, Ollama](https://docs.squadron.sh/config/supported-models). Mix per agent or per task.
- **[Missions](https://docs.squadron.sh/config/missions/overview)** — task DAGs with `depends_on`, conditional [`router`](https://docs.squadron.sh/config/missions/routing), unconditional `send_to`, parallel/sequential [iteration over datasets](https://docs.squadron.sh/config/missions/iteration), and cross-mission routing.
- **[Plugins](https://docs.squadron.sh/config/plugins)** — Go and Python, gRPC subprocess, auto-build, content-hash caching, distributable via GitHub releases.
- **[MCP tools](https://docs.squadron.sh/config/mcp_tools)** — declare any MCP server (npm, GitHub, HTTP, or local) and Squadron can grant its tools to task executors or independent agents. OAuth login flow for hosted servers (`squadron mcp login`).
- **[MCP host](https://docs.squadron.sh/config/mcp_host)** — run Squadron as an MCP server for Claude Desktop, Claude Code, Cursor, and other MCP clients.
- **[Schedules & triggers](https://docs.squadron.sh/product/schedules)** — product-managed scheduled runs and configured webhooks.
- **[Budgets](https://docs.squadron.sh/config/missions/budgets)** — token + dollar caps per mission or per task.
- **[Memory & Scratchpad](https://docs.squadron.sh/config/missions/folders)** — sandboxed filesystem slots granted to task executors. Persistent memory (shared with `memory "name"` or per-mission with `memory { }`) plus per-run `scratchpad = true` opt-in. Squadron owns the paths; scratchpads auto-clean after 7 days.
- **[Gateways](https://docs.squadron.sh/config/gateways)** — managed subprocesses that bridge Squadron to Slack, Discord, Teams, or any custom system via the Gateway SDK.
- **Command Center variables** — workspace values encrypted in the control-plane database and delivered to connected workers only in memory.
- **Single binary, no runtime deps.** Docker images on every release.

## Docker

```bash
docker run -v ./config:/config -v squadron-data:/data/squadron -p 8080:8080 \
  ghcr.io/mlund01/squadron engage
```

Alpine (default) and Debian images on `ghcr.io/mlund01/squadron`. Details: [Docker docs](https://docs.squadron.sh/getting-started/docker).

## Learn more

- [What problem does Squadron solve?](https://docs.squadron.sh/) — full overview on the docs home
- [The Harness](https://docs.squadron.sh/config/missions/harness) — the commander and task-executor boundary explained
- [Declarative Agent Framework](https://docs.squadron.sh/declarative-agent-framework) — the imperative-vs-declarative case
- [No-Code Multi-Agent Walkthrough](https://docs.squadron.sh/guides/no-code-multi-agent-workflow) — daily-brief pipeline end-to-end
- [Distributing Plugins](https://docs.squadron.sh/guides/distributing-plugins) — publish your plugin as a GitHub release
- [Configuration reference](https://docs.squadron.sh/config/overview) — every block, every field
- [Migrating existing missions](https://docs.squadron.sh/guides/migrating-missions) — convert agent-based mission configuration to task executors
- [CLI reference](https://docs.squadron.sh/config/cli/quickstart) — every command, every flag
- [FAQ](https://docs.squadron.sh/faq) — pricing, license, providers, MCP, on-prem, plugins

## Build your own plugin

Plugins extend Squadron with custom tools. The [squadron-sdk](https://github.com/mlund01/squadron-sdk) provides Go and Python interfaces — auto-built on every config load with content-hash caching. See [Distributing Plugins](https://docs.squadron.sh/guides/distributing-plugins) for the publishing flow.

## Community

- File issues at [github.com/mlund01/squadron/issues](https://github.com/mlund01/squadron/issues)
- Read the docs at [docs.squadron.sh](https://docs.squadron.sh)

## License

[MIT](LICENSE)
