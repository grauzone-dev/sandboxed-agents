# Host prerequisites

`sandboxed-agents` runs each sandbox as a rootless Podman container. This page lists what a host needs and describes `sandboxed-agents check`, the preflight that reports missing prerequisites before a sandbox or image is built.

The preflight is currently implemented for Linux only. On any other operating system, `check` reports one unmet item and checks nothing else:

```text
MISSING: Linux host: check currently supports Linux hosts only
```

Windows hosts, which run Podman in a WSL2 machine, get their own preflight in a separate Story.

## Run the check

```sh
sandboxed-agents check
```

`check` takes no arguments or options. It prints one line per prerequisite:

```text
OK: podman
MISSING: pasta: install pasta (the passt package) and make sure it is on PATH
```

If any prerequisite is missing, `check` prints `sandboxed-agents: host prerequisites are missing` on standard error and exits with status 1. When every prerequisite is met, it exits with status 0.

An extra word on the command line is a usage error. A word starting with `-` is reported as an unknown option, any other word as an unexpected argument. In both cases the usage line follows, nothing is checked, and the exit status is 1:

```text
sandboxed-agents: unknown option "--json"
Usage: sandboxed-agents check
```

## What the check does to the host

The check reads files and looks up programs. It does not create, change, or remove host files, and it never runs `sudo`. It tests write permission on cgroup control files by asking the kernel (`access(2)`) without writing to them. It opens no SSH connection.

**One exception.** The check runs `podman --version`, and only that: it does not call `podman info`, `build`, `create`, or any other Podman command. Even for `--version`, Podman initializes its rootless configuration and can create or adjust its own per-user directories:

- `~/.config`, when `XDG_CONFIG_HOME` is unset.
- A runtime directory, when `XDG_RUNTIME_DIR` is unset.
- `$XDG_RUNTIME_DIR/containers`.
- `$XDG_RUNTIME_DIR/libpod`. Podman resets its permissions to `0700` with the sticky bit.

If the administrator has enabled Podman pre-exec hooks, `podman --version` runs those as well. Podman 4.4.0 and 5.0.0 offer no option that skips this initialization. These are Podman's own side effects of an informational query, not changes made by `sandboxed-agents`. The project accepts them as the one permitted exception to a read-only preflight.

## Prerequisites

Each row is one line of `check` output. **Name** is the name the check prints, and **Remedy** is the text it prints after a `MISSING` name.

| Name | Met when | Remedy |
| --- | --- | --- |
| `podman` | `podman` is on `PATH`. | install Podman and make sure podman is on PATH |
| `Podman version` | `podman --version` prints `podman version X.Y.Z` with X.Y.Z at least 4.4.0. Pre-releases of 4.4.0, such as `4.4.0-rc1`, do not count. | install Podman 4.4.0 or newer |
| `rootless` | The check runs with a nonzero effective user ID. | run sandboxed-agents as your own user, not as root or with sudo |
| `subordinate UID` | `/etc/subuid` has a line for your user name or numeric UID with a valid range: start above 0, size above 0, end within the 32-bit ID space. | add a subordinate UID range for your user to /etc/subuid |
| `subordinate GID` | `/etc/subgid` has a line for your user name or numeric **UID** with a valid range. Podman looks up subordinate GIDs by user, not by group. | add a subordinate GID range for your user to /etc/subgid |
| `newuidmap` | `newuidmap` is on `PATH`. | install newuidmap (in the uidmap or shadow-utils package) and make sure it is on PATH |
| `newgidmap` | `newgidmap` is on `PATH`. | install newgidmap (in the uidmap or shadow-utils package) and make sure it is on PATH |
| `pasta` | `pasta` is on `PATH`. | install pasta (the passt package) and make sure it is on PATH |
| `cgroups v2` | `/proc/self/cgroup` has a unified-hierarchy entry (`0::/…`), and `/proc/self/mountinfo` has a cgroup2 mount that contains it. | boot the host with the unified cgroup v2 hierarchy |
| `CPU` | The `cpu` controller is delegated to you (see below). | delegate the cgroup v2 cpu controller to your user, for example with Delegate= in a user@.service drop-in |
| `memory` | The `memory` controller is delegated to you. | delegate the cgroup v2 memory controller to your user, … |
| `process` | The `pids` controller is delegated to you. | delegate the cgroup v2 pids controller to your user, … |
| `ssh` | `ssh` is on `PATH`. | install the OpenSSH client and make sure ssh is on PATH |
| `ssh-keygen` | `ssh-keygen` is on `PATH`. | install the OpenSSH client and make sure ssh-keygen is on PATH |

To add subordinate ranges, run `usermod --add-subuids FIRST-LAST USER` and `usermod --add-subgids FIRST-LAST USER` from shadow-utils as an administrator.

`ssh` and `ssh-keygen` are needed by `sandboxed-agents` itself, not by Podman (ADR-0002).

### cgroup controller delegation

