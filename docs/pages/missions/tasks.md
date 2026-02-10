---
title: Tasks
---

# Tasks

Tasks are the building blocks of missions. Each task has an objective and can depend on other tasks.

## Basic Task

```hcl
task "analyze" {
  objective = "Analyze the data and identify trends"
}
```

## Task with Dependencies

```hcl
task "summarize" {
  objective  = "Summarize the analysis results"
  depends_on = [tasks.analyze]
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `objective` | string | What the task should accomplish |
| `depends_on` | list | Tasks that must complete first |
| `agents` | list | Override mission-level agents (optional) |
| `output` | block | Structured output schema (optional) |

## Dependencies

Tasks can depend on multiple tasks:

```hcl
task "combine" {
  objective  = "Combine results from both sources"
  depends_on = [tasks.fetch_source_a, tasks.fetch_source_b]
}
```

Dependencies are specified using `tasks.<task_name>`.

## Task-Level Agents

Override the mission's agents for specific tasks:

```hcl
mission "pipeline" {
  agents = [agents.general]

  task "code_review" {
    objective = "Review the code changes"
    agents    = [agents.coder]  # Use specialized agent
  }
}
```

## Dynamic Objectives

Use variables and inputs in objectives:

```hcl
mission "report" {
  input "topic" {
    type = "string"
  }

  task "research" {
    objective = "Research the topic: ${inputs.topic}"
  }
}
```

## Commander Behavior

Each task gets a commander that:

1. Receives the objective
2. Reasons about how to accomplish it
3. Delegates to agents using `call_agent`
4. Synthesizes a final summary

## Context from Dependencies

Commanders receive summaries from completed dependencies:

```hcl
task "step_2" {
  objective  = "Continue based on step 1 results"
  depends_on = [tasks.step_1]
}
```

The step_2 commander sees step_1's summary and can query structured data using `query_task_output`.

## Structured Output

Tasks can define a structured output schema. When defined, the commander must output data matching the schema:

```hcl
task "analyze_sales" {
  objective  = "Analyze Q4 sales data"
  depends_on = [tasks.fetch_sales]

  output {
    field "total_revenue" {
      type        = "number"
      description = "Total revenue in USD"
      required    = true
    }
    field "top_product" {
      type        = "string"
      description = "Best-selling product name"
      required    = true
    }
    field "growth_rate" {
      type = "number"
    }
  }
}
```

### Field Types

| Type | Description |
|------|-------------|
| `string` | Text value |
| `number` | Numeric value (float) |
| `integer` | Whole number |
| `boolean` | True/false |

Structured output is automatically captured and stored. Downstream tasks can query it using the `query_task_output` tool (see [Internal Tools](/missions/internal-tools)).
