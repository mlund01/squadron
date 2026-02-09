---
title: Plugins
---

# Plugins

Plugins extend Squadron with additional tools via gRPC.

## Loading Plugins

```hcl
plugin "playwright" {
  source  = "github.com/mlund01/plugin_playwright"
  version = "v0.0.2"

  settings {
    headless     = false
    browser_type = "chromium"
  }
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `source` | string | Plugin source (GitHub path or local path) |
| `version` | string | Version tag or `"local"` for local development |
| `settings` | block | Plugin-specific configuration (optional) |

## Using Plugin Tools

Once loaded, plugin tools are available as `plugins.<name>.<tool>`:

```hcl
agent "browser" {
  tools = [plugins.playwright.browser_navigate]
}

# Or use all tools from a plugin
agent "browser" {
  tools = [plugins.playwright.all]
}
```

## Plugin Paths

Plugins are stored in `~/.squadron/plugins/<name>/<version>/plugin`.

## Built-in Plugins

Squadron includes these built-in plugins (always available):

| Plugin | Tools |
|--------|-------|
| `bash` | `bash` - Execute shell commands |
| `http` | `get`, `post`, `put`, `patch`, `delete` |

## Creating Plugins

Plugins use [hashicorp/go-plugin](https://github.com/hashicorp/go-plugin) over gRPC.

Use the [squadron-sdk](https://github.com/mlund01/squadron-sdk) to build plugins:

```go
package main

import squadron "github.com/mlund01/squadron-sdk"

type MyPlugin struct{}

func (p *MyPlugin) Configure(settings map[string]string) error {
    return nil
}

func (p *MyPlugin) ListTools() ([]*squadron.ToolInfo, error) {
    return []*squadron.ToolInfo{
        {
            Name:        "my_tool",
            Description: "Does something useful",
            Schema:      squadron.Schema{Type: squadron.TypeObject},
        },
    }, nil
}

func (p *MyPlugin) Call(toolName, payload string) (string, error) {
    return "result", nil
}

func main() {
    squadron.Serve(&MyPlugin{})
}
```

Build and install:

```bash
mkdir -p ~/.squadron/plugins/myplugin/local
go build -o ~/.squadron/plugins/myplugin/local/plugin .
```
