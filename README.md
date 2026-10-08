# sandboxed-agents

`sandboxed-agents` runs coding agents in rootless Podman containers, one sandbox per workspace, on Linux and Windows 11. An agent's mistake, such as deleting the wrong directory, installing a bad package, or reading credentials it should not have, stays inside one sandbox: its workspace, its home data, and the credentials stored there.

It is one executable on the host. It creates, starts, stops, updates, and removes sandboxes, installs agents into them, signs agents and integrations in, and sets up SSH access for editors on request. In this version it delivers:

- the agents GitHub Copilot CLI (`copilot`), Claude Code (`claude`), Codex CLI (`codex`), and OpenCode (`opencode`), installed from npm into the sandbox ([Agents](docs/agent-management.md));
- the integrations Git, GitHub, Azure, and Azure DevOps ([Integrations](docs/integrations.md));
- the optional toolchains `azure`, `dotnet`, `native`, and `playwright`, built into a sandbox's image ([Images](docs/images.md));
- persistent agent sessions that you detach from and reattach to, and opt-in SSH access for `ssh`, VS Code Remote SSH, and other desktop UIs ([Shell and SSH access](docs/ssh.md)).

A sandbox protects against an agent that does something wrong, not against code that actively tries to escape. [`SECURITY.md`](SECURITY.md) states the threat model, the protections, and the limits.

## Supported platforms

| Platform | Podman | Also needed |
| --- | --- | --- |
| Linux on amd64 | rootless Podman 4.4.0 or newer | |
| Windows 11 x64 workstation, build 22000 or later | Podman 5.0.0 or newer for the client and for the machine | a running, rootless Podman machine on WSL2 |

ARM64 hosts are not supported, on Windows also not through x64 emulation. Other operating systems have no preflight, and `sandboxed-agents check` reports that.

