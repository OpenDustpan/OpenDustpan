package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// pollTimeout bounds a backup or restore poll when the test binary runs with
// no deadline (-timeout 0).
const pollTimeout = 60 * time.Second

// pollGrace is the time that a poll leaves before the deadline of the test
// binary, so a run that does not end fails the test with its state and not
// with the panic of the timeout.
const pollGrace = 30 * time.Second

// pollContext returns the context of a poll of a backup or restore run. The
// run starves for CPU on a loaded host, so the poll waits on the state of the
// run and not on a fixed time: it ends pollGrace before the deadline of the
// test binary (-timeout). With no deadline, it ends after pollTimeout.
func pollContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	deadline, ok := t.Deadline()
	if !ok {
		return context.WithTimeout(t.Context(), pollTimeout)
	}

	return context.WithDeadline(t.Context(), deadline.Add(-pollGrace))
}

// pollInterval sets how often a backup poll reads the row.
const pollInterval = 10 * time.Millisecond

// gateExporter blocks inside ExportPortable until the test releases it, so the
// test holds one backup running while it starts another.
type gateExporter struct {
	inner   backup.PortableExporter
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGateExporter(inner backup.PortableExporter) *gateExporter {
	return &gateExporter{inner: inner, entered: make(chan struct{}), release: make(chan struct{}), once: sync.Once{}}
}

func (g *gateExporter) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	g.once.Do(func() { close(g.entered) })

	select {
	case <-g.release:
		return g.inner.ExportPortable(ctx, sink)
	case <-ctx.Done():
		return backup.Summary{}, nil, fmt.Errorf("wait for test release: %w", ctx.Err())
	}
}

// backupFixture is a Handler with a SQLite-backed backup service over fresh
// stores.
type backupFixture struct {
	handler testHandler
}

// backupStores groups the stores one backup test service runs over: the
// registry store that tracks the rows with the live and target blob stores.
type backupStores struct {
	registry *sqlite.Store
	live     *blobfs.Store
	target   *blobfs.Store
}

// openBackupDeps opens the stores for one backup test. The registry store
// doubles as the service exporter, so the service exports the database it
// tracks. The registry is copied from the migrated template, so only the
// first test of the binary runs every migration.
func openBackupDeps(t *testing.T) backupStores {
	t.Helper()

	registry := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	// A server claims its installation kind at start. The backup and the
	// restore read it, so the stores of these tests hold one.
	if _, err := registry.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	live, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open live store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live store: %v", closeErr)
		}
	})

	target, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open backup target: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	return backupStores{registry: registry, live: live, target: target}
}

// newBackupFixture builds a Handler with a backup service over stores: the
// registry store tracks the rows, exporter captures the SQL data, and now
// overrides the service clock when not nil.
func newBackupFixture(
	t *testing.T, stores backupStores, exporter backup.PortableExporter, now func() time.Time,
) backupFixture {
	t.Helper()

	return newBackupFixtureWorkers(t, stores, exporter, now, nil)
}

