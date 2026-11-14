## Context

The replay check in internal/pulumijournal stays the single enforcement point. The change adds a measured corpus file, re-derives MaxNewStateBytesPerUpdate and the count bound from it with a fixed formula, and leaves non-journal checkpoint saves on the separate 32 MiB checkpoint bound.

## Decisions

1. **Fixed formula from per-update totals and p1 state size.** Derive the byte bound as two times the largest observed per-update new-state total in the corpus, floored at 1 MiB and capped at 32 MiB. Derive the count bound as 33554432 divided by the p1 smallest real resource state size in bytes, floored at 1000 resources and capped at 500000 resources per update. Each bound comment cites the NEW corpus file internal/pulumijournal/testdata/real-states.jsonl, the Pulumi CLI version, the provider list and the measurement date.
2. **Bound change applies forward only.** Stored updates keep readability under the bound at their write. An in-flight journaled update that exceeds a later lowered bound fails closed with 413. A replay after restart applies the current bound. A non-journal checkpoint save keeps the separate 32 MiB checkpoint bound.
3. **No new setting for the bound.** Add no setting and no API field for the bound. The effective value stays visible in the bound comment in internal/pulumijournal/bounds.go and in the limit field of the 413 answer, because the bound is a fixed constant derived by rule from the committed corpus.

## Structure

No new type. The existing unexported replay byte counter plus MaxNewStateBytesPerUpdate makes an oversize update unrepresentable past the check. The change re-derives the constant by the fixed formula after measurement.

## Risks

- A bound set too low rejects large real updates from the Pulumi CLI.
- A bound set too high weakens the journal memory limit.

## Diagrams

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[internal/pulumijournal]
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | Tuning of an existing memory bound; no latency or storage target changes |  |  |

## Change impact (computed from main)

Tests:

- `pkg/stack/journal_bounds_test.go` names `MaxNewStateBytesPerUpdate`
- `internal/api/pulumi/journal_replay_bounds_test.go` names `MaxNewStateBytesPerUpdate`
- `internal/pulumijournal/corpus_test.go` names `MaxNewStateBytesPerUpdate`
