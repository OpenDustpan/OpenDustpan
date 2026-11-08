package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The principal records of the floor tests.
const (
	floorIDA      = "prn-floor-a"
	floorIDB      = "prn-floor-b"
	floorIDX      = "prn-floor-x"
	floorSubjectA = "a@example.com"
	floorSubjectB = "b@example.com"
	floorSubjectX = "x@example.com"
)

// floorClock returns the time of the floor tests.
func floorClock() time.Time {
	return time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
}

// floorIdentity returns the caller that the token authenticates.
func floorIdentity(subject, displayName string) identity.Principal {
	return identity.Principal{
		Subject:                subject,
		DisplayName:            displayName,
		Admin:                  false,
		TokenID:                "",
		TokenExpiresAt:         time.Time{},
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Kind:                   identity.KindPerson,
		Credential:             "",
		JobID:                  "",
	}
}

// floorAuth signs each token in as the principal that the map holds.
type floorAuth map[string]identity.Principal

func (a floorAuth) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	p, ok := a[token]
	if !ok {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return p, nil
}

// floorStore returns a principal service and its SQLite store with the clock
// of the floor tests.
func floorStore(t *testing.T) (*principal.Service, *sqlite.Store) {
	t.Helper()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      allowAllGuard{},
		Namespaces: nil,
		Tokens:     nil,
		Now:        floorClock,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	return svc, st
}

// floorPerson is one principal that a floor test seeds.
type floorPerson struct {
	ID          string
	Subject     string
	DisplayName string
	BreakGlass  bool
}

// addFloorPrincipal stores an installation person with the given text. The
// store writes the text as given, so a test can seed hostile text.
func addFloorPrincipal(t *testing.T, st *sqlite.Store, p floorPerson) {
	t.Helper()

	record := principal.Principal{
		CreatedAt: floorClock(), UpdatedAt: floorClock(), DisabledAt: time.Time{},
		ID: p.ID, Subject: p.Subject, DisplayName: p.DisplayName, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: p.BreakGlass,
	}

	err := st.CreatePrincipal(t.Context(), record, storetest.ServerChange(floorClock(), "principal:"+p.ID))
	if err != nil {
		t.Fatalf("create principal %q: %v", p.ID, err)
	}
}

// addFloorSiteAdministrator stores a person and a grant of the
// site-administrator role on the installation.
func addFloorSiteAdministrator(t *testing.T, st *sqlite.Store, p floorPerson) {
	t.Helper()

	addFloorPrincipal(t, st, p)

	grant := access.Grant{
		CreatedAt: floorClock(), Condition: nil, Object: identity.ServerObject(), ID: "grt-" + p.ID,
		Role: access.RoleSiteAdministrator, CreatedBy: "test",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: p.ID},
	}

	err := st.CreateGrant(
		t.Context(),
		grant,
		namespace.Fence{Namespace: "", FailoverVersion: 0},
		storetest.ServerChange(floorClock(), "grant:grt-"+p.ID),
		nil,
	)
	if err != nil {
		t.Fatalf("create grant of %q: %v", p.ID, err)
	}
}

// floorCall sends one request with the token of the caller.
func floorCall(t *testing.T, h testHandler, method, path, token string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// floorPath returns the path of one change of the principal id.
func floorPath(method, id string) string {
	if method == http.MethodDelete {
		return "/api/server/principals/" + id
	}

	return "/api/server/principals/" + id + "/disable"
}

// floorGet returns the principal of id.
func floorGet(t *testing.T, h testHandler, id, token string) breakGlassAnswer {
	t.Helper()

	status, body := floorCall(t, h, http.MethodGet, "/api/server/principals/"+id, token)
	if status != http.StatusOK {
		t.Fatalf("get principal %q answers %d %q, want 200", id, status, body)
	}

	var got breakGlassAnswer

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode principal %q: %v", id, err)
	}

	return got
}

// floorEntries returns the entries of a change with the outcome and object.
func floorEntries(
	t *testing.T, st *sqlite.Store, action identity.Action, outcome audit.Outcome, object string,
) []audit.Entry {
	t.Helper()

	entries := serverEntries(t, st)
	out := make([]audit.Entry, 0, len(entries))

	for i := range entries {
		e := &entries[i]
		if e.Action == string(action) && e.Outcome == outcome && e.Object == object {
			out = append(out, *e)
		}
	}

	return out
}

