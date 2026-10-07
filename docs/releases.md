# Releases

Pushing a tag `vX.Y.Z-preview.YYYYMMDD.N` publishes a GitHub prerelease. X, Y, Z, and N are numbers without leading zeros; the date is any eight digits, not checked as a calendar date. Other tags publish nothing. Stable releases, publication to the npm registry, and the NuGet package come with later Stories.

A prerelease holds `sandboxed-agents-linux-amd64`, `sandboxed-agents-windows-amd64.exe`, `SHA256SUMS`, and the npm package `sandboxed-agents-X.Y.Z-preview.YYYYMMDD.N.tgz`, whose file name is the tag without the `v` ([npm package](#npm-package)). `SHA256SUMS` covers the two binaries. `version` prints the full tag, including the `v`, and the embedded build assets hash, which both binaries share.

Every release file is attested: the workflow creates SLSA build provenance, signed through GitHub Actions for this repository, for each file of a prerelease. This holds for previews published since attestations were added; earlier previews such as `v1.0.0-preview.20261003.1` have none, so only their checksums can be verified. The previews published so far hold only the two binaries and `SHA256SUMS`, and attestation has been observed on a published preview only for those three files. The `.tgz` is attested by the same workflow steps, but no preview with a `.tgz` has been published yet, so its attestation has not been observed.

## npm package

The `.tgz` is the npm package `sandboxed-agents`, and its metadata names the repository `grauzone-dev/sandboxed-agents`. Its version is the tag without the `v`. It holds both binaries, `SHA256SUMS`, and the Node.js launcher `launcher.cjs`, all at the package root. The installed command is `sandboxed-agents`.

Previews are not published to the npm registry. There, `sandboxed-agents` stays at 0.2.0, the prototype, until the stable release (#67), so `npm install sandboxed-agents` from the registry installs the prototype and not a preview. To install a preview, download its `.tgz`, verify it ([Verify a download](#verify-a-download)), and install that file, for example:

```sh
npm install --offline --global --prefix PREFIX --cache PREFIX/cache --no-audit --no-fund ./sandboxed-agents-X.Y.Z-preview.YYYYMMDD.N.tgz
```

`--cache PREFIX/cache` keeps npm's cache under the chosen prefix, `--no-audit` skips npm's audit request to the registry, and `--no-fund` its funding message.

The package supports Linux x64 and Windows x64. On any other platform, ARM64 included, installation fails with a message that names the current platform and the two supported ones. The package's install script also checks both bundled binaries against the bundled `SHA256SUMS`, and installation fails when either does not match.

These install-time checks run only when npm runs install scripts, which it does by default. With `--ignore-scripts`, npm installs the package without them; the launcher's check before every run still applies.

The launcher checks the binary for the current platform on every run, before it starts it. When the binary no longer matches `SHA256SUMS`, for example because it was changed after installation, the launcher reports the checksum mismatch, does not start the binary, and exits with a non-zero status. Otherwise it starts the binary with the arguments it received, passes standard input, standard output, and standard error through, and exits with the binary's exit status. On Windows, the `.cmd` and `.ps1` shims are run by `cmd.exe` and PowerShell, which parse the command line first, so quoting and special characters follow that shell's rules before the arguments reach the launcher.

Installing, upgrading, and removing the package never call Podman and never touch sandboxes, SSH configuration, sandbox data, or host state. The package's install script only reads the bundled binaries and verifies them against `SHA256SUMS`; it changes nothing on the host. npm itself manages the package's files in the chosen prefix and its own data in the chosen cache.

The launcher, its `.cmd` and `.ps1` shims, and the launch link that npm creates for the command are protected host paths. `up NAME WORKSPACE` refuses a workspace that contains one of them or lies inside one, also through a symlink, junction, or other alias, before it calls Podman ([Workspace guards](sandboxes.md#workspace-guards)).

## Build locally

From the repository root:

```sh
go run ./tools/release -tag v1.0.0-preview.20261003.1 -output .scratch/preview
```

The output directory must be absent or empty. `-check-tag` only validates the tag. Otherwise the tool builds both binaries twice through `tools/build`, packs the npm package from each build, and writes the four files only if both builds are byte-identical, including `SHA256SUMS` and the `.tgz`. The package is packed deterministically, so the same commit and tag yield the same `.tgz`.

## Verify a download

```sh
gh release download v1.0.0-preview.20261003.1 -R grauzone-dev/sandboxed-agents
sha256sum -c SHA256SUMS
```

`SHA256SUMS` does not list the `.tgz`. Verify it, like every release file of an attested preview, with `gh attestation verify FILE -R grauzone-dev/sandboxed-agents`.

## Validate a preview

A preview is validated on its commit by three gates: the offline suite, the [live suite](live-suite.md) on Linux and on Windows 11, and the manual checklist (#64). The maintainer uploads the two live-suite summaries and the manual-checklist record to the preview's prerelease. [Live suite](live-suite.md#validation-records) records their file names and format, and the redaction of the summaries, and describes the upload. A live run validates a preview only when its summary reports a pass, says that the image part ran, and says that the full image coverage is complete. In this version the live suite is only the harness and always records the image coverage as incomplete, so no run validates a release yet. The stable release check that reads the records comes with #67.

## What is verified where

On Linux or Windows amd64, the release tool runs the native binary's `version`; it checks the cross-compiled binary only for the embedded build assets. The workflow runs the release tool and the offline suite on Linux and on Windows, so each binary's `version` runs natively, and publishes only if both runners produced identical release files and `version` output.

The offline suite covers the launcher, its platform check, its checksum checks at installation and on every run, and the forwarding of arguments, standard streams, and exit status, as well as the workspace guards for the launcher paths. It runs without network, Podman, or credentials.

Attestations are created and checked only in the workflow, because signing and verification depend on GitHub; the release tool and the offline suite do neither. Before creating the release, the workflow verifies each release file against the attestation bundle it just produced. After publishing, it downloads the release files, verifies each against the attestations stored for the repository, and checks that a modified copy of each file fails verification.
