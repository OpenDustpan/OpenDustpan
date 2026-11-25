## 0. Confirm against the code

- [ ] 0.1 Confirm on main: `internal/agent/agent.go` (`Run`, `Actor`, `StopCredentialEnded`), `internal/agent/store.go` (`NewRun`, `RunStore`), `internal/agent/loop.go` (`KindTurn`, `Credentials`, `TurnKind`), `pkg/audit/audit.go` (`DelegationID`), `internal/agent/agenttest/runs.go`. Confirm that no SQL `RunStore`, no start route and no server wiring of `TurnKind` exist. Confirm that `person-delegates-dusty` is on main and `pkg/delegation` has the record.
- [ ] 0.2 Confirm that a change creating Dusty as an agent principal is on main. If it is not, stop and report to the PM.

## 1. Failing tests first

- [ ] 1.1 `internal/agent/agenttest/runs.go`: conformance cases for `RunStore.Create` with an empty, a stored and a read-back `DelegationID`.
- [ ] 1.2 `internal/agent/loop_test.go`: a table for the resolve of the delegation at each step and before each tool call (live, revoked between steps, revoked before a tool call, unknown, other person, other agent, out of scope namespace).
- [ ] 1.3 `internal/agent/tools_test.go`: a call outside the scope of the delegation fails; a call that the person has no grant for fails; each writes the right entry.
- [ ] 1.4 Handler tests in `internal/api/server`: start with no delegation, with an invalid delegation, with a delegated token, with a break-glass session; read, list and cancel with a foreign run.
- [ ] 1.5 Each test in 1.1 to 1.4 fails when its guard is removed.

## 2. Build

- [ ] 2.1 `internal/agent/agent.go`, `store.go`: `DelegationID` in `Run` and `NewRun`; stop reason `delegation-ended`.
- [ ] 2.2 `internal/agent/loop.go`: port `Delegations`; resolve at each step and before each tool call; set the audit block.
- [ ] 2.3 `internal/agent/toolset.go`: the scope check next to the authorization check.
- [ ] 2.4 SQL `RunStore` for `internal/store/sqlite` and `internal/store/postgres`, with the column `delegation_id` in `internal/store/schema/migrations/0168_baseline.sql`; both run the conformance suite.
- [ ] 2.5 Routes for start, read, list and cancel in a NEW package `internal/api/agent`; the start takes an idempotency key.
- [ ] 2.6 Wiring in `cmd/opendustpan`: register `agent.TurnKind` with the real `Credentials` and `Delegations`.
- [ ] 2.7 NEW error codes `agent.delegation-required`, `agent.delegation-invalid`, `agent.delegated-token-refused` in the catalog file `internal/errcode/testdata/catalog/agent.txt` (NEW).

## 3. Side effects

- [ ] 3.1 Regenerate the API description, the clients and the golden files with their generators (`make generate`, `make clients-generate`). Never edit them by hand.
- [ ] 3.2 NEW page `docs/guides/dusty-runs.md`: the five steps of the user path, the three errors and the one command that helps for each.
- [ ] 3.3 An e2e step that starts a run, revokes the delegation and sees the stop reason `delegation-ended`.
- [ ] 3.4 Usability check of the user path with the persona developer.

## 4. Definition of done

- [ ] 4.1 Each task above has its test and its evidence in the pull request body.
- [ ] 4.2 The Opus security review (rev2) is clean on the exact head.
- [ ] 4.3 `make check` passes with zero findings.
