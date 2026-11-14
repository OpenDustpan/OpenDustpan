## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: `pkg/identity/identity.go:156` and the action block at `:158-651`, `pkg/access/access.go:115` and `:229`, `pkg/access/service.go:1040-1043`, `pkg/access/access_test.go:490-493`, and the five uses named in the proposal. Record the differences in the notes under "Confirmed against the code". Check: the notes list each fact with its file and line.

## 1. Registry

- [ ] 1.1 In `pkg/identity/identity.go`, declare each core action with `define` (NEW, unexported) into an unexported registry, and add `Actions() []Action` (NEW) that returns a sorted copy. Check: `go build ./...` passes.
- [ ] 1.2 Add table tests in `pkg/identity/actions_test.go` (NEW): `Actions()` holds 125 actions, is sorted, holds no duplicate, and a change of the returned slice does not change the registry. Add tests in `pkg/identity/define_internal_test.go` (NEW) that `define` panics on an empty value, a value without `area:verb`, and a second record of the same value. Check: `go test ./pkg/identity/...` passes.
- [ ] 1.3 Change the constant declarations at `pkg/trigger/saver.go:32`, `:35` and `internal/agent/actions.go:66` to variables. Check: `go build ./...` and `go vet ./pkg/trigger/ ./internal/agent/` pass.

## 2. The family test

- [ ] 2.1 In `pkg/access/access_test.go`, move the action list of `TestActionsWithoutRoutesAreUnclassified` into one variable `unclassifiedActions` that the test reads. Check: `go test ./pkg/access/ -run TestActionsWithoutRoutesAreUnclassified` passes.
- [ ] 2.2 Add `TestEveryCoreActionIsClassifiedOrListed` (NEW) in `pkg/access/access_test.go`: for each action of `identity.Actions()`, `access.FamilyOf` answers a family, or the action is in `unclassifiedActions`; an action in `unclassifiedActions` has no family. Check: `go test ./pkg/access/ -run TestEveryCoreActionIsClassifiedOrListed` passes.

## 3. Verification

- [ ] 3.1 Prove the guard in `pkg/access/access.go:115`: remove `identity.ActionJobReadLogs` from the family table; `TestEveryCoreActionIsClassifiedOrListed` fails and names `job:read-logs`. Restore it. Check: the failure text holds the action.
- [ ] 3.2 `make check` reports zero findings on `pkg/identity`, `pkg/access`, `pkg/trigger` and `internal/agent`. Check: the command exits 0. Confirmed by: CI (the agent runs the fast checks only). Credential: none. BLOCKED by: none.
