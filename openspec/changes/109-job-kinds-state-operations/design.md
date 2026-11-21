## Context

A runner operator runs state work such as refresh and unlock as jobs. The state-operation kinds and the many-run behavior wait on runs, so the operator cannot plan pools and labels for them yet.

This change waits on the run model of milestone 5. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Kinds, actions, and many-run behavior ship with the run model. Runner operators keep the current job kinds until then.
2. **A closed set of state-operation kinds.** Refresh, unlock, replace, and delete each get their own authorization action, audit entry, and masked output. Restore stays out. An open set would make authorization, audit, and retention review inexact.
3. **One job fans out across many runs with per-unit results.** The job result is failed when any unit fails. One unit per job ships first where the fan-out is not ready yet.
4. **State-operation jobs run as durable operations.** Each claim needs a lease with a fencing token on the database clock (ADR 72, decision 2). Work stays in OpenDustpan jobs and gates with no new execution path (ADR 305, decision 4).
5. **State reads mask secrets.** Any job output that reads state replaces each secret value with a marker (ADR 41, decision 2). Reveal stays a separate call.

## Structure

No new structure now. The bug class is an open-ended kind that bypasses authorization and audit; a closed kind set where each kind carries its own action and audit entry is the structure that removes it. When the run model ships, the job kind is an unexported value built only through its constructor, which accepts only the closed set.

No custom analyzer and no new tool. A later refinement names the kinds, the actions, and the per-unit result shape.

## Risks

- Operators cannot plan pools for state-operation kinds until milestone 5 ships. The docs page says so and points at the current job list.
- A partial failure hides a failed unit. The per-unit results name each unit, and the job result fails when any unit fails.
- A lease takeover runs a unit twice. The fencing token lets one writer win, and a stale write is refused without a second effect.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the per-kind authorization action for the unit before any start. | Start a state-operation job without the grant; the server refuses it. |
| service | Run each unit under its own lease with a fencing token; refuse a stale write. | Replay a taken-over claim; the second write is refused and changes nothing. |
| domain | Mask secret values in any state output; keep reveal on its own call. | Read job output that holds a secret; the answer shows the marker only. |
| store | Carry the fence and the maintenance check on each job write. | Write a job row without a fence; the store refuses it. |
