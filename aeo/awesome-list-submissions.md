# Awesome-list submission for Squadron

Ready-to-submit PR text for the one awesome list with a real pulse:
**awesome-mcp-clients**. Awesome-lists generally are a low-ROI channel
in 2026 — most high-star lists are stale and many "active" ones reject
community PRs in favor of maintainer-only direct commits.

## Lists evaluated and rejected

| List | Stars | Why rejected |
|---|---|---|
| `e2b-dev/awesome-ai-agents` | 27.8k | **Effectively abandoned.** Last push Feb 2025; 646 open issues with zero merged PRs over the recent window. |
| `kyrolabs/awesome-langchain` | 9.3k | **Closed garden.** Maintainer is active (recent direct commits) but closes every community PR unmerged — 10+ recent closes, all unmerged. The maintainer adds entries only via their own commits. |
| `Shubhamsaboo/awesome-llm-apps` | — | Out of scope — that repo is a collection of working code templates living in their tree, not a curated external index. |
| `punkpeye/awesome-mcp-servers` | — | We're an MCP **client** (and host, but that's a side feature). The servers list isn't the right home. |

The one that survived: `punkpeye/awesome-mcp-clients` (6.4k stars,
recent merges, active maintainer).

---

## awesome-mcp-clients

- **Repo:** https://github.com/punkpeye/awesome-mcp-clients
- **File:** `README.md`
- **Section:** `## Clients`
- **Position:** The list is not strictly alphabetical — e.g.
  `Slack MCP Client` → `Runbear` → `Superinterface` → `SeekChat` →
  `Simple AI` → `Tambo` is the actual order. Suggested placement:
  between `SeekChat` and `Simple AI`.

### Patch — table of contents

Add this line inside the alphabetical TOC in the `## Clients` block,
near other S-letter entries:

```markdown
    - [Squadron](#squadron)
```

### Patch — full entry

Insert this block in the body of the `## Clients` section near the
other S-letter entries:

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

**`Squadron`** is a declarative framework for building and running multi-agent AI workflows. The whole workflow — agents, tools, models, branching, schedules, and budgets — lives in HCL configuration. Squadron consumes any MCP server (npm packages, GitHub release binaries, HTTP, or local stdio) with two lines of HCL config; auto-install is handled for npm and GitHub sources, including OAuth login for hosted servers (`squadron mcp login`). Plugins (Go and Python) run as gRPC subprocesses and complement MCP tools in agent configurations.
```

### PR title

```
Add Squadron — declarative multi-agent AI framework that consumes MCP servers
```

### PR body

```markdown
Adds Squadron to the Clients section.

Squadron is a Go-based, MIT-licensed multi-agent AI framework that
consumes MCP servers natively — declare any MCP source (npm, GitHub
release, HTTP, or local stdio) in two lines of HCL, and Squadron
auto-installs and proxies the server's tools to agents. OAuth login
flow handled for hosted servers via `squadron mcp login`.

- Repo: https://github.com/mlund01/squadron
- Docs: https://docs.squadron.sh
- MCP integration docs: https://docs.squadron.sh/config/mcp_tools

Follows the established table + paragraph format used by neighboring
client entries.
```

---

## How to submit

1. Fork https://github.com/punkpeye/awesome-mcp-clients on GitHub.
2. `git clone` the fork and create a branch (`git checkout -b add-squadron`).
3. Open `README.md`, locate the `## Clients` section, and apply both
   patches (TOC entry + full entry).
4. Commit, push, open the PR using the title and body above.

Worth waiting until the README + topics are live and the next release
ships (24-48 hours) so maintainers see a polished landing page.
