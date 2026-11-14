---
version: 1
---

Issue: #1296, #1298.

## Problem

A namespace administrator connects a chat webhook and must rotate its signing key without breaking deliveries and without losing the audit record of each change.

## Evidence

- Sink routes exist on main for list, read, change, delete, and test Proof: GET/POST /api/dustpan/v1/namespaces/{}/sinks, DELETE/GET/PUT /api/dustpan/v1/namespaces/{}/sinks/{} and POST /api/dustpan/v1/namespaces/{}/sinks/{}/test in the pack main list
- Sink error codes exist on main Proof: sink.invalid in internal/errcode/catalog_sink.go:15, sink.not-found in internal/errcode/catalog_sink.go:21, sink.conflict in internal/errcode/catalog_sink.go:9
- No sink rotate route exists on main Proof: The pack route list names POST /api/dustpan/v1/namespaces/{}/issuer/keys/rotate but no sinks rotate route
- The issues ask for a rotate route with two active keys, a decision on installation scope, and a local e2e receiver Proof: Issues #1296 and #1298 in the pack

## Fix boundary

- NEW rotate route for webhook sinks with a server-made second secret and two active keys during rotation
- NEW confirm route that retires the named old signing secret ref
- Continued refusal of installation-scope (@server) chat and webhook sinks
- Local e2e receiver permitted by the operator allowlist with end-to-end signature checks

Out of scope:

