package firstadminsvc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"opendustpan.org/opendustpan/internal/firstadminsvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/firstadmin"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

const window = 5 * time.Minute

// errBoom is the error of a port that fails in a way with no domain error.
var errBoom = errors.New("driver: boom")

// clock is the fixed time of every test.
func clock() time.Time { return time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC) }

const (
	personID  = "prn-ada01"
	otherID   = "prn-bob02"
	sessionID = "ses-0123456789"
	reason    = "first administrator of the new installation"
	adaName   = "ada@example.org"
)

// fakeStore is the port with settable answers. It records each call.
type fakeStore struct {
	mu          sync.Mutex
	state       firstadmin.State
	stateErr    error
	offerErr    error
	redeemErr   error
	offerIn     []firstadmin.OfferInput
	offerEntry  []audit.Entry
	redeemIn    []firstadmin.RedeemInput
	redeemEntry []audit.Entry
	replaced    bool
}

var _ firstadmin.Store = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		mu: sync.Mutex{}, state: emptyState(), stateErr: nil, offerErr: nil, redeemErr: nil,
		offerIn: nil, offerEntry: nil, redeemIn: nil, redeemEntry: nil, replaced: false,
	}
}

func emptyState() firstadmin.State { return firstadmin.State{Marker: nil, Offer: nil} }

func madeState() firstadmin.State {
	return firstadmin.State{
		Marker: &firstadmin.Marker{
			MadeAt: clock(), PrincipalID: otherID, GrantID: "grt-x", SessionID: "ses-x", CreatedBy: "admin",
		},
		Offer: nil,
	}
}

func openOffer(forID string) firstadmin.State {
	return firstadmin.State{
		Marker: nil,
		Offer: &firstadmin.Offer{
			CreatedAt: clock().Add(-time.Minute), ExpiresAt: clock().Add(9 * time.Minute),
			PrincipalID: forID, CreatedBy: "admin", Reason: reason,
		},
	}
}

func (f *fakeStore) State(context.Context, time.Time) (firstadmin.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.state, f.stateErr
}

func (f *fakeStore) Offer(_ context.Context, in firstadmin.OfferInput, e audit.Entry) (firstadmin.OfferResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.offerIn = append(f.offerIn, in)
	f.offerEntry = append(f.offerEntry, e)

	if f.offerErr != nil {
		return firstadmin.OfferResult{ExpiresAt: time.Time{}, Replaced: false}, f.offerErr
	}

	return firstadmin.OfferResult{ExpiresAt: in.Now.Add(firstadmin.Lifetime), Replaced: f.replaced}, nil
}

func (f *fakeStore) Redeem(_ context.Context, in firstadmin.RedeemInput, e audit.Entry) (firstadmin.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.redeemIn = append(f.redeemIn, in)
	f.redeemEntry = append(f.redeemEntry, e)

	if f.redeemErr != nil {
		return firstadmin.Result{MadeAt: time.Time{}, PrincipalID: "", GrantID: ""}, f.redeemErr
	}

	return firstadmin.Result{MadeAt: in.Now, PrincipalID: in.PrincipalID, GrantID: in.GrantID}, nil
}

func (f *fakeStore) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.offerIn) + len(f.redeemIn)
}

type fakePrincipals struct {
	byID map[string]principal.Principal
	err  error
}

func (f fakePrincipals) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	if f.err != nil {
		return principal.Principal{}, f.err
	}

	p, ok := f.byID[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return p, nil
}

type fakeTempAdmins struct {
	subjects map[string]bool
	err      error
}

var _ tempadmin.Reader = fakeTempAdmins{subjects: nil, err: nil}

func (f fakeTempAdmins) GetTempAdminBySubject(_ context.Context, subject string) (tempadmin.TempAdmin, error) {
	if f.err != nil {
		return emptyTempAdmin(), f.err
	}

	if f.subjects[subject] {
		return emptyTempAdmin(), nil
	}

	return emptyTempAdmin(), tempadmin.ErrNotFound
}

func emptyTempAdmin() tempadmin.TempAdmin {
	return tempadmin.TempAdmin{
		CreatedAt: time.Time{}, ExpiresAt: time.Time{}, EndedAt: time.Time{}, ReviewOpenedAt: time.Time{},
		PrincipalID: "", Subject: "", CreatedBy: "", Reason: "",
	}
}

