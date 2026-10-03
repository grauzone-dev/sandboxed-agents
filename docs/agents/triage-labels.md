# Triage labels

The skills speak in terms of seven canonical triage roles. This file maps each role to the actual label string used in this repo's issue tracker. Issues live in `grauzone-dev/planning` (see `issue-tracker.md`), so these labels are applied there, with `gh issue edit <number> -R grauzone-dev/planning`.

| Role type | Canonical role | Label in this tracker | Meaning |
| --- | --- | --- | --- |
| Category | `bug` | `bug` | Something is broken |
| Category | `enhancement` | `enhancement` | New feature or improvement |
| State | `needs-triage` | `help wanted` | Maintainer needs to evaluate this issue |
| State | `needs-info` | `needs-info` | Waiting on reporter for more information |
| State | `ready-for-agent` | `agent` | Fully specified, ready for an AFK agent |
| State | `ready-for-human` | `question` | Requires human implementation |
| State | `wontfix` | `wontfix` | Will not be actioned |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

The Meaning column is authoritative. `help wanted` and `question` are GitHub default labels whose descriptions on GitHub ("Extra attention is needed", "Further information is requested") describe something else; in particular, `question` here means ready for a human, not waiting on the reporter.

## Triage in the planning repo

- **Type labels are not triage labels.** Every issue carries one `type: …` label (`type: epic`, `type: feature`, `type: story`, `type: task`) from its issue form. A triaged issue therefore has three: its type, one category and one state. The `enhancement` category is independent of the type; a Task can be an `enhancement` and a Feature can be a `bug`.
- **"Unlabeled" means no state label.** Because of the type label, no issue is literally unlabeled. An issue has never been triaged when it carries none of the five state labels above.
- **Only this repo's issues.** The planning repo can hold issues for several repositories. Build the triage queue from the list query in `issue-tracker.md`, which keeps issues whose body starts with `Repo: grauzone-dev/sandboxed-agents`.
- **Keep the project Status in step.** After applying `agent` or `question`, set Status to Ready in Product Backlog; after closing as `wontfix`, leave Status as it is. The Status table and the commands are in `issue-tracker.md`.

## Additional category labels

These labels already exist in the GitHub issue tracker and have no canonical role. Apply them as extra categories alongside the roles above when they fit; they never replace the `bug` or `enhancement` category or a state label.

| Label in this tracker | Meaning |
| --- | --- |
| `accessibility` | Barrier affecting people with disabilities |
| `documentation` | Improvements or additions to documentation |
| `duplicate` | This issue or pull request already exists |
| `good first issue` | Good for newcomers |
| `invalid` | This doesn't seem right |

## Labels that do not exist yet

`agent` and `needs-info` are not yet defined in `grauzone-dev/planning`. Create the label before its first use (`gh label create <name> -R grauzone-dev/planning`) rather than substituting another one.

Edit the label columns to match whatever vocabulary you actually use.
