## 1. Failing tests first

- [ ] 1.1 Add failing snapshot test with a concurrent commit during GetDeletedUnitFacts. Check: TestDeletedUnitFactsSnapshot in pkg/store/sqlite fails on main and the pull request shows the failure log
- [ ] 1.2 Move GetDeletedUnitFacts reads into one read-only transaction scoped to the call with deferred close. Check: go test ./pkg/store/sqlite/ -run TestDeletedUnitFactsSnapshot -count=1

## 2. Fix

- [ ] 2.1 Update the fault wrapper and the conformance suite path as needed with no behavior change. Check: go test ./pkg/store/... -count=1
- [ ] 2.2 Remove the blank-assign rollback pattern on the touched fact-read paths only. Check: rg -n '_ = rollbackErr' pkg/store/sqlite/ returns no hits on the touched fact-read files
- [ ] 2.3 Add the one fixed start-up log line for an absent gate service. Check: Server start log shows the one no-gate-service INFO line once per start, and shows no such line when a gate service exists

## 3. Definition of done

- [ ] 3.1 Confirm the wiring with a dev server start with and without a gate service. Check: Wiring: gate fact read runs through the server state.write path in a dev server smoke start
- [ ] 3.2 Confirm no route or authorization change is needed. Check: Route: no route change; authorization unchanged
- [ ] 3.3 Record why no e2e step applies. Check: e2e: not applicable, no user-visible path; store snapshot test plus start-up log check cover the change
- [ ] 3.4 Record why no docs page applies. Check: Docs: not applicable, only an operational log line with fixed text
- [ ] 3.5 Run the operator start-up check. Check: Usability: operator completes the start-up log check from the docs-free path alone
- [ ] 3.6 Run format, vet, lint, and the full store test suite. Check: make check with zero findings
