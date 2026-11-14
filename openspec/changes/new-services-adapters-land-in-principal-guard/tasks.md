## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write a test that builds each NEW pkg/dustpan constructor and gets a working adapter. Check: `go test ./pkg/dustpan/...` fails on main

## 2. Move

- [ ] 2.1 Move NamespaceServiceGuard to internal/principalsvc and update the callers Check: `go build ./...` passes and `rg -n 'principal\.NamespaceServiceGuard'` has no output
- [ ] 2.2 Move pkg/forge/github to internal/forgegithub and the three renderers to NEW packages under internal/notify Check: `go build ./...` passes and `rg -n 'pkg/forge/github|pkg/notify/chat/(teams|mattermost|matrix)'` has no output in code, docs and config

## 3. Wiring

- [ ] 3.1 Add the NEW pkg/dustpan constructors and wire them in the server Check: `go test ./pkg/dustpan/...` passes

## 4. Definition of done

- [ ] 4.1 Add the NEW internal packages to the depguard domain rule in .golangci.yml. Run `make check`. Confirmed by: the driver's gates. Check: `make check` with zero findings
- [ ] 4.2 Run the e2e step with the real CLI. The CLI calls the existing route GET /api/dustpan/v1/capabilities with a session credential. The route passes the sign-in gate of the server, and the moved adapters are wired. Confirmed by: the driver's gates. Check: `make e2e` passes. Credential kind: session.
