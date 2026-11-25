package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	freezesURL   = "/api/dustpan/v1/namespaces/acme/freezes"
	freezeEndURL = freezesURL + "/current/end"
	// breakBodyFormat is a start body with a breakFreezeReason. The verb
	// fills the reason.
	breakBodyFormat = `{"unit":{"tool":"opentofu","name":"net"},"operation":"plan","pool":"ci",` +
		`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"check",` +
		`"breakFreezeReason":"%s"}`
)

// fakeFreezes is an in-memory job.FreezeStore. Like the store, it refuses a
// second open freeze of a namespace and keeps the audit entry of each write.
type fakeFreezes struct {
	mu      sync.Mutex
	freezes []job.Freeze
	entries []audit.Entry
}

var _ job.FreezeStore = (*fakeFreezes)(nil)

func (f *fakeFreezes) CreateFreeze(_ context.Context, fr job.Freeze, _ namespace.Fence, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := fr.Validate(); err != nil {
		return err
	}

	if f.openIn(fr.Namespace) {
		return fmt.Errorf("%w: a freeze of %s is open", job.ErrFreezeActive, fr.Namespace)
	}

	f.freezes = append(f.freezes, fr)
	f.entries = append(f.entries, e)

	return nil
}

func (f *fakeFreezes) EndFreeze(_ context.Context, id, by string, _ namespace.Fence, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.freezes {
		fr := &f.freezes[i]
		if fr.ID != id {
			continue
		}

		if !fr.EndsAt.IsZero() {
			return job.ErrFinished
		}

		fr.EndsAt = time.Now()
		fr.EndedBy = by
		f.entries = append(f.entries, e)

		return nil
	}

	return job.ErrNotFound
}

func (f *fakeFreezes) ListFreezes(_ context.Context, ns namespace.Name, since time.Time) ([]job.Freeze, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []job.Freeze

	for i := range f.freezes {
		fr := &f.freezes[i]
		if fr.Namespace == ns && (fr.EndsAt.IsZero() || fr.EndsAt.After(since)) {
			out = append(out, *fr)
		}
	}

	return out, nil
}

// openIn reports whether a freeze of ns has no end. The caller holds mu.
func (f *fakeFreezes) openIn(ns namespace.Name) bool {
	for i := range f.freezes {
		fr := &f.freezes[i]
		if fr.Namespace == ns && fr.EndsAt.IsZero() {
			return true
		}
	}

	return false
}

// activeIn reports whether a freeze of ns is active now.
func (f *fakeFreezes) activeIn(ns namespace.Name) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()

	for i := range f.freezes {
		fr := &f.freezes[i]
		if fr.Namespace == ns && fr.ActiveAt(now) {
			return true
		}
	}

	return false
}

// freezeStarter is the job service of these tests. Like the service, it
// refuses a start of a changing phase during an active freeze with
// job.ErrFrozen, unless the request is a break-glass start and the caller holds
// the break-freeze action. A plan changes no resource, so a plan start passes.
// It counts only the starts that it lets through.
type freezeStarter struct {
	*fakeStarter

	freezes *fakeFreezes
	authz   identity.Authorizer
}

func (s freezeStarter) StartOnce(ctx context.Context, a job.Actor, req job.StartRequest) (job.Job, bool, error) {
	var refused job.Job

	ns := req.Unit.Namespace()

	if !s.freezes.activeIn(ns) {
		return s.fakeStarter.StartOnce(ctx, a, req)
	}

	if req.BreakGlass == nil {
		if req.Operation == job.OperationPlan {
			return s.fakeStarter.StartOnce(ctx, a, req)
		}

		return refused, false, fmt.Errorf("%w: a change freeze is active", job.ErrFrozen)
	}

	err := s.authz.Authorize(ctx, a.Principal, identity.ActionNamespaceBreakFreeze, identity.NamespaceObject(ns))
	if err != nil {
		return refused, false, fmt.Errorf("%w: a break-glass needs the break-freeze action", job.ErrFrozen)
	}

	return s.fakeStarter.StartOnce(ctx, a, req)
}

// freezeFixture is a handler with the freeze routes and the fakes behind it.
type freezeFixture struct {
	h       testHandler
	freezes *fakeFreezes
	starter *fakeStarter
	audit   *testAudit
}

