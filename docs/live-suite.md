# Live suite

The live suite is the second release gate. It drives the executable against real Podman on Linux and on Windows 11, in a controller group of its own, and records the result as a redacted summary file. The maintainer uploads the summaries to a preview's GitHub prerelease. The first gate is the offline suite ([Development](development.md#test)), and the third is the manual checklist (#64).

> **Limitation: no run validates a release yet.** This version delivers only the harness. It builds the executable for the commit, checks that the executable reports that commit in `version`, and checks that `list` reaches real Podman. When it is selected, the image part calls `build` once, and `build` currently rebuilds only the base image. The live coverage of each Feature comes with #24, #29, #35, #68, and #55. #29 extends the image part with the toolchain sandboxes, their smoke checks, and the outdated checks. Every summary of this version records `"image_coverage_complete": false`, also after a passing run with `-images`, and the stable release check rejects such a summary ([Reading a record](#reading-a-record)). A release needs a passing run of the complete suite, including the full image coverage, on Linux and on Windows 11.

## Requirements

- Linux on amd64, or Windows 11 on x64: a workstation edition with build 22000 or later. Windows 11 reports itself as Windows 10 to programs, so the suite accepts major version 10 with a build of 22000 or later and refuses Windows 10 and Windows Server. On Windows, the suite reads the machine's native architecture, not the architecture of the Go program.
- Go 1.27 or newer and `git` on `PATH`.
- The prerequisites of the executable itself, Podman and OpenSSH, set up as described in [Host prerequisites](host-prerequisites.md).
- A clean checkout of the commit you want to validate, left unchanged while the run lasts.

