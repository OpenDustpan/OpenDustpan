package selfservicetest_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/selfservicetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// memDB is the reference adapter. One mutex stands for the transaction of
// a SQL adapter: each write checks the fence, changes the rows and appends
// the audit entries, or does nothing.
type memDB struct {
	mu         sync.Mutex
	fences     map[namespace.Name]int64
	audit      map[namespace.Name][]audit.Entry
	operations map[string]durable.Kind
	requests   map[selfservice.RequestRef]selfservice.Request
	revisions  map[string]selfservice.Revision
	drafts     map[selfservice.BlueprintRef]draft
	versions   map[selfservice.BlueprintRef][]selfservice.Version
	units      map[string]ownedUnit
	quota      quotaState
	// newUnitTx is the constructor of selfservice.UnitTx that the wiring gives
	// the store: it takes the transaction, here the copy of the units that
	// one write changes.
	newUnitTx func(units map[string]ownedUnit) *memUnitTx
	nextID    int
}

// quotaState is the counter that shares the database of the stores. The
// store writes it through a quota.Tx in the transaction of a submit.
type quotaState struct {
	settled      map[quota.Key]int
	reservations map[quota.ReservationID]quota.Reservation
	requests     map[quota.Key][]time.Time
}

func (q quotaState) clone() quotaState {
	out := quotaState{
		settled: maps.Clone(q.settled), reservations: maps.Clone(q.reservations),
		requests: make(map[quota.Key][]time.Time, len(q.requests)),
	}
	for k, v := range q.requests {
		out.requests[k] = slices.Clone(v)
	}

	return out
}

type draft struct {
	blueprint selfservice.Blueprint
	version   int64
}

func newMemDB() *memDB {
	return &memDB{
		mu:         sync.Mutex{},
		fences:     map[namespace.Name]int64{},
		audit:      map[namespace.Name][]audit.Entry{},
		operations: map[string]durable.Kind{},
		requests:   map[selfservice.RequestRef]selfservice.Request{},
		revisions:  map[string]selfservice.Revision{},
		drafts:     map[selfservice.BlueprintRef]draft{},
		versions:   map[selfservice.BlueprintRef][]selfservice.Version{},
		units:      map[string]ownedUnit{},
		quota: quotaState{
			settled: map[quota.Key]int{}, reservations: map[quota.ReservationID]quota.Reservation{},
			requests: map[quota.Key][]time.Time{},
		},
		nextID:    0,
		newUnitTx: newMemUnitTx,
	}
}

// ownedUnit is a unit that a request made.
type ownedUnit struct {
	unit    identity.Object
	request selfservice.RequestID
}

// memUnitTx is the selfservice.UnitTx of the reference wiring, on the copy of
// the units that one write changes.
type memUnitTx struct {
	units map[string]ownedUnit
}

func newMemUnitTx(units map[string]ownedUnit) *memUnitTx {
	return &memUnitTx{units: units}
}

// Provision implements selfservice.UnitTx.
func (tx *memUnitTx) Provision(_ context.Context, p selfservice.Provision) (identity.Object, error) {
	if p.Request == "" || p.Tool == "" || p.Unit == "" || p.Ref.Name == "" || p.Blueprint.Name == "" ||
		p.Version < 1 {
		return identity.Object{}, fmt.Errorf("%w: a provision needs each field", selfservice.ErrInvalid)
	}

	key := unitKey(p.Ref.Namespace, p.Tool, p.Unit)
	if have, ok := tx.units[key]; ok {
		if have.request != p.Request {
			return identity.Object{}, fmt.Errorf("%w: %s belongs to another request", selfservice.ErrUnitName, p.Unit)
		}

		return have.unit, nil
	}

	o, err := identity.UnitObject(p.Tool, p.Ref.Namespace, p.Unit)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", selfservice.ErrUnitName, err)
	}

	o = o.WithUnitID("ws-" + p.Unit)
	tx.units[key] = ownedUnit{unit: o, request: p.Request}

	return o, nil
}

