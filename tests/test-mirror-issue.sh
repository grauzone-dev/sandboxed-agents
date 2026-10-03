#!/usr/bin/env bash
set -euo pipefail

root=$(cd -- "${BASH_SOURCE[0]%/*}/.." && pwd)
test_dir=$(mktemp -d)
trap 'test_status=$?; if [[ $test_status != 0 && -f "$test_dir/error" ]]; then cat "$test_dir/error" >&2; fi; rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin" "$test_dir/fixtures"
export FIXTURES="$test_dir/fixtures" CALLS="$test_dir/calls.jsonl"
export PATH="$test_dir/bin:$PATH" GH_TOKEN=offline-token
export SOURCE_REPO=grauzone-dev/sandboxed-agents MIRROR_REPO=grauzone-dev/planning
export PROJECT_ID=PVT_kwHOFBiKrM4BlhUB DRY_RUN=0

cat > "$test_dir/bin/gh" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
arguments=$(jq -n --args '$ARGS.positional' -- "$@")
method=GET
endpoint=
query=
search=
include=0
payload=null
shift
while [[ $# -gt 0 ]]; do
  case $1 in
    --method|-X) method=$2; shift 2 ;;
    --input) payload=$(cat); shift 2 ;;
    --include|-i) include=1; shift ;;
    --paginate) shift ;;
    -f|-F)
      [[ $2 != query=* ]] || query=${2#query=}
      [[ $2 != q=* ]] || search=${2#q=}
      shift 2 ;;
    *) endpoint=$1; shift ;;
  esac
done
jq -nc --argjson args "$arguments" --arg method "$method" --arg endpoint "$endpoint" --argjson payload "$payload" \
  '{args:$args,method:$method,endpoint:$endpoint,payload:$payload}' >> "$CALLS"
case "$endpoint" in
  repos/grauzone-dev/sandboxed-agents/issues/10) cat "$FIXTURES/source.json" ;;
  search/issues)
    if [[ -f "$FIXTURES/search-error" ]]; then printf 'Search failed\n' >&2; exit 1; fi
    if [[ $search == *sandboxed-agents#5* ]]; then cat "$FIXTURES/search-parent.json"; else cat "$FIXTURES/search.json"; fi ;;
  repos/grauzone-dev/sandboxed-agents/issues/*/comments\?*)
    if [[ -f "$FIXTURES/comments-error" ]]; then printf 'Comments failed\n' >&2; exit 1; fi
    if [[ $endpoint == */10/* ]]; then cat "$FIXTURES/comments.json"; else cat "$FIXTURES/comments-parent.json"; fi ;;
  repos/grauzone-dev/planning/labels\?*) cat "$FIXTURES/labels.json" ;;
  repos/*/issues/*/parent)
    if [[ $endpoint == repos/grauzone-dev/sandboxed-agents/* ]]; then file=source-parent.json; else file=mirror-parent.json; fi
    if [[ $(jq -r '.status // ""' "$FIXTURES/$file") == 403 ]]; then
      printf 'HTTP/2.0 403 Forbidden\r\n\r\n{"message":"Forbidden"}\n'
      exit 1
    elif [[ $(cat "$FIXTURES/$file") == null ]]; then
      printf 'HTTP/2.0 404 Not Found\r\n\r\n{"message":"Not Found"}\n'
      exit 1
    else
      [[ $include == 0 ]] || printf 'HTTP/2.0 200 OK\r\n\r\n'
      cat "$FIXTURES/$file"
    fi ;;
  repos/grauzone-dev/planning/issues)
    [[ $method == POST ]] || exit 2
    jq '. + {number:42,id:420,node_id:"I_mirror",state:"open",state_reason:null} | .labels = ((.labels // []) | map({name:.}))' <<< "$payload" ;;
  repos/grauzone-dev/sandboxed-agents/issues/10/comments)
    jq -s --argjson payload "$payload" 'add + [$payload]' "$FIXTURES/comments.json" > "$FIXTURES/comments-next.json"
    mv "$FIXTURES/comments-next.json" "$FIXTURES/comments.json"
    printf '{}\n' ;;
  repos/grauzone-dev/planning/issues/*/sub_issues|repos/grauzone-dev/planning/issues/*/sub_issue) printf '{}\n' ;;
  repos/grauzone-dev/planning/issues/*)
    if [[ -f "$FIXTURES/fetch-error" ]]; then printf 'Candidate fetch failed\n' >&2; exit 1; fi
    if [[ $method == PATCH ]]; then
      printf '{}\n'
    elif [[ ! -f "$FIXTURES/mirror-${endpoint##*/}.json" ]]; then
      printf 'HTTP/2.0 404 Not Found\r\n\r\n{"message":"Not Found"}\n'
      exit 1
    else
      if [[ $include != 0 ]]; then
        if [[ -f "$FIXTURES/header-lf" ]]; then printf 'HTTP/2.0 200 OK\nContent-Type: application/json\n\n'; else printf 'HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n\r\n'; fi
      fi
      cat "$FIXTURES/mirror-${endpoint##*/}.json"
    fi ;;
  graphql)
    if [[ -f "$FIXTURES/project-error" ]]; then printf 'Project failed\n' >&2; exit 1; fi
    if [[ $query == *addProjectV2ItemById* ]]; then cat "$FIXTURES/project.json"; else printf '{"data":{"updateProjectV2ItemFieldValue":{"projectV2Item":{"id":"PVTI_item"}}}}\n'; fi ;;
  *) printf 'Unexpected fake gh call: %s\n' "$endpoint" >&2; exit 2 ;;
