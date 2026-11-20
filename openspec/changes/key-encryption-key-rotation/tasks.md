## 0. Confirm against the code

- [ ] 0.1 Read each fact of "What main already does" at its file, line and function on the current main, and record each difference in the pull request body before the first test. Also run `rg -i rewrap --glob '*.go'` and `rg ListItemKeyWrapperIDs --glob '*.go'` and record that no code rewraps and that no start check reads the item key IDs. Check: the pull request body lists the differences, or says that there are none.

## 1. Failing tests first

Each test is black-box, in a `_test` package, with `t.Parallel()`. A test that needs a NEW symbol fails on main because the build fails. A guard on behavior that main already has passes on main and says so.

- [ ] 1.1 Add the NEW file `pkg/secret/secrettest/rotation.go` (conformance suite of NEW `RotationStore`) and the NEW file `pkg/secret/rotation_test.go` with `TestRotatingOpensOldAndNew`, `TestRotatingWritesNew`, `TestHoldsAnswersHeldIDsOnly` and `TestNewRotatingRefusesEqualIDs`. Check: each fails on main; BLOCKED by NEW `secret.Rotating` and NEW `secret.Holds`.
- [ ] 1.2 Add `TestStartRotationRefusesSecond` and `TestStartRotationNeedsOpenableRecords` to the NEW file `pkg/secret/secrettest/rotation.go` of task 1.1. Check: fail on main; BLOCKED by NEW `RotationStore`.
- [ ] 1.3 In the NEW file `pkg/secret/secrettest/rotation_rewrap.go` add `TestRewrapBatchUpdatesOnlyOldIDs`, `TestRewrapBatchReplayChangesNothing` and `TestRewrapKeepsScopeAndAAD` for the data keys, the installation data keys and the idempotency key. Check: fail on main; BLOCKED by NEW `RewrapKeys`.
- [ ] 1.4 Add `TestRewrapItemKeyWritesNewBlob`, `TestRewrapItemKeySkipsShredded` and `TestRewrapItemKeyDeletesNewBlobAfterShredRace` to the item key suite `pkg/secret/secrettest/itemkeys.go`. Check: fail on main; BLOCKED by NEW `RewrapItemKey` on `ItemKeyIndex`.
- [ ] 1.5 Add `TestRotationResumesAfterStop` (NEW file `pkg/secret/rotation_resume_test.go`): stop a durable operation after one batch, claim it again, and check that every record holds the new ID once. Check: fails on main; BLOCKED by NEW const `KeyRotationKind` (the durable kind `key-rotation`).
- [ ] 1.6 In the NEW file `pkg/secret/rotation_finish_test.go` add `TestFinishNeedsFullCount` for each family and `TestFinishFlipsOnce`. Check: fail on main; BLOCKED by NEW `FinishRotation`.
- [ ] 1.7 Add `TestStartUpNeedsRotatingWrapperWhileActive`, `TestStartUpCountsItemKeyAndIdempotencyIDs` and `TestRestoreHoldsEitherID` (`pkg/dustpan` and `pkg/backup`). Check: `TestStartUpCountsItemKeyAndIdempotencyIDs` fails on main at `pkg/dustpan/dustpan.go:2592 (func startUp)`, which lists no item key ID; the other two fail on main; BLOCKED by NEW `ErrRotationActive` and NEW `Holds`.
- [ ] 1.8 Add `TestStartUpRefusesOldKeyAfterFinish`: a database of new IDs with a plain old wrapper. Check: passes on main, guards (`pkg/secret/secret.go:225 (func checkWrapperIDs)` refuses it); it must keep passing.
- [ ] 1.9 Add `TestHalfRotatedStoreStillNamesRewrap` as a check that the refusal text of a plain wrapper keeps the word "rewrap" while no rotation is active. Check: passes on main, guards (`pkg/secret/builtin_test.go:208 (func TestCheckWrapper)`).
- [ ] 1.10 SECURITY, port adapter layer: add one table of hostile cases to the NEW file `pkg/secret/secrettest/rotation_hostile.go`, run by NEW `TestRotationStore` in the NEW files `pkg/store/sqlite/rotation_test.go` and `pkg/store/postgres/rotation_test.go`: two concurrent starts, a replayed batch, a wrapper ID with quote and semicolon, equal IDs, a stale old ID. For each guard, show a test that fails when the guard is removed (remove it once by hand and paste the failure in the pull request). Check: `go test ./pkg/store/sqlite/ -run TestRotationStore -timeout 60m -count=1` and the same for `./pkg/store/postgres/`.
- [ ] 1.11 SECURITY, service layer: one table in NEW file `pkg/secret/rotation_hostile_test.go`: a record under a third ID, a record that fails to open, finish with one old record left in each family, an audit store that fails. Same removal proof. Check: `go test ./pkg/secret/ -run TestRotationHostile -count=1`.
- [ ] 1.12 SECURITY, wrapper, start-up and restore, log layers: one table each: an ID of a third key and a ciphertext of another AAD; a plain wrapper with an active rotation and a restore while active; a capture of logs and audit entries searched for the key bytes in base64 and in hex (NEW file `pkg/secret/rotation_logs_test.go`). Same removal proof. Check: `go test ./pkg/secret/ ./pkg/dustpan/ ./pkg/backup/ -run 'TestRotationHostile|TestRotationLogs' -count=1`.

