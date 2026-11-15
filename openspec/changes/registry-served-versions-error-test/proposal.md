---
version: 1
---

Issue: #3171.

Short form: tech debt. Owner lane: F4 (it drives #2849).

Waits for: #2849

## Problem

**Persona: provider author.** A provider author lists the versions of a
provider and reads one version, and publishes again with the same
`Idempotency-Key` after a lost answer. Two gaps from the review of #2849:

1. No test covers the read-error path of `servedVersions`. A change that
   drops that error would answer a list or a version from incomplete data
   and fail no test.
2. The API reference does not say what a replayed publish answer holds. A
   replay answers the stored answer of the first publish, so it describes the
   release as it was then, not as it is now.

## Evidence

- On the head of #2849 (`fix/2705-review-followups`),
  `internal/api/registry/handlers.go:402` (func Handler.servedVersions) maps
  `registry.ErrNotFound` to an empty set and returns any other error of
  `ServableReleases`. Its callers are `listReleases` (:433) and `getRelease`
  (:459). No test in `internal/api/registry/registry_test.go` makes
  `ServableReleases` fail.
- `docs/reference/registry-api.md` lists the publish route
  `PUT /providers/{name}/versions/{version}` as taking `Idempotency-Key`
  (:18) and has a section "Publish" (:58), with no word on a replayed answer.

## Fix boundary

- One test makes the read of `servedVersions` fail through each caller, and
  checks that the route answers the error (500 with the generic text) and no
  partial list or version. A run with the error path removed fails.
- The section "Publish" of the API reference says that a replayed answer
  describes the release at the time of the first publish.
- Out of scope: any change of the replay behavior.

## Check

`go test ./internal/api/registry/ -run TestServedVersionsReadError -count=1`
passes, `make docs-lint` passes, and `make check` reports zero findings
(Confirmed by: CI).

## ADR check

No ADR is touched. The replay behavior is that of the `Idempotency-Key`
header, which this change documents and does not change. Conflict: none.

## Open questions

none. Considered: answer the current release on a replay (refused: it changes
the replay behavior, which is out of scope for a tech-debt change).
