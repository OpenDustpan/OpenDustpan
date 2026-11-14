---
version: 2
---

Issue: #1283.

## Why

**Persona: operator.** The operator runs a server where state.write gates must see stable facts. Separate reads under a shared lock can mix rows from before and after a commit, which makes the gate input hard to explain.

The user path this change must make work:

1. **Server start.** Start the server with and without a gate service and read the start-up log for the one gate line.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Fact read fails: the state.write gate did not read deleted-unit facts because the store read failed. | The gate refuses the write and reports the store error. Retry the write. If the failure repeats, run the store fact test to check the read path. | `go test ./pkg/store/sqlite/ -run TestDeletedUnitFacts -count=1` |

Evidence for the claims above:

- Issue #1283 asks for a SQLite snapshot read, a start-up log, and removal of the blank-assign rollback pattern. Proof: Follow-ups from the final review of #1259. None blocks the merge.
- The issue reports that a shared lock does not give a snapshot because writers take the same lock. Proof: SQLite `GetDeletedUnitFacts`: `s.mu.RLock` does not give a snapshot, because writers take `RLock` too, so a commit can land between the reads. Use one read-only transaction. Impact is low.
- Gate fact tests call GetDeletedUnitFacts. Proof: pkg/store/storetest/gate.go:809: got, err := facts.GetDeletedUnitFacts(t.Context(), "acme", tc.tool, id)
- The store fault wrapper has a GetDeletedUnitFacts path. Proof: internal/store/storefault/storefault.go:911
- The SQLite store uses a shared lock on a read path. Proof: pkg/store/sqlite/state_at.go:20: s.mu.RLock()
- The context pack lists no ADR for this item. Proof: The pack adrs list is empty, so there is no decision text to quote.

## What Changes

- SQLite GetDeletedUnitFacts reads all deleted-unit rows in one read-only transaction scoped to the call with deferred close.
- Server logs one fixed INFO line per start when there is no gate service.
- The touched rollback path no longer uses the blank-assign pattern, with unchanged close behavior.
- No conformance suite behavior change; the fault wrapper keeps its fault behavior.

Out of scope:

- New gate facts, new decision points, or atomic check-and-write of the gate and the state write.
- Postgres GetDeletedUnitFacts read path changes.
- Gate route, console, or user docs changes.

## Usability

Where the path still costs the person time or surprises them:

- The operator path is one start-up log line with no new command; snapshot behavior is not visible from the API.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Gate fact conformance tests call GetDeletedUnitFacts. (`pkg/store/storetest/gate.go:809`)
- Store fault wrapper implements GetDeletedUnitFacts with fault injection. (`internal/store/storefault/storefault.go:911`)
- SQLite store read path uses a shared lock. (`pkg/store/sqlite/state_at.go:20`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: test `TestDeletedUnitFacts` (.errors[0].command) does not resolve on main; use the name main has, or mark it NEW in the same sentence.

Questions considered and answered:

- Does this change need a wider gate or store redesign? No. The issue lists three small follow-ups. The gated write stays unchanged.
- Does this change conflict with an ADR? No. The pack adrs list is empty, so adr_check is empty.
- Should the Postgres GetDeletedUnitFacts path change? No. The issue names only SQLite. Both dialects keep the same logical rows.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
