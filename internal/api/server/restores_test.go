package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// restoreFixture is a Handler with a backup service and a restore service
// over fresh stores, with the services kept for synchronous setup.
type restoreFixture struct {
	handler    *apiserver.Handler
	namespaces *namespace.Service
	backups    *backup.Service
	restores   *backup.RestoreService
}

// newRestoreFixture builds a Handler with a backup service and a restore
// service over fresh stores. The registry store doubles as the restore live
// store, so the restore swaps the database it tracks.
func newRestoreFixture(t *testing.T) restoreFixture {
	t.Helper()

	stores := openBackupDeps(t)

	return newRestoreFixtureOver(t, stores, stores.target)
}

// newRestoreFixtureOver builds a Handler with a backup service and a restore
// service over stores, with the restore and backup services reading the
// backup target through target instead of the fresh target store. A test
// passes a gated target to hold a restore before it reads the target.
func newRestoreFixtureOver(t *testing.T, stores backupStores, target blob.Store) restoreFixture {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Exporter:    stores.registry,
		Registry:    stores.registry,
		Blobs:       stores.live,
		StateOpener: nil,
		Target:      target,
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:        stores.registry,
		Registry:    stores.registry,
		Backups:     backups,
		Maintenance: newFakeMaintenance(maintenance.StateNormal),
		Blobs:       stores.live,
		Target:      target,
		Key:         nil,
		Region:      "test",
		AfterApply:  nil,
		Now:         nil,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        backups,
		Restores:       restores,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         stubSchema{migrations: nil},
		Principals:     nil,
		Audit:          nil,
		OnAuditFailure: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return restoreFixture{handler: handler, namespaces: namespaces, backups: backups, restores: restores}
}

// serveRestore sends one installation API request with the test token and
// returns its status and body.
func serveRestore(t *testing.T, handler *apiserver.Handler, method, path, body string) (int, string) {
	t.Helper()

	var reader *strings.Reader

	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	var req *http.Request

	if body != "" {
		req = httptest.NewRequestWithContext(t.Context(), method, path, reader)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
	}

	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// startRestoreDoc decodes a started restore in tests.
type startRestoreDoc struct {
	ID string `json:"id"`
}

// startRestore starts a restore of backupID and returns the restore ID,
// failing the test on any other status.
func startRestore(t *testing.T, handler *apiserver.Handler, body string) startRestoreDoc {
	t.Helper()

	status, resp := serveRestore(t, handler, http.MethodPost, "/api/server/restores", body)
	if status != http.StatusAccepted {
		t.Fatalf("POST restores answers %d, want %d (%s)", status, http.StatusAccepted, resp)
	}

	var doc startRestoreDoc

	if err := json.Unmarshal([]byte(resp), &doc); err != nil || doc.ID == "" {
		t.Fatalf("POST restores answers %q, want an id: %v", resp, err)
	}

	if !strings.HasPrefix(doc.ID, "rst-") {
		t.Fatalf("restore id = %q, want the rst- prefix", doc.ID)
	}

	return doc
}

// getRestoreDoc decodes one restore in tests.
type getRestoreDoc struct {
	ID          string           `json:"id"`
	State       string           `json:"state"`
	BackupID    string           `json:"backupID"`
	PreBackupID string           `json:"preBackupID"`
	RequestedBy string           `json:"requestedBy"`
	Error       string           `json:"error"`
	Manifest    *backup.Manifest `json:"manifest"`
}

// waitRestoreState polls one restore until it reaches want or the wait times
// out, failing the test otherwise.
func waitRestoreState(t *testing.T, handler *apiserver.Handler, id, want string) getRestoreDoc {
	t.Helper()

	deadline := time.Now().Add(pollTimeout)

	for time.Now().Before(deadline) {
		status, body := serveRestore(t, handler, http.MethodGet, "/api/server/restores/"+id, "")

		if status != http.StatusOK {
			select {
			case <-t.Context().Done():
				t.Fatalf("restore %s: %v", id, t.Context().Err())
			case <-time.After(pollInterval):
			}

			continue
		}

		var doc getRestoreDoc

		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("decode restore %s %q: %v", id, body, err)
		}

		if doc.State == want {
			return doc
		}

		if doc.State != "checking" && doc.State != "applying" {
			t.Fatalf("restore %s state = %q, want %q (error %q)", id, doc.State, want, doc.Error)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("restore %s: %v", id, t.Context().Err())
		case <-time.After(pollInterval):
		}
	}

	t.Fatalf("restore %s never reached %q", id, want)

	return getRestoreDoc{
		ID:          "",
		State:       "",
		BackupID:    "",
		PreBackupID: "",
		RequestedBy: "",
		Error:       "",
		Manifest:    nil,
	}
}

