---
version: 1
---

Issue: #1312.

## Problem

Embedder builds a server binary with the SDK and lists each route in the API description so generated clients reach each handler.

## Evidence

- The handler-list guard exists on main. Proof: pkg/dustpan/apidesc_internal_test.go:279
- The adapters wiring exists on main. Proof: pkg/dustpan/dustpan.go:1108 (func prepareBackground)
- The neutralHandlers wiring exists on main. Proof: pkg/dustpan/dustpan.go:1216 (func newAdapters)
- The sink test limiter constructor exists on main. Proof: pkg/notify/sinks/limit.go:73 (func newTestLimiter)

## Fix boundary

- Match each field type that implements the handler interface in the guard, including pointer receivers, embedded fields, and interface-typed fields, with an exclusion list for fields that serve no route.
- Keep one profiles handler declaration in neutralHandlers and remove the adapters copy, with a startup nil check.
- Leave the shared sink test limit unchanged and out of scope until the outbox and dispatcher land.

Out of scope:

- Shared sink test limit until outbox and dispatcher land.
- Profile routes and profile behavior.
- Sink notification behavior.

## Check

- In `pkg/dustpan/apidesc_internal_test.go` add failing guard cases for a named handler type, a pointer-receiver handler type, an embedded field, and a negative case for an exclusion-listed non-route field. Check: TestEveryHandlerFieldIsListed fails on main for a wrapper handler type and for a pointer-receiver handler type without rows.
- In `pkg/dustpan/apidesc_internal_test.go` widen the guard to each field type that implements the handler interface with the exclusion list rule. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes.
- In `pkg/dustpan/dustpan.go` keep the profiles handler in neutralHandlers, remove the adapters copy, and fix wiring with a startup nil check. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes with one profiles field and startup fails on a nil profiles handler.
- Run the guard tests for the touched path, covering the existing route GET /api/dustpan/v1/openapi.json with an access token; no new route, so no new authorization action. Check: `go test ./pkg/dustpan -run TestEveryHandlerFieldIsListed -count=1` passes on the touched path.
- Complete the usability check for the unchanged read path of the API description. A person's step. Check: Persona reads the API reference docs page alone with no surprise; path costs no extra step. Confirmed by: docs page read-through.
- Run `make check` with zero findings. Check: `make check` passes with zero findings. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main guards that each handler field has a row in the API description. (`pkg/dustpan/apidesc_internal_test.go:279 (func TestEveryHandlerFieldIsListed)`)
- Main declares the adapters wiring. (`pkg/dustpan/dustpan.go:1108 (func prepareBackground)`)
- Main declares the neutralHandlers wiring. (`pkg/dustpan/dustpan.go:1216 (func newAdapters)`)
- Main has a sink test limiter constructor. (`pkg/notify/sinks/limit.go:73 (func newTestLimiter)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- How to keep each handler listed in the API description? The pack names the guard at pkg/dustpan/apidesc_internal_test.go:279. No ADR is listed in the pack, so the fix widens the match to each field type that implements the handler interface, counting pointer receivers, embedded fields, and interface-typed fields.
- Where to declare the profiles handler? The pack names adapters at pkg/dustpan/dustpan.go:1108 and neutralHandlers at pkg/dustpan/dustpan.go:1216. No ADR is listed in the pack, so the fix keeps the neutralHandlers copy and removes the adapters copy with a startup nil check.
- Does this change alter sink test limit scope? The pack names newTestLimiter at pkg/notify/sinks/limit.go:73 and SharedLimits at pkg/store/storetest/shared_limits.go:185. The issue text waits for outbox and dispatcher work, so this change leaves limit scope unchanged and out of scope.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