// InTx implements selfservicetest.UnitTxHarness.
func (m *memDB) InTx(_ context.Context, fn func(tx selfservice.UnitTx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	units := maps.Clone(m.units)
	if err := fn(m.newUnitTx(units)); err != nil {
		return err
	}

	m.units = units

	return nil
}

// CreateNamespace implements selfservicetest.Domain.
func (m *memDB) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

// CreateUnit implements selfservicetest.Domain.
func (m *memDB) CreateUnit(_ context.Context, fence namespace.Fence, tool, name string) (identity.Object, error) {
	o, err := identity.UnitObject(tool, fence.Namespace, name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("create unit: %w", err)
	}

	return o.WithUnitID("ws-" + name), nil
}

// BumpFence implements selfservicetest.Domain.
func (m *memDB) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: m.fences[fence.Namespace]}, nil
}

// AuditEntries implements selfservicetest.Domain.
func (m *memDB) AuditEntries(_ context.Context, ns namespace.Name) ([]audit.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.audit[ns]), nil
}

// Operation implements selfservicetest.Domain.
func (m *memDB) Operation(_ context.Context, key string) (durable.Kind, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	k, ok := m.operations[key]

	return k, ok, nil
}

// QuotaUsage implements selfservicetest.Domain.
func (m *memDB) QuotaUsage(_ context.Context, checks []quota.Check, when time.Time) ([]quota.Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	tx := memQuotaTx{state: m.quota}

	return tx.usages(checks, when), nil
}

// Unit implements selfservicetest.Domain.
func (m *memDB) Unit(_ context.Context, ns namespace.Name, tool, name string) (identity.Object, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.units[unitKey(ns, tool, name)]

	return u.unit, ok, nil
}

func unitKey(ns namespace.Name, tool, name string) string {
	return ns.String() + "/" + tool + "/" + name
}

// Submit implements selfservice.Store.
func (m *memDB) Submit(ctx context.Context, s *selfservice.Submission, fence namespace.Fence) (selfservice.Request,
	selfservice.Revision, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return selfservice.Request{}, selfservice.Revision{}, err
	}

	rows, err := m.rowsOf(s)
	if err != nil {
		return selfservice.Request{}, selfservice.Revision{}, err
	}

	// The writes below change a copy of the counter, which replaces it only
	// when each one passes: the rollback of a SQL transaction.
	tx := memQuotaTx{state: m.quota.clone()}

	if err := m.supersede(ctx, &tx, s, &rows); err != nil {
		return selfservice.Request{}, selfservice.Revision{}, err
	}

	if err := writeQuota(ctx, &tx, s, &rows.rev); err != nil {
		return selfservice.Request{}, selfservice.Revision{}, err
	}

	units := maps.Clone(m.units)

	if s.Provision != nil {
		p := *s.Provision
		p.Request = m.requestID(&rows.req)

		u, err := m.newUnitTx(units).Provision(ctx, p)
		if err != nil {
			return selfservice.Request{}, selfservice.Revision{}, err
		}

		rows.req.Unit = u
	}

	m.commit(&rows, units, tx.state)
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], s.Audit...)

	return rows.req, rows.rev, nil
}

// submitRows are the rows that a submit writes: the request, the new
// revision, and the last revision of a request that exists, or nil, with
// supersedes set when the submit supersedes it.
type submitRows struct {
	last       *selfservice.Revision
	req        selfservice.Request
	rev        selfservice.Revision
	supersedes bool
}

// checkSubmission checks the fields of s that need no stored row.
func checkSubmission(s *selfservice.Submission) error {
	switch {
	case s.Revision.Deadline.IsZero():
		return fmt.Errorf("%w: a revision needs a deadline", selfservice.ErrInvalid)
	case s.Quota != nil && s.Quota.Reservation != nil && !s.Quota.Reservation.EndsAt.Equal(s.Revision.Deadline):
		return fmt.Errorf("%w: the reservation does not end at the deadline", selfservice.ErrInvalid)
	default:
		return nil
	}
}

