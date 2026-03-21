---
title: Installation
---

# Installation

## Download Binary

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

# Linux (arm64)
curl -L https://github.com/mlund01/squadron/releases/latest/download/squadron_linux_arm64.tar.gz | tar xz
sudo mv squadron /usr/local/bin/
```

### Windows

Download the zip from [GitHub Releases](https://github.com/mlund01/squadron/releases) and extract it. In PowerShell:

```powershell
# Download and extract
Invoke-WebRequest -Uri https://github.com/mlund01/squadron/releases/latest/download/squadron_windows_amd64.zip -OutFile squadron.zip
Expand-Archive squadron.zip -DestinationPath .
Remove-Item squadron.zip

# Move to a directory in your PATH
Move-Item squadron.exe C:\Windows\System32\
```

## Upgrade

```bash
squadron upgrade
```

Or install a specific version:

```bash
squadron upgrade --version v0.0.28
```

## Build from Source

Requires Go 1.25+.

```bash
git clone https://github.com/mlund01/squadron.git
cd squadron
go build -o squadron ./cmd/cli
sudo mv squadron /usr/local/bin/
```

## Verify

```bash
squadron --help
```

## API Keys

Set API keys for the providers you want to use:

```bash
squadron vars set anthropic_api_key sk-ant-...
squadron vars set openai_api_key sk-...
squadron vars set gemini_api_key AIza...
```

Variables are stored in `~/.squadron/vars.txt`.
