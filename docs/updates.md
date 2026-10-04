# Update a sandbox

`sandboxed-agents update NAME` moves an existing sandbox to the image that the installed executable produces for the sandbox's toolchain set. It replaces the sandbox's container and keeps everything else. This page describes what `update` does when every step succeeds. What happens after a failed step, how an interrupted update is recovered, the session guard, `update --all`, and changing the toolchain set with `--with` come with later Stories ([Not in this version](#not-in-this-version)).

## What `update` keeps

The new container gets the configuration recorded on the old one:

- the three volumes, or the same host directory bound at `/workspace` in place of the workspace volume ([Workspace bind](sandboxes.md#workspace-bind));
- the owner label, the resource limits, the toolchain set, and the SSH port on `127.0.0.1` ([Podman names and labels](sandboxes.md#podman-names-and-labels));
- the [container defaults](sandboxes.md#container-defaults).

The agent selection, agent pins, credentials, the authorized key, and the SSH host keys live in the volumes. `update` does not rewrite or regenerate them, and it deletes no volume. It does not go through `remove` (ADR-0002): it creates, changes, and deletes no [SSH setup](ssh.md#ssh-setup), it leaves the SSH setup's files in host state unchanged, and it reads and writes no file in your SSH directory. Because the host keys stay in the SSH server state volume, a pinned host key still matches after the update. The only things `update` may create in host state are the separate `locks` directory and the empty [lifecycle lock](#lifecycle-lock) file in it.

A sandbox that was running before the update is running afterwards, and a sandbox that was stopped is stopped afterwards.

## When a sandbox is outdated

A sandbox is up to date when its container was created from the current image for its toolchain set, compared by Podman image ID, and that image is current: for a toolchain image, its `base-image` label equals the ID of the current base image ([Image names and labels](images.md#image-names-and-labels)). `update` on an up-to-date sandbox prints `Sandbox NAME is already up to date.` and exits with status 0. It builds nothing and does not rename, create, start, stop, or remove any container.

Every other sandbox is outdated. An image that is missing, or a toolchain image that was not built on the current base image, as after a `build` that failed to rebuild it, counts as missing. `update` builds a missing image as `up` does, the base image first when it is missing, and may use the layer cache. It does not rebuild an image that exists and is current. To get fresh packages, run `build` first and then `update`.

## What `update` does

1. It checks the command line and the sandbox name. `update` takes exactly one argument, `NAME`.
2. It runs the preflight, as `up` does ([Host prerequisites](host-prerequisites.md)). On Windows, the preflight selects the Podman machine, and every later Podman call names it with `--connection` (ADR-0006). A failing preflight stops `update` before it looks up any sandbox object.
3. It takes the sandbox's [lifecycle lock](#lifecycle-lock).
4. It looks up the sandbox's container, its volumes, and its backup container, and checks their owners. An unknown sandbox name exits non-zero. An owner conflict exits non-zero, names the Podman objects concerned, and points to Podman. A sandbox of which only volumes remain exits non-zero and names `sandboxed-agents up NAME`, which adopts the volumes. A backup container with the current owner, left by an interrupted update, is refused as well: the message names the backup container, says that recovering an interrupted update is not available in this version and that `update` changes nothing, and points to Podman to inspect it ([Not in this version](#not-in-this-version)).
5. It decides whether the sandbox is [outdated](#when-a-sandbox-is-outdated). A container that reports no image ID is refused. A container without the toolchain label counts as recorded `none`, as for `up`. An up-to-date sandbox ends here.
6. For an outdated sandbox, it reads the configuration recorded on the container and refuses when any part of it is malformed ([Recorded configuration](#recorded-configuration)).
7. It builds the image it needs when that image is missing. When the image is not current after the build because it changed while `update` prepared it, `update` stops before the rename, says that the sandbox was not changed, and asks you to retry `sandboxed-agents update NAME`. Until the preflight has passed, the recorded configuration has been read, and the build has succeeded, nothing is stopped, renamed, or created.
8. It renames the container from `sandboxed-agents.GROUP.NAME` to the backup name `sandboxed-agents-backup.GROUP.NAME` while the container keeps running.
9. It creates the new container under `sandboxed-agents.GROUP.NAME` from the ID of the new image, not from its tag, without starting it. Its configuration is the one described in [What `update` keeps](#what-update-keeps), plus a label that records whether the sandbox was running or stopped before the update.
10. It stops the old container if it runs, and only then starts the new one. That way the new container can publish the same SSH port, and two containers never run on the same volumes. A sandbox that was stopped is started here too, for the readiness wait.
11. It [waits until the new container is ready](#readiness-wait).
12. It removes the backup container.
13. When the sandbox was stopped before the update, it stops the new container again.

On success, `update` prints `Sandbox NAME is updated.` and exits with status 0. Podman's own output, such as that of a build, passes through. Every refusal and failure exits non-zero.

### Recorded configuration

`update` copies the configuration of the new container from labels and mounts of the old one, so it needs all of them in the form `up` writes ([Podman names and labels](sandboxes.md#podman-names-and-labels)):

- the `toolchains` label, when present, must hold a valid toolchain set; an empty value or a missing label means no toolchains, as for `up`;
- the `memory`, `cpus`, `pids-limit`, `shm-size`, and `ssh-port` labels must be present and hold values that `up` accepts;
- the container must have exactly three mounts, one each at `/workspace`, `/home/agent`, and `/etc/ssh`. `/home/agent` and `/etc/ssh` must mount the sandbox's own home and SSH server state volumes under their standard names, such as `sandboxed-agents.GROUP.NAME.home`. `/workspace` must mount the standard workspace volume, or a host directory when the `workspace-kind` label is `bind`;
- every volume that the container mounts must exist.

When one of these does not hold, `update` names the container and the label or the mounts concerned, or the missing volume, and exits non-zero. It has then built, renamed, created, stopped, and started nothing. Containers created before these labels existed are refused in this way. `remove NAME` followed by `up NAME` replaces such a container and keeps the volumes ([Remove a sandbox](sandboxes.md#remove-a-sandbox)).

### Stored state

Nothing about an update is stored outside Podman. Whether the sandbox was running before the update is recorded only in the label on the new container, and an update in progress is visible only in the backup container's name (ADR-0005).

## Readiness wait

After it starts the new container, `update` waits until both of these checks have succeeded once:

- **Manager.** The manager answers its existing version query, `podman exec --user=0:0 CONTAINER /usr/local/bin/sandboxed-agents-manager version`, which runs as container root (ADR-0006).
- **SSH.** On the host, `ssh-keyscan -T 1 -t ed25519 -p PORT 127.0.0.1` returns an Ed25519 host key for `[127.0.0.1]:PORT`, where `PORT` is the SSH port recorded on the container. A host key arrives only after sshd in the container has completed the SSH key exchange. An open TCP port is not enough, because rootless Podman's port forwarding can accept a connection before sshd listens. `ssh-keyscan` does not authenticate, uses no key of yours, and reads and writes no file in your SSH directory or in host state. It comes with the OpenSSH client and is part of the host prerequisites that every preflight checks, also for commands that do not run it ([Host prerequisites](host-prerequisites.md), ADR-0007).

The wait lasts at most 60 seconds, including the time the probes take to run. The first attempt runs both checks right after the start. After that, every 250 milliseconds, it repeats only the checks that have not succeeded yet. Each probe has a timeout of 3 seconds, cut to the time left; `ssh-keyscan` also gives up after 1 second without an answer. When the wait ends before both checks have succeeded, `update` exits non-zero with `readiness wait for sandbox container CONTAINER failed`, followed by the reason and the last result of each check that had not succeeded.

On a cold start, the Podman machine, the container's initialization, the manager, and sshd can take several seconds to answer. 60 seconds leaves room for that and still bounds how long a failed update keeps you waiting (ADR-0007). Only offline tests check this value. No live run has measured it yet.

## Lifecycle lock

The lifecycle commands `up`, `start`, `stop`, `restart`, `remove`, and `update` never act on the same sandbox at the same time (ADR-0007). Each one takes an exclusive lock for its sandbox after its usage checks and its preflight or Windows target selection, and before it looks up the sandbox. It holds the lock until it exits, through every check, build, and Podman call, and through the installation or removal of an SSH setup. When the lock is taken, the second command does not wait: it exits non-zero, names the sandbox, says that another lifecycle command is in progress for it, and asks you to retry. The operating system releases the lock when the command exits or its process dies, so a crashed command leaves no stale lock behind.

The lock is one empty file per sandbox in `group-GROUP/locks/` in [host state](sandboxes.md#host-state). Its name is a hash of the sandbox's Podman container name. A lifecycle command creates this directory and the file when they are missing and never deletes them, so one empty file remains for every sandbox name a lifecycle command has run for. The file holds no update progress, no previous running state, and no configuration. Apart from this file, `update` writes nothing in host state. On Windows, a lifecycle command fails when `LOCALAPPDATA` is not set or is not an absolute path, as every command that needs host state does.

Know the limits of this lock:

- **Same host state root only.** Two commands share a lock only when they resolve the same host state directory. A command run with another `XDG_STATE_HOME` on Linux, or another `LOCALAPPDATA` on Windows, locks a different file and is not coordinated with commands that use the default. Run every lifecycle command for a sandbox with the same state directory.
- **Own commands only.** The lock serializes this executable's lifecycle commands for one host user. Writers that use Podman directly, such as your own `podman` calls or scripts, do not take it, and `update` cannot detect or prevent their changes. Commands of other host users use their own Podman and host state.
- **Coordination, not security.** Any program with your Podman authority can ignore the lock, delete the lock file, or change a sandbox's objects directly.
- **Per sandbox only.** Commands on different sandbox names, or on the same name in different controller groups, run in parallel. Image builds and SSH port allocation are shared across sandboxes and are not serialized by this lock.

When a Podman call fails unexpectedly, for example because something outside the executable removed or renamed a container, `update` stops, exits non-zero, and does not report success. It does not retry and does not undo earlier steps ([Not in this version](#not-in-this-version)).

## Not in this version

- **Failed steps** (#53). When a build, the rename, the creation, a start, the readiness wait, the removal of the backup container, or the final stop fails, this version stops and reports the failure. It does not roll back what it already did. Depending on the step, the backup container can remain beside the new container or in its place. Every other command that takes the sandbox name then refuses the sandbox and names `sandboxed-agents update NAME` ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)), and `update` refuses it as described under Interrupted updates.
- **Interrupted updates** (#54). Recovering a sandbox from a backup container that an earlier update left behind. In this version `update` refuses such a sandbox with its own message: recovering an interrupted update is not available yet, `update` changes nothing, and you can inspect the backup container with Podman. It neither completes nor undoes the interrupted update. The other commands that take a sandbox name still refuse such a sandbox and name `update NAME`; recovery through `update` comes with #54.
- **Session guard and `--force`** (#58). This version does not refuse an update while an agent session runs. Stopping the old container ends every process in it.
- **`update --all` and the usage errors of `update`** (#57).
- **Changing the toolchain set with `update NAME --with SET`** (#71).

## Verification

`update` adds no new manager functionality: the readiness wait uses the manager's existing version query, which the existing manager tests cover. Offline tests cover the rest. The readiness tests run the manager and `ssh-keyscan` probes through injected process functions. The CLI tests run the executable against fake Podman and a fake `ssh-keyscan`, and check the order and arguments of the Podman calls, the already-up-to-date case, the refusals, and the lifecycle lock. These tests do not show that an update works against real Podman or a real sshd. No live run covers `update` yet; that coverage belongs to the live suite (#24, [Live suite](live-suite.md)).
