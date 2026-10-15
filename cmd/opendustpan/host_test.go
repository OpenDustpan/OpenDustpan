package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// hostTestDB returns a fresh SQLite database URL in a temporary directory.
func hostTestDB(t *testing.T) string {
	t.Helper()

	return "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")
}

// runHost runs the host command with args and returns its standard output.
func runHost(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()

	var stdout, stderr bytes.Buffer

	if err := run(ctx, args, &stdout, &stderr); err != nil {
		t.Fatalf("host %s: %v (%s)", strings.Join(args, " "), err, stderr.String())
	}

	return stdout.String()
}

// hostCredentials holds the person, the token and the expiry of host
// output.
type hostCredentials struct {
	person  string
	token   string
	expires time.Time
}

// parseHostOutput reads the person, the token and the expiry of host
// output.
func parseHostOutput(t *testing.T, out string) hostCredentials {
	t.Helper()

	var creds hostCredentials

	for line := range strings.Lines(out) {
		name, value, _ := strings.Cut(line, ":")

		switch strings.TrimSpace(name) {
		case "person":
			creds.person = strings.TrimSpace(value)
		case "token":
			creds.token = strings.TrimSpace(value)
		case "expires":
			at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
			if err != nil {
				t.Fatalf("parse expiry %q: %v", value, err)
			}

			creds.expires = at
		default:
			continue
		}
	}

	if creds.person == "" || creds.token == "" || creds.expires.IsZero() {
		t.Fatalf("host output = %q, want person, token and expires lines", out)
	}

	return creds
}

// openHostStore reopens the SQLite database of a host run for the checks.
func openHostStore(ctx context.Context, t *testing.T, db string) *sqlite.Store {
	t.Helper()

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}

	wrapped, ok := st.(sqliteStore)
	if !ok {
		t.Fatalf("store of %q is no SQLite store", db)
	}

	t.Cleanup(func() {
		if err := wrapped.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return wrapped.Store
}

// TestHostCreateTempAdmin makes a temporary administrator for one named
// person through the database only: no listener serves. It proves the
// fixed expiry, the banner record, the usable token, and one audit entry
// for each step, then proves that a second run issues a new token for the
// time that is left.
func TestHostCreateTempAdmin(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	creds := parseHostOutput(t, runHost(ctx, t, "host", "create-temp-admin", "--database", db, "alice"))

	checkHostExpiry(t, creds)

	st := openHostStore(ctx, t, db)

	p := checkHostRecords(ctx, t, st, creds)
	checkHostToken(ctx, t, st, creds.token, "alice")
	checkHostAudit(ctx, t, st, p)

	again := parseHostOutput(t, runHost(ctx, t, "host", "create-temp-admin", "--database", db, "alice"))

	if again.token == creds.token {
		t.Fatal("second run issued the same secret, want a new token")
	}

	if !again.expires.Equal(creds.expires) {
		t.Fatalf("second expiry = %s, want the fixed %s", again.expires, creds.expires)
	}

	checkHostToken(ctx, t, st, again.token, "alice")
}

// checkHostExpiry proves the printed expiry is the fixed lifetime away.
func checkHostExpiry(t *testing.T, creds hostCredentials) {
	t.Helper()

	if creds.person != "alice" {
		t.Fatalf("person = %q, want alice", creds.person)
	}

	if got := time.Until(creds.expires); got <= 23*time.Hour || got > tempadmin.Lifetime {
		t.Fatalf("expiry is in %s, want about %s", got, tempadmin.Lifetime)
	}
}

// checkHostRecords proves the banner record and the person principal, with
// the printed expiry.
func checkHostRecords(
	ctx context.Context, t *testing.T, st *sqlite.Store, creds hostCredentials,
) principal.Principal {
	t.Helper()

	rec, err := st.GetTempAdminBySubject(ctx, "alice")
	if err != nil {
		t.Fatalf("read temporary administrator: %v", err)
	}

	if !rec.ExpiresAt.Equal(creds.expires) {
		t.Fatalf("record expiry = %s, want the printed %s", rec.ExpiresAt, creds.expires)
	}

	p, err := st.GetPrincipalBySubject(ctx, "alice")
	if err != nil {
		t.Fatalf("read principal: %v", err)
	}

	if p.Kind != principal.KindPerson || !p.DisabledAt.IsZero() {
		t.Fatalf("principal = %+v, want an enabled person", p)
	}

	return p
}

// checkHostToken proves the secret signs in as the person.
func checkHostToken(ctx context.Context, t *testing.T, st *sqlite.Store, token, person string) {
	t.Helper()

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	got, err := tokens.AuthenticateToken(ctx, token)
	if err != nil {
		t.Fatalf("authenticate the host token: %v", err)
	}

	if got.Subject != person {
		t.Fatalf("token subject = %q, want %s", got.Subject, person)
	}
}

// checkHostAudit proves one audit entry for each step: the principal and
// the banner record.
func checkHostAudit(ctx context.Context, t *testing.T, st *sqlite.Store, p principal.Principal) {
	t.Helper()

	entries := hostServerEntries(ctx, t, st)

	for _, want := range []string{principal.Ref(p.ID), tempadmin.Ref(p.ID)} {
		found := false

		for i := range entries {
			if entries[i].Object == want {
				found = true

				break
			}
		}

		if !found {
			t.Fatalf("no audit entry names %q in %+v", want, entries)
		}
	}
}

// hostServerEntries returns the entries of @server, newest first.
func hostServerEntries(ctx context.Context, t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	listing, err := st.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	return listing.Entries
}

// TestHostNeedsAPerson refuses a host run without exactly one person name,
// and a run for a bootstrap name.
func TestHostNeedsAPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	for _, args := range [][]string{
		{"host", "create-temp-admin", "--database", db},
		{"host", "create-temp-admin", "--database", db, "alice", "bob"},
		{"host", "create-temp-admin", "--database", db, "admin"},
		{"host", "create-temp-admin", "--database", db, "admin-2"},
	} {
		var stdout, stderr bytes.Buffer

		if err := run(ctx, args, &stdout, &stderr); err == nil {
			t.Fatalf("host %s succeeded, want an error", strings.Join(args, " "))
		}
	}
}

