## Context

A developer runs infrastructure against OpenDustpan and needs to know when real resources drifted from state, what the last check found, and the one next step to refresh it. Drift detection waits on runs, and no executor exists yet.

This change refines the drift detection RFC: trigger, executor, schedule setting, result shape, and audit entries. It keeps the existing drift error code and API shape unchanged until the RFC is accepted. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** The manual check ships first; scheduled checks ship as a plan job kind after runners land (from 109-job-kinds-state-operations). Without an executor the schedule cannot run.
2. **A drift check runs as a plan job kind.** It adds no new job kind. If checks run as durable work, they run only on the primary installation under a claim lease with a fencing token (ADR 248, decision 14; ADR 72, decision 2).
3. **The drift read answers status plus last-check time and summary only.** No full resource diff and no secret values. Tool models stay separate, and state reads mask secrets (ADR 41, decision 2). Reveal stays a separate call.
4. **The drift schedule setting lives in the one settings resource** with effective value and source shown (ADR 212, decision 7). The default is off.
5. **Each check writes an audit entry with actor and schedule source.** Automatic work always leaves a record of who or what did it and why.
6. **Keep the prior result and mark it stale on failure, timeout, failover, or missing grant.** Refuse without a grant and answer a concurrent start with 409 and `drift.start-conflict`. A failed check never hides the prior result.

## Structure

No new structure now. The bug class is a drift result that carries a plain secret or a second schedule store beside the settings resource; the summary-only masked shape plus the one settings resource is the structure that removes it. When the run model ships, a drift result is an unexported record built only through its constructor, which checks the masked summary and the fence.

No custom analyzer and no new tool. A later refinement names the result shape and the constructor. The existing `drift.start-conflict` code and the API description test stay unchanged.

## Risks

- Developers see stale state with no schedule until runners land. The manual check stays, and the read names the last-check time.
- A failover runs a scheduled check twice. Primary-only execution plus the claim lease lets one worker win; the loser ends without a write.
- A concurrent start looks like a hung check. The 409 names the conflict and the read shows the running check.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Check the drift read grant for the unit; refuse without it; answer a concurrent start with 409 and `drift.start-conflict`. | Start a second check while one runs; the answer is 409 with the named code. |
| service | Run scheduled checks on the primary installation only, under a claim lease with a fencing token. | Start the scheduler on a secondary; no check starts there. |
| domain | Build the summary from the masked state form; never carry a plain secret value. | Drift a field that holds a secret; the summary shows the marker only. |
| store | Carry the fence and the maintenance check on each drift write; keep the prior result on failure. | Fail a check after a good one; the read still shows the prior result marked stale. |