The suite needs no `sudo`, no administrator rights, and no credentials, and it signs in nowhere. It may use the network: `build` pulls the Debian image and downloads packages, and the agent coverage (#68) will need npm.

## Run the suite

The suite runs only with `-opt-in`. Without it, `go run ./tools/live` prints that the live suite was skipped and exits with status 0, also when `-images` or `-output` is given. It calls neither `git` nor Podman and writes no summary. `go test ./...` and the `Offline suite` workflow never start the live suite.

The suite runs in a dedicated controller group, selected with `SANDBOXED_AGENTS_GROUP` ([Controller groups](sandboxes.md#controller-groups)). It refuses the group `default`, whether set explicitly or because the variable is not set, as well as any name that does not match `^[a-z0-9][a-z0-9-]*$`. Both refusals exit non-zero before Podman is called. The suite passes its group to every command of the executable it runs.

On Linux, from the repository root:

```sh
export SANDBOXED_AGENTS_GROUP=live
go run ./tools/live -opt-in -images
```

On Windows 11, in PowerShell, from the repository root:

```powershell
$env:SANDBOXED_AGENTS_GROUP = 'live'
go run ./tools/live -opt-in -images
```

Any valid group name works on both platforms, including names that Windows reserves for devices such as `con`, because the group's host state directory is named `group-GROUP` ([Host state](sandboxes.md#host-state)).

Options:

- `-opt-in` runs the suite against real Podman.
- `-images` also runs the image part (see [Image part](#image-part)). A run that validates a preview must select it.
- `-output <directory>` sets the directory for the summary, relative to the current directory. The default is `.scratch/live`, which Git ignores. The directory may also lie inside the checkout without being ignored: the check of the checkout leaves out exactly the summary file this run writes. Any other file in that directory, such as a summary copied from the other platform, still counts as a change and makes the run refuse.

An unknown option or an extra argument exits non-zero before anything runs. The suite waits at most one hour for a run. When that deadline passes or you interrupt the run with Ctrl+C, it leaves a failing summary. It does not ensure that processes started by the build, or work Podman has already begun, have stopped or been undone.

### What a run does

1. It asks `git` for the full SHA of `HEAD` and for the repository root.
2. It writes a failing summary for the platform (see [Failed runs](#failed-runs)).
3. It checks the controller group and the host.
4. It runs `git status` and refuses a checkout with modified tracked files or untracked files that are not ignored, so the tested executable is built from exactly that commit. Ignored files, such as `.scratch/` and `internal/assets/bundle.zip`, do not count, and neither does the summary file this run writes.
5. `build-host`: it builds the host executable and the embedded manager through `tools/build` into a new temporary directory, with the full commit SHA as the version. It removes that directory when the run ends.
6. `version`: it runs the built executable's `version` and checks that the output names the commit as the version, followed by an asset hash.
7. `list`: it runs `list`, which shows that the executable reaches real Podman. `list` only reads.
8. `images`: with `-images`, it runs the image part. The image part counts as ran only once its `build` has succeeded.

Without `-images`, a run creates no container, no volume, no image, and no SSH setup. The live coverage of later Stories will create sandboxes in the suite's group, named with the prefix `sandboxed-agents.GROUP.` and owned by that group, so they cannot touch a sandbox of another group.

The console shows the output of `git`, the build, Podman, and the executable, including paths and other host details. Keep that output to yourself; the summary file is the only result to share.

### Image part

Images are the exception to the separation by controller group. An image name contains no controller group, and an image has no owner ([Image name and labels](images.md#image-name-and-labels)). Every controller group that runs the same executable therefore uses the same images, and a `build` in the suite's group renews the images of that executable for every controller group on the host. Existing sandboxes in every group keep the image they were created from. Once `list` can mark sandboxes as outdated, which comes with a later Story, it will show them as outdated.

That is why the suite calls `build` only in the image part, and only when you select it with `-images`. Without `-images`, a run issues no `build`. With it, the image part calls `build` exactly once. In this version that call rebuilds the base image. #29 adds the toolchain sandboxes created with `up NAME --with <toolchain>`, their smoke checks, and the checks that the one `build` rebuilt the base image and all four toolchain images and that `list` marks the four toolchain sandboxes as outdated. The full image coverage counts as complete only when the image part ran and every one of these checks passed.

For everyday runs you can leave out `-images`. For a run that validates a preview, select it on both platforms.

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

The live suite currently uses the identifiers `build-host`, `version`, `list`, and `images`, in that order. `checks` lists the checks the run reached. A run that stops before building, for example because of its controller group, has an empty array. The live coverage Stories add identifiers of their own; a new identifier does not change `schema_version`. The checklist items of the manual record are defined by #64. A record holds only the fields listed for its kind. A change to the set of fields or to their meaning raises `schema_version`.

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
- file system paths;
- environment variables;
- tokens, keys, and passwords;
- SSH configuration and host key fingerprints;
- ports and URLs;
- the output of `version`;
- logs.

### Failed runs

The suite first asks `git` for the commit and the repository root. It then writes a summary with `"result": "fail"` before it calls any other program, and replaces that file atomically each time it writes the summary again. An interrupted run therefore leaves a failing summary, not a passing or partial one. If that first summary cannot be written, the suite exits non-zero without calling Podman.

These refusals and failures exit non-zero and leave a failing summary:

- the controller group `default` or an invalid group name;
- an unsupported host whose operating system is Linux or Windows: another architecture than amd64, Windows 10, Windows Server, or a Windows build before 22000. The summary still uses the file name and `platform` of that operating system, `linux` or `windows-11`;
- a checkout with uncommitted changes, or a failed `git status`;
- a failed build, a failed `version`, or a version that does not name the commit;
- a failed `list` or `build`.

When `git` cannot report the commit or the repository root, when it reports a commit that is not a full 40-character SHA, or when the operating system is neither Linux nor Windows, the suite cannot write a valid record. It then exits non-zero with a message and writes no summary.

## Upload the records

1. Push the preview tag and wait for the prerelease ([Releases](releases.md)).
2. On a Linux host and on a Windows 11 host, check out the preview's commit and run the suite with `-opt-in -images`.
3. Copy both summaries and the manual-checklist record into `.scratch/live` on one machine, keeping their file names.
4. Upload them to the preview's prerelease:

   ```sh
   gh release upload <preview-tag> .scratch/live/live-suite-linux.json .scratch/live/live-suite-windows-11.json .scratch/live/manual-checklist.json -R grauzone-dev/sandboxed-agents --clobber
   ```

   `--clobber` replaces the records of an earlier attempt.

Upload only the JSON files, never console output. Every record must name the prerelease's commit. A failing record is not a validation, and until the live coverage of #24, #29, #35, #68, and #55 exists, neither is a passing one (see the limitation at the top of this page). The stable release check (#67) reads these files from the prerelease.

## Verification

The offline tests in `internal/livesuite` and `tools/live` run against the fake programs of the offline suite ([Test seams](development.md#test-seams)). `git` is faked as well, so the tests need no Git checkout. A test run that reaches the build simulates `tools/build` through the injected process runner: the fake builder copies a real CLI fixture executable to the requested output, and the suite then runs that executable's `version`, `list`, and `build` against the fake `podman`. The tests cover:

- a run without `-opt-in`, also with `-images` and `-output`: exit status 0, the skip message, no Podman call, and no summary;
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

No offline test reaches real Podman or a real Git checkout. Live runs are recorded separately from the offline result, by the summaries uploaded to a prerelease.

The only live run so far is a native Linux run without `-images` in a development environment, with Podman 4.3.1, below the supported minimum of 4.4.0. It built the executable, confirmed its version, and ran `list` against real Podman, which showed no sandboxes. Its summary passed, with `image_part_ran` and `image_coverage_complete` both `false`. That run shows only that the harness reaches real Podman on Linux. No live run has covered a supported host, the image part, or Windows 11, so validating the target platforms remains a task for the maintainer.
