---
title: Models
---

# Models

Models define connections to LLM providers and which models are available.

## Defining Models

```hcl
model "anthropic" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4", "claude_3_5_haiku", "claude_opus_4"]
  api_key        = vars.anthropic_api_key
}

model "openai" {
  provider       = "openai"
  allowed_models = ["gpt_4o", "gpt_4o_mini", "gpt_4_turbo"]
  api_key        = vars.openai_api_key
}

model "gemini" {
  provider       = "gemini"
  allowed_models = ["gemini_2_0_flash", "gemini_1_5_pro"]
  api_key        = vars.gemini_api_key
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `provider` | string | Provider name: `anthropic`, `openai`, or `gemini` |
| `allowed_models` | list | Model keys that can be used with this config |
| `api_key` | string | API key for the provider |

## Supported Models

### Anthropic

| Key | Model |
|-----|-------|
| `claude_opus_4` | claude-opus-4-20250514 |
| `claude_sonnet_4` | claude-sonnet-4-20250514 |
| `claude_3_5_haiku` | claude-3-5-haiku-20241022 |

### OpenAI

| Key | Model |
|-----|-------|
| `gpt_4o` | gpt-4o |
| `gpt_4o_mini` | gpt-4o-mini |
| `gpt_4_turbo` | gpt-4-turbo |

### Google Gemini

| Key | Model |
|-----|-------|
| `gemini_2_0_flash` | gemini-2.0-flash |
| `gemini_1_5_pro` | gemini-1.5-pro |
| `gemini_1_5_flash` | gemini-1.5-flash |

## Referencing Models

Use `models.<config_name>.<model_key>` to reference a model:

```hcl
agent "assistant" {
  model = models.anthropic.claude_sonnet_4
}

workflow "pipeline" {
  supervisor_model = models.anthropic.claude_sonnet_4
}
```

## Multiple Configs Per Provider

You can have multiple model configs for the same provider:

```hcl
model "anthropic_prod" {
  provider       = "anthropic"
  allowed_models = ["claude_sonnet_4"]
  api_key        = vars.anthropic_prod_key
}

model "anthropic_dev" {
  provider       = "anthropic"
  allowed_models = ["claude_3_5_haiku"]
  api_key        = vars.anthropic_dev_key
}
```
