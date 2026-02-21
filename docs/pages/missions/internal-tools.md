---
title: Internal Tools
---

# Internal Tools

Missions use specialized internal tools that enable communication between commanders and agents. These tools are automatically available during mission execution and are managed by the system.

> **Note:** These tools are used internally by the mission runtime. You do not need to configure or reference them in your mission definitions—they are automatically available to commanders and agents as needed.

## Commander Tools

Commanders have access to tools for delegating work to agents and querying data from completed tasks.

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

The commander waits for the agent to complete and receives the result.

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

### ask_commander

Ask a follow-up question to a completed commander from a dependency task. Use this when you need more details than what was provided in the task summary.

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

The queried commander will answer from its existing context and can use `ask_agent` to query its own agents if needed.

#### Querying Iterated Tasks

For tasks that iterate over a dataset, use the `index` parameter to query a specific iteration's commander. Get the index from `query_task_output` results—each iteration has an `index` field.

```json
{
  "task_name": "process_cities",
  "index": 2,
  "question": "Why was this city flagged for review?"
}
```

**Context behavior:** The first query to a commander creates a clone from its completed state. Subsequent queries to the same commander build on previous questions and answers, enabling natural follow-up conversations.

### ask_agent

Query an agent that was used by a dependency task. Use this to get additional information from agents that have already executed and have relevant context.

```json
{
  "agent_id": "assistant",
  "question": "What API endpoints did you use?"
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `agent_id` | string | Name of the agent (matches the name used in `call_agent`) (required) |
| `question` | string | Question to ask the agent (required) |

The agent responds from its existing conversation context without making new tool calls.

### list_commander_questions

List questions that have already been asked to a dependency task's commander. Useful in parallel iterations to avoid asking duplicate questions.

```json
{
  "task_name": "fetch_sales"
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `task_name` | string | Name of the dependency task (required) |

Returns a list of previously asked questions with their indices.

### get_commander_answer

Get a cached answer for a previously asked question by its index. Use with `list_commander_questions` to reuse answers from other iterations.

```json
{
  "task_name": "fetch_sales",
  "index": 0
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `task_name` | string | Name of the dependency task (required) |
| `index` | integer | Index of the question from `list_commander_questions` (required) |

## Agent Tools

Agents have access to the tools configured in their agent definition, plus mission-level dataset tools when running in mission context. See [Tools](/config/tools) for configuring agent tools.

### Dataset Tools

When running inside a mission, agents automatically get these dataset tools:

| Tool | Description |
|------|-------------|
| `set_dataset` | Populate a dataset with items |
| `dataset_sample` | Get sample items from a dataset |
| `dataset_count` | Get the number of items in a dataset |

See [Datasets](/missions/datasets) for details and examples.
