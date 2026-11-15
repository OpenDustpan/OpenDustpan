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

- A too-soon refusal appends one denied entry in the namespace of the fence,
  with the reason `sink.delivery-too-soon`, the same kind and the same
  fields as the entry of a refusal at the limit. If that append fails, the
  error joins the refusal and the redeliver stays refused.
- The change builds on the audit port and the limit entry of #2899. If #2899
  is still open when this change is ready, F4 implements these tasks in
  #2899 itself, so that the two do not conflict in one function.
- Out of scope: how many entries repeated refusals write. That is an open
  question for the owner (the decision queue item on bounded denial
  entries). Until the owner decides, each too-soon refusal writes one entry,
  as each refusal at the limit does.

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
namespace, one entry for each minute with refusals". Effect: none here; the
bound on repeated entries is the open question. Conflict: none.

## Open questions

none. Considered: route the entry through the shared denial
limiter now (left out: it changes how many entries repeated refusals write,
which the owner decides in the queue item).
