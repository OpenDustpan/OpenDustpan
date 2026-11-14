## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. failing tests

- [ ] 1.1 Add failing tests in NEW file `pkg/secret/namespacekey_test.go` for Wrap double wrap, single wrap, per-namespace context, ProbeConfusedDeputy fail-closed, hostile KMS answers, and concurrent wrap. Check: NEW file `pkg/secret/namespacekey_test.go` TestNamespaceKeyWrap fails on main, passes after.
- [ ] 1.2 Record the GCM 2^32 bound and the per-stream HKDF contrast in a comment in `pkg/secret/secret.go`. Check: NEW file `pkg/secret/namespacekey_test.go` TestGCMBoundNote checks the 2^32 bound text in `pkg/secret/secret.go`.

## 2. build

- [ ] 2.1 Add the NEW Wrapper port with Wrap, Unwrap, and ProbeConfusedDeputy in NEW file `pkg/secret/namespacekey.go` and the namespace key record with wrapper IDs and KMS key version. Check: `go test ./pkg/secret -run TestNamespaceKey -count=1` passes.

## 3. Side effects (computed from main)

- [ ] 3.1 Check the callers that type-assert on `Wrapper`: `pkg/store/storetest/delete_audit.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. definition of done

- [ ] 4.1 Verify wiring, usability check, and `make check` with zero findings; no route, no e2e step, and no docs page in this part. Check: `go test ./pkg/secret -count=1` passes and `make check` reports zero findings.