- Chat sender B (#1173) and inbound Slack app (#1178)
- Person-supplied write-only signingKey decision of RFC #1298
- Installation-scope sinks beyond continued refusal

## Check

- Write a failing service test in the NEW file pkg/notify/rotate_test.go for NEW rotate: two active refs, secret answered once Check: TestSinkRotateKeepsTwoKeys fails on main
- Write a failing test in the NEW file pkg/notify/rotate_test.go for NEW confirm: one ref after confirm, unknown ref refused Check: TestSinkRotateConfirmRetiresOldRef fails on main
- Write failing guard tests in the NEW file pkg/notify/rotate_test.go for stale If-Match, concurrent rotate and Idempotency-Key replay Check: TestSinkRotateGuards fails on main
- Wire the NEW rotate and confirm routes with namespace:manage-notifications in the NEW file internal/api/notify/sinks_rotate.go; reuse sink.conflict and sink.invalid from internal/errcode/catalog_sink.go:15 Check: POST /api/dustpan/v1/namespaces/{namespace}/sinks/{id}/rotate with an access token returns the new key id
- Add the NEW rotate and confirm operations to the NEW file internal/api/apidesc/openapi.sinks.json and regenerate the clients Check: make clients-check passes and the description lists both NEW routes
- Add the e2e step in the NEW file e2e/chat_sinks_rotation_test.go against a local receiver that the operator allowlist permits, using the existing test route Check: POST /api/dustpan/v1/namespaces/{namespace}/sinks/{id}/test with an access token shows both signatures verify on the local receiver
- Write the docs page in the NEW file docs/guides/chat-sinks-rotation.md for rotation and the local receiver setup with the receiver key-id move Check: A test runs each command of the page as written
- Run the route policy check, hostile tests, usability check from the docs page, and make check Check: make check reports zero findings

Depends on:

- egress-allowlist-private-hosts-pkg
- `egress-allowlist-private-hosts-pkg` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The webhook sink sender refuses a blocked URL (`pkg/logsink/webhook/webhook_test.go:52 (func TestWebhookSinkRefusesBlockedURL)`)
- The chat sender retries on a temporary status (`pkg/notify/chat/sender_test.go:439 (func TestSendRetryAfterOnTemporaryStatus)`)
- Rotate test for access tokens exists (`pkg/accesstoken/accesstoken_test.go:584 (func TestRotate)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 145: One notification model with sinks for email, chat and signed webhooks** (not owner-tagged).

- Decisions that apply: 13 (changed), 21 (current), 22 (current), 27 (extended).
- Changed by: ADR 146 extends decision 5; ADR 149 changes decisions 15 and 24; ADR 149 extends decisions 3, 13 and 27; ADR 197 extends decision 5; ADR 203 extends decision 5; ADR 204 extends decision 5; ADR 210 changes; ADR 234 extends decisions 1 and 24; ADR 241 extends decision 5; ADR 248 changes decision 9; ADR 262 extends; ADR 315 extends; ADR 334 changes decisions 13 and 20. Chain to the end: ADR 146, ADR 149, ADR 197, ADR 203, ADR 204, ADR 210, ADR 234, ADR 241, ADR 248, ADR 262, ADR 315, ADR 334, ADR 227, ADR 297, ADR 222, ADR 229, ADR 230, ADR 225, ADR 286, ADR 300, ADR 353, ADR 247, ADR 313, ADR 343, ADR 301, ADR 320, ADR 339, ADR 354, ADR 333, ADR 335, ADR 351, ADR 268, ADR 355, ADR 299, ADR 332, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 146, 149, 197, 203, 204, 210, 234, 241, 248, 262, 315, 334.
- Quote: "Two active secrets. A webhook sink has one or two active signing secrets. To rotate, a person adds a second secret, moves the receiver to it, and then deletes the first. During the change, each delivery carries both sign"
- Quote: "The signature. A webhook delivery is an HTTP `POST` of the JSON of the notification, with three headers: - `Dustpan-Notification-Id`: the notification ID, - `Dustpan-Timestamp`: the time of the request in Unix seconds, -"
- Effect: Follows decision 22 two-secret rotation and decision 21 signatures. Server-made key of decision 13 stays. NEW rotate and confirm routes extend the route table of decision 27.
- Conflict: none.

**ADR 213: Job tokens and outputs, plans with no change, the start policy input, bulk calls, the options action, forge connections and locks** (not owner-tagged).

- Decisions that apply: 7 (changed).
- Changed by: ADR 241 changes decision 10; ADR 244 changes decision 10; ADR 265 changes decision 9; ADR 267 extends decision 1; ADR 271 changes decisions 7 and 8; ADR 273 changes decision 1; ADR 273 extends; ADR 286 changes; ADR 300 changes decision 2; ADR 300 changes decision 10; ADR 309 extends decision 5; ADR 353 changes decision 2. Chain to the end: ADR 241, ADR 244, ADR 265, ADR 267, ADR 271, ADR 273, ADR 286, ADR 300, ADR 309, ADR 353, ADR 248, ADR 313, ADR 343, ADR 299, ADR 307, ADR 310, ADR 338, ADR 369, ADR 370, ADR 303, ADR 336, ADR 328, ADR 359, ADR 368.
- Current rule: this ADR, with ADR 241, 244, 265, 267, 271, 273, 286, 300, 309, 353.
- Quote: "A rotation adds the new credentials before it removes the old ones. Two sets can be active during a rotation, and the server uses the newest set that works."
- Effect: Follows the sealed-credential rotation pattern of two active sets.
- Conflict: none.

**ADR 149: Hardening of the operations ADRs** (not owner-tagged).

- Decisions that apply: 9 (current), 12 (current).
- Changed by: ADR 210 changes. Chain to the end: ADR 210, ADR 247, ADR 286.
- Current rule: this ADR, with ADR 210.
- Quote: "One ref names one value. In addition to ADR 145 decision 13, a ref names one value for good."
- Effect: Each new secret gets a new ref per decision 9. Rotate and confirm are key lifecycle under ADR 145 decision 22, so subscriptions stay active; decision 12 suspend covers a new destination, not rotation.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Which rotation pattern do webhook sink signing keys follow? ADR 145 decisions 13 and 22: server-made secrets, one or two active, both signatures on each delivery. ADR 213 decision 7 gives the same two-set pattern.
- Does this change build the person-supplied write-only signingKey? No. Issue #1298 stays an RFC. This change builds only the server-made key of ADR 145 decision 13. The write-only person-supplied key waits for the RFC.
- Do operators need installation-scope sinks? Keep the refusal. No operator use case appears in the pack. Namespace stays the only scope for chat and webhook sinks.
- Where do the new routes and error codes live? ADR 145 decision 27 lists sink routes; this change adds NEW rotate and confirm routes and reuses sink.conflict and sink.invalid that exist on main.
- Does rotate suspend subscriptions? No. ADR 145 decision 22 describes rotation as key lifecycle with both signatures sent. ADR 149 decision 12 covers a new destination of kind, target, or secrets. BeforeAttempt still checks the creator grant before each delivery.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