The Linux floor follows from `pasta` networking, which every sandbox uses and which Podman 4.4.0 introduced. On Windows, Podman 5.0.0 provides everything a sandbox needs. Both floors were confirmed from versioned upstream documentation and source, not on a live host ([Host prerequisites](docs/host-prerequisites.md#why-podman-440)).

## Host prerequisites

The host needs only Podman and OpenSSH, set up as follows. The executable never installs or configures any of them, and it never runs with `sudo`. `sandboxed-agents check` reports each missing prerequisite, and `build`, `up`, and `update` run the same check, the preflight, before they build or create anything.

On Linux:

- rootless Podman 4.4.0 or newer, with `podman`, `pasta` (the `passt` package), `newuidmap`, and `newgidmap` on `PATH`;
- a subordinate UID range and a subordinate GID range for your user in `/etc/subuid` and `/etc/subgid`;
- the unified cgroup v2 hierarchy, with the `cpu`, `memory`, and `pids` controllers delegated to your user;
- the OpenSSH client: `ssh`, `ssh-keygen`, and `ssh-keyscan` on `PATH`.

On Windows 11:

- the Podman client 5.0.0 or newer;
- a Podman machine on WSL2 that runs rootless with Podman 5.0.0 or newer, is running, and is the default or the only machine;
- cgroups v2 in the machine, with the `cpu`, `memory`, and `pids` controllers delegated;
- the Windows OpenSSH Client: `ssh`, `ssh-keygen`, and `ssh-keyscan` on `PATH`.

[Host prerequisites](docs/host-prerequisites.md) describes every check and its remedy.

## Install

Releases are currently published as GitHub prereleases of [`grauzone-dev/sandboxed-agents`](https://github.com/grauzone-dev/sandboxed-agents/releases), tagged `vX.Y.Z-preview.YYYYMMDD.N`. The stable version 1.0.0 is not published yet. The npm and NuGet registries still hold the prototype, version 0.2.0, under the names `sandboxed-agents` and `SandboxedAgents`, and previews are not published there, so install from the files of a prerelease.

Each prerelease offers three install methods:

| Method | Release file | Platforms |
| --- | --- | --- |
| Downloaded binary | `sandboxed-agents-linux-amd64` or `sandboxed-agents-windows-amd64.exe`, checked against `SHA256SUMS` | Linux, Windows |
| npm package | `sandboxed-agents-X.Y.Z-preview.YYYYMMDD.N.tgz`, installed with npm from the downloaded file | Linux, Windows |
| NuGet package | `SandboxedAgents.X.Y.Z-preview.YYYYMMDD.N.nupkg`, extracted and installed with its `install-command.ps1` in PowerShell 7 | Windows |

Every release file is attested: the release workflow creates SLSA build provenance, signed through GitHub Actions for this repository, for both binaries, `SHA256SUMS`, the `.tgz`, and the `.nupkg`, so each can be verified with `gh attestation verify`. Previews published before attestations were added have none.

Installing, upgrading, or removing the command never calls Podman and never touches sandboxes, SSH configuration, or sandbox data. The command is always `sandboxed-agents`; there is no official short alias.

The quick guides show each method step by step, with the checksum comparison, the attestation check, and placing the command on `PATH`. [Releases](docs/releases.md) describes the release files and the packages in detail.

## Get started

Follow the quick guide for your platform from installation to a signed-in agent session:

- [Quick guide: Linux](docs/quick-guide-linux.md)
- [Quick guide: Windows 11](docs/quick-guide-windows.md)

## Documentation

- [Command reference](docs/command-reference.md): every command and option, the exit status rule, and the order of checks.
- [`SECURITY.md`](SECURITY.md): threat model, default Podman options, and limits.

Topic pages:

- [Host prerequisites](docs/host-prerequisites.md): platforms, Podman floors, and the preflight of `check`.
- [Sandboxes](docs/sandboxes.md): controller groups, `up`, the workspace bind, `stop`, `start`, `restart`, `remove`, and `list`.
- [Check a sandbox](docs/check.md): the report of `check NAME`.
- [Images](docs/images.md): toolchains and `build`.
- [Update a sandbox](docs/updates.md): `update NAME`, `update --all`, and recovery of an interrupted update.
- [Agents](docs/agent-management.md): the agent catalog, enabling, signing in, runs, and agent sessions.
- [Integrations](docs/integrations.md): Git, GitHub, Azure, and Azure DevOps.
- [Shell and SSH access](docs/ssh.md): `shell`, the SSH setup, and VS Code Remote SSH.
- [Releases](docs/releases.md): release files, attestations, and the npm and NuGet packages.
- [Development](docs/development.md): building and testing the executable.
- [Live suite](docs/live-suite.md): the live test gate for releases.

[`CONTEXT.md`](CONTEXT.md) defines the terms used throughout, and [`docs/adr/`](docs/adr/) records the architecture decisions.

## Limitations

### Old images

No command of this version removes old images. Every `build` rebuilds the images under their existing tags, and `up` and `update` build a missing image, but the executable never removes or retags an image. An image that a rebuild replaced stays on the host until you remove it with Podman, for example with `podman image prune`.

### Other limitations

- Sandboxes created by the prototype (`sandboxed-ai-agents`) are not supported.
- Outbound networking from a sandbox is open, agents in one sandbox share its user, files, and credentials, and the kernel is shared with the host. [`SECURITY.md`](SECURITY.md#limits) lists every limit.

## Verification status

The behavior described in this repository is checked by an offline suite at the CLI boundary, against fake Podman and fake SSH programs, on Linux and on Windows. It shows what the executable does and which commands it accepts; it does not start a container, open an SSH connection, or sign in anywhere. Live runs against real Podman are recorded separately ([Live suite](docs/live-suite.md)), and following the quick guides on a clean machine is part of the manual release checklist. Where a topic page describes behavior, it also says what has and has not been confirmed on a live host.

## License

[MIT](LICENSE)