esac
FAKE
chmod +x "$test_dir/bin/gh"

title='literal $(touch pwned) `touch pwned` "quotes" and '\''apostrophes'\'''
body=$(cat <<'BODY'
Repo: old/repository

### Title
#1 and (#22), word#3, /#4, other/repo#5, grauzone-dev/sandboxed-agents#6
Unicode é#7, _#8; #9.
```bash
echo #11
```
~~~~ lang
#12
~~~
#13
~~~~
After #14
````
#15
```
#16
````
BODY
)
body+=$'\n\n'
expected_body=$(cat <<'BODY'
Repo: grauzone-dev/sandboxed-agents
Source: grauzone-dev/sandboxed-agents#10

### Title
grauzone-dev/sandboxed-agents#1 and (grauzone-dev/sandboxed-agents#22), word#3, /#4, other/repo#5, grauzone-dev/sandboxed-agents#6
Unicode é#7, _#8; grauzone-dev/sandboxed-agents#9.
```bash
echo #11
```
~~~~ lang
#12
~~~
#13
~~~~
After grauzone-dev/sandboxed-agents#14
````
#15
```
#16
````
BODY
)
expected_body+=$'\n\n'
jq -n --arg title "$title" --arg body "$body" '{number:10,id:100,node_id:"I_source",author_association:"OWNER",title:$title,body:$body,labels:[{name:"bug"},{name:"type: story"},{name:"source only"}],state:"open",state_reason:null}' > "$test_dir/source-base.json"
jq -n --arg title "$title" --arg body "$expected_body" '{number:42,id:420,node_id:"I_mirror",title:$title,body:$body,labels:[{name:"type: story"},{name:"bug"}],state:"open",state_reason:null}' > "$test_dir/mirror-base.json"

reset() {
  rm -f "$FIXTURES/search-error" "$FIXTURES/fetch-error" "$FIXTURES/comments-error" "$FIXTURES/header-lf" "$FIXTURES/project-error"
  cp "$test_dir/source-base.json" "$FIXTURES/source.json"
  cp "$test_dir/mirror-base.json" "$FIXTURES/mirror-42.json"
  printf '{"total_count":1,"items":[{"number":7}]}\n{"total_count":1,"items":[]}\n' > "$FIXTURES/search.json"
  printf '{"total_count":1,"items":[{"number":50}]}\n' > "$FIXTURES/search-parent.json"
  printf '[]\n[]\n' > "$FIXTURES/comments.json"
  printf '[]\n' > "$FIXTURES/comments-parent.json"
  printf '%s\n' '{"number":7,"id":70,"body":"Repo: test\nSource: grauzone-dev/sandboxed-agents#100\nSource: grauzone-dev/sandboxed-agents#10"}' > "$FIXTURES/mirror-7.json"
  printf '%s\n' '{"number":50,"id":500,"body":"Repo: grauzone-dev/sandboxed-agents\nSource: grauzone-dev/sandboxed-agents#5\n"}' > "$FIXTURES/mirror-50.json"
  printf '[{"name":"bug"}]\n[{"name":"type: story"}]\n' > "$FIXTURES/labels.json"
  printf '{"number":5,"id":50,"repository_url":"https://api.github.com/repos/grauzone-dev/sandboxed-agents"}\n' > "$FIXTURES/source-parent.json"
  printf 'null\n' > "$FIXTURES/mirror-parent.json"
  printf '{"data":{"addProjectV2ItemById":{"item":{"id":"PVTI_item","fieldValues":{"nodes":[]}}}}}\n' > "$FIXTURES/project.json"
  : > "$CALLS"
}

