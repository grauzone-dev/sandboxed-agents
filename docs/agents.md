# Agents

An agent is a coding CLI from the agent catalog. `sandboxed-agents agents enable NAME AGENT` installs it into the home volume of the sandbox `NAME` and records it as enabled there. Agents in one sandbox share its user `agent`, its files, and the credentials stored in its home volume.

This version ships only `agents enable NAME AGENT`, without options. The other `agents` commands, `--version` and `--force`, `up NAME --agents`, and the enabled agents in the `AGENTS` column of `list` come with later Stories: logins (#41), status (#42), pins and updates (#43), runs (#44), agent sessions (#45, #46), and `up --agents` and `list` (#69).

## Agent catalog

The catalog lists the agents that `sandboxed-agents` can install. It is data embedded in the executable (see [Catalog data format](#catalog-data-format)). This version delivers four agents:

| Agent | Program | npm package | Command | Login workflows | Documentation |
| --- | --- | --- | --- | --- | --- |
| `copilot` | GitHub Copilot CLI | `@github/copilot` | `copilot` | `github` | [Copilot CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference) |
| `claude` | Claude Code | `@anthropic-ai/claude-code` | `claude` | `subscription`, `console` | [Claude Code CLI reference](https://code.claude.com/docs/en/cli-reference) |
| `codex` | Codex CLI | `@openai/codex` | `codex` | `chatgpt`, `api-key` | [Codex CLI reference](https://developers.openai.com/codex/cli/reference/) |
| `opencode` | OpenCode | `opencode-ai` | `opencode` | `provider` | [OpenCode CLI](https://opencode.ai/docs/cli/) |

Each login workflow names the arguments that the later `agents login` command (#41) passes to the agent's own command:

| Agent | Workflow | Runs | Signs in with |
| --- | --- | --- | --- |
| `copilot` | `github` | `copilot login --device-code` | a GitHub account, through the OAuth device code flow |
| `claude` | `subscription` | `claude auth login` | a Claude subscription |
| `claude` | `console` | `claude auth login --console` | Anthropic Console, billed by API usage |
| `codex` | `chatgpt` | `codex login --device-auth` | a ChatGPT account, through the OAuth device code flow |
| `codex` | `api-key` | `codex login --with-api-key` | an OpenAI API key read from standard input |
| `opencode` | `provider` | `opencode auth login` | the credentials of a provider you choose |

Only Claude Code declares a status probe, `claude auth status`, because its CLI reference documents JSON output for that command. The other three agents declare none; for them, the later `agents status` command (#42) will report the sign-in state as unknown.

## Enable an agent

```sh
sandboxed-agents agents enable NAME AGENT
```

`NAME` is the sandbox and `AGENT` is a name from the [agent catalog](#agent-catalog). The sandbox must be running.

When the agent is not enabled yet, the manager installs the `latest` version of the agent's npm package at that moment into `/home/agent/.local` in the home volume. It then records the agent in the agent selection, `/home/agent/.local/state/sandboxed-agents/selection.json`, and the command exits with status 0. Enabling an agent leaves the other contents of the home volume unchanged.

When the agent is already enabled, `agents enable` runs no installation, prints the installed version of the package, and exits with status 0. It does not move the agent to a newer version; that is what the later `agents update` (#43) is for.

The installed agent and the selection live in the home volume, so they survive `stop`, `start`, and `restart`. They also survive `remove NAME` without `--volumes`, and the next `up NAME` adopts the volume ([Kept volumes](sandboxes.md#kept-volumes)).

### How the request reaches the manager

The executable addresses the in-container manager as container root, as for every administrative control call (ADR-0006):

```sh
podman exec --user=0:0 sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager agents enable AGENT
```

On Windows, the call also names the [selected Podman machine](sandboxes.md#target-on-windows) with `--connection`.

The manager does not install the agent as root. It starts its own trusted worker, the same manager program from the image, with the identity of the user `agent`: UID 1000, GID 1000, and no supplementary groups. The worker reads the home volume only after this change of identity. It takes the manager lock, reads the selection, runs npm and every other command of the installation, and writes the selection, all as `agent`. Every file the installation creates in the home volume therefore belongs to `agent`.

While the manager runs as root, it starts no program and no script from the home volume or the workspace, and it takes no command or argument from data stored there. A selection file or an npm configuration in the home volume that names a command can only make that command run as UID and GID 1000.

### Manager lock

The manager serializes changes to the agent installations with one lock, the file `/home/agent/.local/state/sandboxed-agents/manager.lock` in the home volume. It is an advisory lock held through the operating system, so the operating system releases it when the process that holds it ends, also after a crash.

`agents enable` holds the lock from before it reads the selection until after it has written it. A second `agents enable` on the same sandbox, for the same or another agent, waits until the first has released the lock. Two concurrent calls for different agents therefore leave both agents in the selection. The later Stories take the same lock for `agents update`, `agents disable`, and for starting and stopping agent sessions.

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

An owner conflict on a stopped sandbox is therefore reported as the owner conflict, not as the stopped sandbox. If npm or another step of the installation fails, `agents enable` reports the failure and exits with status 1.

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

The behavior on this page is covered by offline tests: `agents enable` at the CLI boundary against a fake `podman`, and the manager's catalog reading, installation, selection handling, and lock with injected process functions, including two concurrent calls and a fifth catalog entry ([Development](development.md#test-seams)). These tests check the identity each process would start with and the arguments passed to Podman and npm. No offline test runs npm against the registry, starts a real container, or signs in to an agent. Nothing on this page has been confirmed on a live host; a real npm install belongs to the live suite (#24).
