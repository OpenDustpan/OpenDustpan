#!/usr/bin/env bash
# Check that an AI-authored pull request names a spec-ready OpenSpec change.
# Usage: PR_BODY_FILE=<file> PR_COMMITS_FILE=<file> scripts/check-ai-pr-spec.sh
#
# CI writes the pull request body and the author email of each commit of the
# pull request into two files and names them in PR_BODY_FILE and
# PR_COMMITS_FILE. The script reads the change folders under
# OPENSPEC_CHANGES_DIR (default openspec/changes).
#
# A pull request is AI-authored when a commit author email is
# noreply@anthropic.com, or when the body has a line that starts with "Tool:"
# or "AI tool:". Every other pull request is a person's pull request and
# passes.
#
# An AI-authored pull request must name a change whose proposal is
# spec-ready: the first line of its section "## Open questions" reads "none",
# or the proposal has the line "Open questions: none". An archived change
# (openspec/changes/archive/YYYY-MM-DD-<id>) counts.
#
# A link is a line of the body, outside a fenced code block, that names
# openspec/changes/<id>.
#
# Defense in depth: the body and the proposals are untrusted input. The script
# reads them as data with awk and grep only, and it never runs a line of them.
# It validates the change id with an allowlist before it builds a path from
# it, and it reads and writes only under the change folder.
set -euo pipefail

# LC_ALL=C keeps the length caps below on bytes, and it keeps the regular
# expressions on the ASCII alphabet of a change id.
export LC_ALL=C

body_file="${PR_BODY_FILE:-}"
commits_file="${PR_COMMITS_FILE:-}"
changes_dir="${OPENSPEC_CHANGES_DIR:-openspec/changes}"

if [[ -z "$body_file" || -z "$commits_file" ]]; then
  echo "usage: PR_BODY_FILE=<file> PR_COMMITS_FILE=<file> check-ai-pr-spec.sh" >&2
  exit 1
fi

for file in "$body_file" "$commits_file"; do
  if [[ ! -f "$file" || ! -r "$file" ]]; then
    echo "check-ai-pr-spec: $file is not a readable file" >&2
    exit 1
  fi
done

# sanitize removes CR, LF and ESC from an untrusted text and cuts it to a
# maximum number of bytes. This is layer 3 for the change id: the id reaches
# the log and the error only through this function, so a hostile id cannot
# forge a log line or an ANSI escape. It does not rely on the allowlist (layer
# 1) or on the length cap (layer 2).
sanitize() {
  local text="$1" max="$2"

  text="${text//$'\r'/}"
  text="${text//$'\n'/}"
  text="${text//$'\033'/}"

  printf '%s' "${text:0:$max}"
}

