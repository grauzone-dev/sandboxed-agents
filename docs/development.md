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

The image build context lives in `build/context/`. Until #14 it deliberately has no image build content. Its only file is the `build/context/.gitkeep` placeholder, which the archive leaves out. A `.gitkeep` in a subdirectory is an ordinary asset: it is packaged and hashed.

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

The build tool's test copies the sources into a temporary directory and builds hosts for Linux and Windows. It runs only the executable for the operating system it runs on and checks the other one's file format and embedded assets without running it.

### CI

The `Offline suite` workflow (`.github/workflows/offline.yml`) runs on every push and pull request, on Linux and on Windows, with module downloads turned off. Each job runs the build tool, then `go test ./...` and `go vet ./...`. The Linux job also runs the mirror test. Each job then records the output of its native `version`, and a final job checks that the Linux and Windows outputs are identical. That check confirms that both builds embed the same assets.

### Test seams

- **CLI boundary.** Tests run the public CLI as a separate process. `testutil.NewFakePrograms` places native fake `podman` and `ssh` programs first on `PATH`. A test scripts their output and exit statuses with `Script` and asserts the exact calls they recorded with `Calls`. For host preflight tests, the test binary enters the CLI through `cli.RunWithHost` with a read-only host adapter, which supplies fixture files, user identity, and write permissions in place of the real host. The tests still drive the public CLI as a subprocess with the fake programs.
- **Manager.** `manager.New` takes a `process.Runner`, so a manager test injects its process functions and inspects each process a command would start.
- **Order of checks.** A test registers a stand-in command in a `cli.Tree` to assert that the tree runs its checks, its preparation, and its action in order. The executable has no debug commands for this.

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
