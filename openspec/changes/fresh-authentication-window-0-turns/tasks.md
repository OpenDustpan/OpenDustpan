## 1. Failing tests first

- [ ] 1.1 Add failing test for window 0 with old step refused Check: TestFreshWindowZeroAsksEachTime fails on main
- [ ] 1.2 Add failing test for window 0 with age 0 refused Check: TestFreshWindowZeroAgeZeroRefused fails on main
- [ ] 1.3 Add failing test for namespace 0 against installation 900 Check: TestFreshNarrowZeroAgainst900 fails on main
- [ ] 1.4 Add failing test for longer narrow window refused Check: TestFreshLongerNarrowWindowRefused fails on main
- [ ] 1.5 Add failing test for 403 code and sign-in path Check: TestStaleErrorNamesSignInPath fails on main
- [ ] 1.6 Add failing test for no-factor enrollment page Check: TestFreshNoFactorNamesEnrollment fails on main
- [ ] 1.7 Add failing test for access-token refusal Check: TestFreshTokenNeverSatisfies fails on main
- [ ] 1.8 Add failing test for age equal to nonzero window accepted Check: TestFreshBoundaryEqualsWindow fails on main
- [ ] 1.9 Add failing test that each guarded route gives the same code mfa.fresh-step-required and reads the one window setting of ADR 227 Check: TestFreshGuardedRoutesShareCodeAndWindow fails on main

## 2. Definition of done

- [ ] 2.1 Fix zero check, error text, and fresh comment Check: `make check` with zero findings
- [ ] 2.2 Update docs page in same pull request Check: Docs page lists window values 0 and 60 to 900 and the refusal text
- [ ] 2.3 Run usability check of error and one helping command Check: Site administrator completes path from docs page alone
