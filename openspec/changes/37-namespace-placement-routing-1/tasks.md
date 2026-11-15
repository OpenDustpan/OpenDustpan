## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Write the failing domain test in the NEW file pkg/namespace/primary_test.go for the contract of GetPrimary (NEW) and RequirePrimary (NEW), including a missing primary and an unknown caller ID. Check: `go test ./pkg/namespace/... -run TestRequirePrimaryFailsClosed -count=1` fails on main.
- [ ] 1.2 Write the failing server test in the NEW file internal/servertest/nonprimary_test.go: a lock, a state write and a settings PATCH on a non-primary installation answer 421 on the Pulumi, TFE-compatible and OpenTofu routes. Credential kind: access token. Gate: the namespace write gate of each route. A read of GET /api/dustpan/v1/namespaces/{namespace}/settings answers 200. Check: `go test ./internal/servertest/... -run TestNonPrimaryWriteAnswers421 -count=1` fails on main.

## 2. Build

- [ ] 2.1 Add the primary ID to the namespace row in the baseline of each dialect (NEW column in the namespace table of the baseline files under pkg/store), add the same column to the backup column list of the namespace table in `pkg/backup/portable_tables.go` (a backup holds every column), set it in the create transaction, and add primaryGuard with its constructor. Tests call PATCH /api/dustpan/v1/namespaces/{namespace}/settings with an access token. Check: `go test ./pkg/namespace/... ./pkg/store/... -count=1` passes.
- [ ] 2.2 Map the NEW sentinel to 421 in each tool adapter, and keep the existing Pulumi mapping in internal/api/pulumi/pulumi.go with the same body shape. Edit the TFE-compatible adapter and the OpenTofu http adapter under internal/api/ in the same way. Declare the error code federation.not-primary (NEW) in the route declaration file internal/api/server/githost.go. Join the new apidesc labels into joinLabels in internal/api/server/operations.go (:187-198), the one join point of the labels. Check: `go test ./internal/api/... -count=1` passes.
- [ ] 2.3 Add the NEW file docs/reference/namespace-placement.md for placement, 421 and reads. Check: `make docs-lint` passes with the NEW file.

## 3. Side effects (computed from main)

- [ ] 3.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 3.2 Regenerate `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Wire the guard into every namespace write path. Run an e2e step with the real Pulumi CLI and OpenTofu, with an access token, against GET /api/dustpan/v1/namespaces/{namespace}/settings and the write routes of each tool, each with its authorization action. Run the usability check from the docs page. Check: `make e2e` passes, then `make check` reports zero findings. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