// newBackupFixtureWorkers is newBackupFixture with the worker check of the
// server.
func newBackupFixtureWorkers(
	t *testing.T, stores backupStores, exporter backup.PortableExporter, now func() time.Time,
	workers apiserver.WorkerSource,
) backupFixture {
	t.Helper()

	service, err := backup.NewService(backup.Config{
		Exporter:    exporter,
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
		Now:         now,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	// A worker claims the backup operations, as the worker component of a
	// server does. It stops before the stores close.
	workCtx, stopWork := context.WithCancel(context.WithoutCancel(t.Context()))

	var work sync.WaitGroup

	work.Go(func() { service.Work(workCtx) })

	t.Cleanup(func() {
		stopWork()
		work.Wait()
	})

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
		Workers:          workers,
		InstallationKind: "",
		StateWriteGates:  stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
		Now:              nil,
		TempAdmins:       nil,
		Authenticator:    tokenAuthenticator{},
		Authorizer:       selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:           slog.New(slog.DiscardHandler),
		Namespaces:       namespaces,
		Backups:          service,
		Restores:         nil,
		Maintenance:      newFakeMaintenance(maintenance.StateNormal),
		Metrics:          stubMetrics{},
		Version:          "test",
		Schema:           stores.registry,
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

	return backupFixture{handler: handler}
}

// quickBackupFixture builds a Handler whose service exports the registry
// database it tracks, over fresh stores.
func quickBackupFixture(t *testing.T, now func() time.Time) backupFixture {
	t.Helper()

	stores := openBackupDeps(t)

	return newBackupFixture(t, stores, stores.registry, now)
}

// serveBackup sends one installation API request with the test token and
// returns its status and body.
func serveBackup(t *testing.T, handler testHandler, method, path string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// serveBackupBare sends one installation API request without a token and
// returns its status and body.
func serveBackupBare(t *testing.T, handler testHandler, method, path string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, http.NoBody)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// errorDoc decodes a JSON error body in tests.
type errorDoc struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// decodeError decodes body as the one JSON error shape the installation API
// answers.
func decodeError(t *testing.T, body string) errorDoc {
	t.Helper()

	var doc errorDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode error body %q: %v", body, err)
	}

	return doc
}

// startBackupDoc decodes a started backup in tests.
type startBackupDoc struct {
	ID string `json:"id"`
}

// startBackup starts a backup and returns its ID, failing the test on any
// other status.
func startBackup(t *testing.T, handler testHandler) string {
	t.Helper()

	status, body := serveBackup(t, handler, http.MethodPost, "/api/server/backups")
	if status != http.StatusAccepted {
		t.Fatalf("POST backups answers %d, want %d (%s)", status, http.StatusAccepted, body)
	}

	var doc startBackupDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.ID == "" {
		t.Fatalf("POST backups answers %q, want an id: %v", body, err)
	}

	if !strings.HasPrefix(doc.ID, "bkp-") {
		t.Fatalf("backup id = %q, want the bkp- prefix", doc.ID)
	}

	return doc.ID
}

// getBackupDoc decodes one backup in tests.
type getBackupDoc struct {
	ID                 string         `json:"id"`
	State              string         `json:"state"`
	StartedAt          string         `json:"startedAt"`
	EndedAt            string         `json:"endedAt"`
	Target             string         `json:"target"`
	Error              string         `json:"error"`
	Manifest           backupManifest `json:"manifest"`
	UnverifiedKeyLimit int            `json:"unverifiedKeyLimit"`
	UnverifiedKeyCount int            `json:"unverifiedKeyCount"`
}

// backupManifest holds the manifest fields the route tests check.
type backupManifest struct {
	ID            string `json:"id"`
	Format        int    `json:"format"`
	Dialect       string `json:"dialect"`
	SchemaVersion int    `json:"schemaVersion"`
	SQLSize       int64  `json:"sqlSize"`
}

// readBackup reads one backup, failing the test on any other status.
func readBackup(t *testing.T, handler testHandler, id string) getBackupDoc {
	t.Helper()

	status, body := serveBackup(t, handler, http.MethodGet, "/api/server/backups/"+id)
	if status != http.StatusOK {
		t.Fatalf("GET backup %s answers %d, want %d (%s)", id, status, http.StatusOK, body)
	}

	var doc getBackupDoc

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode backup %s %q: %v", id, body, err)
	}

	return doc
}

// pollBackupSucceeded polls one backup until it succeeds, failing the test
// when the deadline passes first.
func pollBackupSucceeded(t *testing.T, handler testHandler, id string) getBackupDoc {
	t.Helper()

	ctx, cancel := pollContext(t)
	defer cancel()

	timer := time.NewTimer(pollInterval)
	defer timer.Stop()

	for {
		doc := readBackup(t, handler, id)
		if doc.State == string(backup.StateSucceeded) {
			return doc
		}

		select {
		case <-ctx.Done():
			t.Fatalf("backup %s stays %q, want succeeded", id, doc.State)
		case <-timer.C:
			timer.Reset(pollInterval)
		}
	}
}

// waitGateEntered waits until the gated export begins, failing the test when
// the deadline passes first.
func waitGateEntered(t *testing.T, gate *gateExporter) {
	t.Helper()

	ctx, cancel := pollContext(t)
	defer cancel()

	select {
	case <-gate.entered:
	case <-ctx.Done():
		t.Fatal("the backup did not start exporting")
	}
}

// TestBackupRoutesDeclareServerBackup proves all four backup routes declare
// exactly the action server:backup, with a resolver that returns the
// installation.
func TestBackupRoutesDeclareServerBackup(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	var routes []apiserver.Route

	for _, route := range handler.Routes() {
		if strings.HasPrefix(route.Path, "/api/server/backups") {
			routes = append(routes, route)
		}
	}

	if len(routes) != 4 {
		t.Fatalf("backup routes = %d, want 4", len(routes))
	}

	for _, route := range routes {
		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkBackupRouteDeclaresServerBackup(t, route)
		})
	}
}

