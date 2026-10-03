# Development

How to build and test `sandboxed-agents` from source. The module holds two commands (ADR-0001): the host executable `sandboxed-agents` in `cmd/sandboxed-agents` and the in-container manager in `cmd/sandboxed-agents-manager`. The manager is compiled for Linux and embedded in the host executable.

## Requirements

- Go 1.27 or newer.

The module uses only the standard library. Building it and running the offline test suite need no network access, no Podman, no SSH, and no credentials.

## Build

Run the build tool from the repository root:

```sh
go run ./tools/build
```

It writes the host executable to `.scratch/sandboxed-agents`, or to `.scratch/sandboxed-agents.exe` when the target is Windows. Options:

- `-output <path>` sets a different path for the host executable.
- `-goos linux` or `-goos windows` selects the target host operating system. The default is the operating system you build on.
- `-version <value>` sets the version the host executable reports. The default is `dev`. The value must be nonempty and contain no whitespace, quotes, or backslashes.

The only supported architecture is amd64. The build tool sets the Go build environment itself, including `GOOS`, `GOARCH`, `CGO_ENABLED`, `GOAMD64`, `GOFLAGS`, `GOEXPERIMENT`, `GOWORK`, `GOPROXY`, and `GOSUMDB`, so their values in your environment do not affect the build. It also sets `GOTOOLCHAIN=local`, so the `go` command on your `PATH` must itself be Go 1.27 or newer.

The build tool runs three steps in this order:

1. It compiles the manager as a static Linux amd64 executable.
2. It packages the manager and the normalized image build context into `internal/assets/bundle.zip`. The file is git-ignored.
3. It compiles the host executable, which embeds that archive.

Run the build tool again after you change the manager or the build context. A plain `go build` of the host executable without a prepared bundle is not supported.

### Embedded build assets

The image build context lives in `build/context/`: the `Containerfile` of the base image and the scripts it runs. [Images](images.md) describes what the image contains. The archive leaves out a `.gitkeep` directly in `build/context/`; a `.gitkeep` in a subdirectory is an ordinary asset and is packaged and hashed. The manager is not part of `build/context/`. `sandboxed-agents build` writes the embedded context and the manager into one temporary directory before it calls Podman.

The archive holds the Linux manager and the build context. Before packaging, the build tool converts CRLF line endings to LF in every context file that has no NUL bytes, whatever its text encoding. Files that contain a NUL byte are treated as binary and packaged unchanged. `.gitattributes` also checks text files in the context out with LF. The asset hash is the SHA-256 hash of the archive, so it covers the manager and the normalized context's path names and file contents. One commit therefore yields the same hash on Linux and on Windows, whether its context files were checked out with LF or CRLF line endings.

`sandboxed-agents version` prints the version and the asset hash. It calls neither Podman nor SSH and needs no external tools.

## Test

On a clean checkout, run the build tool before the tests, because `internal/assets` embeds `internal/assets/bundle.zip` and does not compile without it:

```sh
go run ./tools/build
go test ./...
go vet ./...
```

The mirror workflow has its own offline test:

```sh
bash tests/test-mirror-issue.sh
```

The build tool's test copies the sources into a temporary directory and builds hosts for Linux and Windows. It converts every file of the real build context to CRLF line endings and checks that the asset hash and the embedded, normalized context stay the same. It runs only the executable for the operating system it runs on and checks the other one's file format and embedded assets without running it.

### CI

The `Offline suite` workflow (`.github/workflows/offline.yml`) runs on every push and pull request, on Linux and on Windows, with module downloads turned off. Each job runs the build tool, then `go test ./...` and `go vet ./...`. The Linux job also runs the mirror test. Each job then records the output of its native `version`, and a final job checks that the Linux and Windows outputs are identical. That check confirms that both builds embed the same assets.

### Test seams

