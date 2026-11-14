## 1. failing tests first

- [ ] 1.1 Add failing fact tests for touches, only, empty list, unclean paths, deleted sets, and missing. Check: `go test ./pkg/gate/ -run TestPathSetFacts -count=1`
- [ ] 1.2 Add failing port tests for pattern count, ID grammar, and scope ID reuse with 400 body. Check: `go test ./pkg/gate/ -run TestPathSetValidation -count=1`
- [ ] 1.3 Add failing service tests for reads and writes with no grant with 403 and denial audit. Check: `go test ./pkg/gate/ -run TestPathSetAuthz -count=1`
- [ ] 1.4 Add failing truncation tests that empty largest sets until 256 KiB fits and mark path facts unavailable. Check: `go test ./pkg/gate/ -run TestPathSetTruncation -count=1`
- [ ] 1.5 Add failing race test for save of one ID at two scopes and save against scope delete. Check: `go test ./pkg/gate/ -run TestPathSetScopeRace -count=1`

## 2. implementation

- [ ] 2.1 Implement path set store, constructor checks, and fact producer. Check: `go test ./pkg/gate/... -count=1`
- [ ] 2.2 Wire PUT routes with If-Match, authorization actions, 412 on stale version, and audit entries. Check: `go test ./internal/gatesvc/... -count=1`
- [ ] 2.3 Add the user docs page for path sets with tested glob samples. Check: `go test ./pkg/gate/ -run TestPathSetGlobSamples -count=1 && make docs-lint`

## 3. definition of done

- [ ] 3.1 Run an e2e step that edits modules/net/a.tf and checks touches and only facts. Check: `make e2e` path set step against the real CLI
- [ ] 3.2 Pass wiring, route, e2e step, docs page, usability check, and make check. Check: `make check` with zero findings
