## Context

Open of a static profile without provider blocks, with import merge, final, checks, Expr values, mark propagation, sessions, new routes, audit, and idempotent replay.

## Decisions

1. **Resolve without provider blocks.** Service.Open resolves imports breadth-first, reads each revision once, merges later wins, honors $final, runs checks, evaluates ${ } with the pinned Expr, propagates secret and never-store marks from $neverStore on any value, refuses provider blocks with ErrProvider, and refuses never-store leaves for consumer pulumi and for open mode data with an error that names the path and the ephemeral resource.
2. **Sessions store and lifecycle.** Sessions port with Put, Get, Sweep backed by SQL in both dialects, sealed only with the data key of the root profile so rotation of an imported profile key changes nothing, limited to 100 live sessions for each principal or each job, duration 1 minute to 12 hours, excluded from backups, swept on schedule; service checks profile:open again on each session read and deletes the session on refusal; a rotated or revoked root data key makes old sessions unreadable with no value.

## Structure

Unexported openResolver built only by Service.Open; constructor checks actor, profile ref, paths, consumer, mode, duration, and fence before any read.

## Risks

- A secret value reaches logs, audit, or replay record.
- An old provider build skips the never-store refusal.
- Sessions grow without bound or stay readable after revoke or key rotation.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Service checks actor, consumer, mode, paths, duration, and profile:open on root and each reached profile before any read. | Open with no grant answers 403 and writes one access entry; session read after revoke answers 403 and deletes the session. |
| store | Store seals each session with the data key of the root profile and checks fence, owner, expiry, and limit in the same transaction as the open and its audit entry. | Read with another credential, an ended session, a session past 100 live, or a rotated root key answers 404 or 403 with no value. |
| handler | Handler checks Idempotency-Key once, answers only the session ID on replay including replays after delete, and never writes values to logs, audit, or replay records. | Replay with the same key under load stores one session; logs and audit hold no secret value. |
