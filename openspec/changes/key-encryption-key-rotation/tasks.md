## 0. Confirm against the code first

- [ ] 0.1 Confirm against the code first: read each fact of "What main already does" at its file, line and function on the current main, and record each difference in design.md before the first test. Check: design.md lists the differences, or says that there are none.

## 1. Failing tests first

- [ ] 1.1 Add failing server test for NEW `POST /api/dustpan/v1/installation/key-encryption-key/rotations` start with the NEW action `installation:rotate-key`; BLOCKED by NEW route and NEW action. Check: `TestRotationStartStoresStarted` fails on main; BLOCKED by NEW route `POST /api/dustpan/v1/installation/key-encryption-key/rotations`.
- [ ] 1.2 Add failing server test for RewrapNextBatch dual-read of old and new wrapper IDs during rotation; BLOCKED by NEW rotation service. Check: `TestRotationReadsOldAndNew` fails on main; BLOCKED by NEW rotation service and NEW routes.
- [ ] 1.3 Add failing server test for RewrapNextBatch resume from the stored cursor after a stop; BLOCKED by NEW resume operation. Check: `TestRotationResume` fails on main; BLOCKED by NEW resume operation.
- [ ] 1.4 Add failing server test for FinishRotation start refusal with the old key after finish; BLOCKED by NEW finish operation. Check: `TestRotationFinishRefusesOldKey` fails on main; BLOCKED by NEW finish operation.
- [ ] 1.5 Add failing server tests for second start with the NEW error code `installation.rotation-active`, incomplete finish with the NEW error code `installation.rotation-incomplete`, abort, show progress, double finish, concurrent resume, and 403 without the grant; BLOCKED by NEW routes. Check: `TestRotationConflictAndDeny` fails on main; BLOCKED by NEW routes.

## 2. Build

- [ ] 2.1 Build the NEW rotation service, store fields, and audit entries; wire the NEW routes with authorization actions; register the NEW action `installation:rotate-key` in the action list and API description with audit kind `change`. Check: `TestRotationStartStoresStarted` passes with `make check` clean.
- [ ] 2.2 Change the start check to need both `DUSTPAN_KEY_ENCRYPTION_KEY` and the NEW `DUSTPAN_KEY_ENCRYPTION_KEY_PREVIOUS` while active and the new key alone after finish. Check: `TestRotationStartNeedsBothKeys` passes; covers refusal with one key staged while active and with the old key alone after finish.
- [ ] 2.3 Add e2e step against the real server directory that rotates the key through the NEW routes and then reads a stack secret with the Pulumi CLI and with OpenTofu; BLOCKED by NEW rotation routes. Check: E2E step starts a rotation through the NEW routes, finishes it, then reads one Pulumi stack secret with the Pulumi CLI and one workspace state with OpenTofu; BLOCKED by NEW rotation routes for start, show, resume, abort, and finish.
- [ ] 2.4 Add operator docs page for rotation with tested curl commands for each NEW route: stage new key, start, show progress, resume after stop, abort, finish, remove old key. Check: Docs page commands run as written in a test, with one curl command for each NEW route: start, show, resume, abort, and finish.
- [ ] 2.5 Add restore test for a pre-rotation backup with the wrapping key staged and for rejection mid-rotation; BLOCKED by NEW rotation service. Check: `TestRestoreOldBackupAcrossRotation` fails on main then passes; BLOCKED by NEW rotation service.
- [ ] 2.6 Add audit fail-closed test for start and finish and upgrade-check health for rotation state. Check: `TestRotationAuditFailClosed` passes; upgrade-check reports rotation state during rotation.

## 3. Side effects (computed from main)

- [ ] 3.1 Regenerate `internal/api/apidesc/openapi.json` and the clients (`make generate`, `make clients-generate`); `docs/adr/INDEX.md` (`make adr-index`). Never edit a generated file by hand. Check: the named files change in the same pull request

## 4. Definition of done

- [ ] 4.1 Run wiring, route authorization check, e2e step, docs page, and usability check from the docs page alone; BLOCKED by NEW rotation routes. Check: `make check` with zero findings after wiring the NEW routes, the NEW action `installation:rotate-key`, the e2e step with the Pulumi CLI and OpenTofu reads, the docs page with tested curl commands, and a usability check from the docs page alone; BLOCKED by NEW rotation routes until they land.
