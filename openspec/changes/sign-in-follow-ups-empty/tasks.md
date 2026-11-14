## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add the NEW test TestSignInRefusesEmptySource in the NEW file `pkg/principal/signin_empty_source_test.go` as a direct call of Service.signIn with an empty source. Check: TestSignInRefusesEmptySource fails on main and passes after the fix.
- [ ] 1.2 Add the NEW test TestSignInValidSourceStillSignsIn in the NEW file `pkg/principal/signin_empty_source_test.go` as a direct call of Service.signIn with a valid source. Check: TestSignInValidSourceStillSignsIn passes before and after the fix.
- [ ] 1.3 Add a compile-time assertion that the production store satisfies the narrow interface, and type the field in `pkg/dustpan/area_signin.go`. Check: go build ./pkg/dustpan fails with a mismatched store and passes with the production store.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the empty-source guard at the start of Service.signIn. No route and no docs page change. Check: The two tests above pass; the HTTP mapping is unchanged.
- [ ] 3.2 Run the full checks with `make check` with zero findings. Confirmed by: CI (`make check`). Check: `make check` with zero findings.
