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

The image build context lives in `build/context/`:

- the root holds the base image's `Containerfile`, the scripts it runs, and `record-versions.sh`;
- `toolchains/NAME/` holds one toolchain's recipe: its `Containerfile`, which starts `FROM ${BASE_IMAGE}`, an `install.sh`, and a `smoke.sh`. This version ships only `toolchains/native/`.

[Images](images.md) describes what the images contain. The toolchain catalog in `internal/toolchains` names the delivered toolchains and the smoke check and user of each for #29; the Debian packages are listed only in the recipe's `install.sh`. The archive leaves out a `.gitkeep` directly in `build/context/`; a `.gitkeep` in a subdirectory is an ordinary asset and is packaged and hashed. The manager is not part of `build/context/`.

Each image build gets its own temporary directory. For the base image, the executable writes the context root without `toolchains/`, plus the manager. For a toolchain image, it writes the contents of `toolchains/NAME/` plus the root's `record-versions.sh`.

The archive holds the Linux manager and the build context. Before packaging, the build tool converts CRLF line endings to LF in every context file that has no NUL bytes, whatever its text encoding. Files that contain a NUL byte are treated as binary and packaged unchanged. `.gitattributes` also checks text files in the context out with LF. The manager embeds the agent catalog `internal/agentcatalog/catalog.json`, which the build tool does not convert; `.gitattributes` checks it out with LF as well, so a checkout with `core.autocrlf=true` builds a byte-identical manager. The asset hash is the SHA-256 hash of the archive, so it covers the manager and the normalized context's path names and file contents. One commit therefore yields the same hash on Linux and on Windows, whether its context files were checked out with LF or CRLF line endings.

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

The build tool's test copies the sources into a temporary directory and builds hosts for Linux and Windows. It converts every file of the real build context, including those in nested directories such as `toolchains/native/`, to CRLF line endings and checks that the asset hash and the embedded, normalized context stay the same. It runs only the executable for the operating system it runs on and checks the other one's file format and embedded assets without running it.

### CI

The `Offline suite` workflow (`.github/workflows/offline.yml`) runs on every push and pull request, on Linux and on Windows, with module downloads turned off. Each job runs the build tool, then `go test ./...` and `go vet ./...`. The Linux job also runs the mirror test. Each job then records the output of its native `version`, and a final job checks that the Linux and Windows outputs are identical. That check confirms that both builds embed the same assets.

### Live suite

