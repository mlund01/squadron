---
title: Introduction
---

# Squadron

Squadron is an HCL-based CLI for defining and running AI agents and multi-agent workflows.

## Features

- **Multi-provider support** - OpenAI, Anthropic, Google Gemini
- **HCL configuration** - Declare agents, tools, and workflows in `.hcl` files
- **Workflows** - Multi-task pipelines with supervisor orchestration
- **Datasets & Iteration** - Process items in parallel or sequentially
- **Plugin system** - Extend with custom gRPC plugins
- **Custom tools** - Wrap plugins with custom schemas

## Quick Example

```hcl
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.anthropic_api_key
}

agent "assistant" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful and concise"
  role        = "General assistant"
  tools       = [plugins.bash.bash]
}
```

```bash
squadron chat -c ./config assistant
```

## Next Steps

- [Installation](/getting-started/installation)
- [Quick Start](/getting-started/quickstart)
- [Configuration](/config/overview)
