package cli

const upHelp = `Usage: sandboxed-agents up NAME [WORKSPACE] [--memory SIZE] [--cpus N] [--pids-limit N] [--shm-size SIZE] [--with SET] [--port N] [--agents LIST]
       [--ssh-config]

Create the sandbox NAME and start it, or start it if it already exists.

Arguments:
  WORKSPACE          existing host directory to bind at /workspace in place
                     of the workspace volume, on Linux or Windows; a relative
                     path is resolved against the current directory

Options:
  --memory SIZE      memory limit, at least 6m (default 8g)
  --cpus N           number of CPUs (default 4)
  --pids-limit N     maximum number of processes (default 2048)
  --shm-size SIZE    size of /dev/shm (default 1g)
  --with SET         toolchains built into the image (default none)
  --port N           SSH port on 127.0.0.1 (default: the first free port
                     from 2222 upward, chosen when the sandbox is created)
  --agents LIST      agents to enable once the sandbox runs (default none)
  --ssh-config       after starting the sandbox, install its SSH setup as
                     sandboxed-agents ssh-config NAME --install does
  --help             show this help

WORKSPACE, when given, directly follows NAME. Options follow NAME and
WORKSPACE, as --option VALUE or --option=VALUE, each at most once.

Value formats (every number must be greater than zero; no sign, no exponent):
  SIZE           whole number of bytes, optionally followed by k, m, g, or t
                 in either case (powers of 1024), for example 512m or 16g;
                 at most 9223372036854775807 bytes
  --cpus N       whole number, optionally with a point and one to three
                 decimals, for example 2 or 1.5; at most 9223372036.854
  --pids-limit N whole number, at most 9223372036854775807
  --with SET     comma-separated toolchain names, or none alone for the
                 base image; valid values: azure, dotnet, native, none,
                 playwright
  --port N       whole number from 1 to 65535
  --agents LIST  comma-separated agent names from the agent catalog, for
                 example claude,codex; no empty name, a repeated name counts
                 once; valid names: claude, codex, copilot, opencode

The workspace bind is the only host path a sandbox mounts. Symlinks,
junctions, and other aliases are resolved first. Before the preflight, up
refuses a WORKSPACE that does not exist, is not a directory, or contains or
lies inside a protected host path: the executable, host state, the system
temporary directory, your SSH directory, or, on Linux, /tmp. On Windows it
also refuses a directory that does not start with a drive letter, such as a
UNC path, a workspace that holds a symlink or junction to a protected host
path, or a directory it cannot read. up never creates the directory. When a
workspace volume kept by an earlier remove exists, up leaves it unused and
says so; remove --volumes deletes it.

On Windows, after the preflight, up translates the directory to its path in
the Podman machine that the preflight checked, for example
C:\Users\me\project to /mnt/c/Users/me/project. Only the default WSL automount
root /mnt/ is supported: when that machine reports another root, or automount
is disabled or its root could not be read, up refuses the WORKSPACE before it
looks up, builds, or creates anything. up only queries the machine and never
starts it; when it does not run, the preflight reports it.

The workspace, the limits, the toolchain set, and the SSH port are set when
the sandbox is created and recorded on its container. up never changes them:
on an existing sandbox, a WORKSPACE that resolves to another directory, any
WORKSPACE on a sandbox with a workspace volume, or a limit, toolchain set, or
SSH port that differs from the recorded value makes up fail without starting
it. Omit them to start the sandbox unchanged. To change them, run
sandboxed-agents remove NAME, which keeps the sandbox's volumes and never
deletes a bound directory, then sandboxed-agents up NAME with the new values.
On Windows, up translates the recorded bind back to a Windows path and
resolves it before the comparison, and a conflict names both directories as
Windows paths.

--agents is not part of the configuration and is never compared. up checks
every name before it calls Podman. Once the sandbox runs, new or existing,
up enables each listed agent that is not enabled yet, as
sandboxed-agents agents enable NAME AGENT does, and leaves enabled agents
as they are. It prints the output of these installations after the last
attempt. Each installation may take up to 15 minutes; one that takes
longer counts as failed. Stopping the waiting podman exec may not stop the
installation inside the sandbox, so check that it has finished before
retrying it. When an installation fails, up asks the manager for its version
again. If it answers, the sandbox keeps running, the other agents are
still attempted, and up exits with status 1 naming the failed agents and
the command to retry each. If the manager does not answer, before the
first or after a failed attempt, up prints no installation output, only
one message naming sandboxed-agents check NAME and the commands to retry
every listed agent; agents enabled before then stay enabled.

--ssh-config is not recorded on the container and never causes a conflict.
When installing the SSH setup fails, the sandbox keeps running, and
sandboxed-agents ssh-config NAME --install retries the installation. With
--agents, up enables the agents first; when up fails before the SSH setup,
for example on an agent, it skips the SSH setup and exits with status 1,
naming the commands to retry.
`
