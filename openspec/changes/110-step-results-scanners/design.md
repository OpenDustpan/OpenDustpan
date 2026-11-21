## Context

A developer runs a job with several steps and scanners and needs to see what each step did, where approvals pause the job, and what each scanner found. No step result or scanner coverage exists yet, and the shape waits on runs in milestone 5.

This change is a coarse placeholder until the run model ships. It changes no server code, no route, and no schema now.

## Decisions

1. **No code now.** Step results, hooks, approval steps, and scanners ship with the run model. Jobs keep their current result shape until then.
2. **Per-step records with exit status, masked output, and scanner findings.** A job-level result alone would hide which step failed and what each scanner found. Step output and scanner findings mask secret values like a state read (ADR 41, decisions 1 and 2).
3. **A gating approval step with named approvers and a fresh authentication.** Approvals happen in OpenDustpan, and operator work runs as OpenDustpan jobs and gates (ADR 305, decisions 3 and 4). An approval is a change that reduces security posture only when granted, so it needs the one fresh-authentication window (ADR 212, decision 3; ADR 227, decision 2). A grant holder may skip the step, and the skip writes an audit entry.
4. **One writer wins by lease and fencing token.** A stale write after runner crash, lease takeover, or restart is refused (ADR 72, decision 2). A retried write answers the stored result without a second effect.
5. **Step records, approvals, and findings carry the namespace fence and pass the installation write check** (ADR 72, decision 4). A restore keeps them; maintenance mode refuses new writes while reads stay available.
6. **A scanner gate can stop any job, including one the operator requested through the REST API.** Policy checks apply to each job the same way.

## Structure

No new structure now. The bug class is a step result or finding that carries a plain secret or a missing approver; per-step records with masked output plus a gating step with named approvers is the structure that removes it. When the run model ships, a step result is an unexported record built only through its constructor, which checks the exit status, the masked output, and the approver set.

No custom analyzer and no new tool. A later refinement names the record shape, the scanner finding shape, and the constructor.

## Risks

- Developers see no per-step results until milestone 5 ships. The docs page says so and points at job logs instead.
- A crashed runner at an approval step leaves the job paused. The lease decides the one writer, and the same approver repeats the step after sign-in.
- A scanner finding that holds a secret leaks through the API. Masking applies at write time and again on reads, so a missed mask at one layer still hides the value.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| handler | Refuse an approval without a fresh authentication and without the grant; change nothing on refusal. | Approve with an old session; the server refuses and the job stays paused. |
| service | Decide the one writer by lease and fencing token; refuse a stale write; record a skip with its grant holder. | Replay an approval write after takeover; the second write is refused. |
| domain | Mask secret values in step output and scanner findings at write time. | Store output that holds a secret; the stored form shows the marker only. |
| store | Carry the fence and pass the installation write check on each step, approval, and finding write. | Write a step row without a fence; the store refuses it. |
