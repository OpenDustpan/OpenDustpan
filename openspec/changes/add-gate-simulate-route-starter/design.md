## Context

Simulate replays stored decision records against draft gates and draft approval rules. Starter packs ship as data and install as evaluate mode gates.

## Decisions

1. **Replay through the gate engine with no writes.** Simulate loads stored fact documents and runs the draft gates and draft approval rules through the same gate engine with writes disabled, from one snapshot of the drafts with a bounded time. It skips records whose change the caller cannot read and records whose fact schema version the engine cannot read, with no row, no ID and no count for skipped records. A range that matches more than 1,000 readable records is refused, never truncated.
2. **Packs as versioned data with all-or-nothing install.** Packs ship as versioned data files with pack ID, pack version, gates, approval rules, and test files. Install runs in one transaction and is all-or-nothing: it copies packs as new gates and refuses the whole install with no gate made when any pack gate asks for a mode other than evaluate, when any pack text claims compliance, when any gate name already exists, or when the same pack ID and version is already installed.

## Structure

Unexported simulateRequest built only by newSimulateRequest, which checks the decision point, the time range, the 1,000 record limit, and refuses pack gates that ask for a mode other than evaluate.

## Risks

- Replay of many records costs time; the 1,000 record refusal bound, the time range, one snapshot, and a timeout bound it, with no partial writes.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler checks action namespace:read-gates on the namespace for simulate and pack list, and action namespace:manage-gates on the namespace for pack install, and filters records to changes the caller can read, with no row, no ID and no count for skipped records. | Caller without read rights on one change gets no row, no ID and no count for that change, and cannot tell skipped records exist. |
| service | Service evaluates drafts in a read-only context from one snapshot and blocks store writes during replay, with a timeout and cancel that leaves no partial answer stored. | Draft with a write side effect stores nothing and changes no change order or job, and a concurrent edit of drafts during replay does not change the run. |
| port adapter | Pack loader forces mode evaluate by refusal and rejects framework wording that claims compliance, inside the same transaction as the name and version checks. | Pack with enforce mode or compliant wording is refused at load and in CI text test; two concurrent installs of the same pack leave one install only. |
