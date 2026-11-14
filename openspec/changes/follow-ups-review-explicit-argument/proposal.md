---
version: 1
---

Issue: #2416.

## Problem

Problem: after the review of the explicit-argument area files, five small gaps remain. The NewServer chain test checks only a prefix of the chain. It does not see the token gate inside identity.Chain. The guard tests check the whole chain, so a failure does not name the guard. The entries field name does not say what the entries are. A worker that runs only backups has no heartbeat.

## Evidence

- No non-test code names assertSomeCall Proof: rg -n -F --glob '!*_test.go' 'assertSomeCall' pkg internal cmd matches 0
- The token gate is added to identity.Chain in the sign-in area Proof: pkg/dustpan/area_signin.go:127: return identity.Chain{next, gate}, gates, nil
- The text profileInputs.entries is not in non-test code Proof: rg -n -F --glob '!*_test.go' 'profileInputs.entries' pkg internal cmd matches 0

## Fix boundary

- The chain test compares the whole NewServer chain and expects the token gate inside identity.Chain
- One lone unit test stays for the dev guard alone
- The entries field of profileInputs gets a name that says what the entries are
- The backup-only worker gets the worker watch (provisional decision: yes)

Out of scope:

- A change of wrapper order or guard logic
- New backup behavior beyond the heartbeat wiring

## Check

- Write TestNewServerChainFull in `pkg/dustpan/dustpan_test.go` covering NewServer in `pkg/dustpan/dustpan.go`: compare the whole chain and expect the token gate inside identity.Chain from `pkg/dustpan/area_signin.go` Check: go test ./pkg/dustpan -run TestNewServerChainFull fails on main
- Write one lone unit test of the dev guard in `pkg/dustpan/devrefusal_internal_test.go` so a failure names the dev guard Check: Lone dev guard test fails when its guard is removed
- Write TestBackupOnlyWorkerHeartbeat in NEW `pkg/dustpan/workerwatch_test.go`: a worker with only backups reports a heartbeat via `pkg/dustpan/workerwatch.go` Check: go test ./pkg/dustpan -run TestBackupOnlyWorkerHeartbeat fails on main
- Run rg search and rename the entries field of profileInputs to valueEntries in `pkg/profile/profile.go`: update each use in the file that rg names Check: rg -n profileInputs `pkg/dustpan/dustpan.go` names the type file, old entries field has no match, package tests pass
- Wire the worker watch from `pkg/dustpan/workerwatch.go` into newBackupService in `pkg/dustpan/area_backup.go` Check: TestBackupOnlyWorkerHeartbeat passes
- Run the touched package tests and make check for the change in `pkg/dustpan/dustpan.go` Confirmed by: CI (make check) Check: make check reports zero findings

Depends on:

- no-magic-namespace-service-principal
- `store-without-installation-minimumchecker-silently` (order of the milestone review)
- `no-magic-namespace-service-principal` (order of the milestone review)

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- NewServer builds the server (`pkg/dustpan/dustpan.go:774 (func NewServer)`)
- The sign-in area builds identity.Chain with the token gate (`pkg/dustpan/area_signin.go:127 (func gatedAuthenticator)`)
- newBackupService builds the backup service (`pkg/dustpan/area_backup.go:108 (func newBackupService)`)
- newWorkerWatch builds the worker watch (`pkg/dustpan/workerwatch.go:81 (func newWorkerWatch)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Must a worker that runs only backups report a heartbeat?
   - Options: Wire the worker watch into the backup service; Leave a backup-only worker with no heartbeat.
   - Provisional decision (owner review 14 Nov): Wire the worker watch into the backup service
   - Reason: A silent backup-only worker looks dead to fleet checks. The watch already exists. Provisional decision (owner review 14 Nov): yes, wire the watch. Benchmark: AWS: each worker reports health, so the fleet can see a silent worker.
   - Persona impact: The Runner operator sees a heartbeat for each backup-only worker. No other persona changes. Escape hatch: An operator can run backups on a worker that runs jobs and has a heartbeat.

Questions considered and answered:

- Does an ADR answer the chain test, the guard tests, the rename or the heartbeat? No. The pack lists no ADR for these points.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
