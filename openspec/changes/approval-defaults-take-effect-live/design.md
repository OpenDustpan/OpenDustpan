## Context

Slice 1 stored approval defaults and rules and left pkg/gate/jobgate tested in isolation and wired nowhere. This change wires the existing Decider into the job service path from #968 with gate evaluation from #899 so stored defaults take effect on live Pulumi and OpenTofu jobs at the fixed decision points.

## Decisions

1. **Reuse jobgate.Decider at propose, planned and apply.** Wire the existing jobgate.Decider at the job service points that map to change.propose at job start, change.planned after the plan is stored, and change.apply before dispatch of the apply phase; state.write, profile.save and profile.open stay with their own changes.
2. **Hold and resume on the latest requirement set.** Hold a job that gains a requirement in approval wait and resume it only when the stored signatures meet every requirement of the latest evaluation; a recheck only adds so removal of a default while in wait does not release the wait. Distinct-approver counting, stale-revision reset and break-glass dev-data rules live in their own changes.

## Structure

The unexported gate evaluation state lives behind jobgate.Decider which only its constructor builds after it checks the unit reference and the fact document, so callers cannot construct a partial approval gate.

## Risks

- A job started before a default save uses the facts at evaluation time per the recheck rule.
- Two tools share one wait semantic with tool-specific copy only.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| port adapter | Decider input validation rejects unknown unit reference and empty subject before evaluation. | Call the Decider with an empty subject and a malformed unit reference and check refusal without engine call. |
| service | Job service checks the grant for the job action on the unit object at the point of use. | Start an apply job as a principal with only plan rights and check 403 even when gates allow. |
| HTTP handler | Tool handlers recheck the stored decision record version before state write and refuse a stale approval. | Replay an approval for a prior plan revision against a new plan and check refusal with the record named. |
