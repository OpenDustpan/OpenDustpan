## 1. The shared check

- [ ] 1.1 `internal/api/triggers/routes.go`: the local `authorizeRoute` and `readRefusal` go; the call site uses `route.Guard{Refusals: h.guard}.AuthorizeObject` with `route.Checks{Namespace: ..., Reads: route.Reads, Actions: route.Actions, NotFound: errNotFound, RequireAction: false}`. Check: `TestAddressOutsideTheAllowlistAnswers403` in the package, and the existing not-found tests still answer 404.
- [ ] 1.2 `internal/api/objectcontext/routes.go`: the same, and the caller keeps the rule that drops the namespace read when the object is the namespace. Check: the same two tests in the package.
- [ ] 1.3 `internal/api/settings/routes.go`: the same, including the call at `internal/api/settings/serve.go:499`. Check: the same two tests in the package.
- [ ] 1.4 `rg 'func .*readRefusal|func .*authorizeRoute' internal/api/triggers internal/api/objectcontext internal/api/settings` finds no line. Check: the search.

## 2. The tests

- [ ] 2.1 `internal/api/triggers/ipallow_test.go` (new): `TestAddressOutsideTheAllowlistAnswers403` joins `identity.ErrDenied` and `ipallow.ErrAddressNotAllowed` in the authorizer of the namespace read, sends a read of a unit trigger, and expects 403 `access.ip-not-allowed` with the message `forbidden`. Check: restore the local `readRefusal`; the test answers 404 `TriggerNotFound`.
- [ ] 2.2 `internal/api/objectcontext/ipallow_test.go` (new): the same for a context read, with the 404 `ContextObjectNotFound` as the failing answer.
- [ ] 2.3 `internal/api/settings/ipallow_test.go` (new): the same for a unit settings read, with the 404 `UnitSettingsNotFound` as the failing answer.
- [ ] 2.4 Each test sends the read for a real object, an unknown object, an unknown namespace and a malformed ID, as `internal/api/jobs/ipallow_test.go` does. Check: each path answers the same 403, and the refusal names no object.
- [ ] 2.5 The existing not-found tests of the three packages stay green. Check: `go test ./internal/api/triggers/... ./internal/api/objectcontext/... ./internal/api/settings/...`.

## 3. The e2e step and the docs

- [ ] 3.1 `e2e/ipallow_test.go`: the step reads one route of each surface from outside the list and expects 403 with `access.ip-not-allowed`, as the `override-switches` step does. Check: `go test -tags e2e ./e2e/ -run TestIPAllowlists`.
- [ ] 3.2 Docs: no change. `docs/guides/ip-allowlists.md:104-105` and `GLOSSARY.md:191` already state the 403, and the fix makes the code agree. Check: `make docs-lint` exits 0.
- [ ] 3.3 `make check` reports zero findings.
