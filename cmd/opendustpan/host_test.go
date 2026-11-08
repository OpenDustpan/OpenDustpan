package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// hostTestDB returns the SQLite database URL of a fresh, migrated database
// in a temporary directory, as serve leaves it: the host command applies no
// migration.
func hostTestDB(t *testing.T) string {
	t.Helper()

	db := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	st, err := openStore(t.Context(), db, slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("migrate the test database: %v", err)
	}

	st.close()

	return db
}

// runHost runs the host command with args and returns its standard output.
// It proves that the command writes the review note to standard error.
func runHost(ctx context.Context, t *testing.T, args ...string) string {
	t.Helper()

	var stdout, stderr bytes.Buffer

	if err := run(ctx, args, &stdout, &stderr); err != nil {
		t.Fatalf("host %s: %v (%s)", strings.Join(args, " "), err, stderr.String())
	}

	if stderr.String() != hostReviewNote+"\n" {
		t.Fatalf("host stderr = %q, want the review note line", stderr.String())
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

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler), openMigrate, "")
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

	creds := parseHostOutput(
		t,
		runHost(ctx, t, "host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice"),
	)

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

	runHost(ctx, t, "host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice")

	var stdout, stderr bytes.Buffer

	args := []string{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice"}

	err := run(ctx, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("second host run succeeded, want a refusal")
	}

	if !strings.Contains(err.Error(), `person "alice" already exists`) ||
		!strings.Contains(err.Error(), "opendustpan host create-temp-admin --reason <text> <person>") {
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
		Reach: access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil},
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil, Logger: nil,
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
		{"host", "create-temp-admin", "--reason", "test recovery", "--database", db},
		{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice", "bob"},
		{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "admin"},
		{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "admin-2"},
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

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil},
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		st.close()
		t.Fatalf("access token service: %v", err)
	}

	off := true

	change := accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: &off,
		ExchangeLifetime: nil,
	}

	_, setErr := tokens.SetPolicy(ctx, hostPolicyEntry(), change)
	if !errors.Is(setErr, accesstoken.ErrInvalid) {
		st.close()
		t.Fatalf("set break-glass disable = %v, want ErrInvalid", setErr)
	}

	st.close()

	creds := parseHostOutput(
		t,
		runHost(ctx, t, "host", "create-temp-admin", "--reason", "test recovery", "--database", db, "bob"),
	)

	if creds.person != "bob" {
		t.Fatalf("person = %q, want bob", creds.person)
	}

	checkSt := openHostStore(ctx, t, db)

	checkTokens, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{
			Grants:      nil,
			Roles:       nil,
			Callers:     nil,
			Principals:  nil,
			Collections: nil,
			Units:       nil,
		},
		Store:      checkSt,
		Principals: checkSt,
		TempAdmins: checkSt,
		Freshness:  nil,
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
		FenceVersion:  0,
		At:            time.Time{},
		Namespace:     "",
		Subject:       tempadmin.HostSubject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        "server:manage-token-policy",
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          "",
		Outcome:       "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
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

	creds := parseHostOutput(
		t,
		runHost(ctx, t, "host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice"),
	)

	requireHostExpiryAbout(t, creds.expires, time.Hour)

	st := openHostStore(ctx, t, db)

	requireHostRecordExpiry(ctx, t, st, creds)
	checkHostToken(ctx, t, st, creds.token, "alice")
}

