## Context

Two runner jobs apply disjoint changes to one workspace at the same time. Each job planned from a named base. The server serves that base and merges each upload without a lost resource.

## Decisions

1. **Serve base per claim.** Serve reads under a claim from the named base state version, never from latest. Reads without a claim return latest. How a request names its claim is an open question for the owner.
2. **Merge per instance with atomic commit.** Merge uploads one resource instance at a time and commit with compare-and-swap on the serial in one fenced transaction. A conflict on one instance refuses the whole upload. A failed merge writes nothing.

## Structure

Unexported type claimMerge with fields baseID, baseSerial, and claimFence and constructor newClaimMerge that checks accepted claim state, workspace identity, base presence, and fence before merge.

## Risks

- A merge that reads latest instead of base breaks plan safety.
- A non-atomic commit leaves a half merged state.
- A missing fence check lets a stale job overwrite a new state version.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| adapter | HTTP adapter checks claim identity, workspace identity, fence, content type, size, and MD5 before the service call. | Send mismatched fence, foreign claim, bad lineage, and malformed JSON directly to the adapter and expect 409, 422, or 400 with no state change. |
| service | Service checks accepted claim state, base presence, strict cases, empty upload, per-instance conflict, and serial nextness before commit. | Replay an expired claim, an upload outside the claim, and a same-instance double change directly to the service and expect refusal. |
| store | Store commits blob reference, merged state, and next serial in one fenced transaction with compare-and-swap on current serial. | Run two concurrent commits from the same current serial and prove only one wins; kill the process mid-merge and prove no half state appears. |
