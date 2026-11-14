---
version: 1
---

Issue: #2712.

## Problem

Stuck claims, unclear cancel outcomes, wrong refusal codes, repeated namespace lookups, weak plan secrecy checks, cross-tool drift steps and a flaky lease test waste the time of a runner operator and hide real faults.

## Evidence

- pkg/job/claimer.go exists on main Proof: paths: pkg/job/claimer.go exists true
- pkg/job/recorder.go and pkg/job/record.go exist on main Proof: paths: pkg/job/recorder.go exists true; pkg/job/record.go exists true
- pkg/job/driftsteps.go and pkg/runner/executor.go exist on main Proof: paths: both exist true
- pkg/store/internal/jobstore/record.go exists on main Proof: paths: pkg/store/internal/jobstore/record.go exists true
- pkg/durable/durabletest/durabletest.go exists on main Proof: paths: pkg/durable/durabletest/durabletest.go exists true
- CheckPlanBody exists on main Proof: pkg/store/internal/jobstore/record.go:819 (func CheckPlanBody)

## Fix boundary

- Distinguish permanently missing namespaces from transient fence-read failures in pkg/job/claimer.go with a confirming second read
- Document cancel versus job-fail outcome in pkg/job/store.go and pkg/job/claimer.go
- Classify job-token start rejection as policy refusal in pkg/job/service_start.go
- Reuse the resolved namespace ID across chunk stores in pkg/job/recorder.go within one claim
- Enforce the plan-part secrecy rule in pkg/job/record.go and pkg/store/internal/jobstore/record.go
- Scope the drift-step allowlist by unit tool in pkg/job/driftsteps.go and pkg/runner/executor.go
- Give testExtend a 3 to 5 second initial lease in pkg/durable/durabletest/durabletest.go with expiryWait unchanged

Out of scope:

- Other October review batches for API handlers, docs, domain packages, e2e, generated clients, identity and access
- New job features, new runner protocol and new drift behavior beyond the named fixes
- internal/servertest/roundtrips_test.go
- internal/servertest/roundtrips_model_test.go
- internal/servertest/roundtrips_conflict_test.go

## Check

- Add the NEW test `TestMissingNamespaceDisposition` in pkg/job/jobtest/claimer.go for confirmed missing versus transient namespace disposition, with and without cancellation Check: NEW `go test ./pkg/job -run TestMissingNamespaceDisposition -count=1` fails on main
- Add the NEW test `TestJobTokenStartRefusal` for job-token start policy refusal and an ordinary missing grant Check: NEW `go test ./pkg/job -run TestJobTokenStartRefusal -count=1` fails on main
- Add the NEW test `TestChunkNamespaceReuse` as an instrumented test for namespace lookup count, retries, separate namespaces and mid-claim delete Check: NEW `go test ./pkg/job -run TestChunkNamespaceReuse -count=1` fails on main
- Add the NEW test `TestPlanPartSecrecy` for plan-part secrecy through PutPlan and CheckPlanBody including reservation restore and a legacy body Check: NEW `go test ./pkg/job -run TestPlanPartSecrecy -count=1` fails on main
- Add the NEW test `TestPlanPartSecrecyStore` for plan-part secrecy through the jobstore write path Check: NEW `go test ./pkg/store/internal/jobstore -run TestPlanPartSecrecyStore -count=1` fails on main
- Add the NEW test `TestDriftStepAllowlist` for tool-scoped drift-step allowlists including empty and mixed lists Check: NEW `go test ./pkg/runner -run TestDriftStepAllowlist -count=1` fails on main
- Add the NEW test `TestExtendLease` lease case with a 3 to 5 second initial lease and unchanged expiryWait on sqlite and postgres, in the file pkg/durable/durabletest/durabletest.go Check: NEW `go test ./pkg/durable/durabletest -run TestExtendLease -count=1` fails on main on sqlite and postgres
- Update the cancel comment in pkg/job/store.go and pkg/job/claimer.go to distinguish the ended operation from the failed job and agree with testExpiryDuringCancel Check: `rg -n 'canceled operation' pkg/job/store.go pkg/job/claimer.go` shows the updated comment

Depends on:

- `architecture-review-7-nov-no` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The job store record file already validates plan bodies through CheckPlanBody (`pkg/store/internal/jobstore/record.go:819 (func CheckPlanBody)`)
- The job claimer already defines a namespace reader seam (`pkg/job/claimer.go:19 (type NamespaceReader)`)
- The job test help already defines missing-namespace cases (`pkg/job/jobtest/missingnamespace.go:21 (func missingNamespaceCases)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does an ADR decide any of the seven follow-ups? No ADR in the pack decides fence reads, job-token refusal class, plan secrecy, drift allowlists or durable test leases, so this change follows the issue text at the named sites.
- Does the change add a store port method? No. Fixes stay inside existing functions at the named files. Conformance cases are added only in pkg/job/jobtest for the claimer disposition.
- Why do the named tests not resolve on main? Each named NEW test does not exist on main. The task text marks it NEW in the same sentence, so Open questions reads none.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
