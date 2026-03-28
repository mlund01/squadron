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

To connect to a remote command center, add a `command_center` block to your config:

```hcl
command_center {
  url           = "ws://command-center.example.com/ws"
  instance_name = "my-instance"
}
```

When using `-w` (local mode), no `command_center` block is needed — Squadron starts its own server.

## Docker

Run the command center in a container:

```bash
docker run -v ./config:/config -v squadron-data:/data/squadron -p 8080:8080 \
  ghcr.io/mlund01/squadron serve -w --cc-port 8080 --no-browser
```

The container's working directory is `/config`, so the `-c` flag is not needed. See the [Docker guide](/getting-started/docker) for full details.

## See Also

- [Missions Overview](/missions/overview) - Mission structure
- [mission](/cli/mission) - Run missions from the CLI
- [Docker](/getting-started/docker) - Running in containers
