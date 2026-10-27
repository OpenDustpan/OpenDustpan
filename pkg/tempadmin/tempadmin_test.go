package tempadmin_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/breakglass"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// baseTime is the fixed clock of the fakes, in UTC to the millisecond.
// testReason is the reason of the records of the tests.
const testReason = "every administrator lost access"

func baseTime() time.Time {
	return time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
}

// fakeStore is an in-memory tempadmin.Store that records the audit entries
// of its writes. Like the SQL adapters it keeps an expired record as a
// tombstone and ends it only once.
type fakeStore struct {
	records  map[string]tempadmin.TempAdmin
	disabled map[string]bool
	// people maps a subject to the ID of its live principal, when it is
	// not the principal of the record of the subject.
	people map[string]string
	// fail maps a principal ID to the error that its expiry returns.
	fail    map[string]error
	entries []audit.Entry
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		records: map[string]tempadmin.TempAdmin{}, disabled: map[string]bool{}, people: map[string]string{},
		fail: map[string]error{}, entries: nil,
	}
}

// GetPrincipalBySubject answers the live principal of subject: the entry
// of people, else the principal of the record of subject.
func (f *fakeStore) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	id, ok := f.people[subject]

	if !ok {
		for key := range f.records {
			if f.records[key].Subject == subject {
				id, ok = f.records[key].PrincipalID, true
			}
		}
	}

	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return principal.Principal{
		CreatedAt: baseTime(), UpdatedAt: baseTime(), DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: subject, Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}, nil
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
	for key := range f.records {
		if f.records[key].Subject == subject {
			return f.records[key], nil
		}
	}

	return tempadmin.TempAdmin{}, tempadmin.ErrNotFound
}

func (f *fakeStore) ListTempAdmins(_ context.Context) ([]tempadmin.TempAdmin, error) {
	out := make([]tempadmin.TempAdmin, 0, len(f.records))

	for key := range f.records {
		out = append(out, f.records[key])
	}

	slices.SortFunc(out, func(a, b tempadmin.TempAdmin) int {
		return a.ExpiresAt.Compare(b.ExpiresAt)
	})

	return out, nil
}

func (f *fakeStore) ExpireTempAdmin(_ context.Context, id string, e audit.Entry) error {
	r, ok := f.records[id]
	if !ok {
		return tempadmin.ErrNotFound
	}

	if err := f.fail[id]; err != nil {
		return err
	}

	if !r.EndedAt.IsZero() {
		return tempadmin.ErrExpired
	}

	r.EndedAt = e.At
	f.records[id] = r
	f.disabled[id] = true
	f.entries = append(f.entries, e)

	return nil
}

func (f *fakeStore) ListTempAdminsAwaitingReview(_ context.Context) ([]tempadmin.TempAdmin, error) {
	var out []tempadmin.TempAdmin

	for key := range f.records {
		if r := f.records[key]; !r.EndedAt.IsZero() && r.ReviewOpenedAt.IsZero() {
			out = append(out, r)
		}
	}

	slices.SortFunc(out, func(a, b tempadmin.TempAdmin) int {
		return strings.Compare(a.PrincipalID, b.PrincipalID)
	})

	return out, nil
}

func (f *fakeStore) MarkTempAdminReviewOpened(_ context.Context, id string, at time.Time) error {
	r, ok := f.records[id]
	if !ok {
		return tempadmin.ErrNotFound
	}

	if r.EndedAt.IsZero() {
		return tempadmin.ErrInvalid
	}

	if r.ReviewOpenedAt.IsZero() {
		r.ReviewOpenedAt = at
		f.records[id] = r
	}

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
	svc, err := tempadmin.NewService(tempadmin.Config{Store: store, Reviews: nil, Now: baseTime, Logger: nil})
	if err != nil {
		panic(err)
	}

	return svc
}

