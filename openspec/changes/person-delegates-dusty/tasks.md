## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing server tests for agent principal, create, bad input, list own, read one and namespace list. Check: TestCreateDelegationStoresHolderScopeCreatorTime calls the NEW route POST /api/dustpan/v1/delegations with a personal access token; BLOCKED by the NEW delegation routes, so it fails on main with 404.
- [ ] 1.2 Add failing server tests for creator revoke, administrator revoke, repeat revoke and unknown id. Check: TestRevokeDelegationMarksRevoked calls the NEW route POST /api/dustpan/v1/delegations/{id}/revoke with a personal access token; BLOCKED by the NEW delegation routes, so it fails on main with 404.

## 2. build

- [ ] 2.1 Make the NEW agent principal kind and the dusty record on model connect. Check: NEW identity kind agent with stable ID dusty exists after model connect.
- [ ] 2.2 Wire delegation service, store rows and NEW routes with actions delegation:create, delegation:read and delegation:revoke plus audit entries for create and revoke. Check: NEW route POST /api/dustpan/v1/delegations with action delegation:create on the delegation object and fenced write in the scope namespace stores a dlg- row with holder dusty.
- [ ] 2.3 Add e2e step for person delegates to Dusty against the real binary. Check: The e2e step uses a personal access token, creates one delegation through the NEW route POST /api/dustpan/v1/delegations, lists it through the NEW route GET /api/dustpan/v1/delegations and revokes it through the NEW route POST /api/dustpan/v1/delegations/{id}/revoke; BLOCKED by the NEW delegation routes until they are wired. It runs on PostgreSQL over TLS.
- [ ] 2.4 Write docs page for delegation with exact commands. Check: Docs page commands create, list, read and revoke a delegation as written.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Run usability check of the delegation path. Check: Developer completes create, list, read and revoke from the docs page alone.
- [ ] 4.2 Run full check suite. Check: `make check` with zero findings.
