# Issue tracker: GitHub Projects (planning repo)

Issues and specs for this repo do **not** live in `grauzone-dev/sandboxed-agents`. They live as GitHub issues in the planning repo `grauzone-dev/planning` and are managed in the GitHub Project **Product Backlog** (`https://github.com/users/grauzone-dev/projects/1`). Use the `gh` CLI for all operations, always with `-R grauzone-dev/planning`; `gh` would otherwise infer this repo from the clone.

The installed `gh` may have no `gh project` command. Project operations below use `gh api graphql`, which works on every version and needs the `project` token scope.

## Work item types

Every issue is exactly one of four types. The hierarchy is strict: an issue's parent is always the type one level up.

| Type | Purpose | Parent | Title prefix | Label | Work Item option |
| --- | --- | --- | --- | --- | --- |
| Epic | Large goal spanning weeks to months | none | `[Epic] ` | `type: epic` | Epic |
| Feature | Complete, user-facing capability | Epic | `[Feature] ` | `type: feature` | Feature |
| Story | A single user need, deliverable within a sprint | Feature | `[Story] ` | `type: story` | Story |
| Task | Concrete technical work step, usually under a day | Story | `[Task] ` | `type: task` | Task |

How the skills use them:

| Skill concept | Type |
| --- | --- |
| Approved specification (`to-spec`) | Feature |
| Implementation ticket (`to-tickets`, what `implement` picks up) | Story, as a sub-issue of its Feature |
| Wayfinder map | Story |
| Wayfinder decision ticket | Task, as a sub-issue of the map |

## Issue body

The issue forms in `grauzone-dev/planning/.github/ISSUE_TEMPLATE/` are the source of truth for body structure. `gh issue create` cannot fill issue forms, so reproduce the form's headings by hand. Blank issues are disabled in the web UI; keep to the forms' shape.

```markdown
Repo: grauzone-dev/sandboxed-agents

### <first heading for the type>

...

### Acceptance criteria

- [ ] ...

### Parent issue

#<parent number>
```

- **First heading by type**: Epic `Goal and value`; Feature `Description`; Story `User story` (As a <role> I want <goal> so that <benefit>); Task `What needs to be done`.
- **`Repo:` line**: the planning repo can hold issues for several repositories. The exact first line `Repo: grauzone-dev/sandboxed-agents` marks an issue as belonging to this repo and is what list queries filter on.
- **Parent issue**: leave the section out for an Epic.

## Conventions

- **Create an issue**: `gh issue create -R grauzone-dev/planning --title "[Story] ..." --label "type: story" --body "..."`. Use a heredoc for multi-line bodies. Then add it to the project and link it to its parent (both below); the web forms do the project step automatically, the CLI does not.
- **Read an issue**: `gh issue view <number> -R grauzone-dev/planning --comments`, filtering comments by `jq` and also fetching labels.
- **List issues for this repo**: `gh issue list -R grauzone-dev/planning --state open --search '"Repo: grauzone-dev/sandboxed-agents" in:body' --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters. Retain only issues whose body starts with the exact `Repo:` line.
- **Comment on an issue**: `gh issue comment <number> -R grauzone-dev/planning --body "..."`
- **Apply / remove labels**: `gh issue edit <number> -R grauzone-dev/planning --add-label "..."` / `--remove-label "..."`. Triage labels are applied in the planning repo; see `triage-labels.md`.
- **Close**: `gh issue close <number> -R grauzone-dev/planning --comment "..."`
- **Reference from this repo**: in commits and PRs here, write `grauzone-dev/planning#<number>`. A bare `#<number>` points at this repo and is wrong.

### Link to the parent (sub-issues)

Parent/child links use GitHub sub-issues. The API takes the child's numeric **database id**, not its `#number`:

```bash
child_id=$(gh api repos/grauzone-dev/planning/issues/<child> --jq .id)
gh api --method POST repos/grauzone-dev/planning/issues/<parent>/sub_issues -F sub_issue_id="$child_id"
```

List children with `gh api repos/grauzone-dev/planning/issues/<parent>/sub_issues`.

### Project: Product Backlog

Project id `PVT_kwHOFBiKrM4BlhUB` (user project number 1, owner `grauzone-dev`).

| Field | Field id | Options |
| --- | --- | --- |
| Status | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5D4` | Backlog `85525193`, Ready `d0e644ed`, In Progress `44604106`, Review `1401ae6c`, Done `6263a483` |
| Work Item | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok` | Epic `d17eec20`, Feature `c357fbb1`, Story `0a88d2b9`, Task `70992acc` |
| Priority | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5Gs` | P0 - Critical `99d2d70c`, P1 - High `40602135`, P2 - Medium `5adb1540`, P3 - Low `121756c7` |
| Estimate (number) | `PVTF_lAHOFBiKrM4BlhUBzhkN5Gw` | |
| Sprint (iteration) | `PVTIF_lAHOFBiKrM4BlhUBzhkN5G0` | |

Add an issue to the project and keep the returned item id:

```bash
node_id=$(gh api repos/grauzone-dev/planning/issues/<number> --jq .node_id)
item_id=$(gh api graphql -f query='mutation($p:ID!,$c:ID!){addProjectV2ItemById(input:{projectId:$p,contentId:$c}){item{id}}}' \
  -f p=PVT_kwHOFBiKrM4BlhUB -f c="$node_id" --jq .data.addProjectV2ItemById.item.id)