// callerEntry is the audit entry of the host operator for a create.
func callerEntry() audit.Entry {
	return audit.Entry{
		FenceVersion:  0,
		At:            time.Time{},
		Namespace:     "",
		Subject:       "host",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "",
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

func TestCreateSetsAFixedExpiry(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	svc := testService(store)

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host", Reason: testReason}

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

	if e.Reason != testReason {
		t.Fatalf("audit entry reason = %q, want %q", e.Reason, testReason)
	}
}

// TestValidateReasonFitsAnAuditEntry proves that a reason of an audit entry
// length is usable, and that a longer one is refused, because the audit entry
// of the create holds the reason.
func TestValidateReasonFitsAnAuditEntry(t *testing.T) {
	t.Parallel()

	if err := tempadmin.ValidateReason(strings.Repeat("a", audit.MaxReasonLen)); err != nil {
		t.Fatalf("reason at the limit: %v", err)
	}

	if err := tempadmin.ValidateReason(strings.Repeat("a", audit.MaxReasonLen+1)); err == nil {
		t.Fatal("reason over the limit was accepted")
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
		{name: "empty subject", input: tempadmin.Input{Subject: "", PrincipalID: "prn-a", CreatedBy: "host", Reason: testReason}},
		{
			name:  "bootstrap subject",
			input: tempadmin.Input{Subject: "admin", PrincipalID: "prn-a", CreatedBy: "host", Reason: testReason},
		},
		{name: "missing principal", input: tempadmin.Input{Subject: "alice", PrincipalID: "", CreatedBy: "host", Reason: testReason}},
		{name: "missing creator", input: tempadmin.Input{Subject: "alice", PrincipalID: "prn-a", CreatedBy: "", Reason: testReason}},
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

	in := tempadmin.Input{Subject: subject, PrincipalID: id, CreatedBy: "host", Reason: testReason}

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

	done, err := later.Sweep(t.Context(), serverEntry())
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

// serverEntry is the entry of the server for a sweep: it names no
// subject, so the sweep names SweepSubject.
func serverEntry() audit.Entry {
	e := callerEntry()
	e.Subject = ""

	return e
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
	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: store, Reviews: nil, Now: func() time.Time { return now }, Logger: nil,
	})
	if err != nil {
		panic(err)
	}

	return svc
}

// requestKey holds the client address and the request ID of a test
// request in the context.
type requestKey struct{}

// testRequest answers the client address and the request ID that a test
// stored under requestKey, as the HTTP adapter answers them.
func testRequest(ctx context.Context) tempadmin.Request {
	v, ok := ctx.Value(requestKey{}).([2]string)
	if !ok {
		return tempadmin.Request{SourceIP: "", RequestID: ""}
	}

	return tempadmin.Request{SourceIP: v[0], RequestID: v[1]}
}

// signInDecorator returns a Decorator over store that resolves caller for
// the token "good", with a clock one hour after the create.
func signInDecorator(
	t *testing.T, store *fakeStore, audits *fakeAudit, caller identity.Principal,
) *tempadmin.Decorator {
	t.Helper()

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next:       fakeAuthn{principal: caller},
		Records:    store,
		Principals: store,
		Request:    testRequest,
		Expirer:    store,
		Audit:      audits,
		Now:        func() time.Time { return baseTime().Add(time.Hour) },
		Logger:     nil,
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

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host", Reason: testReason}

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
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
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

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host", Reason: testReason}

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
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
	}

	dec := signInDecorator(t, store, audits, caller)

	ctx := tempadmin.WithSignInScope(t.Context())
	ctx = context.WithValue(ctx, requestKey{}, [2]string{"192.0.2.7", "req-1"})

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
			Next:       fakeAuthn{principal: p},
			Records:    store,
			Principals: store,
			Request:    nil,
			Expirer:    store,
			Audit:      audits,
			Now:        nil,
			Logger:     nil,
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
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
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
		Kind:                   "",
		Credential:             "",
		JobID:                  "",
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

	in := tempadmin.Input{Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host", Reason: testReason}

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
			Kind:                   "",
			Credential:             "",
			JobID:                  "",
		}},
		Records:    store,
		Principals: store,
		Request:    nil,
		Expirer:    store,
		Audit:      audits,
		Now:        func() time.Time { return rec.ExpiresAt },
		Logger:     nil,
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
		Next:       inner,
		Records:    store,
		Principals: store,
		Request:    nil,
		Expirer:    store,
		Audit:      audits,
		Now:        nil,
		Logger:     nil,
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
		CreatedAt:      baseTime(),
		ExpiresAt:      baseTime().Add(tempadmin.Lifetime),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    "prn-alice",
		Subject:        "alice",
		CreatedBy:      "host",
		Reason:         testReason,
	}

	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	r.Subject = "admin-2"

	if err := r.Validate(); !errors.Is(err, principal.ErrReservedSubject) {
		t.Fatalf("validate of a bootstrap name = %v, want ErrReservedSubject", err)
	}
}

