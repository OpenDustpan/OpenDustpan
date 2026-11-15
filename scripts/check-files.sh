#!/usr/bin/env bash
# Check all tracked index blobs, including symbolic links as Git stores them.
# Refuse files over 1 MiB or with a NUL byte in the first 8000 bytes,
# unless .large-files-allowlist lists the exact path with a reason.
set -euo pipefail
export LC_ALL=C

limit=1048576
window=8000
if ! root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
  echo "check-files: not a git repository" >&2
  exit 1
fi
if ! cd -- "$root" 2>/dev/null; then
  echo "check-files: cannot open the repository" >&2
  exit 1
fi
allow=".large-files-allowlist"

# Defense in depth: log output does not rely on Git path parsing or allowlist validation.
# The allowlist and file checks use the raw path, not this display value.
printable_path() {
  printf '%s' "$1" | tr '[:cntrl:]' '?'
}

allow_lines=()
if [[ -e "$allow" || -L "$allow" ]]; then
  if [[ ! -f "$allow" || ! -r "$allow" ]]; then
    echo "check-files: cannot read .large-files-allowlist" >&2
    exit 1
  fi
  if ! awk '
    # Defense in depth: messages do not rely on allowlist validation.
    function printable_path(value) {
      gsub(/[[:cntrl:]]/, "?", value)
      return value
    }
    !/^#/ && /[^[:space:]]/ {
      separator = index($0, " #")
      path = separator ? substr($0, 1, separator - 1) : $0
      reason = separator ? substr($0, separator + 2) : ""
      if ($0 ~ /[[:cntrl:]]/ || path == "" || path ~ /^[-\/]/ || path ~ /(^|\/)\.\.(\/|$)/) {
        bad = 1
        print "check-files: " FILENAME ":" FNR " lists invalid path " printable_path(path) > "/dev/stderr"
      }
      if (!separator || reason !~ /[^[:space:]]/) {
        bad = 1
        print "check-files: " FILENAME ":" FNR " lists " printable_path(path) " with no reason" > "/dev/stderr"
      }
    }
    END { exit bad }
  ' "$allow"; then
    exit 1
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    allow_lines+=("$line")
  done < "$allow"
fi

# allowed reports whether the allowlist lists a path.
allowed() {
  local line entry reason
  # Defense in depth: matches still exclude comments and require a reason after validation.
  for line in "${allow_lines[@]-}"; do
    [[ "$line" != \#* && "$line" == *" #"* ]] || continue
    entry="${line%%" #"*}"
    reason="${line#*" #"}"
    if [[ "$reason" =~ [^[:space:]] && "$1" == "$entry" ]]; then
      return 0
    fi
  done
  return 1
}

work="$(mktemp -d)"
trap 'rm -rf -- "$work"' EXIT

# Write the list first so that a Git error cannot disappear in a read loop.
if ! git ls-files -s -z > "$work/entries" 2>/dev/null; then
  echo "check-files: cannot list tracked files" >&2
  exit 1
fi

# One index entry has the form: mode, object id, stage, tab, path.
# Defense in depth: the object id comes from the index entry, never from the
# path, so a name such as "0:big.txt" cannot select the blob of another file.
tab=$'\t'
entry_re='^([0-7]{6}) ([0-9a-f]{40}|[0-9a-f]{64}) ([0-3])$'
paths=()
oids=()
while IFS= read -r -d '' entry || [[ -n "$entry" ]]; do
  meta="${entry%%"$tab"*}"
  path="${entry#*"$tab"}"
  # Fail closed on any entry that does not parse, so a new shape cannot pass.
  if [[ "$entry" != *"$tab"* ]] || [[ ! "$meta" =~ $entry_re ]]; then
    printf 'check-files: cannot read the index entry %s\n' "$(printable_path "$entry")" >&2
    exit 1
  fi
  paths+=("$path")
  oids+=("${BASH_REMATCH[2]}")
done < "$work/entries"

: > "$work/oids"
for ((i = 0; i < ${#oids[@]}; i++)); do
  printf '%s\n' "${oids[i]}" >> "$work/oids"
done

# Read every blob before the allowlist check: an allowed path must still be
# readable. One Git process answers each object id, in the same order.
if ! git cat-file --batch-check < "$work/oids" > "$work/info" 2>/dev/null; then
  echo "check-files: cannot read tracked files" >&2
  exit 1
fi

# Each answer has the form: object id, blob, size. Line i answers object id i.
# Each size also gives the offset of the content in the batch stream below:
# the header is "oid blob size" and one LF, then the content, then one LF.
# Defense in depth: the offsets rely on this framing. The checks on each line,
# and the total size check below, stop the script when the stream departs from
# this shape.
sizes=()
offsets=()
expected=0
count=0
while read -r roid rtype rsize rest || [[ -n "$roid" ]]; do
  if (( count >= ${#oids[@]} )) || [[ "$rest" != "" || "$roid" != "${oids[$count]}" ]]; then
    echo "check-files: cannot read tracked files" >&2
    exit 1
  fi
  if [[ "$rtype" == "blob" && "$rsize" =~ ^[0-9]+$ ]]; then
    sizes[$count]="$rsize"
    header=$(( ${#roid} + ${#rtype} + ${#rsize} + 3 ))
    offsets[$count]=$(( expected + header ))
    expected=$(( expected + header + rsize + 1 ))
  else
    printf 'check-files: cannot read %s\n' "$(printable_path "${paths[$count]}")" >&2
    exit 1
  fi
  count=$((count + 1))
done < "$work/info"
if (( count != ${#oids[@]} )); then
  echo "check-files: cannot read tracked files" >&2
  exit 1
fi

# One Git process writes every blob into one framed stream, in the same order.
if ! git cat-file --batch < "$work/oids" > "$work/batch" 2>/dev/null; then
  echo "check-files: cannot read tracked files" >&2
  exit 1
fi
actual="$(wc -c < "$work/batch")"
if (( actual != expected )); then
  echo "check-files: cannot read tracked files" >&2
  exit 1
fi

problems=0
for ((i = 0; i < ${#paths[@]}; i++)); do
  path="${paths[i]}"
  if allowed "$path"; then
    continue
  fi

  size="${sizes[i]}"
  if (( size > limit )); then
    printf 'check-files: %s is %d bytes, over %d\n' "$(printable_path "$path")" "$size" "$limit" >&2
    problems=$((problems + 1))
  fi

  check_bytes=$window
  if (( size < check_bytes )); then
    check_bytes=$size
  fi
  # Only the hex text enters a shell variable, never the NUL bytes. In the hex
  # form one NUL byte appears as a " 00" pair, and no other pair can hold a
  # space, because every byte gets two hex digits.
  if ! hex="$(od -An -v -j "${offsets[i]}" -N "$check_bytes" -t x1 "$work/batch" 2>/dev/null)"; then
    printf 'check-files: cannot read %s\n' "$(printable_path "$path")" >&2
    exit 1
  fi
  if [[ "$hex" == *" 00"* ]]; then
    printf 'check-files: %s is binary\n' "$(printable_path "$path")" >&2
    problems=$((problems + 1))
  fi
done

if (( problems > 0 )); then
  echo "check-files: for each refused file, add to .large-files-allowlist with a reason, or remove it" >&2
  exit 1
fi

echo "check-files: no file over $limit bytes and no binary file without an allowlist entry"
