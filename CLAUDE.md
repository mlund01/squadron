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
2. **Stage 2**: Load `model` blocks with `vars` context → enables `api_key = vars.anthropic_api_key`
3. **Stage 3**: Load `plugin` blocks with `vars` context
4. **Stage 4**: Load `agent` blocks with `vars` + `models` + `plugins` context → enables `model = models.anthropic.claude_sonnet_4` and `tools = [plugins.playwright.all]`
5. **Stage 5**: Load `mission` blocks with full context

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

1. **Runner** resolves task dependencies (topological sort)
2. **Tasks** execute in parallel when dependencies are satisfied
3. **Commander** manages each task:
   - Queries ancestor commanders for context (`queryAncestorsForContext`)
   - Delegates work to agents via `call_agent` tool
   - Produces structured output via `<OUTPUT>` blocks
4. **Agents** execute ReAct loops:
   - Reason → Act (tool call) → Observe (tool result) → Repeat
   - Return final answer or ask commander for input (`ASK_COMMANDER`)

### Task Dependencies & Context Passing

- Tasks declare `depends_on = [tasks.previous_task]`
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

---

## Persistence & Resume (store/)

Mission state is persisted to SQLite (`.squadron/store.db`) during execution:

- **Missions**: ID, status, config, inputs
- **Tasks**: Status, output JSON, summaries
- **Sessions**: Commander and agent LLM conversation histories
- **Datasets**: Items stored for resume

### Resume Flow

When `--resume <missionID>` is used:

1. Runner loads the stored mission record and identifies completed/pending tasks
2. Completed tasks are "resaturated" — their commanders are rebuilt from stored sessions so downstream tasks can query them via `ask_commander`
3. Pending/failed tasks resume from stored session state using `ContinueStream` (if LLM was interrupted) or by re-executing the interrupted tool call
4. Agent sessions are healed via `HealSessionMessages()` — if the last message was an in-flight tool call, a placeholder observation is injected

### Key Types

| Type | File | Purpose |
|------|------|---------|
| `MissionStore` | `store/store.go` | Mission/task CRUD |
| `SessionStore` | `store/store.go` | Session/message persistence |
| `DatasetStore` | `store/store.go` | Dataset item persistence |
| `SQLiteStore` | `store/sqlite.go` | SQLite implementation of all stores |
| `MemoryBundle` | `store/memory.go` | In-memory implementation (testing) |

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

Use iterated tasks with `parallel = false` to pass context between iterations via `PrevIterationOutput` and `PrevIterationLearnings`.

### Parallel with Shared Context

Use `list_commander_questions` + `get_commander_answer` to deduplicate queries across parallel iterations.
