---
title: chat
---

# squadron chat

Start an interactive chat session with an agent.

## Usage

```bash
squadron chat -c <config-path> <agent-name>
```

## Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to the configuration directory (required) |
| `-d, --debug` | Log full LLM messages to debug.txt |
| `-w, --workflow` | Run in workflow mode (non-interactive) |
| `-t, --task` | Task to run in workflow mode (requires `--workflow`) |

## Arguments

| Argument | Description |
|----------|-------------|
| `agent-name` | Name of the agent to chat with |

## Example

```bash
squadron chat -c ./my-config assistant
```

This opens an interactive REPL where you can send messages to the agent.

## Workflow Mode

Use the `--workflow` flag to run an agent in autonomous task completion mode:

```bash
squadron chat -c ./my-config assistant --workflow --task "Summarize the README file"
```

In workflow mode:
- The agent runs non-interactively
- It continuously reasons and acts until the task is complete
- Structured reasoning is always used

## Debug Mode

Use the `--debug` flag to log all LLM request/response messages to `debug.txt`:

```bash
squadron chat -c ./my-config assistant --debug
```

## Available Tools

During chat, agents have access to the tools defined in their configuration. Common built-in tools:

- `plugins.bash.bash` - Execute shell commands
- `plugins.http.get` - Make HTTP GET requests
- `plugins.http.post` - Make HTTP POST requests

See [Tools](/config/tools) for more information.
