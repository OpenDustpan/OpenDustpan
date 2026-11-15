## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing tests in pkg/registry for the NEW SignFileURL and VerifyFileURL: cross-instance verify, tamper, expiry, v=2, overlong query, empty key. Check: `go test ./pkg/registry -run TestCrossInstanceURLVerifies -count=1` fails on main
- [ ] 1.2 In pkg/dustpan/registry.go (func newRegistryProtocol), replace the random key with the NEW newSigningKey in NEW file pkg/registry/signingkey.go, derived from the stored key-encryption key, a stored secret; keep dustpan.WithRegistryURLKey (pkg/dustpan/registry.go:47) for tests and embedders only. Check: `go test ./pkg/dustpan -run TestRegistryURLKeyDerivedFromKEK -count=1` passes

- [ ] 1.3 Build SignFileURL and VerifyFileURL in NEW file pkg/registry/signurl.go with the length-prefixed HMAC over version, namespace ID, file path and expiry, a constant-time compare, the v=1 version check and a 2048 byte length cap. Check: `go test ./pkg/registry -run TestCrossInstanceURLVerifies -count=1` passes
- [ ] 1.4 SECURITY. Add a hostile test in NEW file pkg/registry/signurl_grant_test.go, test name TestSignFileURLWithoutReadGrantRefused, that calls SignFileURL without the registry read grant. Check: `go test ./pkg/registry -run TestSignFileURLWithoutReadGrantRefused -count=1` passes and no URL is made

## 2. Side effects (computed from main)

- [ ] 2.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Wire the NEW SignFileURL and VerifyFileURL into the existing route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions; the sign step passes the registry read grant gate with an access token credential kind; the verify step reads no database. Check: internal/servertest with access token on route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate: sign on one server, fetch on a second, returns 200
- [ ] 3.2 Add an e2e step in the NEW file e2e/signed_file_urls_registry_need_test.go on the existing route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate with an access token credential kind that signs on one instance and fetches the file on the other, including the 503 retry. Check: `make e2e` on route GET /api/dustpan/v1/namespaces/{}/registry/providers/{}/versions through the registry read grant gate with an access token credential kind: tofu init through a two-instance proxy shows no 403. Confirmed by: CI (the agent writes the e2e test).
- [ ] 3.3 Update the several-instances guide and remove the one-instance registry note. Check: Docs page commands run as written. Confirmed by: CI (an e2e step in the NEW file e2e/signed_file_urls_registry_need_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 3.4 Run the usability check and make check. Check: `make check` reports zero findings. A person's step. Confirmed by: CI (the agent runs the fast checks only).
