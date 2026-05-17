# Awesome-list submissions for Squadron

Ready-to-submit PR text for three actively-maintained awesome lists. Each
section has: target URL, the file to edit, the exact patch to insert, the PR
title, and a draft PR body. Copy-paste each into a fork-and-PR flow.

Target lists (in priority order):

1. **awesome-ai-agents** — broadest reach, AutoGen/CrewAI already listed
2. **awesome-mcp-clients** — high-intent audience for the MCP angle
3. **awesome-langchain** — captures "LangChain alternatives" search intent

A fourth (`awesome-llm-apps`) was evaluated and rejected — that list is a
collection of self-contained working code templates living in the repo, not
a curated index of external frameworks.

---

## 1. awesome-ai-agents

- **Repo:** https://github.com/e2b-dev/awesome-ai-agents
- **File:** `README.md`
- **Section:** `# Open-source projects` (alphabetical order)
- **Position:** Between the existing `Stackwise` and `Superagent` entries
- **Rules from the README:** *"Have anything to add? Create a pull request or fill in this form. Please keep the alphabetical order and in the correct category."*

### Patch (insert into `README.md`)

Find the line right after the `Stackwise` entry's closing `</details>` and insert:

```markdown
## [Squadron](https://github.com/mlund01/squadron)
Declarative multi-agent AI workflow framework in HCL

<details>

### Category
Build-your-own, SDK for agents, Multi-agent, Declarative

### Description
- Declarative framework for multi-agent AI workflows: agents, tools, models, missions, schedules, budgets, and routing are defined entirely in HCL configuration — no Python glue code.
- Ships as a single Go binary (MIT license). Supports Anthropic, OpenAI, Google Gemini, and Ollama; models can be mixed per agent or per task in the same mission.
- Two-tier execution: **commanders** orchestrate (delegate, decide branches, query past tasks) and **agents** execute (native function calling, structured outputs). A deterministic runtime — "the harness" — handles state, dependency resolution, conditional routing, persistence, and resume after crashes.
- First-class plugin system in **Go and Python**: plugins run as gRPC subprocesses (process-isolated, stateful across tasks), auto-built from local source with content-hash caching, distributable via GitHub releases.
- Speaks **Model Context Protocol** in both directions: consumes any MCP server (npm, GitHub release, HTTP, local stdio) with auto-install, and runs as an MCP server itself so Claude Desktop / Claude Code / Cursor can browse and trigger missions.
- Built-in scheduling (cron, daily-at-time, recurring intervals with timezone + weekday filters), webhook triggers, token + dollar budgets, and a web command center for live mission graph visualization.

### Links
- [GitHub](https://github.com/mlund01/squadron)
- [Docs](https://docs.squadron.sh)
- [Quick start](https://docs.squadron.sh/getting-started/quickstart)
- [Compare vs LangGraph / CrewAI / AutoGen / n8n](https://docs.squadron.sh/compare/langgraph)

</details>
```

### PR title

```
Add Squadron — declarative multi-agent AI workflow framework in HCL
```

### PR body

```markdown
Adds [Squadron](https://github.com/mlund01/squadron) to the Open-source
projects section, between Stackwise and Superagent (alphabetical).

Squadron is a declarative framework for multi-agent AI workflows: agents,
tools, models, missions, branching, schedules, and budgets are all HCL
config; the runtime is a single Go binary that handles state, resume,
scheduling, and budget enforcement. It complements the imperative
frameworks already in this list (AutoGen, CrewAI, LangGraph etc.) by
covering the declarative side of the design space.

- Repo: https://github.com/mlund01/squadron
- Docs: https://docs.squadron.sh
- License: MIT

Follows the existing entry format and alphabetical ordering.
```

---

## 2. awesome-mcp-clients

- **Repo:** https://github.com/punkpeye/awesome-mcp-clients
- **File:** `README.md`
- **Section:** `## Clients`
- **Position:** Anywhere among the existing S-letter entries (the list is not strictly alphabetical — e.g. `Slack MCP Client` → `Runbear` → `Superinterface` → `SeekChat` → `Simple AI`). Suggested placement: between `SeekChat` and `Simple AI`.

### Patch (Table of Contents entry)

