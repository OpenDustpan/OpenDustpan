## Context

Denied output reads write one audit entry for each request with no request fields and no limiter. A caller with read permission can flood the audit log with refused reads.

## Decisions

1. **Reuse the refusal recorder.** Route each denied output-read audit write through refusal.Recorder so the entry carries the request fields that the issue names and passes the shared limiter.
2. **Pass request fields at the boundary.** Pass the request fields that the handler observes to the output-sharing checker instead of building the denial entry inside the checker with empty fields. The handler adds no new header parsing and trusts no new proxy header; it passes the same observed values that existing recorder call sites pass.

## Structure

The unexported recorder field on the output-read handler owns denial writes; only its constructor wires the shared refusal.Recorder, so no call site appends a denial without the request fields and without the limiter.

## Risks

- A flood of refused reads fills the audit log before this change ships.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | Handler copies the observed client address, user agent and request ID into the checker call; the checker writes only what it receives and invents no address. | Send a denied output read with empty and forged client fields direct to the handler and check the entry holds the observed values. |
| effect limit | The checker writes the denial only through refusal.Recorder, which applies its limiter before the append. | Flood denied output reads and check the recorder stops further denial entries after its budget. |
| fail closed | The denial path keeps the 403 or 409 answer for the read when the recorder drops or fails the entry, so a limiter or audit error never grants the read. | Force the audit append to fail or exhaust the budget during a denied read and check the read still fails closed. |
