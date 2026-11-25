## 0. Confirm against the code first

- [ ] 0.1 Read each fact of "What main already does" at its file and line on the current main. Record each difference in the pull request body before the first test. Check: the body lists the differences, or says there are none.
- [ ] 0.2 Confirm that `ConnectModel` (NEW in this change) does not write the connection: `rg 'PutConnection' internal/agent --glob '!*_test.go'` shows no call in `internal/agent/connect.go`.

## 1. Failing tests first

- [ ] 1.1 Add the conformance tests `TestAgentRecordStoreRefusesHostileSponsors` and `TestAgentRecordStoreRoundTrip` in `pkg/principal/principaltest`. Check: they fail to build on main.
- [ ] 1.2 Add `TestConnectModelRefusesACallerWithoutTheAction`, `TestConnectModelTakesCreatorFromTheCaller`, `TestConnectModelRollsBackEveryWrite`, `TestConnectModelMakesNoSecondRecord`, `TestConnectModelWritesOneEntryForEachWrite` in the NEW file `internal/agent/connect_test.go`. Check: each fails on main: the NEW symbol `agent.ConnectModel` does not exist (`rg 'func .*ConnectModel' internal/agent` finds nothing), so the package test does not build. `TestConnectModelRefusesACallerWithoutTheAction` also fails on main because no code in `internal/agent` checks `ActionServerManageAgent` (`pkg/identity/identity.go:541`, used by no file of `internal/agent`).
- [ ] 1.3 Add `TestAgentCannotJoinAGroupOrHoldAProtectedRole` in the NEW files `pkg/principal/agent_member_test.go` and `pkg/access/agent_grant_test.go`. Check: it fails on main, because the member add (`pkg/principal/service.go:509`) and the grant (`pkg/access/service.go:354`) have no refusal for the kind `agent`, and the test makes the agent with the NEW symbol `principal.Service.CreateAgent`, which main lacks.
- [ ] 1.4 Add `TestValidateRefusesSignInMethodsForAgent` in the NEW file `pkg/principal/agent_test.go`. Add `TestListAgentPrincipalsNeedsTheAction` in `internal/api/server/principals_test.go`. Check: each fails on main: `pkg/principal/principal.go:544` has no refusal for the kind `agent`, and the list route (`internal/api/server/principals.go:793`) adds no `sponsors` field.
- [ ] 1.5 Add the lines of each NEW error code in `internal/errcode/testdata/catalog/agent.txt` (NEW if absent). Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 2. Build

- [ ] 2.1 Edit the baseline migration of each dialect in place for the tables `agent_record` and `agent_sponsor` (`internal/store/sqlite/migrations/0168_baseline.sql` and `internal/store/postgres/migrations/0168_baseline.sql`), add the queries to `internal/store/sqlite/queries.sql` and `internal/store/postgres/queries.sql`, add the NEW adapter files `internal/store/sqlite/agent.go` and `internal/store/postgres/agent.go`, and run `make generate`. Never edit the generated `internal/store/sqlite/internal/db` or `internal/store/postgres/internal/db`. Check: `sqlc diff` is clean and the conformance suite passes on SQLite and PostgreSQL.
- [ ] 2.2 Build `principal.Service.CreateAgent` (NEW) in the NEW file `pkg/principal/agent.go`, the validation of decision 1 in `pkg/principal/principal.go`, the refusal of a member of the kind `agent` in `pkg/principal/service.go`, and the reserved subject in `pkg/principal/principal.go`. Check: the tests of 1.1 and 1.3 pass.
- [ ] 2.3 Add the roles `agent-tools-read` and `agent-tools` to the built-in roles in `pkg/access/roles.go`, and the refusal of a protected role in `pkg/access/service.go`. Check: `TestBuiltInRoles` lists them and each action is in the catalog.
- [ ] 2.4 Build `agent.ConnectModel` (NEW) in the NEW file `internal/agent/connect.go`. Check: the tests of 1.2 pass.
- [ ] 2.5 Extend `GET /api/server/principals?kind=agent` in `listPrincipals` (`internal/api/server/principals.go:793`). Check: `TestListAgentPrincipalsNeedsTheAction` passes.
- [ ] 2.6 SECURITY. Add the hostile tests of the layers table of `design.md`, one table for each layer, in the NEW test files `pkg/principal/principaltest/agent.go`, `internal/agent/connect_test.go`, `pkg/access/agent_grant_test.go` and the existing `internal/api/server/principals_test.go`. Check: remove each guard in turn in a local run and see its test fail.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make clients-generate`). Check: the named files change in the same pull request. Confirmed by: the driver's gates.
- [ ] 3.2 Add the GLOSSARY entry for "sponsor" and "agent record". Check: `make docs-lint` passes.
- [ ] 3.3 Write the docs page `docs/guides/dusty-principal.md` with the exact commands to list the record and read its sponsors and grants. Check: `make docs-lint` passes and the e2e step of 4.1 runs each command as written.

## 4. Definition of done

- [ ] 4.1 Add the NEW e2e file `e2e/dusty_principal_test.go`. The connect step has no route: the test calls the NEW function `agent.ConnectModel` through the test server of `internal/servertest/servertest.go:107` only. The read step uses the existing route `GET /api/server/principals?kind=agent` with the bootstrap admin token, as `principalCallWithToken` does in `e2e/principals_test.go:531`, and runs the commands of the docs page. Check: the step passes in CI.
- [ ] 4.2 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
