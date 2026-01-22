# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Run Commands

```bash
go build -o squad .    # Build the CLI
./squad verify <path>  # Validate HCL config
./squad chat -c <path> <agent_name>  # Start chat with an agent
./squad vars set <name> <value>      # Set a variable
./squad vars get <name>              # Get a variable
./squad vars list                    # List all variables
```

## Architecture

Squad is an HCL-based CLI for defining and running AI agents. It uses a config-first approach where agents, models, and variables are declared in `.hcl` files.

### HCL Config System (config/)

The config loading uses **staged evaluation** to support HCL expression references:

1. **Stage 1**: Load `variable` blocks (no context needed)
2. **Stage 2**: Load `model` blocks with `vars` context → enables `api_key = vars.anthropic_api_key`
3. **Stage 3**: Load `agent` blocks with `vars` + `models` context → enables `model = models.anthropic.claude_sonnet_4`

Each stage uses partial structs with `hcl:",remain"` to ignore unknown block types during that pass. Expressions are evaluated immediately at decode time (no delayed `hcl.Expression` fields).

### LLM Provider Abstraction (llm/)

- `Provider` interface defines `Chat()` and `ChatStream()` methods
- `Session` maintains conversation history and system prompts
- Implementations: `OpenAIProvider`, `AnthropicProvider`, `GeminiProvider`

### Variable Storage

Variables are stored in `~/.squad/vars.txt` as `key=value` pairs. The config system merges these with defaults from `variable` blocks.

## HCL Config Format

```hcl
# variables.hcl
variable "anthropic_api_key" {
  secret = true
}

# models.hcl
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_opus_4"]
  api_key        = vars.anthropic_api_key
}

# agents.hcl
agent "assistant" {
  type        = "react"
  model       = models.anthropic.claude_sonnet_4
  personality = "Friendly and helpful"
  role        = "General assistant"
}
```

## Supported Providers & Models

Model keys (used in HCL) map to actual API model names in `config/model.go:SupportedModels`. Add new models there first before using them in config.
