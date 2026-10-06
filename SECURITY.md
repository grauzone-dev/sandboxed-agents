# Security

`sandboxed-agents` runs coding agents in rootless Podman containers, one sandbox per workspace. This document states what a sandbox protects against and what it does not, so that you can decide what to trust it with. The limits below are part of the design and are stated as plainly as the protections.

## Threat model

A sandbox is built against an agent that does something wrong: it deletes the wrong directory, installs a bad package, or reads credentials it should not have. Such a mistake stays inside one sandbox, which means inside its workspace, its home data, and the credentials stored there.

A sandbox is not built against code that actively tries to escape from it. A sandbox shares the host's kernel, and a flaw in the kernel, in Podman, or in the container runtime can let such code out. If you run code that you expect to attack its isolation, use a dedicated virtual machine.

A sandbox also does not defend against programs that already run as your user on the host. Such a program can use your Podman directly, so it can change, inspect, or replace any sandbox.

## Protections

### The host boundary

A sandbox has three named volumes: the workspace, the home data, and the SSH server state ([ADR-0003](docs/adr/0003-workspace-is-the-only-host-bind.md)).

- Only `/workspace` may be a host bind, and only one host directory may be bound there, with `up NAME WORKSPACE`. Without `WORKSPACE`, the workspace is a named volume.
- The home data at `/home/agent` and the SSH server state at `/etc/ssh` are always named volumes.
- Host credentials, SSH-agent sockets, container-engine sockets, and display sockets are never mounted. No host path other than the one workspace bind is mounted.
- A workspace bind is refused when the directory equals, contains, or lies inside a protected host path: the executable, host state, the system temporary directory (and on Linux always `/tmp`), and your `.ssh` directory. Symlinks, junctions, and other aliases are resolved before this comparison, so they do not make a protected path bindable. Your home directory contains `.ssh` and therefore cannot be bound as a whole. [Workspace guards](docs/sandboxes.md#workspace-guards) lists every case.

### Default Podman options

`up` creates every sandbox container with these options, spelled exactly as the executable passes them to Podman:

- `--userns=keep-id:uid=1000,gid=1000`: your host user maps to the user `agent`, UID and GID 1000, in the container.
- `--user=0:0`: the container starts as container root, which is not root on the host (see [Execution identities](#execution-identities)).
- `--security-opt=no-new-privileges`: no process in the container can gain privileges, for example through a setuid or setgid program or file capabilities.
- `--network=pasta:--no-map-gw`: the container gets its own network through pasta, and the gateway address is not mapped to the host, so the container cannot reach the host through it ([Why Podman 4.4.0](docs/host-prerequisites.md#why-podman-440)).
- `--memory=8589934592`: at most 8 GiB of memory (`8g`).
- `--cpus=4`: at most 4 CPUs.
- `--pids-limit=2048`: at most 2048 processes.
- `--shm-size=1073741824`: 1 GiB of shared memory (`1g`).
- `--publish 127.0.0.1:PORT:22`: the SSH server on port 22 in the container is published on host loopback only. It is the only port a sandbox publishes.
- `--mount type=volume,source=sandboxed-agents.GROUP.NAME.workspace,target=/workspace`: the workspace volume.
- `--mount type=volume,source=sandboxed-agents.GROUP.NAME.home,target=/home/agent`: the home volume.
- `--mount type=volume,source=sandboxed-agents.GROUP.NAME.ssh,target=/etc/ssh`: the SSH server state volume.

In these options, `GROUP` is the controller group (`default` unless `SANDBOXED_AGENTS_GROUP` selects another) and `NAME` is the sandbox name. `PORT` is the SSH port on `127.0.0.1`: `up` picks a free one when it creates the sandbox, or uses the one given with `--port`, and records it on the container ([Port](docs/sandboxes.md#port)). The four resource limits show their defaults in bytes, CPUs, and processes, as the executable passes them; `up --memory SIZE`, `--cpus N`, `--pids-limit N`, and `--shm-size SIZE` replace them when the sandbox is created.

With `up NAME WORKSPACE`, the workspace volume mount is replaced by exactly one bind of the resolved host directory, `--mount type=bind,source=WORKSPACE,target=/workspace`, and no workspace volume is created. On Windows the source is the directory's path in the Podman machine, such as `/mnt/c/Users/me/project` for `C:\Users\me\project` ([Windows paths](docs/sandboxes.md#windows-paths)). The home and SSH server state volumes stay as listed.

The create call also carries `--name` and `--label` options. They name the container and record its owner and configuration; they do not change its isolation ([Podman names and labels](docs/sandboxes.md#podman-names-and-labels)).

The resource limits keep a runaway agent from exhausting the host's memory, CPUs, or process table. Rootless Podman can apply them only when cgroup v2 delegates the `memory`, `cpu`, and `pids` controllers to your user, which the preflight checks ([Host prerequisites](docs/host-prerequisites.md)).

### Execution identities

Two identities run in a sandbox ([ADR-0006](docs/adr/0006-podman-target-and-execution-identity.md)).

**Container root** is UID 0 in the container. Under the user namespace mapping it is an ID from your subordinate ID range, not root on the host and not your host user. Container root runs administrative work only:

- the entrypoint, which prepares the container, has the in-container manager start the SSH server (sshd), and then starts the container's long-running process as `agent`;
- sshd itself;
- the in-container manager for administrative operations. The executable makes each administrative call to the manager with `podman exec --user=0:0`, so it runs as container root whatever user the container started with. Examples are the session query, the version check that the manager answers, and the authorization and removal of the SSH setup's key;
- `fingerprint NAME`, which reads the public host keys with `podman exec --user=0:0` and `cat`.

**`agent`**, UID and GID 1000, is the host user under the user namespace mapping. On Windows it is the user of the Podman machine, not your Windows account. Shells, toolchains, agents, and agent sessions run as `agent`:

- `shell NAME` opens its shell with `podman exec --user=1000:1000`, and an SSH session signs in as `agent`.
- `agents run`, `agents session`, `agents login`, and the `integrations` workflows start the manager directly as `agent` with `podman exec --user=1000:1000`. The manager refuses these requests under any other identity and starts the agent, the agent session, or the login program with an explicit request for UID and GID 1000.
- For agent installation, removal, and status (`agents enable`, `up --agents`, `agents disable`, `agents status`, and the agent query of `list`) and for the session query, the manager is called as container root and starts itself again as a worker with UID and GID 1000. Only that worker reads the home volume, runs npm, runs an agent's status probe, or reads the agent sessions.

The two kinds of call stay apart. A `podman exec` that addresses the manager for an administrative operation runs as container root and starts no agent. A `podman exec` that starts a shell, a workflow, or an agent runs as `agent`. No agent process runs as container root.

### SSH access

The SSH server is reachable only from your own machine, on `127.0.0.1:PORT`. It accepts public-key authentication only, only for the user `agent`, with no root login and no agent forwarding, and it reads authorized keys only from `/etc/ssh/authorized_keys` in the SSH server state volume. A new sandbox has no authorized key until you install the opt-in SSH setup, which uses a key dedicated to that sandbox and pins its host key. Without `--ssh-config` or `ssh-config NAME --install`, the executable leaves your SSH files untouched ([ADR-0002](docs/adr/0002-ssh-access-with-opt-in-host-setup.md), [Shell and SSH access](docs/ssh.md)).

## Limits

These limits hold for every sandbox. Weigh them before you give a sandbox access to a workspace or an account.

- **Outbound networking is open.** A sandbox can reach the internet and any network the host can reach. There is no egress control and no allowlist. An agent can send the files of its workspace and the credentials stored in its sandbox to any server.
- **Agents in one sandbox share everything in it.** All agents in a sandbox run as the same user, `agent`, with the same home, the same credentials, and the same workspace. One agent can read and change another agent's login, configuration, and files. To separate agents or accounts from each other, use separate sandboxes.
- **The kernel is shared with the host.** A sandbox is a container, not a virtual machine. Code that exploits a flaw in the kernel or the container runtime can escape it.

The current version also has these limits:

- **A bound workspace is your real directory.** Agents read and change its files directly, and whatever they write there reaches the host as written. A host program that runs that content, such as a Git hook, a build script, or an editor task, runs it with your rights on the host.
- **The workspace guards check paths, not every file.** A file inside a protected directory, such as `~/.ssh`, that shares a hard link with a file in the workspace is not detected, and neither is a symlink or junction inside a protected directory that points into the workspace. The guards check the file system only when `up` is given `WORKSPACE`; a link created later is not detected. On Linux, `up` does not scan the workspace for symlinks ([Workspace guards](docs/sandboxes.md#workspace-guards)).
- **An existing container is started as it is.** The owner label authorizes a command on a container, but it does not attest how that container was configured. `up`, `start`, and `restart` start an existing container of the current owner without comparing its image, mounts, user, user namespace, network, or security options with the defaults above. A container that carries the owner label but was created or changed directly with Podman runs with the configuration it has. Creating or changing such a container needs access to your Podman, which is already more than a sandbox may reach ([Existing sandboxes](docs/sandboxes.md#existing-sandboxes)).
- **On Windows, the Podman machine is bound by name.** Each command selects one Podman machine and names it in every later Podman call with `--connection NAME`. `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` are removed from those calls, so an ambient setting does not send a command elsewhere. The name points to an entry in your user's Podman connection configuration, and the executable does not verify which endpoint it reaches. A program running as your user can change that configuration, the machine list, or the machine itself ([Selected Podman machine](docs/host-prerequisites.md#selected-podman-machine)).
- **On Linux, a remote Podman setting is followed.** The executable calls the local `podman` without naming a connection. If `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or a connection in `containers.conf` points Podman at another machine, the commands act there, while the preflight still describes only the local host ([Limits of the check](docs/host-prerequisites.md#limits-of-the-check)).
- **Container root has full control inside the sandbox.** Container root is not root on the host, but inside the sandbox it can read and change everything, including the home data and credentials of `agent`.
- **SSH TCP forwarding is not disabled.** A client signed in with the sandbox's key can forward connections through the sandbox, which reaches what the sandbox's network reaches.

## Verification status

The offline suite checks the arguments the executable passes to Podman, against a fake `podman` on Linux and on Windows. One of its tests runs `up` and fails when an option of the container creation call is missing from this document. Offline tests cannot show isolation: they do not start a container. Nothing in this document has been confirmed against real Podman on a live host, neither the user namespace mapping, the start and exec users, `no-new-privileges`, the network, the mounts, the resource limits, nor the target binding on Windows. That evidence belongs to the [live suite](docs/live-suite.md) and counts only from recorded live runs.