// namespaceGrantPrefix marks a hold that stands for a grant on the namespace
// and not on the installation. The server gives the break-freeze action only
// through a grant on the installation, so the fixture drops a namespace grant
// of that action, as the real authorizer does. A plain hold of the action
// stands for a grant on the installation, which reaches each namespace.
const namespaceGrantPrefix = "ns:"

func newFreezeFixture(t *testing.T, holds ...string) freezeFixture {
	t.Helper()

	kept := make([]string, 0, len(holds))

	for _, hold := range holds {
		if rest, ok := strings.CutPrefix(hold, namespaceGrantPrefix); ok {
			if strings.HasPrefix(rest, string(identity.ActionNamespaceBreakFreeze)+" ") {
				continue
			}

			hold = rest
		}

		kept = append(kept, hold)
	}

	authz := newAuthorizer(kept...)
	freezes := &fakeFreezes{mu: sync.Mutex{}, freezes: nil, entries: nil}
	starter := newFakeStarter(t)
	entries := newTestAudit()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     namespaces,
		Jobs:           newFakeJobs(),
		Units:          nil,
		Artifacts:      nil,
		Logs:           nil,
		Cursors:        nil,
		Approvers:      gate.ApproverPolicy{Production: false, IsDev: nil},
		Starter:        freezeStarter{fakeStarter: starter, freezes: freezes, authz: authz},
		Freezes:        freezes,
		Workers:        nil,
		Pools:          nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		Audit:          entries,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
		Now:            nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return freezeFixture{
		h:       testHandler{Handler: handler, served: apidesctest.CheckStatus(t, handler)},
		freezes: freezes,
		starter: starter,
		audit:   entries,
	}
}

// openFreeze stores an open freeze of acme that started an hour ago.
func (f freezeFixture) openFreeze(t *testing.T) {
	t.Helper()

	ns := namespace.Name("acme")

	var entry audit.Entry

	err := f.freezes.CreateFreeze(
		t.Context(),
		job.Freeze{
			ID: ident.NewID("frz-"), Namespace: ns, Reason: "release week",
			StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
		},
		namespace.Fence{Namespace: ns, FailoverVersion: 0},
		entry,
	)
	if err != nil {
		t.Fatalf("open freeze: %v", err)
	}
}

// freezeReq returns a request with the token of ada.
func freezeReq(t *testing.T, method, url, body string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+testToken)

	return req
}

// serve sends req to h and returns the answer.
func serveFreeze(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// freezeRequest sends one request with the token of ada.
func freezeRequest(t *testing.T, h http.Handler, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()

	return serveFreeze(h, freezeReq(t, method, url, body))
}

// freezeWire is the document of a freeze in the answer.
type freezeWire struct {
	ID        string `json:"id"`
	Namespace string `json:"namespace"`
	Reason    string `json:"reason"`
	CreatedBy string `json:"createdBy"`
	EndedBy   string `json:"endedBy"`
	Active    bool   `json:"active"`
}

// listWire is the document of the freeze list in the answer.
type listWire struct {
	Freezes       []freezeWire `json:"freezes"`
	NextPageToken string       `json:"nextPageToken"`
}

func decodeFreeze(t *testing.T, body string) freezeWire {
	t.Helper()

	var out freezeWire
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("freeze body %s: %v", body, err)
	}

	return out
}

func decodeList(t *testing.T, body string) []freezeWire {
	t.Helper()

	page := decodeListPage(t, body)

	return page.Freezes
}

func decodeListPage(t *testing.T, body string) listWire {
	t.Helper()

	var out listWire
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("list body %s: %v", body, err)
	}

	return out
}

// checkCreated creates the freeze of ada and checks the answer and the audit
// entry of the create.
func checkCreated(t *testing.T, f freezeFixture) {
	t.Helper()

	rec := freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"release week"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", rec.Code, rec.Body.String())
	}

	created := decodeFreeze(t, rec.Body.String())
	if created.CreatedBy != "ada" || created.Reason != "release week" || !created.Active {
		t.Fatalf("created freeze = %+v, want ada's active freeze with the reason", created)
	}

	if len(f.audit.entries) != 0 || len(f.freezes.entries) != 1 {
		t.Fatalf("freeze entries = %d, audit entries = %d, want 1 and 0", len(f.freezes.entries), len(f.audit.entries))
	}

	e := f.freezes.entries[0]
	if e.Subject != "ada" || e.Reason != "release week" {
		t.Fatalf("audit entry = subject %q reason %q, want ada and the reason", e.Subject, e.Reason)
	}
}

