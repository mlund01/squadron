---
title: serve
---

# squadron serve

Connect to a commander server for fleet management, or launch a local command center.

## Usage

```bash
squadron serve -c <config-path> [flags]
```

## Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to config directory (default: `.`) |
| `-w, --web` | Launch a local command center (web UI) |
| `--cc-port` | Custom port for the command center (default: `8080`) |
| `--no-browser` | Don't automatically open the browser |

## Examples

Connect to a configured commander server:

```bash
squadron serve -c ./config
```

Launch a local command center with web UI:

```bash
squadron serve -c ./config -w
```

Custom port without auto-opening the browser:

```bash
squadron serve -c ./config -w --cc-port 9090 --no-browser
```

## Command Center

The command center provides a web UI for managing squadron instances, running missions, and viewing mission history. When launched with `-w`, it starts a local web server and connects the squadron instance to it.

The web UI includes:

- **Instance management** — view connected instances and their configurations
- **Mission execution** — run missions with input forms, view DAG visualizations
- **Run history** — browse past mission runs with task-level detail
- **Live monitoring** — watch mission execution in real time with session logs and tool results

## Configuration

To connect to a remote commander, add a `commander` block to your config:

```hcl
commander {
  url = "ws://commander.example.com/ws"
}
```

When using `-w` (local mode), no `commander` block is needed — Squadron starts its own server.

## See Also

- [Missions Overview](/missions/overview) - Mission structure
- [mission](/cli/mission) - Run missions from the CLI
