---
title: Agents
---

# Agents

Agents are AI assistants that can chat with users and execute tools.

## Defining Agents

```hcl
agent "assistant" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Friendly, helpful, and concise"
  role        = "General purpose assistant"
  tools       = [
    plugins.bash.bash,
    plugins.http.get,
    tools.weather
  ]
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `model` | reference | Model reference (e.g., `models.anthropic.claude_sonnet_4`) |
| `personality` | string | Personality traits for the agent |
| `role` | string | Description of the agent's purpose |
| `tools` | list | Tools available to the agent (optional) |

## Tools

Agents can use three types of tools:

### Built-in Plugin Tools

```hcl
tools = [
  plugins.bash.bash,      # Shell commands
  plugins.http.get,       # HTTP GET
  plugins.http.post,      # HTTP POST
  plugins.http.put,       # HTTP PUT
  plugins.http.patch,     # HTTP PATCH
  plugins.http.delete,    # HTTP DELETE
]
```

### Custom Tools

Reference tools defined in `tool` blocks:

```hcl
tools = [
  tools.weather,
  tools.create_todo
]
```

### External Plugin Tools

Reference tools from loaded plugins:

```hcl
tools = [
  plugins.slack.send_message,
  plugins.github.create_issue
]
```

## Example: Specialized Agents

```hcl
agent "coder" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Precise and methodical"
  role        = "Software development assistant"
  tools       = [plugins.bash.bash]
}

agent "researcher" {
  model       = models.openai.gpt_4o
  personality = "Curious and thorough"
  role        = "Research and information gathering"
  tools       = [plugins.http.get]
}

agent "writer" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Creative and articulate"
  role        = "Content writing and editing"
  tools       = []  # No tools, just conversation
}
```

## Built-in Tools

All agents automatically have access to result tools for handling large data:

| Tool | Purpose |
|------|---------|
| `result_info` | Get type/size of a stored large result |
| `result_items` | Get items from a large array |
| `result_get` | Navigate large objects with dot paths |
| `result_keys` | Get keys of a large object |
| `result_chunk` | Get chunks of large text |

When any tool returns a large result (>8KB), it's automatically stored and a sample is shown. The agent can use these tools to access more data without overwhelming context.

In workflow context, `result_to_dataset` is also available to promote arrays to datasets.