run() {
  (cd "$test_dir" && bash "$root/.github/scripts/mirror-issue.sh" 10) > "$test_dir/output" 2> "$test_dir/error"
}
assert_calls() {
  if ! jq -se "$1" "$CALLS" >/dev/null; then
    printf 'FAIL: %s\n' "$2" >&2
    cat "$CALLS" >&2
    exit 1
  fi
}
assert_summary() {
  [[ $(tail -n 1 "$test_dir/output") == "source #10 -> mirror #42 ($1)" ]] || { cat "$test_dir/output" >&2; exit 1; }
}
existing() {
  printf '{"total_count":2,"items":[{"number":7}]}\n{"total_count":2,"items":[{"number":42}]}\n' > "$FIXTURES/search.json"
}
project_matches() {
  printf '{"data":{"addProjectV2ItemById":{"item":{"id":"PVTI_item","fieldValues":{"nodes":[{"field":{"id":"PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok"},"optionId":"0a88d2b9"}]}}}}}\n' > "$FIXTURES/project.json"
}

reset
run
assert_summary created
jq -se --arg title "$title" --arg body "$expected_body" 'any(.[]; .endpoint == "repos/grauzone-dev/planning/issues" and .payload.title == $title and .payload.body == $body and (.payload | has("labels") | not))' "$CALLS" >/dev/null
assert_calls 'any(.[]; .method == "PATCH" and .payload.labels == ["bug","type: story"])' 'creation labels'
[[ ! -e "$test_dir/pwned" ]]
[[ $(cat "$test_dir/error") == 'Warning: label source only does not exist in grauzone-dev/planning' ]]
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/50/sub_issues" and .payload.sub_issue_id == 420 and .payload.replace_parent == false)' 'create parent linking'
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10/comments" and .payload.body == "Mirrored to grauzone-dev/planning#42 for planning.")' 'creation comment'
assert_calls '([to_entries[] | select(.value.endpoint == "repos/grauzone-dev/planning/issues") | .key][0]) as $create | ([to_entries[] | select(.value.endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10/comments") | .key][0]) as $comment | $comment == $create + 1 and all(to_entries[]; if .value.endpoint == "graphql" or .value.method == "PATCH" or (.value.endpoint | contains("/labels?")) then .key > $comment else true end)' 'creation comment precedes labels, state, and project'
assert_calls 'any(.[]; (.args | index("f=PVTSSF_lAHOFBiKrM4BlhUBzhkN5D4")) and (.args | index("o=85525193")))' 'creation Backlog'
assert_calls 'any(.[]; (.args | index("f=PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok")) and (.args | index("o=0a88d2b9")))' 'creation Work Item'
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/7") and any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/50")' 'exact source line and parent mirror search'

reset
existing
jq '.title="old" | .body="Repo: old\nSource: grauzone-dev/sandboxed-agents#10\nold" | .labels=[{name:"extra"}] | .state="closed" | .state_reason="not_planned"' "$test_dir/mirror-base.json" > "$FIXTURES/mirror-42.json"
printf '{"number":49,"id":490}\n' > "$FIXTURES/mirror-parent.json"
run
assert_summary updated
jq -se --arg title "$title" --arg body "$expected_body" 'any(.[]; .method == "PATCH" and .payload.title == $title and .payload.body == $body and .payload.labels == ["bug","type: story"] and .payload.state == "open" and .payload.state_reason == "reopened")' "$CALLS" >/dev/null
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/50/sub_issues" and .payload.replace_parent == true)' 'replace parent'
assert_calls 'all(.[]; ((.args | index("f=PVTSSF_lAHOFBiKrM4BlhUBzhkN5D4")) | not) and (.method != "POST" or (.endpoint | endswith("/comments") | not)))' 'updates preserve Status and comments'

