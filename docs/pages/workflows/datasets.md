---
title: Datasets
---

# Datasets

Datasets are collections of items that tasks can iterate over.

## Defining Datasets

```hcl
workflow "process_cities" {
  dataset "city_list" {
    description = "Cities to process"

    schema {
      field "name"  { type = "string"; required = true }
      field "state" { type = "string" }
    }
  }

  # Tasks can iterate over this dataset
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `description` | string | Documentation for the dataset |
| `schema` | block | Optional schema for validating items |
| `items` | list | Optional inline list of items |
| `bind_to` | expression | Optional input binding (e.g., `inputs.cities`) |

## Schema Definition

Define expected fields:

```hcl
schema {
  field "id" {
    type     = "integer"
    required = true
  }

  field "name" {
    type     = "string"
    required = true
  }

  field "metadata" {
    type = "object"
  }
}
```

### Field Types

- `string`
- `number`
- `integer`
- `boolean`
- `array`
- `object`

## Populating Datasets

Datasets can be populated in three ways:

### 1. Bind to Workflow Input

```hcl
workflow "process" {
  input "items" {
    type = "list"
  }

  dataset "item_list" {
    bind_to = inputs.items
  }
}
```

### 2. Inline Items

```hcl
dataset "regions" {
  items = [
    { name = "us-east-1" },
    { name = "us-west-2" },
    { name = "eu-west-1" }
  ]
}
```

### 3. Dynamic Population

Agents can populate datasets at runtime using the `set_dataset` tool:

```hcl
task "load_data" {
  objective = "Read cities from data.json and populate the city_list dataset"
}

task "process_cities" {
  depends_on = [tasks.load_data]
  iterator {
    dataset = datasets.city_list
  }
}
```

## Dataset Tools

When running in a workflow, agents automatically have access to:

- **set_dataset** - Populate a dataset with items
- **dataset_sample** - Get sample items from a dataset
- **dataset_count** - Get the number of items in a dataset

### set_dataset

```json
{
  "name": "city_list",
  "items": [
    {"name": "Chicago", "state": "IL"},
    {"name": "Detroit", "state": "MI"}
  ]
}
```

### dataset_sample

```json
{
  "name": "city_list",
  "count": 3
}
```

### dataset_count

```json
{
  "name": "city_list"
}
```

## Schema Validation

When a schema is defined, all items are validated:

```hcl
dataset "users" {
  schema {
    field "email" { type = "string"; required = true }
  }
}
```

Setting an item without `email` will fail validation.

## Large Result Handling

When tools return large results (>8KB), Squad automatically protects context by:

1. Storing the full result outside LLM context
2. Returning a sample/summary to the LLM
3. Providing tools for the LLM to access more data as needed

This prevents context overflow while preserving full access to the data.

### How It Works

```
Tool returns large JSON array (500 items)
         │
         ▼
   Intercepted & stored
         │
         ▼
LLM sees:
  <OBSERVATION>
  [{...}, {...}, ...]  (sample)
  </OBSERVATION>
  <OBSERVATION_METADATA>
  type: array
  id: _result_http_get_1
  partial: true
  total_items: 500
  shown_items: 5
  </OBSERVATION_METADATA>
```

### Result Tools

When a large result is intercepted, the LLM can use these tools:

| Tool | Purpose |
|------|---------|
| `result_info` | Get type and size of stored result |
| `result_items` | Get items from array by offset/count |
| `result_get` | Navigate object with dot path (e.g., `users.0.name`) |
| `result_keys` | Get keys of an object |
| `result_chunk` | Get text by offset/length |

### Promoting to Datasets

Use `result_to_dataset` to convert a large array result into a dataset for iteration:

```json
{
  "id": "_result_http_get_1",
  "dataset_name": "users"
}
```

After promotion, the data is available via standard dataset tools and can be iterated with `for_each`.

### Example Flow

1. Agent calls `http_get` → returns 500 users
2. Interceptor stores full array, LLM sees sample of 5
3. LLM examines sample, decides this is useful
4. LLM calls `result_to_dataset("_result_http_get_1", "users")`
5. Subsequent task iterates: `for_each = datasets.users`

## See Also

- [Iteration](/workflows/iteration) - Process datasets in tasks
