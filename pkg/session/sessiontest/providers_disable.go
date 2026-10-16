package sessiontest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
)

// testProviderDisableEndsSessions proves that the update that disables a
// provider ends every active session of a principal with a subject of the
// provider, in the same write, and records the count in its entry. An
// update that keeps the provider enabled ends nothing, and the sessions of
// another provider with the same start stay active.
func testProviderDisableEndsSessions(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	corp := newProviderConfig("corp")
	mustCreateProvider(t, s, corp)

	mustSubjectPrincipal(t, s, "prn-alice", "corp:alice")
	mustSubjectPrincipal(t, s, "prn-carol", "corp:carol")
	mustSubjectPrincipal(t, s, "prn-bob", "corpx:bob")

	mustSession(t, s, newSession("ses-alice1", "prn-alice", at(1)), digest(1))
	mustSession(t, s, newSession("ses-alice2", "prn-alice", at(2)), digest(2))
	mustSession(t, s, newSession("ses-carol", "prn-carol", at(2)), digest(3))
	mustSession(t, s, newSession("ses-bob", "prn-bob", at(2)), digest(4))

	if err := s.EndSession(ctx, "ses-carol", session.ReasonSignOut, entry("ses-carol", at(3))); err != nil {
		t.Fatalf("sign out carol: %v", err)
	}

	corp.Version, corp.UpdatedAt, corp.DisplayName = 2, at(6), "Corp SSO"
	if err := s.UpdateProviderConfig(ctx, corp, providerEntry("corp", at(6))); err != nil {
		t.Fatalf("rename: %v", err)
	}

	checkSessionActive(t, s, "ses-alice1", "ses-alice2", "ses-bob")

	corp.Version, corp.UpdatedAt, corp.Enabled = 3, at(7), false
	if err := s.UpdateProviderConfig(ctx, corp, providerEntry("corp", at(7))); err != nil {
		t.Fatalf("disable: %v", err)
	}

	for _, id := range []string{"ses-alice1", "ses-alice2"} {
		got := mustGetSession(t, s, id)
		if !got.EndedAt.Equal(at(7)) || got.EndReason != session.ReasonEnded {
			t.Fatalf("session %s after the disable = ended %v, reason %q; want ended at %v by an administrator",
				id, got.EndedAt, got.EndReason, at(7))
		}
	}

	carol := mustGetSession(t, s, "ses-carol")
	if !carol.EndedAt.Equal(at(3)) || carol.EndReason != session.ReasonSignOut {
		t.Fatalf("ended session ses-carol = ended %v, reason %q; want its sign-out", carol.EndedAt, carol.EndReason)
	}

	checkSessionActive(t, s, "ses-bob")
	checkSessionsEndedEntry(t, s)
}

// mustGetSession returns the session id.
func mustGetSession(t *testing.T, s Store, id string) session.Session {
	t.Helper()

	got, err := s.GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}

	return got
}

// checkSessionActive checks that each session of ids has no end.
func checkSessionActive(t *testing.T, s Store, ids ...string) {
	t.Helper()

	for _, id := range ids {
		if got := mustGetSession(t, s, id); !got.EndedAt.IsZero() || got.EndReason != "" {
			t.Fatalf("session %s = ended %v, reason %q; want active", id, got.EndedAt, got.EndReason)
		}
	}
}

// checkSessionsEndedEntry checks that the entry of the disable, and only
// that entry, records two ended sessions. A store with no audit log to
// read checks nothing here.
func checkSessionsEndedEntry(t *testing.T, s Store) {
	t.Helper()

	entries, ok := s.(auditLister)
	if !ok {
		return
	}

	listing, err := entries.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var counts []string

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Object != session.ProviderRef("corp") {
			continue
		}

		for _, c := range e.Changes {
			if c.Key == session.ChangeSessionsEnded {
				counts = append(counts, e.At.String()+" "+c.New)
			}
		}
	}

	if want := at(7).String() + " 2"; len(counts) != 1 || counts[0] != want {
		t.Fatalf("sessionsEnded changes = %q, want one: %q", counts, want)
	}
}