// TestDecoratorReusedSubjectNotLockedOut proves that a new person with the
// subject of an old temporary administrator passes: the old record names
// another principal, so it neither flags nor denies the new person.
func TestDecoratorReusedSubjectNotLockedOut(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	audits := newFakeAudit()

	old := tempadmin.Input{Subject: "alice", PrincipalID: "prn-old", CreatedBy: "host", Reason: testReason}
	svc := testServiceAt(store, baseTime().Add(-48*time.Hour))
	if _, err := svc.Create(t.Context(), callerEntry(), old); err != nil {
		t.Fatalf("create: %v", err)
	}

	store.people["alice"] = "prn-new"

	caller := identity.Principal{
		Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "tok-new",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       "",
		Credential: "",
		JobID:      "",
	}

	got, err := signInDecorator(t, store, audits, caller).AuthenticateToken(t.Context(), "good")
	if err != nil {
		t.Fatalf("sign-in of the new alice: %v", err)
	}

	if got.TemporaryAdministrator || len(audits.entries) != 0 || store.disabled["prn-old"] {
		t.Fatalf("sign-in = %+v, entries %d, old ended %v; want a plain caller and no write",
			got, len(audits.entries), store.disabled["prn-old"])
	}
}

// fakeReviews keeps the reviews it opens. Like the review store it makes
// one review for each use, however often the same use opens. It counts
// every call, and fails the next failNext calls.
type fakeReviews struct {
	uses     []breakglass.Use
	calls    int
	failNext int
}

func (f *fakeReviews) OpenReview(_ context.Context, u breakglass.Use) error {
	f.calls++

	if f.failNext > 0 {
		f.failNext--

		return errDriver
	}

	if err := u.Validate(); err != nil {
		return err
	}

	for i := range f.uses {
		if f.uses[i].Kind == u.Kind && f.uses[i].Ref == u.Ref {
			return nil
		}
	}

	f.uses = append(f.uses, u)

	return nil
}

// reviewService returns a Service over store with reviews and the clock
// fixed at the expiry of a record made at baseTime.
func reviewService(t *testing.T, store *fakeStore, reviews tempadmin.Reviews, logs *bytes.Buffer) *tempadmin.Service {
	t.Helper()

	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: store, Reviews: reviews, Now: func() time.Time { return baseTime().Add(tempadmin.Lifetime) },
		Logger: testLogger(logs),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return svc
}

