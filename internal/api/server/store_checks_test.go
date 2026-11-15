package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/admit/admittest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// storeCheckFixture is a handler with the store check routes over a SQLite
// store and a directory blob store.
type storeCheckFixture struct {
	handler testHandler
	store   *sqlite.Store
	svc     *storecheck.Service
	now     *time.Time
	refuse  *error
}

var errStoreCheckMaintenance = errors.New("installation is in maintenance mode")

func newStoreCheckFixture(t *testing.T, workers apiserver.WorkerSource, deny ...identity.Action) storeCheckFixture {
	t.Helper()

	st := openEpochStore(t)
	live := openRouteTestBlobs(t)
	target := openRouteTestBlobs(t)
	clock := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	var refuse error

	svc, err := storecheck.NewService(storecheck.Config{
		Records: st, Operations: st.DurableOperationsPort(), Blobs: live, Backup: target, Audit: st,
		Admit:  func(context.Context) error { return refuse },
		Limits: storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:    func() time.Time { return clock }, Owner: "", Lease: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new store check service: %v", err)
	}

	denied := map[identity.Action]bool{}
	for _, action := range deny {
		denied[action] = true
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          workers,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:       nil,
		Now:              nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: denied},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       newTestNamespaces(t),
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Tokens:           nil,
		Access:           nil,
		Audit:            st,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		CheckpointEpochs: nil,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		AuditSinks:       nil,
		SignIn:           nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      svc,
		Settings:         nil,
		GitHostLinks:     nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return storeCheckFixture{handler: handler, store: st, svc: svc, now: &clock, refuse: &refuse}
}

func (f storeCheckFixture) post(t *testing.T, body string) (int, string) {
	t.Helper()

	return serveMaintenance(t, f.handler, http.MethodPost, "/api/dustpan/v1/store-checks", body)
}

func (f storeCheckFixture) rows(t *testing.T) []storecheck.Record {
	t.Helper()

	rows, err := f.svc.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	return rows
}

func errorCodeOf(t *testing.T, body string) string {
	t.Helper()

	var doc struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return doc.ErrorCode
}

// startAnswer is the answer of a start.
type startAnswer struct {
	ID     string `json:"id"`
	Target string `json:"target"`
	State  string `json:"state"`
}

func startedID(t *testing.T, body string) string {
	t.Helper()

	var doc startAnswer

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if _, err := storecheck.ParseID(doc.ID); err != nil || doc.State != "running" {
		t.Fatalf("start answer = %+v, %v", doc, err)
	}

	return doc.ID
}

func TestStartStoreCheckAnswers202AndWritesTheStartEntry(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)

	status, body := f.post(t, `{"target":"blobs"}`)
	if status != http.StatusAccepted {
		t.Fatalf("POST = %d %s, want 202", status, body)
	}

	id := startedID(t, body)

	entries := serverEntries(t, f.store)
	if len(entries) != 1 {
		t.Fatalf("%d entries, want the start entry", len(entries))
	}

	e := entries[0]
	if e.Action != "installation:store-check" || e.Subject != "tester" || e.Object != "store-check "+id ||
		e.Outcome != audit.OutcomeSuccess || !strings.Contains(audit.EncodeChanges(e.Changes), "blobs") {
		t.Fatalf("entry = %+v", e)
	}
}

func TestStartStoreCheckRefusesWhatIsNotATargetOnTheList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			"an address",
			`{"target":"https://s3.example.test/bucket"}`,
			http.StatusBadRequest,
			errcode.StoreCheckInvalidTarget,
		},
		{"another word", `{"target":"all"}`, http.StatusBadRequest, errcode.StoreCheckInvalidTarget},
		{"empty target", `{"target":""}`, http.StatusBadRequest, errcode.StoreCheckInvalidTarget},
		{"no target", `{}`, http.StatusBadRequest, errcode.StoreCheckInvalidTarget},
		{
			"a field that the body does not have",
			`{"target":"blobs","url":"https://x.test"}`,
			http.StatusBadRequest,
			errcode.RequestBodyInvalid,
		},
		{"not JSON", `target=blobs`, http.StatusBadRequest, errcode.RequestBodyInvalid},
		{"empty body", ``, http.StatusBadRequest, errcode.RequestBodyInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newStoreCheckFixture(t, nil)

			status, body := f.post(t, tc.body)
			if status != tc.wantStatus || errorCodeOf(t, body) != tc.wantCode {
				t.Fatalf("POST = %d %s, want %d %s", status, body, tc.wantStatus, tc.wantCode)
			}

			if strings.Contains(body, "example.test") {
				t.Fatalf("the answer repeats the address: %s", body)
			}

			rows := f.rows(t)
			if len(rows) != 0 {
				t.Fatalf("a refused start stored %d rows", len(rows))
			}
		})
	}
}

