# Domain docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root, or
- **`CONTEXT-MAP.md`** at the repo root if it exists - it points at one `CONTEXT.md` per context. Read each one relevant to the topic.
- **`docs/adr/`** - read ADRs that touch the area you're about to work in. In multi-context repos, also check `src/<context>/docs/adr/` for context-scoped decisions.

If any of these files don't exist, **proceed silently**. Don't flag their absence; don't suggest creating them upfront. The `domain-modeling` skill creates them lazily when terms or decisions actually get resolved.

## File structure

Single-context repo (most repos):

```text
/
├── .scratch/                          ← temporary files, git-ignored
├── CONTEXT.md
├── docs/adr/
│   ├── 0001-event-sourced-orders.md
│   └── 0002-postgres-for-write-model.md
└── src/
```

Multi-context repo (presence of `CONTEXT-MAP.md` at the root):

```text
/
├── CONTEXT-MAP.md
├── docs/adr/                          ← system-wide decisions
└── src/
    ├── ordering/
    │   ├── CONTEXT.md
    │   └── docs/adr/                  ← context-specific decisions
    └── billing/
        ├── CONTEXT.md
        └── docs/adr/
```

## Temporary files go in `.scratch/`

`.scratch/` at the repo root holds working files that should not be committed: research notes, exploratory analysis, throwaway drafts and similar intermediate output. It is listed in `.gitignore`, so nothing in it is picked up by `git add`.

- **Research notes**: this repo's research-note convention is `.scratch/research/<topic-slug>.md`. The `research` skill writes there instead of `docs/research/`.
- **Other temporary files**: use a subdirectory named for the task, e.g. `.scratch/<topic-slug>/`.
- **Not durable**: files here exist only on the machine that wrote them. Don't cite a `.scratch/` path from an issue, PR, ADR or `CONTEXT.md`. When a finding needs to outlive the session, move it into `CONTEXT.md`, an ADR, or the relevant issue.
- **Never force-add**: don't use `git add -f` on `.scratch/` or remove it from `.gitignore`. To keep a file, move it to a tracked location.

## Use the glossary's vocabulary

When your output names a domain concept (in an issue title, a refactor proposal, a hypothesis, a test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If the concept you need isn't in the glossary yet, that's a signal - either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for the `domain-modeling` skill).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders) - but worth reopening because…_
