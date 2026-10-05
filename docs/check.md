# Check a sandbox

```sh
sandboxed-agents check NAME
```

`check NAME` reports the state of one sandbox of the current [controller group](sandboxes.md#controller-groups): its container, its volumes and their owners, the resource limits recorded on the container, a backup container left by an interrupted update, whether the manager answers, and SSH access. Use it when a sandbox does not behave as expected, or when another command names it for diagnosis.

`check` without a name is a different check: the [preflight](host-prerequisites.md#run-the-check), which reports missing host prerequisites and looks at no sandbox. `check NAME` runs no preflight. On Windows it first selects the Podman machine as `stop` does ([Target on Windows](sandboxes.md#target-on-windows)) and never starts it.

## Command line

`check` takes at most one argument, the sandbox name, and no options. Every usage error prints its message and then the usage line `Usage: sandboxed-agents check`, checks nothing, and exits with status 1:

- More than one argument: the message names the form `sandboxed-agents check [NAME]`.
- An argument starting with `-`: the message reports it as an unknown option, as for other commands.
- An invalid sandbox name: the message is the one every command gives for an invalid name.

None of these errors calls Podman.

## What `check NAME` changes

Nothing. It only reads: it starts, stops, renames, creates, and removes no container or volume, restores no backup container, repairs no owner, and writes no file in host state or in your SSH directory. Two calls reach into a running sandbox: the manager's version query and an SSH connection that runs `true` ([Manager and SSH](#manager-and-ssh)). Neither changes the sandbox.

## Report

`check NAME` prints one line per item to standard output, for example for an intact running sandbox in the group `default` with SSH setup installed:

```text
Sandbox agent01: state running
Container sandboxed-agents.default.agent01: running, owner default
Volume sandboxed-agents.default.agent01.workspace: present, owner default
Volume sandboxed-agents.default.agent01.home: present, owner default
Volume sandboxed-agents.default.agent01.ssh: present, owner default
Resource limit memory: 8589934592
Resource limit cpus: 4
Resource limit pids-limit: 2048
Resource limit shm-size: 1073741824
Manager: the manager answered through podman exec
SSH: the connection through host entry agent01 succeeded
```

The `Sandbox` line shows the same state as `list`, in the same precedence: `owner conflict`, `update interrupted`, `volumes only`, or the container's `running` or `stopped` ([List sandboxes](sandboxes.md#list-sandboxes)). The `Container` line shows `absent` when no container exists under the sandbox's name. An owner label that is missing is shown as owner `missing`. A backup container gets a `Backup container` line with its Podman name and owner.

Each problem gets a line starting with `problem:`, and the other items are still reported. When at least one problem was reported, `check NAME` ends with a message on standard error that it found problems and exits with status 1. Otherwise it exits with status 0.

### Volumes

A `Volume` line appears for each of the sandbox's volumes that exists, as `present`, and for each volume the container needs but lacks, as `missing`. A volume that does not exist is not listed when no container needs it, for example in the `volumes only` state.

### Resource limits

When a container exists, the four limits are read from its labels ([Podman names and labels](sandboxes.md#podman-names-and-labels)): `memory` and `shm-size` in bytes, `cpus` and `pids-limit` as recorded. A container without such a label, for example one created before the labels existed, shows `not recorded` for that limit. `check` assumes no default in its place.

### Workspace bind

On a sandbox whose workspace is a host directory, a `Workspace:` line before the volume lines names the bound directory in place of the workspace volume. A workspace volume kept from before the bind, which `up` left unused ([Workspace volumes beside a bind](sandboxes.md#workspace-volumes-beside-a-bind)), is shown as `unused`. It is not a problem; `remove NAME --volumes` deletes it.

## States and problems

A state describes the sandbox and does not affect the exit status. A problem is something wrong that you have to act on, and makes `check NAME` exit with status 1.

| Finding | Kind | What `check NAME` does |
| --- | --- | --- |
| The container is stopped | state | reports it and skips the manager and SSH checks, which need a running sandbox |
| SSH setup is not installed | state | reports it, opens no SSH connection, and touches no file in your SSH directory |
| Only volumes remain (`volumes only`) | state | names each volume that exists and its owner; skips the manager and SSH checks |
| An unused workspace volume beside a bind | state | names the volume |
| The container lacks one of its volumes | problem | names the missing volume and its mount point |
| An owner conflict | problem | names each foreign object ([Owner conflicts](#owner-conflicts)) |
| A backup container exists (`update interrupted`) | problem | reports the interrupted update and names `sandboxed-agents update NAME` |
| The manager does not answer in a running sandbox | problem | reports the failure |
| The SSH connection fails while SSH setup is installed | problem | reports the host entry and the failure |
| SSH setup is installed, but the sandbox has no container | problem | names `sandboxed-agents ssh-config NAME --remove` where that cleanup is allowed ([SSH setup without a container](#ssh-setup-without-a-container)) |

### Volumes only

A sandbox of which only volumes remain, all three or only some, each with the current owner, counts as known; `remove` without `--volumes` leaves a sandbox in this state. A volume counts as missing only when a container exists that lacks it, so a `volumes only` sandbox with nothing else wrong exits with status 0. `sandboxed-agents up NAME` adopts the volumes again ([Kept volumes](sandboxes.md#kept-volumes)).

### Interrupted update

A backup container `sandboxed-agents-backup.GROUP.NAME` is reported with and without a container under the sandbox's own name. `check NAME` points to `sandboxed-agents update NAME` and restores nothing. In this version `update` does not recover an interrupted update yet; recovery comes with #54 ([Update a sandbox](updates.md#not-in-this-version)).

### Owner conflicts

A container, volume, or backup container under the sandbox's Podman names whose owner label is missing or names another controller group is an owner conflict. This includes a container with the current owner one of whose volumes is foreign. Other commands refuse such a sandbox ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)); `check NAME` reports it instead, names each object concerned by its Podman name, and changes none of them.

An owner conflict never comes from the executable itself, only from interventions made directly with Podman, so only a direct intervention with Podman resolves it: remove or rename the foreign object with Podman. Beside a backup container, `check NAME` reports both, and `update NAME` restores no backup while the owner conflict exists. While an owner conflict exists, `check NAME` also skips the manager query and the SSH connection, because it does not reach into a sandbox that is not provably its own.

### Manager and SSH

Both checks run only on a running sandbox with no owner conflict. Neither is retried, and neither starts a stopped sandbox.

- **Manager.** `check NAME` asks the manager for its version through `podman exec` and waits at most 5 seconds for the answer. No answer in time, a failed call, or an unexpected answer counts as a manager that does not answer.
- **SSH.** With SSH setup installed ([SSH setup](ssh.md#ssh-setup)), `check NAME` runs `ssh` through the sandbox's host entry ([Host entry name](ssh.md#host-entry-name)) in batch mode, so it never prompts, with one connection attempt, a connect timeout of 5 seconds, and at most 10 seconds overall. It checks the pinned host key strictly, forwards nothing, and runs `true` in the sandbox. A failure reports the exit status and the error output of `ssh`.

SSH setup counts as installed when either part of it remains in the current controller group's [host state](ssh.md#files-in-host-state): the sandbox's own directory, or, when that directory is gone, the sandbox's own block in the group's managed SSH configuration ([Host entry](ssh.md#host-entry)). A block counts as the sandbox's own only when it starts with the sandbox's host entry name and names the sandbox's dedicated key as `IdentityFile`. To decide whether SSH setup is installed, `check NAME` reads only the group's managed SSH configuration, and only when the directory is gone; it reads no file in the sandbox's directory or in your SSH directory, and it does not check whether the setup is complete. The SSH connection described above then uses the installed host entry as `ssh` normally does. This needs no running sandbox, so an SSH setup that is not installed or remains without a container is reported in every state. When `check NAME` cannot determine whether either part exists, it fails with an error and exits with status 1; that is not a finding about the sandbox.

### SSH setup without a container

An SSH setup that is still installed for a sandbox without a container is always a problem. Whether the report names a cleanup depends on what else exists:

- **`volumes only`, with no owner conflict and no backup container:** the finding names `sandboxed-agents ssh-config NAME --remove`, which removes the host side of the SSH setup ([Remove the SSH setup](ssh.md#remove-the-ssh-setup)).
- **An owner conflict or a backup container:** `ssh-config NAME --remove` refuses such a sandbox, so the finding names no cleanup command. It says that the SSH setup can be removed only once the owner conflict or the interrupted update is resolved, and the report's own finding about that conflict or update names the way to resolve it: Podman for an owner conflict, `sandboxed-agents update NAME` for an interrupted update.

## Unknown sandbox

When no container, no volume, and no backup container of that name exists in the current controller group, `check NAME` prints no report, says that the sandbox does not exist in this controller group, and exits with status 1. This is step 3 of the order of checks ([Order of checks](sandboxes.md#order-of-checks)). Owner conflicts and an interrupted update are findings of `check NAME`, not refusals.

## Verification

The behavior on this page is covered only by offline tests at the CLI boundary against fake `podman` and `ssh` programs ([Development](development.md#test-seams)). No live run has confirmed it: not the report of a real sandbox, not the manager query against a real container, and not an SSH connection through a real host entry.