reset
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search.json"
printf '%s\n' '[{"body":"ordinary comment"}]' '[{"body":"Mirrored to grauzone-dev/planning#42 for planning."},{"body":"Mirrored to grauzone-dev/planning#42 for planning."}]' > "$FIXTURES/comments.json"
jq '.title="old"' "$test_dir/mirror-base.json" > "$FIXTURES/mirror-42.json"
: > "$FIXTURES/header-lf"
run
assert_summary updated
assert_calls 'all(.[]; .endpoint != "repos/grauzone-dev/planning/issues" and (.method != "POST" or .endpoint != "repos/grauzone-dev/sandboxed-agents/issues/10/comments")) and any(.[]; .method == "PATCH" and .endpoint == "repos/grauzone-dev/planning/issues/42")' 'comment pointer survives search indexing lag'
assert_calls '([.[] | select(.endpoint == "repos/grauzone-dev/planning/issues/42" and .method == "GET")] | length == 1) and any(.[]; .endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10/comments?per_page=100" and (.args | index("--paginate")))' 'comment pagination and duplicate pointers'

reset
existing
printf '%s\n' '[{"body":"Mirrored to grauzone-dev/planning#42 for planning."}]' > "$FIXTURES/comments.json"
run
assert_calls '([.[] | select(.endpoint == "repos/grauzone-dev/planning/issues/42" and .method == "GET")] | length == 1)' 'search and comment union deduplicates'

reset
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search.json"
printf '%s\n' '[{"body":"Mirrored to grauzone-dev/planning#7 for planning."}]' > "$FIXTURES/comments.json"
run
assert_summary created
assert_calls 'all(.[]; .method != "PATCH" or .endpoint != "repos/grauzone-dev/planning/issues/7")' 'forged pointer without exact marker ignored'

reset
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search.json"
printf '%s\n' '[{"body":"Mirrored to grauzone-dev/planning#42 for planning.\n"},{"body":"Mirrored to other/repo#42 for planning."},{"body":"Prefix Mirrored to grauzone-dev/planning#42 for planning."},{"body":"Mirrored to grauzone-dev/planning#42\n for planning."}]' > "$FIXTURES/comments.json"
run
assert_summary created
assert_calls 'all(.[]; .endpoint != "repos/grauzone-dev/planning/issues/42" or .method != "GET")' 'pointer comment requires exact complete body'

reset
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search.json"
printf '%s\n' '[{"body":"Mirrored to grauzone-dev/planning#99 for planning."}]' > "$FIXTURES/comments.json"
run
assert_summary created
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/99") and any(.[]; .endpoint == "repos/grauzone-dev/planning/issues" and .method == "POST")' 'missing pointer target ignored'

reset
: > "$FIXTURES/project-error"
if run; then printf 'FAIL: project failure ignored\n' >&2; exit 1; fi
assert_calls 'any(.[]; .endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10/comments" and .method == "POST")' 'creation pointer survives project failure'
rm "$FIXTURES/project-error"
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search.json"
: > "$CALLS"
run
assert_calls 'all(.[]; .endpoint != "repos/grauzone-dev/planning/issues" and (.method != "POST" or .endpoint != "repos/grauzone-dev/sandboxed-agents/issues/10/comments"))' 'retry after project failure reuses pointer'

reset
existing
project_matches
printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
run
assert_summary unchanged
assert_calls 'all(.[]; .method == "GET") and ([.[] | select(.endpoint == "graphql")] | length == 1)' 'unchanged REST, parent, and project fields'

for reason in completed not_planned; do
  reset
  existing
  jq --arg reason "$reason" '.state="closed" | .state_reason=$reason' "$test_dir/source-base.json" > "$FIXTURES/source.json"
  run
  jq -se --arg reason "$reason" 'any(.[]; .method == "PATCH" and .payload.state == "closed" and .payload.state_reason == $reason)' "$CALLS" >/dev/null
