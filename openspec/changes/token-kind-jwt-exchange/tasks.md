## 1. Failing tests first

- [ ] 1.1 Add `TestCredentialOfEachKind` and `TestRevokeScopeOfEachKind` in `pkg/accesstoken`, with a row for `jwt-exchange`. Check: `go test ./pkg/accesstoken/ -run 'TestCredentialOfEachKind|TestRevokeScopeOfEachKind'` fails on main (the kind does not exist).

## 2. Build

- [ ] 2.1 In `pkg/accesstoken/accesstoken.go:75`, replace `KindTrust` with the NEW kind `KindJWTExchange` and add the NEW method `Kind.FromExchange`. Use it in `credentialOf` in `pkg/accesstoken/service.go:693` and in `authorizeRevoke` in `pkg/accesstoken/scope.go:167`. Check: the tests of 1.1 pass.
- [ ] 2.2 Change `pkg/accesstoken/trust.go:42`, `pkg/store/internal/trustrulestore/trustrulestore.go:206`, `pkg/trustrule/trustruletest/exchange_cases.go:36` and `:93`, `pkg/accesstoken/trust_token_test.go:69` and `internal/trustrulesvc/exchange_test.go:131` and `:340` to the new constant. Check: `go test -race ./pkg/accesstoken/... ./pkg/trustrule/... ./internal/trustrulesvc/...` and `go test -race -timeout 60m ./pkg/store/sqlite/... ./pkg/store/postgres/...` pass.
- [ ] 2.3 Change `docs/guides/workload-identity.md` and `GLOSSARY.md` to the kind `jwt-exchange`. Check: `make docs-lint` exits 0, and `rg -n 'kind .trust.' docs GLOSSARY.md` finds no line outside `docs/adr` and `docs/rfc`.

## 3. Definition of done

- [ ] 3.1 The e2e workload exchange still passes: `TestWorkloadTokenExchange` calls `POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange` with the workload JWT of the test issuer, gets a trust token and runs both CLIs with it. Check: `make e2e` runs `TestWorkloadTokenExchange` and it passes. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.2 Run `make check`. Check: zero findings. Confirmed by: CI (the agent runs the fast checks only).
