# Squadron

HCL-based CLI for defining and running AI agents and multi-agent missions.

## Install

Download the latest release for your platform from [GitHub Releases](https://github.com/mlund01/squadron/releases), extract it, and move the binary to your PATH:

```bash
# macOS (Apple Silicon)
curl -L https://github.com/mlund01/squadron/releases/latest/download/squadron_darwin_arm64.tar.gz | tar xz
sudo mv squadron /usr/local/bin/

# macOS (Intel)
curl -L https://github.com/mlund01/squadron/releases/latest/download/squadron_darwin_amd64.tar.gz | tar xz
sudo mv squadron /usr/local/bin/

# Linux (amd64)
curl -L https://github.com/mlund01/squadron/releases/latest/download/squadron_linux_amd64.tar.gz | tar xz
sudo mv squadron /usr/local/bin/
```

**Windows (PowerShell):**

```powershell
Invoke-WebRequest -Uri https://github.com/mlund01/squadron/releases/latest/download/squadron_windows_amd64.zip -OutFile squadron.zip
Expand-Archive squadron.zip -DestinationPath .
Move-Item squadron.exe C:\Windows\System32\
Remove-Item squadron.zip
```

Upgrade to the latest version:

```bash
squadron upgrade
```

Or build from source:

```bash
git clone https://github.com/mlund01/squadron.git
cd squadron
go build -o squadron ./cmd/cli
```

## Getting Started

```bash
# Create a new project directory
mkdir my-workflow && cd my-workflow

# See available commands
squadron help

# Generate a sample workflow
squadron helloworld

# Set your Anthropic API key
squadron vars set anthropic_api_key sk-ant-...

# Validate the configuration
squadron verify

# Run with the command center UI
squadron serve -w
```

## Missions

Define multi-task pipelines:

```hcl
mission "data_pipeline" {
  commander = models.anthropic.claude_sonnet_4
  agents           = [agents.assistant]

  task "fetch" {
    objective = "Fetch data from the API"
  }

  task "process" {
    objective  = "Process the data"
    depends_on = [tasks.fetch]
  }
}
```

Run:

```bash
squadron mission data_pipeline -c ./config
```

Resume a failed mission:

```bash
squadron mission data_pipeline -c ./config --resume <mission-id>
```

## Providers

- Anthropic (Claude)
- OpenAI (GPT-4)
- Google Gemini

## Plugins

Extend with gRPC plugins using the [squadron-sdk](https://github.com/mlund01/squadron-sdk).

## License

MIT
