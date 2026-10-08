# Quick guide: Linux

This guide takes you from a Linux host without `sandboxed-agents` to an agent that is signed in and running in a persistent agent session inside a sandbox. It contains every step you need; the links under [Further reading](#further-reading) are optional.

The guide uses these names throughout:

- the sandbox `agent01`;
- the controller group `default`, which is the group when the environment variable `SANDBOXED_AGENTS_GROUP` is not set. Leave it unset while you follow this guide;
- the agent `codex` (Codex CLI) with its login workflow `chatgpt`, which signs in with a ChatGPT account.

Run every command as your own user, never as root and never with `sudo`.

## Before you start

### Host prerequisites

`sandboxed-agents` needs the following on the host. The executable never installs or configures any of them, and it never runs `sudo`; it only checks them ([step 2](#2-check-the-host)). Install or configure what is missing with your distribution's tools.

| Prerequisite | Requirement |
| --- | --- |
| Architecture | Linux on amd64 (x86_64). ARM64 is not supported. |
| Podman | Rootless Podman 4.4.0 or newer, with `podman` on `PATH`. Pre-releases of 4.4.0, such as `4.4.0-rc1`, do not count. |
| pasta | `pasta` (the `passt` package) on `PATH`. |
| ID mapping | `newuidmap` and `newgidmap` (the `uidmap` or `shadow-utils` package) on `PATH`. |
| Subordinate IDs | A line for your user in `/etc/subuid` and in `/etc/subgid`. An administrator adds them with `usermod --add-subuids FIRST-LAST USER` and `usermod --add-subgids FIRST-LAST USER`. |
| cgroups | The unified cgroup v2 hierarchy, with the `cpu`, `memory`, and `pids` controllers delegated to your user. On systemd hosts, the usual fix is a drop-in `/etc/systemd/system/user@.service.d/delegate.conf` containing `[Service]` and `Delegate=memory pids cpu cpuset`, followed by logging out and back in. |
| OpenSSH | The OpenSSH client: `ssh`, `ssh-keygen`, and `ssh-keyscan` on `PATH`. |

The 4.4.0 floor comes from the `pasta` network option that every sandbox uses, which Podman 4.4.0 introduced.

### Tools for the installation

- **GitHub CLI** (`gh`) 2.49.0 or newer, which has the `gh attestation` command, signed in to GitHub. `gh` downloads the release files and verifies their attestations. Check that it is signed in:

  ```sh
  gh auth status
  ```

  If it is not, run `gh auth login` and follow its prompts.

- **Node.js with npm**, only if you install from the npm package ([alternative](#alternative-install-the-npm-package)) instead of the downloaded binary.

### Network and account

The first sandbox downloads its image contents, and enabling an agent downloads it from the npm registry, so the host needs network access. Signing in with the `chatgpt` workflow needs a ChatGPT account that can use Codex and a web browser on the host.

## 1. Install the command

Install the downloaded binary as shown here, or the npm package as shown in [the alternative](#alternative-install-the-npm-package). Use one of the two, not both.

Run the commands of this step in one terminal, because they share the variable `TAG`.

### Choose a release

Stable releases do not exist yet. Releases are published as GitHub prereleases with tags of the form `vX.Y.Z-preview.YYYYMMDD.N`. List them:

```sh
gh release list -R grauzone-dev/sandboxed-agents
```

Pick the newest prerelease and set `TAG` to its tag. Replace the value below with the tag you picked:

```sh
TAG=v1.0.0-preview.YYYYMMDD.N
```

Every release file is attested: the release workflow creates SLSA build provenance, signed through GitHub Actions for `grauzone-dev/sandboxed-agents`, for both binaries, `SHA256SUMS`, the npm package `.tgz`, and the NuGet package `.nupkg`. Previews published before attestations were added have none and fail the verification below; pick a newer one.

The npm registry still holds the prototype, version 0.2.0, under the name `sandboxed-agents`, and previews are not published there. Do not install from the registry; install the release files of your chosen tag.

### Download the binary and `SHA256SUMS`

Create a directory for the download and change into it:

```sh
mkdir -p "$HOME/sandboxed-agents-$TAG"
cd "$HOME/sandboxed-agents-$TAG"
```

Download the Linux binary and the checksum file:

```sh
gh release download "$TAG" -R grauzone-dev/sandboxed-agents -p sandboxed-agents-linux-amd64 -p SHA256SUMS
```

### Compare the checksum

`SHA256SUMS` lists both binaries, `sandboxed-agents-linux-amd64` and `sandboxed-agents-windows-amd64.exe`. Check only the line of the Linux binary:

```sh
grep -E '^[0-9a-f]{64}  sandboxed-agents-linux-amd64$' SHA256SUMS | sha256sum -c -
```

The command prints `sandboxed-agents-linux-amd64: OK` and exits with status 0. Any other result means the file is damaged or incomplete: delete the directory and download again. Do not use the file.

### Verify the attestations

Verify that this repository's release workflow built both files:

```sh
gh attestation verify sandboxed-agents-linux-amd64 -R grauzone-dev/sandboxed-agents
gh attestation verify SHA256SUMS -R grauzone-dev/sandboxed-agents
```

Each command reports a successful verification and exits with status 0. If either fails, do not install the file.

### Put the command on `PATH`

Install the binary as `sandboxed-agents` in `~/.local/bin`:

```sh
install -D -m 0755 sandboxed-agents-linux-amd64 "$HOME/.local/bin/sandboxed-agents"
```

Open a new terminal, so that its shell reads your startup files, and check that the command is found:

```sh
command -v sandboxed-agents
```

It prints the path of the installed command. When it prints nothing, `~/.local/bin` is not on your `PATH`. For bash, add it in `~/.bashrc` (for zsh, use `~/.zshrc` instead):

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
```

Then open another new terminal and run `command -v sandboxed-agents` again. Terminals that were already open keep their old `PATH`.

Confirm that the command runs:

```sh
sandboxed-agents version
```

It prints the tag you installed, including the `v`, and the hash of the build assets embedded in the executable.

### Alternative: install the npm package

The `.tgz` release file is the npm package `sandboxed-agents`. It holds both binaries, `SHA256SUMS`, and a Node.js launcher that runs the binary for your platform. This path needs Node.js with npm in addition to `gh`.

In the terminal where you set `TAG`, create the download directory and change into it as above, then download the package and verify its attestation:

```sh
gh release download "$TAG" -R grauzone-dev/sandboxed-agents -p 'sandboxed-agents-*.tgz'
gh attestation verify "sandboxed-agents-${TAG#v}.tgz" -R grauzone-dev/sandboxed-agents
```

`SHA256SUMS` does not list the packages, so the attestation is the check for the `.tgz`. Then install that file into `~/.local`:

```sh
npm install --offline --global --prefix "$HOME/.local" --cache "$HOME/.local/cache" --no-audit --no-fund "./sandboxed-agents-${TAG#v}.tgz"
```

During installation, the package checks both bundled binaries against its bundled `SHA256SUMS` and fails on a mismatch or on an unsupported platform such as ARM64. Before every run, the launcher checks the binary again and refuses to start one that no longer matches. Do not pass `--ignore-scripts`, which skips the installation check.

npm places the command in `~/.local/bin`. Make sure that directory is on your `PATH` and confirm the command as described in [Put the command on `PATH`](#put-the-command-on-path), from opening a new terminal on.

Installing, upgrading, or removing the command, by either method, never calls Podman and never touches sandboxes, SSH configuration, or sandbox data.

## 2. Check the host

```sh
sandboxed-agents check
```

`check` prints one line per prerequisite, starting with `OK:` or `MISSING:`. A `MISSING:` line names what to install or configure. The command exits with status 0 only when every prerequisite is met. Fix what is missing and run `check` again before you continue.

## 3. Create the sandbox

```sh
sandboxed-agents up agent01
```

`up` runs the same host check, then creates the sandbox `agent01` in the controller group `default` and leaves it running. The first `up` on a host builds the base image, which downloads packages and can take a while. `up` prints one line per volume it creates and ends with `Sandbox agent01 is running.`

The sandbox has its own workspace at `/workspace`, stored in a volume of the sandbox, not in a host directory. To put a project there, open a shell in the sandbox and clone it, for example with `git clone`; Git is installed in the sandbox:

```sh
sandboxed-agents shell agent01
```

Leave that shell with `exit`.

## 4. Enable an agent

```sh
sandboxed-agents agents enable agent01 codex
```

The command installs Codex CLI from the npm registry into the home volume of `agent01` and prints `Agent codex is enabled (version VERSION).` followed by `Pin: none.`. The sandbox must be running, as it is after `up`.

## 5. Sign in to the agent

Run this command in an interactive terminal: standard input and standard output must both be connected to the terminal, so do not pipe into it or redirect its output. Without a terminal, `agents login` refuses, exits with status 1, and starts no workflow.

```sh
sandboxed-agents agents login agent01 codex chatgpt
```

The command prints a short explanation, then runs Codex CLI's own sign-in with the OAuth device code flow. Open the URL it prints in a browser on your host, sign in with your ChatGPT account, and enter the code it shows. `agents login` exits with status 0 when the workflow ends successfully and with status 1 otherwise.

The credentials stay in the home volume of `agent01`. They survive stopping and starting the sandbox, and every agent in the sandbox can read them. The executable reads, copies, and stores no credential on the host.

Show the agent's state:

```sh
sandboxed-agents agents status agent01 codex
```

For Codex, the line `Sign-in state:` reads `unknown`: Codex CLI declares no status output that `sandboxed-agents` can read, so `unknown` says nothing about whether you are signed in. Ask Codex CLI itself instead:

```sh
sandboxed-agents agents run agent01 codex login status
```

`agents run` passes every argument after the agent name to Codex CLI unchanged, so this runs its `login status` subcommand once in the sandbox.

## 6. Start the agent session

Run this command in an interactive terminal as well: standard input and standard output must both be connected to the terminal. Without one, `agents session` exits with status 1 and neither starts a session nor attaches to one.

```sh
sandboxed-agents agents session agent01 codex
```

The command starts a persistent agent session, a tmux session named `sandboxed-agents-codex` inside the sandbox, and attaches your terminal to it. Codex runs in `/workspace` as the sandbox user `agent`. You now have a signed-in agent session.

**Detach.** Press Ctrl-b, release both keys, then press d. This is tmux's default detach binding. Your terminal returns to the host prompt, and Codex keeps running in its session. Closing the terminal window detaches in the same way.

**Reattach.** Run the same command again, from this or any other terminal:

```sh
sandboxed-agents agents session agent01 codex
```

When a session of Codex is running, the command attaches to it and starts no second one, also while another terminal is still attached.

**End the session.** The session ends when Codex exits, for example when you quit Codex; the next `agents session` command then starts a fresh session. To end the session and Codex from outside, run:

```sh
sandboxed-agents agents session agent01 codex --stop
```

`--stop` needs no terminal. It prints `Ended the agent session of codex.`, or says that no session was running.

While the session runs, `stop`, `restart`, and `remove` of the sandbox, and disabling or updating Codex, refuse and name the session. End the session first, or repeat such a command with `--force`, which ends the session before the change.

## 7. Optional: connect with SSH

Every sandbox runs an SSH server on a loopback port of your host. Connecting to it through `ssh` or an editor needs the SSH setup, which is opt-in: until you install it, `sandboxed-agents` leaves the files in `~/.ssh` unchanged. Install it for `agent01`:

```sh
sandboxed-agents up agent01 --ssh-config
```

On the running sandbox, `up` creates and starts nothing; it installs the SSH setup. It creates a key dedicated to `agent01`, authorizes it in the sandbox, pins the sandbox's host key, writes a host entry into a configuration file the executable manages, and adds one `Include` line for that file at the top of `~/.ssh/config`, creating the file if needed. Connect:

```sh
ssh agent01
```

You get a shell in the sandbox as the user `agent`, with no password and no host key prompt. Editors that read your SSH configuration, such as VS Code with Remote - SSH, list the host `agent01` too; open `/workspace` there.

The host entry is named after the sandbox only in the controller group `default`. In any other controller group, the entry is named `NAME.GROUP`: with `SANDBOXED_AGENTS_GROUP` set to `team-a`, the sandbox `agent01` is reached with `ssh agent01.team-a`.

Before it writes anything, the installation asks OpenSSH whether the host name `agent01` is already configured, and refuses without changing a file when it is. This includes a `Host agent01` entry of your own and options you set for every host under `Host *`. Exclude the name, for example with `Host * !agent01`, or remove the entry, then repeat the command. When the installation fails, the sandbox keeps running, and the message names the command to retry.

## Everyday commands

```sh
sandboxed-agents list
sandboxed-agents stop agent01
sandboxed-agents start agent01
```

`list` shows the sandboxes of the controller group with their state, SSH port, and enabled agents. `stop` stops the sandbox and keeps everything in it, including Codex and its credentials; `start` starts it again. Nothing starts on its own: after a host restart, run `start` before you use the sandbox. Commands on a stopped sandbox fail and name `start`.

## When something fails

- A command names its own next step; run that step.
- When a command reports that the sandbox's manager does not answer, inspect the sandbox, then restart it:

  ```sh
  sandboxed-agents check agent01
  sandboxed-agents restart agent01
  ```

- When `check` reports a `MISSING:` prerequisite, fix it with your distribution's tools. `sandboxed-agents` does not change the host.

## What has been verified

Every line of this guide that starts with `sandboxed-agents` is run by an offline test against the executable, with fake Podman and fake SSH programs; the test fails when the executable rejects a line as a usage error. This shows only that the executable accepts each command as written. The test creates no sandbox, starts no container, opens no SSH connection, signs in nowhere, downloads nothing, and verifies no attestation. This guide has not yet been followed on a clean machine; that run is recorded by the manual checklist (#64). The Podman 4.4.0 floor was confirmed from versioned upstream documentation and source, not on a live host.

## Further reading

These pages give details beyond this guide. You do not need them to follow it.

- [Host prerequisites](host-prerequisites.md)
- [Releases](releases.md)
- [Sandboxes](sandboxes.md)
- [Agents](agents.md)
- [Shell and SSH access](ssh.md)