// checkLast checks s against last, the last revision of its request.
func checkLast(s *selfservice.Submission, last *selfservice.Revision) error {
	switch {
	case s.Supersedes != 0 && s.Supersedes != last.Number:
		return fmt.Errorf("%w: revision %d is not the last one", selfservice.ErrConflict, s.Supersedes)
	case s.Supersedes != 0:
		return selfservice.CheckSupersede(last, nil)
	case !last.State.Terminal():
		return fmt.Errorf("%w: revision %d", selfservice.ErrInFlight, last.Number)
	default:
		return nil
	}
}

// writeQuota reserves, then records, through tx.
func writeQuota(ctx context.Context, tx *memQuotaTx, s *selfservice.Submission, rev *selfservice.Revision) error {
	if s.Quota == nil {
		return nil
	}

	at := time.Now().UTC()

	if r := s.Quota.Reservation; r != nil {
		if _, err := tx.Reserve(ctx, *r, s.Quota.Units); err != nil {
			return fmt.Errorf("reserve: %w", err)
		}

		rev.Reservation, at = r.ID, r.At
	}

	if err := tx.Record(ctx, at, s.Quota.Requests); err != nil {
		return fmt.Errorf("record: %w", err)
	}

	return nil
}

// memQuotaTx is the quota.Tx of the reference adapter, on the copy of the
// counter that one submit writes.
type memQuotaTx struct {
	state quotaState
}

func (tx *memQuotaTx) Reserve(_ context.Context, r quota.Reservation, checks []quota.Check) (quota.Reservation,
	error,
) {
	if err := r.Validate(); err != nil {
		return quota.Reservation{}, err
	}

	if have, ok := tx.state.reservations[r.ID]; ok {
		return have, nil
	}

	if x := quota.Evaluate(checks, tx.usages(checks, r.At), 1); x != nil {
		return quota.Reservation{}, &quota.ExceededError{Exceeded: x}
	}

	tx.state.reservations[r.ID] = r

	return r, nil
}

func (tx *memQuotaTx) Record(_ context.Context, at time.Time, checks []quota.Check) error {
	if x := quota.Evaluate(checks, tx.usages(checks, at), 1); x != nil {
		return &quota.ExceededError{Exceeded: x}
	}

	for _, c := range checks {
		tx.state.requests[c.Key] = append(tx.state.requests[c.Key], at)
	}

	return nil
}

func (tx *memQuotaTx) SetEnd(_ context.Context, id quota.ReservationID, endsAt time.Time) error {
	r, ok := tx.state.reservations[id]

	switch {
	case !ok:
		return fmt.Errorf("%w: %s", quota.ErrNotFound, id)
	case r.State != quota.ReservationOpen:
		return fmt.Errorf("%w: %s", quota.ErrEnded, id)
	case !endsAt.After(r.At):
		return fmt.Errorf("%w: %s cannot end before %v", quota.ErrInvalid, id, r.At)
	default:
	}

	r.EndsAt = endsAt
	tx.state.reservations[id] = r

	return nil
}

func (tx *memQuotaTx) usages(checks []quota.Check, at time.Time) []quota.Usage {
	out := make([]quota.Usage, 0, len(checks))

	for _, c := range checks {
		u := quota.Usage{Key: c.Key, Settled: tx.state.settled[c.Key], Reserved: 0, Requests: 0}

		for _, r := range tx.state.reservations {
			if r.State == quota.ReservationOpen && r.EndsAt.After(at) && slices.Contains(r.Keys, c.Key) {
				u.Reserved++
			}
		}

		for _, t := range tx.state.requests[c.Key] {
			if c.Key.Kind == quota.KindRequests && t.After(at.Add(-c.Window)) && !t.After(at) {
				u.Requests++
			}
		}

		out = append(out, u)
	}

	return out
}

