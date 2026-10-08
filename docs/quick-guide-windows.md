# Quick guide: Windows 11

This guide takes you from a Windows 11 x64 host without `sandboxed-agents` to an agent that is signed in and running in a persistent agent session inside a sandbox. It contains every step you need; the links under [Further reading](#further-reading) are optional.

The guide uses these names throughout:

- the sandbox `agent01`;
- the controller group `default`, which is the group when the environment variable `SANDBOXED_AGENTS_GROUP` is not set. Leave it unset while you follow this guide;
- the agent `codex` (Codex CLI) with its login workflow `chatgpt`, which signs in with a ChatGPT account.

Run the commands in PowerShell, in a Windows Terminal tab or a PowerShell window, as your own Windows user. No step of this guide needs administrator rights; installing the prerequisites may.

## Before you start

### Host prerequisites

`sandboxed-agents` runs each sandbox as a rootless Podman container inside a Podman machine on WSL2. It needs the following on the host. The executable never installs or configures any of them, and it never starts the Podman machine; it only checks them ([step 2](#2-check-the-host)).

| Prerequisite | Requirement |
| --- | --- |
| Operating system | Windows 11 x64 workstation, build 22000 or later. ARM64 is not supported, also not with x64 emulation. |
| Podman client | Podman 5.0.0 or later, with `podman` on `PATH`. |
| Podman machine | A Podman machine on WSL2, rootless, running, with Podman 5.0.0 or later. It must be the default machine, or the only machine. |
| cgroups | cgroups v2 in the machine, with the `cpu`, `memory`, and `pids` controllers delegated to the machine's user. |
| OpenSSH | `ssh`, `ssh-keygen`, and `ssh-keyscan` from the Windows OpenSSH Client on `PATH`. |

The Podman client and the machine both need 5.0.0 or later. When the machine is stopped, start it yourself; `sandboxed-agents` never does:

```powershell
podman machine start
```

### Tools for the installation

- **GitHub CLI** (`gh`) 2.49.0 or newer, which has the `gh attestation` command, signed in to GitHub. `gh` downloads the release files and verifies their attestations. Check that it is signed in:

  ```powershell
  gh auth status
  ```

  If it is not, run `gh auth login` and follow its prompts.

- **Node.js with npm**, only if you install from the npm package ([alternative](#alternative-install-the-npm-package)).
- **PowerShell 7** (`pwsh`), only if you install from the NuGet package ([alternative](#alternative-install-the-nuget-package)).

### Network and account

The first sandbox downloads its image contents, and enabling an agent downloads it from the npm registry, so the host needs network access. Signing in with the `chatgpt` workflow needs a ChatGPT account that can use Codex and a web browser on the host.

## 1. Install the command

Install the downloaded binary as shown here, the npm package, or the NuGet package. Use one of the three, not several.

Run the commands of this step in one PowerShell window, because they share the variables `$TAG` and `$VERSION`.

### Choose a release

Stable releases do not exist yet. Releases are published as GitHub prereleases with tags of the form `vX.Y.Z-preview.YYYYMMDD.N`. List them:

```powershell
gh release list -R grauzone-dev/sandboxed-agents
```

Pick the newest prerelease and set `$TAG` to its tag. Replace the value below with the tag you picked. `$VERSION` is the tag without the `v`, which the package file names use:

```powershell
$TAG = 'v1.0.0-preview.YYYYMMDD.N'
$VERSION = $TAG.Substring(1)
```

Every release file is attested: the release workflow creates SLSA build provenance, signed through GitHub Actions for `grauzone-dev/sandboxed-agents`, for both binaries, `SHA256SUMS`, the npm package `.tgz`, and the NuGet package `.nupkg`. Previews published before attestations were added have none and fail the verification below; pick a newer one.

The npm and NuGet registries still hold the prototype, version 0.2.0, under the names `sandboxed-agents` and `SandboxedAgents`, and previews are not published there. Do not install from a registry; install the release files of your chosen tag.

### Download the binary and `SHA256SUMS`

Create a directory for the download and change into it:

```powershell
New-Item -ItemType Directory -Force -Path "$HOME\sandboxed-agents-$TAG" | Out-Null
Set-Location "$HOME\sandboxed-agents-$TAG"
```

Download the Windows binary and the checksum file:

```powershell
gh release download $TAG -R grauzone-dev/sandboxed-agents -p sandboxed-agents-windows-amd64.exe -p SHA256SUMS
```

### Compare the checksum

`SHA256SUMS` lists both binaries, `sandboxed-agents-linux-amd64` and `sandboxed-agents-windows-amd64.exe`. Compare only the line of the Windows binary:

```powershell
$entry = @(Select-String -Path SHA256SUMS -Pattern '^([0-9a-f]{64})  sandboxed-agents-windows-amd64\.exe$')
$actual = (Get-FileHash -Algorithm SHA256 -Path sandboxed-agents-windows-amd64.exe).Hash
if ($entry.Count -eq 1 -and $actual -eq $entry[0].Matches[0].Groups[1].Value) { 'sandboxed-agents-windows-amd64.exe: OK' } else { throw 'Checksum mismatch: do not use this file.' }
```

The last command prints `sandboxed-agents-windows-amd64.exe: OK`. An error means the file is damaged or incomplete: delete the directory and download again. Do not use the file.

### Verify the attestations

Verify that this repository's release workflow built both files:

```powershell
gh attestation verify sandboxed-agents-windows-amd64.exe -R grauzone-dev/sandboxed-agents
gh attestation verify SHA256SUMS -R grauzone-dev/sandboxed-agents
```

Each command reports a successful verification. If either fails, do not install the file.

### Put the command on `PATH`

Copy the binary as `sandboxed-agents.exe` into `%LOCALAPPDATA%\Programs\sandboxed-agents`, and copy that directory's path to the clipboard:

```powershell
$dir = Join-Path $env:LOCALAPPDATA 'Programs\sandboxed-agents'
New-Item -ItemType Directory -Force -Path $dir | Out-Null
Copy-Item -Path sandboxed-agents-windows-amd64.exe -Destination (Join-Path $dir 'sandboxed-agents.exe')
Set-Clipboard -Value $dir
```

Add the directory to your user `PATH` in the Windows dialog for your account's environment variables. Open it:

```powershell
rundll32.exe 'sysdm.cpl,EditEnvironmentVariables'
```

In **User variables**, select **Path** and choose **Edit**. Choose **New**, paste the directory with Ctrl+V, and confirm both dialogs with **OK**. When **User variables** has no **Path** entry, choose **New** there instead, enter `Path` as the name and paste the directory as the value. Leave the other entries as they are.

Terminals that are already running keep their old `PATH`. Start a new terminal from the Start menu, or close and restart the terminal application, then check that the command is found. If it is still not found, sign out of Windows and sign in again, then open a new terminal:

```powershell
Get-Command sandboxed-agents
```

Confirm that the command runs:

```powershell
sandboxed-agents version
```

It prints the tag you installed, including the `v`, and the hash of the build assets embedded in the executable.

### Alternative: install the npm package

The `.tgz` release file is the npm package `sandboxed-agents`. It holds both binaries, `SHA256SUMS`, and a Node.js launcher that runs the binary for your platform. This path needs Node.js with npm in addition to `gh`.

In the window where you set `$TAG` and `$VERSION`, create the download directory and change into it as above, then download the package and verify its attestation:

```powershell
gh release download $TAG -R grauzone-dev/sandboxed-agents -p 'sandboxed-agents-*.tgz'
gh attestation verify "sandboxed-agents-$VERSION.tgz" -R grauzone-dev/sandboxed-agents
```

`SHA256SUMS` does not list the packages, so the attestation is the check for the `.tgz`. Then install that file globally. The command calls `npm.cmd`, npm's batch launcher, so PowerShell's script execution policy does not apply to npm itself:

```powershell
npm.cmd install --offline --global --no-audit --no-fund "./sandboxed-agents-$VERSION.tgz"
```

During installation, the package checks both bundled binaries against its bundled `SHA256SUMS` and fails on a mismatch or on an unsupported platform such as ARM64. Before every run, the launcher checks the binary again and refuses to start one that no longer matches. Do not pass `--ignore-scripts`, which skips the installation check.

npm places the command in its global directory. Print that directory:

```powershell
npm.cmd prefix --global
```

When this directory is not on your user `PATH` yet, add it in the dialog described in [Put the command on `PATH`](#put-the-command-on-path), entering the directory that `npm.cmd prefix --global` printed. Then start a new terminal and confirm the command with `Get-Command sandboxed-agents` and `sandboxed-agents version` as above.

npm creates two shims for the command in that directory: `sandboxed-agents.cmd` and `sandboxed-agents.ps1`. When you type `sandboxed-agents`, PowerShell may pick the `.ps1` shim, and its execution policy may refuse to run it, with a message that running scripts is disabled on this system. Microsoft documents `Restricted`, which runs no scripts, as the default on Windows client computers when no policy is set, and installing or switching PowerShell versions does not reliably change that. Do not change the policy for the machine. Call the `.cmd` shim instead, which PowerShell starts through `cmd.exe` without a policy check:

```powershell
sandboxed-agents.cmd version
```

When the `.ps1` shim is refused, write `sandboxed-agents.cmd` in place of `sandboxed-agents` in every command of this guide. The arguments stay the same. `cmd.exe` parses the command line before it reaches the launcher, which does not affect the simple arguments used here.

### Alternative: install the NuGet package

The `.nupkg` release file is the NuGet package `SandboxedAgents`, a command package with the Windows binary, `SHA256SUMS`, and an installer for PowerShell 7. It installs the command for your user without administrator rights. Adding it to a project or restoring it installs nothing. This path needs PowerShell 7 and the `tar` command that comes with Windows, in addition to `gh`.

In the window where you set `$TAG` and `$VERSION`, create the download directory and change into it as above, then download the package and verify its attestation:

```powershell
gh release download $TAG -R grauzone-dev/sandboxed-agents -p '*.nupkg'
gh attestation verify "SandboxedAgents.$VERSION.nupkg" -R grauzone-dev/sandboxed-agents
```

`SHA256SUMS` does not list the packages, so the attestation is the check for the `.nupkg`. Continue only when that verification succeeded. Extract the package:

```powershell
mkdir SandboxedAgents
tar -xf "SandboxedAgents.$VERSION.nupkg" -C SandboxedAgents
```

The installer is an unsigned PowerShell script, which the execution policy may refuse. Run it in a new PowerShell 7 process whose policy is set to `Bypass` for that process only:

```powershell
pwsh -NoProfile -ExecutionPolicy Bypass -File SandboxedAgents/tools/install-command.ps1
```

`-ExecutionPolicy Bypass` applies only to the process it starts and changes no persistent policy. A policy that your organization sets through Group Policy takes precedence and may still refuse the script; then ask your administrator, or install the downloaded binary instead. `-NoProfile` keeps your PowerShell profile out of the installer's process.

The installer checks the binary against the package's `SHA256SUMS` and stops before it changes anything on a mismatch. It installs the command as `%LOCALAPPDATA%\Programs\sandboxed-agents\sandboxed-agents.exe`, prints `Installed sandboxed-agents to` with that path, and adds the directory to your user `PATH` without changing your other entries. Running it again over an existing installation upgrades it.

Terminals that are already running keep their old `PATH`. Start a new terminal from the Start menu, or close and restart the terminal application, then confirm the command with `Get-Command sandboxed-agents` and `sandboxed-agents version` as above.

Keep the extracted `SandboxedAgents` directory: its `tools\remove-command.ps1`, run with the same `pwsh -NoProfile -ExecutionPolicy Bypass -File` invocation, removes the command and the `PATH` entry the installer added. The packages of previews up to `v1.0.0-preview.20261008.2` have older scripts whose remover also removes a `PATH` entry for the directory that was there before the installation; pick a newer preview.

Installing, upgrading, or removing the command, by any of the three methods, never calls Podman and never touches sandboxes, SSH configuration, or sandbox data.

## 2. Check the host

```powershell
sandboxed-agents check
```

`check` prints one line per prerequisite, starting with `ok:`, `missing:`, or `unknown:`. `unknown:` means the prerequisite could not be checked, for example the machine's version while the machine is stopped. The command exits with status 0 only when every required prerequisite is confirmed. Fix what is missing, start the Podman machine when it is not running, and run `check` again before you continue.

## 3. Create the sandbox

```powershell
sandboxed-agents up agent01
```

`up` runs the same preflight (the host check from step 2), then creates the sandbox `agent01` in the controller group `default` on the selected Podman machine and leaves it running. The first `up` on a host builds the base image, which downloads packages and can take a while. No command removes images that a later rebuild replaces ([Old images](../README.md#old-images)). `up` prints one line per volume it creates and ends with `Sandbox agent01 is running.`

The sandbox has its own workspace at `/workspace`, stored in a volume of the sandbox, not in a Windows directory. To put a project there, open a shell in the sandbox and clone it, for example with `git clone`; Git is installed in the sandbox:

```powershell
sandboxed-agents shell agent01
```

Leave that shell with `exit`.

## 4. Enable an agent

```powershell
sandboxed-agents agents enable agent01 codex
```

The command installs Codex CLI from the npm registry into the home volume of `agent01` and prints `Agent codex is enabled (version VERSION).` followed by `Pin: none.`. The sandbox must be running, as it is after `up`.

## 5. Sign in to the agent

Run this command in an interactive terminal: a PowerShell window or Windows Terminal tab whose standard input and standard output are the console. Do not pipe into it, redirect its output, or run it from a script whose output is captured. Without a terminal, `agents login` refuses, exits with status 1, and starts no workflow.

```powershell
sandboxed-agents agents login agent01 codex chatgpt
```

The command prints a short explanation, then runs Codex CLI's own sign-in with the OAuth device code flow. Open the URL it prints in a browser on your host, sign in with your ChatGPT account, and enter the code it shows. `agents login` exits with status 0 when the workflow ends successfully and with status 1 otherwise.

The credentials stay in the home volume of `agent01`. They survive stopping and starting the sandbox, and every agent in the sandbox can read them. The executable reads, copies, and stores no credential on the host.

Show the agent's state:

```powershell
sandboxed-agents agents status agent01 codex
```

For Codex, the line `Sign-in state:` reads `unknown`: Codex CLI declares no status output that `sandboxed-agents` can read, so `unknown` says nothing about whether you are signed in. Ask Codex CLI itself instead:

```powershell
sandboxed-agents agents run agent01 codex login status
```

`agents run` passes every argument after the agent name to Codex CLI unchanged, so this runs its `login status` subcommand once in the sandbox.

## 6. Start the agent session

Run this command in an interactive terminal as well: a PowerShell window or Windows Terminal tab whose standard input and standard output are the console. Without one, `agents session` exits with status 1 and neither starts a session nor attaches to one.

```powershell
sandboxed-agents agents session agent01 codex
```

The command starts a persistent agent session, a tmux session named `sandboxed-agents-codex` inside the sandbox, and attaches your terminal to it. Codex runs in `/workspace` as the sandbox user `agent`. You now have a signed-in agent session.

**Detach.** Press Ctrl+b, release both keys, then press d. This is tmux's default detach binding, handled inside the sandbox. Your terminal returns to the PowerShell prompt, and Codex keeps running in its session. Closing the terminal tab or window detaches in the same way.

**Reattach.** Run the same command again, from this or any other terminal:

```powershell
sandboxed-agents agents session agent01 codex
```

When a session of Codex is running, the command attaches to it and starts no second one, also while another terminal is still attached.

**End the session.** The session ends when Codex exits, for example when you quit Codex; the next `agents session` command then starts a fresh session. To end the session and Codex from outside, run:

```powershell
sandboxed-agents agents session agent01 codex --stop
```

`--stop` needs no terminal. It prints `Ended the agent session of codex.`, or says that no session was running.

While the session runs, `stop`, `restart`, and `remove` of the sandbox, and disabling or updating Codex, refuse and name the session. End the session first, or repeat such a command with `--force`, which ends the session before the change.

## 7. Optional: connect with SSH

Every sandbox runs an SSH server on a loopback port of your host. Connecting to it through `ssh` or an editor needs the SSH setup, which is opt-in: until you install it, `sandboxed-agents` leaves the files in `%USERPROFILE%\.ssh` unchanged. Install it for `agent01`:

```powershell
sandboxed-agents up agent01 --ssh-config
```

On the running sandbox, `up` creates and starts nothing; it installs the SSH setup. It creates a key dedicated to `agent01`, authorizes it in the sandbox, pins the sandbox's host key, writes a host entry into a configuration file the executable manages under `%LOCALAPPDATA%\sandboxed-agents`, and adds one `Include` line for that file at the top of `%USERPROFILE%\.ssh\config`, creating the file if needed. The files it creates are restricted to your Windows account. Connect with the Windows OpenSSH client:

```powershell
ssh agent01
```

You get a shell in the sandbox as the user `agent`, with no password and no host key prompt. Editors that read your SSH configuration, such as VS Code with Remote - SSH, list the host `agent01` too; when VS Code asks for the platform of the host, choose Linux, and open `/workspace`.

The host entry is named after the sandbox only in the controller group `default`. In any other controller group, the entry is named `NAME.GROUP`: with `SANDBOXED_AGENTS_GROUP` set to `team-a`, the sandbox `agent01` is reached with `ssh agent01.team-a`.

Before it writes anything, the installation asks OpenSSH whether the host name `agent01` is already configured, and refuses without changing a file when it is. This includes a `Host agent01` entry of your own and options you set for every host under `Host *`. Exclude the name, for example with `Host * !agent01`, or remove the entry, then repeat the command. When the installation fails, the sandbox keeps running, and the message names the command to retry.

## Everyday commands

```powershell
sandboxed-agents list
sandboxed-agents stop agent01
sandboxed-agents start agent01
```

`list` shows the sandboxes of the controller group with their state, SSH port, and enabled agents. `stop` stops the sandbox and keeps everything in it, including Codex and its credentials; `start` starts it again. Nothing starts on its own: after Windows restarts, start the Podman machine with `podman machine start` and then the sandbox with `start` before you use it. Commands on a stopped sandbox fail and name `start`.

## When something fails

- A command names its own next step; run that step.
- When a command reports that no Podman machine can be selected or that the machine is not running, run `sandboxed-agents check`, which shows the missing prerequisite. Start a stopped machine with `podman machine start`. With several machines, exactly one must be marked as the default in `podman machine list`; `sandboxed-agents` never picks one of them itself.
- When a command reports that the sandbox's manager does not answer, inspect the sandbox, then restart it:

  ```powershell
  sandboxed-agents check agent01
  sandboxed-agents restart agent01
  ```

## What has been verified

Every line of this guide that starts with `sandboxed-agents` is run by an offline test against the executable, with fake Podman and fake SSH programs; the test fails when the executable rejects a line as a usage error. This shows only that the executable accepts each command as written. The test creates no sandbox, starts no container or Podman machine, opens no SSH connection, signs in nowhere, downloads nothing, and verifies no attestation. This guide has not yet been followed on a clean machine; that run is recorded by the manual checklist (#64). The Podman 5.0.0 floor was confirmed from versioned upstream documentation and source, not on a live host, and no command has yet run against a live Windows host or Podman machine.

## Further reading

These pages give details beyond this guide. You do not need them to follow it.

- [Host prerequisites](host-prerequisites.md)
- [Releases](releases.md)
- [Sandboxes](sandboxes.md)
- [Agents](agent-management.md)
- [Shell and SSH access](ssh.md)