func TestStartStoreCheckAnswers409ForARunningCheckAndTheCooldown(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)

	if status, body := f.post(t, `{"target":"blobs"}`); status != http.StatusAccepted {
		t.Fatalf("first POST = %d %s", status, body)
	}

	status, body := f.post(t, `{"target":"backup"}`)
	if status != http.StatusConflict || errorCodeOf(t, body) != errcode.StoreCheckRunning {
		t.Fatalf("second POST = %d %s, want 409 storecheck.running", status, body)
	}

	if err := f.svc.RunOnce(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	status, body = f.post(t, `{"target":"blobs"}`)
	if status != http.StatusConflict || errorCodeOf(t, body) != errcode.StoreCheckCooldown {
		t.Fatalf("POST in the cooldown = %d %s, want 409 storecheck.cooldown", status, body)
	}

	*f.now = f.now.Add(storecheck.Cooldown + time.Second)

	if status, body = f.post(t, `{"target":"blobs"}`); status != http.StatusAccepted {
		t.Fatalf("POST after the cooldown = %d %s, want 202", status, body)
	}

	// Each refusal wrote a denial entry.
	denied := 0

	for _, e := range serverEntries(t, f.store) {
		if e.Outcome == audit.OutcomeDenied {
			denied++
		}
	}

	if denied != 2 {
		t.Fatalf("%d denial entries, want 2", denied)
	}
}

func TestStartStoreCheckAnswers409InMaintenanceOrDuringARestore(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)
	*f.refuse = errStoreCheckMaintenance

	status, body := f.post(t, `{"target":"blobs"}`)
	if status != http.StatusConflict || errorCodeOf(t, body) != errcode.StoreCheckRefused {
		t.Fatalf("POST = %d %s, want 409 storecheck.refused", status, body)
	}

	if rows := f.rows(t); len(rows) != 0 {
		t.Fatalf("a refused start stored %d rows", len(rows))
	}
}

func TestStartStoreCheckNeedsAWorker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workers    func() apiserver.WorkerSource
		wantStatus int
		wantRows   int
	}{
		{
			"no worker",
			func() apiserver.WorkerSource { return workerSource{admittest.NoWorker()} },
			http.StatusServiceUnavailable,
			0,
		},
		{
			"a worker runs",
			func() apiserver.WorkerSource { return workerSource{admittest.Worker()} },
			http.StatusAccepted,
			1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newStoreCheckFixture(t, tc.workers())

			status, body := f.post(t, `{"target":"blobs"}`)
			if status != tc.wantStatus {
				t.Fatalf("POST = %d %s, want %d", status, body, tc.wantStatus)
			}

			rows := f.rows(t)
			if len(rows) != tc.wantRows {
				t.Fatalf("%d rows, want %d", len(rows), tc.wantRows)
			}

			if tc.wantStatus == http.StatusServiceUnavailable {
				checkNoWorkerAnswer(t, body)
			}
		})
	}
}

// answerCase is one case in the report of a check answer.
type answerCase struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

// answerReport is the report of a check answer, as the tests read it.
type answerReport struct {
	Prefix            string       `json:"prefix"`
	ConditionalWrites string       `json:"conditionalWrites"`
	Cases             []answerCase `json:"cases"`
	Passed            bool         `json:"passed"`
	PrefixDeleted     bool         `json:"prefixDeleted"`
}

// checkAnswer is a check answer with its report.
type checkAnswer struct {
	State  string       `json:"state"`
	Report answerReport `json:"report"`
}

