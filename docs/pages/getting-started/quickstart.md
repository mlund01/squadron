---
title: Quick Start
---

# Quick Start

This guide will help you create your first Squad agent in under 5 minutes.

## 1. Create a Config Directory

```bash
mkdir my-agents
cd my-agents
```

## 2. Define Variables

Create `variables.hcl`:

```hcl
variable "anthropic_api_key" {
  secret = true
}
```

## 3. Define a Model

Create `models.hcl`:

```hcl
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_3_5_haiku"]
  api_key        = vars.anthropic_api_key
}
```

## 4. Define an Agent

Create `agents.hcl`:

```hcl
agent "assistant" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Friendly, helpful, and concise"
  role        = "General purpose assistant that can help with various tasks"
  tools       = [
    plugins.bash.bash,
    plugins.http.get
  ]
}
```

## 5. Validate Your Config

```bash
squadron verify .
```

You should see:

```
Configuration is valid!
Found 1 model(s)
Found 1 agent(s)
  - assistant (tools: [plugins.bash.bash plugins.http.get])
```

## 6. Start Chatting

```bash
squadron chat -c . assistant
```

You're now chatting with your agent! Try asking it to:

- Run a shell command: "What's my current directory?"
- Fetch a URL: "Get the contents of https://httpbin.org/get"

## Next Steps

- [CLI Commands](/cli/verify) - Learn all available commands
- [Agents](/config/agents) - Configure agent behavior
- [Tools](/config/tools) - Add and customize tools
- [Workflows](/workflows/overview) - Build multi-step pipelines
