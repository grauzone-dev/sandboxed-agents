# Shell and SSH access

`sandboxed-agents shell NAME` opens a shell in the sandbox `NAME`. It works without any SSH setup: the shell is opened through `podman exec` (ADR-0002), not over SSH.

Every sandbox runs an SSH server, published only on a loopback port of your machine ([SSH server](sandboxes.md#ssh-server)). The opt-in [SSH setup](#ssh-setup) lets `ssh`, editors, and desktop UIs connect to it through a host entry: it authorizes a key dedicated to the sandbox, pins the sandbox's host key, and writes the host entry. Without it, no command reads or writes a file in your SSH directory.

## Open a shell

```sh
sandboxed-agents shell NAME
```

`shell` opens `/bin/bash` in the running sandbox `NAME` of the current [controller group](sandboxes.md#controller-groups), as the user `agent` and with `/workspace` as the working directory. It makes one `podman exec` call:

```sh
podman exec --interactive --tty --user=1000:1000 --workdir=/workspace sandboxed-agents.GROUP.NAME /bin/bash
```

- `--interactive` keeps standard input connected, with and without a terminal.
- `--tty` is passed only when the standard input of `shell` is a terminal ([Terminal detection](#terminal-detection)).
- `--user=1000:1000` runs the shell as `agent`, UID and GID 1000. The user is given as numbers and is always stated, so the shell does not inherit the container's start user, which is root ([Execution identities](sandboxes.md#execution-identities), ADR-0006).
- `sandboxed-agents.GROUP.NAME` is the sandbox's container in the current controller group, for example `sandboxed-agents.default.agent01` ([Podman names and labels](sandboxes.md#podman-names-and-labels)).

On Windows, the call starts with `--connection` and the selected Podman machine ([Target on Windows](#target-on-windows)).

`shell` starts nothing on its own. On a stopped sandbox it fails and names `sandboxed-agents start NAME` ([Refusals](#refusals)).

### Terminal detection

`shell` decides from its standard input alone whether it has a terminal. On Linux, standard input is a terminal when it answers the terminal attribute query `TCGETS`. On Windows, it is a terminal when `GetConsoleMode` accepts it, as it does for a console input handle; input that arrives as a pipe is no terminal. Standard output and standard error do not count: with a terminal as input and output redirected to a file, `shell` passes `--tty`, and with input from a file or pipe it passes no `--tty`, also when output goes to a terminal.

### With a terminal

When the standard input of `shell` is a terminal, the shell is interactive and has a pseudo-terminal. The terminal's input, output, and error are connected to it. The shell ends when you leave it, for example with `exit`.

### Without a terminal

When the standard input of `shell` is not a terminal, for example a pipe or a file, `shell` starts the shell without a pseudo-terminal. The shell reads commands from standard input, and their output is passed through to the standard output and standard error of `shell`. The shell ends when the input ends:

```sh
printf 'pwd\nid -u\n' | sandboxed-agents shell agent01
```

A missing terminal is therefore never an error for `shell`. This version has no command that runs an arbitrary shell command in a sandbox; [`agents run`](agents.md#run-an-agent) runs only agents from the catalog.

### Exit status

`shell` exits with the exit status of `podman exec`. When the shell starts, that is the shell's own exit status: `exit 7` in the shell makes `shell` exit with status 7. When Podman cannot run the shell, its own exit statuses apply, such as 125 for an error in Podman itself ([podman-exec(1), Exit Status](https://docs.podman.io/en/latest/markdown/podman-exec.1.html#exit-status)).

`shell` and [`agents run`](agents.md#run-an-agent) are the commands of this version that pass through another program's exit status. When `shell` refuses before it opens the shell, it exits with status 1, which a shell can also return. A non-zero status alone therefore does not tell whether the shell ran.

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
- **Interrupted update.** The backup container of an interrupted update exists with the current owner, whether the sandbox runs or is stopped. The message names `sandboxed-agents update NAME`. `update NAME` recovers such a sandbox ([Recover an interrupted update](updates.md#recover-an-interrupted-update)).
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

## SSH setup

The SSH setup lets `ssh`, VS Code Remote SSH, and other desktop UIs reach a sandbox under a host entry name. It is opt-in (ADR-0002): only the commands below write or remove it, and `up` and `start` without `--ssh-config` neither read nor write any file in your SSH directory.

| Command | What it does |
| --- | --- |
| `ssh-config NAME` | prints the sandbox's host entry and changes no file ([Print the host entry](#print-the-host-entry)) |
| `ssh-config NAME --install` | installs the SSH setup of the running sandbox `NAME` |
| `up NAME --ssh-config` | creates or starts the sandbox, then installs its SSH setup |
| `start NAME --ssh-config` | starts the sandbox, then installs its SSH setup |
| `ssh-config NAME --remove` | removes the SSH setup of the sandbox `NAME` ([Remove the SSH setup](#remove-the-ssh-setup)) |
| `remove NAME` | removes the sandbox's container, and its volumes with `--volumes`, and the host side of its SSH setup ([Remove with `remove`](#remove-with-remove)) |

An installed SSH setup consists of five parts:

- a key pair dedicated to the sandbox, in [host state](sandboxes.md#host-state) of the current controller group;
- its public key as the only authorized key in the sandbox ([Authorized key](#authorized-key));
- the sandbox's ed25519 host key, pinned in host state ([Pinned host key](#pinned-host-key));
- the host entry, in the managed configuration of the controller group in host state ([Host entry](#host-entry));
- the `Include` line of the controller group in your SSH configuration, which points to that managed configuration ([Your SSH configuration](#your-ssh-configuration)).

### Host entry name

| Controller group | Host entry name | Example |
| --- | --- | --- |
| `default` | `NAME` | `agent01` |
| any other group `GROUP` | `NAME.GROUP` | `agent01.live` |

Every host entry ends up in your one SSH configuration, so its name has to be unique across controller groups (ADR-0005). Sandbox names may contain dots, so two sandboxes can still lead to the same name: the sandbox `agent01.live` in `default` and the sandbox `agent01` in `live` both lead to `agent01.live`. OpenSSH also matches host names without regard to case, so `Agent01` and `agent01` reach the same entry. The [conflict check](#host-entry-conflicts) refuses the second sandbox of such a pair.

### Host entry

The host entry connects to the sandbox's recorded loopback port as the user `agent`, with the dedicated key and the pinned host key only. For `agent01` in the controller group `default` with port 2222 on Linux it reads:

```text
Host agent01
  HostName 127.0.0.1
  Port 2222
  User agent
  IdentityFile "/home/alice/.local/state/sandboxed-agents/group-default/ssh/sandbox-6167656e743031/id_ed25519"
  UserKnownHostsFile "/home/alice/.local/state/sandboxed-agents/group-default/ssh/sandbox-6167656e743031/known_hosts"
  GlobalKnownHostsFile none
  HostKeyAlgorithms ssh-ed25519
  UpdateHostKeys no
  IdentitiesOnly yes
  IdentityAgent none
  ForwardAgent no
  StrictHostKeyChecking yes
```

Paths are absolute, written with forward slashes also on Windows, and enclosed in double quotes. `ssh-config NAME` prints the exact text. Each option has one purpose ([ssh_config(5)](https://man.openbsd.org/ssh_config)):

| Option | Effect |
| --- | --- |
| `HostName 127.0.0.1`, `Port PORT` | the SSH port recorded on the container ([Port](sandboxes.md#port)) |
| `User agent` | the only user the sandbox's SSH server accepts |
| `IdentityFile` | the private key dedicated to this sandbox |
| `IdentitiesOnly yes` | `ssh` offers only that key, not your default keys |
| `IdentityAgent none` | `ssh` asks no SSH agent for keys, so no key from your agent is offered |
| `ForwardAgent no` | your SSH agent is never forwarded into the sandbox |
| `StrictHostKeyChecking yes` | an unknown or changed host key ends the connection without a prompt |
| `UserKnownHostsFile` | only the pin of this sandbox counts as a known host key |
| `GlobalKnownHostsFile none` | no system-wide list of known hosts can vouch for the loopback port |
| `UpdateHostKeys no` | `ssh` never adds or replaces a key in the pin |
| `HostKeyAlgorithms ssh-ed25519` | the sandbox has to prove the pinned ed25519 host key; its ECDSA and RSA host keys are not accepted |

`IdentitiesOnly yes`, `IdentityAgent none`, `ForwardAgent no`, and `StrictHostKeyChecking yes` are required in every entry the executable writes. With this entry, `ssh agent01` connects without a host key prompt and offers only the dedicated key.

### Files in host state

The SSH setup keeps its files in the host state directory `group-GROUP` of the current controller group ([Host state](sandboxes.md#host-state)):

```text
group-GROUP/
  ssh/
    config              managed configuration: one host entry per installed sandbox of the group
    sandbox-HEX/
      entry             a copy of the sandbox's host entry
      id_ed25519        private key dedicated to the sandbox
      id_ed25519.pub    its public key
      known_hosts       the pinned host key of the sandbox
```

In the managed configuration, every host entry is followed by a `Host *` line, which ends the entry's block. A sandbox counts as having an SSH setup when its `entry` file exists; the managed configuration must then contain that entry, or the installation reports the SSH setup as incomplete. Removal also recognizes an incomplete SSH setup without an `entry` file ([Remove the SSH setup](#remove-the-ssh-setup)).

`HEX` is the sandbox name's UTF-8 bytes written as lowercase hexadecimal: `agent01` becomes `sandbox-6167656e743031`. The sandbox name itself is not a safe directory name: the file systems of Windows and macOS ignore case by default, so `Agent01` and `agent01` would share a directory, and Windows reserves names such as `con` and strips a trailing dot, which the sandbox name rules allow. A hexadecimal name avoids all three.

`ssh-keygen` creates the key pair as an ed25519 key without a passphrase, so editors and `ssh` connect without asking for one. File permissions protect the private key; any program running as your user can read it and sign in to the sandbox as `agent`.

The installation reads and writes only the directory of the current controller group. It reads no file in the host state of another group.

### Your SSH configuration

The installation adds one `Include` line to your SSH configuration: `~/.ssh/config` on Linux, `%USERPROFILE%\.ssh\config` on Windows. The line points to the managed configuration of the current controller group, by its absolute path with forward slashes in double quotes:

```text
Include "/home/alice/.local/state/sandboxed-agents/group-default/ssh/config"
```

- **Placed first.** The installation adds the line in front of the first line of the file. An `Include` that follows a `Host` or `Match` line applies only within that block ([ssh_config(5), `Include`](https://man.openbsd.org/ssh_config#Include)); in front of all blocks it applies to every host name.
- **Once per controller group.** The line is added with the group's first SSH setup. When any line of the file is identical to it, a further installation in the group adds a host entry to the managed configuration and leaves your SSH configuration unchanged. Each controller group has its own line, so an installation in `live` after one in `default` adds the line of `live` and leaves the line of `default` and all host state of `default` unchanged.
- **Everything else preserved.** The installation changes nothing else in the file: your host entries, the `Include` lines of other groups, and comments stay as they were. The installation writes the new content to a temporary file beside the file and puts it in place of the file. When `~/.ssh/config` is a symlink, it follows the link, replaces the target this way, and keeps the symlink. On Unix the new file gets the replaced file's mode, on Windows it keeps the replaced file's permissions ([Permissions on Windows](#permissions-on-windows)), and an existing `.ssh` directory is not changed.
- **Created when missing.** When the file or the `.ssh` directory does not exist, the installation creates it, and the new file contains only the `Include` line.
- **Removed with the group's last host entry.** Removing an SSH setup removes the line only when it removes the last host entry of the controller group ([The `Include` line on removal](#the-include-line-on-removal)).

No controller group writes the files or the `Include` line of another group.

### Permission modes on Unix

On Unix, the installation creates its files and directories with modes that OpenSSH accepts. OpenSSH ignores a private key that other users can access and refuses a user configuration that other users can write ([ssh(1), FILES](https://man.openbsd.org/ssh#FILES)).

| Path | Mode |
| --- | --- |
| Every directory the installation creates for host state, including missing parents, `ssh`, and `sandbox-HEX` | `0700` |
| Private key `id_ed25519` | `0600` |
| Public key `id_ed25519.pub` | `0644` |
| Pinned host key `known_hosts` | `0600` |
| Managed configuration `config` | `0600` |
| `~/.ssh` and its missing parents, when the installation creates them | `0700` |
| `~/.ssh/config`, when the installation creates it | `0600` |

An existing `~/.ssh` directory keeps its mode, and a rewritten `~/.ssh/config`, or its symlink target, gets the mode of the file it replaces.

On Windows, the installation sets permissions instead ([Permissions on Windows](#permissions-on-windows)).

### Permissions on Windows

OpenSSH for Windows checks the owner and the permissions of a private key, of your default SSH configuration `%USERPROFILE%\.ssh\config`, and of every file that configuration includes, the managed configuration among them, and ignores a key or refuses a configuration file whose permissions fail its check. On Windows, the installation restricts the files and directories it creates to your Windows account, which is enough for that check:

- **Created files and directories.** The key pair, the pin `known_hosts`, the `entry` file, the managed configuration, the directories the installation creates for host state, and `%USERPROFILE%\.ssh` and `%USERPROFILE%\.ssh\config` when the installation creates them, are owned by your account and grant full control to your account only. They do not inherit permissions from their parent directory. New files and folders in a created directory inherit its permissions.
- **No entry for SYSTEM or Administrators.** OpenSSH for Windows does not need one. Other accounts on the computer cannot open these files; an administrator can still take ownership of them, and any program that runs under your account can read them.
- **Existing paths keep their permissions.** Only directories that the installation itself creates get the permissions above. An existing `.ssh` directory and host state directories that exist already, including those that `up` or `start` creates for its lifecycle lock before it installs the SSH setup, are not changed. An existing `%USERPROFILE%\.ssh\config` keeps its permissions, including whether it inherits them from its folder, when the `Include` line is added. The installation does not tighten them, so a configuration file that OpenSSH for Windows refused before is still refused.
- **Managed configuration set again.** Every installation that adds a host entry, also for a second sandbox, writes the managed configuration with the permissions above. A repeated installation with an unchanged host key changes no file, so it does not repair permissions you changed by hand.

When Windows does not let the installation set these permissions, the installation fails. That it works under a standard account without administrator rights is not confirmed yet ([Verification](#verification)).

### Authorized key

The sandbox's SSH server reads authorized keys only from `/etc/ssh/authorized_keys` (`AuthorizedKeysFile /etc/ssh/authorized_keys` in its configuration), not from `~/.ssh/authorized_keys` of `agent`. `/etc/ssh` is the mount point of the SSH server state volume ([Podman names and labels](sandboxes.md#podman-names-and-labels)), so the authorized key is stored in that volume.

The executable passes the dedicated public key, without its comment, on standard input to the manager, as container root (ADR-0006):

```sh
podman exec --interactive --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager ssh authorize
```

The manager accepts exactly one ed25519 public key, writes it as the only line of a new file, and renames that file to `/etc/ssh/authorized_keys`. The file belongs to root and has mode `0644`.

- **Exactly one key.** Every installation replaces the whole file, so it holds the key of the latest installation and nothing else. A key that an earlier SSH setup left there is replaced.
- **Out of reach of agents.** The user `agent` can read the file but not change it, so an agent cannot authorize a key of its own or remove the dedicated one. sshd's `StrictModes` accepts a root-owned file that nobody else can write.
- **Kept across `stop` and `start`.** The file lives in the volume, so the authorization survives `stop`, `start`, and `restart`. Neither `stop` nor `start` without `--ssh-config` changes it. It also survives `remove` and `up` as long as the SSH server state volume is kept, and `update`, which mounts the same volume in the new container and changes no host SSH file ([Update a sandbox](updates.md#what-update-keeps)).
- **Removed by `ssh-config NAME --remove`.** On a running sandbox whose manager answers, `ssh-config NAME --remove` removes the authorization ([Remove the authorization](#remove-the-authorization)). `remove NAME` never does; the authorization stays in the SSH server state volume until `remove NAME --volumes` deletes that volume or the next installation replaces the key.

### Pinned host key

Before it writes anything, the installation reads the sandbox's ed25519 host key from the manager:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager ssh host-key --wait
```

The manager prints the key from `/etc/ssh/ssh_host_ed25519_key.pub` in the SSH server state volume ([Host keys and sign-in](sandboxes.md#host-keys-and-sign-in)). With `--wait`, it waits while that file is missing or does not yet hold a complete ed25519 key, up to 30 seconds, and the executable gives the call the same 30-second limit. Waiting only reads the file: it runs no `ssh-keygen`, writes nothing, and starts no process. It covers `up NAME --ssh-config` and `start NAME --ssh-config`, which install right after `podman start`, while the entrypoint may still be generating the host keys. This is a trusted control path: the key comes through Podman from the sandbox's own volume, not from whatever answers on the loopback port, so the first connection needs no trust-on-first-use prompt. The installation writes the key into `known_hosts` as the only line, for the loopback address and port of the host entry, for example `[127.0.0.1]:2222 ssh-ed25519 AAAA…`. For the recorded port 22, the line starts with plain `127.0.0.1`, because OpenSSH writes a host with the default port without brackets and port ([sshd(8), SSH_KNOWN_HOSTS FILE FORMAT](https://man.openbsd.org/sshd.8#SSH_KNOWN_HOSTS_FILE_FORMAT)).

The pin is strict, and nothing re-pins it:

- `ssh` with the host entry refuses a connection when the sandbox presents a different host key (`StrictHostKeyChecking yes`), and it never updates the pin (`UpdateHostKeys no`).
- With the SSH setup installed, `ssh-config NAME --install` builds the pin line from the host key the manager reports and the recorded port and compares it with the pin. When they differ, it fails, leaves the pin and every other file unchanged, and names `ssh-config NAME --remove` followed by `ssh-config NAME --install` as the way out. `up NAME --ssh-config` and `start NAME --ssh-config` fail in the same way and leave the sandbox running.

The host key changes only when the sandbox gets a new SSH server state volume, for example after `remove NAME --volumes` and `up NAME`. A new recorded port, for example after `remove NAME` and `up NAME --port N`, also makes the pin line differ. In both examples, `remove NAME` already deletes the pin with the rest of the host side of the SSH setup ([Remove with `remove`](#remove-with-remove)), so the next installation pins the new host key and port. For a mismatch that arises otherwise, `ssh-config NAME --remove` deletes the pin, and the next `ssh-config NAME --install` pins the host key the manager reports then. No command replaces a pin in place.

### Host entry conflicts

Before it writes the host entry of a sandbox that has no SSH setup yet, the installation asks OpenSSH whether the host entry name already resolves to a configured host. It runs two queries and compares their output:

```sh
ssh -G -F USER_CONFIG HOST
ssh -G -F none HOST
```

`USER_CONFIG` is your SSH configuration, `~/.ssh/config` or `%USERPROFILE%\.ssh\config`; when that file does not exist, the first query also uses `-F none`. `-G` prints the effective configuration for `HOST` after evaluating `Host` and `Match` blocks, and exits without connecting ([ssh(1), `-G`](https://man.openbsd.org/ssh#G)). A configuration file given with `-F` replaces the system-wide configuration `/etc/ssh/ssh_config`, and `-F none` reads no file at all ([ssh(1), `-F`](https://man.openbsd.org/ssh#F)). The first query therefore resolves your configuration with every `Include` it contains, and the second prints OpenSSH's built-in defaults. Neither reads the system-wide configuration, so options a distribution sets there for every host do not count as a conflict.

- **No difference: not configured.** The installation continues. This includes a `Host` block for the name without options, or with only options whose values equal the defaults.
- **Any difference: configured.** `--install` refuses, names the host entry, and changes no file. Every effective difference counts, also one from a wildcard such as `ServerAliveInterval 60` under `Host *`. To install anyway, remove or rename that host, or exclude the name from the wildcard, for example `Host * !agent01`.

The check catches entries you wrote by hand and entries of other sandboxes, also of other controller groups, because OpenSSH reads every group's managed configuration through its `Include` line. The executable itself only checks whether your SSH configuration exists and reads the host state of the current controller group; it resolves no `Include` and reads no file of another group. A host configured only in the system-wide configuration is not detected. Because OpenSSH evaluates your configuration, a `Match exec` command in it runs during the check.

When a query exits non-zero, for example because your SSH configuration contains an error, `--install` reports the failure and changes no file. A sandbox that already has an SSH setup skips the check.

### Install the SSH setup

`ssh-config NAME --install` needs a running sandbox and a manager that answers. It runs its checks in the [order of checks](#order-of-checks-for-ssh-config), then installs in this order:

1. At step 7 of the order of checks, it checks that the manager answers, with the same `sandboxed-agents-manager version` call as `agents enable` ([How the request reaches the manager](agents.md#how-the-request-reaches-the-manager)).
2. It reads the host key through the manager with `ssh host-key --wait` ([Pinned host key](#pinned-host-key)).
3. With the SSH setup installed, it compares the pin line with the pin and stops there: on a match it prints that nothing changed and exits with status 0; on a mismatch it fails.
4. Without an SSH setup, it runs the [conflict check](#host-entry-conflicts) and reads the managed configuration and your SSH configuration.
5. It creates the missing directories for host state and a staging directory `ssh/.install-*`. There `ssh-keygen` creates the key pair, and the installation writes the public key, the pin, and the `entry` file.
6. It authorizes the public key in the sandbox ([Authorized key](#authorized-key)).
7. It renames the staging directory to `sandbox-HEX`, appends the host entry and a `Host *` line to the managed configuration, and, when your SSH configuration lacks the `Include` line of the controller group, writes it in front of the file, creating `.ssh` and the file when they are missing.

Steps 1 to 4 write nothing. When the manager does not answer, the command fails with the same message as `agents enable`, which names `sandboxed-agents check NAME` and `sandboxed-agents restart NAME`, and creates no key, pin, host entry, or `Include` line.

Without an `entry` file, the `sandbox-HEX` path must not exist at all. When anything exists there, even an empty directory, for example left over from an interrupted installation, the installation refuses before it creates or authorizes a key, names the path, and changes nothing. Move that path aside, then run the installation again.

When a later step fails, the installation removes the staging directory, the `sandbox-HEX` directory, and the directories it created, and restores the managed configuration to its previous content. An authorization that step 6 already wrote stays in the sandbox; the next installation replaces it.

A repeated installation with an unchanged host key creates no new key, writes no file, prints that the SSH setup is already installed and nothing changed, and exits with status 0. It checks only the pin and the host entry in the managed configuration; an `Include` line you removed from your SSH configuration afterwards is not added again.

### Install with `up` or `start`

`up NAME --ssh-config` and `start NAME --ssh-config` first do everything `up NAME` and `start NAME` do, and then install the SSH setup exactly as `ssh-config NAME --install` does, with the same files and calls. `--ssh-config` is not part of the container configuration: `up NAME --ssh-config` on an existing sandbox starts it and installs the SSH setup without an option conflict.

When the installation fails, whatever the reason, the sandbox stays and keeps running. The command exits with status 1, names the reason, and names `sandboxed-agents ssh-config NAME --install` to retry; it issues no Podman call that stops or removes the container. Checks that `up` and `start` run before they start the sandbox, such as the owner check, refuse as they do without `--ssh-config`. On a sandbox that is already running, `up NAME --ssh-config` checks at step 7 that the manager answers; when it does not, it fails before it changes anything, with the manager message and the retry hint, and with `--agents` also the `agents enable` commands to retry.

With `--agents` as well, `up` enables the agents first and installs the SSH setup afterwards. When `up` fails before the installation, for example because the sandbox does not start or an agent is not enabled, it skips the SSH setup, exits with status 1, and names `sandboxed-agents ssh-config NAME --install` to run once the sandbox runs, besides its own retry commands.

### Remove the SSH setup

```sh
sandboxed-agents ssh-config NAME --remove
```

`ssh-config NAME --remove` removes from your host everything `--install` created for the sandbox `NAME`:

- its host entry, with the `Host *` line that follows it, from the managed configuration of the controller group;
- its `sandbox-HEX` directory in host state, with the key pair, the pin, and the `entry` file;
- the `Include` line of the controller group in your SSH configuration, when the removed host entry was the group's last ([The `Include` line on removal](#the-include-line-on-removal)).

It does not change the container, its volumes, or the SSH server in the sandbox. Host entries, key pairs, and pins of other sandboxes stay intact. Whether the authorization in the sandbox is removed as well depends on the sandbox's state; the host side is removed in every case:

| State of the sandbox | Authorization in the sandbox | Exit status |
| --- | --- | --- |
| Running, and the manager answers | removed through the manager ([Remove the authorization](#remove-the-authorization)) | 0 |
| Running, and the manager does not answer | remains; the message says that it could not be removed and names `sandboxed-agents check NAME` and `sandboxed-agents restart NAME` | 1 |
| Stopped | remains; `--remove` starts nothing, runs no command in the container, and says that the authorization remains in the sandbox | 0 |
| No container, and only volumes with the current owner remain | remains in the SSH server state volume, if that volume exists; `--remove` creates and starts no container | 0 |

An authorization that remains is replaced by the next `ssh-config NAME --install`, which needs the sandbox to run again, so exactly one key is authorized afterwards ([Authorized key](#authorized-key)). Until then, no file in host state holds the private key of the remaining authorization, because `--remove` deleted the key pair.

- **No SSH setup.** For removal, the sandbox has no SSH setup when its `sandbox-HEX` path does not exist and the managed configuration of the current controller group holds no block of the sandbox as defined in the next item. `--remove` then leaves your SSH directory unchanged, says that the sandbox has no SSH setup, and exits with status 0, because the requested state already holds.
- **Incomplete SSH setup.** A missing or empty `entry` file alone does not mean that the sandbox has no SSH setup. `--remove` then takes the recorded host entry from the managed configuration of the current controller group, and from no other group's: the first block that starts with `Host` and the sandbox's host entry name, contains the `IdentityFile` line with the path of the sandbox's dedicated private key, and ends at a `Host *` line. When the `sandbox-HEX` path exists but no such block is found, `--remove` still deletes that path and leaves the content of the managed configuration unchanged. When the `entry` file exists, its content is the recorded host entry, also when the managed configuration does not contain it or does not exist at all. In every case, `--remove` removes from the managed configuration only blocks that match the recorded host entry exactly, each followed by its `Host *` line, leaves any other content there unchanged, and deletes the `sandbox-HEX` directory. The authorization and the `Include` line are then handled as for a complete SSH setup. This is the recovery that `ssh-config NAME` and `--install` name for an incomplete SSH setup: `ssh-config NAME --remove`, then `ssh-config NAME --install`.

### Remove the authorization

On a running sandbox, `--remove` asks the manager, as container root (ADR-0006), to remove the authorization:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager ssh deauthorize
```

The manager deletes `/etc/ssh/authorized_keys`, which holds only the dedicated key ([Authorized key](#authorized-key)). Without that file the SSH server accepts no key, so no one can sign in over SSH until the next installation. When the file is missing already, the call succeeds. When the manager does not answer, or the call fails, `--remove` does not stop at step 7 of the [order of checks](#order-of-checks-for-ssh-config) as `--install` does. It still removes the host side, reports that the authorization in the sandbox could not be removed, names `sandboxed-agents check NAME` and `sandboxed-agents restart NAME`, says that the next `ssh-config NAME --install` replaces the authorization, and exits with status 1.

### The `Include` line on removal

Each controller group has its own `Include` line in your SSH configuration ([Your SSH configuration](#your-ssh-configuration)). Removing an SSH setup keeps the line of the current controller group as long as the group's managed configuration still has content. When the managed configuration is empty after the removal, or does not exist, the removal deletes it and removes the group's line from your SSH configuration, so the line goes together with the group's last host entry. The `Include` lines and the host state of other controller groups are neither read nor changed, and the rest of your SSH configuration, such as your own host entries and comments, is preserved. When no other controller group has an SSH setup, your SSH configuration then has the content it had before the first installation.

When the sandbox has no SSH setup, neither `ssh-config NAME --remove` nor `remove NAME` changes your SSH configuration, also when other sandboxes have an SSH setup.

### Remove with `remove`

`remove NAME`, with and without `--volumes`, also removes the host side of the sandbox's SSH setup (ADR-0002), in addition to what it does to the container and the volumes ([Remove a sandbox](sandboxes.md#remove-a-sandbox)). It deletes the same files and lines as `ssh-config NAME --remove`. It does not ask the manager to remove the authorization, also when the sandbox runs.

- **Volumes kept.** Without `--volumes`, the SSH server state volume is kept, and with it the authorization. The output of `remove` says that the authorization remains in the volume and is replaced by the next `ssh-config NAME --install`. `up NAME` adopts the volume, and the authorization with it ([Kept volumes](sandboxes.md#kept-volumes)).
- **Only volumes remain.** When no container exists and each remaining volume has the current owner, `remove NAME` and `remove NAME --volumes` remove the host side of the SSH setup, create and start no container, and exit with status 0.
- **Foreign volume kept.** `remove NAME --volumes` that removes the container and keeps a volume with a missing or different owner ([Owners and kept volumes](sandboxes.md#owners-and-kept-volumes)) removes the host side of the SSH setup as well, because the container is gone, and still exits with status 1.
- **Refused `remove`.** When `remove` refuses, it leaves the SSH setup untouched. This includes an owner conflict when no container exists and one of the sandbox's volumes has a missing or different owner, with and without `--volumes`, and a running agent session or a manager that does not answer without `--force`.

`update` does not go through `remove` and keeps the SSH setup ([Update a sandbox](updates.md#what-update-keeps)).

### Refusals of `--remove`

`ssh-config NAME --remove` refuses, changes no file, and exits with status 1 in these cases, in the [order of checks](#order-of-checks-for-ssh-config):

- **Unknown sandbox.** Neither the container, a volume, nor the backup container of `NAME` exists in the current controller group, also when a sandbox of that name exists in another group.
- **Owner conflict.** The container, a volume, or the backup container has a missing owner label or one that names another controller group, with a container and when only volumes remain. The message names the Podman objects concerned and points to Podman. The only command that still removes the host side of the SSH setup then is `remove NAME --volumes`, and only when the sandbox's container exists with the current owner, no backup container exists, and only volumes have a missing or different owner ([Remove with `remove`](#remove-with-remove)).
- **Interrupted update.** The backup container of an interrupted update exists with the current owner. The message names `sandboxed-agents update NAME`.

### Print the host entry

`ssh-config NAME` prints the host entry on standard output, exits with status 0, and changes no file, neither in host state nor in your SSH directory. It works on a running and on a stopped sandbox and does not ask the manager.

- With the SSH setup installed, it prints exactly the entry in the managed configuration.
- Without it, it prints the entry as `--install` would write it, with the recorded port, and says on standard error that the SSH setup is not installed and that the entry works only after `sandboxed-agents ssh-config NAME --install`.
- When the `entry` file exists but the managed configuration does not contain that entry, the SSH setup is incomplete: `ssh-config NAME` exits with status 1, prints no entry, changes no file, and names `ssh-config NAME --remove` followed by `ssh-config NAME --install`. When the managed configuration cannot be read at all, for example because it was deleted, it fails the same way with the read error.

### Command line

`ssh-config` takes exactly one sandbox name, checked against the [sandbox name rules](sandboxes.md#sandbox-names), and optionally one of `--install` and `--remove`. A usage error prints a message and a usage line on standard error, calls no other program, and exits with status 1:

- Without a name, `ssh-config` reports `missing sandbox name; use sandboxed-agents ssh-config NAME [--install|--remove]`.
- `--install` and `--remove` together, or either of them twice, is reported as `conflicting or repeated options; give at most one of --install and --remove, once`.

### Order of checks for `ssh-config`

`ssh-config` runs its checks in the order described in [Development](development.md#order-of-checks) and reports only the first failure. Every refusal exits with status 1 and changes no file. A manager that does not answer during `--remove` is not a refusal: `--remove` removes the host side first ([Remove the authorization](#remove-the-authorization)).

| Step | What `ssh-config` does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error or an invalid sandbox name |
| 3. Sandbox existence | reports an unknown sandbox name, also for a sandbox of another controller group. When only volumes remain, `ssh-config NAME` and `--install` name `sandboxed-agents up NAME`, which adopts them, and `--remove` continues; an owner conflict on one of them is reported instead. |
| 4. Owner | reports an owner conflict on the container, a volume, or the backup container, names the Podman objects, and points to Podman |
| 5. Interrupted update | reports a backup container with the current owner and names `sandboxed-agents update NAME` |
| 6. Running state | `--install` only: reports a stopped sandbox and names `sandboxed-agents start NAME`; it starts nothing. `--remove` continues on a stopped sandbox and starts nothing either. |
| 7. Preconditions | `--install` only: reports a manager that does not answer; `--remove` reports nothing here. A host key that cannot be read, a host key that differs from the pin, a host entry conflict, and an existing `sandbox-HEX` path without an `entry` file are reported afterwards by the installation, before it writes anything. |

The preflight (step 2), the terminal check (step 8), and the session guard (step 9) do not apply to `ssh-config`. On Windows, the machine selection runs after step 1 and before step 3, as for `shell`. An owner conflict on a stopped sandbox is therefore reported instead of the message naming `start NAME`.

### Connect

```sh
ssh agent01
```

`ssh` connects through the host entry, whose name is the sandbox name in the controller group `default` and `NAME.GROUP` in any other group: `agent01` for the sandbox `agent01` in `default`, `agent01.live` for the sandbox `agent01` in `live` ([Host entry name](#host-entry-name)). The executable's own commands keep taking the sandbox name, `agent01`, in the current [controller group](sandboxes.md#controller-groups).

VS Code Remote SSH and other desktop UIs read the same SSH configuration, so they list the host entry and connect through it. `sandboxed-agents check NAME` attempts one connection through the host entry of a running sandbox and reports whether it succeeded; it also reports an SSH setup that remains for a sandbox without a container ([Check a sandbox](check.md#manager-and-ssh)). No live run has confirmed a connection yet ([Verification](#verification)).

#### Connect with VS Code Remote SSH

These steps lead to a VS Code window on a sandbox's workspace, whether the sandbox runs, is stopped, or does not exist yet. They follow the SSH setup above and the official guide [Remote Development using SSH](https://code.visualstudio.com/docs/remote/ssh); nobody has run them against a live sandbox yet, and the manual checklist (#64) records that check ([Verification](#verification)).

1. **Install the SSH setup.** Pick the command for the sandbox's state ([SSH setup](#ssh-setup)):

   | Sandbox | Command |
   | --- | --- |
   | running | `sandboxed-agents ssh-config agent01 --install` |
   | stopped | `sandboxed-agents start agent01 --ssh-config` |
   | new, or existing in either state | `sandboxed-agents up agent01 --ssh-config` |

   `sandboxed-agents ssh-config agent01 --install` needs a running sandbox: on a stopped sandbox it fails, names `sandboxed-agents start agent01`, and starts nothing on its own ([Install the SSH setup](#install-the-ssh-setup)). The SSH setup is opt-in: `up` and `start` without `--ssh-config` leave every file in your SSH directory untouched. `sandboxed-agents ssh-config agent01` only prints the host entry and installs nothing; the printed entry works only after the SSH setup is installed ([Print the host entry](#print-the-host-entry)).

2. **Install VS Code and its Remote - SSH extension.** VS Code also needs an OpenSSH-compatible `ssh` on your machine, which the executable needs as well ([Host prerequisites](host-prerequisites.md)).

3. **Connect to the host entry.** Open the Command Palette and run **Remote-SSH: Connect to Host...**. VS Code lists the hosts of your SSH configuration, the host entry among them; when it is not listed, enter its name. When VS Code asks for the platform of the host, choose **Linux**: the sandbox is a Linux container also on Windows. Wait while VS Code connects and sets up the remote window. The connection uses the dedicated key and the pinned host key of the host entry, so VS Code asks for no password and no host key confirmation.

4. **Open the workspace.** The remote window shows the host entry name in its status bar. Choose **File > Open Folder...** and enter `/workspace`, the sandbox's workspace ([Workspace bind](sandboxes.md#workspace-bind)).

#### Other desktop UIs

Every desktop UI that reads your SSH configuration connects to the same host entry name, with the same key, pin, and settings; no second entry is needed. A UI that shows the sandbox's host key fingerprint and asks you to confirm it needs the fingerprint from the sandbox itself:

```sh
sandboxed-agents fingerprint agent01
```

It prints the SHA256 fingerprint of each of the sandbox's three host keys, one per line, each preceded by its key type (`ssh-ed25519`, `ecdsa-sha2-nistp256`, `ssh-rsa`). Compare the line of the key type the UI negotiated; through the host entry that is `ssh-ed25519`, because the entry accepts only that key ([Host entry](#host-entry)). `fingerprint` needs a running sandbox, reads the keys through `podman exec`, needs no SSH setup, and changes nothing ([Host keys and sign-in](sandboxes.md#host-keys-and-sign-in)).

### Not in this version

- **A real SSH connection against real Podman** comes with #35.

## Verification

The behavior of `shell` is covered by offline tests against fake `podman`, `ssh`, and `ssh-keygen` programs, with the Linux host path and with a fake Windows host identity ([Development](development.md#test-seams)). They check the complete `podman exec` call, including `--tty`, `--user=1000:1000`, `--workdir=/workspace`, and on Windows `--connection`, the refusals in the order of checks, and that `shell` calls neither `ssh` nor `ssh-keygen` and changes no file in the SSH directory or in host state.

The terminal tests give `shell` a real terminal handle of the test host as standard input: a pseudo-terminal from `/dev/ptmx` on Linux, and a console input handle on Windows. They show that `shell` detects the terminal and then passes `--tty`. The fake `podman` starts no container and no terminal inside one. The tests without a terminal show that the input reaches the fake `podman exec`; its output and exit status, 0, 7, or 125, are scripted, and `shell` passes them through.

The SSH setup is covered by offline tests at the CLI boundary against the same fake programs, on Linux and with the fake Windows host identity. They check the files that `ssh-config`, `up --ssh-config`, and `start --ssh-config` change in host state and in the SSH directory, byte for byte, the Podman calls of the manager probe, the host key query, and the authorization, the `ssh -G` queries of the conflict check, the `ssh-keygen` call, and the refusals in the order of checks. The Unix permission modes are checked on Linux only. The manager's side, reading the host key and replacing `/etc/ssh/authorized_keys`, is covered by manager tests with injected process functions and files.

The removal of the SSH setup is covered by offline tests at the CLI boundary against the same fake programs, on Linux and with the fake Windows host identity. They check the files that `ssh-config --remove` and `remove` delete or keep in host state and in the SSH directory, byte for byte, also those of other sandboxes and of another controller group, the Podman call that removes the authorization and its absence on a stopped sandbox, on a sandbox of which only volumes remain, and with `remove`, the exit statuses, and the refusals. The manager's side, deleting `/etc/ssh/authorized_keys`, is covered by manager tests with injected process functions.

The Windows permissions are checked only in the Windows job of the offline suite, on the real file system of the test machine. After `ssh-config --install`, `up --ssh-config`, and `start --ssh-config` against the same fake programs, the tests read back the owner, whether inheritance is turned off, and every permission entry of each file and directory the installation creates, also after a second sandbox. They also check that an existing `.ssh` directory and an existing SSH configuration, with and without inherited permissions, keep their permissions. They show which permissions the installation sets, not that OpenSSH for Windows accepts them, and they do not run under a standard account.

No offline test starts a real container or opens an SSH connection, and nothing on this page has been confirmed against Podman on a live host: not that the shell runs as `agent` in `/workspace`, not how it behaves with a pseudo-terminal inside the sandbox, not that it ends when its input ends, not the target binding on Windows, not that `ssh` connects through the host entry with only the dedicated key and the pinned host key, not that `ssh-config --remove` removes the authorization from a real sandbox, not that OpenSSH for Windows accepts the files of the SSH setup with their permissions, and not that the installation sets these permissions under a standard account without administrator rights. That evidence needs the live suite (#24) and the live SSH tests (#35). The VS Code walkthrough in [Connect](#connect-with-vs-code-remote-ssh) has not been followed against a live sandbox either; one VS Code Remote SSH connection is an item of the manual checklist (#64).
