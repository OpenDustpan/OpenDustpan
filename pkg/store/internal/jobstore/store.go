package jobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Page sizes of List.
const (
	// DefaultLimit is the page size of a List with Limit 0.
	DefaultLimit = 100
	// MaxLimit is the largest page size of List.
	MaxLimit = 1000
)

// ErrDamaged reports a stored row that this version cannot read: a job body
// that does not decode, or a plan, log or chunk row that disagrees with
// itself. It is job.ErrDamaged.
var ErrDamaged = job.ErrDamaged

// createAttempts is how many times Create runs when two creates with one
// idempotency key race. The second attempt finds the first job.
const createAttempts = 2

// Store serves job.Store and job.FreezeStore from one Runner.
type Store struct {
	run Runner
}

var (
	_ job.Store              = (*Store)(nil)
	_ job.FreezeStore        = (*Store)(nil)
	_ job.FinalVersionReader = (*Store)(nil)
)

// New returns the job stores over run.
func New(run Runner) *Store {
	return &Store{run: run}
}

// Create implements job.Store.
//
//nolint:gocritic // hugeParam: the signature of job.Store takes the job by value.
func (s *Store) Create(ctx context.Context, j job.Job, d *durable.StartRequest, fence namespace.Fence,
	e []audit.Entry,
) (job.Job, bool, error) {
	if err := checkNewJob(&j, d, fence); err != nil {
		return job.Job{}, false, err
	}

	if err := checkEntries(e, fence); err != nil {
		return job.Job{}, false, err
	}

	for attempt := 1; ; attempt++ {
		stored, created, err := s.createOnce(ctx, &j, d, fence, e)
		if errors.Is(err, ErrKeyIndex) && attempt < createAttempts {
			continue
		}

		return stored, created, err
	}
}

// createIn stores j in the transaction tx, or returns the job that its
// idempotency key already names.
func createIn(ctx context.Context, tx Tx, j *job.Job, d *durable.StartRequest, e []audit.Entry,
) (job.Job, bool, error) {
	first, ok, err := sameKey(ctx, tx, j)
	if err != nil || ok {
		return first, false, err
	}

	if limitErr := checkLimits(ctx, tx, j.Spec.Unit.Namespace(), j.Spec.Unit.UnitID()); limitErr != nil {
		return job.Job{}, false, limitErr
	}

	now, err := tx.Now(ctx)
	if err != nil {
		return job.Job{}, false, err
	}

	stored := *j
	stored.Phases = clonePhases(j.Phases)
	stored.State, stored.Version, stored.CreatedAt, stored.UpdatedAt = job.StateQueued, 1, now, now
	stored.StateChangedAt = now

	// A job whose first gate needs approval is stored in awaiting-approval,
	// in this transaction, so that no queued job with a closed gate is ever
	// visible to the code that dispatches queued jobs.
	if j.State == job.StateAwaitingApproval {
		stored.State, stored.Wait = job.StateAwaitingApproval, job.WaitNone
	}

	if d != nil {
		if err := startDispatch(ctx, tx, &stored, d, dispatchTimes{notBefore: time.Time{}, now: now}); err != nil {
			return job.Job{}, false, err
		}

		stored.State = job.StateDispatched
	}

	if err := insert(ctx, tx, &stored); err != nil {
		return job.Job{}, false, err
	}

	return stored, true, tx.AppendAudit(ctx, e)
}

// Get implements job.Store.
func (s *Store) Get(ctx context.Context, id job.ID) (job.Job, error) {
	var out job.Job

	err := s.run.Read(ctx, func(tx Tx) error {
		r, ok, err := tx.Job(ctx, id)
		if err != nil {
			return err
		}

		if !ok {
			return job.ErrNotFound
		}

		out, err = jobOf(&r)

		return err
	})

	return out, err
}

// List implements job.Store.
func (s *Store) List(ctx context.Context, f job.Filter) (job.Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	limit = min(limit, MaxLimit)

	var out job.Page

	err := s.run.Read(ctx, func(tx Tx) error {
		rows, err := tx.List(ctx, Query{
			Namespace: f.Namespace, UnitID: f.UnitID, States: f.States, Limit: limit, After: f.After,
			StateChangedBeforeMs: millisOrZero(f.StateChangedBefore),
			StateChangedAfterMs:  millisOrZero(f.StateChangedAfter),
		})
		if err != nil {
			return err
		}

		out = pageOf(rows, limit)

		return nil
	})

	return out, err
}

