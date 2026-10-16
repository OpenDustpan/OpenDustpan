package tempadmin_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// baseTime is the fixed clock of the fakes, in UTC to the millisecond.
func baseTime() time.Time {
	return time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
}

// fakeStore is an in-memory tempadmin.Store that records the audit entries
// of its writes. Like the SQL adapters it keeps an expired record as a
// tombstone and ends it only once.
type fakeStore struct {
	records  map[string]tempadmin.TempAdmin
	disabled map[string]bool
	entries  []audit.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: map[string]tempadmin.TempAdmin{}, disabled: map[string]bool{}, entries: nil}
}

func (f *fakeStore) CreateTempAdmin(
	_ context.Context, r tempadmin.TempAdmin, e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return err
	}

	if _, dup := f.records[r.PrincipalID]; dup {
		return tempadmin.ErrAlreadyExists
	}

	f.records[r.PrincipalID] = r
	f.entries = append(f.entries, e)

	return nil
}

func (f *fakeStore) GetTempAdmin(_ context.Context, id string) (tempadmin.TempAdmin, error) {
	r, ok := f.records[id]
	if !ok {
		return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
	}

	return r, nil
}

func (f *fakeStore) GetTempAdminBySubject(_ context.Context, subject string) (tempadmin.TempAdmin, error) {
	for _, r := range f.records {
		if r.Subject == subject {
			return r, nil
		}
	}

	return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
}

func (f *fakeStore) ListTempAdmins(_ context.Context) ([]tempadmin.TempAdmin, error) {
	out := make([]tempadmin.TempAdmin, 0, len(f.records))

	for _, r := range f.records {
		out = append(out, r)
	}

	slices.SortFunc(out, func(a, b tempadmin.TempAdmin) int {
		return a.ExpiresAt.Compare(b.ExpiresAt)
	})

	return out, nil
}

func (f *fakeStore) ExpireTempAdmin(_ context.Context, id string, e audit.Entry) error {
	if _, ok := f.records[id]; !ok {
		return tempadmin.ErrNotFound
	}

	if f.disabled[id] {
		return tempadmin.ErrExpired
	}

	f.disabled[id] = true
	f.entries = append(f.entries, e)

	return nil
}

// fakeAudit is an audit.ServerStore that keeps the entries it appends.
type fakeAudit struct {
	entries []audit.Entry
}

func (f *fakeAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

func newFakeAudit() *fakeAudit {
	return &fakeAudit{entries: nil}
}

// fakeAuthn resolves one fixed principal for the token "good".
type fakeAuthn struct {
	principal identity.Principal
}

func (f fakeAuthn) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != "good" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return f.principal, nil
}

// testService returns a Service over a fresh fake store with a fixed clock.
func testService(store *fakeStore) *tempadmin.Service {
	svc, err := tempadmin.NewService(tempadmin.Config{Store: store, Now: baseTime})
	if err != nil {
		panic(err)
	}

	return svc
}

