## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests named for each scenario: trust set, clear, closed group, race, write group, full sync on and off, final removals, refusals. Put the tests in `pkg/access/access_test.go` (near `TestGroupGrants` at line 394) and in NEW `pkg/principal/grouptrust_test.go`, NEW `internal/api/server/grouptrust_test.go` and NEW `internal/cli/settings_fullsync_test.go`. Check: `go test ./pkg/principal/... ./internal/api/server/... ./internal/cli/... -count=1` fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Add the new route PUT and DELETE /api/server/groups/{id}/trust. Declare it in the route table `internal/api/server/routes.go`, add its apidesc label in `internal/api/server/operations.go` (joinLabels, the one join point of the labels), and write its handler in the NEW file `internal/api/server/grouptrust.go`. Write the group trust type and its constructor in the NEW file `pkg/principal/grouptrust.go`. Write the docs page in the NEW file `docs/guides/group-trust-identity-provider.md`. Its action is server:manage-sign-in on the installation, the object is the group. The audit entry holds the group ID, the setter, the time and the old and new value. Edit the baselines and the backup table list. Add the docs page. BLOCKED by add-one-generic-settings-api for the full sync route. Check: `curl -X PUT $SERVER/api/server/groups/$ID/trust` with the access token of a site administrator principal returns 204 in the e2e step; `sqlc diff` is clean. `make docs-lint` passes on each edited guide.
- [ ] 3.2 BLOCKED by add-one-generic-settings-api for the full sync part. For trust: add the e2e step against the real server on the existing route PUT and DELETE /api/server/groups/{id}/trust, with the access token of a site administrator principal. Run the usability check and the full gate. Check: `make e2e` and `make check` with zero findings.