// checkListAndEnd lists the active freeze, ends it, and checks that the next
// start is no longer refused.
func checkListAndEnd(t *testing.T, f freezeFixture) {
	t.Helper()

	rec := freezeRequest(t, f.h, http.MethodGet, freezesURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", rec.Code, rec.Body.String())
	}

	listed := decodeList(t, rec.Body.String())
	if len(listed) != 1 || listed[0].Reason != "release week" || listed[0].CreatedBy != "ada" || !listed[0].Active {
		t.Fatalf("list = %+v, want the one active freeze of ada", listed)
	}

	rec = freezeRequest(t, f.h, http.MethodPost, freezeEndURL, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("end = %d %s, want 200", rec.Code, rec.Body.String())
	}

	if ended := decodeFreeze(t, rec.Body.String()); ended.Active || ended.EndedBy != "ada" {
		t.Fatalf("ended freeze = %+v, want inactive and ended by ada", ended)
	}

	if len(f.freezes.entries) != 2 || f.freezes.entries[1].ObjectID == "" {
		t.Fatalf("freeze entries after the end = %+v, want a create and an end", f.freezes.entries)
	}

	rec = post(t, f.h, testToken, goodBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start after the end = %d %s, want 201", rec.Code, rec.Body.String())
	}
}

// TestFreezeCreateThenJobStartRefused runs the freeze path in order: create, a
// plan start during the freeze, break-glass start, second create, list, end,
// and start again. A plan start is accepted during a freeze; the refusal of a
// start of a changing phase is proven below this API by the job service tests.
func TestFreezeCreateThenJobStartRefused(t *testing.T) {
	t.Parallel()

	// The plain break-freeze hold is the grant on the installation.
	f := newFreezeFixture(t,
		string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme",
		string(identity.ActionNamespaceBreakFreeze)+" acme",
	)

	checkCreated(t, f)

	rec := post(t, f.h, testToken, goodBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("plan start during the freeze = %d %s, want 201", rec.Code, rec.Body.String())
	}

	if f.starter.calls() != 1 {
		t.Fatalf("starts during the freeze = %d, want 1", f.starter.calls())
	}

	rec = post(t, f.h, testToken, fmt.Sprintf(breakBodyFormat, "hotfix"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("break-glass start = %d %s, want 201", rec.Code, rec.Body.String())
	}

	if f.starter.calls() != 2 || f.starter.requests[1].BreakGlass == nil ||
		f.starter.requests[1].BreakGlass.Reason != "hotfix" {
		t.Fatalf("break-glass request = %+v, want a second start with the reason hotfix", f.starter.requests)
	}

	rec = freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"second"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second create = %d %s, want 409", rec.Code, rec.Body.String())
	}

	checkListAndEnd(t, f)
}

// freezeCase is one request of TestFreezeRoutes, sent to a fresh handler.
type freezeCase struct {
	name       string
	holds      []string
	open       bool
	method     string
	url        string
	body       string
	key        bool
	wantStatus int
	wantCode   string
	wantText   string
}

