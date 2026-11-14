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
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// restoreFixture is a Handler with a backup service and a restore service
// over fresh stores, with the services kept for synchronous setup.
type restoreFixture struct {
	handler    testHandler
	namespaces *namespace.Service
	backups    *backup.Service
	restores   *backup.RestoreService
	audit      *testAudit
}

// workRestores runs the restore worker of restores until the end of the
// test, as the worker of a server does, and waits for it to stop.
func workRestores(t *testing.T, restores *backup.RestoreService) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan struct{})

	go func() {
		defer close(done)

		restores.Work(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})
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

	return newRestoreFixtureHooked(t, stores, target, nil)
}

// newRestoreFixtureHooked builds the fixture of newRestoreFixtureOver with
// afterApply as the after-apply step of the restore service.
func newRestoreFixtureHooked(
	t *testing.T, stores backupStores, target blob.Store, afterApply func(context.Context) error,
) restoreFixture {
	t.Helper()

	return newRestoreFixtureWorkers(t, stores, target, afterApply, nil)
}

// newRestoreFixtureWorkers builds the fixture of newRestoreFixtureHooked
// with workers as the worker check of the handler.
func newRestoreFixtureWorkers(
	t *testing.T, stores backupStores, target blob.Store, afterApply func(context.Context) error,
	workers apiserver.WorkerSource,
) restoreFixture {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Exporter:    stores.registry,
		Kinds:       stores.registry,
		Registry:    stores.registry,
		Operations:  stores.registry.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
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
		Live:            stores.registry,
		Operations:      stores.registry.DurableOperationsPort(),
		Backups:         backups,
		Maintenance:     storetest.StateMaintenance{State: stores.registry, Hold: false},
		Blobs:           stores.live,
		Target:          target,
		Key:             nil,
		Installation:    "test",
		AfterApply:      afterApply,
		Admit:           nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
		Now:             nil,
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	workRestores(t, restores)

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	aud := newTestAudit()

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          workers,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          backups,
		Restores:         restores,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            aud,
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return restoreFixture{handler: handler, namespaces: namespaces, backups: backups, restores: restores, audit: aud}
}

