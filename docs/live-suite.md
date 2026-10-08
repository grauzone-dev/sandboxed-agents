# Live suite

The live suite is the second release gate. It drives the executable against real Podman on Linux and on Windows 11, in a controller group of its own, and records the result as a redacted summary file. The maintainer uploads the summaries to a preview's GitHub prerelease. The first gate is the offline suite ([Development](development.md#test)), and the third is the manual checklist (#64).

> **Limitation: no run validates a release yet.** This version delivers the harness and the lifecycle part (#24). The harness builds the executable for the commit, checks that the executable reports that commit in `version`, and checks that `list` reaches real Podman. When it is selected, the image part calls `build` once, and `build` currently rebuilds only the base image. When it is selected, the lifecycle part takes one sandbox through `up`, `list`, `stop`, `start`, and `remove` and observes its isolation on the running container ([Lifecycle part](#lifecycle-part)). The live coverage of the other Features comes with #29, #35, #68, and #55. #29 extends the image part with the toolchain sandboxes, their smoke checks, and the outdated checks. Every summary of this version records `"image_coverage_complete": false`, also after a passing run with `-images`, and the stable release check rejects such a summary ([Reading a record](#reading-a-record)). A release needs a passing run of the complete suite, including the full image coverage, on Linux and on Windows 11.

## Requirements

- Linux on amd64, or Windows 11 on x64: a workstation edition with build 22000 or later. Windows 11 reports itself as Windows 10 to programs, so the suite accepts major version 10 with a build of 22000 or later and refuses Windows 10 and Windows Server. On Windows, the suite reads the machine's native architecture, not the architecture of the Go program.
- Go 1.27 or newer and `git` on `PATH`.
- The prerequisites of the executable itself, Podman and OpenSSH, set up as described in [Host prerequisites](host-prerequisites.md).
- A clean checkout of the commit you want to validate, left unchanged while the run lasts.
- For the lifecycle part, a controller group in which Podman reports no container and no volume, neither owned by the group nor named with its prefix. On Linux, it needs the local Podman service, with `CONTAINER_HOST` and `CONTAINER_CONNECTION` not set, and `sh`, `awk`, and `id` to read the host-side facts ([Target](#target), [Observations](#observations)). On Windows 11, it reads those facts inside the selected Podman machine through `podman machine ssh`.

The suite needs no `sudo`, no administrator rights, and no credentials, and it signs in nowhere. It may use the network: `build`, and `up` in the lifecycle part when the base image is missing, pull the Debian image and download packages, and the agent coverage (#68) will need npm.

## Run the suite

The suite runs only with `-opt-in`. Without it, `go run ./tools/live` prints that the live suite was skipped and exits with status 0, also when `-images`, `-lifecycle`, or `-output` is given. It calls neither `git` nor Podman and writes no summary. `go test ./...` and the `Offline suite` workflow never start the live suite.

The suite runs in a dedicated controller group, selected with `SANDBOXED_AGENTS_GROUP` ([Controller groups](sandboxes.md#controller-groups)). It refuses the group `default`, whether set explicitly or because the variable is not set, as well as any name that does not match `^[a-z0-9][a-z0-9-]*$`. Both refusals exit non-zero before Podman is called. The suite passes its group to every command of the executable it runs, with one exception: the lifecycle part also runs `list` in the `default` group to check that its sandbox does not appear there. `list` only reads.

On Linux, from the repository root:

```sh
export SANDBOXED_AGENTS_GROUP=live
go run ./tools/live -opt-in -images -lifecycle
```

On Windows 11, in PowerShell, from the repository root:

```powershell
$env:SANDBOXED_AGENTS_GROUP = 'live'
go run ./tools/live -opt-in -images -lifecycle
```

On Linux, a wizard walks you through a lifecycle run: it checks the prerequisites, runs `go run ./tools/live -opt-in -lifecycle` in a new controller group, shows the PowerShell command for the pending Windows 11 run, and prints the Linux summary. Run it from the repository root:

```sh
bash scripts/live-lifecycle-wizard.sh
```

The wizard is tracked in the repository, so it does not make the checkout count as changed. It posts and uploads nothing; share only the summary JSON it prints.

Any valid group name works on both platforms, including names that Windows reserves for devices such as `con`, because the group's host state directory is named `group-GROUP` ([Host state](sandboxes.md#host-state)).

Options:

- `-opt-in` runs the suite against real Podman.
- `-images` also runs the image part (see [Image part](#image-part)). A run that validates a preview must select it.
- `-lifecycle` also runs the lifecycle part (see [Lifecycle part](#lifecycle-part)). It issues no `build`. A run that validates a preview must select it.
- `-output <directory>` sets the directory for the summary, relative to the current directory. The default is `.scratch/live`, which Git ignores. The directory may also lie inside the checkout without being ignored: the check of the checkout leaves out exactly the summary file this run writes. Any other file in that directory, such as a summary copied from the other platform, still counts as a change and makes the run refuse.

An unknown option or an extra argument exits non-zero before anything runs. The suite waits at most one hour for a run. When that deadline passes or you interrupt the run with Ctrl+C, it leaves a failing summary. The lifecycle part then still tries to remove its sandbox and volumes, within a limit of its own ([Cleanup](#cleanup)). Apart from that, the suite does not ensure that processes started by the build, or work Podman has already begun, have stopped or been undone.

### What a run does

1. It asks `git` for the full SHA of `HEAD` and for the repository root.
2. It writes a failing summary for the platform (see [Failed runs](#failed-runs)).
3. It checks the controller group and the host.
4. It runs `git status` and refuses a checkout with modified tracked files or untracked files that are not ignored, so the tested executable is built from exactly that commit. Ignored files, such as `.scratch/` and `internal/assets/bundle.zip`, do not count, and neither does the summary file this run writes.
5. `build-host`: it builds the host executable and the embedded manager through `tools/build` into a new temporary directory, with the full commit SHA as the version. It removes that directory when the run ends.
6. `version`: it runs the built executable's `version` and checks that the output names the commit as the version, followed by an asset hash.
7. `list`: it runs `list`, which shows that the executable reaches real Podman. `list` only reads.
8. `images`: with `-images`, it runs the image part. The image part counts as ran only once its `build` has succeeded.
9. `lifecycle`: with `-lifecycle`, it runs the lifecycle part, after the image part when both are selected. The lifecycle part's own checks are recorded as they run, its cleanup after them, and the check `lifecycle` last.

Without `-images` and `-lifecycle`, a run creates no container, no volume, no image, and no SSH setup. The lifecycle part, and the live coverage of later Stories, create sandboxes only in the suite's group, named with the prefix `sandboxed-agents.GROUP.` and owned by that group, so they cannot touch a sandbox of another group.

The console shows the output of `git`, the build, Podman, and the executable, including paths and other host details. Keep that output to yourself; the summary file is the only result to share.

### Image part

Images are the exception to the separation by controller group. An image name contains no controller group, and an image has no owner ([Image names and labels](images.md#image-names-and-labels)). Every controller group that runs the same executable therefore uses the same images, and a `build` in the suite's group renews the images of that executable for every controller group on the host. Existing sandboxes in every group keep the image they were created from. `list` marks those sandboxes as `running (outdated)` or `stopped (outdated)` ([Outdated sandboxes](sandboxes.md#outdated-sandboxes)).

That is why the suite calls `build` only in the image part, and only when you select it with `-images`. Without `-images`, a run issues no `build`. With it, the image part calls `build` exactly once. In this version that call rebuilds the base image. #29 adds the toolchain sandboxes created with `up NAME --with <toolchain>`, their smoke checks, and the checks that the one `build` rebuilt the base image and all four toolchain images and that `list` marks the four toolchain sandboxes as outdated. The full image coverage counts as complete only when the image part ran and every one of these checks passed.

For everyday runs you can leave out `-images`. For a run that validates a preview, select it on both platforms.

### Lifecycle part

The lifecycle part runs the sandbox lifecycle against real Podman and observes on the running container that the sandbox's isolation takes effect. It never calls `build`. When the base image for the executable is missing, the first `up` builds it, as `up` always does. That image is shared by every controller group on the host ([Image part](#image-part)), and the run leaves it in place. No agent session runs, so no command needs `--force`, and the run makes no SSH setup.

#### Target

The part first fixes the Podman it observes (check `lifecycle/target`). On Linux it requires the local service: `CONTAINER_HOST` and `CONTAINER_CONNECTION` must not be set, and `podman info` must report a service that is not remote. Otherwise it fails before it lists anything. On Windows 11 it selects the Podman machine connection as the executable does, binds every Podman call of its own to that connection with `--connection`, and removes `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` from the environment of those calls. Before each command of the executable it runs, including `list` in the `default` group and the cleanup, it selects the connection again, and when the selection has changed it refuses that command without running it.

#### Steps

Each step is recorded as a check:

1. `lifecycle/group-empty`: Podman reports no container and no volume of the suite's group. A container or volume counts when it carries the group as its owner label or when its name starts with the group's prefix, `sandboxed-agents.GROUP.` or `sandboxed-agents-backup.GROUP.`, also without a label or with another owner. An inventory that Podman returns without names, or that cannot be read, fails the check as well; it never counts as an empty group. A failure here ends the part before `up`.
2. `lifecycle/up`: it creates a sandbox with a new random name: `up NAME --with none --memory 256m --cpus 1 --pids-limit 128 --shm-size 16m`. The small, explicit limits make the observed limits distinguishable from the defaults.
3. `lifecycle/created/list-running` and `lifecycle/created/default-group-absent`: `list` in the suite's group shows the sandbox as running, and `list` in the `default` group does not show it. Such a pair of checks follows every step that changes the sandbox, under the phase names `stopped`, `restarted`, `removed`, `adopted`, and `cleaned`, with the expected state `running`, `stopped`, `volumes-only`, or `absent` in the check name.
4. It observes the running sandbox under the phase `created` ([Observations](#observations)), then records the identity of its three volumes, their names, creation times, and mount points, in `lifecycle/created/volumes`. The volumes must carry the group as their owner label.
5. `lifecycle/stop`, then `list` shows the sandbox as stopped. `lifecycle/start`, then `list` shows it as running, and the run repeats the observations under the phase `restarted`.
6. `lifecycle/remove-keep-volumes`: `remove` without `--volumes` on the running sandbox, so that `remove` stops it first. `list` shows the state `volumes only`, and `lifecycle/removed/volumes-preserved` checks that Podman reports the same three volumes with the same identity.
7. `lifecycle/adopting-up`: `up` with the same name and options adopts those volumes. `list` shows the sandbox as running, `lifecycle/adopted/volumes-preserved` checks the volumes again, and the run repeats the observations under the phase `adopted`.
8. `lifecycle/remove-volumes`: `remove --volumes` on the running sandbox. `list` no longer shows it, and `lifecycle/group-clean` checks that Podman reports no container and no volume of the suite's group, by the same rules as `lifecycle/group-empty`.

The cleanup check `lifecycle/cleanup` follows ([Cleanup](#cleanup)), and the check `lifecycle` for the part as a whole comes last. A failed step ends the part; only the cleanup runs after it.

#### Observations

Each round of observations has a limit of 60 seconds. It reads each fact from the running container and from the kernel, and not from the options the executable passed to Podman. The host-side facts come from a read-only `sh` command: on Linux it runs on the host, and on Windows 11 it runs inside the selected Podman machine through `podman machine ssh`. The container-side facts come from `node` scripts that the run executes in the sandbox with `podman exec`. The checks of one round, each prefixed with `lifecycle/PHASE/`:

| Check | What the run observes |
| --- | --- |
| `mounts-three-named-volumes-no-binds-or-sockets` | `podman container inspect` reports the container under the name `sandboxed-agents.GROUP.NAME`, running, with a process ID, and with exactly three mounts: the sandbox's named volumes at `/workspace`, `/home/agent`, and `/etc/ssh`, each writable. Any other mount, such as a host path or a socket, fails the check. |
| `kernel-observations` | The host-side command and both `node` scripts ran and returned complete, readable facts. |
| `root-maps-to-subordinate-ids` | In `/proc/PID/uid_map` and `/proc/PID/gid_map` of the container's start process, read on the host side with the process ID from `inspect`, container root maps to an ID other than 0 and other than the host user's, and that ID lies in the host user's range in `/etc/subuid` and `/etc/subgid`. |
| `agent-uid-1000-gid-1000-maps-to-host-user` | In the same maps, UID and GID 1000 map to the host user's UID and GID, which are not 0. |
| `start-uid-0-gid-0` | The real, effective, saved, and file system UIDs and GIDs of PID 1 in the container are 0, and on the host side the effective UID and GID of the start process in `/proc/PID/status` are the IDs that container root maps to. |
| `manager-exec-uid-0-gid-0` | A `podman exec --user=0:0` starts the real `/usr/local/bin/sandboxed-agents-manager` with `version`, its output directed into a private, already full FIFO, so the manager blocks on its first write. While it is blocked, the run confirms through `/proc` that the process runs that executable in the user namespace of PID 1, and reads its UIDs and GIDs, which must all be 0. It then kills and reaps the manager and removes the FIFO. |
| `shell-uid-1000-gid-1000` | A `podman exec --user=1000:1000` process has all its UIDs and GIDs at 1000, resolves to the account `agent`, and runs in the same user namespace as PID 1. No shell, agent, or agent session starts. |
| `no-new-privileges` | `NoNewPrivs` is 1 in `/proc/PID/status` of PID 1, of the manager, and of the UID 1000 process. |
| `memory-limit-256m` | `memory.max` in the container's cgroup v2 directory is 268435456. |
| `cpu-limit-1` | `cpu.max` has a numeric quota equal to its period, which is one CPU. |
| `process-limit-128` | `pids.max` is 128. |
| `shm-limit-16m` | `statfs` of `/dev/shm` reports a size of 16777216 bytes. |
| `gateway-host-access-blocked-with-positive-control` | The host is not reachable through the sandbox's gateway address, while a positive control reaches it (see below). |
| `observations-match-created` | Only after `stop` and `start` and after the adopting `up`: the facts above, including the mount sources, the ID maps, the host user's IDs, the limits, and the gateway address, equal those of the `created` round. Only the user namespace identifier may differ, because a container that starts again gets a new namespace. |

The ten checks from `root-maps-to-subordinate-ids` to `shm-limit-16m` are all recorded even when one of them fails. The gateway observation runs only when they all passed.

For the gateway observation, the sandbox reads its default gateway from `/proc/net/route`. The observation needs exactly one IPv4 default route: when the sandbox has none or more than one, the check fails. Networks that give the sandbox only IPv6 or several default routes are therefore not verified by this observation. The run starts two temporary observation containers from the sandbox's image, without pulling, without mounts, as UID 1000 with all capabilities dropped and `no-new-privileges`, named after the sandbox with the suffixes `-gateway-host` and `-gateway-control`, and labelled with the suite's group and the sandbox's name:

- a listener on the host network, on a random port, that answers each connection with a random nonce of this round;
- a control container with `--network=pasta:--map-gw`, which maps the gateway address to the host ([`podman run`](https://docs.podman.io/en/latest/markdown/podman-run.1.html)), and which must see the same gateway address as the sandbox. Podman 4.4.0, the supported minimum on Linux, already documents `no-map-gw` as the default of `pasta` and `--map-gw` as the option that overrides it ([network options](https://raw.githubusercontent.com/containers/podman/v4.4.0/docs/source/markdown/options/network.md), lines 43 to 56), and implements that override ([`networking_pasta_linux.go`](https://raw.githubusercontent.com/containers/podman/v4.4.0/libpod/networking_pasta_linux.go), lines 57 to 87). These sources show that the control is available from that version on; they are not a live run and attest no host.

The control must receive the nonce from the gateway address immediately before and immediately after the sandbox's connection to the same address and port is refused, times out after 2 seconds, or finds no route, without receiving data. Without the control, an unreachable gateway could stem from the host rather than from the sandbox's `--no-map-gw`. On Windows 11, the host network of the listener and the host that `--map-gw` reaches are those of the Podman machine, not of Windows itself.

After each round, within a separate limit of 15 seconds, the run removes the observation containers it started. For each name it checks that the container exists and that Podman reports exactly that name with the suite's group as owner and the sandbox's name as its label. Only then does it run `podman stop --time 2` and `podman rm`, without `--force`. A container of that name with other labels is left in place and fails the check.

What the observations do not show: they cover only the sandbox the suite creates, on the platform of the run. They do not attest the configuration of containers that were created or changed outside the executable (ADR-0006). The manager observation shows the identity that a root `podman exec` gives the manager executable, not a session query of the executable itself. The UID 1000 observation shows the account and the mapping that a shell or agent would get; logging in over SSH is covered by #35. The gateway observation tests one TCP connection to the one IPv4 gateway address and does not show that every other route to the host is closed, including routes over IPv6.

#### Cleanup

Once `lifecycle/group-empty` has passed, the part always ends with `lifecycle/cleanup`, which has a fresh limit of 90 seconds, also after a failure, after Ctrl+C, or after the deadline of the run. It always first checks whether Podman reports anything of the suite's group. When it reports nothing, no removal is needed and the check passes. When it reports something, the part runs `remove NAME --volumes` once, without `--force`, also when an earlier `remove --volumes` of the run exited successfully, and checks the group again. A final `remove --volumes` that exits successfully but leaves a container or volume behind therefore fails `lifecycle/group-clean`, while the cleanup that follows can still remove the rest and pass. On Windows 11 that `remove` is refused, like every command of the executable, when the machine selection has changed; the check then fails, and the sandbox may be left behind.

The cleanup deletes only what the run created through the executable: the sandbox and its three volumes. It never deletes an object of the group that it did not create, such as a foreign container that holds the name of an observation container; such an object fails the check. The base image stays, and so does the empty [lifecycle lock](updates.md#lifecycle-lock) file that every lifecycle command leaves in the group's host state. When the cleanup fails, inspect the group with `list` in that group and remove what is left with `remove NAME --volumes`, or with Podman for objects that the executable does not own.

### Later coverage

Because the suite's group is not `default`, the SSH host entry of a sandbox `NAME` will be named `NAME.GROUP` (`agent01.live` in the group `live`), and the managed SSH configuration and keys will lie in the host state of the suite's group. This keeps the SSH setup of the suite apart from that of the `default` group. It applies once the live coverage of the SSH setup exists; the harness makes no SSH setup.

## Validation records

A validation of a preview consists of three records, all for the commit of the preview:

| Record | File name | Written by |
| --- | --- | --- |
| Live suite on Linux | `live-suite-linux.json` | the live suite on Linux |
| Live suite on Windows 11 | `live-suite-windows-11.json` | the live suite on Windows 11 |
| Manual checklist | `manual-checklist.json` | the manual checklist (#64) |

A run writes its summary to the output directory under its platform's file name and replaces an earlier summary there. The file name `manual-checklist.json` is reserved for the manual checklist; this Story only fixes its name and format.

### Format

Each record is one JSON object encoded in UTF-8. This page is the contract that the manual checklist (#64) and the stable release check (#67) follow. This Story implements no reader for the stable release check.

| Field | Type | Records | Value |
| --- | --- | --- | --- |
| `schema_version` | number | all | `1` |
| `kind` | string | all | `live-suite` or `manual-checklist` |
| `commit` | string | all | the full commit SHA: 40 lowercase hexadecimal characters |
| `platform` | string | live suite only | `linux` or `windows-11` |
| `result` | string | all | `pass` or `fail` |
| `image_part_selected` | boolean | live suite only | whether the run was started with `-images` |
| `image_part_ran` | boolean | live suite only | whether the image part completed: its `build` succeeded |
| `image_coverage_complete` | boolean | live suite only | whether the full image coverage of #29 passed; always `false` in this version |
| `checks` | array | all | one object per check, each with `name` and `result` |

Each element of `checks` has two fields:

- `name` is a fixed check identifier set by the suite or the checklist, never a value taken from the host or from user input.
- `result` is `pass` or `fail`.

The live suite currently uses the identifiers `build-host`, `version`, `list`, and `images`, in that order, followed by the identifiers of the lifecycle part. These are fixed identifiers that start with `lifecycle/` for its steps and observations, listed in [Lifecycle part](#lifecycle-part), for example `lifecycle/up` and `lifecycle/created/no-new-privileges`, followed by `lifecycle/cleanup` and by `lifecycle` for the part as a whole. An identifier names what was checked, such as a phase and the expected state or observation, and never holds an observed value. The format has no field for the lifecycle part: whether a run selected it shows only in `checks`, and a record of a run without `-lifecycle` holds no `lifecycle` identifier. A reader of the records, the stable release check included, does not depend on these names ([Reading a record](#reading-a-record)). `checks` lists the checks the run reached. A run that stops before building, for example because of its controller group, has an empty array. The live coverage Stories add identifiers of their own; a new identifier does not change `schema_version`. The checklist items of the manual record are defined by #64. A record holds only the fields listed for its kind. A change to the set of fields or to their meaning raises `schema_version`.

`result` is `pass` only when the run finished and every check passed; otherwise it is `fail`.

`image_part_ran` is `true` only once the image part's `build` has succeeded. It is `false` when `-images` was not given, when the run stopped before the image part, and when the image part's `build` failed, including its preflight; in that last case `result` is `fail`.

`image_coverage_complete` is `true` only when the image part ran and every check of the full image coverage of #29 passed: the four toolchain sandboxes created with `up NAME --with <toolchain>`, their smoke checks, the one `build` that rebuilt the base image and all four toolchain images, and `list` marking the four toolchain sandboxes as outdated. The current harness has none of these checks, so it always writes `false`.

#### Reading a record

A reader decides from the fields alone. It never interprets a check name or the prose of this page. The stable release check (#67) accepts a live-suite record only when all of these hold:

- `schema_version` is `1` and `kind` is `live-suite`;
- `commit` is the commit of the release;
- `platform` matches the file name: `linux` in `live-suite-linux.json`, `windows-11` in `live-suite-windows-11.json`;
- `result` is `pass`;
- `image_part_ran` is `true`;
- `image_coverage_complete` is `true`.

It rejects every other live-suite record, including one with a missing or an additional field. A summary of the current harness always has `image_coverage_complete` set to `false`, so the release check never accepts it, even after a passing run with `-images`. A manual-checklist record is accepted only when `schema_version` is `1`, `kind` is `manual-checklist`, `commit` is the commit of the release, and `result` is `pass`.

A passing summary of the current harness, from a Linux run with `-images`, looks like this. The release check rejects it, because `image_coverage_complete` is `false`.

```json
{
  "schema_version": 1,
  "kind": "live-suite",
  "commit": "0123456789abcdef0123456789abcdef01234567",
  "platform": "linux",
  "result": "pass",
  "image_part_selected": true,
  "image_part_ran": true,
  "image_coverage_complete": false,
  "checks": [
    {
      "name": "build-host",
      "result": "pass"
    },
    {
      "name": "version",
      "result": "pass"
    },
    {
      "name": "list",
      "result": "pass"
    },
    {
      "name": "images",
      "result": "pass"
    }
  ]
}
```

### What a summary leaves out

A summary contains only the fields above. Everything else is left out, in particular:

- the output on standard output and standard error of every program the suite ran, and error messages;
- user names and host names;
- the controller group and sandbox names;
- container, volume, and image names and IDs;
- user and group IDs, ID mappings, subordinate ranges, process IDs, and user namespace identifiers;
- file system paths, mount sources, and cgroup paths;
- the limits and other values the lifecycle part observed;
- environment variables;
- tokens, keys, and passwords;
- SSH configuration and host key fingerprints;
- IP addresses, including the gateway address, ports, the nonce of the gateway observation, and URLs;
- the output of `version`;
- logs.

### Failed runs

The suite first asks `git` for the commit and the repository root. It then writes a summary with `"result": "fail"` before it calls any other program, and replaces that file atomically each time it writes the summary again. An interrupted run therefore leaves a failing summary, not a passing or partial one. If that first summary cannot be written, the suite exits non-zero without calling Podman.

These refusals and failures exit non-zero and leave a failing summary:

- the controller group `default` or an invalid group name;
- an unsupported host whose operating system is Linux or Windows: another architecture than amd64, Windows 10, Windows Server, or a Windows build before 22000. The summary still uses the file name and `platform` of that operating system, `linux` or `windows-11`;
- a checkout with uncommitted changes, or a failed `git status`;
- a failed build, a failed `version`, or a version that does not name the commit;
- a failed `list` or `build`;
- in the lifecycle part: a remote or unexpected Podman on Linux, a Windows machine selection that changed during the run, a suite group that already holds a container or volume, a container or volume inventory that cannot be read, a failed lifecycle command, a state in `list` other than the expected one, the sandbox shown in the `default` group, a mount other than the three named volumes, an isolation observation that is not in effect or that differs from the first one, a failed gateway control, changed volumes, an observation container that cannot be removed, or a failed cleanup.

When `git` cannot report the commit or the repository root, when it reports a commit that is not a full 40-character SHA, or when the operating system is neither Linux nor Windows, the suite cannot write a valid record. It then exits non-zero with a message and writes no summary.

## Upload the records

1. Push the preview tag and wait for the prerelease ([Releases](releases.md)).
2. On a Linux host and on a Windows 11 host, check out the preview's commit and run the suite with `-opt-in -images -lifecycle`.
3. Copy both summaries and the manual-checklist record into `.scratch/live` on one machine, keeping their file names.
4. Upload them to the preview's prerelease:

   ```sh
   gh release upload <preview-tag> .scratch/live/live-suite-linux.json .scratch/live/live-suite-windows-11.json .scratch/live/manual-checklist.json -R grauzone-dev/sandboxed-agents --clobber
   ```

   `--clobber` replaces the records of an earlier attempt.

Upload only the JSON files, never console output. Every record must name the prerelease's commit. A failing record is not a validation, and until the live coverage of #29, #35, #68, and #55 exists, neither is a passing one (see the limitation at the top of this page). The stable release check (#67) reads these files from the prerelease.

## Verification

The offline tests in `internal/livesuite` and `tools/live` run against the fake programs of the offline suite ([Test seams](development.md#test-seams)). `git` is faked as well, so the tests need no Git checkout. A test run that reaches the build simulates `tools/build` through the injected process runner: the fake builder copies a real CLI fixture executable to the requested output, and the suite then runs that executable's `version`, `list`, and `build` against the fake `podman`. The tests cover:

- a run without `-opt-in`, also with `-images`, `-lifecycle`, and `-output`: exit status 0, the skip message, no Podman call, and no summary;
- the group `default`, set or unset, and invalid names: a non-zero exit, no Podman call, and a failing summary that names the commit and the platform;
- unknown options, an extra argument, and `-output` without a value: a non-zero exit and no Podman call;
- unsupported hosts: a non-zero exit and no Podman call;
- a checkout with changes, a failed build, and an output directory that cannot be written: a non-zero exit and no Podman call;
- a built executable whose `version` does not name the commit: a failing summary;
- an output directory inside the checkout that Git does not ignore: the run's own summary does not make the checkout count as changed;
- runs without `-images` with a host identity for the operating system the tests run on, Linux or a synthetic Windows 11 identity: only the Podman calls of `list`, no `build`, and a passing summary under that platform's file name;
- runs with a Windows 11 host identity with `-images`, with `build` succeeding and failing: exactly one `podman build`, `image_part_ran` `true` only when that `build` succeeded, and `image_coverage_complete` always `false`;
- on Windows only, an output directory on another drive than the checkout: the run passes. Only the Windows CI job runs this test;
- a failing Podman call whose output holds a user name, a host name, paths, the group variable, a token, a key, an SSH host entry, a host key fingerprint, and a URL: each appears on the console and none in the summary, which holds only the fields of its record.

The offline tests of the lifecycle part, in `internal/livesuite/lifecycle_test.go` and `internal/livesuite/observations_test.go`, do not use the fake programs. They pass an injected process runner to `livesuite.Run` or to the observer, which answers every call of `git`, the build, the executable, `podman`, and the host-side `sh` with scripted output, so they need neither Podman nor a container. They cover:

- a lifecycle run whose `up` fails: a failing `lifecycle/up`, and no `build`;
- a group that holds a container owned by it, a volume owned by it, or an unlabelled container named with its prefix: a failing `lifecycle/group-empty` and no `up`;
- a container inventory of `null`, `{}`, an entry without names, or an empty name: a failing run and no `up`;
- an `up` after which `list` does not show the sandbox: a failing `lifecycle/created/list-running`;
- a cancellation during `up`: `remove NAME --volumes` with a live context of at most 90 seconds, a passing `lifecycle/cleanup`, and a failing summary;
- a `stop` or `start` after which `list` shows the wrong state, the sandbox shown by `list` in the `default` group, and a volume recreated with another creation time after `remove`: the expected failing check, a passing `lifecycle/cleanup`, and nothing left behind;
- a final `remove --volumes` that exits successfully but leaves a volume: a failing `lifecycle/group-clean`, a second `remove --volumes` in the cleanup, a passing `lifecycle/cleanup`, and no volume left;
- on Windows 11, Podman calls bound to the selected connection with `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` removed, and a selection that changes before `up`: no `up` and a failing run;
- a complete run on Linux and on Windows 11: every observation check passes in the phases `created`, `restarted`, and `adopted`, the sandbox, its volumes, and the observation containers are gone, no call carries `--force`, the summary has exactly its nine fields, and it holds none of the sandbox name, the volume and fixture paths, the machine name, the remote settings, the gateway address, or the mapped IDs;
- a run that fails at the memory limit, the manager identity, each of the three gateway probes, or a changed mount source after the adopting `up`: the expected failing check, a passing `lifecycle/cleanup`, and nothing left behind;
- three observation rounds with 41 passing checks, a host listener and a `pasta:--map-gw` control per round, nine gateway probes, and both observation containers removed after each round;
- each isolation violation on its own: container root mapped to host root or outside the subordinate range, UID 1000 not mapped to the host user, a start process or manager that is not root, a UID 1000 process with another account, another user namespace, or a saved root UID, `NoNewPrivs` missing on any of the three processes, a missing or different memory, CPU, or process limit, a zero CPU period, and a different shm size;
- a failing gateway probe: the gateway check fails and both observation containers are still removed;
- a changed volume source after reuse and a changed ID map after a restart: a failing `observations-match-created`;
- a host bind mount and unreadable kernel facts: a failing observation;
- on Windows 11, host-side facts read only through `podman machine ssh` with the selected machine, never through a local `sh`;
- no `--force` in the cleanup of the observation containers.

These tests check the logic of the lifecycle part against scripted answers. They do not show that real Podman, the kernel, or the scripts inside a real container behave as scripted.

No offline test reaches real Podman or a real Git checkout. Live runs are recorded separately from the offline result, by the summaries uploaded to a prerelease.

The only live run so far is a native Linux run without `-images` in a development environment, with Podman 4.3.1, below the supported minimum of 4.4.0. It built the executable, confirmed its version, and ran `list` against real Podman, which showed no sandboxes. Its summary passed, with `image_part_ran` and `image_coverage_complete` both `false`. That run shows only that the harness reaches real Podman on Linux. No live run has covered a supported host, the image part, the lifecycle part, or Windows 11. No run of the lifecycle part has passed on any platform, so it counts as verified live on neither Linux nor Windows 11, and validating the target platforms remains a task for the maintainer.