// CancelDamaged implements job.Store.
func (s *Store) CancelDamaged(ctx context.Context, id job.ID, fence namespace.Fence, e []audit.Entry) error {
	if err := checkEntries(e, fence); err != nil {
		return err
	}

	return s.run.Write(ctx, fence, func(tx Tx) error {
		r, ok, err := tx.Job(ctx, id)
		if err != nil {
			return err
		}

		// A row of another namespace is not the job of this fence: the
		// store answers as it does for an unknown ID.
		if !ok || r.Namespace != fence.Namespace {
			return job.ErrNotFound
		}

		// A cancel through this path does not stop the durable operation of
		// the job, so it is only for a row that no other path can read.
		// Defense in depth: the caller checks that Get gives ErrDamaged;
		// this check stays so that a new caller cannot cancel a readable
		// job here.
		if _, err = jobOf(&r); err == nil {
			return fmt.Errorf("%w: job %s is not damaged", job.ErrInvalid, id)
		}

		if r.State.Terminal() {
			return fmt.Errorf("%w: job %s is %s", job.ErrFinished, id, r.State)
		}

		return cancelRow(ctx, tx, &r, e)
	})
}

// cancelRow sets the row r to canceled at the version after its own, updates
// it in the transaction tx and appends the audit entries e.
func cancelRow(ctx context.Context, tx Tx, r *Row, e []audit.Entry) error {
	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	prev := r.Version
	r.State, r.Wait, r.Version = job.StateCanceled, job.WaitNone, prev+1
	r.UpdatedMs, r.StateChangedMs = now.UnixMilli(), now.UnixMilli()

	changed, err := tx.UpdateJob(ctx, *r, prev)
	if err != nil {
		return err
	}

	if !changed {
		return job.ErrConflict
	}

	return tx.AppendAudit(ctx, e)
}

// Orphans implements job.Store.
func (s *Store) Orphans(ctx context.Context, limit int) ([]job.Orphan, []job.DamagedRow, error) {
	if limit <= 0 {
		return nil, nil, nil
	}

	var (
		out     []job.Orphan
		damaged []job.DamagedRow
	)

	err := s.run.Read(ctx, func(tx Tx) error {
		rows, err := tx.Orphans(ctx, min(limit, MaxLimit))
		if err != nil {
			return err
		}

		for i := range rows {
			j, err := jobOf(&rows[i].Row)
			if err != nil {
				if !errors.Is(err, job.ErrDamaged) {
					return err
				}

				damaged = append(damaged, job.DamagedRow{
					ID: rows[i].Row.ID, UnitID: rows[i].Row.UnitID, Reason: DamageReason(err),
				})

				continue
			}

			out = append(out, job.Orphan{Job: j, Missing: rows[i].Missing})
		}

		return nil
	})

	return out, damaged, err
}

// FinalVersion implements job.FinalVersionReader.
func (s *Store) FinalVersion(ctx context.Context, ns namespace.Name, unitID string) (job.FinalVersion, bool, error) {
	if ns == "" {
		return job.FinalVersion{}, false, fmt.Errorf("%w: the namespace of a final version is empty", job.ErrInvalid)
	}

	if unitID == "" {
		return job.FinalVersion{}, false, fmt.Errorf("%w: the unit of a final version is empty", job.ErrInvalid)
	}

	var (
		out job.FinalVersion
		ok  bool
	)

	err := s.run.Read(ctx, func(tx Tx) error {
		row, found, err := tx.FinalVersion(ctx, ns, unitID)
		if err != nil {
			return err
		}

		if !found {
			return nil
		}

		out = job.FinalVersion{
			UnitID:    row.UnitID,
			VersionID: row.VersionID,
			Commit:    row.Commit,
			Job:       row.Job,
			At:        row.At,
		}
		ok = true

		return nil
	})
	if err != nil {
		return job.FinalVersion{}, false, fmt.Errorf("final version of unit %s: %w", unitID, err)
	}

	return out, ok, nil
}

