## 1. Failing tests

- [ ] 1.1 Add failing conformance tests for running, queued, awaiting-approval, failed from both writers and both end orders, divergent ends, single ends, out-of-order ends, cancelled and timed-out ends, and apply-needed set and clear with two plans. Check: go test ./pkg/job/... -run TestUnitStatesFailedBothOrders -count=1 shows failure on main
- [ ] 1.2 Add failing store tests that each write carries the fence and runs in the transaction of its own write, including fence-failure rollback. Check: go test ./pkg/inventory/... -run TestStatesFence -count=1 shows failure on main
- [ ] 1.3 Add failing table test for the stable wire values of the five states. Check: go test ./pkg/inventory/... -run TestUnitStateWireValues -count=1 shows failure on main

## 2. Implementation

- [ ] 2.1 Implement job-model writes for running, queued, awaiting-approval, failed and apply-needed and stack-package write for failed with fence check, including end-time ordering, divergent-end, single-end, terminal-end and reconcile rules. Check: go test ./pkg/job/... ./pkg/stack/... -count=1 passes
- [ ] 2.2 Expose stored state facts through the internal states provider that the states slice reads, with unit:read on each unit. Check: go test ./pkg/inventory/... -run TestStoredUnitStates -count=1 passes

## 3. Definition of done

- [ ] 3.1 Add e2e step against the real CLI that starts jobs and checks the stored states through the inventory. Check: make e2e run of one Pulumi apply and one OpenTofu apply shows running and queued while live, then failed set or cleared after the change
- [ ] 3.2 Write the docs page for the five states in the same pull request. Check: Docs page for unit states lists the five writers and the latest-wins rule; commands on the page run as written
- [ ] 3.3 Run usability check from the docs page alone and make check with zero findings. Check: make check passes with zero findings
