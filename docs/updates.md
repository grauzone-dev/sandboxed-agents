# Update a sandbox

`sandboxed-agents update NAME` moves an existing sandbox to the image that the installed executable produces for the sandbox's toolchain set. It replaces the sandbox's container and keeps everything else. `sandboxed-agents update NAME --with SET` does the same with another toolchain set ([Change the toolchain set](#change-the-toolchain-set)). `sandboxed-agents update --all` updates every sandbox of the current controller group ([Update every sandbox](#update-every-sandbox)). This page describes what `update` does, how it treats running agent sessions ([Session guard](#session-guard)), and how it rolls back when a step fails ([When a step fails](#when-a-step-fails)). Recovering an interrupted update comes with a later Story ([Not in this version](#not-in-this-version)).

```text
sandboxed-agents update NAME [--with SET] [--force]
sandboxed-agents update --all [--force]
```

## What `update` keeps

The new container gets the configuration recorded on the old one:

- the three volumes, or the same host directory bound at `/workspace` in place of the workspace volume ([Workspace bind](sandboxes.md#workspace-bind));
- the owner label, the resource limits, and the SSH port on `127.0.0.1` ([Podman names and labels](sandboxes.md#podman-names-and-labels));
- the toolchain set, unless `--with` gives another one ([Change the toolchain set](#change-the-toolchain-set));
- the [container defaults](sandboxes.md#container-defaults).

The agent selection, agent pins, credentials, the authorized key, and the SSH host keys live in the volumes. `update` does not rewrite or regenerate them, and it deletes no volume. It does not go through `remove` (ADR-0002): it creates, changes, and deletes no [SSH setup](ssh.md#ssh-setup), it leaves the SSH setup's files in host state unchanged, and it reads and writes no file in your SSH directory. Because the host keys stay in the SSH server state volume, a pinned host key still matches after the update. The only things `update` may create in host state are the separate `locks` directory and the empty [lifecycle lock](#lifecycle-lock) file in it.

A sandbox that was running before the update is running afterwards, and a sandbox that was stopped is stopped afterwards. This also holds after a complete rollback. The exceptions are an [incomplete rollback](#when-the-rollback-fails), after which the running state is not promised, and the failures after a successful readiness wait, described in [When a step fails](#when-a-step-fails), which can leave a sandbox that was stopped running.

## When a sandbox is outdated

`update` works with the selected toolchain set: the set given with `--with`, or the set recorded on the container when `--with` is not given.

A sandbox is up to date when the selected set equals the recorded set and its container was created from the current image for that set, compared by Podman image ID, and that image is current: for a toolchain image, its `base-image` label equals the ID of the current base image ([Image names and labels](images.md#image-names-and-labels)). `update` on an up-to-date sandbox prints `Sandbox NAME is already up to date.` and exits with status 0. It builds nothing and does not rename, create, start, stop, or remove any container.

Every other sandbox is outdated, including every sandbox for which `--with` gives a set other than the recorded one. An image that is missing, or a toolchain image that was not built on the current base image, as after a `build` that failed to rebuild it, counts as missing. `update` builds a missing image as `up` does, the base image first when it is missing, and may use the layer cache. It does not rebuild an image that exists and is current. To get fresh packages, run `build` first and then `update`.

## What `update` does

1. It checks the command line, the sandbox name, and the toolchain names of `--with`. `update` takes exactly one target, either `NAME` or `--all`. With neither or both, it exits non-zero with a usage message before it calls Podman. The other refusals of `--with` happen at this step too ([Change the toolchain set](#change-the-toolchain-set)).
2. It runs the preflight, as `up` does ([Host prerequisites](host-prerequisites.md)). On Windows, the preflight selects the Podman machine, and every later Podman call names it with `--connection` (ADR-0006). A failing preflight stops `update` before it looks up any sandbox object.
3. It takes the sandbox's [lifecycle lock](#lifecycle-lock).
4. It looks up the sandbox's container, its volumes, and its backup container, and checks their owners. An unknown sandbox name exits non-zero. An owner conflict exits non-zero, names the Podman objects concerned, and points to Podman. A sandbox of which only volumes remain exits non-zero and names `sandboxed-agents up NAME`, which adopts the volumes. A backup container with the current owner, left by an interrupted update, is refused as well: the message names the backup container, says that recovering an interrupted update is not available in this version and that `update` changes nothing, and points to Podman to inspect it ([Not in this version](#not-in-this-version)).
5. It decides whether the sandbox is [outdated](#when-a-sandbox-is-outdated) for the selected toolchain set. A container that reports no image ID is refused. A container without the toolchain label counts as recorded `none`, as for `up`. An up-to-date sandbox ends here.
6. For an outdated sandbox, it reads the configuration recorded on the container and refuses when any part of it is malformed ([Recorded configuration](#recorded-configuration)).
7. It builds the image for the selected set when that image is missing. When the image is not current after the build because it changed while `update` prepared it, `update` stops before the rename, says that the sandbox was not changed, and asks you to retry `sandboxed-agents update NAME`. When you gave `--with`, the retry command it names includes `--with` and the selected set, such as `sandboxed-agents update NAME --with native`. Until the preflight has passed, the recorded configuration has been read, and the build has succeeded, nothing is stopped, renamed, or created.
8. It passes the [session guard](#session-guard): on a running sandbox, it asks the manager for the running agent sessions and refuses when sessions run or the manager does not answer, unless `--force` is given. A refusal can follow a successful build, but never a rename.
9. It renames the container from `sandboxed-agents.GROUP.NAME` to the backup name `sandboxed-agents-backup.GROUP.NAME` while the container keeps running.
10. It creates the new container under `sandboxed-agents.GROUP.NAME` from the ID of the new image, not from its tag, without starting it. Its configuration is the one described in [What `update` keeps](#what-update-keeps), plus a label that records whether the sandbox was running or stopped before the update. Its toolchain label records the selected set.
11. It stops the old container if it runs, and only then starts the new one. That way the new container can publish the same SSH port, and two containers never run on the same volumes. A sandbox that was stopped is started here too, for the readiness wait. With `--force`, stopping the old container ends the agent sessions that run in it, and `update` reports them at the end ([Session guard](#session-guard)).
12. It [waits until the new container is ready](#readiness-wait).
13. It removes the backup container.
14. When the sandbox was stopped before the update, it stops the new container again.

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

## Change the toolchain set

`update NAME --with SET` replaces the sandbox's container with one created from the image for `SET`. It keeps the volumes and the rest of the recorded configuration, so you can add or drop toolchains without losing the sandbox's data.

```text
sandboxed-agents update agent01 --with dotnet
sandboxed-agents update agent01 --with=azure,dotnet
sandboxed-agents update agent01 --with none
```

`SET` takes the same values as `up NAME --with SET`: a comma-separated list of toolchain names, or `none` alone for the base image ([Create or start a sandbox](sandboxes.md#create-or-start-a-sandbox)). Order and repetition of names do not matter. Both `--with SET` and `--with=SET` are accepted.

- **The given set replaces the recorded set.** The new container gets exactly the toolchains you name. `update` does not add them to the recorded set: a toolchain of the recorded set that you do not name is dropped. On a sandbox recorded with `native`, `update agent01 --with dotnet` creates a container with `dotnet` only.
- **`--with none` selects the base image.** The new container has no toolchains.
- **Without `--with`, `update` keeps the recorded set.**
- **The same set on a current image changes nothing.** When the given set equals the recorded set and the sandbox is [up to date](#when-a-sandbox-is-outdated), `update` prints `Sandbox NAME is already up to date.`, exits with status 0, and does not touch the container. When the image is not current, `update` updates the sandbox as `update NAME` does.
- **The image is built before the rename.** When the image for the given set is missing or not current, `update` builds it before it renames the old container ([What `update` does](#what-update-does)). When that build fails, the sandbox stays as it was, with its previous toolchain set.
- **A rollback restores the previous set.** When a later step fails and the rollback completes, the old container is back under its name, in its original running or stopped state, with its previous toolchain set, and the new container is removed ([When a step fails](#when-a-step-fails)). An incomplete rollback gives no such guarantee ([When the rollback fails](#when-the-rollback-fails)).
- **`list` shows the new set.** The new container records the given set in its toolchain label, and `list` reads it from there ([List sandboxes](sandboxes.md#list-sandboxes)).

`update` checks `--with` at step 1 of [What `update` does](#what-update-does). An invalid value or combination fails there, before the preflight, the lookup of the sandbox, and the owner check: `update` exits non-zero, calls no Podman, and changes nothing, even when a prerequisite is missing or the sandbox does not exist. These fail:

- `--with` without a value, or `--with` given more than once;
- an unknown or undelivered toolchain name, or an empty name, such as in `--with nosuch` or `--with native,`. The message lists the valid values, `none` among them;
- `none` combined with another name, such as `--with none,dotnet`;
- `--with` together with `--all` ([Update every sandbox](#update-every-sandbox)).

`update NAME --with SET` passes the [session guard](#session-guard) as `update NAME` does. `--with` and `--force` can be combined.

## Session guard

Replacing the container ends every process in the old one, agent sessions included. `update` therefore refuses while an [agent session](agents.md#keep-an-agent-running-in-a-session) runs in the sandbox, unless you give `--force`.

The guard is the last check before the change: it comes after the preflight, the recorded configuration, and the builds, and directly before the rename ([What `update` does](#what-update-does)). It is the only point at which `update` asks the manager for agent sessions, and its only manager call before the replacement, so a manager that does not answer is reported there as well, after the builds. The [readiness wait](#readiness-wait) later asks the manager of the new container only for its version. On a running sandbox, `update` asks the manager in the container for the running agent sessions with the session query that `stop` and `restart` use ([Session guard](sandboxes.md#session-guard)).

| Manager answer | Without `--force` | With `--force` |
| --- | --- | --- |
| No running session | updates the sandbox | updates the sandbox |
| Running sessions | refuses: `sandbox NAME has running agent sessions: SESSIONS; update changed nothing in the sandbox; use --force to end them and update it` | updates the sandbox and reports `Stopping the old container of sandbox NAME ended these agent sessions: SESSIONS.` |
| No answer | refuses: `cannot rule out running agent sessions in sandbox NAME: DETAIL; update changed nothing in the sandbox; use --force to update it anyway` | updates the sandbox and reports `Stopping the old container of sandbox NAME ended agent sessions that may have been running; they cannot be named because the manager did not answer.` |

`SESSIONS` lists every running session as `NAME (AGENT)`, separated by commas, in the order the manager reported them. `DETAIL` says why the answer is missing, such as the timeout, the exit status of the query, or an invalid answer. A refusal exits non-zero. Images that `update` built before the guard remain, but it renames, stops, creates, and removes no container.

The guard applies only when `update` would replace the container:

- **Already up to date.** An [up-to-date](#when-a-sandbox-is-outdated) sandbox ends before the guard. `update` prints `Sandbox NAME is already up to date.` and exits with status 0, also while an agent session runs, and asks the manager nothing.
- **Stopped sandbox.** A stopped sandbox has no running agent session. `update` asks the manager nothing and does not refuse.

`--force` does not skip the query: `update` still asks the manager so that it can name the sessions it ends, and only when the manager does not answer does it proceed without that information. `--force` ends no session itself, and `update` makes no call that ends a session. The sessions keep running through the rename and the creation of the new container, and end only when the old container is stopped ([What `update` does](#what-update-does)).

`update` reports the sessions after every attempt to stop the old container of a running sandbox, and only at the end, after the replacement attempt and any rollback have finished, so the report never interrupts the replacement or the rollback. Only a successful `podman stop` confirms that the sessions ended, so only then does the report say that they ended, in the form shown in the table above. When the stop fails, the report says only that the sessions may have ended (see **Failure of the stop** below). `update` prints the report on standard output; after a successful update it follows `Sandbox NAME is updated.` Every report names the sandbox, which tells the reports of `update --all` apart. `update` prints no report when the manager answered and reported no running session, also after a failed stop, and none for a sandbox that was stopped before the update. The report does not mean that the update succeeded: it also follows a rollback, and a failed removal of the backup container after a successful readiness wait.

A rollback does not restart agent sessions ([When a step fails](#when-a-step-fails)):

- **Failure before the stop.** When the build, the rename, or the creation of the new container fails, the old container was never stopped. Its agent sessions keep running, and `update` names no ended session.
- **Failure of the stop.** When `podman stop` of the old container fails, `update` rolls back. A failed stop can still have ended processes in the old container, so `update` cannot tell whether the sessions ended or survived, and its report claims neither. After the rollback it reports `Stopping the old container of sandbox NAME failed, so these agent sessions may have ended: SESSIONS; any that ended were not restarted.` When the manager did not answer, it reports `Stopping the old container of sandbox NAME failed, so agent sessions that may have been running may have ended; they cannot be named because the manager did not answer, and any that ended were not restarted.`
- **Failure after the stop.** When the start of the new container or the readiness wait fails, the rollback brings back the original container, but the sessions that ended with the old container stay ended, and the report after the rollback names them. "As it was" covers the container, the volumes, and the configuration, not the processes.

## Update every sandbox

`sandboxed-agents update --all` updates the sandboxes of the current controller group one after another, each as `update NAME` does. It never touches or names a sandbox of another controller group.

`update --all --force` applies `--force` to every sandbox: it treats each as `update NAME --force` does ([Session guard](#session-guard)).

`update --all` keeps the recorded toolchain set of each sandbox. It does not take `--with`: `update --all --with native` exits non-zero with a usage message before it calls Podman, and changes nothing. To change the toolchain set, run `update NAME --with SET` for one sandbox at a time ([Change the toolchain set](#change-the-toolchain-set)).

1. It runs the preflight, as `update NAME` does.
2. It finds the names of the group's sandboxes in `podman ps --all --format json` and `podman volume ls --format json`: every name with a container, a backup container, or volumes. It uses these lists for the names only, never as the state of a sandbox (ADR-0007).
3. It takes the [lifecycle lock](#lifecycle-lock) of each name. A sandbox whose lock another lifecycle command holds is passed over, reported, and counted as failed.
4. With its lock held, it inspects each sandbox as `update NAME` does and sorts it:
   - **Owner conflict:** passed over, reported as an owner conflict that you resolve with Podman, and counted as not updated. No image is built for it.
   - **Only volumes remain:** passed over with a message that names `sandboxed-agents up NAME`, which adopts the volumes. No image is built for it, and it does not count as a failure.
   - **Backup container of an interrupted update:** refused as `update NAME` refuses it ([Not in this version](#not-in-this-version)), and counted as failed.
   - **Already up to date:** reported as such right away, before any build, and not touched.
   - **No longer exists:** a sandbox whose objects disappeared after the name inventory is reported with the same `does not exist in this controller group` message as for `update NAME`, and counted as failed.
   - **Any other refusal**, such as a malformed recorded configuration: reported and counted as failed.
   - **Outdated:** planned for the update.
5. It builds each missing image once, at most one per distinct toolchain set of the planned sandboxes, as described in [When a sandbox is outdated](#when-a-sandbox-is-outdated). An existing current image is reused. A build failure stops `update --all` before any sandbox is changed: it reports the build error, exits non-zero, and updates no sandbox. Up-to-date sandboxes have already been reported by then.
6. Only after all builds have succeeded, it updates the planned sandboxes one after another, from the [session guard](#session-guard) on, as `update NAME` does. A failure rolls back that sandbox ([When a step fails](#when-a-step-fails)) and does not stop the others. A stopped sandbox is updated too and stays stopped.
   - **Running agent sessions:** without `--force`, a sandbox with a running agent session is skipped at its session guard, reported with its sessions, and counted as not updated. A sandbox whose manager does not answer is skipped and reported in the same way. `update` changes nothing in a skipped sandbox and continues with the next one. With `--force`, the report of ended sessions for each sandbox follows that sandbox's own update or rollback and names the sandbox.

`update --all` holds every lock it has taken until it exits, through all builds and every update, so other lifecycle commands on those sandboxes are refused meanwhile. The output reports each sandbox: updated, already up to date, restored after a failure, passed over, or failed. The exit status is 0 when every sandbox was updated or already up to date, apart from those of which only volumes remain. When a build fails, the build error is the final message. Otherwise, when any sandbox was passed over, failed, or not updated, including a sandbox skipped at its session guard, a backup container that could not be removed and a sandbox that could not be stopped again after its update, a final summary says that one or more sandboxes could not be updated and that each is reported in its own message, and the exit status is non-zero.

## When a step fails

A failed `update` names the failed step, such as `create the new container` or `readiness wait of the new container`, and exits non-zero. When a step up to the readiness wait fails and the rollback completes, it leaves the sandbox as it was: the original container under its name, running when it was running before and stopped when it was stopped. That covers the container, the volumes, and the configuration, including the toolchain set recorded before an `update NAME --with SET`, not the processes that ran in the old container. An incomplete rollback gives no such guarantee ([When the rollback fails](#when-the-rollback-fails)).

| Failed step | What `update` does |
| --- | --- |
| Preflight, a check, the build, or the session guard | Stops there. It renames, stops, creates, and removes nothing, and a running sandbox keeps running. |
| Rename to the backup name | Nothing has changed. A running sandbox keeps running. |
| Creation of the new container | Removes whatever exists of the new container and renames the backup container back. It never stopped the old container and does not start it, so a running sandbox keeps running throughout, and so do its agent sessions. |
| Stop of the old container, start of the new one, or the readiness wait | Removes the new container, renames the backup container back, and starts it when the sandbox was running before. A sandbox that was stopped stays stopped, and no start call is issued for it. Agent sessions that ended with the old container are not restarted. |
| Removal of the backup container | No rollback; see [Backup container left after a successful update](#backup-container-left-after-a-successful-update). |
| Stop that ends the update of a sandbox that was stopped | No rollback; see [Sandbox left running after a successful update](#sandbox-left-running-after-a-successful-update). |

The rollback removes the new container with `podman rm --force --ignore`, which also succeeds when creation left no container behind, and always before it starts the original container again, so two containers never run on the same volumes. After a complete rollback, `update` prints `Sandbox NAME was restored`, says that the original container is back under its name and running or stopped as before, and that processes that ended during the update were not restarted. The report keeps the original failure.

The rollback calls run with their own time limit of 30 seconds in total, independent of the failed step, so a step that ended because its own time ran out or was cancelled does not cut the rollback short. A rollback renames, removes, and starts containers only. It deletes no named volume, changes nothing in the volumes, makes no call to the manager, and leaves an installed SSH setup and every file in your SSH directory unchanged.

### When the rollback fails

When a rollback call fails, `update` stops the rollback at once. It does not try further steps, so it never starts the original container while the new one may still exist. It reports that the rollback of the sandbox is incomplete, names the rollback step that failed, such as `remove the new container`, `rename the backup container back`, or `start the restored container`, and keeps the original failure in the report. It does not claim that the sandbox was restored. The original container is kept: under the backup name when the rollback failed before renaming it back, otherwise under the sandbox's name. Its running state is not promised, because the failed stop, start, or rollback call can have had an effect before it failed. Inspect the current state of the sandbox's containers with Podman. While the backup container remains, the sandbox counts as "update interrupted" ([Not in this version](#not-in-this-version)).

### Backup container left after a successful update

When the readiness wait succeeded but removing the backup container fails, the update itself succeeded and is not rolled back. The new container runs under the sandbox's name. `update` prints a warning and exits non-zero. The warning says that the update succeeded, names the backup container, says that the sandbox counts as "update interrupted" until the backup container is removed and that other commands refuse it, and that `sandboxed-agents update NAME` cleans up. In this version, however, `update` cannot clean up yet: it refuses a sandbox with a backup container, and the warning says so and points to Podman. Recovery through `update` comes with #54.

For a sandbox that was stopped before the update, `update` does not stop the new container, because that stop comes only after the backup container was removed. The warning also says that the new container is still running.

### Sandbox left running after a successful update

For a sandbox that was stopped before the update, the new container is stopped again as the last step, after the backup container was removed. When that stop fails, the update counts as done and is not rolled back, and no backup container is left. `update` warns that the sandbox is still running, names `sandboxed-agents stop NAME`, and exits non-zero.

## Readiness wait

After it starts the new container, `update` waits until both of these checks have succeeded once:

- **Manager.** The manager answers its existing version query, `podman exec --user=0:0 CONTAINER /usr/local/bin/sandboxed-agents-manager version`, which runs as container root (ADR-0006).
- **SSH.** On the host, `ssh-keyscan -T 1 -t ed25519 -p PORT 127.0.0.1` returns an Ed25519 host key for `[127.0.0.1]:PORT`, where `PORT` is the SSH port recorded on the container. A host key arrives only after sshd in the container has completed the SSH key exchange. An open TCP port is not enough, because rootless Podman's port forwarding can accept a connection before sshd listens. `ssh-keyscan` does not authenticate, uses no key of yours, and reads and writes no file in your SSH directory or in host state. It comes with the OpenSSH client and is part of the host prerequisites that every preflight checks, also for commands that do not run it ([Host prerequisites](host-prerequisites.md), ADR-0007).

The wait lasts at most 60 seconds, including the time the probes take to run. The first attempt runs both checks right after the start. After that, every 250 milliseconds, it repeats only the checks that have not succeeded yet. Each probe has a timeout of 3 seconds, cut to the time left; `ssh-keyscan` also gives up after 1 second without an answer. When the wait ends before both checks have succeeded, `update` reports `readiness wait for sandbox container CONTAINER failed`, followed by the reason and the last result of each check that had not succeeded, [rolls back](#when-a-step-fails), and exits non-zero.

On a cold start, the Podman machine, the container's initialization, the manager, and sshd can take several seconds to answer. 60 seconds leaves room for that and still bounds how long a failed update keeps you waiting (ADR-0007). Only offline tests check this value. No live run has measured it yet.

## Lifecycle lock

The lifecycle commands `up`, `start`, `stop`, `restart`, `remove`, and `update` never act on the same sandbox at the same time (ADR-0007). Each one takes an exclusive lock for its sandbox after its usage checks and its preflight or Windows target selection, and before it looks up the sandbox. `update --all` takes one lock per sandbox name after it has found the names ([Update every sandbox](#update-every-sandbox)). Every lifecycle command, `update --all` included, holds each lock it has taken until it exits, through every check, build, and Podman call, and through the installation or removal of an SSH setup. When the lock is taken, the second command does not wait: it exits non-zero, names the sandbox, says that another lifecycle command is in progress for it, and asks you to retry. The operating system releases the lock when the command exits or its process dies, so a crashed command leaves no stale lock behind.

The lock is one empty file per sandbox in `group-GROUP/locks/` in [host state](sandboxes.md#host-state). Its name is a hash of the sandbox's Podman container name. A lifecycle command creates this directory and the file when they are missing and never deletes them, so one empty file remains for every sandbox name a lifecycle command has run for. The file holds no update progress, no previous running state, and no configuration. Apart from this file, `update` writes nothing in host state. On Windows, a lifecycle command fails when `LOCALAPPDATA` is not set or is not an absolute path, as every command that needs host state does.

Know the limits of this lock:

- **Same host state root only.** Two commands share a lock only when they resolve the same host state directory. A command run with another `XDG_STATE_HOME` on Linux, or another `LOCALAPPDATA` on Windows, locks a different file and is not coordinated with commands that use the default. Run every lifecycle command for a sandbox with the same state directory.
- **Own commands only.** The lock serializes this executable's lifecycle commands for one host user. Writers that use Podman directly, such as your own `podman` calls or scripts, do not take it, and `update` cannot detect or prevent their changes. Commands of other host users use their own Podman and host state.
- **Coordination, not security.** Any program with your Podman authority can ignore the lock, delete the lock file, or change a sandbox's objects directly.
- **Per sandbox only.** Commands on different sandbox names, or on the same name in different controller groups, run in parallel. Image builds and SSH port allocation are shared across sandboxes and are not serialized by this lock.

When a Podman call fails unexpectedly, for example because something outside the executable removed or renamed a container, `update` does not retry it. From the rename on, it handles the failure as described in [When a step fails](#when-a-step-fails), and a rollback can then fail as well.

## Not in this version

- **Interrupted updates** (#54). Recovering a sandbox from a backup container that an earlier update left behind: after an interruption such as Ctrl+C, after a failed backup removal, or after an incomplete rollback. Every other command that takes the sandbox name refuses such a sandbox and names `sandboxed-agents update NAME` ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)). In this version `update` refuses such a sandbox with its own message: recovering an interrupted update is not available yet, `update` changes nothing, and you can inspect the backup container with Podman. It neither completes nor undoes the interrupted update, and `update --all` passes such a sandbox over in the same way; recovery through `update` comes with #54.
- **Rollback against real Podman** (#55). No live run has exercised a rollback yet.

## Verification

`update` adds no new manager functionality: the readiness wait uses the manager's existing version query, and the session guard uses the manager's existing session query. The existing manager tests cover both queries. Offline tests cover the rest.

- **Readiness.** The readiness tests run the manager and `ssh-keyscan` probes through injected process functions. Readiness failures run the full 60-second deadline against a manager that never answers on a running sandbox and against an `ssh-keyscan` that never answers on a stopped and on a running sandbox.
- **Steps and rollback.** The CLI tests run the executable against fake Podman and a fake `ssh-keyscan`, and check the order and arguments of the Podman calls, the already-up-to-date case, the refusals, and the lifecycle lock. They inject a failure at each step, from the preflight, the build, and the rename through the creation, the stop of the old container, the start of the new one, each readiness probe, the removal of the backup container, and the final stop, and check the rollback calls and their order, the warnings, and that no call removes a volume and no SSH setup file changes. Further tests drive `update` through its public entry point, cancel the caller's context during each readiness probe of a stopped sandbox, and check that the rollback still runs with its own limit of at most 30 seconds and restores the sandbox.
- **`update --all`.** The CLI tests cover the builds before the first rename, at most one per toolchain set, the sequential updates, an up-to-date sandbox, including its report when a later build fails, a failure in one sandbox that does not stop the next, a build failure that changes no sandbox, a sandbox of another controller group, a sandbox of which only volumes remain, an owner conflict, a sandbox that disappeared after the name inventory, and the usage errors. They also cover the locks: a busy sandbox is passed over, each lock is taken before its sandbox is inspected, the locks are held through all builds and updates, and they are released when the command ends, after success and after a build failure.
- **`update NAME --with SET`.** The CLI tests cover the replacement of the recorded set with the same volumes and configuration, `--with none`, both forms of the option, and repeated names. They cover the build of a missing or stale image before the rename, a build failure that leaves the original sandbox, and a failed creation, stop of the old container, or start whose rollback renames the original container, with its previous toolchain label, back. The tests that cancel the caller's context during each readiness probe also run with `--with`, and a test checks that the retry command after an image change keeps `--with` and the selected set. They cover the already-up-to-date case for an equal set given in another order, a new toolchain label when the selected image has the same ID as the old one, and the refusals before any Podman call or host query: an unknown name, also for a sandbox that does not exist, an empty name, `none` with another name, a missing value, a repeated option, and `--all` with `--with`.
- **Session guard.** The CLI tests run, most of them for a Linux and a Windows host, with a fake session query that reports running sessions, no session, or no answer: a failed query, an answer that is not JSON, `null`, or a session without an agent. They check that a refusal follows the image build, ends with the session query, and issues no rename, stop, creation, removal, or readiness check. With `--force`, they check that `update` issues the session query once, makes no other manager call than that query and the version query, issues the container calls in the order rename, create, stop, start, and removal of the backup, and reports the ended sessions, or that they cannot be named; one of these tests combines `--force` with `--with=none`. Injected failures of the rename, the creation, the stop of the old container, and the start of the new one, each with known and unknown sessions, check the rollback calls and that the rollback makes no manager call. After a failed rename or creation, no session is reported. After a failed stop, the report names the sandbox, says that the sessions may have ended and that any that ended were not restarted, and names them, or says that they cannot be named. After a failed start, the report names the sandbox and the ended sessions, or says that they cannot be named. A readiness failure checks that the rollback restores the old container and the report names the ended sessions. Further tests cover an up-to-date running and an outdated stopped sandbox, with and without `--force`, which issue no session query; an update without sessions, whose output and calls are the same with and without `--force`; `update --all` with three outdated sandboxes, one of which has a running session or a manager that does not answer, with and without `--force`, and its session queries after all builds; and a repeated `--force`, `--force=true`, and `--force` with `--all` and `NAME` or with `--all --with`, which fail with a usage message before any Podman call.

These tests do not show that an update, a rollback, or the session guard works against real Podman, a real manager, a real agent session, or a real sshd. No live run covers `update` yet; that coverage belongs to the live suite (#24, #55, [Live suite](live-suite.md)).
