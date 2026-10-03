# Integrations

An integration connects a sandbox to an external account or service, such as Git, GitHub, Azure, or Azure DevOps. It offers login workflows, config workflows, or both, and has no installable program of its own. The workflow handlers are built into the in-container manager, which the executable reaches through `podman exec`. The catalog of integrations and workflows is also embedded in the executable, so that names are checked on the host before Podman is called.

This version delivers one workflow: the config workflow `identity` of `git`, which sets the commit name and email of a sandbox ([Git identity](#git-identity)). The other workflows of the first version arrive with later Stories: `credentials` of `git` (#48), and the login workflows of `github` (#51), `azure` (#50), and `azdo` (#49).

## Command line

```sh
sandboxed-agents integrations config NAME INTEGRATION [WORKFLOW] [OPTIONS]
sandboxed-agents integrations login NAME INTEGRATION [WORKFLOW]
```

`NAME` is the sandbox, `INTEGRATION` an integration from the catalog, and `WORKFLOW` one of its config or login workflows. The workflow name is optional when the integration has exactly one workflow of that kind, and required when it has more than one. Without it, the command then fails and lists the valid workflow names.

## Catalog

Only delivered integrations and workflows are valid names. One whose Story is not implemented yet is rejected like an unknown name, and the list of valid names in the message shows only what is delivered for that kind of command:

| Command | Valid integrations | Valid workflows |
| --- | --- | --- |
| `integrations config` | `git` | `git`: `identity` |
| `integrations login` | none yet | none yet |

As a result:

- `integrations config NAME github` fails as an unknown integration and lists `git`.
- `integrations config NAME git credentials` fails as an unknown workflow and lists `identity`.
- `integrations login NAME github`, `azure`, or `azdo` fails as an unknown integration and says that no valid names are available yet.
- `integrations login NAME git` fails because `git` has no login workflow, and names its config workflows, `identity` for now.

All of these fail before Podman is called.

## Git identity

```sh
sandboxed-agents integrations config NAME git [identity] [--name N] [--email E]
```

The command writes the commit name and email to the global Git configuration of the user `agent` in the sandbox. Since `identity` is the only config workflow of `git`, `integrations config NAME git` runs it as well. Once `credentials` is added (#48), the workflow name becomes required.

Each option is given as `--name N` or `--name=N`, at most once. A value given as a separate word must not start with `--`, and no value may contain a NUL character.

### Prompts

With both options given, the command needs no terminal and does not prompt. With one or both missing, it needs standard input and standard output to be a terminal. It then prompts for each missing value, with `Git commit name:` and `Git commit email:`, and sets what you enter. All missing values are read before Git is called, so input that ends early or contains a NUL character changes nothing.

Without a terminal, a command with a missing option fails, says that it needs a terminal or both options, and changes nothing. Pass `--name` and `--email` explicitly in scripts.

The name and the email are written by two separate Git calls. When the second call fails, the name stays written; run the command again to set both.

### Scope and persistence

The global Git configuration of `agent` lives in the sandbox's home volume. The identity therefore applies to every repository in the sandbox and to every agent: agents in one sandbox share its user and files, so all of them commit with the same name and email. It is kept across `stop` and `start`. Keeping the home volume, and with it the identity, across `update` arrives with #52.

### What the host contributes

The name and email come only from the options you pass and the values you type. The command imports no host Git configuration and no host credentials:

- It forwards no Git-related host variable. The environment of its Podman calls leaves out every variable whose name starts with `GIT_`, and `EMAIL`. The `podman exec` call of the workflow sets only `HOME=/home/agent` in the container, and the manager runs Git with a fixed `HOME`, `USER`, `LOGNAME`, and `PATH`.
- It reads `SANDBOXED_AGENTS_GROUP` to select the [controller group](sandboxes.md#controller-groups), as every command does.
- On Windows, it selects the Podman machine before its first Podman call, as `start` does ([Target on Windows](sandboxes.md#target-on-windows)).

The result is the same whether or not the host has a Git configuration or Git-related variables.

## Failures

Every `integrations` command needs a running container of a known sandbox. It never starts, creates, or repairs anything to get one:

- **Unknown sandbox.** The name does not exist in the current controller group. The command fails and says so.
- **Only volumes remain.** The sandbox has volumes but no container. The command fails and names `sandboxed-agents up NAME`, which adopts the volumes.
- **Stopped sandbox.** The command fails, names `sandboxed-agents start NAME`, and starts nothing.
- **Manager does not answer.** The container runs, but its manager does not answer the session query. The command fails, says so, and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step. It attempts nothing around the manager and issues no further Podman call.
- **Workflow failure.** Git could not be started or exited with a non-zero status. The command shows Git's output and the manager's message with Git's exit status, and reports that the integration workflow failed.

Owner conflicts and interrupted updates are refused as for the other commands ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)).

The command exits with status 0 when it did what was asked, and with status 1 when it could not or refused. The exit status of Git is not passed through.

### Order of checks

`integrations` commands run their checks in the order described in [Development](development.md#order-of-checks) and report only the first failure:

| Step | What the command does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error, an invalid sandbox name, or an unknown or missing integration or workflow name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox name. When no container exists, reports an owner conflict on the remaining volumes or the backup container or, when only owned volumes and no backup container remain, that no container exists. |
| 4. Owner | reports an owner conflict on the container, its volumes, or the backup container, names the Podman objects concerned, and points to Podman, also when only a volume has a missing or different owner |
| 5. Interrupted update | reports a backup container with the current owner |
| 6. Running state | reports a stopped sandbox |
| 7. Preconditions | reports a manager that does not answer |
| 8. Terminal | `git identity` with `--name` or `--email` missing: reports a missing terminal |

The preflight (step 2) and the session guard (step 9) do not apply. An unknown workflow name is therefore reported ahead of an unknown sandbox, an owner conflict ahead of a stopped sandbox, and a stopped sandbox or a manager that does not answer ahead of a missing terminal.

## Verification

The behavior on this page is covered by offline tests against a fake `podman` and against the manager with injected process functions ([Development](development.md#test-seams)). Nothing on this page has been confirmed against Podman on a live host, and no test observes the isolation of the sandbox or a real Git configuration in its home volume.
