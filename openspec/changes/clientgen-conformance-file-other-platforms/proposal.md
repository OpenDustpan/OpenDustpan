---
version: 1
---

Issue: #2931.

## Problem

A file exists only for platforms the project does not support, and the same wait loop is copied per OS and can drift.

## Evidence

- A file for other platforms exists in the conformance package. Proof: paths: internal/clientgen/conformance/process_group_other.go exists: true
- endGroupAfterExit is defined in the Linux and Darwin files. Proof: rg 'endGroupAfterExit': internal/clientgen/conformance/process_linux.go:25:func endGroupAfterExit(a *ProcessAdapter) {
- The other-platform file defines the fallback. Proof: rg 'endGroupAfterExit': internal/clientgen/conformance/process_group_other.go:5:// endGroupAfterExit does nothing on this platform. Only linux and darwin wait
- The Linux file holds waitid constants. Proof: rg 'waitid': internal/clientgen/conformance/process_linux.go:14: waitidPid = 1

## Fix boundary

- Delete internal/clientgen/conformance/process_group_other.go
- Move endGroupAfterExit to one shared file with no build tag and a portable implementation
- Remove the per-OS copies and unused waitid constants in process_linux.go and process_darwin.go

Out of scope:

- Support for Windows or other platforms
- Changes to the wait logic itself

## Check

- Add a regression test in the NEW file internal/clientgen/conformance/process_tags_test.go that refuses process_group_other.go, refuses any file with a negated build tag, and refuses more than one definition of endGroupAfterExit Check: NEW test TestNoOtherPlatformProcessFile in internal/clientgen/conformance fails on main and passes after the fix. No route: the test reads package files only.
- Delete process_group_other.go, move endGroupAfterExit to the NEW file process_group.go with no build tag, and remove the per-OS copies and unused waitid constants in process_linux.go and process_darwin.go Check: go test ./internal/clientgen/conformance/ passes on Linux and macOS. No route: the test runs the package suite only.
- Run the full check with zero findings. Confirmed by: CI (make check). Check: make check reports zero findings. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main already has endGroupAfterExit for the Linux build. (`internal/clientgen/conformance/process_linux.go:25 (func endGroupAfterExit)`)
- Main already has endGroupAfterExit for the Darwin build. (`internal/clientgen/conformance/process_darwin.go:25 (func endGroupAfterExit)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 278: Supported platforms are Linux and macOS** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "It needs no build tag. Code for the platform does not sit behind a build tag of its own."
- Quote: "The project does not accept a platform-specific patch, for example code in a file that only one other platform builds. (owner)"
- Effect: The change deletes the file that only another platform builds and leaves no per-OS copy. The one shared file has no build tag and uses only the standard library with no waitid constants, so it adds no tag of its own and builds on every platform.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Keep a fallback file for other platforms? No. ADR 278 decisions 3 and 4 forbid a file that only another platform builds.
- Support Windows or other platforms in this change? No. ADR 278 decisions 1 and 2 name Linux and macOS as supported and other platforms as not supported.
- Use one shared file with no build tag instead of a unix tag? Yes. A portable implementation with only the standard library and no waitid constants builds everywhere with no tag, so no unix tag and no per-OS split stays.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