// CreateFreeze implements job.FreezeStore.
func (s *Store) CreateFreeze(ctx context.Context, f job.Freeze, fence namespace.Fence, e audit.Entry) error {
	if err := f.Validate(); err != nil {
		return err
	}

	if f.Namespace != fence.Namespace {
		return fmt.Errorf("%w: freeze %s names namespace %q, the fence names %q", job.ErrInvalid, f.ID, f.Namespace,
			fence.Namespace)
	}

	if f.ID == "" {
		return fmt.Errorf("%w: a freeze needs an ID", job.ErrInvalid)
	}

	entries := []audit.Entry{e}
	if err := checkEntries(entries, fence); err != nil {
		return err
	}

	return s.run.Write(ctx, fence, func(tx Tx) error {
		err := tx.InsertFreeze(ctx, FreezeRow{
			ID: f.ID, Namespace: f.Namespace, Reason: f.Reason, StartsMs: f.StartsAt.UnixMilli(),
			EndsMs: millisOrZero(f.EndsAt), CreatedBy: f.CreatedBy, EndedBy: "",
		})
		if err != nil {
			return err
		}

		return tx.AppendAudit(ctx, entries)
	})
}

// EndFreeze implements job.FreezeStore. A freeze that has not started yet
// ends at its start, so that it is never active.
func (s *Store) EndFreeze(ctx context.Context, id, by string, fence namespace.Fence, e audit.Entry) error {
	entries := []audit.Entry{e}
	if err := checkEntries(entries, fence); err != nil {
		return err
	}

	return s.run.Write(ctx, fence, func(tx Tx) error {
		f, ok, err := tx.Freeze(ctx, id)
		if err != nil {
			return err
		}

		if !ok || f.Namespace != fence.Namespace {
			return job.ErrNotFound
		}

		now, err := tx.Now(ctx)
		if err != nil {
			return err
		}

		nowMs := now.UnixMilli()
		if f.EndsMs != 0 && nowMs >= f.EndsMs {
			return job.ErrFinished
		}

		if err := tx.SetFreezeEnd(ctx, id, max(nowMs, f.StartsMs), by); err != nil {
			return err
		}

		return tx.AppendAudit(ctx, entries)
	})
}

// ListFreezes implements job.FreezeStore.
func (s *Store) ListFreezes(ctx context.Context, ns namespace.Name, since time.Time) ([]job.Freeze, error) {
	var out []job.Freeze

	err := s.run.Read(ctx, func(tx Tx) error {
		rows, err := tx.Freezes(ctx, ns, since.UnixMilli())
		if err != nil {
			return err
		}

		for _, r := range rows {
			f := job.Freeze{
				ID: r.ID, Namespace: r.Namespace, Reason: r.Reason, StartsAt: time.UnixMilli(r.StartsMs).UTC(),
				EndsAt: time.Time{}, CreatedBy: r.CreatedBy, EndedBy: r.EndedBy,
			}
			if r.EndsMs != 0 {
				f.EndsAt = time.UnixMilli(r.EndsMs).UTC()
			}

			out = append(out, f)
		}

		return nil
	})

	return out, err
}

// createOnce stores j in one transaction, or returns the job that its
// idempotency key already names. created is true for a new job.
func (s *Store) createOnce(ctx context.Context, j *job.Job, d *durable.StartRequest, fence namespace.Fence,
	e []audit.Entry,
) (job.Job, bool, error) {
	var (
		out     job.Job
		created bool
	)

	err := s.run.Write(ctx, fence, func(tx Tx) error {
		var err error

		out, created, err = createIn(ctx, tx, j, d, e)

		return err
	})
	if err != nil {
		return job.Job{}, false, err
	}

	return out, created, nil
}

func millisOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// nextUpdateAt returns the update time of a write to a job whose stored
// update time is stored, at the clock time now. The store keeps Unix
// milliseconds, so the result is at least one millisecond after stored. Two
// writes in one millisecond of the clock still move the update time.
func nextUpdateAt(now, stored time.Time) time.Time {
	if now.UnixMilli() <= stored.UnixMilli() {
		return time.UnixMilli(stored.UnixMilli() + 1).UTC()
	}

	return now
}

