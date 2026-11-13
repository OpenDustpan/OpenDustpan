## Context

The planner role is a data role in the existing access model. No new store table and no new grant shape is needed. The built-in role table and the per-route action checks carry the behavior. The server plan-job start for planners stays closed until milestone 5 enforces the protected-branch gate.

## Decisions

1. **Planner as role-table entry with secret deny.** Define planner in the built-in role table as viewer actions plus unit:run-plan, job:read-plans and changeorder:author, with no secret reveal action. Reuse grant evaluation, masked state read and the audit writer.
2. **Protected-branch gate stated now, server start closed until milestone 5.** State the protected-branch gate for planner-started plan jobs now and refuse planner plan-job start on the server until milestone 5 enforces the gate. Local plan and preview on a workstation read masked state through the same state actions; the exact local-plan display when a value is secret is an open question.

## Structure

Built-in role table with unexported roleActionSet built only by NewRoleActionSet, which checks that planner holds exactly viewer actions plus unit:run-plan, job:read-plans and changeorder:author and no secret action.

## Risks

- A handler declares a wrong action and lets a planner write.
- A secret read path forgets the reveal check, including for old state versions and for secrets inside a state file the planner reads.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| service | Access service allows only viewer reads plus unit:run-plan, job:read-plans and changeorder:author for planner holders and refuses secret reveal and non-plan job start. | Call reveal and non-plan job start as planner directly on the service; each call fails with 403 and writes an audit entry. |
| HTTP handler | Pulumi, TFE-compatible, OpenTofu HTTP backend and tool-neutral handlers declare read actions for reads, write actions for apply and destroy, the reveal action for secret reads including state history and export routes, run-plan for plan start, and token and settings actions for those routes; each refusal is 403 with a fixed code. | Send pulumi up, pulumi destroy, tofu apply, tofu destroy, secret reveal, state history export, settings write, token create for another principal and non-plan job start with a planner token; each gets 403. |
| port adapter | Store adapters enforce the same action check in the transaction with the namespace fence and mask secret values at read time for every state version including pre-change versions. | Attempt a write query with planner reach and read a state version written before the change as planner; the write is refused and the read shows masked placeholders. |