// setHostMaxLifetime sets the token policy maximum lifetime of the database
// db to capped.
func setHostMaxLifetime(ctx context.Context, t *testing.T, db string, capped time.Duration) {
	t.Helper()

	st, err := openStore(ctx, db, slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer st.close()

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil},
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil, Logger: nil,
		Now: nil, NewID: nil,
	})
	if err != nil {
		t.Fatalf("access token service: %v", err)
	}

	change := accesstoken.PolicyChange{
		MaxLifetime: &capped, Inactivity: nil, DisableTokens: nil, DisableAccounts: nil, DisableBreakGlass: nil,
		ExchangeLifetime: nil,
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

	args := []string{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice"}

	err := run(ctx, args, &stdout, &stderr)
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

// failRecoveryStore makes the token of each host recovery outlive its
// record, so the store refuses the token after it wrote the person, the
// grant and the record in the same transaction.
type failRecoveryStore struct {
	store
}

// CreateHostRecovery implements accesstoken.HostStore with a token that
// the store refuses.
func (f failRecoveryStore) CreateHostRecovery(ctx context.Context, r *accesstoken.HostRecovery) error {
	r.Token.ExpiresAt = r.Record.ExpiresAt.Add(time.Millisecond)

	return f.store.CreateHostRecovery(ctx, r)
}

// errExpiryRefused is the failure of failSweepStore.
var errExpiryRefused = errors.New("expiry refused")

// failSweepStore lists one ended record that it cannot expire, so each sweep
// ends with ErrSweepIncomplete.
type failSweepStore struct {
	store
}

// ListTempAdmins implements tempadmin.Store with one record past its expiry.
func (failSweepStore) ListTempAdmins(context.Context) ([]tempadmin.TempAdmin, error) {
	return []tempadmin.TempAdmin{{
		CreatedAt:      time.UnixMilli(1),
		ExpiresAt:      time.UnixMilli(2),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    "prn_ended",
		Subject:        "ended",
		CreatedBy:      "test",
		Reason:         "test",
	}}, nil
}

// ExpireTempAdmin implements tempadmin.Store with a failure.
func (failSweepStore) ExpireTempAdmin(context.Context, string, audit.Entry) error {
	return errExpiryRefused
}

// TestHostIncompleteSweepWarns proves that a sweep which does not end every
// record writes one line to standard error and does not stop the create.
func TestHostIncompleteSweepWarns(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	var stderr bytes.Buffer

	made, err := createTempAdmin(ctx, failSweepStore{store: st}, aliceInput(), &stderr)
	if err != nil {
		t.Fatalf("create with an incomplete sweep: %v", err)
	}

	if made.subject != "alice" || made.secret == "" {
		t.Fatalf("create = %+v, want a person and a secret", made)
	}

	if got := stderr.String(); got != hostSweepNote+"\n" {
		t.Fatalf("standard error = %q, want %q", got, hostSweepNote+"\n")
	}
}

// TestHostFailureLeavesNothing proves that a failure at the token step
// rolls back the person, the grant and the record of the same transaction,
// so a retry with the same name succeeds.
func TestHostFailureLeavesNothing(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	st, err := openStore(ctx, hostTestDB(t), slog.New(slog.DiscardHandler), openMigrate, "")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(st.close)

	if _, err := createTempAdmin(ctx, failRecoveryStore{store: st}, aliceInput(), io.Discard); !errors.Is(
		err, accesstoken.ErrNotOwnToken,
	) {
		t.Fatalf("host run with a refused token = %v, want ErrNotOwnToken", err)
	}

	requireHostCleanup(ctx, t, st)
	requireHostRetry(ctx, t, st)
}

// aliceInput is the host input of the person alice.
func aliceInput() hostInput {
	return hostInput{person: "alice", reason: "every administrator lost access"}
}

// grantLister lists the grants on an object. Both SQL adapters implement
// it.
type grantLister interface {
	ListGrantsOn(ctx context.Context, obj identity.Object, q access.GrantQuery) ([]access.ListedGrant, int64, error)
}

// requireHostCleanup proves the failed run left no person, no live grant
// and no record.
func requireHostCleanup(ctx context.Context, t *testing.T, st store) {
	t.Helper()

	if _, err := st.GetPrincipalBySubject(ctx, "alice"); !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("principal after a failed run = %v, want ErrNotFound", err)
	}

	lister, ok := st.(grantLister)
	if !ok {
		t.Fatal("the store lists no grants")
	}

	listed, total, err := lister.ListGrantsOn(ctx, identity.ServerObject(), access.GrantQuery{
		AfterSubject: "", AfterRole: "", Subject: "", Role: "", Limit: 100,
	})
	if err != nil {
		t.Fatalf("list grants after a failed run: %v", err)
	}

	if total != 0 || len(listed) != 0 {
		t.Fatalf("grants after a failed run = %+v, want none live", listed)
	}

	if list, err := st.ListTempAdmins(ctx); err != nil || len(list) != 0 {
		t.Fatalf("records after a failed run = %+v, %v; want none", list, err)
	}
}

// requireHostRetry proves a retry with the same name succeeds and signs in.
func requireHostRetry(ctx context.Context, t *testing.T, st store) {
	t.Helper()

	made, err := createTempAdmin(ctx, st, aliceInput(), io.Discard)
	if err != nil {
		t.Fatalf("retry after a failed run: %v", err)
	}

	if made.subject != "alice" || made.secret == "" || made.expires.IsZero() {
		t.Fatalf("retry = %+v, want a person, a secret and an expiry", made)
	}

	tokens, err := accesstoken.NewService(accesstoken.Config{
		Reach: access.ReachConfig{Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil},
		Store: st, Principals: st, TempAdmins: st, Freshness: nil, Authorizer: nil, Logger: nil,
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

// TestHostRecoveryOff proves that the off switch takes true and 1, keeps
// the command on for false, 0, an empty value and no value, and refuses
// any other value.
func TestHostRecoveryOff(t *testing.T) {
	t.Parallel()

	const key = "DUSTPAN_DISABLE_HOST_RECOVERY"

	for value, want := range map[string]error{
		"true": errHostOff, "TRUE": errHostOff, "1": errHostOff,
		"": nil, "false": nil, "0": nil, "yes": cli.ErrBadBool, "on": cli.ErrBadBool,
	} {
		got := hostAllowed(envOf(map[string]string{key: value}), modeBootstrap)
		if !errors.Is(got, want) || (want == nil && got != nil) {
			t.Errorf("off switch %q: err = %v, want %v", value, got, want)
		}
	}

	if err := hostAllowed(envOf(nil), modeBootstrap); err != nil {
		t.Errorf("no off switch: err = %v, want nil", err)
	}
}

// TestHostRecoveryModes proves that the command runs in the modes
// bootstrap and signed-request, and refuses the mode none and an unknown
// mode.
func TestHostRecoveryModes(t *testing.T) {
	t.Parallel()

	for mode, want := range map[string]error{
		modeBootstrap: nil, modeSignedRequest: nil, modeNone: errHostModeNone, "token": errHostBadMode,
	} {
		got := hostAllowed(envOf(nil), mode)
		if !errors.Is(got, want) || (want == nil && got != nil) {
			t.Errorf("mode %q: err = %v, want %v", mode, got, want)
		}
	}
}

// TestHostOffOpensNoStore runs the command end to end with the off switch,
// with the mode none, and with a bad switch value, and proves that each run
// refuses before it opens a store.
func TestHostOffOpensNoStore(t *testing.T) { //nolint:paralleltest // It swaps the package opener.
	saved := openStore

	t.Cleanup(func() { openStore = saved })

	openStore = func(context.Context, string, *slog.Logger, openMode, installation.Kind) (store, error) {
		t.Fatal("the host command opened a store")

		return nil, errHostOff
	}

	cases := []struct {
		want error
		env  map[string]string
		args []string
	}{
		{want: errHostOff, env: map[string]string{"DUSTPAN_DISABLE_HOST_RECOVERY": "1"}, args: nil},
		{want: cli.ErrBadBool, env: map[string]string{"DUSTPAN_DISABLE_HOST_RECOVERY": "maybe"}, args: nil},
		{want: errHostModeNone, env: nil, args: []string{"--recovery-mode", modeNone}},
	}

	for _, c := range cases {
		var stdout, stderr bytes.Buffer

		args := append(append([]string{"--reason", "lost access"}, c.args...), "alice")
		in := hostIO{stdin: strings.NewReader(""), terminal: false, lookup: envOf(c.env)}

		if err := hostCreateTempAdminWithIO(t.Context(), args, in, &stdout, &stderr); !errors.Is(err, c.want) {
			t.Fatalf("host run %v with %v: err = %v, want %v", args, c.env, err, c.want)
		}
	}
}

// TestHostReason proves that the command needs a reason: from the flag, or
// from the prompt on a terminal, and refuses a run with neither.
func TestHostReason(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	noTerminal := hostIO{stdin: strings.NewReader("ignored\n"), terminal: false, lookup: envOf(nil)}
	if _, err := hostReason("", noTerminal, &stderr); !errors.Is(err, errHostNeedsReason) {
		t.Fatalf("no reason and no terminal: err = %v, want errHostNeedsReason", err)
	}

	terminal := hostIO{stdin: strings.NewReader("  the last administrator left\nmore\n"), terminal: true, lookup: nil}

	got, err := hostReason("", terminal, &stderr)
	if err != nil || got != "the last administrator left" {
		t.Fatalf("prompt reason = %q, %v; want the typed line", got, err)
	}

	if !strings.Contains(stderr.String(), "Reason") {
		t.Fatalf("prompt = %q, want a reason prompt", stderr.String())
	}

	if got, err := hostReason(" from the flag ", noTerminal, &stderr); err != nil || got != "from the flag" {
		t.Fatalf("flag reason = %q, %v; want the flag value", got, err)
	}
}

// TestHostWritesTheReviewNote proves that a host run writes the one line
// that says no break-glass review opens, on standard error and not on
// standard output.
func TestHostWritesTheReviewNote(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	args := []string{"host", "create-temp-admin", "--reason", "test recovery", "--database", hostTestDB(t), "alice"}

	if err := run(t.Context(), args, &stdout, &stderr); err != nil {
		t.Fatalf("host run: %v", err)
	}

	const want = "note: no break-glass review opens for this temporary administrator yet: " +
		"review its audit entries by hand after the expiry\n"

	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}

	if strings.Contains(stdout.String(), "note:") {
		t.Fatalf("stdout = %q, want no note", stdout.String())
	}
}

// TestHostRefusesPendingMigrations proves that the host command applies no
// migration: on a database with a pending migration it refuses, names the
// command that applies them, and leaves the schema as it was.
func TestHostRefusesPendingMigrations(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	file := filepath.Join(t.TempDir(), "dustpan.db")
	db := "sqlite:" + file

	raw, openErr := sql.Open("sqlite", file)
	if openErr != nil {
		t.Fatalf("open raw database: %v", openErr)
	}

	_, execErr := raw.ExecContext(ctx,
		`CREATE TABLE schema_migrations (version INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL) STRICT`)
	if execErr != nil {
		t.Fatalf("create schema_migrations: %v", execErr)
	}

	if closeErr := raw.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	var stdout, stderr bytes.Buffer

	args := []string{"host", "create-temp-admin", "--reason", "test recovery", "--database", db, "alice"}

	err := run(ctx, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("host run on an empty database succeeded, want a refusal")
	}

	if !errors.Is(err, errHostMigrationsPending) || !errors.Is(err, schema.ErrMigrationsPending) {
		t.Fatalf("host run on an empty database = %v, want errHostMigrationsPending", err)
	}

	if !strings.Contains(err.Error(), "opendustpan serve --database "+db) {
		t.Fatalf("refusal = %v, want the serve command that applies the migrations", err)
	}

	st := openHostStore(ctx, t, db)

	if list, listErr := st.ListTempAdmins(ctx); listErr != nil || len(list) != 0 {
		t.Fatalf("records after the refusal = %+v, %v; want none", list, listErr)
	}
}

// TestHostReadsTheModeThroughLookup proves that the command reads
// DUSTPAN_RECOVERY_MODE through its environment lookup, as it reads the off
// switch, and refuses the mode none before it opens a store.
func TestHostReadsTheModeThroughLookup(t *testing.T) { //nolint:paralleltest // It swaps the package opener.
	saved := openStore

	t.Cleanup(func() { openStore = saved })

	openStore = func(context.Context, string, *slog.Logger, openMode, installation.Kind) (store, error) {
		t.Fatal("the host command opened a store")

		return nil, errHostModeNone
	}

	var stdout, stderr bytes.Buffer

	in := hostIO{
		stdin: strings.NewReader(""), terminal: false,
		lookup: envOf(map[string]string{"DUSTPAN_RECOVERY_MODE": modeNone}),
	}

	err := hostCreateTempAdminWithIO(t.Context(), []string{"--reason", "lost access", "alice"}, in, &stdout, &stderr)
	if !errors.Is(err, errHostModeNone) {
		t.Fatalf("host run with DUSTPAN_RECOVERY_MODE=none: err = %v, want errHostModeNone", err)
	}
}
