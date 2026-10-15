package session_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

func TestLimitsValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limits session.Limits
		ok     bool
	}{
		{name: "defaults", limits: session.DefaultLimits(), ok: true},
		{name: "shortest", limits: session.Limits{Idle: session.MinIdle, MaxLength: session.MinIdle}, ok: true},
		{name: "longest", limits: session.Limits{Idle: time.Hour, MaxLength: session.MaxLengthBound}, ok: true},
		{name: "idle equals maximum", limits: session.Limits{Idle: time.Hour, MaxLength: time.Hour}, ok: true},
		{name: "idle too short", limits: session.Limits{Idle: time.Minute, MaxLength: time.Hour}, ok: false},
		{name: "idle after maximum", limits: session.Limits{Idle: 2 * time.Hour, MaxLength: time.Hour}, ok: false},
		{name: "maximum too short", limits: session.Limits{Idle: time.Minute, MaxLength: time.Minute}, ok: false},
		{
			name:   "maximum too long",
			limits: session.Limits{Idle: time.Hour, MaxLength: session.MaxLengthBound + time.Second},
			ok:     false,
		},
		{
			name:   "part of a second",
			limits: session.Limits{Idle: session.MinIdle + time.Millisecond, MaxLength: time.Hour},
			ok:     false,
		},
		{name: "zero", limits: session.Limits{Idle: 0, MaxLength: 0}, ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.limits.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate(%+v) = %v, want nil", tc.limits, err)
			}

			if !tc.ok && !errors.Is(err, session.ErrInvalid) {
				t.Fatalf("Validate(%+v) = %v, want ErrInvalid", tc.limits, err)
			}
		})
	}
}

// memoryPolicies is a PolicyStore in memory. It keeps the entries of the
// changes that it stores.
type memoryPolicies struct {
	policy  session.Policy
	entries []audit.Entry
}

func (m *memoryPolicies) GetSessionPolicy(context.Context) (session.Policy, error) {
	return m.policy, nil
}

func (m *memoryPolicies) UpdateSessionPolicy(_ context.Context, update session.PolicyUpdate) (session.Policy, error) {
	p, e, err := update(m.policy)
	if err != nil {
		return session.Policy{}, err
	}

	m.policy = p
	m.entries = append(m.entries, e)

	return p, nil
}

func TestNewPoliciesNeedsAStore(t *testing.T) {
	t.Parallel()

	_, err := session.NewPolicies(session.PolicyConfig{Store: nil, Now: nil})
	if !errors.Is(err, session.ErrMissingDependency) {
		t.Fatalf("NewPolicies with no store = %v, want ErrMissingDependency", err)
	}
}

func TestSetPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 15, 9, 0, 0, 0, time.UTC)
	store := &memoryPolicies{policy: session.DefaultPolicy(), entries: nil}

	svc, err := session.NewPolicies(session.PolicyConfig{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("new policies: %v", err)
	}

	idle := 30 * time.Minute
	unchanged := session.DefaultMaxLength

	got, err := svc.SetPolicy(t.Context(), callerEntry(), session.PolicyChange{
		Idle: &idle, MaxLength: &unchanged,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	want := session.Policy{UpdatedAt: now, Limits: session.Limits{Idle: idle, MaxLength: session.DefaultMaxLength}}
	if got != want {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}

	if len(store.entries) != 1 {
		t.Fatalf("%d entries, want 1", len(store.entries))
	}

	e := store.entries[0]
	if e.Subject != "alice" || !e.At.Equal(now) || e.Namespace != audit.ServerScope ||
		e.Object != session.PolicyRef || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("entry = %+v, want the change of %s by alice in @server", e, session.PolicyRef)
	}

	before := "900"
	wantChanges := []audit.Change{{Key: "idleSeconds", Old: &before, New: "1800", Layer: "", TakesEffect: ""}}

	if !slices.EqualFunc(e.Changes, wantChanges, equalChange) {
		t.Fatalf("changes = %+v, want only the idle limit from 900 to 1800", e.Changes)
	}
}

func TestSetPolicyRefusesInvalidLimits(t *testing.T) {
	t.Parallel()

	store := &memoryPolicies{policy: session.DefaultPolicy(), entries: nil}

	svc, err := session.NewPolicies(session.PolicyConfig{Store: store, Now: nil})
	if err != nil {
		t.Fatalf("new policies: %v", err)
	}

	short := 10 * time.Minute

	_, err = svc.SetPolicy(t.Context(), callerEntry(), session.PolicyChange{
		Idle: nil, MaxLength: &short,
	})
	if !errors.Is(err, session.ErrInvalid) {
		t.Fatalf("a maximum length under the idle limit = %v, want ErrInvalid", err)
	}

	if store.policy != session.DefaultPolicy() || len(store.entries) != 0 {
		t.Fatalf("store = %+v, want the defaults and no entry", store)
	}
}

func equalChange(a, b audit.Change) bool {
	return a.Key == b.Key && a.New == b.New && (a.Old == nil) == (b.Old == nil) &&
		(a.Old == nil || *a.Old == *b.Old) && a.Layer == b.Layer && a.TakesEffect == b.TakesEffect
}

// callerEntry returns the audit entry of a caller as the route builds it:
// who acted, with the time, the scope and the record left to the service.
func callerEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "",
		Action: "server:manage-sign-in", Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
	}
}
