# 378. The binding of an uploaded plan

Status: accepted · 2026-11-14. Decided by the project owner

Changes:

- Extends ADR 307 decision 2: how the apply of an uploaded plan presents the same plan hash.
- Extends ADR 102 decision 7: the binding of a plan that a team uploads, with the state version that the server records at upload.
- Extends ADR 369 decision 1: the local wrapper presents the plan hash when it opens its apply claim.

## Context

ADR 307 decision 2 (owner, 2026-10-18) says: "Own-CI teams: accept uploaded
plans". The owner said also: "the apply must present the same plan hash". The
owner said also: "weaker than a runner plan, and marked so in the change
order".

ADR 102 decision 7 binds a runner plan: "A gate with `BindPlan` binds each
approval to the plan: its ID, the two hashes and the state version". For a
runner plan, the plan phase records the state version that the plan read. For
an uploaded plan, no server phase reads the state, so a rule must say where
the state version comes from.

ADR 369 decision 1 (owner, 2026-11-14) says: "A TFE-compatible request names
its apply claim through the job token. The local wrapper gets a launcher
token that is bound to its apply claim."

The real CLIs cannot send a field that they do not know. The TFE state write
carries the checksum, the serial and the lineage of the state. It carries no
plan hash.

Benchmark: an AWS CloudFormation change set execution names the reviewed
change set, and a change to the stack after that makes the change set fail.
ISO 27001 Annex A 5.3 (segregation of duties) supports a rule of no
self-approval.

The OpenSpec change `accept-uploaded-plans-team-s` stopped at task 3.1 on
this ADR. The project owner answered on 14 November 2026: "3. A".

## Decision

1. **An uploaded plan binds to both part hashes and to the state version at
   upload (owner, 2026-11-14).** At upload, the server records the SHA-256 of
   each of the two plan parts and the current state version of the unit. The
   approval binds to the plan ID, the two hashes and that state version, as
   ADR 102 decision 7 says for a runner plan.
2. **A state write after the upload makes the plan stale (owner,
   2026-11-14).** Any state write to the unit after the upload makes the
   uploaded plan stale. An apply of a stale plan fails with 409. The error
   tells the person to upload the current plan.
3. **The apply presents the hash through its apply claim (owner,
   2026-11-14).** The apply presents the plan hash when it opens its apply
   claim through the local wrapper, with the launcher token of ADR 369
   decision 1. The hash is not in the tool request. The server refuses to
   open the claim when the hash is not the hash of the approved plan.
4. **No self-approval and no destroy plan for an upload (owner,
   2026-11-14).** The person who uploaded a plan never approves it. The
   server refuses an uploaded destroy plan.

## Consequences

- A team that uploads plans runs the apply through the local wrapper.
- The change order marks an uploaded plan as weaker than a runner plan, as
  ADR 307 decision 2 says.
- The change `accept-uploaded-plans-team-s` follows this ADR. The upload
  route, the authorization actions, the e2e step with the real OpenTofu CLI
  and the docs page are its work.
- This is security code. Each decision gets hostile tests at the store, the
  service and the HTTP handler.
- This ADR does not decide how the server detects a destroy plan, or the
  exact text of the 409 error. The spec of the change decides them.
