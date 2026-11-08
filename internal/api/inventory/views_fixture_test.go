package inventory_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// Tokens of the callers in a view fixture.
const (
	tokenAna      = "ana-token"
	tokenBob      = "bob-token"
	tokenAdmin    = "admin-token"
	tokenService  = "service-token"
	tokenWorkload = "workload-token"
	tokenJob      = "job-token"
	tokenUnknown  = "unknown-kind-token"
	tokenNoRecord = "no-record-token"
	tokenDisabled = "disabled-token"
	tokenOther    = "other-kind-record-token"

	viewsPath = "/api/dustpan/v1/namespaces/acme/views"
	unitsPath = "/api/dustpan/v1/namespaces/acme/units"
)

// viewCallers authenticates the tokens above. Each one is a different kind of
// caller.
type viewCallers struct{}

func (viewCallers) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	p := identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: "", JobID: "",
	}

	switch token {
	case tokenAna:
		p.Subject = "ana"
	case tokenBob:
		p.Subject = "bob"
	case tokenAdmin:
		p.Subject, p.Admin, p.Kind = "root", true, ""
	case tokenService:
		p.Subject, p.Kind = "ci-deploy", identity.KindService
	case tokenWorkload:
		p.Subject, p.Kind = "pod", identity.KindWorkload
	case tokenJob:
		p.Subject, p.JobID = "ana", "job-1"
	case tokenUnknown:
		p.Subject, p.Kind = "ana", "robot"
	case tokenNoRecord:
		p.Subject = "ghost"
	case tokenDisabled:
		p.Subject = "disabled"
	case tokenOther:
		p.Subject = "service-record"
	default:
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return p, nil
}

// viewPeople is the principal directory of a view fixture: the records of the
// people that tokens name.
type viewPeople struct {
	mu      sync.Mutex
	records map[string]principal.Principal
}

func (v *viewPeople) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	rec, ok := v.records[subject]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return rec, nil
}

// replace gives the subject a record with a new ID, as a delete of the
// principal and a new sign-in of the same subject do.
func (v *viewPeople) replace(subject, id string) {
	v.mu.Lock()
	defer v.mu.Unlock()

	rec := v.records[subject]
	rec.ID = id
	v.records[subject] = rec
}

func personRecord(id, subject string) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: id, Subject: subject,
		DisplayName: "", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0,
		BreakGlass: false,
	}
}

// ownerAna and ownerBob are the principal IDs of the two people.
const (
	ownerAna = "prn-anaanaanaanaanaana1"
	ownerBob = "prn-bobbobbobbobbobbob22"
)

// scriptedAuthorizer allows everything except the listed actions and the
// listed unit references for unit:read.
type scriptedAuthorizer struct {
	mu         sync.Mutex
	denyAction map[identity.Action]bool
	denyUnits  map[string]bool
	asked      []recordedCall
}

func (a *scriptedAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.asked = append(a.asked, recordedCall{action: action, object: obj.String()})

	if a.denyAction[action] || (action == identity.ActionUnitRead && a.denyUnits[obj.String()]) {
		return identity.ErrDenied
	}

	return nil
}

func (*scriptedAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// reset forgets the calls that the authorizer recorded.
func (a *scriptedAuthorizer) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.asked = nil
}

// deny makes the authorizer refuse the action, and only that action.
func (a *scriptedAuthorizer) deny(action identity.Action) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.denyAction = map[identity.Action]bool{action: true}
}

// denyUnit makes the authorizer refuse unit:read on the unit reference.
func (a *scriptedAuthorizer) denyUnit(reference string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.denyUnits[reference] = true
}

func (a *scriptedAuthorizer) calls() []recordedCall {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([]recordedCall(nil), a.asked...)
}

// viewFixture is a handler with saved views over a real SQLite store.
type viewFixture struct {
	handler testHandler
	store   *sqlite.Store
	people  *viewPeople
	authz   *scriptedAuthorizer
	// blind is the wrapped saved view store, when the test wrapped it.
	blind savedview.Store
}