// TestRestoreRoutesDeclareRestoreAction proves every restore route declares
// the action server:restore on the installation.
func TestRestoreRoutesDeclareRestoreAction(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	paths := map[string]bool{
		"POST /api/server/restores":     false,
		"GET /api/server/restores":      false,
		"GET /api/server/restores/{id}": false,
	}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path

		if _, ok := paths[pattern]; !ok {
			continue
		}

		paths[pattern] = true

		if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerRestore {
			t.Fatalf("route %q actions = %v, want [server:restore]", pattern, route.Actions)
		}
	}

	for pattern, found := range paths {
		if !found {
			t.Fatalf("route %q is missing from the table", pattern)
		}
	}
}

// gatedRestoreTarget wraps a blob store and blocks every Get until gate
// closes, so a test holds a restore before it reads the backup target. It
// honors ctx, so a test that never opens the gate still ends.
type gatedRestoreTarget struct {
	inner blob.Store
	gate  chan struct{}
}

func (g *gatedRestoreTarget) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("read gated target: %w", ctx.Err())
	case <-g.gate:
	}

	return g.inner.Get(ctx, key)
}

func (g *gatedRestoreTarget) Put(ctx context.Context, key blob.Key, r io.Reader) error {
	return g.inner.Put(ctx, key, r)
}

func (g *gatedRestoreTarget) Delete(ctx context.Context, key blob.Key) error {
	return g.inner.Delete(ctx, key)
}

func (g *gatedRestoreTarget) List(ctx context.Context, prefix string) ([]blob.Object, error) {
	return g.inner.List(ctx, prefix)
}

func (g *gatedRestoreTarget) Ping(ctx context.Context) error {
	return g.inner.Ping(ctx)
}