// Get implements selfservice.Store.
func (m *memDB) Get(_ context.Context, ref selfservice.RequestRef) (selfservice.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.requests[ref]
	if !ok {
		return selfservice.Request{}, fmt.Errorf("%w: %s", selfservice.ErrNotFound, ref)
	}

	return req, nil
}

// Revision implements selfservice.Store.
func (m *memDB) Revision(_ context.Context, id selfservice.RequestID, n int) (selfservice.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rev, ok := m.revisions[selfservice.RevisionKey(id, n)]
	if !ok {
		return selfservice.Revision{}, fmt.Errorf("%w: %s", selfservice.ErrNotFound, selfservice.RevisionKey(id, n))
	}

	return rev, nil
}

// ListRequests implements selfservice.Store.
func (m *memDB) ListRequests(_ context.Context, f selfservice.ListFilter) ([]selfservice.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []selfservice.Request

	for ref := range m.requests {
		req := m.requests[ref]
		last := m.revisions[selfservice.RevisionKey(req.ID, req.Last)]

		switch {
		case f.Namespace != "" && req.Ref.Namespace != f.Namespace,
			f.Blueprint != "" && req.Ref.Blueprint != f.Blueprint,
			f.UnitID != "" && req.Unit.UnitID() != f.UnitID,
			len(f.States) > 0 && !slices.Contains(f.States, last.State),
			f.After != "" && req.ID <= f.After:
			continue
		default:
			out = append(out, req)
		}
	}

	slices.SortFunc(out, func(a, b selfservice.Request) int {
		return compare(a.ID, b.ID)
	})

	return out, nil
}

// Change implements selfservice.Store.
func (m *memDB) Change(_ context.Context, c selfservice.RevisionChange, fence namespace.Fence,
	e []audit.Entry,
) (selfservice.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return selfservice.Revision{}, err
	}

	req, ok := m.requests[c.Ref]
	if !ok {
		return selfservice.Revision{}, fmt.Errorf("%w: %s", selfservice.ErrNotFound, c.Ref)
	}

	key := selfservice.RevisionKey(req.ID, c.Number)

	rev, ok := m.revisions[key]
	if !ok {
		return selfservice.Revision{}, fmt.Errorf("%w: %s", selfservice.ErrNotFound, key)
	}

	if req.Version != c.Version {
		return selfservice.Revision{}, fmt.Errorf("%w: %s at %d", selfservice.ErrConflict, c.Ref, req.Version)
	}

	if err := selfservice.CheckTransition(rev.State, c.To); err != nil {
		return selfservice.Revision{}, err
	}

	m.apply(&req, &rev, c)
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return rev, nil
}

// Extend implements selfservice.Store.
func (m *memDB) Extend(ctx context.Context, d selfservice.DeadlineChange, fence namespace.Fence) (selfservice.Revision,
	error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return noRevision(), err
	}

	req, ok := m.requests[d.Ref]
	if !ok {
		return noRevision(), fmt.Errorf("%w: %s", selfservice.ErrNotFound, d.Ref)
	}

	rev, ok := m.revisions[selfservice.RevisionKey(req.ID, d.Number)]

	switch {
	case !ok:
		return noRevision(), fmt.Errorf("%w: revision %d of %s", selfservice.ErrNotFound, d.Number, d.Ref)
	case req.Version != d.Version:
		return noRevision(), fmt.Errorf("%w: %s at %d", selfservice.ErrConflict, d.Ref, req.Version)
	case rev.State.Terminal():
		return noRevision(), fmt.Errorf("%w: the revision is %s", selfservice.ErrFinished, rev.State)
	case !d.Deadline.After(rev.Deadline):
		return noRevision(), fmt.Errorf("%w: the deadline %v is not after %v", selfservice.ErrInvalid, d.Deadline,
			rev.Deadline)
	default:
	}

	tx := memQuotaTx{state: m.quota.clone()}
	if rev.Reservation != "" {
		if err := tx.SetEnd(ctx, rev.Reservation, d.Deadline); err != nil {
			return noRevision(), fmt.Errorf("extend: %w", err)
		}
	}

	now := time.Now().UTC()
	rev.Deadline, rev.UpdatedAt = d.Deadline, now
	req.Version++
	req.UpdatedAt = now
	m.quota = tx.state
	m.requests[req.Ref] = req
	m.revisions[rev.Key()] = rev

	return rev, nil
}