// TestFreezeRoutes checks each answer of the freeze routes and the start
// refusals during a freeze, with one request for each case.
func TestFreezeRoutes(t *testing.T) {
	t.Parallel()

	read := string(identity.ActionNamespaceRead) + " acme"
	manage := string(identity.ActionNamespaceManageFreezes) + " acme"
	breakFreeze := string(identity.ActionNamespaceBreakFreeze) + " acme"
	nsBreakFreeze := namespaceGrantPrefix + breakFreeze
	longReason := strings.Repeat("r", 501)

	tests := []freezeCase{
		{
			name: "create answers 201", holds: []string{read, manage}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"release week"}`, key: false,
			wantStatus: http.StatusCreated, wantCode: "", wantText: "",
		},
		{
			name: "create without the freeze action is refused", holds: []string{read}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"release week"}`, key: false,
			wantStatus: http.StatusForbidden, wantCode: "", wantText: "",
		},
		{
			name: "create with an empty reason is refused", holds: []string{read, manage}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":""}`, key: false,
			wantStatus: http.StatusBadRequest, wantCode: "job.invalid", wantText: "1 to 500 characters",
		},
		{
			name: "create with a reason of 501 characters is refused", holds: []string{read, manage}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"` + longReason + `"}`, key: false,
			wantStatus: http.StatusBadRequest, wantCode: "job.invalid", wantText: "1 to 500 characters",
		},
		{
			name: "create with an idempotency key is refused", holds: []string{read, manage}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"release week"}`, key: true,
			wantStatus: http.StatusBadRequest, wantCode: "", wantText: "",
		},
		{
			name: "second create is refused", holds: []string{read, manage}, open: true,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"second"}`, key: false,
			wantStatus: http.StatusConflict, wantCode: "job.frozen", wantText: "",
		},
		{
			name: "end with none active is refused", holds: []string{read, manage}, open: false,
			method: http.MethodPost, url: freezeEndURL, body: "", key: false,
			wantStatus: http.StatusNotFound, wantCode: "job.not-found", wantText: "no change freeze",
		},
		{
			name: "end without the freeze action is refused", holds: []string{read}, open: true,
			method: http.MethodPost, url: freezeEndURL, body: "", key: false,
			wantStatus: http.StatusForbidden, wantCode: "", wantText: "",
		},
		{
			name: "end ends the active freeze", holds: []string{read, manage}, open: true,
			method: http.MethodPost, url: freezeEndURL, body: "", key: false,
			wantStatus: http.StatusOK, wantCode: "", wantText: "",
		},
		{
			name: "list shows the active freeze", holds: []string{read}, open: true,
			method: http.MethodGet, url: freezesURL, body: "", key: false,
			wantStatus: http.StatusOK, wantCode: "", wantText: "release week",
		},
		{
			// A namespace the caller may not read answers the same 404 as an unknown one.
			name: "list without read is refused", holds: nil, open: false,
			method: http.MethodGet, url: freezesURL, body: "", key: false,
			wantStatus: http.StatusNotFound, wantCode: "job.not-found", wantText: "",
		},
		{
			name: "plan start during a freeze is accepted", holds: []string{read}, open: true,
			method: http.MethodPost, url: jobsURL, body: goodBody, key: false,
			wantStatus: http.StatusCreated, wantCode: "", wantText: "",
		},
		{
			name: "break-glass start with the action is accepted", holds: []string{read, breakFreeze}, open: true,
			method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, "hotfix"), key: false,
			wantStatus: http.StatusCreated, wantCode: "", wantText: "",
		},
		{
			name: "break-glass start without the action is refused", holds: []string{read}, open: true,
			method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, "hotfix"), key: false,
			wantStatus: http.StatusConflict, wantCode: "job.frozen", wantText: "",
		},
		{
			name: "break-glass start with an empty reason is refused", holds: []string{read, breakFreeze},
			open: true, method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, ""), key: false,
			wantStatus: http.StatusBadRequest, wantCode: "job.invalid", wantText: "",
		},
		{
			name: "break-glass start with a reason of 501 characters is refused", holds: []string{read, breakFreeze},
			open: true, method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, longReason),
			key: false, wantStatus: http.StatusBadRequest, wantCode: "job.invalid", wantText: "1 to 500 characters",
		},
		{
			// The server gives break-freeze through the installation only.
			name: "break-glass start with a namespace grant is refused", holds: []string{read, nsBreakFreeze},
			open: true, method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, "hotfix"), key: false,
			wantStatus: http.StatusConflict, wantCode: "job.frozen", wantText: "",
		},
		{
			name: "break-glass start with manage-freezes only is refused", holds: []string{read, manage},
			open: true, method: http.MethodPost, url: jobsURL, body: fmt.Sprintf(breakBodyFormat, "hotfix"), key: false,
			wantStatus: http.StatusConflict, wantCode: "job.frozen", wantText: "",
		},
		{
			name: "create with break-freeze only is refused", holds: []string{read, breakFreeze}, open: false,
			method: http.MethodPost, url: freezesURL, body: `{"reason":"release week"}`, key: false,
			wantStatus: http.StatusForbidden, wantCode: "", wantText: "",
		},
		{
			name: "end with break-freeze only is refused", holds: []string{read, breakFreeze}, open: true,
			method: http.MethodPost, url: freezeEndURL, body: "", key: false,
			wantStatus: http.StatusForbidden, wantCode: "", wantText: "",
		},
		{
			name: "list of an unknown namespace is refused", holds: []string{read}, open: false,
			method: http.MethodGet, url: "/api/dustpan/v1/namespaces/unknown/freezes", body: "", key: false,
			wantStatus: http.StatusNotFound, wantCode: "job.not-found", wantText: "",
		},
		{
			name: "list of a namespace with SQL text is refused", holds: []string{read}, open: false,
			method: http.MethodGet, url: "/api/dustpan/v1/namespaces/acme%27%20OR%201%3D1%20--/freezes", body: "",
			key: false, wantStatus: http.StatusNotFound, wantCode: "job.not-found", wantText: "",
		},
		{
			name: "list of a malformed namespace ID is refused", holds: []string{read}, open: false,
			method: http.MethodGet, url: "/api/dustpan/v1/namespaces/%00%2e%2e/freezes", body: "", key: false,
			wantStatus: http.StatusNotFound, wantCode: "job.not-found", wantText: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFreezeFixture(t, tt.holds...)
			if tt.open {
				f.openFreeze(t)
			}

			req := freezeReq(t, tt.method, tt.url, tt.body)
			if tt.key {
				req.Header.Set("Idempotency-Key", "freeze-key-1")
			}

			rec := serveFreeze(f.h, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
			}

			if tt.wantCode != "" || tt.wantText != "" {
				checkErrorBody(t, rec.Body.String(), tt.wantCode, tt.wantText)
			}
		})
	}
}

func TestFreezeListPaging(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	for _, reason := range []string{"first", "second", "third"} {
		rec := freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"`+reason+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q = %d %s, want 201", reason, rec.Code, rec.Body.String())
		}

		rec = freezeRequest(t, f.h, http.MethodPost, freezeEndURL, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("end %q = %d %s, want 200", reason, rec.Code, rec.Body.String())
		}
	}

	first := checkFreezePage(t, f, "", "first")
	if first.NextPageToken == "" {
		t.Fatal("the first page has no page token")
	}

	second := checkFreezePage(t, f, first.NextPageToken, "second")
	if second.NextPageToken == "" {
		t.Fatal("the second page has no page token")
	}

	last := checkFreezePage(t, f, second.NextPageToken, "third")
	if last.NextPageToken != "" {
		t.Fatalf("the last page has a page token: %s", last.NextPageToken)
	}

	rec := freezeRequest(t, f.h, http.MethodGet, freezesURL+"?pageSize=1000", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a large limit = %d %s, want 400", rec.Code, rec.Body.String())
	}

	checkErrorBody(t, rec.Body.String(), "request.invalid-query", "")
}

