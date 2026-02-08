---
title: run
---

# squadron run

Execute a workflow.

## Usage

```bash
squadron run -c <config-path> <workflow-name> [--input key=value...]
```

## Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to the configuration directory (required) |
| `--input` | Input values for the workflow (can be repeated) |

## Arguments

| Argument | Description |
|----------|-------------|
| `workflow-name` | Name of the workflow to execute |

## Example

```bash
# Run a workflow
squadron run -c ./my-config data_pipeline

# Run with inputs
squadron run -c ./my-config weather_report --input city=Chicago --input units=celsius
```

## Workflow Execution

When a workflow runs:

1. Tasks execute in dependency order (topological sort)
2. Independent tasks may run in parallel
3. Each task gets a supervisor that orchestrates agents
4. Agents execute subtasks and report back to the supervisor
5. Results flow to dependent tasks

## Output

The CLI displays real-time progress:

```
Workflow: data_pipeline
━━━━━━━━━━━━━━━━━━━━━━━━

[1/3] fetch_data
  → Supervisor reasoning...
  → Calling agent: assistant
    → Tool: plugins.http.get
  ✓ Complete

[2/3] process_data
  → Supervisor reasoning...
  ...
```

## See Also

- [Workflows Overview](/workflows/overview)
- [Tasks](/workflows/tasks)
- [Datasets](/workflows/datasets)
