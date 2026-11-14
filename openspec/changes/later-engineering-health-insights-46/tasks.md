## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Side effects (computed from main)

- [ ] 1.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request

## 2. Refinement

- [ ] 2.1 Refine docs/rfc/0046-insights.md when RFC 46 is accepted: metric definitions, opt-in levels, privacy floor, retention and record shapes. Check: `gh issue view 2038 --json state` plus `rg -n accepted docs/rfc/0046-insights.md`
