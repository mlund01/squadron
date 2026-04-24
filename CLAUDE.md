# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run Commands

```bash
go build -o squadron ./cmd/cli              # Build the CLI
./squadron init                            # Initialize encrypted vault
./squadron verify <path>                   # Validate HCL config
./squadron chat -c <path> <agent_name>     # Start chat with an agent
./squadron mission -c <path> <mission>     # Run a mission
./squadron mission -c <path> -d <mission>  # Run with debug logging
./squadron mission --resume <id> -c <path> <mission> # Resume a failed mission
./squadron vars set <name> <value>         # Set a variable
./squadron vars get <name>                 # Get a variable
./squadron vars list                       # List all variables
./squadron serve -c <path>                 # Connect to command center (requires command_center block)
./squadron serve -c <path> -w              # Launch local command center + connect
./squadron serve -c <path> -w --cc-port 9090  # Custom command center port
./squadron serve -c <path> -w --no-browser # Launch without opening browser
./squadron mcp status                      # Show OAuth status for configured MCP servers
./squadron mcp login <name>                # Authorize an MCP server via OAuth
./squadron mcp logout <name>               # Forget stored OAuth token for an MCP server
./squadron upgrade                         # Upgrade to latest release
./squadron upgrade --version v0.0.13       # Upgrade to specific version
./squadron version                         # Print current version
./squadron docs [output-dir]               # Extract docs to local folder
./squadron plugin tools <path>             # List plugin tools
./squadron plugin call <path> <tool> <json># Call a plugin tool
./squadron plugin info <path> <tool>       # Get plugin tool info
./squadron plugin build <source>           # Build a plugin from source
```

## Architecture Overview

Squadron is a declarative framework for building and running AI agent workflows. LLM-powered agents, tools, plugins, and multi-step missions are defined entirely in HCL configuration files — no code required. Agents reason and act autonomously using a two-tier architecture: commanders orchestrate tasks while agents execute tool calls in ReAct loops.

### Key Directories

| Directory | Purpose |
|-----------|---------|
| `agent/` | Agent and Commander implementations, orchestration |
| `aitools/` | Tool interface, schema definitions, result interception |
| `config/` | HCL config loading with staged evaluation |
| `llm/` | LLM provider abstraction (Anthropic, OpenAI, Gemini, Ollama) |
| `plugin/` | gRPC plugin system using hashicorp/go-plugin |
| `mission/` | Mission runner, task execution, knowledge store |
| `store/` | Persistence interfaces and SQLite implementation |
| `scheduler/` | Cron-based mission scheduling and next-fire calculation |
| `streamers/` | Output streaming interfaces for CLI/TUI |
| `wsbridge/` | WebSocket bridge client for command center communication |
| `mcp/` | Consumer-side MCP client: loads external MCP servers (stdio/http/npm/github) declared in `mcp "name" { ... }` blocks and exposes their tools |
| `mcphost/` | Host-side MCP server: exposes Squadron's own tools over MCP when `mcp_host { ... }` is enabled |
| `internal/release/` | Shared GitHub-release download/extract helpers used by both plugin and MCP auto-install |
| `cmd/` | CLI commands and plugin entry points |

---

## HCL Config System (config/)

The config loading uses **staged evaluation** to support HCL expression references:

1. **Stage 1**: Load `variable` blocks (no context needed)
2. **Stage 1.5**: Load `plugin` blocks and `mcp "name"` blocks with `vars` context. Both happen in the same stage because both expose tools through HCL namespaces that later stages need to resolve against.
3. **Stage 2**: Load `model` blocks with `vars` + `plugins` + `mcp` context → enables `api_key = vars.anthropic_api_key`
4. **Stage 3**: Load custom `tool` blocks with `vars` + `models` + `builtins` + `plugins` + `mcp` context → enables `implements = builtins.http.get`
5. **Stage 4**: Load `agent` blocks with `vars` + `models` + `tools` + `plugins` + `mcp` context → enables `tools = [plugins.playwright.all, mcp.filesystem.read_file, tools.weather]`
6. **Stage 5**: Load `mission` blocks with full context

Each stage uses partial structs with `hcl:",remain"` to ignore unknown block types during that pass.

### HCL Config Format

