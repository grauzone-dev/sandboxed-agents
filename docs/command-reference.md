# Command reference

This page lists every command and option of `sandboxed-agents`, the rules that hold for all of them, and where the topic pages describe each command in detail. The [README](../README.md) describes the product, the supported platforms, and the install methods; the quick guides for [Linux](quick-guide-linux.md) and [Windows 11](quick-guide-windows.md) walk through a first session.

The command is always written `sandboxed-agents`. It has no official short alias and is never installed as `sandbox`.

## Command line

Commands come first, and the sandbox name is the first argument after the command path (ADR-0004): `sandboxed-agents up agent01`, `sandboxed-agents agents enable agent01 codex`. Options follow the arguments.

| Area | Commands |
| --- | --- |
| General | [`version`](#version), [`list`](#list), [`check`](#check), [`build`](#build) |
| Lifecycle | [`up`](#up), [`start`](#start), [`stop`](#stop), [`restart`](#restart), [`remove`](#remove), [`update`](#update) |
| Access | [`shell`](#shell), [`ssh-config`](#ssh-config), [`fingerprint`](#fingerprint), [`check NAME`](#check) |
| Agents | [`agents enable`](#agents-enable), [`agents disable`](#agents-disable), [`agents update`](#agents-update), [`agents login`](#agents-login), [`agents status`](#agents-status), [`agents run`](#agents-run), [`agents session`](#agents-session) |
| Integrations | [`integrations login`](#integrations-login), [`integrations config`](#integrations-config) |

### Syntax and usage lines

Each command entry below shows the command's full syntax in a block. Square brackets mark an optional part, `...` a part that may repeat, and `A|B` a choice of one.

A usage error prints a message and then a usage line on standard error, calls no Podman command, and exits with status 1. The usage line names only the command path, without arguments or options, for example:

- `Usage: sandboxed-agents up` for a usage error of `up`;
- `Usage: sandboxed-agents agents enable` for a usage error of `agents enable`;
- `Usage: sandboxed-agents agents` for a missing or unknown subcommand of `agents`, and `Usage: sandboxed-agents integrations` for one of `integrations`;
- `Usage: sandboxed-agents` for a missing or unknown command.

Each entry names its usage line. The message before it often names the full form, such as `missing sandbox name; use sandboxed-agents remove NAME`. A failure after the usage checks prints only its message.

### Help

Four commands have a help text: [`up`](#up), [`agents enable`](#agents-enable), [`agents update`](#agents-update), and [`agents session`](#agents-session). Given `--help`, each prints its help on standard output, which starts with a `Usage:` line that shows the full syntax, and exits with status 0. It runs no later check: no preflight, no Podman call, and no change. The help's `Usage:` line does not list `--help` itself, so the entries show `--help` on a line of its own. For every other command, `--help` is a usage error like any other unknown word; `agents run` passes it to the agent as one of the agent's arguments.

### Names

A sandbox name matches `^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`: it starts with a letter or digit and continues with letters, digits, `_`, `.`, and `-`. No name is reserved, so `up`, `list`, and `default` are valid sandbox names. An invalid name is a usage error.

`AGENT` is a name from the agent catalog: `claude`, `codex`, `copilot`, or `opencode` ([Agent catalog](agents.md#agent-catalog)). An unknown agent, integration, workflow, or toolchain name fails before Podman is called, and the message lists the valid names.

### Controller group

The environment variable `SANDBOXED_AGENTS_GROUP` selects the controller group, the namespace the executable manages. When it is not set, the group is `default`. A group name matches `^[a-z0-9][a-z0-9-]*$`; an empty or other value makes every command, also with `--help`, print an `invalid controller group` message and the usage line and exit with status 1 before it runs any program.

Every command acts only on the sandboxes of the current group. The sandbox `NAME` in the group `GROUP` is the Podman container `sandboxed-agents.GROUP.NAME`, and its volumes carry the same prefix, so two groups can each hold a sandbox of the same name. A sandbox of another group counts as unknown. Images are the exception: their names contain no group, and all groups share them ([Controller groups](sandboxes.md#controller-groups)).

### Options and values

Each option is given at most once; a repeated option is a usage error. An option that takes a value is given as `--option VALUE` or `--option=VALUE`: the value options of `up`, `--with`, `--agents`, `--version`, `--name`, and `--email`. The other options take no value.

`--with SET` selects a toolchain set: a comma-separated list of the toolchain names `azure`, `dotnet`, `native`, and `playwright`, or `none` alone for the base image without toolchains. `none` must stand alone: `--with none,dotnet` fails. Order and repetition of names do not matter. An empty name, a name with surrounding spaces, an unknown name, or a missing value is a usage error that lists the valid values `azure`, `dotnet`, `native`, `none`, and `playwright` ([Toolchains](images.md#toolchains)).

### Windows

On Windows, each command that reaches Podman selects one Podman machine, the default machine or the only one, and names it in every Podman call with `--connection`. No command starts the machine. When no machine can be selected, or the selected one is stopped, rootful, or not on WSL2, the command fails before it looks at any sandbox, and `sandboxed-agents check` shows which prerequisite is missing ([Selected Podman machine](host-prerequisites.md#selected-podman-machine)).

## Exit status

The exit status is zero when the command did what was asked, or when the requested state already held. It is non-zero when the command could not do it, when it refused, or when a checking command found a problem. So `check` with a missing prerequisite, `check NAME` with any reported problem, and every command given an unknown sandbox name exit non-zero; `up` is the only command that takes an unknown name as a sandbox to create.

Only `agents run` and `shell` pass through another program's exit status: the agent's and the shell's. There are no distinct codes per kind of failure; the executable's own failures exit with status 1, which the agent or shell can also return.

## Order of checks

Every command runs its checks in one fixed order and reports the first failure in this order:

1. usage, invalid names, and unknown agent, integration, workflow, and toolchain names, without calling Podman;
2. the preflight, for the commands that run it (`build`, `up`, `update`); on Windows it also reads the machine's automount root, and a workspace under a custom root is refused here, before any call that builds or creates;
3. an unknown sandbox name, or a sandbox of which only volumes remain when the command needs a container; when one of those volumes has a missing or different owner, that owner conflict is reported here instead;
4. a missing or different owner of the container or of one of the sandbox's volumes;
5. an interrupted update, which every command except `update NAME`, `check NAME`, and `list` refuses, naming `update NAME` (`update --all` handles such a sandbox as `update NAME` does);
6. a stopped sandbox, for commands that need a running container;
7. preconditions, first a manager that does not answer, then a missing toolchain, an agent that is not enabled, or conflicting options on `up`; `update` asks the manager only in its session guard, after the builds;
8. a missing terminal;
9. the session guard, directly before the change.

A command skips the steps that do not apply to it. The topic pages give the steps of each command, for example [`up`](sandboxes.md#order-of-checks), [`remove`](sandboxes.md#order-of-checks-for-remove), [`ssh-config`](ssh.md#order-of-checks-for-ssh-config), and [`integrations`](integrations.md#order-of-checks).

## Owner conflicts

A container or volume with a missing or different owner under the Podman name of one of the group's sandboxes never comes from the executable itself, only from interventions made directly with Podman, and the executable reports such an owner conflict and repairs nothing. Every command checks the owner of the container and of all volumes of the sandbox (step 4 of the order of checks), also when the container has the current owner and only a volume does not, and a backup container with a missing or different owner counts as an owner conflict too. `list` shows the sandbox with the state "owner conflict", which takes precedence over "volumes only" and "update interrupted"; `check NAME` describes the conflict with the Podman names of the objects concerned and exits non-zero; every other command refuses, with one exception: `remove NAME --volumes`, when the sandbox's own container exists, removes the container and the owned volumes, keeps the other volume, names it, exits non-zero, and cleans up the host side of the SSH setup. Without a container, `remove NAME --volumes` deletes nothing in that case and points to Podman. `update --all` passes over the sandbox, builds nothing for it, counts it as not updated, and exits non-zero. For everything else, including an owner conflict beside a leftover backup container, which `update NAME` does not restore while the conflict exists, use Podman: remove or rename the foreign object there. The executable never touches an object that is not provably its own.

## Sandboxes of which only volumes remain

A sandbox of which only volumes remain has no container, and all or only some of its three volumes exist; `remove NAME` without `--volumes` leaves a sandbox in this state. Its name counts as known. When one of the volumes has a missing or different owner, that owner conflict is reported instead.

- `list` and `check NAME` name the volumes that exist. In `list`, the row shows the state `volumes only`, no port, toolchains, or agents, and the workspace kind `volume` when a workspace volume exists.
- `check NAME` reports the volumes and their owner and exits zero when nothing else is wrong. A missing volume is a problem only when a container exists that lacks it. An SSH setup that is still installed for such a sandbox is a problem: `check NAME` reports it, names `ssh-config NAME --remove`, and exits non-zero.
- `up NAME` adopts the existing volumes, creates the missing ones, and says so.
- `remove NAME` without `--volumes` reports that no container exists, names `remove NAME --volumes`, which deletes the volumes, and exits zero. With or without `--volumes`, it also cleans up the host side of the SSH setup.
- Every command that needs a container, `start`, `stop`, `restart`, `shell`, `update NAME`, `fingerprint`, `ssh-config NAME`, `ssh-config NAME --install`, every `agents ...` command, and every `integrations ...` command, fails at step 3 of the order of checks and names `up NAME`.
- `ssh-config NAME --remove` is the exception: it cleans up the host side of the SSH setup even when no container exists.
- `update --all` passes over such a sandbox and does not count it as a failure.

## A manager that does not answer

Each sandbox runs a manager, the program through which the executable installs agents, runs workflows, and queries sessions. When the manager of a running sandbox does not answer, every `agents ...` and `integrations ...` command and `ssh-config NAME --install` fail, say that the manager does not answer, and name `check NAME` for diagnosis and `restart NAME` as the next step. `ssh-config NAME --remove` still cleans up the host side of the SSH setup, says that the authorization in the sandbox could not be removed, and exits non-zero.

This is the first of the preconditions at step 7: it comes after the stopped sandbox and before anything that needs the manager, such as an agent that is not enabled. A missing terminal (step 8) is reported only when the manager is reachable.

`stop`, `restart`, `remove`, and `update` ask the manager only for running agent sessions. When it does not answer, they cannot rule out running sessions and refuse unless `--force` is given ([Running agent sessions and `--force`](#running-agent-sessions-and---force)).

## Running agent sessions and `--force`

An [agent session](#agents-session) keeps an agent running in a sandbox. Every `--force` lets a command proceed although it would end agent sessions; without it, the command refuses, names the sessions concerned, and exits with status 1. With `--force`, the command still asks the manager, so that it can name the sessions it ends, and says so when it could not.

| Command | Refuses without `--force` |
| --- | --- |
| `stop NAME`, `restart NAME`, `remove NAME` | while an agent session runs in the sandbox, or while the manager does not answer and running sessions cannot be ruled out |
| `update NAME`, `update --all` | the same, for each sandbox whose running container would be replaced; the check comes after the builds and before any change |
| `agents disable NAME AGENT` | while the agent `AGENT` has a running session |
| `agents update NAME AGENT` | while the agent `AGENT` has a running session |
| `agents enable NAME AGENT` | when it would replace an existing installation, with `--version X` other than the installed version, while the agent `AGENT` has a running session |

A session of another agent does not block the `agents` commands. For the `agents` commands, `--force` does not help when the manager does not answer: they fail at step 7 as every `agents ...` command does ([Protect a running agent session](agents.md#protect-a-running-agent-session), [Session guard](sandboxes.md#session-guard)).

## `version`

```text
sandboxed-agents version
```

Usage line: `Usage: sandboxed-agents version`.

Prints two lines: `sandboxed-agents` followed by the version, which for a release is its full tag including the `v`, and `assets` followed by the hash of the build assets embedded in the executable. It takes no arguments and calls no Podman command.

## `list`

```text
sandboxed-agents list
```

Usage line: `Usage: sandboxed-agents list`.

Prints one row per sandbox of the current controller group, sorted by name, with the columns `NAME`, `STATE`, `WORKSPACE`, `SSH PORT`, `TOOLCHAINS`, `AGENTS`, and `VOLUMES`. The state is the first that applies of `owner conflict`, `update interrupted`, `volumes only`, and `running` or `stopped`. A `running` or `stopped` sandbox that is [outdated](#update) is shown as `running (outdated)` or `stopped (outdated)`. `AGENTS` lists the enabled agents of a running sandbox whose manager answers within 5 seconds, `none` when no agent is enabled, and `-` otherwise.

`list` takes no arguments, runs no preflight, and only reads. It exits with status 1 and prints no table when a Podman call or an image query fails; an unanswered agent query only shows `-` ([List sandboxes](sandboxes.md#list-sandboxes)).

## `check`

```text
sandboxed-agents check
sandboxed-agents check NAME
```

Usage line: `Usage: sandboxed-agents check`.

Without a name, `check` runs the preflight: it checks the [host prerequisites](host-prerequisites.md) and prints one line per prerequisite, starting with `OK:` or `MISSING:` on Linux and with `ok:`, `missing:`, or `unknown:` on Windows. It exits non-zero when a prerequisite is missing or, on Windows, could not be checked. On another operating system it reports that no preflight is available and exits non-zero.

With a sandbox name, `check NAME` runs no preflight and reports the state of that sandbox instead: the container, the volumes and their owners, the recorded resource limits, a backup container of an interrupted update, whether the manager answers, and, with the SSH setup installed, whether an SSH connection through the host entry succeeds. Each problem gets a line starting with `problem:`, and `check NAME` exits non-zero when it reported at least one. Owner conflicts and an interrupted update are findings of its report, not refusals. An unknown sandbox exits non-zero without a report. `check NAME` changes nothing ([Check a sandbox](check.md)).

`check` takes at most one argument and no options.

## `build`

```text
sandboxed-agents build [--with SET]
```

Usage line: `Usage: sandboxed-agents build`.

`build`, with or without `--with`, pulls the Debian base image again and rebuilds the base image and then every toolchain image that exists on the host for the current executable, all without the layer cache, so that current packages are installed. `build --with SET` additionally makes sure the image for that toolchain set exists. `build --with none` builds what `build` builds. It runs the preflight first and needs network access.

- When the base image fails to build, `build` stops at once, builds no toolchain image, and exits non-zero. The previous images stay usable.
- When a toolchain image fails to build, `build` still rebuilds the remaining ones, names the failed sets at the end, and exits non-zero. A failed set keeps its old image.
- After a `build` without a failure, every image of the current executable is current, so `build` followed by `update --all` brings the whole host to fresh packages.

`up` and `update` build only a missing image, where a toolchain image that was not built on the current base image counts as missing. Existing sandboxes keep their image until `update` replaces their container, and `list` marks them as outdated. Images are shared across the host, so a `build` in one controller group marks sandboxes of other groups as outdated as well ([Build images](images.md#build-images)).

No command removes old images: an image that a rebuild replaced stays on the host until you remove it with Podman, for example with `podman image prune` ([Old images](../README.md#old-images)).

## `up`

```text
sandboxed-agents up NAME [WORKSPACE] [--memory SIZE] [--cpus N] [--pids-limit N] [--shm-size SIZE] [--with SET] [--port N] [--agents LIST] [--ssh-config]
sandboxed-agents up --help
```

Usage line: `Usage: sandboxed-agents up`. `--help` prints the help of `up` and may stand anywhere after `up`, as long as the other words form a valid `up` command line.

Creates the sandbox `NAME` in the current controller group and leaves it running, or starts it when it exists. `up` runs the preflight before it looks up or creates anything, and builds the image for the selected toolchain set only when it is missing. It prints `Sandbox NAME is running.` once the sandbox runs.

| Argument or option | Effect | Default |
| --- | --- | --- |
| `WORKSPACE` | an existing host directory to bind at `/workspace` in place of the workspace volume; it directly follows `NAME` | a workspace volume |
| `--memory SIZE` | memory limit, at least `6m` | `8g` |
| `--cpus N` | CPU limit | `4` |
| `--pids-limit N` | process limit | `2048` |
| `--shm-size SIZE` | size of `/dev/shm` | `1g` |
| `--with SET` | toolchains built into the image ([Options and values](#options-and-values)) | `none` |
| `--port N` | SSH port on `127.0.0.1`, from 1 to 65535 | the first free port from 2222 upward |
| `--agents LIST` | comma-separated agents to enable once the sandbox runs, as [`agents enable`](#agents-enable) does | none |
| `--ssh-config` | install the sandbox's SSH setup once it runs, as [`ssh-config NAME --install`](#ssh-config) does | not installed |

Every value is greater than zero and has no sign and no exponent. `SIZE` is a whole number of bytes, optionally followed by `k`, `m`, `g`, or `t` in either case (powers of 1024), at most 9223372036854775807 bytes. `--cpus` takes a whole number, optionally with a point and one to three decimals, at most `9223372036.854`. `--pids-limit` takes a whole number, at most 9223372036854775807.

The workspace, the resource limits, the toolchain set, and the SSH port are set when the sandbox is created and recorded on its container. `up` on an existing sandbox with an option that differs from the recorded value fails and names the difference and the way to change it: `remove NAME` and then `up NAME` with the new value for the workspace, a limit, or the port, and `update NAME --with SET` for the toolchain set. `--agents` and `--ssh-config` are not part of the configuration and also apply to an existing sandbox. When enabling an agent or installing the SSH setup fails, the sandbox keeps running, and `up` exits non-zero and names the commands to retry.

A workspace that does not exist, is not a directory, or contains or lies inside a protected host path is refused before the preflight ([Workspace bind](sandboxes.md#workspace-bind)). `up` adopts the volumes of a sandbox of which only volumes remain. See [Create or start a sandbox](sandboxes.md#create-or-start-a-sandbox).

## `start`

```text
sandboxed-agents start NAME [--ssh-config]
```

Usage line: `Usage: sandboxed-agents start`.

Starts the existing, stopped container of the sandbox and prints `Sandbox NAME is running.`; on a running sandbox it changes nothing. Before it starts the container, it checks that the recorded SSH port is free on `127.0.0.1`. `--ssh-config` then installs the SSH setup as [`ssh-config NAME --install`](#ssh-config) does. `start` keeps the volumes and the configuration, runs no preflight, and checks and builds no image. Nothing starts on its own: a command that needs a running sandbox fails on a stopped one and names `start NAME` ([Stop, start, and restart a sandbox](sandboxes.md#stop-start-and-restart-a-sandbox)).

## `stop`

```text
sandboxed-agents stop NAME [--force]
```

Usage line: `Usage: sandboxed-agents stop`.

Stops the sandbox's container and prints `Sandbox NAME is stopped.`; on a stopped sandbox it changes nothing. It keeps the volumes and the configuration. Before it stops a running sandbox, it asks the manager for running agent sessions and refuses while one runs or the manager does not answer; `--force` stops it anyway and ends those sessions ([Running agent sessions and `--force`](#running-agent-sessions-and---force)).

## `restart`

```text
sandboxed-agents restart NAME [--force]
```

Usage line: `Usage: sandboxed-agents restart`.

Stops a running sandbox and starts it again, or starts a stopped one, and prints `Sandbox NAME is running.`. It is the next step that commands name when the manager does not answer. On a running sandbox it refuses like [`stop`](#stop), and `--force` ends the running sessions. When the recorded SSH port is not free after the stop, the sandbox stays stopped.

## `remove`

```text
sandboxed-agents remove NAME [--volumes] [--force]
```

Usage line: `Usage: sandboxed-agents remove`.

Deletes the container of the sandbox, stopping it first when it runs, and removes the host side of its SSH setup: the host entry, the key pair, and the pinned host key. `remove` asks for no confirmation.

- Without `--volumes`, the three volumes are kept, and a later `up NAME` adopts them. The authorization in the SSH server state volume stays too, and the next `ssh-config NAME --install` replaces it.
- `--volumes` also deletes the sandbox's volumes that carry the current owner, including the home volume with the agents, credentials, and other files stored in it. A volume with a missing or different owner is never deleted ([Owner conflicts](#owner-conflicts)).
- `--force` removes a running sandbox although agent sessions run in it or the manager does not answer, and ends those sessions ([Running agent sessions and `--force`](#running-agent-sessions-and---force)).

A bound workspace directory is never deleted. On a sandbox of which only volumes remain, `remove NAME` deletes nothing, names `remove NAME --volumes`, and exits zero ([Remove a sandbox](sandboxes.md#remove-a-sandbox)).

## `update`

```text
sandboxed-agents update NAME [--with SET] [--force]
sandboxed-agents update --all [--force]
```

Usage line: `Usage: sandboxed-agents update`.

`update NAME` replaces the container of an outdated sandbox with one created from the current image for its toolchain set, and keeps its volumes, its recorded configuration, its SSH setup, and whether it was running. `update NAME --with SET` does the same with another toolchain set. On an up-to-date sandbox, `update` changes nothing and exits zero. `update` takes exactly one target, `NAME` or `--all`; `update --all` does not take `--with`.

A sandbox counts as outdated, and `list` marks it, when its container was not created from the current image for its toolchain set, or when that image was not built on the current base image, which is also the case for a set whose rebuild failed in [`build`](#build). In the second case `update` treats the image for the current base as missing and builds it before it replaces the container, and `up NAME --with ...` treats it as missing in the same way and builds it before it creates the sandbox. `update` builds only a missing image and may use the layer cache; to get fresh packages, run `build` first and then `update`.

`update` runs the preflight and builds before it changes anything. On a running sandbox, it then asks the manager for running agent sessions and refuses while one runs or the manager does not answer, unless `--force` is given. When a later step fails, `update` restores the previous container. An update that was interrupted leaves a backup container `sandboxed-agents-backup.GROUP.NAME`; every command except `update NAME`, `check NAME`, and `list` refuses such a sandbox, and `update NAME` completes or undoes the interrupted update.

`update --all` updates every sandbox of the current controller group, building each missing image once before it changes any sandbox. A build failure stops it before any change. It passes over sandboxes with an owner conflict and sandboxes of which only volumes remain, and skips sandboxes with running sessions unless `--force` is given. It exits non-zero when any sandbox could not be updated, except for sandboxes of which only volumes remain ([Update a sandbox](updates.md)).

No command removes old images: the image a sandbox used before the update stays on the host until you remove it with Podman, for example with `podman image prune` ([Old images](../README.md#old-images)).

## `shell`

```text
sandboxed-agents shell NAME
```

Usage line: `Usage: sandboxed-agents shell`.

Opens `/bin/bash` in the running sandbox as the user `agent`, in `/workspace`, through `podman exec`. It needs no SSH setup.

- With a terminal on standard input, `shell NAME` opens an interactive shell with a pseudo-terminal, which ends when you leave it.
- Without one, for example with input from a pipe or a file, it starts the shell without a pseudo-terminal. The shell reads commands from standard input, its output passes through to the standard output and standard error of `shell`, and it ends when the input ends.

`shell` therefore never fails for a missing terminal (step 8). It exits with the exit status of `podman exec`, which is the shell's own exit status once the shell has started. A command that runs a single command line in a sandbox is not part of the first version; [`agents run`](#agents-run) runs only agents ([Open a shell](ssh.md#open-a-shell)).

## `ssh-config`

```text
sandboxed-agents ssh-config NAME [--install|--remove]
```

Usage line: `Usage: sandboxed-agents ssh-config`.

Manages the opt-in SSH setup, with which `ssh`, VS Code Remote SSH, and other desktop UIs reach the sandbox's SSH server on its loopback port. Until you install it, no command changes a file in your SSH directory.

- `ssh-config NAME` prints the sandbox's host entry and changes no file.
- `--install` installs the SSH setup of the running sandbox: a key dedicated to the sandbox, authorized in the sandbox, the sandbox's ed25519 host key, pinned in host state, and the host entry. It needs a manager that answers. On a sandbox that already has the setup with the same host key, it changes nothing.
- `--remove` removes the host side of the SSH setup and, on a running sandbox whose manager answers, the authorization in the sandbox. It works on a stopped sandbox and on one of which only volumes remain.

The host entry is named after the sandbox: `agent01` for the sandbox `agent01` in the controller group `default`, and `NAME.GROUP`, such as `agent01.live`, in any other group. `ssh agent01` then connects as `agent` with only the dedicated key and the pinned host key. `--install` refuses, names the conflict, and changes no file when that name already resolves to a configured host in your OpenSSH configuration, which includes entries you wrote by hand and options set for every host under `Host *`.

Each controller group with at least one installed SSH setup has its own `Include` line in your SSH configuration, `~/.ssh/config` on Linux and `%USERPROFILE%\.ssh\config` on Windows. The line is placed first and points to that group's managed configuration in host state, which holds the group's host entries. It is removed with the group's last host entry, by `ssh-config NAME --remove` or `remove NAME`; the rest of your SSH configuration stays as it was.

VS Code Remote SSH lists the host entry and connects through it; choose Linux as the platform and open `/workspace` ([Connect with VS Code Remote SSH](ssh.md#connect-with-vs-code-remote-ssh)). [SSH setup](ssh.md#ssh-setup) describes the files, permissions, and refusals.

## `fingerprint`

```text
sandboxed-agents fingerprint NAME
```

Usage line: `Usage: sandboxed-agents fingerprint`.

Prints the SHA256 fingerprint of each of the running sandbox's three SSH host keys, one per line, each preceded by its key type. It reads them through `podman exec`, needs no SSH setup, and changes nothing. Use it when a desktop UI asks you to confirm a host key; through the host entry the key type is `ssh-ed25519` ([Other desktop UIs](ssh.md#other-desktop-uis)).

## `agents enable`

```text
sandboxed-agents agents enable NAME AGENT [--version X] [--force]
sandboxed-agents agents enable --help
```

Usage line: `Usage: sandboxed-agents agents enable`. `--help` prints the help of `agents enable`.

Installs the agent from its npm package into the home volume of the running sandbox and adds it to the sandbox's agent selection. Without `--version`, an agent that is not enabled gets the version that is `latest` at that moment, and no pin; on an agent that is already enabled, the command installs nothing and reports the installed version and the pin.

- `--version X` installs exactly version `X` and pins the agent to it. `X` is an exact version such as `1.2.3`, also with a prerelease or build metadata; tags such as `latest` and ranges such as `^1.2` are refused.
- `--force` ends a running agent session of `AGENT` before `--version X` replaces the installed version. Where nothing is replaced, it has no effect ([Enable an agent](agents.md#enable-an-agent)).

## `agents disable`

```text
sandboxed-agents agents disable NAME AGENT [--force]
```

Usage line: `Usage: sandboxed-agents agents disable`.

Removes the agent's command and its entry in the agent selection, its pin included. The agent's credentials and cached data stay in the home volume, and so do its installed package files; a later `agents enable` installs the `latest` version without a pin. On an agent that is not enabled, it changes nothing and exits zero. `--force` ends a running agent session of the agent first ([Disable an agent](agents.md#disable-an-agent)).

## `agents update`

```text
sandboxed-agents agents update NAME AGENT [--unpin] [--force]
sandboxed-agents agents update --help
```

Usage line: `Usage: sandboxed-agents agents update`. `--help` prints the help of `agents update`.

Reinstalls the enabled agent: its pinned version when it has a pin, otherwise the version that is `latest` at that moment. `--unpin` removes the pin and installs the `latest` version. `--force` ends a running agent session of the agent before the update. An agent that is not enabled is refused with a message naming `agents enable NAME AGENT` ([Update an agent](agents.md#update-an-agent)).

## `agents login`

```text
sandboxed-agents agents login NAME AGENT [WORKFLOW]
```

Usage line: `Usage: sandboxed-agents agents login`.

Runs one of the agent's login workflows in the running sandbox, as the user `agent`. `WORKFLOW` may be left out for an agent with one login workflow and is required for an agent with more:

| Agent | Login workflows |
| --- | --- |
| `copilot` | `github` |
| `claude` | `subscription`, `console` |
| `codex` | `chatgpt`, `api-key` |
| `opencode` | `provider` |

The command needs an interactive terminal on standard input and standard output and fails without one (step 8, reported only when the manager answers and the agent is enabled). An agent that is not enabled is refused with a message naming `agents enable NAME AGENT`. The credentials stay in the sandbox's home volume, where every agent of the sandbox can read them; the executable stores no credential on the host. `agents login` exits zero when the workflow ended with status 0 and non-zero otherwise, and does not check the sign-in state afterwards ([Sign in to an agent](agents.md#sign-in-to-an-agent)).

## `agents status`

```text
sandboxed-agents agents status NAME AGENT
```

Usage line: `Usage: sandboxed-agents agents status`.

Reports whether the agent is enabled, its installed version, its sign-in state (`signed in`, `not signed in`, or `unknown`), whether its agent session is running, and its pin. Only `claude` declares a status probe; for the other agents the sign-in state is `unknown`, which says nothing about whether the agent is signed in. It exits zero whenever it can report ([Show an agent's status](agents.md#show-an-agents-status)).

## `agents run`

```text
sandboxed-agents agents run NAME AGENT [ARG...]
```

Usage line: `Usage: sandboxed-agents agents run`.

Runs the enabled agent once in `/workspace` of the running sandbox and ends when the agent exits. Every `ARG` reaches the agent unchanged, also one that starts with `-`; `agents run` has no options of its own. It allocates no terminal and keeps standard input, standard output, and standard error as three separate streams, so scripts can pipe and redirect them. It exits with the agent's exit status, and with 128 plus the signal number when a signal ends the agent ([Run an agent](agents.md#run-an-agent)).

## `agents session`

```text
sandboxed-agents agents session NAME AGENT [--stop]
sandboxed-agents agents session --help
```

Usage line: `Usage: sandboxed-agents agents session`. `--help` prints the help of `agents session`.

Starts a persistent agent session for the enabled agent, a tmux session that runs the agent in `/workspace`, and attaches your terminal to it, or attaches to the agent's session when one is running. Detaching with Ctrl-b d, the default tmux binding, or closing the terminal leaves the agent running; the same command attaches again, also from another terminal. The session ends when the agent exits.

Starting or attaching needs an interactive terminal on standard input and standard output and fails without one, starting no session and attaching to none. Starting a session without a terminal is not part of the first version. `--stop` ends the agent's session, and with it the agent, and needs no terminal; when no session runs or the agent is not enabled, it reports that nothing was to do and exits zero ([Keep an agent running in a session](agents.md#keep-an-agent-running-in-a-session)).

## `integrations login`

```text
sandboxed-agents integrations login NAME github|azure|azdo [WORKFLOW]
```

Usage line: `Usage: sandboxed-agents integrations login`.

Signs the running sandbox in to an external account. Each integration has one login workflow, so `WORKFLOW` may be left out:

| Integration | Workflow | What it does | Needs |
| --- | --- | --- | --- |
| `github` | `device` | signs the GitHub CLI in to `github.com` with a device code and sets up Git for `github.com` remotes | an interactive terminal |
| `azure` | `device` | signs the Azure CLI in to an Azure account with a device code | the `azure` toolchain and an interactive terminal |
| `azdo` | `pat` | stores an Azure DevOps personal access token, read from a hidden prompt on a terminal or from the first line of standard input | the `azure` toolchain |

`git` has no login workflow, and `integrations login NAME git` fails and names the config workflows `identity` and `credentials` of [`integrations config`](#integrations-config), with which Git is set up. The command takes no options. The credentials stay in the sandbox's home volume, where every agent of the sandbox can read them. A sandbox without the `azure` toolchain is refused with the `update NAME --with` command that adds it ([Integrations](integrations.md)).

## `integrations config`

```text
sandboxed-agents integrations config NAME git identity [--name N] [--email E]
sandboxed-agents integrations config NAME git credentials
```

Usage line: `Usage: sandboxed-agents integrations config`.

Changes a setting of the running sandbox that is not a sign-in. `git` is the only integration with config workflows, and the workflow name is required because it has two:

- `identity` sets the commit name and email in the global Git configuration of `agent`, for every repository and every agent of the sandbox. `--name N` and `--email E` give the values; the name must not be empty. With either one missing, the command prompts for it and needs an interactive terminal; with both given, it needs none.
- `credentials` makes Git in the sandbox store HTTPS credentials in `/home/agent/.git-credentials`, in plain text in the home volume. Every agent of the sandbox can read the stored credentials and use them. The command stores no credential itself and needs no terminal; Git asks for credentials the next time a remote needs them ([Git credentials](integrations.md#git-credentials)).

## Topic pages

The topic pages describe each command in full, with its refusals and the Podman calls it makes:

- [Host prerequisites](host-prerequisites.md): `check` without a name, and the platforms and Podman floors.
- [Sandboxes](sandboxes.md): controller groups, `up`, the workspace bind, `start`, `stop`, `restart`, `remove`, and `list`.
- [Check a sandbox](check.md): `check NAME`.
- [Images](images.md): toolchains and `build`.
- [Update a sandbox](updates.md): `update`.
- [Agents](agents.md): every `agents` command.
- [Integrations](integrations.md): every `integrations` command.
- [Shell and SSH access](ssh.md): `shell`, `ssh-config`, `fingerprint`, and VS Code Remote SSH.
- [Releases](releases.md), [Development](development.md), and [Live suite](live-suite.md): release files, building and testing, and the live test gate.

## Verification

An offline test at the CLI boundary keeps this page in step with the executable's command tree: it fails when a command or option of the tree is missing here, or when this page names one the tree does not have. Like the rest of the offline suite, that test runs against fake Podman and fake SSH programs and does not start a container, open an SSH connection, or sign in anywhere. This page states nothing as confirmed on a live host; the topic pages say for each behavior what the offline tests cover and what no live run has confirmed yet.
