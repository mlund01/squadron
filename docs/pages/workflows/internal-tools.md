---
title: Internal Tools
---

# Internal Tools

Workflows use specialized internal tools that enable communication between supervisors and agents. These tools are automatically available during workflow execution and are managed by the system.

> **Note:** These tools are used internally by the workflow runtime. You do not need to configure or reference them in your workflow definitions—they are automatically available to supervisors and agents as needed.

## Supervisor Tools

Supervisors have access to tools for delegating work to agents and querying data from completed tasks.

### call_agent

Delegate a task to an agent.

```json
{
  "name": "assistant",
  "task": "Fetch the current weather for Chicago, IL"
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `name` | string | Name of the agent to call (required) |
| `task` | string | Task description for the agent (required) |

The supervisor waits for the agent to complete and receives the result.

### query_task_output

Query structured data from completed dependency tasks.

```json
{
  "task": "fetch_sales",
  "filters": [{"field": "amount", "op": "gt", "value": 1000}],
  "limit": 10
}
```

#### Query Options

| Option | Description |
|--------|-------------|
| `task` | Task name to query (required) |
| `filters` | Array of filter conditions |
| `item_ids` | Specific item IDs for iterated tasks |
| `limit` | Maximum results (default: 20) |
| `offset` | Skip N results |
| `order_by` | Field to sort by |
| `desc` | Sort descending |
| `aggregate` | Aggregate operation |

#### Filter Operators

| Operator | Description |
|----------|-------------|
| `eq` | Equal to |
| `ne` | Not equal to |
| `gt` | Greater than |
| `lt` | Less than |
| `gte` | Greater than or equal |
| `lte` | Less than or equal |
| `contains` | String contains |

#### Aggregate Operations

```json
{
  "task": "get_weather",
  "aggregate": {
    "op": "avg",
    "field": "temperature"
  }
}
```

| Operation | Description |
|-----------|-------------|
| `count` | Count matching items |
| `sum` | Sum of field values |
| `avg` | Average of field values |
| `min` | Minimum value (returns item) |
| `max` | Maximum value (returns item) |
| `distinct` | Unique values |
| `group_by` | Group and aggregate |

#### Group By Example

```json
{
  "task": "get_weather",
  "aggregate": {
    "op": "group_by",
    "group_by": "state",
    "group_op": "avg",
    "field": "temperature"
  }
}
```

### populate_dataset

Add items to a dataset for iteration.

```json
{
  "dataset": "city_list",
  "items": [
    {"name": "Chicago", "state": "IL"},
    {"name": "Detroit", "state": "MI"}
  ]
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `dataset` | string | Name of the dataset to populate (required) |
| `items` | array | Array of items to add (required) |

Items must match the dataset's schema if one is defined.

## Agent Tools

Agents have access to the tools configured in their agent definition, plus any workflow-level tools. See [Tools](/config/tools) for configuring agent tools.

During workflow execution, agents operate autonomously to complete the tasks delegated by supervisors. They use their configured tools (bash, HTTP, custom tools, etc.) to accomplish objectives.
