#!/usr/bin/env bash
# Check that an AI-authored pull request links a spec-ready OpenSpec change.
# Usage: scripts/check-ai-pr-spec.sh <body-file> <commits-file> [repo-root]
#
# The body file holds the pull request body. The commits file holds the
# author email of each commit of the pull request, one per line, from
# git log --format='%ae' <base>..<head>. The repo root defaults to the
# top level of the checkout and holds the change folders under
# openspec/changes.
#
# A pull request is AI-authored when a commit has the author email
# noreply@anthropic.com, or when the body has a disclosure line that starts
# with "Tool:" or "AI tool:". Any other pull request is a person's pull
# request and passes with no link.
#
# A link is a line of the body, outside a fenced code block, that names
# openspec/changes/<id>, where <id> holds only lowercase letters, digits
# and hyphens. The link counts when openspec/changes/<id>/proposal.md
# exists, or when an archived change openspec/changes/archive/YYYY-MM-DD-<id>/
# proposal.md exists, and its "Open questions" section reads "none".
# At least one link must count.
#
# Defense in depth: the id is checked against an allowlist before the
# script builds a path from it, and the script reads only under
# openspec/changes; a later containment check refuses a path that leaves
# that directory, so a new caller cannot pass a hostile id through.
set -euo pipefail

if [[ $# -gt 3 ]]; then
  echo "usage: check-ai-pr-spec.sh <body-file> <commits-file> [repo-root]" >&2
  exit 2
fi

body_file="${1:-${AI_PR_BODY_FILE:-}}"
commits_file="${2:-${AI_PR_COMMITS_FILE:-}}"
root="${3:-${AI_PR_ROOT:-$(git rev-parse --show-toplevel)}}"

if [[ -z "$body_file" || -z "$commits_file" ]]; then
  echo "usage: check-ai-pr-spec.sh <body-file> <commits-file> [repo-root]" >&2
  exit 2
fi

if [[ ! -f "$body_file" ]]; then
  echo "check-ai-pr-spec: body file $body_file is missing" >&2
  exit 2
fi

if [[ ! -f "$commits_file" ]]; then
  echo "check-ai-pr-spec: commits file $commits_file is missing" >&2
  exit 2
fi

changes_dir="$root/openspec/changes"
if [[ ! -d "$changes_dir" ]]; then
  echo "check-ai-pr-spec: $changes_dir is missing" >&2
  exit 2
fi

fix_line='the pull request names no spec-ready change: add a line "Change: openspec/changes/<id>" for a change whose proposal says "Open questions: none"'

# The disclosure signal: a line that starts with "Tool:" or "AI tool:".
# This check and the commit-email check below are independent signals; the
# pull request needs only one of them to count as AI-authored.
ai_by_body=0
if grep -Eq '^[[:space:]]*(Tool|AI tool):' "$body_file"; then
  ai_by_body=1
fi

ai_by_commit=0
if grep -Eq '^[[:space:]]*noreply@anthropic\.com[[:space:]]*$' "$commits_file"; then
  ai_by_commit=1
fi

if [[ "$ai_by_body" -eq 0 && "$ai_by_commit" -eq 0 ]]; then
  echo "check-ai-pr-spec: no AI signal; the pull request is a person's pull request and needs no change link"
  exit 0
fi

# Collect candidate ids from lines outside fenced code blocks. A fence line
# starts with ``` or ~~~ (with an optional language tag). The id on a link
# line is the text after openspec/changes/ up to a space, quote, bracket or
# paren. The read loop avoids mapfile, which the bash of macOS lacks.
link_ids=()
while IFS= read -r link_id; do
  link_ids+=("$link_id")
done < <(awk '
  /^(```|~~~)/ { in_fence = !in_fence; next }
  in_fence { next }
  {
    line = $0
    while (match(line, /openspec\/changes\/[^ [:space:]"\x27`()<>]+/)) {
      hit = substr(line, RSTART, RLENGTH)
      id = substr(hit, length("openspec/changes/") + 1)
      print id
      line = substr(line, RSTART + RLENGTH)
    }
  }
' "$body_file")

