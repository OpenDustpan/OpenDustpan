# 357. The model port gets a stream, and the agent turn route streams

Status: accepted · 2026-11-07. Records owner decision 73.3 of 18 October 2026

Changes:

- Changes ADR 121 decision 6: the port gets a second method that streams. `Complete` does not change.
- Extends ADR 314 decision 2: what the stream of an answer needs.

## Context

On 18 October 2026 the project owner decided: "Stream Dusty's answers in the console". ADR 314 records these words and adds no design.

ADR 121 decision 6 says that the model port has no stream. The console shows the progress of a run by turn. ADR 121 also says: "A stream can come later as a second method, with no change to `Complete`". The decision of the owner is that "later". This ADR records the parts of the design that the decision settles. It adds no decision of its own.

## Decision

1. **The port gets a second method (owner, 2026-10-18).** `agent.Model` keeps `Complete` and `Info`, and gets a streaming method. `Complete` does not change.
2. **The agent turn route streams (owner, 2026-10-18).** While a turn runs, the agent turn route sends the text deltas of the answer. It also sends the progress of the tool calls.

## Consequences

- ADR 121 decision 6 does not apply now. Its other decisions apply.
- The route uses `net/http`, as `CLAUDE.md` requires.
- The stream ends with one final event. The final event matches the turn as the store records it. The deltas are a preview of it.
- When the client closes the connection, the model call stops.
- ADR 124 applies to the stream. A secret value, or a masked value, never appears in a delta or in a progress event. A test checks this with a canary value.
- A canceled call leaves no goroutine that runs. The test package uses `goleak`.
- Each adapter of the model port runs the conformance suite for the second method (ADR 121 decision 10).
- The feature has a page in the user documentation in the same pull request that adds it.
- The console shows the stream in its own repository.

## Not decided here

- The transport of the stream (for example server-sent events).
- The signature of the streaming method, and the type of its events.
- The route path, the name of each event, and the JSON of the data of an event.
- How the route finds the turn that it streams, and the action that it needs.
- Whether a client can resume a stream after a lost connection (for example with `Last-Event-ID`).
- How the stream treats a turn that a model error retries, and a turn that a restart continues.
- What the turn state, the stop reason and the token ledger hold after a disconnect cancels a model call.
- What an adapter does when its provider has no stream.
- How the connection setting `transport` (ADR 129 decision 15) relates to the stream of the port.
- Limits on the size of an event, the number of open streams, and the idle time of a stream.