type viewFixtureOptions struct {
	// views wraps the saved view store, for a test that needs a misbehaving store.
	views func(savedview.Store) savedview.Store
	// units wraps the inventory reader, for a test that needs to see the store query.
	units func(coreinventory.Reader) coreinventory.Reader
	// noIdempotency builds the handler with no Idempotency-Key middleware.
	noIdempotency bool
}

func newViewFixture(t *testing.T) *viewFixture {
	t.Helper()

	return newViewFixtureWith(t, viewFixtureOptions{views: nil, units: nil, noIdempotency: false})
}

func newViewFixtureWith(t *testing.T, opts viewFixtureOptions) *viewFixture {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	if _, err = namespaces.Create(t.Context(), "acme"); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	views := store.SavedViews()
	if opts.views != nil {
		views = opts.views(views)
	}

	wrapped := views

	service, err := savedview.NewService(savedview.Config{Store: views, Namespaces: store, Now: nil})
	if err != nil {
		t.Fatalf("saved view service: %v", err)
	}

	var idem *idempotent.Middleware

	if !opts.noIdempotency {
		idem, err = idempotent.New(idempotent.Config{
			Store:  store.Idempotency(idempotencytest.TestWrapper(t, 1)),
			Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
		})
		if err != nil {
			t.Fatalf("idempotency middleware: %v", err)
		}
	}

	disabled := personRecord("prn-disableddisabled1", "disabled")
	disabled.DisabledAt = time.UnixMilli(1)

	serviceRecord := personRecord("prn-servicerecord12345", "service-record")
	serviceRecord.Kind = principal.KindService

	people := &viewPeople{mu: sync.Mutex{}, records: map[string]principal.Principal{
		"ana":            personRecord(ownerAna, "ana"),
		"bob":            personRecord(ownerBob, "bob"),
		"disabled":       disabled,
		"service-record": serviceRecord,
	}}
	authz := &scriptedAuthorizer{
		mu: sync.Mutex{}, denyAction: map[identity.Action]bool{}, denyUnits: map[string]bool{}, asked: nil,
	}

	reader := coreinventory.Reader(
		&memReader{entries: map[namespace.Name][]coreinventory.Entry{"acme": labeledEntries()}, err: nil},
	)
	if opts.units != nil {
		reader = opts.units(reader)
	}

	handler, err := newCheckedHandler(t, apiinventory.Config{
		Placements:    nil,
		OutputSharing: nil,
		Outputs:       nil,
		Views:         &apiinventory.ViewsConfig{Service: service, Principals: people, Idempotency: idem},
		Namespaces:    namespaces,
		Units:         reader,
		Deleted:       seededDeleted(),
		History:       nil,
		Lifecycles:    testLifecycles(),
		ReadScope: fixedScope{
			err: nil,
			ids: nil,
			all: true,
		},
		Collections:    nil,
		Locks:          nil,
		Moves:          nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Authenticator:  viewCallers{},
		Authorizer:     authz,
		Audit:          store,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		PublicURL:      "",
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return &viewFixture{handler: handler, store: store, people: people, authz: authz, blind: wrapped}
}

// viewResult is the answer to one request.
type viewResult struct {
	status int
	header http.Header
	body   string
}

// emptyKey makes doKeyed send the Idempotency-Key header with an empty value.
const emptyKey = "<empty>"

// viewCall is one request.
type viewCall struct {
	method string
	target string
	token  string
	body   string
}

// do sends one request and returns the answer.
func (f *viewFixture) do(t *testing.T, method, target, token, body string) viewResult {
	t.Helper()

	return f.send(t, &viewCall{method: method, target: target, token: token, body: body}, "")
}

// doKeyed sends one request with the Idempotency-Key header and returns the
// answer.
func (f *viewFixture) doKeyed(t *testing.T, key string, c *viewCall) viewResult {
	t.Helper()

	return f.send(t, c, key)
}

func (f *viewFixture) send(t *testing.T, c *viewCall, key string) viewResult {
	t.Helper()

	var reader io.Reader = http.NoBody
	if c.body != "" {
		reader = strings.NewReader(c.body)
	}

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.target, reader)
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}

	switch key {
	case "":
	case emptyKey:
		req.Header.Set(keyHeader, "")
	default:
		req.Header.Set(keyHeader, key)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return viewResult{status: rec.Code, header: rec.Header(), body: rec.Body.String()}
}

// createView makes a view for token and returns its ID.
func (f *viewFixture) createView(t *testing.T, token, body string) string {
	t.Helper()

	res := f.do(t, http.MethodPost, viewsPath, token, body)
	if res.status != http.StatusCreated {
		t.Fatalf("create view = %d (%s), want 201", res.status, res.body)
	}

	var doc struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(res.body), &doc); err != nil || doc.ID == "" {
		t.Fatalf("decode created view %q: %v", res.body, err)
	}

	return doc.ID
}