// freezeTokenForTest is the wire shape of the page token of the freeze list.
type freezeTokenForTest struct {
	After       string `json:"f"`
	Fingerprint string `json:"n"`
}

func encodeFreezeToken(t *testing.T, after, fingerprintInput string) string {
	t.Helper()

	wire := freezeTokenForTest{After: after, Fingerprint: pagetoken.Fingerprint(fingerprintInput)}

	token, err := pagetoken.Encode(wire)
	if err != nil {
		t.Fatalf("encode page token: %v", err)
	}

	return token
}

// Layer: the page-token guard of the freeze list. Each hostile token answers
// 400 and no freeze. Remove the guard and a token of another namespace or with
// an unknown freeze ID gives a page.
func TestFreezeListRefusesHostilePageTokens(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	for _, reason := range []string{"first", "second"} {
		rec := freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"`+reason+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q = %d %s, want 201", reason, rec.Code, rec.Body.String())
		}

		if rec = freezeRequest(t, f.h, http.MethodPost, freezeEndURL, ""); rec.Code != http.StatusOK {
			t.Fatalf("end %q = %d %s, want 200", reason, rec.Code, rec.Body.String())
		}
	}

	firstID := decodeList(t, freezeRequest(t, f.h, http.MethodGet, freezesURL, "").Body.String())[0].ID

	tests := []struct {
		name  string
		token string
	}{
		{name: "malformed token", token: "not-a-token"},
		{name: "token of another namespace", token: encodeFreezeToken(t, firstID, "ns=other")},
		{name: "token with an unknown freeze ID", token: encodeFreezeToken(t, "frz-unknown", "ns=acme")},
		{name: "token with SQL text as the ID", token: encodeFreezeToken(t, "frz-1' OR 1=1 --", "ns=acme")},
		{name: "token with no freeze ID", token: encodeFreezeToken(t, "", "ns=acme")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A page token is base64url, so the URL needs no escape.
			rec := freezeRequest(t, f.h, http.MethodGet, freezesURL+"?pageSize=1&pageToken="+tt.token, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("list = %d %s, want 400", rec.Code, rec.Body.String())
			}

			checkErrorBody(t, rec.Body.String(), "request.page-token-invalid", "")

			if body := rec.Body.String(); strings.Contains(body, "first") || strings.Contains(body, "second") {
				t.Fatalf("the refused list holds a freeze: %s", body)
			}
		})
	}
}

