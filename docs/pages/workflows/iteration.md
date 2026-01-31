---
title: Iteration
---

# Iteration

Tasks can iterate over datasets to process multiple items.

## Basic Iteration

```hcl
task "process_city" {
  objective = "Get weather for ${item.name}, ${item.state}"

  iterator {
    dataset = datasets.city_list
  }
}
```

## Iterator Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `dataset` | string | Dataset to iterate over |
| `parallel` | bool | Run iterations in parallel (default: false) |
| `max_retries` | int | Max retry attempts per iteration on failure (default: 0) |
| `concurrency_limit` | int | Max concurrent iterations when parallel=true (default: 5). Only valid with `parallel = true`. |
| `start_delay` | int | Milliseconds delay between starts in first concurrent batch (default: 0). Only valid with `parallel = true`. |
| `smoketest` | bool | Run first iteration completely before starting others; skip remaining if first fails (default: false). Only valid with `parallel = true`. |

## The `item` Variable

Inside an iterated task, `item` refers to the current dataset item:

```hcl
task "send_notification" {
  objective = "Send notification to ${item.email} about ${item.topic}"

  iterator {
    dataset = datasets.notifications
  }
}
```

If items are objects with fields, access them as `item.field_name`.

## Sequential vs Parallel

### Sequential (Default)

Items are processed one at a time:

```hcl
iterator {
  dataset  = datasets.items
  parallel = false  # Default
}
```

- Predictable order
- Fail-fast on first error (after retries exhausted)
- Access to previous iteration's output (see below)

### Parallel

All items are processed concurrently:

```hcl
iterator {
  dataset  = datasets.items
  parallel = true
}
```

- Faster for independent operations
- All iterations run simultaneously
- Each iteration retries independently before overall failure

#### Parallel Options

Control parallel execution behavior with these options:

```hcl
iterator {
  dataset           = datasets.items
  parallel          = true
  concurrency_limit = 5     # Max 5 iterations running at once
  start_delay       = 500   # 500ms delay between starts in first batch
  smoketest         = true  # Run first iteration before starting others
}
```

**concurrency_limit**: Limits how many iterations run simultaneously. Useful for rate-limited APIs or resource constraints.

**start_delay**: Staggers the start of iterations in the first concurrent batch. For example, with `start_delay = 500` and `concurrency_limit = 5`:
- Iteration 0 starts immediately
- Iteration 1 starts at +500ms
- Iteration 2 starts at +1000ms
- etc.

This helps when iterations need to populate shared caches before others start.

**smoketest**: Runs the first iteration completely before starting the rest. If the first iteration fails (after retries), the remaining iterations are skipped. Useful for catching configuration errors early without wasting resources on doomed iterations.

## Previous Iteration Context (Sequential Only)

In sequential mode, each iteration has access to the previous iteration's structured output. **Important:** Each iteration processes a *different* item from the dataset—the previous output is from a different dataset item, not the same one.

This enables use cases like:

- **Pagination**: Pass cursor/offset to the next iteration
- **Web crawling**: Track visited URLs across iterations
- **Cumulative state**: Build up results across all items

### How It Works

When running sequentially, the supervisor receives the previous iteration's `output` in its system prompt:

```
## Previous Iteration Output

You are processing one item in a sequential iteration. The PREVIOUS item (a different item from the dataset) produced this output:
{
  "cursor": "abc123",
  "items_processed": 50
}

This is NOT the same item you are processing now - it's from the previous dataset item.
Use this context only if relevant (e.g., pagination cursors, cumulative state, or patterns to follow).
Your current task is for a NEW item with its own parameters.
```

The first iteration has no previous context. Parallel iterations do not receive previous context (since ordering is non-deterministic).

### Example: Paginated API

```hcl
task "fetch_all_pages" {
  objective = <<-EOT
    Fetch the next page of results from the API.
    If there is a cursor from the previous iteration, use it to continue pagination.
    Otherwise, start from the first page.
    Store the results and extract the next_cursor for the following iteration.
  EOT

  iterator {
    dataset     = datasets.page_requests
    parallel    = false  # Required for previous context
    max_retries = 2
  }

  output {
    field "next_cursor" {
      type        = "string"
      description = "Cursor for next page, null if no more pages"
    }
    field "items_count" {
      type = "number"
    }
  }
}
```

