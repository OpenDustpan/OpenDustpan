---
version: 1
---

Issue: #3160.

Short form: bug (SECURITY). Owner lane: F4 (it holds #2899).

Waits for: #2899

## Problem

**Persona: namespace administrator.** The administrator reads the audit log
to see who tried to send a notification delivery again, and who was
refused. Today a redeliver that comes too soon is refused with 429 and
`sink.delivery-too-soon`, and no audit entry records the refusal. The rule
for security code says that every denial goes to the audit log.

## Evidence

- On main, `Service.Redeliver` (`internal/subscriptionsvc/subscriptionsvc.go:313`)
  passes every store refusal on and appends no entry for any of them.
- The open pull request #2899 (head `fix/2697-redeliver-limit`) gives the
  service a required audit port (`Config.Audit`) and appends one denied entry
  for a refusal at the limit (reason `sink.delivery-redeliver-limit`). Its
  description says: "Too soon writes no entry."
- ADR 250 decision 1 (owner): "Fail closed and record: an unknown value, a
  parse error, missing configuration or a dependency error means deny. Every
  denial and every use of a privilege goes to the audit log."

## Fix boundary

- A too-soon refusal is recorded by the redeliver handler
  (`internal/api/notifications/subscriptions.go`, func Handler.redeliver)
  through the refusal recorder of the adapter, which uses the shared denial
  limiter: the first refusal of a key (principal, action, object kind, scope)
  in the window writes one denied entry with the reason
  `sink.delivery-too-soon`, and later ones are counted and reported with the
  next entry. This is the owner decision of 2026-11-15 on repeated refusals.
- The service writes no entry of its own for the refusal.
- The change builds on the error code of #2899. If #2899 is still open when
  this change is ready, F4 implements these tasks in #2899 itself.
- The refusal at the limit moves to the same path in the change
  redeliver-refusals-through-denial-limiter.

## Check

The hostile tests pass and fail with the guard removed, and `make check`
reports zero findings (Confirmed by: CI).

## ADR check

**ADR 250: Defense in depth for security code** (owner-tagged, "The project
owner decided it"). Quote: "Every denial and every use of a privilege goes
to the audit log." Effect: the too-soon refusal gets its denied entry.
Conflict: none.

**ADR 220: Isolation between namespaces on shared infrastructure** (owner).
Quote, decision 15: "The count of refusals goes to the audit scope of the
namespace, one entry for each minute with refusals". Effect: the refusal
goes through the shared denial limiter, as the owner decision of 2026-11-15
on repeated refusals says. Conflict: none.

## Open questions

none. Considered: one entry for each refusal (refused: the owner decision of
2026-11-15 on repeated refusals puts a refusal that a caller can repeat
through the shared denial limiter).