// serveRestore sends one installation API request with the test token and
// returns its status and body.
func serveRestore(t *testing.T, handler testHandler, method, path, body string) (int, string) {
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
func startRestore(t *testing.T, handler testHandler, body string) startRestoreDoc {
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

// waitRestoreState polls one restore until it reaches want or the poll
// context of pollContext ends, failing the test otherwise.
func waitRestoreState(t *testing.T, handler testHandler, id, want string) getRestoreDoc {
	t.Helper()

	ctx, cancel := pollContext(t)
	defer cancel()

	for ctx.Err() == nil {
		status, body := serveRestore(t, handler, http.MethodGet, "/api/server/restores/"+id, "")

		if status != http.StatusOK {
			select {
			case <-ctx.Done():
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
		case <-ctx.Done():
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

// gatedRestoreTarget wraps a blob store and blocks every Get except the first
// until gate closes, so a test holds a restore before it reads the backup
// target. The first Get is the read of the manifest at the start of the
// restore, which refuses a dev backup at once. It honors ctx, so a test that
// never opens the gate still ends.
type gatedRestoreTarget struct {
	inner blob.Store
	gate  chan struct{}
	reads atomic.Int32
}

func (g *gatedRestoreTarget) Get(ctx context.Context, key blob.Key) (io.ReadCloser, error) {
	if g.reads.Add(1) == 1 {
		return g.inner.Get(ctx, key)
	}

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

func (g *gatedRestoreTarget) ListPage(ctx context.Context, req blob.PageRequest) (blob.Page, error) {
	return g.inner.ListPage(ctx, req)
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

	fix := newRestoreFixtureOver(
		t,
		stores,
		&gatedRestoreTarget{inner: stores.target, gate: gate, reads: atomic.Int32{}},
	)

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
func serveRestoreNoAuth(t *testing.T, handler testHandler, method, path, body string) (int, string) {
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

	denied, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer: selectiveAuthorizer{deny: map[identity.Action]bool{
			identity.ActionServerRestore: true,
		}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       fix.namespaces,
		Backups:          fix.backups,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
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
func assertPreBackupSurvivesRestore(t *testing.T, handler testHandler, preBackupID string) {
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

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          nil,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            newTestAudit(),
		OnAuditFailure:   nil,
		DeletedUnits:     nil,
		AuditChain:       nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		SignIn:           nil,
		CheckpointEpochs: nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
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

// TestStartRestoreWritesAccessModeAuditEntry proves a started restore writes
// one audit entry that says whether the access tables were replaced or kept:
// a caller that holds server:restore-access replaces them, any other caller
// keeps the current rows.
func TestStartRestoreWritesAccessModeAuditEntry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		deny map[identity.Action]bool
		want string
	}{
		{name: "replaces with restore-access", deny: map[identity.Action]bool{}, want: "access tables will be replaced"},
		{
			name: "keeps without restore-access",
			deny: map[identity.Action]bool{identity.ActionServerRestoreAccess: true},
			want: "access tables will be kept",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAccessModeAuditEntry(t, tc.deny, tc.want)
		})
	}
}

// checkAccessModeAuditEntry starts a restore with deny controlling
// server:restore-access and proves the restore audit entry names want.
func checkAccessModeAuditEntry(t *testing.T, deny map[identity.Action]bool, want string) {
	t.Helper()

	stores := openBackupDeps(t)

	backups, err := backup.NewService(backup.Config{
		Exporter:    stores.registry,
		Kinds:       stores.registry,
		Registry:    stores.registry,
		Operations:  stores.registry.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
		Blobs:       stores.live,
		StateOpener: nil,
		Target:      stores.target,
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            stores.registry,
		Operations:      stores.registry.DurableOperationsPort(),
		Backups:         backups,
		Maintenance:     storetest.StateMaintenance{State: stores.registry, Hold: false},
		Blobs:           stores.live,
		Target:          stores.target,
		Key:             nil,
		Installation:    "test",
		AfterApply:      nil,
		Admit:           nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
		Now:             nil,
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	workRestores(t, restores)

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:       nil,
		Now:              nil,
		CheckpointEpochs: nil,
		SignIn:           nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: deny},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          backups,
		Restores:         restores,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            stores.registry,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	doc := startRestore(t, handler, `{"backupID":"bkp-missing000000000001"}`)

	checkAuditEntryNamesAccessMode(t, stores, doc.ID, want)
}

// TestRestoreLeavesAuditEntryAfterSwap proves a restore that runs through
// the swap leaves the restore audit entry, with the access mode, in the
// restored audit log: an entry written before the swap would go away with
// the old live data. A caller that holds server:restore-access replaces the
// access tables, any other caller keeps the current rows.
func TestRestoreLeavesAuditEntryAfterSwap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		deny map[identity.Action]bool
		want string
	}{
		{name: "replaces with restore-access", deny: map[identity.Action]bool{}, want: "access tables replaced"},
		{
			name: "keeps without restore-access",
			deny: map[identity.Action]bool{identity.ActionServerRestoreAccess: true},
			want: "access tables kept",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRestoreAuditSurvivesSwap(t, tc.deny, tc.want)
		})
	}
}

// checkRestoreAuditSurvivesSwap takes a backup, restores it through the
// routes to success, and proves the restored audit log holds the restore
// audit entry naming want.
func checkRestoreAuditSurvivesSwap(t *testing.T, deny map[identity.Action]bool, want string) {
	t.Helper()

	stores := openBackupDeps(t)
	ctx := t.Context()

	backups, err := backup.NewService(backup.Config{
		Exporter:    stores.registry,
		Kinds:       stores.registry,
		Registry:    stores.registry,
		Operations:  stores.registry.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
		Blobs:       stores.live,
		StateOpener: nil,
		Target:      stores.target,
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:            stores.registry,
		Operations:      stores.registry.DurableOperationsPort(),
		Backups:         backups,
		Maintenance:     storetest.StateMaintenance{State: stores.registry, Hold: false},
		Blobs:           stores.live,
		Target:          stores.target,
		Key:             nil,
		Installation:    "test",
		AfterApply:      nil,
		Admit:           nil,
		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
		Now:             nil,
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	workRestores(t, restores)

	namespaces, err := namespace.NewService(namespace.Config{Store: stores.registry, Now: nil, Installation: "test"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := newCheckedHandler(t, &apiserver.Config{
		Cursors:          testCursors(t),
		Reach:            noReach(),
		LocalUsers:       nil,
		Preferences:      nil,
		GitHostLinks:     nil,
		Settings:         nil,
		GitHost:          nil,
		Idempotency:      nil,
		Workers:          nil,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		TempAdmins:       nil,
		Now:              nil,
		CheckpointEpochs: nil,
		SignIn:           nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: deny},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          backups,
		Restores:         restores,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stubSchema{migrations: nil},
		Journal:          nil,
		Principals:       nil,
		Access:           nil,
		Tokens:           nil,
		Audit:            stores.registry,
		OnAuditFailure:   nil,
		AuditChain:       nil,
		DeletedUnits:     nil,
		AuditSinks:       nil,
		IPAllowlists:     nil,
		Gates:            nil,
		SessionPolicy:    nil,
		DenialLimiter:    nil,
		Instances:        nil,
		StoreChecks:      nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	backupID := backup.ID("bkp-test00000000000002")

	if _, runErr := backups.Run(ctx, backupID); runErr != nil {
		t.Fatalf("run backup %s: %v", backupID, runErr)
	}

	doc := startRestore(t, handler, `{"backupID":"`+backupID.String()+`"}`)
	waitRestoreState(t, handler, doc.ID, "succeeded")

	listing, err := stores.registry.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	wantObject := "restore " + doc.ID + " of backup " + backupID.String() + " (" + want + ")"

	for i := range listing.Entries {
		entry := listing.Entries[i]
		if entry.Action != string(identity.ActionServerRestore) {
			continue
		}

		if entry.Object != wantObject {
			t.Fatalf("restore audit object = %q, want %q", entry.Object, wantObject)
		}

		return
	}

	t.Fatalf("no audit entry holds %q", wantObject)
}

// checkAuditEntryNamesAccessMode proves the restore audit entry for id names
// want.
func checkAuditEntryNamesAccessMode(t *testing.T, stores backupStores, id, want string) {
	t.Helper()

	listing, err := stores.registry.List(
		t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize},
	)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	for i := range listing.Entries {
		entry := listing.Entries[i]
		if entry.Action != string(identity.ActionServerRestore) {
			continue
		}

		if !strings.Contains(entry.Object, id) || !strings.Contains(entry.Object, want) {
			t.Fatalf("restore audit object = %q, want restore %s with %q",
				entry.Object, id, want)
		}

		return
	}

	t.Fatalf("no audit entry names restore %s with %q", id, want)
}

// TestStartRestoreRefusesADevBackupWith409 sends a restore of a backup whose
// manifest says dev to a production installation: the answer is 409 with the
// refusal text, and no restore row exists (layer 1b through the API).
func TestStartRestoreRefusesADevBackupWith409(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)
	fix := newRestoreFixtureOver(t, stores, stores.target)

	backupID := backup.ID("bkp-dev000000000000001")
	if _, err := fix.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup %s: %v", backupID, err)
	}

	// The manifest is not authenticated: a person who can write to the
	// target changes the kind.
	key, err := blob.ParseKey("backups/" + backupID.String() + "/manifest.json")
	if err != nil {
		t.Fatalf("manifest key: %v", err)
	}

	raw := readTarget(t, stores.target, key)
	edited := strings.Replace(raw, `"installationKind":"production"`, `"installationKind":"dev"`, 1)

	if edited == raw {
		t.Fatalf("manifest %s holds no production kind", raw)
	}

	if delErr := stores.target.Delete(t.Context(), key); delErr != nil {
		t.Fatalf("delete manifest: %v", delErr)
	}

	if putErr := stores.target.Put(t.Context(), key, strings.NewReader(edited)); putErr != nil {
		t.Fatalf("put manifest: %v", putErr)
	}

	for _, body := range []string{
		`{"backupID":"` + backupID.String() + `"}`,
		`{"backupID":"` + backupID.String() + `","checkOnly":true}`,
	} {
		status, resp := serveRestore(t, fix.handler, http.MethodPost, "/api/server/restores", body)
		if status != http.StatusConflict {
			t.Fatalf("POST restores %s answers %d, want 409 (%s)", body, status, resp)
		}

		for _, want := range []string{"production installation", "dev installation", "We do not recommend this restore."} {
			if !strings.Contains(resp, want) {
				t.Fatalf("refusal %q does not hold %q", resp, want)
			}
		}
	}

	status, listBody := serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores", "")
	if status != http.StatusOK || strings.Contains(listBody, backupID.String()) {
		t.Fatalf("restores list = %d %s, want no row for the refused backup", status, listBody)
	}
}

// TestRestoreAnswersCarryWarnings checks that the answers of the restore
// routes hold a warnings list, empty for a restore with no dev data, and no
// dev data summary.
func TestRestoreAnswersCarryWarnings(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	backupID := backup.ID("bkp-warn000000000000001")
	if _, err := fix.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup %s: %v", backupID, err)
	}

	status, resp := serveRestore(
		t, fix.handler, http.MethodPost, "/api/server/restores", `{"backupID":"`+backupID.String()+`"}`,
	)
	if status != http.StatusAccepted || !strings.Contains(resp, `"warnings":[]`) {
		t.Fatalf("POST restores = %d %s, want 202 with an empty warnings list", status, resp)
	}

	var doc startRestoreDoc
	if err := json.Unmarshal([]byte(resp), &doc); err != nil {
		t.Fatalf("decode start answer: %v", err)
	}

	waitRestoreState(t, fix.handler, doc.ID, "succeeded")

	status, resp = serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores/"+doc.ID, "")
	if status != http.StatusOK || !strings.Contains(resp, `"warnings":[]`) || strings.Contains(resp, "devData") {
		t.Fatalf("GET restore = %d %s, want an empty warnings list and no dev data", status, resp)
	}

	status, resp = serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores", "")
	if status != http.StatusOK || !strings.Contains(resp, `"warnings":[]`) {
		t.Fatalf("GET restores = %d %s, want a warnings list in each row", status, resp)
	}
}

// readTarget reads one object of the backup target as text.
func readTarget(t *testing.T, target *blobfs.Store, key blob.Key) string {
	t.Helper()

	reader, err := target.Get(t.Context(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}

	data, readErr := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("close %s: %v", key, closeErr)
	}

	if readErr != nil {
		t.Fatalf("read %s: %v", key, readErr)
	}

	return string(data)
}

// TestStartRestoreInvalidBackupIDAnswers400 proves that a start with a backup
// ID that is not valid answers 400 with the text that names the ID rule, and
// stores no restore row. A restore of such an ID can never succeed, so the
// route refuses it before it starts.
func TestStartRestoreInvalidBackupIDAnswers400(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	const want = "invalid backup ID: use an ID that opendustpan backup printed"

	for _, id := range []string{"../x", "bkp-a/b", "x"} {
		status, resp := serveRestore(t, fix.handler, http.MethodPost, "/api/server/restores", `{"backupID":"`+id+`"}`)
		if status != http.StatusBadRequest || !strings.Contains(resp, want) {
			t.Errorf("POST restores of %q answers %d %s, want 400 that names the ID rule", id, status, resp)
		}
	}

	listStatus, listBody := serveRestore(t, fix.handler, http.MethodGet, "/api/server/restores", "")
	if listStatus != http.StatusOK || strings.Contains(listBody, `"id":"rst-`) {
		t.Fatalf("GET restores answers %d %s, want 200 with no restore", listStatus, listBody)
	}
}

// TestStartRestoreBodyTooLarge proves that a start body over the limit of the
// restore route answers 413 request.body-too-large with the limit, and that a
// body of exactly the limit is read.
func TestStartRestoreBodyTooLarge(t *testing.T) {
	t.Parallel()

	fix := newRestoreFixture(t)

	const limit = 4096

	// The padding is spaces after the backup ID, so the body is valid JSON and
	// only its size decides the answer.
	const head = `{"backupID":"bkp-missing000000000001"}`

	exact := head + strings.Repeat(" ", limit-len(head))
	status, body := serveRestore(t, fix.handler, http.MethodPost, "/api/server/restores", exact)
	if status == http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes = %d %s, want it read", limit, status, body)
	}

	over := head + strings.Repeat(" ", limit-len(head)+1)

	status, body = serveRestore(t, fix.handler, http.MethodPost, "/api/server/restores", over)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("body of %d bytes = %d %s, want 413", limit+1, status, body)
	}

	var got apiError
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("answer %q is not an error: %v", body, err)
	}

	if got.ErrorCode != "request.body-too-large" || got.ErrorArgs["maxBytes"] != float64(limit) {
		t.Fatalf("answer = %+v, want request.body-too-large with maxBytes %d", got, limit)
	}
}