// checkBackupRouteDeclaresServerBackup proves one backup route declares
// exactly the action server:backup, with a resolver that returns the
// installation.
func checkBackupRouteDeclaresServerBackup(t *testing.T, route apiserver.Route) {
	t.Helper()

	if route.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("route scope = %d, want authorized", route.Scope)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionServerBackup {
		t.Fatalf("route actions = %v, want [server:backup]", route.Actions)
	}

	if route.Resolve == nil {
		t.Fatal("route declares actions but no resolver")
	}

	obj, err := route.Resolve(
		httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), route.Method, route.Path, http.NoBody),
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "@server" || !obj.IsServer() {
		t.Fatalf("resolver returns %q, want the installation @server", obj.String())
	}
}

// TestBackupStartRunsInBackground proves POST answers 202 with the ID while
// the backup still runs, and that polling the backup reaches succeeded with
// the manifest.
func TestBackupStartRunsInBackground(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)
	gate := newGateExporter(stores.registry)
	fix := newBackupFixture(t, stores, gate, nil)

	id := startBackup(t, fix.handler)

	waitGateEntered(t, gate)

	// The handler answered 202 before the export began: the row reads running.
	if doc := readBackup(t, fix.handler, id); doc.State != string(backup.StateRunning) {
		t.Fatalf("backup %s reads %q, want running while the export is held", id, doc.State)
	}

	close(gate.release)

	doc := pollBackupSucceeded(t, fix.handler, id)

	if doc.Manifest.ID != id || doc.Manifest.Format != backup.FormatVersion {
		t.Fatalf("backup manifest = %+v, want id %q with format %d", doc.Manifest, id, backup.FormatVersion)
	}

	if doc.Manifest.Dialect != string(backup.DialectSQLite) || doc.Manifest.SQLSize <= 0 {
		t.Fatalf("backup manifest = %+v, want the sqlite dialect with a SQL part", doc.Manifest)
	}
}

// TestBackupSecondStartConflicts proves a second POST while one backup runs
// answers 409 with the one JSON error shape.
func TestBackupSecondStartConflicts(t *testing.T) {
	t.Parallel()

	stores := openBackupDeps(t)
	gate := newGateExporter(stores.registry)
	fix := newBackupFixture(t, stores, gate, nil)

	id := startBackup(t, fix.handler)
	waitGateEntered(t, gate)

	status, body := serveBackup(t, fix.handler, http.MethodPost, "/api/server/backups")
	if status != http.StatusConflict {
		t.Fatalf("second POST answers %d, want %d (%s)", status, http.StatusConflict, body)
	}

	doc := decodeError(t, body)

	if doc.Code != http.StatusConflict || doc.Message == "" {
		t.Fatalf("second POST error = %+v, want code 409 with a message", doc)
	}

	close(gate.release)

	pollBackupSucceeded(t, fix.handler, id)
}

