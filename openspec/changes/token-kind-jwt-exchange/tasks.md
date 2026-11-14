## 1. Failing tests first

- [ ] 1.1 Add `TestCredentialOfEachKind` and `TestRevokeScopeOfEachKind` in `pkg/accesstoken`, with a row for `jwt-exchange`. Check: `go test ./pkg/accesstoken/ -run 'TestCredentialOfEachKind|TestRevokeScopeOfEachKind'` fails on main (the kind does not exist).

## 2. Build

- [ ] 2.1 Replace `KindTrust` with the NEW kind `KindJWTExchange` and add the NEW method `Kind.FromExchange`. Use it in `credentialOf` and `authorizeRevoke`. Check: the tests of 1.1 pass.
- [ ] 2.2 Change the store, the conformance suite and the tests to the new constant. Check: `go test -race ./pkg/accesstoken/... ./pkg/trustrule/... ./internal/trustrulesvc/...` and `go test -race -timeout 60m ./pkg/store/sqlite/... ./pkg/store/postgres/...` pass.
- [ ] 2.3 Change `docs/guides/workload-identity.md` and `GLOSSARY.md` to the kind `jwt-exchange`. Check: `make docs-lint` exits 0, and `rg -n 'kind .trust.' docs GLOSSARY.md` finds no line outside `docs/adr` and `docs/rfc`.

## 3. Definition of done

- [ ] 3.1 The e2e workload exchange still passes: `TestWorkloadTokenExchange` calls `POST /api/dustpan/v1/namespaces/{namespace}/trust-rules/exchange` with the workload JWT of the test issuer, gets a trust token and runs both CLIs with it. Check: `make e2e` runs `TestWorkloadTokenExchange` and it passes.
- [ ] 3.2 Run `make check`. Check: zero findings.
