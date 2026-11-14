## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. failing tests first

- [ ] 1.1 Failing fact tests for touches, only, empty list, cleaned paths, deleted sets and missing. Check: `go test ./pkg/gate/ -run TestPathSetFacts -count=1`
- [ ] 1.2 Failing validation tests for count, ID grammar, shadow rule and size caps, through NewPathSet. Check: `go test ./pkg/gate/ -run TestPathSetValidation -count=1`
- [ ] 1.3 Failing authorization tests with 403 and denial audit. Check: `go test ./pkg/gate/ -run TestPathSetAuthz -count=1`
- [ ] 1.4 Failing truncation tests that mark path facts unavailable. Check: `go test ./pkg/gate/ -run TestPathSetTruncation -count=1`
- [ ] 1.5 Failing race tests for one ID at two scopes and save against scope delete. Check: `go test ./pkg/gate/ -run TestPathSetScopeRace -count=1`
- [ ] 1.6 Failing encoding tests for hostile pattern text and traversal paths. Check: `go test ./pkg/gate/ -run TestPathSetHostileInput -count=1`

## 2. implementation

- [ ] 2.1 Implement the store, matching and fact producer. Check: `go test ./pkg/gate/... -count=1`
- [ ] 2.2 Wire the new path set routes at each scope with authorization actions and audit entries. Check: `go test ./internal/api/route/... -count=1`
- [ ] 2.3 Add the new docs page with glob samples that a test runs. Check: `go test ./pkg/gate/ -run TestPathSetGlobSamples -count=1`

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 E2E step: with a personal access token of a namespace administrator, call PUT and GET on /api/dustpan/v1/namespaces/{namespace}/path-sets/{id}, then check the facts for modules/net/a.tf. Check: `make e2e`
- [ ] 4.2 Wiring, route, docs page and usability check from the docs page alone. Check: `make check`