// TestBackupNeedsToken proves the backup routes answer 401 without a token,
// with the one JSON error shape.
func TestBackupNeedsToken(t *testing.T) {
	t.Parallel()

	fix := quickBackupFixture(t, nil)

	paths := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups/" + string(testBackupID)},
		{http.MethodGet, "/api/server/backups/" + string(testBackupID) + "/check"},
	}

	for _, tc := range paths {
		status, body := serveBackupBare(t, fix.handler, tc.method, tc.path)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s without a token answers %d, want %d (%s)",
				tc.method, tc.path, status, http.StatusUnauthorized, body)
		}

		if doc := decodeError(t, body); doc.Code != http.StatusUnauthorized || doc.Message == "" {
			t.Fatalf("%s %s error = %+v, want code 401 with a message", tc.method, tc.path, doc)
		}
	}
}

// TestBackupGetMissing proves GET of a backup that was never made answers 404
// with the one JSON error shape.
func TestBackupGetMissing(t *testing.T) {
	t.Parallel()

	fix := quickBackupFixture(t, nil)

	status, body := serveBackup(t, fix.handler, http.MethodGet, "/api/server/backups/bkp-missing000000000001")
	if status != http.StatusNotFound {
		t.Fatalf("GET missing backup answers %d, want %d (%s)", status, http.StatusNotFound, body)
	}

	if doc := decodeError(t, body); doc.Code != http.StatusNotFound || doc.Message == "" {
		t.Fatalf("missing backup error = %+v, want code 404 with a message", doc)
	}
}

// TestBackupListNewestFirst proves GET lists the backups this installation
// made, newest first.
func TestBackupListNewestFirst(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	var mu sync.Mutex

	clock := now

	fix := quickBackupFixture(t, func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		at := clock
		clock = clock.Add(time.Second)

		return at
	})

	first := startBackup(t, fix.handler)
	pollBackupSucceeded(t, fix.handler, first)

	second := startBackup(t, fix.handler)
	pollBackupSucceeded(t, fix.handler, second)

	status, body := serveBackup(t, fix.handler, http.MethodGet, "/api/server/backups")
	if status != http.StatusOK {
		t.Fatalf("GET backups answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	var doc struct {
		Backups []getBackupDoc `json:"backups"`
	}

	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode backup list %q: %v", body, err)
	}

	if len(doc.Backups) != 2 || doc.Backups[0].ID != second || doc.Backups[1].ID != first {
		t.Fatalf("backup list holds %v, want [%s %s] newest first", doc.Backups, second, first)
	}
}

// TestBackupWithoutTarget proves all four routes answer 503 with a fixed
// message when no backup target is configured, never driver text.
func TestBackupWithoutTarget(t *testing.T) {
	t.Parallel()

	st := openServerTemplateStore(t, filepath.Join(t.TempDir(), "dustpan.db"))

	namespaces, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "test"})
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
		Schema:           nil,
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

	paths := []struct {
		method, path string
	}{
		{http.MethodPost, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups"},
		{http.MethodGet, "/api/server/backups/bkp-missing000000000001"},
		{http.MethodGet, "/api/server/backups/bkp-missing000000000001/check"},
	}

	for _, tc := range paths {
		status, body := serveBackup(t, handler, tc.method, tc.path)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("%s %s without a target answers %d, want %d (%s)",
				tc.method, tc.path, status, http.StatusServiceUnavailable, body)
		}

		doc := decodeError(t, body)
		if doc.Code != http.StatusServiceUnavailable ||
			doc.Message != "no backup target is configured: set --backup-target" {
			t.Fatalf("%s %s error = %+v, want the fixed no-target message", tc.method, tc.path, doc)
		}
	}
}
