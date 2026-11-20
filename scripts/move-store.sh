#!/usr/bin/env bash
#
# Move the built-in SQL store adapters from pkg/store/ to internal/store/.
#
# The adapters are not SDK. They leave pkg/ so that no later version of this
# server promises their methods. pkg/store keeps only the storetest
# conformance suite, which stays public. The script is mechanical: it renames
# the packages, rewrites the import paths, rewrites the paths of sqlc.yaml and
# runs the generators. It changes no logic.
#
# Usage:
#   scripts/move-store.sh                        # run on a fresh checkout
#   scripts/move-store.sh --base <ref>           # with another base
#   scripts/move-store.sh --branch <branch>      # port an open branch
#
# Default mode runs on a tree whose tracked files hold no change. Branch mode
# ports an open branch: it merges the base into the branch and then applies
# the move again, so the branch's own edit of a store file wins and a conflict
# in a moved package is never resolved by hand.
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

mode=tree
base=origin/main
branch=

usage() {
  sed -n '2,18p' "$0"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --base)
      base="${2:?--base needs a ref}"
      shift 2
      ;;
    --branch)
      branch="${2:?--branch needs a branch}"
      mode=branch
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "move-store: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

# moved_packages prints the directory of each package to move, relative to the
# root, one for each line. pkg/store/storetest is not in the list: the
# conformance suite stays in the SDK.
moved_packages() {
  printf '%s\n' pkg/store/sqlite pkg/store/postgres pkg/store/schema pkg/store/jobcheck
  find pkg/store/internal -mindepth 1 -maxdepth 1 -type d -print | sort
}

# in_place runs sed in place on the last argument, which is the file. The
# backup suffix works on both GNU sed and BSD sed.
in_place() {
  local file="${!#}"
  [ -f "$file" ] || return 0
  sed -i.bak "$@"
  rm -f "$file.bak"
}

# rewrite_imports rewrites the import path of each moved package in every
# tracked Go file. Only the module path of this repository appears in an
# import, so the rewrite cannot touch another project's path.
rewrite_imports() {
  local file
  while IFS= read -r -d '' file; do
    in_place \
      -e 's#opendustpan.org/opendustpan/pkg/store/internal/#opendustpan.org/opendustpan/internal/store/#g' \
      -e 's#opendustpan.org/opendustpan/pkg/store/sqlite#opendustpan.org/opendustpan/internal/store/sqlite#g' \
      -e 's#opendustpan.org/opendustpan/pkg/store/postgres#opendustpan.org/opendustpan/internal/store/postgres#g' \
      -e 's#opendustpan.org/opendustpan/pkg/store/schema#opendustpan.org/opendustpan/internal/store/schema#g' \
      -e 's#opendustpan.org/opendustpan/pkg/store/#opendustpan.org/opendustpan/internal/store/#g' \
      -e 's#opendustpan.org/opendustpan/internal/store/storetest#opendustpan.org/opendustpan/pkg/store/storetest#g' \
      "$file"
  done < <(git grep -l -z -e 'opendustpan.org/opendustpan/pkg/store/' -- '*.go')
}

# rewrite_paths rewrites the paths of the moved packages in sqlc.yaml, so that
# the generators write to the new place.
rewrite_paths() {
  in_place \
    -e 's#pkg/store/sqlite#internal/store/sqlite#g' \
    -e 's#pkg/store/postgres#internal/store/postgres#g' \
    sqlc.yaml
}

# move_packages renames each package under internal/store/. A package that is
# missing is skipped: a branch may not hold every package.
move_packages() {
  local from to
  mkdir -p internal/store
  while IFS= read -r from; do
    [ -d "$from" ] || continue
    to="internal/store/$(basename "$from")"
    if [ -e "$to" ]; then
      # The base renamed this package; the branch still holds its own copy.
      # The branch's copy is the newer one, so it wins.
      rm -rf "$to"
    fi
    git mv "$from" "$to"
  done < <(moved_packages)
}

# format_imports sorts the import lines of each Go file that the move changed.
# The new paths sort in other places than the old ones, so the import groups
# need a new order. The formatter changes only import lines in these files.
format_imports() {
  local files=()
  local file
  while IFS= read -r -d '' file; do
    files+=("$file")
  done < <(git diff -M --name-only --diff-filter=d -z HEAD -- '*.go')
  [ "${#files[@]}" -gt 0 ] || return 0
  golangci-lint fmt "${files[@]}"
}

# branch_mode merges the base into the branch and applies the move again.
branch_mode() {
  if [ -n "$(git status --porcelain)" ]; then
    echo "move-store: the branch holds a change; commit or amend it first" >&2
    exit 1
  fi
  git switch "$branch"
  git merge --no-commit --no-ff "$base" || true
  move_packages
  rewrite_imports
  rewrite_paths
}

tree_mode() {
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "move-store: the tree holds a change; run it on a clean tree" >&2
    exit 1
  fi
  move_packages
  rewrite_imports
  rewrite_paths
}

case "$mode" in
  branch) branch_mode ;;
  tree) tree_mode ;;
esac

format_imports

if ! make generate; then
  echo "move-store: the move is done, but make generate failed; run it by hand" >&2
  exit 1
fi

echo "move-store: the store adapters are under internal/store/"
