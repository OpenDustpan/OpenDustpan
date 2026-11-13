Issue: #2992.

## Problem

A developer renames a stack or a workspace while its job runs. The running job
then asks its tool to take a lock on the unit. The manual lock check compares
the unit name that the job stored at its start with the current name, so the
request fails with `ErrNoPhase`. The request fails closed, so no lock is
bypassed, but the job cannot finish its work. The unit ID is stable and stays
with the unit; the name is only a label.

## Evidence

- `pkg/job/phaseclock.go:41` — `if u.Namespace() != unit.Namespace || u.Tool() != unit.Tool || u.UnitName() != unit.Name {` — the phase clock compares the stored name with the current name.
- `pkg/store/internal/jobstore/body.go:22-27` — `type unitBody struct {` with `Tool`, `Namespace`, `Name` and `UnitID`. The body holds the unit ID at line 26 and the namespace **name** at line 24. It holds no namespace ID today.
- `pkg/store/internal/jobstore/body.go:215` — `return unitBody{Tool: o.Tool(), Namespace: o.Namespace().String(), Name: o.UnitName(), UnitID: o.UnitID()}` — the encoder writes the name, not the ID.
- `pkg/manuallock/service.go:45` — `PhaseStartedAt(ctx context.Context, jobID string, unit collection.UnitRef) (time.Time, error)` — the request also names the unit by name only. `pkg/collection/collection.go:138` — `type UnitRef struct {` with `Namespace`, `Tool` and `Name` from lines 139 to 141.
- Other name comparisons in the two packages: `pkg/job/service.go:289` — `if spec.Unit.String() != unit.String() || spec.Unit.UnitID() != unit.UnitID() ||` — compares the full reference, name included, and the unit ID. `pkg/store/internal/jobstore/store.go:627` — `if j.Spec.Unit.Namespace() != r.Namespace || j.Spec.Unit.UnitID() != r.UnitID {` — compares the namespace name and the unit ID. The lock lookup of the dispatch already uses the unit ID: `pkg/store/internal/jobstore/rules.go:104` says "The lookup uses the stable unit ID, not the name that the spec stored at the start: a rename of the unit must not hide its locks."
- Three other checks compare the namespace name against a fence, not the unit name: `pkg/store/internal/jobstore/store.go:467`, `pkg/job/claimer.go:673` and `pkg/job/recorder.go:632`.
- `pkg/job/job.go:588` — `if j.State != StateAwaitingState || !v.Final || j.Spec.Unit.UnitID() != v.Unit.UnitID() {` — a precedent that compares the unit ID.
- `pkg/job/jobtest/unitid.go:31` — `// that was renamed after its job started: the job holds the name at the start,` — the comment proves that a rename keeps the job for the dispatch, but no test checks the phase clock after a rename.

## Fix boundary

**IN**

- `pkg/store/internal/jobstore/body.go`: `unitBody` gets `NamespaceID`; `encodeUnit` and `decodeUnit` carry it. `Name` stays for display.
- `pkg/job/job.go` and `pkg/job/service_start.go`: the job holds the namespace ID, set in `create`/`persist` from the namespace record.
- `pkg/job/service.go` and `pkg/dustpan/jobservice.go`: `FenceSource` gains a method for the namespace ID; `jobFences` reads it from the namespace record of `namespace.Service.Get`.
- `pkg/job/phaseclock.go`: `PhaseStartedAt` compares `j.Spec.Unit.UnitID()` and the job's namespace ID with the IDs of the requested unit. The name stays in the error text only.
- `pkg/manuallock/manuallock.go` and `pkg/manuallock/service.go`: a `Unit` value with the reference, the namespace ID and the unit ID. `PhaseClock`, `PhaseStart`, `afterPhase`, `CoveringFor` and `NamespaceLockFor` take it. An empty ID fails closed.
- `pkg/stack/updates.go` and `pkg/workspace/service.go`: `checkManualLock` and `refuseManualLock` resolve the stable stack or workspace ID and the namespace ID from the stores they already use, and pass them. The create path for a unit that does not exist yet keeps its current behavior.

**OUT**

- The name fields in the body. They stay for display. No tag shipped the body, so the change adds no read path for a body without the namespace ID.
- The dispatch lock path. It already uses the unit ID.
- The audit entry object text and the unit reference format.

## Check

- **Guard: the clock compares the unit ID.** Test `TestManualLockPhaseStartAfterRename` in `pkg/dustpan/manual_lock_phases_test.go`. It uses a running job with the old name and the current IDs, and a unit reference with the new name and the same IDs. Mutation: compare `UnitName` again; the test gets the zero time.
- **Guard: the clock refuses another unit with the same name.** Test case "another unit with the same name" in `pkg/job/phaseclock_test.go`. Mutation: compare only the name; the case gets a start time instead of `ErrNoPhase`.
- **Guard: the body carries the namespace ID.** Test `TestBodyRoundTrip` in `pkg/store/internal/jobstore/body_test.go`. Mutation: drop `NamespaceID` from `encodeUnit`; the decoded ID is empty.
- **Guard: the job start records the ID.** Test `TestStartRecordsTheNamespaceID` in `pkg/job`. Mutation: leave the ID empty in `create`; the test sees an empty ID.
- **Guard: the caller passes the IDs.** Test `TestCoveringForUsesTheUnitIDs` in `pkg/manuallock`. Mutation: pass a `Unit` with empty IDs; the test gets `ErrNotLocked` instead of the lock.

## ADR check

**ADR 100 decision 2 (not owner-tagged).** "The unit of a job is an `identity.Object` unit reference with its stable unit ID set. A rename of the unit keeps its jobs. A change to many units is many jobs. The job core never parses the tool's own name." Changed by ADR 304 (decision 6: a discovery job is the only job with no unit; PM, for the owner). That change does not apply to a job of a unit. The fix makes the phase clock follow the stable ID that this decision names. No conflict.

**ADR 19 (not owner-tagged).** "A rename keeps the ID." Changed by ADR 47 and ADR 248. The job body keeps the unit ID and adds the namespace ID; the names stay labels. No conflict.

**ADR 362 decision 1 (owner).** "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it. A name is a label for people only." Changed by ADR 363 (decision 2 only). The job body stores both stable IDs. No conflict.

**ADR 220 decision 1 (owner, title).** "Each row that belongs to a namespace holds its namespace ID." Changed by ADR 363, which extends decisions 1, 2 and 3. The job row keeps the namespace name as its key until the store move. The namespace ID is a value in the body, not a key, and ADR 362 decision 1 asks for it. No conflict with the order of ADR 363.

## Open questions

none
