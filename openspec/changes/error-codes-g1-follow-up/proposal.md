---
version: 1
---

Issue: #1744.

## Problem

Problem: answers of the Pulumi Cloud-compatible API name a stack, an update, a state or a limit in text but carry no arguments, so clients cannot select text from the code. Typed errors in pkg/stack let the adapter read each named value from a field.

## Evidence

- The change touches the reads and writes of pkg/stack. Proof: pkg/stack/state_at.go, pkg/stack/state.go, pkg/stack/reveal.go and pkg/stack/update.go exist in the pack paths.
- The delete refusal of the SQLite store is a path that the issue says wraps only the update-conflict sentinel. Proof: pkg/store/sqlite/lifecycle.go exists in the pack paths and the issue text names deleteRefusal there.
- The journal batch helper exists on main. Proof: pkg/stack/journal_entries.go:472 (func BatchOverLimit)

## Fix boundary

- Add NotFoundError, AlreadyExistsError, NoStateError, a Ref on NoStateAtError, ResourceNotFoundError, UpdateNotFoundError, LeaseExpiredError and WrongUpdateStateError in pkg/stack, each with Is kept.
- Fill UpdateConflictError.Active in both store dialects and in the delete refusal paths, after the store checks the row value as an identifier.
- Declare and send the arguments namespace, project, stack, update, state, expiredAt, size, limit, bound and path for the G1 codes.
- Carry the URN as path on the URN read path when it passes the identifier check, else server.internal. The delta preview refusal carries state preview.

Out of scope:

- Later added arguments beyond the named values
- TFE-compatible and tool-neutral API codes
- Changes to the journal batch limit value

## Check

- Write failing tests for the Ref fields of NotFoundError and AlreadyExistsError in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run TestNotFoundArgs fails on main
- Write failing tests for the update, state, lease and batch fields in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run TestUpdateErrorArgs fails on main
- Write failing tests for the reveal path argument and for the innermost precedence in NEW pkg/stack/errors_args_test.go. Check: go test ./pkg/stack -run 'TestRevealPathArgs|TestNestedPrecedence' fails on main
- Write failing store tests for both dialects in pkg/store/storetest. Check: go test ./pkg/store/storetest -run TestStoreErrorArgs fails on main
- Implement the typed errors in pkg/stack and read them in internal/api/pulumi with errcode.New. Check: go test ./pkg/stack ./internal/errcode passes
- Return UpdateConflictError from the delete refusal in pkg/store/sqlite/lifecycle.go and its PostgreSQL copy, with Active checked. Check: go test ./pkg/store/storetest -run TestDeleteRefusalArgs passes
- Update the frozen lists and the API description for the added arguments in internal/errcode/testdata/catalog. Confirmed by: CI (make check) Check: the errcode shipped-list hash test passes
- Check stack, rename, delete, update and reveal flows with the real Pulumi CLI with an access token — BLOCKED by <Pulumi route-to-method map not in pack>. Confirmed by: CI (make e2e) Check: `make e2e` passes

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Service.GetStack reads a stack by reference. (`pkg/stack/stack.go:450 (func Service.GetStack)`)
- Updates.ExportAt reads state at a time. (`pkg/stack/state_at.go:22 (func Updates.ExportAt)`)
- errcode.New builds an error answer. (`internal/errcode/answer.go:102 (func New)`)
- The update-conflict sentinel exists. (`pkg/stack/update.go:24`)
- The update not-found sentinel exists. (`pkg/stack/update.go:22`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 276: Every error answer has a code and its arguments** (owner-tagged: decision 3, 10).

- Decisions that apply: 3 (current), 10 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "An answer carries the values that its text names, as arguments."
- Quote: "A code never changes its meaning."
- Effect: Typed errors carry each named value so each answer carries its declared arguments, and each addition keeps its sentinel and meaning.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Must each value named by the error text become an argument? ADR 276 decision 3: yes, the values that the text names become declared arguments.
- Can a code gain arguments after it entered the catalog? ADR 276 decision 10: a code never changes meaning, and an added argument is an addition.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
