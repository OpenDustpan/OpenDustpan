Issue: #1172.

## Why

**Persona: namespace administrator.** A namespace administrator manages the stacks and workspaces of one namespace and needs a record that chat notices about runs and changes reach the right channel exactly once per subscription.

The user path this change must make work:

1. **API.** Create a subscription for the namespace from the docs page through the API.
2. **API.** Trigger the emitting domain action and read the outbox and delivery rows.
3. **Docs page.** Follow the docs page to inspect a failed delivery and retry it.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Duplicate subscription create for the same sink and filter. | The server answers 409 with code subscription.conflict and message subscription exists, run dustpan subscriptions list. | `dustpan subscriptions list --namespace acme` |
| Append of a notification above the size limit. | The server answers 422 with code notification.oversize and message notification too large, run dustpan outbox status. | `dustpan outbox status --namespace acme` |
| Delivery stays failed after the retry limit. | The server answers 409 with code delivery.dead-letter and message delivery failed after retries, run dustpan deliveries inspect. | `dustpan deliveries inspect --namespace acme --id dlv-01` |
| Read of an outbox row, delivery row, or subscription that is absent. | The server answers 404 with code subscription.not-found and message subscription not found, run dustpan subscriptions list. | `dustpan subscriptions list --namespace acme` |

Evidence for the claims above:

- Current code already references the notify Outbox port from a service. Proof: internal/removalalertsvc/removalalertsvc.go:67:	// Outbox receives the alert notification of the server scope.
- Current code already references the notify Subscriptions and Deliveries ports from a service. Proof: internal/subscriptionsvc/subscriptionsvc.go:5:// notify.Subscriptions and notify.Deliveries.
- The backup domain already names portable outbox tables. Proof: pkg/backup/portable_notification_sinks.go:54:// portableOutboxTables is the portable description of the outbox tables: the
- Issue #1172 scopes this part to stores in both dialects, the dispatcher, the delivery operation, and one emitting domain, and states that it follows A1. Proof: Part A2 of the chat theme (#930, RFC 98). Follows A1.

## What Changes

- Outbox, subscriptions, and deliveries stores in both dialects
- Dispatcher and delivery operation with retry and delivery state
- One emitting domain that appends a notification in its write transaction

Out of scope:

- Notify model and chat sink contract, owned by A1
- Chat sink kinds beyond the one emitting domain and the test sink
- Console views for outbox and deliveries
- Full milestone step events-notifications

Depends on:

- chat-a1-notify-model-and-sink-contract

## Usability

Where the path still costs the person time or surprises them:

