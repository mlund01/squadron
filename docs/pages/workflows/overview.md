---
title: Workflows Overview
---

# Workflows

Workflows orchestrate multi-task pipelines using supervisors and agents.

## Basic Structure

```hcl
workflow "data_pipeline" {
  supervisor_model = models.anthropic.claude_sonnet_4
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
| `supervisor_model` | string | Model for task supervisors |
| `agents` | list | Agents available to all tasks |

## How Workflows Execute

1. **Dependency Resolution** - Tasks are sorted topologically
2. **Parallel Execution** - Independent tasks run concurrently
3. **Supervisor Creation** - Each task gets a supervisor
4. **Agent Delegation** - Supervisors delegate to agents via `call_agent`
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

## Running Workflows

```bash
squad run -c ./config data_pipeline
```

With inputs:

```bash
squad run -c ./config data_pipeline --input source=api --input format=json
```

## Supervisor Tools

Supervisors have access to special tools:

- **call_agent** - Delegate work to an agent
- **ask_agent** - Ask a completed agent follow-up questions
- **query_task_output** - Query structured data from completed dependency tasks

## Context Protection

When tools return large results (>8KB), Squad automatically:

1. Stores the full data outside LLM context
2. Returns a sample to the LLM
3. Provides tools (`result_items`, `result_chunk`, etc.) to access more

This prevents context overflow while preserving full data access. Large arrays can be promoted to datasets using `result_to_dataset` for iteration.

See [Datasets](/workflows/datasets#large-result-handling) for details.

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

Downstream tasks can query this data using `query_task_output` with filtering and aggregation. See [Tasks](/workflows/tasks#structured-output) for details.

## See Also

- [Tasks](/workflows/tasks) - Task configuration
- [Datasets](/workflows/datasets) - Working with data collections
- [Iteration](/workflows/iteration) - Processing lists of items
