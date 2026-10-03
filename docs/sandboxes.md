# Sandboxes

A sandbox is one rootless Podman container with three named volumes of its own: the workspace, the home data, and the SSH server state. `sandboxed-agents up NAME` creates a sandbox with safe defaults and leaves it running, or starts a sandbox that already exists. `sandboxed-agents remove NAME` deletes the container of a sandbox and keeps its volumes unless you ask otherwise (see [Remove a sandbox](#remove-a-sandbox)). `stop`, `start`, and `restart` change whether an existing sandbox runs and keep everything else ([Stop, start, and restart a sandbox](#stop-start-and-restart-a-sandbox)).

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

Volumes can outlive their container, for example when the container was removed with `sandboxed-agents remove NAME` or `podman rm`, or when an earlier `up` failed after creating them. When no container of the sandbox exists but some or all of its volumes do, and each of them carries the current owner, `up` adopts them: it creates only the missing volumes and a new container on all three. The files in an adopted volume are kept.

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

## Remove a sandbox

```sh
sandboxed-agents remove NAME [--volumes] [--force]
```

`remove` deletes the container of the sandbox. A running sandbox is stopped first; you do not have to stop it yourself. Without `--volumes`, all three volumes are kept, and a later `up NAME` adopts them ([Kept volumes](#kept-volumes)). `remove` asks for no confirmation and does not read standard input. It runs no preflight. It accepts the same sandbox names as `up`, and a usage error prints a message and the usage line, calls no Podman command, and exits with status 1.

| Option | Effect |
| --- | --- |
| `--volumes` | Also deletes the sandbox's volumes that carry the current owner. A volume with a missing or different owner is never deleted. |
| `--force` | Removes a running sandbox even while agent sessions run in it or the manager does not answer, and ends those sessions. |

`remove` prints one line for the removed container and one line per removed or kept volume, each with its Podman name. It changes no host SSH file; removing the host side of a sandbox's SSH setup comes with #37.

### Running agent sessions

Before it stops a running sandbox, `remove` asks the manager in the container for the running agent sessions:

```sh
podman exec sandboxed-agents.default.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

The manager answers with a JSON array of objects, one per running session, each with a non-empty `name` and a non-empty `agent` string. `remove` names each session as `AGENT/NAME`. If the query fails, returns no such array, or does not finish within 5 seconds, the manager counts as not answering. A stopped sandbox has no running sessions, so `remove` does not ask.

- **Sessions run.** `remove` refuses, names each running session, and names `--force`. With `--force` it removes the sandbox and names the sessions it ended.
- **No answer.** `remove` refuses, says that running sessions cannot be ruled out, and names `--force`. With `--force` it still asks the manager. If the manager still does not answer, `remove` removes the sandbox and says that sessions that may have been running were ended and cannot be named.

In this version the manager always answers with an empty list, because agent sessions arrive with #45. A sandbox created by this version therefore never refuses on running sessions as long as its manager answers.

### Owners and kept volumes

`remove` checks the owner label of the container and of all three volumes, like `up` ([Podman names and labels](#podman-names-and-labels)). On a refusal it exits with status 1 and stops, removes, or changes nothing.

| Situation | `remove NAME` | `remove NAME --volumes` |
| --- | --- | --- |
| Neither the container nor a volume exists | refuses: the sandbox is unknown | refuses: the sandbox is unknown |
| The container has a missing or different owner | refuses with an owner conflict | refuses with an owner conflict |
| The backup container `sandboxed-agents-backup.default.NAME` exists with the current owner, and no object is foreign | refuses and names `sandboxed-agents update NAME` | refuses and names `sandboxed-agents update NAME` |
| The container has the current owner, a volume does not, and no backup container exists | refuses with an owner conflict | removes the container and the owned volumes, keeps the other volume, names it, points to Podman to remove or rename it, and exits with status 1 |
| A volume has a missing or different owner, and a backup container exists | refuses with an owner conflict | refuses with an owner conflict, also with `--force` |
| No container, and only volumes with the current owner remain | reports that no container exists, names `remove NAME --volumes`, deletes nothing, and exits with status 0 | deletes those volumes, names each, and exits with status 0 |
| No container, and a remaining volume has a missing or different owner | refuses with an owner conflict | refuses with an owner conflict |

An owner-conflict message names each foreign object by its Podman name and points to Podman, where you remove or rename it. A backup container with a missing or different owner is an owner conflict as well. A foreign object is reported ahead of an interrupted update. `remove NAME --volumes` on a sandbox with a foreign volume and no backup container is the only command that acts on a sandbox with an owner conflict; it touches only objects that carry the current owner.

A bound host directory is never deleted, with or without `--volumes`. Binding a host directory as the workspace comes with #26; the workspace volume left unused by such a bind counts as one of the sandbox's volumes.

### Order of checks for `remove`

`remove` reports only the first failure, in the order described in [Development](development.md#order-of-checks):

| Step | What `remove` does at this step |
| --- | --- |
| 1. Usage and names | reports a usage error or an invalid sandbox name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox. When only volumes remain, checks their owners. |
| 4. Owner | checks the owners of the container, the volumes, and the backup container, and reports all foreign objects together in one owner conflict. With `--volumes`, an owned container, and no backup container, a foreign volume is kept instead of reported here. |
| 5. Interrupted update | reports a backup container with the current owner when no object is foreign |
| 7. Preconditions | on a running sandbox, reports a manager that does not answer, unless `--force` is given |
| 9. Session guard | on a running sandbox, reports running agent sessions, unless `--force` is given |

An owner conflict or an interrupted update is therefore reported ahead of running sessions.

## Stop, start, and restart a sandbox

```sh
sandboxed-agents stop NAME [--force]
sandboxed-agents start NAME
sandboxed-agents restart NAME [--force]
```

These commands act on the existing container of the sandbox `NAME` and on nothing else. `start` and `restart` start that same container again. All three keep the three volumes and the sandbox's configuration: the Podman options, the resource limits, the owner labels, and the workspace. They create, remove, and reconfigure no container or volume, and they do not check or build the image. They run no preflight and call neither `ssh` nor `podman machine ssh`.

| Command | On a running sandbox | On a stopped sandbox |
| --- | --- | --- |
| `stop` | asks the manager for running agent sessions, then runs `podman stop` and prints `Sandbox NAME is stopped.` | changes nothing and prints `Sandbox NAME is stopped.` |
| `start` | changes nothing and prints `Sandbox NAME is running.` | runs `podman start` and prints `Sandbox NAME is running.` |
| `restart` | asks the manager for running agent sessions, then runs `podman stop` and `podman start` and prints `Sandbox NAME is running.` | runs `podman start`, no `podman stop`, and prints `Sandbox NAME is running.` |

On success each command exits with status 0, also when nothing had to change.

### Command line

Each command takes exactly one sandbox name. As with `up`, the first word is always read as the sandbox name and is checked against the [sandbox name rules](#sandbox-names). After the name, `stop` and `restart` accept `--force` once; `start` accepts no option. A usage error prints a message and a usage line on standard error, calls no Podman command, and exits with status 1:

- Without a name, the command reports the missing sandbox name and names its own usage, for example `missing sandbox name; use sandboxed-agents stop NAME`.
- An option in place of the name, such as `--force`, is reported as an invalid sandbox name.
- A second `--force` is reported as a duplicate option.
- Any other word after the name is reported as an unexpected argument, or as an unknown option when it starts with `-`. This includes `--force` given to `start`.

### What `stop`, `start`, and `restart` do

1. They check the command line and the sandbox name.
2. They look up the container and the three volumes of the sandbox by their exact Podman names, as `up` does, and read the owner label of each one that exists and whether the container is running.
3. When neither the container nor any of the three volumes exists, the command reports `sandbox NAME does not exist in this controller group` and exits with status 1. When no container exists but some or all of the volumes do, the name is known, but there is no container to act on: if one of those volumes has a missing or different owner, the command reports that owner conflict; otherwise it reports `sandbox NAME has no container; run sandboxed-agents up NAME, which adopts its volumes`. Either way it exits with status 1 and changes nothing.
4. They check the owners of the container and of every existing volume, then look up the backup container and check its owner (see [Refusals](#refusals)).
5. They refuse a sandbox with a backup container of an interrupted update and name `sandboxed-agents update NAME`.
6. They act on the running state of the container, as the table above shows. `stop` and `restart` on a running sandbox first pass the [session guard](#session-guard).

The lookups and checks of steps 2 to 5 run also when the command will change nothing. `stop` on a stopped sandbox and `start` on a running one therefore still refuse an owner conflict or a backup container, exit with status 1, and do not print the sandbox's state.

If a Podman command fails, the command stops, reports the Podman command and its exit status, and exits with status 1 without printing the sandbox's state. If `restart` stopped the container and `podman start` then fails, the sandbox stays stopped.

### Session guard

Before `stop` or `restart` stops a running container, it asks the in-container manager for the running agent sessions:

```sh
podman exec sandboxed-agents.default.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

The manager answers when this command exits with status 0 within 30 seconds and prints exactly one JSON array on standard output. Each element is an object whose `name` and `agent` are strings that are not empty or only whitespace: the name of a running agent session and the agent it runs, for example `[{"name":"main","agent":"claude"}]`. An empty array means that no agent session runs. Anything else counts as no answer, including a partly valid array.

In this version the manager always answers with an empty array, because agent sessions come with #45. The refusals for running sessions below take effect once the manager reports sessions.

| Manager answer | Without `--force` | With `--force` |
| --- | --- | --- |
| No running session | stops the container | stops the container |
| Running sessions | refuses: `sandbox NAME has running agent sessions: SESSIONS; use --force to end them` | stops the container and prints `Ended agent sessions: SESSIONS.` |
| No answer | refuses: `cannot rule out running agent sessions in sandbox NAME: DETAIL; use --force to proceed anyway` | stops the container and prints `Agent sessions that may have been running were ended and cannot be named.` |

`SESSIONS` lists every running session as `NAME (AGENT)`, separated by commas, in the order the manager reported them. `DETAIL` says why the answer is missing: an error running `podman exec`, such as the timeout, its exit status and standard error, or an invalid answer. A refusal exits with status 1. Its only Podman calls are the lookups and `podman exec`; it issues no `podman stop` or `podman start`.

`--force` does not skip the query: the command still asks the manager so that it can name the sessions it ends. It prints the line about ended sessions after `podman stop` succeeds, and prints no such line when the manager reported no running session. `restart` then starts the container. `stop` and `restart` on a stopped sandbox, and `start` in every case, ask the manager nothing.

### Order of checks for `stop`, `start`, and `restart`

These commands run their checks in the order described in [Development](development.md#order-of-checks) and report only the first failure:

| Step | What the command does at this step |
| --- | --- |
| 1. Usage and names | reports a usage error or an invalid sandbox name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox name. When only volumes of the sandbox remain, reports an owner conflict on those volumes or, if they all carry the current owner, that no container exists. |
| 4. Owner | reports an owner conflict on the container or its volumes, then on the backup container |
| 5. Interrupted update | reports a backup container with the current owner |
| 7. Preconditions | `stop` and `restart` on a running sandbox without `--force`: reports that the manager did not answer |
| 9. Session guard | `stop` and `restart` on a running sandbox without `--force`: reports the running agent sessions |

The preflight (step 2), the running-state check (step 6), and the terminal check (step 8) do not apply to these commands. A sandbox with running agent sessions and an owner conflict or a backup container is therefore refused for the owner or the interrupted update, and the manager is not asked.

## Verification

The behavior on this page is covered by offline tests against fake `podman` and `ssh` programs ([Development](development.md#test-seams)). The manager's side of the session query is covered by tests with injected process functions. The refusals of `remove`, `stop`, and `restart` on running sessions are covered by a fake `podman` whose `exec` answer reports sessions. No offline test starts a real container, and nothing on this page has been confirmed against Podman on a live host.
