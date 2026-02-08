---
title: Installation
---

# Installation

## Prerequisites

- Go 1.21 or later

## Build from Source

Clone the repository and build:

```bash
git clone https://github.com/yourorg/squadron.git
cd squadron
go build -o squadron .
```

Move the binary to your PATH:

```bash
sudo mv squadron /usr/local/bin/
```

## Verify Installation

```bash
squadron --help
```

## Set Up API Keys

Squad needs API keys for the LLM providers you want to use. Set them as variables:

```bash
# Anthropic
squadron vars set anthropic_api_key sk-ant-...

# OpenAI
squadron vars set openai_api_key sk-...

# Google Gemini
squadron vars set gemini_api_key AIza...
```

Variables are stored in `~/.squadron/vars.txt`.
