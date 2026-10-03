# Images

A sandbox is created from an image: the base contents every sandbox needs plus the sandbox's toolchains. `sandboxed-agents build` builds the image from the build context embedded in the executable. In this version it builds only the base image; toolchain images come with #28.

## Build the base image

```sh
sandboxed-agents build
```

`build` takes no arguments or options in this version; an extra word is a usage error and calls no Podman command. The command then runs the preflight described in [Host prerequisites](host-prerequisites.md) and prints its lines. If a prerequisite is missing, it builds nothing. The build itself needs network access to pull the Debian image and to download packages.

The build runs in three steps:

1. The executable writes the embedded build context and the in-container manager to a new temporary directory, named with the prefix `sandboxed-agents-context-` in the operating system's temporary directory.
2. It runs one Podman command on that directory:

   ```sh
   podman build --pull=always --no-cache --tag TAG --label ... --file CONTEXT/Containerfile CONTEXT
   ```

3. It removes the temporary directory, whether the build succeeded or failed.

On Windows, the build runs in the Podman machine that the preflight checked: the call is `podman --connection NAME build …`, with the name of the [selected Podman machine](host-prerequisites.md#selected-podman-machine). `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or another default connection does not redirect it. On Linux, `build` calls the local `podman` without a connection.

Podman's output passes through. On success, `build` prints `Built image TAG.` and a reminder that existing sandboxes keep their image. If Podman exits with a nonzero status, `build` reports `podman build failed with exit status N` and exits non-zero.

`--pull=always` and `--no-cache` make every build start from the current Debian image and current packages. The executable does not inspect, remove, or retag images. An image that an earlier build left behind stays on the host until you remove it, for example with `podman image prune`.

## Image name and labels

The base image is named `localhost/sandboxed-agents:base-HASH`, where `HASH` is the full lowercase SHA-256 asset hash that `sandboxed-agents version` prints. The name contains no controller group, so every controller group on the host that runs the same executable shares one base image (ADR-0005). A build in one group therefore affects the sandboxes of every group.

Each image carries these labels:

| Label | Value |
| --- | --- |
| `io.github.sandboxed-agents.managed` | `true` |
| `io.github.sandboxed-agents.asset-hash` | the asset hash, 64 lowercase hexadecimal digits |
| `io.github.sandboxed-agents.toolchains` | the image's toolchain names, sorted and comma-separated; empty for the base image |

A build does not change existing sandboxes. They keep the image they were created from until `update` replaces their container, and `list` marks them as outdated. Both commands come with later Stories.

## Base image contents

- **Distribution:** Debian 12 (bookworm), from `docker.io/library/debian:bookworm-slim`, with glibc.
- **Node.js 24 with npm**, from the signed NodeSource APT repository. Node.js is the runtime for agents, and npm is the package manager that later Stories use to install them. The base image contains no agents and no toolchains.
- **Packages:** OpenSSH client and server, Git, the Debian `gh` package, tmux, curl, jq, ripgrep, less, unzip, bash, coreutils, findutils, procps, util-linux, tar, xz-utils, ca-certificates, and gnupg.
- **User:** `agent`, with UID and GID 1000.
- **Manager:** `/usr/local/bin/sandboxed-agents-manager`.
- **Workspace:** `/workspace`, owned by `agent`.

The image contains no SSH keys; host keys are removed after the OpenSSH packages are installed. Until the SSH server setup (#18), the entrypoint runs as root only to create `/run/sshd` and then runs `sleep infinity` as `agent` through `runuser`. The entrypoint is not the only root process: the host executable makes its administrative control calls to the manager with `podman exec --user=0:0`, as root in the container. Agent installation and agent work, which later Stories add, are to run as `agent` ([Execution identities](sandboxes.md#execution-identities)).

### Version inventory

`/usr/local/share/sandboxed-agents/versions.tsv` records what the image contains. It is UTF-8 text with LF line endings and no header. Each line is a component name and its version, separated by one tab. The file lists every installed Debian package as reported by `dpkg-query`, followed by the lines for `node`, `npm`, and `manager`.

## Toolchain availability on Debian 12

Debian 12 was chosen because every planned toolchain has packages for it. The Azure CLI documentation lists Debian 11 and 12 as tested distributions for its APT packages, but not Debian 13.

This record is based on the upstream documentation listed below. None of these toolchains has been installed in a sandbox image yet; #28 adds them and #29 validates images against real Podman.

| Toolchain | Upstream statement for Debian 12 | Source |
| --- | --- | --- |
| .NET | .NET 10, 9, and 8 are supported on Debian 12, as `dotnet-sdk-*` packages from the Microsoft package repository for x64. | [Install .NET on Debian](https://learn.microsoft.com/en-us/dotnet/core/install/linux-debian) |
| Playwright with browsers | Debian 12 and 13 are listed as supported Linux distributions, with Node.js 22, 24, or 26. | [Playwright installation](https://playwright.dev/docs/intro) |
| Azure CLI | The `azure-cli` APT package for x86_64 and ARM64 is tested on Debian 11 and 12. | [Install the Azure CLI on Linux](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli-linux) |
| Native build tools | `build-essential` and `cmake` are packages in bookworm. | [build-essential](https://packages.debian.org/bookworm/build-essential), [cmake](https://packages.debian.org/bookworm/cmake) |

The Node.js runtime in the base image follows the same approach: NodeSource lists Node.js 24 packages for Debian 12 ([NodeSource distributions](https://github.com/nodesource/distributions/blob/master/DEV_README.md)), and Node.js lists version 24 as an LTS release ([Node.js releases](https://nodejs.org/en/about/previous-releases)).
