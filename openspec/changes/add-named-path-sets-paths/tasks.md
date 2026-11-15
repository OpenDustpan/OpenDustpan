## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Failing fact tests in NEW pkg/gate/pathset_facts_test.go for touches, only, empty list, cleaned paths, deleted sets and missing. Check: `go test ./pkg/gate/ -run TestPathSetFacts -count=1`
- [ ] 1.2 Failing validation tests in NEW pkg/gate/pathset_test.go for count, ID grammar, shadow rule and size caps, through NewPathSet (NEW). Check: `go test ./pkg/gate/ -run TestPathSetValidation -count=1`
- [ ] 1.3 Failing authorization tests in pkg/gate/service_test.go with 403 and denial audit. Check: `go test ./pkg/gate/ -run TestPathSetAuthz -count=1`
- [ ] 1.4 Failing truncation tests in pkg/gate/facts_test.go that mark path facts unavailable. Check: `go test ./pkg/gate/ -run TestPathSetTruncation -count=1`
- [ ] 1.5 Failing race tests in NEW pkg/gate/pathset_race_test.go for one ID at two scopes and save against scope delete. Check: `go test ./pkg/gate/ -run TestPathSetScopeRace -count=1`
- [ ] 1.6 Failing encoding tests in pkg/gate/fuzz_test.go for hostile pattern text and traversal paths. Check: `go test ./pkg/gate/ -run TestPathSetHostileInput -count=1`

## 2. implementation

- [ ] 2.1 Implement the store, matching and fact producer in NEW pkg/gate/pathset.go. Check: `go test ./pkg/gate/... -count=1`
- [ ] 2.2 Wire the new path set routes at each scope with authorization actions and audit entries. Declare them in the route table of the adapter package (`make generate` then writes internal/api/apidesc/openapi.json; never edit it by hand) and write the adapter in NEW internal/api/route/pathsets.go. Check: `go test ./internal/api/route/... -count=1`
- [ ] 2.3 Add the new docs page NEW docs/path-sets.md with glob samples that a test in NEW pkg/gate/pathset_samples_test.go runs. Check: `go test ./pkg/gate/ -run TestPathSetGlobSamples -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. definition of done

- [ ] 4.1 E2E step: with a personal access token of a namespace administrator, send PUT then GET to /api/dustpan/v1/namespaces/{namespace}/path-sets/{id} (routes that this change adds), then check the facts for modules/net/a.tf. BLOCKED by the path-sets routes until the implementation tasks land. Check: `make e2e`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 4.2 Wiring, route, docs page and usability check from the docs page alone. Check: `make check`. A person's step. Confirmed by: CI (the agent runs the fast checks only).
