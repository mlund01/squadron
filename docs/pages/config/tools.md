---
title: Tools
---

# Tools

Tools extend agent capabilities. Squadron provides built-in tools and supports custom tool definitions.

## Built-in Tools

### Bash

Execute shell commands:

```hcl
tools = [plugins.bash.bash]
```

### HTTP

Make HTTP requests:

```hcl
tools = [
  plugins.http.get,
  plugins.http.post,
  plugins.http.put,
  plugins.http.patch,
  plugins.http.delete
]
```

## Custom Tools

Custom tools wrap built-in or plugin tools with custom schemas and transformations.

### Basic Example

```hcl
tool "weather" {
  implements  = plugins.http.get
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
| `implements` | reference | The underlying tool to wrap (e.g., `plugins.http.get`) |
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

### Field Expressions

Use `inputs.field_name` to reference input values in dynamic fields:

```hcl
tool "create_todo" {
  implements  = plugins.http.post
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

See [Plugins](/config/plugins) for more information.
