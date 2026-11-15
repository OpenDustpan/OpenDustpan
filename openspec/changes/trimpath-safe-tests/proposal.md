---
version: 1
---

Short form: a test bug. It covers #3101.

## Problem

**Persona: maintainer.** Four test helpers find their source directory with
`runtime.Caller(0)`. With `-trimpath`, `runtime.Caller` returns a
module-relative path, so these tests cannot find their files. Without
`-trimpath`, each extra checkout or worktree adds a full copy of the build to
the Go build cache: #3101 measured 103 MB for each extra worktree without
`-trimpath` and 1 MB with it, and one machine filled an 18 GB build cache.

## Evidence

- `e2e/e2e_test.go:10115` (`replayScriptPath`) calls `runtime.Caller(0)` at
  :10118 and joins `../scripts/import-tfe-state.sh`. Callers: :10137 and :10201.
- `internal/zonegen/zonegen_test.go:17` (`committedPath`) calls
  `runtime.Caller(0)` at :20 and joins `../../pkg/preferences/zones_gen.go`.
  Caller: `TestCommittedListMatchesToolchain` (:56).
- `internal/cli/reference_test.go:20` (`repoRoot`) calls `runtime.Caller(0)`
  at :23. Callers: `TestConfigurationReference` (:37) and
  `internal/cli/settings_test.go:36`.
- `internal/pulumijournal/internal/gen/gen_test.go:22` (`testDir`) calls
  `runtime.Caller(0)` at :24. Callers: :34 (`testdata/journal.go`) and :44,
  used by `TestGenerateMatchesCommittedFile` (:48).
- `e2e/compose_test.go:297` (`repoRoot`) already finds the root from
  `os.Getwd()`: `go test` runs each test in its package directory.

## Fix boundary

- Each of the four helpers builds its path from the package directory, the
  working directory of `go test`, with a path relative to `.`, and no longer
  calls `runtime.Caller`. The helpers keep their names and their callers.
- `e2e/e2e_test.go` `replayScriptPath` joins `../scripts/import-tfe-state.sh`
  to the working directory of the test from `os.Getwd()`, and returns that
  absolute path: the e2e steps run the script from other directories, so a
  relative path fails there (PM decision, 15 Nov 2026).
- `internal/zonegen/zonegen_test.go` `committedPath` uses
  `filepath.Join("..", "..", "pkg", "preferences", "zones_gen.go")`.
- `internal/cli/reference_test.go` `repoRoot` returns `filepath.Join("..", "..")`.
- `internal/pulumijournal/internal/gen/gen_test.go` `testDir` returns `"."`, and the
  path at :44 uses `filepath.Join(testDir(t), "..", "..")`, because
  `filepath.Dir(".")` is `"."`.
- No other test file, no Makefile target and no CI flag changes.

## Check

`GOFLAGS=-trimpath go test ./internal/zonegen/... ./internal/cli/... ./internal/pulumijournal/...`
passes, and `GOFLAGS=-trimpath go vet ./e2e/...` builds the e2e package.

## ADR check

No ADR decides how a test finds its files. `rg -n "trimpath|runtime.Caller" docs/adr docs/rfc`
finds only the release build flags of ADR 139 and ADR 177, no decision on
how a test finds its files. The change edits test files only.

## Open questions

none

Questions considered and answered:

- Does `go test` run each test in its package directory? Yes; the e2e
  `repoRoot` (`e2e/compose_test.go:297`) relies on it already.
- Does CI turn on `-trimpath`? Not in this change. The fix makes the tests
  pass with it; a flag change is a separate change.
