---
title: Getting started
description: "Start with one command, aiplex dev. Docker, cloud and existing-platform paths come after."
---

# Getting started

Start with `aiplex dev`. It runs AIPlex on your machine with a mock model and a mock MCP server and needs only Go. Move on to the Docker stack or a cloud install when you need them.

| You want to | Go to |
| --- | --- |
| Try AIPlex now, with no Docker, cloud account or API key | [Run it locally in a minute](./local-development.md) |
| Run the API against local emulators and Ory services | [Local stack with Docker](./docker-stack.md) |
| Provision a platform on GCP | [Install the platform](./install-platform.md) |
| Deploy a tool or apply a declarative file on an existing platform | [Deploy a tool](./first-deploy.md) |
| Point an IDE or CLI at an access point | [Connect a client](./connect-client.md) |

## Install

Each tagged release (`v*`) is built by the release workflow and publishes these. No release has been tagged yet, so until the first one, use the source build.

| Method | Command | Notes |
| --- | --- | --- |
| Install script | `curl -fsSL https://raw.githubusercontent.com/vamsiramakrishnan/aiplex/main/install.sh \| bash` | Linux and macOS, amd64 or arm64. Installs `aiplex` and `aiplex-api` into `~/.local/bin` (set `AIPLEX_INSTALL_DIR` to change it). Pass `--version v0.1.0` for a specific release. It checks the archive against `checksums.txt` before installing and needs no sudo. |
| Homebrew (macOS only) | `brew install --cask vamsiramakrishnan/tap/aiplex` | A cask in the `vamsiramakrishnan/homebrew-tap` repository, which must exist before a release can publish to it. Installs both binaries. Homebrew casks do not install on Linux, so Linux users use the install script or the archive. The release binaries are not notarised by Apple: if macOS blocks the first run, allow it under System Settings, Privacy and Security. |
| Release archive | Download from the GitHub release page | `aiplex_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), with `checksums.txt`. Each archive holds `aiplex` and `aiplex-api`. |
| Container image | `docker pull ghcr.io/vamsiramakrishnan/aiplex-api:<version>` and `ghcr.io/vamsiramakrishnan/aiplex:<version>` | Multi-arch (linux/amd64, linux/arm64), distroless, runs as a non-root user. The `aiplex` image is for running CLI commands against a platform; `aiplex dev` needs `aiplex-api` beside it, so use a binary for local mode. |
| Build from source | `git clone` the repository, then `make console && make build` | Go 1.25 or newer. Node 22 is needed only to build the Console; without it, `aiplex dev` says the Console is not in the binary. |

To check an archive by hand, run `sha256sum --check --ignore-missing checksums.txt` (on macOS, `shasum -a 256 --check --ignore-missing checksums.txt`) in the download directory. The release workflow is also set up to sign `checksums.txt` and the images without a stored key, using GitHub's identity. That signing has not been run against a real release yet, so no verification command is documented here.

## Prerequisites

For `aiplex dev`:

- Go 1.25 or newer (the requirement in `go.mod` takes precedence over the older Go entry in `.mise.toml`).
- `curl`.

Only for the later paths:

- Node 22 to build the Console.
- Docker Compose for the local dependency stack.
- GCP project credentials for the platform install. `aiplex init` installs the deployment tools (Terraform, Helm, kubectl) that `.mise.toml` pins.

Clone the repository:

```bash
git clone https://github.com/vamsiramakrishnan/aiplex.git
cd aiplex
```