func (fakeTempAdmins) ListTempAdmins(context.Context) ([]tempadmin.TempAdmin, error) { return nil, nil }

type fakeAudit struct {
	mu      sync.Mutex
	entries []audit.Entry
	err     error
}

func (f *fakeAudit) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// A store refuses a write on a context that ended.
	if err := ctx.Err(); err != nil {
		return audit.Entry{}, fmt.Errorf("append: %w", err)
	}

	if f.err != nil {
		return audit.Entry{}, f.err
	}

	f.entries = append(f.entries, e)

	return e, nil
}

func (f *fakeAudit) all() []audit.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]audit.Entry(nil), f.entries...)
}

// rig is one service with its fakes. A test changes the fields before New.
type rig struct {
	svc        *firstadminsvc.Service
	store      *fakeStore
	principals fakePrincipals
	temps      fakeTempAdmins
	audit      *fakeAudit
	logs       *bytes.Buffer
	random     io.Reader
	window     time.Duration
	grantID    string
}

func person(id, subject string) principal.Principal {
	return principal.Principal{
		CreatedAt: clock().Add(-time.Hour), UpdatedAt: clock().Add(-time.Hour), DisabledAt: time.Time{},
		ID: id, Subject: subject, DisplayName: subject,
		Kind: principal.KindPerson, Source: "", Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

// seq is a deterministic random source that never repeats a byte run.
type seq struct{ n byte }

func (s *seq) Read(p []byte) (int, error) {
	for i := range p {
		s.n++
		p[i] = s.n
	}

	return len(p), nil
}

func newRig(t *testing.T, mutate func(*rig)) *rig {
	t.Helper()

	r := &rig{
		svc:   nil,
		store: newFakeStore(),
		principals: fakePrincipals{err: nil, byID: map[string]principal.Principal{
			personID: person(personID, adaName),
			otherID:  person(otherID, "bob@example.org"),
		}},
		temps:   fakeTempAdmins{subjects: map[string]bool{}, err: nil},
		audit:   &fakeAudit{mu: sync.Mutex{}, entries: nil, err: nil},
		logs:    &bytes.Buffer{},
		random:  &seq{n: 0},
		window:  window,
		grantID: access.GrantIDPrefix + "abc123",
	}

	if mutate != nil {
		mutate(r)
	}

	svc, err := firstadminsvc.New(firstadminsvc.Config{
		Store: r.store, Principals: r.principals, TempAdmins: r.temps, Audit: r.audit,
		Now:     clock,
		Random:  r.random,
		GrantID: func() string { return r.grantID },
		Window:  func() time.Duration { return r.window },
		Logger:  slog.New(slog.NewTextHandler(r.logs, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r.svc = svc

	return r
}

func bootstrap() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: "admin", DisplayName: "admin", Admin: true,
		TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}

func apiRequest() firstadminsvc.Request {
	return firstadminsvc.Request{
		SourceIP: "192.0.2.1", UserAgent: "test", RequestID: "req-1", Channel: audit.ChannelAPI,
	}
}

func offerIn() firstadminsvc.OfferInput {
	return firstadminsvc.OfferInput{
		Caller: bootstrap(), PrincipalID: personID, Reason: reason, Request: apiRequest(),
	}
}

func goodCode(t *testing.T) firstadmin.Code {
	t.Helper()

	code, err := firstadmin.NewCode(&seq{n: 100})
	if err != nil {
		t.Fatalf("NewCode: %v", err)
	}

	return code
}

func redeemIn(t *testing.T) firstadminsvc.RedeemInput {
	t.Helper()

	return firstadminsvc.RedeemInput{
		AuthTime: clock().Add(-time.Minute),
		Caller: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			Subject: adaName, DisplayName: "Ada", Admin: false,
			TokenID: "", TokenExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: identity.CredentialSession, JobID: "",
		},
		PrincipalID: personID, SessionID: sessionID, Code: goodCode(t),
		Request: firstadminsvc.Request{
			SourceIP: "192.0.2.2", UserAgent: "test", RequestID: "req-2", Channel: audit.ChannelConsole,
		},
	}
}

// ruleOf returns the failed rule of a failure entry.
func ruleOf(e audit.Entry) string {
	for _, c := range e.Changes {
		if c.Key == "rule" {
			return c.New
		}
	}

	return ""
}

func hasChange(e audit.Entry, key, value string) bool {
	for _, c := range e.Changes {
		if c.Key == key && c.New == value {
			return true
		}
	}

	return false
}

// assertNoSecret fails when the code or its digest is in an audit entry or in
// the log output.
func assertNoSecret(t *testing.T, r *rig, code firstadmin.Code) {
	t.Helper()

	if code == "" {
		return
	}

	entries := append(r.audit.all(), r.store.offerEntry...)
	entries = append(entries, r.store.redeemEntry...)

	for i := range entries {
		text := fmt.Sprintf("%+v", entries[i])

		for _, secret := range []string{string(code), code.Digest()} {
			if strings.Contains(text, secret) {
				t.Errorf("audit entry %s holds the code or its digest", entries[i].Action)
			}
		}
	}

	for _, secret := range []string{string(code), code.Digest()} {
		if strings.Contains(r.logs.String(), secret) {
			t.Error("log output holds the code or its digest")
		}
	}
}

func TestNewRefusesMissingDependency(t *testing.T) {
	t.Parallel()

	good := func() firstadminsvc.Config {
		r := newRig(t, nil)

		return firstadminsvc.Config{
			Store: r.store, Principals: r.principals, TempAdmins: r.temps, Audit: r.audit,
			Now: clock, Random: r.random,
			GrantID: func() string { return "grt-x" }, Window: func() time.Duration { return window },
			Logger: slog.New(slog.DiscardHandler),
		}
	}

	if _, err := firstadminsvc.New(good()); err != nil {
		t.Fatalf("New(good) = %v", err)
	}

	cases := map[string]func(*firstadminsvc.Config){
		"store":      func(c *firstadminsvc.Config) { c.Store = nil },
		"principals": func(c *firstadminsvc.Config) { c.Principals = nil },
		"temp":       func(c *firstadminsvc.Config) { c.TempAdmins = nil },
		"audit":      func(c *firstadminsvc.Config) { c.Audit = nil },
		"clock":      func(c *firstadminsvc.Config) { c.Now = nil },
		"random":     func(c *firstadminsvc.Config) { c.Random = nil },
		"grant ID":   func(c *firstadminsvc.Config) { c.GrantID = nil },
		"window":     func(c *firstadminsvc.Config) { c.Window = nil },
		"logger":     func(c *firstadminsvc.Config) { c.Logger = nil },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := good()
			mutate(&cfg)

			if _, err := firstadminsvc.New(cfg); !errors.Is(err, firstadmin.ErrMissingDependency) {
				t.Fatalf("New error = %v, want ErrMissingDependency", err)
			}
		})
	}
}

func TestIsBootstrap(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(*identity.Principal)
		want   bool
	}{
		{name: "static bootstrap", change: func(*identity.Principal) {}, want: true},
		{name: "no admin flag", change: func(p *identity.Principal) { p.Admin = false }, want: false},
		{name: "personal token with Admin", change: func(p *identity.Principal) { p.TokenID = "tok-1" }, want: false},
		{name: "token with expiry", change: func(p *identity.Principal) { p.TokenExpiresAt = clock() }, want: false},
		{name: "person kind", change: func(p *identity.Principal) { p.Kind = identity.KindPerson }, want: false},
		{
			name:   "console session",
			change: func(p *identity.Principal) { p.Credential = identity.CredentialSession }, want: false,
		},
		{name: "job token", change: func(p *identity.Principal) { p.JobID = "job-1" }, want: false},
		{
			name:   "temporary administrator",
			change: func(p *identity.Principal) { p.TemporaryAdministrator = true }, want: false,
		},
		{
			name:   "temporary expiry only",
			change: func(p *identity.Principal) { p.TemporaryExpiresAt = clock() }, want: false,
		},
		{name: "no subject", change: func(p *identity.Principal) { p.Subject = "" }, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := bootstrap()
			tc.change(&p)

			if got := firstadminsvc.IsBootstrap(p); got != tc.want {
				t.Errorf("IsBootstrap = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestOfferWritesDigestOnly checks the good offer: the answer holds the
// code, the store gets the digest only, and the audit entry holds the
// creator, the object, the reason and the replaced note.
func TestOfferWritesDigestOnly(t *testing.T) {
	t.Parallel()

	for _, replaced := range []bool{false, true} {
		t.Run("replaced "+strconv.FormatBool(replaced), func(t *testing.T) {
			t.Parallel()

			r := newRig(t, func(r *rig) {
				if replaced {
					r.store.state = openOffer(personID)
					r.store.replaced = true
				}
			})

			res, err := r.svc.Offer(t.Context(), offerIn())
			if err != nil {
				t.Fatalf("Offer: %v", err)
			}

			if !res.Code.ValidForm() || res.PrincipalID != personID || res.Replaced != replaced {
				t.Fatalf("result = %+v, want a code for %s with replaced %t", res, personID, replaced)
			}

			assertOfferStored(t, r, res)
			assertNoSecret(t, r, res.Code)
		})
	}
}

func assertOfferStored(t *testing.T, r *rig, res firstadminsvc.OfferResult) {
	t.Helper()

	if len(r.store.offerIn) != 1 {
		t.Fatalf("store offers = %d, want 1", len(r.store.offerIn))
	}

	in := r.store.offerIn[0]
	if in.Digest != res.Code.Digest() || in.CreatedBy != "admin" || in.Reason != reason || in.PrincipalID != personID {
		t.Errorf("store input = %+v, want the digest of the answer, creator admin", in)
	}

	assertOfferEntry(t, r.store.offerEntry[0], strconv.FormatBool(res.Replaced))
}

func assertOfferEntry(t *testing.T, e audit.Entry, wantReplaced string) {
	t.Helper()

	if e.Action != string(identity.ActionServerOfferFirstAdministrator) || e.Object != principal.Ref(personID) ||
		e.Subject != "admin" || e.Reason != reason {
		t.Errorf("entry = %+v, want the offer entry of admin for the person with the reason", e)
	}

	if e.Outcome != audit.OutcomeSuccess || e.Namespace != audit.ServerScope {
		t.Errorf("entry = %+v, want a success entry in the server scope", e)
	}

	if !hasChange(e, "replaced", wantReplaced) {
		t.Errorf("entry changes = %+v, want replaced %s", e.Changes, wantReplaced)
	}
}

// TestRedeemWritesEntry checks the good redeem: the store gets the person
// and the session of the caller, and the entry holds the person as subject,
// the installation as object, the grant, the creator, the session, the role
// and the reason of the offer.
func TestRedeemWritesEntry(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(r *rig) { r.store.state = openOffer(personID) })
	in := redeemIn(t)

	res, err := r.svc.Redeem(t.Context(), in)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	if res.PrincipalID != personID || res.GrantID != r.grantID {
		t.Errorf("result = %+v, want the person and the grant %s", res, r.grantID)
	}

	got := r.store.redeemIn[0]
	if got.PrincipalID != personID || got.SessionID != sessionID || got.Digest != in.Code.Digest() {
		t.Errorf("store input = %+v, want the person, the session and the digest", got)
	}

	if got.Window != window || !got.AuthTime.Equal(in.AuthTime) || got.GrantID != r.grantID {
		t.Errorf("store input = %+v, want the window, the sign-in time and the grant ID", got)
	}

	assertRedeemEntry(t, r.store.redeemEntry[0], r.grantID)
	assertNoSecret(t, r, in.Code)

	if n := len(r.audit.all()); n != 0 {
		t.Errorf("failure entries = %d, want none for a good redeem", n)
	}
}

func assertRedeemEntry(t *testing.T, e audit.Entry, grantID string) {
	t.Helper()

	if e.Subject != adaName || e.Object != "@server" || e.Namespace != audit.ServerScope ||
		e.Action != string(identity.ActionCallerRedeemFirstAdministrator) {
		t.Errorf("entry = %+v, want the redeem entry of the person on @server", e)
	}

	if e.Reason != reason || e.Outcome != audit.OutcomeSuccess {
		t.Errorf("entry = %+v, want a success entry with the reason of the offer", e)
	}

	for key, want := range map[string]string{
		"grant": grantID, "createdBy": "admin", "session": sessionID, "role": firstadmin.Role,
	} {
		if !hasChange(e, key, want) {
			t.Errorf("entry changes = %+v, want %s = %s", e.Changes, key, want)
		}
	}
}

// TestStateReadsStore checks the read of the marker and the failure mapping.
func TestStateReadsStore(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(r *rig) { r.store.state = openOffer(personID) })

	st, err := r.svc.State(t.Context())
	if err != nil || st.Offer == nil || st.Offer.PrincipalID != personID {
		t.Fatalf("State = %+v, %v, want the open offer", st, err)
	}

	r = newRig(t, func(r *rig) { r.store.stateErr = errBoom })

	if _, err := r.svc.State(t.Context()); !errors.Is(err, firstadminsvc.ErrUnavailable) {
		t.Fatalf("State error = %v, want ErrUnavailable", err)
	}
}

// offerRow is one hostile case of the offer.
type offerRow struct {
	name    string
	want    error
	rule    string
	mutate  func(r *rig)
	input   func(in *firstadminsvc.OfferInput)
	atStore bool
}

func refusedOffer(name string, want error, rule string) offerRow {
	return offerRow{name: name, want: want, rule: rule, mutate: nil, input: nil, atStore: false}
}

func (o offerRow) rig(f func(r *rig)) offerRow { o.mutate = f; return o }

func (o offerRow) with(f func(in *firstadminsvc.OfferInput)) offerRow { o.input = f; return o }

func (o offerRow) reachesStore() offerRow { o.atStore = true; return o }

func offerRows() []offerRow {
	notBoot, form, badReason := firstadminsvc.RuleCallerNotBootstrap, firstadminsvc.RulePrincipalForm,
		firstadminsvc.RuleReasonInvalid
	invalid, notAllowed := firstadmin.ErrPrincipalInvalid, firstadminsvc.RulePrincipalNotAllowed
	reasonWith := func(text string) func(in *firstadminsvc.OfferInput) {
		return func(in *firstadminsvc.OfferInput) { in.Reason = text }
	}
	idWith := func(id string) func(in *firstadminsvc.OfferInput) {
		return func(in *firstadminsvc.OfferInput) { in.PrincipalID = id }
	}
	changePerson := func(change func(p *principal.Principal)) func(r *rig) {
		return func(r *rig) {
			p := person(personID, adaName)
			change(&p)
			r.principals.byID[personID] = p
		}
	}

	return []offerRow{
		refusedOffer("personal token with Admin", firstadminsvc.ErrNotBootstrap, notBoot).
			with(func(in *firstadminsvc.OfferInput) { in.Caller.TokenID = "tok-1" }),
		refusedOffer("temporary administrator", firstadminsvc.ErrNotBootstrap, notBoot).
			with(func(in *firstadminsvc.OfferInput) { in.Caller.TemporaryAdministrator = true }),
		refusedOffer("console session of an administrator", firstadminsvc.ErrNotBootstrap, notBoot).
			with(func(in *firstadminsvc.OfferInput) {
				in.Caller.Credential = identity.CredentialSession
				in.Caller.Kind = identity.KindPerson
			}),
		refusedOffer("caller without Admin", firstadminsvc.ErrNotBootstrap, notBoot).
			with(func(in *firstadminsvc.OfferInput) { in.Caller.Admin = false }),
		refusedOffer("principal ID without the prefix", invalid, form).with(idWith("ada01")),
		refusedOffer("principal ID with a slash", invalid, form).with(idWith("prn-a/../b")),
		refusedOffer("principal ID with a new line", invalid, form).with(idWith("prn-a\nb")),
		refusedOffer("principal ID of the prefix only", invalid, form).with(idWith("prn-")),
		refusedOffer("principal ID too long", invalid, form).with(idWith("prn-" + strings.Repeat("a", 80))),
		refusedOffer("blank reason", firstadmin.ErrReasonInvalid, badReason).with(reasonWith("  ")),
		refusedOffer(
			"reason too long",
			firstadmin.ErrReasonInvalid,
			badReason,
		).with(reasonWith(strings.Repeat("a", 501))),
		refusedOffer("reason not UTF-8", firstadmin.ErrReasonInvalid, badReason).with(reasonWith("a\xffb")),
		refusedOffer(
			"reason with a control character",
			firstadmin.ErrReasonInvalid,
			badReason,
		).with(reasonWith("a\x07b")),
		refusedOffer(
			"reason with a format character",
			firstadmin.ErrReasonInvalid,
			badReason,
		).with(reasonWith("a\u202eb")),
		refusedOffer("marker set", firstadmin.ErrAlreadyMade, firstadminsvc.RuleAlreadyMade).
			rig(func(r *rig) { r.store.state = madeState() }),
		refusedOffer("no such principal", invalid, notAllowed).with(idWith("prn-nobody")),
		refusedOffer("disabled principal", invalid, notAllowed).
			rig(changePerson(func(p *principal.Principal) { p.DisabledAt = clock() })),
		refusedOffer("service principal", invalid, notAllowed).
			rig(changePerson(func(p *principal.Principal) { p.Kind = principal.KindService })),
		refusedOffer("break-glass principal", invalid, notAllowed).
			rig(changePerson(func(p *principal.Principal) { p.BreakGlass = true })),
		refusedOffer("dev subject", invalid, notAllowed).
			rig(changePerson(func(p *principal.Principal) { p.Subject = "dev:ada" })),
		refusedOffer("temporary administrator principal", invalid, notAllowed).
			rig(func(r *rig) { r.temps.subjects[adaName] = true }),
		refusedOffer("principal read fails", firstadminsvc.ErrUnavailable, notAllowed).
			rig(func(r *rig) { r.principals.err = errBoom }),
		refusedOffer("temporary administrator read fails", firstadminsvc.ErrUnavailable, notAllowed).
			rig(func(r *rig) { r.temps.err = errBoom }),
		refusedOffer("state read fails", firstadminsvc.ErrUnavailable, firstadminsvc.RuleStoreError).
			rig(func(r *rig) { r.store.stateErr = errBoom }),
		refusedOffer("random source fails", firstadminsvc.ErrUnavailable, firstadminsvc.RuleRandomSource).
			rig(func(r *rig) { r.random = iotest.ErrReader(errBoom) }),
		refusedOffer("random source is short", firstadminsvc.ErrUnavailable, firstadminsvc.RuleRandomSource).
			rig(func(r *rig) { r.random = bytes.NewReader(make([]byte, 5)) }),
		refusedOffer("store error", firstadminsvc.ErrUnavailable, firstadminsvc.RuleStoreError).
			rig(func(r *rig) { r.store.offerErr = errBoom }).reachesStore(),
		refusedOffer("store says marker set", firstadmin.ErrAlreadyMade, firstadminsvc.RuleAlreadyMade).
			rig(func(r *rig) { r.store.offerErr = firstadmin.ErrAlreadyMade }).reachesStore(),
		refusedOffer("store says principal invalid", invalid, notAllowed).
			rig(func(r *rig) { r.store.offerErr = firstadmin.ErrPrincipalInvalid }).reachesStore(),
	}
}

// redeemRow is one hostile case of the redeem.
type redeemRow struct {
	name    string
	want    error
	rule    string
	mutate  func(r *rig)
	input   func(in *firstadminsvc.RedeemInput)
	atStore bool
}

func refusedRedeem(name string, want error, rule string) redeemRow {
	return redeemRow{name: name, want: want, rule: rule, mutate: nil, input: nil, atStore: false}
}

func (o redeemRow) rig(f func(r *rig)) redeemRow { o.mutate = f; return o }

func (o redeemRow) with(f func(in *firstadminsvc.RedeemInput)) redeemRow { o.input = f; return o }

func (o redeemRow) reachesStore() redeemRow { o.atStore = true; return o }

func redeemRows() []redeemRow {
	badCode, session := firstadmin.ErrCodeInvalid, firstadminsvc.RuleSessionInput
	stale, notAllowed := firstadmin.ErrStaleSignIn, firstadminsvc.RulePrincipalNotAllowed
	storeErr := firstadminsvc.RuleStoreError
	codeWith := func(f func(c firstadmin.Code) firstadmin.Code) func(in *firstadminsvc.RedeemInput) {
		return func(in *firstadminsvc.RedeemInput) { in.Code = f(in.Code) }
	}
	storeFails := func(err error) func(r *rig) { return func(r *rig) { r.store.redeemErr = err } }

	return []redeemRow{
		refusedRedeem("code too short", badCode, firstadminsvc.RuleCodeForm).
			with(codeWith(func(c firstadmin.Code) firstadmin.Code { return c[:10] })),
		refusedRedeem("code too long", badCode, firstadminsvc.RuleCodeForm).
			with(codeWith(func(c firstadmin.Code) firstadmin.Code { return c + "A" })),
		refusedRedeem("empty code", badCode, firstadminsvc.RuleCodeForm).
			with(codeWith(func(firstadmin.Code) firstadmin.Code { return "" })),
		refusedRedeem("code with a bad character", badCode, firstadminsvc.RuleCodeForm).
			with(codeWith(func(c firstadmin.Code) firstadmin.Code { return c[:42] + "+" })),
		refusedRedeem("no principal from the session", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.PrincipalID = "" }),
		refusedRedeem("principal ID of a bad form", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.PrincipalID = "prn-a b" }),
		refusedRedeem("no session ID", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.SessionID = "" }),
		refusedRedeem("session ID too long", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.SessionID = strings.Repeat("s", 300) }),
		refusedRedeem("no sign-in time", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.AuthTime = time.Time{} }),
		refusedRedeem("no subject", badCode, session).
			with(func(in *firstadminsvc.RedeemInput) { in.Caller.Subject = "" }),
		refusedRedeem("stale sign-in", stale, firstadminsvc.RuleStaleSignIn).
			with(func(in *firstadminsvc.RedeemInput) { in.AuthTime = clock().Add(-window - time.Second) }),
		refusedRedeem("sign-in after now", stale, firstadminsvc.RuleStaleSignIn).
			with(func(in *firstadminsvc.RedeemInput) { in.AuthTime = clock().Add(time.Second) }),
		refusedRedeem("window of 0", stale, firstadminsvc.RuleStaleSignIn).rig(func(r *rig) { r.window = 0 }),
		refusedRedeem("window of 0 and a sign-in at this instant", stale, firstadminsvc.RuleStaleSignIn).
			rig(func(r *rig) { r.window = 0 }).with(func(in *firstadminsvc.RedeemInput) { in.AuthTime = clock() }),
		refusedRedeem("marker set", firstadmin.ErrAlreadyMade, firstadminsvc.RuleAlreadyMade).
			rig(func(r *rig) { r.store.state = madeState() }),
		refusedRedeem("no open offer", badCode, firstadminsvc.RuleCodeInvalid).
			rig(func(r *rig) { r.store.state = emptyState() }),
		refusedRedeem("offer for another person", badCode, firstadminsvc.RuleCodeInvalid).
			rig(func(r *rig) { r.store.state = openOffer(otherID) }),
		refusedRedeem("state read fails", firstadminsvc.ErrUnavailable, storeErr).
			rig(func(r *rig) { r.store.stateErr = errBoom }),
		refusedRedeem("principal gone", firstadmin.ErrPrincipalInvalid, notAllowed).
			rig(func(r *rig) { delete(r.principals.byID, personID) }),
		refusedRedeem("principal read fails", firstadminsvc.ErrUnavailable, storeErr).
			rig(func(r *rig) { r.principals.err = errBoom }),
		refusedRedeem(
			"session subject is another person",
			firstadmin.ErrPrincipalInvalid,
			firstadminsvc.RuleSubjectMismatch,
		).
			with(func(in *firstadminsvc.RedeemInput) { in.Caller.Subject = "bob@example.org" }),
		refusedRedeem("disabled principal", firstadmin.ErrPrincipalInvalid, notAllowed).
			rig(func(r *rig) {
				p := person(personID, adaName)
				p.DisabledAt = clock()
				r.principals.byID[personID] = p
			}),
		refusedRedeem("temporary administrator principal", firstadmin.ErrPrincipalInvalid, notAllowed).
			rig(func(r *rig) { r.temps.subjects[adaName] = true }),
		refusedRedeem("bad grant ID", firstadminsvc.ErrUnavailable, storeErr).rig(func(r *rig) { r.grantID = "abc" }),
		refusedRedeem("store error", firstadminsvc.ErrUnavailable, storeErr).rig(storeFails(errBoom)).reachesStore(),
		refusedRedeem("store says wrong code", badCode, firstadminsvc.RuleCodeInvalid).
			rig(storeFails(firstadmin.ErrCodeInvalid)).reachesStore(),
		refusedRedeem("store says grant exists", firstadmin.ErrGrantExists, firstadminsvc.RuleGrantExists).
			rig(storeFails(firstadmin.ErrGrantExists)).reachesStore(),
		refusedRedeem("store says stale", stale, firstadminsvc.RuleStaleSignIn).
			rig(storeFails(firstadmin.ErrStaleSignIn)).reachesStore(),
	}
}

