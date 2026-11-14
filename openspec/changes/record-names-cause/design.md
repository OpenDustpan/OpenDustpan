## Context

Fan-out creates one change order, one discovery job, and N plan jobs plus N apply jobs. Each plan and apply job writes a unit history entry and an audit entry. This change adds cause fields to those entries. No new endpoint is added. Existing entries without a cause keep null cause fields.

## Decisions

1. **Cause as stable-ID fields, not parsed text.** Store cause as three fields copied into history entry rows and audit entry rows at write time: discovery job stable ID, requesting principal stable ID with subject display copy, and fixed reason code terragrunt-run-all-fan-out. Readers never parse free text. The NEW constructor NewCauseRef takes all four values and rejects empty IDs and a reason other than the single allowlisted code. A row with any cause field null or empty reads as no cause. Schema change edits the single baseline per dialect in place; existing rows keep null cause with no numbered migration.
2. **Server-bound cause with existing read grants.** The server loads the named discovery job record in the same namespace before it creates a child job, and copies cause only from that stored record. A child that names an unknown job, a job in another namespace, or a job that is not a discovery job is refused with the NEW error code job.cause-invalid and writes no history or audit entry. Client-supplied cause fields are ignored. Audit cause reads keep the existing audit-read grant. History cause reads keep the existing unit read grant.

## Structure

Unexported type causeRef with NEW constructor NewCauseRef(discoveryJobID, requesterID, requesterSubject, reasonCode) that rejects empty IDs and rejects a reason other than the single allowlisted code; only the constructor can build a value, so an entry cannot carry a partial cause. No golangci-lint setting remains for this invariant because the unexported type enforces it; wide renames use gopls.

## Risks

- Free-text reason drifts across writers and breaks filtering.
- A principal removed and made again breaks a name-based link.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service ignores any client-supplied cause, loads the discovery record in the same namespace, and refuses a mismatch. | Send a job create with forged cause fields and with a discovery job from another namespace and assert refusal with no entry written. |
| handler | Audit read route enforces the audit-read grant before returning cause fields. | Read ANY /api/audit/entries without the grant and assert denial with no cause disclosed. |
