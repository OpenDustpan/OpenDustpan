## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing test for window 0 with old step refused Check: TestFreshWindowZeroAsksEachTime fails on main
- [ ] 1.2 Add failing test for window 0 with age 0 refused Check: TestFreshWindowZeroAgeZeroRefused fails on main
- [ ] 1.3 Add failing test for namespace 0 against installation 900 Check: TestFreshNarrowZeroAgainst900 fails on main
- [ ] 1.4 Add failing test for 403 code and sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- [ ] 1.5 Add failing test for no-factor enrollment page Check: TestFreshNoFactorNamesEnrollment fails on main
- [ ] 1.6 Add failing test for access-token refusal Check: TestFreshTokenNeverSatisfies fails on main
- [ ] 1.7 Add failing test for age equal to nonzero window accepted Check: TestFreshBoundaryEqualsWindow fails on main
- [ ] 1.8 Add failing test for window 30 and 1000 refused Check: TestFreshInvalidWindowRefused fails on main
- [ ] 1.9 Add failing test for change to longer window needs fresh and change to 0 needs none Check: TestFreshWindowChangeToLongerNeedsFresh fails on main
- [ ] 1.10 Add failing test for approval-rule window 0 against installation 900 Check: TestFreshApprovalRuleZeroAsksEachTime fails on main

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Fix the window test in the guarded routes and wire it Check: Guarded routes reachable from server with declared authorization actions
- [ ] 3.2 Add comment on fresh() that names error code mfa.fresh-step-required Check: rg -n 'mfa.fresh-step-required' pkg/breakglass/breakglass.go shows the comment
- [ ] 3.3 Add route and authorization coverage for guarded routes Check: Route test passes
- [ ] 3.4 Add e2e step with a stale session on POST /api/dustpan/v1/sign-in/providers/{provider}/password Check: E2E step checks the 403 names the sign-in path
- [ ] 3.5 Update docs page in same pull request Check: Docs page lists window values 0 and 60 to 900 and the refusal text
- [ ] 3.6 Run usability check of error and one helping command Check: Site administrator completes path from docs page alone
- [ ] 3.7 Run make check with zero findings Check: `make check` with zero findings
