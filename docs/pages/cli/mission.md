---
title: mission
---

# squadron mission

Execute a mission.

## Usage

```bash
squadron mission <mission-name> -c <config-path> [--input key=value...]
```

## Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to config directory (default: `.`) |
| `-d, --debug` | Enable debug mode (captures LLM messages) |
| `-i, --input` | Mission input as key=value (repeatable) |

## Example

```bash
squadron mission data_pipeline -c ./my-config

squadron mission weather_report -c ./config --input city=Chicago
```

## Debug Mode

```bash
squadron mission -d -c ./config data_pipeline
```

Creates a `debug/` folder with:
- `events.log` - Task and tool events
- `commander_*.md` - Full commander conversations
- `agent_*.md` - Full agent conversations

## See Also

- [Missions Overview](/missions/overview)
- [Tasks](/missions/tasks)
