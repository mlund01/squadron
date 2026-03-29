---
title: Tools
---

# Tools

Tools extend agent capabilities. Squadron provides built-in tools and supports custom tool definitions.

## Built-in Tools

### HTTP

Make HTTP requests:

```hcl
tools = [
  builtins.http.get,
  builtins.http.post,
  builtins.http.put,
  builtins.http.patch,
  builtins.http.delete
]
```

### Utils

Utility tools:

```hcl
tools = [builtins.utils.sleep]
```

## Custom Tools

Custom tools wrap built-in or plugin tools with custom schemas and transformations.

### Basic Example

```hcl
tool "weather" {
  implements  = builtins.http.get
  description = "Get weather for a city"

  inputs {
    field "city" {
      type        = "string"
      description = "City name"
      required    = true
    }
  }

  url = "https://wttr.in/${inputs.city}?format=3"
}
```

### Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `implements` | reference | The underlying tool to wrap (e.g., `builtins.http.get`) |
| `description` | string | Description shown to the agent |
| `inputs` | block | Input schema definition |

### Input Fields

Input fields are defined using `field` blocks inside the `inputs` block:

```hcl
inputs {
  field "field_name" {
    type        = "string"   # string, number, integer, boolean, array, object
    description = "Field description"
    required    = true       # or false
  }
}
```

### Shorthand Schema Syntax

For concise definitions, use the shorthand `inputs = { ... }` attribute form with schema helper functions. Both forms are fully equivalent.

#### Primitives — `string` `number` `integer` `bool`

```hcl
inputs = {
  name    = string("Customer name", true)               # required
  region  = string("AWS region", { default = "us-east-1" }) # optional with default
  count   = integer("Number of items", true)            # required integer
  score   = number("Confidence score")                  # optional float
  verbose = bool("Enable verbose output", { default = false })
}
```

Passing `true` as the second argument marks the field required. Pass an options object `{ default = ... }` to set a default value (making it optional).

#### Lists — `list(inner_type, description, required?)`

```hcl
inputs = {
  tags    = list(string, "Labels to apply")            # list of strings
  scores  = list(number, "Numeric scores", true)       # required list of numbers
}
```

The first argument is a type reference (`string`, `number`, `integer`, `bool`) or a nested `object({...})`.

#### Maps — `map(value_type, description, required?)`

`map` is free-form and carries no field schema — use it for arbitrary key-value data:

```hcl
inputs = {
  headers  = map(string, "HTTP headers to include")    # free-form string map
  counts   = map(number, "Counts by category", true)   # required number map
}
```

#### Objects — `object(properties, description, required?)`

`object` is schematic — the first argument defines the nested field layout:

```hcl
inputs = {
  address = object({
    street = string("Street address", true)
    city   = string("City", true)
    zip    = string("ZIP code")
  }, "Shipping address", true)
}
```

Nest `object({...})` inside `list()` for lists of typed objects:

```hcl
inputs = {
  line_items = list(object({
    sku      = string("Product SKU", true)
    quantity = integer("Item quantity", true)
    price    = number("Unit price")
  }), "Order line items", true)
}
```

#### Full example

```hcl
tool "process_order" {
  implements  = builtins.http.post
  description = "Submit a customer order"

  inputs = {
    order_id   = string("Order identifier", true)
    total      = number("Order total in USD", true)
    express    = bool("Use express shipping", { default = false })
    tags       = list(string, "Order labels")
    metadata   = map(string, "Arbitrary order metadata")
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

### Field Expressions

Use `inputs.field_name` to reference input values in dynamic fields:

```hcl
tool "create_todo" {
  implements  = builtins.http.post
  description = "Create a new todo item"

  inputs {
    field "title" {
      type        = "string"
      description = "The title of the todo"
      required    = true
    }
    field "priority" {
      type        = "string"
      description = "Priority level (low, medium, high)"
      required    = false
    }
  }

  url  = "https://jsonplaceholder.typicode.com/todos"
  body = {
    title     = inputs.title
    completed = false
    userId    = 1
  }
}
```

### Wrapping Plugin Tools

Custom tools can wrap external plugin tools:

```hcl
tool "shout" {
  implements  = plugins.pinger.echo
  description = "Echo a message in ALL CAPS"

  inputs {
    field "text" {
      type        = "string"
      description = "The text to shout"
      required    = true
    }
  }

  message  = inputs.text
  all_caps = true
}
```

### Referencing Custom Tools

```hcl
agent "assistant" {
  tools = [
    tools.weather,
    tools.create_todo
  ]
}
```

## Plugin Tools

External plugins can provide additional tools:

```hcl
plugin "slack" {
  source  = "~/.squadron/plugins/slack"
  version = "local"
}

agent "notifier" {
  tools = [plugins.slack.send_message]
}
```

See [Plugins](/squadron/config/plugins) for more information.
