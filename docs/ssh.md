# Shell and SSH access

`sandboxed-agents shell NAME` opens a shell in the sandbox `NAME`. It works without any SSH setup: the shell is opened through `podman exec` (ADR-0002), not over SSH.

This version has no SSH access to a sandbox. The SSH server and its loopback port come with #18, and the opt-in SSH setup on the host with #19. This page describes only `shell`.

## Open a shell

```sh
sandboxed-agents shell NAME
```

`shell` opens `/bin/bash` in the running sandbox `NAME` of the current [controller group](sandboxes.md#controller-groups), as the user `agent` and with `/workspace` as the working directory. It makes one `podman exec` call:

```sh
podman exec --interactive --tty --user=1000:1000 --workdir=/workspace sandboxed-agents.GROUP.NAME /bin/bash
```

- `--interactive` keeps standard input connected, with and without a terminal.
- `--tty` is passed only when the standard input of `shell` is a terminal ([Without a terminal](#without-a-terminal)).
- `--user=1000:1000` runs the shell as `agent`, UID and GID 1000. The user is given as numbers and is always stated, so the shell does not inherit the container's start user, which is root ([Execution identities](sandboxes.md#execution-identities), ADR-0006).
- `sandboxed-agents.GROUP.NAME` is the sandbox's container in the current controller group, for example `sandboxed-agents.default.agent01` ([Podman names and labels](sandboxes.md#podman-names-and-labels)).

On Windows, the call starts with `--connection` and the selected Podman machine ([Target on Windows](#target-on-windows)).

`shell` starts nothing on its own. On a stopped sandbox it fails and names `sandboxed-agents start NAME` ([Refusals](#refusals)).

### With a terminal

When the standard input of `shell` is a terminal, the shell is interactive and has a pseudo-terminal. The terminal's input, output, and error are connected to it. The shell ends when you leave it, for example with `exit`.

### Without a terminal

When the standard input of `shell` is not a terminal, for example a pipe or a file, `shell` starts the shell without a pseudo-terminal. The shell reads commands from standard input, and their output is passed through to the standard output and standard error of `shell`. The shell ends when the input ends:

```sh
printf 'pwd\nid -u\n' | sandboxed-agents shell agent01
```

A missing terminal is therefore never an error for `shell`. This version has no command that runs a single command line in a sandbox.

### Exit status

`shell` exits with the exit status of `podman exec`. When the shell starts, that is the shell's own exit status: `exit 7` in the shell makes `shell` exit with status 7. When Podman cannot run the shell, its own exit statuses apply, such as 125 for an error in Podman itself ([podman-exec(1), Exit Status](https://docs.podman.io/en/latest/markdown/podman-exec.1.html#exit-status)).

`shell` and `agents run` are the only commands that pass through another program's exit status. When `shell` refuses before it opens the shell, it exits with status 1, which a shell can also return. A non-zero status alone therefore does not tell whether the shell ran.

### Command line

`shell` takes exactly one sandbox name and no option. The name is checked against the [sandbox name rules](sandboxes.md#sandbox-names). A usage error prints a message and a usage line on standard error, calls no Podman command, and exits with status 1:

- Without a name, `shell` reports `missing sandbox name; use sandboxed-agents shell NAME`.
- An option in place of the name is reported as an invalid sandbox name.
- Any word after the name is reported as an unexpected argument, or as an unknown option when it starts with `-`.

`shell` has no `--help`; `shell --help` is a usage error like any other option in place of the name.

## No SSH involved

`shell` needs no SSH server in the sandbox and no SSH setup on the host. It calls neither `ssh` nor `ssh-keygen` and opens no SSH connection. It neither reads nor writes any file in your SSH directory or in the managed SSH data in [host state](sandboxes.md#host-state), and it also works when your SSH directory does not exist.

`shell` runs no preflight and does not ask the in-container manager anything.

## Target on Windows

On Windows, `shell` first selects the Podman machine by the rule of the preflight, as `start` does ([Target on Windows](sandboxes.md#target-on-windows)). It reads only `podman machine list` and `podman machine inspect`, after the usage and name checks and before its first lookup of the sandbox. When no machine can be selected or the selected machine is not acceptable, `shell` exits with status 1, looks at no sandbox object, and names `sandboxed-agents check`. It never starts the machine.

Every later Podman call of `shell`, the `podman exec` call included, names the selected machine with `--connection`, without `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or `CONTAINER_SSHKEY` in its environment. On Linux, `shell` calls the local `podman` without a connection.

## Refusals

Before it opens the shell, `shell` looks up the container, the three volumes, and the backup container of the sandbox by their exact Podman names, as `start` does, and reads their owner labels and whether the container is running. It refuses, opens no shell, and exits with status 1 in these cases:

- **Unknown sandbox.** Neither the container, a volume, nor the backup container exists in the current controller group: `sandbox NAME does not exist in this controller group`.
- **Volumes only.** No container exists, but some or all of the sandbox's volumes do, each with the current owner: `sandbox NAME has no container; run sandboxed-agents up NAME, which adopts its volumes`.
- **Owner conflict.** The container, one of the volumes, or the backup container has a missing owner label or one that names another controller group, also when the container has the current owner and only a volume does not. The message starts with `owner conflict`, names every such object, and points to Podman ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)).
- **Interrupted update.** The backup container of an interrupted update exists with the current owner, whether the sandbox runs or is stopped. The message names `sandboxed-agents update NAME`.
- **Stopped sandbox.** The container exists but is not running: `sandbox NAME is stopped; run sandboxed-agents start NAME`.

If a Podman lookup fails or returns output that `shell` cannot read, `shell` reports the failure and exits with status 1 without opening a shell.

### Order of checks for `shell`

`shell` runs its checks in the order described in [Development](development.md#order-of-checks) and reports only the first failure:

| Step | What `shell` does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error or an invalid sandbox name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox name, also for a sandbox of another controller group. When no container exists, reports an owner conflict on the remaining volumes or the backup container or, when only owned volumes and no backup container remain, that no container exists. |
| 4. Owner | reports an owner conflict on the container, its volumes, or the backup container, with all foreign objects in one message |
| 5. Interrupted update | reports a backup container with the current owner |
| 6. Running state | reports a stopped sandbox |

The preflight (step 2), the preconditions (step 7), the terminal check (step 8), and the session guard (step 9) do not apply to `shell`. On Windows, the machine selection runs after step 1 and before step 3, as for `start`. An owner conflict on a stopped sandbox is therefore reported instead of the message naming `start NAME`, and an interrupted update is reported ahead of a stopped sandbox.

## Verification

The behavior on this page is covered by offline tests against fake `podman`, `ssh`, and `ssh-keygen` programs, with the Linux host path and with a fake Windows host identity ([Development](development.md#test-seams)). They check the complete `podman exec` call, including `--tty`, `--user=1000:1000`, `--workdir=/workspace`, and on Windows `--connection`, the refusals in the order of checks, and that `shell` calls neither `ssh` nor `ssh-keygen` and changes no file in the SSH directory or in host state.

The terminal tests give `shell` a real terminal handle of the test host as standard input: a pseudo-terminal from `/dev/ptmx` on Linux, and a console input handle on Windows. They show that `shell` detects the terminal and then passes `--tty`. The fake `podman` starts no container and no terminal inside one. The tests without a terminal show that the input reaches the fake `podman exec`; its output and exit status, 0, 7, or 125, are scripted, and `shell` passes them through.

No offline test starts a real container, and nothing on this page has been confirmed against Podman on a live host: not that the shell runs as `agent` in `/workspace`, not how it behaves with a pseudo-terminal inside the sandbox, not that it ends when its input ends, and not the target binding on Windows. That evidence needs the live suite (#24).
