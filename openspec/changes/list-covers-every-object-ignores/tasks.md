## 1. Failing tests first

- [ ] 1.1 Add conformance row that leaves out a namespace and checks its objects are absent from lists. Check: `go test ./pkg/identity/identitytest -run TestCheckListWithinAuthorize -count=1` fails on main, then passes.
- [ ] 1.2 Add ipallow store conformance rows for empty, ordered, removed, limit, and past-limit cases. Check: `go test ./pkg/store/storetest -run TestIPAllowlistNamespacesWithList -count=1` fails on main, then passes.

## 2. Fix

- [ ] 2.1 Fill excluded namespaces in the IP allowlist gate and apply them in the namespace filter of both dialects, deleted-units read scope, namespace list, and caller-access API. Check: `go test ./pkg/identity/identitytest ./pkg/access/... ./pkg/store/... -count=1` passes plus `make e2e` namespace list step with an allowlist and an outside address hides the namespace.

## 3. Definition of done

- [ ] 3.1 Update the API description, the docs page, and the usability check. Check: Caller-access response shows exclusions next to all, `GET /api/server/namespaces` hides the refused namespace, and the docs page runs as written.
- [ ] 3.2 Run the full definition of done: wiring, route with authorization actions, e2e step against the real CLI, docs page, usability check, make check. Check: `make check` passes with zero findings.
