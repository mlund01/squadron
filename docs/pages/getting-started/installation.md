---
title: Installation
---

# Installation

## Homebrew (macOS)

```bash
brew tap mlund01/squadron
brew install --cask mlund01/squadron/squadron
```

## Upgrade

```bash
squadron upgrade
```

Or install a specific version:

```bash
squadron upgrade --version v0.0.13
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
