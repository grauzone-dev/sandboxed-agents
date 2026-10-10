# Releases

Pushing a tag `vX.Y.Z-preview.YYYYMMDD.N` publishes a GitHub prerelease. X, Y, Z, and N are numbers without leading zeros; the date is any eight digits, not checked as a calendar date. Other tags publish nothing. Stable releases and publication to the npm and NuGet registries come with later Stories.

A prerelease holds five files: `sandboxed-agents-linux-amd64`, `sandboxed-agents-windows-amd64.exe`, `SHA256SUMS`, the npm package `sandboxed-agents-X.Y.Z-preview.YYYYMMDD.N.tgz` ([npm package](#npm-package)), and the NuGet package `SandboxedAgents.X.Y.Z-preview.YYYYMMDD.N.nupkg` ([Install from the NuGet package](#install-from-the-nuget-package)). Both package file names carry the tag without the `v`. `SHA256SUMS` covers the two binaries. `version` prints the full tag, including the `v`, and the embedded build assets hash, which both binaries share.

Every release file is attested: the workflow creates SLSA build provenance, signed through GitHub Actions for this repository, for each file of a prerelease. This holds for previews published since attestations were added; earlier previews such as `v1.0.0-preview.20261003.1` have none, so only their checksums can be verified. A preview holds only the files its commit's release tool produced: earlier previews lack one or both packages, and `v1.0.0-preview.20261007.3`, for example, holds the NuGet package but no npm package.

## npm package

The `.tgz` is the npm package `sandboxed-agents`, and its metadata names the repository `grauzone-dev/sandboxed-agents`. Its version is the tag without the `v`. It holds both binaries, `SHA256SUMS`, and the Node.js launcher `launcher.cjs`, all at the package root. The installed command is `sandboxed-agents`.

Previews are not published to the npm registry. There, `sandboxed-agents` stays at 0.2.0, the prototype, until the stable release (#67), so `npm install sandboxed-agents` from the registry installs the prototype and not a preview. To install a preview, download its `.tgz`, verify it ([Verify a download](#verify-a-download)), and install that file, for example:

```sh
npm install --offline --global --prefix PREFIX --cache PREFIX/cache --no-audit --no-fund ./sandboxed-agents-X.Y.Z-preview.YYYYMMDD.N.tgz
```

`--cache PREFIX/cache` keeps npm's cache under the chosen prefix, `--no-audit` skips npm's audit request to the registry, and `--no-fund` its funding message.

The package supports Linux x64 and Windows x64. On any other platform, ARM64 included, installation fails with a message that names the current platform and the two supported ones. The package's install script also checks both bundled binaries against the bundled `SHA256SUMS`, and installation fails when either does not match.

These install-time checks run only when npm runs install scripts, which it does by default. With `--ignore-scripts`, npm installs the package without them; the launcher's check before every run still applies.

The launcher checks the binary for the current platform on every run, before it starts it. When the binary no longer matches `SHA256SUMS`, for example because it was changed after installation, the launcher reports the checksum mismatch, does not start the binary, and exits with a non-zero status. Otherwise it starts the binary with the arguments it received, passes standard input, standard output, and standard error through, and exits with the binary's exit status. When the binary is ended by a signal, the launcher sends the same signal to itself. If Node.js handles or ignores that signal and the launcher keeps running, it exits with 128 plus the signal's number, or with 1 when Node.js does not know the signal's number. On Windows, the `.cmd` and `.ps1` shims are run by `cmd.exe` and PowerShell, which parse the command line first, so quoting and special characters follow that shell's rules before the arguments reach the launcher.

Installing, upgrading, and removing the package never call Podman and never touch sandboxes, SSH configuration, sandbox data, or host state. The package's install script only reads the bundled binaries and verifies them against `SHA256SUMS`; it changes nothing on the host. npm itself manages the package's files in the chosen prefix and its own data in the chosen cache.

The launcher, its `.cmd` and `.ps1` shims, and the launch link that npm creates for the command are protected host paths. `up NAME WORKSPACE` refuses a workspace that contains one of them or lies inside one, also through a symlink, junction, or other alias, before it calls Podman ([Workspace guards](sandboxes.md#workspace-guards)).

## Build locally

From the repository root:

```sh
go run ./tools/release -tag v1.0.0-preview.20261003.1 -output .scratch/preview
```

The output directory must be absent or empty. `-check-tag` only validates the tag. Otherwise the tool builds both binaries twice through `tools/build`, packs the npm package and the NuGet package from each build, and writes the five files only if both builds are byte-identical, including `SHA256SUMS` and both packages. Both packages are packed deterministically, the NuGet package with a fixed entry order and fixed timestamps, so the same commit and tag yield the same packages.

## Verify a download

```sh
gh release download v1.0.0-preview.20261003.1 -R grauzone-dev/sandboxed-agents
sha256sum -c SHA256SUMS
```

`SHA256SUMS` lists only the two binaries, not the `.tgz` or the `.nupkg`. Verify the packages, like every release file of an attested preview, with `gh attestation verify FILE -R grauzone-dev/sandboxed-agents`.

## Install from the NuGet package

The NuGet package installs the command on Windows for the current user, with PowerShell 7 and without administrator rights. It is a command package, not a library: it has no NuGet install scripts and no library assets, so adding it to a project or restoring it installs nothing. Previews are not published to the NuGet registry, where `SandboxedAgents` stays at the prototype's version 0.2.0 until the stable release (#67).

Download the package from a prerelease, extract it, and run its installer. Replace `TAG` with the prerelease's tag and `VERSION` with that tag without the `v`:

```powershell
gh release download TAG -R grauzone-dev/sandboxed-agents -p '*.nupkg'
mkdir SandboxedAgents
tar -xf SandboxedAgents.VERSION.nupkg -C SandboxedAgents
pwsh -File SandboxedAgents/tools/install-command.ps1
```

This page describes the scripts of the current source. The packages of previews up to `v1.0.0-preview.20261008.2` have no ownership marker and send no change notification: their remover removes every entry that names the install directory, including one that was there before the installation.

The installer checks `tools/sandboxed-agents-windows-amd64.exe` against `tools/SHA256SUMS` and stops before it changes anything if `SHA256SUMS` has no single entry for it or the binary does not match. It copies the binary to a temporary file in `%LOCALAPPDATA%\Programs\sandboxed-agents\`, checks that copy, and only then puts it in place as `sandboxed-agents.exe`, so a checksum failure or a failed copy leaves an existing command as it was. Finally it adds the directory to the user `PATH` ([User PATH](#user-path)). Running the installer over an existing installation upgrades it. Because `SHA256SUMS` comes in the same package, the check catches a damaged binary, not a replaced package; the package's attestation shows that this repository's workflow built it.

The installer copies only the binary and, when it adds a `PATH` entry, its ownership marker; keep the extracted package to remove the command later. `tools/remove-command.ps1` removes `sandboxed-agents.exe` and the `PATH` entry the installer added. Other files and the directory itself stay, even when it is empty. Removing a command that is not installed succeeds. Both scripts accept two options; pass the removal the same ones as the installation:

- `-InstallDirectory DIR` uses `DIR` instead of the default directory.
- `-NoPathUpdate` leaves the user `PATH` untouched. Without it, the scripts refuse a directory that contains `;`, which separates `PATH` entries.

### User PATH

The scripts change only the `Path` value under `HKEY_CURRENT_USER\Environment`. They read it without expanding it and write it back with its registry type; when the value does not exist, the installer creates it as a string. A value that is neither a string nor an expandable string stops either script before it changes the command or the value.

The installer adds the install directory only when no entry already names it. An entry names the directory when it is a fully qualified path to it, ignoring case, surrounding quotes, and a trailing `\`. For that comparison the scripts expand environment variables in an expandable string and take a plain string literally, so `%LOCALAPPDATA%\Programs\sandboxed-agents` names the default directory only in an expandable string. Relative entries such as `.`, `bin`, or `C:bin` never count, whatever directory the script runs in.

When the installer appends the entry, it records it in `.sandboxed-agents-path.json` in the install directory: a JSON object whose only field, `entry`, holds the appended text, the directory's full path. An entry that was already there is not recorded, so it stays the user's. The marker survives reinstalls, upgrades, and runs with `-NoPathUpdate`. Without `-NoPathUpdate`, the remover removes one entry whose text equals the recorded one exactly, the last one, and then deletes the marker; it does so even when the command is already gone. Copies or other spellings of the entry that the user added stay. Without a marker, the remover leaves the user `PATH` unchanged. A removal with `-NoPathUpdate` deletes the command but keeps the marker and the entry for a later removal without it. A marker that is not in this form stops either script, also with `-NoPathUpdate`, before it changes the command or the user `PATH`; the installer checks it after the checksum. Every other entry, including an unexpanded one such as `%USERPROFILE%\bin`, stays byte for byte as it was, and no entry's text is expanded or rewritten.

After each change to the value, the scripts broadcast `WM_SETTINGCHANGE` with `Environment` to all top-level windows, waiting at most one second for each window and not waiting for windows that appear hung. A window that does not answer in time does not undo the change. Each program decides whether to reload its environment on that message, and a process started by a program that has not reloaded inherits the old `PATH`. Terminals that are already running keep their old `PATH`: start a new terminal from the Start menu, or restart the terminal application, to use the command.

Neither script calls Podman, needs network access, or touches sandboxes, SSH configuration, or host state: they change only the install directory and the user `PATH`.

## Validate a preview

A preview is validated on its commit by three gates: the offline suite, the [live suite](live-suite.md) on Linux and on Windows 11, and the manual checklist (#64). The maintainer uploads the two live-suite summaries and the manual-checklist record to the preview's prerelease. [Live suite](live-suite.md#validation-records) records their file names and format, and the redaction of the summaries, and describes the upload. A live run validates a preview only when its summary reports a pass, says that the image part ran, and says that the full image coverage is complete. In this version the live suite has the harness, the image part (#29), the lifecycle part (#24), the SSH part (#35), and the update part (#55). Live records exist for the lifecycle part, the SSH part, and the update part; no live run of the image part is recorded, and the live coverage of agent sessions (#68) is still to come, so no run validates a release yet. A validation run selects every part: `go run ./tools/live -opt-in -images -lifecycle -ssh -updates`. The stable release check that reads the records comes with #67.

## What is verified where

On Linux or Windows amd64, the release tool runs the native binary's `version`; it checks the cross-compiled binary only for the embedded build assets. The workflow runs the release tool and the offline suite on Linux and on Windows, so each binary's `version` runs natively, and publishes only if both runners produced identical release files, including both packages, and `version` output.

The offline suite covers the launcher, its platform check, its checksum checks at installation and on every run, and the forwarding of arguments, standard streams, and exit status, on Linux also for a binary ended by a signal, as well as the workspace guards for the launcher paths. It runs without network, Podman, or credentials.

Attestations are created and checked only in the workflow, because signing and verification depend on GitHub; the release tool and the offline suite do neither. Before creating the release, the workflow verifies each release file against the attestation bundle it just produced. After publishing, it downloads the release files, verifies each against the attestations stored for the repository, and checks that a modified copy of each file fails verification.

The offline suite runs both installer scripts ([NuGet installer tests](development.md#nuget-installer-tests)). Only its native Windows job changes the user `PATH` in the registry, receives the change notification, and runs the scripts without administrator rights. On other operating systems the tests cover runs with `-NoPathUpdate`, the `;` refusal, and, with the registry access replaced by a file, a removal that keeps an entry that was there before the installation. A package's attestation exists only for a package that the workflow built and published for a pushed tag, so it can be checked only on such a prerelease. Installing from either package on a clean machine is part of the manual checklist (#64).
