package server_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// upgradeCheckDoc decodes an upgrade check answer in tests.
type upgradeCheckDoc struct {
	Version                 string   `json:"version"`
	SchemaVersion           int64    `json:"schemaVersion"`
	AppliedMigrations       []string `json:"appliedMigrations"`
	KeyEncryptionKey        bool     `json:"keyEncryptionKey"`
	RunningJournaledUpdates int64    `json:"runningJournaledUpdates"`
}

// readUpgradeCheck reads the upgrade check and returns its status and
// document, failing the test when the body is not the upgrade check shape.
func readUpgradeCheck(t *testing.T, handler *apiserver.Handler) (int, upgradeCheckDoc) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/upgrade-check", http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var doc upgradeCheckDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode upgrade check %q: %v", rec.Body.String(), err)
	}

	return rec.Code, doc
}

// TestUpgradeCheckDeclaresServerObserve proves the upgrade check route
// declares exactly the action server:observe, with a resolver that returns
// the installation.
func TestUpgradeCheckDeclaresServerObserve(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	var found *apiserver.Route

	for _, route := range handler.Routes() {
		if route.Method == http.MethodGet && route.Path == "/api/server/upgrade-check" {
			found = &route

			break
		}
	}

	if found == nil {
		t.Fatal("upgrade check route is missing, want GET /api/server/upgrade-check")
	}

	if found.Scope != apiserver.ScopeAuthorized {
		t.Fatalf("route scope = %d, want authorized", found.Scope)
	}

	if len(found.Actions) != 1 || found.Actions[0] != identity.ActionServerObserve {
		t.Fatalf("route actions = %v, want [server:observe]", found.Actions)
	}

	if found.Resolve == nil {
		t.Fatal("route declares actions but no resolver")
	}

	obj, err := found.Resolve(
		httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), found.Method, found.Path, http.NoBody),
	)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "@server" || !obj.IsServer() {
		t.Fatalf("resolver returns %q, want the installation @server", obj.String())
	}
}

// TestUpgradeCheckAnswersVersions proves the upgrade check answers the
// server version, a schema version above zero and the applied migration
// names.
func TestUpgradeCheckAnswersVersions(t *testing.T) {
	t.Parallel()

	handler := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	status, doc := readUpgradeCheck(t, handler)
	if status != http.StatusOK {
		t.Fatalf("GET upgrade check answers %d, want %d", status, http.StatusOK)
	}

	if doc.Version != "test" {
		t.Fatalf("server version = %q, want test", doc.Version)
	}

	if doc.SchemaVersion <= 0 {
		t.Fatalf("schema version = %d, want above zero", doc.SchemaVersion)
	}

	if len(doc.AppliedMigrations) == 0 {
		t.Fatal("upgrade check holds no applied migration, want the migrated store")
	}

	for _, name := range doc.AppliedMigrations {
		if !strings.HasSuffix(name, ".sql") {
			t.Fatalf("applied migration = %q, want a migration file name", name)
		}
	}
}

// stubSchema reports fixed applied migrations, for the handler version
// test.
type stubSchema struct {
	migrations []schemaMigration
}

// schemaMigration is one applied migration in tests. The field names match
// the store package so the test converts without importing it.
type schemaMigration struct {
	Version int64
	Name    string
}

func (s stubSchema) AppliedMigrations(_ context.Context) ([]schema.AppliedMigration, error) {
	out := make([]schema.AppliedMigration, 0, len(s.migrations))
	for _, migration := range s.migrations {
		out = append(out, schema.AppliedMigration{Version: migration.Version, Name: migration.Name})
	}

	return out, nil
}

// TestUpgradeCheckEmptyVersionDefaultsDev proves an empty server version
// answers dev, as the backup manifest records it.
func TestUpgradeCheckEmptyVersionDefaultsDev(t *testing.T) {
	t.Parallel()

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Backups:       nil,
		Restores:      nil,
		Maintenance:   newFakeMaintenance(maintenance.StateNormal),
		Metrics:       stubMetrics{},
		Version:       "",
		Schema: stubSchema{migrations: []schemaMigration{
			{Version: 1, Name: "0001_init.sql"},
		}},
		Journal:        nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditSinks:     nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	status, doc := readUpgradeCheck(t, handler)
	if status != http.StatusOK {
		t.Fatalf("GET upgrade check answers %d, want %d", status, http.StatusOK)
	}

	if doc.Version != "dev" {
		t.Fatalf("server version = %q, want dev", doc.Version)
	}

	if doc.SchemaVersion != 1 || len(doc.AppliedMigrations) != 1 || doc.AppliedMigrations[0] != "0001_init.sql" {
		t.Fatalf("upgrade check = %+v, want schema version 1 with one migration", doc)
	}
}

