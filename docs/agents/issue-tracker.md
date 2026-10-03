# Issue tracker: GitHub issues, mirrored to the planning project

Issues and specs for this repo live as GitHub issues in `grauzone-dev/sandboxed-agents`. Create, read, edit, comment on, label and close them here with the `gh` CLI; `gh` infers this repo from the clone, so issue commands need no `-R`. A workflow keeps a one-way mirror of each eligible issue in the planning repo `grauzone-dev/planning`, where the mirrors are managed in the GitHub Project **Product Backlog** (`https://github.com/users/grauzone-dev/projects/1`). See [Mirror in the planning repo](#mirror-in-the-planning-repo).

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

The issue forms in `grauzone-dev/planning/.github/ISSUE_TEMPLATE/` are the source of truth for body structure; this repo has no issue forms of its own. `gh issue create` cannot fill issue forms, so reproduce the form's headings by hand.

```markdown
### <first heading for the type>

...

### Acceptance criteria

- [ ] ...

### Parent issue

#<parent number>
```

- **First heading by type**: Epic `Goal and value`; Feature `Description`; Story `User story` (As a <role> I want <goal> so that <benefit>); Task `What needs to be done`.
- **No `Repo:` line**: start the body with the first heading. The workflow adds the `Repo:` and `Source:` lines to the mirror.
- **Parent issue**: `#<parent number>` is the parent's number in this repo. Leave the section out for an Epic.

## Conventions

- **Create an issue**: `gh issue create --title "[Story] ..." --label "type: story" --body "..."`. Use a heredoc for multi-line bodies. Then link it to its parent (below). The workflow creates the mirror and adds it to the project.
- **Read an issue**: `gh issue view <number> --comments`, filtering comments by `jq` and also fetching labels.
- **List issues**: `gh issue list --state open --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'` with appropriate `--label` and `--state` filters.
- **Comment on an issue**: `gh issue comment <number> --body "..."`
- **Apply / remove labels**: `gh issue edit <number> --add-label "..."` / `--remove-label "..."`. Triage labels are applied here; see `triage-labels.md`.
- **Close**: `gh issue close <number> --comment "..."`
- **Reference from this repo**: in commits and PRs, write `#<number>`. `Closes #<number>` in a PR closes the issue on merge.

### Link to the parent (sub-issues)

Parent/child links use GitHub sub-issues and are created in this repo; the workflow copies them to the mirrors. The API takes the child's numeric **database id**, not its `#number`:

```bash
child_id=$(gh api repos/grauzone-dev/sandboxed-agents/issues/<child> --jq .id)
gh api --method POST repos/grauzone-dev/sandboxed-agents/issues/<parent>/sub_issues -F sub_issue_id="$child_id"
```

List children with `gh api repos/grauzone-dev/sandboxed-agents/issues/<parent>/sub_issues`.

## Mirror in the planning repo

`.github/workflows/mirror-issues.yml` runs `.github/scripts/mirror-issue.sh` when an issue is opened, edited, closed, reopened, labeled or unlabeled. A manual `workflow_dispatch` run resyncs the issue given in its optional `issue` input, or all issues without it.

- **Who is mirrored**: only issues whose author is `OWNER`, `MEMBER` or `COLLABORATOR`. Issues from outside contributors get no mirror, also on a manual run.
- **Mirror body**: the lines `Repo: grauzone-dev/sandboxed-agents` and `Source: grauzone-dev/sandboxed-agents#<n>`, then the source body with bare `#<n>` references rewritten to `grauzone-dev/sandboxed-agents#<n>`. The `Source:` line is how the mirror is identified; never edit or remove it.
- **Copied on every run**: title, body, labels that exist in both repos, open/closed state with close reason, and the parent link. The mirror's parent is always the mirror of the source's parent. When there is no such mirror (the source has no parent, its parent has no mirror, or its parent lives in another repo), the mirror's parent link is removed, and a later sync links it again once the parent has a mirror. Comments, assignees and blocking dependencies are not mirrored.
- **Project**: the workflow adds the mirror to Product Backlog, sets Work Item from the `type: …` label, and sets Status to Backlog when it creates the mirror.
- **Pointer back**: on creation the workflow comments on the source issue `Mirrored to grauzone-dev/planning#<m> for planning.`
- **Edit the source, not the mirror.** The next sync overwrites any change to a mirror's title, body, labels or state. On the mirror, only its project fields are set by hand: Status after creation, Priority, Estimate and Sprint.
- **Find the mirror**: read the `Mirrored to` comment on the source issue. Without one (the hand-made mirrors of #1 to #8 have none), run `gh issue list -R grauzone-dev/planning --state all --search '"Source: grauzone-dev/sandboxed-agents#<n>" in:body' --json number,body` and keep the issue whose second body line is exactly that `Source:` line. If no mirror exists, report it; the maintainer can resync it with a manual workflow run.
- **Token**: the workflow needs the repository secret `PLANNING_SYNC_TOKEN`, a token that can write issues in `grauzone-dev/planning` and write to the user project. Until that secret is set and the workflow is on `main`, nothing is mirrored automatically.

### Project: Product Backlog

The workflow adds each mirror to the project and sets Work Item and the initial Status. Agents use the ids and calls below to move Status on a mirror's project item.

Project id `PVT_kwHOFBiKrM4BlhUB` (user project number 1, owner `grauzone-dev`).

| Field | Field id | Options |
| --- | --- | --- |
| Status | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5D4` | Backlog `85525193`, Ready `d0e644ed`, In Progress `44604106`, Review `1401ae6c`, Done `6263a483` |
| Work Item | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok` | Epic `d17eec20`, Feature `c357fbb1`, Story `0a88d2b9`, Task `70992acc` |
| Priority | `PVTSSF_lAHOFBiKrM4BlhUBzhkN5Gs` | P0 - Critical `99d2d70c`, P1 - High `40602135`, P2 - Medium `5adb1540`, P3 - Low `121756c7` |
| Estimate (number) | `PVTF_lAHOFBiKrM4BlhUBzhkN5Gw` | |
| Sprint (iteration) | `PVTIF_lAHOFBiKrM4BlhUBzhkN5G0` | |

Look up the mirror's project item id:

```bash
node_id=$(gh api repos/grauzone-dev/planning/issues/<mirror number> --jq .node_id)
item_id=$(gh api graphql -f query='mutation($p:ID!,$c:ID!){addProjectV2ItemById(input:{projectId:$p,contentId:$c}){item{id}}}' \
  -f p=PVT_kwHOFBiKrM4BlhUB -f c="$node_id" --jq .data.addProjectV2ItemById.item.id)
```

Adding an issue that is already in the project returns its existing item id, so this call looks an item up.

Set a single-select field (Status, Work Item, Priority):

```bash
gh api graphql -f query='mutation($p:ID!,$i:ID!,$f:ID!,$o:String!){updateProjectV2ItemFieldValue(input:{projectId:$p,itemId:$i,fieldId:$f,value:{singleSelectOptionId:$o}}){projectV2Item{id}}}' \
  -f p=PVT_kwHOFBiKrM4BlhUB -f i="$item_id" -f f=<field id> -f o=<option id>
```

**Priority**, **Sprint** and **Estimate** are the maintainer's to set; leave them empty unless told otherwise.

If the ids above stop working, the project was reconfigured. Re-read them with `gh api graphql -f query='{user(login:"grauzone-dev"){projectV2(number:1){id fields(first:30){nodes{... on ProjectV2FieldCommon{id name} ... on ProjectV2SingleSelectField{options{id name}}}}}}}'` and update this file.

### Status

Status in the project moves Backlog → Ready → In Progress → Review → Done. It exists only on the mirror's project item and sits alongside the triage labels on the source issue, which stay the source of truth for triage state. The workflow sets Backlog; set every later Status on the mirror with the calls above.

| Event | Status |
| --- | --- |
| Mirror created by the workflow | Backlog |
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

PRs and issues share this repo's number space.

## When a skill says "publish to the issue tracker"

Create an issue in `grauzone-dev/sandboxed-agents` of the type the table above assigns and link it to its parent as a sub-issue. The workflow mirrors it into Product Backlog with Work Item and Status set. A specification is a Feature; ask which Epic it belongs under when none is given.

## When a skill says "fetch the relevant ticket"

Run `gh issue view <number> --comments`.

## Wayfinding operations

Used by the `wayfinder` skill. The **map** is a single Story with **child** Tasks as tickets. All issues are created and linked in this repo as described above.

- **Map**: a Story (`[Story] ` prefix, `type: story`) also labelled `wayfinder:map`, with the canonical map body from the `wayfinder` skill under the `User story` heading. Link it under the Feature it explores when there is one.
- **Child ticket**: a Task (`[Task] ` prefix, `type: task`) linked to the map as a sub-issue. Make `Wayfinding order: <NN>` the first line of every child body, assigning consecutive numbers in breadth-first discovery order. Labels: `wayfinder:<type>` (`research`/`prototype`/`grilling`/`task`) in addition to `type: task`. Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: GitHub's **native issue dependencies** - the canonical, UI-visible representation. Add an edge with `gh api --method POST repos/grauzone-dev/sandboxed-agents/issues/<child>/dependencies/blocked_by -F issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric **database id** (`gh api repos/grauzone-dev/sandboxed-agents/issues/<n> --jq .id`, _not_ the `#number` or `node_id`). GitHub reports `issue_dependencies_summary.blocked_by` (open blockers only - the live gate). Where dependencies aren't available, fall back to a `Blocked by: #<n>, #<n>` line directly under the `Wayfinding order` line. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's open sub-issues (`gh api repos/grauzone-dev/sandboxed-agents/issues/<map>/sub_issues`). Drop any ticket with an open blocker (`issue_dependencies_summary.blocked_by > 0`, or an open issue in the `Blocked by` line) or an assignee, then sort by `Wayfinding order` ascending. The lowest number wins.
- **Claim**: `gh issue edit <n> --add-assignee @me` - the session's first write. Then set the mirror's Status to In Progress.
- **Resolve**: `gh issue comment <n> --body "<answer>"`, then `gh issue close <n>`, set the mirror's Status to Done, then append a context pointer (artifact + link) to the map's Decisions so far.

## Verification status

- **Verified against GitHub (2026-10-03)**: the planning repo's four issue forms; the `type: …` labels in both repos; the Product Backlog project, its fields, options and ids; the `project` token scope.
- **Exercised (2026-10-03)**: issue creation and the sub-issue link in this repo, with Epic #1 and Features #2 to #8. Their mirrors `grauzone-dev/planning#5` to `#12` were created by hand before the workflow existed, with the `Source:` line added by hand; that run exercised the project-item calls (add, Work Item, Status) and the sub-issue link in the planning repo.
- **Mirror workflow not yet run on GitHub**: it is not on `main` and `PLANNING_SYNC_TOKEN` is not set. The script has been exercised by its offline test `tests/test-mirror-issue.sh` and by one local run against the real repos for #1 to #8, which covered finding and updating existing mirrors. Creating a mirror, the `Mirrored to` comment and the Actions triggers have not run against GitHub.
- **Not yet exercised**: the dependency (`blocked_by`) calls and the wayfinding operations. If one fails, report the error rather than falling back silently.
- **Chosen here, not taken from the planning repo**: the mirror's `Repo:` and `Source:` lines and the event-to-Status table.