// TestSweepRetriesAFailedReview proves that a failed open of a review
// makes the sweep incomplete, and that the next sweep opens the review of
// the record that already ended, with no second expiry entry.
func TestSweepRetriesAFailedReview(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	reviews := &fakeReviews{uses: nil, calls: 0, failNext: 1}

	var logs bytes.Buffer

	svc := reviewService(t, store, reviews, &logs)

	done, err := svc.Sweep(t.Context(), serverEntry())
	if !errors.Is(err, tempadmin.ErrSweepIncomplete) || !slices.Equal(done, []string{"prn-a"}) {
		t.Fatalf("first sweep = %v, %v; want prn-a ended and ErrSweepIncomplete", done, err)
	}

	if len(reviews.uses) != 0 || !store.records["prn-a"].ReviewOpenedAt.IsZero() {
		t.Fatalf("after the failed open: %d reviews, mark %s; want none", len(reviews.uses),
			store.records["prn-a"].ReviewOpenedAt)
	}

	if strings.Contains(logs.String(), "pq:") {
		t.Fatalf("log = %q, want no driver text", logs.String())
	}

	entries := len(store.entries)

	done, err = svc.Sweep(t.Context(), serverEntry())
	if err != nil || len(done) != 0 {
		t.Fatalf("second sweep = %v, %v; want no new expiry and no error", done, err)
	}

	if len(reviews.uses) != 1 || store.records["prn-a"].ReviewOpenedAt.IsZero() || len(store.entries) != entries {
		t.Fatalf("after the retry: %d reviews, mark %s, entries %d -> %d; want 1 review, a mark, no new entry",
			len(reviews.uses), store.records["prn-a"].ReviewOpenedAt, entries, len(store.entries))
	}
}