// checkNewJob returns job.ErrInvalid for a job that Create cannot store.
//
// Defense in depth: the service checks the same fields before it calls the
// store, and this check stays so that a new caller cannot store a job with
// no unit ID, or a job of another namespace than its fence.
func checkNewJob(j *job.Job, d *durable.StartRequest, fence namespace.Fence) error {
	switch {
	case j.ID == "":
		return fmt.Errorf("%w: a job needs an ID", job.ErrInvalid)
	case !j.Spec.Unit.IsUnit() || j.Spec.Unit.UnitID() == "":
		return fmt.Errorf("%w: the unit of job %s needs a unit reference with a unit ID", job.ErrInvalid, j.ID)
	case j.Spec.Unit.Namespace() != fence.Namespace:
		return fmt.Errorf("%w: job %s names namespace %q, the fence names %q", job.ErrInvalid, j.ID,
			j.Spec.Unit.Namespace(), fence.Namespace)
	case j.State != job.StateQueued && j.State != job.StateAwaitingApproval:
		return fmt.Errorf("%w: job %s starts in state %q, not queued or awaiting-approval", job.ErrInvalid, j.ID,
			j.State)
	case j.State == job.StateAwaitingApproval && d != nil:
		return fmt.Errorf("%w: job %s waits for approval, so it has no dispatch", job.ErrInvalid, j.ID)
	case j.Current < 0 || j.Current > len(j.Phases):
		return fmt.Errorf("%w: job %s has current phase %d of %d", job.ErrInvalid, j.ID, j.Current, len(j.Phases))
	default:
		return nil
	}
}

// checkEntries returns the audit error for an entry that fails validation or
// names another namespace than the fence.
func checkEntries(e []audit.Entry, fence namespace.Fence) error {
	for i := range e {
		if err := e[i].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e[i].Namespace,
				fence.Namespace)
		}
	}

	return nil
}

// sameKey returns the job that the idempotency key of j already names, and
// false when j has no key or the key names no job. It returns
// job.ErrKeyConflict when the key names a job with another spec.
func sameKey(ctx context.Context, tx Tx, j *job.Job) (job.Job, bool, error) {
	if j.IdempotencyKey == "" {
		return noJob(), false, nil
	}

	row, ok, err := tx.JobByKey(ctx, j.Spec.Unit.Namespace(), j.IdempotencyKey)
	if err != nil || !ok {
		return noJob(), false, err
	}

	first, err := jobOf(&row)
	if err != nil {
		return noJob(), false, err
	}

	same, err := sameSpec(&first.Spec, &j.Spec)
	if err != nil {
		return noJob(), false, err
	}

	// The request hash covers what the spec does not hold: the reason, and
	// the pool and location as the caller named them. A job that has a hash
	// and a retry with another one are two requests under one key.
	if !same || first.RequestHash != j.RequestHash {
		return noJob(), false, job.ErrKeyConflict
	}

	return first, true, nil
}

// sameSpec reports whether two specs encode to the same JSON.
func sameSpec(a, b *job.Spec) (bool, error) {
	x, err := json.Marshal(encodeSpec(a))
	if err != nil {
		return false, fmt.Errorf("encode spec: %w", err)
	}

	y, err := json.Marshal(encodeSpec(b))
	if err != nil {
		return false, fmt.Errorf("encode spec: %w", err)
	}

	return bytes.Equal(x, y), nil
}

// insert stores j as a new row.
func insert(ctx context.Context, tx Tx, j *job.Job) error {
	r, err := rowOf(j)
	if err != nil {
		return err
	}

	err = tx.InsertJob(ctx, r)
	if errors.Is(err, ErrHolderIndex) {
		return fmt.Errorf("%w: %w", job.ErrUnitBusy, err)
	}

	return err
}

// update replaces the row of version prev with j.
func update(ctx context.Context, tx Tx, j *job.Job, prev int64) error {
	r, err := rowOf(j)
	if err != nil {
		return err
	}

	ok, err := tx.UpdateJob(ctx, r, prev)
	if errors.Is(err, ErrHolderIndex) {
		return fmt.Errorf("%w: %w", job.ErrUnitBusy, err)
	}

	if err != nil {
		return err
	}

	if !ok {
		return job.ErrConflict
	}

	return nil
}

// rowOf returns the row of j.
func rowOf(j *job.Job) (Row, error) {
	body, err := encodeBody(j)
	if err != nil {
		return Row{}, err
	}

	r := Row{
		ID: j.ID, Namespace: j.Spec.Unit.Namespace(), UnitID: j.Spec.Unit.UnitID(), Seq: 0, State: j.State,
		Wait: j.Wait, Phase: j.Current, Changes: false, Operation: "", Key: j.IdempotencyKey, Version: j.Version,
		Body: body, CreatedMs: j.CreatedAt.UnixMilli(), UpdatedMs: j.UpdatedAt.UnixMilli(),
		StateChangedMs: j.StateChangedAt.UnixMilli(),
	}

	if j.Current < len(j.Phases) {
		r.Changes = j.Phases[j.Current].Kind.Changes()
		r.Operation = j.Phases[j.Current].Operation
	}

	return r, nil
}