// TestUpgradeCheckWithoutSchema proves the upgrade check without a schema
// store answers 500 with the one JSON error shape.
func TestUpgradeCheckWithoutSchema(t *testing.T) {
	t.Parallel()

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Backups:        nil,
		Restores:       nil,
		Maintenance:    newFakeMaintenance(maintenance.StateNormal),
		Metrics:        stubMetrics{},
		Version:        "test",
		Schema:         nil,
		Journal:        nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditSinks:     nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/upgrade-check", http.NoBody)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET upgrade check without a schema answers %d, want %d (%s)",
			rec.Code, http.StatusInternalServerError, rec.Body.String())
	}

	if doc := decodeError(t, rec.Body.String()); doc.Code != http.StatusInternalServerError || doc.Message == "" {
		t.Fatalf("upgrade check error = %+v, want code 500 with a message", doc)
	}
}

// TestUpgradeCheckNeedsToken proves the upgrade check answers 401 without a
// token, with the one JSON error shape.
func TestUpgradeCheckNeedsToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/upgrade-check", http.NoBody)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET upgrade check without a token answers %d, want %d (%s)",
			rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	if doc := decodeError(t, rec.Body.String()); doc.Code != http.StatusUnauthorized || doc.Message == "" {
		t.Fatalf("upgrade check error = %+v, want code 401 with a message", doc)
	}
}

// TestUpgradeCheckAnswersKeyEncryptionKey proves the upgrade check answers
// that the server runs with a key-encryption key (ADR 41 decision 12). A
// server does not start without one, so the answer is always true.
func TestUpgradeCheckAnswersKeyEncryptionKey(t *testing.T) {
	t.Parallel()

	handler, err := apiserver.NewHandler(apiserver.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Backups:       nil,
		Restores:      nil,
		Maintenance:   newFakeMaintenance(maintenance.StateNormal),
		Metrics:       stubMetrics{},
		Version:       "test",
		Schema: stubSchema{migrations: []schemaMigration{
			{Version: 1, Name: "0001_init.sql"},
		}},
		Journal:        nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		AuditSinks:     nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	status, doc := readUpgradeCheck(t, handler)
	if status != http.StatusOK {
		t.Fatalf("GET upgrade check answers %d, want %d", status, http.StatusOK)
	}

	if !doc.KeyEncryptionKey {
		t.Fatal("keyEncryptionKey = false, want true")
	}
}

// stubJournal answers a fixed count of running journaled updates.
type stubJournal struct {
	count int64
}

func (s stubJournal) CountRunningJournaledUpdates(_ context.Context) (int64, error) {
	return s.count, nil
}

// newJournalHandler builds an upgrade check handler with the stub schema of
// one migration and the given journaled count. A negative count wires no
// journal reporter, so the check answers 0 as a store without the count does.
func newJournalHandler(t *testing.T, journaled int64) *apiserver.Handler {
	t.Helper()

	cfg := apiserver.Config{
		Authenticator: tokenAuthenticator{},
		Authorizer:    selectiveAuthorizer{deny: map[identity.Action]bool{}},
		Logger:        slog.New(slog.DiscardHandler),
		Namespaces:    newTestNamespaces(t),
		Backups:       nil,
		Restores:      nil,
		Maintenance:   newFakeMaintenance(maintenance.StateNormal),
		Metrics:       stubMetrics{},
		Version:       "test",
		Schema: stubSchema{migrations: []schemaMigration{
			{Version: 1, Name: "0001_init.sql"},
		}},
		Journal:        nil,
		Principals:     nil,
		Access:         nil,
		Tokens:         nil,
		Audit:          nil,
		OnAuditFailure: nil,
		DeletedUnits:   nil,
		IPAllowlists:   nil,
		SessionPolicy:  nil,
		AuditSinks:     nil,
	}

	if journaled >= 0 {
		cfg.Journal = stubJournal{count: journaled}
	}

	handler, err := apiserver.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestUpgradeCheckAnswersNoJournaledUpdates proves the upgrade check without
// a journal reporter answers a zero journaled count.
func TestUpgradeCheckAnswersNoJournaledUpdates(t *testing.T) {
	t.Parallel()

	status, doc := readUpgradeCheck(t, newJournalHandler(t, -1))
	if status != http.StatusOK {
		t.Fatalf("GET upgrade check answers %d, want %d", status, http.StatusOK)
	}

	if doc.RunningJournaledUpdates != 0 {
		t.Fatalf("runningJournaledUpdates = %d, want 0", doc.RunningJournaledUpdates)
	}
}

// TestUpgradeCheckAnswersTwoJournaledUpdates proves the upgrade check answers
// the count of running journaled updates from its journal reporter.
func TestUpgradeCheckAnswersTwoJournaledUpdates(t *testing.T) {
	t.Parallel()

	status, doc := readUpgradeCheck(t, newJournalHandler(t, 2))
	if status != http.StatusOK {
		t.Fatalf("GET upgrade check answers %d, want %d", status, http.StatusOK)
	}

	if doc.RunningJournaledUpdates != 2 {
		t.Fatalf("runningJournaledUpdates = %d, want 2", doc.RunningJournaledUpdates)
	}
}
