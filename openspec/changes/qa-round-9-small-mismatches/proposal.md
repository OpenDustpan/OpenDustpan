---
version: 1
---

Issue: #2345, #2409.

## Problem

An operator restores installations, cancels restores, and resets trial data. Mismatched guides and missing IDs cost time and risk loss of the kept dev copy.

## Evidence

- Restore start stores a checking restore Proof: pkg/backup/restore_operation.go:404:	// StartRestore stores r as a checking restore, with r.DevReport when
- Restore steps carry PreBackupID Proof: pkg/backup/restore_steps.go:41:	PreBackupID      ID        `json:"preBackupID"`
- The API renders preBackupFound from the restore record Proof: internal/api/server/restores.go:354:		PreBackupFound:   preBackupFoundOf(record),
- The dev data directory has a fixed database name Proof: cmd/opendustpan/devdir.go:20:	devDBName        = "dev.db"
- The guide-only claim: no source code names the kept-copy file, so the refusal text is built elsewhere Proof: rg -n -F --glob '!*_test.go' 'dev.db.reset.bak' pkg internal cmd: 0 matches

## Fix boundary

- State in the restore guide that cancel answers at once, the running step finishes first, the final state is failed, and a cancel counts as stopped-with-no-end for staging schemas.
- Include preBackupID in restore.live-blobs-damaged results when the pre-restore backup was taken; omit it when none was taken.
- Fix the dev refusal text before any reset ran, name kept copies and put-back steps in failed-remake errors, and refuse to replace a kept copy that holds data when the current database holds zero namespace rows.
- Document the 3-character Pulumi CLI minimum for namespace names, the kept 400 answer for oversized bodies, and OpenPGP-only registry keys; remove ADR numbers from user guides.

Out of scope:

- Fleet lifecycle, engineering-health insights, and other backlog items
- ECDSA SSH-key signatures in the registry beyond documenting OpenPGP-only
- New restore semantics beyond cancel wording, final failed state, and preBackupID rendering

## Check

- Add failing restore cancel and preBackupID tests in NEW pkg/backup/restore_cancel_test.go Check: NEW TestRestoreCancelKeepsStagingUntilNextRestore fails on main, passes after the fix
- Add failing dev reset tests in NEW cmd/opendustpan/devreset_test.go Check: NEW TestDevResetFailedRemakeNamesCopies fails on main, passes after the fix
- Render preBackupID on the damaged live-blobs path in preBackupFoundOf in internal/api/server/restores.go Check: GET /api/server/restores/{id} names preBackupID on restore.live-blobs-damaged
- Fix the dev reset error and the kept-copy guard in cmd/opendustpan/dev.go with no new flag Check: opendustpan dev reset --data-dir D names kept copies and refuses on zero namespace rows
- Update docs/guides/dev-server.md, docs/guides/backup-restore-upgrade.md, docs/guides/load.md, docs/guides/soak.md, docs/guides/observe.md, docs/guides/s3-blobs.md and docs/guides/migrate-out.md for cancel timing, body limit, namespace minimum, OpenPGP-only registry and no internal references Check: `make docs-lint` passes on these guides
- Run an e2e step against the real directory: POST /api/server/restores/{id}/cancel with an access token that holds server:restore. Confirmed by: CI (make e2e). Check: e2e restore cancel step passes
- Usability check: the operator follows the guides alone and each error names one command. A person's step. Check: Operator completes the restore cancel and dev reset path from the docs
- Run make check with zero findings. Confirmed by: CI (make check). Check: `make check` with zero findings

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- dev command runs the trial server (`cmd/opendustpan/dev.go:151 (func dev)`)
- Restore live error renders the live-blob failure (`pkg/dustpan/area_backup.go:302 (func restoreLiveError)`)
- Demo data seeds namespace names for dev (`cmd/opendustpan/devdemo.go:25 (func demoNamespaceNames)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 38: Restore through the server, in maintenance mode** (not owner-tagged).

- Decisions that apply: 1 (current), 8 (current).
- Changed by: ADR 42 changes decision 3; ADR 48 extends decisions 4 and 6; ADR 70 changes decision 6; ADR 151 changes decision 3; ADR 210 changes; ADR 212 extends decision 9; ADR 248 changes decision 3. Chain to the end: ADR 42, ADR 48, ADR 70, ADR 151, ADR 210, ADR 212, ADR 248, ADR 253, ADR 72, ADR 142, ADR 216, ADR 222, ADR 227, ADR 245, ADR 306, ADR 320, ADR 321, ADR 332, ADR 354, ADR 369, ADR 154, ADR 217, ADR 220, ADR 247, ADR 286, ADR 232, ADR 235, ADR 301, ADR 365, ADR 73, ADR 75, ADR 103, ADR 126, ADR 166, ADR 240, ADR 249, ADR 280, ADR 298, ADR 149, ADR 297.
- Current rule: this ADR, with ADR 42, 48, 70, 151, 210, 212, 248.
- Quote: "Restore is an API operation. The installation API (ADR 30 decision 9) gets restore routes with the new action `server:restore` on the object `@server`."
- Quote: "A backup before the restore. Before it applies, the server takes a backup of the live data (ADR 30) and names it in the restore result."
- Effect: The damaged live-blobs result names the stored pre-restore backup; cancel keeps the restore routes and action.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does a canceled or failed restore keep staging schemas and must it name the pre-restore backup? ADR 38 decisions 3, 8 and 9 answer it: keep a record and name the backup before restore. The miss is the render of preBackupID on the damaged path and the guide wording.
- Does dev reset keep one copy and name it on failure? ADR 237 decisions 1 and 2: dev decides for the user with no new flag, so the fix is an error text and a refusal, not a flag.
- Do body-too-large answers, namespace minimums or OpenPGP-only registry need behavior changes? No ADR in the pack asks for it. The fix is docs only; the 400 answer stays.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
