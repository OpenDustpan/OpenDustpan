---
version: 1
---

Issue: #1326.

## Why

**Persona: operator.** An operator audits who changed a job. The change `job-start-idempotency-retention-24h` lets a start take an expired key. The key can name a job on another unit, and the clear then writes that old job. Review of that change asked for a rule for this case. The rule is missing, so the operator cannot tell if the write is allowed.

The user path this change must make work:

1. **API.** A principal starts a job on unit A with an Idempotency-Key and gets the job.
2. **API.** More than 24 hours later, the same principal starts a job on unit B with the same key. The server starts the new job.
3. **API.** The operator reads the audit log and finds the entry `job:key-expired`. It names the job on unit A, the unit B, and the principal.
4. **Docs page.** The operator reads why the server writes the old job with no check on unit A.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Another principal sends the same key string. | The server treats it as another key and starts a job. It does not touch the job of the first principal. | None needed. |
| The clear fails. | The start fails and the old job keeps its key. | Send the start again with the same key. |

Evidence for the claims above (the key rules are in the open change `job-start-idempotency-retention-24h`, not on main):

- The key that the store keeps holds the subject. Proof: func scopedKey at `pkg/job/service_start.go:588` hashes `subject + "\x00" + key`. So a key names only a job that the same principal started. The clear never writes the job of another principal.
- The store looks up a key by namespace and key. Proof: `tx.JobByKey(ctx, j.Spec.Unit.Namespace(), j.IdempotencyKey)` in func sameKey at `internal/store/jobstore/store.go:554`. The unit is not in the lookup.
- The owner decision of ADR 210 decision 6 gives the key one scope. Proof: see the ADR check.

## What Changes

- The spec says that an expired key of the same principal in the same namespace is a new key, on any unit. The clear writes the old job with no check on its unit, because the key belongs to the principal that sends the new start.
- The audit entry `job:key-expired` names the old job, the unit of the old job, the unit of the new start and the principal.
- NEW: a conformance case in `pkg/job/jobtest` for a start on a second unit under an expired key. NEW: a test that another principal with the same key string clears nothing.
- NEW: a section in the NEW file `docs/reference/job-start-idempotency.md` (the retention change adds the file) that states the rule.

No schema change. No change to the key scope. No change of a route.

Out of scope:

- Idempotency of other routes.
- A key scope that holds the unit. It changes ADR 210 decision 6 and needs an RFC.
- The explicit clear signal in `Row` (review B1 of the retention change).

Depends on:

- `job-start-idempotency-retention-24h`

## Usability

Where the path still costs the person time or surprises them:

- The operator reads the entry and must know which unit the old job had. The entry names both units.

## What main already does

- The store keeps a hash of the subject and the key. (`pkg/job/service_start.go:588`)
- The store looks up a job by namespace and key. (`internal/store/jobstore/store.go:554`)

## ADR check

- **ADR 210, decision 6** (`docs/adr/0210-the-tool-neutral-api-prefix-paths-actions-versions-and-style.md`). Owner-tagged: "Decision 6 is an owner decision of 2026-10-18." Quoted lines:
  - "**Retention.** The server keeps the replay record (the key, a keyed hash of the request body and the first answer) for 24 hours from the first request."
  - "**A different body.** The same key with a different body answers 422 with the error code `request.idempotency-key-reused` and changes nothing."
  - Result: no conflict. A key of the same principal that names another unit is a different body, so it answers 422 within 24 hours. After 24 hours the record is gone, and the key is new. A key scope with the unit would change the 422 within 24 hours, so this change does not use it.

## Open questions

none

Questions considered and answered:

- Does the clear need an authorization check on unit A? No. The key holds the subject, so the job that the clear writes is a job that the same principal started with this key. The principal holds the grant for the new start on unit B. The clear changes the key and the version of the old job, and no other field. Decided by the PM (rule 16; benchmark: Stripe idempotency keys are per account, and a key that expires is free for any request of that account).
- Should the key scope hold the unit? No. Within 24 hours the same key with another unit must answer 422 (ADR 210 decision 6). A scope with the unit would start a second job there. A change to that needs the owner.
- Should the start answer 422 when the expired job is on another unit? No. The record is gone after 24 hours, so the key is new (ADR 210 decision 6). A 422 would keep a record for longer than 24 hours.
- Is a client with the old ETag of the old job hurt? It gets 412 on its next conditional write and reads the job again. The retention change lists this risk. The old job is more than 24 hours old.
- Is this a security semantic? It is an authorization rule for a write. The PM took it provisionally (rule 88). It goes to the next owner batch.

Drafted by the PM after rev2 of the retention change (finding N1).