## 2. Build

- [ ] 2.1 Add NEW file `pkg/secret/rotating.go` (NEW `Rotating`, NEW `NewRotating`, NEW `Holds`) and use `Holds` in `pkg/secret/envelope.go:55`, `pkg/secret/secret.go:225`, `pkg/store/internal/codec/idempotency.go:321` and `pkg/backup/staging.go:295`. Keep the redundant checks and their comments. Check: tasks 1.1 and 1.8 pass.
- [ ] 2.2 Add the table `key_rotations` and its partial unique index to the baseline of each dialect, in name order, add the NEW constant `TableKeyRotations` to `pkg/backup/backup.go` and the name `key_rotations` to `ControlTables` in `pkg/backup/coverage.go` (read `pkg/backup/portable_tables.go` to confirm that the portable list holds no entry for it), add the queries to `queries.sql` of each dialect in name order, and run `make generate`. Never edit the generated `db` package of a store. Check: `sqlc diff` is clean and the backup coverage test passes. Confirmed by: the driver's gates.
- [ ] 2.3 Add NEW `RotationStore` and its adapters in the NEW files `pkg/store/sqlite/rotation.go` and `pkg/store/postgres/rotation.go`, and NEW `RewrapItemKey`. Check: tasks 1.2 to 1.4 and 1.10 pass on both dialects (`-timeout 60m` for a store package).
- [ ] 2.4 Add NEW file `pkg/secret/rotation.go` (service: start, batch, finish, abort, the durable kind `key-rotation`) and the audit entries of decision 7. Check: tasks 1.5, 1.6 and 1.11 pass.
- [ ] 2.5 Change the start check in `pkg/dustpan/dustpan.go:2592` as in decision 9, and the restore refusal as in decision 8. Check: tasks 1.7 and 1.12 pass.

## 3. Side effects

- [ ] 3.1 Update the text of `ErrWrapperMismatch` and of `checkWrapperIDs` only if the owner answers the parked questions; until then they keep the word "rewrap" (task 1.9). Check: `make docs-lint` and `go test ./pkg/secret/` pass.
- [ ] 3.2 Add the term "key rotation" to `GLOSSARY.md` if it has none. Check: `make docs-lint` passes.

## 4. Parked until the owner decides

Each task below waits for the owner questions of the proposal. Do not start them. Each one gets its own failing-test-first tasks in a revised change after the answer.

- [ ] 4.1 PARKED (owner question 1). No route: this task runs no test until the owner decides. The authorization action in `pkg/identity/identity.go`, its holders, the fresh-step rule, the row of the action table in the access ADR, and the route test row. Check: the revised change quotes the owner decision.
- [ ] 4.2 PARKED (owner questions 1 and 3). No route: the test of this task waits for the owner decision only. The routes in `internal/api/server/` to start, show, resume, abort and finish, the error codes and their catalog lines, the regenerated API description and clients. Check: the route test passes and `make e2e` drives the real CLIs.
- [ ] 4.3 PARKED (owner question 2). No route: this task runs no test until the owner decides. The source of the second key in `cmd/opendustpan/main.go` and the settings reference. Check: the revised change quotes the owner decision.
- [ ] 4.4 PARKED (owner questions 1 to 3). No route: the test of this task waits for the owner decision only. The NEW file `docs/operations/key-rotation.md` is the operator docs page, with commands that a test runs, the e2e step that rotates the key and then reads a Pulumi stack secret with the Pulumi CLI and a workspace state with OpenTofu, and a usability check from the docs page alone. Check: the e2e step passes.
- [ ] 4.5 PARKED (owner question 4). No route: this task runs no test until the owner decides. The deletion in `pkg/secret/itemkeyblob.go` of old item key blobs in the backup targets. Check: the revised change quotes the owner decision.

## 5. Definition of done

- [ ] 5.1 Run the fast checks of the agent and push. CI runs the rest. The pull request body holds the output of the checks, the removal proofs of tasks 1.10 to 1.12 and the line that the Claude security review (rev2) is needed on the exact head. Check: `make test-changed` passes. Confirmed by: CI (the agent runs the fast checks only).
