# Host prerequisites

`sandboxed-agents` runs each sandbox as a rootless Podman container. This page lists what a host needs and describes `sandboxed-agents check`, the preflight that reports missing prerequisites before a sandbox or image is built.

`check` detects the host operating system and runs the [Linux](#linux) or the [Windows](#windows) preflight. Windows hosts run Podman in a WSL2 machine. On any other operating system, `check` reports that no preflight is available for that system yet. It checks nothing else and exits with a nonzero status.

`check` only reports the host prerequisites. `sandboxed-agents build` and `sandboxed-agents up` already run the same preflight for the host. `build` runs it before it writes the build context, and `up` runs it before it looks up or creates any sandbox object. Both commands stop if a required prerequisite is missing or, on Windows, could not be checked. A later Story will make `update` run the preflight as well. The preflight itself only reads, apart from the Linux exception described in [What the check does to the host](#what-the-check-does-to-the-host), and it never repairs a missing prerequisite. The temporary build context that `build` creates and removes is part of the build, not of the preflight.

## Run the check

```sh
sandboxed-agents check
```

`check` takes no arguments or options, so it doesn't take a sandbox name yet. It checks every prerequisite in one run and prints one line per prerequisite. The status words depend on the host:

- On Linux, each line starts with `OK:` or `MISSING:`.
- On Windows, each line starts with `ok:`, `missing:`, or `unknown:`. `unknown:` means the prerequisite could not be checked.

An extra word on the command line is a usage error. A word starting with `-` is reported as an unknown option, any other word as an unexpected argument. In both cases the usage line follows, nothing is checked, and the exit status is 1:

```text
sandboxed-agents: unknown option "--json"
Usage: sandboxed-agents check
```

## Linux

On Linux, `check` prints lines such as:

```text
OK: podman
MISSING: pasta: install pasta (the passt package) and make sure it is on PATH
```

If any prerequisite is missing, `check` prints `sandboxed-agents: host prerequisites are missing` on standard error and exits with status 1. When every prerequisite is met, it exits with status 0.

### What the check does to the host

The check reads files and looks up programs. It may also query account data through `getent` (see [Prerequisites](#prerequisites)). It runs every program directly, without a shell. It does not create, change, or remove host files, and it never runs `sudo`. It tests write permission on cgroup control files by asking the kernel (`access(2)`) without writing to them. It opens no SSH connection.

**One exception.** The check runs `podman --version`, and only that: it does not call `podman info`, `build`, `create`, or any other Podman command. Even for `--version`, Podman initializes its rootless configuration and can create or adjust its own per-user directories:

- `~/.config`, when `XDG_CONFIG_HOME` is unset.
- A runtime directory, when `XDG_RUNTIME_DIR` is unset.
- `$XDG_RUNTIME_DIR/containers`.
- `$XDG_RUNTIME_DIR/libpod`. Podman resets its permissions to `0700` with the sticky bit.

If the administrator has enabled Podman pre-exec hooks, `podman --version` runs those as well. Podman 4.4.0 and 5.0.0 offer no option that skips this initialization. These are Podman's own side effects of an informational query, not changes made by `sandboxed-agents`. The project accepts them as the one permitted exception to a read-only preflight.

### Prerequisites

Each row is one line of `check` output. **Name** is the name the check prints, and **Remedy** is the text it prints after a `MISSING` name.

| Name | Met when | Remedy |
| --- | --- | --- |
| `podman` | `podman` is on `PATH`. | install Podman and make sure podman is on PATH |
| `Podman version` | `podman --version` succeeds and prints `podman version X.Y.Z` with X.Y.Z at least 4.4.0. Pre-releases of 4.4.0, such as `4.4.0-rc1`, do not count. | When the reported version is too old: install Podman 4.4.0 or newer. When the query fails or its output cannot be read: fix the Podman installation or configuration so that podman --version reports version 4.4.0 or newer |
| `rootless` | The check runs with a nonzero effective user ID. | run sandboxed-agents as your own user, not as root or with sudo |
| `subordinate UID` | `/etc/subuid` has a line for your user name or numeric UID with a valid range: start above 0, size above 0, end within the 32-bit ID space. | add a subordinate UID range for your user to /etc/subuid |
| `subordinate GID` | `/etc/subgid` has a line for your user name or numeric **UID** with a valid range. Podman looks up subordinate GIDs by user, not by group. | add a subordinate GID range for your user to /etc/subgid |
| `newuidmap` | `newuidmap` is on `PATH`. | install newuidmap (in the uidmap or shadow-utils package) and make sure it is on PATH |
| `newgidmap` | `newgidmap` is on `PATH`. | install newgidmap (in the uidmap or shadow-utils package) and make sure it is on PATH |
| `pasta` | `pasta` is on `PATH`. | install pasta (the passt package) and make sure it is on PATH |
| `cgroups v2` | `/proc/self/cgroup` has only the unified-hierarchy entry (`0::/…`) and no cgroup v1 entries, and `/proc/self/mountinfo` has a cgroup2 mount that contains it. A hybrid host, with v1 controllers next to a unified mount, does not count. | boot the host with the unified cgroup v2 hierarchy |
| `CPU` | The `cpu` controller is delegated to you (see below). | delegate the cgroup v2 cpu controller to your user, for example with Delegate= in a user@.service drop-in |
| `memory` | The `memory` controller is delegated to you. | delegate the cgroup v2 memory controller to your user, … |
| `process` | The `pids` controller is delegated to you. | delegate the cgroup v2 pids controller to your user, … |
| `ssh` | `ssh` is on `PATH`. | install the OpenSSH client and make sure ssh is on PATH |
| `ssh-keygen` | `ssh-keygen` is on `PATH`. | install the OpenSSH client and make sure ssh-keygen is on PATH |

To match subordinate ID lines, the check resolves your user name once from your effective UID:

1. It looks up the UID in `/etc/passwd`.
2. If the UID is not there, as for an account from SSSD, LDAP, or systemd-homed, it runs `getent passwd UID`, but only if `getent` is on `PATH`. It accepts the answer only if it is one passwd record with seven fields, a nonempty name, and exactly your UID.

If neither step gives a name, the user name stays unknown. Lines keyed by your numeric UID still match, but lines keyed by a user name are not trusted. glibc lets environment variables make a program write trace and profile files:

- `getent` calls `mtrace`, which writes a malloc trace to the file named by `MALLOC_TRACE` ([getent.c](https://github.com/bminor/glibc/blob/glibc-2.39/nss/getent.c#L977-L1003), [mtrace-impl.c](https://github.com/bminor/glibc/blob/glibc-2.39/malloc/mtrace-impl.c#L167-L199)).
- The dynamic loader creates debug output files for `LD_DEBUG` and `LD_DEBUG_OUTPUT` ([rtld.c](https://github.com/bminor/glibc/blob/glibc-2.39/elf/rtld.c#L2725-L2746)).
- The loader creates profile files for `LD_PROFILE` and `LD_PROFILE_OUTPUT`. When `LD_PROFILE_OUTPUT` is unset, it falls back to a default directory such as `/var/tmp` ([dl-profile.c](https://github.com/bminor/glibc/blob/glibc-2.39/elf/dl-profile.c#L318-L329), [ld.so(8)](https://man7.org/linux/man-pages/man8/ld.so.8.html)).

All three sources are glibc 2.39. The check removes these five variables, both the switches and the destinations, from the environment of `getent` only. It passes the rest of your environment unchanged and does not change the environment of `podman --version`. The check never takes the name from environment variables such as `USER`. `getent` is optional and not a prerequisite. Without it, ranges keyed by your numeric UID are still found.

If `podman` is not on `PATH`, the check does not run `podman --version` and reports both `podman` and `Podman version` as missing.

To add subordinate ranges, run `usermod --add-subuids FIRST-LAST USER` and `usermod --add-subgids FIRST-LAST USER` from shadow-utils as an administrator.

`ssh` and `ssh-keygen` are needed by `sandboxed-agents` itself, not by Podman (ADR-0002).

#### cgroup controller delegation

Rootless Podman can apply `--memory`, `--cpus`, and `--pids-limit` only when the cgroup v2 controllers `memory`, `cpu`, and `pids` are delegated to the user. Podman documents `--memory` and `--cpus` as unsupported on rootless cgroup v1 ([`--memory`](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/memory.md#L15), [`--cpus`](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/cpus.container.md#L15), v5.0.0).

To find a delegated cgroup, the check maps its own cgroup from `/proc/self/cgroup` onto the cgroup2 mount point from `/proc/self/mountinfo`. On systemd hosts, delegation to a user happens at the user manager, `user-UID.slice/user@UID.service`.

A login shell runs in a session scope such as `user-1000.slice/session-3.scope`, which is a sibling of the user manager. A terminal started by the desktop often runs in a leaf below the user manager, such as `user@1000.service/app.slice/…`. A leaf like that may list fewer controllers than the user manager does. So when your cgroup lies under `user-UID.slice`, the check examines your user manager first. It then examines your own cgroup and each parent up to the mount root.

If the host is not unified (see `cgroups v2` above), the check looks for no delegated cgroup. `CPU`, `memory`, and `process` are then reported missing as well.

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

### Why Podman 4.4.0

Each sandbox uses these Podman options:

| Option | Available since | Source |
| --- | --- | --- |
| `--network=pasta:--no-map-gw` | 4.4.0 | [4.4.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L721), [podman-run(1) v4.4, `--network`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--userns=keep-id:uid=UID,gid=GID` | 4.3.0 | [4.3.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L844), [podman-run(1) v4.4, `--userns`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--security-opt=no-new-privileges` | before 4.4.0 | [podman-run(1) v4.4, `--security-opt`](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |
| `--memory`, `--cpus`, `--pids-limit`, `--shm-size` | before 4.4.0 | [podman-run(1) v4.4](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html) |

Pasta sets the floor, so 4.4.0 is the earliest release that supports every option. Podman 5.0.0 did not add pasta. It changed the *default* rootless network tool from slirp4netns to pasta ([5.0.0 release notes](https://github.com/containers/podman/blob/v5.0.0/RELEASE_NOTES.md#L41)). `sandboxed-agents` selects pasta explicitly, so a Linux host does not depend on that default and does not need 5.0.0. Windows hosts need 5.0.0 for the reasons in [Why Podman 5.0.0](#why-podman-500).

`--no-map-gw` keeps the container from reaching the host through the gateway address. Podman passes it to pasta by default unless `--map-gw` is given ([network option, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/docs/source/markdown/options/network.md#L41-L55)). It is not a firewall against other host addresses the container can route to.

The floor describes feature support only. Whether a 4.4.x release still receives security fixes depends on your distribution.

### Limits of the check

The check confirms host configuration. It does not start a container, so a host that passes can still fail when a sandbox starts. Nothing in this section has been confirmed by starting a container on a live host.

The check does not:

- **Look for `pasta` in Podman's helper directories.** Podman searches `helper_binaries_dir` (for example `/usr/libexec/podman`) before `PATH` ([`FindHelperBinary`, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/common/pkg/config/config.go#L1046-L1095)). A `pasta` found only there is reported missing.
- **Ask NSS for subordinate IDs.** Podman built with `libsubid` can get subordinate ID ranges from an NSS `subid` provider, such as SSSD or FreeIPA, instead of `/etc/subuid` and `/etc/subgid` ([idtools, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/pkg/idtools/idtools_supported.go)). The check reads only the files, so such hosts are reported as missing their ranges. This is separate from account lookup: `getent passwd` resolves your user name through NSS, but subordinate ranges are still read only from `/etc/subuid` and `/etc/subgid`.
- **Validate subordinate ID files fully.** It does not reject malformed lines that Podman would refuse to parse. It does not check that a range avoids your own UID or GID, or that it is large enough for the IDs an image uses.
- **Prove the mapping helpers work.** It does not check that `newuidmap` and `newgidmap` have the setuid bit or file capabilities they need.
- **Prove user namespaces are allowed.** It does not read `user.max_user_namespaces` or `kernel.unprivileged_userns_clone`, and it does not evaluate AppArmor or SELinux policy. "Rootless" means only that you are not root.
- **Prove a future cgroup write succeeds.** The write-permission test is taken at one moment. The check does not test the cgroup manager Podman uses, or controllers enabled further down the delegated subtree. A delegated cgroup that is not the user manager and not a parent of the current cgroup is not found.
- **Check the pasta version** or whether pasta accepts the options Podman passes.
- **Check a remote Podman.** If `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or a `containers.conf` connection points Podman at another machine, the check still describes only the local host.

## Windows

The Windows preflight never installs, configures, or starts anything. It queries the Podman client and the Podman machine. On a running machine, it also runs read-only commands through Podman machine SSH as the non-root machine user. It never runs `ssh-keygen` and does not perform SSH setup.

`check` exits with status 0 when every required prerequisite is confirmed. It exits with a nonzero status when any required prerequisite is missing or unknown. If the Podman connection doesn't respond in time, the check reports a timeout and exits with a nonzero status.

### Required prerequisites

| Prerequisite | Requirement |
| --- | --- |
| Operating system | Windows 11 x64 workstation, build 22000 or later. ARM64 running x64 emulation is not supported. |
| Podman client | 5.0.0 or later |
| Podman machine | Running, on WSL2, rootless, Podman 5.0.0 or later |
| cgroups | cgroups v2 with the `cpu`, `memory`, and `pids` controllers delegated |
| OpenSSH | `ssh` and `ssh-keygen` on `PATH`. Preflight checks only that they are present. |

If the Podman machine is stopped, preflight reports the running machine as missing. It reports the machine version and cgroups as unknown, because only a running machine provides them. Rootless mode is read from machine inspect, which also works on a stopped machine. If inspect shows a stopped machine as rootful, preflight reports rootless mode as missing. If no Podman machine exists, preflight reports the machine version, rootless mode, and cgroups as unknown.

#### cgroup delegation in the Podman machine

`podman info` lists the cgroup controllers that are available, but that doesn't show whether they are delegated to the user. On a running rootless WSL2 machine, preflight therefore also checks delegation as the non-root SSH user. It reads `Delegate`, `ActiveState`, and `ControlGroup` for `user@<UID>.service` and confirms that `cpu`, `memory`, and `pids` are available in the cgroup of `user@<UID>.service`. It then uses file tests to confirm that this cgroup directory is writable and searchable, and that its `cgroup.procs`, `cgroup.threads`, and `cgroup.subtree_control` files are writable ([kernel delegation model](https://docs.kernel.org/admin-guide/cgroup-v2.html#model-of-delegation), [systemd cgroup delegation](https://systemd.io/CGROUP_DELEGATION/)). These checks only read and never write to or change any cgroup. Preflight skips them on machines known to be rootful or not to use WSL2. In that case it reports delegation as unknown, unless `podman info` already shows that cgroups v2 or one of these controllers is missing. These delegation checks have not yet been run on a live Windows host.

### Automount root

The automount root is not a prerequisite. Preflight reads it so that later commands can use it. On a running machine, preflight reads `/etc/wsl.conf` through Podman machine SSH and changes nothing. If no root is set, preflight uses the WSL default `/mnt/` ([WSL configuration](https://learn.microsoft.com/en-us/windows/wsl/wsl-config)). A custom root is returned as found, and only the later workspace bind checks can reject it. If automount is turned off or the configuration can't be read, the root is reported as unknown, but the check doesn't fail because of it. Preflight never reads the automount root when no machine is running.

### Why Podman 5.0.0

The client and the machine both need Podman 5.0.0 or later. The sources below confirm that Podman 5.0.0 provides everything a sandbox on Windows needs. They do not show that 5.0.0 is the first release to support each option.

- The [5.0.0 `podman run` reference](https://docs.podman.io/en/v5.0.0/markdown/podman-run.1.html) documents every option a sandbox needs: `--userns=keep-id` with `uid`/`gid`, `no-new-privileges`, `--network=pasta:--no-map-gw`, `--memory`, `--cpus`, `--pids-limit`, and `--shm-size`.
- The [5.0.0 release notes](https://github.com/containers/podman/releases/tag/v5.0.0) make pasta the default rootless network.
- The [5.0.0 WSL machine image](https://github.com/containers/podman-machine-wsl-os/releases/tag/v20240319175608) ships the `passt` package, version `0^20240220.g1e6f92b-1.fc39`, so pasta is available inside the machine.
- [`pkg/machine/config.go` at v5.0.0](https://github.com/containers/podman/blob/v5.0.0/pkg/machine/config.go) defines the `Rootful` field, which machine inspect reports.

The resource options (`--memory`, `--cpus`, `--pids-limit`) only work when cgroups v2 delegates their controllers. Preflight checks that delegation as a separate prerequisite.

### Verification status

The 5.0.0 floor was confirmed by reviewing versioned upstream documentation and source and the WSL machine image's package list. Offline tests run the preflight logic against simulated process results. These tests don't verify sandbox options or image builds on a live Windows host, and no test has been run yet against a live Windows host or a live Podman machine.

## Sources

- Podman: [4.4.0 release](https://github.com/containers/podman/releases/tag/v4.4.0), [5.0.0 release](https://github.com/containers/podman/releases/tag/v5.0.0), [podman-run(1) v4.4](https://docs.podman.io/en/v4.4/markdown/podman-run.1.html), [podman-run(1) v5.0.0](https://docs.podman.io/en/v5.0.0/markdown/podman-run.1.html), [troubleshooting §26, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/troubleshooting.md#L694-L737)
- `podman --version` start-up: [root.go](https://github.com/containers/podman/blob/v5.0.0/cmd/podman/root.go#L90-L100), [registry/config.go](https://github.com/containers/podman/blob/v5.0.0/cmd/podman/registry/config.go#L80-L169), [homedir_unix.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/pkg/homedir/homedir_unix.go#L107-L175), [default.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/common/pkg/config/default.go#L496-L520), [storage options.go](https://github.com/containers/podman/blob/v5.0.0/vendor/github.com/containers/storage/types/options.go#L288-L307), [pre-exec hooks](https://github.com/containers/podman/blob/v5.0.0/pkg/rootless/rootless_linux.c#L255-L269), all at v5.0.0
- Podman machine on Windows: [WSL machine image v20240319175608](https://github.com/containers/podman-machine-wsl-os/releases/tag/v20240319175608), [`pkg/machine/config.go`, v5.0.0](https://github.com/containers/podman/blob/v5.0.0/pkg/machine/config.go)
- WSL: [WSL configuration](https://learn.microsoft.com/en-us/windows/wsl/wsl-config)
- Linux kernel: [cgroup v2 delegation, v6.8](https://www.kernel.org/doc/html/v6.8/admin-guide/cgroup-v2.html#delegation), [model of delegation](https://docs.kernel.org/admin-guide/cgroup-v2.html#model-of-delegation)
- systemd: [Control Group APIs and Delegation](https://systemd.io/CGROUP_DELEGATION/)
