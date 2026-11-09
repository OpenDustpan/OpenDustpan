## 1. The cursor

- [ ] 1.1 `internal/api/server/namespaces.go`: `namespaceListFingerprint(filter)` returns `pagetoken.Fingerprint` of `all`, or of the sorted names of the filter. Check: a unit test of two filters gives two fingerprints, and the same filter gives one.
- [ ] 1.2 `parseNamespaceList` takes the fingerprint; `readNamespacePage` seals the cursor with `h.cursors.Wrap` and an AAD of the fingerprint; the opener unwraps with the same AAD, calls `pagetoken.Decode` and checks `pagetoken.SameFingerprint`. Check: `TestNamespaceListCursorOfAnotherFilterIsRefused` and `TestNamespaceListCursorWithoutSealIsRefused` fail when the seal or the check goes.
- [ ] 1.3 The comment on the fingerprint check says that it is a defense-in-depth layer and that the AAD of the seal refuses a cursor of another query too. Check: `golangci-lint run ./internal/api/server/...` reports nothing, and the review of the diff sees the comment.
- [ ] 1.4 `internal/api/server/server.go`: `Config.Cursors` is a `secret.Wrapper`, and the Handler keeps it. `pkg/dustpan/area_installation.go` and `pkg/dustpan/dustpan.go` pass `cfg.secretWrapper`, as the jobs handler gets it. Check: a `pkg/dustpan` test lists namespaces twice through the installation server, gets a sealed cursor, and gets 400 for a cursor of another filter.

## 2. The tests

- [ ] 2.1 `internal/api/server/namespaces_list_test.go`: `TestNamespaceListCursorOfAnotherFilterIsRefused` pages as the administrator, takes `nextCursor`, and sends it as a caller with one visible namespace; expects 400 `request.page-token-invalid`. Check: remove the fingerprint from the AAD and the check; the test answers 200.
- [ ] 2.2 `TestNamespaceListCursorWithoutSealIsRefused` takes a cursor from a handler without a sealer and sends it to a handler with a sealer; expects the same 400. Check: return the decoded name without `Unwrap`; the test answers 200.
- [ ] 2.3 `TestNamespaceListBadQuery` gains a token that is not base64url, a token that is not JSON, an unknown field, an empty `after`, a name that is not a namespace name, a changed byte, and a token above `pagetoken.MaxLen`. Check: each case answers 400 with the same code.
- [ ] 2.4 `TestNamespaceListCursorWithAnotherLimitIsAccepted` sends a cursor with another `limit`. Check: put the limit in the fingerprint; the test fails.
- [ ] 2.5 `TestNamespaceListPagesAll` walks the list with the seal on. Check: each namespace is in the walk once, and the last page has no cursor.
- [ ] 2.6 `e2e/namespace_list_test.go` (new): `TestNamespaceListCursor` lists the namespaces as one principal, takes `nextCursor`, and calls again as a principal with another scope. Check: `go test -tags e2e ./e2e/ -run TestNamespaceListCursor`.

## 3. Docs and checks

- [ ] 3.1 `docs/reference/namespaces-api.md` (new): the list route, its `limit` and `cursor` parameters, the answer fields, and the rule that a cursor works only with the query that made it, and that another query answers 400 `request.page-token-invalid`. Check: `make docs-lint` exits 0.
- [ ] 3.2 `go test ./internal/api/server/... ./pkg/dustpan/...` and `go vet ./internal/api/server/...` report nothing.
- [ ] 3.3 `make check` reports zero findings.
