---
version: 1
---

## Why

**Persona: developer.** Developer runs a job with several steps and scanners and needs to see what each step did, where approvals pause the job, and what each scanner found.

The user path this change must make work:

1. **Console.** Developer runs a job with several steps, then reads per-step results and scanner findings.
2. **API.** Developer approves or refuses a paused approval step when the job waits.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Step failed: one step reports a nonzero exit and the job stops at that step. | The server names the failed step and the job stops at that step. Rerun the job after the fix. | `make e2e` |
| Approval refused: the approver has no fresh authentication or no grant to approve. | The server refuses the approval without a fresh authentication and changes nothing. Sign in again and repeat the approval. | `make e2e` |

Evidence for the claims above:

- The roadmap lists RFC 110 as waiting on runs in milestone 5. Proof: docs/roadmap.md:814
- The pack proves no step result or scanner coverage on main: its only main tests are a backup check and a scheduler delay. Proof: pkg/backup/pre_backup_owner_internal_test.go:194 (TestCheckPreBackupResultRefusesBeforeTheCopy)

## What Changes

- Coarse placeholder for step results, hooks, approval steps and scanners. It waits on runs in milestone 5.

Out of scope:

- Run model, run queue, run limits and executors
- Job kinds for state operations
- Existing CI as executor, Kubernetes execution, drift detection

Depends on:

- runs (milestone 5)

## Usability

Where the path still costs the person time or surprises them:

- No user path changes until the RFC is refined.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main has a pre-backup check test that refuses before the copy. (`pkg/backup/pre_backup_owner_internal_test.go:194 (TestCheckPreBackupResultRefusesBeforeTheCopy)`)
- Main has a scheduler test that waits the first delay before its first claim. (`pkg/durable/scheduler_test.go:175 (TestSchedulerWaitsTheFirstDelayBeforeItsFirstClaim)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 305: Our own Kubernetes operator, modeled on the Pulumi Kubernetes Operator** (owner-tagged (all decisions)).

- Decisions that apply: 3 (current), 4 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The approval happens in OpenDustpan, not in the cluster."
- Quote: "The work runs through OpenDustpan jobs and gates (owner). The operator does not run a tool."
- Effect: Approvals stay in OpenDustpan and operator work runs as OpenDustpan jobs and gates. Step results and scanner gates refine that rule and add no cluster side approval.
- Conflict: none.

**ADR 212: The planner role, SMTP settings, fresh MFA for weaker settings and approvals, trusted proxies, restore audit entries, the branding preset and one settings API** (owner-tagged: decision 3).

- Decisions that apply: 3 (changed).
- Changed by: ADR 227 changes decision 3; ADR 232 changes decision 6; ADR 235 changes decision 6; ADR 301 extends decision 2; ADR 365 changes decision 5. Chain to the end: ADR 227, ADR 232, ADR 235, ADR 301, ADR 365, ADR 320, ADR 339, ADR 354, ADR 288, ADR 332.
- Current rule: this ADR, with ADR 227, 232, 235, 301, 365.
- Quote: "Fresh MFA for a change that reduces security, and for approvals."
- Effect: An approval step is an approval and needs a fresh authentication.
- Conflict: none.

**ADR 227: One fresh-authentication window** (owner-tagged: decision 2).

- Decisions that apply: 2 (current).
- Changed by: ADR 301 extends decision 1; ADR 320 extends decision 1; ADR 339 extends decision 6; ADR 354 extends. Chain to the end: ADR 301, ADR 320, ADR 339, ADR 354, ADR 332.
- Current rule: this ADR, with ADR 301, 320, 339, 354.
- Quote: "One window everywhere (owner). The installation has one setting, `freshAuthSeconds`, with the default 900 (15 minutes)."
- Effect: Approval steps use the one fresh authentication window.
- Conflict: none.

**ADR 41: The server reads state and masks secrets** (not owner-tagged).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: ADR 60 changes decisions 5 and 13; ADR 103 changes decision 13; ADR 146 extends decision 13; ADR 151 changes decision 10; ADR 238 changes decision 12; ADR 240 changes decisions 9 and 13; ADR 240 extends decision 5; ADR 352 extends decision 10. Chain to the end: ADR 60, ADR 103, ADR 146, ADR 151, ADR 238, ADR 240, ADR 352, ADR 63, ADR 64, ADR 65, ADR 68, ADR 72, ADR 126, ADR 210, ADR 334, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 149, ADR 262, ADR 297, ADR 154, ADR 217, ADR 220, ADR 315, ADR 356, ADR 354, ADR 124, ADR 216.
- Current rule: this ADR, with ADR 60, 103, 146, 151, 238, 240, 352.
- Quote: "The server may read the contents of every state that it stores, for each tool."
- Quote: "A read masks secret values. A state read answers the structure and the values of a state. It replaces each secret value with a marker."
- Effect: Step output and scanner findings mask secret values like a state read.
- Conflict: none.

**ADR 72: Scale out on one database with durable operations** (not owner-tagged).

- Decisions that apply: 2 (current), 4 (current).
- Changed by: ADR 73 changes decision 7; ADR 75 extends decision 10; ADR 103 changes decision 6; ADR 126 changes decision 6; ADR 151 extends decision 5; ADR 166 extends decision 1; ADR 240 extends decision 6; ADR 249 extends decision 11; ADR 280 extends; ADR 298 extends. Chain to the end: ADR 73, ADR 75, ADR 103, ADR 126, ADR 151, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 322, ADR 110, ADR 128, ADR 138, ADR 141, ADR 142, ADR 146, ADR 148, ADR 190, ADR 212, ADR 213, ADR 222, ADR 224, ADR 225, ADR 227, ADR 299, ADR 129, ADR 154, ADR 210, ADR 217, ADR 220, ADR 286, ADR 297, ADR 354, ADR 281, ADR 302, ADR 337, ADR 325, ADR 326, ADR 362.
- Current rule: this ADR, with ADR 73, 75, 103, 126, 151, 166, 240, 249, 280, 298.
- Quote: "A lease per operation (L1). Each claimed operation has an expiry on the database clock."
- Quote: "A write-path installation check. Every write transaction reads the installation state in the same statement that reads its namespace fence."
- Effect: Step results and approvals are written under a lease with a fencing token and pass the installation write check.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What is one step result and how are secrets in step output and scanner findings masked, sealed and redacted?
   - Options: Per-step records with exit status, masked output and scanner findings; Job level result only.
   - Provisional decision (owner review 14 Nov): Per-step records with exit status, masked output and scanner findings
   - Reason: Per-step records keep the audit trail complete and ADR 41 requires masking of secret values. Benchmark: SOC 2: per-step change records with masked secrets
   - Persona impact: Developer sees one shape for every run. Escape hatch: Ship masked records first and add output references later.
2. How does an approval step pause a job, who may approve, and does self-approval count?
   - Options: Gating approval step in the job with named approvers and a fresh authentication; Approval outside the job only.
   - Provisional decision (owner review 14 Nov): Gating approval step in the job with named approvers and a fresh authentication
   - Reason: Approvals happen in OpenDustpan and ADR 212 with ADR 227 requires a fresh authentication for approvals. Benchmark: SOC 2: visible change approval with fresh authentication
   - Persona impact: Developer sees a visible pause with one sign-in path. Escape hatch: A grant holder may skip the step and the skip writes an audit entry.
   - Owner-tagged ADRs involved: ADR 305, ADR 212, ADR 227 (their words do not change).
3. What happens on runner crash, lease takeover, double write and restart at an approval step?
   - Options: Lease and fencing token decide one writer; a stale write is refused; Last write wins.
   - Provisional decision (owner review 14 Nov): Lease and fencing token decide one writer; a stale write is refused
   - Reason: Lease and fencing token make one writer win without split effects. Benchmark: ISO 27001: single execution of each recovery step
   - Persona impact: Developer sees one result for each step after a crash. Escape hatch: A retried write answers the stored result without a second effect.
4. Do step records, approvals and findings carry the namespace fence, and what do restore and maintenance mode do to them?
   - Options: Step records, approvals and findings carry the namespace fence and pass the installation write check; a restore keeps them; No fence for step records.
   - Provisional decision (owner review 14 Nov): Step records, approvals and findings carry the namespace fence and pass the installation write check; a restore keeps them
   - Reason: Namespace data carries the fence and maintenance mode refuses writes. Benchmark: ISO 27001: writes carry tenancy checks and stay refused in maintenance
   - Persona impact: Developer sees refused writes with one retry command. Escape hatch: Reads stay available while writes are refused.
5. How does a job requested through the operator show step results, and can a scanner gate stop it?
   - Options: Scanner gate can stop any job including one the operator requested through the REST API; Scanner gate skips operator requested jobs.
   - Provisional decision (owner review 14 Nov): Scanner gate can stop any job including one the operator requested through the REST API
   - Reason: Operator work runs through OpenDustpan jobs and gates with the same rules. Benchmark: PCI DSS: policy checks apply to every change before apply
   - Persona impact: Operator sees the same gate for every job. Escape hatch: An operator requested job runs with its requested scanner set recorded.
   - Owner-tagged ADRs involved: ADR 305 (their words do not change).

Questions considered and answered:

- Does an accepted ADR already decide step results, hooks, approval steps or scanners? No. ADR 305 decisions 3 and 4 place approvals and work in OpenDustpan jobs and gates. ADR 212 decision 3 and ADR 227 decision 2 require a fresh authentication for approvals. ADR 41 decisions 1 and 2 require masking. ADR 72 decisions 2 and 4 require leases and the write check. The refined RFC must follow them.
- What must exist before step results and scanners can be specified? Runs in milestone 5 with jobs, claims, leases, fences and gates. This change stays coarse until that model ships and the RFC is accepted.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