Add this line to the alphabetical TOC inside the `## Clients` block, near the other S-letter entries:

```markdown
    - [Squadron](#squadron)
```

### Patch (full entry)

Insert this block in the body of the `## Clients` section, next to other S-letter entries:

```markdown
### Squadron

<table>
<tr><th align="left">GitHub</th><td>https://github.com/mlund01/squadron</td></tr>
<tr><th align="left">Website</th><td>https://docs.squadron.sh</td></tr>
<tr><th align="left">License</th><td>MIT</td></tr>
<tr><th align="left">Type</th><td>CLI, single binary</td></tr>
<tr><th align="left">Platforms</th><td>macOS, Linux, Windows</td></tr>
<tr><th align="left">Pricing</th><td>Free</td></tr>
<tr><th align="left">Programming Languages</th><td>Go</td></tr>
</table>

**`Squadron`** is a declarative framework for building and running multi-agent AI workflows. The whole workflow — agents, tools, models, branching, schedules, and budgets — lives in HCL configuration. Squadron speaks Model Context Protocol in **both directions**: it consumes any MCP server (npm packages, GitHub release binaries, HTTP, or local stdio) and auto-installs them, and Squadron itself can run as an MCP server so Claude Desktop, Claude Code, and Cursor can browse missions and trigger runs. Native plugin system in Go and Python (gRPC subprocesses, auto-built from local source).
```

### PR title

```
Add Squadron — multi-agent AI framework that speaks MCP both directions
```

### PR body

```markdown
Adds Squadron to the Clients section.

Squadron is a Go-based, MIT-licensed multi-agent AI framework that uses
the Model Context Protocol in both directions:

- As a **client**, it consumes any MCP server (npm, GitHub release, HTTP,
  or local stdio) with two lines of HCL config; auto-install is handled
  for npm and GitHub sources.
- As a **server**, Squadron runs an MCP host so Claude Desktop /
  Claude Code / Cursor can browse missions and trigger runs.

Repo: https://github.com/mlund01/squadron
Docs: https://docs.squadron.sh
MCP docs: https://docs.squadron.sh/config/mcp_tools and
          https://docs.squadron.sh/config/mcp_host

Follows the established table + paragraph format used by neighboring
client entries.
```

---

## 3. awesome-langchain

- **Repo:** https://github.com/kyrolabs/awesome-langchain
- **File:** `README.md`
- **Section:** `## Other LLM Frameworks`
- **Format:** Single-line bullet with a stars badge

### Patch

Append to the `## Other LLM Frameworks` section:

```markdown
- [Squadron](https://github.com/mlund01/squadron): Declarative multi-agent AI workflow framework in HCL. Single Go binary, MCP both directions, plugins in Go and Python. ![GitHub Repo stars](https://img.shields.io/github/stars/mlund01/squadron?style=social)
```

### PR title

```
Add Squadron to Other LLM Frameworks
```

### PR body

```markdown
Adds [Squadron](https://github.com/mlund01/squadron) to the "Other LLM
Frameworks" section.

Squadron is a declarative multi-agent AI workflow framework where the
whole pipeline lives in HCL configuration. Ships as a single Go binary,
speaks Model Context Protocol in both directions, and supports plugins
in Go and Python (gRPC subprocesses, auto-built from local source).

Differentiates from the LangChain ecosystem by being **declarative,
config-first, and code-free** — a Terraform-style approach to agent
orchestration.

- Repo: https://github.com/mlund01/squadron
- Docs: https://docs.squadron.sh
- License: MIT
```

---

## How to submit each PR

For each list:

1. Fork the target repo on GitHub.
2. `git clone` your fork and create a branch (`git checkout -b add-squadron`).
3. Open `README.md`, locate the exact insertion point described above, and apply the patch.
4. Build / preview locally if the project has a render step (most awesome lists are plain markdown — no build required).
5. Commit: `git commit -m "Add Squadron"`
6. Push and open a PR using the title and body provided.

If you'd like Squadron's GitHub topics and star count to look healthier
*before* submitting these PRs (some maintainers gate on social proof),
hold the PRs for ~24-48 hours after the README + topics changes land and
the next release ships.
