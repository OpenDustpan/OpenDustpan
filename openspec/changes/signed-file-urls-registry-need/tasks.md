## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests in pkg/registry for the NEW SignFileURL and VerifyFileURL: cross-instance verify, tamper, expiry, v=2, overlong query, empty key. Check: `go test ./pkg/registry -run TestCrossInstanceURLVerifies -count=1` fails on main
- [ ] 1.2 Replace the random key with the NEW newSigningKey derived from the stored key-encryption key, a stored secret; keep dustpan.WithRegistryURLKey for tests and embedders only. Check: `go test ./pkg/dustpan -run TestRegistryURLKeyDerivedFromKEK -count=1` passes

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 3. Definition of done

- [ ] 3.1 Wire the NEW SignFileURL and VerifyFileURL into the existing route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions; the sign step passes the registry read grant gate with an access token credential kind; the verify step reads no database. Check: internal/servertest with access token on route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate: sign on one server, fetch on a second, returns 200
- [ ] 3.2 Add an e2e step on the existing route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate with an access token credential kind that signs on one instance and fetches the file on the other, including the 503 retry. Check: `make e2e` on route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate with an access token credential kind: tofu init through a two-instance proxy shows no 403
- [ ] 3.3 Update the several-instances guide and remove the one-instance registry note. Check: Docs page commands run as written
- [ ] 3.4 Run the usability check and make check. Check: `make check` reports zero findings
