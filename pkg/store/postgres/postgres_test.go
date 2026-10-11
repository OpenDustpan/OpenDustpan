package postgres_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// envURL names the variable holding a PostgreSQL URL for integration tests.
// Each subtest gets its own schema, so the database can be shared.
const envURL = "DUSTPAN_TEST_POSTGRES_URL"

func TestConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t, base)
	})
}

// TestPingClosed checks Ping answers on an open store and returns exactly the
// sentinel text, with no driver text, after Close.
func TestPingClosed(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping open store: %v", err)
	}

	s.Close()

	err := s.Ping(t.Context())
	if !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ping closed store: err = %v, want ErrUnavailable", err)
	}

	if want := "ping postgres database: database unavailable"; err.Error() != want {
		t.Fatalf("ping closed store error = %q, want %q", err, want)
	}
}

func TestSecretsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	secrettest.Run(t, func(t *testing.T) secrettest.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return open(t, base)
	})
}

// TestUpdateHistoryConformance runs the history cases of ADR 28 decision 4
// on PostgreSQL: the creator and restore source survive, and history reports
// the checkpoint size.
func TestUpdateHistoryConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUpdateHistory(t, func(t *testing.T) storetest.HistoryStore {
		t.Helper()

		return open(t, base)
	})
}

func TestWorkspaceConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunWorkspace(t, func(t *testing.T) storetest.WorkspaceStore {
		t.Helper()

		return open(t, base)
	})
}

func TestBackupConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunBackup(t, func(t *testing.T) storetest.BackupStore {
		t.Helper()

		return openBackup(t, base)
	})
}

// backupStore wraps a PostgreSQL store with the URL of its schema, so the
// suite can close and reopen the same backing storage.
type backupStore struct {
	*postgres.Store

	url string
	t   *testing.T
}

var _ storetest.Reopener = (*backupStore)(nil)

// openBackup returns a backup store in a fresh schema of the database at base.
func openBackup(t *testing.T, base string) *backupStore {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return &backupStore{Store: s, url: schemaURL, t: t}
}

// Close implements storetest.Reopener: it releases the pool. Closing twice is
// safe, so the cleanup above stays.
func (s *backupStore) Close() error {
	s.Store.Close()

	return nil
}

// Reopen implements storetest.Reopener: it reconnects to the same schema. A
// reconnect preserves running backup rows; the server, not Open, marks them
// failed at start.
func (s *backupStore) Reopen() storetest.BackupStore { //nolint:ireturn // Reopener returns a new handle as the interface.
	reopened, err := postgres.Open(s.t.Context(), s.url)
	if err != nil {
		s.t.Fatalf("reopen store: %v", err)
	}

	s.t.Cleanup(reopened.Close)

	return &backupStore{Store: reopened, url: s.url, t: s.t}
}

