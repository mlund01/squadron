# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run Commands

```bash
go build -o squadron ./cmd/cli              # Build the CLI
./squadron verify <path>                   # Validate HCL config
./squadron chat -c <path> <agent_name>     # Start chat with an agent
./squadron mission -c <path> <mission>     # Run a mission
./squadron mission -c <path> -d <mission>  # Run with debug logging
./squadron mission --resume <id> -c <path> <mission> # Resume a failed mission
./squadron vars set <name> <value>         # Set a variable
./squadron vars get <name>                 # Get a variable
./squadron vars list                       # List all variables
./squadron serve -c <path>                 # Connect to commander server (requires commander block)
./squadron serve -c <path> -w              # Launch local command center + connect
./squadron serve -c <path> -w --cc-port 9090  # Custom command center port
./squadron serve -c <path> -w --no-browser # Launch without opening browser
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

Squad is an HCL-based CLI for defining and running AI agents and multi-agent missions. It uses a config-first approach where agents, models, tools, plugins, and missions are declared in `.hcl` files.

### Key Directories

| Directory | Purpose |
|-----------|---------|
| `agent/` | Agent and Commander implementations, orchestration |
| `aitools/` | Tool interface, schema definitions, result interception |
| `config/` | HCL config loading with staged evaluation |
| `llm/` | LLM provider abstraction (Anthropic, OpenAI, Gemini) |
| `plugin/` | gRPC plugin system using hashicorp/go-plugin |
| `mission/` | Mission runner, task execution, knowledge store |
| `store/` | Persistence interfaces and SQLite implementation |
| `streamers/` | Output streaming interfaces for CLI/TUI |
| `cmd/` | CLI commands and plugin entry points |

---

## HCL Config System (config/)

The config loading uses **staged evaluation** to support HCL expression references:

1. **Stage 1**: Load `variable` blocks (no context needed)
2. **Stage 1.5**: Load `plugin` blocks with `vars` context
3. **Stage 2**: Load `model` blocks with `vars` + `plugins` context → enables `api_key = vars.anthropic_api_key`
4. **Stage 3**: Load custom `tool` blocks with `vars` + `models` + `plugins` context → enables `implements = plugins.http.get`
5. **Stage 4**: Load `agent` blocks with `vars` + `models` + `tools` + `plugins` context → enables `model = models.anthropic.claude_sonnet_4` and `tools = [plugins.playwright.all, tools.weather]`
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
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_opus_4"]
  api_key        = vars.anthropic_api_key
}

# plugins.hcl
plugin "playwright" {
  version = "local"
}

# agents.hcl
agent "browser_navigator" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Methodical and precise"
  role        = "Browser automation specialist"
  tools       = [plugins.playwright.all]
}

# missions.hcl
mission "example" {
  commander = models.anthropic.claude_sonnet_4
  agents           = [agents.browser_navigator]

  task "login" {
    objective = "Log into the application"
    output {
      field "session_id" {
        type     = "string"
        required = true
      }
    }
  }

  task "scrape" {
    depends_on = [tasks.login]
    objective  = "Scrape data from the logged-in session"
  }
}
```

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

**How it works at runtime (`task_complete` two-phase flow):**
1. Commander calls `task_complete` → tool returns route options as a JSON list (plus "none"). Mission targets include `required_inputs`.
2. Commander evaluates options (can call agents for more info first)
3. Commander calls `task_complete` again with `route` parameter to select a route. For mission targets, also provides `mission_inputs`.
4. The chosen route's target task is activated (or target mission is launched); unchosen branches never execute

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
~/.squadron/plugins/<name>/<version>/plugin
```

Example: `~/.squadron/plugins/playwright/local/plugin`

### Building a Plugin

```bash
# Build a plugin to the correct path
mkdir -p ~/.squadron/plugins/playwright/local
go build -o ~/.squadron/plugins/playwright/local/plugin ./cmd/plugin_playwright
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
   - Queries ancestor commanders for context (`queryAncestorsForContext`) — ancestors include `depends_on` parents and the `routerParent` that activated it
   - Delegates work to agents via `call_agent` tool
   - Produces structured output via `<OUTPUT>` blocks
   - If task has a `router`, `task_complete` triggers two-phase route selection
4. **Dynamic activation**: After a task completes, its `send_to` targets are immediately queued. If it chose a route, the route target is queued. (See "Task Connectivity" in HCL Config section for full rules.)
5. **Agents** execute ReAct loops:
   - Reason → Act (tool call) → Observe (tool result) → Repeat
   - Return final answer or ask commander for input (`ASK_COMMANDER`)

### Task Dependencies & Context Passing

- Static dependencies: `depends_on = [tasks.previous_task]` — task waits for all listed tasks
- Dynamic dependencies: `router`/`send_to` targets get the activating task as their ancestor
- When a task starts, Runner queries each ancestor commander with the new task's objective
- Ancestors provide targeted context based on what they learned
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
- `tool_call`, `tool_result`
- `route_chosen`

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


---

## Variable Storage

Variables are stored in `~/.squadron/vars.txt` as `key=value` pairs. The config system merges these with defaults from `variable` blocks. Secret variables are stored but never displayed.

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