// checkReportOf fails unless body is the answer of a succeeded check id with a
// whole report.
func checkReportOf(t *testing.T, body, id string) {
	t.Helper()

	var doc checkAnswer

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	r := doc.Report
	if doc.State != "succeeded" || !r.Passed || !r.PrefixDeleted || r.Prefix != "store-checks/"+id ||
		len(r.Cases) != 11 || r.ConditionalWrites == "" {
		t.Fatalf("report = %+v", doc)
	}
}

func TestGetStoreCheckAnswersTheReport(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)

	_, body := f.post(t, `{"target":"blobs"}`)
	id := startedID(t, body)
	path := "/api/dustpan/v1/store-checks/" + id

	status, body := serveMaintenance(t, f.handler, http.MethodGet, path, "")
	if status != http.StatusOK || !strings.Contains(body, `"state":"running"`) || strings.Contains(body, `"report"`) {
		t.Fatalf("GET while running = %d %s", status, body)
	}

	if err := f.svc.RunOnce(t.Context()); err != nil {
		t.Fatalf("run: %v", err)
	}

	status, body = serveMaintenance(t, f.handler, http.MethodGet, path, "")
	if status != http.StatusOK {
		t.Fatalf("GET = %d %s", status, body)
	}

	checkReportOf(t, body, id)

	// The list holds the check without the report.
	status, body = serveMaintenance(t, f.handler, http.MethodGet, "/api/dustpan/v1/store-checks", "")
	if status != http.StatusOK || !strings.Contains(body, id) || strings.Contains(body, `"report"`) {
		t.Fatalf("list = %d %s", status, body)
	}

	// The end wrote the second entry.
	if got := len(serverEntries(t, f.store)); got != 2 {
		t.Fatalf("%d entries, want the start and the end", got)
	}
}

func TestGetStoreCheckOfAnIDThatIsNotACheckAnswers404(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)

	for _, id := range []string{"chk-unknownunknownunkn", "bkp-test00000000000001", "chk-%2e%2e", "CHK-ABC", "x"} {
		status, body := serveMaintenance(t, f.handler, http.MethodGet, "/api/dustpan/v1/store-checks/"+id, "")
		if status != http.StatusNotFound || errorCodeOf(t, body) != errcode.StoreCheckNotFound {
			t.Errorf("GET %q = %d %s, want 404 storecheck.not-found", id, status, body)
		}
	}
}

func TestStoreCheckRoutesNeedTheAction(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil, identity.ActionInstallationStoreCheck)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/dustpan/v1/store-checks", `{"target":"blobs"}`},
		{http.MethodGet, "/api/dustpan/v1/store-checks", ""},
		{http.MethodGet, "/api/dustpan/v1/store-checks/chk-aaaaaaaaaaaaaaaaaaaa", ""},
	} {
		status, body := serveMaintenance(t, f.handler, tc.method, tc.path, tc.body)
		if status != http.StatusForbidden {
			t.Errorf("%s %s = %d %s, want 403", tc.method, tc.path, status, body)
		}
	}

	if rows := f.rows(t); len(rows) != 0 {
		t.Fatalf("a denied start stored %d rows", len(rows))
	}
}

func TestStoreCheckActionIsDeclaredOnTheInstallation(t *testing.T) {
	t.Parallel()

	f := newStoreCheckFixture(t, nil)

	seen := 0

	for _, route := range f.handler.Routes() {
		if !strings.HasPrefix(route.Path, "/api/dustpan/v1/store-checks") {
			continue
		}

		seen++

		if len(route.Actions) != 1 || route.Actions[0] != identity.ActionInstallationStoreCheck ||
			route.Scope != apiserver.ScopeAuthorized {
			t.Errorf("route %s %s = %+v", route.Method, route.Path, route)
		}

		obj, err := route.Resolve(nil, nil)
		if err != nil || !obj.IsServer() {
			t.Errorf("route %s %s resolves %+v, %v; want the installation", route.Method, route.Path, obj, err)
		}
	}

	if seen != 3 {
		t.Fatalf("%d store check routes, want 3", seen)
	}
}