```hcl
# variables.hcl
variable "anthropic_api_key" {
  secret = true
}

# models.hcl
model "anthropic" {
  provider = "anthropic"
  api_key  = vars.anthropic_api_key
}

# plugins.hcl
plugin "playwright" {
  version = "local"
}

# mcp_servers.hcl — consumer-side MCP servers (Squadron pulling tools from external servers)
#
# Exactly one of `command`, `url`, or `source` per block.

# Auto-installed npm package (cache at .squadron/mcp/filesystem/2024.12.1/)
mcp "filesystem" {
  source  = "npm:@modelcontextprotocol/server-filesystem"
  version = "2024.12.1"
  args    = ["/tmp"]
}

# Auto-installed GitHub release binary
mcp "custom" {
  source  = "github.com/owner/mcp-custom"
  version = "v1.0.0"
  # entry = "bin/server"  # optional; disambiguates when the archive has multiple executables
}

# Bare command escape hatch — Squadron runs what you tell it, no install
mcp "local" {
  command = "./my-mcp-server"
  args    = ["--debug"]
}

# HTTP transport — remote MCP server
mcp "remote_api" {
  url = "https://example.com/mcp"
  headers = {
    Authorization = "Bearer ${vars.api_key}"
  }
}

# HTTP transport with OAuth client credentials (for servers that don't support DCR)
mcp "slack" {
  url           = "https://tools.slack.dev/agent-tools-mcp/sse"
  client_id     = vars.slack_client_id
  client_secret = vars.slack_client_secret
}

# mcp_host.hcl — OPPOSITE direction: Squadron hosts its own MCP server
mcp_host {
  enabled = true
  port    = 8090
  secret  = vars.mcp_host_secret
}

# agents.hcl
agent "browser_navigator" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Methodical and precise"
  role        = "Browser automation specialist"
  tools       = [plugins.playwright.all, mcp.filesystem.read_file]
}

# missions.hcl
mission "example" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.browser_navigator]

  inputs = {
    url      = string("Target URL to scrape", true)
    max_pages = integer("Max pages to scrape", { default = 10 })
    tags     = list(string, "Tags to apply")
    options  = map(string, "Extra key-value options")
    auth     = object({
      username = string("Login username", true)
      password = string("Login password", { protected = true })
    }, "Authentication credentials")
  }

  task "login" {
    objective = "Log into the application"
    output = {
      session_id = string("The session token", true)
    }
  }

  task "scrape" {
    depends_on = [tasks.login]
    objective  = "Scrape data from the logged-in session"
    output = {
      results   = list(object({ title = string("Title"), url = string("URL") }), "Scraped results", true)
      metadata  = map(string, "Extra metadata")
      summary   = string("Summary of scraped data", true)
    }
  }
}
```

### Schedules, Triggers, and Concurrency

Missions can run automatically via schedules (cron-based timers) or triggers (webhooks). Both are defined inside the `mission` block and are active only in serve mode.

#### Schedule Block

Three mutually exclusive modes — `at` (daily at specific times), `every` (recurring interval), or `cron` (raw 5-field expression):

```hcl
mission "daily_report" {
  max_parallel = 2   # Max concurrent instances (default: 3)

  # Daily at 9am on weekdays
  schedule {
    at       = ["09:00"]
    weekdays = ["mon", "tue", "wed", "thu", "fri"]
    timezone = "America/Chicago"
    inputs = {
      report_type = "daily"
    }
  }

  # Raw cron — Sunday midnight
  schedule {
    cron     = "0 0 * * 0"
    timezone = "America/Chicago"
  }

  task "generate" { objective = "Generate the report" }
}
```

Multiple `schedule` blocks per mission are allowed; each fires independently. Friendly fields (`at`/`every`/`weekdays`) compile to cron expressions via `ToCron()` in `config/mission.go`.

**Field rules:**
- `at` — time(s) of day in `HH:MM` 24h format. Implies daily. Cannot combine with `every` or `cron`.
- `every` — interval: `"5m"`, `"15m"`, `"1h"`, `"2h"`, etc. Must divide evenly into 60 minutes (sub-hour) or 24 hours (hourly+). Cannot combine with `at` or `cron`.
- `weekdays` — day filter: `["mon", "wed", "fri"]`. Works with `at` or `every`.
- `cron` — standard 5-field cron expression. Mutually exclusive with `at`/`every`/`weekdays`.
- `timezone` — IANA timezone (e.g. `"America/Chicago"`). Defaults to system local.
- `inputs` — key-value map passed to the mission when the schedule fires.

#### Trigger Block

Webhooks are declared in squadron config but handled by the command center:

```hcl
mission "ingest" {
  trigger {
    webhook_path = "/ingest"       # Defaults to "/<mission_name>"
    secret       = vars.hook_secret # Validates X-Webhook-Secret header
  }
  task "process" { objective = "Process incoming data" }
}
```

The command center registers the route `POST /webhooks/<instance_name>/<webhook_path>` and dispatches to squadron via WebSocket when hit. One `trigger` block per mission max.

#### Budgets

Missions and tasks can declare spending limits via a `budget` block. Both
fields are optional but at least one must be set. Whichever limit is reached
first fails the current task and crashes the whole mission — in-flight
commanders and agents unwind via the mission's shared cancellable context.

```hcl
mission "expensive_research" {
  budget {
    tokens  = 5000000   # cumulative across every task
    dollars = 25.00
  }

  task "crawl" {
    budget { tokens = 500000 }   # per-task cap, across all iterations
    objective = "Crawl the target domain"
  }
}
```

- **Scope.** Task budgets sum tokens/cost across the task's commander and every
  agent it spawns, including all iterations of an iterated task (iteration
  suffixes are stripped so `crawl[0]`, `crawl[1]`, ... share the same counter).
  Mission budgets sum across every task in the mission.
