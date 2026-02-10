---
title: verify
---

# squadron verify

Validate an HCL configuration directory.

## Usage

```bash
squadron verify <path>
```

## Arguments

| Argument | Description |
|----------|-------------|
| `path` | Path to the configuration directory |

## Example

```bash
squadron verify ./my-config
```

## Output

On success:

```
Configuration is valid!
Found 3 model(s)
  - openai (provider: openai, models: [gpt_4o gpt_4o_mini])
  - anthropic (provider: anthropic, models: [claude_sonnet_4])
Found 2 variable(s)
  - openai_api_key (secret, set)
  - anthropic_api_key (secret, set)
Found 1 agent(s)
  - assistant (tools: [plugins.bash.bash plugins.http.get])
Found 1 mission(s)
  - data_pipeline (commander: claude_sonnet_4, agents: [assistant], tasks: 3)
```

On error:

```
Error: agent 'assistant': model 'invalid_model' not found
```

## What Gets Validated

- HCL syntax
- Variable references (`vars.name`)
- Model references (`models.provider.model_key`)
- Tool references (`plugins.namespace.tool`, `tools.name`)
- Mission task dependencies (no cycles)
- Dataset schemas and bindings
