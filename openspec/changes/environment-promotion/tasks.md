## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing comparison test for staging and production effective values in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareUnitsShowsFieldDiff fails on main
- [ ] 1.2 Add failing comparison test for masked secret fields in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareMasksSecretValues fails on main
- [ ] 1.3 Add failing test for secret equality state with no plain text in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareSecretEqualityWithoutPlaintext fails on main
- [ ] 1.4 Add failing test for mixed secret and plain field in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareMixedSecretPlain fails on main
- [ ] 1.5 Add failing test for compare 404 and 422 shapes in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareRefusesBadPair fails on main
- [ ] 1.6 Add failing auth test for compare without target read in NEW file pkg/unitsettings/compare_test.go. Check: TestCompareRefusesWithoutTargetRead fails on main
- [ ] 1.7 Add failing promotion test for same-tool copy with write-back commit and plan in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteUnitsCopiesConfigAsEdit fails on main
- [ ] 1.8 Add failing refusal test for cross-tool promotion in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteUnitsRefusesCrossTool fails on main
- [ ] 1.9 Add failing refusal test for different blueprints in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesCrossBlueprint fails on main
- [ ] 1.10 Add failing test for identity field refusal in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesFieldNotAllowed fails on main
- [ ] 1.11 Add failing test for secret flag refusal in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesImplicitSecret fails on main
- [ ] 1.12 Add failing test for sealed secret in write-back commit in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteKeepsSecretSealed fails on main
- [ ] 1.13 Add failing test for unusable sealed reference in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesSealedScopeMismatch fails on main
- [ ] 1.14 Add failing test for 409 on concurrent target edit in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesStaleTarget fails on main
- [ ] 1.15 Add failing test for missing source and missing target 404 in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteMissingUnitReturns404 fails on main
- [ ] 1.16 Add failing auth test for read-only caller and missing source read in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesWithoutTargetManage fails on main
- [ ] 1.17 Add failing test for empty field list and no-difference in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesEmptyFields fails on main
- [ ] 1.18 Add failing test for manual lock and running job in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRefusesLockedTarget fails on main
- [ ] 1.19 Add failing test for 201 with committed edit plus decision record on gate refusal in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteKeepsEditOnGateRefusal fails on main
- [ ] 1.20 Add failing test for committed edit when plan fails to start in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteKeepsEditWhenPlanCannotStart fails on main
- [ ] 1.21 Add failing test for retry that reuses committed edit after restart in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteRecoversAfterCrash fails on main
- [ ] 1.22 Add failing test for a retry with the same Idempotency-Key inside 24 hours that starts at most 1 plan in NEW file pkg/unitsettings/promote_test.go. Check: TestPromoteMatchesRetryByKey fails on main

## 2. Wiring and routes

- [ ] 2.1 Wire the new comparison route with unit:read on source unit and on target unit. Declare it in the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the adapter in NEW file internal/api/compare_handler.go. Check: TestCompareUnitsShowsFieldDiff passes through the route test
- [ ] 2.2 Wire the new promotion route with unit:read on source unit and unit:manage on target unit plus gate evaluation. Declare it in the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the adapter in NEW file internal/api/promote_handler.go. Check: TestPromoteUnitsCopiesConfigAsEdit passes through the route test
- [ ] 2.3 Wire the new promotion read route for committed edit and decision record. Declare it in the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the adapter in NEW file internal/api/promote_handler.go. Check: TestPromoteKeepsEditOnGateRefusal reads the decision record through the new promotions route
- [ ] 2.4 Add guard with mutation test for NEW promotionEdit constructor and record lint setting in NEW file pkg/unitsettings/promote.go, with the test in pkg/unitsettings/promote_test.go. Check: Mutation test removes allowlist check and fails

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Add e2e step for compare then promote with OpenTofu and Pulumi CLI and plan apply The step calls `GET /api/dustpan/v1/namespaces/{namespace}/units/compare` and `POST /api/dustpan/v1/namespaces/{namespace}/promotions` (both added by this change) with an access token. Check: make e2e runs e2e/unit_settings_test.go on these routes, then tofu and pulumi plan and apply. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Write docs page for compare and promote in NEW file docs/guides/promote-environment-config.md. Check: docs page command sequence runs as written. Confirmed by: CI (an e2e step in the NEW file e2e/environment_promotion_docs_test.go runs each command of the docs page against the e2e test server). `make docs-lint` passes on each edited guide.
- [ ] 4.3 Run usability check of full path Check: Developer completes compare and promote from docs page alone. A person's step.
- [ ] 4.4 Run full check suite Check: make check passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
