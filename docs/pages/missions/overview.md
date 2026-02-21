---
title: Missions Overview
---

# Missions

Missions orchestrate multi-task pipelines using commanders and agents.

## Basic Structure

```hcl
mission "data_pipeline" {
  commander = models.anthropic.claude_sonnet_4
  agents           = [agents.researcher, agents.writer]

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
| `commander` | string | Model for task commanders |
| `agents` | list | Agents available to all tasks |

## How Missions Execute

1. **Dependency Resolution** - Tasks are sorted topologically
2. **Parallel Execution** - Independent tasks run concurrently
3. **Commander Creation** - Each task gets a commander
4. **Agent Delegation** - Commanders delegate to agents via `call_agent`
5. **Result Propagation** - Task summaries flow to dependents

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

- **call_agent** - Delegate work to an agent
- **ask_agent** - Ask a completed agent follow-up questions
- **query_task_output** - Query structured data from completed dependency tasks
- **ask_commander** - Query a dependency task's commander for more context
- **list_commander_questions** - See questions already asked by other iterations (parallel dedup)
- **get_commander_answer** - Get a cached answer from the shared question store

See [Internal Tools](/missions/internal-tools) for full details.

## Context Protection

When tools return large results (>8KB), Squadron automatically:

1. Stores the full data outside LLM context
2. Returns a sample to the LLM
3. Provides tools (`result_items`, `result_chunk`, etc.) to access more

This prevents context overflow while preserving full data access. Large arrays can be promoted to datasets using `result_to_dataset` for iteration.

See [Datasets](/missions/datasets#large-result-handling) for details.

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

Downstream tasks can query this data using `query_task_output` with filtering and aggregation. See [Tasks](/missions/tasks#structured-output) for details.

## Persistence & Resume

Mission state is automatically persisted to SQLite during execution. If a mission fails or is interrupted, you can resume it:

```bash
squadron mission data_pipeline -c ./config --resume <mission-id>
```

Resume skips completed tasks and picks up interrupted tasks from where they left off — including restoring LLM conversation state for commanders and agents.

See [squadron mission](/cli/mission#resume) for details.

## See Also

- [Tasks](/missions/tasks) - Task configuration
- [Datasets](/missions/datasets) - Working with data collections
- [Iteration](/missions/iteration) - Processing lists of items
