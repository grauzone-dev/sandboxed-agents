---
status: accepted
---

# Podman names carry a product prefix and the controller group

The sandbox `NAME` in controller group `GROUP` is the Podman container `sandboxed-agents.GROUP.NAME`, and its volumes carry the same prefix. While `update` replaces a container, the old one is kept as the backup container `sandboxed-agents-backup.GROUP.NAME`. Controller group names match `^[a-z0-9][a-z0-9-]*$`, so they contain no dot and the Podman name splits unambiguously into prefix, group, and sandbox name. The sandbox name is also recorded in a label, and the owner label stays the authoritative check: a matching name alone authorizes nothing.

Two requirements led here. `update` needs a backup name that cannot collide with a sandbox, while no sandbox name may be reserved (ADR-0004). And a controller group is meant to be a namespace: the live suite and a second installation must be able to use a sandbox name that already exists in another group on the same host.

## Considered options

- **Sandbox name as the container name, with a reserved backup suffix** such as `NAME.backup`. Rejected because it reserves sandbox names, which ADR-0004 rules out.
- **A random backup name, with the mapping kept in host state.** Rejected because recovering an interrupted update would depend on a file outside Podman that can be missing or stale after the interruption.
- **Marking the backup with a label.** Not possible: Podman can rename an existing container but cannot change its labels.
- **Controller group only in the owner label.** The names would be shorter, but sandbox names would be unique across all groups on a host, and `up` in one group could fail because of a sandbox in another.

## Consequences

- `podman ps` shows the long names. Users address a sandbox by its sandbox name; the executable derives the Podman name.
- `list` and `update --all` tell a backup container from a sandbox by its name prefix and never treat it as a sandbox.
- An invalid controller group name fails before Podman is called. The same rule makes the group usable as a directory name in host state.
- Loopback ports belong to the host and not to a controller group, so port allocation reads the ports recorded on the containers of every group.