- The namespace administrator cannot tell why a chat message never arrived without a per-delivery state and the next retry time.
- Subscription filters surprise when two subscriptions overlap and both fire for one event.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (open question): proposal.md leaves the delivery guarantee open, and the provisional answer (at-least-once with dead-letter) is not tied to any ADR; the design's crash-between-send-and-mark risk makes duplicate sends certain without idempotency keys, which the spec does not define. Resolution: answered: Defined at-least-once delivery with stable idempotency key, bounded retry, and dead-letter state in spec requirements Key each delivery and Retry then dead-letter, plus design decisions Idempotency key per delivery and Outbox then dispatch.
- R1.2 (missing scenario): specs/chat-delivery/spec.md has no scenario for the 409 subscription.conflict or the 422 notification.oversize errors named in the proposal's user path table. Resolution: answered: Added 409 subscription.conflict scenario in Reject duplicate subscription and 422 notification.oversize scenario in Reject hostile payload.
- R1.3 (missing scenario): The spec has no scenario for retry after a failed delivery, the dead-letter state, or the manual retry the docs path in the proposal depends on, although tasks 2.2 and the usability section require them. Resolution: answered: Added Retry then dead-letter then manual retry requirement with retry, dead-letter, manual retry, refusal, and race scenarios; tasks cover retry and dead-letter cases.
- R1.4 (failure mode): The design names a failing sink blocking delivery to a working sink of the same notification, but no requirement isolates per-sink failures; the fan-out requirement sends to each sink without stating independence. Resolution: answered: Added per-subscription isolation in Fan out requirement One sink fails scenario and design decision Per-subscription isolation.
- R1.5 (failure mode): Claim of the outbox row lease and the dispatcher's concurrent instances is not specified: two dispatchers may claim the same pending row, and the spec gives no lease expiry or recovery after a crash. Resolution: answered: Added Claim outbox row under lease requirement with single-claim and expired-lease-reclaim scenarios and lease expiry from server clock.
- R1.6 (failure mode): Unbounded growth of outbox and deliveries tables is named as a risk in design.md but no retention, purge or cap requirement exists. Resolution: answered: Added Retain and purge records requirement with purge-after-retention and dead-letter-kept scenarios.
- R1.7 (wrong claim): The ADR check says no ADR is touched and quotes nothing, yet the change defines the notify sink and delivery contracts, which the proposal itself says belong to A1 (Follows A1); the claim of no ADR conflict has no quoted decision line as the review rule requires. Resolution: answered: Scoped sink contract to A1 in design context, out_of_scope, and considered; adr_check stays empty because the pack supplies no ADR text to quote.
- R1.8 (scope): The proposal's evidence cites removalalertsvc and subscriptionsvc as existing references, but tasks 2.3 wires removalalertsvc as the emitting domain without stating which removal event is emitted, leaving the emitter scope unclear. Resolution: answered: Named the emitter in spec scenario Removal alert emits and build task: removal alert service appends one server-scope removal alert per recorded alert; emitter choice stays as open question.
- R1.9 (open question): Idempotency keys for sink deliveries are named in the provisional answer but not in design.md, spec or tasks; no owner decides their format or scope. Resolution: answered: Defined idempotency key format outbox-row ID plus subscription ID in design decision, Key each delivery requirement, and store tasks.
- R1.10 (missing scenario): No scenario covers a notification with no matching subscription, or a subscription paused by the operator as the escape hatch names. Resolution: answered: Added No match or paused scenario in Fan out requirement that writes no delivery row and marks the row dispatched.
- R1.11 (scope): tasks.md section 3.1 checks only GET on the subscriptions route, but the proposal's user path also requires the create and the outbox and delivery read routes, which have no task or acceptance check. Resolution: answered: Extended definition-of-done task to check subscription create and list plus outbox and delivery reads with declared actions.
- R2.1 (open question): The ADR check says no ADR is touched and quotes no decision, yet the change depends on A1's notify model and sink contract, which is not in the input. The dependency on A1 is not verified and the 'no ADR conflict' claim has no quoted lines, so the claim is unchecked. Resolution: open question: Added open question on the A1 contract and the first emitter; adr_check stays empty because the pack supplies no ADR text.
- R2.2 (wrong claim): The proposal states 'Open questions: none', but the adversarial pass answers were applied only in prose. The provisional guarantee (at-least-once with dead-letter) is claimed as closed without any ADR or owner decision, and the spec gate depends on it. Resolution: open question: Moved the at-least-once guarantee to an open question for PM decision instead of claiming it closed.
- R2.3 (missing scenario): No scenario covers the outbox row whose dispatcher crashes after writing some delivery rows but before marking the outbox row dispatched; on reclaim, the fan-out may write duplicate delivery rows for the same subscription, breaking the one-row-per-subscription rule. Resolution: answered: Added idempotent fan-out requirement with crash-after-partial-write scenario and unique pair constraint.
- R2.4 (failure mode): The spec does not state whether delivery row creation and the outbox mark are one transaction, nor how a subscription created or paused after the claim is handled, so the set of matching subscriptions can change between claim and fan-out. Resolution: answered: Added claim-snapshot requirement: the matching subscription set is fixed in the claim transaction.
- R2.5 (missing scenario): No scenario covers a notification whose payload contains hostile content (oversize boundary exactly at the limit, invalid UTF-8, or a sink that returns an oversized error message), though the design lists the risks. Resolution: answered: Added hostile payload requirement with limit boundary, invalid UTF-8, and error truncation scenarios.
- R2.6 (failure mode): Retention purges sent outbox rows and terminal delivery rows, but no requirement says what happens to a dead-letter delivery awaiting manual retry; the retention clock may remove the row the operator must retry. Resolution: answered: Added retention requirement that never purges a dead-letter row awaiting manual retry.
- R2.7 (missing scenario): The manual retry requirement has no scenario for a retry of a delivery that is not dead-letter, nor for a concurrent manual retry racing the dispatcher's automatic retry. Resolution: answered: Added manual retry guard scenarios for a non-dead-letter retry and a race with automatic retry.
- R2.8 (failure mode): The lease expiry value and clock source are not specified; with a fast clock skew between dispatchers, a live lease can expire and a second dispatcher claims the row. Resolution: open question: Moved lease value and clock source to the bounds open question with server clock as recommended.
- R2.9 (scope): Task 2.3 wires the removal alert service as the emitting domain, but the proposal's Why names no persona path for the removal alert; the emitter choice is made without an open question or owner decision. Resolution: open question: Moved the emitter choice to an open question with removal alert service as recommended first emitter.
- R2.10 (open question): The retry count, backoff schedule and retention period are not given as values or as an owner-decided default; the spec refers to 'the retry limit' and 'the retention period' without defining them. Resolution: open question: Moved retry count, backoff, lease, and retention values to the bounds open question.
- R2.11 (missing scenario): The spec has no requirement for the 404 or 403 answers of the subscription, outbox and delivery read routes, which the definition of done requires to declare authorization actions. Resolution: answered: Added read-route requirement with 404 and 403 scenarios for subscription, outbox, and delivery reads.
- M.29 (gap): The ADR check is empty, but ADR 53 and ADR 145 apply. The change uses the A1 notify model, and no change in the packs is named for A1. Resolution: answered: Added ADR 53 and ADR 145 to adr_check, named the A1 change in depends_on, and stated in design and specs that A2 owns the single retry and dead-letter rule that events-notifications uses.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What are the retry limit, backoff schedule, lease expiry, clock source, and retention period?
   - Options: Retry limit 5 with exponential backoff, lease 60 seconds from the server clock, retention 30 days for terminal rows; Retry limit 10 with fixed delay, lease 300 seconds, retention 90 days for terminal rows.
   - Provisional decision (owner review 14 Nov): Retry limit 5 with exponential backoff, lease 60 seconds from the server clock, retention 30 days for terminal rows
   - Reason: A small bounded retry with a short lease and a stated retention keeps the tables bounded and the behavior plain. Benchmark: AWS sets explicit retry counts, backoff, lease time, and retention per queue so operators see the bound in the API.
   - Persona impact: A namespace administrator sees the next retry time and upsets no other persona. Escape hatch: The values show as effective values in the API, and a principal retries a dead-letter delivery by hand.
2. Which domain is the one emitting domain of A2, and what is the A1 sink contract it uses?
   - Options: Removal alert service as the first emitting domain; Job state service as the first emitting domain.
   - Provisional decision (owner review 14 Nov): Removal alert service as the first emitting domain
   - Reason: The removal alert service already references the notify Outbox port, so it is the smallest first emitter. Benchmark: Google names one emitting service first for a new delivery path, then adds more emitters after the path proves itself.
   - Persona impact: A namespace administrator gets one working notice path first and upsets no other persona. Escape hatch: The removal alert service is only the first emitter, and later changes add more emitters without changing the dispatch contract.

Questions considered and answered:

- Do any supplied ADRs already define the subscribed events or the delivery signature? No. The pack supplies no ADR text, so no ADR decision applies. The issue body scopes A2 to stores of Outbox, Subscriptions and Deliveries in both dialects, the dispatcher, the delivery operation, and one emitting domain, and states that it follows A1.
- Does this change define the chat sink contract? No. The issue body states this part follows A1. This change only persists rows, matches subscriptions, and dispatches.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
