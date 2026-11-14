## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add a boundary test that fails on main: the NEW gatesvc package runs the same approver check cases as pkg/gate. Check: go test ./internal/gatesvc/... -run TestApproverCheckSameAfterMove

## 2. Move

- [ ] 2.1 Create internal/gatesvc and move ConfigService, checkApprovers, ApproverDirectory and MemberCounts. Rewire pkg/dustpan/gateconfig.go. Check: go build ./... and go test ./pkg/dustpan/...
- [ ] 2.2 List internal/gatesvc in the depguard domain rule of .golangci.yml. Check: golangci-lint run ./internal/gatesvc/...
- [ ] 2.3 Check that no caller keeps a moved name in pkg/dustpan/gateconfig.go and pkg/dustpan/area_installation.go. Check: rg -n 'gate\.(ConfigService|NewConfigService)' pkg/dustpan internal cmd prints nothing

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wiring: the route GET /api/dustpan/v1/namespaces/{namespace}/gates answers through the NEW service with the same authorization actions. The e2e step calls it with an access token. Confirmed by: CI (make e2e). Check: make e2e
- [ ] 4.2 Docs and usability: no user page changes. Confirmed by: CI (make docs-lint). Check: make docs-lint
- [ ] 4.3 Full check with zero findings. Confirmed by: CI (make check). Check: make check
