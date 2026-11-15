## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing server tests for agent principal, create, bad input, list own, read one and namespace list in the NEW file pkg/access/delegation_test.go. Check: TestCreateDelegationStoresHolderScopeCreatorTime calls the NEW route POST /api/dustpan/v1/delegations with a personal access token; BLOCKED by the NEW delegation routes, so it fails on main with 404.
- [ ] 1.2 Add failing server tests for creator revoke, administrator revoke, repeat revoke and unknown id in the NEW file pkg/access/delegation_revoke_test.go. Check: TestRevokeDelegationMarksRevoked calls the NEW route POST /api/dustpan/v1/delegations/{id}/revoke with a personal access token; BLOCKED by the NEW delegation routes, so it fails on main with 404.

## 2. build

- [ ] 2.1 Make the NEW agent principal kind and the dusty record on model connect in the NEW file pkg/access/agent.go. Check: NEW identity kind agent with stable ID dusty exists after model connect.
- [ ] 2.2 Wire delegation service, store rows and NEW routes with actions delegation:create, delegation:read and delegation:revoke plus audit entries for create and revoke. Declare the routes in the NEW file internal/api/apidesc/delegations.go and write the adapter in the NEW file internal/api/delegations.go. Put the service in the NEW file pkg/access/delegation.go. Check: NEW route POST /api/dustpan/v1/delegations with action delegation:create on the delegation object and fenced write in the scope namespace stores a dlg- row with holder dusty.
- [ ] 2.3 Extend the existing file e2e/delegation_test.go with an e2e step for person delegates to Dusty against the real binary. Check: The e2e step uses a personal access token, creates one delegation through the NEW route POST /api/dustpan/v1/delegations, lists it through the NEW route GET /api/dustpan/v1/delegations and revokes it through the NEW route POST /api/dustpan/v1/delegations/{id}/revoke; BLOCKED by the NEW delegation routes until they are wired. It runs on PostgreSQL over TLS. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.4 Write docs page for delegation with exact commands in the NEW file docs/guides/delegation.md. Check: Docs page commands create, list, read and revoke a delegation as written. `make docs-lint` passes on each edited guide.

- [ ] 2.5 SECURITY. Build the store predicate in the NEW file pkg/access/delegation.go. The predicate refuses an expiry more than 30 days after creation and a holder other than dusty in the same transaction as the insert. Check: the store returns the NEW error code delegation.invalid-expiry for a 31-day expiry and refuses a holder other than dusty.
- [ ] 2.6 SECURITY. Add the hostile store-level test TestStoreRefusesBadExpiryAndHolder in the existing file pkg/access/delegation_test.go. The test inserts a 31-day expiry and a holder other than dusty directly at the store. Check: both inserts are refused and no row is stored.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` (`go test ./pkg/dustpan -run TestAPIDescriptionGolden -update-api-description`) and then the clients (`make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 Run usability check of the delegation path. Check: Developer completes create, list, read and revoke from the docs page alone. A person's step.
- [ ] 4.2 Run full check suite. Check: `make check` with zero findings. Confirmed by: CI (the agent runs the fast checks only).
