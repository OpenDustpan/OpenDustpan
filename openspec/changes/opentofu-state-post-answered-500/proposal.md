---
version: 1
---

Issue: #2200.

## Problem

An operator runs OpenTofu against two server instances and hits twelve 500 answers on state POST in 0.3 s. The error log names no cause and no request id, so the operator cannot tell if PostgreSQL, the blob store, or the server failed.

## Evidence

- Twelve POST state answers returned 500 in 0.3 s, six on each instance, with a 15 byte body. Proof: Issue #2200: 02:27:29.541 to 02:27:29.764 UTC, 12 requests POST /tofu/state/{namespace}/{workspace} answered status=500, 6 on each instance, each 272 to 334 ms, 15 bytes.
- Each failed request logged an ERROR line with no cause and no request id plus an INFO request line with the request id. Proof: Issue #2200: level=ERROR msg="request failed" status=500 with no err, error, cause, or request ID; level=INFO msg=request ... status=500 with request_id.
- The event did not reproduce in three later runs. Proof: Issue #2200 runs A, B, C: 3318, 4462 and 4073 writes with 0 answers of 500.
- The state write and lock files exist on main under internal/api/tofuhttp. Proof: The pack evidence list for the keyword UNLOCK shows internal/api/tofuhttp/locks.go:41 and internal/api/tofuhttp/state_write.go:163.

## Fix boundary

- Each 500 answer of the OpenTofu state POST path adds the redacted wrapped cause and the request id to the one problem-path fault-log line, with no second line and the response body unchanged.

Out of scope:

- Fix of the unknown shared cause itself (database, blob store, or schedule)
- New retry or mapping of serialization and lock timeout errors to 409 or 503
- Shared middleware logging fix for all routes including the corrupt-manifest case
- Change to LOCK and UNLOCK routes, which answered 200 in the event
- Change to the 500 response body shape

## Check

- Write the failing test in NEW file `internal/api/tofuhttp/state_write_logging_test.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. It forces the 500 with a fault-injecting workspace store and checks one problem-path line with cause and request id. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: NEW test TestStateWrite500LogNamesCauseAndRequestID fails on main, then passes after the fix.
- Write the failing redaction test in NEW file `internal/api/tofuhttp/state_write_logging_test.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. It checks redaction of driver detail and the 500 character cut. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: NEW test TestStateWrite500LogRedactsAndCutsText fails on main, then passes after the fix.
- Edit file `internal/api/tofuhttp/state_write.go` for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token: add the redacted wrapped cause and the request id to the one problem-path fault-log line for the state POST, with no second line and no body change. Check: NEW file internal/api/tofuhttp/state_write_logging_test.go: TestStateWrite500LogNamesCauseAndRequestID passes.
- Run `make check` with zero findings for existing route `POST /tofu/state/{namespace}/{workspace}` with an access token. No docs page changes, because no user-visible API changes. Confirmed by: CI (make check). Check: `make check` passes with zero findings, plus existing route `POST /tofu/state/{namespace}/{workspace}` called with an access token in the tests above passes.

Depends on:

- writer-gate-problem-writejson-variable
- `writer-gate-problem-writejson-variable` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A restore failure log test already checks that a failure log keeps the cause without driver detail. (`pkg/backup/restore_service_test.go:1222 (func TestRestoreFailureLogsCauseWithoutDriverDetail)`)
- The test harness can seed OpenTofu state for e2e and server tests. (`e2e/harness.go:1203 (func Env.SeedTofuState)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Which ADR governs the 500 log line for OpenTofu state writes? The pack names no ADR for error logging or for OpenTofu state writes, so no ADR answers it. The change follows the redacted-cause log pattern on main in pkg/backup/restore_service_test.go:1222.
- Is the fix scoped to the state POST path or to shared middleware? Only the state POST path. LOCK and UNLOCK answered 200 in the event. The corrupt-manifest case in #1714 and the probe in #2195 stay out of scope as separate work.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