// callerEntry is the audit entry of the host operator for a create.
func callerEntry() audit.Entry {
	return audit.Entry{
		At:         time.Time{},
		Namespace:  "",
		Subject:    "host",
		IDPSubject: "",
		TokenID:    "",
		Action:     "",
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

func TestCreateSetsAFixedExpiry(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host"}

	got, err := svc.Create(t.Context(), callerEntry(), in)
	if err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	if !got.ExpiresAt.Equal(baseTime().Add(tempadmin.Lifetime)) {
		t.Fatalf("expiry = %s, want %s", got.ExpiresAt, baseTime().Add(tempadmin.Lifetime))
	}

	if !got.ActiveAt(baseTime()) {
		t.Fatal("record is not active at its creation")
	}

	if got.ActiveAt(got.ExpiresAt) {
		t.Fatal("record is still active at its expiry")
	}

	if len(store.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 for the create", len(store.entries))
	}

	e := store.entries[0]

	if e.Action != tempadmin.Action || e.Object != tempadmin.Ref("prn-alice") || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("audit entry = %+v, want a success of %s on %s", e, tempadmin.Action, tempadmin.Ref("prn-alice"))
	}
}

func TestCreateRejectsABadSubject(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	for _, tc := range []struct {
		name  string
		input tempadmin.Input
	}{
		{name: "empty subject", input: tempadmin.Input{Subject: "", PrincipalID: "prn-a", CreatedBy: "host"}},
		{
			name:  "bootstrap subject",
			input: tempadmin.Input{Subject: "admin", PrincipalID: "prn-a", CreatedBy: "host"},
		},
		{name: "missing principal", input: tempadmin.Input{Subject: "alice", PrincipalID: "", CreatedBy: "host"}},
		{name: "missing creator", input: tempadmin.Input{Subject: "alice", PrincipalID: "prn-a", CreatedBy: ""}},
	} {
		_, err := svc.Create(t.Context(), callerEntry(), tc.input)
		if err == nil {
			t.Fatalf("%s: create succeeded, want an error", tc.name)
		}
	}

	if len(store.entries) != 0 {
		t.Fatalf("audit entries = %d, want none after refused creates", len(store.entries))
	}
}

func TestSweepDisablesOnlyExpiredRecords(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	seedSweepRecords(t, svc)
	requireSweepBeforeExpiryDisablesNothing(t, svc)
	later := sweepAfterExpiry(t, store)
	requireTombstonesKept(t, store)
	requireSecondSweepDisablesNothing(t, store, later)
}

// seedSweepRecords creates the alice and bob records of the sweep test.
func seedSweepRecords(t *testing.T, svc *tempadmin.Service) {
	t.Helper()

	for _, tc := range []struct{ id, subject string }{
		{id: "prn-alice", subject: "alice"},
		{id: "prn-bob", subject: "bob"},
	} {
		createSweepRecord(t, svc, tc.id, tc.subject)
	}
}

// createSweepRecord creates one sweep record for subject with id.
func createSweepRecord(t *testing.T, svc *tempadmin.Service, id, subject string) {
	t.Helper()

	in := tempadmin.Input{Subject: subject, PrincipalID: id, CreatedBy: "host"}

	if _, err := svc.Create(t.Context(), callerEntry(), in); err != nil {
		t.Fatalf("create %s: %v", subject, err)
	}
}

// requireSweepBeforeExpiryDisablesNothing proves a sweep before the expiry
// disables nothing and writes nothing.
func requireSweepBeforeExpiryDisablesNothing(t *testing.T, svc *tempadmin.Service) {
	t.Helper()

	done, err := svc.Sweep(t.Context(), callerEntry())
	if err != nil {
		t.Fatalf("sweep before expiry: %v", err)
	}

	if len(done) != 0 {
		t.Fatalf("sweep disabled %v before the expiry, want none", done)
	}
}

// sweepAfterExpiry sweeps past the expiry and proves both records end with
// one audit entry for each expiry. It answers the late service.
func sweepAfterExpiry(t *testing.T, store *fakeStore) *tempadmin.Service {
	t.Helper()

	later := testServiceAt(store, baseTime().Add(tempadmin.Lifetime).Add(time.Minute))

	done, err := later.Sweep(t.Context(), callerEntry())
	if err != nil {
		t.Fatalf("sweep after expiry: %v", err)
	}

	if len(done) != 2 {
		t.Fatalf("sweep disabled %v, want both records", done)
	}

	if len(store.entries) != 4 {
		t.Fatalf("audit entries = %d, want 2 creates and 2 expiries", len(store.entries))
	}

	requireExpirySubjects(t, store)

	return later
}

// requireExpirySubjects proves each expiry entry carries the sweep subject.
func requireExpirySubjects(t *testing.T, store *fakeStore) {
	t.Helper()

	for i := range store.entries[2:] {
		e := store.entries[2:][i]

		if e.Subject != tempadmin.SweepSubject {
			t.Fatalf("expiry subject = %q, want %q", e.Subject, tempadmin.SweepSubject)
		}
	}
}

// requireTombstonesKept proves the sweep kept both records as tombstones.
func requireTombstonesKept(t *testing.T, store *fakeStore) {
	t.Helper()

	rest, err := store.ListTempAdmins(t.Context())
	if err != nil {
		t.Fatalf("list after sweep: %v", err)
	}

	if len(rest) != 2 {
		t.Fatalf("records after sweep = %d, want both tombstones", len(rest))
	}
}

// requireSecondSweepDisablesNothing proves a second sweep ends nothing
// again and writes no second entry.
func requireSecondSweepDisablesNothing(t *testing.T, store *fakeStore, later *tempadmin.Service) {
	t.Helper()

	again, err := later.Sweep(t.Context(), callerEntry())
	if err != nil {
		t.Fatalf("second sweep after expiry: %v", err)
	}

	if len(again) != 0 {
		t.Fatalf("second sweep disabled %v, want none", again)
	}

	if len(store.entries) != 4 {
		t.Fatalf("audit entries = %d, want no second expiry entry", len(store.entries))
	}
}

// testServiceAt returns a Service over store with the clock fixed at now.
func testServiceAt(store *fakeStore, now time.Time) *tempadmin.Service {
	svc, err := tempadmin.NewService(tempadmin.Config{Store: store, Now: func() time.Time { return now }})
	if err != nil {
		panic(err)
	}

	return svc
}

// signInDecorator returns a Decorator over store that resolves caller for
// the token "good", with a clock one hour after the create.
func signInDecorator(
	t *testing.T, store *fakeStore, audits *fakeAudit, caller identity.Principal,
) *tempadmin.Decorator {
	t.Helper()

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next:    fakeAuthn{principal: caller},
		Records: store,
		Expirer: store,
		Audit:   audits,
		Now:     func() time.Time { return baseTime().Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("new decorator: %v", err)
	}

	return dec
}

// checkSignedIn proves one use carries the banner flag with the fixed
// expiry.
func checkSignedIn(t *testing.T, dec *tempadmin.Decorator, i int) {
	t.Helper()

	got, err := dec.AuthenticateToken(t.Context(), "good")
	if err != nil {
		t.Fatalf("sign-in %d: %v", i, err)
	}

	if !got.TemporaryAdministrator {
		t.Fatalf("sign-in %d: flag is false, want true for the banner", i)
	}

	if !got.TemporaryExpiresAt.Equal(baseTime().Add(tempadmin.Lifetime)) {
		t.Fatalf("sign-in %d: expiry = %s, want the fixed expiry", i, got.TemporaryExpiresAt)
	}
}

func TestDecoratorSetsTheFlagAndWritesASignInEntry(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host"}

	_, err := svc.Create(t.Context(), callerEntry(), in)
	if err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	audits := newFakeAudit()

	caller := identity.Principal{
		Subject:                "alice",
		DisplayName:            "alice",
		Admin:                  false,
		TokenID:                "tok-alice",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
	}

	dec := signInDecorator(t, store, audits, caller)

	for i := range 2 {
		checkSignedIn(t, dec, i)
	}

	if len(audits.entries) != 2 {
		t.Fatalf("sign-in entries = %d, want one for each sign-in", len(audits.entries))
	}

	for _, e := range audits.entries {
		if e.Subject != "alice" || e.TokenID != "tok-alice" || e.Action != tempadmin.Action ||
			e.Object != tempadmin.Ref("prn-alice") {
			t.Fatalf("sign-in entry = %+v, want the subject, the token and the record", e)
		}
	}
}

// TestDecoratorWritesOneSignInEntryPerRequest proves the request log and
// the adapter resolve the token twice but write one entry: the second use
// in the scope of one request carries the flag with no second entry. The
// entry holds the client address and the request ID of the request.
func TestDecoratorWritesOneSignInEntryPerRequest(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host"}

	if _, err := svc.Create(t.Context(), callerEntry(), in); err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	audits := newFakeAudit()

	caller := identity.Principal{
		Subject:                "alice",
		DisplayName:            "alice",
		Admin:                  false,
		TokenID:                "tok-alice",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
	}

	dec := signInDecorator(t, store, audits, caller)

	ctx := tempadmin.WithSignInScope(t.Context())
	ctx = clientip.WithAddr(ctx, netip.MustParseAddr("192.0.2.7"))
	ctx = requestid.WithContext(ctx, "req-1")

	for i := range 2 {
		checkSignedInWith(ctx, t, dec, i)
	}

	if len(audits.entries) != 1 {
		t.Fatalf("sign-in entries = %d, want one for the request", len(audits.entries))
	}

	e := audits.entries[0]

	if e.SourceIP != "192.0.2.7" || e.RequestID != "req-1" {
		t.Fatalf("sign-in entry = %+v, want the client address and the request ID", e)
	}

	if e.Subject != "alice" || e.Outcome != audit.OutcomeSuccess {
		t.Fatalf("sign-in entry = %+v, want the subject with a success outcome", e)
	}
}

// checkSignedInWith proves one use in ctx carries the banner flag with the
// fixed expiry.
func checkSignedInWith(ctx context.Context, t *testing.T, dec *tempadmin.Decorator, i int) {
	t.Helper()

	got, err := dec.AuthenticateToken(ctx, "good")
	if err != nil {
		t.Fatalf("sign-in %d: %v", i, err)
	}

	if !got.TemporaryAdministrator {
		t.Fatalf("sign-in %d: flag is false, want true for the banner", i)
	}

	if !got.TemporaryExpiresAt.Equal(baseTime().Add(tempadmin.Lifetime)) {
		t.Fatalf("sign-in %d: expiry = %s, want the fixed expiry", i, got.TemporaryExpiresAt)
	}
}

func TestDecoratorLeavesOtherCallersAlone(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	audits := newFakeAudit()

	newDecorator := func(p identity.Principal) *tempadmin.Decorator {
		dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
			Next:    fakeAuthn{principal: p},
			Records: store,
			Expirer: store,
			Audit:   audits,
			Now:     nil,
		})
		if err != nil {
			t.Fatalf("new decorator: %v", err)
		}

		return dec
	}

	// A bootstrap administrator passes through with no flag and no entry.
	admin, err := newDecorator(identity.Principal{
		Subject:                "admin",
		DisplayName:            "admin",
		Admin:                  true,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
	}).AuthenticateToken(t.Context(), "good")
	if err != nil {
		t.Fatalf("bootstrap sign-in: %v", err)
	}

	if admin.TemporaryAdministrator {
		t.Fatal("bootstrap administrator carries the flag")
	}

	// A person with no record passes through with no flag and no entry.
	bob, err := newDecorator(identity.Principal{
		Subject:                "bob",
		DisplayName:            "bob",
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
	}).AuthenticateToken(t.Context(), "good")
	if err != nil {
		t.Fatalf("ordinary sign-in: %v", err)
	}

	if bob.TemporaryAdministrator {
		t.Fatal("ordinary person carries the flag")
	}

	if len(audits.entries) != 0 {
		t.Fatalf("sign-in entries = %d, want none", len(audits.entries))
	}
}

