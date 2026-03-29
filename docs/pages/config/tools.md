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

For concise definitions, you can use the shorthand `inputs = { ... }` attribute form with schema helper functions:

```hcl
tool "weather" {
  implements  = builtins.http.get
  description = "Get current weather"

  inputs = {
    city  = string("City name", true)
    units = string("Unit system", { default = "metric" })
    days  = number("Forecast days", { default = 3 })
  }

  url = "https://wttr.in/${inputs.city}?format=3"
}
```

Available helper functions: `string`, `number`, `integer`, `bool`, `list`, `map`, `object`. Pass `true` as the second argument to mark a field as required, or an options object to set a default:

```hcl
inputs = {
  required_field   = string("Always needed", true)
  optional_default = string("Has a fallback", { default = "fallback" })
  optional_field   = number("No default, not required")
}
```

Both the block form and the shorthand are fully equivalent.

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
