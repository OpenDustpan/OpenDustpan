## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write failing tests named for each scenario: trust set, clear, closed group, race, write group, full sync on and off, final removals, refusals. Check: `go test ./pkg/principal/... ./internal/api/server/... ./internal/cli/... -count=1` fails on main.

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `internal/db` (`make generate`, sqlc). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Add the new route PUT and DELETE /api/server/groups/{id}/trust. Its action is server:manage-sign-in on the installation, the object is the group. The audit entry holds the group ID, the setter, the time and the old and new value. Edit the baselines and the backup table list. Add the docs page. Check: `curl -X PUT $SERVER/api/server/groups/$ID/trust` with a site administrator access token returns 204 in the e2e step; `sqlc diff` is clean.
- [ ] 3.2 Add the e2e step against the real server with a site administrator access token. Run the usability check and the full gate. Check: `make e2e` and `make check` with zero findings.
