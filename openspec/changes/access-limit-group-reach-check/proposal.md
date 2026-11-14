---
version: 1
---

Issue: #2980.

## Problem

A namespace administrator saves access limits to bound protected groups. The check runs only in the HTTP handler today, so a later save path that skips that handler would skip the check and widen a group.

## Evidence

- The HTTP handler serves PUT access limits through writeAccessLimit. Proof: internal/api/server/gate_objects.go:517 (func Handler.writeAccessLimit)
- ConfigService owns access-limit writes through PutAccessLimit. Proof: pkg/gate/config_objects.go:182 (func ConfigService.PutAccessLimit)
- The handler holds a limitGroupReach helper. Proof: internal/api/server/gate_objects.go:588 (func Handler.limitGroupReach)
- The access service exposes CheckGroupReach for group reach decisions. Proof: pkg/access/service.go:811 (func Service.CheckGroupReach)

## Fix boundary

- ConfigService.PutAccessLimit runs the group reach check on every save.
- Handler.writeAccessLimit keeps limitGroupReach as defense in depth with a comment.
- Decision errors fail closed and audit an internal error reason, not a caller refusal.

Out of scope:

- New access-limit routes or actions.
- Change of the reach rule itself.
- Delete access-limit paths, which remove a limit and run no reach check.
- New replay machinery; a later replay path calls the same PutAccessLimit check.

## Check

- Add hostile-row service tests for PutAccessLimit group reach refusal in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitRefusesHostileGroupLimit fails on main.
- Add service test for decision error failing closed with the NEW reason access-limit.decision-error in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitDecisionErrorFailsClosed fails on main.
- Add service test for the allowed and narrowing paths where the saver holds the reach in NEW file pkg/gate/configreach_test.go. No route: the test calls the service only. Check: TestPutAccessLimitStoresAllowedGroupLimit fails on main.
- Move the check into PutAccessLimit with fail-closed error handling in pkg/gate/config_objects.go:182 (func ConfigService.PutAccessLimit). Check: TestPutAccessLimitRefusesHostileGroupLimit passes with the service check active.
- Keep the handler reach helper as defense in depth with a comment in internal/api/server/gate_objects.go:588 (func Handler.limitGroupReach) that names the service check as authoritative. Check: Hostile save through a direct service call that bypasses the handler in pkg/gate/config_objects.go:182 is refused.
- Audit decision errors with the NEW reason access-limit.decision-error in internal/api/server/gate_objects.go:517 (func Handler.writeAccessLimit) using the single service audit helper. Check: Hostile save and forced reach error through PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id} with an access token show 403 and 500, with the NEW reason access-limit.decision-error for decision errors.
- Wire the service check through the existing PUT access-limit routes in internal/api/server/gate_objects.go:517 with their authorization actions. Check: PUT /api/dustpan/v1/namespaces/{namespace}/access-limits/{id} with an access token refuses a hostile group limit.
- Update the docs page for access limits in docs/reference/. The page states the service check and both audit reasons. Check: Docs page for access limits states the service check and the audit reasons.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- ConfigService writes access limits through PutAccessLimit. (`pkg/gate/config_objects.go:182 (func ConfigService.PutAccessLimit)`)
- The HTTP handler serves PUT access limits through writeAccessLimit. (`internal/api/server/gate_objects.go:517 (func Handler.writeAccessLimit)`)
- The handler checks group reach through limitGroupReach. (`internal/api/server/gate_objects.go:588 (func Handler.limitGroupReach)`)
- The access service computes group reach through CheckGroupReach. (`pkg/access/service.go:811 (func Service.CheckGroupReach)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Where does the group reach check belong and does it wait for the structural layer? The service owns the refusal so every save path runs it. The handler keeps its helper as defense in depth. The owner decides whether a later structural authorization layer subsumes both. This change does not wait for that layer and does not change its scope.
- How do the two checks avoid two reach decisions that disagree and two audit entries? Only the service audit helper writes the entry. A handler early refusal calls the same helper and skips the service call. A service refusal or decision error is mapped by the handler to 403 or 500 with no second entry. One save writes one entry.
- Is the check atomic with the write? Main shows PutAccessLimit at pkg/gate/config_objects.go:182 and writeAccessLimit at internal/api/server/gate_objects.go:517. The service runs the CheckGroupReach decision immediately before the store in the same call. There is no fenced transaction that makes the decision and the store atomic, so a concurrent grant change between decision and store can leave a stale allow. The next evaluation fails closed on the stored limit.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
