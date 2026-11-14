---
version: 1
---

Short form: a missing check. It covers #2727, which D2 builds from this change.

## Problem

**Persona: maintainer.** `CLAUDE.md:363` says: "Add files by name. Never use
`git add -A`. `make files-check` refuses large and binary files." Main has no
target `files-check`. A pull request that commits a build output, a large
fixture or a binary passes every gate, and the history keeps it for ever.

## Evidence

- `grep -n "files-check" Makefile` finds no line. `CLAUDE.md:363` names the
  target.
- `ls scripts` lists no file check. The scripts of main follow one form, for
  example `scripts/check-nolint.sh` and `scripts/check-release-record.sh`.

## Fix boundary

- The NEW script `scripts/check-files.sh` lists the files that git tracks
  (`git ls-files -z`). It refuses a file larger than 1 MiB and a file that
  holds a NUL byte in its first 8000 bytes (a binary file), unless the file
  is on the allowlist. It prints each refused file with its size or the word
  binary, and one line that names the fix: add the file to the allowlist with
  a reason, or remove it. Any error of git, or a path that it cannot read,
  fails the check (fail closed).
- The NEW file `.large-files-allowlist` holds one path on each line, then a
  space, `#`, and the reason. A line with no reason, a path that starts with
  `/` or `-`, or a path with a `..` part is refused. Paths are matched as
  exact strings, never as patterns.
- The NEW target `files-check` in the `Makefile` runs the script, and `check`
  lists it.
- The NEW table test `TestCheckFiles` in the NEW file
  `scripts/checkfiles/checkfiles_test.go` (with a NEW `scripts/checkfiles/leak_test.go`)
  runs the script on a temporary git repository for each case: a large file,
  a binary file, an allowlisted file, a file name with spaces and one with a
  leading dash, an allowlist line that tries `../`, and a git error (a
  directory that is not a repository), which must fail.

## Check

`make files-check` passes on main, and the table test passes.

## ADR check

**ADR 364: Unexported fields and golangci-lint before added checks (owner)**
(owner-tagged, decision 1; Changed by: none).
- Quote: "The guard against a bug class is Go visibility first. ... This
  decision adds no custom analyzer and no new tool."
- Effect: ADR 364 decision 1 is about guards against bug classes in the Go
  code. This check guards the content of the repository (sizes and binary
  files), which no Go visibility or golangci-lint rule can express. It is a
  shell script and a Makefile target that `CLAUDE.md:363` already names, not
  a code analyzer.

## Open questions

none

Questions considered and answered:

- What is large? More than 1 MiB. No source file of main is near that size,
  and a fixture above it gets an allowlist line with its reason.
- How does the script read names safely? With `git ls-files -z` and a read
  loop on NUL, so that a space or a leading dash in a name is data, never an
  option.