// floorCase is one row of the floor test.
type floorCase struct {
	name       string
	method     string
	caller     string
	second     bool
	auditFails bool
	wantRefuse bool
}

// TestBreakGlassFloorRefusalAtTheAPI proves that a disable and a delete that
// the break-glass floor refuses answer 409 with the catalog code, keep the
// account as it was, and write one denied entry and no failure entry. A
// change that leaves another enabled break-glass account works. A lost
// denial entry still refuses with one ERROR log record.
func TestBreakGlassFloorRefusalAtTheAPI(t *testing.T) {
	t.Parallel()

	cases := []floorCase{
		{
			name:       "X disables A",
			method:     http.MethodPost,
			caller:     "x-token",
			second:     false,
			auditFails: false,
			wantRefuse: true,
		},
		{
			name:       "X deletes A",
			method:     http.MethodDelete,
			caller:     "x-token",
			second:     false,
			auditFails: false,
			wantRefuse: true,
		},
		{
			name:       "A disables itself",
			method:     http.MethodPost,
			caller:     "a-token",
			second:     false,
			auditFails: false,
			wantRefuse: true,
		},
		{
			name:       "A deletes itself",
			method:     http.MethodDelete,
			caller:     "a-token",
			second:     false,
			auditFails: false,
			wantRefuse: true,
		},
		{
			name:       "X disables A while B is enabled",
			method:     http.MethodPost,
			caller:     "x-token",
			second:     true,
			auditFails: false,
			wantRefuse: false,
		},
		{
			name:       "X disables A when the audit write fails",
			method:     http.MethodPost,
			caller:     "x-token",
			second:     false,
			auditFails: true,
			wantRefuse: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runFloorCase(t, tc)
		})
	}
}

// runFloorCase runs one row of the floor test.
func runFloorCase(t *testing.T, tc floorCase) {
	t.Helper()

	svc, st := floorStore(t)
	seedFloorCase(t, st, tc)

	h, recorder, failures := floorHandler(t, svc, st, tc)

	status, body := floorCall(t, h, tc.method, floorPath(tc.method, floorIDA), tc.caller)
	result := floorResult{body: body, status: status}

	if !tc.wantRefuse {
		if result.status != http.StatusOK {
			t.Fatalf("%s answers %d %q, want 200", tc.name, result.status, result.body)
		}

		if got := floorGet(t, h, floorIDA, tc.caller); got.DisabledAt == nil {
			t.Fatalf("%s left A enabled, want A disabled", tc.name)
		}

		return
	}

	assertFloorRefused(t, h, st, tc, result)
	assertFloorAuditFailure(t, recorder, *failures, tc)
}

// floorResult is the answer of one floor row.
type floorResult struct {
	body   string
	status int
}

// seedFloorCase seeds A, X and, when the row asks for it, B.
func seedFloorCase(t *testing.T, st *sqlite.Store, tc floorCase) {
	t.Helper()

	addFloorPrincipal(t, st, floorPerson{
		ID: floorIDA, Subject: floorSubjectA, DisplayName: "Account A", BreakGlass: true,
	})

	if tc.second {
		addFloorPrincipal(t, st, floorPerson{
			ID: floorIDB, Subject: floorSubjectB, DisplayName: "Account B", BreakGlass: true,
		})
	}

	addFloorSiteAdministrator(t, st, floorPerson{
		ID: floorIDX, Subject: floorSubjectX, DisplayName: "Site administrator X", BreakGlass: false,
	})
}

// floorHandler returns the handler of one floor row, its log recorder and the
// count of the audit-failure hook.
func floorHandler(
	t *testing.T, svc *principal.Service, st *sqlite.Store, tc floorCase,
) (testHandler, *floorLogRecorder, *int) {
	t.Helper()

	var (
		auditStore audit.ServerStore = st
		recorder                     = newFloorLogRecorder()
		failures                     = new(int)
		hook       func()
	)

	if tc.auditFails {
		auditStore = failingServerAppend{Store: st}
		hook = func() { *failures++ }
	}

	h := newReachPrincipalHandlerWith(t, svc, auditStore, reachConfigOf(svc), principalHandlerOptions{
		now: floorClock,
		authenticator: floorAuth{
			"a-token": floorIdentity(floorSubjectA, "Account A"),
			"x-token": floorIdentity(floorSubjectX, "Site administrator X"),
		},
		logger:         slog.New(recorder),
		onAuditFailure: hook,
	})

	return h, recorder, failures
}

