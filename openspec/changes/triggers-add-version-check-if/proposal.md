---
version: 1
---

Issue: #1386.

## Problem

Two people who replace one trigger at the same time overwrite each other without a notice, and the lost update also moves the last saver to a caller who never saw the other rule.

## Evidence

- PUT on a trigger replaces the whole rule today with last-write-wins. Proof: Issue 1386 body and main fact internal/api/triggers/serve.go:202 (func Handler.replaceTrigger).
- Shared ETag and If-Match helpers exist on main. Proof: internal/api/apidesc/collect.go:120 (func Label.WithETag) and internal/api/apidesc/collect.go:105 (func Label.WithIfMatch).
- Trigger read and replace routes exist on main. Proof: Route DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/units/{}/triggers/{} in the pack main facts.

## Fix boundary

- Add a version to the trigger record and the store port, with conformance cases first.
- Answer ETag on the trigger read route and take If-Match on PUT, with 412 for a stale version.
- Update the API description, the header-flag test and the reference page.

Out of scope:

- Change to fire, create, list or delete trigger behavior.
- Change to other units or other tool-neutral APIs.

## Check

- Add store-port conformance cases in `pkg/trigger/triggertest/store.go:562` (func caseLastSaver) for the version compare on replace: stale, concurrent same ETag, replace after delete, and baseline row at version 1. Check: TestTriggerReplaceStaleVersionFails fails on main.
- Add handler tests in the NEW file `internal/api/triggers/serve_test.go` for ETag on the single read, the version member on the list, If-Match on PUT, and 412 for a stale version. Check: TestGetTriggerAnswersETag and TestReplaceTriggerNeedsIfMatch fail on main.
- Wire the version through the record, the store and Handler.replaceTrigger in `internal/api/triggers/serve.go:202` (func Handler.replaceTrigger), add the three NEW codes in `internal/errcode/catalog_trigger.go:7`, use WithETag in `internal/api/apidesc/collect.go:120` and WithIfMatch in `internal/api/apidesc/collect.go:105` in the API description, update the header-flag test and the trigger reference page; this route is the operation that unblocks the 428 conformance case missing-required-if-match-gives-428. Check: PUT with the fresh ETag returns 200 with a new ETag; the old ETag returns 412; no If-Match returns 428.
- Run an e2e step against the real server on PUT /api/dustpan/v1/namespaces/{}/units/{}/triggers/{} with an access token for the read ETag and the stale PUT, Confirmed by: CI (make e2e) Check: E2E replaceTrigger with fresh and stale ETag on the real server.
- Check the docs page and the usability of the path, then run the full checks, A person's step. Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Handler serves the trigger replace. (`internal/api/triggers/serve.go:202 (func Handler.replaceTrigger)`)
- A shared label helper adds ETag to the API description. (`internal/api/apidesc/collect.go:120 (func Label.WithETag)`)
- A shared label helper adds If-Match to the API description. (`internal/api/apidesc/collect.go:105 (func Label.WithIfMatch)`)
- The trigger error codes exist on main. (`internal/errcode/catalog_trigger.go:7`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Should the replace use ETag and If-Match or a body version field? Use ETag and If-Match: the issue scope names them and the pack shows WithETag and WithIfMatch as the shared helpers.
- Does the store port need a version before the HTTP change? Yes: the issue asks for a version on the record and the store port with conformance cases first.
- What status does a missing or malformed If-Match get? Missing gets 428. Malformed, weak, wildcard or multi-value gets 400.
- In what order are the checks answered? 403, then 404, then 428 and 400, then 412.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
