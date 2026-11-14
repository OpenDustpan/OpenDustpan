## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Work

- [ ] 1.1 Add failing trigger-start authorization tests in NEW pkg/trigger/triggerstart_auth_test.go. BLOCKED by the actor item in depends_on for the stored run-as field. Check: NEW pkg/trigger/triggerstart_auth_test.go fails on main: allow, no-grant 403 with audit, revoked grant, suspended principal.
- [ ] 1.2 Delete NamespaceServiceRule in pkg/identity/namespace_service.go and the authorizeNamespaceService branch in pkg/access/authorizer.go; route every trigger start through stored grants. Check: rg -n NamespaceServiceRule pkg internal returns no match.

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the trigger start path in pkg/dustpan/triggers.go with its unit:run-plan authorization action and stored-grant check. Check: Route test passes for trigger start with stored-grant authorization actions.
- [ ] 3.2 Update the note in docs/reference/code-rules.md for the removed rule and the glossary lines for the namespace service principal. Confirmed by: CI (make docs-lint). Check: docs/reference/code-rules.md names the removed rule as removed and `make check` reports zero findings.
- [ ] 3.3 Run `make check` with zero findings. Confirmed by: CI (make check). Check: `make check` reports zero findings.
