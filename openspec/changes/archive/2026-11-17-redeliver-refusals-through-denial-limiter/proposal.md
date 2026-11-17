---
version: 1
---

Issue: #3160.

Short form: bug (SECURITY). Owner lane: F4.

Waits for: #2899

## Problem

**Persona: namespace administrator.** A script that repeats a refused
redeliver must not grow the audit log of the namespace without a bound, and
the administrator must still see each refusal. #2899 writes one denied entry
for each redeliver refused at the limit, from the subscription service. The
owner decision of 2026-11-15 on repeated refusals says that a domain refusal
that a caller can repeat goes through the shared denial limiter.

## Evidence

- On the head of #2899, `Service.Redeliver`
  (`internal/subscriptionsvc/subscriptionsvc.go`) appends one denied entry
  with the reason `sink.delivery-redeliver-limit` for each refusal at the
  limit, through its own audit port.
- The shared denial limiter is `internal/denylimit`, used by the refusal
  recorder `internal/api/refusal` (`Recorder.Record`, `refusal.go:210`). The
  notifications adapter already holds a recorder with that limiter
  (`internal/api/notifications/notifications.go:117`).
- The too-soon refusal records through the same recorder (no separate change).

## Fix boundary

- The refusal at the limit is recorded by the redeliver handler through the
  refusal recorder of the adapter, as the too-soon refusal is. The service
  stops appending its own entry for it.
- Both refusals share one key of the limiter for one person and one
  delivery scope (principal, action, object kind, scope), so N identical
  refusals in one window write one entry and a count of N-1.
- Out of scope: the window and the bounds of the limiter.

## Check

The hostile table passes, it fails with the record removed, and `make check`
reports zero findings (Confirmed by: CI).

## ADR check

**ADR 250: Defense in depth for security code** (owner). Quote: "Every denial
and every use of a privilege goes to the audit log." Effect: each refusal is
recorded, as one entry or as a count. Conflict: none.

**ADR 220** (owner), decision 15. Quote: "The count of refusals goes to the
audit scope of the namespace, one entry for each minute with refusals".
Effect: the same form. Conflict: none.

The owner decision of 2026-11-15 on repeated refusals (its ADR is in
progress): a domain refusal that a caller can repeat goes through the shared
denial limiter. Effect: this change applies it to the refusal at the limit.
Conflict: none.

## Open questions

none. Considered: keep the service append and add a limiter there
(refused: the shared limiter and its recorder exist, and one place should
record the refusals of the adapter).

Depends on: none.