- **Both types matter.** Models without configured pricing (Ollama/local) always
  contribute $0, so a dollar-only budget cannot constrain them — pair it with a
  token budget. Conversely, a dollar budget lets you cap spend without having to
  think about per-model token rates.
- **First breach wins.** The tracker latches on first breach, cancels the
  mission context, and returns a `*mission.BudgetBreach` as the mission error.
  In-flight tasks return `ctx.Canceled`, but the runner substitutes the breach
  so the mission status is `failed`, not `stopped`.
- **Zero overhead when unused.** If neither the mission nor any task declares a
  budget, no tracker is created.

#### Concurrency (`max_parallel`)

`max_parallel` (default 3) limits concurrent instances of a mission across all sources — schedules, webhooks, and manual runs. When at capacity, new runs are skipped and a `schedule_skip` event is emitted.

#### Architecture

The scheduler lives in `scheduler/` but its lifecycle (creation, config updates, shutdown) is managed by `cmd/serve.go`, not wsbridge. The wsbridge client receives a `ConcurrencyTracker` interface for enforcing `max_parallel` on all mission starts. The cron library used is `robfig/cron/v3`.

### Folders

Folders are sandboxed filesystem locations that agents access via the `file_list`,
`file_read`, `file_create`, `file_delete`, `file_search`, and `file_grep` tools.
The `folder` parameter is required on every tool call — there is no implicit
default.

Three kinds exist, with strict naming rules:

| Kind | HCL | Registered name | Scope | Persistence |
|------|-----|-----------------|-------|-------------|
| Shared | top-level `shared_folder "name" { ... }` | user-chosen name | referenced by any mission via `folders = [shared_folders.name]` | persists |
| Mission | `folder { ... }` inside a mission | literal `"mission"` | one per mission | persists across runs |
| Run | `run_folder { ... }` inside a mission | literal `"run"` | one per mission per run | ephemeral; path is `<base>/<missionID>/` |

The names `"mission"` and `"run"` are reserved — `shared_folder` cannot use them
(enforced in `config/shared_folder.go`).

```hcl
shared_folder "reference" {
  path     = "./data/reference"
  editable = false   # default read-only
}

mission "analyze" {
  folders = [shared_folders.reference]

  folder {
    path        = "./analyses"
    description = "Cumulative reports — persists across runs"
  }

  run_folder {
    base        = "./runs"            # optional, default ".squadron/runs"
    description = "Per-run scratch"
    cleanup     = 7                   # optional, auto-delete after N days (0 = never)
  }
}
```

**Implementation:**

- `config.SharedFolder`, `config.MissionFolder`, `config.MissionRunFolder` in
  [config/shared_folder.go](config/shared_folder.go) and [config/mission.go](config/mission.go).
- Shared folders are parsed in Stage 1.5 (with `vars` context). The mission's
  `folder` and `run_folder` blocks are parsed inside the mission block in Stage 5.
- [mission/folder_store.go](mission/folder_store.go) is the authoritative
  runtime resolver. `buildFolderStore(mission, sharedFolders, missionID)` must be
  called **after** the mission ID is assigned in `Runner.Run()`, because the run
  folder path depends on it. There is no implicit default folder — every
  tool call must name the folder explicitly.
- Run folders are materialized at `<base>/<missionID>/` and a sidecar
  `.squadron-run.json` records `created_at` + `cleanup_days`.
- `mission.SweepExpiredRunFolders(base)` walks a base directory and deletes any
  subfolder whose sidecar says it is past its cleanup deadline. It runs
  opportunistically at the start of every `Runner.Run()` (for that mission's
  base) and on an hourly ticker in `cmd/engage.go` (across every mission in the
  current config).

### Mission-Scoped Agents

Agents can be defined inside a `mission` block, making them available only to that mission. Mission-scoped agents use the same syntax and parsing as global agents but are namespaced to their mission.

```hcl
mission "research" {
  commander { model = models.anthropic.claude_sonnet_4 }

  agent "specialist" {
    model       = models.anthropic.claude_opus_4
    personality = "Deep domain expert"
    role        = "Research specialist with access to specialized tools"
    tools       = [plugins.shell.exec]
  }

  agents = [agents.global_helper, agents.specialist]

  task "gather" {
    objective = "Research the topic"
    agents    = [agents.specialist]
  }
}
```

**Rules:**
- Same syntax as global agents — same fields, same capabilities
- A scoped agent name must not conflict with any global agent name (validation error)
- Two different missions CAN each define a scoped agent with the same name (independently scoped)
- Scoped agents must be listed in the mission's `agents = [...]` to be available
- Can be assigned at the task level via `agents = [agents.specialist]`
- Multiple scoped agents per mission are supported

### Task Connectivity: depends_on, router, and send_to

There are three ways tasks connect to each other. Each serves a different purpose, but they share a common execution model built around the distinction between **static** and **dynamic** task activation.

#### Static activation: `depends_on` (pull)

