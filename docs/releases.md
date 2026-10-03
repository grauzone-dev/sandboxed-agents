# Releases

Pushing a tag `vX.Y.Z-preview.YYYYMMDD.N` publishes a GitHub prerelease. X, Y, Z, and N are numbers without leading zeros; the date is any eight digits, not checked as a calendar date. Other tags publish nothing. Stable releases and npm and NuGet packages come with later Stories.

A prerelease holds `sandboxed-agents-linux-amd64`, `sandboxed-agents-windows-amd64.exe`, and `SHA256SUMS`. `version` prints the full tag, including the `v`, and the embedded build assets hash, which both binaries share.

Every release file is attested: each of the three files carries SLSA build provenance signed through GitHub Actions for this repository. This holds for previews published since attestations were added; earlier previews such as `v1.0.0-preview.20261003.1` have none, so only their checksums can be verified.

## Build locally

From the repository root:

```sh
go run ./tools/release -tag v1.0.0-preview.20261003.1 -output .scratch/preview
```

The output directory must be absent or empty. `-check-tag` only validates the tag. Otherwise the tool builds both binaries twice through `tools/build` and writes the three files only if both builds are byte-identical, including `SHA256SUMS`.

## Verify a download

```sh
gh release download v1.0.0-preview.20261003.1 -R grauzone-dev/sandboxed-agents
sha256sum -c SHA256SUMS
```

## What is verified where

On Linux or Windows amd64, the release tool runs the native binary's `version`; it checks the cross-compiled binary only for the embedded build assets. The workflow runs the release tool and the offline suite on Linux and on Windows, so each binary's `version` runs natively, and publishes only if both runners produced identical release files and `version` output.

Attestations are created and checked only in the workflow, because signing and verification depend on GitHub; the release tool and the offline suite do neither. Before creating the release, the workflow verifies each release file against the attestation bundle it just produced. After publishing, it downloads the release files, verifies each against the attestations stored for the repository, and checks that a modified copy of each file fails verification.
