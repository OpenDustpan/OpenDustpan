## Context

The job start path checks the caller action per operation in one mapping function. A plan maps to unit:run-plan. An apply and a destroy map to their write actions. An unknown operation is refused before any authorization check. Branch protection is read once at job start for the exact commit named in the request.

## Decisions

1. **Authorize by operation.** Check authorization at job start by operation: plan maps to unit:run-plan, apply and destroy map to their write actions, unknown operations fail closed with 400 and create no job.
2. **Gate secrets on named commit.** Gate planner plan secrets on the named commit: read branch protection of that commit at job start and open unit secrets only when that commit is on a protected branch; a read error or timeout runs masked with no secret value in the log.
3. **Order the writes.** Keep the operation check and the queue insert in one store transaction; write the audit entry after commit as a follow-up record that never blocks or rolls back the job decision, and recheck stored grants at each plan read.

## Structure

Authorization stays in the job service with unexported field operationActions set only by newJobService; the constructor checks the mapping covers plan, apply and destroy, so a new operation cannot skip the mapping and fails closed.

## Risks

- A workstation plan reads state with state actions outside this check; the docs page warns about this caveat and names the state actions.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| HTTP handler | Handler checks bearer token and requires unit:run-plan for plan jobs, the apply action for apply jobs, the destroy action for destroy jobs, and refuses unknown operations with 400. | Send plan, apply, destroy and unknown operations as planner directly to the handler and confirm only plan gives 201. |
| service | Job service rechecks the action for the operation and object before queue, independent of the handler, and reads branch protection of the named commit at job start before it opens secrets; an error or timeout opens the masked profile. | Call the service with a planner principal for apply and for an unprotected or unreadable commit and confirm refusal and masked open even when the handler check is bypassed. |
| port adapter | Store grant check resolves only stored grants for the planner role and never adds an implicit apply, destroy or secret-read grant; changeorder:author comes from a stored planner grant. | Grant planner only, resolve reach for apply, destroy and secret read, and confirm denial, and confirm changeorder:author is allowed. |