`depends_on` is the standard way to sequence tasks. A task with `depends_on` waits until **all** listed dependencies complete before it starts. These tasks are part of the **static DAG** — the runner knows about them upfront via topological sort and schedules them automatically.

```hcl
task "fetch" { objective = "Fetch data" }
task "process" {
  depends_on = [tasks.fetch]
  objective  = "Process fetched data"
}
```

- The dependent task **pulls** from its predecessors — it says "I need these done first"
- Multiple `depends_on` entries are an AND gate: all must complete
- When a task starts, the runner queries each ancestor's commander for context, giving the new task access to what its predecessors learned

#### Dynamic activation: `router` (conditional push)

A `router` block lets the LLM decide which branch to activate after a task completes. The commander evaluates the route conditions and picks one (or none). This is the **if/else for DAGs**.

```hcl
task "classify" {
  objective = "Classify the request"
  router {
    route {
      target    = tasks.handle_billing
      condition = "The request is about billing"
    }
    route {
      target    = tasks.handle_support
      condition = "The request is technical support"
    }
  }
}
task "handle_billing" { objective = "Process billing request" }
task "handle_support" { objective = "Handle support ticket" }
```

**Cross-mission routing:** Route targets can reference other missions via `missions.foo` instead of `tasks.foo`. When a mission route is chosen, the current mission completes and the target mission launches as a new instance. The commander provides any required inputs for the target mission via `mission_inputs` in the `task_complete` call. A mission can even route to itself (creates a new instance).

```hcl
route {
  target    = missions.escalation_mission
  condition = "The complaint is severe and needs escalation"
}
```

**How it works at runtime:**
Route options are injected as a system prompt when the commander starts, so it knows the available routes upfront. When calling `task_complete`, the commander includes both a `summary` and a `route` parameter in a single call. If routes are configured but no `route` is provided, `task_complete` returns an error prompting the commander to include one.

#### Dynamic activation: `send_to` (unconditional push)

`send_to` unconditionally activates target tasks when the source completes. No LLM decision — all targets fire immediately. This is useful for fan-out patterns where every branch should always run.

```hcl
task "fetch" {
  objective = "Fetch data"
  send_to   = [tasks.process_a, tasks.process_b]
}
task "process_a" { objective = "Process path A" }
task "process_b" { objective = "Process path B" }
```

#### The static/dynamic divide

This is the critical design constraint: tasks are either **statically scheduled** (part of the initial topological sort) or **dynamically activated** (only run when a `router` or `send_to` fires). These two worlds do not mix.

**A dynamically activated task is the root of its own sub-DAG.** It cannot participate in the static dependency graph because:

1. **Dynamic targets cannot have `depends_on`.** If a task is reachable via `router` or `send_to`, it cannot also declare `depends_on`. It starts when activated, not when some other set of tasks completes.

2. **No task can `depends_on` a dynamic target.** If task D declared `depends_on = [tasks.B]` and B is a `send_to` target, D would be in the static topological sort waiting for B. But B only runs if its source completes and activates it — if B is never activated, D hangs forever. So this is a validation error.

3. **`router` and `send_to` are mutually exclusive on a task.** A task pushes work either conditionally (router) or unconditionally (send_to), not both.

**Why this matters:** The graph is a collection of sub-DAGs. The static DAG runs from topological sort. Each `router` or `send_to` edge is a bridge that spawns a new sub-DAG at runtime. These sub-DAGs are independent — they can chain further via their own `router` or `send_to`, but nothing in the static graph can wait on them.

#### First-one-wins

A dynamic target can be referenced by **multiple** routers or send_to sources. The first activation wins — once a task starts or completes, subsequent activations are silently ignored. This is safe because tasks run at most once.

```hcl
# Both classifiers can route to shared_handler — whichever fires first activates it
task "classify_a" {
  objective = "Classify stream A"
  router {
    route { target = tasks.shared_handler; condition = "Needs handling" }
  }
}
task "classify_b" {
  objective = "Classify stream B"
  router {
    route { target = tasks.shared_handler; condition = "Needs handling" }
  }
}
task "shared_handler" { objective = "Handle the request" }
```

#### Ancestor context for dynamic tasks

When a dynamically activated task starts, the runner treats the activating task as its parent for context purposes. The `getDependencyChain()` function follows `routerParents` links, so a routed-to task gets access to the **full ancestry** of the task that activated it — the same depth of context as if it had `depends_on`.

#### Validation summary

| Rule | Enforced in |
|------|-------------|
| No cycles (depends_on + router + send_to edges combined) | `ValidateDAG()` |
| Dynamic targets cannot have `depends_on` | `Mission.Validate()` |
| No task can `depends_on` a dynamic target | `Mission.Validate()` |
| `router` and `send_to` mutually exclusive | `Task.Validate()` |
| No self-routing / self-send | `Task.Validate()` |
| No duplicate targets within a router or send_to | `Task.Validate()` |
| Router targets must exist (task or mission) | `Task.Validate()` |
| Mission route targets must reference valid missions | `Task.Validate()` |
| Send_to targets must exist | `Task.Validate()` |
| Parallel iterators cannot have a router | `Task.Validate()` |
| At least one task can start (no deps, not router-only) | `Mission.Validate()` |

