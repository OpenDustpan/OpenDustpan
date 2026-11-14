---
version: 1
---

Issue: #2691, #2694, #2695, #2699.

## Problem

Left-over notes from merged pull request reviews stay unverified. An operator who runs dev reset can read an error that does not name the next step.

## Evidence

- The cmd files named by the issue exist on main. Proof: paths: cmd/opendustpan/main.go, cmd/opendustpan/devreset.go, cmd/dustpan-launcher/harden_linux_test.go exist
- The e2e files named by the issue exist on main. Proof: paths: e2e/image_test.go, e2e/audit_export_test.go, e2e/placement_test.go, e2e/faults/faults_test.go, e2e/launcher_test.go, e2e/notification_sinks_test.go exist
- The access, gate, and store files named by the issue exist on main. Proof: paths: pkg/access/review_service.go, pkg/gate/approvers_check.go, pkg/store/storetest/sweep_lock.go, pkg/store/storetest/item_keys_backup.go, pkg/store/sqlite/frozen_test.go, pkg/store/postgres/long_path.go, pkg/store/internal/localuserstore/localuserstore.go exist

## Fix boundary

- Name the next step in the dev reset error text
- Harden tests and fix comments for the rows that still apply on main
- Skip each row that main already fixes, with a reason

Out of scope:

- New refusals, caps, and error codes for collection export, trust-rule payload, and gate user name
- Long-path bounds in the settings API and the catalog
- The missing e2e/coverage_runner.go runner route builder
- Removal of the database trial hint
- Other backlog items

## Check

- In cmd/opendustpan/devreset.go write the failing test of the dev reset error text for a symlinked old entry; the error names the next step. Check: `go test ./cmd/opendustpan -run TestDevResetNextStep -count=1`
- In cmd/dustpan-launcher/harden_linux_test.go change the hardening test to fail instead of skip in CI without tmpfs. Check: `go test ./cmd/dustpan-launcher -count=1`
- In pkg/store/storetest/sweep_lock.go, pkg/store/storetest/item_keys_backup.go, pkg/store/sqlite/frozen_test.go, pkg/store/postgres/self_service_blueprints_internal_test.go, and pkg/store/internal/localuserstore/localuserstore.go harden the tests and comments that still apply on main. Check: `go test ./pkg/store/storetest -count=1`
- In e2e/audit_export_test.go and e2e/placement_test.go harden the checks that still apply on main, using an access token of a principal that holds a stored grant. Check: `go test ./e2e -run TestAuditExport -count=1`
- In pkg/access/review_service.go, pkg/gate/approvers_check.go, pkg/trustrule/jwt.go, pkg/tempadmin/service.go, and pkg/localuser/argon2id/argon2id_test.go harden the tests and comments that still apply on main; list each applied and each skipped row with its reason in the pull request description. Check: `go test ./pkg/access ./pkg/gate -count=1`
- Run the full check for the test-only scope. Check: `make check`

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The postgres store implements TryLockSweep, which the sweep-lock helper uses (`pkg/store/postgres/internal/db/queries.sql.go:22434 (func Queries.TryLockSweep)`)
- The localuser store implements RemovePassword, touched by the passkey comment row (`pkg/store/internal/localuserstore/localuserstore.go:557 (func RemovePassword)`)
- The review service decodes review cursors (`pkg/access/review_service.go:916 (func decodeReviewCursor)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

Not spec-ready: a code check fails: path `e2e/coverage_runner.go` (.out_of_scope[2]) does not exist in the checkout; use an existing path, or name it as new ("add the new file e2e/coverage_runner.go"); a code check fails: test `TestDevResetNextStep` (.errors[0].command) does not resolve on main; use the name main has, or mark it NEW in the same sentence; a code check fails: task "In e2e/audit_export_test.go and e2e/placement_test.go harden the check" runs an acceptance path but names no existing route and credential kind; name them, or write "BLOCKED by <the missing part; a code check fails: and 5 more.

Questions considered and answered:

- Does the change need an ADR decision? No. The pack names no ADR, and no row asks for a decision that changes an ADR.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
