# Images

A sandbox is created from an image: the base contents every sandbox needs plus the sandbox's toolchains. `sandboxed-agents build` builds the images from the build context embedded in the executable: the base image, and a toolchain image for each toolchain set that needs one. Sandboxes with the same toolchain set share one image.

## Toolchains

A toolchain is a set of SDKs or system packages built into a sandbox's image. All toolchains are off by default. `build --with SET`, `up NAME --with SET`, and `update NAME --with SET` select a toolchain set: a comma-separated list of toolchain names, such as `--with native`.

This version delivers four toolchains, which can be selected alone or in any combination:

| Toolchain | Contents |
| --- | --- |
| `azure` | Azure CLI from Microsoft's APT repository, with the Azure DevOps extension |
| `dotnet` | the .NET SDKs 8.0, 9.0, and 10.0 from Microsoft's APT repository for Debian 12 |
| `native` | the Debian packages `build-essential`, `cmake`, `pkg-config`, and `ninja-build` |
| `playwright` | Playwright `1.63.0` with Chromium, Firefox, and WebKit and the Debian packages they need |

`none` selects the base image without toolchains and has the same result as leaving `--with` out. It must stand alone: `--with none,native` is rejected.

The selection is a set. Order and repetition do not matter, so `--with native,azure,native` selects the same set as `--with azure,native`. The executable records a set in its canonical form: the names sorted and deduplicated, separated by commas, and the empty string for the base image.

An unknown or undelivered name, `none` combined with another name, an empty name, a name with surrounding spaces such as `native, native`, or a missing value is a usage error. The message lists the valid values, which in this version are `azure`, `dotnet`, `native`, `none`, and `playwright`. The command exits with status 1 before the preflight and before any Podman call, so the error is reported even when a prerequisite is missing or the sandbox belongs to another controller group.

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

For each toolchain image, it writes the recipe directory of each toolchain in the set, as `NAME/`, and the version recording script to a new temporary directory with the prefix `sandboxed-agents-toolchains-`. It composes one `Containerfile` there: `ARG BASE_IMAGE` and `FROM ${BASE_IMAGE}` once, then each toolchain's `Containerfile.fragment` in canonical order, then one final step that copies and runs the version recording script after all toolchains have installed. It runs one Podman build on that `Containerfile`:

```sh
podman build --pull=never --no-cache --build-arg BASE_IMAGE=BASE_ID --tag TAG ... --label ... --file CONTEXT/Containerfile CONTEXT
```

`BASE_ID` is the Podman image ID of the base image just built or found, so the image starts from exactly the image its `base-image` label records. The set `azure,native` is therefore one image built on the base image, not an `azure` image layered on a `native` one. An image that is rebuilt under several tags gets one `--tag` for each. The executable removes each temporary directory whether its build succeeded or failed.

On Windows, the builds run in the Podman machine that the preflight checked: each call is `podman --connection NAME build …`, with the name of the [selected Podman machine](host-prerequisites.md#selected-podman-machine). `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or another default connection does not redirect them. On Linux, `build` calls the local `podman` without a connection.

Podman's output passes through. `build` prints `Built image TAG.` for each tag it built, the base image first, and on success a reminder that existing sandboxes keep their image until they are updated and that `list` marks them as outdated.

When the base image fails to build, `build` reports `podman build failed with exit status N`, issues no toolchain image build, and exits non-zero. When a toolchain image fails to build, `build` still builds the remaining ones. At the end it prints `Failed toolchain sets: SETS.` on standard error, followed by the same reminder, and exits non-zero. A failed set keeps its previous image under its tag, built on the previous base image.

`--pull=always` and `--no-cache` make every base image build start from the current Debian image and current packages. The executable never removes or retags an image. An image that an earlier build left behind, such as the image whose tag a rebuild took over, stays on the host until you remove it, for example with `podman image prune`.

## Image names and labels

The base image is named `localhost/sandboxed-agents:base-HASH`. A toolchain image is named `localhost/sandboxed-agents:toolchains-NAMES-HASH`, where `NAMES` are the names of its canonical toolchain set joined by `-`; the image for `native` is `localhost/sandboxed-agents:toolchains-native-HASH`, and the image for `azure,native` is `localhost/sandboxed-agents:toolchains-azure-native-HASH`. `HASH` is the full lowercase SHA-256 asset hash that `sandboxed-agents version` prints. The same set therefore always yields the same tag, and a new executable version yields new tags.

The names contain no controller group, so every controller group on the host that runs the same executable shares the same images (ADR-0005). A build in one group therefore rebuilds the images that the sandboxes of every group use.

Each image carries these labels:

| Label | Value | Carried by |
| --- | --- | --- |
| `io.github.sandboxed-agents.managed` | `true` | every image |
| `io.github.sandboxed-agents.asset-hash` | the asset hash, 64 lowercase hexadecimal digits | every image |
| `io.github.sandboxed-agents.toolchains` | the canonical toolchain set: sorted, deduplicated names separated by commas; empty for the base image | every image |
| `io.github.sandboxed-agents.base-image` | the Podman image ID of the base image it was built on, exactly as `podman image inspect` reports it in `Id` | toolchain images |

A toolchain image is current only when its `base-image` label equals the ID of the current base image. After a `build` in which a toolchain image failed, the old image of that set stays under its tag but is not current. `up` treats an image that is not current as missing and builds it on the current base image before it creates a sandbox, so a new sandbox is never created from an outdated image ([What `up` does](sandboxes.md#what-up-does)). `update` treats such an image the same way before it replaces a sandbox's container, and it also creates the new container from the image ID ([When a sandbox is outdated](updates.md#when-a-sandbox-is-outdated)). `up` creates the container from the image ID it checked, not from the tag, so a tag moved to another image after the check does not change what the container runs. After its own build it inspects the tag again and refuses with `toolchain image TAG changed during its build; retry up` when the image there was not built on the base image it used. This pins one `up`; it does not lock the shared image store against other commands. After a successful `build`, every image of the current executable is current.

A build does not change existing sandboxes. Because every `build` rebuilds the base image and every existing toolchain image, each sandbox created before it, in any controller group, keeps running on its previous image. `sandboxed-agents update NAME` moves one sandbox to the current image for its toolchain set ([Update a sandbox](updates.md)). `list` marks each such sandbox as `running (outdated)` or `stopped (outdated)` ([Outdated sandboxes](sandboxes.md#outdated-sandboxes)).

## Base image contents

- **Distribution:** Debian 12 (bookworm), from `docker.io/library/debian:bookworm-slim`, with glibc.
- **Node.js 24 with npm**, from the signed NodeSource APT repository. Node.js is the runtime for agents, and npm is the package manager that later Stories use to install them. The base image contains no agents and no toolchains.
- **Packages:** OpenSSH client and server, Git, the Debian `gh` package, tmux, curl, jq, ripgrep, less, unzip, bash, coreutils, findutils, procps, util-linux, tar, tini, xz-utils, ca-certificates, and gnupg.
- **User:** `agent`, with UID and GID 1000.
- **Manager:** `/usr/local/bin/sandboxed-agents-manager`.
- **SSH server configuration:** `/usr/local/etc/sandboxed-agents/sshd_config`.
- **Workspace:** `/workspace`, owned by `agent`.

The image contains no SSH keys; host keys are removed after the OpenSSH packages are installed. The sshd configuration comes from the image and lies outside `/etc/ssh`, where each sandbox mounts its SSH server state volume, so sshd does not take its configuration from that volume.

The image's `ENTRYPOINT` starts `/usr/bin/tini` as PID 1, and tini runs the entrypoint as its child. Both run as root. The entrypoint creates `/run/sshd` and runs `sandboxed-agents-manager ssh start`, which generates the host keys that are missing in `/etc/ssh` and starts sshd with the configuration from the image. It then runs `sleep infinity` as `agent` through `runuser` ([SSH server](sandboxes.md#ssh-server)).

tini reaps orphaned processes. In the container, a process whose parent exits is re-parented to PID 1, and tini collects it when it ends, so it does not stay behind as a zombie that keeps a slot under the container's PID limit. sshd leaves such a process for every connection that ends before sign-in, for example each `ssh-keyscan` check of the host key; with `runuser` as PID 1, which waits only for its own child, they accumulated until no new process could start. A container created from an earlier image keeps `runuser` as PID 1 until `update` replaces it with a container of the rebuilt image ([Updates](updates.md)).

No offline test starts sshd in a container built from this image. The [image part of the live suite](live-suite.md#image-part) checks against real Podman that each sandbox has host keys of its own and, with a root-level probe of the base image, that the image holds none; no live run of it is recorded yet. The entrypoint is not the only root process: the host executable makes its administrative control calls to the manager with `podman exec --user=0:0`, as root in the container. `sandboxed-agents shell` opens its shell with `podman exec --user=1000:1000`, so a shell runs as `agent`, UID and GID 1000 ([Shell and SSH access](ssh.md#open-a-shell)). `agents enable` installs an agent as `agent` too: the manager starts its own worker with UID and GID 1000 before that worker reads the home volume or runs npm ([Agents](agent-management.md#how-the-request-reaches-the-manager)). `agents run` starts the manager as `agent` with `podman exec --user=1000:1000`, and the manager starts the agent as `agent` as well. `agents session` starts the manager as `agent` in the same way, and the manager runs the tmux server, the agent session, and the agent in it as `agent` ([Agents](agent-management.md#keep-an-agent-running-in-a-session)). Toolchains used by agents are to run as `agent` too ([Execution identities](sandboxes.md#execution-identities)).

### Version inventory

`/usr/local/share/sandboxed-agents/versions.tsv` records what the image contains. It is UTF-8 text with LF line endings and no header. Each line is a component name and its version, separated by one tab. The file lists every installed Debian package as reported by `dpkg-query`, followed by the lines for `node`, `npm`, and `manager`.

A toolchain image records the inventory once more, in its final step after every toolchain of the set has installed, so its `versions.tsv` replaces the base image's file and also lists the toolchains' Debian packages and their dependencies. A toolchain that installs something `dpkg-query` does not see adds a recorder script to `/usr/local/share/sandboxed-agents/versions.d/`; the recording script runs every `*.sh` file there and appends its lines after `manager`. The Azure CLI version is the `azure-cli` line among the `dpkg-query` lines, such as `2.90.0-1~bookworm`. The `azure` recorder adds one line, `azure-devops`, with the extension version that `az version` reports. The `dotnet` toolchain needs no recorder: its SDKs are Debian packages, so their versions are the `dotnet-sdk-8.0`, `dotnet-sdk-9.0`, and `dotnet-sdk-10.0` lines among the `dpkg-query` lines, as package versions that may include a packaging revision. The `playwright` recorder runs `/usr/local/share/sandboxed-agents/record-playwright.cjs` with Node. It reads the version of `@playwright/test` from that package's `package.json` and the version and revision of each browser download from `browsers.json` of the installed `playwright-core`. For each download, it checks that its directory under `/opt/playwright-browsers` holds Playwright's `INSTALLATION_COMPLETE` marker and fails otherwise. It launches no browser: the browser versions come from Playwright's manifest and are not read from the installed binaries. With Playwright `1.63.0` it adds these lines:

| Component | Version |
| --- | --- |
| `playwright` | `1.63.0` |
| `chromium` | `153.0.8010.12 (revision 1243)` |
| `chromium-headless-shell` | `153.0.8010.12 (revision 1243)` |
| `firefox` | `155.0 (revision 1543)` |
| `webkit` | `26.6 (revision 2359)` |
| `ffmpeg` | `revision 1011` |

## Toolchain image contents

A toolchain image is layered on the base image, so all toolchain images share the base layers. Each toolchain installs only its own contents: neither the base image nor the `native` or `dotnet` toolchain installs anything from Azure, neither the base image nor a set without `dotnet` installs a .NET SDK, and neither the base image nor a set without `playwright` installs Playwright or a browser.

The `native` toolchain adds:

- **Packages:** `build-essential`, `cmake`, `pkg-config`, and `ninja-build`, installed with APT without recommended packages.
- **Smoke check:** `/usr/local/share/sandboxed-agents/smoke/native.sh`, which checks that it runs as UID and GID 1000, then compiles a trivial C program with `cc` in a temporary directory and runs it.

The `azure` toolchain adds:

- **APT repository:** `https://packages.microsoft.com/repos/azure-cli/` for `bookworm`, component `main`, architecture `amd64`. APT accepts it only when it is signed with Microsoft's key. The recipe downloads that key from `https://packages.microsoft.com/keys/microsoft.asc`, stores it as `/etc/apt/keyrings/microsoft.gpg`, and names that file in the repository's `signed-by` option.
- **Azure CLI:** the `azure-cli` package, without a pinned version.
- **Azure DevOps extension:** the latest stable `azure-devops` extension from the official extension index, installed with `az extension add --system --name azure-devops --allow-preview false`. `--system` installs it into the system extension directory of the Python that the Azure CLI bundles under `/opt/az`, so `agent` sees it without `AZURE_EXTENSION_DIR`. The recipe then makes the extension readable for every user with `chmod -R a+rX`. During the build, the Azure CLI writes its configuration to a temporary `AZURE_CONFIG_DIR` that is removed afterwards, so the image keeps no Azure configuration. In a sandbox, `HOME` stays `/home/agent`, so the Azure CLI keeps its configuration and its sign-ins in the home volume.
- **`keyring` package:** version `25.7.0`, installed with `/opt/az/bin/python3 -m pip install --no-cache-dir keyring==25.7.0` into the Python environment the Azure CLI bundles under `/opt/az`. Neither the `azure-cli` package nor the extension ships it, but `az devops login` needs it. Without it, the extension tries to install `keyring` with pip itself during the login, which fails in a sandbox ([`credential_store.py`, release 20260902.1](https://github.com/Azure/azure-devops-cli-extension/blob/20260902.1/azure-devops/azext_devops/dev/common/credential_store.py#L21-L27)). pip adds `keyring` and whatever dependencies are missing. It keeps a package already installed in that environment when its version satisfies `keyring`'s requirements. [Azure DevOps login](integrations.md#azure-devops-login) uses it.
- **Version recorder:** `/usr/local/share/sandboxed-agents/versions.d/azure.sh` ([Version inventory](#version-inventory)).
- **Smoke check:** the command `az version`, run as UID and GID 1000, with no script file of its own. Its output is expected to list the `azure-devops` extension. The [image part of the live suite](live-suite.md#image-part) runs it and requires a non-empty version for `azure-cli` and for the `azure-devops` extension.

The image contains no Azure credentials. The `azure` and `azdo` login workflows need this toolchain ([Integrations](integrations.md)); the toolchain itself signs nothing in.

The `dotnet` toolchain adds:

- **APT repository:** `https://packages.microsoft.com/debian/12/prod` for `bookworm`, component `main`, architecture `amd64`, in `/etc/apt/sources.list.d/dotnet.list`. APT accepts it only when it is signed with Microsoft's key. The recipe downloads that key from `https://packages.microsoft.com/keys/microsoft.asc`, converts it with a temporary GnuPG home directory that it removes afterwards, and stores it with mode `0644` as `/etc/apt/keyrings/microsoft-dotnet.gpg`, which the repository's `signed-by` option names. The source list and the key file are separate from those of `azure`, so the two toolchains can be combined.
- **SDKs:** the packages `dotnet-sdk-8.0`, `dotnet-sdk-9.0`, and `dotnet-sdk-10.0`, installed with APT without recommended packages and without a pinned version ([.NET SDK versions](#net-sdk-versions)). They are installed system-wide in the image, not in the home or workspace volume, so every sandbox created from the image has them and `agent` can use them.
- **Smoke check:** the command `dotnet --list-sdks`, run as UID and GID 1000, with no script file of its own. Its output is expected to list every installed SDK. The [image part of the live suite](live-suite.md#image-part) runs it and requires an SDK of each series 8.0, 9.0, and 10.0.

The `playwright` toolchain adds:

- **Playwright:** the npm package `@playwright/test` at version `1.63.0` ([Playwright version](#playwright-version)), installed with `npm install --prefix /opt/playwright --save-exact` into `/opt/playwright/node_modules`, together with the matching `playwright` and `playwright-core` packages it depends on. The recipe removes npm's cache and the APT package lists afterwards.
- **Browsers:** Chromium, the Chromium headless shell, Firefox, and WebKit, with Playwright's FFmpeg build, installed with `playwright install --with-deps chromium firefox webkit` into `/opt/playwright-browsers`. `--with-deps` also installs the Debian packages the browsers need.
- **Command:** `/usr/local/bin/playwright`, a wrapper that sets `PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers` and runs `node /opt/playwright/node_modules/@playwright/test/cli.js` with its arguments. Because it sets the browser path itself, it finds the browsers in any environment and does not depend on a browser cache in the home or workspace volume.
- **Environment:** the image sets `PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers`, so a shell that `sandboxed-agents shell` opens finds the browsers too. The manager passes exactly this value on to the agents it starts, in runs and in agent sessions ([Agents](agent-management.md#how-the-request-reaches-the-manager)).
- **Permissions:** the recipe runs `chmod -R a+rX` on `/opt/playwright` and `/opt/playwright-browsers`, so every user can read their files, enter their directories, and run their executables, and `agent` can run Playwright and launch the browsers.
- **Version recorder:** `/usr/local/share/sandboxed-agents/versions.d/playwright.sh` ([Version inventory](#version-inventory)).
- **Smoke check:** `/usr/local/share/sandboxed-agents/smoke/playwright.sh`, which checks that it runs as UID and GID 1000, sets `PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers`, and runs `/usr/local/share/sandboxed-agents/smoke/playwright.cjs` with Node. That script loads the `playwright` package from `/opt/playwright` and launches Chromium, Firefox, and WebKit one after the other, headless and with no other launch option. For each browser it sets an in-memory HTML page, checks its title, prints the browser's name and version, and closes the browser even when a step fails. The check relaxes no Podman option; it is meant to pass under the default options of a sandbox.

Playwright and its browsers lie under `/opt`, outside the workspace, home, and SSH server state volumes, so every sandbox created from the image has them and no volume hides or keeps them.

`update NAME --with SET` changes the toolchain set of an existing sandbox and keeps its volumes ([Change the toolchain set](updates.md#change-the-toolchain-set)).

Each smoke check is meant to run as `agent` inside a sandbox created from the image. No command of the executable runs it. The [image part of the live suite](live-suite.md#image-part) runs each toolchain's smoke check through `sandboxed-agents shell` without a terminal, as UID and GID 1000, in a sandbox created with that toolchain alone, and first checks that the image defines the account `agent` with UID and GID 1000. No toolchain image has been built or run against real Podman, and no live run of the image part is recorded yet.

### Azure versions

The `azure` recipe pins only `keyring`, at `25.7.0`. The Azure CLI and the extension are resolved again at each build without the layer cache, like every other package, so a `build` installs their current stable releases (#3). `versions.tsv` records the versions an image actually contains ([Version inventory](#version-inventory)). It has no line for `keyring` or the packages pip installs with it, because `dpkg-query` does not see them and the `azure` recorder lists only the extension.

On 2026-10-04, these were the current releases:

| Component | Version | Source |
| --- | --- | --- |
| `azure-cli` package | `2.90.0-1~bookworm` | [Microsoft package index for bookworm, amd64](https://packages.microsoft.com/repos/azure-cli/dists/bookworm/main/binary-amd64/Packages) |
| `azure-devops` extension | `1.0.8`, release `20260902.1`, published 2026-09-03 | [Release 20260902.1](https://github.com/Azure/azure-devops-cli-extension/releases/tag/20260902.1) |

The repository setup follows [Install the Azure CLI on Linux](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli-linux). `--system` is documented in the [`az extension add` reference](https://learn.microsoft.com/en-us/cli/azure/extension#az-extension-add), and the location of the system extension directory is defined in the [Azure CLI 2.90.0 source](https://github.com/Azure/azure-cli/blob/azure-cli-2.90.0/src/azure-cli-core/azure/cli/core/extension/__init__.py).

### .NET SDK versions

The `dotnet` toolchain installs the three SDK series that Microsoft supports today side by side, so a sandbox can build projects that target any currently supported .NET version: .NET 10 is a long-term support release supported until 2028-11-14, and .NET 8 (long-term support) and .NET 9 (standard-term support) are both supported until 2026-11-10 ([.NET support policy](https://dotnet.microsoft.com/en-us/platform/support/policy/dotnet-core)). Previews, such as .NET 11, and series that are no longer supported are not installed.

Each package name fixes a series, not a release. Like every other package, the SDKs are resolved again at each build without the layer cache, so a `build` installs the current release within each series (#3). `versions.tsv` records the versions an image actually contains ([Version inventory](#version-inventory)). A series that reaches its end of support stays in the recipe, and so in every image built from it, until the recipe is changed.

The package source is Microsoft's production APT repository for Debian 12, which [Install .NET on Debian](https://learn.microsoft.com/en-us/dotnet/core/install/linux-debian) names for these SDKs on x64. On 2026-10-04 its [package index for bookworm, amd64](https://packages.microsoft.com/debian/12/prod/dists/bookworm/main/binary-amd64/Packages.gz) contained all three SDK packages.

### Playwright version

The `playwright` recipe pins `@playwright/test` at `1.63.0`. Each Playwright release works with the browser revisions it was released with and downloads exactly those ([Browsers](https://playwright.dev/docs/browsers)), and a Playwright package that does not match the installed browsers cannot find them ([Docker](https://playwright.dev/docs/docker)). Unlike the Azure CLI and the .NET SDKs, Playwright is therefore not resolved again at each build: the pin keeps the package and the browser revisions matching, and a newer Playwright comes with a change to the recipe. The Debian packages that `--with-deps` installs are resolved again at each build, like every other package.

`1.63.0` is the current stable release, published on 2026-09-04 ([Release v1.63.0](https://github.com/microsoft/playwright/releases/tag/v1.63.0)) and the `latest` version on npm on 2026-10-06. Playwright lists Debian 12 and Node.js 24, which the base image uses, as supported ([Playwright installation](https://playwright.dev/docs/intro)). Chromium, Firefox, and WebKit are the three browser engines Playwright supports, so a sandbox can run cross-browser tests.

Playwright `1.63.0` installs these browser downloads ([`browsers.json` at v1.63.0](https://github.com/microsoft/playwright/blob/v1.63.0/packages/playwright-core/browsers.json)):

| Download | Browser version | Revision |
| --- | --- | --- |
| Chromium | `153.0.8010.12` | `1243` |
| Chromium headless shell | `153.0.8010.12` | `1243` |
| Firefox | `155.0` | `1543` |
| WebKit | `26.6` | `2359`, with no override for Debian 12 |
| FFmpeg | - | `1011` |

#### Use Playwright in a project

The `playwright` command runs the preinstalled Playwright with the image's browsers, for example `playwright --version` or `playwright show-report`. A project whose tests import `@playwright/test` needs that package in its own dependencies, at the preinstalled version `1.63.0`: nothing makes the package under `/opt/playwright` resolve for a project's imports. With that version, the project's Playwright uses the image's browsers when its process has `PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers` in its environment. A shell has it from the image, and an agent started with `agents run` or `agents session` gets it from the manager, so commands they run, such as `npx playwright test`, need no prefix. A process started some other way that does not inherit the variable needs it set explicitly, for example `PLAYWRIGHT_BROWSERS_PATH=/opt/playwright-browsers npx playwright test`; without it, Playwright looks for browsers in its default cache under the home directory, where the image installs none. Another Playwright version does not match the image's browsers.

## Toolchain availability on Debian 12

Debian 12 was chosen because every planned toolchain has packages for it. The Azure CLI documentation lists Debian 11 and 12 as tested distributions for its APT packages, but not Debian 13.

This record is based on the upstream documentation listed below. All four toolchains are delivered, as `native`, `azure`, `dotnet`, and `playwright`. The [image part of the live suite](live-suite.md#image-part) builds and checks the images against real Podman; no live run of it is recorded yet.

| Toolchain | Upstream statement for Debian 12 | Source |
| --- | --- | --- |
| .NET | .NET 10, 9, and 8 are supported on Debian 12, as `dotnet-sdk-*` packages from the Microsoft package repository for x64. | [Install .NET on Debian](https://learn.microsoft.com/en-us/dotnet/core/install/linux-debian) |
| Playwright with browsers | Debian 12 and 13 are listed as supported Linux distributions, with Node.js 22, 24, or 26. | [Playwright installation](https://playwright.dev/docs/intro) |
| Azure CLI | The `azure-cli` APT package for x86_64 and ARM64 is tested on Debian 11 and 12. | [Install the Azure CLI on Linux](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli-linux) |
| Native build tools | `build-essential`, `cmake`, `pkg-config`, and `ninja-build` are packages in bookworm. | [build-essential](https://packages.debian.org/bookworm/build-essential), [cmake](https://packages.debian.org/bookworm/cmake), [pkg-config](https://packages.debian.org/bookworm/pkg-config), [ninja-build](https://packages.debian.org/bookworm/ninja-build) |

The Node.js runtime in the base image follows the same approach: NodeSource lists Node.js 24 packages for Debian 12 ([NodeSource distributions](https://github.com/nodesource/distributions/blob/master/DEV_README.md)), and Node.js lists version 24 as an LTS release ([Node.js releases](https://nodejs.org/en/about/previous-releases)).

## Hardening options evaluated

The review of the base image build ([#79](https://github.com/grauzone-dev/sandboxed-agents/pull/79#issuecomment-5969557084)) proposed two supply-chain hardening options. #29 evaluated them. Neither is implemented in this version.

- **Checking the NodeSource signing key against a pinned fingerprint.** Adopted, through [#132](https://github.com/grauzone-dev/sandboxed-agents/issues/132). The base recipe downloads the NodeSource key from `deb.nodesource.com`, the host that also serves the Node.js packages, and trusts it without comparing it with a known value, so whoever can serve another key from that host can also serve packages signed with it. #132 keeps the expected fingerprint in the build context and makes the base image build fail when the downloaded key does not match. NodeSource publishes no fingerprint for this key in its documentation or setup scripts, so the pinned value is the key as it was fetched and reviewed when the pin was added: the check detects a later change of the key, not a key that was already substituted then.
- **Recording the digest of the pulled Debian image and of each built image beside `versions.tsv`.** Declined. An image cannot contain its own digest, because writing the digest into the image changes the image. Nothing about images is stored in host state, and the images are built locally and never pushed to a registry, so a built image's digest would have no place to be kept and no one to compare it with. A toolchain image already records the ID of its base image in its `base-image` label. The digest of the pulled Debian image alone would not make an image reproducible, because every `build` resolves all packages again without the layer cache, and `versions.tsv` already records what an image contains.
