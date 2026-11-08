#!/usr/bin/env bash
# Stacked pull requests in a cloud session. REST only: no GraphQL.
# See docs/agents/stacked-prs.md.
#
#   stack.sh create --bodies DIR <bottom> <next>...
#   stack.sh status
#   stack.sh advance <stack>
#   stack.sh unstack <stack>
set -euo pipefail

REPO="${STACK_REPO:-OpenDustpan/OpenDustpan}"
TRUNK="${STACK_TRUNK:-main}"
REMOTE="${STACK_REMOTE:-origin}"

die() {
  echo "stack.sh: $*" >&2
  exit 1
}

usage() {
  cat >&2 <<'EOF'
Usage:
  stack.sh create --bodies DIR <bottom> <next>...
  stack.sh status
  stack.sh advance <stack>
  stack.sh unstack <stack>
EOF
  exit 2
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is not installed."
}

# Reject a branch name that is not on the allowlist.
check_branch_name() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._/-]*$ ]] || die "Branch name not allowed: $1"
  [[ "$1" != *..* ]] || die "Branch name not allowed: $1"
}

# Print the number of the open PR with head $1, or nothing.
open_pr_for() {
  gh api -X GET "repos/$REPO/pulls" -f state=open -f head="${REPO%%/*}:$1" --jq '.[0].number // empty'
}