func TestDecoratorDeniesAnExpiredRecordAndDisablesIt(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host"}

	rec, err := svc.Create(t.Context(), callerEntry(), in)
	if err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	audits := newFakeAudit()

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next: fakeAuthn{principal: identity.Principal{
			Subject:                "alice",
			DisplayName:            "alice",
			Admin:                  false,
			TokenID:                "tok-alice",
			TokenExpiresAt:         time.Time{},
			TemporaryAdministrator: false,
			TemporaryExpiresAt:     time.Time{},
		}},
		Records: store,
		Expirer: store,
		Audit:   audits,
		Now:     func() time.Time { return rec.ExpiresAt },
	})
	if err != nil {
		t.Fatalf("new decorator: %v", err)
	}

	_, err = dec.AuthenticateToken(t.Context(), "good")
	if !errors.Is(err, identity.ErrUnauthenticated) || !errors.Is(err, tempadmin.ErrExpired) {
		t.Fatalf("expired sign-in = %v, want unauthenticated wrapping expired", err)
	}

	rec, err = store.GetTempAdmin(t.Context(), "prn-alice")
	if err != nil {
		t.Fatalf("record after expiry = %v, want the tombstone", err)
	}

	if rec.ActiveAt(baseTime().Add(tempadmin.Lifetime)) {
		t.Fatal("tombstone is still active after its expiry")
	}

	if len(store.entries) != 2 {
		t.Fatalf("store entries = %d, want the create and the expiry", len(store.entries))
	}

	if len(audits.entries) != 0 {
		t.Fatalf("sign-in entries = %d, want none for a denied use", len(audits.entries))
	}

	// A later use is still denied and writes no second expiry entry.
	_, err = dec.AuthenticateToken(t.Context(), "good")
	if !errors.Is(err, identity.ErrUnauthenticated) || !errors.Is(err, tempadmin.ErrExpired) {
		t.Fatalf("second expired sign-in = %v, want unauthenticated wrapping expired", err)
	}

	if len(store.entries) != 2 {
		t.Fatalf("store entries = %d, want no second expiry entry", len(store.entries))
	}
}

