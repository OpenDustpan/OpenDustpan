#!/usr/bin/env bash
# Check all tracked index blobs, including symbolic links as Git stores them.
# Refuse files over 1 MiB or with a NUL byte in the first 8000 bytes,
# unless .large-files-allowlist lists the exact path with a reason.
set -euo pipefail
export LC_ALL=C

limit=1048576
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
if ! git ls-files -z > "$work/paths" 2>/dev/null; then
  echo "check-files: cannot list tracked files" >&2
  exit 1
fi

problems=0
while IFS= read -r -d '' path; do
  # Read before the allowlist check: an allowed path must still be readable.
  if ! git cat-file blob ":$path" > "$work/blob" 2>/dev/null; then
    printf 'check-files: cannot read %s\n' "$(printable_path "$path")" >&2
    exit 1
  fi
  if allowed "$path"; then
    continue
  fi

  size="$(wc -c < "$work/blob")"
  if (( size > limit )); then
    printf 'check-files: %s is %d bytes, over %d\n' "$(printable_path "$path")" "$size" "$limit" >&2
    problems=$((problems + 1))
  fi

  # Only the byte count enters a shell variable, never the NUL bytes.
  nul_bytes="$(head -c 8000 "$work/blob" | tr -cd '\000' | wc -c)"
  if (( nul_bytes > 0 )); then
    printf 'check-files: %s is binary\n' "$(printable_path "$path")" >&2
    problems=$((problems + 1))
  fi
done < "$work/paths"

if (( problems > 0 )); then
  echo "check-files: for each refused file, add to .large-files-allowlist with a reason, or remove it" >&2
  exit 1
fi

echo "check-files: no file over $limit bytes and no binary file without an allowlist entry"
