---
title: Variables
---

# Variables

Variables store configuration values that can be referenced throughout your HCL files.

## Defining Variables

```hcl
variable "api_key" {
  secret = true  # Mask value in output
}

variable "app_name" {
  default = "myapp"  # Optional default value
}

variable "max_retries" {
  default = 3
}
```

## Attributes

| Attribute | Type | Description |
|-----------|------|-------------|
| `secret` | bool | If true, value is masked in CLI output |
| `default` | any | Default value if not set via `squad vars set` |

## Setting Values

Use the CLI to set variable values:

```bash
squad vars set api_key sk-ant-...
squad vars set app_name production-app
```

Values are stored in `~/.squad/vars.txt`.

## Referencing Variables

Use `vars.name` to reference a variable:

```hcl
model "anthropic" {
  api_key = vars.api_key
}

agent "assistant" {
  personality = "Assistant for ${vars.app_name}"
}
```

## Resolution Order

1. Value set via `squad vars set` (stored in `~/.squad/vars.txt`)
2. Default value from `variable` block
3. Error if neither exists

## Best Practices

### Secrets

Always mark sensitive values as secrets:

```hcl
variable "anthropic_api_key" {
  secret = true
}

variable "database_password" {
  secret = true
}
```

### Defaults for Non-Sensitive Values

Provide defaults for convenience:

```hcl
variable "environment" {
  default = "development"
}

variable "log_level" {
  default = "info"
}
```

### Naming Convention

Use snake_case for variable names:

```hcl
variable "anthropic_api_key" { }  # Good
variable "anthropicApiKey" { }    # Avoid
```
