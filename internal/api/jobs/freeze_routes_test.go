package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/ident"
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

func newFreezeFixture(t *testing.T, holds ...string) freezeFixture {
	t.Helper()

	authz := newAuthorizer(holds...)
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
	Freezes []freezeWire `json:"freezes"`
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

	var out listWire
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("list body %s: %v", body, err)
	}

	return out.Freezes
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
