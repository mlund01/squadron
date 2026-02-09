---
title: workflow
---

# squadron workflow

Execute a workflow.

## Usage

```bash
squadron workflow <workflow-name> -c <config-path> [--input key=value...]
```

## Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to config directory (default: `.`) |
| `-d, --debug` | Enable debug mode (captures LLM messages) |
| `-i, --input` | Workflow input as key=value (repeatable) |

## Example

```bash
squadron workflow data_pipeline -c ./my-config

squadron workflow weather_report -c ./config --input city=Chicago
```

## Debug Mode

```bash
squadron workflow -d -c ./config data_pipeline
```

Creates a `debug/` folder with:
- `events.log` - Task and tool events
- `supervisor_*.md` - Full supervisor conversations
- `agent_*.md` - Full agent conversations

## See Also

- [Workflows Overview](/workflows/overview)
- [Tasks](/workflows/tasks)
