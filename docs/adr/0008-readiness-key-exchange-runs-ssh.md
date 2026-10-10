---
status: accepted
---

# The readiness key exchange runs ssh with a private known_hosts file

The SSH check of `update`'s readiness wait (ADR-0007) ran `ssh-keyscan`. The `ssh-keyscan` of the Windows OpenSSH Client 9.5 proposes `sntrup761x25519-sha512@openssh.com`, which its Windows build cannot compute, while `ssh` from the same client proposes only what it supports ([Win32-OpenSSH #2140](https://github.com/PowerShell/Win32-OpenSSH/issues/2140), fixed in later releases by [openssh-portable #756](https://github.com/PowerShell/openssh-portable/pull/756)). The sandbox's sshd offers that method, so on Windows the check never succeeded and every `update` rolled back; a Windows 11 host with the inbox client 9.5.6.2 showed `choose_kex: unsupported KEX method sntrup761x25519-sha512@openssh.com`.

The check now runs the host's `ssh -v` with `-F none`, `BatchMode=yes`, `StrictHostKeyChecking=accept-new`, and a `UserKnownHostsFile` in a new temporary directory, with every authentication method off. The sandbox counts as reachable over SSH when `ssh` logs `debug1: SSH2_MSG_NEWKEYS received` and the file holds an Ed25519 host key for the loopback port. `ssh` records the host key as soon as it arrives, before it verifies the server's signature over the exchange; it sends its NEWKEYS and accepts the server's only after that verification (`input_kex_gen_reply` in `kexgen.c` and `kex_send_newkeys` in `kex.c`, in OpenSSH 9.5 and in current releases). The NEWKEYS line therefore shows the completed key exchange, and the recorded key shows that it came with an Ed25519 host key for that port. Authentication then fails, so the exit status of `ssh` is not used. The temporary directory is removed after each attempt, and no file in the user's SSH directory or in host state is read or written.

## Considered options

- **Removing `sntrup761x25519` from the image's sshd configuration.** Rejected: it would take the hybrid post-quantum key exchange away from every client to work around one client tool.
- **An SSH library such as `golang.org/x/crypto/ssh`.** Rejected: the module uses only the standard library.
- **A key exchange implemented in the executable.** Rejected: protocol and cryptography code to maintain for a readiness check.
- **A TCP connect or SSH banner check.** Rejected for the reason in ADR-0007: port forwarding can accept a connection before sshd listens, and the check is meant to show a completed key exchange.
- **Requiring a newer Windows OpenSSH or another `ssh-keyscan`, such as Git for Windows'.** Rejected: the inbox Windows OpenSSH Client is a supported prerequisite.
- **The recorded host key alone.** Rejected: `ssh` records it before it verifies the exchange signature, so a server that sends a valid host key and a wrong signature, or none, would count as ready.
- **The authentication refusal of `ssh`, such as `Permission denied (publickey)`.** Rejected: it depends on the server's authentication settings, and a server that accepts a connection without authentication never sends it.
- **`ssh-keyscan` first, `ssh` as a fallback.** Rejected: two probes for one check, and the first fails on every Windows attempt.

## Consequences

- `update` no longer runs `ssh-keyscan`. The preflight still requires it as part of the prerequisite set described in ADR-0007 until that set is revised.
- Each readiness attempt makes one unauthenticated SSH connection that ends before sign-in, as `ssh-keyscan` did.
- The check depends on a debug line of OpenSSH. A test drives the host's real `ssh` against a test SSH server whose exchange is valid, has a corrupted signature, or has no signature, and expects only the first to count as ready; it runs wherever `ssh` is on `PATH`.
