## Context

Six non-blocking review items from merged PRs in October 2026. Five items harden API description coverage, handler field discovery, body declarations, streamed-write guards, and history error mapping. One item removes blob keys from workspace error text.

## Decisions

1. **Test-first hardening with small source fixes.** Do test-only changes first as failing regression tests, then apply small source fixes for blob-key redaction, the history race, and the early recorder guard of WriteStreamed when main lacks it. Map store lookup failures to sentinels without wrapping raw key text so errors.Is still matches and no unwrapped cause prints the key. Treat empty ListUpdates for a deleted stack as ErrNotFound and pass other store errors through unchanged. No new packages and no new routes.

## Structure

No new unexported type. The fix keeps existing error sentinels and maps to them without key material; the constructor rule does not apply because no new value type is added.

## Risks

- Fixture changes to API description tests can hide real coverage gaps when fixtures drift from server assembly.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/dustpan]
P2[pkg/workspace]
P1 --> P2
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Test-only hardening plus removal of key material from four error sites; no latency or storage target changes |  |  |

## Contracts

**workspace state blob opener**

- Invariant: Errors from OpenStateBlob carry safe identifiers only
- `OpenStateBlob`: pre: Caller supplies workspace identity and version identity. Post: Result opens the named version or returns a matched sentinel without the blob key in the message.

**stack update history**

- Invariant: Missing stack maps to ErrNotFound
- `History`: pre: Caller supplies stack identity. Post: Deleted stack between checks still returns ErrNotFound; existing stack without updates returns an empty list.

**workspace streamed write**

- Invariant: No placeholder declaration or attempt write occurs without a recorder
- `WriteStreamed`: pre: Override write path requires a recorder. Post: Override without recorder is refused before any placeholder or attempt write.