# first_open_question prints the first non-blank line of the section
# "## Open questions" of a proposal.
first_open_question() {
  awk '
    {
      low = tolower($0)
      if (low ~ /^##[[:space:]]+open[[:space:]]+questions[[:space:]]*$/) { in_section = 1; next }
      if (in_section && low ~ /^##[[:space:]]/) exit
      if (!in_section) next
      line = $0
      sub(/^[[:space:]]+/, "", line)
      sub(/[[:space:]]+$/, "", line)
      if (line != "") { print line; exit }
    }
  ' "$1"
}

# proposal_is_ready reports whether a proposal is spec-ready: the first line
# of its section "## Open questions" reads "none", or the proposal has the
# line "Open questions: none".
proposal_is_ready() {
  local file="$1"

  if grep -Eqi '^[[:space:]]*open[[:space:]]+questions[[:space:]]*:[[:space:]]*none[[:space:]]*$' "$file"; then
    return 0
  fi

  local first lower
  first="$(first_open_question "$file")"
  # tr, not ${first,,}: macOS ships bash 3.2, which has no lowercase
  # expansion.
  lower="$(tr '[:upper:]' '[:lower:]' <<< "$first")"

  [[ "$lower" == "none" ]]
}

# Signal 1: a commit of the pull request has the author email of an agent
# commit.
ai=""
if grep -Eqi '^[[:space:]]*noreply@anthropic\.com[[:space:]]*$' "$commits_file"; then
  ai=1
fi

# Keep the lines of the body that are outside a fenced code block. A block
# opens with a line of three backticks or three tildes, after optional spaces,
# and only the same marker closes it.
prose="$(awk '
  {
    if (marker == "") {
      if ($0 ~ /^[[:space:]]*```/) { marker = "```"; next }
      if ($0 ~ /^[[:space:]]*~~~/) { marker = "~~~"; next }
      print
      next
    }
    if ($0 ~ ("^[[:space:]]*" marker)) { marker = "" }
  }
' "$body_file")"

# Signal 2: the body has a line that starts with "Tool:" or "AI tool:". The
# text goes in as a here-string, not through a pipe: grep exits at the first
# match, and under pipefail a pipe gives the writer SIGPIPE on a body over the
# pipe buffer, which would miss the signal.
if grep -Eqi '^[[:space:]]*(ai[[:space:]]+tool|tool)[[:space:]]*:' <<< "$prose"; then
  ai=1
fi

if [[ -z "$ai" ]]; then
  exit 0
fi

# The candidate ids: the text after "openspec/changes/" to the end of the
# line, for each line outside a fenced code block. The text stays whole, so
# that a space or a path character in it reaches layer 1 and fails there.
matches="$(grep -oE 'openspec/changes/[^[:space:]].*' <<< "$prose" || true)"

counted=""
problems=""

while IFS= read -r match; do
  [[ -n "$match" ]] || continue

  # The text after the prefix is the id. A CRLF line ending leaves one
  # trailing CR, which is part of the line ending and not of the id.
  id="${match#openspec/changes/}"
  id="${id%$'\r'}"

  # Layer 2 (limit the effect): cap the length before the allowlist runs, so a
  # long value costs no regular expression. Defense in depth: the allowlist
  # below is also bounded. The message below is this layer's own message, so a
  # case that asserts it fails when this check goes away.
  if (( ${#id} > 80 )); then
    problems+="check-ai-pr-spec: ignored a change id that is longer than 80 bytes: $(sanitize "$id" 80)"$'\n'
    continue
  fi

  # Layer 1 (allowlist): refuse every id outside ^[a-z0-9][a-z0-9-]{0,79}$
  # before any path is built from it. Defense in depth: layer 2 also refuses a
  # long id, and layer 3 sanitizes every refused id that goes to the log.
  if [[ ! "$id" =~ ^[a-z0-9][a-z0-9-]{0,79}$ ]]; then
    problems+="check-ai-pr-spec: ignored a change id that is not valid: $(sanitize "$id" 80)"$'\n'
    continue
  fi

  # The change folder of the head, or the folder of an archived change:
  # archive/YYYY-MM-DD-<id>.
  proposal=""
  if [[ -f "$changes_dir/$id/proposal.md" ]]; then
    proposal="$changes_dir/$id/proposal.md"
  else
    for archived in "$changes_dir"/archive/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-"$id"; do
      if [[ -f "$archived/proposal.md" ]]; then
        proposal="$archived/proposal.md"
        break
      fi
    done
  fi

  if [[ -z "$proposal" ]]; then
    problems+="check-ai-pr-spec: the change $id has no proposal under $changes_dir"$'\n'
    continue
  fi

  if proposal_is_ready "$proposal"; then
    counted=1
    break
  fi

  open_question="$(first_open_question "$proposal")"
  problems+="check-ai-pr-spec: the change $id is not spec-ready: Open questions: $(sanitize "$open_question" 200)"$'\n'
done <<< "$matches"

if [[ -n "$counted" ]]; then
  exit 0
fi

printf '%s' "$problems" >&2
printf '%s\n' 'check-ai-pr-spec: the pull request names no spec-ready change: add a line "Change: openspec/changes/<id>" for a change whose proposal says "Open questions: none".' >&2
exit 1
