## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Follow-ups

- [ ] 1.1 In cmd/opendustpan/devreset.go write the failing test of the dev reset error text for a symlinked old entry; the error names the next step. Check: `go test ./cmd/opendustpan -run TestDevResetNextStep -count=1`
- [ ] 1.2 In cmd/dustpan-launcher/harden_linux_test.go change the hardening test to fail instead of skip in CI without tmpfs. Check: `go test ./cmd/dustpan-launcher -count=1`
- [ ] 1.3 In pkg/store/storetest/sweep_lock.go, pkg/store/storetest/item_keys_backup.go, pkg/store/sqlite/frozen_test.go, pkg/store/postgres/self_service_blueprints_internal_test.go, and pkg/store/internal/localuserstore/localuserstore.go harden the tests and comments that still apply on main. Check: `go test ./pkg/store/storetest -count=1`
- [ ] 1.4 In e2e/audit_export_test.go and e2e/placement_test.go harden the checks that still apply on main, using an access token of a principal that holds a stored grant. Check: `go test ./e2e -run TestAuditExport -count=1`. Confirmed by: CI (the agent writes the e2e test).
- [ ] 1.5 In pkg/access/review_service.go, pkg/gate/approvers_check.go, pkg/trustrule/jwt.go, pkg/tempadmin/service.go, and pkg/localuser/argon2id/argon2id_test.go harden the tests and comments that still apply on main; list each applied and each skipped row with its reason in the pull request description. Check: `go test ./pkg/access ./pkg/gate -count=1`

## 2. Side effects (computed from main)

- [ ] 2.1 Set the round-trip budgets of each request flow that the change adds or changes, in `internal/servertest/roundtrips_conflict_test.go`, `internal/servertest/roundtrips_model_test.go`, `internal/servertest/roundtrips_test.go`. Check: the named files change in the same pull request
- [ ] 2.2 Regenerate `internal/db` (`make generate`, sqlc); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 3. Definition of done

- [ ] 3.1 Run the full check for the test-only scope. Check: `make check`. Confirmed by: CI (the agent runs the fast checks only).
