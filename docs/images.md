# Images

A sandbox is created from an image: the base contents every sandbox needs plus the sandbox's toolchains. `sandboxed-agents build` builds the images from the build context embedded in the executable: the base image, and a toolchain image for each toolchain set that needs one. Sandboxes with the same toolchain set share one image.

## Toolchains

A toolchain is a set of SDKs or system packages built into a sandbox's image. All toolchains are off by default. `build --with SET` and `up NAME --with SET` select a toolchain set: a comma-separated list of toolchain names, such as `--with native`.

This version delivers one toolchain:

| Toolchain | Debian packages |
| --- | --- |
| `native` | `build-essential`, `cmake`, `pkg-config`, `ninja-build` |

`none` selects the base image without toolchains and has the same result as leaving `--with` out. It must stand alone: `--with none,native` is rejected. The catalog also plans `dotnet`, `playwright`, and `azure`; until each of them is delivered, its name is rejected like an unknown name.

The selection is a set. Order and repetition do not matter, so `--with native,native` selects the same set as `--with native`. The executable records a set in its canonical form: the names sorted and deduplicated, separated by commas, and the empty string for the base image.

An unknown or undelivered name, `none` combined with another name, an empty name, a name with surrounding spaces such as `native, native`, or a missing value is a usage error. The message lists the valid values, which in this version are `native` and `none`. The command exits with status 1 before the preflight and before any Podman call, so the error is reported even when a prerequisite is missing or the sandbox belongs to another controller group.

## Build images

```sh
sandboxed-agents build [--with SET]
```

`build` accepts one option, `--with SET`, at most once; any other word is a usage error and calls no Podman command. The command then runs the preflight described in [Host prerequisites](host-prerequisites.md) and prints its lines. If a prerequisite is missing, it builds nothing. The builds need network access to pull the Debian image and to download packages.

Every `build` rebuilds, even when images with the same tags exist, so that current packages are installed:

