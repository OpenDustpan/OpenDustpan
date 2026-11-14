## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Add failing domain test for publisher ID beside subject on PublishRelease and AddKey in NEW file pkg/registry/publisher_test.go, with bootstrap, removed, and recreated principal cases Check: TestRegistryPublisherStoresIDBesideSubject in pkg/registry fails on main, passes after
- [ ] 1.2 Add failing handler test in NEW file pkg/dustpan/registry_host_test.go that the mirror builds URLs from the configured base with mixed-case and alias Host values and writes no audit entry Check: TestRegistryMirrorIgnoresHostHeader fails on main, passes after

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. definition of done

- [ ] 3.1 Wire publisher ID beside subject through service, store, and REST versions route Check: GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{} with access token answers publisher ID and subject, including bootstrap empty-ID case
- [ ] 3.2 Wire adder ID beside subject through service, store, and REST keys route Check: GET /api/dustpan/v1/namespaces/{}/registry/keys with access token answers adder ID and subject
- [ ] 3.3 Update baselines with the new nullable publisher and adder ID columns Check: Edit the single baseline of each dialect in place; dialect store tests pass
- [ ] 3.4 Add e2e step for the mirror read on the configured base with no credential Check: GET /api/dustpan/v1/namespaces/{}/registry/providers with no credential answers the mirror index built from the configured base
- [ ] 3.5 Write docs page for who published and the configured mirror host Check: Docs page runs each curl as written from the user path
- [ ] 3.6 Run usability check and full check Check: `make check` with zero findings
