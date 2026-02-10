---
title: Internal Tools
---

# Internal Tools

Missions use specialized internal tools that enable communication between supervisors and agents. These tools are automatically available during mission execution and are managed by the system.

> **Note:** These tools are used internally by the mission runtime. You do not need to configure or reference them in your mission definitions—they are automatically available to supervisors and agents as needed.

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

### ask_supe

Ask a follow-up question to a completed supervisor from a dependency task. Use this when you need more details than what was provided in the task summary.

```json
{
  "task_name": "fetch_sales",
  "question": "What was the average order value for premium customers?"
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `task_name` | string | Name of the completed dependency task (required) |
| `question` | string | Follow-up question to ask (required) |
| `index` | integer | For iterated tasks: the iteration index to query |

The queried supervisor will answer from its existing context and can use `ask_agent` to query its own agents if needed.

#### Querying Iterated Tasks

For tasks that iterate over a dataset, use the `index` parameter to query a specific iteration's supervisor. Get the index from `query_task_output` results—each iteration has an `index` field.

```json
{
  "task_name": "process_cities",
  "index": 2,
  "question": "Why was this city flagged for review?"
}
```

**Context behavior:** The first query to a supervisor creates a clone from its completed state. Subsequent queries to the same supervisor build on previous questions and answers, enabling natural follow-up conversations.

### ask_agent

Query an agent that was used by a dependency task. Use this to get additional information from agents that have already executed and have relevant context.

```json
{
  "agent_id": "agent_1_assistant",
  "question": "What API endpoints did you use?"
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `agent_id` | string | ID of the agent (from call_agent results) (required) |
| `question` | string | Question to ask the agent (required) |

The agent responds from its existing conversation context without making new tool calls.

## Agent Tools

Agents have access to the tools configured in their agent definition, plus any mission-level tools. See [Tools](/config/tools) for configuring agent tools.

During mission execution, agents operate autonomously to complete the tasks delegated by supervisors. They use their configured tools (bash, HTTP, custom tools, etc.) to accomplish objectives.
