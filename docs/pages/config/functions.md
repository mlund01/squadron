---
title: Functions
---

# Functions

Squadron provides built-in HCL functions for defining schemas concisely. These functions are available everywhere a schema can be defined: tool inputs, mission inputs, task outputs, and dataset schemas.

## Schema Helper Functions

Instead of verbose `field` blocks, use the shorthand `= { ... }` attribute form with schema helper functions. Both forms are fully equivalent.

### Primitives

`string`, `number`, `integer`, `bool` define scalar fields.

**Signature:** `type(description, required_or_options?)`

```hcl
inputs = {
  name    = string("Customer name", true)                    # required
  region  = string("AWS region", { default = "us-east-1" })  # optional with default
  count   = integer("Number of items", true)                 # required integer
  score   = number("Confidence score")                       # optional float
  verbose = bool("Enable verbose output", { default = false })
}
```

The second argument is optional:
- `true` marks the field as required
- An options object `{ default = value }` sets a default (making it optional)
- For mission inputs, `{ secret = true }` marks the field as sensitive

### list

Defines an ordered array of a given element type.

**Signature:** `list(inner_type, description, required?)`

```hcl
inputs = {
  tags    = list(string, "Labels to apply")              # list of strings
  scores  = list(number, "Numeric scores", true)         # required list of numbers
  mixed   = list(any, "Items of any type")               # heterogeneous list
  items   = list(object({                                # list of typed objects
    sku      = string("Product SKU", true)
    quantity = integer("Quantity", true)
  }), "Order line items", true)
}
```

The first argument is a [type reference](#type-references).

### map

Defines a free-form key-value mapping. Maps carry no field schema — use them for arbitrary data where the keys are not known ahead of time.

**Signature:** `map(value_type, description, required?)`

```hcl
inputs = {
  headers  = map(string, "HTTP headers to include")         # string values only
  counts   = map(number, "Counts by category", true)        # required, number values
  config   = map(any_primitive, "Flat configuration data")   # any primitive value type
  metadata = map(any, "Arbitrary data including nested")     # any value including objects
}
```

The first argument is a [type reference](#type-references).

### object

Defines a structured object with known properties. Objects are always schematic — the first argument is a properties definition. For free-form key-value data without a defined schema, use `map` instead.

**Signature:** `object(properties, description?, required?)`

```hcl
inputs = {
  address = object({
    street = string("Street address", true)
    city   = string("City", true)
    zip    = string("ZIP code")
  }, "Shipping address", true)

  coords = object({
    lat = number("Latitude", true)
    lon = number("Longitude", true)
  })
}
```

As a type reference inside `list`:

```hcl
line_items = list(object({
  sku      = string("Product SKU", true)
  quantity = integer("Quantity", true)
}), "Line items", true)
```

## Type References

Type references are bare identifiers used as the first argument to `list` and `map` to specify the element/value type.

| Reference | Description |
|-----------|-------------|
| `string` | String values |
| `number` | Floating-point numbers |
| `integer` | Whole numbers |
| `bool` | Boolean values |
| `any` | Any type — strings, numbers, objects, arrays, etc. |
| `any_primitive` | Any primitive — strings, numbers, integers, booleans (no nested objects or arrays) |
| `object({...})` | Inline object with defined properties |

## Options Object

Primitives accept an optional options object as the second argument instead of a boolean:

| Key | Type | Description |
|-----|------|-------------|
| `default` | any | Default value (makes the field optional) |
| `secret` | bool | Mark as sensitive — masked in logs and UI (mission inputs only) |

```hcl
inputs = {
  region  = string("AWS region", { default = "us-east-1" })
  api_key = string("API key", { secret = true })
  verbose = bool("Verbose mode", { default = false })
}
```

## Where Functions Are Used

Schema helper functions work in four contexts:

### Tool Inputs

```hcl
tool "weather" {
  implements  = builtins.http.get
  description = "Get weather for a city"

  inputs = {
    city = string("City name", true)
  }

  url = "https://wttr.in/${inputs.city}?format=3"
}
```

See [Tools](/squadron/config/tools) for full tool configuration.

### Mission Inputs

```hcl
mission "report" {
  inputs = {
    topic   = string("The topic to research")
    format  = string("Output format", { default = "markdown" })
    api_key = string("API key", { secret = true })
  }
}
```

See [Missions](/squadron/missions/overview) for full mission configuration.

### Task Outputs

```hcl
task "analyze" {
  objective = "Analyze the data"

  output = {
    summary    = string("Analysis summary", true)
    confidence = number("Confidence score", true)
    tags       = list(string, "Relevant tags")
  }
}
```

See [Tasks](/squadron/missions/tasks) for full task configuration.

### Dataset Schemas

```hcl
dataset "orders" {
  schema = {
    id      = integer("Order ID", true)
    status  = string("Order status", true)
    address = object({
      city    = string("City", true)
      country = string("Country code", true)
    }, "Shipping address")
  }
}
```

See [Datasets](/squadron/missions/datasets) for full dataset configuration.

## Full Example

A single tool using every function type:

```hcl
tool "process_order" {
  implements  = builtins.http.post
  description = "Submit a customer order"

  inputs = {
    order_id   = string("Order identifier", true)
    total      = number("Order total in USD", true)
    express    = bool("Use express shipping", { default = false })
    tags       = list(string, "Order labels")
    metadata   = map(any_primitive, "Arbitrary order metadata")
    address    = object({
      street = string("Street address", true)
      city   = string("City", true)
      zip    = string("ZIP code")
    }, "Shipping address", true)
    line_items = list(object({
      sku      = string("Product SKU", true)
      quantity = integer("Quantity", true)
    }), "Line items", true)
  }

  url  = "https://api.example.com/orders"
  body = { order_id = inputs.order_id }
}
```
