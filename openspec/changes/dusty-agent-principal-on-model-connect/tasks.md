## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main. Record each difference in the pull request body before the first test. Check: the body lists the differences, or says there are none.
- [ ] 0.2 Confirm that no route calls `SettingsStore.PutConnection` (`rg PutConnection internal pkg --glob '!*_test.go'`). Check: the output names only the interface.

## 1. Failing tests first

- [ ] 1.1 Add the conformance tests `TestAgentRecordStoreRefusesHostileSponsors` and `TestAgentRecordStoreRoundTrip` in `pkg/principal/principaltest`. Check: they fail to build on main.
- [ ] 1.2 Add `TestConnectModelRefusesACallerWithoutTheAction`, `TestConnectModelTakesCreatorFromTheCaller`, `TestConnectModelRollsBackEveryWrite`, `TestConnectModelMakesNoSecondRecord`, `TestConnectModelWritesOneEntryForEachWrite` in `internal/agent`. Check: each fails on main.
- [ ] 1.3 Add `TestAgentCannotJoinAGroupOrHoldAProtectedRole` and `TestValidateRefusesSignInMethodsForAgent` in `pkg/principal` and `pkg/access`. Add `TestListAgentPrincipalsNeedsTheAction` in `internal/api/server`. Check: each fails on main.
- [ ] 1.4 Add the lines of each NEW error code in `internal/errcode/testdata/catalog/agent.txt` (NEW if absent). Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 2. Build

- [ ] 2.1 Edit the baseline migration of each dialect in place for the tables `agent_record` and `agent_sponsor`, add the queries, and run `make generate`. Never edit `internal/db`. Check: `sqlc diff` is clean and the conformance suite passes on SQLite and PostgreSQL.
- [ ] 2.2 Build `principal.Service.CreateAgent`, the validation of decision 1 and the reserved subject. Check: the tests of 1.1 and 1.3 pass.
- [ ] 2.3 Add the roles `agent-tools-read` and `agent-tools` to `pkg/access/roles.go`. Check: `TestBuiltInRoles` lists them and each action is in the catalog.
- [ ] 2.4 Build `agent.ConnectModel`. Check: the tests of 1.2 pass.
- [ ] 2.5 Extend `GET /api/server/principals?kind=agent`. Check: `TestListAgentPrincipalsNeedsTheAction` passes.
- [ ] 2.6 SECURITY. Add the hostile tests of the layers table of `design.md`, one table for each layer. Check: remove each guard in turn in a local run and see its test fail.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make clients-generate`). Check: the named files change in the same pull request. Confirmed by: the driver's gates.
- [ ] 3.2 Add the GLOSSARY entry for "sponsor" and "agent record". Check: `make docs-lint` passes.
- [ ] 3.3 Write the docs page `docs/guides/dusty-principal.md` with the exact commands to list the record and read its sponsors and grants. Check: `make docs-lint` passes and the e2e step of 4.1 runs each command as written.

## 4. Definition of done

- [ ] 4.1 Add the NEW e2e file `e2e/dusty_principal_test.go` against the real binary: connect a model through the test server's call of `agent.ConnectModel`, then run the commands of the docs page. Check: the step passes in CI.
- [ ] 4.2 Run the full check suite. Check: `make check` with zero findings.
