## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.
- [ ] 0.2 BLOCKED by the open questions of the proposal. Do not start a task that names an open question until the owner's answer is a repository record (an ADR or an ADR amendment). Check: the pull request body lists each answer with its record.
- [ ] 0.3 BLOCKED by the dependencies to be written: the model connect change that creates Dusty, and the change for the agent class of each catalog action. Check: each exists in `openspec/changes/` and is merged or has its code on main.

## 1. Failing tests first

Each scenario of the delta spec maps to one named test. Each test fails on main before the build.

- [ ] 1.1 Add the conformance suite `pkg/delegation/delegationtest` for the port: create, get, list by person, list all, renew, end once, end twice, and the sentinel errors. Check: the suite fails to build on main because `pkg/delegation` does not exist. Each store adapter runs it.
- [ ] 1.2 Add failing service tests in `internal/delegationsvc/delegationsvc_test.go`: `TestCreateStoresPersonAgentModeScopeCreatorTime`, `TestCreateRefusesAnAccessToken`, `TestCreateRefusesABreakGlassSession`, `TestCreateRefusesAnExpiryAboveTheMaximum`, `TestCreateRefusesAnUnknownMode`, `TestCreateRefusesANonAgentPrincipal`, `TestMaximumComesFromTheSetting`. Check: each fails on main with a build error.
- [ ] 1.3 Add failing service tests in the same file for renewal: `TestRenewKeepsTheIDAndWritesAnEntry`, `TestRenewRefusesAWiderScope`, `TestRenewNeverRunsByItself`. BLOCKED by open question 4 for the cases of an expired and an ended row. Check: each fails on main.
- [ ] 1.4 Add failing service tests for the ends: `TestEachEndWritesAnEntryWithItsCause` (one case for each cause of RFC 121 decision 16), `TestSecondRevokeWritesNoSecondEntry`, `TestResolveRefusesAnEndedDelegation`. BLOCKED by open question 5 for the ends that no request causes. Check: each fails on main.
- [ ] 1.5 Add failing handler tests in `internal/api/server/delegations_test.go`: `TestDelegationRoutesDeclareTheirActions` (the route test of the table), `TestOwnRowsAreFilteredInTheQuery`, `TestAdministratorListsAndRevokesEveryDelegation`, `TestSettingsRouteShowsTheEffectiveLifetimes`. BLOCKED by open questions 1 and 3 for the action and the path. Check: each fails on main with 404.
- [ ] 1.6 Add the line of each NEW error code (the code and its arguments, as `TestNewCodesHaveTheirLine` prints it) in its place in the name order in the NEW file `internal/errcode/testdata/catalog/delegation.txt`: `delegation.session-required`, `delegation.invalid-expiry`, `delegation.invalid-mode`, `delegation.unknown-agent`, `delegation.unknown-namespace`, `delegation.not-found`, `delegation.ended`. Check: `go test ./internal/errcode/ -run TestNewCodesHaveTheirLine -count=1` passes.

## 2. Build

- [ ] 2.1 Build `pkg/delegation` (the type, `New`, the port, the sentinels, `Resolve`) and add `pkg/delegation` to the domain rule of `.golangci.yml`. Check: the tests of 1.1 compile and the depguard rule refuses an import of `net/http` in the package.
- [ ] 2.2 Edit the baseline migration of each dialect in place: the tables `delegation` and `delegation_namespace` in the name order, with the queries in `queries.sql` in the name order of their area. Regenerate with `make generate`. Never edit `internal/db`. Check: `sqlc diff` is clean and the conformance suite passes on SQLite and PostgreSQL.
- [ ] 2.3 Build `internal/delegationsvc` with `FromSession`, the create, renew, revoke and list functions, and the audit entries in the transaction of each write. BLOCKED by open questions 2, 5, 6 and 7. Check: the tests of 1.2 to 1.4 pass.
- [ ] 2.4 Wire the routes in `internal/api/server/routes.go`, like `internal/api/server/githostlinks.go`, with the action of open question 1 for the own-row routes and `server:manage-access` on the installation for the administrator routes. The create and renew routes use the session gate and the break-glass refusal. Check: the route test of 1.5 passes, and a route with no action fails it.
- [ ] 2.5 Build the setting of the lifetimes and the settings route. BLOCKED by open question 3. Check: `TestSettingsRouteShowsTheEffectiveLifetimes` passes, and a change of the setting writes an audit entry with the old and the new value.
- [ ] 2.6 Write the GLOSSARY entries and the fix of "Principal kind" (done in this pull request). Check: `make docs-lint` passes.
- [ ] 2.7 SECURITY. Add the hostile tests of the layers table of `design.md`, one table for each layer (handler, service, store, audit, read paths), and for each guard a test that fails when the guard is removed. Check: remove each guard in turn in a local run and see its test fail.
- [ ] 2.8 Write the docs page `docs/guides/delegation.md` with the exact commands to create, list, read, renew and revoke, and the sentence on the temporary administrator grant. Check: `make docs-lint` passes, and the e2e step of 4.1 runs each command as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.
- [ ] 3.2 The answers of the open questions become ADRs or ADR amendments with the tag "(owner, YYYY-MM-DD)", and `make adr-index` regenerates `docs/adr/INDEX.md` and the decisions file. This change adds no ADR itself. Check: the generators leave no diff.

## 4. Definition of done

- [ ] 4.1 Add the NEW e2e file `e2e/delegation_to_dusty_test.go` against the real binary over TLS on PostgreSQL: sign in to the console, create a delegation, list, read, renew and revoke it, and read the entries. The existing `e2e/delegation_test.go` tests the act-as right and stays as it is. BLOCKED by the model connect change for the agent principal. Check: the step passes in CI.
- [ ] 4.3 Run the full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
