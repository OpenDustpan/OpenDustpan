## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing conformance tests for running, queued, awaiting-approval, failed from both writers and both end orders, divergent ends, single ends, out-of-order ends, tie break, cancelled and timed-out ends, and rejected approval. Check: go test ./pkg/job/... -run TestUnitStatesFailedBothOrders -count=1 shows failure on main
- [ ] 1.2 Add failing store tests that each write carries the fence and runs in the transaction of its own write, including fence-failure rollback and delete cleanup. Check: go test ./pkg/inventory/... -run TestStatesFence -count=1 shows failure on main
- [ ] 1.3 Add failing table test for the stable wire values of the five states. Check: go test ./pkg/inventory/... -run TestUnitStateWireValues -count=1 shows failure on main
- [ ] 1.4 Add failing apply-needed tests for set, consume, supersede, expiry and two ready plans. Check: go test ./pkg/job/... -run TestApplyNeededTwoPlans -count=1 shows failure on main with a fake stored-plan feed

## 2. Implementation

- [ ] 2.1 Implement job-model writes and the stack-package write of failed with fence check, change identity, server-time ordering, tie break, divergent-end, single-end, terminal-end and reconcile rules. Check: go test ./pkg/job/... ./pkg/stack/... -count=1 passes
- [ ] 2.2 Expose stored state facts through the internal states provider that the states slice reads, with unit:read on each unit, and include the new table in the backup table list. Check: go test ./pkg/inventory/... -run TestStoredUnitStates -count=1 passes

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step against the real CLI. BLOCKED by the states slice (#1196): the inventory units route does not yet return a states field. Until it lands, the step starts jobs with POST /api/dustpan/v1/namespaces/{namespace}/jobs using an access token of a principal with the unit:run-apply action, runs one Pulumi and one OpenTofu change, and checks the stored rows through the store provider. Check: make e2e run with real CLI; after #1196, GET /api/dustpan/v1/namespaces/{namespace}/units with the same access token shows running, queued and failed transitions
- [ ] 4.2 Write the docs page for the five states in the same pull request. Check: Docs page for unit states lists the five writers and the latest-wins rule; commands on the page run as written
- [ ] 4.3 Run usability check from the docs page alone and make check with zero findings. Check: make check passes with zero findings