```

Adding an issue that is already in the project returns its existing item id, so the same call looks an item up.

Set a single-select field (Status, Work Item, Priority):

```bash
gh api graphql -f query='mutation($p:ID!,$i:ID!,$f:ID!,$o:String!){updateProjectV2ItemFieldValue(input:{projectId:$p,itemId:$i,fieldId:$f,value:{singleSelectOptionId:$o}}){projectV2Item{id}}}' \
  -f p=PVT_kwHOFBiKrM4BlhUB -f i="$item_id" -f f=<field id> -f o=<option id>
```

On creation, always set **Work Item** to the issue's type and **Status** to Backlog. **Priority**, **Sprint** and **Estimate** are the maintainer's to set; leave them empty unless told otherwise.

If the ids above stop working, the project was reconfigured. Re-read them with `gh api graphql -f query='{user(login:"grauzone-dev"){projectV2(number:1){id fields(first:30){nodes{... on ProjectV2FieldCommon{id name} ... on ProjectV2SingleSelectField{options{id name}}}}}}}'` and update this file.

### Status

Status in the project moves Backlog → Ready → In Progress → Review → Done. It sits alongside the triage labels, which stay the source of truth for triage state.

| Event | Status |
| --- | --- |
| Issue created | Backlog |
| Triaged as ready (`ready-for-agent` or `ready-for-human` role applied) | Ready |
| Claimed (assignee set, work started) | In Progress |
| Pull request open in `grauzone-dev/sandboxed-agents` | Review |
| Issue closed as completed | Done |

## Pull requests as a triage surface

**PRs as a request surface: no.** _(Set to `yes` if this repo treats external PRs as feature requests; the `triage` skill reads this flag.)_

When set to `yes`, PRs in `grauzone-dev/sandboxed-agents` run through the same labels and states as issues, using the `gh pr` equivalents:

- **Read a PR**: `gh pr view <number> --comments` and `gh pr diff <number>` for the diff.
- **List external PRs for triage**: `gh pr list --state open --json number,title,body,labels,author,authorAssociation,comments` then keep only `authorAssociation` of `CONTRIBUTOR`, `FIRST_TIME_CONTRIBUTOR`, or `NONE` (drop `OWNER`/`MEMBER`/`COLLABORATOR`).
- **Comment / label / close**: `gh pr comment`, `gh pr edit --add-label`/`--remove-label`, `gh pr close`.

PRs live in this repo and issues in the planning repo, so the two number spaces are separate.

## When a skill says "publish to the issue tracker"

Create an issue in `grauzone-dev/planning` of the type the table above assigns, add it to Product Backlog with Work Item and Status set, and link it to its parent as a sub-issue. A specification is a Feature; ask which Epic it belongs under when none is given.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> -R grauzone-dev/planning --comments`.

## Wayfinding operations

Used by the `wayfinder` skill. The **map** is a single Story with **child** Tasks as tickets. All issues are created, added to the project and linked as described above.

- **Map**: a Story (`[Story] ` prefix, `type: story`) also labelled `wayfinder:map`, with the canonical map body from the `wayfinder` skill under the `User story` heading. Link it under the Feature it explores when there is one.
- **Child ticket**: a Task (`[Task] ` prefix, `type: task`) linked to the map as a sub-issue. Put `Wayfinding order: <NN>` directly under the `Repo:` line of every child body, assigning consecutive numbers in breadth-first discovery order. Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`) in addition to `type: task`. Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: GitHub's **native issue dependencies** - the canonical, UI-visible representation. Add an edge with `gh api --method POST repos/grauzone-dev/planning/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric **database id** (`gh api repos/grauzone-dev/planning/issues/<n> --jq .id`, _not_ the `#number` or `node_id`). GitHub reports `issue_dependencies_summary.blocked_by` (open blockers only - the live gate). Where dependencies aren't available, fall back to a `Blocked by: #<n>, #<n>` line directly under the `Wayfinding order` line. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's open sub-issues (`gh api repos/grauzone-dev/planning/issues/<map>/sub_issues`). Drop any ticket with an open blocker (`issue_dependencies_summary.blocked_by > 0`, or an open issue in the `Blocked by` line) or an assignee, then sort by `Wayfinding order` ascending. The lowest number wins.
- **Claim**: `gh issue edit <n> -R grauzone-dev/planning --add-assignee @me` - the session's first write. Then set Status to In Progress.
- **Resolve**: `gh issue comment <n> -R grauzone-dev/planning --body "<answer>"`, then `gh issue close <n> -R grauzone-dev/planning`, set Status to Done, then append a context pointer (artifact + link) to the map's Decisions so far.

## Verification status

- **Verified against GitHub (2026-10-03)**: the planning repo's four issue forms and `type: …` labels; the Product Backlog project, its fields, options and ids; the `project` token scope.
- **Not yet exercised**: the planning repo has no issues, so the sub-issue, dependency and project-item calls above have not been run against it. If one fails, report the error rather than falling back silently.
- **Chosen here, not taken from the planning repo**: the `Repo:` body line and the event-to-Status table.
