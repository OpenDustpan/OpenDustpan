## 1. Failing tests first

- [ ] 1.1 Add failing API tests for the generic locked-section reason and the same answer for unseen and nonexistent objects. Check: `go test ./internal/api/... -run TestConsoleLockedReason -count=1` fails on main, passes after the fix.

## 2. Wiring and checks

- [ ] 2.1 Run the console browser tests against the real server over TLS: add member, make group, give and revoke grant, download export, show expiry banner, open locked section. Check: `make e2e CONSOLE=1` runs the console browser tests against the TLS server and passes.
- [ ] 2.2 Write the docs page for the console access views with copy-paste commands. Check: Docs page commands run as written against the same server.

## 3. Definition of done

- [ ] 3.1 Route with authorization actions is present and the public list is unchanged. Check: Route with actions for the access endpoint is present in the route test.
- [ ] 3.2 Usability check with a site administrator. Check: Persona completes the path from the docs page alone and each error names the one command that helps.
- [ ] 3.3 `make check` with zero findings. Check: `make check` reports zero findings.