1. It rebuilds the base image from the current Debian image without the layer cache.
2. It finds the toolchain images of the current executable: the images labelled as managed by `sandboxed-agents` with the current asset hash and a non-empty toolchain set, whichever controller group built them or uses them ([Image names and labels](#image-names-and-labels)). It rebuilds each of them under its existing tag, for the toolchain set its label records, on top of the new base image and without the layer cache. Images of other executable versions are not rebuilt.
3. With `--with SET`, it also builds the image for that set under its canonical tag ([Image names and labels](#image-names-and-labels)) when no image found in step 2 carries that tag. An image of the set that carries only other tags is still rebuilt under those tags in step 2, and the canonical tag is built in addition; an image that already carries the canonical tag is rebuilt only once, in step 2. `build --with none` builds what `build` without options builds.

Nothing about images is stored in host state; `build` finds the toolchain images through their labels each time.

For the base image, the executable writes the embedded build context and the in-container manager to a new temporary directory, named with the prefix `sandboxed-agents-context-` in the operating system's temporary directory, and runs one Podman command on it:

```sh
podman build --pull=always --no-cache --tag TAG --label ... --file CONTEXT/Containerfile CONTEXT
```

For each toolchain image, it writes the toolchain's recipe and the version recording script to a new temporary directory with the prefix `sandboxed-agents-toolchains-` and runs:

```sh
podman build --pull=never --no-cache --build-arg BASE_IMAGE=BASE_ID --tag TAG ... --label ... --file CONTEXT/Containerfile CONTEXT
```

`BASE_ID` is the Podman image ID of the base image just built or found, so the recipe starts from exactly the image its `base-image` label records. An image that is rebuilt under several tags gets one `--tag` for each. The executable removes each temporary directory whether its build succeeded or failed.

On Windows, the builds run in the Podman machine that the preflight checked: each call is `podman --connection NAME build …`, with the name of the [selected Podman machine](host-prerequisites.md#selected-podman-machine). `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or another default connection does not redirect them. On Linux, `build` calls the local `podman` without a connection.

Podman's output passes through. `build` prints `Built image TAG.` for each tag it built, the base image first, and on success a reminder that existing sandboxes keep their image until they are updated and that `list` marks them as outdated.

When the base image fails to build, `build` reports `podman build failed with exit status N`, issues no toolchain image build, and exits non-zero. When a toolchain image fails to build, `build` still builds the remaining ones. At the end it prints `Failed toolchain sets: SETS.` on standard error, followed by the same reminder, and exits non-zero. A failed set keeps its previous image under its tag, built on the previous base image.

`--pull=always` and `--no-cache` make every base image build start from the current Debian image and current packages. The executable never removes or retags an image. An image that an earlier build left behind, such as the image whose tag a rebuild took over, stays on the host until you remove it, for example with `podman image prune`.

## Image names and labels

The base image is named `localhost/sandboxed-agents:base-HASH`. A toolchain image is named `localhost/sandboxed-agents:toolchains-NAMES-HASH`, where `NAMES` are the names of its canonical toolchain set joined by `-`; the image for `native` is `localhost/sandboxed-agents:toolchains-native-HASH`. `HASH` is the full lowercase SHA-256 asset hash that `sandboxed-agents version` prints. The same set therefore always yields the same tag, and a new executable version yields new tags.

The names contain no controller group, so every controller group on the host that runs the same executable shares the same images (ADR-0005). A build in one group therefore rebuilds the images that the sandboxes of every group use.

Each image carries these labels:

| Label | Value | Carried by |
| --- | --- | --- |
| `io.github.sandboxed-agents.managed` | `true` | every image |
| `io.github.sandboxed-agents.asset-hash` | the asset hash, 64 lowercase hexadecimal digits | every image |
| `io.github.sandboxed-agents.toolchains` | the canonical toolchain set: sorted, deduplicated names separated by commas; empty for the base image | every image |
| `io.github.sandboxed-agents.base-image` | the Podman image ID of the base image it was built on, exactly as `podman image inspect` reports it in `Id` | toolchain images |

A toolchain image is current only when its `base-image` label equals the ID of the current base image. After a `build` in which a toolchain image failed, the old image of that set stays under its tag but is not current. `up` treats an image that is not current as missing and builds it on the current base image before it creates a sandbox, so a new sandbox is never created from an outdated image ([What `up` does](sandboxes.md#what-up-does)). `up` creates the container from the image ID it checked, not from the tag, so a tag moved to another image after the check does not change what the container runs. After its own build it inspects the tag again and refuses with `toolchain image TAG changed during its build; retry up` when the image there was not built on the base image it used. This pins one `up`; it does not lock the shared image store against other commands. After a successful `build`, every image of the current executable is current.

A build does not change existing sandboxes. Because every `build` rebuilds the base image and every existing toolchain image, each sandbox created before it, in any controller group, keeps running on its previous image. Replacing a sandbox's container with `update` (#52) and marking outdated sandboxes in `list` (#56) come with later Stories.

## Base image contents

- **Distribution:** Debian 12 (bookworm), from `docker.io/library/debian:bookworm-slim`, with glibc.
- **Node.js 24 with npm**, from the signed NodeSource APT repository. Node.js is the runtime for agents, and npm is the package manager that later Stories use to install them. The base image contains no agents and no toolchains.
- **Packages:** OpenSSH client and server, Git, the Debian `gh` package, tmux, curl, jq, ripgrep, less, unzip, bash, coreutils, findutils, procps, util-linux, tar, xz-utils, ca-certificates, and gnupg.
- **User:** `agent`, with UID and GID 1000.
- **Manager:** `/usr/local/bin/sandboxed-agents-manager`.
- **SSH server configuration:** `/usr/local/etc/sandboxed-agents/sshd_config`.
- **Workspace:** `/workspace`, owned by `agent`.

The image contains no SSH keys; host keys are removed after the OpenSSH packages are installed. The sshd configuration comes from the image and lies outside `/etc/ssh`, where each sandbox mounts its SSH server state volume, so sshd does not take its configuration from that volume.

The entrypoint runs as root. It creates `/run/sshd` and runs `sandboxed-agents-manager ssh start`, which generates the host keys that are missing in `/etc/ssh` and starts sshd with the configuration from the image. It then runs `sleep infinity` as `agent` through `runuser` ([SSH server](sandboxes.md#ssh-server)). No offline test starts sshd in a container built from this image; #29 validates images against real Podman. The entrypoint is not the only root process: the host executable makes its administrative control calls to the manager with `podman exec --user=0:0`, as root in the container. `sandboxed-agents shell` opens its shell with `podman exec --user=1000:1000`, so a shell runs as `agent`, UID and GID 1000 ([Shell and SSH access](ssh.md#open-a-shell)). `agents enable` installs an agent as `agent` too: the manager starts its own worker with UID and GID 1000 before that worker reads the home volume or runs npm ([Agents](agents.md#how-the-request-reaches-the-manager)). `agents run` starts the manager as `agent` with `podman exec --user=1000:1000`, and the manager starts the agent as `agent` as well. Toolchains used by agents and agent sessions, which a later Story adds, are to run as `agent` too ([Execution identities](sandboxes.md#execution-identities)).

### Version inventory

`/usr/local/share/sandboxed-agents/versions.tsv` records what the image contains. It is UTF-8 text with LF line endings and no header. Each line is a component name and its version, separated by one tab. The file lists every installed Debian package as reported by `dpkg-query`, followed by the lines for `node`, `npm`, and `manager`.

A toolchain image records its inventory again after its packages are installed, so its `versions.tsv` replaces the base image's file and also lists the toolchain's Debian packages and their dependencies.

## Toolchain image contents

A toolchain image is layered on the base image, so all toolchain images share the base layers. The `native` image adds:

- **Packages:** `build-essential`, `cmake`, `pkg-config`, and `ninja-build`, installed with APT without recommended packages.
- **Smoke check:** `/usr/local/share/sandboxed-agents/smoke/native.sh`, which checks that it runs as UID and GID 1000, then compiles a trivial C program with `cc` in a temporary directory and runs it. It is meant to run as `agent` inside a sandbox created from the image. No command runs it in this version; running each toolchain's smoke check against real Podman comes with #29.

The `native` recipe is covered by offline tests of the build context and of the Podman calls; it has not been built or run against real Podman.

## Toolchain availability on Debian 12

Debian 12 was chosen because every planned toolchain has packages for it. The Azure CLI documentation lists Debian 11 and 12 as tested distributions for its APT packages, but not Debian 13.

This record is based on the upstream documentation listed below. Of these toolchains, only native build tools are delivered, as `native`; .NET, Playwright, and Azure CLI come with #31, #32, and #30. #29 validates the images against real Podman.

| Toolchain | Upstream statement for Debian 12 | Source |
| --- | --- | --- |
| .NET | .NET 10, 9, and 8 are supported on Debian 12, as `dotnet-sdk-*` packages from the Microsoft package repository for x64. | [Install .NET on Debian](https://learn.microsoft.com/en-us/dotnet/core/install/linux-debian) |
| Playwright with browsers | Debian 12 and 13 are listed as supported Linux distributions, with Node.js 22, 24, or 26. | [Playwright installation](https://playwright.dev/docs/intro) |
| Azure CLI | The `azure-cli` APT package for x86_64 and ARM64 is tested on Debian 11 and 12. | [Install the Azure CLI on Linux](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli-linux) |
| Native build tools | `build-essential`, `cmake`, `pkg-config`, and `ninja-build` are packages in bookworm. | [build-essential](https://packages.debian.org/bookworm/build-essential), [cmake](https://packages.debian.org/bookworm/cmake), [pkg-config](https://packages.debian.org/bookworm/pkg-config), [ninja-build](https://packages.debian.org/bookworm/ninja-build) |

The Node.js runtime in the base image follows the same approach: NodeSource lists Node.js 24 packages for Debian 12 ([NodeSource distributions](https://github.com/nodesource/distributions/blob/master/DEV_README.md)), and Node.js lists version 24 as an LTS release ([Node.js releases](https://nodejs.org/en/about/previous-releases)).
