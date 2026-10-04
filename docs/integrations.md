# Integrations

An integration connects a sandbox to an external account or service, such as Git, GitHub, Azure, or Azure DevOps. It offers login workflows, config workflows, or both, and has no installable program of its own. The workflow handlers are built into the in-container manager, which the executable reaches through `podman exec`. The catalog of integrations and workflows is also embedded in the executable, so that names are checked on the host before Podman is called.

This version delivers the two config workflows of `git`: `identity` sets the commit name and email of a sandbox ([Git identity](#git-identity)), and `credentials` makes Git store HTTPS credentials in the sandbox ([Git credentials](#git-credentials)). The login workflows of `github` (#51), `azure` (#50), and `azdo` (#49) arrive with later Stories.

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
| `integrations config` | `git` | `git`: `identity`, `credentials` |
| `integrations login` | none yet | none yet |

As a result:

- `integrations config NAME github` fails as an unknown integration and lists `git`.
- `integrations config NAME git` without a workflow name fails and lists `identity` and `credentials`.
- `integrations login NAME github`, `azure`, or `azdo` fails as an unknown integration and says that no valid names are available yet.
- `integrations login NAME git` fails because `git` has no login workflow, and names its config workflows `identity` and `credentials`.

All of these fail before Podman is called.

## Git identity

```sh
sandboxed-agents integrations config NAME git identity [--name N] [--email E]
```

The command writes the commit name and email to the global Git configuration of the user `agent` in the sandbox. The workflow name `identity` is required, because `git` has two config workflows.

Each option is given as `--name N` or `--name=N`, at most once. A value given as a separate word must not start with `--`, and no value may contain a NUL character. The name must not be empty, because Git refuses to commit with an empty author name: `--name=` and `--name ''` fail as a missing value for `--name` before Podman is called.

### Prompts

With both options given, the command needs no terminal and does not prompt. With one or both missing, it needs standard input and standard output to be a terminal. It then prompts for each missing value, with `Git commit name:` and `Git commit email:`, and sets what you enter. All missing values are read before Git is called, so input that ends early, contains a NUL character, or leaves the name empty fails and changes nothing.

Without a terminal, a command with a missing option fails, says that it needs a terminal or both options, and changes nothing. Pass `--name` and `--email` explicitly in scripts.

The name and the email are written by two separate Git calls. When the second call fails, the name stays written; run the command again to set both.

### Scope and persistence

The global Git configuration of `agent` lives in the sandbox's home volume. The identity therefore applies to every repository in the sandbox and to every agent: agents in one sandbox share its user and files, so all of them commit with the same name and email. It is kept across `stop` and `start`, and across `update`, which mounts the same home volume in the new container ([Update a sandbox](updates.md#what-update-keeps)).

## Git credentials

```sh
sandboxed-agents integrations config NAME git credentials
```

The command configures the credential helper that Git in the sandbox uses for HTTPS remotes. The manager runs one Git call as `agent`, with the same fixed environment as for the identity:

```sh
git config --global --replace-all -- credential.helper 'store --file=/home/agent/.git-credentials'
```

This replaces every `credential.helper` entry in the global Git configuration of `agent`. It does not change the system Git configuration or a repository's own configuration, and a credential helper configured there can still change which helpers Git asks.

The command stores no credentials itself and creates no credential file. The helper takes effect the next time Git needs credentials for an HTTPS remote. Git then asks for a username and password, for many hosts a personal access token, and once the remote accepts them, the helper stores them and Git uses them from then on without asking. Git asks on the terminal of the program that runs it, so an agent without a terminal cannot answer. Enter the credentials once yourself, for example with `git clone` or `git push` in [`sandboxed-agents shell NAME`](ssh.md#open-a-shell).

### Why `credential-store`

The helper is Git's built-in [`credential-store`](https://git-scm.com/docs/git-credential-store):

- It ships with the Git already in the sandbox image, so the integration installs nothing.
- It needs no daemon, keyring, desktop session, or browser, none of which a sandbox has.
- `--file` fixes the store at `/home/agent/.git-credentials`, a path in the home volume, so the credentials outlast the container.

### Terminal

Configuring the helper needs no terminal: the command takes no options, prompts for nothing, and behaves the same with and without a terminal. Only the later Git operation that asks for credentials needs one.

### Storage and who can read it

`credential-store` writes each credential as one line of plain text, a URL with the username and password. The file is not encrypted; Git protects it only with file permissions against other users. Every agent in the sandbox runs as `agent` ([Execution identities](sandboxes.md#execution-identities)), so every agent, shell, and program of the sandbox can read the stored credentials and use them.

The file lies in the home volume. It is kept across `stop`, `start`, and [`update`](updates.md#what-update-keeps), and across `remove NAME` without `--volumes`. `remove NAME --volumes` deletes the home volume and the credentials with it ([Remove a sandbox](sandboxes.md#remove-a-sandbox)).

## What the host contributes

The commit name and email come only from the options you pass and the values you type, and credentials only from what you enter inside the sandbox. Neither workflow imports host Git configuration or host credentials, and neither adds a mount. Each `integrations` command treats the host environment the same way:

- It forwards no Git-related host variable into the sandbox. The Podman calls that look up the sandbox, query the manager, and run the workflow leave out every variable whose name starts with `GIT_`, and `EMAIL`. The `podman exec` call of the workflow sets only `HOME=/home/agent` in the container, and the manager runs Git with a fixed `HOME`, `USER`, `LOGNAME`, and `PATH`.
- It reads `SANDBOXED_AGENTS_GROUP` to select the [controller group](sandboxes.md#controller-groups), as every command does.
- On Windows, it selects the Podman machine before its first lookup of the sandbox, as `start` does ([Target on Windows](sandboxes.md#target-on-windows)). The selection runs only the read-only queries `podman machine list` and `podman machine inspect`, under the environment rule of every Windows Podman call: `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` are removed. The Git-related filter above does not apply to these queries.

The result is the same whether or not the host has a Git configuration or Git-related variables.

## Failures

Every `integrations` command needs a running container of a known sandbox. It never starts, creates, or repairs anything to get one:

- **Unknown sandbox.** The name does not exist in the current controller group. The command fails and says so.
- **Only volumes remain.** The sandbox has volumes but no container. The command fails and names `sandboxed-agents up NAME`, which adopts the volumes.
- **Stopped sandbox.** The command fails, names `sandboxed-agents start NAME`, and starts nothing.
- **Manager does not answer.** The container runs, but its manager does not answer the session query. The command fails, says so, and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step. It attempts nothing around the manager and issues no further Podman call.
- **Workflow failure.** Git could not be started or exited with a non-zero status. The command shows Git's output and the manager's message with Git's exit status, and reports that the integration workflow failed.
- **Workflow timeout.** For `git credentials`, and for `git identity` with both `--name` and `--email` given, the `podman exec` call that runs the workflow has 30 seconds to finish. When it does not, the executable ends that call and the command fails. When the command prompts for a missing value, this call has no added deadline, so it waits for your input.

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

The behavior on this page is covered by offline tests against a fake `podman` and against the manager with injected process functions ([Development](development.md#test-seams)). For `git credentials`, a manager test checks that the workflow reads no input and makes exactly the one `git config --global --replace-all -- credential.helper 'store --file=/home/agent/.git-credentials'` call with the fixed environment. A CLI test runs the command without a terminal and checks that it ends with the session query and one `podman exec --user=1000:1000 --env HOME=/home/agent` call of the manager, without `-it`. No test runs Git against an HTTPS remote or looks at a stored credential file or its permissions. Nothing on this page has been confirmed against Podman on a live host, and no test observes the isolation of the sandbox or a real Git configuration in its home volume. The [live suite](live-suite.md) does not run `integrations` commands.
