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
# Defense in depth: the id that the script reads from the body goes through
# three layers. The length cap refuses an id over 80 bytes before any regex
# runs; the allowlist ^[a-z0-9][a-z0-9-]{0,79}$ refuses every other token
# before the script builds a path from it; the log form removes CR, LF and
# ESC and cuts the text to 80 bytes before the script writes a refused id.
# The script also reads only under openspec/changes, and a later containment
# check refuses a path that leaves that directory, so a new caller cannot
# pass a hostile id through. The quoted open-questions line replaces a
# non-printable byte with "?", so a proposal cannot write a terminal
# sequence or a very long line into the CI log.
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
#
# grep reports 0 for a match, 1 for no match and 2 for an error. An error is
# not "no signal": the check stops, because a body or a commit list that the
# check cannot read must not pass as a person's pull request.
# Defense in depth: the file checks above refuse a missing file; this check
# stays so that a file that the check cannot read, which those checks do not
# see, cannot pass as a person's pull request.
body_status=0
grep -Eq '^[[:space:]]*(Tool|AI tool):' "$body_file" || body_status=$?
if [[ "$body_status" -ge 2 ]]; then
  echo "check-ai-pr-spec: cannot read body file $body_file" >&2
  exit 2
fi
ai_by_body=0
if [[ "$body_status" -eq 0 ]]; then
  ai_by_body=1
fi

commits_status=0
grep -Eq '^[[:space:]]*noreply@anthropic\.com[[:space:]]*$' "$commits_file" || commits_status=$?
if [[ "$commits_status" -ge 2 ]]; then
  echo "check-ai-pr-spec: cannot read commits file $commits_file" >&2
  exit 2
fi
ai_by_commit=0
if [[ "$commits_status" -eq 0 ]]; then
  ai_by_commit=1
fi

if [[ "$ai_by_body" -eq 0 && "$ai_by_commit" -eq 0 ]]; then
  echo "check-ai-pr-spec: no AI signal; the pull request is a person's pull request and needs no change link"
  exit 0
fi

# Collect candidate ids from lines outside fenced code blocks and outside HTML
# comments. A fence line starts with ``` or ~~~ (with an optional language
# tag); the opening fence records its marker character and its length, and only
# a line of at least that many of the same character closes it. A comment
# starts with <!-- and ends with -->, and it may span lines.
# The id on a link line is the text after openspec/changes/ up to a quote,
# a paren or an angle bracket. A space, a slash, a dot and every other
# character stay in the token, because the script passes the token as the
# body wrote it, with no change to the text, and the allowlist refuses a
# token that is not an id: "openspec/changes/bad id" with its space,
# "openspec/changes/../x" with its dots, or "openspec/changes/demo-ready."
# with its sentence mark. The read loop avoids mapfile, which the bash of
# macOS lacks.
link_ids=()
while IFS= read -r link_id; do
  link_ids+=("$link_id")
done < <(awk '
  {
    line = $0
    # Trailing blank space of a line is never part of an id: a CRLF line end
    # or a stray space at the end must not turn a valid link into a refused
    # one. Blank space inside the token stays, and the allowlist refuses it.
    sub(/[[:space:]]+$/, "", line)
    # Inside a fence the text is literal, so a comment marker has no effect and
    # only the closing fence ends the fence. The closing fence holds the marker
    # character of the opening fence, at least as many times; GitHub keeps the
    # fence open on any other line, such as a ~~~ line inside a ``` block.
    if (in_fence) {
      run = 0
      while (substr(line, run + 1, 1) == fence_char) { run++ }
      if (run >= fence_len) { in_fence = 0 }
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
    # A fence line opens a fence. The check records the marker character and
    # its length, because only that character and that many of it close the
    # fence. The check runs on the visible text, so a marker inside a comment
    # does not open a fence.
    if (visible ~ /^(```|~~~)/) {
      fence_char = substr(visible, 1, 1)
      fence_len = 0
      while (substr(visible, fence_len + 1, 1) == fence_char) { fence_len++ }
      in_fence = 1
      next
    }
    line = visible
    while (match(line, /openspec\/changes\/[^"\x27`()<>]*/)) {
      hit = substr(line, RSTART, RLENGTH)
      id = substr(hit, length("openspec/changes/") + 1)
      rest = substr(line, RSTART + RLENGTH)
      print id
      line = rest
    }
  }
' "$body_file")

# An id goes through three layers before the script builds a path from it:
# the length cap, then the allowlist, and the log form for a refused id.

# The id in a log line. The id comes from the pull request body, so before
# the script writes a refused id to its log or its error, it removes CR,
# LF and ESC and cuts the text to 80 bytes. A hostile body can then not
# write a terminal control sequence or a second line into the CI log.
# Defense in depth: the length cap and the allowlist below refuse a hostile
# id before the script builds a path from it; this encoding stays so that a
# new caller cannot print a raw id through this path.
printable_id() {
  printf '%s' "$1" | LC_ALL=C tr -d '\r\n\033' | LC_ALL=C cut -c1-80
}

valid_ids=()
refused_note=""
# The guard keeps the empty-array case working on the bash of macOS (3.2),
# where a bare "${link_ids[@]}" on an empty array with set -u fails.
if [[ ${#link_ids[@]} -gt 0 ]]; then
  for id in "${link_ids[@]}"; do
    # The length cap: at most 80 bytes. It runs before the allowlist, so an
    # over-long id is refused with no regex run. The cap looks redundant
    # with the {0,79} bound of the allowlist below and with the 80-byte cut
    # of printable_id above; it stays so that neither of those layers ever
    # sees an over-long id. Defense in depth: this cap does not rely on the
    # allowlist or on the log form.
    id_bytes="$(printf '%s' "$id" | wc -c)"
    if [[ "$id_bytes" -gt 80 ]]; then
      refused_note="refused id \"$(printable_id "$id")\": an id holds at most 80 bytes"
      continue
    fi
    # The allowlist: only a lowercase letter, a digit or a hyphen, 1 to 80
    # of them. A token that holds any other character fails it, and a bare
    # "openspec/changes/" fails it too, because the empty token does not
    # match. The length cap above already refused an over-long id, and the
    # containment check below refuses a resolved path outside
    # openspec/changes; this allowlist stays so that a "../x", an
    # upper-case id, an id with a space or a sentence mark never becomes a
    # path at all. Defense in depth: this allowlist does not rely on the
    # length cap or on the containment check.
    if [[ ! "$id" =~ ^[a-z0-9][a-z0-9-]{0,79}$ ]]; then
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
