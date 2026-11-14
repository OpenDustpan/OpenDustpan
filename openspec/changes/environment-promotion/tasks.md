## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing comparison test for staging and production effective values Check: TestCompareUnitsShowsFieldDiff fails on main
- [ ] 1.2 Add failing comparison test for masked secret fields Check: TestCompareMasksSecretValues fails on main
- [ ] 1.3 Add failing test for secret equality state with no plain text Check: TestCompareSecretEqualityWithoutPlaintext fails on main
- [ ] 1.4 Add failing test for mixed secret and plain field Check: TestCompareMixedSecretPlain fails on main
- [ ] 1.5 Add failing test for compare 404 and 422 shapes Check: TestCompareRefusesBadPair fails on main
- [ ] 1.6 Add failing auth test for compare without target read Check: TestCompareRefusesWithoutTargetRead fails on main
- [ ] 1.7 Add failing promotion test for same-tool copy with write-back commit and plan Check: TestPromoteUnitsCopiesConfigAsEdit fails on main
- [ ] 1.8 Add failing refusal test for cross-tool promotion Check: TestPromoteUnitsRefusesCrossTool fails on main
- [ ] 1.9 Add failing refusal test for different blueprints Check: TestPromoteRefusesCrossBlueprint fails on main
- [ ] 1.10 Add failing test for identity field refusal Check: TestPromoteRefusesFieldNotAllowed fails on main
- [ ] 1.11 Add failing test for secret flag refusal Check: TestPromoteRefusesImplicitSecret fails on main
- [ ] 1.12 Add failing test for sealed secret in write-back commit Check: TestPromoteKeepsSecretSealed fails on main
- [ ] 1.13 Add failing test for unusable sealed reference Check: TestPromoteRefusesSealedScopeMismatch fails on main
- [ ] 1.14 Add failing test for 409 on concurrent target edit Check: TestPromoteRefusesStaleTarget fails on main
- [ ] 1.15 Add failing test for missing source and missing target 404 Check: TestPromoteMissingUnitReturns404 fails on main
- [ ] 1.16 Add failing auth test for read-only caller and missing source read Check: TestPromoteRefusesWithoutTargetManage fails on main
- [ ] 1.17 Add failing test for empty field list and no-difference Check: TestPromoteRefusesEmptyFields fails on main
- [ ] 1.18 Add failing test for manual lock and running job Check: TestPromoteRefusesLockedTarget fails on main
- [ ] 1.19 Add failing test for 201 with committed edit plus decision record on gate refusal Check: TestPromoteKeepsEditOnGateRefusal fails on main
- [ ] 1.20 Add failing test for committed edit when plan fails to start Check: TestPromoteKeepsEditWhenPlanCannotStart fails on main
- [ ] 1.21 Add failing test for retry that reuses committed edit after restart Check: TestPromoteRecoversAfterCrash fails on main
- [ ] 1.22 Add failing test for a retry with the same Idempotency-Key inside 24 hours that starts at most 1 plan Check: TestPromoteMatchesRetryByKey fails on main

## 2. Wiring and routes

- [ ] 2.1 Wire the new comparison route with unit:read on source unit and on target unit Check: TestCompareUnitsShowsFieldDiff passes through the route test
- [ ] 2.2 Wire the new promotion route with unit:read on source unit and unit:manage on target unit plus gate evaluation Check: TestPromoteUnitsCopiesConfigAsEdit passes through the route test
- [ ] 2.3 Wire the new promotion read route for committed edit and decision record Check: TestPromoteKeepsEditOnGateRefusal reads the decision record through the new promotions route
- [ ] 2.4 Add guard with mutation test for promotionEdit constructor and record lint setting Check: Mutation test removes allowlist check and fails

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Add e2e step for compare then promote with OpenTofu and Pulumi CLI and plan apply Check: BLOCKED by the missing routes GET units/compare and POST promotions; once built, make e2e runs e2e/unit_settings_test.go with a personal token on the new routes, then tofu and pulumi plan and apply
- [ ] 4.2 Write docs page for compare and promote Check: docs page command sequence runs as written
- [ ] 4.3 Run usability check of full path Check: Developer completes compare and promote from docs page alone
- [ ] 4.4 Run full check suite Check: make check passes with zero findings