// TestSweepOpensOneReviewPerUse proves that a record leaves the retry once
// its review opened: later sweeps do not call OpenReview again.
func TestSweepOpensOneReviewPerUse(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	reviews := &fakeReviews{uses: nil, calls: 0, failNext: 0}
	svc := reviewService(t, store, reviews, &bytes.Buffer{})

	for range 3 {
		if _, err := svc.Sweep(t.Context(), serverEntry()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}

	if reviews.calls != 1 || len(reviews.uses) != 1 {
		t.Fatalf("OpenReview calls = %d, reviews = %d; want 1 and 1", reviews.calls, len(reviews.uses))
	}
}

// TestSweepNeverReopensAReviewOfAMarkedRecord proves that a record with a
// mark is not in the retry, and that the same use never gets a second
// review even when the mark is missing and OpenReview runs again.
func TestSweepNeverReopensAReviewOfAMarkedRecord(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	reviews := &fakeReviews{uses: nil, calls: 0, failNext: 0}
	svc := reviewService(t, store, reviews, &bytes.Buffer{})

	if _, err := svc.Sweep(t.Context(), serverEntry()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// A crash between the open and the mark leaves no mark.
	r := store.records["prn-a"]
	r.ReviewOpenedAt = time.Time{}
	store.records["prn-a"] = r

	if _, err := svc.Sweep(t.Context(), serverEntry()); err != nil {
		t.Fatalf("sweep after the lost mark: %v", err)
	}

	if reviews.calls != 2 || len(reviews.uses) != 1 {
		t.Fatalf("calls = %d, reviews = %d; want the open to repeat and still 1 review",
			reviews.calls, len(reviews.uses))
	}
}

// TestSweepWithNoReviewsRetriesNothing proves that with Reviews nil the
// sweep only warns once for a record it ends, and sets no mark.
func TestSweepWithNoReviewsRetriesNothing(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	var logs bytes.Buffer

	svc := reviewService(t, store, nil, &logs)

	for range 3 {
		if _, err := svc.Sweep(t.Context(), serverEntry()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}

	if got := strings.Count(logs.String(), "no break-glass review opens"); got != 1 {
		t.Fatalf("warnings = %d, want 1; log = %q", got, logs.String())
	}

	if !store.records["prn-a"].ReviewOpenedAt.IsZero() {
		t.Fatal("record has a review mark with no review store")
	}
}

// TestSweepOpensReviews proves that the sweep opens a review for each
// record that it ends, with the reason and the end of the record, and
// for a record that a sign-in ended first, which has no review yet.
func TestSweepOpensReviews(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	reviews := &fakeReviews{uses: nil, calls: 0, failNext: 0}

	for _, id := range []string{"prn-a", "prn-b"} {
		in := tempadmin.Input{Subject: id, PrincipalID: id, CreatedBy: "host", Reason: testReason}
		if _, err := testService(store).Create(t.Context(), callerEntry(), in); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	ended := store.records["prn-b"]
	ended.EndedAt = baseTime().Add(tempadmin.Lifetime)
	store.records["prn-b"] = ended
	store.disabled["prn-b"] = true

	later := baseTime().Add(tempadmin.Lifetime)

	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: store, Reviews: reviews, Now: func() time.Time { return later }, Logger: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	done, err := svc.Sweep(t.Context(), callerEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if !slices.Equal(done, []string{"prn-a"}) || len(reviews.uses) != 2 {
		t.Fatalf("sweep ended %v and opened %d reviews; want prn-a and 2", done, len(reviews.uses))
	}

	for _, u := range reviews.uses {
		if u.Kind != breakglass.KindTempAdmin || u.Reason != testReason || !u.Until.Equal(later) {
			t.Fatalf("review use = %+v, want a temp-admin use with the reason, until %s", u, later)
		}
	}
}

func TestValidateNeedsReason(t *testing.T) {
	t.Parallel()

	r := tempadmin.TempAdmin{
		CreatedAt:      baseTime(),
		ExpiresAt:      baseTime().Add(time.Hour),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    "prn-alice",
		Subject:        "alice",
		CreatedBy:      "host",
		Reason:         " ",
	}

	if err := r.Validate(); !errors.Is(err, tempadmin.ErrInvalid) {
		t.Fatalf("validate with a blank reason = %v, want ErrInvalid", err)
	}
}

// testLogger returns a logger that writes text to buf.
func testLogger(buf *bytes.Buffer) *slog.Logger {
	opts := &slog.HandlerOptions{AddSource: false, Level: slog.LevelInfo, ReplaceAttr: nil}

	return slog.New(slog.NewTextHandler(buf, opts))
}

// errDriver is a store error with driver text that no log line may show.
var errDriver = errors.New("pq: relation temp_admins does not exist")

// TestSweepContinuesPastAFailure proves that a failed expiry of one record
// does not stop the sweep: the other record ends, the sweep answers
// ErrSweepIncomplete, and the log names the principal with no driver text.
func TestSweepContinuesPastAFailure(t *testing.T) {
	t.Parallel()

	store := newFakeStore()

	for _, id := range []string{"prn-a", "prn-b"} {
		createSweepRecord(t, testService(store), id, id)
	}

	store.fail["prn-a"] = errDriver

	var logs bytes.Buffer

	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: store, Reviews: nil, Now: func() time.Time { return baseTime().Add(tempadmin.Lifetime) },
		Logger: testLogger(&logs),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	done, err := svc.Sweep(t.Context(), serverEntry())
	if !errors.Is(err, tempadmin.ErrSweepIncomplete) {
		t.Fatalf("sweep with a failed record: err = %v, want ErrSweepIncomplete", err)
	}

	if !slices.Equal(done, []string{"prn-b"}) {
		t.Fatalf("sweep ended %v, want prn-b past the failure of prn-a", done)
	}

	if !strings.Contains(logs.String(), "prn-a") || strings.Contains(logs.String(), "pq:") {
		t.Fatalf("log = %q, want the principal and no driver text", logs.String())
	}
}

// TestSweepWarnsWithNoReviews proves that a sweep with no Reviews writes
// one warning for each record that it ends: no break-glass review opens.
func TestSweepWarnsWithNoReviews(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	var logs bytes.Buffer

	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: store, Reviews: nil, Now: func() time.Time { return baseTime().Add(tempadmin.Lifetime) },
		Logger: testLogger(&logs),
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	for range 2 {
		if _, err := svc.Sweep(t.Context(), serverEntry()); err != nil {
			t.Fatalf("sweep: %v", err)
		}
	}

	if got := strings.Count(logs.String(), "no break-glass review opens"); got != 1 ||
		!strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("log = %q, want one warning that no review opens", logs.String())
	}
}

// TestHostSweepKeepsTheHostSubject proves that a sweep with the entry of
// the host command writes its expiry entries with the subject @host.
func TestHostSweepKeepsTheHostSubject(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	createSweepRecord(t, testService(store), "prn-a", "alice")

	host := callerEntry()
	host.Subject = tempadmin.HostSubject

	if _, err := testServiceAt(store, baseTime().Add(tempadmin.Lifetime)).Sweep(t.Context(), host); err != nil {
		t.Fatalf("host sweep: %v", err)
	}

	if last := store.entries[len(store.entries)-1]; last.Subject != tempadmin.HostSubject {
		t.Fatalf("expiry subject = %q, want %q", last.Subject, tempadmin.HostSubject)
	}
}

// TestValidateRefusesHiddenRunes proves that a reason or a creator with a
// control character or a format character, such as a direction override or
// a zero-width space, is refused.
func TestValidateRefusesHiddenRunes(t *testing.T) {
	t.Parallel()

	base := tempadmin.TempAdmin{
		CreatedAt:      baseTime(),
		ExpiresAt:      baseTime().Add(time.Hour),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    "prn-alice",
		Subject:        "alice",
		CreatedBy:      "host",
		Reason:         testReason,
	}

	if err := base.Validate(); err != nil {
		t.Fatalf("valid record: %v", err)
	}

	for _, bad := range []string{"lost\u202eaccess", "lost\u200baccess", "lost\x07access", "lost\u0085access"} {
		reason := base
		reason.Reason = bad

		if err := reason.Validate(); !errors.Is(err, tempadmin.ErrInvalid) {
			t.Errorf("reason %q: err = %v, want ErrInvalid", bad, err)
		}

		if err := tempadmin.ValidateReason(bad); err == nil {
			t.Errorf("ValidateReason(%q) = nil, want an error", bad)
		}

		creator := base
		creator.CreatedBy = bad

		if err := creator.Validate(); !errors.Is(err, tempadmin.ErrInvalid) {
			t.Errorf("creator %q: err = %v, want ErrInvalid", bad, err)
		}
	}
}

// TestDecoratorLogsTheExpiry proves that a use after the expiry logs that
// no break-glass review opens when it ends the record, and that a failed
// expiry logs the principal with no driver text and still denies the use.
func TestDecoratorLogsTheExpiry(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		fail error
		want string
		name string
	}{
		{name: "the use ends the record", fail: nil, want: "no break-glass review opens"},
		{name: "the expiry fails", fail: errDriver, want: "temporary administrator expiry failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			out := expiredUseLog(t, c.fail)
			if !strings.Contains(out, c.want) || !strings.Contains(out, "prn-alice") || strings.Contains(out, "pq:") {
				t.Fatalf("log = %q, want %q with the principal and no driver text", out, c.want)
			}
		})
	}
}

// expiredUseLog makes alice a temporary administrator, uses her token at
// the expiry with the store failing the expiry with fail when it is not
// nil, and answers the log of the decorator. The use must be denied.
func expiredUseLog(t *testing.T, fail error) string {
	t.Helper()

	store := newFakeStore()

	rec, err := testService(store).Create(t.Context(), callerEntry(), tempadmin.Input{
		Subject: "alice", PrincipalID: "prn-alice", CreatedBy: "host", Reason: testReason,
	})
	if err != nil {
		t.Fatalf("create temporary administrator: %v", err)
	}

	if fail != nil {
		store.fail["prn-alice"] = fail
	}

	var logs bytes.Buffer

	dec, err := tempadmin.NewDecorator(tempadmin.DecoratorConfig{
		Next: fakeAuthn{principal: identity.Principal{
			Subject: "alice", DisplayName: "alice", Admin: false, TokenID: "tok-alice",
			TokenExpiresAt: time.Time{}, TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Kind:       "",
			Credential: "",
			JobID:      "",
		}},
		Records: store, Principals: store, Request: nil, Expirer: store, Audit: newFakeAudit(),
		Now: func() time.Time { return rec.ExpiresAt }, Logger: testLogger(&logs),
	})
	if err != nil {
		t.Fatalf("new decorator: %v", err)
	}

	if _, err := dec.AuthenticateToken(t.Context(), "good"); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("use after the expiry = %v, want ErrUnauthenticated", err)
	}

	return logs.String()
}