// TestServiceHostileRows sends hostile input straight to the service, as if
// the handler and the store checked nothing. Each row names the guard that
// stops it: the error, the failed rule of the failure entry, and whether the
// store may be called. A row without a store call proves that the guard of
// the service stops the input before any port.
func TestServiceHostileRows(t *testing.T) {
	t.Parallel()

	for _, tc := range offerRows() {
		t.Run("offer "+tc.name, func(t *testing.T) {
			t.Parallel()
			runOfferRow(t, tc)
		})
	}

	for _, tc := range redeemRows() {
		t.Run("redeem "+tc.name, func(t *testing.T) {
			t.Parallel()
			runRedeemRow(t, tc)
		})
	}
}

func runOfferRow(t *testing.T, tc offerRow) {
	t.Helper()

	r := newRig(t, tc.mutate)
	in := offerIn()

	if tc.input != nil {
		tc.input(&in)
	}

	res, err := r.svc.Offer(t.Context(), in)
	if !errors.Is(err, tc.want) {
		t.Fatalf("Offer error = %v, want %v", err, tc.want)
	}

	if res.Code != "" {
		t.Error("Offer returned a code with an error, want none")
	}

	if got := r.store.calls() > 0; got != tc.atStore {
		t.Errorf("store written = %t, want %t", got, tc.atStore)
	}

	assertRefused(t, r, tc.rule, string(identity.ActionServerOfferFirstAdministrator))
}

