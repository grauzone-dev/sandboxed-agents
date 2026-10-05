# Sandboxes

A sandbox is one rootless Podman container with three named volumes of its own: the workspace, the home data, and the SSH server state. On Linux and Windows, `sandboxed-agents up NAME WORKSPACE` binds one existing host directory as the workspace in place of the workspace volume ([Workspace bind](#workspace-bind)). `sandboxed-agents up NAME` creates a sandbox with safe defaults and leaves it running, or starts a sandbox that already exists. `sandboxed-agents remove NAME` deletes the container of a sandbox and keeps its volumes unless you ask otherwise (see [Remove a sandbox](#remove-a-sandbox)). `stop`, `start`, and `restart` change whether an existing sandbox runs and keep everything else ([Stop, start, and restart a sandbox](#stop-start-and-restart-a-sandbox)). `sandboxed-agents update NAME` replaces the container of an outdated sandbox with one created from the current image and keeps its volumes and recorded configuration ([Update a sandbox](updates.md)). `sandboxed-agents list` shows the sandboxes of the current controller group ([List sandboxes](#list-sandboxes)). `sandboxed-agents shell NAME` opens a shell in a running sandbox through `podman exec`, without SSH ([Shell and SSH access](ssh.md)).

`up` first runs the preflight for the host operating system: the Linux preflight on Linux and the Windows preflight on Windows ([Host prerequisites](host-prerequisites.md)). On any other operating system, the preflight reports that no prerequisite check is available, and `up` stops before it looks up or creates anything.

Every command acts only on the sandboxes of the current [controller group](#controller-groups). Only the resource limits, the toolchain set, the SSH port, and the workspace of a sandbox can differ from the defaults.

## Controller groups

A controller group is the namespace the executable manages. The environment variable `SANDBOXED_AGENTS_GROUP` selects it:

- When the variable is not set, the group is `default`.
- When it is set, its value is the group. A group name matches `^[a-z0-9][a-z0-9-]*$`: lowercase letters, digits, and `-`, starting with a letter or digit. An empty value is invalid, as are values such as `Team`, `a.b`, or `-a`.

Every command checks the group first, after it has recognized the command name: `up`, `remove`, `start`, `stop`, `restart`, `list`, `build`, `check`, `version`, [`agents enable`, `agents disable`, `agents status`, `agents run`, and `agents login`](agents.md), [`shell` and `ssh-config`](ssh.md), `fingerprint`, `integrations config`, and `integrations login` ([Integrations](integrations.md)), also with `--help`. An invalid group makes the command print an `invalid controller group` message, which names the value and the pattern, and the usage line on standard error and exit with status 1, before it runs Podman or any other program.

The group does not depend on where the executable is installed. Two installed copies with the same value of the variable manage the same sandboxes, and one copy run with two values manages two separate groups.

### One namespace per group

`up` writes the current group into the Podman names of the container and the volumes and into their owner label ([Podman names and labels](#podman-names-and-labels)). Two groups can each hold a sandbox of the same name, for example `sandboxed-agents.default.agent01` and `sandboxed-agents.team-a.agent01`.

A command looks for a sandbox only under the current group's Podman names. A sandbox of another group is therefore unknown to it: `stop`, `start`, and `restart` report `sandbox NAME does not exist in this controller group`, `remove` reports that no sandbox named `NAME` exists, and `up` creates a new sandbox of that name in the current group. `list` shows no row for it.

Images are shared by all groups: their names contain no controller group ([Images](images.md)). `build` and `check` validate the group and otherwise do not use it.

### Owners and backup containers

Every command that takes a sandbox name checks the owner label of the container and of all three volumes under the sandbox's Podman names, and of the backup container `sandboxed-agents-backup.GROUP.NAME`. A missing owner label, or one that names another group, is an owner conflict, also when the container carries the current owner and only a volume or the backup container does not. The command refuses, names every foreign object in one message by its Podman name, and points to Podman, where you remove or rename it. No command repairs or adopts a foreign object. The only exception is `remove NAME --volumes` on a sandbox whose own container carries the current owner and that has no backup container: it removes the container and the owned volumes, keeps each foreign volume, names it, and exits with status 1 ([Owners and kept volumes](#owners-and-kept-volumes)).

A backup container with the current owner marks an interrupted update. Every command that takes a sandbox name, except `update`, refuses such a sandbox and names `sandboxed-agents update NAME`. In this version `update` refuses it as well, with its own message: recovering an interrupted update is not available yet, `update` changes nothing, and you can inspect the backup container with Podman. Recovery through `update` comes with #54 ([Update a sandbox](updates.md#not-in-this-version)). The owner check comes first: when a backup container sits beside a foreign object, or is itself foreign, the command reports the owner conflict and not the interrupted update.

A backup container is not a sandbox of its own: `list` shows it as the state of the sandbox it belongs to. A sandbox of which only the backup container exists, with no container and no volume under its own names, still counts as known. Every command that takes its name refuses it: with an owner conflict when the backup container is foreign, otherwise with the interrupted update. Every command except `update` then names `sandboxed-agents update NAME`; `update` gives its own message, which points to Podman because recovery is not available in this version.

When the sandbox has no container, the commands look up the backup container already at the sandbox existence step (step 3) and report an owner conflict on the remaining volumes or the backup container there. When the container exists, they report it at the owner step (step 4).

### Host state

Host state is per-group data the executable keeps outside Podman, in the operating system's state directory. Each group has its own directory, named `group-GROUP` on every platform, for example `group-con`. The prefix keeps the directory name valid on Windows also for group names that Windows reserves for devices, such as `con` or `nul` (ADR-0005).

| Platform | Directory |
| --- | --- |
| Linux | `$XDG_STATE_HOME/sandboxed-agents/group-GROUP` when `XDG_STATE_HOME` is an absolute path, otherwise `~/.local/state/sandboxed-agents/group-GROUP` |
| Windows | `%LOCALAPPDATA%\sandboxed-agents\group-GROUP` |

The [SSH setup](ssh.md#ssh-setup) and the [lifecycle lock](updates.md#lifecycle-lock) use host state. `ssh-config` reads it, and `ssh-config --install`, `up --ssh-config`, and `start --ssh-config` keep the group's keys, pinned host keys, and managed SSH configuration there and create the directories they need. The lifecycle commands `up`, `start`, `stop`, `restart`, `remove`, and `update` create the group's directory and its `locks` directory when they are missing and keep one empty lock file per sandbox there. They write no other data there, and they coordinate only with commands that use the same state directory. No other command reads or writes data in host state or creates these directories; `up NAME WORKSPACE` only computes their paths to keep them out of a workspace bind ([Workspace guards](#workspace-guards)). On Windows, a command that needs host state fails when `LOCALAPPDATA` is not set or is not an absolute path. That includes `up NAME WORKSPACE`, which cannot rule out host state as a protected host path without it.

## Create or start a sandbox

```sh
sandboxed-agents up NAME [WORKSPACE] [--memory SIZE] [--cpus N] [--pids-limit N] [--shm-size SIZE] [--with SET] [--port N] [--agents LIST]
```

`up` takes one sandbox name, optionally followed by `WORKSPACE`, the host directory to bind as the workspace ([Workspace bind](#workspace-bind)), and then the resource limit options, the toolchain option, `--port`, and `--agents`. `WORKSPACE` is the word directly after the name; a directory whose name starts with `-` is given as `./-dir`. Each option is given as `--option VALUE` or `--option=VALUE`, at most once. An option that is not given keeps its default:

| Option | Sets | Default | Accepted values |
| --- | --- | --- | --- |
| `--memory SIZE` | memory limit | `8g` | a `SIZE` of at least 6 MiB (`6m`) |
| `--cpus N` | CPU limit | `4` | a positive decimal number with at least one digit before the point and at most three after it, such as `2` or `1.5`, up to `9223372036.854`; no sign and no exponent |
| `--pids-limit N` | process limit | `2048` | a positive whole number up to 9223372036854775807 |
| `--shm-size SIZE` | shared memory (`/dev/shm`) | `1g` | a positive `SIZE` |
| `--port N` | SSH port on `127.0.0.1` ([SSH server](#ssh-server)) | the first free port from 2222 upward, chosen when the sandbox is created | a whole number from 1 to 65535 |

A `SIZE` is a positive whole number of bytes, optionally followed by one of the suffixes `k`, `m`, `g`, or `t`, in upper or lower case, which multiply it by 1024, 1024², 1024³, or 1024⁴. For example, `512m` is 512 MiB and `16g` is 16 GiB. The result is at most 9223372036854775807 bytes.

`--with SET` selects the toolchains built into the sandbox's image: a comma-separated list of toolchain names, or `none` alone for the base image. Without `--with`, a new sandbox uses the base image, as with `--with none`. In this version the toolchains are `azure`, `dotnet`, and `native`, alone or in any combination, so the valid values are `azure`, `dotnet`, `native`, and `none` ([Toolchains](images.md#toolchains)). Order and repetition of names do not matter.

`--agents LIST` names agents from the [agent catalog](agents.md#agent-catalog) that `up` enables once the sandbox runs, as a comma-separated list such as `--agents claude,codex`. A name given more than once is enabled once. `--agents` is not part of the sandbox's configuration: nothing records it on the container, `up` never compares it with an existing sandbox, and it also applies to a sandbox that already exists ([Enable agents with `up`](#enable-agents-with-up)). Without `--agents`, `up` enables no agent.

`--help` prints the help of `up`, with `WORKSPACE`, the option formats, the defaults, and how to change the workspace, a limit, the toolchain set, or the SSH port, and exits with status 0. It may stand anywhere after `up`, as long as the other words form a valid `up` command line: `up --help`, `up NAME --help`, and `up NAME --memory 16g --help` all print the help. Otherwise `up` reports the usage error instead. `--help` runs no preflight and calls no Podman command.

A usage error prints a message and a usage line on standard error, calls no Podman command, and exits with status 1:

- Without a name, `up` reports the missing sandbox name.
- The first word is the sandbox name, so an option in its place, other than `--help`, is a usage error.
- The word after the name, when it is not an option, is `WORKSPACE`. A further word that is not an option is reported as an unexpected argument, and an unknown option as an unknown option.
- A limit option or `--port` without a value, given twice, or with a value in a format it does not accept is a usage error that names the option. For `--port`, that is anything but a whole number from 1 through 65535, such as `0`, `65536`, `+2300`, or `2e3`.
- `--with` without a value or given twice is a usage error. So is a value with an unknown or undelivered toolchain name, with an empty name, or with `none` combined with another name; the message lists the valid values.
- `--agents` without a value, given twice, or with an empty agent name, such as `--agents=`, `--agents ''`, or `--agents claude,,codex`, is a usage error that names `--agents`. An unknown agent name, or a catalog name whose agent is not delivered yet, is refused at the same step, and the message lists the valid agent names ([Agents](agents.md#enable-agents-with-up)). `up` checks these names on the host against the catalog embedded in the executable, so it creates no sandbox, volume, or image.

### Sandbox names

A sandbox name matches `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`: it starts with a letter or digit and continues with letters, digits, `_`, `.`, and `-`. A name such as `-x`, `.x`, `a/b`, or `a b` is rejected before any Podman command runs. No name is reserved, so `up`, `list`, `default`, and `backup` are valid sandbox names (ADR-0004).

### What `up` does

1. It checks the command line, the sandbox name, the format of each limit option and of `--port`, the toolchain names of `--with`, and the agent names of `--agents`. With `WORKSPACE`, it resolves the directory and applies the [workspace guards](#workspace-guards), without any Podman call.
2. It runs the preflight and prints its lines. On Linux, the preflight's only Podman call is `podman --version`. If a prerequisite is missing, `up` reports `host prerequisites are missing` and exits with status 1. On Windows, the preflight runs read-only queries of the Podman client and the [selected Podman machine](host-prerequisites.md#selected-podman-machine), including commands in the machine through `podman machine ssh`, one of which reads the machine's WSL automount root. If a required prerequisite is missing or could not be checked, `up` exits with status 1. On Windows with `WORKSPACE`, `up` then translates the workspace into the machine's path for it, or refuses it ([Windows paths](#windows-paths)), with no further Podman call. Every later Podman call of `up` names that machine with `--connection`, without `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or `CONTAINER_SSHKEY` in its environment, so neither these settings nor another default connection redirect it. When the preflight fails, `up` stops before it looks up, creates, or changes any sandbox object.
3. It looks up the container and the three volumes of the sandbox by their exact Podman names (see [Podman names and labels](#podman-names-and-labels)) and reads the owner label of each one that exists. It looks at no other container or volume. When no container exists, it also looks up the backup container of an interrupted update and checks the owners of the existing volumes and of the backup container here.
4. When the container exists, it looks up the backup container now and checks the owners of the container, its volumes, and the backup container together. It refuses to continue on an owner conflict or an interrupted update (see [Refusals](#refusals)).
5. If the container exists, `up` compares a given `WORKSPACE` with the workspace recorded on the container and each given limit option and the given toolchain set with the values recorded on the container, then reads the recorded SSH port and compares `--port` with it, and refuses on a difference. When the container is stopped, it also checks that the recorded port is free on `127.0.0.1`. Then it starts a stopped container and leaves a running one alone (see [Existing sandboxes](#existing-sandboxes)).
6. Otherwise `up` chooses the SSH port before it builds or creates anything: the value of `--port` when it is free, or the first free port from 2222 upward ([Port](#port)). To find the ports that sandboxes record, it reads `podman ps --all`. When `--port` names a port that is not free, or no port is free, `up` exits with status 1 and builds and creates nothing.
7. It creates the sandbox:
   - It makes sure the image for the selected toolchain set exists ([Images](images.md)). Without toolchains, that is the base image: if it does not exist, `up` builds it, as `sandboxed-agents build` does. With toolchains, `up` builds the base image first when it is missing, and then builds the toolchain image when it is missing or not current, that is, when it was not built on the current base image ([Image names and labels](images.md#image-names-and-labels)). When the image exists and is current, `up` builds nothing. It never rebuilds a current image or any image of another toolchain set; that is what `build` is for. The image names contain no controller group, so an image that another controller group built with the same executable counts as existing (ADR-0005).
   - It creates each of the three volumes that does not exist yet and adopts each one that does (see [Kept volumes](#kept-volumes)). It prints one line per volume, `Created volume NAME.` or `Adopted volume NAME.`. With `WORKSPACE`, it creates no workspace volume, and a kept workspace volume stays unused (see [Workspace bind](#workspace-bind)).
   - It creates the container from that image with the defaults below, the given limits, a label recording the toolchain set, the workspace bind when `WORKSPACE` is given, and the chosen SSH port, and starts it. A toolchain image is passed to `podman create` by the image ID that `up` inspected and found current, not by its tag; the base image is passed by its tag. When the toolchain tag no longer names an image built on the current base image right after `up` built it, `up` reports that the image changed during its build and stops before it creates a volume or the container.

When the sandbox runs, `up` prints `Sandbox NAME is running.`. Without `--agents`, it then exits with status 0. With `--agents`, it enables the listed agents next ([Enable agents with `up`](#enable-agents-with-up)). Podman's own output, such as that of a build, passes through.

If a Podman command fails, `up` stops, reports the command and its exit status, and exits with status 1. It does not remove what it created before the failure. Volumes it created carry the owner label, so the next `up` adopts them.

### Enable agents with `up`

`up NAME --agents a,b` has the same result as `up NAME` followed by `sandboxed-agents agents enable NAME a` and `sandboxed-agents agents enable NAME b`: the same agent selection and the same installations in the home volume ([Enable an agent](agents.md#enable-an-agent)). It enables the agents only after the sandbox runs, whether `up` created it, adopted kept volumes, started a stopped container, or found it running. When `up` refuses or fails before the sandbox runs, it enables no agent. A sandbox with a new home volume starts with no agent enabled, while an adopted home volume keeps the agents installed in it ([Kept volumes](#kept-volumes)).

On a sandbox that already exists, `up` starts it as it would without `--agents` and then enables each listed agent that is not enabled yet. An agent that is already enabled stays as it is: `up` runs no installation for it and does not move it to a newer version. When all listed agents end up enabled, `up` exits with status 0, also when every one of them was enabled before. Agents enabled earlier and not listed stay enabled; `--agents` adds agents and removes none.

`up` addresses the manager as [`agents enable`](agents.md#how-the-request-reaches-the-manager) does, as container root through `podman exec --user=0:0`: it first checks that the manager answers its `version` call, then sends one `agents enable AGENT` call per agent in the order given, and the manager installs each agent as `agent`, UID and GID 1000. `up` collects the output of these calls, from the manager and from npm, and prints it after the last attempt, standard output first and then standard error.

- **A failed installation.** `up` waits at most 15 minutes for each `agents enable` call; each agent gets its own 15 minutes. A call that does not finish in time counts as a failed installation. `up` then stops waiting for that `podman exec`, which does not ensure that the manager or npm inside the sandbox has stopped, so before retrying that agent, check that its installation has ended, for example with `sandboxed-agents shell NAME`. When an `agents enable` call fails or times out, `up` asks the manager for its version again, with the usual 30-second limit of that check. When the manager answers, the failure belongs to that agent: `up` attempts the remaining agents, the sandbox keeps running, and every agent that was enabled before or during this `up` stays enabled. After printing the collected output, `up` exits with status 1 and names the failed agents together with the command to retry each, `sandboxed-agents agents enable NAME AGENT`.
- **A manager that does not answer.** When the manager does not answer the first check, or stops answering after a failed call, `up` stops attempting agents, discards the collected output, and prints a single message instead of one per agent. The message says that `up` cannot confirm which agents were enabled and names `sandboxed-agents check NAME` for diagnosis and the `sandboxed-agents agents enable NAME AGENT` command for every listed agent; a repeated `agents enable` on an agent that is already enabled changes nothing. The sandbox stays as it is, and agents enabled before the manager stopped answering stay enabled. `up` exits with status 1. `check NAME` reports whether the manager answers ([Check a sandbox](check.md)).

## Workspace bind

On Linux and Windows, `up NAME WORKSPACE` binds the host directory `WORKSPACE` at `/workspace` in place of the workspace volume. The home and SSH server state stay named volumes. The bind mounts only that directory, and agents in the sandbox read and change its files directly (ADR-0003). This does not isolate everything else on the host: agents reach a file in a protected directory directly when it shares a hard link with a file in the workspace, and host programs that follow a symlink in a protected directory into the workspace read or write content that agents can read and change ([Workspace guards](#workspace-guards)).

`up` resolves `WORKSPACE` before it uses it: a relative path such as `./dir` is resolved against the current directory, and symlinks and `..` components are resolved one component at a time, as the file system resolves them, so on Linux `link/..` names the parent of the symlink's target. Components that do not exist are kept as written. On Linux, the bind source in the Podman call is that resolved directory. `up` never creates the directory.

On Windows, `..` components are removed from the path as written before any link is followed, as Windows itself does, so `link\..` names the directory that holds `link`. `up` then resolves symlinks and junctions, keeps components that do not exist as written, and takes the letter case of existing components from the file system, so `C:\USERS\ME\PROJECT` and `C:\Users\me\project` name the same workspace. A link target that starts with a backslash but names no drive, such as `\Users\me\project`, is resolved from the root of the link's own drive. The result, a Windows path such as `C:\Users\me\project`, is the resolved workspace: the [workspace guards](#workspace-guards) and the comparison with an existing sandbox use it, and messages name it. The bind source in the Podman call is a separate value, its translation into the Podman machine ([Windows paths](#windows-paths)).

The workspace bind is the only host path that any Podman call of the executable mounts. No call mounts a socket: no host credentials, no SSH-agent socket, no container-engine socket, and no display socket.

### Workspace guards

Before the preflight and without any Podman call, `up` refuses a `WORKSPACE` and exits with status 1 in these cases:

- **Unresolvable path.** A path in the workspace or in a protected host path cannot be resolved, for example because of a symlink loop or more than 255 symlinks or a missing permission, on Linux `/proc/self/mountinfo` cannot be read or parsed or has no mount for a path, or the [hard link search](#workspace-guards) cannot read the workspace. On Windows, a link whose target names a drive without a backslash after it, such as `C:project`, is also unresolvable: its meaning depends on a per-drive current directory. `up` then cannot rule out a protected host path. The message names the path, such as the looping symlink, and the underlying error. When the link scan inside the workspace fails, the message names the scanned directory.
- **Unusable.** `WORKSPACE` as given cannot be opened, for example because it or one of its components does not exist or is a file, as in `file/child`, or on Linux `missing/..`. The message names the resolved path and the underlying error, and the resolved path need not exist.
- **Not a directory.** `WORKSPACE` names a file or anything else that is not a directory. The message names the resolved path.
- **No drive letter.** On Windows, the resolved workspace does not start with a drive letter, such as the UNC path `\\server\share\project`. Only a drive-letter path can be translated into the Podman machine ([Windows paths](#windows-paths)). The message names the resolved path.
- **Protected host path.** The workspace equals a protected host path, lies inside one, or contains one. The message names the workspace and the resolved protected host path.

The protected host paths are:

| Protected host path | Linux | Windows |
| --- | --- | --- |
| The executable | the running executable, as the operating system reports its path | the same |
| Host state of the controller group | `group-GROUP` of the current group ([Host state](#host-state)) | the same |
| Host state root | the `sandboxed-agents` directory that holds the `group-GROUP` directories of all groups | the same |
| System temporary directory, where `build` and `up` write every build context | `$TMPDIR`, or `/tmp` when it is not set | the directory Windows reports as temporary, from `TMP`, `TEMP`, or `USERPROFILE`, in that order, otherwise the Windows directory |
| `/tmp` | always, also when `$TMPDIR` names another directory | not protected |
| The SSH directory | `.ssh` in your home directory, `$HOME` | `.ssh` in your user profile directory, `%USERPROFILE%` |

Each protected host path is resolved like the workspace, so a workspace that reaches one through a symlink or, on Windows, a junction, and a protected host path that is itself such a link into the workspace, are refused. A protected host path that does not exist yet, such as host state before its first use, stays protected, also when a link on its path, such as a dangling symlink or a junction in place of one of its parents, points it into the workspace. Your home directory contains `.ssh` and is therefore refused, and so is the root that holds it: `/` on Linux, and on Windows the root of its drive, such as `C:\`.

Beyond symlinks and junctions, `up` detects these aliases:

- **Bind mounts.** On Linux, `up` reads the mount table from `/proc/self/mountinfo` and maps the workspace, every mount below it, and each protected host path to its file system and the path within it. When several mounts share a target, the later record, the one that is visible, counts. A workspace that reaches a protected host path through another mount of the same file system is refused. On Windows, `up` reads no mount table.
- **Same directory or file.** A workspace that is the same directory as a protected host path or one of its parents, or the reverse, is refused, whatever paths lead to them.
- **Hard links.** When a protected file, such as the executable, has more than one hard link, `up` searches the whole workspace, without following symlinks, for a link to it and refuses the workspace when it finds one. This search runs on Linux and on Windows.
- **Links inside the workspace.** On Windows, `up` also scans the whole workspace for symlinks and junctions. It resolves each one and refuses the workspace when the target equals, lies inside, or contains a protected host path, before it scans anything below that target. A link to another directory adds that directory to the scan, and each directory is scanned once. Other reparse points, such as cloud placeholder files, are not links and count as ordinary entries. On Linux, `up` does not scan for such links.

These searches fail closed. On Linux, when the executable has more than one hard link, every directory in the workspace must be readable to rule out a link to it; with a single hard link, `up` does not search the workspace, and an unreadable subtree does not matter. On Windows, the link scan always runs, so every directory in the workspace and in the directories its links reach must be readable. An unreadable subtree, even one without a link, refuses the workspace before any Podman call as an unresolvable path; the message names the executable on Linux or the scanned directory on Windows, and the file system error, such as `permission denied`. Skipping the subtree would be unsafe: an agent could later make its own unreadable directory readable and reach a link hidden there.

The guards compare the protected host paths themselves, not the files beneath them. A file inside a protected directory, such as `~/.ssh`, host state, or the temporary directory, that shares a hard link with a file in the workspace is not detected, and neither is a symlink or junction inside a protected directory that points into the workspace. A hard-linked file is the same file in both places, so agents read and change it directly through the workspace. Host programs that follow such a symlink read or write workspace content, which agents can read and change. The guards also check the file system only when `up` is given `WORKSPACE`; a link created afterwards is not detected.

Later Stories add protected host paths, such as the npm launcher and its shims (#61).

### Windows paths

On Windows, the Podman machine is a WSL2 machine, and Podman creates the container inside it, so the bind source must be a path in that machine. WSL mounts each Windows drive below its automount root, which is `/mnt/` by default. After a successful preflight, `up` translates the resolved workspace: the drive letter becomes a lowercase directory below `/mnt/`, and backslashes become slashes. `C:\Users\me\project` is bound from `/mnt/c/Users/me/project`. The rest of the path keeps the case of the resolved workspace. The guards above run on the Windows path, before this translation.

The preflight reads the automount root from the machine it checked, and every later Podman call of `up`, the `create` call with the bind included, names that same machine with `--connection` ([What `up` does](#what-up-does)). A root read from one machine is never applied to a bind on another.

A workspace without a drive letter never gets here: the [workspace guards](#workspace-guards) refuse it before the preflight. After the preflight and before it looks up any sandbox object, builds, or creates anything, `up` refuses a `WORKSPACE` with an unsupported automount root and exits with status 1. That is the case when the machine reports an automount root other than `/mnt/`, automount is disabled in the machine's WSL configuration, or the root could not be read. The message names the reported root, which is empty when automount is disabled or the root could not be read, and `/mnt/`. Omit `WORKSPACE` to use a workspace volume.

`up` only queries the machine for this and never starts it. When the machine does not run, the preflight reports it, and `up` stops there before it reads the automount root.

### Workspace volumes beside a bind

With `WORKSPACE`, `up` creates no workspace volume. A workspace volume kept by an earlier `remove` is neither mounted nor removed; `up` prints `Kept volume NAME unused: the workspace is a bind; remove --volumes deletes it.` with the volume's Podman name. Kept home and SSH server state volumes are adopted as described in [Kept volumes](#kept-volumes). `check NAME` names the unused volume and does not count it as a problem ([Workspace bind](check.md#workspace-bind)).

### The workspace is part of the configuration

`up` records the workspace kind, `volume` or `bind`, in a label on the container ([Podman names and labels](#podman-names-and-labels)), and `list` shows it. No label records the bound directory: `up` reads it from the source of the container's `/workspace` mount. `up` never changes the workspace of an existing sandbox:

| Existing sandbox | `up NAME` | `up NAME WORKSPACE` |
| --- | --- | --- |
| Workspace volume | starts it | refuses |
| Bind of the directory `WORKSPACE` resolves to | starts it | starts it |
| Bind of another directory | starts it | refuses |

On Windows, the mount source is a path in the Podman machine. `up` translates it back to a Windows path, `/mnt/c/Users/me/project` to `C:\Users\me\project` with the drive letter in upper case, resolves that path as it resolves `WORKSPACE`, and compares the result with the resolved `WORKSPACE`. A mount source that does not lie below `/mnt/` and a drive letter cannot be translated back and is always a conflict.

A refusal is a workspace conflict (see [Refusals](#refusals)), reported after an owner conflict and an interrupted update. To change the workspace, run `remove NAME`, which keeps the sandbox's volumes and never deletes a bound directory, and then `up NAME` with the new `WORKSPACE`.

## Podman names and labels

The executable derives every Podman name from the current controller group and the sandbox name (ADR-0005). For the sandbox `NAME` in the group `GROUP`, which is `default` when `SANDBOXED_AGENTS_GROUP` is not set:

| Object | Podman name | Mounted at |
| --- | --- | --- |
| Container | `sandboxed-agents.GROUP.NAME` | |
| Workspace volume | `sandboxed-agents.GROUP.NAME.workspace` | `/workspace`, unless `WORKSPACE` binds a host directory there |
| Home volume | `sandboxed-agents.GROUP.NAME.home` | `/home/agent` |
| SSH server state volume | `sandboxed-agents.GROUP.NAME.ssh` | `/etc/ssh` |
| Backup container of an interrupted update | `sandboxed-agents-backup.GROUP.NAME` | |

A group name contains no dot, so a Podman name splits unambiguously into the prefix, the group, and the sandbox name.

A volume name is the container name followed by one more dot component, `workspace`, `home`, or `ssh`, which names the role of the volume. The role names contain no dot, so the last component always identifies the role, and everything before it is the container name of exactly one sandbox. Sandbox names may contain dots, but two different sandbox names never yield the same volume name. For example, the sandbox `a.home` has the volumes `sandboxed-agents.default.a.home.workspace`, `….a.home.home`, and `….a.home.ssh`, none of which is a volume of the sandbox `a`.

The container and the volumes carry these labels:

| Label | Value | Carried by |
| --- | --- | --- |
| `io.github.sandboxed-agents.owner` | the current controller group | the container and each of the three volumes |
| `io.github.sandboxed-agents.sandbox-name` | the sandbox name | the container |
| `io.github.sandboxed-agents.workspace-kind` | `volume`, or `bind` when `WORKSPACE` is given | the container |
| `io.github.sandboxed-agents.memory` | the memory limit in bytes, `8589934592` by default | the container |
| `io.github.sandboxed-agents.cpus` | the number of CPUs as a decimal number whose trailing zeros after the point are trimmed, with the point dropped when no decimals remain: `--cpus 1.50` records `1.5`, `--cpus 10.000` records `10`, and `--cpus 10` stays `10`; `4` by default | the container |
| `io.github.sandboxed-agents.pids-limit` | the process limit, `2048` by default | the container |
| `io.github.sandboxed-agents.shm-size` | the shared memory size in bytes, `1073741824` by default | the container |
| `io.github.sandboxed-agents.toolchains` | the canonical toolchain set: sorted, deduplicated names separated by commas, such as `native` or `azure,native`; empty for a sandbox without toolchains | the container |
| `io.github.sandboxed-agents.ssh-port` | the SSH port on `127.0.0.1`, for example `2222` | the container |

`up` records all four limits, the toolchain set, and the SSH port on every container it creates, the defaults included, so `podman container inspect` shows the values in effect under `Config.Labels`. Containers created before these labels existed carry none of them. Podman cannot change the labels of an existing container, so the toolchain set and the SSH port of a sandbox change only when its container is replaced.

The owner label is the authoritative check: a container or volume with a matching name but a missing or different owner label belongs to no sandbox of this controller group, and `up` neither changes nor adopts it. The label authorizes a command; it does not attest how the object was configured (see [Existing sandboxes](#existing-sandboxes)).

## Container defaults

`up` creates the container with these Podman options:

| Setting | Value |
| --- | --- |
| User namespace | `--userns=keep-id:uid=1000,gid=1000`: your host user maps to the user `agent` (UID and GID 1000) in the container |
| Start user | `--user=0:0`: with `keep-id`, Podman starts the container as the mapped user unless `--user` is given, which would override the image's `USER root` ([podman-create(1), `--userns`](https://docs.podman.io/en/latest/markdown/podman-create.1.html#userns-mode)). The entrypoint therefore starts as root in the container, creates `/run/sshd`, starts the [SSH server](#ssh-server) through the in-container manager, and then runs `sleep infinity` as `agent` through `runuser` ([Images](images.md#base-image-contents)). Your host user still maps to `agent`; root in the container maps to an ID from your subordinate range, not to root on the host. See [Execution identities](#execution-identities). |
| Privileges | `--security-opt=no-new-privileges` |
| Network | `--network=pasta:--no-map-gw`: the container cannot reach the host through the gateway address ([Why Podman 4.4.0](host-prerequisites.md#why-podman-440)) |
| Memory | `--memory`: 8 GiB, or the value of `--memory` |
| CPUs | `--cpus`: 4, or the value of `--cpus` |
| Processes | `--pids-limit`: 2048, or the value of `--pids-limit` |
| Shared memory | `--shm-size`: 1 GiB, or the value of `--shm-size` |
| Published port | `--publish 127.0.0.1:PORT:22`: the sandbox's SSH server on port 22 in the container, published on host loopback only. It is the only port a sandbox publishes. |

A container that `up` creates mounts its three named volumes, or with `WORKSPACE` the workspace bind and the home and SSH server state volumes, and nothing else: no other host path, no SSH-agent socket, no container-engine socket, and no display socket (ADR-0003). The resource limits need delegated cgroup v2 controllers, which the preflight checks.

A sandbox created by this version does not yet offer agent version pins on `up` (#43). `--agents` takes agent names only and installs the version that is current at that time ([Enable agents with `up`](#enable-agents-with-up)).

`up` opens no SSH connection to the sandbox. Without `--ssh-config` it neither reads nor writes any file in your SSH directory; with it, `up` installs the opt-in [SSH setup](ssh.md#ssh-setup) after the sandbox runs. On Windows, the preflight runs its read-only machine checks through `podman machine ssh`. These checks run in the Podman machine, not in the sandbox.

### Execution identities

Two identities run in a sandbox (ADR-0006):

- **Container root** runs administrative control. The entrypoint starts as root to prepare the container, and the in-container manager starts the SSH server as root, as sshd requires. The executable makes its administrative control calls to the in-container manager with `podman exec --user=0:0`, so they run as root in the container whatever the container's start user is. The [session query](#running-agent-sessions), which [`agents login`](agents.md#sign-in-to-an-agent) also makes to check that the manager answers, the two calls of [`agents enable`](agents.md#how-the-request-reaches-the-manager), which `up --agents` also makes, the two calls of [`agents disable`](agents.md#how-the-request-reaches-the-manager), the two calls of [`agents status`](agents.md#show-an-agents-status), the [agent query](#agents-column) of `list`, and the `version` check before [`agents run`](agents.md#how-the-request-reaches-the-manager) and [`agents session`](agents.md#identity) are such calls.
- **`agent`, UID and GID 1000,** is the required identity for agent installation and all agent work: agents, shells, toolchains used by agents, and agent sessions. The entrypoint's long-running process already runs as `agent`. For `agents enable`, `up --agents`, `agents disable`, `agents status`, and the agent query of `list`, the manager, called as container root, starts itself again as a worker with UID and GID 1000 before it reads the home volume, runs npm, removes an agent's command, or runs an agent's status probe ([How the request reaches the manager](agents.md#how-the-request-reaches-the-manager)). `shell` opens its shell with `podman exec --user=1000:1000` ([Shell and SSH access](ssh.md#open-a-shell)), and an SSH session signs in as `agent`. The manager call that runs the [Git identity](integrations.md#git-identity) or [Git credentials](integrations.md#git-credentials) workflow runs as `agent` from the start: it uses `podman exec --user=1000:1000 --env HOME=/home/agent`, so Git runs as `agent` and writes the configuration of `agent`. The manager call that runs the [GitHub login](integrations.md#github-login) starts as `agent` in the same way, with a terminal; the manager refuses it under any other identity and starts the GitHub CLI with an explicit request for UID and GID 1000. `agents run` starts the manager as `agent` with `podman exec --user=1000:1000`, and the manager starts the agent with an explicit request for UID and GID 1000 ([How the request reaches the manager](agents.md#how-the-request-reaches-the-manager)). `agents login` starts the manager as `agent` in the same way, both to ask whether the agent is enabled and to run the login workflow, and the manager starts the agent's login command with an explicit request for UID and GID 1000 ([Sign in to an agent](agents.md#sign-in-to-an-agent)). `agents session` starts the manager as `agent` in the same way, and the manager runs the tmux server, the session, and the agent in it with UID and GID 1000; it refuses the request under any other identity ([Keep an agent running in a session](agents.md#keep-an-agent-running-in-a-session)). For the [session query](#running-agent-sessions), the manager, called as container root, starts itself again as a worker with UID and GID 1000, which reads the sessions from the tmux server of `agent`.

Root in the container is root only inside the sandbox's user namespace: it maps to an ID from your subordinate range, not to root or to your user on the host.

## SSH server

Every sandbox runs an SSH server (sshd), the endpoint that editors and desktop UIs connect to. It is reachable only from your own machine: its port is published on `127.0.0.1` and on no other address. Signing in needs a key authorized for the user `agent` in the sandbox. Only the opt-in [SSH setup](ssh.md#ssh-setup) authorizes one, so a sandbox with a new SSH server state volume has none until it is installed ([Host keys and sign-in](#host-keys-and-sign-in)).

### Port

`up` chooses the port when it creates the container and records it in the `io.github.sandboxed-agents.ssh-port` label:

- With `--port N`, the port is `N`. When `N` is not free, `up` exits with status 1 and names the port.
- Without `--port`, the port is the first free one from 2222 through 65535. When none is free, `up` exits with status 1.

In both cases `up` builds and creates nothing before it has the port.

A port is free when no `sandboxed-agents` container records it and `up` can bind it on `127.0.0.1`:

- **Recorded ports.** `up` reads `podman ps --all` and collects the `ssh-port` label of every `sandboxed-agents` container, running or stopped, in every controller group: containers and backup containers under the product's Podman names, and renamed containers that still carry an owner and a sandbox name label. This is the only place where a command reads containers of other controller groups. It reads their labels and changes nothing on them. A container without an `ssh-port` label, such as one created before ports were recorded, reserves no port and is skipped. A product container that has the label with a value that is not a port from 1 through 65535, an empty value included, makes `up` exit with status 1 and name that container and value, because a port it cannot read could be in use.
- **Listeners.** `up` opens a listening TCP socket on `127.0.0.1` and that port on the host where the executable runs, on Windows too, and closes it again at once. When the port is in use or the operating system denies the bind, the port is not free. On Linux, ports below 1024 usually need privileges, so they are not free for `up`. Any other error stops `up` and is reported with the port.

The port belongs to the sandbox's configuration and never changes:

- `stop`, `start`, and `restart` keep it, and a started sandbox is published on the same port again. Before `start` starts a stopped container, it checks that the recorded port is free on `127.0.0.1`. When another program holds the port, `start` exits with status 1, names the port, and chooses no other one. `restart` runs the same check after it has stopped the container, so a `restart` that fails here leaves the sandbox stopped. A running container already holds its own port, so `start` and `up` on a running sandbox check no listener.
- `up` on an existing sandbox starts it when `--port` is not given or equals the recorded port. A different `--port` is refused (see [Refusals](#refusals)). A stopped sandbox is checked for listeners as `start` checks it.
- To change the port, run `remove NAME` and then `up NAME --port N`. `remove` without `--volumes` keeps the volumes, so the sandbox keeps its files and its host keys.

Every container that `up` starts must record a valid port. A container whose `ssh-port` label is missing or not a port from 1 through 65535, such as one created before ports were recorded, is refused by `up`, and a stopped one also by `start` and `restart`. The message names `remove NAME` followed by `up NAME`. `stop` and `remove` still work on it. `list` shows `-` as its port when the label is missing or empty, and otherwise the label's value as recorded.

### Host keys and sign-in

On every start of the container, the entrypoint runs `sandboxed-agents-manager ssh start` as root. The manager makes sure that each of the three host keys exists in the SSH server state volume, which is mounted at `/etc/ssh`: `ssh_host_ed25519_key`, `ssh_host_ecdsa_key`, and `ssh_host_rsa_key`. It runs `ssh-keygen` only for a key that is missing and leaves existing key files unchanged. It then starts `/usr/sbin/sshd` with the configuration `/usr/local/etc/sandboxed-agents/sshd_config` from the image and with those three keys, and sshd runs in the background. When a key cannot be generated or sshd does not start, the entrypoint exits with an error and the container stops. `up`, `start`, and `restart` do not wait for the SSH server: they report the sandbox as running once `podman start` succeeds. With `--ssh-config`, the installation that follows waits up to 30 seconds for the ed25519 host key ([Pinned host key](ssh.md#pinned-host-key)).

The image contains no host keys. On an empty SSH server state volume, the first start therefore generates all three. Every start, also the first one on a kept volume, reuses the keys that exist and generates only those that are missing. The sandbox keeps its host keys across `stop`, `start`, and `restart`, and across `remove` and `up` as long as the volume is kept. `update` keeps them too, because the new container mounts the same volume ([Update a sandbox](updates.md)). `sandboxed-agents fingerprint NAME` prints the SHA256 fingerprints of the three host keys, which it reads from the SSH server state through `podman exec` as root in the container. It needs a running sandbox, changes nothing, and needs no SSH setup.

The configuration in the image allows only public-key authentication, only for the user `agent`, and no root login. Passwords, keyboard-interactive authentication, and agent forwarding are disabled. sshd uses PAM for the account and session of `agent`, and it serves SFTP. TCP forwarding is not disabled. Authorized keys are read only from `/etc/ssh/authorized_keys`, in the SSH server state volume; `~/.ssh/authorized_keys` of `agent` in the home volume is not read.

The opt-in SSH setup writes the sandbox's dedicated key as the only line of that file, owned by root with mode `0644`, so agents cannot change which key signs in ([Authorized key](ssh.md#authorized-key)). A sandbox with a new SSH server state volume has no authorized key until the SSH setup is installed.

## Existing sandboxes

When the container of the sandbox exists with the current owner, and no volume or backup container under its names has a missing or different owner, `up` checks a given `WORKSPACE` ([The workspace is part of the configuration](#the-workspace-is-part-of-the-configuration)), the given limit options, the given toolchain set, and `--port`. If none is given, or each one equals the value recorded on the container, `up` starts the container if it is stopped and exits with status 0. If it already runs, `up` exits with status 0 without a change. With `--agents`, `up` then enables the listed agents before it exits ([Enable agents with `up`](#enable-agents-with-up)); `--agents` is never compared with the container. In both cases it creates, removes, and reconfigures no container or volume, and it does not check or build the image. `up` never changes the configuration of an existing sandbox.

`up`, `start`, and `restart` start an existing container of the current owner as it is. The owner label authorizes them to act on it, but it is not a record of the container's whole configuration. `up` compares only a `WORKSPACE` you give with the recorded workspace, and the limit options, the toolchain set, and `--port` you give with their labels. The image, the other mounts, the user, the user namespace, the network, and the security options of an existing container are not compared with the [container defaults](#container-defaults). A container that carries the current owner label but was created or changed outside `sandboxed-agents`, which needs access to your Podman, is therefore started with the configuration it has. Comparing that configuration has no Story yet.

Only the options you give are compared, and they are compared by value: `--memory 8192m` equals a recorded `8589934592`, and `--cpus 1.50` equals a recorded `1.5`. A limit option that differs from the recorded value is refused (see [Refusals](#refusals)). To change a limit, run `remove NAME` and then `up NAME` with the new value; `up` adopts the kept volumes.

Without `--with`, `up` keeps the recorded toolchain set and starts the sandbox. With `--with`, the given set must equal the recorded one, in any order: `up NAME --with native` starts a sandbox created with `--with native`. A different set is refused, including `--with none` on a sandbox with toolchains and `--with native` on one without (see [Refusals](#refusals)). To change the toolchain set, run `update NAME --with SET`, which keeps the volumes ([Change the toolchain set](updates.md#change-the-toolchain-set)).

Without limit options and without `--with`, `up` does not refuse a container for missing limit or toolchain labels, such as one created before they existed. It does need a valid SSH port label on every existing container ([Port](#port)).

### Kept volumes

Volumes can outlive their container, for example when the container was removed with `sandboxed-agents remove NAME` or `podman rm`, or when an earlier `up` failed after creating them. When no container of the sandbox exists but some or all of its volumes do, and each of them carries the current owner, `up` adopts them: it creates only the missing volumes and a new container on all three. The files in an adopted volume are kept. The new container gets the given limits and the defaults for the others, and a port chosen as for a new sandbox; the volumes record no limits and no port. An adopted SSH server state volume keeps its host keys and its authorized key. With `WORKSPACE`, a kept workspace volume is not adopted but stays unused ([Workspace volumes beside a bind](#workspace-volumes-beside-a-bind)).

## Refusals

`up` refuses to act, exits with status 1, and creates and changes nothing in these cases:

- **Owner conflict.** The container, one of the three volumes, or the backup container exists under the sandbox's Podman name, and its owner label is missing or names another controller group. This includes a container with the current owner when one of its volumes does not have it. The message starts with `owner conflict`, names every such object, and points to Podman: remove or rename each foreign object there. `up` repairs and adopts nothing.
- **Interrupted update.** The backup container `sandboxed-agents-backup.GROUP.NAME` exists and carries the current owner. The message names the backup container and `sandboxed-agents update NAME`. `update` itself refuses such a sandbox in this version and points to Podman instead, because recovering an interrupted update comes with #54 ([Update a sandbox](updates.md#not-in-this-version)).
- **Limit conflict.** On an existing sandbox, a given limit option differs from the value recorded on the container. The message names the limit, the recorded value as the label stores it (in bytes for memory and shared memory), the given value as you typed it, such as `16g`, and `remove NAME` followed by `up NAME` as the way to change it. When the label of a given limit is missing or unreadable, `up` refuses as well, since it cannot rule out a difference; the message then shows the recorded value as empty or as the unreadable label value. In both cases it also says that `up` without that option starts the sandbox as it is. `remove NAME` without `--volumes` keeps the volumes, and the next `up NAME` with the new value adopts them ([Remove a sandbox](#remove-a-sandbox)).
- **Toolchain conflict.** On an existing sandbox, the set given with `--with` differs from the set recorded on the container. The message names the recorded set and the given set, each shown as `none` when it is empty, and `update NAME --with ...` as the way to change it. A container without the toolchain label counts as recorded `none`. `update NAME --with SET` replaces the container with one for the given set and keeps the volumes ([Change the toolchain set](updates.md#change-the-toolchain-set)). `up` issues no build and does not start the sandbox.
- **Workspace conflict.** On an existing sandbox, `WORKSPACE` resolves to another directory than the source of the container's `/workspace` mount, or is given for a sandbox with a workspace volume. The message names the recorded workspace, the bound directory or the workspace volume, and the given one, on Windows both directories as Windows paths, says that `up` without `WORKSPACE` starts the sandbox as it is, and names `remove NAME` followed by `up NAME` with the new `WORKSPACE`.
- **Port conflict.** On an existing sandbox, `--port` differs from the recorded port. The message names both ports and `sandboxed-agents remove NAME` followed by `sandboxed-agents up NAME --port N` as the way to change the port.
- **No valid recorded port.** The existing container has no `ssh-port` label, or its value is not a port from 1 through 65535. The message names `remove NAME` followed by `up NAME`.
- **Port not free.** On a stopped existing sandbox, the recorded port cannot be bound on `127.0.0.1`. On a new sandbox, `--port N` names a port that cannot be bound on `127.0.0.1` or that a `sandboxed-agents` container records, or no port from 2222 through 65535 is free. The message names the port. `up` builds and creates nothing.
- **Unreadable recorded port.** On a new sandbox, a `sandboxed-agents` container in any controller group has an `ssh-port` label that is not a port from 1 through 65535. The message names the label value and the container.

If a Podman lookup itself fails or returns output that `up` cannot read, `up` also stops with status 1, reports the failure with Podman's message, and changes nothing.

### Order of checks

`up` runs its checks in the order described in [Development](development.md#order-of-checks) and reports only the first failure:

| Step | What `up` does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error, an invalid sandbox name, a limit option or `--port` in a format it does not accept, an invalid toolchain selection, an unknown agent name in `--agents`, or a `WORKSPACE` that the [workspace guards](#workspace-guards) refuse, before any Podman call. `--help` ends here and exits with status 0 when the group is valid. |
| 2. Preflight | reports a missing host prerequisite. On Windows, it also reports a required prerequisite that could not be checked, and then, with `WORKSPACE`, an unsupported automount root ([Windows paths](#windows-paths)). |
| 3. Sandbox existence | looks up the container and the volumes. An unknown name is no failure: it is a sandbox to create. When no container exists, also looks up the backup container and reports an owner conflict on the remaining volumes or the backup container. |
| 4. Owner | reports an owner conflict on an existing container, its volumes, or the backup container, with all foreign objects in one message |
| 5. Interrupted update | reports a backup container with the current owner |
| 7. Preconditions | reports a workspace conflict first, then a limit conflict, then a toolchain conflict on an existing sandbox. Then, on an existing sandbox, a missing or invalid recorded port, a port conflict, or, when it is stopped, a recorded port that is not free. On a new sandbox, an unreadable port inventory, a `--port` that is not free, or no free port. All of this happens before `up` builds an image or creates a volume or container. |

An invalid name, limit value, port value, toolchain selection, agent name, or `WORKSPACE` is therefore reported ahead of a missing prerequisite, and a missing prerequisite ahead of an unsupported automount root, which in turn comes ahead of an owner conflict or a backup container. An owner conflict is reported ahead of an interrupted update, and both ahead of a workspace, limit, toolchain, or port conflict. `up NAME --with nosuch` reports the unknown toolchain, and `up NAME --agents nosuch` the unknown agent, even when a prerequisite is missing or `NAME` belongs to another controller group. Images are built only after all checks have passed, when `up` creates the sandbox. The agents of `--agents` are enabled after the sandbox runs and are not part of these checks: a failed installation or a manager that does not answer leaves the sandbox in place ([Enable agents with `up`](#enable-agents-with-up)).

## Remove a sandbox

```sh
sandboxed-agents remove NAME [--volumes] [--force]
```

`remove` deletes the container of the sandbox. A running sandbox is stopped first; you do not have to stop it yourself. Without `--volumes`, all three volumes are kept, and a later `up NAME` adopts them ([Kept volumes](#kept-volumes)). `remove` asks for no confirmation and does not read standard input. It runs no preflight. On Windows it first selects the Podman machine, as `stop`, `start`, and `restart` do ([Target on Windows](#target-on-windows)). It accepts the same sandbox names as `up`, and a usage error prints a message and the usage line, calls no Podman command, and exits with status 1.

| Option | Effect |
| --- | --- |
| `--volumes` | Also deletes the sandbox's volumes that carry the current owner. A volume with a missing or different owner is never deleted. |
| `--force` | Removes a running sandbox even while agent sessions run in it or the manager does not answer, and ends those sessions. |

`remove` prints one line for the removed container and one line per removed or kept volume, each with its Podman name. When the sandbox has an SSH setup, `remove` also removes its host side, the host entry, key pair, and pinned host key, as `ssh-config NAME --remove` does, also when only volumes of the sandbox remain; a refused `remove` leaves it untouched. `remove` does not remove the authorization from the sandbox's SSH server state volume: without `--volumes`, which keeps that volume, the output says that the authorization remains in it and is replaced by the next `ssh-config NAME --install` ([Remove with `remove`](ssh.md#remove-with-remove)).

### Running agent sessions

Before it stops a running sandbox, `remove` asks the manager in the container for the running agent sessions:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

On Windows, the call also names the selected Podman machine with `--connection`. The manager answers with a JSON array of objects, one per running session, each with a non-empty `name` and a non-empty `agent` string. `remove` names each session as `AGENT/NAME`. If the query fails, returns no such array, or does not finish within 5 seconds, the manager counts as not answering. A stopped sandbox has no running sessions, so `remove` does not ask.

- **Sessions run.** `remove` refuses, names each running session, and names `--force`. With `--force` it removes the sandbox and names the sessions it ended.
- **No answer.** `remove` refuses, says that running sessions cannot be ruled out, and names `--force`. With `--force` it still asks the manager. If the manager still does not answer, `remove` removes the sandbox and says that sessions that may have been running were ended and cannot be named.

The manager answers with every agent session started by [`agents session`](agents.md#keep-an-agent-running-in-a-session) that is still running, and with an empty list when none runs ([Session query](agents.md#session-query)). `remove` therefore refuses on a sandbox with a running agent session unless `--force` is given.

### Owners and kept volumes

`remove` checks the owner label of the container, of all three volumes, and of the backup container, like `up` ([Podman names and labels](#podman-names-and-labels)). On a refusal it exits with status 1 and stops, removes, or changes nothing.

| Situation | `remove NAME` | `remove NAME --volumes` |
| --- | --- | --- |
| Neither the container, a volume, nor the backup container exists | refuses: the sandbox is unknown | refuses: the sandbox is unknown |
| The container has a missing or different owner | refuses with an owner conflict | refuses with an owner conflict |
| The backup container `sandboxed-agents-backup.GROUP.NAME` exists with the current owner, and no object is foreign | refuses and names `sandboxed-agents update NAME` | refuses and names `sandboxed-agents update NAME` |
| The container has the current owner, a volume does not, and no backup container exists | refuses with an owner conflict | removes the container and the owned volumes, keeps the other volume, names it, points to Podman to remove or rename it, and exits with status 1 |
| A volume has a missing or different owner, and a backup container exists | refuses with an owner conflict | refuses with an owner conflict, also with `--force` |
| No container, and only volumes with the current owner remain | reports that no container exists, names `remove NAME --volumes`, deletes nothing, and exits with status 0 | deletes those volumes, names each, and exits with status 0 |
| No container, and a remaining volume has a missing or different owner | refuses with an owner conflict | refuses with an owner conflict |

An owner-conflict message names each foreign object by its Podman name and points to Podman, where you remove or rename it. A backup container with a missing or different owner is an owner conflict as well. A foreign object is reported ahead of an interrupted update. `remove NAME --volumes` on a sandbox with a foreign volume and no backup container is the only command that acts on a sandbox with an owner conflict; it touches only objects that carry the current owner.

A bound host directory is never deleted, with or without `--volumes`, and no Podman call of `remove` names it. A workspace volume left unused beside a bind ([Workspace volumes beside a bind](#workspace-volumes-beside-a-bind)) counts as one of the sandbox's volumes.

### Order of checks for `remove`

`remove` reports only the first failure, in the order described in [Development](development.md#order-of-checks):

| Step | What `remove` does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error or an invalid sandbox name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox, also for a sandbox of another controller group. When no container exists, looks up the backup container and checks the owners of the remaining volumes and of the backup container. |
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

These commands act on the existing container of the sandbox `NAME` and on nothing else. `start` and `restart` start that same container again. All three keep the three volumes and the sandbox's configuration: the Podman options, the resource limits, the SSH port, the owner labels, and the workspace. Before `start` or `restart` starts a stopped container, it checks the recorded SSH port ([Port](#port)). They create, remove, and reconfigure no container or volume, and they do not check or build the image. They run no preflight and call neither `ssh` nor `podman machine ssh`.

### Target on Windows

On Windows, `stop`, `start`, `restart`, `remove`, [`agents enable`, `agents disable`, `agents status`, and `agents run`](agents.md#how-the-request-reaches-the-manager), [`agents login`](agents.md#sign-in-to-an-agent), [`shell`](ssh.md#target-on-windows), [`ssh-config`](ssh.md#order-of-checks-for-ssh-config), `fingerprint`, [`check NAME`](check.md), and [`integrations config` and `integrations login`](integrations.md#what-the-host-contributes) first select the Podman machine by the rule of the preflight ([Selected Podman machine](host-prerequisites.md#selected-podman-machine)). They read only `podman machine list` and `podman machine inspect`, after the usage and name checks and before their first lookup of the sandbox. The command exits with status 1 and looks at no sandbox object when the machine list or inspect output cannot be read, when no machine exists, when several machines exist and not exactly one of them is the default, when the selected machine's name starts with `-` or contains a line break or a NUL character, or when the selected machine is stopped, does not use WSL2, is rootful, or does not report whether it is rootful. The message ends with the advice to run `sandboxed-agents check`, which shows which prerequisite is missing. The command never starts the machine.

Every later Podman call of the command, including the session query, names the selected machine with `--connection`, without `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or `CONTAINER_SSHKEY` in its environment, so neither these settings nor another default connection redirect it. On Linux these commands call the local `podman` as before.

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

1. They check the command line and the sandbox name. On Windows, they then select the Podman machine ([Target on Windows](#target-on-windows)).
2. They look up the container and the three volumes of the sandbox by their exact Podman names, as `up` does, and read the owner label of each one that exists and whether the container is running. When no container exists, they also look up the backup container.
3. When neither the container, a volume, nor the backup container exists, the command reports `sandbox NAME does not exist in this controller group` and exits with status 1. When no container exists, an owner conflict on the remaining volumes or the backup container is reported here. When only owned volumes remain, with no backup container, the name is known, but there is no container to act on: the command reports `sandbox NAME has no container; run sandboxed-agents up NAME, which adopts its volumes`. Either way it exits with status 1 and changes nothing. A backup container with the current owner, with or without volumes, is reported in step 5.
4. When the container exists, they look up the backup container and check the owners of the container, every existing volume, and the backup container together (see [Owners and backup containers](#owners-and-backup-containers)).
5. They refuse a sandbox with a backup container of an interrupted update and name `sandboxed-agents update NAME`.
6. They act on the running state of the container, as the table above shows. `stop` and `restart` on a running sandbox first pass the [session guard](#session-guard). Before `start` and `restart` run `podman start`, they read the recorded SSH port and check that it is free on `127.0.0.1`. A missing or invalid recorded port is refused with `remove NAME` followed by `up NAME`. A port that another program holds is refused with a message naming the port. Either way no `podman start` runs.

The lookups and checks of steps 2 to 5 run also when the command will change nothing. `stop` on a stopped sandbox and `start` on a running one therefore still refuse an owner conflict or a backup container, exit with status 1, and do not print the sandbox's state.

If a Podman command fails, the command stops, reports the Podman command and its exit status, and exits with status 1 without printing the sandbox's state. If `restart` stopped the container and the port check or `podman start` then fails, the sandbox stays stopped.

### Session guard

Before `stop` or `restart` stops a running container, it asks the in-container manager for the running agent sessions:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

The manager answers when this command exits with status 0 within 30 seconds and prints exactly one JSON array on standard output. Each element is an object whose `name` and `agent` are strings that are not empty or only whitespace: the name of a running agent session and the agent it runs, for example `[{"name":"sandboxed-agents-claude","agent":"claude"}]`. An empty array means that no agent session runs. Anything else counts as no answer, including a partly valid array.

The manager reports every running session started by [`agents session`](agents.md#keep-an-agent-running-in-a-session) ([Session query](agents.md#session-query)), so the refusals for running sessions below apply to those sessions.

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
| 1. Usage and names | reports an invalid controller group, then a usage error or an invalid sandbox name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox name, also for a sandbox of another controller group. When no container exists, reports an owner conflict on the remaining volumes or the backup container or, when only owned volumes and no backup container remain, that no container exists. |
| 4. Owner | reports an owner conflict on the container, its volumes, or the backup container, with all foreign objects in one message |
| 5. Interrupted update | reports a backup container with the current owner |
| 7. Preconditions | `stop` and `restart` on a running sandbox without `--force`: reports that the manager did not answer |
| 9. Session guard | `stop` and `restart` on a running sandbox without `--force`: reports the running agent sessions |

The SSH port check of `start` and `restart` is not one of these steps: it is part of the action and runs directly before `podman start`, for `restart` after `podman stop`.

The preflight (step 2), the running-state check (step 6), and the terminal check (step 8) do not apply to these commands. On Windows, the machine selection adds no step: it runs after step 1 and before step 3, and a failed selection is reported before an unknown sandbox, an owner conflict, or an interrupted update. The same holds for `remove`. A sandbox with running agent sessions and an owner conflict or a backup container is therefore refused for the owner or the interrupted update, and the manager is not asked.

## List sandboxes

```sh
sandboxed-agents list
```

`list` takes no arguments; an extra word is a usage error and calls no Podman command. It runs no preflight. On Windows it first selects the Podman machine, as `stop` does ([Target on Windows](#target-on-windows)). It only reads: it runs `podman ps --all`, `podman volume ls`, and `podman container inspect` or `podman volume inspect` for each object it shows, and it asks the manager of each running sandbox for its enabled agents ([`AGENTS` column](#agents-column)). It starts no sandbox and opens no SSH connection.

`list` prints one row per sandbox of the current controller group, sorted by sandbox name:

```text
NAME     STATE         WORKSPACE  SSH PORT  TOOLCHAINS  AGENTS  VOLUMES
agent01  running       volume     2222      native      claude  -
agent02  volumes only  volume     -         -           -       sandboxed-agents.default.agent02.home,sandboxed-agents.default.agent02.workspace
```

| Column | Content |
| --- | --- |
| `NAME` | the sandbox name, not the Podman name |
| `STATE` | one of the states below |
| `WORKSPACE` | the workspace kind recorded on the container, `volume` or `bind`. Without a container, `volume` when the workspace volume exists, otherwise the kind recorded on the backup container. `-` when it is not known. |
| `SSH PORT` | the value of the `ssh-port` label on the container, running or stopped, also in an `owner conflict` row. Without a container, the value on the backup container, also when volumes remain beside it. `-` for a `volumes only` row and when the label is missing or empty. |
| `TOOLCHAINS` | the toolchain set recorded on the container, such as `native`. Without a container, the set recorded on the backup container. `-` for a sandbox without toolchains, for a container without the toolchain label, such as one created before the label existed, and when neither a container nor a backup container exists. |
| `AGENTS` | the enabled agents of a running sandbox whose manager answers, separated by commas, such as `claude,codex`, or `none` when no agent is enabled. `-`, meaning "not available", in every other row ([`AGENTS` column](#agents-column)). |
| `VOLUMES` | the Podman names of the sandbox's volumes that exist, separated by commas, or `-` when none exists |

A sandbox is shown when a container, a backup container, or a volume exists under the current group's Podman names ([Podman names and labels](#podman-names-and-labels)). A container that carries the current group in its owner label is also shown when it was renamed with Podman, under the sandbox name from its `sandbox-name` label; the other commands look only under the exact Podman names and do not find such a container. A sandbox of another group gets no row. Without any sandbox, `list` prints only the header line. Each sandbox gets one row in the first state that applies:

1. `owner conflict`: the container, a volume, or the backup container under the sandbox's Podman names has a missing owner label or one that names another group.
2. `update interrupted`: a backup container exists, with or without a container under the sandbox's own name. A backup container never gets a row of its own.
3. `volumes only`: no container exists, but some or all of the three volumes do.
4. `running` or `stopped`: the state of the container.

If a Podman call fails or returns output that `list` cannot read, `list` prints no table, reports the failure, and exits with status 1. The agent query is the exception ([`AGENTS` column](#agents-column)).

### `AGENTS` column

The agent selection lives only in the sandbox's home volume ([Agents](agents.md#see-the-enabled-agents)). Host state keeps no copy, so `list` reads the selection through the manager of each sandbox in the `running` state:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents list
```

On Windows, the call also names the [selected Podman machine](#target-on-windows) with `--connection`. The manager answers with a JSON array of the enabled agent names, sorted by name. `list` waits at most 5 seconds for each answer. It shows the names in alphabetical order, each once, separated by commas, or `none` for an empty array.

`list` shows `-` in `AGENTS` when the manager does not answer within 5 seconds, when the call exits with a non-zero status, or when its output is not a JSON array of names or names an agent that the catalog of the executable does not know. Such a row is no failure: `list` still prints the table and exits with status 0. A sandbox in the `stopped`, `volumes only`, `owner conflict`, or `update interrupted` state also gets `-`, and `list` sends no query to it. `list` starts no sandbox to read its agents.

The query changes nothing. The manager, called as container root, starts its trusted worker as `agent`, UID and GID 1000, which reads the selection file from the home volume. The worker takes no [manager lock](agents.md#manager-lock) and creates no file or directory, also when the selection file or its directory does not exist; a missing selection counts as no agent enabled. A selection file that is not a valid JSON object, or that names an agent unknown to the catalog, makes the query fail, and `list` shows `-`. The manager saves the selection atomically, so a query that runs while `agents enable` writes it reads either the old or the new selection.

## Verification

The behavior on this page is covered by offline tests against fake `podman` and `ssh` programs ([Development](development.md#test-seams)). The host state locations are covered by tests of the path resolver, and the workspace tests use the resolved paths only as protected host paths; the SSH setup's use of host state is covered as described in [Shell and SSH access](ssh.md#verification). The workspace bind, its guards, the unused workspace volume, and the workspace conflict are covered on Linux against the fake `podman`, with real directories, symlinks, and hard links in temporary test directories. Bind-mount aliases are covered only through a static mount table that the tests inject in place of `/proc/self/mountinfo`; no test creates a real mount. On Windows, the refusal of a custom, disabled, or unreadable automount root after the preflight, and the stop at the preflight when the machine does not run, are covered with a fake Windows host identity, so these tests run on Linux as well. The translation, the guards with real symlinks, junctions, and hard links, and the workspace conflict need the native Windows file system; those native tests run in the Windows job of the offline suite and not on other hosts ([Development](development.md#windows-workspace-tests)). The refusal of a workspace without a drive letter has no test. These tests check the mounts that `up` passes to Podman, not what a real container can reach. The manager's side of the session query is covered by tests with injected process functions. The refusals of `remove`, `stop`, and `restart` on running sessions are covered by a fake `podman` whose `exec` answer reports sessions. The toolchain selection, the image builds of `up`, the toolchain label, and the `TOOLCHAINS` column are covered by a fake `podman` that reports images and their labels. `up --agents` and the `AGENTS` column are covered by a fake `podman` whose `exec` answers stand in for the manager, and by tests that route `exec` to a manager with injected process functions ([Test seams](development.md#test-seams)). These tests check the arguments the executable passes to Podman, such as `--connection`, `--user=0:0`, and the published SSH port. Host key generation and the sshd configuration are covered by manager tests with injected process functions and files. No offline test starts a real container or opens an SSH connection; a real SSH connection against real Podman comes with #35, and nothing on this page has been confirmed against Podman on a live host: neither the target binding on Windows nor the isolation the container defaults and execution identities are meant to provide. Confirming that isolation needs a live test against real Podman (#24).