# An id holds only lowercase letters, digits and hyphens. Anything else is
# refused before the script builds a path from it.
# Defense in depth: the containment check below refuses a resolved path
# outside openspec/changes too, even when this allowlist changes.
valid_ids=()
refused_note=""
# The guard keeps the empty-array case working on the bash of macOS (3.2),
# where a bare "${link_ids[@]}" on an empty array with set -u fails.
if [[ ${#link_ids[@]} -gt 0 ]]; then
  for raw in "${link_ids[@]}"; do
    # Strip trailing slashes and sentence punctuation that the text around
    # the link adds. Pure bash, so the strip works the same on the bash of
    # Linux and of macOS.
    id="$raw"
    while true; do
      case "$id" in
      *"/" | *"." | *"," | *";" | *":" | *"!" | *"?" | *")") id="${id%?}" ;;
      *) break ;;
      esac
    done
    if [[ -z "$id" ]]; then
      refused_note="refused id \"$raw\": an id holds only lowercase letters, digits and hyphens"
      continue
    fi
    if [[ ! "$id" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
      refused_note="refused id \"$id\": an id holds only lowercase letters, digits and hyphens"
      continue
    fi
    valid_ids+=("$id")
  done
fi

# A proposal is spec-ready when it holds the line "Open questions: none",
# or when the first non-blank line of its "## Open questions" section reads
# "none"; text after that line, such as answered questions, does not matter.
spec_ready() {
  local proposal="$1"
  if grep -Eq '^[[:space:]]*Open questions:[[:space:]]*none[[:space:]]*$' "$proposal"; then
    return 0
  fi
  local first
  first="$(section_first_line "$proposal")"
  # Lowercase without ${var,,}, which the bash of macOS lacks.
  first="$(printf '%s' "$first" | tr '[:upper:]' '[:lower:]')"
  [[ "$first" == "none" ]]
}

# The first non-blank line of the "## Open questions" section of a
# proposal, with leading and trailing blank space removed, or empty when
# the section has no such line.
section_first_line() {
  local proposal="$1"
  awk '
    /^## / { if (seen) exit; if ($0 ~ /^## Open questions[[:space:]]*$/) { seen = 1; next } }
    seen { print }
  ' "$proposal" | tr -d '\r' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' | head -n 1 || true
}

resolve_proposal() {
  local id="$1"
  local direct="$changes_dir/$id/proposal.md"
  if [[ -f "$direct" ]]; then
    printf '%s' "$direct"
    return 0
  fi
  # An archived change lives in a directory named YYYY-MM-DD-<id>
  # (openspec/changes/archive/YYYY-MM-DD-<id>/proposal.md). The match
  # takes the whole date prefix and the hyphen boundary: id "spec" matches
  # "2026-01-01-spec", and it does not match "2026-01-01-specx" or
  # "2026-01-01-ai-pr-spec".
  local candidate
  while IFS= read -r candidate; do
    case "$(basename "$(dirname "$candidate")")" in
    [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-"$id")
      printf '%s' "$candidate"
      return 0
      ;;
    esac
  done < <(find "$changes_dir/archive" -maxdepth 2 -name proposal.md 2>/dev/null | sort || true)
  return 1
}

if [[ ${#valid_ids[@]} -eq 0 ]]; then
  if [[ -n "$refused_note" ]]; then
    echo "check-ai-pr-spec: $refused_note: $fix_line" >&2
  else
    echo "check-ai-pr-spec: $fix_line" >&2
  fi
  exit 1
fi

seen_not_ready=""
seen_questions=""
seen_missing=""
for id in "${valid_ids[@]}"; do
  proposal=""
  if ! proposal="$(resolve_proposal "$id")"; then
    seen_missing="no change openspec/changes/$id"
    continue
  fi
  # Defense in depth: the path must stay under openspec/changes, so a
  # hostile id cannot read another file even when the allowlist above is
  # bypassed by a new caller. The check resolves symlinks, so a link under
  # openspec/changes that points outside does not pass.
  resolved_changes="$(cd -P "$changes_dir" && pwd -P)"
  proposal_dir="$(dirname "$proposal")"
  resolved_dir="$(cd -P "$proposal_dir" 2>/dev/null && pwd -P || printf '%s' "$proposal_dir")"
  if [[ "$resolved_dir" != "$resolved_changes" && "$resolved_dir" != "$resolved_changes"/* ]]; then
    echo "check-ai-pr-spec: refused id \"$id\": the resolved path leaves openspec/changes" >&2
    exit 1
  fi
  if spec_ready "$proposal"; then
    echo "check-ai-pr-spec: openspec/changes/$id is spec-ready"
    exit 0
  fi
  seen_not_ready="$id"
  seen_questions="$(section_first_line "$proposal")"
  if [[ -z "$seen_questions" ]]; then
    seen_questions="(empty)"
  fi
  # Bound the quoted line, so a long section does not flood the log.
  seen_questions="${seen_questions:0:200}"
done

if [[ -n "$seen_not_ready" ]]; then
  echo "check-ai-pr-spec: openspec/changes/$seen_not_ready is not spec-ready: open questions: $seen_questions" >&2
  exit 1
fi

if [[ -n "$seen_missing" ]]; then
  echo "check-ai-pr-spec: $seen_missing: $fix_line" >&2
  exit 1
fi

if [[ -n "$refused_note" ]]; then
  echo "check-ai-pr-spec: $refused_note: $fix_line" >&2
  exit 1
fi

echo "check-ai-pr-spec: $fix_line" >&2
exit 1