// TestHostTokenSurvivesBreakGlassOff proves the host path works with the
// break-glass disable on: the policy switch never gates recovery.
func TestHostTokenSurvivesBreakGlassOff(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	st := openHostStore(ctx, t, db)

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	off := true

	change := accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: &off,
	}

	if _, err := tokens.SetPolicy(ctx, hostPolicyEntry(), change); err != nil {
		t.Fatalf("turn break-glass off: %v", err)
	}

	creds := parseHostOutput(t, runHost(ctx, t, "host", "create-temp-admin", "--database", db, "bob"))

	if creds.person != "bob" {
		t.Fatalf("person = %q, want bob", creds.person)
	}

	if _, err := tokens.AuthenticateToken(ctx, creds.token); err != nil {
		t.Fatalf("authenticate with break-glass off: %v", err)
	}
}

// hostPolicyEntry is the audit entry of a policy change in these tests.
func hostPolicyEntry() audit.Entry {
	return audit.Entry{
		At:         time.Time{},
		Namespace:  "",
		Subject:    "host",
		IDPSubject: "",
		TokenID:    "",
		Action:     "server:manage-token-policy",
		Object:     "",
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes:    nil,
		SourceIP:   "",
		UserAgent:  "",
		Channel:    audit.ChannelAPI,
		Kind:       "",
		Outcome:    "",
		RequestID:  "",
		ObjectID:   "",
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
	}
}

// TestHostReadFailsForUnknownSubject proves GetTempAdminBySubject answers
// ErrNotFound for a stranger: the decorator passes ordinary callers
// through on it.
func TestHostReadFailsForUnknownSubject(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	st := openHostStore(ctx, t, hostTestDB(t))

	if _, err := st.GetTempAdminBySubject(ctx, "stranger"); !errors.Is(err, tempadmin.ErrNotFound) {
		t.Fatalf("read of a stranger = %v, want ErrNotFound", err)
	}

	if _, err := st.GetPrincipalBySubject(ctx, "stranger"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("principal read of a stranger = %v, want ErrNotFound", err)
	}
}