- **CLI boundary.** Tests run the public CLI as a separate process. `testutil.NewFakePrograms` places native fake `podman`, `ssh`, `ssh-keygen`, and `getent` programs first on `PATH`. A test scripts their output and exit statuses with `Script` and asserts the exact calls they recorded with `Calls`. For host preflight, see [Host preflight tests](#host-preflight-tests).
- **Image build.** Build tests run `sandboxed-agents build` from an empty working directory, with the real embedded assets, against the fake `podman`. The fake copies the build context while the build runs. The tests check that context: the recipe files have LF line endings and the manager is an executable static Linux amd64 binary. They also assert the following:
  - the exact `podman build` call, with its tag, labels, and context directory, and no Podman call besides the preflight's own calls (on Linux, only `--version`);
  - removal of the temporary context directory after a successful and after a failed build;
  - the same tag from two controller groups;
  - usage errors before the preflight, a stop after a failed preflight, and a stop when the context directory cannot be created.

  No offline test runs a real Podman build; #29 validates images against real Podman.
- **Sandbox creation.** `up` tests drive the public CLI as a subprocess that enters through `cli.RunWithHost` with the read-only host adapter, so the Linux preflight passes on fixture files. These tests run only on Linux and are skipped by the Windows CI job. The Windows `up` tests are described in [Host preflight tests](#host-preflight-tests). Two fixtures differ in the asset hash the CLI receives: `linux-preflight` passes `fixture-assets`, so the base image is `localhost/sandboxed-agents:base-fixture-assets`, and `linux-build` passes the real embedded asset hash, so a build writes the real build context. Most `up` tests use `linux-preflight`; the image-build test and the build-failure case use `linux-build`. A test scripts the fake `podman` answers for `exists` and the JSON of `inspect`, including the owner labels.

  The tests compare the complete list of Podman calls in these cases:
  - on a host without the sandbox: the preflight's `--version`, the `exists` lookups of the container, the three volumes, the backup container, and the image, the three `volume create` calls with the owner label, the `create` call with its labels, default options, limits, and exactly the three volume mounts, and `start`;
  - for an owned stopped sandbox, the lookups and `inspect` calls followed by `start`, and for a running one, the same calls without `start`;
  - for a failed preflight, only `--version`.

  Other cases assert only part of the calls:
  - the image-build test checks the image lookup, exactly one `podman build` with the real tag and labels when the image is missing and none when it exists, the captured build context, its removal, and the tag in the `create` call;
  - the kept-volume tests cover every subset of kept volumes and check which volumes `volume create` received, the `Adopted volume` and `Created volume` lines, and the three mounts of the `create` call;
  - the name tests accept `up`, `list`, `default`, `backup`, and dotted names such as `a.home`, and check that no two of them yield the same volume name;
  - refusals for owner conflicts (an empty, missing, or other owner on the container, each volume, or the backup container, and several foreign objects in one message), for an interrupted update, and for Podman lookups that fail or return unusable JSON check the error message and that every recorded call is `--version` or a `container` or `volume` `exists` or `inspect`, so nothing was created or changed;
  - failures of the image lookup, the build, `volume create`, `create`, and `start` check the error message, the last call, and the number of calls, and that `up` did not report a running sandbox;
  - usage errors and invalid names check that neither `podman` nor `ssh` was called, with the preflight set to fail.

  Every `up` test also asserts that `ssh` was never called. No offline test starts a real container.
- **Manager.** `manager.New` takes a `process.Runner`, so a manager test injects its process functions and inspects each process a command would start.
- **Order of checks.** A test registers a stand-in command in a `cli.Tree` to assert that the tree runs its checks, its preparation, and its action in order. The executable has no debug commands for this.

### Host preflight tests

`check`, `build`, and `up` all run the preflight that matches the host operating system. `build` and `up` run it at the preflight step: `build` runs it before it writes the build context, and `up` before it looks up any sandbox object. On Linux, these commands run Podman through the host adapter's process runner. On Windows, they use the platform process runner. Each preflight is public, so later commands can also run it at the preflight step. The `check` action runs the preflight on its own and reports the results.

- **Linux.** `preflight.Check` takes a `context.Context` and a `preflight.Host` and returns a `[]Result` with one entry per prerequisite. The test binary enters the CLI through `cli.RunWithHost` with a read-only host adapter, which supplies fixture files, user identity, and write permissions in place of the real host. The tests still drive the public CLI as a subprocess with the fake programs. They assert that `podman` received only `--version`, that `ssh` was never called, and that the account lookup sent exactly one `getent passwd UID` query, without `MALLOC_TRACE`, `LD_DEBUG`, `LD_DEBUG_OUTPUT`, `LD_PROFILE`, or `LD_PROFILE_OUTPUT` in its environment. These tests are in `preflight_test.go`.
- **Windows.** `preflight.CheckWindows` takes a `context.Context`, a `platform.Host`, and a `process.Runner` and returns a `Report` with the status of each prerequisite. It also returns the automount root in `Report.AutomountRoot` for later commands. The test binary enters the CLI through `cli.RunWithWindowsHost` with a Windows host identity fixture. The tests run the fake `podman`, `ssh`, and `ssh-keygen` programs as real processes, not as in-process stubs. These tests are in `windows_preflight_test.go`. None of them verify a live Windows host or a live Podman machine.
- **Windows `up`.** These tests are in `windows_preflight_test.go` and use a fake Windows host identity, so they run on the Linux and the Windows CI job alike. The `windows-build` fixture passes the real embedded asset hash, and the tests read that hash from `version`. They cover a host without the sandbox, both with an existing and with a missing base image. In each case they check that the preflight's read-only calls come first. Next come the `exists` lookups of the container, the three volumes, the backup container, and the image. The run ends with the three `volume create` calls with the owner label, the `create` call with the real tag, and `start`. When the image is missing, they also check exactly one `podman build` with the real tag, the captured build context, and its removal. A missing and an unknown Podman client version each stop `up` after the preflight's calls, before any sandbox lookup. Usage errors and invalid names are rejected before the preflight, with no Podman call. Every Windows `up` test asserts that `ssh` was never called. Existing sandboxes, kept volumes, owner conflicts, and interrupted updates are covered by the Linux `up` tests only.

### Order of checks

A command declares its checks in `cli.Checks`. The tree runs them in this order and reports the first failure:

1. usage and names
2. preflight
3. sandbox existence
4. owner
5. interrupted update
6. running state
7. preconditions
8. terminal
9. session guard

A command can also set `Prepare`, which runs after the terminal check and before the session guard. It is for work that must finish before the guard, such as the builds `update` will run; no shipped command uses it yet. Nothing runs between the session guard and the command's action.

A usage failure, including an unknown command or option, also prints a usage line on standard error; a failure at a later step prints only its message. Later Stories add their commands to the tree and their checks at the matching step.
