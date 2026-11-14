---
version: 1
---

Issue: #1430.

## Problem

The client conformance harness caches a failed seed write for the life of the process and never clears per-URL seed state, so a later run returns a stale error or skips seeding on a reused test port.

## Evidence

- The fallback seed state is keyed per server address with a mutex in the conformance fixtures file. Proof: internal/clientgen/conformance/fixtures.go:130:	fallbackSeeds   = map[string]*seedState{}
- ensureProfile writes the seed profile with the harness in the conformance fixtures file. Proof: internal/clientgen/conformance/fixtures.go:281 (func ensureProfile)
- writeSeed makes the seed profile in the conformance fixtures file. Proof: internal/clientgen/conformance/fixtures.go:308 (func writeSeed)

## Fix boundary

- Do not cache a failed fallback seed write so the next call retries.
- Scope fallback seed state to the server instance and clear it when the server closes so a reused port reseeds.
- Add one regression test per case.

Out of scope:

- Server behavior, API contracts, and profile routes

## Check

- Add TestRetryAfterFailedSeedWrite in internal/clientgen/conformance/limits_test.go for a failed seed write that retries after recovery (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestRetryAfterFailedSeedWrite)) Check: go test -race ./internal/clientgen/conformance/ -run TestRetryAfterFailedSeedWrite
- Add TestReseedOnReusedAddress in internal/clientgen/conformance/limits_test.go for a new server on a reused address that reseeds (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestReseedOnReusedAddress)) Check: go test -race ./internal/clientgen/conformance/ -run TestReseedOnReusedAddress
- Add TestSuccessSeededOncePerInstance in internal/clientgen/conformance/limits_test.go for one post per live server instance (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestSuccessSeededOncePerInstance)) Check: go test -race ./internal/clientgen/conformance/ -run TestSuccessSeededOncePerInstance
- Add TestConcurrentSeedSharesInFlight in internal/clientgen/conformance/limits_test.go for shared in-flight write, joint retry, conflict success, and close during write (Confirmed by: CI (go test -race ./internal/clientgen/conformance/ -run TestConcurrentSeedSharesInFlight)) Check: go test -race ./internal/clientgen/conformance/ -run TestConcurrentSeedSharesInFlight
- Change the fallback seed cache in internal/clientgen/conformance/fixtures.go to key by server instance, skip storage of failed writes, and delete the entry on close (Confirmed by: CI (go test -race ./internal/clientgen/conformance/)) Check: go test -race ./internal/clientgen/conformance/
- Run the full check for internal/clientgen/conformance/fixtures.go and the new tests (Confirmed by: CI (`make check`)) Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ensureProfile writes the seed profile with the harness for cases that need it (`internal/clientgen/conformance/fixtures.go:281 (func ensureProfile)`)
- writeSeed makes the seed profile and a conflict means another writer made it (`internal/clientgen/conformance/fixtures.go:308 (func writeSeed)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does the fix change server behavior or any API contract? No. The issue states both findings affect the test harness only in internal/clientgen/conformance/fixtures.go.
- How to fix the stale cached error and the stale URL-keyed state? Do not store a failed write result so the next call retries, and key the entry by server instance with delete on close.
- Does the success path stay cached and how do concurrent callers behave? The successful seed stays cached once per live instance. Concurrent callers on one instance share one in-flight write and retry together on failure.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
