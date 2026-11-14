## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Write failing conformance tests for path match, two rules, evaluate mode, unless auto-apply, warn, unless error, operationSource, excuse scope, apply recheck, count with fresh authentication and record fields. Check: gatetest.TestOwnershipPathAddsRequirement fails on main.

## 2. Build

- [ ] 2.1 Implement the ownership matcher, the unless evaluation and the autoApply record fields. Check: gatetest.TestOwnershipUnlessAutoApply passes on SQLite and PostgreSQL.
- [ ] 2.2 Test matching cost with 256 rules and the 4 KiB condition limit. Check: Evaluation of 256 rules takes at most 500 ms; an oversized body fails at save.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add an e2e step: BLOCKED by the trigger fire route of #901 and the rule routes of rest-routes-ownership-rules-create. After they land, use POST /api/dustpan/v1/namespaces/{}/units/{}/triggers/{}/fire with a service access token and the real CLI. Check: e2e step shows autoApply true for the schedule plan and a wait for a warn plan.
- [ ] 4.2 Write the docs page in the same pull request. Check: The commands on the page run as written.
- [ ] 4.3 Run the usability check. Check: The persona completes the path from the docs page alone.
- [ ] 4.4 Run all checks. Check: `make check` reports zero findings.
