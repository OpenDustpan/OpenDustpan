package trigger_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
)

const (
	acme  namespace.Name = "acme"
	ada                  = "user:ada"
	grace                = "user:grace"
)

// person returns a principal of a person with the subject s.
func person(s string) identity.Principal {
	return identity.Principal{
		Subject: s, DisplayName: "", Admin: false, TokenID: "tok-" + s, TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// fakeResolver answers the saver subjects it knows.
type fakeResolver struct {
	people map[string]identity.Principal
	errs   map[string]error
}

func newResolver() *fakeResolver {
	return &fakeResolver{people: map[string]identity.Principal{}, errs: map[string]error{}}
}

func (r *fakeResolver) Saver(_ context.Context, subject string) (identity.Principal, error) {
	if err := r.errs[subject]; err != nil {
		return identity.Principal{}, err
	}

	p, ok := r.people[subject]
	if !ok {
		return identity.Principal{}, trigger.ErrSaverGone
	}

	return p, nil
}

// fakeAuthz allows the triples in allow, and every action for an administrator.
// A call that is not allowed returns identity.ErrDenied, or the error in fail.
type fakeAuthz struct {
	mu    sync.Mutex
	allow map[string]bool
	fail  map[string]error
	asked []string
}

func newAuthz() *fakeAuthz {
	return &fakeAuthz{mu: sync.Mutex{}, allow: map[string]bool{}, fail: map[string]error{}, asked: nil}
}

// grant allows subject to do the actions on the unit with the ID id.
func (a *fakeAuthz) grant(subject, id string, actions ...identity.Action) {
	for _, action := range actions {
		a.allow[subject+" "+string(action)+" "+id] = true
	}
}

func (a *fakeAuthz) Authorize(_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	key := p.Subject + " " + string(action) + " " + obj.UnitName()
	a.asked = append(a.asked, key)

	if err := a.fail[key]; err != nil {
		return err
	}

	if p.Admin || a.allow[key] {
		return nil
	}

	return fmt.Errorf("%w: %s", identity.ErrDenied, key)
}

func (*fakeAuthz) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}, nil
}

// fakeUnits knows the units whose ID is not in gone.
type fakeUnits struct {
	gone map[string]bool
}

func newUnits() *fakeUnits { return &fakeUnits{gone: map[string]bool{}} }

func (u *fakeUnits) Object(_ context.Context, unit graph.Unit) (identity.Object, error) {
	if u.gone[unit.ID] {
		return identity.Object{}, trigger.ErrUnitGone
	}

	obj, err := identity.UnitObject(unit.Tool, unit.Namespace, unit.ID)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %w", trigger.ErrUnitGone, err)
	}

	return obj.WithUnitID(unit.ID), nil
}

func (u *fakeUnits) Find(_ context.Context, ns namespace.Name, unitID string) (graph.Unit, error) {
	if u.gone[unitID] {
		return graph.Unit{}, trigger.ErrUnitGone
	}

	return graph.Unit{Namespace: ns, Tool: "opentofu", ID: unitID}, nil
}

// fakeFences answers one fence for every namespace.
type fakeFences struct{ err error }

func (f *fakeFences) Fence(_ context.Context, ns namespace.Name) (namespace.Fence, error) {
	if f.err != nil {
		return namespace.Fence{}, f.err
	}

	return namespace.Fence{Namespace: ns, FailoverVersion: 1}, nil
}

// putCall is one call of fakeStore.Put or Delete.
type putCall struct {
	trigger trigger.Trigger
	entry   audit.Entry
	deleted bool
}

// fakeStore is a minimal trigger.Store. It applies the cycle rule, like an
// adapter.
type fakeStore struct {
	mu       sync.Mutex
	triggers map[trigger.ID]trigger.Trigger
	calls    []putCall
	putErr   error
}

func newStore() *fakeStore {
	return &fakeStore{mu: sync.Mutex{}, triggers: map[trigger.ID]trigger.Trigger{}, calls: nil, putErr: nil}
}

func (s *fakeStore) Put(_ context.Context, t trigger.Trigger, _ namespace.Fence, e audit.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.putErr != nil {
		return s.putErr
	}

	if old, ok := s.triggers[t.ID]; ok && (old.Unit != t.Unit || old.Kind != t.Kind) {
		return fmt.Errorf("%w: a trigger keeps its unit and its kind", trigger.ErrInvalid)
	}

	if err := trigger.CheckCycle(s.all(), t); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	s.triggers[t.ID] = t
	s.calls = append(s.calls, putCall{trigger: t, entry: e, deleted: false})

	return nil
}

func (s *fakeStore) all() []trigger.Trigger {
	out := make([]trigger.Trigger, 0, len(s.triggers))
	for _, t := range s.triggers {
		out = append(out, t)
	}

	slices.SortFunc(out, func(a, b trigger.Trigger) int {
		if a.ID < b.ID {
			return -1
		}

		return 1
	})

	return out
}

func (s *fakeStore) Get(_ context.Context, ns namespace.Name, id trigger.ID) (trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.triggers[id]
	if !ok || t.Unit.Namespace != ns {
		return trigger.Trigger{}, trigger.ErrNotFound
	}

	return t, nil
}

func (s *fakeStore) List(_ context.Context, unit graph.Unit) ([]trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []trigger.Trigger

	for _, t := range s.all() {
		if t.Unit == unit {
			out = append(out, t)
		}
	}

	return out, nil
}

func (s *fakeStore) Followers(_ context.Context, source graph.Unit) ([]trigger.Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []trigger.Trigger

	for _, t := range s.all() {
		if t.Kind == trigger.KindFollows && t.Enabled && t.Source == source {
			out = append(out, t)
		}
	}

	return out, nil
}

func (s *fakeStore) Delete(_ context.Context, _ namespace.Name, id trigger.ID, _ namespace.Fence, e audit.Entry,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.triggers[id]
	if !ok {
		return trigger.ErrNotFound
	}

	delete(s.triggers, id)

	s.calls = append(s.calls, putCall{trigger: t, entry: e, deleted: true})

	return nil
}

func (s *fakeStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}

// fakeSaverCheck answers err for every check.
type fakeSaverCheck struct {
	err     error
	saves   int
	started int
}

func (c *fakeSaverCheck) CheckSave(context.Context, trigger.Trigger) error {
	c.saves++

	return c.err
}

func (c *fakeSaverCheck) CheckStart(context.Context, trigger.Trigger) error {
	c.started++

	return c.err
}

// fakeDispatcher records the manual fires.
type fakeDispatcher struct {
	actors []job.Actor
	reqs   []trigger.ManualRequest
	err    error
}

func (*fakeDispatcher) Scheduled(context.Context, namespace.Name, trigger.ID, time.Time) (job.ID, error) {
	return "", errors.New("not used")
}

func (*fakeDispatcher) Upstream(context.Context, trigger.Applied) ([]trigger.Outcome, error) {
	return nil, errors.New("not used")
}

func (d *fakeDispatcher) Manual(_ context.Context, actor job.Actor, req trigger.ManualRequest) (job.ID, error) {
	d.actors = append(d.actors, actor)
	d.reqs = append(d.reqs, req)

	if d.err != nil {
		return "", d.err
	}

	return "job-1", nil
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Errorf("%s: %v, want no error", what, err)
		}

		return
	}

	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}
