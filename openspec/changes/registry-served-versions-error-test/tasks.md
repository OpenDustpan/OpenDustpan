## 1. Test

- [ ] 1.1 Add `TestServedVersionsReadError` (NEW) in `internal/api/registry/registry_test.go`: with a registry service whose `ServableReleases` returns an error that is not `registry.ErrNotFound`, the routes GET /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions and GET /api/dustpan/v1/namespaces/{namespace}/registry/providers/{name}/versions/{version} with an access token that holds the read action answer 500 with the generic error text and no list or release. Run it once with the error return of `servedVersions` (`internal/api/registry/handlers.go`) removed, and paste the failing run into the pull request body. Check: `go test ./internal/api/registry/ -run TestServedVersionsReadError -count=1` passes, and the pull request body holds the failing run.

## 2. Docs

- [ ] 2.1 In the section "Publish" of `docs/reference/registry-api.md`, say that a publish replayed with the same `Idempotency-Key` answers the stored answer of the first publish, so the release in it is the release at the time of the first publish, and that a GET of the version gives the current state. Check: `make docs-lint` passes.

## 3. Definition of done

- [ ] 3.1 Run the checks. Check: `make check` reports zero findings. Confirmed by: CI (the agent runs the fast checks only).