#### Iterator interaction

- **Parallel iterators** cannot have a `router` (each iteration is independent — there's no single decision point)
- **Sequential iterators** can have a `router` — the route is evaluated after the final iteration completes
- Both parallel and sequential iterators can use `send_to` — targets activate after the iteration completes

#### Persistence and resume

Route decisions (both `router` choices and `send_to` activations) are persisted to the `route_decisions` table. On resume:
1. Route decisions are loaded to reconstruct `routerParents` (which task activated which)
2. Incomplete dynamic targets are re-queued for execution
3. The activating task's commander is resaturated so the dynamic target can query it for context

---

## LLM Provider Abstraction (llm/)

- `Provider` interface defines `Chat()` and `ChatStream()` methods
- `Session` maintains conversation history and system prompts
- Implementations: `AnthropicProvider`, `OpenAIProvider`, `GeminiProvider`
- Sessions support cloning for isolated query processing (used in `ask_commander`)
- `ContinueStream()` resumes from existing state without adding a new user message (used for mission resume)
- `LoadMessages()` restores session from persisted state

Model keys (used in HCL) map to actual API model names in `config/model.go:SupportedModels`. Add new models there before using them in config.

---

## Plugin System (plugin/)

Plugins extend Squad with external tools using **hashicorp/go-plugin** over gRPC.

### Plugin Interface

```go
type ToolProvider interface {
    Configure(settings map[string]string) error
    Call(toolName string, payload string) (string, error)
    GetToolInfo(toolName string) (*ToolInfo, error)
    ListTools() ([]*ToolInfo, error)
}
```

### Plugin Paths

Plugins are stored in versioned directories:
```
.squadron/plugins/<name>/<version>/plugin
```

Example: `.squadron/plugins/playwright/local/plugin`

### Building a Plugin

```bash
# Build a plugin to the correct path (plugins are separate repos)
squadron plugin build shell /path/to/plugin_shell
squadron plugin build playwright /path/to/plugin_playwright
```

### Plugin Reference in HCL

```hcl
# Load a plugin
plugin "playwright" {
  version = "local"
  settings = {
    headless = "true"
  }
}

# Use specific tools
tools = [plugins.playwright.browser_navigate, plugins.playwright.browser_screenshot]

# Use all tools from a plugin
tools = [plugins.playwright.all]
```

### Plugin Registration

Plugins are globally cached (`plugin/client.go:globalRegistry`) so plugin state (like browser sessions) persists across mission tasks. Use `plugin.CloseAll()` at program exit.

---

## MCP Consumer System (mcp/)

The `mcp/` package lets Squadron pull tools from external MCP (Model Context
Protocol) servers and expose them to agents as if they were native plugins.
Used for `npm` packages like `@modelcontextprotocol/server-filesystem`, GitHub
release binaries, remote HTTP MCP servers, and any local binary the user
wants to run.

The companion `mcphost/` package handles the opposite direction — Squadron
acting AS an MCP server so other LLMs can consume its tools — and is
controlled by the `mcp_host { ... }` singleton block.

### The four modes of `mcp "name" { ... }`

| Mode | Required | Optional | Install |
|------|----------|----------|---------|
| bare stdio | `command` | `args`, `env` | none — user runs their own binary |
| HTTP | `url` | `headers` | none — remote |
| npm | `source = "npm:..."`, `version` | `args`, `env` | `npm install --prefix <cache>` on first load |
| GitHub binary | `source = "github.com/..."`, `version` | `args`, `env`, `entry` | download + checksum-verify + extract on first load |

Exactly one of `command`, `url`, `source` per block. `env` is stdio-only;
`headers` is http-only; `version` is required with `source` and forbidden
otherwise; `entry` is only valid with `source = "github.com/..."`.

### Cache layout

Sits next to `.squadron/plugins/`:

```
.squadron/mcp/
├── filesystem/2024.12.1/
│   ├── runner.json
│   └── node_modules/...
└── custom/v1.0.0/
    ├── runner.json
    └── mcp-custom
```

`runner.json` is the "done" marker. Its presence short-circuits the installer
on subsequent loads. Force a reinstall by `rm -rf .squadron/mcp/<name>/<version>/`.

### Agent reference syntax

Once loaded, tools resolve through the `mcp` HCL namespace alongside
`builtins`, `plugins`, and `tools`:

```hcl
agent "fs" {
  tools = [
    mcp.filesystem.read_file,    # specific tool
    mcp.remote_api.all,          # every tool from that server
    plugins.playwright.all,       # native plugins still work the same
  ]
}
```

Refs get sanitized to API-safe names via `aitools.AddSanitizedAliases` — e.g.
`mcp.filesystem.read_file` → `mcp_filesystem_read_file` when the tool is sent
to the provider.

### OAuth for HTTP MCP servers

HTTP MCP servers that require OAuth 2.1 are authenticated via `squadron mcp login`:

```bash
squadron mcp login linear    # discovery → DCR → PKCE → browser → token exchange
squadron mcp status           # shows auth state for every configured MCP
squadron mcp logout linear    # wipes token (keeps DCR credentials for faster relogin)
```

The HCL block is zero-config — just declare the URL:

```hcl
mcp "linear" {
  url = "https://mcp.linear.app/sse"
}
```

If the server returns 401 on load, Squadron surfaces an `AuthRequiredError` with a pointer to `squadron mcp login <name>`. Tokens live in the encrypted vault under `oauth:<name>:token`; mcp-go handles refresh transparently via the stored refresh token.

The `mcp/oauth/` package houses:
- `VaultTokenStore` — implements `transport.TokenStore` against the vault
- `RunLoginFlow` — the orchestrator (discovery, DCR, PKCE, browser, exchange)
- `LoopbackCallbackSource` — serves `/callback` on `127.0.0.1:0` for CLI mode
- `CallbackSource` interface — Phase 2 adds a wsbridge-backed source for command-center mode

SSE vs streamable HTTP is auto-detected from the URL path suffix (`/sse`). The OAuth transport is only engaged when a token is already stored — anonymous servers fall through to the plain client.

### Architecture

| File | Purpose |
|------|---------|
| `mcp/client.go` | `Client` struct + `globalRegistry` + `Load`/`LoadWithContext`/`CloseAll` |
| `mcp/tool.go` | `mcpTool` adapter implementing `aitools.Tool` |
| `mcp/schema.go` | `convertSchema` — raw JSON Schema passthrough + best-effort typed projection |
| `mcp/errors.go` | `AuthRequiredError` + `classifyAuthError` |
| `mcp/oauth/` | OAuth token store, login orchestrator, loopback callback |
| `mcp/install.go` | `resolveRunner`, `installNPM`, `installGitHub`, `pickEntry` |
| `mcp/paths.go` | cache-dir path helpers |

### Lifecycle

Mirrors native plugins. The registry is process-global — one subprocess per
declared server, shared across all agents, skills, tasks, and missions in a
single CLI invocation. Startup is eager (Stage 1.5 of config load); liveness
is checked on reuse via `client.Ping`. Mid-run crashes are surfaced as
tool-call errors and not auto-recovered in v1 (users re-run the command).
`mcp.CloseAll()` is deferred in `cmd/root.go` alongside `plugin.CloseAll()`
and invoked from the SIGINT handler.

### Known limitations

- Tool list is snapshot at load time; `tools/list_changed` is ignored.
- MCP prompts and resources are not exposed — tools only.
- Schema fidelity: raw JSON Schema bytes are preserved for LLM providers via `WithRawJSONSchema`; the typed `aitools.Schema` projection is lossy but only used for in-process introspection.
- npm sources require `npm` and `node` on PATH; clear error if missing.
- No Python/uv support — use the bare `command` escape hatch.
- `version` must be pinned exactly; no semver ranges.
- Hard squadron crash → orphaned subprocesses (same as plugins).

---

## Mission System (mission/)

Missions orchestrate multi-agent task execution with dependency management.

### Core Components

| Component | File | Purpose |
|-----------|------|---------|
| `Runner` | `runner.go` | Executes missions, manages task dependencies |
| `Commander` | `agent/commander.go` | Orchestrates agents for a single task |
| `Agent` | `agent/agent.go` | Executes tool calls with ReAct-style reasoning |
| `KnowledgeStore` | `knowledge.go` | Stores structured task outputs for querying |
| `DebugLogger` | `debug.go` | Logs events and LLM messages for debugging |

### Execution Flow

1. **Runner** resolves task dependencies (topological sort), filtering out dynamically-activated tasks (router/send_to targets with no `depends_on`)
2. **Static tasks** execute in parallel when all `depends_on` dependencies are satisfied
3. **Commander** manages each task:
   - Receives push summaries from ancestor tasks as static context (no LLM queries needed)
   - Can use `ask_commander` to get more detail from ancestor commanders if summaries aren't enough
   - Delegates work to agents via `call_agent` tool
   - Calls `task_complete` with a `summary` (and `route` if routing is configured)
4. **Dynamic activation**: After a task completes, its `send_to` targets are immediately queued. If it chose a route, the route target is queued. (See "Task Connectivity" in HCL Config section for full rules.)
5. **Agents** execute ReAct loops:
   - Reason → Act (tool call) → Observe (tool result) → Repeat
   - Return final answer or ask commander for input (`ASK_COMMANDER`)

### Task Dependencies & Context Passing

- Static dependencies: `depends_on = [tasks.previous_task]` — task waits for all listed tasks
- Dynamic dependencies: `router`/`send_to` targets get the activating task as their ancestor
- When a task completes, the commander provides a `summary` in `task_complete` — stored in DB and in-memory `taskSummaries` map
- When a dependent task starts, it receives static summaries from all ancestors (no LLM queries needed — instant)
- Commanders can use `ask_commander` to query ancestor commanders for more detail if summaries aren't enough
- Structured outputs are stored in KnowledgeStore for `query_task_output` queries

### Iterated Tasks

Tasks can iterate over datasets:

```hcl
dataset "items" {
  items = [
    { name = "Item 1" },
    { name = "Item 2" },
  ]
}

task "process" {
  iterator {
    dataset  = datasets.items
    parallel = true           # Run iterations concurrently
    concurrency_limit = 5     # Max concurrent iterations
    max_retries = 2           # Retry failed iterations
    smoketest = true          # Run first iteration alone first
  }
  objective = "Process ${item.name}"
}
```

### Commander Tools

| Tool | Purpose |
|------|---------|
| `call_agent` | Delegate work to an agent (or respond to agent's question) |
| `ask_agent` | Query a completed agent for follow-up information |
| `ask_commander` | Query a dependency task's commander for more context |
| `query_task_output` | Access structured outputs from completed tasks |
| `task_complete` | Signal task completion; triggers routing flow if task has a router |
| `list_commander_questions` | See questions asked by other iterations (parallel dedup) |
| `get_commander_answer` | Get cached answer from shared question store |

---

## Agent System (agent/)

### Agent Modes

- `ModeChat`: Interactive chat mode (default)
- `ModeMission`: Mission execution mode (uses `ASK_COMMANDER` for commander queries)

### ReAct Loop

Agents use a ReAct (Reason + Act) pattern:

```
<REASONING>Think about what to do...</REASONING>
<ACTION>tool_name</ACTION>
<ACTION_INPUT>{"param": "value"}</ACTION_INPUT>
```

Tool results are returned as:
```
<OBSERVATION>result from tool</OBSERVATION>
```

Final answers:
```
<ANSWER>The task is complete...</ANSWER>
```

### Result Interception

Large tool results are automatically intercepted (`aitools/interceptor.go`) and stored in a `ResultStore`. The LLM receives a summary with instructions to use `result_*` tools to access full data.

---

## Debug Logging (mission/debug.go)

Enable debug mode with `-d` flag:
```bash
./squadron mission -c config/ -d my_mission
```

Creates a debug directory: `debug/<mission>_<timestamp>/`

### Debug Output Files

| File | Contents |
|------|----------|
| `events.log` | JSON-line events (task start/complete, tool calls) |
| `commander_<task>.md` | Full LLM conversation for task's commander |
| `agent_<task>_<agent>.md` | Full LLM conversation for each agent |

### Event Types

- `mission_started`, `mission_completed`
- `task_started`, `task_completed`
- `iteration_started`, `iteration_completed`
- `agent_started`, `agent_completed`
- `commander_reasoning_started`, `commander_reasoning_completed`
- `agent_reasoning_started`, `agent_reasoning_completed`
- `commander_calling_tool`, `commander_tool_complete`
- `agent_calling_tool`, `agent_tool_complete`
- `commander_answer`, `agent_answer`
- `route_chosen`
- `compaction`

---

## Persistence & Resume (store/)

Mission state is persisted to SQLite (`.squadron/store.db`) during execution:

- **Missions**: ID, status, config, inputs
- **Tasks**: Status, output JSON, summaries
- **Sessions**: Commander and agent LLM conversation histories
- **Datasets**: Items stored for resume
- **Route decisions**: Router task, target task, condition (for resume)

### Resume Flow

When `--resume <missionID>` is used:

1. Runner loads the stored mission record and identifies completed/pending tasks
2. Route decisions are loaded from store to reconstruct `routerParents` and re-queue pending router-activated tasks
3. Completed tasks are "resaturated" — their commanders are rebuilt from stored sessions so downstream tasks can query them via `ask_commander`
4. Pending/failed tasks resume from stored session state using `ContinueStream` (if LLM was interrupted) or by re-executing the interrupted tool call
5. Agent sessions are healed via `HealSessionMessages()` — if the last message was an in-flight tool call, a placeholder observation is injected

### Key Types

| Type | File | Purpose |
|------|------|---------|
| `MissionStore` | `store/store.go` | Mission/task CRUD |
| `SessionStore` | `store/store.go` | Session/message persistence |
| `DatasetStore` | `store/store.go` | Dataset item persistence |
| `SQLiteStore` | `store/sqlite.go` | SQLite implementation of all stores |

### Schema Migrations

All schema changes flow through the versioned runner in `store/migrations.go`.
The runner tracks applied versions in a `schema_migrations` table and applies
each pending migration in its own transaction together with the bookkeeping
insert — a partial failure leaves the DB untouched. It runs automatically
during `NewSQLiteBundle` / `NewPostgresBundle`.

#### File layout

Migration SQL lives under `store/migrations/` as paired, dialect-specific
files embedded via `//go:embed`:

```
store/
  migrations.go                      # runner + loader
  migrations_checksums_test.go       # tamper guardrail
  migrations/
    0001_baseline.sqlite.sql
    0001_baseline.postgres.sql
    0002_<name>.sqlite.sql
    0002_<name>.postgres.sql
    ...
```

Filenames MUST match `NNNN_<name>.<sqlite|postgres>.sql`. At startup,
`LoadMigrations()` pairs each version's sqlite and postgres files, enforces
that both exist and share the same name, and errors if versions skip.

#### Adding a migration — the ONLY sanctioned way to change a schema

1. Create two new files under `store/migrations/`:
   - `NNNN_<name>.sqlite.sql`
   - `NNNN_<name>.postgres.sql`

   where `NNNN` is the previous version + 1, zero-padded to 4 digits, and
   `<name>` is `lowercase_snake_case` (matches `[a-z0-9_]+`).
2. Write dialect-specific DDL in each file. Dialects diverge on type names
   (`REAL` vs `DOUBLE PRECISION`), auto-increment (`AUTOINCREMENT` vs
   `SERIAL`), and a few `ALTER TABLE` semantics — write each side
   explicitly. If a change is meaningful for only one dialect, the other
   file can be empty but must still exist.
3. Prefer `IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS` so the migration is
   safe to re-run if something wedges mid-apply.
4. Do NOT wrap the SQL in `BEGIN`/`COMMIT` — the runner handles that.
5. Register both files' sha256 checksums in `migrationChecksums` in
   [store/migrations_checksums_test.go](store/migrations_checksums_test.go):

   ```
   shasum -a 256 store/migrations/NNNN_<name>.sqlite.sql
   shasum -a 256 store/migrations/NNNN_<name>.postgres.sql
   ```

6. Update the stores (`sqlite.go`, `postgres.go`, `cost_store*.go`, etc.) in
   the same PR so code and schema advance together.

#### Append-only invariants (enforced by tests)

- **NEVER edit a shipped migration file.** The checksum test compares every
  file on disk against its recorded sha256 — any byte-level change breaks
  the build with a message pointing to the offender. If a migration is
  wrong, write a NEW one that fixes it forward.
- **NEVER delete a shipped migration file.** `migrationChecksums` entries
  without a matching file on disk also fail the test. Historical migrations
  must stay in the tree because users in the field have the old version
  recorded and the runner still needs to reason about it.
- **NEVER add a migration file without registering it.** Files under
  `store/migrations/` without a `migrationChecksums` entry also fail the
  test — every migration must be locked down.
- **NEVER add bare `db.Exec("ALTER TABLE ...")`** calls outside the
  migration list. All schema mutations go through a numbered migration.

Baseline (migration 1) captures the schema that existed when the versioned
system was introduced. It uses `IF NOT EXISTS` everywhere so existing
deployments upgrade cleanly: their tables are preserved, baseline is
recorded as applied, and subsequent migrations run on top.


---

## Variable Storage

Variables are encrypted at rest in `.squadron/vars.vault` using AES-256-GCM with an Argon2id-derived key. The encryption passphrase is stored in the OS keychain (macOS Keychain, Linux Secret Service/KeyCtl, Windows WinCred).

Run `squadron init` before using vars commands. Commands `serve`, `chat`, and `mission` require init (or pass `--init` to auto-initialize).

Passphrase resolution order: in-process cache → `--passphrase-file` flag → `/run/secrets/vault_passphrase` (Docker) → OS keyring → hardcoded fallback (with warning).

Legacy plaintext `vars.txt` is still supported for backward compatibility but `squadron init` migrates it to the encrypted vault.

---

## Adding New Tools

### Built-in Tools (aitools/)

1. Create a struct implementing `aitools.Tool` interface:
```go
type MyTool struct{}

func (t *MyTool) ToolName() string { return "my_tool" }
func (t *MyTool) ToolDescription() string { return "..." }
func (t *MyTool) ToolPayloadSchema() Schema { return Schema{...} }
func (t *MyTool) Call(input string) string { ... }
```

2. Add to agent's tools in `config.BuildToolsMap()`

### Plugin Tools

1. Add tool info to plugin's `tools` map
2. Handle the tool name in the plugin's `Call()` method
3. Rebuild the plugin binary

---

## Testing Missions

1. Run with debug mode to capture full LLM conversations
2. Check `events.log` for tool call/result timing
3. Read agent markdown files to see full reasoning chains
4. Use `query_task_output` in HCL to verify structured outputs

---

## Common Patterns

### Browser Automation

Use Playwright plugin with AI-friendly tools:
- `browser_aria_snapshot`: Get accessibility tree (best for understanding page structure)
- `browser_screenshot`: Visual confirmation
- `browser_click_coordinates`: Reliable clicking when selectors fail

### Sequential Data Processing

Use iterated tasks with `parallel = false` to pass context between iterations via `PrevIterationOutput`.

### Parallel with Shared Context

Use `list_commander_questions` + `get_commander_answer` to deduplicate queries across parallel iterations.
