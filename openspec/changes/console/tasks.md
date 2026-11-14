## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Wiring first

- [ ] 1.1 Write the failing browser job: add member, make group, give and revoke grant, download export, banner and locked section. It calls GET /api/server/access-review and POST /api/server/grants with a session cookie over TLS. Check: `make e2e CONSOLE=1` fails on main and passes after the wiring; BLOCKED by the console repository job and the depends_on changes.
- [ ] 1.2 Add NEW test e2e TestConsoleBrowserWiring. It reads the audit entries of each browser step with GET /api/audit/export and an access token of the site administrator. Check: `go test ./e2e/ -run TestConsoleBrowserWiring -count=1` (NEW test).

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Write the docs page for the console access views with copy-paste commands. Check: A test runs each docs page command as written over TLS.
- [ ] 3.2 Confirm that the routes the job uses are in the route test with their actions. Check: The route test passes and the public list is unchanged.
- [ ] 3.3 Usability check with a site administrator. Check: The persona completes the path from the docs page alone and each error names the one command that helps.
- [ ] 3.4 Run the full check. Check: `make check` reports zero findings.