done
for reason in '"not_planned"' null; do
  reset
  existing
  jq --argjson reason "$reason" '.state="closed" | .state_reason=$reason' "$test_dir/source-base.json" > "$FIXTURES/source.json"
  jq '.state="closed" | .state_reason="completed"' "$test_dir/mirror-base.json" > "$FIXTURES/mirror-42.json"
  run
  assert_summary updated
  jq -se --argjson reason "$reason" '[.[] | select(.method == "PATCH") | .payload] == [{state:"open",state_reason:"reopened"},{state:"closed",state_reason:$reason}]' "$CALLS" >/dev/null
done
reset
existing
project_matches
printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
jq '.state="closed"' "$test_dir/source-base.json" > "$FIXTURES/source.json"
jq '.state="closed"' "$test_dir/mirror-base.json" > "$FIXTURES/mirror-42.json"
run
assert_summary unchanged
assert_calls 'all(.[]; .method != "PATCH")' 'matching nullable close reason'
reset
jq '.state="closed" | .state_reason="not_planned"' "$test_dir/source-base.json" > "$FIXTURES/source.json"
run
assert_summary created
assert_calls 'any(.[]; .method == "PATCH" and .payload.state == "closed" and .payload.state_reason == "not_planned")' 'created mirror closed state'

reset
existing
jq '.labels=[]' "$test_dir/source-base.json" > "$FIXTURES/source.json"
run
assert_calls 'any(.[]; .method == "PATCH" and .payload.labels == []) and all(.[]; (.args | index("f=PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok")) | not)' 'empty label restriction and absent type'

reset
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search-parent.json"
run
[[ $(cat "$test_dir/error") == *'source parent #5 has no mirror'* ]]
assert_calls 'all(.[]; .endpoint | endswith("/sub_issues") | not)' 'parent without mirror'
reset
printf '{"number":5,"repository_url":"https://api.github.com/repos/other/repo"}\n' > "$FIXTURES/source-parent.json"
run
assert_calls 'all(.[]; .endpoint | endswith("/sub_issues") | not)' 'external parent'
reset
printf 'null\n' > "$FIXTURES/source-parent.json"
run
assert_calls 'all(.[]; (.endpoint | endswith("/sub_issues") | not) and (.endpoint != "repos/grauzone-dev/planning/issues/50"))' '404 source parent'

reset
existing
project_matches
printf 'null\n' > "$FIXTURES/source-parent.json"
printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
run
assert_summary updated
assert_calls 'any(.[]; .method == "DELETE" and .endpoint == "repos/grauzone-dev/planning/issues/50/sub_issue" and .payload == {sub_issue_id:420}) and ([.[] | select(.method == "DELETE")] | length == 1)' 'removed source parent detaches mirror using database id'
: > "$CALLS"
DRY_RUN=1 run
assert_summary updated
assert_calls 'all(.[]; .method == "GET" and .endpoint != "graphql")' 'parent removal dry run makes no writes'
[[ $(cat "$test_dir/output") == *'DRY RUN: DELETE repos/grauzone-dev/planning/issues/50/sub_issue'*'"sub_issue_id": 420'* ]]

reset
existing
project_matches
printf 'null\n' > "$FIXTURES/source-parent.json"
run
assert_summary unchanged
assert_calls 'all(.[]; .method == "GET") and any(.[]; .endpoint == "repos/grauzone-dev/planning/issues/42/parent")' 'both parents absent is unchanged'

reset
existing
project_matches
printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
printf '{"number":5,"repository_url":"https://api.github.com/repos/other/repo"}\n' > "$FIXTURES/source-parent.json"
run
assert_summary unchanged
assert_calls 'all(.[]; .method != "DELETE")' 'external source parent preserves existing mirror parent'

reset
existing
project_matches
printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
printf '{"total_count":0,"items":[]}\n' > "$FIXTURES/search-parent.json"
run
assert_summary unchanged
[[ $(cat "$test_dir/error") == *'source parent #5 has no mirror'* ]]
assert_calls 'all(.[]; .method != "DELETE")' 'parent lacking mirror preserves existing mirror parent'

