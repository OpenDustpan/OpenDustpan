## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing API tests for valid publish, bad SHA-256, missing signature, bad signature, expired key and unknown version in NEW file `pkg/registry/provider_api_test.go`. Check: TestRegistryProvider_PublishValidListsInMirror fails on main.
- [ ] 1.2 Add failing tests for team protocol discovery, versions list and download metadata in NEW file `pkg/registry/provider_team_test.go`. Check: TestRegistryProvider_TeamProtocolServesVersions fails on main.
- [ ] 1.3 Add failing tests for host match, safe name, traversal names, size limit, key checks and cross-namespace isolation in NEW file `pkg/registry/provider_safety_test.go`. Check: TestRegistryProvider_SafeAddressRefusesForeignHost fails on main.

## 2. Build

- [ ] 2.1 Build publish, key register and removal, staged upload, mirror index, team protocol and download with atomic and immutable writes in NEW file `pkg/registry/provider.go`. Check: `go test -race ./pkg/registry/...` passes.
- [ ] 2.2 Wire in NEW declaration file `internal/api/registry_routes.go` and NEW adapter file `internal/api/registry_handlers.go` the routes `GET/PUT /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions/{}`, `GET/POST /api/dustpan/v1/namespaces/{}/registry/keys` and `POST /api/dustpan/v1/namespaces/{}/registry/uploads` with an access token of a personal token, each with its authorization action on the namespace object and an audit entry. Check: `make e2e` runs `tofu init` with a personal token, fetches a private provider and refuses a bad signature and a bad checksum. Confirmed by: CI (the agent writes the e2e test).

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run all checks and fix findings in code. Check: `make check` passes with zero findings. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 4.2 Add the docs page and the usability check with the one helping command per error. Check: A Developer completes `tofu init` from the docs page alone against the real binary over TLS. A person's step.
