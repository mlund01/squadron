---
title: Internal Tools
---

# Internal Tools

Missions use specialized internal tools that enable communication between commanders and agents. These tools are automatically available during mission execution and are managed by the system.

> **Note:** These tools are used internally by the mission runtime. You do not need to configure or reference them in your mission definitions — they are automatically available to commanders and agents as needed.

## Commander Tools

Commanders have access to tools for planning work, delegating to agents, submitting outputs, and querying data from completed tasks.

### Subtask Planning

Every commander must plan its work using subtasks before doing anything else.

#### set_subtasks

Define the ordered subtasks for the current task. This **must** be the commander's first action.

```json
{
  "subtasks": ["Research the API endpoints", "Implement data extraction", "Validate results"]
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `subtasks` | array of strings | Ordered list of 1-10 subtask titles (required) |

Once any subtask has been completed, the plan is locked and `set_subtasks` cannot be called again. For sequential dataset tasks, `set_subtasks` can be called again after each `dataset_next` advancement.

#### get_subtasks

Get all subtasks with their current status.

```json
{}
```

Returns each subtask's index, title, and status (`pending`, `in_progress`, or `completed`).

#### complete_subtask

Mark the current subtask as completed and advance to the next one.

```json
{}
```

Completes the first non-completed subtask in order. All subtasks must be completed before calling `task_complete`.

### Task Completion

#### task_complete

Signal that the task is done. Call this when all subtasks are completed and all work is finished.

```json
{}
```

**With routing:** If the task has a `router` block, `task_complete` triggers a two-phase routing flow instead of immediately completing:

1. **First call** — returns route options:
```json
{
  "status": "routing",
  "message": "Choose the most appropriate route, or 'none'.",
  "options": [
    {"key": "handle_billing", "condition": "The request is about billing"},
    {"key": "handle_support", "condition": "The request is technical support"},
    {"key": "none", "condition": "No route applies"}
  ]
}
```

2. **Second call** — commander selects a route:
```json
{
  "route": "handle_billing"
}
```

For **cross-mission routes**, the options include `required_inputs` and the second call includes `mission_inputs`:

```json
{
  "route": "escalation_mission",
  "mission_inputs": {
    "complaint_summary": "Customer demands refund for defective product",
    "severity": "critical"
  }
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `route` | string | Key of the chosen route, or `"none"` (optional — only for routing tasks) |
| `mission_inputs` | object | Input values for mission route targets (optional — only for cross-mission routes) |

See [Routing](/squadron/missions/routing) for full details.

#### submit_output

Submit structured output for the task. Only available when the task defines an `output` schema.

```json
{
  "output": {
    "total_revenue": 150000,
    "top_product": "Widget Pro"
  }
}
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `output` | object | The structured output matching the task's output schema (required) |

For iterated tasks, `submit_output` is called once per item. Required output fields are validated automatically.

### Agent Delegation

#### call_agent

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

#### ask_agent

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

### Data Querying

#### query_task_output

Query structured data from completed dependency tasks.

```json
{
  "task": "fetch_sales",
  "filters": [{"field": "amount", "op": "gt", "value": 1000}],
  "limit": 10
}
```

##### Query Options

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

##### Filter Operators

| Operator | Description |
|----------|-------------|
| `eq` | Equal to |
| `ne` | Not equal to |
| `gt` | Greater than |
| `lt` | Less than |
| `gte` | Greater than or equal |
| `lte` | Less than or equal |
| `contains` | String contains |

##### Aggregate Operations

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

##### Group By Example

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

#### ask_commander

Ask a follow-up question to a completed commander from a dependency task. Use this when you need more details than what's available in the structured output.

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

##### Querying Iterated Tasks

For tasks that iterate over a dataset, use the `index` parameter to query a specific iteration's commander. Get the index from `query_task_output` results — each iteration has an `index` field.

```json
{
  "task_name": "process_cities",
  "index": 2,
  "question": "Why was this city flagged for review?"
}
```

**Context behavior:** The first query to a commander creates a clone from its completed state. Subsequent queries to the same commander build on previous questions and answers, enabling natural follow-up conversations.

#### list_commander_questions

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

#### get_commander_answer

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

### Sequential Dataset Processing

These tools are available when a task iterates over a dataset sequentially (`parallel = false`).

#### dataset_next

Get the next item from the dataset for sequential processing.

```json
{}
```

Returns the next item with its index and total count. Returns `{"status": "exhausted"}` when all items have been processed.

**Constraints:**
- `submit_output` must be called for the current item before advancing
- All subtasks must be completed before advancing (if subtasks are defined)

## Agent Tools

Agents have access to the tools configured in their agent definition, plus mission-level dataset tools when running in mission context. See [Tools](/squadron/config/tools) for configuring agent tools.

### Dataset Tools

When running inside a mission, agents automatically get these dataset tools:

| Tool | Description |
|------|-------------|
| `set_dataset` | Populate a dataset with items |
| `dataset_sample` | Get sample items from a dataset |
| `dataset_count` | Get the number of items in a dataset |
| `result_to_dataset` | Convert a large intercepted result into a dataset for iteration |

See [Datasets](/squadron/missions/datasets) for details and examples.
