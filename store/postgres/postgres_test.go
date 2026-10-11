package postgres_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/postgres"
	"opendustpan.org/opendustpan/store/storetest"
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

	schema := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	_, err = conn.Exec(t.Context(), "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", envURL, err)
	}

	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	return u.String()
}
