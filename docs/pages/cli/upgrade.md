---
title: upgrade
---

# squadron upgrade

Upgrade squadron to the latest version by downloading from GitHub releases.

## Usage

```bash
squadron upgrade [--version <version>]
```

## Flags

| Flag | Description |
|------|-------------|
| `--version` | Install a specific version (e.g., `v0.0.13`) |

## Examples

Upgrade to the latest release:

```bash
squadron upgrade
```

Install a specific version:

```bash
squadron upgrade --version v0.0.13
```

## How It Works

1. Fetches release info from the GitHub Releases API
2. Finds the correct binary for your OS and architecture
3. Downloads and extracts the tar.gz archive
4. Replaces the current binary with an atomic swap

If the current version matches the target, it prints "Already up to date" and exits.

Dev builds (built from source without version ldflags) will show a warning but proceed with the upgrade.

## Permissions

If the binary is installed in a system directory (e.g., `/usr/local/bin/`), you may need to run with `sudo`:

```bash
sudo squadron upgrade
```