// ObserveStateVersion implements selfservice.Store.
func (m *memDB) ObserveStateVersion(_ context.Context, v job.StateVersion, fence namespace.Fence) (selfservice.Revision,
	bool, error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return noRevision(), false, err
	}

	if !v.Final {
		return noRevision(), false, nil
	}

	for ref := range m.requests {
		req := m.requests[ref]
		rev := m.revisions[selfservice.RevisionKey(req.ID, req.Last)]

		closes := rev.Origin == selfservice.OriginGit && rev.Job == "" && rev.State == selfservice.StateAwaitingState &&
			req.Unit.UnitID() == v.Unit.UnitID() &&
			(rev.Commit == v.Commit || (v.Commit == "" && !rev.UpdatedAt.After(v.At)))
		if !closes {
			continue
		}

		m.apply(&req, &rev, selfservice.RevisionChange{
			Ref: req.Ref, To: selfservice.StateApplied, Job: "", JobVersion: 0, Reason: "", Failures: nil,
			Unit: identity.Object{}, Number: rev.Number, Version: req.Version,
		})

		return rev, true, nil
	}

	return noRevision(), false, nil
}

// PutDraft implements selfservice.BlueprintStore.
func (m *memDB) PutDraft(_ context.Context, b selfservice.Blueprint, version int64, fence namespace.Fence,
	e []audit.Entry,
) (int64, error) {
	if !selfservice.ValidName(b.Name) || version < 0 {
		return 0, fmt.Errorf("%w: draft %q at %d", selfservice.ErrInvalid, b.Name, version)
	}

	if err := checkEntries(fence, e); err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return 0, err
	}

	ref := selfservice.BlueprintRef{Namespace: fence.Namespace, Name: b.Name}

	if _, ok := m.drafts[ref]; !ok && version == 0 && m.blueprintsIn(fence.Namespace) >= selfservice.MaxBlueprints {
		return 0, fmt.Errorf(
			"%w: %s has %d blueprints",
			selfservice.ErrLimit,
			fence.Namespace,
			selfservice.MaxBlueprints,
		)
	}

	if m.drafts[ref].version != version {
		return 0, fmt.Errorf("%w: draft %s at %d", selfservice.ErrConflict, ref, m.drafts[ref].version)
	}

	m.drafts[ref] = draft{blueprint: b, version: version + 1}
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return version + 1, nil
}

// checkEntries refuses an audit entry that fails its checks or names another
// namespace than the fence.
func checkEntries(fence namespace.Fence, e []audit.Entry) error {
	for i := range e {
		if err := e[i].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf(
				"%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry,
				e[i].Namespace,
				fence.Namespace,
			)
		}
	}

	return nil
}

// checkRef checks the reference of a write of a version and its entries
// against the fence.
func checkRef(ref selfservice.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry) error {
	if ref.Namespace != fence.Namespace {
		return fmt.Errorf("%w: blueprint %s, fence names %q", namespace.ErrFenced, ref, fence.Namespace)
	}

	if n < 1 {
		return fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
	}

	return checkEntries(fence, e)
}

// documentHash returns the hash of the document of b without its Expr
// version, the form in which a publish compares a blueprint with its draft.
func documentHash(b selfservice.Blueprint) (string, error) {
	b.ExprVersion = ""

	data, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encode blueprint: %w", err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}

// validExprVersion reports whether v has the form of an Expr version.
func validExprVersion(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}

	for _, c := range v {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-+_", c) {
			return false
		}
	}

	return true
}