func checkFreezePage(t *testing.T, f freezeFixture, token, wantReason string) listWire {
	t.Helper()

	url := freezesURL + "?pageSize=1"
	if token != "" {
		url += "&pageToken=" + token
	}

	page := decodeListPage(t, freezeRequest(t, f.h, http.MethodGet, url, "").Body.String())
	if len(page.Freezes) != 1 || page.Freezes[0].Reason != wantReason {
		t.Fatalf("page of %q = %+v, want one freeze %q", token, page, wantReason)
	}

	return page
}

func TestFreezeReasonIsEncoded(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	reason := `<script>alert(1)</script>`
	body, err := json.Marshal(freezeBodyForTest(reason))
	if err != nil {
		t.Fatalf("marshal the reason: %v", err)
	}

	rec := freezeRequest(t, f.h, http.MethodPost, freezesURL, string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", rec.Code, rec.Body.String())
	}

	listed := freezeRequest(t, f.h, http.MethodGet, freezesURL, "").Body.String()
	if !strings.Contains(listed, `\u003cscript\u003e`) || strings.Contains(listed, `<script>`) {
		t.Fatalf("the list does not encode the reason: %s", listed)
	}
}

func freezeBodyForTest(reason string) map[string]string {
	return map[string]string{"reason": reason}
}

func TestFreezeRejectsLargeBody(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	reason := strings.Repeat("x", 1<<20)
	rec := freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"`+reason+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a 1 MB reason = %d %s, want 400", rec.Code, rec.Body.String())
	}

	if len(f.freezes.freezes) != 0 {
		t.Fatalf("freezes after the refused create = %d, want 0", len(f.freezes.freezes))
	}
}

func TestFreezeUnknownNamespaceIsRefused(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	rec := freezeRequest(t, f.h, http.MethodPost,
		"/api/dustpan/v1/namespaces/unknown/freezes", `{"reason":"release week"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("create on an unknown namespace = %d %s, want 404", rec.Code, rec.Body.String())
	}

	if len(f.freezes.freezes) != 0 {
		t.Fatalf("freezes after the refused create = %d, want 0", len(f.freezes.freezes))
	}
}

func TestFreezeSQLNamespaceMatchesNoRow(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	url := "/api/dustpan/v1/namespaces/acme%27%20OR%201%3D1%20--/freezes"
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := freezeRequest(t, f.h, method, url, `{"reason":"release week"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s with SQL in the namespace = %d %s, want 404", method, rec.Code, rec.Body.String())
		}
	}

	if len(f.freezes.freezes) != 0 {
		t.Fatalf("freezes after the hostile names = %d, want 0", len(f.freezes.freezes))
	}
}

func TestFreezeConcurrentCreates(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionNamespaceManageFreezes)+" acme")

	answers := make(chan int, 2)
	for range 2 {
		go func() {
			answers <- freezeRequest(t, f.h, http.MethodPost, freezesURL, `{"reason":"release week"}`).Code
		}()
	}

	var created, refused int
	for range 2 {
		switch code := <-answers; code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			refused++
		default:
			t.Fatalf("concurrent create = %d, want 201 or 409", code)
		}
	}

	if created != 1 || refused != 1 {
		t.Fatalf("concurrent creates = %d created and %d refused, want one of each", created, refused)
	}

	listed := decodeList(t, freezeRequest(t, f.h, http.MethodGet, freezesURL, "").Body.String())
	if len(listed) != 1 || !listed[0].Active {
		t.Fatalf("freezes after the race = %+v, want one active freeze", listed)
	}
}

func TestFreezeRefusalsWriteAuditEntries(t *testing.T) {
	t.Parallel()

	holds := []string{
		string(identity.ActionNamespaceRead) + " acme",
		string(identity.ActionNamespaceManageFreezes) + " acme",
	}

	cases := []struct {
		name, method, url, body string
		want                    int
	}{
		{"end with none active", http.MethodPost, freezeEndURL, "", http.StatusNotFound},
		{"invalid reason", http.MethodPost, freezesURL, `{"reason":""}`, http.StatusBadRequest},
		{"second create", http.MethodPost, freezesURL, `{"reason":"second"}`, http.StatusConflict},
		{"malformed body", http.MethodPost, freezesURL, `{"reason":`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, freezesURL, `{"reason":"x","extra":1}`, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFreezeFixture(t, holds...)
			if tc.name == "second create" {
				f.openFreeze(t)
			}

			rec := freezeRequest(t, f.h, tc.method, tc.url, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.url, rec.Code, rec.Body.String(), tc.want)
			}

			checkFreezeDenial(t, f)
		})
	}
}