func runRedeemRow(t *testing.T, tc redeemRow) {
	t.Helper()

	r := newRig(t, func(r *rig) {
		r.store.state = openOffer(personID)

		if tc.mutate != nil {
			tc.mutate(r)
		}
	})
	in := redeemIn(t)

	if tc.input != nil {
		tc.input(&in)
	}

	_, err := r.svc.Redeem(t.Context(), in)
	if !errors.Is(err, tc.want) {
		t.Fatalf("Redeem error = %v, want %v", err, tc.want)
	}

	if got := r.store.calls() > 0; got != tc.atStore {
		t.Errorf("store written = %t, want %t", got, tc.atStore)
	}

	assertRefused(t, r, tc.rule, string(identity.ActionCallerRedeemFirstAdministrator))
	assertNoSecret(t, r, in.Code)
}

// assertRefused checks that the refusal wrote one failure entry with the
// failed rule and the action.
func assertRefused(t *testing.T, r *rig, rule, action string) {
	t.Helper()

	entries := r.audit.all()
	if len(entries) != 1 {
		t.Fatalf("failure entries = %d, want 1", len(entries))
	}

	e := entries[0]
	if e.Outcome != audit.OutcomeFailure || e.Action != action || ruleOf(e) != rule {
		t.Errorf("failure entry = outcome %s, action %s, rule %q; want failure, %s, %q",
			e.Outcome, e.Action, ruleOf(e), action, rule)
	}

	if e.Reason != "" {
		t.Errorf("failure entry reason = %q, want none: text of a request never goes in a refusal", e.Reason)
	}
}

// TestRefusalSurvivesAuditFailure checks that a failed append of the failure
// entry does not change the answer: the request is refused, and the failure
// is logged.
func TestRefusalSurvivesAuditFailure(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(r *rig) { r.audit.err = errBoom })
	in := offerIn()
	in.Caller.TokenID = "tok-1"

	if _, err := r.svc.Offer(t.Context(), in); !errors.Is(err, firstadminsvc.ErrNotBootstrap) {
		t.Fatalf("Offer error = %v, want ErrNotBootstrap", err)
	}

	if !strings.Contains(r.logs.String(), "failure entry was not written") {
		t.Errorf("log = %q, want the failed append logged", r.logs.String())
	}
}

// TestRefusalSurvivesCanceledContext checks that the failure entry is
// written when the context of the request has ended.
func TestRefusalSurvivesCanceledContext(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	in := offerIn()
	in.Caller.TokenID = "tok-1"

	if _, err := r.svc.Offer(ctx, in); !errors.Is(err, firstadminsvc.ErrNotBootstrap) {
		t.Fatalf("Offer error = %v, want ErrNotBootstrap", err)
	}

	if n := len(r.audit.all()); n != 1 {
		t.Errorf("failure entries = %d, want 1", n)
	}
}
