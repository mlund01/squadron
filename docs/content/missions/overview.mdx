---
title: Missions Overview
---

# Missions

Missions orchestrate multi-task pipelines using commanders and agents.

## Basic Structure

```hcl
mission "data_pipeline" {
  commander {
    model = models.anthropic.claude_sonnet_4
  }
  agents = [agents.researcher, agents.writer]

  task "fetch_data" {
    objective = "Fetch the latest data from the API"
  }

  task "process_data" {
    objective  = "Process and analyze the fetched data"
    depends_on = [tasks.fetch_data]
  }

  task "generate_report" {
    objective  = "Generate a summary report"
    depends_on = [tasks.process_data]
  }
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `directive` | string | High-level description of the mission's purpose |
| `commander` | string or block | Model for task commanders (block form: `commander { model = ... }`) |
| `agents` | list | Agents available to all tasks |
| `input` | block | Mission input parameters (repeatable) |
| `task` | block | Task definitions (repeatable) |
| `dataset` | block | Dataset definitions (optional) |
| `schedule` | block | Automatic run schedules (optional, repeatable) |
| `trigger` | block | Webhook trigger (optional) |
| `max_parallel` | number | Max concurrent instances (default: 3) |

## Mission Inputs

Missions can declare typed inputs that are passed at runtime:

```hcl
mission "report" {
  input "topic" {
    type        = "string"
    description = "The topic to research"
  }

  input "format" {
    type        = "string"
    description = "Output format"
    default     = "markdown"
  }

  task "research" {
    objective = "Research ${inputs.topic} and output in ${inputs.format}"
  }
}
```

| Attribute | Type | Description |
|-----------|------|-------------|
| `type` | string | Input type (`string`, `number`, `integer`, `boolean`) |
| `description` | string | Human-readable description |
| `default` | any | Default value (makes the input optional) |
| `secret` | bool | Mark the input as sensitive (masked in logs/UI) |

Inputs without a `default` are required. Pass them via CLI:

### Shorthand Schema Syntax

Instead of `input` blocks you can use a single `inputs = { ... }` attribute with schema helper functions:

```hcl
mission "report" {
  inputs = {
    topic   = string("The topic to research")
    format  = string("Output format", { default = "markdown" })
    limit   = number("Max results", { default = 10 })
    api_key = string("API key", { secret = true })
  }

  task "research" {
    objective = "Research ${inputs.topic} and output in ${inputs.format}"
  }
}
```

See [Functions](/squadron/config/functions) for the complete reference on all helper functions, type references, and the options object (`default`, `secret`).

```bash
squadron mission report -c ./config --input topic="AI safety" --input format=html
```

## How Missions Execute

1. **Dependency Resolution** - Tasks are sorted topologically; dynamically activated tasks (router/send_to targets) are excluded from the initial sort
2. **Parallel Execution** - Independent tasks run concurrently
3. **Commander Creation** - Each task gets a commander
4. **Agent Delegation** - Commanders delegate to agents via `call_agent`
5. **Dynamic Activation** - After a task completes, its `send_to` targets fire immediately; if it has a `router`, the commander picks a branch
6. **Result Propagation** - Structured outputs are stored and queryable by downstream tasks

## Execution Flow

```
┌─────────────────┐
│   fetch_data    │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  process_data   │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ generate_report │
└─────────────────┘
```

## Running Missions

```bash
squadron mission data_pipeline -c ./config
```

With inputs:

```bash
squadron mission data_pipeline -c ./config --input source=api --input format=json
```

## Commander Tools

Commanders have access to special tools:

- **set_subtasks** / **complete_subtask** / **get_subtasks** - Plan and track work through ordered subtasks
- **call_agent** - Delegate work to an agent
- **ask_agent** - Ask a completed agent follow-up questions
- **submit_output** - Submit structured output matching the task's output schema
- **task_complete** - Signal that the task is done
- **query_task_output** - Query structured data from completed dependency tasks
- **ask_commander** - Query a dependency task's commander for more context
- **dataset_next** - Get the next item in sequential dataset processing
- **list_commander_questions** / **get_commander_answer** - Reuse answers from the shared question store (parallel dedup)

See [Internal Tools](/squadron/missions/internal-tools) for full details.

## Context Protection

When tools return large results (>8KB), Squadron automatically:

1. Stores the full data outside LLM context
2. Returns a sample to the LLM
3. Provides tools (`result_items`, `result_chunk`, etc.) to access more

This prevents context overflow while preserving full data access. Large arrays can be promoted to datasets using `result_to_dataset` for iteration.

See [Datasets](/squadron/missions/datasets#large-result-handling) for details.

## Structured Outputs

Tasks can define output schemas to capture structured data:

```hcl
task "analyze" {
  objective = "Analyze the data"

  output {
    field "count" {
      type     = "integer"
      required = true
    }
    field "average" {
      type = "number"
    }
  }
}
```

Downstream tasks can query this data using `query_task_output` with filtering and aggregation. See [Tasks](/squadron/missions/tasks#structured-output) for details.

## Persistence & Resume

Mission state is automatically persisted to SQLite during execution. If a mission fails or is interrupted, you can resume it:

```bash
squadron mission data_pipeline -c ./config --resume <mission-id>
```

Resume skips completed tasks and picks up interrupted tasks from where they left off — including restoring LLM conversation state for commanders and agents.

See [squadron mission](/squadron/cli/mission#resume) for details.

## See Also

- [Tasks](/squadron/missions/tasks) - Task configuration and dependencies
- [Routing](/squadron/missions/routing) - Conditional and unconditional task routing
- [Datasets](/squadron/missions/datasets) - Working with data collections
- [Iteration](/squadron/missions/iteration) - Processing lists of items
- [Schedules & Triggers](/squadron/missions/schedules) - Automatic scheduling and webhooks
- [Internal Tools](/squadron/missions/internal-tools) - Commander and agent tools
