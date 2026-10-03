# Sandboxed agents

`sandboxed-agents` runs coding agents in rootless Podman containers so that an agent's mistake stays inside one sandbox: its workspace, home data, and the credentials stored there. This glossary defines the language used to describe the product.

## Language

### Product

**Sandbox**:
One isolated rootless container with its own workspace, home data, SSH server state, agent selection, and toolchain set. It is the unit users create, update, stop, and remove.
_Avoid_: Container (as a product term), environment, instance

**Workspace**:
The project files available to agents in a sandbox, mounted at `/workspace`. It is a sandbox-owned volume by default; the user may instead bind exactly one host directory.
_Avoid_: Project directory, mount

**Agent**:
A coding CLI from the agent catalog that is installed into a sandbox's home data and enabled there. Agents in one sandbox share its user, files, and credentials.
_Avoid_: Assistant, bot, model

**Agent session**:
A persistent terminal session running one agent in a sandbox, which the user can detach from and reattach to. It ends when its agent exits, so a running agent session always means a running agent.
_Avoid_: Agent run (a run is a one-shot command that ends with its terminal)

**Integration**:
A connection between a sandbox and an external account or service, such as Git, GitHub, Azure, or Azure DevOps. An integration offers login and config workflows and has no installable program of its own.
_Avoid_: Tool, built-in workflow target, plugin

**Workflow**:
A named login or config operation that an agent or integration exposes. A login workflow authenticates or links an account; a config workflow changes a setting that is not authentication.

**Toolchain**:
An optional set of SDKs or system packages built into a sandbox's image, such as .NET, Playwright with browsers, Azure CLI, or native build tools. Toolchains are chosen per sandbox when it is created.
_Avoid_: Tool, image option, capability

**Tool**:
Reserved for installable programs and managed services that run beside agents. The first version ships none.

**Image**:
The container filesystem a sandbox is created from: the base contents every sandbox needs plus the sandbox's toolchains. Sandboxes with the same toolchain set share one image.

### Ownership

**Controller group**:
The namespace an installed executable manages. The default group is `default`; an environment variable selects another. It does not depend on where the executable is installed. Two controller groups can each hold a sandbox of the same name.
_Avoid_: Controller, owner path, installation

**Owner**:
The controller group recorded on a sandbox and its volumes. The executable changes only objects whose owner is its own controller group. An object under one of its sandbox names with a missing or different owner is an owner conflict: the executable reports it and leaves the repair to the user.

**Backup container**:
The previous container of a sandbox, kept under its own name while `update` replaces it and removed once the new container is ready. It is not a sandbox and never appears in `list`.

### Host boundaries

**Host state**:
Mutable data the executable keeps in the operating system's state directory, separated by controller group. It holds managed SSH keys and configuration and temporary image-build inputs.

**Protected host paths**:
Host locations that must never be exposed through a workspace bind: the executable and its package launchers, host state, temporary build inputs, and the user's SSH directory. Symlinks, junctions, and other aliases do not make a protected path bindable.

**SSH setup**:
The opt-in host configuration that lets editors and desktop UIs reach a sandbox over SSH, using a key dedicated to that sandbox and a pinned host key. Without it, the executable leaves host SSH files untouched.

**Preflight**:
The read-only check of host prerequisites that runs before a sandbox or image is built. It reports each missing prerequisite and never changes the host.
