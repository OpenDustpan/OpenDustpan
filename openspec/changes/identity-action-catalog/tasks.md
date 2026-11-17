## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: `pkg/identity/identity.go:156` and the action block at `:158-651`, `pkg/access/access.go:115` and `:229`, `pkg/access/service.go:1040-1043` and `pkg/access/access_test.go:490-493`. Record the differences in the notes under "Confirmed against the code". Check: the notes list each fact with its file and line.

## 1. Catalog

- [ ] 1.1 In `pkg/identity/identity.go`, keep the action constants as they are. Directly after their block, add the unexported array `catalog` (NEW) that lists each constant once, in the order of the block, and `Actions() []Action` (NEW) that returns a sorted copy. Extend the comment above the block: a new action goes into `catalog` too. Check: `go build ./...` passes.
- [x] 1.2 Add table tests in `pkg/identity/actions_test.go` (NEW): `Actions()` holds 125 actions, is sorted, holds no duplicate, and a change of the returned slice does not change the next result. Check: `go test ./pkg/identity/...` passes.

## 2. The family test

- [x] 2.1 In `pkg/access/access_test.go`, move the action list of `TestActionsWithoutRoutesAreUnclassified` into one variable `unclassifiedActions` that the test reads. Check: `go test ./pkg/access/ -run TestActionsWithoutRoutesAreUnclassified` passes.
- [x] 2.2 Add `TestEveryCoreActionIsClassifiedOrListed` (NEW) in `pkg/access/access_test.go`: for each action of `identity.Actions()`, `access.FamilyOf` answers a family, or the action is in `unclassifiedActions`; an action in `unclassifiedActions` has no family. Check: `go test ./pkg/access/ -run TestEveryCoreActionIsClassifiedOrListed` passes.

## 3. Verification

- [ ] 3.1 Prove the guard in `pkg/access/access.go:115`: remove `identity.ActionJobReadLogs` from the family table; `TestEveryCoreActionIsClassifiedOrListed` fails and names `job:read-logs`. Restore it. Check: the failure text holds the action.
- [ ] 3.2 `make check` reports zero findings on `pkg/identity` and `pkg/access`. Check: the command exits 0. Confirmed by: CI (the agent runs the fast checks only). Credential: none. BLOCKED by: none.
