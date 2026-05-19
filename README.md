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

Squadron is a **declarative agent framework** for building and running multi-agent AI workflows. You describe agents, tools, models, and the task graph in HCL configuration — Squadron's runtime handles orchestration, state, dependency resolution, conditional routing, persistence, and resume. No Python glue code, no LangChain-style call chains, no hand-rolled state machines.

It ships as a single Go binary. MIT-licensed. Supports Anthropic, OpenAI, Google Gemini, and Ollama, and speaks [Model Context Protocol](https://modelcontextprotocol.io) in both directions.

```hcl
mission "research" {
  commander { model = models.anthropic.claude_sonnet_4 }
  agents    = [agents.researcher, agents.analyst]

  task "gather" {
    objective = "Find the top 5 papers on ${inputs.topic}"
    agents    = [agents.researcher]
  }

  task "analyze" {
    depends_on = [tasks.gather]
    objective  = "Read each paper and extract the key findings"
    agents     = [agents.analyst]

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
squadron mission research -c ./config --topic "post-quantum cryptography"
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

# Set an API key (stored in an encrypted vault)
squadron vars set anthropic_api_key sk-ant-...

# Launch the command center
squadron engage -w
```

Full walkthrough: [Quick Start](https://docs.squadron.sh/getting-started/quickstart).

## Why Squadron?

**Workflows as config, not code.** Your entire mission graph — agents, tools, dependencies, branching, schedules, budgets — lives in one HCL file you can diff in a pull request. No imperative orchestration buried across Python modules.

**Resilient by default.** Squadron persists every commander session, agent session, route decision, and task output to SQLite (or Postgres) as the mission runs. `squadron mission --resume <id>` picks up from the last completed step, including mid-flight tool calls.

**Plugins in two languages.** Squadron's [plugin system](https://docs.squadron.sh/config/plugins) runs **Go or Python** plugins as gRPC subprocesses via [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin). Auto-built from local source with content-hash caching. Process-isolated, stateful across tasks (a Playwright plugin opens a browser once and reuses it across every task in every mission), and distributable via GitHub releases.

**MCP both directions.** Squadron [consumes any MCP server](https://docs.squadron.sh/config/mcp_tools) (npm packages, GitHub release binaries, HTTP, or local stdio) — auto-install handled. Squadron itself can also [run as an MCP server](https://docs.squadron.sh/config/mcp_host) so Claude Desktop, Claude Code, and Cursor can browse your missions and trigger runs.

**Mix model providers per task.** Use Claude Sonnet for orchestration, GPT-4 for the hard subtask, a local Llama for the privacy-sensitive step, Gemini for vision. Declare each `model` block once and reference per agent.

**Scheduled, webhook-triggered, budgeted.** Each mission can declare a [`schedule` block](https://docs.squadron.sh/missions/schedules) (cron / daily / interval with timezone + weekday filters), a `trigger` block (webhook), and a [`budget` block](https://docs.squadron.sh/missions/budgets) that halts the run when token or dollar caps are reached.

**Web command center.** `squadron engage -w` launches a local web UI for live mission graph visualization, run history, log streaming, and webhook routing. Or connect multiple Squadron instances to a remote command center.

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
- **[Missions](https://docs.squadron.sh/missions/overview)** — task DAGs with `depends_on`, conditional [`router`](https://docs.squadron.sh/missions/routing), unconditional `send_to`, parallel/sequential [iteration over datasets](https://docs.squadron.sh/missions/iteration), and cross-mission routing.
- **[Plugins](https://docs.squadron.sh/config/plugins)** — Go and Python, gRPC subprocess, auto-build, content-hash caching, distributable via GitHub releases.
- **[MCP tools](https://docs.squadron.sh/config/mcp_tools)** — declare any MCP server (npm, GitHub, HTTP, or local) and Squadron auto-installs and proxies tools to agents. OAuth login flow for hosted servers (`squadron mcp login`).
- **[MCP host](https://docs.squadron.sh/config/mcp_host)** — run Squadron as an MCP server for Claude Desktop, Claude Code, Cursor, and other MCP clients.
- **[Schedules & triggers](https://docs.squadron.sh/missions/schedules)** — cron-based or HTTP-triggered missions, with per-mission concurrency limits.
- **[Budgets](https://docs.squadron.sh/missions/budgets)** — token + dollar caps per mission or per task.
- **[Memory](https://docs.squadron.sh/missions/folders)** — sandboxed filesystem locations agents can read/write, with shared, mission-scoped, and ephemeral run-scoped variants. (HCL: `shared_memory`, `memory`, `run_memory` — the old `shared_folder`/`folder`/`run_folder` spellings still work.)
- **[Gateways](https://docs.squadron.sh/config/gateways)** — managed subprocesses that bridge Squadron to Slack, Discord, Teams, or any custom system via the Gateway SDK.
- **Encrypted vault** — secrets stored at rest with AES-256-GCM + Argon2id; passphrase in the OS keychain.
- **Single binary, no runtime deps.** Docker images on every release.

## Docker

```bash
docker run -v ./config:/config -v squadron-data:/data/squadron -p 8080:8080 \
  ghcr.io/mlund01/squadron engage
```

Alpine (default) and Debian images on `ghcr.io/mlund01/squadron`. Details: [Docker docs](https://docs.squadron.sh/getting-started/docker).

## Learn more

- [What problem does Squadron solve?](https://docs.squadron.sh/) — full overview on the docs home
- [The Harness](https://docs.squadron.sh/missions/harness) — the commander/agent runtime explained
- [Declarative Agent Framework](https://docs.squadron.sh/declarative-agent-framework) — the imperative-vs-declarative case
- [No-Code Multi-Agent Walkthrough](https://docs.squadron.sh/guides/no-code-multi-agent-workflow) — daily-brief pipeline end-to-end
- [Distributing Plugins](https://docs.squadron.sh/guides/distributing-plugins) — publish your plugin as a GitHub release
- [Configuration reference](https://docs.squadron.sh/config/overview) — every block, every field
- [CLI reference](https://docs.squadron.sh/cli/quickstart) — every command, every flag
- [FAQ](https://docs.squadron.sh/faq) — pricing, license, providers, MCP, on-prem, plugins

## Build your own plugin

Plugins extend Squadron with custom tools. The [squadron-sdk](https://github.com/mlund01/squadron-sdk) provides Go and Python interfaces — auto-built on every config load with content-hash caching. See [Distributing Plugins](https://docs.squadron.sh/guides/distributing-plugins) for the publishing flow.

## Community

- File issues at [github.com/mlund01/squadron/issues](https://github.com/mlund01/squadron/issues)
- Read the docs at [docs.squadron.sh](https://docs.squadron.sh)

## License

[MIT](LICENSE)
