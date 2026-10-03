# Sandboxes

A sandbox is one rootless Podman container with three named volumes of its own: the workspace, the home data, and the SSH server state. `sandboxed-agents up NAME` creates a sandbox with safe defaults and leaves it running, or starts a sandbox that already exists.

`up` first runs the preflight for the host operating system: the Linux preflight on Linux and the Windows preflight on Windows ([Host prerequisites](host-prerequisites.md)). On any other operating system, the preflight reports that no prerequisite check is available, and `up` stops before it looks up or creates anything.

In this version every sandbox belongs to the controller group `default`, and `up` uses fixed defaults. Selecting another controller group comes with #21, options that override the resource limits with #16, and binding a host directory as the workspace with #26.

## Create or start a sandbox

```sh
sandboxed-agents up NAME
```

`up` takes exactly one sandbox name and no options in this version. A usage error prints a message and a usage line on standard error, calls no Podman command, and exits with status 1:

- Without a name, `up` reports the missing sandbox name.
- The first word is always read as the sandbox name, so an option in its place, such as `-x`, is reported as an invalid sandbox name.
- A word after the name is reported as an unexpected argument, or as an unknown option when it starts with `-`.

### Sandbox names

A sandbox name matches `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`: it starts with a letter or digit and continues with letters, digits, `_`, `.`, and `-`. A name such as `-x`, `.x`, `a/b`, or `a b` is rejected before any Podman command runs. No name is reserved, so `up`, `list`, `default`, and `backup` are valid sandbox names (ADR-0004).

### What `up` does

