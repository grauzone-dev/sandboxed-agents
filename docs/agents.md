# Agents

An agent is a coding CLI from the agent catalog. `sandboxed-agents agents enable NAME AGENT` installs it into the home volume of the sandbox `NAME` and records it as enabled there. Agents in one sandbox share its user `agent`, its files, and the credentials stored in its home volume.

This page covers `agents enable NAME AGENT`, which takes no options, `up NAME --agents LIST`, which enables agents as part of `up`, and the `AGENTS` column of `list`.

## Agent catalog

The catalog lists the agents that `sandboxed-agents` can install. It is data embedded in the executable (see [Catalog data format](#catalog-data-format)). This version delivers four agents:

| Agent | Program | npm package | Command | Login workflows | Documentation |
| --- | --- | --- | --- | --- | --- |
| `copilot` | GitHub Copilot CLI | `@github/copilot` | `copilot` | `github` | [Copilot CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference) |
| `claude` | Claude Code | `@anthropic-ai/claude-code` | `claude` | `subscription`, `console` | [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference) |
| `codex` | Codex CLI | `@openai/codex` | `codex` | `chatgpt`, `api-key` | [Codex CLI reference](https://developers.openai.com/codex/cli/reference/) |
| `opencode` | OpenCode | `opencode-ai` | `opencode` | `provider` | [OpenCode CLI](https://opencode.ai/docs/cli/) |

Each login workflow names the arguments passed to the agent's own command to sign in. No command runs them yet; signing in comes with #41.

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

The installation, the npm cache, and the manager's own state in `/home/agent/.local/state/sandboxed-agents` all lie below `/home/agent/.local`. The manager removes no existing file of the home volume, credentials included. npm and the package's install scripts run as `agent`, UID and GID 1000, like any other work of that user, so they can still write elsewhere in the home volume; what they create belongs to `agent`.

When the agent is already enabled, `agents enable` runs no installation and leaves the selection as it is. It prints the same line with the installed version and exits with status 0. It does not move the agent to a newer version.

The installed agent and the selection live in the home volume, so they survive `stop`, `start`, and `restart`. They also survive `remove NAME` without `--volumes`, and the next `up NAME` adopts the volume ([Kept volumes](sandboxes.md#kept-volumes)).

### How the request reaches the manager

The executable addresses the in-container manager as container root, as for every administrative control call (ADR-0006). It first checks that the manager answers:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager version
```

The manager answers when this call exits with status 0 within 30 seconds and prints one line `sandboxed-agents-manager VERSION`. The executable then passes the request on:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents enable AGENT
```

The manager's output and the output of npm pass through to your terminal.

On Windows, both calls also name the [selected Podman machine](sandboxes.md#target-on-windows) with `--connection`.

The manager does not install the agent as root. It starts its own trusted worker, the same manager program from the image, with the identity of the user `agent`: UID 1000, GID 1000, and no supplementary groups. The worker gets a fixed environment (`HOME`, `USER`, `LOGNAME`, `SHELL`, and a `PATH` that starts with `/home/agent/.local/bin`) and inherits none of root's. It reads the home volume only after this change of identity. It takes the manager lock, reads the selection, runs npm and every other command of the installation, and writes the selection, all as `agent`. Every file the installation creates in the home volume therefore belongs to `agent`.

While the manager runs as root, it starts no program and no script from the home volume or the workspace, and it takes no command or argument from data stored there. A selection file or an npm configuration in the home volume that names a command can only make that command run as UID and GID 1000.

### Manager lock

The manager serializes changes to the agent installations with one lock, the file `/home/agent/.local/state/sandboxed-agents/manager.lock` in the home volume. It is an advisory lock held through the operating system, so the operating system releases it when the process that holds it ends, also after a crash.

`agents enable` holds the lock from before it reads the selection until after it has written it. A second `agents enable` on the same sandbox, for the same or another agent, waits until the first has released the lock. Two concurrent calls for different agents therefore leave both agents in the selection.

## Enable agents with `up`

```sh
sandboxed-agents up NAME --agents a,b
```

`--agents` takes a comma-separated list of agent names. It is given as `--agents VALUE` or `--agents=VALUE`, at most once. A name given more than once is enabled once. An option without a value, given twice, or with an empty agent name, such as `--agents=` or `--agents claude,,codex`, is a usage error that names `--agents`.

`up` checks every name against the [agent catalog](#agent-catalog) embedded in the executable before it calls Podman, at the same step as its other usage checks. An unknown name, or a catalog name whose agent is not delivered yet, makes `up` exit with status 1 and list the valid agent names. It creates no sandbox, volume, or image.

`up NAME --agents a,b` has the same result as `up NAME` followed by `agents enable NAME a` and `agents enable NAME b`: the same selection and the same installations. `--agents` is not part of the sandbox's configuration, so it also applies to a sandbox that exists already. `up` starts such a sandbox as it would without `--agents` and enables each listed agent that is not enabled yet. An agent that is already enabled stays as it is, without an installation, as with a repeated `agents enable`. `up` exits with status 0 when every listed agent is enabled at the end.

`up` collects the output of the installations and prints it after the last attempt. When an installation fails, `up` asks the manager for its version again. When the manager answers, `up` still attempts the remaining agents. The sandbox stays and keeps running, and agents already enabled stay enabled. `up` exits with status 1 and names the failed agents together with `sandboxed-agents agents enable NAME AGENT` to retry each.

When the manager does not answer, before the first agent or after a failed one, the sandbox stays, and agents enabled before then stay enabled. `up` discards the collected output, exits with status 1, and prints a single message about the manager, not one per agent. The message says that `up` cannot confirm which agents were enabled and names `sandboxed-agents check NAME` for diagnosis and the `agents enable` command for every listed agent.

[Enable agents with `up`](sandboxes.md#enable-agents-with-up) describes how this step fits into `up`.

## See the enabled agents

The agent selection lives only in the home volume; host state keeps no copy of it. `sandboxed-agents list` therefore reads it through the manager of each running sandbox:

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents list
```

The manager answers with a JSON array of the enabled agent names, sorted by name, and `list` waits at most 5 seconds for it. Like `agents enable`, the manager starts its trusted worker as `agent`, UID and GID 1000, which reads the selection. The query takes no [manager lock](#manager-lock) and creates no file, also when the selection does not exist yet. The manager saves the selection atomically, so the query reads either the selection before a concurrent `agents enable` or the one after it.

The `AGENTS` column shows the names in alphabetical order, separated by commas, or `none` when the manager answers and no agent is enabled. It shows `-`, meaning "not available", for a sandbox that is not running and for one whose manager does not answer in time, fails, or answers with anything but a JSON array of names from the catalog. A selection file that cannot be read as a JSON object, or that names an unknown agent, makes the query fail. `list` starts nothing to read the agents and still exits with status 0 ([`AGENTS` column](sandboxes.md#agents-column)).

## Refusals

`agents enable` exits with status 1 and installs nothing in these cases. When several apply, it reports the first in the [order of checks](development.md#order-of-checks):

| Step | Refusal |
| --- | --- |
| 1. Usage and names | An invalid controller group, a usage error, or an invalid sandbox name. An unknown agent name lists the valid agent names. A catalog name whose agent is not delivered yet counts as unknown and is not listed; in this version all four agents are delivered. None of these calls Podman. |
| 3. Sandbox existence | An unknown sandbox, also one of another controller group. A sandbox of which only volumes remain is refused with a message naming `sandboxed-agents up NAME`, which adopts the volumes. |
| 4. Owner | The container, one of the volumes, or the backup container has a missing or different owner label. The message names each such Podman object and points to Podman, also when the container carries the current owner and only a volume does not ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)). |
| 5. Interrupted update | A backup container with the current owner exists. The message names `sandboxed-agents update NAME`. |
| 6. Running state | The sandbox is stopped. The message names `sandboxed-agents start NAME`, and nothing starts on its own. |
| 7. Preconditions | The manager does not answer. The message says so and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step. `agents enable` issues no further call into the sandbox. |

This version has no `check NAME` yet; the check of one sandbox comes with #20. Until then, `sandboxed-agents check` without a name checks only the host prerequisites and does not diagnose a sandbox or its manager. `restart NAME` is available ([Stop, start, and restart a sandbox](sandboxes.md#stop-start-and-restart-a-sandbox)).

An owner conflict on a stopped sandbox is therefore reported as the owner conflict, not as the stopped sandbox. If npm or another step of the installation fails, `agents enable` reports the failure, leaves the selection unchanged, and exits with status 1.

## Catalog data format

The catalog is the file `internal/agentcatalog/catalog.json` in the repository. The host executable `sandboxed-agents` and the manager `sandboxed-agents-manager` both embed it and read it through the same Go types in `internal/agentcatalog` (ADR-0001). The host uses it to check agent names before any Podman call; the manager uses it to install.

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

Offline tests cover the behavior on this page ([Test seams](development.md#test-seams)):

- `agents enable` at the CLI boundary against a fake `podman`, on the Linux and the Windows target, including the order of checks and a fifth catalog entry;
- the manager's catalog reading, installation, selection handling, and lock with injected process functions. These tests check the identity and arguments each process is requested with, including two concurrent calls and a failed installation;
- a selection in a temporary home directory that stays byte for byte unchanged across `stop` and `start` against a fake `podman`, after which a repeated `agents enable` runs no installation. The test does not stop or start a real container;
- `up NAME --agents` at the CLI boundary against a fake `podman`, on the Linux and the Windows target: a new sandbox; a stopped and a running existing sandbox, routed to a real manager with injected process functions, where an enabled agent is not reinstalled; invalid selections and undelivered names without a Podman call; failed installations with retry commands; and a manager that does not answer before the first or after a failed installation;
- the `AGENTS` column of `list` against a fake `podman`: names, `none`, a stopped sandbox, unusable or failed answers, the 5-second bound, the Windows target, and `agents enable` followed by `list` with a fifth catalog entry;
- the manager's `agents list` query: a missing and a written selection, the worker started as UID and GID 1000, a refused identity, and invalid selection files left unchanged.

The manager lock uses the native file lock of the platform the tests run on, so these manager tests also run on Windows; the manager itself ships only for Linux.

The process runner has its own tests on native Linux only. Run without privileges, a test asks for a different UID and GID and checks that the start fails instead of running under the caller's identity. Run as root, it checks that the process runs as UID and GID 1000 with no supplementary groups. Neither is a change of identity inside a sandbox. No test runs npm against the registry, starts a real container, or signs in to an agent. Nothing on this page has been confirmed on a live host. In the [live suite](live-suite.md), the identities in a real sandbox come with #24, and a real `agents enable` with an npm install comes with #68; neither exists yet. No live test runs `up --agents` or reads the `AGENTS` column of a real sandbox.
