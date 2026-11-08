package casauth_test

import (
	"net/netip"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/casauth"
	"opendustpan.org/opendustpan/internal/castest"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestFinishRefusesAReplayAcrossReplicasAndRestarts is layer 3 of the store:
// a ticket that one method used is refused at a second method that shares the
// database, and at a method over the database after a restart. The CAS server
// would validate the ticket again, so only the durable record refuses it.
func TestFinishRefusesAReplayAcrossReplicasAndRestarts(t *testing.T) {
	t.Parallel()

	now := func() time.Time { return time.UnixMilli(1_800_000_000_000) }
	srv := castest.New(t, castest.Config{Now: now, RedirectTo: "", SlowFor: 0})
	path := filepath.Join(t.TempDir(), "dustpan.db")

	method := func(store session.TicketStore) *casauth.Method {
		tickets, err := session.NewStoreTickets(store, now)
		if err != nil {
			t.Fatalf("NewStoreTickets() = %v", err)
		}

		m, err := casauth.New(baseConfig(srv, tickets, &atomic.Bool{}, now))
		if err != nil {
			t.Fatalf("New() = %v", err)
		}

		t.Cleanup(m.Close)

		return m
	}

	open := func() *sqlite.Store {
		store, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		return store
	}

	first := open()
	s := setup{
		srv: srv, method: method(first), tickets: nil, off: &atomic.Bool{}, now: now,
		opts: options{resolve: netip.Addr{}, groups: false, subjectAttr: false},
	}
	r := s.begin(t, false)
	ticket := srv.Issue(loginQuery(t, r).Get("service"), false, s.person(alice()), castest.AnswerSuccess)
	callback := s.callback(t, r, loginQuery(t, r).Get("service"), ticket)

	_, err := s.method.Finish(t.Context(), callback)
	if err != nil {
		t.Fatalf("first Finish() = %v", err)
	}

	// A second method over the same store, as a second replica.
	a, err := method(first).Finish(t.Context(), callback)
	refused(t, a, err, session.ErrReplay)

	err = first.Close()
	if err != nil {
		t.Fatalf("close store: %v", err)
	}

	// A new store over the same file, as after a restart.
	second := open()

	t.Cleanup(func() {
		if closeErr := second.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	a, err = method(second).Finish(t.Context(), callback)
	refused(t, a, err, session.ErrReplay)

	if n := srv.Validations(); n != 1 {
		t.Fatalf("%d validations, want 1: a replay must not reach the CAS server", n)
	}
}