// jobOf returns the job that a row holds. It returns an error that wraps
// ErrDamaged for a row whose body does not decode, and for a row whose body
// names another namespace or another unit than the row.
func jobOf(r *Row) (job.Job, error) {
	j := job.Job{
		ID: r.ID, Spec: emptySpec(), State: r.State, Wait: r.Wait, Reason: "", Phases: nil, Current: r.Phase,
		StartedBy: "", Source: "", Agent: "", AgentMode: "", BreakGlass: nil, Trigger: nil, Delivery: nil, Closed: nil,
		Verification: job.VerificationNone, IdempotencyKey: r.Key, RequestHash: "", Version: r.Version,
		CreatedAt: time.UnixMilli(r.CreatedMs).UTC(), UpdatedAt: time.UnixMilli(r.UpdatedMs).UTC(),
		StateChangedAt: time.UnixMilli(r.StateChangedMs).UTC(),
	}

	if err := decodeBody(r.Body, &j); err != nil {
		return job.Job{}, fmt.Errorf("%w: %w", ErrDamaged, err)
	}

	// Defense in depth: the namespace and the unit of the row decide the fence
	// and the one-change rule. A body that names another namespace or another
	// unit than its row is damaged, for example by a restored backup, and the
	// store answers with an error and never with the job.
	if j.Spec.Unit.Namespace() != r.Namespace || j.Spec.Unit.UnitID() != r.UnitID {
		return job.Job{}, fmt.Errorf("%w: %w: job %s", ErrDamaged, errRowUnit, r.ID)
	}

	return j, nil
}

// pageOf returns the page of rows: the jobs that decode, the damaged rows
// that do not, and the cursor of the last row of a full page. Every error of
// jobOf is a damage.
func pageOf(rows []Row, limit int) job.Page {
	out := job.Page{Jobs: nil, Damaged: nil, After: ""}

	for i := range rows {
		j, err := jobOf(&rows[i])
		if err != nil {
			out.Damaged = append(out.Damaged, job.DamagedRow{
				ID: rows[i].ID, UnitID: rows[i].UnitID, Reason: DamageReason(err),
			})

			continue
		}

		out.Jobs = append(out.Jobs, j)
	}

	// A full page may have rows behind it: the cursor of the next page is
	// the last row read, whether it decoded or not.
	if len(rows) == limit {
		out.After = rows[len(rows)-1].ID
	}

	return out
}

// cloneJob returns a copy of j that shares no slice with it.
func cloneJob(j *job.Job) job.Job {
	out := *j
	out.Phases = clonePhases(j.Phases)

	return out
}

func clonePhases(in []job.Phase) []job.Phase {
	out := slices.Clone(in)

	for i := range out {
		out[i].Steps = slices.Clone(out[i].Steps)
		out[i].Gates = slices.Clone(out[i].Gates)
		out[i].Approvals = slices.Clone(out[i].Approvals)
	}

	return out
}

// emptySpec returns the spec that decodeBody fills.
func emptySpec() job.Spec {
	return job.Spec{
		Unit:      identity.Object{},
		Operation: "",
		Purpose:   "",
		Executor:  "",
		Location:  job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""},
		Steps:     nil,
		Pool:      "",
		Profile:   "",
		Edits:     nil,
		Delivery:  "",
		Defaulted: nil,
	}
}

// noJob returns the job that a lookup answers when it finds none.
func noJob() job.Job {
	return job.Job{
		ID:             "",
		Spec:           emptySpec(),
		State:          "",
		Wait:           "",
		Reason:         "",
		Phases:         nil,
		Current:        0,
		StartedBy:      "",
		Source:         "",
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}
}

// Decode returns the job that the row r holds. It returns an error that wraps
// job.ErrDamaged for a row that this version cannot read: a body that does not
// decode, and a body that names another namespace or another unit than the
// row. The drift store reads a job with it, so that it never trusts a row that
// the job store would refuse.
func Decode(r *Row) (job.Job, error) {
	return jobOf(r)
}

// logger returns the logger of the runner, which records a damaged row that a
// reader skips, for example the close loop of ObserveStateVersion. A nil
// logger discards.
func (s *Store) logger() *slog.Logger {
	if log := s.run.Logger(); log != nil {
		return log
	}

	return slog.New(slog.DiscardHandler)
}