### Example: Web Crawling

```hcl
task "crawl_site" {
  objective = <<-EOT
    Continue crawling the website.
    Check the previous iteration's output for the last URL visited.
    Avoid revisiting pages. Process the next unvisited page.
  EOT

  iterator {
    dataset  = datasets.pages_to_crawl
    parallel = false
  }

  output {
    field "last_url" {
      type = "string"
    }
    field "visited_urls" {
      type = "array"
    }
  }
}
```

## Example: Weather Report

```hcl
workflow "midwest_weather" {
  supervisor_model = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  dataset "city_list" {
    description = "Midwest cities to check"
    schema {
      field "name"  { type = "string"; required = true }
      field "state" { type = "string" }
    }
  }

  task "load_cities" {
    objective = "Read cities from data.json and populate the city_list dataset"
  }

  task "get_weather" {
    objective  = "Get current weather for ${item.name}, ${item.state}"
    depends_on = [tasks.load_cities]

    iterator {
      dataset     = datasets.city_list
      parallel    = true
      max_retries = 2  # Retry failed iterations up to 2 times
    }

    output {
      field "temperature" {
        type     = "number"
        required = true
      }
      field "conditions" {
        type     = "string"
        required = true
      }
    }
  }

  task "analyze" {
    objective  = "Compare temperatures and find the coldest city"
    depends_on = [tasks.get_weather]
  }
}
```

## Error Handling

### Output Validation

When a task has an `output` schema with required fields, each iteration is validated:

- If required output fields are missing, the iteration is marked as failed
- This enables automatic retry without supervisor intervention

### Retry Behavior

Configure retries per iteration with `max_retries`:

```hcl
iterator {
  dataset     = datasets.items
  max_retries = 3  # Retry each iteration up to 3 times on failure
}
```

When an iteration fails:

1. If retries remain, the iteration is automatically retried
2. If all retries are exhausted, fail-fast behavior kicks in
3. Remaining iterations are cancelled (parallel) or skipped (sequential)
4. The task fails with the first unrecoverable error

### Empty Datasets

If a dataset is empty, the task completes immediately with a "No items to process" summary.

## Querying Iteration Supervisors

Dependent tasks can query specific iteration supervisors using `ask_supe` with the `index` parameter. This enables follow-up questions to the supervisor that processed a particular item.

### Getting Iteration Results

First, use `query_task_output` to get results from the iterated task. Each result includes an `index` field:

```json
{
  "task": "get_weather",
  "filters": [{"field": "conditions", "op": "eq", "value": "snowy"}]
}
// Returns: [{"city": "Chicago", "temperature": 28, "index": 0}, ...]
```

### Querying a Specific Iteration

Use the `index` from the query results to ask follow-up questions:

```json
{
  "task_name": "get_weather",
  "index": 0,
  "question": "What was the wind speed in Chicago?"
}
```

The iteration supervisor responds from its completed context—it remembers the full conversation with its agents and can query them for additional details.

### Example: Character Crossover

```hcl
task "create_backstories" {
  objective = "Create a backstory for ${item.name}, who is a ${item.role}"

  iterator {
    dataset = datasets.characters
  }

  output {
    field "origin" { type = "string"; required = true }
    field "secret_talent" { type = "string"; required = true }
  }
}

task "character_crossover" {
  depends_on = ["create_backstories"]

  objective = <<-EOT
    1. Use query_task_output to get all backstories
    2. Pick two interesting characters
    3. For each, use ask_supe with the character's index to ask:
       "How would this character react to meeting someone from another world?"
    4. Write a crossover scene using both answers
  EOT
}
```

## Streaming Output

During iteration, the CLI shows progress:

```
[2/3] get_weather (5 iterations, parallel)
  → [0] Processing Chicago, IL...
  → [1] Processing Detroit, MI...
  → [2] Processing Milwaukee, WI...
  ✓ [0] Complete
  ✓ [1] Complete
  ✓ [2] Complete
  → Aggregating summaries...
  ✓ Complete
```
