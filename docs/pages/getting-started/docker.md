---
title: Docker
---

# Running with Docker

Squadron publishes Docker images to GitHub Container Registry on every release. Two variants are available:

| Image | Tag | Use case |
|-------|-----|----------|
| Alpine | `ghcr.io/mlund01/squadron:latest` | Default — small, static binary |
| Debian | `ghcr.io/mlund01/squadron:latest-debian` | Plugins that need glibc/CGO |

Both images support `linux/amd64` and `linux/arm64`.

## Quick Start

```bash
docker run --rm ghcr.io/mlund01/squadron version
```

## Running with Config

Squadron in Docker uses two mount points:

| Mount | Purpose |
|-------|---------|
| `/config` | Your HCL configuration files (bind mount from host) |
| `/data/squadron` | Runtime state — variables, plugins, database (named volume or bind mount) |

The container's working directory is `/config`, so the `-c` flag is not needed.

### Launch with local command center

```bash
docker run \
  -v /path/to/your/config:/config \
  -v squadron-data:/data/squadron \
  -p 8080:8080 \
  ghcr.io/mlund01/squadron serve -w --cc-port 8080 --no-browser
```

Then open `http://localhost:8080` in your browser.

### Connect to a remote commander

If your HCL config has a `commander` block, no port mapping is needed — Squadron connects outbound:

```bash
docker run \
  -v /path/to/your/config:/config \
  -v squadron-data:/data/squadron \
  ghcr.io/mlund01/squadron serve
```

### Run a mission directly

```bash
docker run \
  -v /path/to/your/config:/config \
  -v squadron-data:/data/squadron \
  ghcr.io/mlund01/squadron mission my_mission
```

## Docker Compose

```yaml
services:
  squadron:
    image: ghcr.io/mlund01/squadron:latest
    volumes:
      - ./my-config:/config
      - squadron-data:/data/squadron
    command: ["serve", "-w", "--cc-port", "8080", "--no-browser"]
    ports:
      - "8080:8080"

volumes:
  squadron-data:
```

```bash
docker compose up
```

## Setting Variables

Variables (API keys, etc.) are stored in `/data/squadron/vars.txt`. You can set them by exec-ing into a running container:

```bash
docker exec <container> squadron vars set anthropic_api_key sk-ant-...
```

Or copy a local vars file into the volume:

```bash
docker cp ~/.squadron/vars.txt <container>:/data/squadron/vars.txt
```

## Pinning a Version

Use a version tag instead of `latest`:

```bash
docker pull ghcr.io/mlund01/squadron:v0.0.45
docker pull ghcr.io/mlund01/squadron:v0.0.45-debian
```

## SQUADRON_HOME

Inside the container, `SQUADRON_HOME` is set to `/data/squadron`. This consolidates all runtime state (variables, plugins, database, shared folders) into a single directory. When `SQUADRON_HOME` is set:

- Variables are stored at `$SQUADRON_HOME/vars.txt`
- Plugins are downloaded to `$SQUADRON_HOME/plugins/`
- The SQLite database defaults to `$SQUADRON_HOME/store.db`
- Shared folder paths in HCL are resolved relative to `$SQUADRON_HOME/folders/`

You can also use `SQUADRON_HOME` outside of Docker to consolidate state into a custom directory.

## Alpine vs Debian

- **Alpine** (`latest`) — smaller image, static binary with `CGO_ENABLED=0`. Use this unless you have a reason not to.
- **Debian** (`latest-debian`) — larger image, built with `CGO_ENABLED=1`. Use this if you have plugins that depend on C libraries or glibc.
