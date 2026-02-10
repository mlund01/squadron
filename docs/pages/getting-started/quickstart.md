---
title: Quick Start
---

# Quick Start

Create your first agent in 5 minutes.

## 1. Create Config Directory

```bash
mkdir my-agents && cd my-agents
```

## 2. Define Variables

`variables.hcl`:

```hcl
variable "anthropic_api_key" {
  secret = true
}
```

## 3. Define a Model

`models.hcl`:

```hcl
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.anthropic_api_key
}
```

## 4. Define an Agent

`agents.hcl`:

```hcl
agent "assistant" {
  model       = models.anthropic.claude_sonnet_4
  personality = "Helpful and concise"
  role        = "General purpose assistant"
  tools       = [plugins.bash.bash, plugins.http.get]
}
```

## 5. Validate

```bash
squadron verify .
```

## 6. Chat

```bash
squadron chat -c . assistant
```

## Next Steps

- [Agents](/config/agents) - Configure agent behavior
- [Tools](/config/tools) - Add custom tools
- [Missions](/missions/overview) - Build multi-step pipelines
