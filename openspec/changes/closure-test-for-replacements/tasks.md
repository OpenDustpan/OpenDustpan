SECURITY: this change is security code (ADR 250). A wrong "disjoint" loses state. Each layer has its own hostile test, and each guard has a mutation test. Review: a cheap-model review of the change, a Claude review for security, and the Opus review of the stack.

## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 SECURITY. Replace the pending case `AReplaceIsNeverDisjoint` with the closure cases `AReplaceWithItsClosureInItsOwnPlanIsDisjoint`, `AReplaceWhoseClosureReachesTheOtherPlanConflicts`, `AReaderOutsideTheWriteSetConflicts`, `AnUnprovenClosureConflicts` and `TheCheckIsTheSameInBothDirections`. Check: NEW cases in `pkg/reviewrequest/reviewrequesttest`; `go test ./pkg/reviewrequest/...` lists them, and `TestCaseNamesAreUniqueAndNotEmpty` passes.
- [ ] 1.2 SECURITY. Write the decoder hostile table. Check: NEW test `TestDecodeHostilePlans` in `internal/tofuplan` fails on main (the package does not exist).
- [ ] 1.3 SECURITY. Write the domain hostile table. Check: NEW test `TestProveReplaceDisjointHostile` in `pkg/reviewrequest` fails on main.
- [ ] 1.4 SECURITY. Write the adapter test that runs the conformance suite and the unknown kind. Check: NEW test `TestCheckerUnknownKindIsConflict` in `internal/conflictcheck` fails on main.
- [ ] 1.5 SECURITY. Write the handler hostile table. Check: NEW test `TestRegisterClaimHostileReplace` fails on main.

## 2. Build

- [ ] 2.1 SECURITY. Add `reviewrequest.PlanGraph` with unexported fields, `NewPlanGraph` and `ProveReplaceDisjoint`, and the field `Graph` of `PlanClosure`. Check: `go test -race ./pkg/reviewrequest/...` passes; removing each guard of `NewPlanGraph` makes `TestProveReplaceDisjointHostile` fail (mutation list in the pull request).
- [ ] 2.2 SECURITY. Add `internal/tofuplan.Decode`. Check: `go test -race ./internal/tofuplan/...` passes, and each hostile case gives an error or a not-provable graph.
- [ ] 2.3 SECURITY. Add `internal/conflictcheck.Checker`, and turn each conflict case of `reviewrequesttest` from pending into a real test. Check: `go test -race ./internal/conflictcheck/...` runs `RunConflicts` with no skipped case.
- [ ] 2.4 SECURITY. Wire the checker into the register route of two-disjoint-claims, in place of the fixed `claim.closure-unproven` answer for a replacement. Check: `TestRegisterClaimHostileReplace` passes; the route test shows the actions of the route.
- [ ] 2.5 Add the error codes `claim.overlap`, `claim.closure-unproven` and `claim.plan-invalid` to the error catalog, if two-disjoint-claims did not add them. Check: `go test ./internal/errcode/...` passes.

## 3. Definition of done

- [ ] 3.1 E2e with real OpenTofu: job A replaces an instance with its closure, job B updates a disjoint resource, both claims are accepted; job C reads the replaced instance and is refused. Check: `make e2e` passes.
- [ ] 3.2 Docs page `docs/explanation/plan-conflicts.md`: the closure of a replacement, the three reasons, and the command that shows them. Check: `make docs-lint` passes, and a test runs each command of the page.
- [ ] 3.3 Usability check: the developer finds the reason of a refusal from the docs page alone. Check: each error row names `GET /api/dustpan/v1/workspaces/{id}/claims` or `tofu show -json plan.tfplan`.
- [ ] 3.4 Full check. Check: `make check` reports zero findings.