Rootless Podman can apply `--memory`, `--cpus`, and `--pids-limit` only when the cgroup v2 controllers `memory`, `cpu`, and `pids` are delegated to the user. Podman documents `--memory` and `--cpus` as unsupported on rootless cgroup v1 ([`--memory`](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/memory.md#L15), [`--cpus`](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/cpus.container.md#L15), v5.0.0).

To find a delegated cgroup, the check maps its own cgroup from `/proc/self/cgroup` onto the cgroup2 mount point from `/proc/self/mountinfo`. It then examines that cgroup and each parent up to the mount root. A login shell normally runs in a session scope such as `user-1000.slice/session-3.scope`. systemd delegates to the user manager `user-1000.slice/user@1000.service`, which is a sibling of that scope rather than a parent. So when the check passes `user-UID.slice`, it also examines `user@UID.service`.

The first cgroup that meets all four of these conditions is the delegated one:

- its `cgroup.controllers` is readable;
- you have write permission on its directory, which creating child cgroups needs;
- you have write permission on its `cgroup.procs`;
- you have write permission on its `cgroup.subtree_control`.

`CPU`, `memory`, and `process` are reported as met when that cgroup's `cgroup.controllers` lists `cpu`, `memory`, and `pids`. If no cgroup qualifies, all three are reported missing.

On systemd hosts, the usual fix is the drop-in that Podman's troubleshooting guide gives, `/etc/systemd/system/user@.service.d/delegate.conf`:

```ini
[Service]
Delegate=memory pids cpu cpuset
```

Then log out and back in. You can check the result with:

```sh
cat "/sys/fs/cgroup/user.slice/user-$(id -u).slice/user@$(id -u).service/cgroup.controllers"
```

## Why Podman 4.4.0

The sandbox container uses these Podman options:

| Option | Available since | Source |
| --- | --- | --- |
| `--network=pasta:--no-map-gw` | 4.4.0 | [4.4.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L721), [podman-run(1) v4.4, `--network`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--userns=keep-id:uid=UID,gid=GID` | 4.3.0 | [4.3.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L844), [podman-run(1) v4.4, `--userns`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--security-opt=no-new-privileges` | before 4.4.0 | [podman-run(1) v4.4, `--security-opt`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--memory`, `--cpus`, `--pids-limit`, `--shm-size` | before 4.4.0 | [podman-run(1) v4.4](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |

Pasta sets the floor, so 4.4.0 is the earliest release that supports every option. Podman 5.0.0 did not add pasta. It changed the *default* rootless network tool from slirp4netns to pasta ([5.0.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L41)). `sandboxed-agents` selects pasta explicitly, so it does not depend on that default and does not require 5.0.0.

`--no-map-gw` keeps the container from reaching the host through the gateway address. Podman passes it to pasta by default unless `--map-gw` is given ([network option, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/network.md#L41-L55)). It is not a firewall against other host addresses the container can route to.

The floor describes feature support only. Whether a 4.4.x release still receives security fixes depends on your distribution.

## Limits of the check

The check confirms host configuration. It does not start a container, so a host that passes can still fail when a sandbox starts. Nothing in this page has been confirmed by starting a container on a live host.

The check does not:

- **Look for `pasta` in Podman's helper directories.** Podman searches `helper_binaries_dir` (for example `/usr/libexec/podman`) before `PATH` ([`FindHelperBinary`, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/common/pkg/config/config.go#L1046-L1095)). A `pasta` found only there is reported missing.
- **Ask NSS for subordinate IDs.** Podman built with `libsubid` can get subordinate ID ranges from an NSS `subid` provider, such as SSSD or FreeIPA, instead of `/etc/subuid` and `/etc/subgid` ([idtools, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/pkg/idtools/idtools_supported.go)). The check reads only the files, so such hosts are reported as missing their ranges.
- **Validate subordinate ID files fully.** It does not reject malformed lines that Podman would refuse to parse. It does not check that a range avoids your own UID or GID, or that it is large enough for the IDs an image uses.
- **Prove the mapping helpers work.** It does not check that `newuidmap` and `newgidmap` have the setuid bit or file capabilities they need.
- **Prove user namespaces are allowed.** It does not read `user.max_user_namespaces` or `kernel.unprivileged_userns_clone`, and it does not evaluate AppArmor or SELinux policy. "Rootless" means only that you are not root.
- **Prove a future cgroup write succeeds.** The write-permission test is taken at one moment. The check does not test the cgroup manager Podman uses, or controllers enabled further down the delegated subtree. A delegated cgroup that is not the user manager and not a parent of the current cgroup is not found.
- **Check the pasta version** or whether pasta accepts the options Podman passes.
- **Check a remote Podman.** If `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or a `containers.conf` connection points Podman at another machine, the check still describes only the local host.

## Sources

- Podman: [4.4.0 release](https://github.com/containers/podman/releases/tag/v4.4.0), [5.0.0 release](https://github.com/containers/podman/releases/tag/v5.0.0), [podman-run(1) v4.4](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html), [troubleshooting §26, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/troubleshooting.md#L694-L737)
- `podman --version` start-up: [root.go](https://github.com/containers/podman/blob/v5.0.0/cmd/podman/root.go#L90-L100), [registry/config.go](https://github.com/containers/podman/blob/v5.0.0/cmd/podman/registry/config.go#L80-L169), [homedir_unix.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/pkg/homedir/homedir_unix.go#L107-L175), [default.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/common/pkg/config/default.go#L496-L520), [storage options.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/types/options.go#L288-L307), [pre-exec hooks](https://github.com/containers/podman/blob/v5.0.0/pkg/rootless/rootless_linux.c#L255-L269), all at v5.0.0
- Linux kernel: [cgroup v2 delegation, v6.8](https://www.kernel.org/doc/html/v6.8/admin-guide/cgroup-v2.html#delegation)
- systemd: [Control Group APIs and Delegation](https://systemd.io/CGROUP_DELEGATION/)
