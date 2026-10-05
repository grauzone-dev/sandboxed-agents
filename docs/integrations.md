# Integrations

An integration connects a sandbox to an external account or service, such as Git, GitHub, Azure, or Azure DevOps. It offers login workflows, config workflows, or both, and has no installable program of its own. The workflow handlers are built into the in-container manager, which the executable reaches through `podman exec`. The catalog of integrations and workflows is also embedded in the executable, so that names are checked on the host before Podman is called.

This version delivers the two config workflows of `git` and the login workflows of `github` and `azure`. `identity` sets the commit name and email of a sandbox ([Git identity](#git-identity)), `credentials` makes Git store HTTPS credentials in the sandbox ([Git credentials](#git-credentials)), `github device` signs the sandbox in to GitHub ([GitHub login](#github-login)), and `azure device` signs the Azure CLI of the sandbox in to an Azure account ([Azure login](#azure-login)). The login workflow of `azdo` (#49) arrives with a later Story.

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
| `integrations login` | `github`, `azure` | `github`: `device`; `azure`: `device` |

As a result:

- `integrations config NAME github` fails as an unknown integration and lists `git`.
- `integrations config NAME git` without a workflow name fails and lists `identity` and `credentials`.
- `integrations login NAME azdo` fails as an unknown integration and lists `github` and `azure`.
- `integrations login NAME github WORKFLOW` or `integrations login NAME azure WORKFLOW` with any name other than `device` fails as an unknown workflow and lists `device`.
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

The command stores no credentials itself and creates no credential file. The helper takes effect the next time Git needs credentials for an HTTPS remote. When no helper has credentials for that remote, Git asks for a username and password, for many hosts a personal access token, and once the remote accepts them, the helper stores them and Git uses them from then on without asking. Git asks through the program named by `GIT_ASKPASS`, `core.askPass`, or `SSH_ASKPASS`, the first of them that is set in the sandbox, and otherwise on the terminal ([gitcredentials](https://git-scm.com/docs/gitcredentials), "Requesting credentials"). Git started without a terminal and without such a program therefore cannot ask. The usual way is to enter the credentials once yourself, for example with `git clone` or `git push` in [`sandboxed-agents shell NAME`](ssh.md#open-a-shell).

### Why `credential-store`

The helper is Git's built-in [`credential-store`](https://git-scm.com/docs/git-credential-store):

- It ships with the Git already in the sandbox image, so the integration installs nothing.
- It needs no daemon, keyring, desktop session, or browser.
- `--file` fixes the store at `/home/agent/.git-credentials`, a path in the home volume, so the credentials outlast the container.

### Terminal

Configuring the helper never needs a terminal: the command takes no options, prompts for nothing, and behaves the same with and without a terminal. A later Git operation needs one only to enter credentials that no helper provides when no askpass program is set ([Git credentials](#git-credentials)).

### Storage and who can read it

`credential-store` writes each credential as one line of plain text, a URL with the username and password. The file is not encrypted; Git protects it only with file permissions against other users. Every agent in the sandbox runs as `agent` ([Execution identities](sandboxes.md#execution-identities)), so every agent, shell, and program of the sandbox can read the stored credentials and use them.

The file lies in the home volume. It is kept across `stop`, `start`, and [`update`](updates.md#what-update-keeps), and across `remove NAME` without `--volumes`. `remove NAME --volumes` deletes the home volume and the credentials with it ([Remove a sandbox](sandboxes.md#remove-a-sandbox)).

## GitHub login

```sh
sandboxed-agents integrations login NAME github [device]
```

The command signs the GitHub CLI in the sandbox in to `github.com` with a device-code login and then makes Git in the sandbox use that login for `github.com` and `gist.github.com` remotes. Afterwards, `git clone` and `git push` over HTTPS to a repository your GitHub account can reach ask for no further credentials. The GitHub CLI is part of the base image, so no toolchain is needed.

`device` is the only login workflow of `github`, so `integrations login NAME github` and `integrations login NAME github device` do the same. The command takes no options.

### What the workflow runs

After the [checks](#order-of-checks), the executable starts the manager as `agent` in an interactive `podman exec` with a terminal:

```sh
podman exec --user=1000:1000 --env HOME=/home/agent -it sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager integrations login github device
```

The manager refuses the request under any identity other than UID and GID 1000. It runs the GitHub CLI with UID and GID 1000 stated explicitly, in `/home/agent`, with a fixed environment: `HOME=/home/agent`, `USER=agent`, `LOGNAME=agent`, `PATH=/usr/local/bin:/usr/bin:/bin`, `GH_CONFIG_DIR=/home/agent/.config/gh`, and `GH_PROMPT_DISABLED=1`. First it signs in ([`gh auth login`](https://cli.github.com/manual/gh_auth_login)):

```sh
gh auth login --hostname github.com --git-protocol https --web --scopes workflow
```

`--scopes workflow` requests the `workflow` scope in addition to the GitHub CLI's default scopes. Without it, GitHub refuses a push that adds or changes a GitHub Actions workflow file under `.github/workflows/`, unless the same file already exists on another branch ([Scopes for OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps)). The GitHub CLI 2.23.0 adds this scope on its own only in the interactive Git setup that `GH_PROMPT_DISABLED=1` skips ([`login_flow.go`, v2.23.0](https://github.com/cli/cli/blob/v2.23.0/pkg/cmd/auth/shared/login_flow.go)), so the manager requests it explicitly.

Only when this call ends with status 0 does the manager configure Git to use the GitHub CLI as credential helper for `github.com` and `gist.github.com` ([`gh auth setup-git`](https://cli.github.com/manual/gh_auth_setup-git)):

```sh
gh auth setup-git --hostname github.com
```

### Signing in

The login is GitHub's browser flow with a one-time device code, which `--web` selects. `GH_PROMPT_DISABLED=1` turns off every prompt of the GitHub CLI ([`default.go`, v2.23.0](https://github.com/cli/cli/blob/v2.23.0/pkg/cmd/factory/default.go)), so `gh auth login` runs non-interactively. In that mode the GitHub CLI asks no questions, sets up no Git credential helper of its own ([`login_flow.go`, v2.23.0](https://github.com/cli/cli/blob/v2.23.0/pkg/cmd/auth/shared/login_flow.go)), and launches no browser. It prints the one-time code and the address where you enter it, and waits for the authorization ([`flow.go`, v2.23.0](https://github.com/cli/cli/blob/v2.23.0/internal/authflow/flow.go)). Open that address in a browser on your own computer, enter the code, and authorize the GitHub CLI. The command waits until the GitHub CLI ends; the call has no added deadline. The GitHub CLI's output reaches you through the terminal of the interactive `podman exec`, which is why the command still requires one.

### Terminal

The login needs an interactive terminal: standard input and standard output must be a terminal. Without one, the command fails with `Integration login needs an interactive terminal` and starts no login. It reports this only after the manager has answered ([Order of checks](#order-of-checks)).

### Credentials and Git configuration

The base image installs the GitHub CLI from Debian bookworm, version 2.23.0 ([Debian package `gh`](https://packages.debian.org/bookworm/gh)). After a successful login, that version writes the token in plain text into its configuration, together with the user name and the Git protocol ([`login_flow.go`, v2.23.0](https://raw.githubusercontent.com/cli/cli/v2.23.0/pkg/cmd/auth/shared/login_flow.go)). `GH_CONFIG_DIR` fixes that configuration at `/home/agent/.config/gh`, in the home volume. Every agent, shell, and program of the sandbox runs as `agent` and can read the token and use it. It is kept across `stop`, `start`, and `remove NAME` without `--volumes`; `remove NAME --volumes` deletes the home volume and the token with it.

`gh auth setup-git --hostname github.com` writes the GitHub CLI as credential helper for two hosts, `https://github.com` and `https://gist.github.com`, into the global Git configuration of `agent`, which also lies in the home volume ([`setupgit.go`](https://github.com/cli/cli/blob/v2.23.0/pkg/cmd/auth/setupgit/setupgit.go) and [`git_credential.go`](https://github.com/cli/cli/blob/v2.23.0/pkg/cmd/auth/shared/git_credential.go), v2.23.0). For each of the two, it first replaces every existing helper entry for that host with an empty value and then adds the GitHub CLI. An empty helper value makes Git drop the helpers it has read from the configuration up to that entry ([gitcredentials](https://git-scm.com/docs/gitcredentials)). A helper configured earlier for one of these two hosts is therefore replaced, and for these two hosts Git no longer asks a helper for all hosts that comes before the new entries, such as one that [`git credentials`](#git-credentials) wrote before the login. Remotes on all other hosts, including other GitHub instances, keep the credential helpers configured for them. Only the manager configures Git, and only through `gh auth setup-git`: the login itself skips its own Git setup because prompting is disabled. When the login fails, the manager does not run `gh auth setup-git`, so the Git configuration stays unchanged. When the login succeeds but `gh auth setup-git` fails, the command fails too; the token from the login stays stored.

### Exit status

The command exits with status 0 when the login and the Git setup both ended with status 0, and with status 1 otherwise. The exit status of the GitHub CLI is not passed through; the message names the step, `login` or `setup-git`, and its exit status.

## Azure login

```sh
sandboxed-agents integrations login NAME azure [device]
```

The command signs the Azure CLI in the sandbox in to an Azure account with a device-code login. Afterwards, `az account show` in the sandbox uses that login. The Azure CLI is not part of the base image: the command needs a sandbox with the `azure` toolchain ([Toolchain](#toolchain)).

`device` is the only login workflow of `azure`, so `integrations login NAME azure` and `integrations login NAME azure device` do the same. The command takes no options.

### What the workflow runs

After the [checks](#order-of-checks), the executable starts the manager as `agent` in an interactive `podman exec` with a terminal:

```sh
podman exec --user=1000:1000 --env HOME=/home/agent -it sandboxed-agents.GROUP.NAME /usr/local/bin/sandboxed-agents-manager integrations login azure device
```

The manager refuses the request under any identity other than UID and GID 1000. It runs the Azure CLI once, with UID and GID 1000 stated explicitly, in `/home/agent`, with a fixed environment: `HOME=/home/agent`, `USER=agent`, `LOGNAME=agent`, `PATH=/usr/local/bin:/usr/bin:/bin`, and `AZURE_CONFIG_DIR=/home/agent/.azure`:

```sh
az login --use-device-code
```

### Signing in

The login is Microsoft's device code flow. Without `--use-device-code`, the Azure CLI on Linux tries to open a browser for the sign-in first; the option forces the device code flow, which Microsoft names for the case that no web browser is available ([Sign in with Azure CLI at a command line](https://learn.microsoft.com/cli/azure/authenticate-azure-cli-interactively)). No browser opens in the container. The Azure CLI shows an address and a code. Open that address in a browser on your own computer, enter the code, and sign in with your account. The command waits until the Azure CLI ends; the call has no added deadline.

When your account can reach more than one subscription, the Azure CLI asks after the sign-in which subscription and tenant to use ([Subscription selector](https://learn.microsoft.com/cli/azure/authenticate-azure-cli-interactively#subscription-selector)). Its output and prompts reach you through the terminal of the interactive `podman exec`, and your answer goes back the same way, which is why the command requires a terminal.

### Toolchain

The Azure CLI comes with the `azure` toolchain ([Toolchain image contents](images.md#toolchain-image-contents)). The executable reads the sandbox's toolchain set from the container label `io.github.sandboxed-agents.toolchains`. When the set does not contain `azure`, the command fails, names the `azure` toolchain, and prints the complete command that adds it:

```text
Azure login requires the azure toolchain; run sandboxed-agents update agent01 --with azure,dotnet
```

`update NAME --with SET` replaces the recorded set instead of adding to it ([Change the toolchain set](updates.md#change-the-toolchain-set)), so the printed set is the recorded set plus `azure`, in canonical form: the names sorted and separated by commas. Every recorded toolchain stays in it. A sandbox recorded with `dotnet` gets `--with azure,dotnet`, as above, and one recorded with `dotnet,native` gets `--with azure,dotnet,native`. A sandbox without toolchains, including one created with `--with none`, gets `--with azure`; `none` never appears in the printed set.

A recorded set that is not a valid toolchain set, such as one with an unknown name, fails at the same step with a message that lists the valid toolchain values and names the unknown toolchain when there is one; no `update` command is printed.

A missing toolchain or an invalid recorded set is reported only after the manager has answered, and ahead of a missing terminal ([Order of checks](#order-of-checks)). The command then starts no workflow and writes nothing in the sandbox.

### Terminal

The login needs an interactive terminal: standard input and standard output must be a terminal. Without one, the command fails with `Integration login needs an interactive terminal` and starts no login. It reports this only after the manager has answered and the `azure` toolchain has been found ([Order of checks](#order-of-checks)).

### Credentials

`AZURE_CONFIG_DIR` fixes the configuration directory of the Azure CLI at `/home/agent/.azure`, in the home volume. The Azure CLI keeps its configuration in that directory ([Azure CLI configuration options](https://learn.microsoft.com/cli/azure/azure-cli-configuration)), and it stores the token cache of the login in the same directory ([`identity.py`, 2.90.0](https://github.com/Azure/azure-cli/blob/azure-cli-2.90.0/src/azure-cli-core/azure/cli/core/auth/identity.py#L74-L77)), so the login lies in `/home/agent/.azure`, in the home volume. On Linux, the Azure CLI saves its token cache as plain-text files, not encrypted ([MSAL-based Azure CLI](https://learn.microsoft.com/cli/azure/msal-based-azure-cli)). Without `AZURE_CONFIG_DIR`, the Azure CLI uses `~/.azure` ([`_environment.py`, 2.90.0](https://github.com/Azure/azure-cli/blob/azure-cli-2.90.0/src/azure-cli-core/azure/cli/core/_environment.py)), and `HOME` is `/home/agent` in the sandbox ([Toolchain image contents](images.md#toolchain-image-contents)), so an agent or shell that runs `az` without setting `AZURE_CONFIG_DIR` uses the same login. Agents in one sandbox share it: every agent, shell, and program of the sandbox runs as `agent` and can read the tokens and use the account.

The login is kept across `stop`, `start`, and `remove NAME` without `--volumes`; `remove NAME --volumes` deletes the home volume and the login with it. Keeping it across `update` is not covered in this version (#52).

### Exit status

The command exits with status 0 when the Azure CLI login ended with status 0, and with status 1 otherwise. The exit status of the Azure CLI is not passed through; the message names it.

## What the host contributes

The commit name and email come only from the options you pass and the values you type, and credentials only from what you enter inside the sandbox or authorize on GitHub or Microsoft's sign-in page. No workflow imports host Git configuration, host Azure CLI configuration, or host credentials, and none adds a mount. Each `integrations` command treats the host environment the same way:

- It forwards no Git-, GitHub-, or Azure-related host variable into the sandbox. The Podman calls that look up the sandbox, query the manager, and run the workflow leave out every variable whose name starts with `GIT_`, `GH_`, `GITHUB_`, `AZURE_`, or `ARM_`, and `EMAIL`, so `GH_TOKEN`, `GITHUB_TOKEN`, `GH_CONFIG_DIR`, `AZURE_CONFIG_DIR`, `AZURE_CLIENT_SECRET`, and `ARM_CLIENT_SECRET` of the host are among them. The `podman exec` call of the workflow sets only `HOME=/home/agent` in the container, and the manager runs Git, the GitHub CLI, and the Azure CLI with the fixed environments described above.
- It reads `SANDBOXED_AGENTS_GROUP` to select the [controller group](sandboxes.md#controller-groups), as every command does.
- On Windows, it selects the Podman machine before its first lookup of the sandbox, as `start` does ([Target on Windows](sandboxes.md#target-on-windows)). The selection runs only the read-only queries `podman machine list` and `podman machine inspect`, under the environment rule of every Windows Podman call: `CONTAINER_HOST`, `CONTAINER_CONNECTION`, and `CONTAINER_SSHKEY` are removed. The Git-, GitHub-, and Azure-related filter above does not apply to these queries.

The result is the same whether or not the host has a Git configuration, a GitHub CLI login, an Azure CLI login, or Git-, GitHub-, or Azure-related variables.

## Failures

Every `integrations` command needs a running container of a known sandbox. It never starts, creates, or repairs anything to get one:

- **Unknown sandbox.** The name does not exist in the current controller group. The command fails and says so.
- **Only volumes remain.** The sandbox has volumes but no container. The command fails and names `sandboxed-agents up NAME`, which adopts the volumes.
- **Stopped sandbox.** The command fails, names `sandboxed-agents start NAME`, and starts nothing.
- **Manager does not answer.** The container runs, but its manager does not answer the session query. The command fails, says so, and names `sandboxed-agents check NAME` for diagnosis and `sandboxed-agents restart NAME` as the next step. It attempts nothing around the manager and issues no further Podman call. For `azure` login, this is reported also when the sandbox lacks the `azure` toolchain.
- **Missing toolchain.** For `azure` login, the sandbox's recorded toolchain set does not contain `azure`. The command fails, names the toolchain, prints the `update NAME --with` command that adds it ([Toolchain](#toolchain)), starts no workflow, and writes nothing in the sandbox.
- **Workflow failure.** Git, the GitHub CLI, or the Azure CLI could not be started or exited with a non-zero status. The command shows its output and the manager's message with its exit status, and reports that the integration workflow failed.
- **Workflow timeout.** For `git credentials`, and for `git identity` with both `--name` and `--email` given, the `podman exec` call that runs the workflow has 30 seconds to finish. When it does not, the executable ends that call and the command fails. When the command prompts for a missing value, and for `github` and `azure` login, this call has no added deadline, so it waits for your input.

Owner conflicts and interrupted updates are refused as for the other commands ([Owners and backup containers](sandboxes.md#owners-and-backup-containers)).

The command exits with status 0 when it did what was asked, and with status 1 when it could not or refused. The exit status of Git, the GitHub CLI, or the Azure CLI is not passed through.

### Order of checks

`integrations` commands run their checks in the order described in [Development](development.md#order-of-checks) and report only the first failure:

| Step | What the command does at this step |
| --- | --- |
| 1. Usage and names | reports an invalid controller group, then a usage error, an invalid sandbox name, or an unknown or missing integration or workflow name, before any Podman call |
| 3. Sandbox existence | reports an unknown sandbox name. When no container exists, reports an owner conflict on the remaining volumes or the backup container or, when only owned volumes and no backup container remain, that no container exists. |
| 4. Owner | reports an owner conflict on the container, its volumes, or the backup container, names the Podman objects concerned, and points to Podman, also when only a volume has a missing or different owner |
| 5. Interrupted update | reports a backup container with the current owner |
| 6. Running state | reports a stopped sandbox |
| 7. Preconditions | reports a manager that does not answer; then, for `azure` login, a missing `azure` toolchain |
| 8. Terminal | `git identity` with `--name` or `--email` missing, and `github` and `azure` login: reports a missing terminal |

The preflight (step 2) and the session guard (step 9) do not apply. An unknown workflow name is therefore reported ahead of an unknown sandbox, an owner conflict ahead of a stopped sandbox, and a stopped sandbox or a manager that does not answer ahead of a missing terminal. For `azure` login, a manager that does not answer is reported ahead of a missing toolchain, and a missing toolchain ahead of a missing terminal.

## Verification

The behavior on this page is covered by offline tests against a fake `podman` and against the manager with injected process functions ([Development](development.md#test-seams)). For `git credentials`, a manager test checks that the workflow reads no input and makes exactly the one `git config --global --replace-all -- credential.helper 'store --file=/home/agent/.git-credentials'` call with the fixed environment. A CLI test runs the command without a terminal and checks that it ends with the session query and one `podman exec --user=1000:1000 --env HOME=/home/agent` call of the manager, without `-it`. No test runs Git against an HTTPS remote or looks at a stored credential file or its permissions. For `github`, the manager tests inject process functions in place of the GitHub CLI, and the CLI tests run against fake Podman; no test signs in to GitHub, runs the GitHub CLI of the image, or looks at where it writes the token. A login against real GitHub is an item of the manual checklist (#64). For `azure`, the manager tests inject process functions in place of the Azure CLI, and the CLI tests run against fake Podman, including the order of a manager that does not answer, a missing toolchain, and a missing terminal, the printed toolchain set, and an invalid recorded set. No test runs the Azure CLI, signs in to Azure, or looks at `/home/agent/.azure`. A login against a real Azure account is an item of the manual checklist (#64), and keeping the login across `update` is tracked in #52. Nothing on this page has been confirmed against Podman on a live host, and no test observes the isolation of the sandbox or a real Git configuration in its home volume. The [live suite](live-suite.md) does not run `integrations` commands.
