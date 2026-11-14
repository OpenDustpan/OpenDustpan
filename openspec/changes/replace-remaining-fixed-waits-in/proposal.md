---
version: 1
---

Issue: #2015.

## Problem

Store conformance tests use fixed waits that fail on a loaded machine. The waits must poll a state change and end at one shared per-test limit so results stay stable under load.

## Evidence

- StuckTestLimit is defined in the public storetest package on main. Proof: pkg/store/storetest/item_keys_backup.go:28: const StuckTestLimit = 20 * time.Minute
- No non-test production file uses the listed wait helpers on main. Proof: rg -n -F --glob '!*_test.go' 'waitForDropSchema' pkg internal cmd matches 0; same 0 for waitForSwap, staleFinish, gateWait, lockWaitLimit, checkRecovered, waitBackupEnd
- No non-test production file uses storetest.StuckTestLimit on main. Proof: rg -n -F --glob '!*_test.go' 'storetest.StuckTestLimit' pkg internal cmd matches 0

## Fix boundary

- Each listed store test wait polls a state change and ends at one shared per-test deadline based on StuckTestLimit.
- Each test shares one deadline across its helpers so a stuck test waits once.
- StuckTestLimit keeps its exported name pending the owner decision and is documented as test-only.

Out of scope:

- Production retry, timeout, or restore logic
- New store conformance cases

## Check

- Write the failing tests TestPollsPassUnderLoad, TestPollTimeoutNamesWaitAndState, TestPollRetriesTransientNotReady, TestPollFailsFastOnClosedDatabase and TestPollStopsAtCancellation in NEW `pkg/store/storetest/poll_test.go`. Check: `go test ./pkg/store/storetest/ -run 'TestPoll'` fails on main before the fix.
- Replace waitForDropSchema, waitForSwap, staleFinish, gateWait, and lockWaitLimit in pkg/store/storetest with poll loops on state change that end at the shared per-test deadline. Check: `rg -n 'waitForDropSchema|waitForSwap|staleFinish|gateWait|lockWaitLimit' pkg/store/storetest` shows each helper polls with the shared deadline; no fixed 10 second, 30 second, or 1 minute wait remains.
- Share one per-test deadline in checkRecovered in pkg/store/storetest so waitBackupEnd plus the operation loop end once and stop at test cancellation. Check: `rg -n 'newTestDeadline|testDeadline' pkg/store/storetest` shows one deadline passed into waitBackupEnd and the operation loop.
- Document StuckTestLimit in pkg/store/storetest as test-only pending the owner export decision. Check: `rg -n 'StuckTestLimit' pkg/store/storetest` shows the doc comment.
- Run the store conformance tests in pkg/store/storetest under load several times beside another package. Check: `go test -race -count=3 ./pkg/store/storetest/ -timeout 60m` passes beside `go test ./pkg/backup/`, with every old assertion still running.
- Run full checks and confirm every assertion from the old waits in pkg/store/storetest still runs. Check: `make check` passes with zero findings. Confirmed by: CI (make check).

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines the shared wait limit StuckTestLimit in the storetest package. (`pkg/store/storetest/item_keys_backup.go:28 (const StuckTestLimit)`)
- Main defines the StuckTestLimit value as 20 minutes in the storetest package. (`pkg/store/storetest/item_keys_backup.go:28 (const StuckTestLimit)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Should StuckTestLimit stay exported from the public storetest package for SDK adapter authors, or move to an internal helper?
   - Options: Keep StuckTestLimit exported from pkg/store/storetest with a test-only doc comment; Move the limit to an internal helper and keep a thin exported alias for adapter authors; Move the limit to an internal helper with no exported alias.
   - Provisional decision (owner review 14 Nov): Keep StuckTestLimit exported with a test-only doc comment.
   - Reason: No non-test use proves nothing about adapter-author need, so the owner decides between a stable import and a smaller public surface. Benchmark: Google test practice uses one generous stuck-test deadline per test with named timeout errors.
   - Persona impact: Embedder adapter authors either keep one import or must change one import. Escape hatch: Keep the name exported until the owner decides, so existing adapter suites keep working.
2. Does one 20 minute per-test deadline keep three stuck tests inside the 60 minute package timeout?
   - Options: Keep one 20 minute deadline per test; Lower the shared per-test deadline so three stuck tests stay below the 60 minute package timeout; Keep 20 minutes but fail fast on test cancellation.
   - Provisional decision (owner review 14 Nov): Use one 15 minute per-test deadline provisionally and stop each Poll at test cancellation.
   - Reason: Three 20 minute waits total 60 minutes, equal to the package timeout, so the owner sets the size or accepts the overflow. Benchmark: Google test practice sizes stuck-test limits below the package timeout divided by the number of stuck tests.
   - Persona impact: Embedder sees slower but stable suite runs under load; a lower limit fails stuck tests sooner. Escape hatch: Keep the provisional 15 minute value until the owner decides a lower value.

Questions considered and answered:

- Do any ADRs govern fixed waits or the shared stuck-test limit? The pack lists no ADRs (adrs: []). A search of docs/adr/INDEX.md for store, test, wait, and conformance returns no decision, so no ADR governs the waits.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
