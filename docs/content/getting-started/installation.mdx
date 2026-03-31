---
title: Installation
---

# Installation

## Quick Install

The fastest way to install Squadron:

```bash
curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash
```

This auto-detects your platform, downloads the latest release, verifies the checksum, and installs to `~/.local/bin`. To install a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash -s v0.0.43
```

You can override the install directory with `INSTALL_DIR`:

```bash
INSTALL_DIR=/usr/local/bin curl -fsSL https://raw.githubusercontent.com/mlund01/squadron/main/install.sh | bash
```

## Manual Download

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

## Docker

Squadron publishes Docker images to GitHub Container Registry on every release:

```bash
# Alpine (default, small image)
docker pull ghcr.io/mlund01/squadron:latest

# Debian (for plugins that need glibc)
docker pull ghcr.io/mlund01/squadron:latest-debian
```

Run with your config mounted:

```bash
docker run -v ./config:/config -v squadron-data:/data/squadron -p 8080:8080 \
  ghcr.io/mlund01/squadron serve --init -w --cc-port 8080 --no-browser
```

See the [Docker guide](/getting-started/docker) for full setup details including Docker Compose.

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

## Initialize

Before using Squadron, initialize the encrypted vault for secret storage:

```bash
squadron init
```

This creates `~/.squadron/` and sets up an encrypted vault with a passphrase stored in your OS keychain.

## API Keys

Set API keys for the providers you want to use:

```bash
squadron vars set anthropic_api_key sk-ant-...
squadron vars set openai_api_key sk-...
squadron vars set gemini_api_key AIza...
```

Variables are encrypted at rest in `~/.squadron/vars.vault`.
