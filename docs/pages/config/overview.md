---
title: Configuration Overview
---

# Configuration Overview

Squadron uses HCL (HashiCorp Configuration Language) for all configuration.

## File Structure

A typical config directory:

```
my-config/
├── variables.hcl    # Variable definitions
├── models.hcl       # LLM provider configurations
├── agents.hcl       # Agent definitions
├── tools.hcl        # Custom tool definitions (optional)
└── missions.hcl    # Mission definitions (optional)
```

You can also put everything in a single file—Squadron reads all `.hcl` files in the directory.

## Loading Order

Squadron uses **staged evaluation** to resolve references:

1. **Variables** - Load `variable` blocks first
2. **Models** - Load `model` blocks with `vars` context
3. **Agents** - Load `agent` blocks with `vars` and `models` context
4. **Missions** - Load `mission` blocks with full context

This enables expressions like:

```hcl
model "anthropic" {
  api_key = vars.anthropic_api_key  # Reference a variable
}

agent "assistant" {
  model = models.anthropic.claude_sonnet_4  # Reference a model
}
```

## Block Types

| Block | Purpose |
|-------|---------|
| `variable` | Define configuration variables |
| `model` | Configure LLM providers |
| `agent` | Define AI agents |
| `tool` | Create custom tools |
| `plugin` | Load external plugins |
| `mission` | Define multi-task missions |

## Expressions

HCL supports expressions for dynamic values:

```hcl
# String interpolation
description = "Agent for ${vars.app_name}"

# References
api_key = vars.anthropic_api_key
model = models.anthropic.claude_sonnet_4

# Lists
tools = [plugins.bash.bash, plugins.http.get]
```

## Validation

Always validate your config before running:

```bash
squadron verify ./my-config
```

This catches errors like:
- Invalid HCL syntax
- Missing variable values
- Invalid model references
- Circular mission dependencies
