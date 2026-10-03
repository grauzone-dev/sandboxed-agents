## Language and commits

- Discuss with the user in German or English. Write all work artifacts in English, including documents, research requests and proposals, ticket descriptions, pull request descriptions, and commit messages.
- Format every commit message according to the Conventional Commits standard. Remove every `Co-authored-by` trailer before committing, including trailers added by tools or harnesses.

## Agent skills

### Model routing

At the start of every session, before starting or delegating any task, load and apply the `model-routing` skill when available. Follow its model selection, harness fallback, and session-reuse rules.

### Issue tracker

Issues and specs live in this repo as Epics, Features, Stories and Tasks. Run `gh issue` commands here without `-R`, and reference issues from commits and PRs as `#<number>`. A specification is a Feature, an implementation ticket is a Story, and a wayfinder map is a Story with Task children; each new issue is linked to its parent as a sub-issue. A workflow mirrors each issue one way into `grauzone-dev/planning` and the GitHub Project **Product Backlog**; edit only the source issue, and set only project fields such as Status on the mirror. Before creating or reading tickets, publishing specifications, changing hierarchy, triaging, wayfinding, or completing work, use the type roles, mirror rules, state mappings, and verification status in `docs/agents/issue-tracker.md`.

### Triage labels

Triage labels are applied to issues in this repo. The seven canonical triage roles map onto its labels, with `needs-triage` as `help wanted`, `ready-for-agent` as `agent`, and `ready-for-human` as `question`; the other existing labels serve as additional categories, and the `type: …` labels mark the work item type rather than a triage role. Before applying triage roles, use `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one `CONTEXT.md` and `docs/adr/` at the repo root. Temporary files such as research notes go in the git-ignored `.scratch/` directory (research in `.scratch/research/`). Before exploring domain behavior or naming domain concepts, use `docs/agents/domain.md`.