cmd_create() {
  local bodies=""
  if [[ "${1:-}" == "--bodies" ]]; then
    bodies="${2:-}"
    shift 2 || usage
  fi
  [[ -n "$bodies" && -d "$bodies" ]] || die "Give --bodies DIR. DIR holds one file per branch, named <branch with / as ->.md."
  [[ $# -ge 2 ]] || die "A stack needs at least two branches: create --bodies DIR <bottom> <next>..."

  local branch local_sha remote_sha
  for branch in "$@"; do
    check_branch_name "$branch"
    local_sha="$(git rev-parse --verify -q "refs/heads/$branch")" || die "Branch $branch does not exist locally."
    remote_sha="$(git ls-remote "$REMOTE" "refs/heads/$branch" | cut -f1)"
    [[ -n "$remote_sha" ]] || die "Branch $branch is not pushed. Run: git push -u $REMOTE $branch"
    [[ "$local_sha" == "$remote_sha" ]] || die "Branch $branch differs from $REMOTE/$branch. Run: git push $REMOTE $branch"
    [[ -f "$bodies/${branch//\//-}.md" ]] || die "Body file missing: $bodies/${branch//\//-}.md"
  done

  local base="$TRUNK" number title existing_base
  local -a numbers=()
  for branch in "$@"; do
    number="$(open_pr_for "$branch")"
    if [[ -n "$number" ]]; then
      existing_base="$(gh api "repos/$REPO/pulls/$number" --jq '.base.ref')"
      [[ "$existing_base" == "$base" ]] || die "PR $number has base $existing_base. It must be $base. Run: gh api -X PATCH repos/$REPO/pulls/$number -f base=$base"
    else
      title="$(git log -1 --format=%s "refs/heads/$branch")"
      number="$(gh api -X POST "repos/$REPO/pulls" \
        -f title="$title" -f head="$branch" -f base="$base" \
        -F body=@"$bodies/${branch//\//-}.md" -F draft=true --jq '.number')"
      echo "Opened draft PR $number: $branch -> $base"
      if gh api -X POST "repos/$REPO/pulls/$number/ccr/ready_for_review" >/dev/null; then
        echo "PR $number is ready for review."
      else
        echo "stack.sh: PR $number stays a draft. Mark it ready by hand." >&2
      fi
    fi
    numbers+=("$number")
    base="$branch"
  done

  link_stack "${numbers[@]}"
}

# Link the PR numbers (bottom to top) as one stack. Print the stack URL.
link_stack() {
  local json
  json="$(printf '%s\n' "$@" | jq -s '{pull_requests: .}')"
  echo "$json" | gh api -X POST "repos/$REPO/stacks" --input - --jq '"Stack " + (.number|tostring) + ": https://github.com/'"$REPO"'/pull/" + (.pull_requests[0].number|tostring)'
}

cmd_status() {
  local out
  out="$(gh api -X GET "repos/$REPO/stacks" --jq '
    .[] | "Stack \(.number) (base \(.base.ref), \(if .open then "open" else "closed" end))",
      (.pull_requests[] | "  #\(.number) \(.head.ref) -> \(.base.ref): \(if .merged_at then "merged" elif .state == "closed" then "closed" elif .draft then "draft" else "ready" end)")')"
  if [[ -z "$out" ]]; then
    echo "No stacks."
  else
    echo "$out"
  fi
}

cmd_advance() {
  local n="${1:-}"
  [[ "$n" =~ ^[0-9]+$ ]] || usage
  if ! git diff --quiet || ! git diff --cached --quiet; then
    die "The working tree has changes. Commit or stash them first."
  fi

  local stack
  stack="$(gh api "repos/$REPO/stacks/$n")" || die "Stack $n not found. Run: stack.sh status"

  local bottom_merged
  bottom_merged="$(echo "$stack" | jq -r '.pull_requests[0].merged_at // empty')"
  [[ -n "$bottom_merged" ]] || die "The bottom PR of stack $n is not merged. Merge it first."

  # The first PR that is not merged is the next layer.
  local next head
  next="$(echo "$stack" | jq -r '[.pull_requests[] | select(.merged_at == null and .state == "open")][0].number // empty')"
  [[ -n "$next" ]] || die "Stack $n has no open PR left."
  head="$(echo "$stack" | jq -r --argjson n "$next" '.pull_requests[] | select(.number == $n) | .head.ref')"
  check_branch_name "$head"

  gh api -X PATCH "repos/$REPO/pulls/$next" -f base="$TRUNK" --jq '"PR " + (.number|tostring) + " now has base " + .base.ref'

  git fetch "$REMOTE" "$TRUNK" "$head"
  git checkout -q -B "$head" "$REMOTE/$head"
  if ! git merge --no-ff --no-edit -m "Merge $TRUNK into $head" "$REMOTE/$TRUNK"; then
    git merge --abort
    die "Merging $TRUNK into $head has conflicts. Resolve them by hand, push, then run: stack.sh advance $n"
  fi
  git push "$REMOTE" "$head"

  # Link the remaining open PRs again if GitHub dropped the stack.
  local -a rest
  mapfile -t rest < <(echo "$stack" | jq -r '.pull_requests[] | select(.merged_at == null and .state == "open") | .number')
  if [[ ${#rest[@]} -lt 2 ]]; then
    echo "One PR left. No stack to link."
    return 0
  fi
  local current
  current="$(gh api -X GET "repos/$REPO/stacks" --jq "[.[] | select(.number == $n) | .pull_requests[].number] | join(\",\")")"
  if [[ -n "$current" ]]; then
    gh api -X POST "repos/$REPO/stacks/$n/unstack" >/dev/null
  fi
  link_stack "${rest[@]}"
}

# Remove the stack link of stack $1. It changes no PR, base or branch.
cmd_unstack() {
  local n="${1:-}"
  [[ "$n" =~ ^[0-9]+$ ]] || usage
  gh api -X POST "repos/$REPO/stacks/$n/unstack" >/dev/null || die "Unstack of stack $n failed. Run: stack.sh status"
  echo "Stack $n is unstacked. The PRs keep their bases and branches."
}

need gh
need jq
need git

[[ $# -ge 1 ]] || usage
sub="$1"
shift
case "$sub" in
  create) cmd_create "$@" ;;
  status) cmd_status ;;
  advance) cmd_advance "$@" ;;
  unstack) cmd_unstack "$@" ;;
  *) usage ;;
esac