The live suite runs the executable against real Podman and starts only with `go run ./tools/live -opt-in`. `go test ./...` and CI never call real Podman. The offline tests in `internal/livesuite` and `tools/live` cover the harness against the fake programs ([Verification](live-suite.md#verification)). [Live suite](live-suite.md) describes how to run it on Linux and on Windows 11, how to select its image part, and the summary it writes.

### Test seams

- **CLI boundary.** Tests run the public CLI as a separate process. `testutil.NewFakePrograms` places native fake `podman`, `ssh`, `ssh-keygen`, and `getent` programs first on `PATH`. A test scripts their output and exit statuses with `Script` and asserts the exact calls they recorded with `Calls`. `TestMain` in `cli_test.go` unsets `SANDBOXED_AGENTS_GROUP` in the parent test process, the one without `SANDBOXED_AGENTS_CLI_FIXTURE`, so a group exported in your shell does not change the tests. The CLI subprocess keeps the environment it is given, so a group that a test sets with `t.Setenv` reaches it. For host preflight, see [Host preflight tests](#host-preflight-tests).
- **Image build.** Build tests run `sandboxed-agents build` from an empty working directory, with the real embedded assets, against the fake `podman`. The fake copies the build context while the build runs. The tests check that context: the recipe files have LF line endings and the manager is an executable static Linux amd64 binary. The base context holds five files, among them `sshd_config`, which the `Containerfile` copies to `/usr/local/etc/sandboxed-agents/sshd_config`, and the entrypoint, which runs `sandboxed-agents-manager ssh start` before `sleep infinity`. They also assert the following:
  - the exact `podman build` call, with its tag, labels, and context directory, and the exact number of Podman calls: after a successful base build, exactly one more, the image query `images --filter label=io.github.sandboxed-agents.managed=true --filter label=io.github.sandboxed-agents.asset-hash=HASH --format json`; after a failed one, none besides the preflight's own calls (on Linux, only `--version`);
  - removal of the temporary context directory after a successful and after a failed build;
  - the same tag from two builds, the first in the controller group `default` and the second in `second-group`, selected through `SANDBOXED_AGENTS_GROUP`;
  - usage errors before the preflight, including `--with` without a value and an extra word after `--with none`, a stop after a failed preflight, and a stop when the context directory cannot be created.

  The toolchain build tests in `toolchain_images_test.go` script the image query and the `image inspect` answers, with their tags and labels, and compare the exact Podman calls:
  - `build --with native` on a host without toolchain images: the base build, the image query, `image inspect` of the base tag, and one toolchain build with `--pull=never --no-cache --build-arg BASE_IMAGE=ID`, the `native` tag, and the four labels, among them `base-image` with the inspected ID. The output names both built tags and carries the reminder;
  - `build`, `build --with none`, and `build --with native` with a query that lists a current `native` image twice, an image of another asset hash, and the base image: each listed image is inspected once, and only the `native` image is rebuilt, once, under both of its existing tags;
  - `TestBuildRequestedToolchainCreatesItsCanonicalTagWhenOnlyAnAliasExists`: when the only `native` image carries just another tag, `build --with native` rebuilds it under that tag and then builds the canonical `native` tag;
  - `native`, `native,native`, and `native,native,native` in three controller groups yield the same tag; `build` and `build --with none` issue only the base build and the query;
  - a failed base build stops before the query; a failed build of one of two toolchain images still builds the other, prints `Failed toolchain sets: native` and the reminder, exits non-zero, and issues no call that removes or retags an image.

  The fake copies each toolchain build context, and the tests check the captured `native` recipe for LF line endings, the `FROM ${BASE_IMAGE}` line, the four packages, the version recording, and the smoke check, and that each temporary directory is gone afterwards. `internal/images/build_test.go` checks the tag derivation and that `images.Ensure` with an injected runner uses a current `native` image without building. `internal/toolchains` tests check set parsing, the rejected selections, and that only `native` is delivered and carries a smoke check.

  No offline test runs a real Podman build or the smoke check; #29 validates images against real Podman.
- **Sandbox creation.** `up` tests drive the public CLI as a subprocess that enters through `cli.RunWithHost` with the read-only host adapter, so the Linux preflight passes on fixture files. These tests run only on Linux and are skipped by the Windows CI job. The Windows `up` tests are described in [Host preflight tests](#host-preflight-tests). Two fixtures differ in the asset hash the CLI receives: `linux-preflight` passes `fixture-assets`, so the base image is `localhost/sandboxed-agents:base-fixture-assets`, and `linux-build` passes the real embedded asset hash, so a build writes the real build context. Most `up` tests use `linux-preflight`; the image-build test and the build-failure case use `linux-build`. A test scripts the fake `podman` answers for `exists` and the JSON of `inspect`, including the owner labels.

  The tests compare the complete list of Podman calls in these cases:
  - on a host without the sandbox: the preflight's `--version`, the `exists` lookups of the container, the three volumes, and the backup container, the `ps --all --format json` inventory of recorded SSH ports, the image lookup, the three `volume create` calls with the owner label, the `create` call with its labels, including an empty toolchain label, the SSH port label and `--publish 127.0.0.1:PORT:22` for the first port from 2222 that the test machine can bind, default options, limits, and exactly the three volume mounts, and `start`;
  - for an owned stopped sandbox, the lookups and `inspect` calls followed by `start`, and for a running one, the same calls without `start`;
  - for a failed preflight, only `--version`.

  Other cases assert only part of the calls:
  - the image-build test checks the image lookup, exactly one `podman build` with the real tag and labels when the image is missing and none when it exists, the captured build context, its removal, and the tag in the `create` call;
  - the kept-volume tests cover every subset of kept volumes and check which volumes `volume create` received, the `Adopted volume` and `Created volume` lines, and the three mounts of the `create` call;
  - the name tests accept `up`, `list`, `default`, `backup`, and dotted names such as `a.home`, and check that no two of them yield the same volume name;
  - refusals for owner conflicts (an empty, missing, or other owner on the container, each volume, or the backup container, and several foreign objects in one message), for an interrupted update, and for Podman lookups that fail or return unusable JSON check the error message and that every recorded call is `--version` or a `container` or `volume` `exists` or `inspect`, so nothing was created or changed;
  - failures of the image lookup, the build, `volume create`, `create`, and `start` check the error message, the last call, and the number of calls, and that `up` did not report a running sandbox;
  - usage errors, invalid names, and refused workspaces check that neither `podman` nor `ssh` was called, with the preflight set to fail.

  The toolchain tests of `up` are in `toolchains_test.go`:
  - an unknown, undelivered, empty, or `none`-combined selection, or one with surrounding spaces such as ` native` or `native, native`, for `build` and `up`, with the preflight set to fail, prints the usage line and the valid values and calls neither `podman` nor `ssh`;
  - `up agent01 --with native,native` on the Linux and the Windows fixture, with the `native` image missing, with the base image missing too, built on another base image ID, and current: the tests count the builds (one, two with the base first, one, and none), check that the toolchain build pins `BASE_IMAGE` to the current base ID, that no image query is issued and each build context is removed, and that `create` uses the inspected `native` image ID and the toolchain label `native`;
  - `TestUpCreatesFromTheInspectedNativeImageID` checks that `create` names the validated image ID rather than the tag, and `TestUpRefusesAToolchainImageReplacedDuringItsBuild` scripts a tag that names an image on the old base after the build and checks the `changed during its build` refusal with no `volume create`, `create`, or `start`;
  - `up --with none` creates the container from the base image with an empty toolchain label;
  - on an existing sandbox, no `--with` and a matching set, also as `--with=native,native`, start it with no build, image call, or `create`; `--with none` on a `native` sandbox and `--with native` on one without toolchains exit non-zero with read-only calls and a message that names the recorded and the given set and `update NAME --with`;
  - an owner conflict and an interrupted update are reported ahead of a toolchain conflict.

  Every `up` test also asserts that `ssh` was never called. No offline test starts a real container.
- **Resource limits.** The limit tests of `up` run with the Linux host fixture and with the Windows host fixture, against the fake `podman` and `ssh`. They check the `create` call with all four options given, with one option and the defaults for the other three, and without options, and that the four limit labels on that call record the values in effect. They check `--help` before the preflight with no Podman call, and each malformed, out-of-range, missing, and duplicate value as a usage error with no Podman call. On an existing sandbox the fake `podman` answers `container inspect` with recorded limit labels: equal values, also in another notation, start a stopped container, and a differing value, a missing label, or an unreadable label exits non-zero with no call that starts or changes a container or volume. A refusal for an owner conflict or an interrupted update is reported ahead of a limit conflict. These tests check the arguments and labels that `up` passes to Podman; no offline test confirms that Podman enforces the limits.
- **Workspace bind.** The workspace tests of `up` in `workspace_test.go` run on Linux with the Linux host fixture, against the fake `podman` and `ssh`. They create their directories, files, and symlinks in temporary directories under `.scratch`, and point `HOME`, `XDG_STATE_HOME`, and `TMPDIR` there for the protected host path cases:
  - an absolute path, `./project`, a symlink to the project, and `alias/..` and `./alias/..`, where `alias` is a symlink to a subdirectory of the project, each issue a `create` call that binds exactly the resolved directory at `/workspace`. The call mounts the home and SSH server state volumes, records the workspace kind `bind`, creates no workspace volume, and mounts no other host path or socket. A path with a comma, quotes, a CRLF pair (carriage return and line feed), and `=` is passed as one quoted field that a CSV reader parses back into the same mount, byte for byte. CSV readers drop a carriage return before a line feed, so `up` doubles that carriage return and the reader gives back the validated path;
  - `up agent01 DIR --cpus 2 --with native` binds the directory and mounts only the home and SSH server state volumes besides it, creates no workspace volume, records the toolchain label `native`, and passes the inspected image ID `sha256:validated-native` to `create`. A directory given after `--with native`, `--with=native`, or `--cpus 2 --with native` is reported as an unexpected argument, with no `podman` or `ssh` call;
  - a `WORKSPACE` that does not exist, names a file, is `missing/..`, or is `alias/child` where `alias` is a symlink to a file exits non-zero, issues no Podman call, and creates nothing on the host. The message names the resolved path, such as `ROOT/file/child`;
  - for the host state of the group, the system temporary directory, and `~/.ssh`, a `WORKSPACE` equal to it, inside it, containing it, or reaching it through a symlink, a protected host path that is itself a symlink to the workspace, and a protected host path that does not exist yet, each exit non-zero with a message naming the protected host path and issue no Podman call. A future host state that a dangling symlink points into the workspace is refused as well;
  - the test executable, its directory, the real system temporary directory, and its parent are refused the same way. A copy of the test executable outside the temporary directory, started directly, through a symlink while the workspace is a symlink to its directory, and with a hard link to it inside the workspace, refuses each workspace and names the copy. With a second hard link outside the workspace and an unreadable directory inside it, the hard link search fails closed: `up` refuses, names the copy and `permission denied`, and calls no Podman;
  - a symlink that points to itself is refused with a message naming the symlink;
  - a static mount table injected through the host fixture in place of `/proc/self/mountinfo` aliases a protected host path, for the host state, `~/.ssh`, the temporary directory, and the executable's directory, so that the workspace equals it, lies inside it, or contains a mount of it, or so that the protected host path is a mount of the workspace. Each case is refused before Podman. An empty, malformed, or incomplete mount table is refused as well. A table with two mounts stacked on the home directory uses the later, visible one, so `~/.ssh` reached through a mount in the workspace is refused. No test creates a real mount;
  - with no container but kept volumes, `up agent01 DIR` binds the directory, issues no call that creates, removes, or mounts the workspace volume, and prints the unused volume's name;
  - on an existing sandbox, stopped and running, bound to a directory, `up agent01` with no `WORKSPACE`, the same directory, or a symlink to it starts it. Another directory, and any `WORKSPACE` on a sandbox with a workspace volume, exit non-zero, issue only read-only calls, and print the recorded and the given workspace with `remove` and `up`. A failed preflight, an owner conflict, and an interrupted update are reported ahead of a workspace conflict;
  - `list` shows a container with the workspace kind `bind` as `bind`;
  - on the Windows host fixture, any `WORKSPACE` is refused with no Podman call. Through the same public CLI seam, the `unsupported-preflight` fixture refuses a `WORKSPACE` with a generic message that names the host operating system, `darwin`, and not Windows, also with no Podman call.

  These tests check the mounts that `up` passes to Podman. No offline test starts a real container or confirms which host paths a running sandbox can reach.
- **SSH port.** The tests in `ssh_port_test.go` run each case with the Linux host fixture and with the Windows host fixture, against the fake `podman` and `ssh`. Recorded ports come from scripted `ps --all --format json` and `inspect` answers. Listeners are real: a test holds a TCP listener on `127.0.0.1` of the machine that runs the tests, or probes which ports it can bind, so the bind check runs natively on each CI job. The tests cover:
  - `up agent01` on a host without sandboxes records `io.github.sandboxed-agents.ssh-port=2222` and publishes exactly `127.0.0.1:2222:22`, accepting `--publish`, `-p`, or `--publish=` as the form. The test is skipped when the test machine cannot bind port 2222 itself;
  - `--port N` beside resource limit options in both value forms, with the label, the single publication, and the limits on the `create` call;
  - allocation that skips a port the test holds and a port recorded on a running or exited container of another controller group, ignores an SSH port label on a container outside the product's names, and inspects neither container;
  - a second `up` after the first sandbox stopped, whose recorded port appears in the inventory of an exited container, gets the next free port;
  - `--port N` for a held port and for a port recorded on a stopped container of another group exits non-zero naming the port, with `ps` as the last and only non-lookup Podman call;
  - a failed, malformed, `null`, non-array, duplicate, or incomplete inventory, and an out-of-range or non-string `ssh-port` label on a product container, stop `up` with only read-only calls;
  - on an existing sandbox with a recorded port, running or stopped, `up` without `--port` and with an equal `--port` starts only a stopped container, with no `create` and no `ps`. A different `--port` exits non-zero naming both ports, `remove agent01`, and `up agent01`, with only read-only calls;
  - `start` and `restart` of a stopped sandbox whose recorded port the test holds exit non-zero naming the port and `unavailable`, with only read-only calls. `up` and `start` of a running sandbox whose port the test holds succeed without a listener check;
  - each of 13 malformed `--port` command lines, such as a missing value, `0`, `65536`, `+2300`, `2e3`, a value with spaces, and a repeated option, is a usage error with no Podman call. `--port` values `1`, `65535`, and `02300` parse with `--help` and make no Podman call;
  - `up`, `start`, and `restart` of a stopped sandbox whose `ssh-port` label is missing, empty, `not-a-port`, `0`, or `65536` exit non-zero with a message naming the recorded SSH port, `remove agent01`, and `up agent01`, with only read-only calls;
  - `restart` of a running sandbox whose port the test holds runs the session query and `stop`, then exits non-zero naming the port and `unavailable`, with no `start`;
  - `list` shows the recorded port of a running and of a stopped sandbox under the `SSH PORT` header, and the backup container's port in an `update interrupted` row beside a kept workspace volume, with only read-only calls.

  No test covers the case where no port from 2222 through 65535 is free.
- **SSH directory.** `ssh_directory_test.go` runs `up` and `start` with the Linux and the Windows host fixture, with `HOME` and `USERPROFILE` pointing to a temporary directory. In each case, `~/.ssh` either does not exist or holds a configuration, a key pair, `known_hosts`, and a nested file with binary content. After the command, every file and directory under `~/.ssh` is unchanged byte for byte, or the directory still does not exist, and neither `ssh` nor `ssh-keygen` was called.

  These tests check the arguments and labels that `up` passes to Podman and the host-side bind check; no offline test opens an SSH connection or confirms that Podman publishes the port.
- **Sandbox removal.** `remove` tests drive the public CLI as a subprocess against the fake `podman` and `ssh`, through the Linux host path with the `sandbox-host` fixture, as the lifecycle tests below do. They script the `exists` and `inspect` answers, including owner labels, running state, and mounts, and compare every other Podman call:
  - a stopped owned sandbox gets only `rm` of the container. With `--volumes`, each owned volume gets `volume rm`, and a volume with an empty or other owner is kept, named, and makes `remove` exit non-zero;
  - every non-empty subset of volumes without a container, with and without `--volumes`, gets either the `no container` report or one `volume rm` and one `Removed volume` line per volume;
  - an empty or other owner on the container, each volume, or the backup container, with and without a container and `--volumes`, and an owned backup container, are refused with only read-only lookups, even with `--force`. A foreign volume beside an owned backup container is reported as an owner conflict, not as an interrupted update, also with `--volumes --force`;
  - on a running sandbox, the `podman exec … sessions list` call comes first, followed by `stop` and `rm` when `remove` goes on. The scripted answers are an empty list, two sessions, a failed `exec`, invalid JSON, `null`, and an object without `name` or without `agent`, each with and without `--force`. The tests check the named sessions, `--force` in a refusal, and the message about sessions that cannot be named;
  - a bound workspace keeps its host file, and no Podman call names the host path. Its unused workspace volume is removed with `--volumes` and kept without;
  - refusals for an owner conflict, a backup container, running sessions, and a manager that does not answer leave fixture host SSH files unchanged;
  - an unusable `inspect` answer and failures of `stop`, `rm`, and `volume rm` stop `remove` before any later mutating call;
  - an unknown sandbox is refused with only read-only lookups. Usage errors, including a repeated option and `--volumes=true`, call neither `podman` nor `ssh`.

  Most `remove` tests also assert that `ssh` was never called. No offline test checks the session query against a real manager.
- **Sandbox lifecycle.** `stop`, `start`, and `restart` tests drive the public CLI as a subprocess against the native fake `podman` and `ssh` programs. The commands run no preflight. These tests enter the CLI through the Linux host path with the `sandbox-host` fixture, so their Podman calls carry no `--connection` and the tests run on the Linux and the Windows CI job alike. The Windows target of these commands is covered by the Podman target tests below. A test scripts the fake `podman` answers for `exists`, the JSON of `inspect`, including the owner labels and the running state of the container, and the output and exit status of `podman exec` for the session query. `sandbox.RunningSessions`, the session query that `remove`, `stop`, and `restart` share, has its own test in `internal/sandbox/sessions_test.go`: its injected process runner cancels the caller's context and then returns `[]` with status 0, and the query rejects that answer as no answer.

  The tests compare the complete list of Podman calls in these cases. The lookups are the `exists` calls of the container, the three volumes, and the backup container, and an `inspect` call for each object that exists:
  - `stop` on a running sandbox: the lookups, the `exec` call of `/usr/local/bin/sandboxed-agents-manager sessions list` in the sandbox's container, and `stop`; on a stopped sandbox, only the lookups;
  - `start` on a stopped sandbox: the lookups and `start`; on a running sandbox, only the lookups;
  - `restart` on a running sandbox: the lookups, the `exec` call, `stop`, and `start`; on a stopped sandbox, the lookups and `start`, with no `exec` and no `stop`;
  - `stop` and `restart` with `--force`, with the same calls as without it when the manager reports no running session.

  The session tests check the `exec` call and, with `--force`, that exactly `stop`, or `stop` and `start` for `restart`, follow it; a refusal issues no call after `exec`:
  - a manager that reports two running sessions makes `stop` and `restart` refuse and name each session; with `--force` they name both as ended;
  - a manager without an answer: `exec` exits non-zero, or prints nothing, text that is not JSON, `null`, an object, an array followed by more text, or an element whose `name` or `agent` is missing, whitespace-only, or not a string, also after a valid element. Without `--force`, `stop` and `restart` refuse, say that running sessions cannot be ruled out, and name `--force`. With `--force` they say that the ended sessions cannot be named and name none.

  The refusals before the session query assert the error message, the exit status, and that every recorded call is a `container` or `volume` `exists` or `inspect` call:
  - an unknown sandbox name, for each command;
  - kept volumes without a container, for every nonempty subset of the three volumes and each command: the message names `up NAME`. With one of those volumes carrying an empty, missing, or other owner, the message reports the owner conflict and does not name `up NAME`;
  - owner conflicts with an empty, missing, or other owner on the container, on each volume, with and without the container, and on the backup container, and a backup container with the current owner, whose message names `update NAME`. These cases run for each command on a running and on a stopped sandbox, so they include `stop` on a stopped sandbox and `start` on a running one;
  - order of checks: a running sandbox whose manager would report running sessions is refused for an owner conflict, with and without `--force`, or for a backup container, and is never queried;
  - failed `exists` lookups of the container, a volume, and the backup container, and `inspect` output that is not JSON, empty, or for another object.

  Failures of `podman stop` and `podman start`, including each step of `restart`, check the error message, the last call, and that the command did not report the sandbox's state. Only a `restart` whose `start` fails has already reported the ended sessions. Usage errors and invalid names check that neither `podman` nor `ssh` was called: a missing name, an invalid name, `--force` in place of the name, a second `--force`, `--force` given to `start`, an unknown option, and an extra argument. Every lifecycle test also asserts that `ssh` was never called. No offline test starts a real container.
- **Shell.** The tests in `shell_test.go` drive the public CLI as a subprocess against the fake `podman`, `ssh`, and `ssh-keygen`, with the `sandbox-host` fixture and the `windows` fixture, and script the `exists` and `inspect` answers as the lifecycle tests do. They check:
  - the exact `podman exec --interactive --user=1000:1000 --workdir=/workspace sandboxed-agents.default.agent01 /bin/bash` call, with `--tty` only when standard input is a terminal. `windows_target_test.go` checks `--connection` and the selected machine on every call after the selection, the `exec` call included, and the refusal of an unavailable machine before any sandbox lookup;
  - without a terminal, that the exact text written to the standard input of `shell` reaches the fake `podman exec`, and that the output, error output, and exit status scripted for that fake, with the statuses 0, 7, and 125, become those of `shell`;
  - with a terminal, the same pass-through of scripted output and the statuses 0 and 7. `shell_terminal_linux_test.go` opens a pseudo-terminal of the test host through `/dev/ptmx` and gives its slave side to `shell` as standard input. `shell_terminal_windows_test.go` allocates a console when the test process has none and gives `shell` the console input handle `CONIN$`; it runs only on a Windows host;
  - refusals for an unknown name, volumes only, an owner conflict on the container, each volume, or the backup container, an interrupted update on a running and a stopped sandbox, and a stopped sandbox. Each checks the first failure in the order of checks, that no later one is named, that every Podman call is a `container` or `volume` `exists` or `inspect`, and that `ssh` was not called;
  - usage errors, a missing name, an invalid name, `--help`, an extra argument, and `--force`, with a usage line and no `podman` call;
  - in the controller group `team-a`, that every call names only `sandboxed-agents.team-a.agent01` and that `exec` addresses that container;
  - `up agent01` without `--ssh-config` followed by `shell agent01` against one fake `podman`, with the Linux preflight fixture and the `windows` fixture: `shell` ends with the `exec` call, and neither command calls `ssh` or `ssh-keygen`;
  - with temporary `HOME`, `USERPROFILE`, `XDG_STATE_HOME`, and `LOCALAPPDATA`, that files in the SSH directory and in host state are unchanged byte for byte, that `shell` creates no file or directory there when none exists, and that neither `ssh` nor `ssh-keygen` was called.

  The fake `podman` runs no command: the terminal tests use a real terminal of the test host only to check how `shell` detects it. No offline test opens a shell in a container or a pseudo-terminal inside one.
- **Controller groups.** The tests in `controller_group_test.go` drive the public CLI as a subprocess against the fake `podman` and `ssh`:
  - an empty value and each of the invalid group names `Team`, `a.b`, `-a`, `a/b`, and `a b` makes `up`, `up --help`, `start`, `stop`, `restart`, `remove`, `shell`, `list`, `build`, `check`, `version`, `integrations config`, and `integrations login` exit non-zero with a message naming the group, with no `podman` or `ssh` call;
  - `up` with the group `team-a` creates the volumes and the container under `sandboxed-agents.team-a.agent01` with the owner label `team-a`, and no call names the `default` group;
  - `start`, `stop`, `restart`, `remove`, and `shell` in `team-a` report an unknown sandbox when no object exists under the `team-a` names, and every call names only those names;
  - in `team-a`, an owner of `default` on the container, each volume, or the backup container makes `up`, `start`, `stop`, `restart`, `remove`, and `shell` report an owner conflict that names the foreign object and Podman;
  - a sandbox of which only the backup container exists, with the current, another, or an empty owner, is refused by the same six commands with `update agent01` or an owner conflict;
  - a foreign container, two foreign volumes, and a foreign backup container are named together in one owner conflict by the same six commands, without a mention of the interrupted update;
  - `up agent01` runs twice against one fake `podman`, first in `default` and then in `team-a`, each from its own empty working directory, and the two `create` calls name `sandboxed-agents.default.agent01` and `sandboxed-agents.team-a.agent01`. This shows that the group comes from the variable and not from the working directory. Both runs use the same test executable, so the tests do not vary its install location.

  These tests assert read-only Podman calls on every refusal and that `ssh` was never called. The host state resolver `controllergroup.StateDirectory` has its own tests in `internal/controllergroup/group_test.go`: separate `group-GROUP` directories for `default`, `team-a`, `con`, and `nul` on Linux and Windows without creating them, the fallback to `~/.local/state` for an unset or relative `XDG_STATE_HOME`, and refusals of an invalid group and of an empty or relative `LOCALAPPDATA`. Only `up NAME WORKSPACE` calls the resolver, to guard the bind; no command reads or writes host state data.
- **List.** The tests in `list_test.go` script the fake `podman` answers for `ps --all --format json`, `volume ls --format json`, and the `inspect` calls, and read the printed table with whitespace normalized. These tests compare the whole table:
  - rows only for the current group, sorted by sandbox name, with `-` in `AGENTS`, in `TOOLCHAINS` when no toolchain label is recorded, and in `SSH PORT` for containers that record no port, and only the header without sandboxes;
  - `native` in `TOOLCHAINS` from the label of a running container, and from the backup container's label when only the backup container and a volume remain;
  - `volumes only` for each nonempty subset of volumes, including sandbox names that end in `.workspace`, `.home`, `.ssh`, or `.backup` or contain more dots, with the workspace kind `volume` only when the workspace volume exists and the existing volume names in `VOLUMES`;
  - `update interrupted` when only the backup container remains;
  - the group `team-a` on the Linux and the Windows fixture. On Windows the machine is selected first, and every later call carries `--connection` without `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or `CONTAINER_SSHKEY`;
  - an owned container that was renamed with Podman, listed under its `sandbox-name` label;
  - in `default` and in `team-a`, volumes under another group's prefix (`sandboxed-agents.other.…`) that carry the current group as owner get no row and no `volume inspect` call. Beside them, an owned home volume and a foreign workspace volume under the current group's names give one `owner conflict` row that names both volumes.

  The precedence tests check only the start of the sandbox's row: the name, the state, the workspace kind, and the three `-` placeholders. They cover `owner conflict` for an empty or other owner on the container, each volume, or the backup container, with and without a container, ahead of `update interrupted`, which is also shown with a container. They also cover `owner conflict` for a foreign container or a single foreign volume without a backup container. The precedence tests also check that no row names the backup container.

  Further tests check that an extra argument to `list` is a usage error with no Podman call, on the Linux and the Windows fixture, and that failed, malformed, `null`, duplicate, or incomplete inventory and `inspect` answers make `list` exit non-zero with nothing on standard output.

  Every `list` test asserts that its only Podman calls are `ps`, `volume ls`, and `container` or `volume` `inspect`, so `list` asks no manager, and that `ssh` was never called.
- **Podman target on Windows.** These tests are in `windows_target_test.go` and use a fake Windows host identity, so they run on the Linux and the Windows CI job alike.
  - `build`, `up`, `start`, `stop`, `restart`, `remove`, and `shell` succeed for a default machine among two and for a sole machine that is not marked as default. Each case runs four times: with `CONTAINER_CONNECTION`, with `CONTAINER_HOST`, with both and `CONTAINER_SSHKEY`, and with none of them set to a value. None of the three variables reaches a Podman call. Every call after the preflight or the selection, the session query and the `exec` call of `shell` included, starts with `--connection` and the selected machine.
  - For `stop`, `restart`, and `remove` on a running sandbox, an owner conflict, running sessions, and a manager that does not answer are refused. The only calls after the selection are lookups and the session query on the selected machine. An owner conflict issues no session query.
  - For `start`, `stop`, `restart`, `remove`, and `shell`, each of these cases exits non-zero with a message that names `sandboxed-agents check`, and the only calls issued are `machine list` and `machine inspect`:
    - an empty machine list;
    - two machines without a default, or two defaults;
    - an unreadable list or inspect answer;
    - a stopped, rootful, or non-WSL2 machine, or one without a rootful field;
    - a machine name that starts with `-` or contains a line feed, carriage return, or NUL.
  - `check`, `build`, and `up` refuse two default machines and these unsafe names after only `--version` and `machine list`, so such a machine is neither inspected nor named in a call.
  - Usage errors and invalid names make no Podman call.

  The fake `podman` records arguments and environment. It does not model how Podman resolves a saved default connection or the priority of `--connection`, and no offline test reaches a real Podman machine.
- **Manager identity.** The `remove` and lifecycle tests expect the session query as `exec --user=0:0 CONTAINER /usr/local/bin/sandboxed-agents-manager sessions list` and fail when `--user=0:0` is missing. They check the argument; no offline test observes the user a real container process runs as.
- **Manager.** `manager.New` takes a `process.Runner`, so a manager test injects its process functions and inspects each process a command would start. The `sessions list` tests inject a runner that fails the test when it is called. They check that the query prints `[]`, exits with status 0, and starts no process; that invalid usage, such as `sessions` alone, another subcommand, an extra argument, or an unknown option, prints the usage message on standard error, nothing on standard output, and exits non-zero; and that a failing standard output makes it exit non-zero.
- **Manager SSH server.** `manager.NewWithOptions` takes `manager.Options`, whose `SSHStateDirectory` replaces `/etc/ssh`, so a test of `ssh start` in `ssh_test.go` works in a temporary directory with an injected `process.Runner`:
  - with an empty state directory, it runs `ssh-keygen -q -t TYPE -N "" -f PATH` for `ed25519`, `ecdsa`, and `rsa`, in that order and into that directory, and only then `/usr/sbin/sshd -f /usr/local/etc/sandboxed-agents/sshd_config` with one `-h` per key;
  - with all three keys present, two starts run only sshd and leave the key files unchanged; with only one key present, it generates the other two and leaves the existing file's content, mode, and modification time unchanged;
  - `build/context/sshd_config` sets `AuthenticationMethods publickey`, `PubkeyAuthentication yes`, `PasswordAuthentication no`, `KbdInteractiveAuthentication no`, `PermitRootLogin no`, `AllowUsers agent`, `AllowAgentForwarding no`, and `UsePAM yes`;
  - invalid usage, such as `ssh` alone, another subcommand, an extra argument, or an unknown option, exits non-zero with a message, starts no process, and creates no state directory. A state path that is a file starts no process and is left unchanged;
  - a failing or non-zero `ssh-keygen` or `sshd` exits non-zero with a message and starts no further process.

  No manager test runs a real `sshd` or `ssh-keygen`.
- **Integrations.** The tests cover `integrations config` and `integrations login` at five seams:
  - **CLI against fake Podman.** `integrations_test.go` drives the public CLI as a subprocess with the `sandbox-host` fixture. `git identity` with both options, with and without the workflow name, ends with the session query and one `podman exec --user=1000:1000 --env HOME=/home/agent CONTAINER /usr/local/bin/sandboxed-agents-manager integrations config git identity --name=… --email=…` call. Unknown and undelivered integration and workflow names, `login git`, a missing or invalid sandbox name, a missing integration name, an empty `--name` given as `--name=` or as a separate empty word, and malformed, duplicate, or unexpected options exit non-zero with a usage line and no Podman call. An undelivered login integration names no valid integration, `git`, or `identity`. The order tests cover an unknown sandbox, only volumes, owner conflicts on the container, each volume, and the backup container, an interrupted update, a stopped sandbox, a manager query that fails or returns unusable JSON, and a missing terminal. Each reports the first failure; refusals before the manager issue only read-only lookups, and a manager that does not answer gets no further call. A missing option without a terminal exits with status 1 after the session query. A failing workflow `exec` exits with status 1 and issues no further call. With `GIT_*` variables, `EMAIL`, and a host `GIT_CONFIG_GLOBAL` file set, the fake `podman` records none of them, and no argument carries a host value.
  - **Manager with injected processes.** `internal/manager/integrations_test.go` checks the two `git config --global --replace-all` calls with their fixed environment. Prompts go to standard error, only for missing values, and every missing value is read before the first Git call. Input that ends early, contains NUL, or enters an empty name starts no process, nor does an undelivered workflow, an unknown option, a NUL in an option, or an empty `--name=`. A failed start or a non-zero status of either Git call exits with status 1 and makes no further call. The two writes are separate calls: when the second fails, the first has already run, and nothing undoes it.
  - **Catalog.** `internal/integrations/catalog_test.go` checks that the shipped catalog holds only `git` with `identity`, and resolves a synthetic catalog in which `git` has two config workflows. There, omitting the workflow name fails and lists both, while naming either one or omitting the name of a single login workflow succeeds.
  - **Workflow execution context.** `internal/sandbox/integrations_test.go` builds a `sandbox.NewIntegrationWorkflow` with an injected runner and calls `Apply`. With both options given, the runner receives a context whose deadline is at most 30 seconds away, and that context is canceled once `Apply` returns. With an option missing, so that the manager prompts, the runner receives the caller's context unchanged, and `Apply` does not cancel it. The tests do not wait for a deadline to pass.
  - **Pseudo-terminal and Windows target.** `integrations_terminal_linux_test.go` runs only on Linux. It attaches a real pseudo-terminal as standard input and output and checks that `git` with `--email` alone passes `-it` and the given option to the fake `podman`; it does not reach a real Podman or manager. `TestIntegrationUsesTheSelectedWindowsTargetAndControllerGroup` uses the `windows` fixture with `SANDBOXED_AGENTS_GROUP=team-a` and the three `CONTAINER_*` variables set. It checks that every call after the machine selection carries `--connection` and the selected machine, names only `team-a` objects, and receives none of those variables. Like the other Windows fixture tests, it uses a fake Windows host identity, so it does not verify a real Windows host or Podman machine.

  The [CI](#ci) jobs on Linux and on Windows run all of these tests as part of the offline suite, except the pseudo-terminal test, which runs only on Linux.

  No offline test runs a real Podman, a real manager in a container, or a real Git, and none confirms the isolation of the sandbox.
- **Order of checks.** A test registers a stand-in command in a `cli.Tree` to assert that the tree runs its checks, its preparation, and its action in order. The executable has no debug commands for this.

### Host preflight tests

`check`, `build`, and `up` all run the preflight that matches the host operating system. `build` and `up` run it at the preflight step: `build` runs it before it writes any build context, and `up` before it looks up any sandbox object. On Linux, these commands run Podman through the host adapter's process runner. On Windows, they use a wrapper around `platform.Run` that lives for one invocation. It removes `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` from the environment of each Podman call. Once the preflight or the lazy selection has chosen the machine, it also prefixes each later Podman call of that invocation with `--connection` and that machine. Each preflight is public, so later commands can also run it at the preflight step. The `check` action runs the preflight on its own and reports the results.

- **Linux.** `preflight.Check` takes a `context.Context` and a `preflight.Host` and returns a `[]Result` with one entry per prerequisite. The test binary enters the CLI through `cli.RunWithHost` with a read-only host adapter, which supplies fixture files, user identity, and write permissions in place of the real host. The tests still drive the public CLI as a subprocess with the fake programs. They assert that `podman` received only `--version`, that `ssh` was never called, and that the account lookup sent exactly one `getent passwd UID` query, without `MALLOC_TRACE`, `LD_DEBUG`, `LD_DEBUG_OUTPUT`, `LD_PROFILE`, or `LD_PROFILE_OUTPUT` in its environment. These tests are in `preflight_test.go`.
- **Windows.** `preflight.CheckWindows` takes a `context.Context`, a `platform.Host`, and a `process.Runner` and returns a `Report` with the status of each prerequisite. It also returns the automount root in `Report.AutomountRoot` and the selected Podman machine for later commands. `preflight.SelectWindowsConnection` applies the same selection rule for commands that run no preflight. The test binary enters the CLI through `cli.RunWithWindowsHost` with a Windows host identity fixture. The tests run the fake `podman`, `ssh`, and `ssh-keygen` programs as real processes, not as in-process stubs. These tests are in `windows_preflight_test.go`. None of them verify a live Windows host or a live Podman machine.
- **Windows `up`.** These tests are in `windows_preflight_test.go` and use a fake Windows host identity, so they run on the Linux and the Windows CI job alike. The `windows-build` fixture passes the real embedded asset hash, and the tests read that hash from `version`. They cover a host without the sandbox, both with an existing and with a missing base image. In each case they check that the preflight's read-only calls come first. Next come the `exists` lookups of the container, the three volumes, and the backup container, the `ps --all --format json` inventory of recorded SSH ports, and the image lookup. Each call after the preflight must start with `--connection` and the machine the preflight selected. The run ends with the three `volume create` calls with the owner label, the `create` call with the real tag, and `start`. When the image is missing, they also check exactly one `podman build` with the real tag, the captured build context, and its removal. A missing and an unknown Podman client version each stop `up` after the preflight's calls, before any sandbox lookup. Usage errors and invalid names are rejected before the preflight, with no Podman call. Every Windows `up` test asserts that `ssh` was never called. The resource limit tests in `resource_limits_test.go` also cover existing sandboxes, owner conflicts, and interrupted updates with the Windows fixture, and check `--connection` and the selected machine on the calls after the preflight that they compare; kept volumes are covered by the Linux `up` tests only.

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

The tree itself can set `Usage`, which runs for every command once the command name is recognized, before the command's `Help` and its own usage check. The executable uses it to read and validate the controller group, so an invalid group fails every command at step 1, also with `--help`.

A command can also set `Help`. When its arguments contain `--help`, the tree runs `Help` in place of the usage check and stops: no later check, including the preflight, and no action runs. An error from `Help` is reported as a usage failure.

A usage failure, including an unknown command or option, also prints a usage line on standard error; a failure at a later step prints only its message. Later Stories add their commands to the tree and their checks at the matching step.
