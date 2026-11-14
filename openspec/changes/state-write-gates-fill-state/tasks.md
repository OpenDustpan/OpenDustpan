## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests for the unlocked-write fill, the first write and the valid envelope in the NEW file pkg/gate/statechange_unlocked_test.go. Check: go test ./pkg/gate -run TestUnlockedWriteFillsStateFacts fails on main
- [ ] 1.2 Write failing hostile tests for unreadable, over-limit and fake-envelope states in the NEW file internal/api/tfe/state_unlocked_test.go. Check: go test ./internal/api/tfe -run TestUnlockedWriteFailsClosed fails on main
- [ ] 1.3 Write failing tests for the save-time refusal in the NEW file pkg/gate/gate_save_test.go. Check: go test ./pkg/gate -run TestSaveRefusesUnfilledRead fails on main

## 2. Implementation

- [ ] 2.1 Edit internal/api/tfe/states.go:602 (func Handler.writeStateVersion) to fill gate.StateChange for the unlocked write with the version recheck and the NEW codes state.unreadable, state.over-limit, state.invalid-envelope and state.changed, after gate-subject-job-token-has and state-facts-follow-ups-one. BLOCKED by the owner decision in RFC 119. Check: The tests of the first section pass

## 3. Definition of done

- [ ] 3.1 Add an e2e step in the NEW file e2e/state_write_gates_test.go: a real OpenTofu unlocked write with an access token that holds the state.write grant, through Handler.writeStateVersion. BLOCKED by the owner decision in RFC 119. A person's step. Check: make e2e
- [ ] 3.2 Update the matrix of docs/guides/state-write-gates.md and run the usability check from that page. A person's step. Check: make docs-lint
- [ ] 3.3 Run the full checks. Confirmed by: CI (make check) Check: make check with zero findings