// assertFloorRefused checks the refusal of one floor row.
func assertFloorRefused(t *testing.T, h testHandler, st *sqlite.Store, tc floorCase, result floorResult) {
	t.Helper()

	if result.status != http.StatusConflict {
		t.Fatalf("%s answers %d %q, want 409", tc.name, result.status, result.body)
	}

	var answer errorAnswer
	if err := json.Unmarshal([]byte(result.body), &answer); err != nil {
		t.Fatalf("decode the refusal: %v", err)
	}

	if answer.Code != http.StatusConflict || answer.ErrorCode != errcode.PrincipalNoWorkingBreakGlass {
		t.Fatalf("%s answers %+v, want 409 with the code %q", tc.name, answer, errcode.PrincipalNoWorkingBreakGlass)
	}

	if got := floorGet(t, h, floorIDA, tc.caller); got.DisabledAt != nil {
		t.Fatalf("%s disabled A at %q, want A enabled", tc.name, *got.DisabledAt)
	}

	if tc.auditFails {
		return
	}

	action := identity.ActionServerManagePrincipals
	object := principal.Ref(floorIDA)

	denied := floorEntries(t, st, action, audit.OutcomeDenied, object)
	if len(denied) != 1 {
		t.Fatalf("%s writes %d denied entries for %q, want 1: %+v", tc.name, len(denied), object, denied)
	}

	if denied[0].Kind != audit.KindChange || denied[0].Namespace != audit.ServerScope ||
		denied[0].Reason != errcode.PrincipalNoWorkingBreakGlass {
		t.Fatalf("%s writes the denied entry %+v, want kind change, scope @server and reason %q",
			tc.name, denied[0], errcode.PrincipalNoWorkingBreakGlass)
	}

	if failed := floorEntries(t, st, action, audit.OutcomeFailure, object); len(failed) != 0 {
		t.Fatalf("%s writes %d failure entries for %q, want 0", tc.name, len(failed), object)
	}
}

// assertFloorAuditFailure checks the log record and the hook of a lost denial
// entry.
func assertFloorAuditFailure(t *testing.T, recorder *floorLogRecorder, failures int, tc floorCase) {
	t.Helper()

	if !tc.auditFails {
		return
	}

	records := recorder.errorRecords()
	if len(records) != 1 {
		t.Fatalf("the lost denial entry logs %d ERROR records, want 1: %+v", len(records), records)
	}

	if !strings.Contains(records[0].message, "break-glass refusal") {
		t.Fatalf("the lost denial entry logs %q, want a record about the failed break-glass refusal audit write",
			records[0].message)
	}

	if failures != 1 {
		t.Fatalf("the lost denial entry calls the audit-failure hook %d times, want 1", failures)
	}
}

// failingServerAppend is an audit store whose AppendServer fails.
type failingServerAppend struct {
	*sqlite.Store
}

var errFloorAuditAppend = errors.New("append the server entry: disk I/O error at /var/lib/secret")

func (failingServerAppend) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errFloorAuditAppend
}

// floorLogRecord is one record of the floor log recorder.
type floorLogRecord struct {
	message string
	level   slog.Level
}

// floorLogRecorder keeps the records of a slog handler.
type floorLogRecorder struct {
	mu      sync.Mutex
	records []floorLogRecord
}

// newFloorLogRecorder returns an empty recorder.
func newFloorLogRecorder() *floorLogRecorder {
	return &floorLogRecorder{mu: sync.Mutex{}, records: nil}
}

func (r *floorLogRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *floorLogRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, floorLogRecord{message: rec.Message, level: rec.Level})

	return nil
}

func (r *floorLogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *floorLogRecorder) WithGroup(string) slog.Handler { return r }

// errorRecords returns the records at level ERROR.
func (r *floorLogRecorder) errorRecords() []floorLogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]floorLogRecord, 0, len(r.records))

	for _, rec := range r.records {
		if rec.level == slog.LevelError {
			out = append(out, rec)
		}
	}

	return out
}