// TestStartRestoreAnswersAcceptedAndSecondConflicts proves the start route
// answers 202 with the restore ID only (never a pre-backup ID: the service
// takes that backup only after the checks pass) and that a second start
// while one runs answers 409. The backup ID names no backup, so the
// background restore fails once the gate opens; the GET proves the failure
// lands in the row without driver text or capability paths. The gate holds
// the first restore before it reads the target, so the second start meets a
// running restore on every run: without it the missing backup fails faster
// than the second POST arrives and the test flakes.
func TestStartRestoreAnswersAcceptedAndSecondConflicts(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)
	gate := make(chan struct{})
	released := false

	defer func() {
		if !released {
			close(gate)
		}
	}()

	fix := newRestoreFixtureOver(t, stores, &gatedRestoreTarget{inner: stores.target, gate: gate})

	status, startBody := serveRestore(
		t, fix.handler, http.MethodPost, "/api/server/restores", `{"backupID":"bkp-missing000000000001"}`,
	)
	if status != http.StatusAccepted {
		t.Fatalf("POST restores answers %d, want %d (%s)", status, http.StatusAccepted, startBody)
	}

	if strings.Contains(startBody, "preBackupID") {
		t.Fatalf("202 body %q names a pre-backup ID before the checks pass", startBody)
	}

	var doc startRestoreDoc

	if err := json.Unmarshal([]byte(startBody), &doc); err != nil || doc.ID == "" {
		t.Fatalf("POST restores answers %q, want an id: %v", startBody, err)
	}

	status, body := serveRestore(
		t,
		fix.handler,
		http.MethodPost,
		"/api/server/restores",
		`{"backupID":"bkp-other00000000000002"}`,
	)
	if status != http.StatusConflict {
		t.Fatalf("second POST restores answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	close(gate)
	released = true

	failed := waitRestoreState(t, fix.handler, doc.ID, "failed")
	if failed.Error == "" {
		t.Fatalf("failed restore %s holds no error text", doc.ID)
	}

	if strings.Contains(failed.Error, "backups/") {
		t.Fatalf("failed restore error %q holds a capability path", failed.Error)
	}

	if failed.PreBackupID != "" {
		t.Fatalf("failed check restores pre-backup ID = %q, want empty", failed.PreBackupID)
	}

	if failed.RequestedBy != "tester" {
		t.Fatalf("restore requestedBy = %q, want tester", failed.RequestedBy)
	}
}

// TestStartRestoreCheckOnlyAnswersAccepted proves a check-only start answers
// 202 with no pre-backup ID: a check applies nothing.
func TestStartRestoreCheckOnlyAnswersAccepted(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	status, startBody := serveRestore(
		t,
		fix.handler,
		http.MethodPost,
		"/api/server/restores",
		`{"backupID":"bkp-missing000000000001","checkOnly":true}`,
	)
	if status != http.StatusAccepted {
		t.Fatalf("POST restores answers %d, want %d (%s)", status, http.StatusAccepted, startBody)
	}

	if strings.Contains(startBody, "preBackupID") {
		t.Fatalf("202 body %q names a pre-backup ID", startBody)
	}

	var doc startRestoreDoc

	if err := json.Unmarshal([]byte(startBody), &doc); err != nil || doc.ID == "" {
		t.Fatalf("POST restores answers %q, want an id: %v", startBody, err)
	}

	failed := waitRestoreState(t, fix.handler, doc.ID, "failed")
	if failed.PreBackupID != "" {
		t.Fatalf("failed check-only restore pre-backup ID = %q, want empty", failed.PreBackupID)
	}
}

// TestStartRestoreBadBodyAnswers400 proves a start without a backup ID
// answers 400.
func TestStartRestoreBadBodyAnswers400(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	for _, body := range []string{"", "{}", `{"backupID":""}`} {
		status, resp := serveRestore(t, fix.handler, http.MethodPost, "/api/server/restores", body)
		if status != http.StatusBadRequest {
			t.Fatalf("POST restores with %q answers %d, want %d (%s)", body, status, http.StatusBadRequest, resp)
		}
	}
}

// TestStartRestoreUnknownFieldAnswers400 proves the start route answers 400
// when the body names a field it does not use: the target is never a request
// field (ADR 30 decision 9), so a target value must not be silently ignored.
// The rejected start stores no row: the target is never used.
func TestStartRestoreUnknownFieldAnswers400(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	status, resp := serveRestore(
		t,
		fix.handler,
		http.MethodPost,
		"/api/server/restores",
		`{"backupID":"bkp-missing000000000001","target":"dir:/evil"}`,
	)
	if status != http.StatusBadRequest {
		t.Fatalf("POST restores with a target field answers %d, want %d (%s)", status, http.StatusBadRequest, resp)
	}

	listStatus, listBody := serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores", "")
	if listStatus != http.StatusOK {
		t.Fatalf("GET restores answers %d, want %d (%s)", listStatus, http.StatusOK, listBody)
	}

	var listed struct {
		Restores []getRestoreDoc `json:"restores"`
	}

	if err := json.Unmarshal([]byte(listBody), &listed); err != nil {
		t.Fatalf("decode restores %q: %v", listBody, err)
	}

	if len(listed.Restores) != 0 {
		t.Fatalf("restores = %+v, want none: the rejected target started nothing", listed.Restores)
	}
}

// TestGetRestoreMissingAnswers404 proves a missing restore answers 404.
func TestGetRestoreMissingAnswers404(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	status, body := serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores/rst-missing000000000001", "")
	if status != http.StatusNotFound {
		t.Fatalf("GET missing restore answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	doc := decodeError(t, body)
	if doc.Code != http.StatusNotFound || doc.Message == "" {
		t.Fatalf("missing restore error = %+v, want code 404 with a message", doc)
	}
}

// serveRestoreNoAuth sends one installation API request without credentials
// and returns its status and body.
func serveRestoreNoAuth(t *testing.T, handler *apiserver.Handler, method, path, body string) (int, string) {
	t.Helper()

	var reader *strings.Reader

	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}

	var req *http.Request

	if body != "" {
		req = httptest.NewRequestWithContext(t.Context(), method, path, reader)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// TestRestoreUnknownIDAuthBeforeExistence proves authorization runs before
// the existence check: an unknown restore ID answers 401 without credentials
// and 403 when the action is denied, never 404, so callers cannot tell
// whether the ID exists.
func TestRestoreUnknownIDAuthBeforeExistence(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)
	unknown := "/api/server/restores/rst-missing000000000001"

	status, body := serveRestoreNoAuth(t, fix.handler, http.MethodGet, unknown, "")
	if status != http.StatusUnauthorized {
		t.Fatalf(
			"GET unknown restore without credentials answers %d, want %d (%s)",
			status,
			http.StatusUnauthorized,
			body,
		)
	}

	denied, err := apiserver.NewHandler(apiserver.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer: selectiveAuthorizer{deny: map[identity.Action]bool{
			identity.ActionServerRestore: true,
		}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     fix.namespaces,
		Backups:        fix.backups,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         stubSchema{migrations: nil},
		Principals:     nil,
		Audit:          nil,
		OnAuditFailure: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if status, body := serveRestore(t, denied, http.MethodGet, unknown, ""); status != http.StatusForbidden {
		t.Fatalf("GET unknown restore when denied answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}
}

// TestRestoreStartAfterFinishSucceeds proves the slot frees with the row: a
// start after the first restore reaches its end state answers 202.
func TestRestoreStartAfterFinishSucceeds(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	first := startRestore(t, fix.handler, `{"backupID":"bkp-missing000000000001"}`)
	waitRestoreState(t, fix.handler, first.ID, "failed")

	second := startRestore(t, fix.handler, `{"backupID":"bkp-missing000000000001"}`)
	waitRestoreState(t, fix.handler, second.ID, "failed")

	if first.ID == second.ID {
		t.Fatalf("two starts share restore ID %q, want distinct IDs", first.ID)
	}
}

// TestListRestoresAnswersNewestFirst proves the list holds the started
// restore with its requested subject.
func TestListRestoresAnswersNewestFirst(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	doc := startRestore(t, fix.handler, `{"backupID":"bkp-missing000000000001"}`)

	status, body := serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores", "")
	if status != http.StatusOK {
		t.Fatalf("GET restores answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var listed struct {
		Restores []getRestoreDoc `json:"restores"`
	}

	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decode restores %q: %v", body, err)
	}

	if len(listed.Restores) != 1 || listed.Restores[0].ID != doc.ID {
		t.Fatalf("restores = %+v, want the started restore %s", listed.Restores, doc.ID)
	}

	if listed.Restores[0].RequestedBy != "tester" {
		t.Fatalf("restore requestedBy = %q, want tester", listed.Restores[0].RequestedBy)
	}
}

// TestRestoreSucceedsEndToEnd proves the whole restore order on a small
// database: a synchronous backup, then a restore through the routes that
// succeeds with the manifest and the pre-backup ID.
func TestRestoreSucceedsEndToEnd(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)
	ctx := t.Context()

	backupID := backup.ID("bkp-test00000000000001")

	if _, err := fix.backups.Run(ctx, backupID); err != nil {
		t.Fatalf("run backup %s: %v", backupID, err)
	}

	doc := startRestore(t, fix.handler, `{"backupID":"`+backupID.String()+`"}`)

	done := waitRestoreState(t, fix.handler, doc.ID, "succeeded")

	if done.PreBackupID == "" {
		t.Fatal("succeeded restore holds no pre-backup ID")
	}

	if done.Manifest == nil {
		t.Fatal("succeeded restore holds no manifest")
	}

	if done.Manifest.ID != backupID {
		t.Fatalf("restored manifest id = %q, want %q", done.Manifest.ID, backupID)
	}

	if done.BackupID != backupID.String() {
		t.Fatalf("restore backupID = %q, want %q", done.BackupID, backupID)
	}

	assertPreBackupSurvivesRestore(t, fix.handler, done.PreBackupID)
}

// preBackupDoc decodes the backup row the restore took before the apply.
type preBackupDoc struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// backupIDDoc decodes one row of the backups list.
type backupIDDoc struct {
	ID string `json:"id"`
}

// listBackupIDsDoc decodes the backups list.
type listBackupIDsDoc struct {
	Backups []backupIDDoc `json:"backups"`
}

// assertPreBackupSurvivesRestore proves the backup taken before the apply
// survives the swap (ADR 30 decision 9): the undo source stays readable and
// listed after the restore.
func assertPreBackupSurvivesRestore(t *testing.T, handler *apiserver.Handler, preBackupID string) {
	t.Helper()

	preStatus, preBody := serveRestore(t, handler, http.MethodGet, "/api/server/backups/"+preBackupID, "")
	if preStatus != http.StatusOK {
		t.Fatalf("GET pre-backup %s answers %d, want %d (%s)",
			preBackupID, preStatus, http.StatusOK, preBody)
	}

	var pre preBackupDoc

	if err := json.Unmarshal([]byte(preBody), &pre); err != nil {
		t.Fatalf("decode pre-backup %q: %v", preBody, err)
	}

	if pre.ID != preBackupID {
		t.Fatalf("pre-backup id = %q, want %q", pre.ID, preBackupID)
	}

	if pre.State != "succeeded" {
		t.Fatalf("pre-backup state = %q, want succeeded", pre.State)
	}

	listStatus, listBody := serveRestore(t, handler, http.MethodGet, "/api/server/backups", "")
	if listStatus != http.StatusOK {
		t.Fatalf("GET backups answers %d, want %d (%s)", listStatus, http.StatusOK, listBody)
	}

	var listed listBackupIDsDoc

	if err := json.Unmarshal([]byte(listBody), &listed); err != nil {
		t.Fatalf("decode backups %q: %v", listBody, err)
	}

	found := false

	for _, record := range listed.Backups {
		if record.ID == preBackupID {
			found = true

			break
		}
	}

	if !found {
		t.Fatalf("backups omit the pre-backup %s after the restore (%s)", preBackupID, listBody)
	}
}

// TestRestoreWithoutTargetAnswers503 proves the restore routes answer 503
// with the backup-target setting named when the server has no target.
func TestRestoreWithoutTargetAnswers503(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Region: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     namespaces,
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         stubSchema{migrations: nil},
		Principals:     nil,
		Audit:          nil,
		OnAuditFailure: nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	for _, target := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/server/restores", `{"backupID":"bkp-x"}`},
		{http.MethodGet, "/api/server/restores", ""},
		{http.MethodGet, "/api/server/restores/rst-x", ""},
	} {
		status, body := serveRestore(t, handler, target.method, target.path, target.body)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s %s answers %d, want %d (%s)",
				target.method, target.path, status, http.StatusServiceUnavailable, body)
		}

		if !strings.Contains(body, "--backup-target") {
			t.Fatalf("%s %s body %q lacks the backup-target setting", target.method, target.path, body)
		}
	}
}