// open returns a store in a fresh schema of the database at base.
func open(t *testing.T, base string) *postgres.Store {
	t.Helper()

	s, err := postgres.Open(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// TestRenameKeepsTags checks that tags follow the stack ID through a rename:
// the renamed stack keeps its tags and ID, and the old name is gone.
func TestRenameKeepsTags(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)
	ctx := t.Context()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{CreatedAt: now, Name: "acme", HomeRegion: "eu-west", FailoverVersion: 1}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, parseErr := stack.ParseRef("acme", "web", "prod")
	if parseErr != nil {
		t.Fatalf("parse ref: %v", parseErr)
	}

	st := stack.Stack{
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      map[string]string{"team": "platform"},
		Ref:       ref,
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}

	if err := s.CreateStack(ctx, st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	next := map[string]string{"team": "e2e"}
	if err := s.UpdateStackTags(ctx, ref, next, now, ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := s.RenameStack(ctx, ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	renamed, err := stack.ParseRef("acme", "web", "renamed")
	if err != nil {
		t.Fatalf("parse renamed ref: %v", err)
	}

	got, err := s.GetStack(ctx, renamed)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if !maps.Equal(got.Tags, next) {
		t.Fatalf("renamed tags = %v, want %v", got.Tags, next)
	}

	if got.ID != st.ID {
		t.Fatalf("renamed ID = %q, want %q", got.ID, st.ID)
	}

	if _, err := s.GetStack(ctx, ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get old name: err = %v, want ErrNotFound", err)
	}
}

func TestMigration0003FillsIDs(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0002(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: "acme", Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("migrated stacks = %d, want 2", len(list))
	}

	seen := map[stack.ID]bool{}

	for _, st := range list {
		if st.ID == "" || !strings.HasPrefix(st.ID.String(), "stk-") {
			t.Fatalf("migrated stack %s ID = %q, want stk- prefix", st.Ref, st.ID)
		}

		if seen[st.ID] {
			t.Fatalf("duplicate migrated ID %q", st.ID)
		}

		seen[st.ID] = true
	}
}

func TestMigration0005KeepsHistory(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0003WithHistory(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	checkMigratedStack(t, s, ref)
	checkMigratedUpdate(t, s, ref)
	checkMigratedHistory(t, s, ref)
	checkMigratedCheckpoint(t, s, ref)
	checkMigratedEvents(t, s, ref)
}

func checkMigratedStack(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	gotStack, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if gotStack.ID.String() != "stk-aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("migrated stack ID = %q, want stk-aaaaaaaaaaaaaaaaaaaa", gotStack.ID)
	}
}

func checkMigratedUpdate(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	gotUpdate, err := s.GetUpdate(t.Context(), ref, "u1")
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if gotUpdate.Version != 1 || gotUpdate.Status != stack.StatusSucceeded {
		t.Fatalf("migrated update = version %d status %q, want 1 succeeded", gotUpdate.Version, gotUpdate.Status)
	}
}

func checkMigratedHistory(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	history, err := s.ListUpdates(t.Context(), ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 1 || history[0].ID != "u1" {
		t.Fatalf("migrated history = %+v, want one update u1", history)
	}
}

func checkMigratedCheckpoint(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != "{}" || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {} u1", cp.Deployment, cp.Update)
	}
}

func checkMigratedEvents(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	events, err := s.ListEvents(t.Context(), ref, "u1", -1, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("migrated events = %d, want 2", len(events))
	}
}

func TestMigration0006KeepsOneRowData(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0005WithOneRow(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != `{"v":1}` || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {\"v\":1} u1", cp.Deployment, cp.Update)
	}

	byVersion, err := s.CheckpointByVersion(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("checkpoint of version 1: %v", err)
	}

	if string(byVersion.Deployment) != `{"v":1}` || byVersion.Update != "u1" {
		t.Fatalf(
			"migrated checkpoint of version 1 = deployment %q update %q, want {\"v\":1} u1",
			byVersion.Deployment,
			byVersion.Update,
		)
	}

	if _, err := s.CheckpointByVersion(t.Context(), ref, 2); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint of missing version 2: err = %v, want ErrNoState", err)
	}
}

// seedAt0005WithOneRow creates a database at migration 0005 with a stack, one
// finished update and its checkpoint: the one-row-per-stack layout.
func seedAt0005WithOneRow(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0005(t, conn)
	recordMigrations0005(t, conn)
	seedOneRowNamespace(t, conn)
	seedOneRowStack(t, conn)
	seedOneRowUpdate(t, conn)
	seedOneRowCheckpoint(t, conn)
}

func applyMigrations0005(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0005(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

func seedOneRowNamespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedOneRowStack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedOneRowUpdate(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (stack_id, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', 'update', false, 'succeeded', 1, 3, ''::bytea, 0, 1000, 2000, 3000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedOneRowCheckpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (stack_id, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{"v":1}', 4000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

// seedAt0003WithHistory creates a database at migration 0003 with a stack,
// one update, its checkpoint and two events, all keyed by name.
func seedAt0003WithHistory(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0003(t, conn)
	recordMigrations0003(t, conn)
	seedHistoryNamespace(t, conn)
	seedHistoryStack(t, conn)
	seedHistoryUpdate(t, conn)
	seedHistoryCheckpoint(t, conn)
	seedHistoryEvents(t, conn)
}

func applyMigrations0003(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		_, execErr := conn.Exec(t.Context(), string(data))
		if execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0003(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	_, err := conn.Exec(t.Context(), createMigrations)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for _, m := range []struct {
		version int64
		name    string
	}{{version: 1, name: "0001_init.sql"}, {version: 2, name: "0002_updates.sql"}, {version: 3, name: "0003_stack_id.sql"}} {
		_, err := conn.Exec(t.Context(), record, m.version, m.name)
		if err != nil {
			t.Fatalf("record migration %s: %v", m.name, err)
		}
	}
}

func seedHistoryNamespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedHistoryStack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedHistoryUpdate(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (namespace, project, stack, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', 'update', false, 'succeeded', 1, 3, ''::bytea, 0, 1000, 2000, 3000)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedHistoryCheckpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (namespace, project, stack, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{}', 4000)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

func seedHistoryEvents(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO update_events (namespace, project, stack, update_id, sequence, timestamp_ms, payload)
		VALUES ('acme', 'web', 'prod', 'u1', $1, $2, '{}')`

	_, err := conn.Exec(t.Context(), seed, 0, 5000)
	if err != nil {
		t.Fatalf("seed event 0: %v", err)
	}

	_, err = conn.Exec(t.Context(), seed, 1, 5001)
	if err != nil {
		t.Fatalf("seed event 1: %v", err)
	}
}

func TestMigration0007KeepsTags(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0006WithTags(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	got, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if got.Tags["team"] != "platform" || len(got.Tags) != 1 {
		t.Fatalf("migrated tags = %v, want map[team:platform]", got.Tags)
	}
}

func TestMigration0009FillsInventory(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0008WithStacks(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	checkMigratedInventory(t, s)
}

// TestMigration0012FillsHistoryFields checks that updates from before the
// history columns read with an empty creator, no restore source and the
// checkpoint size from the join.
func TestMigration0012FillsHistoryFields(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0010WithUpdates(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	checkMigratedHistoryUpdates(t, s, ref)
	checkMigratedHistoryList(t, s, ref)
}

// checkMigratedHistoryUpdates checks the migrated updates carry empty history fields
// and the checkpoint size of u1.
func checkMigratedHistoryUpdates(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	for _, want := range []struct {
		id   stack.UpdateID
		size int64
	}{
		{id: "u1", size: int64(len(`{"v":1}`))},
		{id: "u2", size: 0},
	} {
		checkMigratedHistoryUpdate(t, s, ref, want.id, want.size)
	}
}

// checkMigratedHistoryUpdate checks one migrated update against its expected size.
func checkMigratedHistoryUpdate(t *testing.T, s *postgres.Store, ref stack.Ref, id stack.UpdateID, size int64) {
	t.Helper()

	got, getErr := s.GetUpdate(t.Context(), ref, id)
	if getErr != nil {
		t.Fatalf("get update %s: %v", id, getErr)
	}

	if got.CreatedBy != "" || got.RestoredFrom != 0 || got.CheckpointBytes != size {
		t.Fatalf(
			"migrated update %s = creator %q restored %d size %d, want empty, 0 and %d",
			id,
			got.CreatedBy,
			got.RestoredFrom,
			got.CheckpointBytes,
			size,
		)
	}
}

// checkMigratedHistoryList checks the migrated history holds u2 then u1
// with empty history fields and the size of u1.
func checkMigratedHistoryList(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	history, err := s.ListUpdates(t.Context(), ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 2 || history[0].ID != "u2" || history[0].CheckpointBytes != 0 ||
		history[1].ID != "u1" || history[1].CheckpointBytes != int64(len(`{"v":1}`)) {
		t.Fatalf("migrated history = %+v, want [u2 without checkpoint, u1 with size]", history)
	}

	for i := range history {
		if history[i].CreatedBy != "" || history[i].RestoredFrom != 0 {
			t.Fatalf(
				"migrated history %s = creator %q restored %d, want empty and 0",
				history[i].ID,
				history[i].CreatedBy,
				history[i].RestoredFrom,
			)
		}
	}
}

// seedAt0010WithUpdates creates a database at migration 0010 with a stack,
// two finished updates and the checkpoint of the first.
func seedAt0010WithUpdates(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0010(t, conn)
	recordMigrations0010(t, conn)
	seed0010Namespace(t, conn)
	seed0010Stack(t, conn)
	seed0010Updates(t, conn)
	seed0010Checkpoint(t, conn)
}

// seed0010Namespace seeds the namespace of the 0011 and 0012 migration tests.
func seed0010Namespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

// seed0010Stack seeds one stack without tags, as the schema at 0010 holds it.
func seed0010Stack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', 2, 0, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

// applyMigrations0010 applies migrations 0001 to 0010 to conn.
func applyMigrations0010(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql", "0009_inventory.sql", "0010_workspaces.sql",
	}

	for _, name := range names {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

// recordMigrations0010 records migrations 0001 to 0010 as applied.
func recordMigrations0010(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql", "0009_inventory.sql", "0010_workspaces.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

// seed0010Updates seeds two finished updates without the history columns.
func seed0010Updates(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (stack_id, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', $1, 'update', false, 'succeeded', $2, 3, ''::bytea, 0, 1000, 2000, 3000)`

	for _, u := range []struct {
		id      string
		version int64
	}{{id: "u1", version: 1}, {id: "u2", version: 2}} {
		if _, err := conn.Exec(t.Context(), seed, u.id, u.version); err != nil {
			t.Fatalf("seed update %s: %v", u.id, err)
		}
	}
}

// seed0010Checkpoint seeds the checkpoint of update u1.
func seed0010Checkpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (stack_id, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{"v":1}', 4000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

// checkMigratedInventory checks the migration filled one entry per stack.
func checkMigratedInventory(t *testing.T, s *postgres.Store) {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), "acme")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("migrated inventory entries = %+v, want two", entries)
	}

	for i := range entries {
		checkMigratedEntry(t, &entries[i])
	}
}

// checkMigratedEntry checks one migrated entry against its stack.
func checkMigratedEntry(t *testing.T, e *inventory.Entry) {
	t.Helper()

	want := map[string]struct {
		unitID      string
		lastChanged time.Time
	}{
		"web/prod": {unitID: "stk-aaaaaaaaaaaaaaaaaaaa", lastChanged: time.UnixMilli(2000).UTC()},
		"web/dev":  {unitID: "stk-bbbbbbbbbbbbbbbbbbbb", lastChanged: time.UnixMilli(3000).UTC()},
	}

	w, ok := want[e.Name]
	if !ok {
		t.Fatalf("migrated inventory entry name = %q, want web/prod or web/dev", e.Name)
	}

	if e.Tool != "pulumi" || e.Namespace.String() != "acme" || e.UnitID != w.unitID ||
		e.DisplayName != e.Name || !e.LastChanged.Equal(w.lastChanged) ||
		e.Status != "" || e.ResourceCount != nil {
		t.Fatalf("migrated inventory entry = %+v, want unit %q changed %v", e, w.unitID, w.lastChanged)
	}
}

// seedAt0008WithStacks creates a database at migration 0008 with two stacks
// and no inventory rows.
func seedAt0008WithStacks(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0008(t, conn)
	recordMigrations0008(t, conn)
	seedInventoryStacks(t, conn)
}

// applyMigrations0008 applies migrations 0001 to 0008 to conn.
func applyMigrations0008(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql",
	}

	for _, name := range names {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

// recordMigrations0008 records migrations 0001 to 0008 as applied.
func recordMigrations0008(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

// seedInventoryStacks seeds a namespace with two stacks.
func seedInventoryStacks(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seedNamespace = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seedNamespace); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', $1, $2, 0, 1000, $3)`

	for _, s := range []struct {
		name    string
		id      string
		updated int64
	}{
		{name: "prod", id: "stk-aaaaaaaaaaaaaaaaaaaa", updated: 2000},
		{name: "dev", id: "stk-bbbbbbbbbbbbbbbbbbbb", updated: 3000},
	} {
		if _, err := conn.Exec(t.Context(), seedStack, s.name, s.id, s.updated); err != nil {
			t.Fatalf("seed stack %s: %v", s.name, err)
		}
	}
}

// seedAt0006WithTags creates a database at migration 0006 with a stack whose
// tags still live in the stacks row.
func seedAt0006WithTags(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}

	const seedNamespace = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seedNamespace); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{"team":"platform"}', 0, 0, 0)`

	if _, err := conn.Exec(t.Context(), seedStack); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

// seedAt0002 creates a database at migration 0002 with two stacks and no IDs.
func seedAt0002(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyBaseMigrations(t, conn)
	recordBaseMigrations(t, conn)
	seedBaseStacks(t, conn)
}

func applyBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for _, m := range []struct {
		version int64
		name    string
	}{{version: 1, name: "0001_init.sql"}, {version: 2, name: "0002_updates.sql"}} {
		if _, err := conn.Exec(t.Context(), record, m.version, m.name); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedBaseStacks(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(
		t.Context(),
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms) VALUES ('acme', 'eu-west', 1, 0)`,
	)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks
		(namespace, project, name, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', $1, $2, '{}', 0, 0, 0)`

	for _, s := range [][2]string{{"web", "prod"}, {"web", "dev"}} {
		if _, err := conn.Exec(t.Context(), seedStack, s[0], s[1]); err != nil {
			t.Fatalf("seed stack %s: %v", s[1], err)
		}
	}
}

// isolatedURL creates a fresh schema and returns base with search_path set to it.
func isolatedURL(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		t.Fatalf("create schema: %v", execErr)
	}

	u, parseErr := url.Parse(base)
	if parseErr != nil {
		t.Fatalf("parse %s: %v", envURL, parseErr)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// TestOpenRefusesNewerSchema proves that a database with a version the binary
// does not embed stops open and leaves the database unchanged.
func TestOpenRefusesNewerSchema(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	s.Close()

	beforeVersions, _ := newerSchemaSnapshot(t, migrated)
	if len(beforeVersions) == 0 {
		t.Fatal("schema versions are empty, want the embedded migrations")
	}

	extra := beforeVersions[len(beforeVersions)-1] + 1

	insertFutureVersion(t, migrated, extra)

	wantVersions := append(slices.Clone(beforeVersions), extra)
	gotVersions, gotTables := newerSchemaSnapshot(t, migrated)

	if !slices.Equal(gotVersions, wantVersions) {
		t.Fatalf("seeded versions = %v, want %v", gotVersions, wantVersions)
	}

	if _, err := postgres.Open(t.Context(), migrated); !errors.Is(err, schema.ErrSchemaNewer) {
		t.Fatalf("open newer database: err = %v, want ErrSchemaNewer", err)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, migrated)

	if !slices.Equal(afterVersions, wantVersions) {
		t.Fatalf("versions after refusal = %v, want %v", afterVersions, wantVersions)
	}

	if !slices.Equal(afterTables, gotTables) {
		t.Fatalf("tables after refusal = %v, want %v", afterTables, gotTables)
	}
}

// newerSchemaSnapshot reads the recorded versions and the table names of the
// database at migrated.
func newerSchemaSnapshot(t *testing.T, migrated string) ([]int64, []string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	return readSchemaVersions(t, conn), readSchemaTables(t, conn)
}

// readSchemaVersions reads the recorded versions, ordered.
func readSchemaVersions(t *testing.T, conn *pgx.Conn) []int64 {
	t.Helper()

	rows, queryErr := conn.Query(t.Context(), `SELECT version FROM schema_migrations ORDER BY version`)
	if queryErr != nil {
		t.Fatalf("read schema versions: %v", queryErr)
	}

	defer rows.Close()

	var versions []int64

	for rows.Next() {
		var version int64
		if scanErr := rows.Scan(&version); scanErr != nil {
			rows.Close()
			t.Fatalf("scan schema version: %v", scanErr)
		}

		versions = append(versions, version)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read schema versions: %v", iterErr)
	}

	return versions
}

// readSchemaTables reads the table names, ordered.
func readSchemaTables(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, queryErr := conn.Query(
		t.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename`,
	)
	if queryErr != nil {
		t.Fatalf("read tables: %v", queryErr)
	}

	defer rows.Close()

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			rows.Close()
			t.Fatalf("scan table: %v", scanErr)
		}

		tables = append(tables, name)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read tables: %v", iterErr)
	}

	return tables
}

// insertFutureVersion records one version newer than the binary embeds.
func insertFutureVersion(t *testing.T, migrated string, version int64) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	name := fmt.Sprintf("%04d_future.sql", version)

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	if _, execErr := conn.Exec(t.Context(), record, version, name); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}
}

// TestMigration0011FillsRestoredFrom checks that migration 0011 keeps the
// finalized versions of a database at 0010: every existing row reads back
// with an empty restored_from, and the list stays newest first with the
// right total.
func TestMigration0011FillsRestoredFrom(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0010WithStateVersions(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	checkMigratedStateVersions(t, s)
}

// checkMigratedStateVersions checks the migrated versions are listed newest
// first with an empty restored_from and the right total.
func checkMigratedStateVersions(t *testing.T, s *postgres.Store) {
	t.Helper()

	ctx := t.Context()
	wsID := workspace.ID("ws-aaaaaaaaaaaaaaaaaaaa")

	list, total, err := s.ListStateVersions(ctx, wsID, workspace.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != 2 {
		t.Fatalf("migrated total = %d, want 2", total)
	}

	if len(list) != 2 {
		t.Fatalf("migrated state versions = %d, want 2", len(list))
	}

	if list[0].ID.String() != "sv-bbbbbbbbbbbbbbbbbbbb" ||
		list[1].ID.String() != "sv-aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("migrated order = [%s %s], want newest first", list[0].ID, list[1].ID)
	}

	for i := range list {
		if list[i].RestoredFrom != "" {
			t.Fatalf("migrated state version %s restored_from = %q, want empty", list[i].ID, list[i].RestoredFrom)
		}
	}

	for _, id := range []workspace.StateVersionID{
		"sv-aaaaaaaaaaaaaaaaaaaa",
		"sv-bbbbbbbbbbbbbbbbbbbb",
		"sv-cccccccccccccccccccc",
	} {
		got, err := s.GetStateVersion(ctx, id)
		if err != nil {
			t.Fatalf("get state version %s: %v", id, err)
		}

		if got.RestoredFrom != "" {
			t.Fatalf("migrated state version %s restored_from = %q, want empty", id, got.RestoredFrom)
		}
	}
}

// seedAt0010WithStateVersions creates a database at migration 0010 with one
// workspace, two finalized state versions and one pending version.
func seedAt0010WithStateVersions(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0010(t, conn)
	recordMigrations0010(t, conn)
	seed0010Namespace(t, conn)
	seed0010Workspace(t, conn)
	seed0010StateVersions(t, conn)
}

// seed0010Workspace seeds one workspace that points at its newest version.
func seed0010Workspace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO workspaces (id, namespace, name, revision, lock_id, lock_holder,
		lock_reason, locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms)
		VALUES ('ws-aaaaaaaaaaaaaaaaaaaa', 'acme', 'network', 1, '', '', '', 0,
		'sv-bbbbbbbbbbbbbbbbbbbb', 1000, 3000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
}

// seed0010StateVersions seeds two finalized versions and one pending version
// with the 0010 columns: no restored_from yet.
func seed0010StateVersions(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO state_versions (id, workspace_id, status, serial, lineage, md5, size,
		content_key, outputs, force, upload_digest, upload_expires_at_ms, holder, lock_id,
		created_at_ms, finalized_at_ms)
		VALUES
		('sv-aaaaaaaaaaaaaaaaaaaa', 'ws-aaaaaaaaaaaaaaaaaaaa', 'finalized', 1, 'lineage-1',
		'\x00000000000000000000000000000001', 11, 'content-old', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 1000, 2000),
		('sv-bbbbbbbbbbbbbbbbbbbb', 'ws-aaaaaaaaaaaaaaaaaaaa', 'finalized', 2, 'lineage-1',
		'\x00000000000000000000000000000002', 22, 'content-new', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 2000, 3000),
		('sv-cccccccccccccccccccc', 'ws-aaaaaaaaaaaaaaaaaaaa', 'pending', 3, 'lineage-1',
		'\x00000000000000000000000000000003', 0, '', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 2500, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed state versions: %v", err)
	}
}
