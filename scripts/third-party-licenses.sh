#!/usr/bin/env bash
# Collect the license files of the code that is linked into the server binary.
# Usage: scripts/third-party-licenses.sh <goos> <goarch> <outdir>
#        scripts/third-party-licenses.sh --check <goos>/<goarch>...
#
# The script asks the Go tool which packages the binary links for one
# platform. For each package outside this module it copies the license files
# from the package directory and from each parent directory up to the module
# root, into <outdir>/<module path>/. A module can hold more than one license
# file (for example LICENSE-3RD-PARTY.md), so every file with a license name
# is copied, not only LICENSE. The license of the Go distribution goes to
# <outdir>/go/, because the Go runtime is in every binary.
#
# The script fails if a module has no license file at its root. The --check
# mode collects into a temporary directory for each platform and keeps
# nothing: `make licenses` uses it as a gate.
set -euo pipefail

usage() {
  echo "usage: third-party-licenses.sh <goos> <goarch> <outdir>" >&2
  echo "       third-party-licenses.sh --check <goos>/<goarch>..." >&2
  exit 1
}

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The functions below match file names without regard to case. They use
# nocasematch and no child process: a process for each file made the check
# take minutes, because the linked packages hold thousands of files.

# copy_license_files <source dir> <destination dir>: copy each file with a
# license name. The module cache is read-only, so the copies get mode 0644.
copy_license_files() {
  local src="$1" dst="$2" file
  shopt -s nocasematch
  for file in "$src"/*; do
    [[ -f "$file" ]] || continue
    case "${file##*/}" in
      # Source code with a license name, for example notice_response.go.
      *.GO) ;;
      LICENSE* | LICENCE* | COPYING* | NOTICE* | PATENTS* | AUTHORS* | CONTRIBUTORS*)
        mkdir -p "$dst"
        install -m 0644 "$file" "$dst/"
        ;;
    esac
  done
  shopt -u nocasematch
}

# has_license <dir>: true if the directory holds a LICENSE, LICENCE or
# COPYING file.
has_license() {
  local file found=1
  shopt -s nocasematch
  for file in "$1"/*; do
    [[ -f "$file" ]] || continue
    case "${file##*/}" in
      LICENSE* | LICENCE* | COPYING*)
        found=0
        break
        ;;
    esac
  done
  shopt -u nocasematch

  return "$found"
}

collect() {
  local goos="$1" goarch="$2" out="$3"
  local module module_dir package_dir dir rel goroot go_license
  # The directories that are done, one on each line. Many packages share
  # their parent directories, so each directory is copied only one time.
  local seen=$'\n'

  rm -rf "$out"
  mkdir -p "$out"

  # One line for each linked package outside the standard library and outside
  # this module: module path, module directory, package directory.
  while IFS=$'\t' read -r module module_dir package_dir; do
    dir="$package_dir"
    while :; do
      if [[ "$seen" != *$'\n'"$dir"$'\n'* ]]; then
        rel="${dir#"$module_dir"}"
        copy_license_files "$dir" "$out/$module$rel"
        seen+="$dir"$'\n'
      fi
      [[ "$dir" == "$module_dir" ]] && break
      dir="$(dirname "$dir")"
    done
    if ! has_license "$out/$module"; then
      echo "third-party-licenses: module $module has no license file in $module_dir" >&2
      exit 1
    fi
  done < <(
    cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go list -deps \
      -f '{{if not .Standard}}{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Dir}}{{"\t"}}{{$.Dir}}{{end}}{{end}}{{end}}' \
      ./cmd/opendustpan
  )

  # The Go distribution. Homebrew keeps LICENSE one level above GOROOT.
  goroot="$(go env GOROOT)"
  go_license=""
  for dir in "$goroot" "$goroot/.."; do
    if [[ -f "$dir/LICENSE" ]]; then
      go_license="$dir/LICENSE"
      break
    fi
  done
  if [[ -z "$go_license" ]]; then
    echo "third-party-licenses: the Go distribution in $goroot has no LICENSE file" >&2
    exit 1
  fi
  mkdir -p "$out/go"
  install -m 0644 "$go_license" "$out/go/LICENSE"
  if [[ -f "$goroot/PATENTS" ]]; then
    install -m 0644 "$goroot/PATENTS" "$out/go/PATENTS"
  fi
}

if [[ "${1:-}" == "--check" ]]; then
  shift
  [[ $# -ge 1 ]] || usage
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  for platform in "$@"; do
    goos="${platform%/*}"
    goarch="${platform#*/}"
    collect "$goos" "$goarch" "$tmp/$goos-$goarch"
    count="$(find "$tmp/$goos-$goarch" -type f | wc -l | tr -d ' ')"
    echo "third-party-licenses: $platform: $count license files"
  done
  exit 0
fi

[[ $# -eq 3 && -n "$1" && -n "$2" && -n "$3" ]] || usage
collect "$1" "$2" "$3"