func checkFreezeDenial(t *testing.T, f freezeFixture) {
	t.Helper()

	page := audit.Page{Size: 0, Before: 0}
	entries, err := f.audit.List(t.Context(), namespace.Name("acme"), page)
	if err != nil {
		t.Fatalf("List audit = %v", err)
	}

	if len(entries.Entries) != 1 {
		t.Fatalf("denial entries = %d, want 1", len(entries.Entries))
	}

	e := entries.Entries[0]
	if e.Outcome != audit.OutcomeDenied || e.Action != string(identity.ActionNamespaceManageFreezes) ||
		e.Reason == "" {
		t.Fatalf("denial entry = %+v, want a named denial of freeze management", e)
	}
}

func TestFreezeKeyRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, url string }{{"create", freezesURL}, {"end", freezeEndURL}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
				string(identity.ActionNamespaceManageFreezes)+" acme")
			f.openFreeze(t)

			req := freezeReq(t, http.MethodPost, tc.url, `{"reason":"release week"}`)
			req.Header.Set("Idempotency-Key", "freeze-key-1")

			rec := serveFreeze(f.h, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s with a key = %d %s, want 400", tc.name, rec.Code, rec.Body.String())
			}

			checkFreezeDenial(t, f)
		})
	}
}

// A break-glass start whose reason is not valid writes a denial entry, like
// each other refusal of a start.
func TestBreakGlassReasonRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	for name, reason := range map[string]string{"empty": "", "501 characters": strings.Repeat("r", 501)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme",
				string(identity.ActionNamespaceBreakFreeze)+" acme")
			f.openFreeze(t)

			rec := post(t, f.h, testToken, fmt.Sprintf(breakBodyFormat, reason))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("break-glass start with a %s reason = %d %s, want 400", name, rec.Code, rec.Body.String())
			}

			entries, err := f.audit.List(t.Context(), namespace.Name("acme"), audit.Page{Size: 0, Before: 0})
			if err != nil {
				t.Fatalf("List audit = %v", err)
			}

			if len(entries.Entries) != 1 || entries.Entries[0].Outcome != audit.OutcomeDenied ||
				entries.Entries[0].Reason == "" {
				t.Fatalf("audit entries = %+v, want one named denial", entries.Entries)
			}

			if f.starter.calls() != 0 {
				t.Fatalf("starts after the refused reason = %d, want 0", f.starter.calls())
			}
		})
	}
}

func TestBreakGlassRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	f := newFreezeFixture(t, string(identity.ActionNamespaceRead)+" acme")
	f.openFreeze(t)

	rec := post(t, f.h, testToken, fmt.Sprintf(breakBodyFormat, "hotfix"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("break-glass start = %d %s, want 409", rec.Code, rec.Body.String())
	}

	page := audit.Page{Size: 0, Before: 0}
	entries, err := f.audit.List(t.Context(), namespace.Name("acme"), page)
	if err != nil {
		t.Fatalf("List audit = %v", err)
	}

	if len(entries.Entries) != 1 || !strings.Contains(entries.Entries[0].Reason, reasonFrozenText) {
		t.Fatalf("audit entries = %+v, want one denial of a frozen start", entries.Entries)
	}
}

const reasonFrozenText = "a change freeze is active"

func TestStartRequestBreakFreezeReasonIsOptional(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../apidesc/openapi.json")
	if err != nil {
		t.Fatalf("read the API description: %v", err)
	}

	var document struct {
		Components json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode the API description: %v", err)
	}

	var components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	}
	if err := json.Unmarshal(document.Components, &components); err != nil {
		t.Fatalf("decode the components: %v", err)
	}

	var startRequest struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(components.Schemas["StartJobRequest"], &startRequest); err != nil {
		t.Fatalf("decode StartJobRequest: %v", err)
	}

	for _, field := range startRequest.Required {
		if field == "breakFreezeReason" {
			t.Fatal("breakFreezeReason is required in the API description")
		}
	}
}
