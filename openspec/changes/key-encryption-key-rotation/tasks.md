## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test in NEW file `internal/api/server/rotation_test.go` for NEW `POST /api/dustpan/v1/installation/key-encryption-key/rotations` start with the NEW action `installation:rotate-key`; BLOCKED by NEW route and NEW action. Join the new apidesc labels into joinLabels in internal/api/server/operations.go (:187-198), the one join point of the labels. Check: `TestRotationStartStoresStarted` fails on main; BLOCKED by NEW route `POST /api/dustpan/v1/installation/key-encryption-key/rotations`.
- [ ] 1.2 Add failing server test in NEW file `pkg/secret/rotation_test.go` for NEW `RewrapNextBatch` dual-read of old and new wrapper IDs during rotation; BLOCKED by NEW rotation service. Check: `TestRotationReadsOldAndNew` fails on main; BLOCKED by NEW rotation service and NEW routes.
- [ ] 1.3 Add failing server test in NEW file `pkg/secret/rotation_resume_test.go` for NEW `RewrapNextBatch` resume from the stored cursor after a stop; BLOCKED by NEW resume operation. Check: `TestRotationResume` fails on main; BLOCKED by NEW resume operation.
- [ ] 1.4 Add failing server test in NEW file `internal/api/server/rotation_finish_test.go` for NEW `FinishRotation` start refusal with the old key after finish; BLOCKED by NEW finish operation. Check: `TestRotationFinishRefusesOldKey` fails on main; BLOCKED by NEW finish operation.
- [ ] 1.5 Add failing server tests in NEW file `internal/api/server/rotation_conflict_test.go` for second start with the NEW error code `installation.rotation-active`, incomplete finish with the NEW error code `installation.rotation-incomplete`, abort, show progress, double finish, concurrent resume, and 403 without the grant; BLOCKED by NEW routes. Check: `TestRotationConflictAndDeny` fails on main; BLOCKED by NEW routes.

## 2. Build

- [ ] 2.1 Build the NEW rotation service in NEW file `pkg/secret/rotation.go`, store fields, and audit entries; wire the NEW routes with authorization actions; register the NEW action `installation:rotate-key` in the action list and API description with audit kind `change`. Check: `TestRotationStartStoresStarted` passes with `make check` clean. Confirmed by: CI (the agent runs the fast checks only).
- [ ] 2.2 Change the start check in NEW file `pkg/secret/rotation_start.go` to need both `DUSTPAN_KEY_ENCRYPTION_KEY` and the NEW `DUSTPAN_KEY_ENCRYPTION_KEY_PREVIOUS` while active and the new key alone after finish. Check: `TestRotationStartNeedsBothKeys` passes; covers refusal with one key staged while active and with the old key alone after finish.
- [ ] 2.3 Add e2e step in NEW file `e2e/rotation_test.go` against the real server directory that rotates the key through the NEW routes and then reads a stack secret with the Pulumi CLI and with OpenTofu; BLOCKED by NEW rotation routes. Check: E2E step starts a rotation through the NEW routes, finishes it, then reads one Pulumi stack secret with the Pulumi CLI and one workspace state with OpenTofu; BLOCKED by NEW rotation routes for start, show, resume, abort, and finish. Confirmed by: CI (the agent writes the e2e test).
- [ ] 2.4 Add operator docs page NEW file `docs/operations/key-rotation.md` for rotation with tested curl commands for each NEW route: stage new key, start, show progress, resume after stop, abort, finish, remove old key. Check: Docs page commands run as written in a test, with one curl command for each NEW route: start, show, resume, abort, and finish. Confirmed by: CI (an e2e step in the NEW file e2e/key_encryption_key_rotation_docs_test.go runs each command of the docs page against the e2e test server).
- [ ] 2.5 Add restore test in NEW file `pkg/secret/rotation_restore_test.go` for a pre-rotation backup with the wrapping key staged and for rejection mid-rotation; BLOCKED by NEW rotation service. Check: `TestRestoreOldBackupAcrossRotation` fails on main then passes; BLOCKED by NEW rotation service.
- [ ] 2.6 Add audit fail-closed test in NEW file `internal/api/server/rotation_audit_test.go` for start and finish and upgrade-check health for rotation state. Check: `TestRotationAuditFailClosed` passes; upgrade-check reports rotation state during rotation.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`). Never edit a generated file by hand. Check: the named files change in the same pull request. Confirmed by: the driver's gates.

## 4. Definition of done

- [ ] 4.1 Run wiring, route authorization check, e2e step, docs page, and usability check from the docs page alone; BLOCKED by NEW rotation routes. Check: `make check` with zero findings after wiring the NEW routes, the NEW action `installation:rotate-key`, the e2e step with the Pulumi CLI and OpenTofu reads, the docs page with tested curl commands, and a usability check from the docs page alone; BLOCKED by NEW rotation routes until they land. A person's step. Confirmed by: CI (the agent writes the e2e test). Confirmed by: CI (the agent runs the fast checks only).
