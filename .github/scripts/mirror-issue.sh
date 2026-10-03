#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 1 || ! $1 =~ ^[1-9][0-9]*$ ]]; then
  printf 'Usage: mirror-issue.sh <source-issue-number>\n' >&2
  exit 1
fi
: "${GH_TOKEN:?GH_TOKEN is required}"
SOURCE_REPO=${SOURCE_REPO:-grauzone-dev/sandboxed-agents}
MIRROR_REPO=${MIRROR_REPO:-grauzone-dev/planning}
PROJECT_ID=${PROJECT_ID:-PVT_kwHOFBiKrM4BlhUB}
DRY_RUN=${DRY_RUN:-0}
number=$1
changed=0
created=0

optional_get() {
  local response status
  if response=$(gh api --include "$1" 2>&1); then
    response=${response//$'\r'/}
    printf '%s\n' "${response#*$'\n\n'}" | jq -e '.'
  else
    status=${response%%$'\n'*}
    if [[ $status =~ ^HTTP/[^[:space:]]+[[:space:]]+404([[:space:]]|$) ]]; then
      printf 'null\n'
    else
      printf '%s\n' "$response" >&2
      return 1
    fi
  fi
}

parent_of() {
  optional_get "repos/$1/issues/$2/parent"
}

find_mirror() {
  local source_number=$1 pages comments candidates candidate issue found='[]' marker
  marker="Source: $SOURCE_REPO#$source_number"
  pages=$(gh api --method GET search/issues --paginate -f per_page=100 \
    -f q="repo:$MIRROR_REPO is:issue \"$marker\" in:body" | jq -s '.') || return
  if jq -e 'any(.[]; .incomplete_results == true or .total_count > 1000)' <<<"$pages" >/dev/null; then
    printf 'Mirror search is incomplete for source #%s\n' "$source_number" >&2
    return 1
  fi
  comments=$(gh api --paginate "repos/$SOURCE_REPO/issues/$source_number/comments?per_page=100" | jq -s '.') || return
  candidates=$(jq -n --argjson pages "$pages" --argjson comments "$comments" --arg repo "$MIRROR_REPO" '
    ("Mirrored to " + $repo + "#") as $prefix
    | " for planning." as $suffix
    | [
        $pages[].items[].number,
        ($comments[][] | .body // "" | select(startswith($prefix) and endswith($suffix))
          | .[($prefix | length): -($suffix | length)]
          | select(test("\\A[1-9][0-9]*\\z")) | tonumber)
      ] | unique
  ') || return
  while IFS= read -r candidate; do
    issue=$(optional_get "repos/$MIRROR_REPO/issues/$candidate") || return
    [[ $issue != null ]] || continue
    if jq -e --arg marker "$marker" '(.pull_request == null) and ((.body // "" | split("\n"))[1] == $marker)' <<<"$issue" >/dev/null; then
      found=$(jq --argjson issue "$issue" '. + [$issue]' <<<"$found") || return
    fi
  done < <(jq -r '.[]' <<<"$candidates")
  if [[ $(jq 'length' <<<"$found") -gt 1 ]]; then
    printf 'Multiple mirrors found for source #%s\n' "$source_number" >&2
    return 1
  fi
  jq '.[0] // null' <<<"$found"
}

rest_write() {
  local method=$1 endpoint=$2 payload=$3
  if [[ $DRY_RUN == 1 ]]; then
    printf 'DRY RUN: %s %s\n' "$method" "$endpoint"
    printf '%s\n' "$payload" | jq '.'
  else
    printf '%s\n' "$payload" | gh api --method "$method" "$endpoint" --input -
  fi
}

source=$(gh api "repos/$SOURCE_REPO/issues/$number")
if jq -e '.pull_request != null' <<<"$source" >/dev/null; then
  printf 'source #%s -> skipped (pull request)\n' "$number"
  exit 0
fi
content=$(jq --arg repo "$SOURCE_REPO" --arg number "$number" '
  def rewrite:
    split("\n")
    | if (.[0] | startswith("Repo:")) then .[1:] | if .[0] == "" or .[0] == "\r" then .[1:] else . end else . end
    | reduce .[] as $line ({fence: null, lines: []};
        if .fence != null then
          .fence as $fence | .lines += [$line]
          | if ($line | test("^ {0,3}" + $fence.char + "{" + ($fence.length | tostring) + ",}[ \\t\\r]*$")) then .fence = null else . end
        else
          ($line | [capture("^ {0,3}(?<ticks>`{3,}|~{3,})(?<tail>.*)$")] | .[0]) as $opening
          | if $opening != null and (($opening.ticks | startswith("~")) or ($opening.tail | contains("`") | not)) then
              .fence = {char: ($opening.ticks[0:1]), length: ($opening.ticks | length)} | .lines += [$line]
            else .lines += [($line | gsub("(?<![\\w/])#(?<n>[0-9]+)"; "\($repo)#\(.n)"))] end
        end)
    | .lines | join("\n");
  {title, body: ("Repo: " + $repo + "\nSource: " + $repo + "#" + $number + "\n\n" + ((.body // "") | rewrite))}
' <<<"$source")
mirror=$(find_mirror "$number")
if [[ $mirror == null ]]; then
  created=1
  if [[ $DRY_RUN == 1 ]]; then
    rest_write POST "repos/$MIRROR_REPO/issues" "$content"
    mirror='{"number":"new","id":null,"node_id":null,"state":"open","state_reason":null,"labels":[]}'
  else
    mirror=$(rest_write POST "repos/$MIRROR_REPO/issues" "$content")
  fi
fi
mirror_number=$(jq -r '.number' <<<"$mirror")
if [[ $created == 1 ]]; then
  payload=$(jq -n --arg body "Mirrored to $MIRROR_REPO#$mirror_number for planning." '{body: $body}')
  if [[ $DRY_RUN == 1 ]]; then
    rest_write POST "repos/$SOURCE_REPO/issues/$number/comments" "$payload"
  else
    rest_write POST "repos/$SOURCE_REPO/issues/$number/comments" "$payload" >/dev/null
  fi
fi
available=$(gh api --paginate "repos/$MIRROR_REPO/labels?per_page=100" | jq -s '[.[][] .name] | unique')
labels=$(jq --argjson available "$available" '[.labels[].name | select(. as $name | $available | index($name))] | unique' <<<"$source")
while IFS= read -r label; do
  printf 'Warning: label %s does not exist in %s\n' "$label" "$MIRROR_REPO" >&2
done < <(jq -r --argjson available "$available" '.labels[].name | select(. as $name | $available | index($name) | not)' <<<"$source")
endpoint="repos/$MIRROR_REPO/issues/$mirror_number"
patch=$(jq -n --argjson source "$source" --argjson content "$content" --argjson mirror "$mirror" --argjson labels "$labels" --argjson created "$created" '
  {}
  | if $created == 0 and $content.title != $mirror.title then .title = $content.title else . end
  | if $created == 0 and $content.body != $mirror.body then .body = $content.body else . end
  | if ($mirror.labels | map(.name) | sort) != ($labels | sort) then .labels = $labels else . end
  | if $source.state != $mirror.state or ($source.state == "closed" and $source.state_reason != $mirror.state_reason) then
      .state = $source.state | .state_reason = (if $source.state == "open" then "reopened" else $source.state_reason end)
    else . end')
if jq -ne --argjson source "$source" --argjson mirror "$mirror" '$source.state == "closed" and $mirror.state == "closed" and $source.state_reason != $mirror.state_reason' >/dev/null; then
  if [[ $DRY_RUN == 1 ]]; then
    rest_write PATCH "$endpoint" '{"state":"open","state_reason":"reopened"}'
  else
    rest_write PATCH "$endpoint" '{"state":"open","state_reason":"reopened"}' >/dev/null
  fi
fi
if [[ $patch != '{}' ]]; then
  rest_write PATCH "$endpoint" "$patch" >/dev/null
  [[ $DRY_RUN != 1 ]] || printf 'DRY RUN: PATCH %s\n%s\n' "$endpoint" "$patch"
  changed=1
fi
if [[ $DRY_RUN == 1 ]]; then
  printf 'DRY RUN: computed mirror body\n'
  jq -r '.body' <<<"$content"
fi

source_parent=$(parent_of "$SOURCE_REPO" "$number")
if [[ $source_parent != null ]] && jq -e --arg repo "$SOURCE_REPO" '.repository_url == ("https://api.github.com/repos/" + $repo)' <<<"$source_parent" >/dev/null; then
  parent_number=$(jq -r '.number' <<<"$source_parent")
  parent_mirror=$(find_mirror "$parent_number")
  if [[ $parent_mirror == null ]]; then
    printf 'Warning: source parent #%s has no mirror\n' "$parent_number" >&2
  else
    current_parent=null
    if [[ $created == 0 ]]; then current_parent=$(parent_of "$MIRROR_REPO" "$mirror_number"); fi
    if [[ $(jq -r '.id // "none"' <<<"$current_parent") != $(jq -r '.id' <<<"$parent_mirror") ]]; then
      parent_mirror_number=$(jq -r '.number' <<<"$parent_mirror")
      payload=$(jq --argjson replace "$([[ $current_parent == null ]] && printf false || printf true)" '{sub_issue_id: .id, replace_parent: $replace}' <<<"$mirror")
      rest_write POST "repos/$MIRROR_REPO/issues/$parent_mirror_number/sub_issues" "$payload" >/dev/null
      [[ $DRY_RUN != 1 ]] || printf 'DRY RUN: link mirror #%s to parent mirror #%s (replace_parent=%s)\n' "$mirror_number" "$parent_mirror_number" "$(jq -r '.replace_parent' <<<"$payload")"
      changed=1
    fi
  fi
fi

work_option=$(jq -r '[.labels[].name] | if index("type: epic") then "d17eec20" elif index("type: feature") then "c357fbb1" elif index("type: story") then "0a88d2b9" elif index("type: task") then "70992acc" else "" end' <<<"$source")
if [[ $DRY_RUN == 1 ]]; then
  printf 'DRY RUN: add/look up mirror #%s in project %s\n' "$mirror_number" "$PROJECT_ID"
  [[ -z $work_option ]] || printf 'DRY RUN: set Work Item option %s when needed\n' "$work_option"
  [[ $created == 0 ]] || printf 'DRY RUN: set Status to Backlog\n'
else
  project_item=$(gh api graphql -f query='mutation($p:ID!,$c:ID!){addProjectV2ItemById(input:{projectId:$p,contentId:$c}){item{id fieldValues(first:100){nodes{... on ProjectV2ItemFieldSingleSelectValue{optionId field{... on ProjectV2SingleSelectField{id}}}}}}}}' \
    -f p="$PROJECT_ID" -f c="$(jq -r '.node_id' <<<"$mirror")" | jq -e '.data.addProjectV2ItemById.item')
  item_id=$(jq -r '.id' <<<"$project_item")
  set_field() {
    gh api graphql -f query='mutation($p:ID!,$i:ID!,$f:ID!,$o:String!){updateProjectV2ItemFieldValue(input:{projectId:$p,itemId:$i,fieldId:$f,value:{singleSelectOptionId:$o}}){projectV2Item{id}}}' \
      -f p="$PROJECT_ID" -f i="$item_id" -f f="$1" -f o="$2" >/dev/null
  }
  work_field=PVTSSF_lAHOFBiKrM4BlhUBzhkN5Ok
  if [[ -n $work_option ]] && ! jq -e --arg field "$work_field" --arg option "$work_option" 'any(.fieldValues.nodes[]; .field.id == $field and .optionId == $option)' <<<"$project_item" >/dev/null; then
    set_field "$work_field" "$work_option"
    changed=1
  fi
  if [[ $created == 1 ]]; then
    set_field PVTSSF_lAHOFBiKrM4BlhUBzhkN5D4 85525193
  fi
fi
result=unchanged
if [[ $created == 1 ]]; then result=created; elif [[ $changed == 1 ]]; then result=updated; fi
printf 'source #%s -> mirror #%s (%s)\n' "$number" "$mirror_number" "$result"
