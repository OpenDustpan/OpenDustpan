#!/usr/bin/env bash
# Check that a branch does not move the description pin before the first tag.
# Usage: scripts/check-pin-frozen.sh [base-ref]   (default origin/main)
#
# The check runs the Go command in cmd/pinfrozen. It reads the release record
# with the same package that the description test uses, so the two agree on
# when the first tag shipped. The command does nothing after the first tag.
set -euo pipefail

if [[ $# -gt 1 ]]; then
  echo "usage: check-pin-frozen.sh [base-ref]" >&2
  exit 2
fi

base="${1:-origin/main}"
root="$(git rev-parse --show-toplevel)"

cd "$root"
exec go run ./cmd/pinfrozen "$base"