1. It checks the command line and the sandbox name.
2. It runs the preflight and prints its lines. On Linux, the preflight's only Podman call is `podman --version`. If a prerequisite is missing, `up` reports `host prerequisites are missing` and exits with status 1. On Windows, the preflight runs read-only queries of the Podman client and the selected Podman machine, including commands in the machine through `podman machine ssh`. If a required prerequisite is missing or could not be checked, `up` exits with status 1. When the preflight fails, `up` stops before it looks up, creates, or changes any sandbox object.
3. It looks up the container and the three volumes of the sandbox by their exact Podman names (see [Podman names and labels](#podman-names-and-labels)) and reads the owner label of each one that exists. It looks at no other container or volume. When no container exists but volumes do, it checks the owners of those volumes here.
4. It checks the owners of an existing container and its volumes, and then looks up the backup container of an interrupted update and checks its owner. It refuses to continue on an owner conflict or an interrupted update (see [Refusals](#refusals)).
5. If the container exists, `up` starts it when it is stopped and leaves it alone when it is running (see [Existing sandboxes](#existing-sandboxes)).
6. Otherwise `up` creates the sandbox:
   - If the base image does not exist, it builds it first, as `sandboxed-agents build` does ([Images](images.md)). It never rebuilds an existing image; that is what `build` is for. The image name contains no controller group, so an image that another controller group built with the same executable counts as existing (ADR-0005).
   - It creates each of the three volumes that does not exist yet and adopts each one that does (see [Kept volumes](#kept-volumes)). It prints one line per volume, `Created volume NAME.` or `Adopted volume NAME.`.
   - It creates the container from the base image with the defaults below and starts it.

When the sandbox runs, `up` prints `Sandbox NAME is running.` and exits with status 0. Podman's own output, such as that of a build, passes through.

If a Podman command fails, `up` stops, reports the command and its exit status, and exits with status 1. It does not remove what it created before the failure. Volumes it created carry the owner label, so the next `up` adopts them.

## Podman names and labels

The executable derives every Podman name from the controller group and the sandbox name (ADR-0005). For the sandbox `NAME` in the `default` group:

| Object | Podman name | Mounted at |
| --- | --- | --- |
| Container | `sandboxed-agents.default.NAME` | |
| Workspace volume | `sandboxed-agents.default.NAME.workspace` | `/workspace` |
| Home volume | `sandboxed-agents.default.NAME.home` | `/home/agent` |
| SSH server state volume | `sandboxed-agents.default.NAME.ssh` | `/etc/ssh` |
| Backup container of an interrupted update | `sandboxed-agents-backup.default.NAME` | |

A volume name is the container name followed by one more dot component, `workspace`, `home`, or `ssh`, which names the role of the volume. The role names contain no dot, so the last component always identifies the role, and everything before it is the container name of exactly one sandbox. Sandbox names may contain dots, but two different sandbox names never yield the same volume name. For example, the sandbox `a.home` has the volumes `sandboxed-agents.default.a.home.workspace`, `….a.home.home`, and `….a.home.ssh`, none of which is a volume of the sandbox `a`.

The container and the volumes carry these labels:

| Label | Value | Carried by |
| --- | --- | --- |
| `io.github.sandboxed-agents.owner` | the controller group, `default` in this version | the container and each of the three volumes |
| `io.github.sandboxed-agents.sandbox-name` | the sandbox name | the container |
| `io.github.sandboxed-agents.workspace-kind` | `volume` | the container |

The owner label is the authoritative check: a container or volume with a matching name but a missing or different owner label belongs to no sandbox of this controller group, and `up` neither changes nor adopts it.

## Container defaults

`up` creates the container with these Podman options:

| Setting | Value |
| --- | --- |
| User namespace | `--userns=keep-id:uid=1000,gid=1000`: your host user maps to the user `agent` (UID and GID 1000) in the container |
| Start user | `--user=0:0`: with `keep-id`, Podman starts the container as the mapped user unless `--user` is given, which would override the image's `USER root` ([podman-create(1), `--userns`](https://docs.podman.io/en/latest/markdown/podman-create.1.html#userns-mode)). The entrypoint therefore starts as root in the container, creates `/run/sshd`, and then runs `sleep infinity` as `agent` through `runuser` ([Images](images.md#base-image-contents)). Your host user still maps to `agent`; root in the container maps to an ID from your subordinate range, not to root on the host. |
| Privileges | `--security-opt=no-new-privileges` |
| Network | `--network=pasta:--no-map-gw`: the container cannot reach the host through the gateway address ([Why Podman 4.4.0](host-prerequisites.md#why-podman-440)) |
| Memory | 8 GiB (`--memory=8g`) |
| CPUs | 4 (`--cpus=4`) |
| Processes | 2048 (`--pids-limit=2048`) |
| Shared memory | 1 GiB (`--shm-size=1g`) |

The container mounts its three named volumes and nothing else: no host path, no SSH-agent socket, no container-engine socket, and no display socket (ADR-0003). The resource limits need delegated cgroup v2 controllers, which the preflight checks.

A sandbox created by this version does not yet offer:

- a published SSH port (#18) or an SSH setup on the host (#19);
- installed agents (#69);
- toolchains (#28).

`up` opens no SSH connection to the sandbox and changes no host SSH file. On Windows, the preflight runs its read-only machine checks through `podman machine ssh`. These checks run in the Podman machine, not in the sandbox.

## Existing sandboxes

When the container of the sandbox exists with the current owner, and no volume or backup container under its names has a missing or different owner, `up` starts the container if it is stopped and exits with status 0. If it already runs, `up` exits with status 0 without a change. In both cases it creates, removes, and reconfigures no container or volume, and it does not check or build the image. `up` never changes the configuration of an existing sandbox.

An option given to `up` that differs from the configuration recorded on an existing sandbox will make `up` exit non-zero without starting the sandbox, naming the difference and the way to change it. `up` has no such option in this version; the Stories that add options apply this rule to them.

### Kept volumes

Volumes can outlive their container, for example when the container was removed with `podman rm`, or when an earlier `up` failed after creating them. When no container of the sandbox exists but some or all of its volumes do, and each of them carries the current owner, `up` adopts them: it creates only the missing volumes and a new container on all three. The files in an adopted volume are kept.

## Refusals

`up` refuses to act, exits with status 1, and creates and changes nothing in these cases:

- **Owner conflict.** The container, one of the three volumes, or the backup container exists under the sandbox's Podman name, and its owner label is missing or names another controller group. This includes a container with the current owner when one of its volumes does not have it. The message starts with `owner conflict`, names every such object, and points to Podman: remove or rename each foreign object there. `up` repairs and adopts nothing.
- **Interrupted update.** The backup container `sandboxed-agents-backup.default.NAME` exists and carries the current owner. The message names the backup container and `sandboxed-agents update NAME`. This version has no `update` command yet; it comes with a later Story. Until then, `up` refuses such a sandbox.

If a Podman lookup itself fails or returns output that `up` cannot read, `up` also stops with status 1, reports the failure with Podman's message, and changes nothing.

### Order of checks

`up` runs its checks in the order described in [Development](development.md#order-of-checks) and reports only the first failure:

| Step | What `up` does at this step |
| --- | --- |
| 1. Usage and names | reports a usage error or an invalid sandbox name, before any Podman call |
| 2. Preflight | reports a missing host prerequisite. On Windows, it also reports a required prerequisite that could not be checked. |
| 3. Sandbox existence | looks up the container and the volumes. An unknown name is no failure: it is a sandbox to create. When only volumes of the sandbox remain, reports an owner conflict on those volumes. |
| 4. Owner | reports an owner conflict on an existing container or its volumes, then on the backup container |
| 5. Interrupted update | reports a backup container with the current owner |
| 7. Preconditions | reserved for an option that conflicts with the recorded configuration; none exists in this version |

An invalid name is therefore reported ahead of a missing prerequisite, and a missing prerequisite ahead of an owner conflict or a backup container. An owner conflict is reported ahead of an interrupted update.

## Verification

The behavior on this page is covered by offline tests against fake `podman` and `ssh` programs ([Development](development.md#test-seams)). No offline test starts a real container, and nothing on this page has been confirmed against Podman on a live host.
