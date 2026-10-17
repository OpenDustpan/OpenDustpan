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

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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
// for each step.
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
}

// TestHostRefusesAnExistingPerson refuses a host run for a person that a
// principal already holds, with an error that names the person and the one
// command that helps. Reusing the principal would end a normal account at
// the expiry of the record.
func TestHostRefusesAnExistingPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	runHost(ctx, t, "host", "create-temp-admin", "--database", db, "alice")

	var stdout, stderr bytes.Buffer

	err := run(ctx, []string{"host", "create-temp-admin", "--database", db, "alice"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("second host run succeeded, want a refusal")
	}

	if !strings.Contains(err.Error(), `person "alice" already exists`) ||
		!strings.Contains(err.Error(), "opendustpan host create-temp-admin <person>") {
		t.Fatalf("refusal = %v, want the person and the command that helps", err)
	}
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

	if rec.CreatedBy == "" {
		t.Fatal("temporary administrator names no creator, want the OS user of the host run")
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

// checkHostAudit proves one audit entry for each step: the person create,
// the site-administrator grant, the token, and the banner record. A
// missing or repeated step fails the test.
func checkHostAudit(ctx context.Context, t *testing.T, st *sqlite.Store, p principal.Principal) {
	t.Helper()

	entries := hostServerEntries(ctx, t, st)
	counts := countHostActions(entries)

	for _, want := range []string{
		string(identity.ActionServerManagePrincipals),
		string(identity.ActionServerManageAccess),
		string(identity.ActionServerManageTokens),
		tempadmin.Action,
	} {
		if counts[want] != 1 {
			t.Fatalf("audit entries with action %q = %d, want exactly 1 in %+v", want, counts[want], counts)
		}
	}

	requireHostObject(t, entries, string(identity.ActionServerManagePrincipals), principal.Ref(p.ID))
	requireHostObject(t, entries, tempadmin.Action, tempadmin.Ref(p.ID))
}

// requireHostObject proves the one entry with the given action names the
// object object.
func requireHostObject(t *testing.T, entries []audit.Entry, action, object string) {
	t.Helper()

	for i := range entries {
		if entries[i].Action == action && entries[i].Object == object {
			return
		}
	}

	t.Fatalf("no audit entry with action %q names %q in %+v", action, object, entries)
}

// countHostActions counts the entries by action.
func countHostActions(entries []audit.Entry) map[string]int {
	counts := make(map[string]int, len(entries))

	for i := range entries {
		counts[entries[i].Action]++
	}

	return counts
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

// TestHostWorksWhileBreakGlassDisableIsRefused proves the host path works
// while the break-glass disable stays refused: turning the switch on
// answers ErrBreakGlassUnavailable, and the host recovery still signs in.
func TestHostWorksWhileBreakGlassDisableIsRefused(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		st.close()
		t.Fatalf("access token service: %v", err)
	}

	off := true

	change := accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: &off,
	}

	_, setErr := tokens.SetPolicy(ctx, hostPolicyEntry(), change)
	if !errors.Is(setErr, accesstoken.ErrInvalid) {
		st.close()
		t.Fatalf("set break-glass disable = %v, want ErrInvalid", setErr)
	}

	st.close()

	creds := parseHostOutput(t, runHost(ctx, t, "host", "create-temp-admin", "--database", db, "bob"))

	if creds.person != "bob" {
		t.Fatalf("person = %q, want bob", creds.person)
	}

	checkSt := openHostStore(ctx, t, db)

	checkTokens, err := accesstoken.NewService(accesstoken.Config{
		Store:      checkSt,
		Principals: checkSt,
		TempAdmins: checkSt,
		Freshness:  nil,
		Grants:     nil,
		Authorizer: nil,
		Logger:     nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	if _, err := checkTokens.AuthenticateToken(ctx, creds.token); err != nil {
		t.Fatalf("authenticate with the break-glass disable refused: %v", err)
	}
}

// hostPolicyEntry is the audit entry of a policy change in these tests.
func hostPolicyEntry() audit.Entry {
	return audit.Entry{
		FenceVersion: 0,
		At:           time.Time{},
		Namespace:    "",
		Subject:      tempadmin.HostSubject,
		IDPSubject:   "",
		TokenID:      "",
		Action:       "server:manage-token-policy",
		Object:       "",
		Version:      "",
		ValuePath:    "",
		ValueCount:   nil,
		Changes:      nil,
		SourceIP:     "",
		UserAgent:    "",
		Channel:      audit.ChannelAPI,
		Kind:         "",
		Outcome:      "",
		RequestID:    "",
		ObjectID:     "",
		PrevHash:     "",
		Hash:         "",
		Sequence:     0,
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

// TestHostShortPolicyLifetime proves a token policy with a maximum below
// the fixed lifetime shortens both the token and the record: with a 1h
// maximum the host run succeeds and both expire in 1h, not 24h.
func TestHostShortPolicyLifetime(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	setHostMaxLifetime(ctx, t, db, time.Hour)

	creds := parseHostOutput(t, runHost(ctx, t, "host", "create-temp-admin", "--database", db, "alice"))

	requireHostExpiryAbout(t, creds.expires, time.Hour)

	st := openHostStore(ctx, t, db)

	requireHostRecordExpiry(ctx, t, st, creds)
	checkHostToken(ctx, t, st, creds.token, "alice")
}

// setHostMaxLifetime sets the token policy maximum lifetime of the database
// db to capped.
func setHostMaxLifetime(ctx context.Context, t *testing.T, db string, capped time.Duration) {
	t.Helper()

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer st.close()

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	change := accesstoken.PolicyChange{
		MaxLifetime: &capped, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: nil,
	}

	if _, err := tokens.SetPolicy(ctx, hostPolicyEntry(), change); err != nil {
		t.Fatalf("set maximum lifetime to %s: %v", capped, err)
	}
}

// TestHostRefusesWhenSQLiteFileIsInUse proves the host command against a
// SQLite file held by a running server fails with the file and the one
// step: stop the server that uses this file, then run the command again.
func TestHostRefusesWhenSQLiteFileIsInUse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	db := hostTestDB(t)

	held := openHostStore(ctx, t, db)

	_ = held

	var stdout, stderr bytes.Buffer

	err := run(ctx, []string{"host", "create-temp-admin", "--database", db, "alice"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("host run while the server holds the file succeeded, want a refusal")
	}

	if !errors.Is(err, sqlite.ErrInUse) {
		t.Fatalf("host run while the server holds the file = %v, want ErrInUse", err)
	}

	if !strings.Contains(err.Error(), strings.TrimPrefix(db, "sqlite:")) {
		t.Fatalf("refusal = %v, want it to name the database file", err)
	}

	if !strings.Contains(err.Error(), "stop the server that uses this file") ||
		!strings.Contains(err.Error(), "run the command again") {
		t.Fatalf("refusal = %v, want the stop-the-server step", err)
	}
}

// requireHostExpiryAbout proves expiry is within the hour before want from
// now: after a short policy the host expiry is about want, not 24h.
func requireHostExpiryAbout(t *testing.T, expiry time.Time, want time.Duration) {
	t.Helper()

	got := time.Until(expiry)

	if got <= want-10*time.Minute || got > want {
		t.Fatalf("expiry is in %s, want about %s", got, want)
	}
}

// requireHostRecordExpiry proves the banner record expires with the printed
// token expiry.
func requireHostRecordExpiry(
	ctx context.Context, t *testing.T, st *sqlite.Store, creds hostCredentials,
) {
	t.Helper()

	rec, err := st.GetTempAdminBySubject(ctx, creds.person)
	if err != nil {
		t.Fatalf("read temporary administrator: %v", err)
	}

	if !rec.ExpiresAt.Equal(creds.expires) {
		t.Fatalf("record expiry = %s, want the printed %s", rec.ExpiresAt, creds.expires)
	}
}

// errFailToken reports the injected token failure of the cleanup test.
var errFailToken = errors.New("injected token failure")

// failTokenStore fails the token writes of the store it wraps, so a host
// run fails at the token step after the person, the grant and the record.
type failTokenStore struct {
	store
}

// CreateToken implements accesstoken.TokenStore with an injected failure.
func (f failTokenStore) CreateToken(
	_ context.Context, _ accesstoken.Token, _ accesstoken.Digest, _ audit.Entry,
) error {
	return errFailToken
}

// TestHostTokenFailureCleansUp proves a token failure undoes the earlier
// steps: no person, no live grant and no active record stay, so a retry
// with the same name succeeds.
func TestHostTokenFailureCleansUp(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	if _, err := createTempAdmin(ctx, failTokenStore{store: st}, "alice"); err == nil {
		t.Fatal("host run with a failing token store succeeded, want an error")
	}

	requireHostCleanup(ctx, t, st)
	requireHostRetry(ctx, t, st)
}

// requireHostCleanup proves the failed run left no person, no live grant
// and no usable record: the person is gone, no grant stays live, and any
// record left is an ended tombstone whose principal is gone, so a retry
// with the same name starts clean.
func requireHostCleanup(ctx context.Context, t *testing.T, st store) {
	t.Helper()

	if _, err := st.GetPrincipalBySubject(ctx, "alice"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("principal after a failed run = %v, want ErrNotFound", err)
	}

	listed, total, err := st.ListGrantsOn(ctx, identity.ServerObject(), access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: 100,
	})
	if err != nil {
		t.Fatalf("list grants after a failed run: %v", err)
	}

	if total != 0 || len(listed) != 0 {
		t.Fatalf("grants after a failed run = %+v, want none live", listed)
	}

	requireHostRecordUnusable(ctx, t, st)
}

// requireHostRecordUnusable proves no usable record stays for the failed
// person: either no record, or a tombstone whose principal is gone.
func requireHostRecordUnusable(ctx context.Context, t *testing.T, st store) {
	t.Helper()

	rec, err := st.GetTempAdminBySubject(ctx, "alice")
	if errors.Is(err, tempadmin.ErrNotFound) {
		return
	}

	if err != nil {
		t.Fatalf("record after a failed run = %v, want ErrNotFound or an ended record", err)
	}

	if _, err := st.GetPrincipal(ctx, rec.PrincipalID); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("principal %q of the failed record still exists, want it gone", rec.PrincipalID)
	}
}

// requireHostRetry proves a retry with the same name succeeds after the
// cleanup and signs in.
func requireHostRetry(ctx context.Context, t *testing.T, st store) {
	t.Helper()

	made, err := createTempAdmin(ctx, st, "alice")
	if err != nil {
		t.Fatalf("retry after a failed run: %v", err)
	}

	if made.subject != "alice" || made.secret == "" || made.expires.IsZero() {
		t.Fatalf("retry = %+v, want a person, a secret and an expiry", made)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Grants: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	got, err := tokens.AuthenticateToken(ctx, made.secret)
	if err != nil {
		t.Fatalf("authenticate the retry token: %v", err)
	}

	if got.Subject != "alice" {
		t.Fatalf("retry token subject = %q, want alice", got.Subject)
	}
}

// errFailPerson reports the injected person-removal failure of the cleanup
// test.
var errFailPerson = errors.New("injected person failure")

// failTokenPersonStore fails the token write and the person removal of the
// store it wraps, so a host run fails at the token step and the person
// undo fails too.
type failTokenPersonStore struct {
	store
}

// CreateToken implements accesstoken.TokenStore with an injected failure.
func (f failTokenPersonStore) CreateToken(
	_ context.Context, _ accesstoken.Token, _ accesstoken.Digest, _ audit.Entry,
) error {
	return errFailToken
}

// DeletePrincipal implements principal.RecordStore with an injected
// failure.
func (f failTokenPersonStore) DeletePrincipal(_ context.Context, _ string, _ audit.Entry) error {
	return errFailPerson
}

// TestHostCleanupFailureNamesPerson proves a token failure with a failing
// person undo reports the leftover person: the joined error keeps both the
// token failure and the person that stays.
func TestHostCleanupFailureNamesPerson(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	_, err = createTempAdmin(ctx, failTokenPersonStore{store: st}, "alice")
	if err == nil {
		t.Fatal("host run with failing token and person cleanup succeeded, want an error")
	}

	if !errors.Is(err, errFailToken) {
		t.Fatalf("cleanup failure = %v, want the token failure", err)
	}

	if !errors.Is(err, errFailPerson) {
		t.Fatalf("cleanup failure = %v, want the person cleanup failure", err)
	}

	if !strings.Contains(err.Error(), "alice") || !strings.Contains(err.Error(), "person") {
		t.Fatalf("cleanup failure = %v, want it to name the leftover person", err)
	}

	leftover, getErr := st.GetPrincipalBySubject(ctx, "alice")
	if getErr != nil {
		t.Fatalf("leftover person alice: read = %v, want the person to stay", getErr)
	}

	if !strings.Contains(err.Error(), "DELETE /api/server/principals/"+leftover.ID) {
		t.Fatalf("cleanup failure = %v, want it to name the principal delete route", err)
	}

	if strings.Contains(err.Error(), "host create-temp-admin") {
		t.Fatalf("cleanup failure = %v, want no hint at host create-temp-admin", err)
	}
}

// errFailGrant reports the injected grant-removal failure of the cleanup
// test.
var errFailGrant = errors.New("injected grant failure")

// failTokenGrantStore fails the token write and the grant removal of the
// store it wraps, so a host run fails at the token step and the grant
// undo fails too.
type failTokenGrantStore struct {
	store
}

// CreateToken implements accesstoken.TokenStore with an injected failure.
func (f failTokenGrantStore) CreateToken(
	_ context.Context, _ accesstoken.Token, _ accesstoken.Digest, _ audit.Entry,
) error {
	return errFailToken
}

// RevokeGrant implements access.Store with an injected failure.
func (f failTokenGrantStore) RevokeGrant(
	_ context.Context, _ string, _ namespace.Fence, _ audit.Entry, _ access.GrantCheck,
) error {
	return errFailGrant
}

// TestHostCleanupFailureNamesGrant proves a token failure with a failing
// grant undo reports the leftover grant: the joined error keeps both the
// token failure and the grant that stays, and names the grant removal
// route.
func TestHostCleanupFailureNamesGrant(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	_, err = createTempAdmin(ctx, failTokenGrantStore{store: st}, "alice")
	if err == nil {
		t.Fatal("host run with failing token and grant cleanup succeeded, want an error")
	}

	if !errors.Is(err, errFailToken) {
		t.Fatalf("cleanup failure = %v, want the token failure", err)
	}

	if !errors.Is(err, errFailGrant) {
		t.Fatalf("cleanup failure = %v, want the grant cleanup failure", err)
	}

	if !strings.Contains(err.Error(), "DELETE /api/server/grants/") {
		t.Fatalf("cleanup failure = %v, want it to name the grant removal route", err)
	}

	if strings.Contains(err.Error(), "host create-temp-admin") {
		t.Fatalf("cleanup failure = %v, want no hint at host create-temp-admin", err)
	}
}

// errFailRecord reports the injected record failure of the cleanup test.
var errFailRecord = errors.New("injected record failure")

// failRecordStore fails the temporary administrator record write of the
// store it wraps, so a host run fails at the record step after the person
// and the grant.
type failRecordStore struct {
	store
}

// CreateTempAdmin implements tempadmin.Store with an injected failure.
func (f failRecordStore) CreateTempAdmin(
	_ context.Context, _ tempadmin.TempAdmin, _ audit.Entry,
) error {
	return errFailRecord
}

// TestHostRecordFailureCleansUp proves a record failure undoes the earlier
// steps: no person and no live grant stay, so a retry with the same name
// succeeds.
func TestHostRecordFailureCleansUp(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	if _, err := createTempAdmin(ctx, failRecordStore{store: st}, "alice"); err == nil {
		t.Fatal("host run with a failing record store succeeded, want an error")
	}

	requireHostCleanup(ctx, t, st)
	requireHostRetry(ctx, t, st)
}
