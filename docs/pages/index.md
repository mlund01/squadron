---
title: Introduction
---

# Squad

Squad is an HCL-based CLI for defining and running AI agents. It uses a config-first approach where agents, models, and workflows are declared in `.hcl` files.

## Features

- **Multi-provider support** - Use OpenAI, Anthropic, or Google Gemini models
- **HCL configuration** - Define agents, tools, and workflows in declarative config files
- **Workflows** - Orchestrate multi-task pipelines with supervisors and agent delegation
- **Datasets & Iteration** - Process lists of items in parallel or sequentially
- **Plugin system** - Extend functionality with custom plugins
- **Custom tools** - Wrap internal tools with custom schemas and transformations

## Quick Example

```hcl
# Define a model
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.anthropic_api_key
}

# Define an agent
agent "assistant" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful and concise"
  role        = "General assistant"
  tools       = [plugins.bash.bash]
}
```

Start chatting:

```bash
squad chat -c ./config assistant
```

## Next Steps

- [Installation](/getting-started/installation) - Get Squad up and running
- [Quick Start](/getting-started/quickstart) - Build your first agent
- [Configuration](/config/overview) - Learn the HCL config system
