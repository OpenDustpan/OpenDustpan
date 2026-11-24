---
version: 5
---

Issue: #3931.

Roadmap: `docs/roadmap.md:475` (Add long and short outage steps with retry measurement.).

## Why

**Persona: operator.** Operator runs three instances on one PostgreSQL database and must prove that a database outage is safe. The operator stops the database while tofu apply and pulumi up run, checks that each instance answers 503 with Retry-After, measures how long each CLI version retries, and checks that work recovers with no half-written state and no stuck lock.

The user path this change must make work:

1. **E2e.** Start three serve instances behind the round-robin proxy on PostgreSQL and run tofu apply and pulumi up through the proxy
2. **E2e.** Stop PostgreSQL for 60 s at the database proxy, poll GET /health/ready on each instance, and record per-version retry time and failure text
3. **CLI.** Cancel the stopped Pulumi update, rerun both commands, then run pulumi preview and tofu plan to show no changes
4. **E2e.** Stop PostgreSQL for the short window, 2 s before the Pulumi step ends. A write kind that retries (window above 0 s) lets its command end with exit 0. A write kind that does not retry (window 0 s) makes its command fail at once and clean, and the next run of the same command ends with exit 0 and exactly one new state version

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Command fails during the 60 s outage | API writes answer 503 with Retry-After and the error shape of each API during the outage. | `curl -i https://proxy/health/ready` |
| Pulumi update stopped by the outage stays running | The update stays running until `pulumi cancel` ends it, then the same commands run again and complete. | `pulumi cancel --stack <namespace>/<project>/<stack>` |
| Pulumi fails at once with "failed to save snapshot" in a short outage | The proxy answers `503 no healthy instance` to the end-of-step checkpoint save, and the CLI does not retry that write. The command exits non-zero, leaves no corrupt state and no lock, and the same command runs again with exit 0. | `pulumi up --stack <namespace>/<project>/<stack>` |
| Workspace locked after a failed apply during the outage | A failed command leaves no half-written state version and no lock on the workspace. | `tofu plan` against the same workspace |

Evidence for the claims above:

- Main has the OpenTofu state version write path that the probe exercises Proof: internal/api/tfe/states.go:602 (func Handler.writeStateVersion)
- Main has the workspace lock and unlock handlers that the probe checks for no leftover lock Proof: internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)
- Main has the readiness and liveness routes that the probe polls on each instance Proof: GET /health/ready in route table or API description
- Main has the suite helpers for tofu apply, tofu plan, and pulumi preview steps Proof: pkg/dustpan/jobservice.go:890 (func tofuApplySteps)

## What Changes

- Add long outage step 7a: 60 s PostgreSQL stop during tofu apply and pulumi up, with 503 readiness and write assertions, retry measurement per CLI version, cancel, recovery, and no-change preview and plan checks
- Add short outage step 7b: stop below the smallest retry window above 0 s with margin. For each write kind with a window above 0 s the step asserts exit 0. For each write kind with a window of 0 s the step asserts a clean failure: non-zero exit, no corrupt state, no lock, and a next run with exit 0 and exactly one new state version
- Report per-version retry windows for each write kind (checkpoint, events, complete and the OpenTofu state version). A kind that the CLI does not retry reports 0 s
- Leave no half-written state version and no workspace lock

Out of scope:

- Whether an instance stays in the proxy pool during a short database outage, so that the CLI gets the server's own 503 shape and not the proxy's (an open question for the owner, filed in the decision queue, not decided here)
- 503 with Retry-After server behavior
- Atomic state version and lock commits with fencing
- PostgreSQL failover readiness and guides
- Rolling deploy and other exit test steps

Depends on:

- database-outage-503-shape
- database-outage-atomic-state-locks
- `database-outage-atomic-state-locks` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- The operator waits 60 s plus recovery for the long step with no progress output unless the probe logs each check
- Retry windows differ by CLI version and by write kind, and the report must state the version and the write kind with each number
- Measured on 3 instances with PostgreSQL 18: each tested version (Pulumi 3.262, 3.250, 3.200 and OpenTofu 1.12, 1.11, 1.10) retries a refused write for 28 to 30 s in the long outage, and Pulumi does not retry the checkpoint save at the end of a step

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Suite helpers run tofu apply steps (`pkg/dustpan/jobservice.go:890 (func tofuApplySteps)`)
- Handler writes a state version in the TFE-compatible API (`internal/api/tfe/states.go:602 (func Handler.writeStateVersion)`)
- Handler locks a workspace (`internal/api/tfe/locks.go:134 (func Handler.lockWorkspace)`)
- Handler unlocks a workspace (`internal/api/tfe/locks.go:266 (func Handler.unlockWorkspace)`)
- Suite helper runs a pulumi preview step (`pkg/dustpan/jobservice.go:944 (func pulumiPreviewStep)`)
- Suite helpers run tofu plan steps (`pkg/dustpan/jobservice.go:875 (func tofuPlanSteps)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. What short outage duration gives margin below the smallest per-version retry window above 0 s measured in 7a?
   - Options: 50 percent of the smallest measured retry window above 0 s; Smallest measured retry window minus 10 s; Fixed 5 s short outage.
   - Provisional decision (owner review 14 Nov): 50 percent of the smallest measured retry window above 0 s
   - Reason: A fixed fraction scales with slow and fast CLI versions and keeps a clear margin. Benchmark: AWS practice for fault injection tests: pick the short outage as a fixed fraction of the smallest observed retry window with a stated safety margin.
   - Persona impact: No persona impact. The choice affects only suite time and flake risk. Escape hatch: The short outage duration is a test setting, so no persona is blocked if it changes.

Questions considered and answered:

- Does the short outage always end with exit 0? No. A local run measured that Pulumi fails at once on the end-of-step checkpoint save when the proxy answers `503 no healthy instance`, although every instance answers readiness 200 at the first poll after the database returns and the proxy passes requests again in 0.8 to 2.1 s. This version of the change measures the window for each write kind and asserts survival only where the window is above 0 s.
- Does this part change API error shapes or storage commits? No. The pack scope says this part adds only suite steps, fixtures, and measurements. It uses the 503 behavior from database-outage-503-shape and the atomic commits from database-outage-atomic-state-locks and adds no server code.
- What does the probe cover and how does it check no half-write and no lock? Yes. The roadmap step runs tofu apply and pulumi up through the proxy, checks cancel, recovery, and no-change preview and plan, and measures per-version retry windows. The method for no half-written state is a version list before and after, and the method for no lock is a lock read before and after.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