// auditEntries returns the audit entries of acme, oldest first.
func (f *viewFixture) auditEntries(tb testing.TB) []audit.Entry {
	tb.Helper()

	listing, err := f.store.List(tb.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		tb.Fatalf("list audit: %v", err)
	}

	entries := listing.Entries
	slices.SortFunc(entries, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return entries
}

// ownerBlindStore is a saved view store with a bug on purpose: when blind is
// set, a read ignores the owner and returns the view of any owner.
// newOwnerBlindStore wraps inner. The store is honest until setBlind turns the
// bug on.
func newOwnerBlindStore(inner savedview.Store) *ownerBlindStore {
	return &ownerBlindStore{Store: inner, blind: false, last: savedview.View{
		CreatedAt: time.Time{},
		ID:        "",
		Namespace: "",
		Name:      "",
		Owner:     "",
		Query:     savedview.Query{Labels: nil, References: nil},
	}, mu: sync.Mutex{}}
}

type ownerBlindStore struct {
	savedview.Store

	blind bool
	last  savedview.View
	mu    sync.Mutex
}

func (s *ownerBlindStore) Create(
	ctx context.Context, v savedview.View, f namespace.Fence, e audit.Entry,
) error {
	if err := s.Store.Create(ctx, v, f, e); err != nil {
		return err
	}

	s.mu.Lock()
	s.last = v
	s.mu.Unlock()

	return nil
}

func (s *ownerBlindStore) Get(
	ctx context.Context, ns namespace.Name, id savedview.ID, owner string,
) (savedview.View, error) {
	s.mu.Lock()
	last, blind := s.last, s.blind
	s.mu.Unlock()

	if blind && last.ID == id {
		return last, nil
	}

	return s.Store.Get(ctx, ns, id, owner)
}

func (s *ownerBlindStore) List(ctx context.Context, ns namespace.Name, owner string) ([]savedview.View, error) {
	s.mu.Lock()
	last, blind := s.last, s.blind
	s.mu.Unlock()

	if blind {
		return []savedview.View{last}, nil
	}

	return s.Store.List(ctx, ns, owner)
}

//nolint:revive // argument-limit: the signature is the one of savedview.Store.
func (s *ownerBlindStore) Delete(
	ctx context.Context, ns namespace.Name, id savedview.ID, owner string, f namespace.Fence, e audit.Entry,
) error {
	s.mu.Lock()
	last, blind := s.last, s.blind
	s.mu.Unlock()

	// The blind delete would remove the view of ana for bob.
	if blind && last.ID == id {
		return s.Store.Delete(ctx, ns, id, last.Owner, f, e)
	}

	return s.Store.Delete(ctx, ns, id, owner, f, e)
}

// setBlind turns the bug on or off.
func (s *ownerBlindStore) setBlind(blind bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.blind = blind
}

// findBlind returns the blind store of f.
func findBlind(f *viewFixture) (*ownerBlindStore, bool) {
	b, ok := f.blind.(*ownerBlindStore)

	return b, ok
}