// Draft implements selfservice.BlueprintStore.
func (m *memDB) Draft(_ context.Context, ref selfservice.BlueprintRef) (selfservice.Blueprint, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.drafts[ref]
	if !ok {
		return selfservice.Blueprint{}, 0, fmt.Errorf("%w: draft %s", selfservice.ErrNotFound, ref)
	}

	return d.blueprint, d.version, nil
}

// checkPublication runs the checks of a publish that need no stored row.
func checkPublication(p *selfservice.Publication, fence namespace.Fence, e []audit.Entry) error {
	switch {
	case p.Ref.Namespace != fence.Namespace:
		return fmt.Errorf("%w: blueprint %s, fence names %q", namespace.ErrFenced, p.Ref, fence.Namespace)
	case p.Ref.Name != p.Blueprint.Name:
		return fmt.Errorf("%w: reference %s, blueprint %q", selfservice.ErrInvalid, p.Ref, p.Blueprint.Name)
	case p.DraftVersion < 1:
		return fmt.Errorf("%w: draft %s at %d", selfservice.ErrConflict, p.Ref, p.DraftVersion)
	case p.Blueprint.ExprVersion != "" && !validExprVersion(string(p.Blueprint.ExprVersion)):
		return fmt.Errorf("%w: Expr version %q", selfservice.ErrInvalid, p.Blueprint.ExprVersion)
	}

	if err := p.Blueprint.Validate(); err != nil {
		return fmt.Errorf("publish %s: %w", p.Ref, err)
	}

	return checkEntries(fence, e)
}

