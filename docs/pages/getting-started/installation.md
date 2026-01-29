---
title: Installation
---

# Installation

## Prerequisites

- Go 1.21 or later

## Build from Source

Clone the repository and build:

```bash
git clone https://github.com/yourorg/squad.git
cd squad
go build -o squad .
```

Move the binary to your PATH:

```bash
sudo mv squad /usr/local/bin/
```

## Verify Installation

```bash
squad --help
```

## Set Up API Keys

Squad needs API keys for the LLM providers you want to use. Set them as variables:

```bash
# Anthropic
squad vars set anthropic_api_key sk-ant-...

# OpenAI
squad vars set openai_api_key sk-...

# Google Gemini
squad vars set gemini_api_key AIza...
```

Variables are stored in `~/.squad/vars.txt`.
