# Architecture review, round 8

Range: main `da75bb7` (end of round 7) to `6c35afa` (trains 31 to 37). Reviewed on 2026-11-02.

Scope: the attempt deadline, the job artifact sweep, sealed update events, the W9 lock record, the restore fence, the restore repair of damaged blobs, local users with a password and name locks, the session generation, ADRs 311 to 319, and the docs fixes.

Method: `git diff da75bb7 6c35afa` by area, a read of each changed port and both store adapters, and `rg` over `docs/adr` and `docs/rfc` for each decision. Each finding below was confirmed against the code at `6c35afa`.

Note on commits: main now starts at the root commit `ffff73f`, so `da75bb7` is not an ancestor of `6c35afa`, and most lines blame to `ffff73f`. Where a later commit of the range introduced a line, the finding names it.

## Findings of severity major

| # | Finding | Issue |
| --- | --- | --- |
| 1 | Gate rows still key on the unit name. ADR 317 decision 1 (key on the unit ID; a new unit with a reused name starts clean) is not built. The storetest cases test the name model (`pkg/store/storetest/gate.go:107,129,134`). One-way door after the first tag. | #2267 |
| 2 | The restore replaces live blobs (`pkg/backup/restore_damaged.go:219-258`), against ADR 38 decision 6, with no RFC or ADR. The repair is automatic, has no opt-in and no bound. A wrong blob store makes every blob "missing", and the restore goes on where it used to stop. | #2268 |
| 3 | A backup before restore with a `damaged` list is refused as corrupt (`pkg/backup/damaged.go:182`), so it cannot undo the restore (ADR 38 decision 8). The backups API does not show the damage. | #2269 |
| 4 | The lock of a name is recorded best effort after the commit (`internal/localusersvc/locks.go:131-153`). The end of a lock has no record. The administrator alert of ADR 285 decision 16 is missing. | #2270 |
| 5 | The effective lockout rules (`internal/authgate/namelock.go:43-62`) are not in `GET /local-users/policy`. | #2271 |
| 6 | The state.write guard is still three adapter copies that differ (tfe and inventory drop `Decision.Result`, write no audit denial, set no `PlanLock`; tfe answers 500 for an unknown code). Unchanged in this range. | #2234 (fix open in #2263) |
| 7 | The W9 last-release record is in no API, against ADR 298 ("The API of the workspace shows it"). A repeated unlock answers 200 with no audit entry and no visible reason. | #2229 |
| 8 | The job artifact sweep reads at most 2,000 jobs for each pass, newest first, inside a 25-hour window, so older jobs and a sweep outage leak blobs for good. | #2220 (the minor issue adds the missing capped flag and the doc text) |

## Findings of severity minor

All in #2272: the lock timing against ADR 285 decision 13; the clear on a good current password against decision 14; `server:manage-sign-in` against `installation:manage-sign-in`; two name-lock adapter methods with no request check; the `NameKeys` port in a service package with no suite; existence before reach in `Locks` and `Unlock`; silent session renewal and silent feature disable; the wrong generation cause on a delete; the manifest format number; the manifest digest binding; the repair warning count; the wedge and size bound of `preserveDamaged`; the cost of the damage evidence; the sweep capped flag and doc; the silent sweep disable and the exported `SweepJob`; the Pulumi orphan sweep with no record; the two attempt deadlines; fixed code rules not in the API; the TFE force-unlock repeat log line; ADR 315 decision 3 with no code (#931).

## Checked and clean

- Layering: no domain package changed in the range imports `net/http`, `database/sql`, a driver, `pkg/store/...` or `internal/api/...`. `pkg/backup` stays free of store imports.
- Shared core: no tool-specific field in the shared packages changed in this range. The TFE and tofuhttp adapters do not call each other.
- Conformance suites: both SQL adapters run `authgatetest.RunNameLocks`, `sessiontest.RunPrincipalStateStore`, `localusertest.Run`, `jobtest.RunArtifactSweep`, the last-release cases, the attempt-deadline cases and `testLiveBlobRef`.
- SQLite and PostgreSQL agree on the name locks, the generation bump, `MarkRestoreApplying`, `writeCheckpoint` and `checkAttempt`.
- Error codes: the new answers of local users and `update.attempt-expired` come from the catalog.
- Unlock and prune of a name, and each generation bump, write their audit entry in the same transaction as the change.
- A restore keeps the live name lock counts and key, and refuses an invalid `last_release_kind`.
- ADR 312 is built: `POST /me/password` declares `caller:manage-sign-in`.

## The finding that matters most

Finding 2 (#2268), with finding 3 (#2269). The restore now deletes and rewrites live data by itself, which ADR 38 forbids, with no owner decision. A misconfigured blob store turns a restore that used to stop into one that goes on, and the backup that should undo it is refused as corrupt. Both are cheaper to settle before the first tag.
