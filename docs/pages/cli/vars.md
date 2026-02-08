---
title: vars
---

# squadron vars

Manage configuration variables.

Variables are stored in `~/.squadron/vars.txt` and can be referenced in HCL configs using `vars.name`.

## Commands

### vars set

Set a variable value.

```bash
squadron vars set <name> <value>
```

Example:

```bash
squadron vars set anthropic_api_key sk-ant-api03-...
```

### vars get

Get a variable value.

```bash
squadron vars get <name>
```

Example:

```bash
squadron vars get anthropic_api_key
# Output: sk-ant-api03-...
```

### vars list

List all variables.

```bash
squadron vars list
```

Example output:

```
anthropic_api_key = sk-ant-*** (secret)
openai_api_key = sk-*** (secret)
app_name = myapp
```

## Using Variables in HCL

Define a variable:

```hcl
variable "anthropic_api_key" {
  secret = true  # Masks value in output
}

variable "app_name" {
  default = "myapp"  # Optional default
}
```

Reference variables:

```hcl
model "anthropic" {
  api_key = vars.anthropic_api_key
}
```

## Storage

Variables are stored as key=value pairs in `~/.squadron/vars.txt`:

```
anthropic_api_key=sk-ant-api03-...
openai_api_key=sk-...
app_name=myapp
```

This file should be kept secure and not committed to version control.
