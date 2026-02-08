---
title: Plugins
---

# Plugins

Plugins extend Squad with additional tools and capabilities.

## Loading Plugins

```hcl
plugin "pinger" {
  source  = "~/.squadron/plugins/pinger"
  version = "local"
}

plugin "slack" {
  source = "/path/to/slack-plugin"
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `source` | string | Path to the plugin binary or directory |
| `version` | string | Plugin version (e.g., `"local"` for local plugins) |

## Using Plugin Tools

Once loaded, plugin tools are available as `plugins.<plugin_name>.<tool_name>`:

```hcl
agent "assistant" {
  tools = [
    plugins.pinger.echo,
    plugins.slack.send_message
  ]
}
```

## Plugin Protocol

Plugins communicate with Squad over stdin/stdout using JSON-RPC. A plugin must implement:

### Tool Discovery

Return available tools and their schemas.

### Tool Execution

Execute a tool with given parameters and return results.

## Reserved Namespaces

The following plugin namespaces are reserved for built-in tools:

- `bash` - Shell command execution
- `http` - HTTP requests
- `dataset` - Dataset operations (workflow context only)

You cannot create plugins with these names.

## Creating Plugins

Plugins are executables that implement the Squad plugin protocol. Example structure:

```
my-plugin/
├── main.go       # Plugin entry point
└── tools/
    └── mytool.go # Tool implementation
```

The plugin must:

1. Accept JSON-RPC requests on stdin
2. Return JSON-RPC responses on stdout
3. Implement the `list_tools` and `call_tool` methods

## Validation

Plugin tools are validated during config loading:

```bash
squadron verify ./my-config
```

Output:

```
Found 1 plugin(s)
  - slack (source: ~/.squadron/plugins/slack, version: 1.0.0, loaded)
```
