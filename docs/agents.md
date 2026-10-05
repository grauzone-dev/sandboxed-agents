# Agents

An agent is a coding CLI from the agent catalog. `sandboxed-agents agents enable NAME AGENT` installs it into the home volume of the sandbox `NAME` and records it as enabled there, and `sandboxed-agents agents login NAME AGENT [WORKFLOW]` runs one of its login workflows there. `sandboxed-agents agents disable NAME AGENT` removes the agent's command and its record and keeps its credentials and cached data. Agents in one sandbox share its user `agent`, its files, and the credentials stored in its home volume.

This page covers `agents enable NAME AGENT`, `agents disable NAME AGENT`, and `agents status NAME AGENT`, which take no options, `agents login NAME AGENT [WORKFLOW]`, which signs in to an enabled agent, `up NAME --agents LIST`, which enables agents as part of `up`, the `AGENTS` column of `list`, `agents run NAME AGENT [ARG...]`, which runs an enabled agent once, and `agents session NAME AGENT [--stop]`, which keeps an enabled agent running in a persistent agent session.

## Agent catalog

The catalog lists the agents that `sandboxed-agents` can install. It is data embedded in the executable (see [Catalog data format](#catalog-data-format)). This version delivers four agents:

| Agent | Program | npm package | Command | Login workflows | Documentation |
| --- | --- | --- | --- | --- | --- |
| `copilot` | GitHub Copilot CLI | `@github/copilot` | `copilot` | `github` | [Copilot CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference) |
| `claude` | Claude Code | `@anthropic-ai/claude-code` | `claude` | `subscription`, `console` | [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference) |
| `codex` | Codex CLI | `@openai/codex` | `codex` | `chatgpt`, `api-key` | [Codex CLI reference](https://developers.openai.com/codex/cli/reference/) |
| `opencode` | OpenCode | `opencode-ai` | `opencode` | `provider` | [OpenCode CLI](https://opencode.ai/docs/cli/) |

Each login workflow names the arguments passed to the agent's own command to sign in. [`agents login`](#sign-in-to-an-agent) runs them.

| Agent | Workflow | Runs | Signs in with |
| --- | --- | --- | --- |
| `copilot` | `github` | `copilot login --device-code` | a GitHub account, through the OAuth device code flow |
| `claude` | `subscription` | `claude auth login` | a Claude subscription |
| `claude` | `console` | `claude auth login --console` | Anthropic Console, billed by API usage |
| `codex` | `chatgpt` | `codex login --device-auth` | a ChatGPT account, through the OAuth device code flow |
| `codex` | `api-key` | `codex login --with-api-key` | an OpenAI API key read from standard input |
| `opencode` | `provider` | `opencode auth login` | the credentials of a provider you choose |

Only Claude Code declares a status probe: `claude auth status` prints JSON whose boolean field `loggedIn` tells whether it is signed in. The other three agents declare none.

## Enable an agent

```sh
sandboxed-agents agents enable NAME AGENT
```

`NAME` is the sandbox and `AGENT` is a name from the [agent catalog](#agent-catalog). The sandbox must be running.

When the agent is not enabled yet, the manager installs the version of the agent's npm package that is `latest` at that moment:

```sh
npm install --global --prefix /home/agent/.local --cache /home/agent/.local/cache/sandboxed-agents/npm PACKAGE@latest
```

`--cache` moves npm's cache from its default `~/.npm` to a directory of its own under `/home/agent/.local`. Options on the command line take priority over npm's environment variables and `.npmrc` files ([npm config, `cache`](https://docs.npmjs.com/cli/v11/using-npm/config/#cache)).

It reads the installed version from the package's `package.json` under `/home/agent/.local/lib/node_modules`, records the agent in the agent selection, `/home/agent/.local/state/sandboxed-agents/selection.json`, prints `Agent AGENT is enabled (version VERSION).`, and exits with status 0.

The installation, the npm cache, and the manager's own state in `/home/agent/.local/state/sandboxed-agents` all lie below `/home/agent/.local`. `agents enable` removes no existing file of the home volume, credentials included. npm and the package's install scripts run as `agent`, UID and GID 1000, like any other work of that user, so they can still write elsewhere in the home volume; what they create belongs to `agent`.

When the agent is already enabled, `agents enable` runs no installation and leaves the selection as it is. It prints the same line with the installed version and exits with status 0. It does not move the agent to a newer version.

The installed agent and the selection live in the home volume, so they survive `stop`, `start`, and `restart`. They also survive `remove NAME` without `--volumes`, and the next `up NAME` adopts the volume ([Kept volumes](sandboxes.md#kept-volumes)).

## Disable an agent

```sh
sandboxed-agents agents disable NAME AGENT
```

`NAME` is the sandbox and `AGENT` is a name from the [agent catalog](#agent-catalog). The sandbox must be running.

When the agent is enabled, the manager removes its managed command, `/home/agent/.local/bin/COMMAND`, where `COMMAND` is the agent's command from the catalog. It also removes the agent's whole entry from the agent selection, including a pin. The other entries keep their fields and values, also fields this version does not use; only the file's whitespace may change when the manager writes it. It prints `Agent AGENT is disabled.` and exits with status 0. When the entry recorded a pin, the line names it instead: `Agent AGENT is disabled (removed pin PIN).` This version sets no pins; setting a pin comes with #43.

After `agents disable`, the agent no longer appears in the `AGENTS` column of `list`, `agents status` reports it as not enabled, and `agents login` and `agents run` refuse it and name `sandboxed-agents agents enable NAME AGENT`. Other agents in the sandbox are not affected.

`agents disable` removes no other file. The agent's credentials and cached data stay in the home volume, and so do the installed package files under `/home/agent/.local/lib/node_modules` and the npm cache. A later `agents enable NAME AGENT` installs the version that is `latest` at that time and records no pin.

When the agent is not enabled, `agents disable` changes nothing, prints `Agent AGENT is not enabled; nothing to do.`, and exits with status 0. On a home volume where no agent was ever enabled, it creates no file either.

If removing the command or writing the selection fails, `agents disable` reports the failure and exits with status 1. The two steps are not undone together: when writing the selection fails after the command was removed, the agent stays in the selection without its command. Run `agents disable` again to finish the removal.

`agents disable` does not check for a running [agent session](#keep-an-agent-running-in-a-session) of the agent. Refusing while a session runs, and `--force`, come with #46.

## How the request reaches the manager

The executable addresses the in-container manager as container root, as for every administrative control call (ADR-0006). It first checks that the manager answers:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager version
```

The manager answers when this call exits with status 0 within 30 seconds and prints one line `sandboxed-agents-manager VERSION`. The executable then passes the request on with the call for its command:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents enable AGENT
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents disable AGENT
```

The manager's output and the output of npm pass through to your terminal.

On Windows, the version check and the request also name the [selected Podman machine](sandboxes.md#target-on-windows) with `--connection`.

The manager does not install or remove an agent as root. It starts its own trusted worker, the same manager program from the image, with the identity of the user `agent`: UID 1000, GID 1000, and no supplementary groups. The worker gets a fixed environment (`HOME`, `USER`, `LOGNAME`, `SHELL`, and a `PATH` that starts with `/home/agent/.local/bin`) and inherits none of root's. It reads the home volume only after this change of identity. It takes the [manager lock](#manager-lock) and reads the selection. For `agents enable`, it then runs npm and every other command of the installation and writes the selection; for `agents disable`, it removes the managed command and writes the selection. All of this runs as `agent`, so every file the installation creates in the home volume belongs to `agent`.

While the manager runs as root, it starts no program and no script from the home volume or the workspace, and it takes no command or argument from data stored there. A selection file or an npm configuration in the home volume that names a command can only make that command run as UID and GID 1000.

For `agents run`, the executable makes the same `version` check as root and then starts the manager directly as `agent`, with `--interactive` and without `--tty`:

```sh
podman exec --interactive --user=1000:1000 --workdir=/workspace --env HOME=/home/agent sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents run NAME AGENT [ARG...]
```

On Windows, both calls also name the selected Podman machine with `--connection`. The manager refuses any identity other than UID and GID 1000 before it reads the home volume or starts a process. `NAME` reaches it only for the recovery command in its not-enabled message. It reads the agent selection and starts `/home/agent/.local/bin/COMMAND` with an explicit request for UID and GID 1000, in `/workspace`, with the same fixed environment as the worker above. It takes no manager lock. The 30-second limit applies only to the `version` check, not to the run.

## Manager lock

The manager serializes changes to the agent installations with one lock, the file `/home/agent/.local/state/sandboxed-agents/manager.lock` in the home volume. It is an advisory lock held through the operating system, so the operating system releases it when the process that holds it ends, also after a crash.

`agents enable` and `agents disable` hold the lock from before they read the selection until their change is complete. One exception keeps `agents disable` from creating files: when the lock file does not exist yet, it first reads the selection without the lock, and if the agent is not listed there, it reports that nothing was to do and stops. A second `agents enable` or `agents disable` that takes the lock on the same sandbox, for the same or another agent, waits until the first has released it. Two concurrent `agents enable` calls for different agents therefore leave both agents in the selection.

Starting an [agent session](#keep-an-agent-running-in-a-session) and ending one with `agents session NAME AGENT --stop` take the same lock. A session start or `--stop` issued while an installation change holds it does not run until the lock is released. Attaching to a running session takes no lock.

## Enable agents with `up`

```sh
sandboxed-agents up NAME --agents a,b
```

`--agents` takes a comma-separated list of agent names. It is given as `--agents VALUE` or `--agents=VALUE`, at most once. A name given more than once is enabled once. An option without a value, given twice, or with an empty agent name, such as `--agents=` or `--agents claude,,codex`, is a usage error that names `--agents`.

`up` checks every name against the [agent catalog](#agent-catalog) embedded in the executable before it calls Podman, at the same step as its other usage checks. An unknown name, or a catalog name whose agent is not delivered yet, makes `up` exit with status 1 and list the valid agent names. It creates no sandbox, volume, or image.

`up NAME --agents a,b` has the same result as `up NAME` followed by `agents enable NAME a` and `agents enable NAME b`: the same selection and the same installations. `--agents` is not part of the sandbox's configuration, so it also applies to a sandbox that exists already. `up` starts such a sandbox as it would without `--agents` and enables each listed agent that is not enabled yet. An agent that is already enabled stays as it is, without an installation, as with a repeated `agents enable`. `up` exits with status 0 when every listed agent is enabled at the end.

`up` collects the output of the installations and prints it after the last attempt. It waits at most 15 minutes for each installation, and one that takes longer counts as failed. When `up` stops waiting, the installation inside the sandbox may still be running, so check that it has ended before you retry that agent. When an installation fails, `up` asks the manager for its version again, within 30 seconds. When the manager answers, `up` still attempts the remaining agents. The sandbox stays and keeps running, and agents already enabled stay enabled. `up` exits with status 1 and names the failed agents together with `sandboxed-agents agents enable NAME AGENT` to retry each.

When the manager does not answer, before the first agent or after a failed one, the sandbox stays, and agents enabled before then stay enabled. `up` discards the collected output, exits with status 1, and prints a single message about the manager, not one per agent. The message says that `up` cannot confirm which agents were enabled and names `sandboxed-agents check NAME` for diagnosis and the `agents enable` command for every listed agent.

[Enable agents with `up`](sandboxes.md#enable-agents-with-up) describes how this step fits into `up`.

## See the enabled agents

The agent selection lives only in the home volume; host state keeps no copy of it. `sandboxed-agents list` therefore reads it through the manager of each running sandbox:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents list
```

The manager answers with a JSON array of the enabled agent names, sorted by name, and `list` waits at most 5 seconds for it. Like `agents enable`, the manager starts its trusted worker as `agent`, UID and GID 1000, which reads the selection. The query takes no [manager lock](#manager-lock) and creates no file, also when the selection does not exist yet. The manager saves the selection atomically, so the query reads either the selection before a concurrent `agents enable` or the one after it.

The `AGENTS` column shows the names in alphabetical order, separated by commas, or `none` when the manager answers and no agent is enabled. It shows `-`, meaning "not available", for a sandbox that is not running and for one whose manager does not answer in time, fails, or answers with anything but a JSON array of names from the catalog. A selection file that cannot be read as a JSON object, or that names an unknown agent, makes the query fail. `list` starts nothing to read the agents and still exits with status 0 ([`AGENTS` column](sandboxes.md#agents-column)).

## Show an agent's status

```sh
sandboxed-agents agents status NAME AGENT
```

`NAME` is the sandbox and `AGENT` is a name from the [agent catalog](#agent-catalog). The sandbox must be running. The request reaches the manager as for `agents enable`, with `agents status AGENT` in place of `agents enable AGENT`.

For an enabled agent, it prints the installed version, the sign-in state, and whether an [agent session](#keep-an-agent-running-in-a-session) of the agent is running:

```text
Agent claude is enabled (version 1.2.3).
Sign-in state: signed in.
Agent session: running.
```

The sign-in state is `signed in`, `not signed in`, or `unknown`. The session field is `running` while the agent runs in its session and `not running` otherwise; the manager reads it from the tmux server of `agent`, as for the [session query](#session-query). For an agent that is not enabled, it prints only these two lines and runs nothing of the agent:

```text
Agent claude is not enabled.
Agent session: not running.
```

The session field of an agent that is not enabled comes from the same query, so it reads `running` when the agent's session still runs after `agents disable`. When the manager cannot read the sessions, `agents status` reports the failure and exits with status 1.

The sign-in state comes from the agent's status probe in the catalog. The manager's worker runs the installed command `/home/agent/.local/bin/COMMAND` with the probe's arguments as the user `agent`, never as root, and with the same fixed environment as an installation. It reads the probe's standard output and does not show it; the probe's standard error is discarded. When the output is one JSON object whose field named by the probe is `true`, the state is `signed in`; when it is `false`, the state is `not signed in`. This holds whatever status the command exits with. The state is `unknown` when:

- the catalog entry declares no status probe, as for `copilot`, `codex`, and `opencode`;
- the command cannot start, or does not finish within 30 seconds, whatever it printed until then;
- its standard output is not exactly one JSON object, for example no output, text, trailing data after the object, `null`, or an array;
- the named field is missing or not a boolean.

The probe runs in a process group of its own. When the 30 seconds run out, the manager kills that group at once and then waits at most one second for the probe's output to close. When the probe exits on its own, the manager first waits at most one second for its output to close, closes the output if it is still open, and then kills what remains of the group, which ends descendants the probe left running. It then reads the output it collected like any other probe output: a complete JSON object with a boolean field still gives `signed in` or `not signed in`, and output that was cut off gives `unknown`. Either way, the manager then goes on with the report, so a probe delays it by at most about 31 seconds. A descendant that moves to another process group or session is not killed, but it cannot hold up the report beyond that second either. This cleanup only keeps a probe from blocking `agents status`; it does not contain a probe or its descendants, which run as `agent` like any other agent work.

`unknown` means only that the manager could not tell; it says nothing about whether the agent is signed in. The report shows what the agent's own command answered at the moment of the call.

`agents status` exits with status 0 whenever it can report, whatever it reports: also for an agent that is not enabled, not signed in, or in the `unknown` state. It changes neither the installation nor the agent selection, and for an agent that is not enabled it writes nothing to the home volume. The report contains no pin.

`agents status` refuses in the same cases and in the same order as `agents enable` ([Refusals](#refusals)), and reports no state of the agent then.

## Sign in to an agent

```sh
sandboxed-agents agents login NAME AGENT [WORKFLOW]
```

`NAME` is the sandbox, `AGENT` an agent enabled in it, and `WORKFLOW` one of the agent's [login workflows](#agent-catalog). `WORKFLOW` may be left out for an agent with one login workflow, `copilot` and `opencode`, and is required for an agent with more, `claude` and `codex`. The command needs a running sandbox and an interactive terminal.

```sh
sandboxed-agents agents login dev copilot
sandboxed-agents agents login dev claude console
```

The executable checks the agent and workflow names against its embedded catalog before it calls Podman, and resolves an omitted workflow name to the agent's only workflow. It then runs the sandbox checks of `agents enable`. To find out whether the manager answers, it sends the administrative session query as container root, the same call `remove` uses ([Running agent sessions](sandboxes.md#running-agent-sessions)):

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

The manager answers when this call exits with status 0 within 30 seconds and prints a valid JSON array of sessions. Only then does the executable ask the manager whether the agent is enabled, as `agent`:

```sh
podman exec --user=1000:1000 --env HOME=/home/agent sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents check-enabled AGENT
```

The manager prints `true` or `false`. A call that cannot be run, exits with a non-zero status, does not finish within 30 seconds, or prints any other answer counts as a manager that does not answer, and the command names `check NAME` and `restart NAME` as for a failed session query. When the agent is enabled, the executable checks that its standard input and standard output are a terminal, and then runs the workflow request in an interactive `podman exec` with a terminal:

```sh
podman exec --user=1000:1000 --env HOME=/home/agent -it --env SANDBOXED_AGENTS_SANDBOX=NAME sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents login AGENT WORKFLOW
```

`SANDBOXED_AGENTS_SANDBOX` tells the manager the sandbox name only so that its own refusal of an agent that is not enabled can name `sandboxed-agents agents enable NAME AGENT`.

On Windows, all three calls also name the [selected Podman machine](sandboxes.md#target-on-windows) with `--connection`.

The manager prints the agent's login message from the catalog, then starts the agent's command `/home/agent/.local/bin/COMMAND` with the workflow's arguments, for example `copilot login --device-code`. The message appears only after every check has passed and directly before the workflow starts. The workflow uses your terminal, so device codes, links, prompts, and API keys pass between you and the agent's own command.

`agents login` exits with status 0 when the workflow ended with status 0, and with status 1 when it ended with any other status or could not start; it does not pass the workflow's status through. It does not check afterwards whether the agent is signed in; [`agents status`](#show-an-agents-status) reports the sign-in state.

### Identity and credentials

Only the session query runs as container root. The question whether the agent is enabled and the workflow itself run under the identity of `agent`, UID and GID 1000: the executable starts the manager for them with `--user=1000:1000`, and the manager refuses either request under any other identity. The manager starts the agent's command with UID and GID 1000 stated explicitly, in `/home/agent`, with the fixed environment of the installation worker (`HOME`, `USER`, `LOGNAME`, `SHELL`, and a `PATH` that starts with `/home/agent/.local/bin`) instead of the caller's. The only variable it keeps from the `podman exec` call is a non-empty `TERM`, so that the agent's command can drive your terminal. No part of a login workflow runs as container root (ADR-0006).

The agent's command decides where it stores what you sign in with. Because its home directory `/home/agent` is the sandbox's home volume, files it writes there stay in that volume. They survive `stop`, `start`, `restart`, and `remove NAME` without `--volumes`, and every agent in the sandbox can read them. `remove NAME --volumes` deletes them. The executable reads, copies, and stores no credential on the host.

## Refusals

`agents enable`, `agents disable`, `agents status`, and `agents login` share these refusals. In each case the command exits with status 1: `agents enable` installs nothing, `agents disable` removes nothing, `agents status` reports no state of the agent, and `agents login` starts no workflow and prints no login message. When several apply, the command reports the first in the [order of checks](development.md#order-of-checks):

| Step | Refusal |
| --- | --- |
| 1. Usage and names | An invalid controller group, a usage error, or an invalid sandbox name. An unknown agent name lists the valid agent names. A catalog name whose agent is not delivered yet counts as unknown and is not listed; in this version all four agents are delivered. For `agents login`, a missing workflow name for an agent with more than one login workflow and an unknown workflow name list the agent's workflow names. None of these calls Podman. |
| 3. Sandbox existence | An unknown sandbox, also one of another controller group. A sandbox of which only volumes remain is refused with a message naming `sandboxed-agents up NAME`, which adopts the volumes. |
| 4. Owner | The container, one of the volumes, or the backup container has a missing or different owner label. The message names each such Podman object and points to Podman, also when the container carries the current owner and only a volume does not ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)). |
| 5. Interrupted update | A backup container with the current owner exists. The message names `sandboxed-agents update NAME`. |
| 6. Running state | The sandbox is stopped. The message names `sandboxed-agents start NAME`, and nothing starts on its own. |
| 7. Preconditions | The manager does not answer. The message says so and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step. The command issues no further call into the sandbox. For `agents login`, the agent is not enabled in the sandbox; the message names `sandboxed-agents agents enable NAME AGENT`. |
| 8. Terminal | `agents login` without an interactive terminal. This is reported only when the manager answers and the agent is enabled. |

`sandboxed-agents check NAME` reports whether the manager answers, together with the rest of the sandbox's state ([Check a sandbox](check.md)). `restart NAME` is available ([Stop, start, and restart a sandbox](sandboxes.md#stop-start-and-restart-a-sandbox)).

An owner conflict on a stopped sandbox is therefore reported as the owner conflict, not as the stopped sandbox. Whether the agent is enabled is known only to the manager, so `agents disable` reports these refusals also for an agent that is not enabled. If npm or another step of the installation fails, `agents enable` reports the failure, leaves the selection unchanged, and exits with status 1. If the manager cannot read the agent selection or, for an enabled agent, the installed version, `agents status` reports the failure, prints no report, and exits with status 1; it runs no status probe then.

## Run an agent

```sh
sandboxed-agents agents run NAME AGENT [ARG...]
```

`agents run` starts the enabled agent `AGENT` once in the running sandbox `NAME` and ends when the agent exits. Every `ARG` reaches the agent unchanged and in order, including `--help` and other arguments that start with `-`: `agents run` has no options and no help of its own. `sandboxed-agents agents run agent01 codex --help`, for example, prints the help of Codex.

The agent runs as `agent`, UID and GID 1000, in `/workspace`, with a fixed environment, so variables from your host environment do not reach it. `agents run` allocates no terminal. Standard input, standard output, and standard error stay three separate streams whose bytes pass through unchanged, so a script can pipe input into the agent and redirect its output and errors separately.

`agents run` exits with the agent's exit status. When a signal ends the agent, the status is 128 plus the signal number, as in a Unix shell: 143 for `SIGTERM` and 137 for `SIGKILL`. When it refuses or cannot start the agent, it exits with status 1. An agent can exit with status 1 itself, so the status alone does not tell the two apart.

`agents run` checks the names, the sandbox, and the manager in the same [order](#refusals) as `agents enable`, and starts no agent when a check fails. It starts, updates, and repairs nothing on its own. An owner conflict names the Podman objects concerned, and these refusals name the next step:

| Case | Next step |
| --- | --- |
| Only the volumes of the sandbox remain | `sandboxed-agents up NAME` |
| An update was interrupted | `sandboxed-agents update NAME` |
| The sandbox is stopped | `sandboxed-agents start NAME` |
| The manager does not answer | `sandboxed-agents check NAME` for diagnosis ([Check a sandbox](check.md)), then `sandboxed-agents restart NAME` |
| The agent is not enabled in the sandbox | `sandboxed-agents agents enable NAME AGENT` |

An agent selection that cannot be read or is not a valid JSON object is refused as well. [How the request reaches the manager](#how-the-request-reaches-the-manager) describes the Podman call.

## Keep an agent running in a session

```sh
sandboxed-agents agents session NAME AGENT
sandboxed-agents agents session NAME AGENT --stop
```

`NAME` is the sandbox and `AGENT` an agent enabled in it. The sandbox must be running.

Without `--stop`, `agents session` starts a persistent agent session for the agent and attaches your terminal to it. The session is a tmux session named `sandboxed-agents-AGENT`. It lives on a tmux server of its own, with the socket name `sandboxed-agents`, which reads no tmux configuration file. The session's first window does not run the agent directly: it runs the manager from the image, which starts the agent's command `/home/agent/.local/bin/COMMAND` from the catalog as `agent`, UID and GID 1000, with `/workspace` as its working directory, and waits for it. When a session of the agent is already running, `agents session` attaches to it and starts no second one, also when you call it from another terminal while the first is still attached.

Detaching from tmux with its default key binding Ctrl-b d, or closing the terminal leaves the agent running in its session. The next `agents session NAME AGENT` attaches to it again.

A session ends with its agent. As soon as the agent's process exits, whatever its exit status, the manager in the first window ends the whole tmux session, also when you opened further windows in it, so a running session always means a running agent. It ends the session in the same way when the agent's command cannot start. The next `agents session NAME AGENT` then starts a fresh session.

Starting a session and attaching to one need an interactive terminal on standard input and standard output. Without one, `agents session` exits with status 1, starts no session, and attaches to none, whether or not a session is running. Starting a session without a terminal is not part of this version. When the call into the sandbox cannot run or ends with a non-zero status, `agents session` reports it and exits with status 1.

### End a session

`agents session NAME AGENT --stop` ends the agent's session, which ends the agent running in it. It prints `Ended the agent session of AGENT.` and exits with status 0. When no session of the agent is running, it prints `No agent session of AGENT is running; nothing to do.` and exits with status 0. The same holds when the agent exits on its own while `--stop` is ending its session. When the agent is not enabled, `--stop` prints `Agent AGENT is not enabled; nothing to do.` and exits with status 0. This does not mean that no session runs: `agents disable` does not end a running session (refusing while one runs comes with #46), and `--stop` on an agent that is not enabled leaves such a session running. [`agents status`](#show-an-agents-status) still reports it. `--stop` needs no terminal, so a script can call it.

### Identity

The tmux server, the session, and the agent in it run as the user `agent`, UID and GID 1000. Starting a session, attaching to it, and `--stop` all address the tmux server of `agent`. The executable checks the names and the sandbox, and makes the `version` check as container root, as for [`agents enable`](#how-the-request-reaches-the-manager), to find out whether the manager answers. It then starts the manager as `agent`. For `--stop` the call is:

```sh
podman exec --user=1000:1000 --workdir=/workspace --env HOME=/home/agent sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents session NAME AGENT --stop
```

Without `--stop`, the executable first asks the manager whether the agent is enabled, as `agents login` does, then checks for the terminal, and makes the same call without `--stop` and with `--interactive --tty`. On Windows, every call also names the [selected Podman machine](sandboxes.md#target-on-windows) with `--connection`. The manager refuses `agents session` under any identity other than UID and GID 1000, and checks again that the agent is enabled and, without `--stop`, that it has a terminal. It never starts a session or an agent as container root.

`--stop` holds the [manager lock](#manager-lock) while it ends the session. Starting a session holds it until the new session runs and releases it before attaching. Attaching to a session that is already running takes no lock.

### Session query

The manager's query for the running sessions of a sandbox, the call `stop`, `restart`, `remove`, and `update` use for their session guard ([Running agent sessions](sandboxes.md#running-agent-sessions), [Session guard of `update`](updates.md#session-guard)), answers with every running agent session of the sandbox, and with an empty list when none runs:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager sessions list
```

The executable addresses the manager as container root for this query, as for every administrative call. The manager, called as root, starts itself again as `agent`, UID and GID 1000, and that process reads the sessions from the tmux server of `agent`, not from a tmux server of root, which would always be empty. Each element names the session, `sandboxed-agents-AGENT`, and its agent, for example `[{"name":"sandboxed-agents-claude","agent":"claude"}]`. Sessions on that server whose names do not start with `sandboxed-agents-` are not listed, and when no tmux server of `agent` runs, the answer is `[]`. `stop`, `restart`, `remove`, and `update` therefore refuse on a sandbox with a session started by `agents session` unless `--force` is given. `update` sends the query only when it would replace a running sandbox's container, after its image builds and directly before the replacement. The query fails when tmux cannot be started, does not answer within 5 seconds, or fails for another reason, and when a session name names an agent outside the catalog or appears twice. The commands that sent the query then treat the manager as not answering.

### Session refusals

`agents session` checks the names, the sandbox, and the manager in the [order of checks](development.md#order-of-checks) and reports the first failure. In each case it exits with status 1 and starts no session:

| Step | Refusal |
| --- | --- |
| 1. Usage and names | A usage error, an invalid sandbox name, or an unknown agent name, which lists the valid agent names. None of these calls Podman. |
| 3. Sandbox existence | An unknown sandbox. A sandbox of which only volumes remain is refused with a message naming `sandboxed-agents up NAME`, which adopts the volumes. |
| 4. Owner | The container, one of the volumes, or the backup container has a missing or different owner label. The message names each such Podman object and points to Podman. |
| 5. Interrupted update | The message names `sandboxed-agents update NAME`. |
| 6. Running state | The sandbox is stopped. The message names `sandboxed-agents start NAME`, also when the agent is not enabled there, and nothing starts on its own. |
| 7. Preconditions | The manager does not answer. The message says so and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step, also with `--stop` and without a terminal. Without `--stop`, the agent is not enabled; the message names `sandboxed-agents agents enable NAME AGENT`. |
| 8. Terminal | No interactive terminal, without `--stop`. This is reported only when the manager answers and the agent is enabled. |

## Catalog data format

The catalog is the file `internal/agentcatalog/catalog.json` in the repository. The host executable `sandboxed-agents` and the manager `sandboxed-agents-manager` both embed it and read it through the same Go types in `internal/agentcatalog` (ADR-0001). The host uses it to check agent names before any Podman call; the manager uses it to install and to run status probes.

The file is one JSON object with two fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `schema_version` | number | The version of this format. It is `1`. |
| `entries` | array | One object per agent. |

Each entry has these fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `name` | string | The agent name used on the command line, such as `codex`. Unique in the catalog. |
| `delivered` | boolean | Whether the agent is offered. An entry with `false` counts as unknown. |
| `command` | string | The program the agent installs and that its workflows run. |
| `install` | object | How to install the agent. `kind` selects the install method, and the other fields belong to that kind. The only kind is `npm`, with `package`, the npm package name. |
| `login_workflows` | object | A map from workflow name to the array of arguments passed to `command`. |
| `login_message` | string | The text printed before a login workflow runs. |
| `status_probe` | object, optional | `args`, the arguments passed to `command`, and `boolean_field`, the name of a boolean field in the JSON the command prints. Leave it out when the agent's documentation describes no such output. |
| `documentation` | string | A link to the agent's CLI documentation. |

An entry for Claude Code:

```json
{
  "name": "claude",
  "delivered": true,
  "command": "claude",
  "install": {"kind": "npm", "package": "@anthropic-ai/claude-code"},
  "login_workflows": {
    "subscription": ["auth", "login"],
    "console": ["auth", "login", "--console"]
  },
  "login_message": "…",
  "status_probe": {"args": ["auth", "status"], "boolean_field": "loggedIn"},
  "documentation": "https://code.claude.com/docs/en/cli-reference"
}
```

### Add an agent

An agent that is an npm package needs only a new entry with `"install": {"kind": "npm", "package": "…"}`. No Go code changes: the host accepts the name, and the manager installs the package as it does for the other entries.

A further install method is a new `kind`. Adding one takes a Go type for the fields of that kind, its case in the catalog's kind check and validation, and an installer for it in the manager. The existing `npm` entries stay as they are, and so does `schema_version`.

## Verification

Offline tests cover the behavior on this page as follows ([Test seams](development.md#test-seams)):

- `agents run` at the CLI boundary and in the manager ([Agent runs](development.md#test-seams));
- `agents enable` and `agents disable` at the CLI boundary against a fake `podman`, for each of the four agents, on the Linux and the Windows target: usage errors and unknown or undelivered agent names before any Podman call, the order of checks, a manager that does not answer, and a failed manager request. On Windows, with the controller group `team-a`, both manager calls of `agents enable`, `agents disable`, and `agents status` stay on the selected machine without `CONTAINER_HOST`, `CONTAINER_CONNECTION`, or `CONTAINER_SSHKEY`, and an unavailable machine is refused. `agents enable` is also tested with a fifth catalog entry;
- `agents status` at the CLI boundary against a fake `podman`: the manager calls for each of the four agents, exit status 0 for each kind of report, a failure of the manager passed on with a non-zero status, and the same refusals and order of checks as `agents enable`, on the Linux and the Windows target;
- the manager's catalog reading, installation, selection handling, and lock with injected process functions. These tests check the identity and arguments each process is requested with, including two concurrent calls and a failed installation;
- the manager's `agents disable` with injected process functions:
  - for each agent, it starts no process, removes the managed command and the agent's entry, keeps the other selection entries with their field values, and leaves every other home file unchanged; a following `agents enable` installs `latest` again without a pin;
  - after a disable, the manager's `agents list`, `agents status`, `agents login`, and `agents run` treat the agent as not enabled, while another enabled agent still lists, reports, signs in, and runs;
  - a managed command that is a symlink is removed and its target stays; a missing command does not stop the removal of the entry. Where the platform cannot create a symlink, as on some Windows setups, that test is skipped;
  - for an agent that is not enabled, the home directory stays unchanged, also when it is empty or has no lock file;
  - called as root, the manager starts only the fixed worker, as UID and GID 1000 with its fixed environment, before it reads the home directory;
  - a removal waits while an installation holds the lock, and a removal canceled while it waits leaves the home directory unchanged;
  - an invalid selection, a directory in place of the selection, the lock file, or the command, a failed worker, and an unexpected identity each exit with status 1 and leave the home directory unchanged;
- the manager's `agents status` with injected process functions: the version of each of the four agents read after `agents enable`, a not-enabled agent without any process or write to the home directory, and the root manager starting only its worker. For a fifth catalog entry, they check the probe's command, arguments, identity, environment, process group cleanup, and 30-second deadline, and how each probe answer maps to a sign-in state, including non-zero exit statuses, invalid or non-object JSON, missing or non-boolean fields, a failed start, and a timeout. Two cases close the output after the one-second wait that follows the probe's own exit, once with a `true` field, which still gives `signed in`, and once with a `null` field, which gives `unknown`. An unreadable selection or installed version fails without a probe;
- a selection in a temporary home directory that stays byte for byte unchanged across `stop` and `start` against a fake `podman`, after which a repeated `agents enable` runs no installation. The test does not stop or start a real container;
- `up NAME --agents` at the CLI boundary against a fake `podman`, on the Linux and the Windows target: a new sandbox; a stopped and a running existing sandbox, routed to a real manager with injected process functions, where an enabled agent is not reinstalled; invalid selections and undelivered names without a Podman call; failed installations with retry commands; a manager that does not answer before the first or after a failed installation; and the 15-minute bound of each installation with a fresh manager check afterwards;
- the `AGENTS` column of `list` against a fake `podman`: names, `none`, a stopped sandbox, unusable or failed answers, the 5-second bound, the Windows target, and `agents enable` followed by `list` with a fifth catalog entry;
- the manager's `agents list` query: a missing and a written selection, the worker started as UID and GID 1000, a refused identity, and invalid selection files left unchanged;
- `agents login` at the CLI boundary against a fake `podman`, on the Linux and the Windows target, including the name checks before any Podman call, the order of checks up to the missing terminal, the arguments and identity of each call into the sandbox, and the exit status 0 or 1. For terminal detection these tests give the executable a native pseudo-terminal on Linux and the native console on Windows, and replace standard input or standard output with a non-terminal file to check the refusal. The terminal is local to the test; the fake `podman` behind it starts no sandbox and no workflow;
- the manager's workflow selection with injected process functions: the login message before the workflow, the command and arguments from the catalog, and the requested UID and GID 1000, with a test that fails when the workflow would start as container root;
- `agents session` and `agents session --stop` at the CLI boundary against a fake `podman`, on the Linux and the Windows target: usage errors and unknown agent names before any Podman call, `--help` without Podman, the refusals in their order up to the missing terminal, a terminal required on both standard input and standard output, `--stop` without a terminal and without asking whether the agent is enabled, the arguments of each call into the sandbox, the terminal passed through, and the exit status 1 for any non-zero status of the call;
- the manager's `agents session` with injected process functions: a start, a second call that attaches to the running session and starts no second one, a fresh session after the agent exits, `--stop` without a terminal, repeated, and on an agent that is not enabled, a `--stop` on an agent that was disabled while its session runs, which reports that the agent is not enabled, does not claim that no session runs, and leaves the session running, so that `agents status` still reports it as `running`, a `--stop` that reports nothing to do when the agent exits between the session query and the end of the session, a failed or unlaunchable tmux start, attach, or stop reported with exit status 1, refusals of root and of other identities, of an agent that is not enabled, and of a missing terminal, none of which starts tmux, and a session start and a `--stop` that wait while an installation holds the manager lock;
- the manager process in the session's first window with injected process functions: it refuses container root and a GID other than 1000 without starting tmux. Otherwise it starts the agent's command as UID and GID 1000 in `/workspace`, without arguments, and keeps the `TERM` that the manager process receives, and after the agent exits with status 0, exits with another status, cannot start, or is canceled, it asks tmux exactly once to kill the session `=sandboxed-agents-AGENT` by its exact name. That cleanup runs under a context of its own, bounded to 5 seconds, which a canceled request does not cancel. The tests check this request to tmux; that tmux then ends every window of the session is tmux's own behavior and is not tested;
- the manager's session query with injected process functions: called as root, it starts only its worker as UID and GID 1000, which queries the tmux server of `agent`, and a test fails when the query addresses another user; no tmux server counts as no session, while a refused connection, a failed or timed-out query, an unknown agent, and a duplicate session make the query fail;
- the session field of `agents status` in the manager with injected process functions: `running` while a session runs, `not running` after its agent exits, `not running` for an agent that is not enabled, and `running` for an agent that was disabled while its session still runs, without starting the agent's command.

No test covers the line that names a removed pin, which comes with pins in #43, or a failure while writing the selection after the command was removed.

The manager lock uses the native file lock of the platform the tests run on, so these manager tests also run on Windows; the manager itself ships only for Linux.

The process runner has its own tests on native Linux only. Run without privileges, a test asks for a different UID and GID and checks that the start fails instead of running under the caller's identity. Run as root, it checks that the process runs as UID and GID 1000 with no supplementary groups. Both checks also run with process group cleanup. Neither is a change of identity inside a sandbox. Further tests start a test program that leaves a child process holding its output open. When the run is canceled, or when the program exits while the child keeps running, they check that the runner returns within the bound instead of waiting for the child's output, and, polling briefly, that the child no longer runs afterwards. In a third case the child deliberately moves to a process group of its own before the run is canceled, so the cleanup does not kill it; this test checks only that the runner still returns within the bound. These test programs stand in for a probe; they are not an agent. No test runs npm against the registry, starts a real container, runs a real agent's status probe, removes an agent from a real sandbox, signs in to an agent, or starts a real tmux session. Nothing on this page has been confirmed on a live host. One real login per agent is a manual check that comes with #64. In the [live suite](live-suite.md), the identities in a real sandbox come with #24, and a real `agents enable` with an npm install comes with #68; neither exists yet. No live test runs `up --agents` or reads the `AGENTS` column of a real sandbox.
