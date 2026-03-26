---
title: Introduction
---

# Squadron

Squadron is a framework for building AI agent workflows as configuration, not code.

Define agents, tools, and multi-step missions in HCL files. Squadron handles orchestration, state management, branching, and recovery — so you describe *what* you want done, not how to wire it together.

## Why Squadron

Most agent frameworks require writing code to orchestrate multi-agent workflows — glue code, retry logic, state serialization, branching. Squadron takes a different approach: your workflow definition *is* the configuration.

**Workflows are readable and reviewable.** A `.hcl` mission file describes the full task graph, agent assignments, branching conditions, and structured outputs. Anyone on the team can read it, diff it, and review it — no Python class hierarchies to decode.

**Intelligent branching built in.** Workflows aren't always linear — sometimes the next step depends on what the LLM found. Squadron treats conditional routing as a first-class concept, so workflows like triage, classification, and escalation are part of the config, not custom code.

**Structured data flows between tasks.** Tasks can define typed output schemas, and downstream tasks can query that data with filters, aggregations, and sorting. This means tasks don't just pass context through conversation — they produce and consume structured results that other tasks can reason over precisely.

**Built for production, not just prototyping.** Persistent state means missions survive crashes and resume from where they failed. The command center gives you a web UI with DAG visualization, live execution monitoring, and run history.

**Extensible through plugins.** Squadron ships with built-in tools for shell and HTTP, but anything beyond that is a plugin. Write plugins in any language, version them independently, and share them across projects. Custom tools let you wrap any plugin capability with a typed schema, so agents get clean interfaces instead of raw API calls.

## How It Works

Squadron separates *strategic thinking* from *tactical execution*:

- **Commanders** orchestrate each task — they plan subtasks, delegate to agents, query upstream results, and make routing decisions
- **Agents** execute the work — they call tools, process results, and report back
- **Missions** wire it all together — tasks form a dependency graph with static sequencing, conditional branching, and fan-out patterns

Different models can fill different roles. A fast model can handle routing while a capable model handles analysis.

## Quick Example

```hcl
mission "research" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.researcher]

  task "gather" {
    objective = "Research the topic and collect key findings"
    output {
      field "summary" { type = "string"; required = true }
    }
  }

  task "analyze" {
    depends_on = [tasks.gather]
    objective  = "Analyze the findings and produce a report"
  }
}
```

```bash
squadron mission research -c ./config
```

Or launch the command center to run missions from a web UI:

```bash
squadron serve -c ./config -w
```

## Features

- **[HCL configuration](/config/overview)** — declare agents, tools, and missions as version-controllable config files
- **[Multi-provider](/config/models)** — Anthropic, OpenAI, and Google Gemini, with per-agent model assignment
- **[Mission orchestration](/missions/overview)** — multi-task pipelines with dependency graphs, structured outputs, and commander-agent delegation
- **[Conditional routing](/missions/routing)** — LLM-driven branching, unconditional fan-out, and cross-mission routing
- **[Structured outputs](/missions/tasks)** — typed output schemas on tasks with downstream querying, filtering, and aggregation
- **[Datasets and iteration](/missions/datasets)** — process collections in parallel or sequentially with concurrency controls and retries
- **[Persistence and resume](/cli/mission)** — mission state is saved automatically; resume interrupted runs from where they failed
- **[Command center](/cli/serve)** — web UI for running missions, viewing DAGs, and monitoring execution in real time
- **[Plugin system](/config/plugins)** — extend agents with plugins for browser automation, APIs, and custom integrations
- **[Custom tools](/config/tools)** — wrap any plugin capability with a typed schema and description
- **[Interactive chat](/cli/chat)** — chat directly with any configured agent from the terminal
- **[Variables](/config/variables)** — manage API keys and configuration values with secret support
- **[Self-upgrade](/cli/upgrade)** — update to the latest release with a single command
- **[Docker support](/getting-started/docker)** — published Alpine and Debian images on every release, with multi-arch support
- **Single binary** — one `curl` to install, no runtime dependencies

## Next Steps

- [Installation](/getting-started/installation) — install Squadron in one command
- [Quick Start](/getting-started/quickstart) — build your first agent in 5 minutes
- [Configuration](/config/overview) — understand the config system
- [Missions](/missions/overview) — build multi-step agent workflows
