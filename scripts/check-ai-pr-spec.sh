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
# A link is a line of the body, outside a fenced code block and outside an
# HTML comment (<!-- ... -->), that names openspec/changes/<id>, where <id>
# holds only lowercase letters, digits and hyphens. The link counts when openspec/changes/<id>/proposal.md
# exists, or when an archived change openspec/changes/archive/YYYY-MM-DD-<id>/
# proposal.md exists, and its "Open questions" section reads "none".
# At least one link must count.
#
# Defense in depth: the id is checked against an allowlist before the
# script builds a path from it, and the script reads only under
# openspec/changes; a later containment check refuses a path that leaves
# that directory, so a new caller cannot pass a hostile id through. The
# log prints at most 64 bytes of printable ASCII for an id and replaces
# a non-printable byte of the quoted open-questions line with "?", so a
# body cannot write a terminal sequence or a very long line into the CI
# log.
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

# Collect candidate ids from lines outside fenced code blocks and outside HTML
# comments. A fence line starts with ``` or ~~~ (with an optional language
# tag). A comment starts with <!-- and ends with -->, and it may span lines.
# The id on a link line is the text after openspec/changes/ up to a space, a
# quote, a bracket, a paren or an angle bracket. The awk removes one sentence
# mark at the end of the token only when a space or the end of the line follows
# it, so "openspec/changes/demo-ready." gives "demo-ready" and
# "openspec/changes/demo-ready..x" keeps its dots. The script then validates
# the token as the body wrote it, and the allowlist refuses the rest. The
# read loop avoids mapfile, which the bash of macOS lacks.
link_ids=()
while IFS= read -r link_id; do
  link_ids+=("$link_id")
done < <(awk '
  {
    line = $0
    # Inside a fence the text is literal, so a comment marker has no effect and
    # only a fence line ends the fence.
    if (in_fence) {
      if (line ~ /^(```|~~~)/) { in_fence = 0 }
      next
    }
    # A comment opened on an earlier line ends at the first -->. The text
    # before the marker on this line is already gone; the rest is visible.
    if (in_comment) {
      cend = index(line, "-->")
      if (cend == 0) { next }
      line = substr(line, cend + 3)
      in_comment = 0
    }
    # Remove each <!-- ... --> region. A region may close on a later line.
    visible = ""
    while (1) {
      copen = index(line, "<!--")
      if (copen == 0) { visible = visible line; break }
      visible = visible substr(line, 1, copen - 1)
      line = substr(line, copen + 4)
      cend = index(line, "-->")
      if (cend == 0) { in_comment = 1; break }
      line = substr(line, cend + 3)
    }
    # A fence line toggles the fence. The check runs on the visible text, so a
    # marker inside a comment does not open a fence.
    if (visible ~ /^(```|~~~)/) { in_fence = 1; next }
    line = visible
    while (match(line, /openspec\/changes\/[^[:space:]"\x27`()<>]*/)) {
      hit = substr(line, RSTART, RLENGTH)
      id = substr(hit, length("openspec/changes/") + 1)
      rest = substr(line, RSTART + RLENGTH)
      # Remove one sentence mark at the end of the token only when a space
      # or the end of the line follows it. "demo-ready." gives "demo-ready";
      # "demo-ready..x" keeps its dots and the allowlist refuses it.
      if ((rest == "" || rest ~ /^[[:space:]]/) && id ~ /[.,;:!?]$/) {
        id = substr(id, 1, length(id) - 1)
      }
      print id
      line = rest
    }
  }
' "$body_file")

# An id holds only lowercase letters, digits and hyphens. The awk above
# removes at most one sentence mark at the end of the token, and only when a
# space or the end of the line follows it, so the script checks the token as
# the body wrote it. Anything else is refused before the script builds a
# path from it.
# Defense in depth: the containment check below refuses a resolved path
# outside openspec/changes too, even when this allowlist changes.

# The id in a log line. The id comes from the pull request body, so the
# printed form holds only printable ASCII and at most 64 bytes. A hostile
# body can then not write a terminal control sequence or a very long line
# into the CI log.
# Defense in depth: the allowlist below refuses a hostile id before the
# script builds a path from it; this bound stays so that a new caller
# cannot print a raw id through this path.
printable_id() {
  printf '%s' "$1" | LC_ALL=C tr -c '[:print:]' '?' | LC_ALL=C cut -c1-64
}

valid_ids=()
refused_note=""
# The guard keeps the empty-array case working on the bash of macOS (3.2),
# where a bare "${link_ids[@]}" on an empty array with set -u fails.
if [[ ${#link_ids[@]} -gt 0 ]]; then
  for id in "${link_ids[@]}"; do
    # A token that holds a character other than a lowercase letter, a digit
    # or a hyphen fails this allowlist and is refused. A bare
    # "openspec/changes/" also fails it, because the empty token does not
    # match.
    if [[ ! "$id" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
      refused_note="refused id \"$(printable_id "$id")\": an id holds only lowercase letters, digits and hyphens"
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
    seen_missing="no change openspec/changes/$(printable_id "$id")"
    continue
  fi
  # Defense in depth: the path must stay under openspec/changes, so a
  # hostile id cannot read another file even when the allowlist above is
  # bypassed by a new caller. The check resolves the directory of the
  # proposal and refuses a proposal that is itself a symlink, so a link
  # under openspec/changes that points outside does not pass.
  resolved_changes="$(cd -P "$changes_dir" && pwd -P)"
  proposal_dir="$(dirname "$proposal")"
  proposal_base="$(basename "$proposal")"
  resolved_dir="$(cd -P "$proposal_dir" 2>/dev/null && pwd -P || printf '%s' "$proposal_dir")"
  resolved_file="$resolved_dir/$proposal_base"
  # Defense in depth: a symlinked proposal file points at a file that the
  # directory check below does not see, because the directory of the link
  # stays inside openspec/changes. This check refuses such a link, so that
  # a new caller cannot read a file outside the tree through it.
  if [[ -L "$proposal" ]]; then
    echo "check-ai-pr-spec: refused id \"$(printable_id "$id")\": the resolved path leaves openspec/changes" >&2
    exit 1
  fi
  if [[ "$resolved_file" != "$resolved_changes" && "$resolved_file" != "$resolved_changes"/* ]]; then
    echo "check-ai-pr-spec: refused id \"$(printable_id "$id")\": the resolved path leaves openspec/changes" >&2
    exit 1
  fi
  if spec_ready "$proposal"; then
    echo "check-ai-pr-spec: openspec/changes/$(printable_id "$id") is spec-ready"
    exit 0
  fi
  seen_not_ready="$id"
  seen_questions="$(section_first_line "$proposal")"
  # The line comes from the proposal, so make it printable before it goes
  # into the log. A hostile proposal can then not write a control sequence.
  # Defense in depth: the id already goes through printable_id; this
  # encoding stays so that a new caller cannot print a raw section line.
  seen_questions="$(printf '%s' "$seen_questions" | LC_ALL=C tr -c '[:print:]' '?')"
  if [[ -z "$seen_questions" ]]; then
    seen_questions="(empty)"
  fi
  # Bound the quoted line, so a long section does not flood the log.
  seen_questions="${seen_questions:0:200}"
done

if [[ -n "$seen_not_ready" ]]; then
  echo "check-ai-pr-spec: openspec/changes/$(printable_id "$seen_not_ready") is not spec-ready: open questions: $seen_questions" >&2
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
