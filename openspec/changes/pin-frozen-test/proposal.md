---
version: 1
---

Short form: a missing check. It covers #2726, which D2 rebuilds from this change.

## Problem

**Persona: maintainer.** `CLAUDE.md` says: "Until the first server tag, do
not change info.version in the API description or
`internal/api/apidesc/openapi.pin`. The release sets both. `make pin-frozen`
checks this." Main has no target `pin-frozen`, so nothing checks it. A pull
request that raises the version before the first tag passes every gate.

## Evidence

- `grep -n "pin-frozen" Makefile` finds no line. `CLAUDE.md:260` names the
  target.
- `internal/api/apidesc/document.go:20` holds `const DocumentVersion = "0.36.0"`,
  and its comment says: "Until the first release, it stays at this one value
  while the description changes, and the test of openapi.pin is disabled."
- `internal/api/apidesc/openapi.json` has info.version `0.36.0`.
- `internal/api/apidesc/openapi.pin` holds the line `version 0.36.0` and a
  `sha256` line. `internal/api/apidesc/version_test.go:33` names the file
  (`pinFile`).
- #2726 added a new tool, `cmd/pinfrozen`. ADR 364 decision 1 (owner) says:
  "This decision adds no custom analyzer and no new tool." A test in the
  existing package does the same work with no new tool.

## Fix boundary

- The NEW test `TestPinFrozen` in the NEW file
  `internal/api/apidesc/pin_frozen_test.go` is table-driven. It asserts that
  `DocumentVersion` is `0.36.0`, that info.version of the embedded
  description is `0.36.0`, and that the `version` line of openapi.pin is
  `0.36.0`. It reads only the files of its own package. It uses no git and
  takes no path input.
- The failure message names the one step that may change the values: the
  first tag. "The first release sets info.version and openapi.pin; until
  then they stay 0.36.0." No release-gate change on main names that step
  yet, so the test names the tag, and the release work removes the test in
  the same pull request that sets both values.
- The NEW target `pin-frozen` in the `Makefile` runs
  `go test ./internal/api/apidesc -run TestPinFrozen`, and `check` lists it.
- The `sha256` line of openapi.pin is not checked. It changes with the
  description.

## Check

`make pin-frozen` passes on main, and it fails when `DocumentVersion`,
info.version or the `version` line of the pin changes.

## ADR check

**ADR 364: Unexported fields and golangci-lint before added checks (owner)**
(owner-tagged, decision 1; Changed by: none).
- Quote: "This decision adds no custom analyzer and no new tool."
- Effect: the check is a test in the existing package and a Makefile
  target, not a new tool.

`CLAUDE.md:260` (quoted above) asks for the target.

## Open questions

none

Questions considered and answered:

- Does the test need git, to compare with the last tag? No. Before the first
  tag the values are fixed, so the test compares with the fixed value.
- Who changes the values? The first release, in the pull request that tags.
  That pull request removes this test.
