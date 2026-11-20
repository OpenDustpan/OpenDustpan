## 1. Test first

- [x] 1.1 Write the table test TestCheckFiles in the NEW file `internal/filescheck/filescheck_test.go`, with a NEW `internal/filescheck/leak_test.go`. Each row makes a temporary git repository and runs `scripts/check-files.sh` in it. Rows: a file of 1 MiB plus one byte (refused), a file with a NUL byte (refused as binary), the same files on `.large-files-allowlist` with a reason (pass), a file named `a b.txt` and one named `-x.txt` (handled as names), an allowlist line `../x reason` (refused), an allowlist line with no reason (refused), and a directory that is not a git repository (fails closed). Check: the test fails on main, because the script does not exist.

## 2. Build

- [x] 2.1 Write the NEW script `scripts/check-files.sh` (bash, `set -euo pipefail`, `git diff --name-only -z` against the merge base, read on NUL, an exact-string allowlist match). Check: `go test ./internal/filescheck/... -count=1` passes, and `shellcheck` reports nothing when it is installed.
- [x] 2.2 Add the NEW file `.large-files-allowlist`, with each large or binary file that main tracks today and its reason. Check: `scripts/check-files.sh` exits 0 on main.
- [x] 2.3 Add the NEW target `files-check` to the `Makefile`, and add it to the target `check`. Check: `make files-check` exits 0.

## 3. Definition of done

- [x] 3.1 Run the local checks (`Makefile`). Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
