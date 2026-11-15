## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing conformance tests in the NEW file pkg/gate/gatetest/ownership_test.go for path match, two rules, evaluate mode, unless auto-apply, warn, unless error, operationSource, excuse scope, apply recheck, count with fresh authentication and record fields. Check: gatetest.TestOwnershipPathAddsRequirement fails on main.

## 2. Build

- [ ] 2.1 Implement the ownership matcher in the NEW file pkg/gate/ownership.go, the unless evaluation and the autoApply record fields in pkg/gate/record.go:173. Check: gatetest.TestOwnershipUnlessAutoApply passes on SQLite and PostgreSQL.
- [ ] 2.2 Test matching cost with 256 rules and the 4 KiB condition limit in the NEW file pkg/gate/ownership_bench_test.go. Check: Evaluation of 256 rules takes at most 500 ms; an oversized body fails at save.

## 3. Side effects (computed from main)

- [x] 3.1 Not applicable: this change adds no ADR, so `docs/adr/INDEX.md` does not change.

## 4. Definition of done

- [ ] 4.1 Add an e2e step: BLOCKED by the trigger fire route of #901 and the rule routes of rest-routes-ownership-rules-create. After they land, use POST /api/dustpan/v1/namespaces/{}/units/{}/triggers/{}/fire with a service access token and the real CLI. Check: e2e step shows autoApply true for the schedule plan and a wait for a warn plan. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write the docs page in the same pull request. Check: The commands on the page run as written. Confirmed by: CI (an e2e step in the NEW file e2e/add_ownership_rules_conditional_auto_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 4.3 Run the usability check. Check: The persona completes the path from the docs page alone. A person's step.
- [ ] 4.4 Run all checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