func TestDecoratorKeepsBootstrapSubjectsVisible(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	audits := newFakeAudit()

	inner, err := identity.NewStaticTokens("test-token-0123456789abcdef0123456789")
	if err != nil {
		t.Fatalf("bootstrap tokens: %v", err)
	}

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next:    inner,
		Records: store,
		Expirer: store,
		Audit:   audits,
		Now:     nil,
	})
	if err != nil {
		t.Fatalf("new decorator: %v", err)
	}

	named, ok := any(dec).(interface{ AdminSubjects() []string })
	if !ok {
		t.Fatal("decorator names no bootstrap subjects")
	}

	if got := named.AdminSubjects(); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("subjects = %v, want [admin]", got)
	}
}

func TestValidateSubjectKeepsBootstrapNamesOut(t *testing.T) {
	t.Parallel()

	r := tempadmin.TempAdmin{
		CreatedAt:   baseTime(),
		ExpiresAt:   baseTime().Add(tempadmin.Lifetime),
		PrincipalID: "prn-alice",
		Subject:     "alice",
		CreatedBy:   "host",
	}

	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	r.Subject = "admin-2"

	if err := r.Validate(); !errors.Is(err, principal.ErrReservedSubject) {
		t.Fatalf("validate of a bootstrap name = %v, want ErrReservedSubject", err)
	}
}
