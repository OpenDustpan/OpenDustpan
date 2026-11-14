## 1. Failing tests first

- [ ] 1.1 Add failing comparison test for staging and production effective values Check: TestCompareUnitsShowsFieldDiff fails on main
- [ ] 1.2 Add failing comparison test for masked secret fields Check: TestCompareMasksSecretValues fails on main
- [ ] 1.3 Add failing test for secret equality state with no plain text Check: TestCompareSecretEqualityWithoutPlaintext fails on main
- [ ] 1.4 Add failing promotion test for same-tool copy with write-back commit and plan Check: TestPromoteUnitsCopiesConfigAsEdit fails on main
- [ ] 1.5 Add failing refusal test for cross-tool promotion Check: TestPromoteUnitsRefusesCrossTool fails on main
- [ ] 1.6 Add failing refusal test for different blueprints Check: TestPromoteRefusesCrossBlueprint fails on main
- [ ] 1.7 Add failing test for identity field refusal Check: TestPromoteRefusesFieldNotAllowed fails on main
- [ ] 1.8 Add failing test for secret flag refusal Check: TestPromoteRefusesImplicitSecret fails on main
- [ ] 1.9 Add failing test for sealed secret in write-back commit Check: TestPromoteKeepsSecretSealed fails on main
- [ ] 1.10 Add failing test for 409 on concurrent target edit Check: TestPromoteRefusesStaleTarget fails on main
- [ ] 1.11 Add failing test for missing source and missing target 404 Check: TestPromoteMissingUnitReturns404 fails on main
- [ ] 1.12 Add failing auth test for read-only caller Check: TestPromoteRefusesWithoutTargetManage fails on main
- [ ] 1.13 Add failing test for empty field list and no-difference Check: TestPromoteRefusesEmptyFields fails on main
- [ ] 1.14 Add failing test for 201 with committed edit plus decision record on gate refusal Check: TestPromoteKeepsEditOnGateRefusal fails on main
- [ ] 1.15 Add failing test for committed edit when plan fails to start Check: TestPromoteKeepsEditWhenPlanCannotStart fails on main
- [ ] 1.16 Add failing test for retry that reuses committed edit after restart Check: TestPromoteRecoversAfterCrash fails on main

## 2. Wiring and routes

- [ ] 2.1 Wire the new comparison route with unit:read on source unit and on target unit Check: TestCompareUnitsShowsFieldDiff passes through the route test
- [ ] 2.2 Wire the new promotion route with unit:read on source unit and unit:manage on target unit plus gate evaluation Check: TestPromoteUnitsCopiesConfigAsEdit passes through the route test
- [ ] 2.3 Wire the new promotion read route for committed edit and decision record Check: TestPromoteKeepsEditOnGateRefusal reads the decision record through the new promotions route

## 3. Definition of done

- [ ] 3.1 Add e2e step for compare then promote with real CLI Check: make e2e promotes staging config to production through real CLI with write-back commit
- [ ] 3.2 Write docs page for compare and promote Check: docs page command sequence runs as written
- [ ] 3.3 Run usability check of full path Check: Developer completes compare and promote from docs page alone
- [ ] 3.4 Run full check suite Check: make check passes with zero findings