// Publish implements selfservice.BlueprintStore.
func (m *memDB) Publish(_ context.Context, p selfservice.Publication, fence namespace.Fence,
	e []audit.Entry,
) (selfservice.Version, error) {
	if err := checkPublication(&p, fence, e); err != nil {
		return selfservice.Version{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return selfservice.Version{}, err
	}

	if err := m.checkDraft(&p); err != nil {
		return selfservice.Version{}, err
	}

	if len(m.versions[p.Ref]) >= selfservice.MaxVersions {
		return selfservice.Version{}, fmt.Errorf(
			"%w: %s has %d versions",
			selfservice.ErrLimit,
			p.Ref,
			selfservice.MaxVersions,
		)
	}

	v := selfservice.Version{
		PublishedAt: p.At, Blueprint: p.Blueprint, Namespace: p.Ref.Namespace, PublishedBy: p.By,
		Number: len(m.versions[p.Ref]) + 1, DraftVersion: p.DraftVersion, Retired: false,
	}
	m.versions[p.Ref] = append(m.versions[p.Ref], v)
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return v, nil
}

// Version implements selfservice.BlueprintStore.
func (m *memDB) Version(_ context.Context, ref selfservice.BlueprintRef, n int) (selfservice.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	vs := m.versions[ref]
	if n < 1 || n > len(vs) {
		return selfservice.Version{}, fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
	}

	return vs[n-1], nil
}

// ListBlueprints implements selfservice.BlueprintStore.
func (m *memDB) ListBlueprints(_ context.Context, ns namespace.Name) ([]selfservice.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []selfservice.Version

	for ref, vs := range m.versions {
		if ref.Namespace == ns && len(vs) > 0 {
			out = append(out, vs[len(vs)-1])
		}
	}

	slices.SortFunc(out, func(a, b selfservice.Version) int {
		return compare(a.Blueprint.Name, b.Blueprint.Name)
	})

	return out, nil
}

// Retire implements selfservice.BlueprintStore.
func (m *memDB) Retire(_ context.Context, ref selfservice.BlueprintRef, n int, fence namespace.Fence,
	e []audit.Entry,
) error {
	return m.changeVersion(ref, n, fence, e, func(v *selfservice.Version) { v.Retired = true })
}

// SetExprVersion implements selfservice.BlueprintStore.
func (m *memDB) SetExprVersion(_ context.Context, c selfservice.ExprVersionChange, fence namespace.Fence,
	e []audit.Entry,
) error {
	if !validExprVersion(c.Expr) {
		return fmt.Errorf("%w: Expr version %q", selfservice.ErrInvalid, c.Expr)
	}

	return m.changeVersion(c.Ref, c.Number, fence, e, func(v *selfservice.Version) {
		v.Blueprint.ExprVersion = selfservice.ExprVersionName(c.Expr)
	})
}

// checkDraft checks that p publishes the stored draft, once. The caller holds
// the mutex.
func (m *memDB) checkDraft(p *selfservice.Publication) error {
	d, ok := m.drafts[p.Ref]
	if !ok || d.version != p.DraftVersion {
		return fmt.Errorf("%w: draft %s", selfservice.ErrConflict, p.Ref)
	}

	want, err := documentHash(d.blueprint)
	if err != nil {
		return err
	}

	got, err := documentHash(p.Blueprint)
	if err != nil {
		return err
	}

	if got != want {
		return fmt.Errorf("%w: the document is not the stored draft %s", selfservice.ErrInvalid, p.Ref)
	}

	for i := range m.versions[p.Ref] {
		if v := &m.versions[p.Ref][i]; v.DraftVersion == p.DraftVersion {
			return fmt.Errorf(
				"%w: %s draft %d is version %d",
				selfservice.ErrAlreadyPublished,
				p.Ref,
				p.DraftVersion,
				v.Number,
			)
		}
	}

	return nil
}

// blueprintsIn counts the blueprints of ns. The caller holds the mutex.
func (m *memDB) blueprintsIn(ns namespace.Name) int {
	n := 0

	for ref := range m.drafts {
		if ref.Namespace == ns {
			n++
		}
	}

	return n
}

// rowsOf checks s against the stored rows and returns the rows to write.
func (m *memDB) rowsOf(s *selfservice.Submission) (submitRows, error) {
	rows := submitRows{last: nil, req: s.Request, rev: s.Revision, supersedes: s.Supersedes != 0}

	if err := checkSubmission(s); err != nil {
		return rows, err
	}

	have, found := m.requests[rows.req.Ref]

	switch {
	case found && have.Version != rows.req.Version, !found && rows.req.Version != 0:
		return rows, fmt.Errorf("%w: %s", selfservice.ErrConflict, rows.req.Ref)
	case !found:
		rows.req.ID = ""

		return rows, nil
	default:
	}

	last := m.revisions[selfservice.RevisionKey(have.ID, have.Last)]
	if err := checkLast(s, &last); err != nil {
		return rows, err
	}

	if len(have.QuotaKeys) == 0 {
		have.QuotaKeys = rows.req.QuotaKeys
	}

	rows.req, rows.last = have, &last

	return rows, nil
}

// supersede gives the reservation of a superseded create to the new
// revision, and moves its end time to the new deadline.
func (m *memDB) supersede(ctx context.Context, tx *memQuotaTx, s *selfservice.Submission, rows *submitRows) error {
	last, rev := rows.last, &rows.rev
	if !rows.supersedes || last == nil || last.Operation != selfservice.OperationCreate ||
		rev.Operation != selfservice.OperationCreate || last.Reservation == "" {
		return nil
	}

	if s.Quota != nil && s.Quota.Reservation != nil {
		return fmt.Errorf("%w: a supersede moves the reservation of revision %d, so the submit holds none",
			selfservice.ErrInvalid, last.Number)
	}

	if err := tx.SetEnd(ctx, last.Reservation, rev.Deadline); err != nil {
		return fmt.Errorf("supersede: %w", err)
	}

	rev.Reservation = last.Reservation

	return nil
}

// requestID returns the ID of req, or the ID that commit gives a new one.
func (m *memDB) requestID(req *selfservice.Request) selfservice.RequestID {
	if req.ID != "" {
		return req.ID
	}

	return selfservice.RequestID(selfservice.RequestIDPrefix + strconv.Itoa(m.nextID+1))
}

// commit writes the rows of a submit that passed each check, the units and
// the counter q.
func (m *memDB) commit(rows *submitRows, units map[string]ownedUnit, q quotaState) {
	req, rev := &rows.req, &rows.rev

	if req.ID == "" {
		req.ID = m.requestID(req)
		m.nextID++
		req.CreatedAt = time.Now().UTC()
	}

	rev.Request = req.ID
	rev.Number = req.Last + 1
	rev.State = selfservice.StateAccepted
	rev.CreatedAt = time.Now().UTC()
	req.Last = rev.Number
	req.Version++
	req.UpdatedAt = rev.CreatedAt

	if rows.last != nil && rows.supersedes {
		rows.last.State, rows.last.UpdatedAt = selfservice.StateSuperseded, rev.CreatedAt
		m.revisions[rows.last.Key()] = *rows.last
	}

	m.units = units
	m.quota = q
	m.requests[req.Ref] = *req
	m.revisions[rev.Key()] = *rev

	if rev.Origin == selfservice.OriginAPI {
		m.operations[rev.Key()] = selfservice.KindStart
	}
}

func (m *memDB) changeVersion(ref selfservice.BlueprintRef, n int, fence namespace.Fence, e []audit.Entry,
	f func(v *selfservice.Version),
) error {
	if err := checkRef(ref, n, fence, e); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkFence(fence); err != nil {
		return err
	}

	vs := m.versions[ref]
	if n < 1 || n > len(vs) {
		return fmt.Errorf("%w: %s version %d", selfservice.ErrNotFound, ref, n)
	}

	f(&vs[n-1])
	m.audit[fence.Namespace] = append(m.audit[fence.Namespace], e...)

	return nil
}

// apply writes c to req and rev. The caller holds the mutex and checked the
// transition.
func (m *memDB) apply(req *selfservice.Request, rev *selfservice.Revision, c selfservice.RevisionChange) {
	now := time.Now().UTC()

	if c.To != rev.State && c.To.Terminal() && c.To != selfservice.StateSuperseded &&
		rev.Operation == selfservice.OperationCreate {
		m.operations[selfservice.QuotaKey(rev.Key())] = selfservice.KindQuota
	}

	rev.State = c.To
	rev.UpdatedAt = now

	if c.Job != "" {
		rev.Job, rev.JobVersion = c.Job, c.JobVersion
	}

	if c.Reason != "" || c.Failures != nil {
		rev.Reason, rev.Failures = c.Reason, c.Failures
	}

	if c.Unit.IsUnit() {
		req.Unit = c.Unit
	}

	if c.To == selfservice.StateApplied {
		req.Applied = rev.Number

		req.Ensure = rev.Document.Ensure
		if req.Ensure == "" {
			req.Ensure = selfservice.EnsurePresent
		}
	}

	req.Version++
	req.UpdatedAt = now
	m.requests[req.Ref] = *req
	m.revisions[rev.Key()] = *rev
}

func (m *memDB) checkFence(fence namespace.Fence) error {
	if m.fences[fence.Namespace] != fence.FailoverVersion {
		return fmt.Errorf("self-service: %w", namespace.ErrFenced)
	}

	return nil
}

func noRevision() selfservice.Revision {
	var r selfservice.Revision

	return r
}

func compare[T ~string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func TestMemConformance(t *testing.T) {
	t.Parallel()

	selfservicetest.Run(t, func(_ *testing.T) selfservicetest.Harness {
		db := newMemDB()

		return selfservicetest.Harness{Requests: db, Blueprints: db, Domain: db}
	})
}

func TestMemUnitTxConformance(t *testing.T) {
	t.Parallel()

	selfservicetest.RunUnitTx(t, func(_ *testing.T) selfservicetest.UnitTxHarness { return newMemDB() })
}
