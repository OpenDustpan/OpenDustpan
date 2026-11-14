## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the implementer's notes (the pull request body) before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests

- [ ] 1.1 Add failing restore cancel and preBackupID tests in NEW pkg/backup/restore_cancel_test.go Check: NEW TestRestoreCancelKeepsStagingUntilNextRestore fails on main, passes after the fix
- [ ] 1.2 Add failing dev reset tests in NEW cmd/opendustpan/devreset_test.go Check: NEW TestDevResetFailedRemakeNamesCopies fails on main, passes after the fix

## 2. Fix

- [ ] 2.1 Render preBackupID on the damaged live-blobs path in preBackupFoundOf in internal/api/server/restores.go Check: GET /api/server/restores/{id} names preBackupID on restore.live-blobs-damaged
- [ ] 2.2 Fix the dev reset error and the kept-copy guard in cmd/opendustpan/dev.go with no new flag Check: opendustpan dev reset --data-dir D names kept copies and refuses on zero namespace rows
- [ ] 2.3 Update docs/guides/dev-server.md, docs/guides/backup-restore-upgrade.md, docs/guides/load.md, docs/guides/soak.md, docs/guides/observe.md, docs/guides/s3-blobs.md and docs/guides/migrate-out.md for cancel timing, body limit, namespace minimum, OpenPGP-only registry and no internal references Check: `make docs-lint` passes on these guides

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run an e2e step against the real directory: POST /api/server/restores/{id}/cancel with an access token that holds server:restore. Confirmed by: CI (make e2e). Check: e2e restore cancel step passes
- [ ] 4.2 Usability check: the operator follows the guides alone and each error names one command. A person's step. Check: Operator completes the restore cancel and dev reset path from the docs
- [ ] 4.3 Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings
