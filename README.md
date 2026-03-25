# Squadron

AI agent workflows as configuration, not code.

Squadron is a framework for defining and running multi-agent missions in HCL. You declare agents, tools, and task graphs in config files — Squadron handles orchestration, state management, branching, and recovery.

**[Documentation](https://mlund01.github.io/squadron/)**

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash
```

Or download from [GitHub Releases](https://github.com/mlund01/squadron/releases). See the [installation docs](https://mlund01.github.io/squadron/getting-started/installation) for more options.

## Quick Start

```bash
# Generate a sample workflow
squadron helloworld && cd helloworld

# Set your API key
squadron vars set anthropic_api_key sk-ant-...

# Launch the command center
squadron serve -w
```

## Example Mission

```hcl
mission "data_pipeline" {
  commander = models.anthropic.claude_sonnet_4
  agents    = [agents.assistant]

  task "fetch" {
    objective = "Fetch data from the API"
    output {
      field "records" { type = "string"; required = true }
    }
  }

  task "process" {
    depends_on = [tasks.fetch]
    objective  = "Analyze the data and produce a summary"
  }

  task "review" {
    depends_on = [tasks.process]
    objective  = "Review the analysis"
    router {
      route {
        target    = tasks.escalate
        condition = "Issues were found that need human review"
      }
    }
  }

  task "escalate" {
    objective = "Flag issues and prepare an escalation report"
  }
}
```

```bash
squadron mission data_pipeline -c ./config
```

## Docker

```bash
docker run -v ./config:/config -v squadron-data:/data/squadron -p 8080:8080 \
  ghcr.io/mlund01/squadron serve -w --cc-port 8080 --no-browser
```

Alpine (default) and Debian images are published to `ghcr.io/mlund01/squadron` on every release. See the [Docker docs](https://mlund01.github.io/squadron/getting-started/docker) for details.

## Key Features

- **HCL configuration** — agents, tools, and missions defined as config files
- **Mission orchestration** — task dependencies, LLM-driven routing, fan-out, cross-mission routing
- **Persistence and resume** — resume interrupted missions from where they failed
- **Command center** — web UI for running missions, DAG visualization, live monitoring
- **Multi-provider** — Anthropic, OpenAI, Google Gemini
- **Plugin system** — extend with plugins for browser automation, APIs, and custom integrations

## Providers

- Anthropic (Claude)
- OpenAI (GPT-4)
- Google Gemini

## Plugins

Build custom plugins with the [squadron-sdk](https://github.com/mlund01/squadron-sdk).

## License

MIT
