package gate_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// memBreakGlassStore is the in-memory BreakGlassStore of the tests.
type memBreakGlassStore struct {
	mu       sync.Mutex
	passes   []memPass
	consumes atomic.Int64
	failWith error
}

func newMemBreakGlassStore() *memBreakGlassStore {
	return &memBreakGlassStore{mu: sync.Mutex{}, passes: nil, consumes: atomic.Int64{}, failWith: nil}
}

type memPass struct {
	pass gate.BreakGlassPass
	used bool
}

func (m *memBreakGlassStore) Issue(_ context.Context, pass gate.BreakGlassPass) error {
	if pass.Key() == (gate.BreakGlassKey{Holder: "", UnitID: "", Kind: ""}) {
		return gate.ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.passes = append(m.passes, memPass{pass: pass, used: false})

	return nil
}

func (m *memBreakGlassStore) Consume(_ context.Context, key gate.BreakGlassKey, now time.Time) error {
	m.consumes.Add(1)

	if m.failWith != nil {
		return m.failWith
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.passes {
		p := &m.passes[i]
		if !p.used && p.pass.Key() == key && p.pass.ValidAt(now) {
			p.used = true

			return nil
		}
	}

	return gate.ErrNotFound
}

// unused counts the passes that no Consume used.
func (m *memBreakGlassStore) unused() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for _, p := range m.passes {
		if !p.used {
			n++
		}
	}

	return n
}

// bgUnit returns the unit "name" of acme with the stable ID id.
func bgUnit(t *testing.T, name, id string) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", "acme", name)
	if err != nil {
		t.Fatal(err)
	}

	return u.WithUnitID(id)
}

// bgPass makes a pass and fails the test on an error.
func bgPass(
	t *testing.T, holder string, kind gate.OperationKind, unit identity.Object, at time.Time,
) gate.BreakGlassPass {
	t.Helper()

	p, err := gate.NewBreakGlassPass("incident 42: the gate text is broken", kind, holder, unit, at)
	if err != nil {
		t.Fatalf("NewBreakGlassPass: %v", err)
	}

	return p
}

// bgSuiteT0 is the time of the issue in the suite.
func bgSuiteT0() time.Time { return time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC) }

// bgSuiteKey is the key of the pass that the suite issues.
func bgSuiteKey() gate.BreakGlassKey {
	return gate.BreakGlassKey{Holder: "alice", UnitID: "ws-test-web", Kind: gate.OperationApply}
}

// bgSuiteIssue issues the pass of the suite into s.
func bgSuiteIssue(t *testing.T, s gate.BreakGlassStore) {
	t.Helper()

	pass := bgPass(t, "alice", gate.OperationApply, bgUnit(t, "web", "ws-test-web"), bgSuiteT0())
	if err := s.Issue(t.Context(), pass); err != nil {
		t.Fatal(err)
	}
}

// runBreakGlassStoreSuite is the conformance suite of gate.BreakGlassStore.
// A follow-up adapter runs it with a function that makes an empty store.
func runBreakGlassStoreSuite(t *testing.T, newStore func(t *testing.T) gate.BreakGlassStore) {
	t.Helper()

	for name, f := range map[string]func(*testing.T, func(*testing.T) gate.BreakGlassStore){
		"a pass is used once":                        suiteUsedOnce,
		"the window is 15 minutes":                   suiteWindow,
		"the whole key matches":                      suiteWholeKey,
		"one winner of concurrent calls":             suiteConcurrent,
		"a pass that no constructor made is refused": suiteZeroPass,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f(t, newStore)
		})
	}
}

func suiteUsedOnce(t *testing.T, newStore func(*testing.T) gate.BreakGlassStore) {
	t.Helper()

	s, at := newStore(t), bgSuiteT0().Add(time.Minute)

	bgSuiteIssue(t, s)

	if err := s.Consume(t.Context(), bgSuiteKey(), at); err != nil {
		t.Fatalf("first Consume = %v, want nil", err)
	}

	if err := s.Consume(t.Context(), bgSuiteKey(), at); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("second Consume = %v, want ErrNotFound", err)
	}
}

func suiteWindow(t *testing.T, newStore func(*testing.T) gate.BreakGlassStore) {
	t.Helper()

	t0 := bgSuiteT0()

	for name, tc := range map[string]struct {
		at   time.Time
		want error
	}{
		"before the issue":      {t0.Add(-time.Second), gate.ErrNotFound},
		"at the issue":          {t0, nil},
		"just before the end":   {t0.Add(gate.BreakGlassPassWindow - time.Nanosecond), nil},
		"at the end":            {t0.Add(gate.BreakGlassPassWindow), gate.ErrNotFound},
		"long after the window": {t0.Add(24 * time.Hour), gate.ErrNotFound},
	} {
		s := newStore(t)

		bgSuiteIssue(t, s)

		err := s.Consume(t.Context(), bgSuiteKey(), tc.at)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: Consume = %v, want %v", name, err, tc.want)
		}
	}
}

func suiteWholeKey(t *testing.T, newStore func(*testing.T) gate.BreakGlassStore) {
	t.Helper()

	s, at := newStore(t), bgSuiteT0().Add(time.Minute)

	bgSuiteIssue(t, s)

	for name, k := range map[string]gate.BreakGlassKey{
		"another holder":        {Holder: "bob", UnitID: "ws-test-web", Kind: gate.OperationApply},
		"another unit":          {Holder: "alice", UnitID: "ws-test-other", Kind: gate.OperationApply},
		"another kind":          {Holder: "alice", UnitID: "ws-test-web", Kind: gate.OperationDestroy},
		"a prefix of the unit":  {Holder: "alice", UnitID: "ws-test-we", Kind: gate.OperationApply},
		"a name for the unit":   {Holder: "alice", UnitID: "web", Kind: gate.OperationApply},
		"a prefix of the owner": {Holder: "alic", UnitID: "ws-test-web", Kind: gate.OperationApply},
		"the empty key":         {Holder: "", UnitID: "", Kind: ""},
	} {
		if err := s.Consume(t.Context(), k, at); !errors.Is(err, gate.ErrNotFound) {
			t.Errorf("%s: Consume = %v, want ErrNotFound", name, err)
		}
	}

	if err := s.Consume(t.Context(), bgSuiteKey(), at); err != nil {
		t.Fatalf("the pass is still valid after each foreign key: Consume = %v", err)
	}
}

func suiteConcurrent(t *testing.T, newStore func(*testing.T) gate.BreakGlassStore) {
	t.Helper()

	var (
		wg   sync.WaitGroup
		wins atomic.Int64
	)

	s, at := newStore(t), bgSuiteT0().Add(time.Minute)

	bgSuiteIssue(t, s)

	for range 16 {
		wg.Go(func() {
			if s.Consume(t.Context(), bgSuiteKey(), at) == nil {
				wins.Add(1)
			}
		})
	}

	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("winners = %d, want 1", got)
	}
}

func suiteZeroPass(t *testing.T, newStore func(*testing.T) gate.BreakGlassStore) {
	t.Helper()

	if err := newStore(t).Issue(t.Context(), gate.BreakGlassPass{}); !errors.Is(err, gate.ErrInvalid) {
		t.Fatalf("Issue(zero pass) = %v, want ErrInvalid", err)
	}
}

func TestMemBreakGlassStoreConformance(t *testing.T) {
	t.Parallel()

	runBreakGlassStoreSuite(t, func(*testing.T) gate.BreakGlassStore { return newMemBreakGlassStore() })
}