reset
DRY_RUN=1 run
assert_calls 'all(.[]; .method == "GET" and .endpoint != "graphql")' 'dry run creates no writes'
[[ $(cat "$test_dir/output") == *'set Status to Backlog'* ]]
[[ $(cat "$test_dir/output") == *'DRY RUN: POST repos/grauzone-dev/planning/issues'*'DRY RUN: POST repos/grauzone-dev/sandboxed-agents/issues/10/comments'*'DRY RUN: PATCH repos/grauzone-dev/planning/issues/new'*'DRY RUN: add/look up'* ]]
reset
existing
DRY_RUN=1 run
assert_calls 'all(.[]; .method == "GET" and .endpoint != "graphql")' 'dry run update creates no writes'
[[ $(cat "$test_dir/output") == *"$expected_body"* ]]

reset
existing
cp "$test_dir/mirror-base.json" "$FIXTURES/mirror-43.json"
printf '{"total_count":2,"items":[{"number":42},{"number":43}]}\n' > "$FIXTURES/search.json"
if run; then printf 'FAIL: duplicate mirror accepted\n' >&2; exit 1; fi
[[ $(cat "$test_dir/error") == *'Multiple mirrors found'* ]]
assert_calls 'all(.[]; .method == "GET" and .endpoint != "graphql")' 'duplicates fail before writes'
reset
printf '{"total_count":1001,"items":[]}\n' > "$FIXTURES/search.json"
if run; then printf 'FAIL: capped search accepted\n' >&2; exit 1; fi
for failure in search-error fetch-error comments-error; do
  reset
  : > "$FIXTURES/$failure"
  if run; then printf 'FAIL: %s ignored\n' "$failure" >&2; exit 1; fi
  assert_calls 'all(.[]; .method == "GET" and .endpoint != "graphql")' 'failed lookup prevents writes'
done
reset
printf '{"status":403}\n' > "$FIXTURES/source-parent.json"
if DRY_RUN=1 run; then printf 'FAIL: parent permission failure ignored\n' >&2; exit 1; fi
reset
jq '.pull_request={url:"https://example.test/pr/10"}' "$test_dir/source-base.json" > "$FIXTURES/source.json"
run
[[ $(cat "$test_dir/output") == 'source #10 -> skipped (pull request)' ]]
assert_calls 'length == 1' 'pull requests skipped'

for association in OWNER MEMBER COLLABORATOR; do
  reset
  existing
  project_matches
  printf '{"number":50,"id":500}\n' > "$FIXTURES/mirror-parent.json"
  jq --arg association "$association" '.author_association=$association' "$test_dir/source-base.json" > "$FIXTURES/source.json"
  run
  assert_summary unchanged
done
for association in NONE CONTRIBUTOR FIRST_TIMER FIRST_TIME_CONTRIBUTOR MANNEQUIN owner unknown ''; do
  reset
  jq --arg association "$association" '.author_association=$association' "$test_dir/source-base.json" > "$FIXTURES/source.json"
  run
  [[ $(cat "$test_dir/output") == 'source #10 -> skipped (author not eligible)' ]]
  assert_calls 'length == 1 and .[0].method == "GET" and .[0].endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10"' 'ineligible author skips before lookup'
done
reset
jq 'del(.author_association)' "$test_dir/source-base.json" > "$FIXTURES/source.json"
run
[[ $(cat "$test_dir/output") == 'source #10 -> skipped (author not eligible)' ]]
assert_calls 'length == 1 and .[0].method == "GET" and .[0].endpoint == "repos/grauzone-dev/sandboxed-agents/issues/10"' 'missing author association skips before lookup'
reset
jq '.author_association=null' "$test_dir/source-base.json" > "$FIXTURES/source.json"
run
[[ $(cat "$test_dir/output") == 'source #10 -> skipped (author not eligible)' ]]
assert_calls 'length == 1' 'null author association skips before lookup'

workflow=$(< "$root/.github/workflows/mirror-issues.yml")
[[ $workflow == *$'concurrency:\n  group: mirror-issues\n  cancel-in-progress: false\n  queue: max\n'* ]]
[[ $workflow == *'if: github.event_name == '\''workflow_dispatch'\'' || contains(fromJSON('\''["OWNER", "MEMBER", "COLLABORATOR"]'\''), github.event.issue.author_association)'* ]]

printf 'All mirror issue tests passed.\n'
