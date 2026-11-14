## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing replay test for a repeat publish with the same Idempotency-Key in NEW internal/api/idempotent/registry_replay_test.go Check: TestRegistryReplayReturnsFirstPublishAnswer fails on main, passes after fix
- [ ] 1.2 Add failing concurrent replay test for the same Idempotency-Key in NEW internal/api/idempotent/registry_replay_test.go Check: TestRegistryConcurrentReplayMakesOneRelease fails on main, passes after fix
- [ ] 1.3 Add failing served flag test for module and provider version lists in NEW pkg/registry/served_test.go Check: TestRegistryVersionListHasServedFlag fails on main, passes after fix

## 2. Fix

- [ ] 2.1 Return the stored first answer on replay and refuse a fingerprint mismatch with 409 in internal/api/idempotent and internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes) Check: POST /api/dustpan/v1/namespaces/{}/registry/uploads with a reused key returns the first publisher subject and time, using an access token
- [ ] 2.2 Add the served flag to module and provider version lists from the read-only helper in pkg/registry and internal/api/tfe/registry_modules.go:99 (func Handler.moduleRoutes) Check: GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions and GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions entries carry served, using an access token

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run an e2e `tofu init` step against the real OpenTofu CLI. It calls GET /api/dustpan/v1/namespaces/{}/registry/modules/{}/{}/versions and POST /api/dustpan/v1/namespaces/{}/registry/uploads with an access token, through the registry read and write authorization gates. Add the docs page and check the usability from the docs page alone. Check: The e2e step shows served flags, `tofu init` picks only served releases, the docs page shows the flag, `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
